// Copyright (c) 2020 Tigera, Inc. All rights reserved.

package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/SermoDigital/jose/jws"

	log "github.com/sirupsen/logrus"

	authnv1 "k8s.io/api/authentication/v1"
	authzv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sserviceaccount "k8s.io/apiserver/pkg/authentication/serviceaccount"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/client-go/kubernetes"
	authenticationv1 "k8s.io/client-go/kubernetes/typed/authentication/v1"
	"k8s.io/client-go/rest"
)

// Common claim names
const (
	ClaimNameIss           = "iss"
	ClaimNameSub           = "sub"
	ClaimNameExp           = "exp"
	ClaimNameName          = "name"
	ClaimNameEmail         = "email"
	ClaimNameAud           = "aud"
	ClaimNameEmailVerified = "email_verified"
	ClaimNameGroups        = "groups"
)

const (
	// ServiceAccountIss is the value for iss in service account tokens.
	ServiceAccountIss = "kubernetes/serviceaccount"
)

// JWTAuth replaces the now deprecated AggregateAuthenticator for the following reasons:
//   - It is faster. It extracts the issuer from the token and only authenticates based on that issuer.
//   - It takes impersonation headers into account.
//   - It uses token reviews in favour of authentication reviews. This is the k8s native way of authn and does not need
//     special RBAC permissions.
//
// JWTAuth should be constructed with a k8s client and interface for service account token auth and for the authorization
// checks that are related to impersonation. It then accepts extra authenticators for any other bearer JWT token issuers,
// as described in RFC-7519.
//
// Note: JWTAuth is for JWT bearer tokens and does not support basic auth or other tokens.
type JWTAuth interface {
	Authenticator

	RBACAuthorizer
}

// Authenticator authenticates a user based on an authorization header, whether the user uses basic auth or token auth.
type Authenticator interface {
	// Authenticate checks if a request is authenticated. It accepts only JWT bearer tokens (RFC-6750, RFC-7519).
	// If it has impersonation headers, it will also check if the authenticated user is authorized
	// to impersonate. The resulting user info will be that of the impersonated user.
	Authenticate(r *http.Request) (userInfo user.Info, httpStatusCode int, err error)
}

// JWTAuthOption can be provided to NewJWTAuth to configure the authenticator.
type JWTAuthOption func(*jwtAuth) error

type k8sAuthn struct {
	authnCli authenticationv1.AuthenticationV1Interface
}

// Authenticate expects an authorization header containing a bearer token and returns the authenticated user.
func (k *k8sAuthn) Authenticate(r *http.Request) (userInfo user.Info, httpStatusCode int, err error) {
	// This will return an error when:
	// - No authorization header is present
	// - No Bearer prefix is present in the authorization header
	// - No JWT is present
	_, err = jws.ParseJWTFromRequest(r)
	if err != nil {
		return nil, 401, jws.ErrNoTokenInRequest
	}
	authHeader := r.Header.Get("Authorization")

	// Strip the "Bearer " part of the token.
	token := authHeader[7:]
	tknReview, err := k.authnCli.TokenReviews().Create(
		context.Background(),
		&authnv1.TokenReview{
			Spec: authnv1.TokenReviewSpec{Token: token},
		},
		metav1.CreateOptions{})
	if err != nil {
		return nil, 500, err
	}
	if !tknReview.Status.Authenticated {
		return nil, 401, fmt.Errorf("user is not authenticated")
	}

	return &user.DefaultInfo{
		Name:   tknReview.Status.User.Username,
		Groups: tknReview.Status.User.Groups,
		Extra:  toExtra(tknReview.Status.User.Extra),
	}, 200, nil
}

// WithAuthenticator adds an authenticator for a specific token issuer.
func WithAuthenticator(issuer string, authenticator Authenticator) JWTAuthOption {
	return func(a *jwtAuth) error {
		a.authenticators[issuer] = authenticator
		return nil
	}
}

// NewJWTAuth creates an object adhering to the Auth interface. It can perform authN and authZ.
func NewJWTAuth(restConfig *rest.Config, k8sCli kubernetes.Interface, options ...JWTAuthOption) (JWTAuth, error) {
	// This will return an error when:
	// - No authorization header is present
	// - No Bearer prefix is present in the authorization header
	// - No JWT is present
	jwt, err := jws.ParseJWT([]byte(restConfig.BearerToken))
	if err != nil {
		return nil, err
	}

	// The rest config's issuer may be different from ServiceAccountIss. If this is the case, we need to add the authenticator
	// for this issuer as well. Impersonating users' bearer tokens will have this issuer.
	k8sIss, ok := jwt.Claims().Issuer()
	if !ok {
		return nil, fmt.Errorf("cannot derive issuer from in-cluster configuration: %v", jws.ErrIsNotJWT)
	}

	authn := &k8sAuthn{k8sCli.AuthenticationV1()}
	jAuth := &jwtAuth{
		authenticators: map[string]Authenticator{
			// This issuer is used for tokens from service account secrets.
			ServiceAccountIss: authn,
			// This user is used for tokens from impersonating users.
			k8sIss: authn,
		},
		RBACAuthorizer: NewRBACAuthorizer(k8sCli),
	}

	for _, opt := range options {
		err := opt(jAuth)
		if err != nil {
			return nil, err
		}
	}

	return jAuth, nil
}

type jwtAuth struct {
	authenticators map[string]Authenticator
	RBACAuthorizer
}

// Authenticate checks if a request is authenticated. It accepts only JWT bearer tokens.
// If it has impersonation headers, it will also check if the authenticated user is authorized
// to impersonate. The resulting user info will be that of the impersonated user.
func (a *jwtAuth) Authenticate(req *http.Request) (user.Info, int, error) {
	// This will return an error when:
	// - No authorization header is present
	// - No Bearer prefix is present in the authorization header
	// - No JWT is present
	jwt, err := jws.ParseJWTFromRequest(req)
	if err != nil {
		return nil, 401, jws.ErrNoTokenInRequest
	}

	issuer, ok := jwt.Claims().Issuer()
	if !ok {
		return nil, 401, jws.ErrIsNotJWT
	}

	authn, ok := a.authenticators[issuer]
	var userInfo user.Info
	if ok {
		usr, stat, err := authn.Authenticate(req)
		if err != nil {
			return usr, stat, err
		}
		userInfo = usr
	} else {
		return nil, 400, fmt.Errorf("bearer token was not issued by a trusted issuer")
	}

	// If a user was impersonated, see if the impersonating user is allowed to impersonate.
	impersonatedUser, err := extractUserFromImpersonationHeaders(req)
	if err != nil {
		return nil, 401, err
	}
	if impersonatedUser != nil {
		attributes := buildResourceAttributesForImpersonation(impersonatedUser)

		for _, resAtr := range attributes {
			ok, err = a.Authorize(userInfo, resAtr, nil)
			if err != nil {
				return nil, 500, err
			} else if !ok {
				return nil, 401, fmt.Errorf("user is not allowed to impersonate")
			}
		}
		userInfo = impersonatedUser
	}
	return userInfo, 200, nil
}

// toExtra convenience func to convert the extra's from user.info's.
func toExtra(extra map[string]authnv1.ExtraValue) map[string][]string {
	ret := make(map[string][]string)
	for k, v := range extra {
		ret[k] = v
	}
	return ret
}

// extractUserFromImpersonationHeaders extracts the user info if a user is impersonated.
// See https://kubernetes.io/docs/reference/access-authn-authz/authentication/ for more information on how authn
// and authz come into play when authenticating.
func extractUserFromImpersonationHeaders(req *http.Request) (user.Info, error) {
	userName := req.Header.Get(authnv1.ImpersonateUserHeader)
	groups := req.Header[authnv1.ImpersonateGroupHeader]
	extras := make(map[string][]string)
	for headerName, value := range req.Header {
		if strings.HasPrefix(headerName, authnv1.ImpersonateUserExtraHeaderPrefix) {
			encodedKey := strings.ToLower(headerName[len(authnv1.ImpersonateUserExtraHeaderPrefix):])
			extraKey, err := url.PathUnescape(encodedKey)
			if err != nil {
				err := fmt.Errorf("malformed extra key for impersonation request")
				log.WithError(err).Errorf("Could not decode extra key %s", encodedKey)
			}
			extras[extraKey] = value
		}
	}

	if len(userName) == 0 && (len(groups) != 0 || len(extras) != 0) {
		return nil, fmt.Errorf("impersonation headers are missing impersonate user header")
	}

	if len(userName) != 0 {
		return &user.DefaultInfo{
			Name:   userName,
			Groups: groups,
			Extra:  extras,
		}, nil
	}
	return nil, nil
}

// buildResourceAttributesForImpersonation is a convenience func for performing authz checks when users are impersonated.
// See https://kubernetes.io/docs/reference/access-authn-authz/authentication/ for more information on how authn
// and authz come into play when authenticating.
func buildResourceAttributesForImpersonation(usr user.Info) []*authzv1.ResourceAttributes {
	var result []*authzv1.ResourceAttributes
	namespace, name, err := k8sserviceaccount.SplitUsername(usr.GetName())
	if err == nil {
		result = append(result, &authzv1.ResourceAttributes{
			Verb:      "impersonate",
			Resource:  "serviceaccounts",
			Name:      name,
			Namespace: namespace,
		})
	} else {
		result = append(result, &authzv1.ResourceAttributes{
			Verb:     "impersonate",
			Resource: "users",
			Name:     usr.GetName(),
		})
	}

	for _, group := range usr.GetGroups() {
		result = append(result, &authzv1.ResourceAttributes{
			Verb:     "impersonate",
			Resource: "groups",
			Name:     group,
		})
	}

	for key, extra := range usr.GetExtra() {
		for _, value := range extra {
			result = append(result, &authzv1.ResourceAttributes{
				Verb:        "impersonate",
				Resource:    "userextras",
				Subresource: key,
				Name:        value,
			})
		}
	}

	return result
}
