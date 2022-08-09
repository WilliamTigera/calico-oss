# ts-queryserver

This directory contains a proof of concept for a "calicoq" web server.

### Code structure

#### /queryserver
Main web server binary.  Registers a bunch of handlers for each URL endpoint.

#### /queryserver/handlers
Handlers which parse query parameters and convert web queries into querycache queries.

#### /pkg
Reusable code (could be used in calicoq if we wanted to rework it to use the same infrastructure)

#### /pkg/clientmgr
Simple helper to instantiate a v3 client instance from either a supplied config file or environment
variables.

TODO: We don't hook in a file at the moment - so only really uses environments.

#### /pkg/querycache
Contains the core functionality.  A syncer-fed cache that passes endpoint labels and policy selectors to maintain
a summary of endpoint/policy counts and to allow "fake" selector or endpoints to be specified allowing a query to 
see endpoint/selector links.

##### cache.go
This is the main cache.  It has methods for updating the cache (syncer events) and for performing a query.  The
cache is not thread safe.

##### syncerqueryserializer.go
This implements both the QueryInterface and the syncer callback interface.  It is used by the cache to serialize
calls into the two interfaces, thus the cache itself does not need to be synchronized.  Separating this out
simplifies the overall logic in cache.go, and allows us (if required) to manage separately the rate at which we
process events vs. queries.

##### query.go
This contains the public facing structures and API for the querycache.

### Building and running the code

Running `make testenv` will build all of the required binaries and containers and will run and drop you into a
test environment container that has `curl` and `calicoctl` installed.  For example from there you could run:

```
# Move into directory containing test data.
cd /code/test-data

# Should see etcd, webserver and this test environment running
docker ps

# Check server version.
curl localhost:8080/version

# Check summary stats.  Should be nothing configured
curl https://localhost:8080/summary -k

# Apply endpoint config, but no policy at the moment.
# 1 WEP with label "panda=sad", namespace1
# 1 WEP with label "panda=verysad", namespace2
# 2 HEPs with label "host="
calicoctl apply -f 1.yaml

# Check summary stats.  Should be some endpoint counts and no policies.
curl https://localhost:8080/summary -k

# Query endpoints by selector.  Should be a o workload endpoint with level gold.
curl https://localhost:8080/endpoints?selector=panda==\'sad\' -k

# and two host endpoints which have a host label.
curl https://localhost:8080/endpoints?selector=has\(host\) -k

# Apply policy configuration.
# GNP matching all(host):  should match both HEPs
# NP matching "panda=sad", namespace1:  should match 1 WEP
# NP matching "panda=verysad", namespace1:  should match 0 WEP (wrong namespacae)
calicoctl apply -f 2.yaml

# Check summary stats.  Should be some total endpoint counts and endpoint counts for each policies.
curl https://localhost:8080/summary -k

# Query endpoints by selector.  Should be a couple referenced, with total policies that match these endpoints.
curl https://localhost:8080/endpoints?selector=panda==\'verysad\' -k

# Query which GNP policies match a particular label set.
curl https://localhost:8080/policies?host=yes -k

# And for NPs, include the namespace label.
curl "https://localhost:8080/policies?panda=verysad&projectcalico.org/namespace=namespace1" -k
```
