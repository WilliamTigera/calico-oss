// Copyright (c) 2023 Tigera, Inc. All rights reserved.
// Package waf implements a coraza-based checker.CheckProvider server.
// In this case, this authorization server provides a WAF service for the external authz request.
//
// The WAF service is implemented as a checker.CheckProvider for the checker.Checker server.
// The checker.Checker server is a gRPC server that implements envoy ext_authz
package waf

import (
	"fmt"
	"net/http"
	"strings"

	log "github.com/sirupsen/logrus"

	coreruleset "github.com/corazawaf/coraza-coreruleset"
	coraza "github.com/corazawaf/coraza/v3"
	corazatypes "github.com/corazawaf/coraza/v3/types"

	mergefs "github.com/jcchavezs/mergefs"
	mergefsio "github.com/jcchavezs/mergefs/io"

	code "google.golang.org/genproto/googleapis/rpc/code"
	status "google.golang.org/genproto/googleapis/rpc/status"

	envoycore "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	envoyauthz "github.com/envoyproxy/go-control-plane/envoy/service/auth/v3"

	"github.com/projectcalico/calico/app-policy/checker"
	"github.com/projectcalico/calico/app-policy/policystore"
	"github.com/projectcalico/calico/felix/proto"
)

var (
	OK   = newResponseWithCode(code.Code_OK, "OK")
	DENY = newResponseWithCode(code.Code_PERMISSION_DENIED, "Forbidden")
)

func newResponseWithCode(code code.Code, message string) *envoyauthz.CheckResponse {
	return &envoyauthz.CheckResponse{Status: &status.Status{Code: int32(code), Message: message}}
}

type eventCallbackFn func(interface{})

var _ checker.CheckProvider = (*Server)(nil)

type Server struct {
	coraza.WAF
	evp *wafEventsPipeline
}

func New(files, directives []string, evp *wafEventsPipeline) (*Server, error) {
	cfg := coraza.NewWAFConfig().
		WithRootFS(mergefs.Merge(
			coreruleset.FS,
			mergefsio.OSFS,
		)).
		WithRequestBodyAccess().
		WithErrorCallback(func(rule corazatypes.MatchedRule) {
			evp.Process(rule)
		})

	for _, f := range files {
		log.WithField("file", f).Debug("loading directives from file")
		cfg = cfg.WithDirectivesFromFile(f)
	}

	for _, d := range directives {
		log.WithField("directive", d).Debug("loading directive")
		cfg = cfg.WithDirectives(d)
	}

	waf, err := coraza.NewWAF(cfg)
	if err != nil {
		return nil, err
	}

	return &Server{
		WAF: waf,
		evp: evp,
	}, nil
}

func (w *Server) Name() string {
	return "coraza"
}

func (w *Server) Check(st *policystore.PolicyStore, checkReq *envoyauthz.CheckRequest) (*envoyauthz.CheckResponse, error) {
	if log.IsLevelEnabled(log.DebugLevel) {
		log.WithFields(log.Fields{"attributes": checkReq.Attributes}).Debug("check request received")
	}

	attrs := checkReq.Attributes
	req := attrs.Request
	httpReq := req.Http

	tx := w.NewTransactionWithID(httpReq.Id)

	defer tx.Close()

	if tx.IsRuleEngineOff() {
		return OK, nil
	}

	dstHost, dstPort, _ := peerToHostPort(attrs.Destination)
	srcHost, srcPort, _ := peerToHostPort(attrs.Source)

	// after the transaction is closed, process the http info for
	// the events pipeline
	defer func() {
		action := "pass"
		if in := tx.Interruption(); in != nil {
			action = in.Action
		}
		w.evp.Process(&txHttpInfo{
			txID:     tx.ID(),
			destIP:   dstHost,
			uri:      httpReq.Path,
			method:   httpReq.Method,
			protocol: req.Http.Protocol,
			srcPort:  srcPort,
			dstPort:  dstPort,
			action:   action,
		})
	}()

	tx.ProcessConnection(srcHost, int(srcPort), dstHost, int(dstPort))
	tx.ProcessURI(httpReq.Path, httpReq.Method, httpReq.Protocol)
	for k, v := range httpReq.Headers {
		tx.AddRequestHeader(k, v)
	}
	if host := req.Http.Host; host != "" {
		tx.AddRequestHeader("Host", host)
		tx.SetServerName(host)
	}
	if it := tx.ProcessRequestHeaders(); it != nil {
		return w.processInterruption(it)
	}

	if tx.IsRequestBodyAccessible() {
		s := strings.NewReader(httpReq.Body)
		switch it, _, err := tx.ReadRequestBodyFrom(s); {
		case err != nil:
			return nil, err
		case it != nil:
		}
	}
	switch it, err := tx.ProcessRequestBody(); {
	case err != nil:
		return nil, err
	case it != nil:
		return w.processInterruption(it)
	}
	return OK, nil
}

func (w *Server) processInterruption(it *corazatypes.Interruption) (*envoyauthz.CheckResponse, error) {
	resp := &envoyauthz.CheckResponse{Status: &status.Status{Code: int32(code.Code_OK)}}

	switch it.Action {
	// We only handle disruptive actions here.
	// However, not all disruptive actions mean a change in response code. See below:
	// - drop Initiates an immediate close of the TCP connection by sending a FIN packet.
	// - deny Stops rule processing and intercepts transaction.
	// - block Performs the disruptive action defined by the previous SecDefaultAction.
	// - pause Pauses transaction processing for the specified number of milliseconds. We don't support this, yet.
	// - proxy Intercepts the current transaction by forwarding the request to another web server using the proxy backend.
	// 		The forwarding is carried out transparently to the HTTP client (i.e., there’s no external redirection taking place)
	// - redirect Intercepts transaction by issuing an external (client-visible) redirection to the given location
	//
	// for more info about actions: https://coraza.io/docs/seclang/actions/ and note the Action Group for each.
	case "allow":
		// default response code is OK, do nothing but return OK
	case "drop", "deny", "block":
		resp = newResponseWithCode(
			statusFromAction(it, code.Code_PERMISSION_DENIED),
			messageFromInterruption(it),
		)
	case "pause", "proxy", "redirect":
		log.Warnf("unsupported action (%s), proceeding with no-op", it.Action)
	default:
		// all other actions should be non-disruptive. Do nothing but return OK
	}

	return resp, nil
}

func messageFromInterruption(it *corazatypes.Interruption) string {
	return fmt.Sprintf("WAF rule %d interrupting request: %s (%d)", it.RuleID, it.Action, it.Status)
}

func statusFromAction(it *corazatypes.Interruption, fallbackValue code.Code) code.Code {
	if it.Status != 0 {
		return statusToCode(it.Status)
	}
	return fallbackValue
}

func statusToCode(s int) code.Code {
	switch s {
	case http.StatusOK:
		return code.Code_OK
	case http.StatusBadRequest:
		return code.Code_INVALID_ARGUMENT
	case http.StatusUnauthorized:
		return code.Code_UNAUTHENTICATED
	case http.StatusForbidden:
		return code.Code_PERMISSION_DENIED
	case http.StatusNotFound:
		return code.Code_NOT_FOUND
	case http.StatusConflict:
		return code.Code_ALREADY_EXISTS
	case http.StatusTooManyRequests:
		return code.Code_RESOURCE_EXHAUSTED
	case 499: // HTTP 499 Client Closed Request
		return code.Code_CANCELLED
	case http.StatusInternalServerError:
		return code.Code_INTERNAL
	case http.StatusNotImplemented:
		return code.Code_UNIMPLEMENTED
	case http.StatusBadGateway:
		return code.Code_UNAVAILABLE
	case http.StatusServiceUnavailable:
		return code.Code_UNAVAILABLE
	case http.StatusGatewayTimeout:
		return code.Code_DEADLINE_EXCEEDED
	}
	return code.Code_UNKNOWN
}

func peerToHostPort(peer *envoyauthz.AttributeContext_Peer) (host string, port uint32, ok bool) {
	switch v := peer.Address.Address.(type) {
	case *envoycore.Address_SocketAddress:
		host = v.SocketAddress.Address
		switch vv := v.SocketAddress.PortSpecifier.(type) {
		case *envoycore.SocketAddress_PortValue:
			port = vv.PortValue
			ok = true
		}
		return
	}
	return "127.0.0.1", 80, false
}

func extractFirstWepNameAndNamespace(weps []proto.WorkloadEndpointID) (string, string) {
	if len(weps) == 0 {
		return "-", "-"
	}

	wepName := weps[0].WorkloadId
	parts := strings.Split(wepName, "/")
	if len(parts) == 2 {
		return parts[0], parts[1]
	}

	return wepName, "-"
}
