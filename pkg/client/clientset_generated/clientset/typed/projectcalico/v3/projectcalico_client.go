/*
Copyright 2017 Tigera.
*/package v3

import (
	v3 "github.com/tigera/calico-k8sapiserver/pkg/apis/projectcalico/v3"
	"github.com/tigera/calico-k8sapiserver/pkg/client/clientset_generated/clientset/scheme"
	serializer "k8s.io/apimachinery/pkg/runtime/serializer"
	rest "k8s.io/client-go/rest"
)

type ProjectcalicoV3Interface interface {
	RESTClient() rest.Interface
	GlobalNetworkPoliciesGetter
	NetworkPoliciesGetter
	TiersGetter
}

// ProjectcalicoV3Client is used to interact with features provided by the projectcalico.org group.
type ProjectcalicoV3Client struct {
	restClient rest.Interface
}

func (c *ProjectcalicoV3Client) GlobalNetworkPolicies() GlobalNetworkPolicyInterface {
	return newGlobalNetworkPolicies(c)
}

func (c *ProjectcalicoV3Client) NetworkPolicies(namespace string) NetworkPolicyInterface {
	return newNetworkPolicies(c, namespace)
}

func (c *ProjectcalicoV3Client) Tiers() TierInterface {
	return newTiers(c)
}

// NewForConfig creates a new ProjectcalicoV3Client for the given config.
func NewForConfig(c *rest.Config) (*ProjectcalicoV3Client, error) {
	config := *c
	if err := setConfigDefaults(&config); err != nil {
		return nil, err
	}
	client, err := rest.RESTClientFor(&config)
	if err != nil {
		return nil, err
	}
	return &ProjectcalicoV3Client{client}, nil
}

// NewForConfigOrDie creates a new ProjectcalicoV3Client for the given config and
// panics if there is an error in the config.
func NewForConfigOrDie(c *rest.Config) *ProjectcalicoV3Client {
	client, err := NewForConfig(c)
	if err != nil {
		panic(err)
	}
	return client
}

// New creates a new ProjectcalicoV3Client for the given RESTClient.
func New(c rest.Interface) *ProjectcalicoV3Client {
	return &ProjectcalicoV3Client{c}
}

func setConfigDefaults(config *rest.Config) error {
	gv := v3.SchemeGroupVersion
	config.GroupVersion = &gv
	config.APIPath = "/apis"
	config.NegotiatedSerializer = serializer.DirectCodecFactory{CodecFactory: scheme.Codecs}

	if config.UserAgent == "" {
		config.UserAgent = rest.DefaultKubernetesUserAgent()
	}

	return nil
}

// RESTClient returns a RESTClient that is used to communicate
// with API server by this client implementation.
func (c *ProjectcalicoV3Client) RESTClient() rest.Interface {
	if c == nil {
		return nil
	}
	return c.restClient
}
