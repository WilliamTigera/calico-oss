// Copyright (c) 2024 Tigera, Inc. All rights reserved.
package token

import (
	"context"
	"fmt"
	"time"

	"github.com/SermoDigital/jose/jws"
	"github.com/sirupsen/logrus"

	authv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	v1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/projectcalico/calico/fluent-bit/plugins/out_linseed/pkg/config"
)

const (
	// tokenExpiration controls how often we renew serviceaccount token for log forwarding.
	tokenExpiration = 24 * time.Hour
	// tokenRenewal controls how early do we want to renew the token when it is close to expiry.
	tokenRenewal = 15 * time.Minute
)

type TokenController struct {
	clientset kubernetes.Interface

	serviceAccountName string
	expiration         time.Time
	token              string
}

func NewController(cfg *config.Config) (*TokenController, error) {
	// get service account from the kubeconfig file
	serviceAccountName, err := extractServiceAccountName(cfg.Kubeconfig)
	if err != nil {
		return nil, err
	}
	logrus.Debugf("service_account=%v", serviceAccountName)

	// initialize kubernetes clients
	clientset, err := kubernetes.NewForConfig(cfg.RestConfig)
	if err != nil {
		return nil, err
	}

	return &TokenController{
		clientset:          clientset,
		serviceAccountName: serviceAccountName,
	}, nil
}

func (c *TokenController) Run(stopCh <-chan struct{}) error {
	logrus.Info("linseed plugin token controller is started")
	return nil
}

func (c *TokenController) ServiceAccountName() string {
	return c.serviceAccountName
}

func (c *TokenController) Token() (string, error) {
	if time.Until(c.expiration) < tokenRenewal {
		logrus.Infof("token expired for serviceaccount %q", c.serviceAccountName)

		token, expiration, err := getServiceAccountToken(c.clientset.CoreV1(), corev1.NamespaceDefault, c.serviceAccountName)
		if err != nil {
			return "", err
		}
		c.expiration = expiration
		c.token = token
		logrus.Infof("successfully renewed token for serviceaccount %q", c.serviceAccountName)
	}

	return c.token, nil
}

func extractServiceAccountName(kubeconfig string) (string, error) {
	config, err := clientcmd.LoadFromFile(kubeconfig)
	if err != nil {
		return "", err
	}

	currentContext := config.CurrentContext
	if currentContext == "" {
		return "", fmt.Errorf("no current-context set in kubeconfig")
	}

	ctx, exists := config.Contexts[currentContext]
	if !exists {
		return "", fmt.Errorf("context %q not found in kubeconfig", currentContext)
	}
	return ctx.AuthInfo, nil
}

func getServiceAccountToken(coreV1Client v1.CoreV1Interface, namespace, serviceAccountName string) (string, time.Time, error) {
	seconds := int64(tokenExpiration.Seconds())
	tokenRequest := &authv1.TokenRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceAccountName,
			Namespace: namespace,
		},
		Spec: authv1.TokenRequestSpec{
			Audiences:         []string{},
			ExpirationSeconds: &seconds,
		},
	}

	tokenResponse, err := coreV1Client.ServiceAccounts(namespace).CreateToken(context.Background(), serviceAccountName, tokenRequest, metav1.CreateOptions{})
	if err != nil {
		return "", time.Time{}, err
	}
	token := tokenResponse.Status.Token
	expiration := tokenResponse.Status.ExpirationTimestamp.Time

	jwtToken, err := jws.ParseJWT([]byte(token))
	if err != nil {
		return "", time.Time{}, err
	}
	if exp, ok := jwtToken.Claims().Expiration(); ok {
		expiration = exp
	}

	return token, expiration, nil
}
