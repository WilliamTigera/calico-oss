// Copyright (c) 2021 Tigera, Inc. All rights reserved.
package middleware

import (
	"context"
	"encoding/json"
	"net/http"

	log "github.com/sirupsen/logrus"
	"github.com/tigera/compliance/pkg/datastore"
	"github.com/tigera/es-proxy/pkg/user"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apiserver/pkg/endpoints/request"
)

type ElasticsearchLicenseType string

const (
	ECKOperatorNamespace    = "tigera-eck-operator"
	ECKLicenseConfigMapName = "elastic-licensing"

	ElasticsearchNamespace = "tigera-elasticsearch"

	ElasticsearchLicenseTypeBasic ElasticsearchLicenseType = "basic"

	OIDCUsersConfigMapName = "tigera-known-oidc-users"
)

type esBasicUserHandler struct {
	k8sClient     datastore.ClientSet
	dexEnabled    bool
	dexIssuer     string
	esLicenseType ElasticsearchLicenseType
}

func NewUserHandler(k8sClient datastore.ClientSet, dexEnabled bool, dexIssuer, elasticLicenseType string) http.Handler {
	return &esBasicUserHandler{
		k8sClient:     k8sClient,
		dexEnabled:    dexEnabled,
		dexIssuer:     dexIssuer,
		esLicenseType: ElasticsearchLicenseType(elasticLicenseType),
	}
}

// ServeHTTP stores the OIDC user information in OIDCUsersConfigMapName configmap,
// if the request is authenticated by dex, and Elasticsearch uses basic license,
// else return 200 OK.
func (handler *esBasicUserHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "Invalid http method", http.StatusMethodNotAllowed)
		return
	}

	log.WithFields(log.Fields{
		"Dex Enabled":           handler.dexEnabled,
		"Dex Issuer":            handler.dexIssuer,
		"Elasticsearch License": handler.esLicenseType,
	}).Debug("ServeHTTP called")

	ctx := context.Background()
	userInfo, ok := request.UserFrom(req.Context())
	if !ok {
		log.Error("failed to get userInfo from http request")
		http.Error(w, "Failed to get userInfo from http request", http.StatusInternalServerError)
		return
	}

	oidcUser, err := user.OIDCUserFromUserInfo(userInfo)
	if err != nil {
		log.WithError(err).Debug("failed to get OIDC user information")
		w.WriteHeader(http.StatusOK)
		return
	}

	if handler.dexEnabled && handler.esLicenseType == ElasticsearchLicenseTypeBasic && oidcUser.Issuer == handler.dexIssuer {
		if err := handler.addOIDCUserToConfigMap(ctx, oidcUser); err != nil {
			log.WithError(err).Debug("failed to add user to ConfigMap ", OIDCUsersConfigMapName)
		}
	}

	w.WriteHeader(http.StatusOK)
}

// addOIDCUserToConfigMap adds/updates a map into the data of OIDCUsersConfigMapName,
// where map key is the subject claim in JWT, this is a unique identifier within the Issuer
// and value contains username and all groups that the user belongs to
func (handler *esBasicUserHandler) addOIDCUserToConfigMap(ctx context.Context, oidcUser *user.OIDCUser) error {
	userGroupsStr, err := oidcUser.ToStr()
	if err != nil {
		return err
	}

	payload := map[string]interface{}{
		"data": map[string]string{
			oidcUser.SubjectID: userGroupsStr,
		},
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	_, err = handler.k8sClient.CoreV1().ConfigMaps(ElasticsearchNamespace).
		Patch(ctx, OIDCUsersConfigMapName, types.MergePatchType, payloadBytes, metav1.PatchOptions{})
	if err != nil {
		return err
	}
	return nil
}
