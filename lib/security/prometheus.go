// Copyright (c) 2018 Tigera, Inc. All rights reserved.

package security

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Serve Prometheus metrics from the specified gatherer at /metrics.
// The service is TLS-secured (HTTPS) if certFile, keyFile and caFile
// are all specified, in that (a) it only accepts connection from a
// client with a certificate signed by a trusted CA, and (b) data is
// sent to that client encrypted, and cannot be snooped.  Otherwise it
// is insecure (HTTP).
func ServePrometheusMetrics(gatherer prometheus.Gatherer, port int, certFile, keyFile, caFile string) (err error) {
	mux := http.NewServeMux()
	handler := promhttp.HandlerFor(gatherer, promhttp.HandlerOpts{})
	mux.Handle("/metrics", handler)
	if certFile != "" && keyFile != "" && caFile != "" {
		var caCert []byte
		caCert, err = ioutil.ReadFile(caFile)
		if err != nil {
			return
		}
		caCertPool := x509.NewCertPool()
		caCertPool.AppendCertsFromPEM(caCert)
		cfg := &tls.Config{
			ClientAuth: tls.RequireAndVerifyClientCert,
			ClientCAs:  caCertPool,
		}
		srv := &http.Server{
			Addr:      fmt.Sprintf(":%v", port),
			Handler:   handler,
			TLSConfig: cfg,
		}
		err = srv.ListenAndServeTLS(certFile, keyFile)
	} else {
		err = http.ListenAndServe(fmt.Sprintf(":%v", port), handler)
	}
	return
}
