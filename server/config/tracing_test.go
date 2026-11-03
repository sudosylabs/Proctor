// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package config

import (
	"math"
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
	require.NoError(t, cfg.Validate())
}
