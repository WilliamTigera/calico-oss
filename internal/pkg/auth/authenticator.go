// Copyright (c) 2019 Tigera, Inc. All rights reserved.

package auth

import (
	"encoding/base64"
	"strings"

	"k8s.io/client-go/rest"

	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	authn "k8s.io/api/authentication/v1"
	authz "k8s.io/api/authorization/v1"
	k8s "k8s.io/client-go/kubernetes"
)

// Authenticator authenticates a token to a User
type Authenticator interface {
	Authenticate(token string) (*User, error)
}

// BearerAuthenticator is a wrapper that authenticates Bearer tokens against K8s Api
type BearerAuthenticator struct {
	k8sAPI k8s.Interface
}

// NewBearerAuthenticator creates a Bearer Authenticator
func NewBearerAuthenticator(client k8s.Interface) *BearerAuthenticator {
	return &BearerAuthenticator{client}
}

// Authenticate attempts to authenticate a Bearer token to a known User against K8s Api
func (id BearerAuthenticator) Authenticate(token string) (*User, error) {
	if len(token) == 0 {
		return nil, errors.New("Invalid token")
	}

	review := &authn.TokenReview{
		Spec: authn.TokenReviewSpec{
			Token: token,
		}}

	result, err := id.k8sAPI.AuthenticationV1().TokenReviews().Create(review)
	if err != nil {
		return nil, err
	}

	if result.Status.Authenticated {
		user := &User{Name: result.Status.User.Username, Groups: result.Status.User.Groups}
		log.Debug("User was authenticated")
		return user, nil
	}

	return nil, errors.New("Token does not authenticate the user")
}

// BasicAuthenticator is a wrapper that authenticates Basic tokens
type BasicAuthenticator struct {
	apiGen K8sClientGenerator
}

// NewBasicAuthenticator creates a Basic Authenticator
func NewBasicAuthenticator(apiGen K8sClientGenerator) *BasicAuthenticator {
	return &BasicAuthenticator{apiGen: apiGen}
}

// Authenticate attempts to authenticate a Basic token to a User. The Basic token will be decoded
// and split into user and password. Username and password will be used in an anonymous k8s
// configuration to make a call to validate access against a fake K8s resource
func (id BasicAuthenticator) Authenticate(token string) (*User, error) {
	userPwd, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return nil, err
	}
	slice := strings.Split(string(userPwd), ":")
	if len(slice) != 2 {
		return nil, errors.New("Could not parse basic token")
	}

	log.Debugf("Creating anonymous configuration for k8s api for user %s", slice[0])
	k8sAPI, err := id.apiGen.Generate(slice[0], slice[1])
	if err != nil {
		return nil, err
	}

	selfAccessReview := authz.SelfSubjectAccessReview{
		Spec: authz.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authz.ResourceAttributes{Name: "fake"},
		},
	}

	log.Debug("Perform a call to Kube Api server to validate username and password")
	response, err := k8sAPI.AuthorizationV1().SelfSubjectAccessReviews().Create(&selfAccessReview)

	// In any case where a basic auth header is supplied that is not recognized, there will be an error.
	if err != nil {
		return nil, err
	}

	// We are not interested in status.Allowed, since we used a SSAR with no intention of checking
	// authz. However,if permission is Denied it means that the user is not authenticated.
	if response.Status.Denied {
		return nil, errors.New("user not authenticated")
	}

	user := &User{Name: slice[0], Groups: []string{"system:authenticated"}}
	return user, nil
}

// K8sClientGenerator will generate a K8s API Client tailored for the given user and password
type K8sClientGenerator interface {
	Generate(user string, password string) (k8s.Interface, error)
}

type k8sClientGenerator struct {
	config *rest.Config
}

// Generate creates a custom k8s API client. The client will authenticate using the given username and password
// and an anonymous k8s configuration (an anonymous K8s configuration is a copy of the current configuration omitting
// identity)
func (apiGen *k8sClientGenerator) Generate(user string, password string) (k8s.Interface, error) {
	anonymousCfg := rest.AnonymousClientConfig(apiGen.config)
	anonymousCfg.Username = user
	anonymousCfg.Password = password
	return k8s.NewForConfig(anonymousCfg)
}
