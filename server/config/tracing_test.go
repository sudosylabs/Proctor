// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package config

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTracingIsDisabledByDefaultWithALocalCollectorEndpoint(t *testing.T) {
	t.Parallel()

	tracing := Default().Tracing
	require.False(t, tracing.Enabled)
	require.Equal(t, "http://127.0.0.1:4318/v1/traces", tracing.EndpointURL)
	require.InDelta(t, 1.0, tracing.SampleRatio, 0)
	require.NotNil(t, tracing.Headers, "the canonical example renders an empty Headers object")
	require.Empty(t, tracing.Headers)
}

func TestTracingValidation(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		update func(*Tracing)
		field  string
	}{
		{name: "relative endpoint", update: func(tracing *Tracing) { tracing.EndpointURL = "/v1/traces" }, field: "tracing.endpoint_url"},
		{name: "unsupported scheme", update: func(tracing *Tracing) { tracing.EndpointURL = "grpc://collector:4317" }, field: "tracing.endpoint_url"},
		{name: "credentials in endpoint", update: func(tracing *Tracing) { tracing.EndpointURL = "https://user:secret@collector/v1/traces" }, field: "tracing.endpoint_url"},
		{name: "query in endpoint", update: func(tracing *Tracing) { tracing.EndpointURL = "https://collector/v1/traces?token=secret" }, field: "tracing.endpoint_url"},
		{name: "negative ratio", update: func(tracing *Tracing) { tracing.SampleRatio = -0.1 }, field: "tracing.sample_ratio"},
		{name: "ratio above one", update: func(tracing *Tracing) { tracing.SampleRatio = 1.5 }, field: "tracing.sample_ratio"},
		{name: "NaN ratio", update: func(tracing *Tracing) { tracing.SampleRatio = math.NaN() }, field: "tracing.sample_ratio"},
		{name: "zero export timeout", update: func(tracing *Tracing) { tracing.ExportTimeout = Duration{} }, field: "tracing.export_timeout"},
		{name: "long shutdown timeout", update: func(tracing *Tracing) { tracing.ShutdownTimeout = Duration{Duration: 2 * time.Minute} }, field: "tracing.shutdown_timeout"},
		{name: "headers over plaintext to a remote collector", update: func(tracing *Tracing) {
			tracing.EndpointURL = "http://collector.internal:4318/v1/traces"
			tracing.Headers = map[string]string{"X-Api-Key": "secret"}
		}, field: "tracing.headers"},
		{name: "header name is not a token", update: func(tracing *Tracing) { tracing.Headers = map[string]string{"X Api Key": "secret"} }, field: "tracing.headers"},
		{name: "empty header value", update: func(tracing *Tracing) { tracing.Headers = map[string]string{"X-Api-Key": ""} }, field: "tracing.headers"},
		{name: "header value with a line break", update: func(tracing *Tracing) { tracing.Headers = map[string]string{"X-Api-Key": "a\r\nInjected: b"} }, field: "tracing.headers"},
		{name: "header value with surrounding whitespace", update: func(tracing *Tracing) { tracing.Headers = map[string]string{"X-Api-Key": " secret"} }, field: "tracing.headers"},
		{name: "oversized header value", update: func(tracing *Tracing) { tracing.Headers = map[string]string{"X-Api-Key": strings.Repeat("s", 8193)} }, field: "tracing.headers"},
		{name: "transport-controlled header", update: func(tracing *Tracing) { tracing.Headers = map[string]string{"content-type": "text/plain"} }, field: "tracing.headers"},
		{name: "duplicate header ignoring case", update: func(tracing *Tracing) {
			tracing.Headers = map[string]string{"X-Api-Key": "one", "x-api-key": "two"}
		}, field: "tracing.headers"},
		{name: "too many headers", update: func(tracing *Tracing) {
			tracing.Headers = map[string]string{}
			for index := range 17 {
				tracing.Headers[fmt.Sprintf("X-Header-%d", index)] = "value"
			}
		}, field: "tracing.headers"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			cfg := Default()
			cfg.Tracing.Enabled = true
			test.update(&cfg.Tracing)
			err := cfg.Validate()
			require.Error(t, err)
			require.Contains(t, err.Error(), test.field)
		})
	}

	cfg := Default()
	cfg.Tracing.Enabled = true
	cfg.Tracing.EndpointURL = "https://collector.internal:4318/v1/traces"
	cfg.Tracing.SampleRatio = 0
	cfg.Tracing.Headers = map[string]string{"x-honeycomb-team": "team-key", "Authorization": "Basic dXNlcjprZXk="}
	require.NoError(t, cfg.Validate())

	cfg.Tracing.EndpointURL = "http://localhost:4318/v1/traces"
	require.NoError(t, cfg.Validate(), "loopback collectors may receive headers over plaintext")
}

func TestTracingHeaderValidationNeverEchoesHeaderContent(t *testing.T) {
	t.Parallel()

	cfg := Default()
	cfg.Tracing.EndpointURL = "http://collector.internal:4318/v1/traces"
	cfg.Tracing.Headers = map[string]string{"X-Secret-Name": " secret-header-value\n"}
	err := cfg.Validate()
	require.Error(t, err)
	require.NotContains(t, err.Error(), "secret-header-value")
	require.NotContains(t, err.Error(), "X-Secret-Name")
}

func TestTracingHeadersAreClonedAndRedacted(t *testing.T) {
	t.Parallel()

	cfg := Default()
	cfg.Tracing.Headers = map[string]string{"x-honeycomb-team": "honeycomb-api-key-value"}
	cloned := cfg.Clone()
	cloned.Tracing.Headers["x-honeycomb-team"] = "changed"
	require.Equal(t, "honeycomb-api-key-value", cfg.Tracing.Headers["x-honeycomb-team"], "Clone shares the header map")

	redacted := cfg.Redacted()
	require.Equal(t, map[string]string{"x-honeycomb-team": "[redacted]"}, redacted.Tracing.Headers)
	require.Equal(t, "honeycomb-api-key-value", cfg.Tracing.Headers["x-honeycomb-team"], "redaction mutated the original configuration")
	document, err := cfg.RedactedJSON()
	require.NoError(t, err)
	require.NotContains(t, string(document), "honeycomb-api-key-value")
	require.Contains(t, string(document), "x-honeycomb-team")
}

func TestTracingHeadersEnvironmentOverride(t *testing.T) {
	t.Parallel()

	lookup := func(value string) LookupEnv {
		return func(key string) (string, bool) { return value, key == "PROCTOR_TRACING_HEADERS" }
	}

	persisted := Default()
	persisted.Tracing.Headers = map[string]string{"X-Persisted": "persisted"}
	cfg := persisted.Clone()
	applied, err := applyEnvironment(&cfg, lookup(" x-honeycomb-team = team%2Ckey , Authorization=Basic%20dXNlcjprZXk%3D"))
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"x-honeycomb-team": "team,key",
		"Authorization":    "Basic dXNlcjprZXk=",
	}, cfg.Tracing.Headers, "the override replaces rather than merges the persisted headers")
	require.NoError(t, cfg.Validate())
	applied.restore(&cfg, persisted)
	require.Equal(t, persisted.Tracing.Headers, cfg.Tracing.Headers)

	cleared := persisted.Clone()
	_, err = applyEnvironment(&cleared, lookup(""))
	require.NoError(t, err)
	require.Empty(t, cleared.Tracing.Headers, "an empty value clears the headers")

	for _, malformed := range []string{"api-key-secret-without-separator", "X-Api-Key=bad%zzsecret", "=orphan-secret", "X-A=1,X-A=2-secret"} {
		candidate := persisted.Clone()
		_, err := applyEnvironment(&candidate, lookup(malformed))
		require.Error(t, err, malformed)
		require.Contains(t, err.Error(), "PROCTOR_TRACING_HEADERS")
		require.NotContains(t, err.Error(), "secret", "the parse error echoed the header value")
	}
}
