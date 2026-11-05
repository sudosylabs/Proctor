// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package config

import (
	"errors"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/http/httpguts"
)

const (
	// maximumTracingHeaders bounds the exporter headers an operator may set.
	maximumTracingHeaders = 16
	// maximumTracingHeaderNameBytes and maximumTracingHeaderValueBytes bound
	// one exporter header so a malformed secret cannot inflate every export.
	maximumTracingHeaderNameBytes  = 256
	maximumTracingHeaderValueBytes = 8 << 10
)

// reservedTracingHeaders are set by the HTTP client or the OTLP exporter
// itself; overriding them would break framing or encoding of every export.
var reservedTracingHeaders = map[string]struct{}{
	"Connection":        {},
	"Content-Encoding":  {},
	"Content-Length":    {},
	"Content-Type":      {},
	"Host":              {},
	"Keep-Alive":        {},
	"Proxy-Connection":  {},
	"Te":                {},
	"Trailer":           {},
	"Transfer-Encoding": {},
	"Upgrade":           {},
}

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
	SampleRatio float64 `json:"SampleRatio"`
	// Headers are sent with every export request, typically a hosted
	// backend's API key. Values are secrets: they are redacted from every
	// configuration display and never logged. Headers require an https
	// endpoint unless the collector is on loopback.
	Headers         map[string]string `json:"Headers"`
	ExportTimeout   Duration          `json:"ExportTimeout"`
	ShutdownTimeout Duration          `json:"ShutdownTimeout"`
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
	validateTracingHeaders(tracing.Headers, err == nil && parsed.Scheme == "http" && !isLoopbackHost(parsed.Hostname()), add)
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

// validateTracingHeaders checks exporter header names and values. Messages
// never repeat a name or value because values are credentials.
func validateTracingHeaders(headers map[string]string, plaintextRemoteEndpoint bool, add func(string, string)) {
	if len(headers) == 0 {
		return
	}
	if len(headers) > maximumTracingHeaders {
		add("tracing.headers", "must contain at most 16 headers")
	}
	if plaintextRemoteEndpoint {
		add("tracing.headers", "require an https endpoint_url unless the collector is on loopback")
	}
	canonical := make(map[string]struct{}, len(headers))
	for name, value := range headers {
		switch {
		case len(name) > maximumTracingHeaderNameBytes || !httpguts.ValidHeaderFieldName(name):
			add("tracing.headers", "names must be HTTP header tokens of at most 256 bytes")
			continue
		case value == "" || len(value) > maximumTracingHeaderValueBytes ||
			!httpguts.ValidHeaderFieldValue(value) || strings.TrimSpace(value) != value:
			add("tracing.headers", "values must be non-empty HTTP header values without surrounding whitespace, at most 8192 bytes")
		}
		key := http.CanonicalHeaderKey(name)
		if _, reserved := reservedTracingHeaders[key]; reserved {
			add("tracing.headers", "must not set transport-controlled headers such as Content-Type or Host")
		}
		if _, duplicate := canonical[key]; duplicate {
			add("tracing.headers", "names must be unique ignoring case")
		}
		canonical[key] = struct{}{}
	}
}

// errMalformedTracingHeaders is deliberately value-free: the rejected input
// is a credential and must not reach a log or terminal.
var errMalformedTracingHeaders = errors.New(
	"must be comma-separated name=value pairs with percent-encoded values",
)

// parseTracingHeaders reads the OTEL_EXPORTER_OTLP_HEADERS list format:
// comma-separated name=value pairs whose values are percent-decoded, with
// surrounding whitespace trimmed. An empty input clears every header.
func parseTracingHeaders(input string) (map[string]string, error) {
	headers := map[string]string{}
	if strings.TrimSpace(input) == "" {
		return headers, nil
	}
	for _, pair := range strings.Split(input, ",") {
		name, encoded, found := strings.Cut(pair, "=")
		if !found {
			return nil, errMalformedTracingHeaders
		}
		value, err := url.PathUnescape(encoded)
		if err != nil {
			return nil, errMalformedTracingHeaders
		}
		name = strings.TrimSpace(name)
		if _, duplicate := headers[name]; duplicate || name == "" {
			return nil, errMalformedTracingHeaders
		}
		headers[name] = strings.TrimSpace(value)
	}
	return headers, nil
}
