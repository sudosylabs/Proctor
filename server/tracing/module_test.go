// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package tracing

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/sudosylabs/proctor/server/config"
)

const parentTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"

func recordingModule(t *testing.T, ratio float64) (*Module, *tracetest.SpanRecorder) {
	t.Helper()
	settings := config.Default().Tracing
	settings.Enabled = true
	settings.SampleRatio = ratio
	recorder := tracetest.NewSpanRecorder()
	module := newModule(settings, Identity{Version: "test", NodeID: "node-a"}, recorder)
	t.Cleanup(func() { require.NoError(t, module.Close()) })
	return module, recorder
}

func parentHeader() http.Header {
	header := http.Header{}
	header.Set("traceparent", "00-"+parentTraceID+"-00f067aa0ba902b7-01")
	header.Set("baggage", "student=secret")
	return header
}

func attributes(span sdktrace.ReadOnlySpan) map[attribute.Key]attribute.Value {
	values := map[attribute.Key]attribute.Value{}
	for _, value := range span.Attributes() {
		values[value.Key] = value.Value
	}
	return values
}

func TestHTTPRequestSpanIsNamedByRouteTemplateAndBoundedAttributes(t *testing.T) {
	t.Parallel()

	module, recorder := recordingModule(t, 1)
	_, span := module.StartHTTPRequest(context.Background(), http.Header{}, http.MethodGet, false, "request-1")
	span.SetRoute("/api/v1/users/{user_id}")
	span.End(http.StatusServiceUnavailable)
	span.End(http.StatusOK)

	ended := recorder.Ended()
	require.Len(t, ended, 1, "End is idempotent")
	require.Equal(t, "GET /api/v1/users/{user_id}", ended[0].Name())
	values := attributes(ended[0])
	require.Equal(t, map[attribute.Key]attribute.Value{
		"http.request.method":       attribute.StringValue(http.MethodGet),
		"http.route":                attribute.StringValue("/api/v1/users/{user_id}"),
		"http.response.status_code": attribute.IntValue(http.StatusServiceUnavailable),
		"proctor.request_id":        attribute.StringValue("request-1"),
	}, values)
	require.Equal(t, codes.Error, ended[0].Status().Code)

	resource := map[attribute.Key]string{}
	for _, value := range ended[0].Resource().Attributes() {
		resource[value.Key] = value.Value.Emit()
	}
	require.Equal(t, map[attribute.Key]string{
		"service.name": "proctor", "service.version": "test", "service.instance.id": "node-a",
	}, resource)
}

func TestHTTPRequestContinuesTraceContextOnlyFromTrustedPeers(t *testing.T) {
	t.Parallel()

	t.Run("trusted proxy", func(t *testing.T) {
		t.Parallel()
		module, recorder := recordingModule(t, 0)
		_, span := module.StartHTTPRequest(context.Background(), parentHeader(), http.MethodGet, true, "")
		span.End(http.StatusOK)
		ended := recorder.Ended()
		require.Len(t, ended, 1, "a sampled trusted parent is recorded even at ratio 0")
		require.Equal(t, parentTraceID, ended[0].SpanContext().TraceID().String())
		require.Equal(t, "00f067aa0ba902b7", ended[0].Parent().SpanID().String())
	})
	t.Run("untrusted client", func(t *testing.T) {
		t.Parallel()
		module, recorder := recordingModule(t, 1)
		_, span := module.StartHTTPRequest(context.Background(), parentHeader(), http.MethodGet, false, "")
		span.End(http.StatusOK)
		ended := recorder.Ended()
		require.Len(t, ended, 1)
		require.NotEqual(t, parentTraceID, ended[0].SpanContext().TraceID().String())
		require.False(t, ended[0].Parent().IsValid(), "untrusted request continued a caller-chosen trace")
	})
	t.Run("untrusted client cannot force sampling", func(t *testing.T) {
		t.Parallel()
		module, recorder := recordingModule(t, 0)
		_, span := module.StartHTTPRequest(context.Background(), parentHeader(), http.MethodGet, false, "")
		span.End(http.StatusOK)
		require.Empty(t, recorder.Ended())
	})
}

func TestStoreOperationSpansOnlyJoinARecordingTrace(t *testing.T) {
	t.Parallel()

	module, recorder := recordingModule(t, 1)
	_, finish := module.StartStoreOperation(context.Background(), "job.claim_next")
	finish(false)
	require.Empty(t, recorder.Ended(), "a store operation without a parent started a root trace")

	ctx, request := module.StartHTTPRequest(context.Background(), http.Header{}, http.MethodPost, false, "")
	storeCtx, finish := module.StartStoreOperation(ctx, "user.get_by_email")
	traceID, spanID, ok := Correlation(storeCtx)
	require.True(t, ok)
	finish(true)
	request.End(http.StatusOK)

	ended := recorder.Ended()
	require.Len(t, ended, 2)
	store := ended[0]
	require.Equal(t, "store user.get_by_email", store.Name())
	require.Equal(t, traceID, store.SpanContext().TraceID().String())
	require.Equal(t, spanID, store.SpanContext().SpanID().String())
	require.Equal(t, ended[1].SpanContext().SpanID(), store.Parent().SpanID())
	require.Equal(t, map[attribute.Key]attribute.Value{
		"db.system.name":    attribute.StringValue("postgresql"),
		"db.operation.name": attribute.StringValue("user.get_by_email"),
	}, attributes(store))
	require.Equal(t, sdktrace.Status{Code: codes.Error, Description: "store operation failed"}, store.Status())
}

func TestJobExecutionSpanIsANewRootWithItsOutcome(t *testing.T) {
	t.Parallel()

	module, recorder := recordingModule(t, 1)
	parentCtx, request := module.StartHTTPRequest(context.Background(), http.Header{}, http.MethodGet, false, "")
	_, finish := module.StartJobExecution(parentCtx, "mail_delivery")
	finish("retryable_failure", true)
	request.End(http.StatusOK)

	ended := recorder.Ended()
	require.Len(t, ended, 2)
	job := ended[0]
	require.Equal(t, "job mail_delivery", job.Name())
	require.False(t, job.Parent().IsValid(), "Job execution joined the caller's trace")
	require.Equal(t, map[attribute.Key]attribute.Value{
		"proctor.job.type":    attribute.StringValue("mail_delivery"),
		"proctor.job.outcome": attribute.StringValue("retryable_failure"),
	}, attributes(job))
	require.Equal(t, codes.Error, job.Status().Code)
}

func TestDisabledModuleStartsNothing(t *testing.T) {
	t.Parallel()

	module, err := New(config.Default().Tracing, Identity{}, nil)
	require.NoError(t, err)
	require.False(t, module.Enabled())
	ctx, span := module.StartHTTPRequest(context.Background(), parentHeader(), http.MethodGet, true, "")
	span.SetRoute("/health/live")
	span.End(http.StatusOK)
	_, _, ok := Correlation(ctx)
	require.False(t, ok, "disabled tracing produced a correlatable span")
	require.NoError(t, module.Close())
	require.NoError(t, module.Close())
}

func TestEnabledModuleConstructsAnExporterWithoutConnecting(t *testing.T) {
	t.Parallel()

	settings := config.Default().Tracing
	settings.Enabled = true
	settings.EndpointURL = "http://127.0.0.1:1/v1/traces"
	module, err := New(settings, Identity{Version: "test", NodeID: "node-a"}, nil)
	require.NoError(t, err)
	require.True(t, module.Enabled())
	require.NoError(t, module.Close(), "closing with no buffered spans needs no collector")
	require.NoError(t, module.Close())
}
