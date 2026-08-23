// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"strings"
	"testing"
)

func TestMetricsExposureRequiresTLSAndAuthenticationBeyondLoopback(t *testing.T) {
	t.Parallel()

	cfg := Default()
	cfg.Metrics.Enabled = true
	cfg.Metrics.ListenAddress = "0.0.0.0:8067"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "non-loopback metrics require TLS") {
		t.Fatalf("validation error = %v", err)
	}
	cfg.Metrics.TLS.CertificateFile = "metrics.crt"
	cfg.Metrics.TLS.PrivateKeyFile = "metrics.key"
	cfg.Metrics.BearerToken = strings.Repeat("s", 32)
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate protected non-loopback metrics: %v", err)
	}
}

func TestMetricsBearerTokenIsRedacted(t *testing.T) {
	t.Parallel()

	cfg := Default()
	cfg.Metrics.BearerToken = strings.Repeat("s", 32)
	redacted := cfg.Redacted()
	if redacted.Metrics.BearerToken != "[redacted]" {
		t.Fatalf("redacted token = %q", redacted.Metrics.BearerToken)
	}
	if cfg.Metrics.BearerToken == "[redacted]" {
		t.Fatal("redaction mutated the original configuration")
	}
}

func TestMetricsListenerCannotCollideWithHTTPForwarding(t *testing.T) {
	t.Parallel()

	cfg := Default()
	cfg.Authentication.Bootstrap.DevelopmentMode = false
	cfg.Server.PublicURL = "https://localhost:8065"
	cfg.Server.TLS.Mode = ServerTLSModeStatic
	cfg.Server.TLS.CertificateFile = "server.crt"
	cfg.Server.TLS.PrivateKeyFile = "server.key"
	cfg.Server.TLS.ForwardHTTPToHTTPS = true
	cfg.Metrics.Enabled = true
	cfg.Metrics.ListenAddress = cfg.Server.TLS.HTTPListenAddress
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "server.tls.http_listen_address") {
		t.Fatalf("validation error = %v", err)
	}
}
