// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMetricsExposureRequiresTLSAndAuthenticationBeyondLoopback(t *testing.T) {
	t.Parallel()

	cfg := Default()
	cfg.Metrics.Enabled = true
	cfg.Metrics.ListenAddress = "0.0.0.0:8067"
	err := cfg.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "non-loopback metrics require TLS")
	cfg.Metrics.TLS.CertificateFile = "metrics.crt"
	cfg.Metrics.TLS.PrivateKeyFile = "metrics.key"
	cfg.Metrics.BearerToken = strings.Repeat("s", 32)
	require.NoError(t, cfg.Validate())
}

func TestMetricsBearerTokenIsRedacted(t *testing.T) {
	t.Parallel()

	cfg := Default()
	cfg.Metrics.BearerToken = strings.Repeat("s", 32)
	redacted := cfg.Redacted()
	require.Equal(t, "[redacted]", redacted.Metrics.BearerToken)
	require.NotEqual(t, "[redacted]", cfg.Metrics.BearerToken, "redaction mutated the original configuration")
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
	err := cfg.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "server.tls.http_listen_address")
}

func TestMetricsProfilingRequiresTheAuthenticatedMetricsListener(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		enabled bool
		token   string
		message string
	}{
		{name: "metrics disabled", enabled: false, token: strings.Repeat("s", 32), message: "metrics.profiling_enabled: requires metrics.enabled"},
		{name: "no bearer token", enabled: true, token: "", message: "metrics.profiling_enabled: requires metrics.bearer_token"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			cfg := Default()
			cfg.Metrics.Enabled = test.enabled
			cfg.Metrics.BearerToken = test.token
			cfg.Metrics.ProfilingEnabled = true
			err := cfg.Validate()
			require.Error(t, err)
			require.Contains(t, err.Error(), test.message)
		})
	}

	cfg := Default()
	cfg.Metrics.Enabled = true
	cfg.Metrics.BearerToken = strings.Repeat("s", 32)
	cfg.Metrics.ProfilingEnabled = true
	require.NoError(t, cfg.Validate())
}
