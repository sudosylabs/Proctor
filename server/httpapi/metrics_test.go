// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		_, err := io.ReadFull(request.Body, buffer)
		assert.NoError(t, err)
		_, _ = writer.Write([]byte("ok"))
	}), metrics, "/example", http.MethodPost)
	request := httptest.NewRequest(http.MethodPost, "/example", strings.NewReader("payload"))
	request.ContentLength = -1
	request.TransferEncoding = []string{"chunked"}
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	require.Equal(t, 1, metrics.started)
	require.Equal(t, 1, metrics.finished)
	require.Equal(t, http.StatusOK, metrics.status)
	require.Equal(t, int64(3), metrics.requestBytes)
	require.Equal(t, int64(2), metrics.responseBytes)
}
