// Copyright (c) 2024 Tigera, Inc. All rights reserved.

package alertmanager

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	lsApi "github.com/projectcalico/calico/linseed/pkg/apis/v1"
	"github.com/projectcalico/calico/webhooks-processor/pkg/helpers"
	"github.com/projectcalico/calico/webhooks-processor/pkg/providers"
)

const (
	AlertManagerRequiredLabel = "alertname"
	CalicoForAlertManager     = "Calico Security Event"
)

var (
	ErrNoUrlField                   = errors.New("url field is not present in webhook configuration")
	ErrWrongPrefix                  = errors.New("url field does not start with 'http://' nor 'https://'")
	ErrWrongSuffix                  = errors.New("url field does not end with '/api/v2/alerts'")
	ErrBasicAuthFieldValueError     = errors.New("basicAuth field value is incorrect")
	ErrTLSConfigurationErrorCA      = errors.New("unable to add Certificate Authority to certificate pool")
	ErrTLSConfigurationErrorKeyPair = errors.New("unable to configure TLS client cert/key pair")
)

type AlertManagerProvider struct {
	config providers.Config
}

type AlertManagerProviderPayload struct {
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations,omitempty"`
	GeneratorURL string            `json:"generatorURL,omitempty"`
}

func NewProvider(config providers.Config) providers.Provider {
	return &AlertManagerProvider{
		config: config,
	}
}

func (p *AlertManagerProvider) Validate(config map[string]string) error {
	if url, ok := config["url"]; !ok {
		return ErrNoUrlField
	} else if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return ErrWrongPrefix
	} else if !strings.HasSuffix(url, "/api/v2/alerts") {
		return ErrWrongSuffix
	}
	if basicAuth, ok := config["basicAuth"]; ok {
		if parts := strings.SplitN(basicAuth, ":", 2); len(parts) != 2 {
			return ErrBasicAuthFieldValueError
		}
	}
	if tlsEnabled(config) {
		if _, _, err := tlsConfig(config); err != nil {
			return err
		}
	}
	return nil
}

func (p *AlertManagerProvider) Process(ctx context.Context, config map[string]string, labels map[string]string, event *lsApi.Event) (httpResponse providers.ProviderResponse, err error) {
	helpers.FillInEventBlanks(event)
	payload := new(AlertManagerProviderPayload)

	// set payload labels:
	labels[AlertManagerRequiredLabel] = CalicoForAlertManager
	payload.Labels = labels

	// set payload generatorURL when configured:
	if generatorURL, generatorSet := config["generatorURL"]; generatorSet {
		payload.GeneratorURL = generatorURL
	}

	// set alert annotations:
	payload.Annotations = map[string]string{
		"Description":    event.Description,
		"Origin":         event.Origin,
		"Severity":       fmt.Sprintf("%d", event.Severity),
		"Destination IP": *event.DestIP,
		"Source IP":      *event.DestIP,
		"Attack Vector":  event.AttackVector,
		"Mitre Tactic":   event.MitreTactic,
		"Mitre IDs":      strings.Join(*event.MitreIDs, "\n"),
		"Mitigations":    strings.Join(*event.Mitigations, "\n"),
	}

	// generate payload data:
	payloadBytes, err := json.Marshal([]AlertManagerProviderPayload{*payload})
	if err != nil {
		return
	}

	retryFunc := func() (err error) {
		requestCtx, requestCtxCancel := context.WithTimeout(ctx, p.config.RequestTimeout)
		defer requestCtxCancel()

		// prepare the HTTP POST request:
		request, err := http.NewRequestWithContext(requestCtx, "POST", config["url"], bytes.NewReader(payloadBytes))
		if err != nil {
			return // retry if failed
		}
		request.Header.Set("Content-Type", "application/json")

		// configure basic authentication when enabled:
		if basicAuth, ok := config["basicAuth"]; ok {
			if parts := strings.SplitN(basicAuth, ":", 2); len(parts) == 2 {
				request.SetBasicAuth(parts[0], parts[1])
			}
		}

		client := new(http.Client)
		if tlsEnabled(config) {
			if caPool, cert, err := tlsConfig(config); err == nil {
				client = &http.Client{
					Transport: &http.Transport{
						TLSClientConfig: &tls.Config{
							RootCAs:      caPool,
							Certificates: []tls.Certificate{*cert},
						},
					},
				}
			}
		}

		// execute the request:
		response, err := client.Do(request)
		if err != nil {
			return // retry if failed
		}
		defer response.Body.Close()

		// read the response:
		responseBytes, err := io.ReadAll(response.Body)
		if err != nil {
			return // retry if failed
		}

		// log and process the response:
		responseText := string(responseBytes)
		logrus.WithField("url", config["url"]).
			WithField("statusCode", response.StatusCode).
			WithField("response", responseText).
			Info("HTTP request processed")
		httpResponse = providers.ProviderResponse{
			Timestamp:             time.Now(),
			HttpStatusCode:        response.StatusCode,
			HttpStatusDescription: http.StatusText(response.StatusCode),
		}

		// terminate if all went well:
		if response.StatusCode == http.StatusOK {
			return nil
		}

		// otherwise retry the attempt:
		return fmt.Errorf("unexpected AlertManager response [%d]:%s", response.StatusCode, responseText)
	}

	// process the request with a back-off policy:
	return httpResponse, helpers.RetryWithLinearBackOff(retryFunc, p.config.RetryDuration, p.config.RetryTimes)
}

func (p *AlertManagerProvider) Config() providers.Config {
	return p.config
}

func tlsEnabled(config map[string]string) bool {
	_, caPresent := config["tlsCA"]
	_, keyPresent := config["tlsKey"]
	_, certPresent := config["tlsCert"]
	return caPresent && keyPresent && certPresent
}

func tlsConfig(config map[string]string) (*x509.CertPool, *tls.Certificate, error) {
	caCertPool := x509.NewCertPool()
	if ok := caCertPool.AppendCertsFromPEM([]byte(config["tlsCA"])); !ok {
		return nil, nil, ErrTLSConfigurationErrorCA
	}
	cert, err := tls.X509KeyPair([]byte(config["tlsCert"]), []byte(config["tlsKey"]))
	if err != nil {
		return nil, nil, ErrTLSConfigurationErrorKeyPair
	}
	return caCertPool, &cert, nil
}
