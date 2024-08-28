// Copyright (c) 2023 Tigera, Inc. All rights reserved.

package server

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"time"

	log "github.com/sirupsen/logrus"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	"github.com/projectcalico/calico/app-policy/checker"
	"github.com/projectcalico/calico/app-policy/logger"
	"github.com/projectcalico/calico/app-policy/policystore"
	"github.com/projectcalico/calico/app-policy/statscache"
	"github.com/projectcalico/calico/app-policy/syncher"
	"github.com/projectcalico/calico/app-policy/waf"
	"github.com/projectcalico/calico/felix/proto"
	"github.com/projectcalico/calico/libcalico-go/lib/uds"
)

type Dikastes struct {
	subscriptionType             string
	dialNetwork, dialAddress     string
	listenNetwork, listenAddress string
	wafEnabled                   bool
	wafLogFile                   string
	wafRulesetFiles              []string
	wafDirectives                []string
	wafEventsFlushDuration       time.Duration
	grpcServerOptions            []grpc.ServerOption
	policyStoreManager           policystore.PolicyStoreManager

	Ready chan struct{}
}

type DikastesServerOptions func(*Dikastes)

func WithDialAddress(network, addr string) DikastesServerOptions {
	return func(ds *Dikastes) {
		ds.dialNetwork = network
		ds.dialAddress = addr
	}
}

func WithListenArguments(network, address string) DikastesServerOptions {
	return func(ds *Dikastes) {
		ds.listenNetwork = network
		ds.listenAddress = address
	}
}

func WithSubscriptionType(s string) DikastesServerOptions {
	return func(ds *Dikastes) {
		ds.subscriptionType = s
	}
}

func WithGRPCServerOpts(opts ...grpc.ServerOption) DikastesServerOptions {
	return func(ds *Dikastes) {
		ds.grpcServerOptions = append(ds.grpcServerOptions, opts...)
	}
}

func WithWAFConfig(enabled bool, logfile string, files, directives []string) DikastesServerOptions {
	return func(ds *Dikastes) {
		ds.wafEnabled = enabled
		ds.wafLogFile = logfile
		ds.wafDirectives = directives
		ds.wafRulesetFiles = files
	}
}

func WithWAFFlushDuration(duration time.Duration) DikastesServerOptions {
	return func(ds *Dikastes) {
		ds.wafEventsFlushDuration = duration
	}
}

func NewDikastesServer(opts ...DikastesServerOptions) *Dikastes {
	s := &Dikastes{
		Ready:                  make(chan struct{}),
		wafEventsFlushDuration: time.Second * 15,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

func ensureSocketFileNone(filePath string) error {
	_, err := os.Stat(filePath)
	if !os.IsNotExist(err) {
		// file exists, try to delete it.
		if err := os.Remove(filePath); err != nil {
			return fmt.Errorf("unable to remove socket file: %w", err)
		}
	}
	return nil
}

func ensureSocketFileAccessible(filePath string) error {
	// anyone on system can connect.
	if err := os.Chmod(filePath, 0777); err != nil {
		return fmt.Errorf("unable to set write permission on socket: %w", err)
	}
	return nil
}

func (d *Dikastes) GetWorkloadEndpoints() map[proto.WorkloadEndpointID]*proto.WorkloadEndpoint {
	return d.policyStoreManager.GetCurrentEndpoints()
}

func (s *Dikastes) Serve(ctx context.Context, readyCh ...chan struct{}) {
	if s.listenNetwork == "unix" {
		if err := ensureSocketFileNone(s.listenAddress); err != nil {
			log.Fatal("could not start listener: ", err)
		}
	}

	lis, err := net.Listen(s.listenNetwork, s.listenAddress)
	if err != nil {
		log.Fatal("could not start listener: ", err)
		return
	}
	defer lis.Close()

	if s.listenNetwork == "unix" {
		if err := ensureSocketFileAccessible(s.listenAddress); err != nil {
			log.Fatal("could not start listener: ", err)
		}
	}

	log.Infof("Dikastes listening at %s", lis.Addr())
	s.listenAddress = lis.Addr().String()

	// listen ready
	for _, r := range readyCh {
		r <- struct{}{}
	}

	gs := grpc.NewServer(s.grpcServerOptions...)

	dpStats := statscache.New()
	s.policyStoreManager = policystore.NewPolicyStoreManager()

	checkServerOptions := []checker.AuthServerOption{}

	if s.policySyncEnabled() {
		// features that require policy sync

		// syncClient provides synchronization with the policy store and start reporting stats.
		// ALP uses the policy store to retrieve the policy for a given endpoint.
		opts := uds.GetDialOptionsWithNetwork(s.dialNetwork)
		syncClient := syncher.NewClient(
			s.dialAddress,
			s.policyStoreManager,
			opts,
			syncher.WithSubscriptionType(s.subscriptionType),
		)
		syncClient.RegisterGRPCServices(gs)
		dpStats.RegisterFlushCallback(syncClient.OnStatsCacheFlush)
		go syncClient.Start(ctx)

		// register ALP check provider. registrations are ordered (first-registered-processed-first)
		checkServerOptions = append(
			checkServerOptions,
			checker.WithSubscriptionType(s.subscriptionType),
			checker.WithRegisteredCheckProvider(checker.NewALPCheckProvider(s.subscriptionType)),
		)
	}

	if s.wafEnabled {
		wafLogHandler := logger.New(setupWAFLogging(s.wafLogFile)...)
		events := waf.NewEventsPipeline(wafLogHandler.Process)
		go events.Start(ctx, s.wafEventsFlushDuration)
		wafServer, err := waf.New(s.wafRulesetFiles, s.wafDirectives, events)
		if err != nil {
			log.Fatalf("cannot initialize WAF: %v", err)
		}

		checkServerOptions = append(
			checkServerOptions,
			checker.WithRegisteredCheckProvider(wafServer),
		)
	}

	// checkServer provides envoy v3, v2, v2 alpha ext authz services
	checkServer := checker.NewServer(
		ctx, s.policyStoreManager, dpStats,
		checkServerOptions...,
	)
	checkServer.RegisterGRPCServices(gs)

	// register grpc reflection service, if enabled
	if _, ok := os.LookupEnv("DIKASTES_ENABLE_CHECKER_REFLECTION"); ok {
		reflection.Register(gs)
	}
	// Run gRPC server on separate goroutine so we catch any signals and clean up.
	go func() {
		if err := gs.Serve(lis); err != nil {
			log.Errorf("failed to serve: %v", err)
		}
	}()
	close(s.Ready)
	<-ctx.Done()

	gs.GracefulStop()
}

func (s *Dikastes) Addr() string {
	u := url.URL{Scheme: s.listenNetwork}
	if s.listenNetwork == "unix" {
		u.Path = s.listenAddress
		return u.String()
	}
	u.Host = s.listenAddress
	return u.String()
}

func setupWAFLogging(logFile string) []io.Writer {
	res := []io.Writer{os.Stderr}
	if logFile == "" {
		// blank logfile param. skip logfile setup
		log.Warn("only logging to stderr")
		return res
	}
	w, err := logger.FileWriter(logFile)
	if err != nil {
		log.Warnf("cannot create log file writer (%v). writing to stderr only", err)
		return res
	} else {
		res = append(res, w)
	}
	return res
}

func (s *Dikastes) policySyncEnabled() bool {
	return s.dialAddress != ""
}
