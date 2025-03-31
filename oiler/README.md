# OILER

Oiler is a tool used for Calico Cloud to migrate ES data regardless of the format. This component is not part of
Enterprise, but it will be built by our CI pipelines.

Its immediate application will be to migrate data from internal es single tenant to external es single tenant.

We will make use of Linseed's backend that can transform data in format (single-tenant/multi-tenant) and can connect
with two different Elasticsearch clusters. The logic behind this migration is that we will perform a read from a primary
source and write to secondary source.

Reading from primary is always done using field generated_time as a starting point and ordered by generated_time
ascendant. This field is populated by Linseed at ingestion. Oiler will launch periodically queries to check for new
data.

## Building and testing

To build this locally, use one of the following commands:

```
make image
```

or

```
make ci
```

## Adding and running test

To run all tests

```
make test
```

FV tests require an Elasticsearch and a K8S-api instance. Running FV locally need run-elastic and k8s-setup

## Configuration and permissions

| ENV             | Default value |              Description |
|-----------------|:-------------:|-------------------------:|
| OILER_LOG_LEVEL |    `Info`     | Log Level across service |

Oiler needs the following permissions inside a K8S cluster:

- GET/CREATE/UPDATE configmaps

Installing Oiler in a management cluster is done via the Helm charts repository

## Docs

- https://docs.google.com/document/d/1KQdm6TEiyp9rNZaoauFlh5tPmnrtJLN6P6jnLuT0-RU/edit?tab=t.0
- https://tigera.atlassian.net/wiki/spaces/ENG/pages/3122167815/ElasticSearch+Data+Migration+internal+-+external?force_transition=e610a214-b655-435e-8333-cf4562cadd88


