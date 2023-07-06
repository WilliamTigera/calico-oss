# LINSEED

Linseed is a REST API for interacting with Calico Enterprise data primitives like:
- flows and flow logs
- DNS flows and DNS logs
- L7 flows and L7 logs
- audit logs (Kubernetes and Calico Enterprise)
- BGP logs
- events
- runtime reports
- WAF logs

It also provided additional APIs that extract high level information from the primitives described above.
- processes (extracted from flow logs)

*What defines a log ?*

A log records the raw event that happened when two components interact within a K8S cluster over a period of time.
An example of such interaction can be establishing the connection between a client application and server application.
A log can gather statistics like how much data is being transmitted, who initiated the interaction and if the interaction was successful or not.

Logs have multiple flavours, as they gather raw data at L3-L4 level, L7 level, DNS, K8s Audit Service, BGP etc.

*What defines a flow ?*

A flow is an aggregation of one or multiple logs that describe the interaction between a source and destination over a given period of time.
Flows have direction, as they can be reported by either the source and destination.


## Building and testing

To build this locally, use one of the following commands:

```
make image
```

or

```
make ci
```

## Local development

To run all tests

```
make test
```

In order to run locally, start an elastic server on localhost and k8s server using:

```
make run-elastic k8s-setup
```

Start Linseed with the following environment variables:

- ELASTIC_SCHEME=http
- ELASTIC_HOST=localhost
- LINSEED_HTTPS_CERT=~/calico-private/linseed/fv/cert/localhost.crt
- LINSEED_HTTPS_KEY=~/calico-private/linseed/fv/cert/localhost.key
- LINSEED_CA=~/calico-private/linseed/fv/cert/RootCA.crt
- KUBERNETES_SERVICE_HOST=127.0.0.1
- KUBERNETES_SERVICE_PORT=6443


Or simply use the following command:

```
make run-image
```

## Clients

In order to call Linseed API, you can make use of the clients provided as part of the API.

An example to make a paginated query to read flow logs from the last 5 minutes is provided below:

```
	// Create linseed client.
	config := rest.Config{
		URL:             "https://tigera-linseed.tigera-elasticsearch.svc",
		CACertPath:      "<replace with Linseed CA>",
		ClientKeyPath:   "<replace with Linseed Client Certificate Path>",
		ClientCertPath:  "<replace with Linseed Client Certificate Key>",
		FIPSModeEnabled: true,
	}
	linseed, err := client.NewClient("<replace with Tenant ID or leave blank>", config, rest.WithTokenPath("<replace with Token path>"))
	if err != nil {
		log.WithError(err).Fatal("failed to create linseed client")
	}

	// Define a context that will be used to make requests to Linseed
	ctx, cancel := context.WithTimeout(context.Background(), 5 * time.Second)
	defer cancel()

	// Define query parameters
	params := v1.FlowLogParams{
		QueryParams: v1.QueryParams{
			TimeRange: &lmav1.TimeRange{
				From: time.Now().Add(-5 * time.Second),
				To:   time.Now().Add(5 * time.Second),
			},
			MaxPageSize: 10,
		},
	}

	// Perform paginated list.
	pager := client.NewListPager[v1.FlowLog](&params)
	pages, errors := pager.Stream(ctx, linseed.FlowLogs("<replace with managed CLUSTER name or leave blank>").List)

	for page := range pages {
		for _, item := range page.Items {
			// Process returned data
			log.Infof("Received item %v", item)
		}
	}

	if err, ok := <-errors; ok {
		log.WithError(err).Error("failed to read flow logs")
	}
```

## Configuration and permissions

| ENV                             |                  Default value                   |                                                                                                                  Description |
|---------------------------------|:------------------------------------------------:|-----------------------------------------------------------------------------------------------------------------------------:|
| LINSEED_PORT                    |                      `443`                       |                                                                                              Local Port to start the service |
| LINSEED_HOST                    |                     <empty>                      |                                                                                                         Host for the service |
| LINSEED_LOG_LEVEL               |                      `Info`                      |                                                                                                     Log Level across service |
| LINSEED_HTTPS_CERT              |              `/certs/https/tls.crt`              |                                                                                                             Path to tls cert |
| LINSEED_HTTPS_KEY               |              `/certs/https/tls.key`              |                                                                                                              Path to tls key |
| LINSEED_CA_CERT                 |            `/certs/https/client.crt`             |                                                                                                              Path to ca cert |
| LINSEED_FIPS_MODE_ENABLED       |                     `false`                      |                                                                      FIPSModeEnabled Enables FIPS 140-2 verified crypto mode |
| LINSEED_EXPECTED_TENANT_ID      |                        -                         |                                               ExpectedTenantID will be verified against x-tenant-id header for all API calls |
| ELASTIC_HOST                    | `tigera-secure-es-http.tigera-elasticsearch.svc` |                                                                            Elastic Host; For local development use localhost |
| ELASTIC_PORT                    |                      `9200`                      |                                                                                 Elastic Port; For local development use 9200 |
| ELASTIC_SCHEME                  |                     `https`                      |                                                                         Defines what protocol is used to sniff Elastic nodes |
| ELASTIC_USERNAME                |                     <empty>                      |                                        Elastic username; If left empty, communication with Elastic will not be authenticated |
| ELASTIC_PASSWORD                |                     <empty>                      |                                        Elastic password; If left empty, communication with Elastic will not be authenticated |
| ELASTIC_CA                      |          `/certs/elasticsearch/tls.crt`          |                                                                                                  Elastic ca certificate path |
| ELASTIC_CLIENT_CERT             |        `/certs/elasticsearch/client.crt`         |                        Elastic client certificate path; It will only be picked up if LINSEED_ELASTIC_MTLS_ENABLED is enabled |
| ELASTIC_CLIENT_KEY              |        `/certs/elasticsearch/client.key`         |                                Elastic client key path; It will only be picked up if LINSEED_ELASTIC_MTLS_ENABLED is enabled |
| ELASTIC_MTLS_ENABLED            |                     `false`                      |                                                                       Enables mTLS communication between Elastic and Linseed |
| ELASTIC_GZIP_ENABLED            |                     `false`                      |                                                                       Enables gzip communication between Elastic and Linseed |
| ELASTIC_SNIFFING_ENABLED        |                     `false`                      |                                                                                           Enabled sniffing for Elastic nodes |
| ELASTIC_REPLICAS                |                       `0`                        |                                                                                          Elastic replicas for index creation |
| ELASTIC_SHARDS                  |                       `1`                        |                                                                                            Elastic shards for index creation |
| ELASTIC_FLOW_REPLICAS           |                       `0`                        |                                                                                    Elastic replicas for flows index creation |
| ELASTIC_FLOW_SHARDS             |                       `1`                        |                                                                                      Elastic shards for flows index creation |
| ELASTIC_DNS_REPLICAS            |                       `0`                        |                                                                                      Elastic replicas for DNS index creation |
| ELASTIC_DNS_SHARDS              |                       `1`                        |                                                                                        Elastic shards for DNS index creation |
| ELASTIC_AUDIT_REPLICAS          |                       `0`                        |                                                                                    Elastic replicas for Audit index creation |
| ELASTIC_AUDIT_SHARDS            |                       `1`                        |                                                                                      Elastic shards for Audit index creation |
| ELASTIC_BGP_REPLICAS            |                       `0`                        |                                                                                      Elastic replicas for BGP index creation |
| ELASTIC_BGP_SHARDS              |                       `1`                        |                                                                                        Elastic shards for BGP index creation |
| ELASTIC_WAF_REPLICAS            |                       `0`                        |                                                                                      Elastic replicas for WAF index creation |
| ELASTIC_WAF_SHARDS              |                       `1`                        |                                                                                        Elastic shards for WAF index creation |
| ELASTIC_L7_REPLICAS             |                       `0`                        |                                                                                       Elastic replicas for L7 index creation |
| ELASTIC_L7_SHARDS               |                       `1`                        |                                                                                         Elastic shards for L7 index creation |
| ELASTIC_RUNTIME_REPLICAS        |                       `0`                        |                                                                                  Elastic replicas for runtime index creation |
| ELASTIC_RUNTIME_SHARDS          |                       `1`                        |                                                                                    Elastic shards for runtime index creation |
| ELASTIC_INDEX_MAX_RESULT_WINDOW |                     `10000`                      | Elastic setting that sets the maximum value of from + size for searches. After this limit is hit, deep pagination is enabled |


Linseed is deployed in namespace `tigera-elasticsearch` as part of Calico Enterprise installation.
It establishes connections with the following components:
- `tigera-elasticsearch/tigera-elasticsearch-*` pod via service `tigera-secure-es-http.tigera-elasticsearch.svc:9200`

It has the following clients, via service `tigera-linseed.tigera-elasticsearch.svc`
- `es-proxy` container from `tigera-manager/tigera-manager-*` pod, deployment `tigera-manager/tigera-manager`
- `intrusion-detection-controller` container from `tigera-intrusion-detection/intrusion-detection-controller-*` pod, deployment `tigera-intrusion-detection/intrusion-detection-controller`
- `fluentd-node` container from `tigera-fluentd/fluentd-node-*` pod, daemonset `tigera-fluentd/fluentd-node`
- `fluentd-node` container from `tigera-fluentd/fluentd-node-windows*` pod, daemonset `tigera-fluentd/fluentd-node-windows`
- `tigera-dpi` container from `tigera-dpi/tigera-dpi-*` pod, daemonset `tigera-dpi/tigera-dpi`
- `compliance-benchmarker` container from `tigera-compliance/compliance-benchmarker-*` pod, daemonset `tigera-compliance/compliace-benchmarker`
- `compliance-controller` container from `tigera-compliance/compliance-controller-*` pod, deployment `tigera-compliance/compliace-controller`
- `compliance-snapshotter` container from `tigera-compliance/compliance-snapshotter-*` pod, deployment `tigera-compliance/compliace-snapshotter`
- `compliance-server` container from `tigera-compliance/compliance-server-*` pod, deployment `tigera-compliance/compliance-server`
- `policy-recommendation-controller` container from `tigera-policy-recommendation/tigera-policy-recommendation-*` pod, deployment `tigera-policy-recommendation\tigera-policy-recommendation`
- `adjobs` container from `tigera-intrusion-detection/cluster-tigera.io.detector.*` cron jobs

It requires RBAC access for:
- CREATE for authorization.k8s.io.SubjectAccessReview
- CREATE for authentication.k8s.io.TokenReviews.
- LIST,WATCH for projectcalico.org.ManagedClusters

All communication wil Linseed requires mTLS. X509 certificates will be mounted inside the pod via operator at `/etc/pki/tls/certs/` and `/tigera-secure-linseed-cert`


## Docs

- [Low level design for changes to the log storage subsystem](https://docs.google.com/document/d/1raHOohq0UWlLD9ygqsvu4vPMNNS9iGeY5xhHKt0O3Hc/edit?usp=sharing)
- [Multi-tenancy Proposal](https://docs.google.com/document/d/1HM0gba3hlR_cdTqHWc-NSqoiGHrVdTc_g1w3k8NmSdM/edit?usp=sharing)



