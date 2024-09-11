// Copyright (c) 2024 Tigera, Inc. All rights reserved.
package main

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"os"
	"unsafe"

	"github.com/fluent/fluent-bit-go/output"
	"github.com/sirupsen/logrus"

	"github.com/projectcalico/calico/fluent-bit/plugins/out_linseed/pkg/config"
	"github.com/projectcalico/calico/fluent-bit/plugins/out_linseed/pkg/controller/endpoint"
	"github.com/projectcalico/calico/fluent-bit/plugins/out_linseed/pkg/recordprocessor"
	"github.com/projectcalico/calico/fluent-bit/plugins/out_linseed/pkg/token"
	"github.com/projectcalico/calico/libcalico-go/lib/logutils"
)

import "C"

var (
	cfg                *config.Config
	tk                 *token.Token
	endpointController *endpoint.EndpointController

	stopCh chan struct{}
)

//export FLBPluginRegister
func FLBPluginRegister(def unsafe.Pointer) int {
	configureLogging()

	return output.FLBPluginRegister(def, "linseed", "Calico Enterprise linseed output plugin")
}

//export FLBPluginInit
func FLBPluginInit(plugin unsafe.Pointer) int {
	var err error

	cfg, err = config.NewConfig(plugin, output.FLBPluginConfigKey)
	if err != nil {
		logrus.WithError(err).Error("failed to create config")
		return output.FLB_ERROR
	}

	tk, err = token.NewToken(cfg)
	if err != nil {
		logrus.WithError(err).Error("failed to initialize token")
		return output.FLB_ERROR
	}

	endpointController, err = endpoint.NewController(cfg)
	if err != nil {
		logrus.WithError(err).Error("failed to initialize endpoint controller")
		return output.FLB_ERROR
	}

	stopCh = make(chan struct{})
	if err := endpointController.Run(stopCh); err != nil {
		logrus.WithError(err).Error("failed to start endpoint controller")
		return output.FLB_ERROR
	}

	logrus.Info("linseed output plugin initialized")
	return output.FLB_OK
}

//export FLBPluginFlushCtx
func FLBPluginFlushCtx(ctx, data unsafe.Pointer, length C.int, tag *C.char) int {
	// process fluent-bit internal messagepack buffer
	processor := recordprocessor.NewRecordProcessor()
	ndjsonBuffer, count, err := processor.Process(data, int(length))
	if err != nil {
		logrus.WithError(err).Error("failed to process record data")
		// drop the buffer when processor failed to process
		return output.FLB_ERROR
	}

	// post to ingestion endpoint
	endpoint := endpointController.Endpoint()
	tagString := C.GoString(tag)
	token, err := tk.Token()
	if err != nil {
		logrus.WithError(err).Error("failed to get token")
		return output.FLB_RETRY
	}
	if err := doRequest(endpoint, tagString, token, ndjsonBuffer); err != nil {
		logrus.WithError(err).Errorf("failed to send %d logs", count)
		// retry the buffer when we failed to send
		return output.FLB_RETRY
	}

	logrus.Infof("successfully sent %d logs", count)
	return output.FLB_OK
}

//export FLBPluginExit
func FLBPluginExit() int {
	close(stopCh)
	return output.FLB_OK
}

func configureLogging() {
	logutils.ConfigureFormatter("linseed")
	logrus.SetOutput(os.Stdout)

	logLevel := logrus.InfoLevel
	rawLogLevel := os.Getenv("LOG_LEVEL")
	if rawLogLevel != "" {
		parsedLevel, err := logrus.ParseLevel(rawLogLevel)
		if err == nil {
			logLevel = parsedLevel
		} else {
			logrus.WithError(err).Warnf("failed to parse log level %q, defaulting to INFO.", parsedLevel)
		}
	}

	logrus.SetLevel(logLevel)
	logrus.Infof("log level set to %q", logLevel)
}

func doRequest(endpoint, tag, token string, ndjsonBuffer *bytes.Buffer) error {
	url := ""
	switch tag {
	case "flows":
		url = fmt.Sprintf("%s/ingestion/api/v1/%s/logs/bulk", endpoint, tag)
	default:
		return fmt.Errorf("unknown log type %q", tag)
	}

	logrus.WithField("tag", tag).Debugf("sending logs to %q", url)
	req, err := http.NewRequest("POST", url, io.NopCloser(bytes.NewBuffer(ndjsonBuffer.Bytes())))
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	req.Header.Set("Content-Type", "application/x-ndjson")

	insecureSkipVerify := false
	if cfg != nil {
		insecureSkipVerify = cfg.InsecureSkipVerify
	}
	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: insecureSkipVerify,
		},
	}}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("error response from server %q", resp.Status)
	}

	return nil
}

func main() {
}
