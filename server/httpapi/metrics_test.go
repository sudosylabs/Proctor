// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type requestMetricsFake struct {
	started, finished           int
	status                      int
	requestBytes, responseBytes int64
}

func (m *requestMetricsFake) HTTPStarted() func() {
	m.started++
	return func() { m.finished++ }
}

func (m *requestMetricsFake) ObserveHTTPRequest(_, _ string, status int, _ time.Duration) {
	m.status = status
}

func (m *requestMetricsFake) ObserveHTTPPayload(_, _ string, requestBytes, responseBytes int64) {
	m.requestBytes, m.responseBytes = requestBytes, responseBytes
}

func TestRequestMetricsCountConsumedBodyBytes(t *testing.T) {
	t.Parallel()

	metrics := &requestMetricsFake{}
	handler := observeRequestMetrics(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		buffer := make([]byte, 3)
		if _, err := io.ReadFull(request.Body, buffer); err != nil {
			t.Fatal(err)
		}
		_, _ = writer.Write([]byte("ok"))
	}), metrics, "/example", http.MethodPost)
	request := httptest.NewRequest(http.MethodPost, "/example", strings.NewReader("payload"))
	request.ContentLength = -1
	request.TransferEncoding = []string{"chunked"}
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if metrics.started != 1 || metrics.finished != 1 || metrics.status != http.StatusOK ||
		metrics.requestBytes != 3 || metrics.responseBytes != 2 {
		t.Fatalf("metrics = %#v", metrics)
	}
}
