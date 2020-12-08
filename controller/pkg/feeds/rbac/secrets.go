// Copyright (c) 2020 Tigera Inc. All rights reserved.

package rbac

import (
	"context"
	"errors"
	"fmt"
	"strings"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	core "k8s.io/client-go/kubernetes/typed/core/v1"
)

var (
	secretDeniedPrefix = []string{
		"alertmanager-calico-",
		"calico-",
		"cnx-",
		"default-token-",
		"elastic-",
		"intrusion-detection-controller-token-",
		"prometheus-calico-",
		"prometheus-token-",
		"tigera-",
	}

	secretDenied = map[string]interface{}{
		"webhook-server-secret": nil,
	}

	UnsupportedOperation = errors.New("unsupported operation")
)

func secretAccessDenied(name string) error {
	return fmt.Errorf("access denied: %s", name)
}

type RestrictedSecretsClient struct {
	Client core.SecretInterface
}

func (r RestrictedSecretsClient) isPermitted(name string) bool {
	if _, ok := secretDenied[name]; ok {
		return false
	}
	for _, prefix := range secretDeniedPrefix {
		if strings.HasPrefix(name, prefix) {
			return false
		}
	}
	return true
}

func (r RestrictedSecretsClient) Get(ctx context.Context, name string, options metav1.GetOptions) (*v1.Secret, error) {
	if !r.isPermitted(name) {
		return nil, secretAccessDenied(name)
	}
	return r.Client.Get(ctx, name, options)
}

func (r RestrictedSecretsClient) List(ctx context.Context, opts metav1.ListOptions) (*v1.SecretList, error) {
	return nil, UnsupportedOperation
}

func (r RestrictedSecretsClient) Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	return nil, UnsupportedOperation
}

func (r RestrictedSecretsClient) Create(ctx context.Context, secret *v1.Secret, options metav1.CreateOptions) (*v1.Secret, error) {
	return nil, UnsupportedOperation
}

func (r RestrictedSecretsClient) Update(ctx context.Context, secret *v1.Secret, options metav1.UpdateOptions) (*v1.Secret, error) {
	return nil, UnsupportedOperation
}

func (r RestrictedSecretsClient) Delete(ctx context.Context, name string, options metav1.DeleteOptions) error {
	return UnsupportedOperation
}

func (r RestrictedSecretsClient) DeleteCollection(ctx context.Context, options metav1.DeleteOptions, listOptions metav1.ListOptions) error {
	return UnsupportedOperation
}

func (r RestrictedSecretsClient) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, options metav1.PatchOptions, subresources ...string) (result *v1.Secret, err error) {
	return nil, UnsupportedOperation
}

var _ core.SecretInterface = RestrictedSecretsClient{}
