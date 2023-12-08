# Deep Packet Inspection

It starts a typha client to get updates on `DeepPacketInspection` and `WorkloadEndpoint` resources required by tigera-dpi daemonset.
Starts a snort process for each WEP that matches the selector in `DeepPacketInspection` resource.

## Building and testing

To build this locally, use one of the following commands:

```bash
make image
```

or

```bash
make ci
```

## Adding and running test

To run all tests

```bash
make fv ut
```

FV test runs against real k8s, they should be added to the `FV_GINKGO_FOCUS` variable in Makefile.

## Testing snort with local rules for development

To trigger snort alert during development, one option is to use custom snort rules.

Running snort with custom rules:

- Create and populate `local.rules` file, below is sample rule that alerts on any ICMP request.

```text
alert icmp any any -> any any ( sid:1000005; rev:1;)
```

- Copy the `local.rules` file to docker image by adding this line in Dockerfile.amd64.

```dockerfile
# Copy local rules for dev testing
COPY local.rules /usr/etc/rules/
```

- Pass the local rules file when setting snort command line in `/pkg/exec/snort_exec.go` like below.

```go
    exec.Command(
    "snort",
    ....
    "-R", "/usr/etc/rules/local.rules",
    )
```

## Debug logs

Set environment variable `DPI_LOG_LEVEL` with value `debug`.

## Update Snort3 version

To update the Snort3 version used for DPI, update the version number assigned to the variable `SNORT3_VERSION` in Dockerfile.

## Code flow

![Alt text](flow_diagram.svg)

```mermaid
sequenceDiagram
    participant M as Main
    participant EF as ESForwarder
    participant FM as FileMaintainer
    participant S as Syncer
    participant D as Dispatcher
    participant P as Processor
    participant E as EventGenerator
    participant ES as ElasticSearch
    participant T as Typha

    M-->>T: Creates a local/typha syncer client
    M->>S: Sync
    Note over S: Wait for typha to callback
    M->>EF: Run
    Note over EF: Wait for events and forward them to ES
    M->>FM: Run
    Note over FM: Delete older alter files on interval
    T-->>S: Send DPI and WEP resorces via Syncer callback
    S->>D: Send the DPI and WEP <br/> resource to Dispatch
    Note over D: For valid combination of WEP and DPI <br/> resource initailize Processor and EvenGenerator 
    D->>P: start the snort process
    Note over P: Starts/restarts snort on interface
    D->>E: start event generator
    Note over E: Tails the alert file, generates events <br/> forwards it to ESForwarder
    E->>EF: Security events
    EF-->>ES: Send received events to ES
```
