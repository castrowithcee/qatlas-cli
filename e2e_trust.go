//go:build e2e

package main

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"os"
)

// Test builds only: the end-to-end tests serve BookStack from a local TLS mock. This file adds the mock's
// certificate named by QATLAS_E2E_CA_FILE to the trusted roots of the default transport. It relaxes no
// origin rule and is not part of a binary built without the e2e tag.
func init() {
	path := os.Getenv("QATLAS_E2E_CA_FILE")
	if path == "" {
		return
	}
	pemBytes, err := os.ReadFile(path)
	if err != nil {
		return
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(pemBytes) {
		return
	}
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		base.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
}
