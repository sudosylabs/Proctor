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

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type nativeDeliveryHTTPFake struct {
	*examAttemptHTTPFake
	query application.NativeDeliveryQuery
	calls int
}

func (f *nativeDeliveryHTTPFake) NativeDeliveryStatus(_ context.Context, _ application.Invocation, q application.NativeDeliveryQuery) (*model.NativeSecurityStreamStatus, error) {
	f.query = q
	f.calls++
	return &model.NativeSecurityStreamStatus{NativeDeliveryProgress: model.NativeDeliveryProgress{MissingBatchRanges: []model.SequenceRange{}, ServerTime: time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)}, StreamID: q.StreamID, DetailMode: "collecting"}, nil
}
func (f *nativeDeliveryHTTPFake) UpdateNativeDeliverySummary(context.Context, application.Invocation, application.NativeDeliverySummaryCommand) (*model.NativeSecurityStreamStatus, error) {
	f.calls++
	return nil, application.NewError("exam.delivery.summary_rate_limited")
}

func TestNativeDeliveryHTTPPreservesOptionalHistoricalFences(t *testing.T) {
	f := &nativeDeliveryHTTPFake{examAttemptHTTPFake: newExamAttemptHTTPFake(t)}
	logger, _ := newTestLogger(t)
	api := newFocusedResourceAPI(t, logger, f, nativeDeliveryResource(f))
	path := "/api/v1/exam-attempts/" + f.attempt.ID.String() + "/security-streams/" + model.NewId()
	for _, live := range []bool{true, false} {
		request := f.candidateRequest(http.MethodGet, path)
		if !live {
			request.Header.Del(candidateAttemptCredentialHeader)
			request.Header.Del(candidateAttemptConnectionHeader)
		}
		response := httptest.NewRecorder()
		api.ServeHTTP(response, request)
		if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("response: %d %s", response.Code, response.Body.String())
		}
		if live && (f.query.Access.ContinuityCredential != f.credential || f.query.Access.ConnectionID != f.connection.ID) {
			t.Fatal("live fences not forwarded")
		}
		if !live && (f.query.Access.ContinuityCredential != "" || f.query.Access.ConnectionID != "") {
			t.Fatal("historical request invented live fences")
		}
		if strings.Contains(response.Body.String(), f.credential) {
			t.Fatal("response exposed continuity credential")
		}
	}
	before := f.calls
	request := f.candidateRequest(http.MethodGet, path)
	request.Header.Add(candidateAttemptCredentialHeader, f.credential)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != 400 || f.calls != before {
		t.Fatalf("duplicate fence reached application: %d", response.Code)
	}
}

func TestNativeDeliverySummaryStrictBodyAndRetryDelay(t *testing.T) {
	f := &nativeDeliveryHTTPFake{examAttemptHTTPFake: newExamAttemptHTTPFake(t)}
	logger, _ := newTestLogger(t)
	api := newFocusedResourceAPI(t, logger, f, nativeDeliveryResource(f))
	path := "/api/v1/exam-attempts/" + f.attempt.ID.String() + "/security-streams/" + model.NewId() + "/summary"
	for _, body := range []string{
		`{"summary_sequence":1,"unretained_record_count":0,"count_complete":false,"first_unretained_at":null,"last_unretained_at":null,"raw_content":"forbidden"}`,
		`{"summary_sequence":1,"unretained_record_count":0,"count_complete":false}`,
	} {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer credential")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", "summary-once")
		response := httptest.NewRecorder()
		api.ServeHTTP(response, request)
		if response.Code != 400 || f.calls != 0 {
			t.Fatalf("invalid body reached application: %d %s", response.Code, response.Body.String())
		}
	}
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"summary_sequence":1,"unretained_record_count":0,"count_complete":false,"first_unretained_at":null,"last_unretained_at":null}`))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "summary-once")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != 429 || response.Header().Get("Retry-After") != "1" || f.calls != 1 {
		t.Fatalf("summary rate response: %d %s", response.Code, response.Body.String())
	}
}

func (f *nativeDeliveryHTTPFake) AppendNativeDelivery(_ context.Context, _ application.Invocation, c application.NativeDeliveryAppendCommand) (*model.NativeSecurityAcknowledgement, error) {
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
	batch := model.NativeSecurityBatch{StreamID: model.NewId(), BatchSequence: 1, ParticipationID: model.NewAttemptParticipationID(), Generation: 1, SecuritySessionID: model.NewId(), PolicyDigest: model.SHA256Fingerprint([]byte("synthetic-policy")), ApplicationReleaseID: "synthetic-release", MatrixID: "synthetic-matrix", Records: []model.NativeRecord{{Gap: &model.NativeSourceGap{Kind: "source_gap", SourceID: model.NativeSourceCapture, SourceInstanceID: model.NewId(), Reason: "source_loss", OccurredAt: time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)}}}}
	raw, err := json.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}
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
			if response.Code != 200 || f.calls != before+1 || f.query.StreamID != batch.StreamID || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("append: %d %s", response.Code, response.Body.String())
			}
		} else if response.Code != 400 || f.calls != before {
			t.Fatalf("invalid append reached application: %d %s", response.Code, response.Body.String())
		}
	}
}
