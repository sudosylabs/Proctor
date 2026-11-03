// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package server_test

import (
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/testlib"
)

// otlpReceiver is a minimal OTLP/HTTP traces endpoint that keeps every span.
type otlpReceiver struct {
	mu    sync.Mutex
	spans []*tracepb.Span
}

func (r *otlpReceiver) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	body, err := io.ReadAll(request.Body)
	if err != nil || request.URL.Path != "/v1/traces" {
		http.Error(writer, "unexpected export", http.StatusBadRequest)
		return
	}
	var export coltracepb.ExportTraceServiceRequest
	if err := proto.Unmarshal(body, &export); err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	r.mu.Lock()
	for _, resource := range export.GetResourceSpans() {
		for _, scope := range resource.GetScopeSpans() {
			r.spans = append(r.spans, scope.GetSpans()...)
		}
	}
	r.mu.Unlock()
	response, _ := proto.Marshal(&coltracepb.ExportTraceServiceResponse{})
	writer.Header().Set("Content-Type", "application/x-protobuf")
	_, _ = writer.Write(response)
}

func TestTracingExportsRouteTemplatedSpansAndCorrelatesLogs(t *testing.T) {
	t.Parallel()

	receiver := &otlpReceiver{}
	collector := httptest.NewServer(receiver)
	t.Cleanup(collector.Close)
	helper := testlib.Setup(t, testlib.WithConfig(func(cfg *config.Config) {
		cfg.Tracing.Enabled = true
		cfg.Tracing.EndpointURL = collector.URL + "/v1/traces"
	}))

	const clientTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	userID := model.NewUserID().String()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/api/v1/users/"+userID+"/affiliations?access_token=secret", nil)
	request.Header.Set("traceparent", "00-"+clientTraceID+"-00f067aa0ba902b7-01")
	request.Header.Set("Authorization", "Bearer credential-must-not-be-traced")
	response := httptest.NewRecorder()
	helper.Handler().ServeHTTP(response, request)
	requestID := response.Header().Get("X-Request-ID")
	require.NotEmpty(t, requestID)

	require.NoError(t, helper.Server.Close(), "close flushes buffered spans")

	receiver.mu.Lock()
	spans := append([]*tracepb.Span(nil), receiver.spans...)
	receiver.mu.Unlock()
	var requestSpan *tracepb.Span
	for _, span := range spans {
		if span.GetKind() == tracepb.Span_SPAN_KIND_SERVER {
			requestSpan = span
		}
	}
	require.NotNil(t, requestSpan, "no server span exported among %d spans", len(spans))
	require.Equal(t, "GET /api/v1/users/{user_id}/affiliations", requestSpan.GetName())
	traceID := hex.EncodeToString(requestSpan.GetTraceId())
	require.NotEqual(t, clientTraceID, traceID, "an untrusted client chose the trace")
	encoded, err := proto.Marshal(requestSpan)
	require.NoError(t, err)
	for _, forbidden := range []string{userID, "secret", "credential", "access_token"} {
		require.NotContains(t, string(encoded), forbidden, "span leaked request content")
	}

	var requestLog map[string]any
	for _, line := range strings.Split(strings.TrimSpace(helper.Logs.String()), "\n") {
		var record map[string]any
		if json.Unmarshal([]byte(line), &record) == nil && record["msg"] == "http request" && record["request_id"] == requestID {
			requestLog = record
		}
	}
	require.NotNil(t, requestLog, "request log record not found")
	require.Equal(t, traceID, requestLog["trace_id"], "request log is correlated with its span")
}
