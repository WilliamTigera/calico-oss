// Copyright (c) 2021 Tigera, Inc. All rights reserved.
package user

import (
	"encoding/json"
	"fmt"

	"k8s.io/apiserver/pkg/authentication/user"
)

const (
	Subject = "sub"
	Issuer  = "iss"
)

// OIDCUserFromUserInfo validates and extracts OIDC related information from user.Info
// Dex authenticated user will have issuer and subject in user.Info.Extra,
// if these are not present it is not an OIDC user, return error.
func OIDCUserFromUserInfo(userInfo user.Info) (*OIDCUser, error) {
	extras := userInfo.GetExtra()
	var issClaim, subClaim []string
	var ok bool

	issClaim, ok = extras[Issuer]
	if !ok || len(issClaim) == 0 {
		return nil, fmt.Errorf("user not authenticated by dex, missing issuer in user.Info")
	}

	subClaim, ok = extras[Subject]
	if !ok || len(subClaim) == 0 {
		return nil, fmt.Errorf("user not authenticated by dex, missing subject in user.Info")
	}

	return &OIDCUser{
		Issuer:    issClaim[0],
		SubjectID: subClaim[0],
		Groups:    userInfo.GetGroups(),
		Username:  userInfo.GetName()}, nil
}

type OIDCUser struct {
	SubjectID string   `json:"-"`
	Issuer    string   `json:"-"`
	Username  string   `json:"username"`
	Groups    []string `json:"groups"`
}

func (o *OIDCUser) ToStr() (string, error) {
	payload, err := json.Marshal(o)
	if err != nil {
		return string(payload), err
	}
	return string(payload), err
}
