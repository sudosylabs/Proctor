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
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestExamSittingHTTPScheduleUsesStrictIdempotentCommandAndSafeResponse(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	fake := newExamSittingHTTPFake(t)
	httpAPI := newFocusedResourceAPI(t, logger, fake, examSittingResource(fake))
	body := `{"exam_revision_id":"` + fake.sitting.ExamRevisionID.String() + `","class_id":"` + fake.sitting.ClassID.String() + `","scheduled_start_at":"2026-08-15T12:30:00+02:00","scheduled_end_at":"2026-08-15T14:30:00+02:00"}`
	request := newJSONRequest(http.MethodPost, examSittingCollectionPath(fake.sitting.ExamID), strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Idempotency-Key", "schedule-once")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)

	require.Equal(t, http.StatusCreated, response.Code, "status = %d, body = %s", response.Code, response.Body.String())
	require.Equal(t, fake.sitting.ExamID, fake.schedule.ExamID)
	require.Equal(t, fake.sitting.ExamRevisionID, fake.schedule.ExamRevisionID)
	require.Equal(t, fake.sitting.ClassID, fake.schedule.ClassID)
	require.Equal(t, "schedule-once", fake.schedule.IdempotencyKey)
	require.Same(t, time.UTC, fake.schedule.ScheduledStartAt.Location())
	require.Equal(t, 10, fake.schedule.ScheduledStartAt.Hour())
	require.Same(t, time.UTC, fake.schedule.ScheduledEndAt.Location())
	require.Equal(t, 12, fake.schedule.ScheduledEndAt.Hour())
	var payload map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	wantKeys := []string{"id", "exam_id", "exam_revision_id", "class_id", "scheduled_start_at", "scheduled_end_at", "state", "created_at", "updated_at", "revision"}
	require.Len(t, payload, len(wantKeys), "response fields = %v, body = %s", payload, response.Body.String())
	for _, key := range wantKeys {
		_, ok := payload[key]
		assert.True(t, ok, "response omitted %q", key)
	}
	for _, forbidden := range []string{"private_reason", "actor_user_id", "manager_override", "audit_event_id", "content", "path"} {
		_, ok := payload[forbidden]
		assert.False(t, ok, "response exposed %q", forbidden)
	}
}

func TestExamSittingHTTPMutationBodiesAreClosedDuplicateFreeAndPresenceAware(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		body func(*examSittingHTTPFake) string
	}{
		{name: "unknown create field", body: func(fake *examSittingHTTPFake) string {
			return `{"exam_revision_id":"` + fake.sitting.ExamRevisionID.String() + `","class_id":"` + fake.sitting.ClassID.String() + `","scheduled_start_at":"2026-08-15T10:00:00Z","scheduled_end_at":"2026-08-15T11:00:00Z","extra":true}`
		}},
		{name: "duplicate create field", body: func(fake *examSittingHTTPFake) string {
			return `{"exam_revision_id":"` + fake.sitting.ExamRevisionID.String() + `","exam_revision_id":"` + fake.sitting.ExamRevisionID.String() + `","class_id":"` + fake.sitting.ClassID.String() + `","scheduled_start_at":"2026-08-15T10:00:00Z","scheduled_end_at":"2026-08-15T11:00:00Z"}`
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			logger, _ := newTestLogger(t)
			fake := newExamSittingHTTPFake(t)
			httpAPI := newFocusedResourceAPI(t, logger, fake, examSittingResource(fake))
			request := newJSONRequest(http.MethodPost, examSittingCollectionPath(fake.sitting.ExamID), strings.NewReader(test.body(fake)))
			request.Header.Set("Authorization", "Bearer credential")
			request.Header.Set("Idempotency-Key", "schedule-once")
			response := httptest.NewRecorder()
			httpAPI.ServeHTTP(response, request)
			require.Equal(t, http.StatusBadRequest, response.Code, "status = %d command = %#v body = %s", response.Code, fake.schedule, response.Body.String())
			require.False(t, fake.schedule.ExamID.IsValid(), "status = %d command = %#v body = %s", response.Code, fake.schedule, response.Body.String())
		})
	}

	logger, _ := newTestLogger(t)
	fake := newExamSittingHTTPFake(t)
	httpAPI := newFocusedResourceAPI(t, logger, fake, examSittingResource(fake))
	path := examSittingMemberPath(fake.sitting.ExamID, fake.sitting.ID)
	patch := `{"expected_revision":1,"class_id":"` + model.NewClassID().String() + `","scheduled_start_at":"2026-08-16T09:00:00Z"}`
	request := newJSONRequest(http.MethodPatch, path, strings.NewReader(patch))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Idempotency-Key", "reschedule-once")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, "status = %d command = %#v body = %s", response.Code, fake.update, response.Body.String())
	require.NotNil(t, fake.update.ClassID, "status = %d command = %#v body = %s", response.Code, fake.update, response.Body.String())
	require.NotNil(t, fake.update.ScheduledStartAt, "status = %d command = %#v body = %s", response.Code, fake.update, response.Body.String())
	require.Nil(t, fake.update.ExamRevisionID, "status = %d command = %#v body = %s", response.Code, fake.update, response.Body.String())
	require.Nil(t, fake.update.ScheduledEndAt, "status = %d command = %#v body = %s", response.Code, fake.update, response.Body.String())
	for name, body := range map[string]string{
		"no change":     `{"expected_revision":1}`,
		"explicit null": `{"expected_revision":1,"class_id":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			bad := newJSONRequest(http.MethodPatch, path, strings.NewReader(body))
			bad.Header = request.Header.Clone()
			got := httptest.NewRecorder()
			httpAPI.ServeHTTP(got, bad)
			require.Equal(t, http.StatusBadRequest, got.Code, "status = %d calls = %d body = %s", got.Code, fake.updateCalls, got.Body.String())
			require.Equal(t, 1, fake.updateCalls, "status = %d calls = %d body = %s", got.Code, fake.updateCalls, got.Body.String())
		})
	}
}

func TestExamSittingHTTPCancelKeepsPrivateReasonOutOfResponse(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	fake := newExamSittingHTTPFake(t)
	canceled := *fake.sitting
	canceled.State = model.ExamSittingCanceled
	canceled.CanceledAt = model.OptionalTimeFrom(canceled.UpdatedAt.Add(time.Minute))
	canceled.UpdatedAt = canceled.CanceledAt.Time
	canceled.ReasonCode = model.ExamSittingReasonManagerCanceled
	canceled.Revision = 2
	fake.cancelResult = app.ExamSittingView{Sitting: &canceled}
	httpAPI := newFocusedResourceAPI(t, logger, fake, examSittingResource(fake))
	request := newJSONRequest(http.MethodPost, examSittingMemberPath(fake.sitting.ExamID, fake.sitting.ID)+"/cancel", strings.NewReader(`{"expected_revision":1,"reason":"Suspected identity substitution"}`))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Idempotency-Key", "cancel-once")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, "status = %d command = %#v body = %s", response.Code, fake.cancel, response.Body.String())
	require.Equal(t, "Suspected identity substitution", fake.cancel.PrivateReason, "status = %d command = %#v body = %s", response.Code, fake.cancel, response.Body.String())
	require.Equal(t, "cancel-once", fake.cancel.IdempotencyKey, "status = %d command = %#v body = %s", response.Code, fake.cancel, response.Body.String())
	require.False(t, bytes.Contains(response.Body.Bytes(), []byte("Suspected identity substitution")))
	require.True(t, bytes.Contains(response.Body.Bytes(), []byte(`"reason_code":"manager_canceled"`)))
}

func TestExamSittingHTTPLifecycleCommandsAreStrictIdempotentAndKeepReasonsPrivate(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, suffix, body, key string
		assert                  func(*testing.T, *examSittingHTTPFake)
	}{
		{"pause", "/pause", `{"expected_revision":1,"reason":"Investigating candidate access"}`, "pause-once", func(t *testing.T, fake *examSittingHTTPFake) {
			require.Equal(t, int64(1), fake.pause.ExpectedRevision)
			require.Equal(t, "Investigating candidate access", fake.pause.PrivateReason)
			require.Equal(t, "pause-once", fake.pause.IdempotencyKey)
		}},
		{"resume", "/resume", `{"expected_revision":2,"reason":"Investigation completed"}`, "resume-once", func(t *testing.T, fake *examSittingHTTPFake) {
			require.Equal(t, int64(2), fake.resume.ExpectedRevision)
			require.Equal(t, "Investigation completed", fake.resume.PrivateReason)
			require.Equal(t, "resume-once", fake.resume.IdempotencyKey)
		}},
		{"extend", "/extend", `{"expected_revision":3,"scheduled_end_at":"2026-08-15T15:30:00+02:00","reason":"Compensating for disruption"}`, "extend-once", func(t *testing.T, fake *examSittingHTTPFake) {
			require.Equal(t, int64(3), fake.extend.ExpectedRevision)
			require.Equal(t, "Compensating for disruption", fake.extend.PrivateReason)
			require.Equal(t, "extend-once", fake.extend.IdempotencyKey)
			require.Same(t, time.UTC, fake.extend.ScheduledEndAt.Location())
			require.Equal(t, 13, fake.extend.ScheduledEndAt.Hour())
		}},
		{"close", "/close", `{"expected_revision":4,"reason":"Authorized early close"}`, "close-once", func(t *testing.T, fake *examSittingHTTPFake) {
			require.Equal(t, int64(4), fake.close.ExpectedRevision)
			require.Equal(t, "Authorized early close", fake.close.PrivateReason)
			require.Equal(t, "close-once", fake.close.IdempotencyKey)
		}},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			logger, _ := newTestLogger(t)
			fake := newExamSittingHTTPFake(t)
			httpAPI := newFocusedResourceAPI(t, logger, fake, examSittingResource(fake))
			request := newJSONRequest(http.MethodPost, examSittingMemberPath(fake.sitting.ExamID, fake.sitting.ID)+test.suffix, strings.NewReader(test.body))
			request.Header.Set("Authorization", "Bearer credential")
			request.Header.Set("Idempotency-Key", test.key)
			response := httptest.NewRecorder()
			httpAPI.ServeHTTP(response, request)
			require.Equal(t, http.StatusOK, response.Code, "status = %d body = %s", response.Code, response.Body.String())
			test.assert(t, fake)
			require.False(t, bytes.Contains(response.Body.Bytes(), []byte(`"reason"`)))
			require.False(t, bytes.Contains(response.Body.Bytes(), []byte("disruption")))
		})
	}
}

func TestExamSittingHTTPLifecycleBodiesRejectUnknownDuplicateAndInvalidValues(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"unknown":       `{"expected_revision":1,"reason":"Valid reason","extra":true}`,
		"duplicate":     `{"expected_revision":1,"expected_revision":2,"reason":"Valid reason"}`,
		"zero revision": `{"expected_revision":0,"reason":"Valid reason"}`,
		"padded reason": `{"expected_revision":1,"reason":" padded "}`,
		"long reason":   `{"expected_revision":1,"reason":"` + strings.Repeat("x", 1001) + `"}`,
	} {
		name, body := name, body
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			logger, _ := newTestLogger(t)
			fake := newExamSittingHTTPFake(t)
			httpAPI := newFocusedResourceAPI(t, logger, fake, examSittingResource(fake))
			request := newJSONRequest(http.MethodPost, examSittingMemberPath(fake.sitting.ExamID, fake.sitting.ID)+"/pause", strings.NewReader(body))
			request.Header.Set("Authorization", "Bearer credential")
			request.Header.Set("Idempotency-Key", "invalid-once")
			response := httptest.NewRecorder()
			httpAPI.ServeHTTP(response, request)
			require.Equal(t, http.StatusBadRequest, response.Code, "status=%d command=%#v body=%s", response.Code, fake.pause, response.Body.String())
			require.False(t, fake.pause.ExamID.IsValid(), "status=%d command=%#v body=%s", response.Code, fake.pause, response.Body.String())
		})
	}
}

func TestExamSittingHTTPListNormalizesFiltersAndUsesVersionedDescendingTupleCursor(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	fake := newExamSittingHTTPFake(t)
	older := *fake.sitting
	older.ID = model.NewExamSittingID()
	older.ScheduledStartAt = older.ScheduledStartAt.Add(-time.Hour)
	older.ScheduledEndAt = older.ScheduledEndAt.Add(-time.Hour)
	fake.page = app.ExamSittingPage{Items: []app.ExamSittingView{{Sitting: fake.sitting}, {Sitting: &older}}, HasMore: true}
	httpAPI := newFocusedResourceAPI(t, logger, fake, examSittingResource(fake))
	beforeSittingID := model.NewExamSittingID()
	cursor, err := encodeExamSittingCursor(examSittingCursor{StartAt: fake.sitting.ScheduledStartAt, ID: beforeSittingID})
	require.NoError(t, err)
	query := url.Values{}
	query.Set("class_id", fake.sitting.ClassID.String())
	query.Add("state", "scheduled")
	query.Add("state", "scheduled")
	query.Add("state", "paused")
	query.Set("ends_after", "2026-08-15T09:00:00+02:00")
	query.Set("starts_before", "2026-08-15T18:00:00+02:00")
	query.Set("limit", "2")
	query.Set("cursor", cursor)
	request := httptest.NewRequest(http.MethodGet, examSittingCollectionPath(fake.sitting.ExamID)+"?"+query.Encode(), nil)
	request.Header.Set("Authorization", "Bearer credential")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, "status = %d query = %#v body = %s", response.Code, fake.list, response.Body.String())
	require.Equal(t, 2, fake.list.Limit, "status = %d query = %#v body = %s", response.Code, fake.list, response.Body.String())
	require.Equal(t, fake.sitting.ClassID, fake.list.ClassID, "status = %d query = %#v body = %s", response.Code, fake.list, response.Body.String())
	require.Len(t, fake.list.States, 2, "status = %d query = %#v body = %s", response.Code, fake.list, response.Body.String())
	require.Equal(t, 7, fake.list.OverlapStartAt.Hour(), "status = %d query = %#v body = %s", response.Code, fake.list, response.Body.String())
	require.Equal(t, 16, fake.list.OverlapEndAt.Hour(), "status = %d query = %#v body = %s", response.Code, fake.list, response.Body.String())
	require.Equal(t, fake.sitting.ScheduledStartAt, fake.list.BeforeScheduledStartAt, "status = %d query = %#v body = %s", response.Code, fake.list, response.Body.String())
	require.Equal(t, beforeSittingID, fake.list.BeforeSittingID, "status = %d query = %#v body = %s", response.Code, fake.list, response.Body.String())
	var page examSittingListResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page))
	decoded, err := decodeExamSittingCursor(page.NextCursor)
	require.NoError(t, err, "cursor = %#v err", decoded)
	require.Equal(t, older.ScheduledStartAt, decoded.StartAt, "cursor = %#v err", decoded)
	require.Equal(t, older.ID, decoded.ID, "cursor = %#v err", decoded)
	fake.page = app.ExamSittingPage{HasMore: true}
	noProgress := httptest.NewRecorder()
	httpAPI.ServeHTTP(noProgress, request.Clone(request.Context()))
	require.Equal(t, http.StatusInternalServerError, noProgress.Code, "no-progress status = %d body = %s", noProgress.Code, noProgress.Body.String())
}

func TestExamSittingHTTPListRejectsPartialOverlapAndMalformedCursor(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	fake := newExamSittingHTTPFake(t)
	httpAPI := newFocusedResourceAPI(t, logger, fake, examSittingResource(fake))
	unsupported, _ := json.Marshal(map[string]any{"version": 2, "start_at": fake.sitting.ScheduledStartAt.Format(time.RFC3339Nano), "id": fake.sitting.ID.String()})
	for name, query := range map[string]string{
		"partial overlap":         "ends_after=2026-08-15T09%3A00%3A00Z",
		"bad interval":            "ends_after=2026-08-15T12%3A00%3A00Z&starts_before=2026-08-15T11%3A00%3A00Z",
		"bad state":               "state=unknown",
		"unsupported state count": "state=scheduled&state=open&state=paused&state=closing&state=closed&state=canceled&state=extra",
		"malformed cursor":        "cursor=not-a-cursor",
		"cursor version":          "cursor=" + url.QueryEscape(base64.RawURLEncoding.EncodeToString(unsupported)),
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, examSittingCollectionPath(fake.sitting.ExamID)+"?"+query, nil)
			request.Header.Set("Authorization", "Bearer credential")
			response := httptest.NewRecorder()
			httpAPI.ServeHTTP(response, request)
			assertHTTPProblem(t, response, http.StatusBadRequest, "request.invalid")
		})
	}
}

func TestExamSittingHTTPGetUsesBothExactIdentities(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	fake := newExamSittingHTTPFake(t)
	httpAPI := newFocusedResourceAPI(t, logger, fake, examSittingResource(fake))
	request := httptest.NewRequest(http.MethodGet, examSittingMemberPath(fake.sitting.ExamID, fake.sitting.ID), nil)
	request.Header.Set("Authorization", "Bearer credential")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, "status = %d query = %#v body = %s", response.Code, fake.get, response.Body.String())
	require.Equal(t, fake.sitting.ExamID, fake.get.ExamID, "status = %d query = %#v body = %s", response.Code, fake.get, response.Body.String())
	require.Equal(t, fake.sitting.ID, fake.get.SittingID, "status = %d query = %#v body = %s", response.Code, fake.get, response.Body.String())
}

func TestExamSittingHTTPListsNoShowsWithOpaqueUserCursor(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	fake := newExamSittingHTTPFake(t)
	first, second := model.NewUserID(), model.NewUserID()
	fake.noShowPage = app.ExamSittingNoShowPage{Items: []store.ExamSittingNoShow{
		{CandidateUserID: first}, {CandidateUserID: second}}, HasMore: true}
	httpAPI := newFocusedResourceAPI(t, logger, fake, examSittingResource(fake))
	request := httptest.NewRequest(http.MethodGet, examSittingMemberPath(fake.sitting.ExamID, fake.sitting.ID)+"/no-shows?limit=2", nil)
	request.Header.Set("Authorization", "Bearer credential")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, "status=%d query=%#v body=%s", response.Code, fake.noShows, response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"), "status=%d query=%#v body=%s", response.Code, fake.noShows, response.Body.String())
	require.Equal(t, fake.sitting.ExamID, fake.noShows.ExamID, "status=%d query=%#v body=%s", response.Code, fake.noShows, response.Body.String())
	require.Equal(t, fake.sitting.ID, fake.noShows.SittingID, "status=%d query=%#v body=%s", response.Code, fake.noShows, response.Body.String())
	require.Equal(t, 2, fake.noShows.Limit, "status=%d query=%#v body=%s", response.Code, fake.noShows, response.Body.String())
	var body examSittingNoShowListResponse
	err := json.Unmarshal(response.Body.Bytes(), &body)
	require.NoError(t, err, "body=%#v err", body)
	require.Len(t, body.Items, 2, "body=%#v err", body)
	require.Equal(t, second.String(), body.Items[1].CandidateUserID, "body=%#v err", body)
	require.NotEmpty(t, body.NextCursor, "body=%#v err", body)
	decoded, err := decodeExamSittingNoShowCursor(body.NextCursor)
	require.NoError(t, err, "cursor")
	require.Equal(t, second, decoded, "cursor")
	require.NotContains(t, body.NextCursor, second.String(), "cursor")
	fake.noShowPage = app.ExamSittingNoShowPage{}
	secondRequest := httptest.NewRequest(http.MethodGet, examSittingMemberPath(fake.sitting.ExamID, fake.sitting.ID)+"/no-shows?cursor="+url.QueryEscape(body.NextCursor), nil)
	secondRequest.Header.Set("Authorization", "Bearer credential")
	secondResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(secondResponse, secondRequest)
	require.Equal(t, http.StatusOK, secondResponse.Code, "no-show cursor forwarding = %d query=%#v body=%s", secondResponse.Code, fake.noShows, secondResponse.Body.String())
	require.Equal(t, second, fake.noShows.AfterCandidateUserID, "no-show cursor forwarding = %d query=%#v body=%s", secondResponse.Code, fake.noShows, secondResponse.Body.String())
	malformedRequest := httptest.NewRequest(http.MethodGet, examSittingMemberPath(fake.sitting.ExamID, fake.sitting.ID)+"/no-shows?cursor=not-a-cursor", nil)
	malformedRequest.Header.Set("Authorization", "Bearer credential")
	malformedResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(malformedResponse, malformedRequest)
	assertHTTPProblem(t, malformedResponse, http.StatusBadRequest, "request.invalid")
	fake.noShowPage = app.ExamSittingNoShowPage{HasMore: true}
	noProgress := httptest.NewRecorder()
	httpAPI.ServeHTTP(noProgress, request.Clone(request.Context()))
	require.Equal(t, http.StatusInternalServerError, noProgress.Code, "no-progress status=%d body=%s", noProgress.Code, noProgress.Body.String())
	invalid := []string{
		"not-base64",
		base64.RawURLEncoding.EncodeToString([]byte(`{"version":1,"candidate_user_id":"` + second.String() + `"}`)),
		base64.RawURLEncoding.EncodeToString([]byte(`{"candidate_user_id":"` + second.String() + `","candidate_user_id":"` + second.String() + `"}`)),
		base64.RawURLEncoding.EncodeToString([]byte(`{"candidate_user_id":"` + second.String() + `","extra":true}`)),
		base64.RawURLEncoding.EncodeToString([]byte(`{"candidate_user_id":"` + second.String() + `"}{}`)),
	}
	for _, raw := range invalid {
		_, decodeErr := decodeExamSittingNoShowCursor(raw)
		assert.Error(t, decodeErr, "decodeExamSittingNoShowCursor(%q) succeeded", raw)
	}
}

type examSittingHTTPFake struct {
	Application
	principal    model.Principal
	sitting      *model.ExamSitting
	schedule     app.ScheduleExamSittingCommand
	get          app.GetExamSittingQuery
	list         app.ListExamSittingsQuery
	page         app.ExamSittingPage
	update       app.UpdateExamSittingScheduleCommand
	updateCalls  int
	cancel       app.CancelExamSittingCommand
	cancelResult app.ExamSittingView
	pause        app.PauseExamSittingCommand
	resume       app.ResumeExamSittingCommand
	extend       app.ExtendExamSittingCommand
	close        app.CloseExamSittingCommand
	noShows      app.ListExamSittingNoShowsQuery
	noShowPage   app.ExamSittingNoShowPage
}

func newExamSittingHTTPFake(t *testing.T) *examSittingHTTPFake {
	t.Helper()
	at := time.Date(2026, time.August, 15, 9, 30, 0, 123456789, time.UTC)
	sitting, err := model.NewExamSitting(model.NewExamSittingID(), model.NewExamID(), model.NewExamRevisionID(), model.NewClassID(), at.Add(time.Hour), at.Add(3*time.Hour), at)
	require.NoError(t, err)
	return &examSittingHTTPFake{principal: testExamHTTPPrincipal(), sitting: sitting, cancelResult: app.ExamSittingView{Sitting: sitting}}
}

func (f *examSittingHTTPFake) AuthenticateAccess(context.Context, string) (*model.Principal, error) {
	principal := f.principal
	return &principal, nil
}

func (f *examSittingHTTPFake) AuthenticateBearer(context.Context, string) (*model.Principal, error) {
	principal := f.principal
	return &principal, nil
}

func (f *examSittingHTTPFake) ScheduleExamSitting(_ context.Context, _ app.Invocation, command app.ScheduleExamSittingCommand) (app.ExamSittingView, error) {
	f.schedule = command
	return app.ExamSittingView{Sitting: f.sitting}, nil
}

func (f *examSittingHTTPFake) GetExamSitting(_ context.Context, _ app.Invocation, query app.GetExamSittingQuery) (app.ExamSittingView, error) {
	f.get = query
	return app.ExamSittingView{Sitting: f.sitting}, nil
}

func (f *examSittingHTTPFake) ListExamSittings(_ context.Context, _ app.Invocation, query app.ListExamSittingsQuery) (app.ExamSittingPage, error) {
	f.list = query
	return f.page, nil
}

func (f *examSittingHTTPFake) UpdateExamSittingSchedule(_ context.Context, _ app.Invocation, command app.UpdateExamSittingScheduleCommand) (app.ExamSittingView, error) {
	f.update, f.updateCalls = command, f.updateCalls+1
	return app.ExamSittingView{Sitting: f.sitting}, nil
}

func (f *examSittingHTTPFake) CancelExamSitting(_ context.Context, _ app.Invocation, command app.CancelExamSittingCommand) (app.ExamSittingView, error) {
	f.cancel = command
	return f.cancelResult, nil
}

func (f *examSittingHTTPFake) PauseExamSitting(_ context.Context, _ app.Invocation, command app.PauseExamSittingCommand) (app.ExamSittingView, error) {
	f.pause = command
	return app.ExamSittingView{Sitting: f.sitting}, nil
}

func (f *examSittingHTTPFake) ResumeExamSitting(_ context.Context, _ app.Invocation, command app.ResumeExamSittingCommand) (app.ExamSittingView, error) {
	f.resume = command
	return app.ExamSittingView{Sitting: f.sitting}, nil
}

func (f *examSittingHTTPFake) ExtendExamSitting(_ context.Context, _ app.Invocation, command app.ExtendExamSittingCommand) (app.ExamSittingView, error) {
	f.extend = command
	return app.ExamSittingView{Sitting: f.sitting}, nil
}

func (f *examSittingHTTPFake) CloseExamSitting(_ context.Context, _ app.Invocation, command app.CloseExamSittingCommand) (app.ExamSittingView, error) {
	f.close = command
	return app.ExamSittingView{Sitting: f.sitting}, nil
}

func (f *examSittingHTTPFake) ListExamSittingNoShows(_ context.Context, _ app.Invocation,
	query app.ListExamSittingNoShowsQuery,
) (app.ExamSittingNoShowPage, error) {
	f.noShows = query
	return f.noShowPage, nil
}

func examSittingCollectionPath(examID model.ExamID) string {
	return "/api/v1/exams/" + examID.String() + "/sittings"
}

func examSittingMemberPath(examID model.ExamID, sittingID model.ExamSittingID) string {
	return examSittingCollectionPath(examID) + "/" + sittingID.String()
}
