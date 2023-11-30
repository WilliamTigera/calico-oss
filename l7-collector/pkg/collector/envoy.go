// Copyright (c) 2020 Tigera, Inc. All rights reserved.

package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/nxadm/tail"
	log "github.com/sirupsen/logrus"

	"github.com/projectcalico/calico/l7-collector/pkg/config"
)

const DestinationEnvoyReporter = "destination"

type envoyCollector struct {
	collectedLogs    chan EnvoyInfo
	config           *config.Config
	batch            *BatchEnvoyLog
	connectionCounts map[TupleKey]int
}

func EnvoyCollectorNew(cfg *config.Config) EnvoyCollector {
	return &envoyCollector{
		collectedLogs:    make(chan EnvoyInfo),
		config:           cfg,
		batch:            NewBatchEnvoyLog(cfg.EnvoyRequestsPerInterval),
		connectionCounts: make(map[TupleKey]int),
	}
}

func stop(t *tail.Tail) {
	err := t.Stop()
	if err != nil {
		return
	}
}

func (ec *envoyCollector) ReadLogs(ctx context.Context) {
	// Tail the file
	// Currently this reads from the end of the tail file to prevent
	// rereading the file.
	t, err := tail.TailFile(ec.config.EnvoyLogPath, tail.Config{
		Follow: true,
		ReOpen: true,
		Location: &tail.SeekInfo{
			Whence: ec.config.TailWhence,
		},
	})
	defer func() {
		// Call stop from within a defered function so that
		// t can be reassigned if the tail is restarted.
		stop(t)
	}()
	if err != nil {
		// TODO: Figure out proper error handling
		log.Warnf("Failed to tail envoy logs: %v", err)
		return
	}
	defer log.Errorf("Tail stopped with error: %v", t.Err())

	// Open the file for  monitoring it's size
	file, _ := os.Open(ec.config.EnvoyLogPath)
	defer file.Close()

	// Set up the ticker for reading the log files
	ticker := time.NewTicker(time.Duration(ec.config.EnvoyLogIntervalSecs) * time.Second)
	defer ticker.Stop()

	// Read logs from the file, add them to the batch, and periodically send the batch.
	for {
		// Periodically send the batched logs to the collection channel.
		// Having the ticker channel in its own select clause forces
		// the ticker case to get precedence over reading lines.
		select {
		case <-ticker.C:
			ec.ingestLogs()

			// If a maximum tail lag amount is set, check to make sure
			// that the tail is not falling too far behind.
			if ec.config.EnvoyTailMaxLag > 0 {
				// Seek the current progress of the tail
				current, err := t.Tell()
				if err != nil {
					log.Errorf("Error in attempting to monitor tail progress: %s", err)
					continue
				}

				// Seek the location of the end of the file
				end, err := file.Seek(0, 2)
				if err != nil {
					log.Errorf("Error in attempting retrieve end of log file for monitoring tail progress: %s", err)
					continue
				}

				// Restart the tail if we have fallen behind the maximum offset while tailing the logs
				if end-current > int64(ec.config.EnvoyTailMaxLag) {
					log.Warn("Log ingestion has fallen behind creation by 100 MB. Skipping to the most recent logs")
					newTail, err := tail.TailFile(ec.config.EnvoyLogPath, tail.Config{
						Follow: true,
						ReOpen: true,
						Location: &tail.SeekInfo{
							Whence: ec.config.TailWhence,
						},
					})
					if err != nil {
						log.Errorf("Error creating new tail for the envoy logs: %s. Continuing with the behind tail.", err)
						continue
					}
					t = newTail
				}
			}
			continue
		default:
			// Leave an empty default case so select statement will not block and wait.
		}
		// Read logs from the file and add them to the batch
		select {
		case <-ticker.C:
			ec.ingestLogs()
			continue
		case line := <-t.Lines:
			envoyLog, err := ec.ParseRawLogs(line.Text)
			if err != nil {
				log.Error("error in parsing raw logs", err)
				// Log line does not have properly formatted envoy info
				// Skip writing a lot to record this error because it is too noisy.
				continue
			}
			// Add this log to the batch
			ec.batch.Insert(envoyLog)

			// count connection statistics, this will contain connection counts even when batch is full
			tupleKey := TupleKeyFromEnvoyLog(envoyLog)
			ec.connectionCounts[tupleKey] = ec.connectionCounts[tupleKey] + 1

		case <-ctx.Done():
			log.Info("Collector shut down")
			return
		}
	}
}

func (ec *envoyCollector) ingestLogs() {
	intervalBatch := ec.batch
	intervalCounts := ec.connectionCounts
	ec.batch = NewBatchEnvoyLog(ec.config.EnvoyRequestsPerInterval)
	ec.connectionCounts = make(map[TupleKey]int)

	// Send a batch if there is data.
	logs := intervalBatch.logs
	if len(logs) != 0 {
		ec.collectedLogs <- EnvoyInfo{Logs: logs, Connections: intervalCounts}
	}
}

func (ec *envoyCollector) Report() <-chan EnvoyInfo {
	return ec.collectedLogs
}

// ParseRawLogs takes a log in the format: {} // TODO: add final format of the logs. Recent version can be found in data_test.go in FVs
// and returns an EnvoyLog with the relevant information.
func (ec *envoyCollector) ParseRawLogs(text string) (EnvoyLog, error) {
	log.Debug("parsing raw envoy logs ")

	// Unmarshall the bytes into the EnvoyLog data
	var envoyLog EnvoyLog
	err := json.Unmarshal([]byte(text), &envoyLog)

	if err != nil {
		// TODO: Figure out proper error handling
		log.Warnf("Failed to unmarshal L7 logs. Logs may be formatted incorrectly: %v", err)
		return EnvoyLog{}, err
	}

	// calculate latency
	if ust, err := strconv.Atoi(envoyLog.UpstreamServiceTime); err == nil {
		envoyLog.Latency = envoyLog.Duration - int32(ust)
	}

	return ParseFiveTupleInformation(envoyLog)
}

func ParseFiveTupleInformation(envoyLog EnvoyLog) (EnvoyLog, error) {

	if envoyLog.Reporter != DestinationEnvoyReporter {
		// client side logs are not processed at the time
		// on the client side downstream_local_address refers to destination, upstream_local_address to source
		log.Warnf("log of reporter type %v are not processed at this time", envoyLog.Reporter)
		return EnvoyLog{}, fmt.Errorf("log of reporter type %v are not processed at this time", envoyLog.Reporter)
	}

	// this case is envoyLog.Reporter == "destination" or the server side logs
	// on the server side downstream_local_address refers to destination, downstream_remote_address refers to source
	// Refer doc https://www.envoyproxy.io/docs/envoy/latest/configuration/observability/access_log/usage.html
	dh, dp, derr := net.SplitHostPort(envoyLog.DSLocalAddress)
	sh, sp, serr := net.SplitHostPort(envoyLog.DSRemoteAddress)

	if serr != nil {
		return EnvoyLog{}, fmt.Errorf("error parsing five tuple from downstream_remote_address %w", derr)
	}

	if derr != nil {
		return EnvoyLog{}, fmt.Errorf("error parsing five tuple from downstream_local_address %w", serr)
	}

	envoyLog.SrcIp = sh
	envoyLog.DstIp = dh
	sport, _ := strconv.Atoi(sp)
	dport, _ := strconv.Atoi(dp)
	envoyLog.SrcPort = int32(sport)
	envoyLog.DstPort = int32(dport)

	return envoyLog, nil

}
