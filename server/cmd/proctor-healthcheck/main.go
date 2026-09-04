// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

// proctor-healthcheck is the narrow distroless-container readiness probe.
package main

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

const defaultReadinessURL = "http://127.0.0.1:8065/health/ready"

func main() {
	url := flag.String("url", environmentDefault("PROCTOR_HEALTHCHECK_URL", defaultReadinessURL), "readiness URL")
	timeout := flag.Duration("timeout", 2*time.Second, "request timeout")
	serverName := flag.String("server-name", environmentDefault("PROCTOR_HEALTHCHECK_SERVER_NAME", ""), "TLS certificate server name")
	caFile := flag.String("ca-file", environmentDefault("PROCTOR_HEALTHCHECK_CA_FILE", ""), "optional PEM certificate authority file")
	flag.Parse()

	if err := checkReadiness(*url, *timeout, *serverName, *caFile); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func environmentDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func checkReadiness(url string, timeout time.Duration, serverName, caFile string) error {
	if timeout <= 0 {
		return errors.New("readiness timeout must be positive")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.TLSClientConfig = &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: strings.TrimSpace(serverName),
	}
	if strings.TrimSpace(caFile) != "" {
		roots, err := certificatePool(caFile)
		if err != nil {
			return err
		}
		transport.TLSClientConfig.RootCAs = roots
	}
	client := &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("request readiness: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("readiness returned %s", response.Status)
	}
	return nil
}

func certificatePool(caFile string) (*x509.CertPool, error) {
	contents, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read healthcheck certificate authority: %w", err)
	}
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	if !roots.AppendCertsFromPEM(contents) {
		return nil, errors.New("healthcheck certificate authority contains no certificates")
	}
	return roots, nil
}
