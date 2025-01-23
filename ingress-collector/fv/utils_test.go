// Copyright (c) 2019-2023 Tigera, Inc. All rights reserved.

package fv_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"path"
	"sync"

	"google.golang.org/grpc"

	"github.com/projectcalico/calico/felix/proto"
	"github.com/projectcalico/calico/ingress-collector/pkg/collector"
	"github.com/projectcalico/calico/ingress-collector/pkg/config"
	"github.com/projectcalico/calico/ingress-collector/pkg/felixclient"
	"github.com/projectcalico/calico/libcalico-go/lib/logutils"
	"github.com/projectcalico/calico/libcalico-go/lib/uds"
)

const ListenerSocket = "policysync.sock"
const IngressLogFile = "ingress.log"

// CollectorTestHandler keeps track of all of the separate components
// needed for running collector FV tests.
type CollectorTestHandler struct {
	config     *config.Config
	collector  collector.IngressCollector
	client     felixclient.FelixClient
	context    context.Context
	cancel     context.CancelFunc
	stats      chan *proto.DataplaneStats
	server     *testPolicySyncServer
	grpcServer *grpc.Server
}

func NewCollectorTestHandler() *CollectorTestHandler {
	cfg := createTestConfig()
	c := collector.NewIngressCollector(cfg)
	opts := uds.GetDialOptions()
	felixClient := felixclient.NewFelixClient(cfg.DialTarget, opts)
	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)

	// Create the tmp log file to collect from
	f, _ := os.Create(cfg.IngressLogPath)
	defer f.Close()

	statsChan := make(chan *proto.DataplaneStats, 20)
	grpcServer := grpc.NewServer()
	server := newTestPolicySyncServer(statsChan)

	proto.RegisterPolicySyncServer(grpcServer, server)

	return &CollectorTestHandler{
		config:     cfg,
		collector:  c,
		client:     felixClient,
		context:    ctx,
		cancel:     cancel,
		stats:      statsChan,
		grpcServer: grpcServer,
		server:     server,
	}
}

func (cth *CollectorTestHandler) Shutdown() {
	cth.cancel()
	cth.grpcServer.Stop()
}

func (cth *CollectorTestHandler) CollectAndSend() {
	wg := sync.WaitGroup{}

	// Start the log ingestion go routine.
	wg.Add(1)
	go func() {
		cth.collector.ReadLogs(cth.context)
		cth.cancel()
		wg.Done()
	}()

	// Start the DataplaneStats reporting go routine.
	wg.Add(1)
	go func() {
		cth.client.SendStats(cth.context, cth.collector)
		cth.cancel()
		wg.Done()
	}()

	// Wait for the go routine to complete before exiting
	wg.Wait()
}

func createTestConfig() *config.Config {
	cfg := config.MustLoadConfig()
	tmpDir := makeTmpListenerDir()
	socketPath := path.Join(tmpDir, ListenerSocket)
	ingressLogFilePath := path.Join(tmpDir, IngressLogFile)
	cfg.DialTarget = socketPath
	cfg.IngressLogPath = ingressLogFilePath
	// Set the log level to debug
	cfg.LogLevel = "debug"
	// Set the interval between collecting logs to 5 seconds
	cfg.IngressLogIntervalSecs = 5
	// Set the max batch size to 5 for the tests
	cfg.IngressRequestsPerInterval = 5
	cfg.ParsedLogLevel = logutils.SafeParseLogLevel(cfg.LogLevel)
	// Set the tail to read from the beginning of the fake log file
	// to prevent waiting for the collector to start.
	cfg.TailWhence = 0
	cfg.InitializeLogging()
	return cfg
}

func makeTmpListenerDir() string {
	dirPath, err := os.MkdirTemp("", "felixut")
	if err != nil {
		return ""
	}
	return dirPath
}

func (cth *CollectorTestHandler) WriteToLog(logline string) {
	f, err := os.OpenFile(cth.config.IngressLogPath, os.O_APPEND|os.O_WRONLY, 0777)
	if err != nil {
		panic(err)
	}
	defer f.Close()

	_, err = f.WriteString(logline)
	if err != nil {
		if err, ok := err.(*os.PathError); ok {
			fmt.Println(err.Err.Error())
		}
		panic(err)
	}
	err = f.Sync()
	if err != nil {
		panic(err)
	}
}

func (cth *CollectorTestHandler) StatsChan() chan *proto.DataplaneStats {
	return cth.stats
}

func (cth *CollectorTestHandler) StartPolicySyncServer() {
	unixListener, err := net.Listen("unix", cth.config.DialTarget)
	if err != nil {
		panic(err)
	}
	_ = cth.grpcServer.Serve(unixListener)
}

func (cth *CollectorTestHandler) Timeout() string {
	return fmt.Sprintf("%vs", cth.config.IngressLogIntervalSecs*24)
}

func (cth *CollectorTestHandler) Interval() string {
	return fmt.Sprintf("%vs", cth.config.IngressLogIntervalSecs)
}

type testPolicySyncServer struct {
	stats chan *proto.DataplaneStats
}

func newTestPolicySyncServer(stats chan *proto.DataplaneStats) *testPolicySyncServer {
	return &testPolicySyncServer{
		stats: stats,
	}
}

func (s *testPolicySyncServer) Sync(sr *proto.SyncRequest, pss proto.PolicySync_SyncServer) error {
	// Don't do anything with this since our test server will not handle any syncs
	return nil
}

func (s *testPolicySyncServer) Report(ctx context.Context, dps *proto.DataplaneStats) (*proto.ReportResult, error) {
	s.stats <- dps
	// Always say it was successful
	return &proto.ReportResult{
		Successful: true,
	}, nil
}

func (s *testPolicySyncServer) ReportWAF(stream proto.PolicySync_ReportWAFServer) error {
	// Don't do anything with this since our test server will not handle any WAFEvent
	return nil
}

func DeepCopyDpsWithoutHttpData(src *proto.DataplaneStats) *proto.DataplaneStats {
	return &proto.DataplaneStats{
		SrcIp:    src.SrcIp,
		DstIp:    src.DstIp,
		SrcPort:  src.SrcPort,
		DstPort:  src.DstPort,
		Protocol: src.Protocol,
		Stats:    src.Stats,
	}
}
