// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package config

import (
	"math"
	"net/url"
	"strings"
	"time"
)

// Tracing configures opt-in OpenTelemetry tracing exported over OTLP/HTTP.
// Spans carry route templates, closed operation names, and bounded outcomes;
// never credentials, User data, exam content, or raw request paths.
type Tracing struct {
	Enabled bool `json:"Enabled"`
	// EndpointURL is the complete OTLP/HTTP traces URL, normally a local
	// collector's http://host:4318/v1/traces.
	EndpointURL string `json:"EndpointURL"`
	// SampleRatio is the fraction of new root traces recorded, from 0 to 1.
	// A trace continued from a trusted proxy follows the proxy's decision.
	SampleRatio     float64  `json:"SampleRatio"`
	ExportTimeout   Duration `json:"ExportTimeout"`
	ShutdownTimeout Duration `json:"ShutdownTimeout"`
}

func validateTracing(tracing Tracing, add func(string, string)) {
	parsed, err := url.Parse(tracing.EndpointURL)
	switch {
	case err != nil || strings.ContainsAny(tracing.EndpointURL, "\x00\r\n"):
		add("tracing.endpoint_url", "must be an absolute http or https URL")
	case parsed.Scheme != "http" && parsed.Scheme != "https", parsed.Host == "", parsed.Opaque != "":
		add("tracing.endpoint_url", "must be an absolute http or https URL")
	case parsed.User != nil:
		add("tracing.endpoint_url", "must not contain credentials")
	case parsed.RawQuery != "" || parsed.Fragment != "":
		add("tracing.endpoint_url", "must not contain a query or fragment")
	}
	if math.IsNaN(tracing.SampleRatio) || tracing.SampleRatio < 0 || tracing.SampleRatio > 1 {
		add("tracing.sample_ratio", "must be between 0 and 1")
	}
	for _, timeout := range []struct {
		field string
		value time.Duration
	}{
		{"tracing.export_timeout", tracing.ExportTimeout.Duration},
		{"tracing.shutdown_timeout", tracing.ShutdownTimeout.Duration},
	} {
		if timeout.value <= 0 || timeout.value > time.Minute {
			add(timeout.field, "must be positive and no greater than one minute")
		}
	}
}
