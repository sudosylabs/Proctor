// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/exam/attempt"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
)

type nativeDeliveryHTTPFake struct {
	*examAttemptHTTPFake
	query attempt.NativeDeliveryQuery
	calls int
}

func (f *nativeDeliveryHTTPFake) NativeDeliveryStatus(_ context.Context, _ invocation.Invocation, q attempt.NativeDeliveryQuery) (*model.NativeSecurityStreamStatus, error) {
	f.query = q
	f.calls++
	return &model.NativeSecurityStreamStatus{NativeDeliveryProgress: model.NativeDeliveryProgress{MissingBatchRanges: []model.SequenceRange{}, ServerTime: time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)}, StreamID: q.StreamID, DetailMode: "collecting"}, nil
}
func (f *nativeDeliveryHTTPFake) UpdateNativeDeliverySummary(context.Context, invocation.Invocation, attempt.NativeDeliverySummaryCommand) (*model.NativeSecurityStreamStatus, error) {
	f.calls++
	return nil, apperror.New("exam.delivery.summary_rate_limited")
}

func TestNativeDeliveryHTTPPreservesOptionalHistoricalFences(t *testing.T) {
	f := &nativeDeliveryHTTPFake{examAttemptHTTPFake: newExamAttemptHTTPFake(t)}
	logger, _ := newTestLogger(t)
	api := newFocusedResourceAPI(t, logger, f, nativeDeliveryResource(f))
	path := "/api/v1/exam-attempts/" + f.attempt.ID.String() + "/security-streams/" + model.NewID()
	for _, live := range []bool{true, false} {
		request := f.candidateRequest(http.MethodGet, path)
		if !live {
			request.Header.Del(candidateAttemptCredentialHeader)
			request.Header.Del(candidateAttemptConnectionHeader)
		}
		response := httptest.NewRecorder()
		api.ServeHTTP(response, request)
		require.Equal(t, 200, response.Code, "response: %d %s", response.Code, response.Body.String())
		require.Equal(t, "no-store", response.Header().Get("Cache-Control"), "response: %d %s", response.Code, response.Body.String())
		if live {
			require.Equal(t, f.credential, f.query.Access.ContinuityCredential, "live fences not forwarded")
			require.Equal(t, f.connection.ID, f.query.Access.ConnectionID, "live fences not forwarded")
		}
		if !live {
			require.Empty(t, f.query.Access.ContinuityCredential, "historical request invented live fences")
			require.Empty(t, f.query.Access.ConnectionID, "historical request invented live fences")
		}
		require.NotContains(t, response.Body.String(), f.credential, "response exposed continuity credential")
	}
	before := f.calls
	request := f.candidateRequest(http.MethodGet, path)
	request.Header.Add(candidateAttemptCredentialHeader, f.credential)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	require.Equal(t, 400, response.Code, "duplicate fence reached application")
	require.Equal(t, before, f.calls, "duplicate fence reached application")
}

func TestNativeDeliverySummaryStrictBodyAndRetryDelay(t *testing.T) {
	f := &nativeDeliveryHTTPFake{examAttemptHTTPFake: newExamAttemptHTTPFake(t)}
	logger, _ := newTestLogger(t)
	api := newFocusedResourceAPI(t, logger, f, nativeDeliveryResource(f))
	path := "/api/v1/exam-attempts/" + f.attempt.ID.String() + "/security-streams/" + model.NewID() + "/summary"
	for _, body := range []string{
		`{"summary_sequence":1,"unretained_record_count":0,"count_complete":false,"raw_content":"forbidden"}`,
		`{"summary_sequence":1,"unretained_record_count":0,"count_complete":false,"first_unretained_at":null}`,
	} {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer credential")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", "summary-once")
		response := httptest.NewRecorder()
		api.ServeHTTP(response, request)
		require.Equal(t, 400, response.Code, "invalid body reached application: %d %s", response.Code, response.Body.String())
		require.Equal(t, 0, f.calls, "invalid body reached application: %d %s", response.Code, response.Body.String())
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(`{"summary_sequence":1,"unretained_record_count":0,"count_complete":false}`))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "summary-once")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	require.Equal(t, 429, response.Code, "summary rate response: %d %s", response.Code, response.Body.String())
	require.Equal(t, "1", response.Header().Get("Retry-After"), "summary rate response: %d %s", response.Code, response.Body.String())
	require.Equal(t, 1, f.calls, "summary rate response: %d %s", response.Code, response.Body.String())
}

func (f *nativeDeliveryHTTPFake) AppendNativeDelivery(_ context.Context, _ invocation.Invocation, c attempt.NativeDeliveryAppendCommand) (*model.NativeSecurityAcknowledgement, error) {
	f.query = c.Query
	f.calls++
	raw, err := c.Batch.Canonical()
	if err != nil {
		return nil, err
	}
	at := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	return &model.NativeSecurityAcknowledgement{Receipt: model.NativeBatchReceipt{StreamID: c.Batch.StreamID, BatchSequence: c.Batch.BatchSequence, RequestDigest: model.SHA256Fingerprint(raw), ReceivedAt: at}, NativeDeliveryProgress: model.NativeDeliveryProgress{HighestContiguousBatchSequence: 1, SettledThroughBatchSequence: 1, HighestSeenBatchSequence: 1, MissingBatchRanges: []model.SequenceRange{}, ServerTime: at}}, nil
}

func TestNativeDeliveryAppendClosedBodyAndBounds(t *testing.T) {
	f := &nativeDeliveryHTTPFake{examAttemptHTTPFake: newExamAttemptHTTPFake(t)}
	logger, _ := newTestLogger(t)
	api := newFocusedResourceAPI(t, logger, f, nativeDeliveryResource(f))
	path := "/api/v1/exam-attempts/" + f.attempt.ID.String() + "/security-batches"
	batch := model.NativeSecurityBatch{StreamID: model.NewID(), BatchSequence: 1, ParticipationID: model.NewAttemptParticipationID(), Generation: 1, SecuritySessionID: model.NewID(), PolicyDigest: model.SHA256Fingerprint([]byte("synthetic-policy")), ApplicationReleaseID: "synthetic-release", MatrixID: "synthetic-matrix", Records: []model.NativeRecord{{Gap: &model.NativeSourceGap{Kind: "source_gap", SourceID: model.NativeSourceCapture, SourceInstanceID: model.NewID(), Reason: "source_loss", OccurredAt: time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)}}}}
	raw, err := json.Marshal(batch)
	require.NoError(t, err)
	for _, body := range [][]byte{raw, bytes.Replace(raw, []byte(`"kind":"source_gap"`), []byte(`"raw_inventory":"forbidden","kind":"source_gap"`), 1), append(bytes.Repeat([]byte(" "), 256*1024), raw...)} {
		before := f.calls
		request := f.candidateRequest(http.MethodPost, path)
		request.Body = io.NopCloser(bytes.NewReader(body))
		request.ContentLength = int64(len(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", "native-append-http")
		response := httptest.NewRecorder()
		api.ServeHTTP(response, request)
		if bytes.Equal(body, raw) {
			require.Equal(t, 200, response.Code, "append: %d %s", response.Code, response.Body.String())
			require.Equal(t, before+1, f.calls, "append: %d %s", response.Code, response.Body.String())
			require.Equal(t, batch.StreamID, f.query.StreamID, "append: %d %s", response.Code, response.Body.String())
			require.Equal(t, "no-store", response.Header().Get("Cache-Control"), "append: %d %s", response.Code, response.Body.String())
			continue
		}
		want := http.StatusBadRequest
		if len(body) > 256*1024 {
			want = http.StatusRequestEntityTooLarge
		}
		require.Equal(t, want, response.Code, "invalid append reached application: %d %s", response.Code, response.Body.String())
		require.Equal(t, before, f.calls, "invalid append reached application: %d %s", response.Code, response.Body.String())
	}
}
