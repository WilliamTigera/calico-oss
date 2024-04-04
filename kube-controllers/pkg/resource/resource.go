// Copyright (c) 2019-2021 Tigera, Inc. All rights reserved.

package resource

const (
	ElasticsearchConfigMapName   = "tigera-secure-elasticsearch"
	ElasticsearchCertSecret      = "tigera-secure-es-http-certs-public"
	KibanaCertSecret             = "tigera-secure-kb-http-certs-public"
	ESGatewayCertSecret          = "tigera-secure-es-gateway-http-certs-public"
	VoltronLinseedPublicCert     = "tigera-voltron-linseed-certs-public"
	OperatorNamespace            = "tigera-operator"
	TigeraElasticsearchNamespace = "tigera-elasticsearch"
	DefaultTSEEInstanceName      = "tigera-secure"
	OIDCUsersConfigMapName       = "tigera-known-oidc-users"
	OIDCUsersEsSecreteName       = "tigera-oidc-users-elasticsearch-credentials"
	LicenseName                  = "default"
	CalicoNamespaceName          = "calico-system"
	ActiveOperatorConfigMapName  = "active-operator"
)
