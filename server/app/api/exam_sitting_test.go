// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

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

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestExamSittingHTTPScheduleUsesStrictIdempotentCommandAndSafeResponse(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	fake := newExamSittingHTTPFake(t)
	httpAPI := newFocusedResourceAPI(t, logger, fake, examSittingResource(fake))
	body := `{"exam_revision_id":"` + fake.sitting.ExamRevisionID.String() + `","class_id":"` + fake.sitting.ClassID.String() + `","scheduled_start_at":"2026-08-15T12:30:00+02:00","scheduled_end_at":"2026-08-15T14:30:00+02:00"}`
	request := httptest.NewRequest(http.MethodPost, examSittingCollectionPath(fake.sitting.ExamID), strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Idempotency-Key", "schedule-once")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if fake.schedule.ExamID != fake.sitting.ExamID || fake.schedule.ExamRevisionID != fake.sitting.ExamRevisionID ||
		fake.schedule.ClassID != fake.sitting.ClassID || fake.schedule.IdempotencyKey != "schedule-once" ||
		fake.schedule.ScheduledStartAt.Location() != time.UTC || fake.schedule.ScheduledStartAt.Hour() != 10 ||
		fake.schedule.ScheduledEndAt.Location() != time.UTC || fake.schedule.ScheduledEndAt.Hour() != 12 {
		t.Fatalf("command = %#v", fake.schedule)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	wantKeys := []string{"id", "exam_id", "exam_revision_id", "class_id", "scheduled_start_at", "scheduled_end_at", "state", "created_at", "updated_at", "revision"}
	if len(payload) != len(wantKeys) {
		t.Fatalf("response fields = %v, body = %s", payload, response.Body.String())
	}
	for _, key := range wantKeys {
		if _, ok := payload[key]; !ok {
			t.Errorf("response omitted %q", key)
		}
	}
	for _, forbidden := range []string{"private_reason", "actor_user_id", "manager_override", "audit_event_id", "content", "path"} {
		if _, ok := payload[forbidden]; ok {
			t.Errorf("response exposed %q", forbidden)
		}
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
			request := httptest.NewRequest(http.MethodPost, examSittingCollectionPath(fake.sitting.ExamID), strings.NewReader(test.body(fake)))
			request.Header.Set("Authorization", "Bearer credential")
			request.Header.Set("Idempotency-Key", "schedule-once")
			response := httptest.NewRecorder()
			httpAPI.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest || fake.schedule.ExamID.IsValid() {
				t.Fatalf("status = %d command = %#v body = %s", response.Code, fake.schedule, response.Body.String())
			}
		})
	}

	logger, _ := newTestLogger(t)
	fake := newExamSittingHTTPFake(t)
	httpAPI := newFocusedResourceAPI(t, logger, fake, examSittingResource(fake))
	path := examSittingMemberPath(fake.sitting.ExamID, fake.sitting.ID)
	patch := `{"expected_revision":1,"class_id":"` + model.NewClassID().String() + `","scheduled_start_at":"2026-08-16T09:00:00Z"}`
	request := httptest.NewRequest(http.MethodPatch, path, strings.NewReader(patch))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Idempotency-Key", "reschedule-once")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusOK || fake.update.ClassID == nil || fake.update.ScheduledStartAt == nil || fake.update.ExamRevisionID != nil || fake.update.ScheduledEndAt != nil {
		t.Fatalf("status = %d command = %#v body = %s", response.Code, fake.update, response.Body.String())
	}
	for name, body := range map[string]string{
		"no change":     `{"expected_revision":1}`,
		"explicit null": `{"expected_revision":1,"class_id":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			bad := httptest.NewRequest(http.MethodPatch, path, strings.NewReader(body))
			bad.Header = request.Header.Clone()
			got := httptest.NewRecorder()
			httpAPI.ServeHTTP(got, bad)
			if got.Code != http.StatusBadRequest || fake.updateCalls != 1 {
				t.Fatalf("status = %d calls = %d body = %s", got.Code, fake.updateCalls, got.Body.String())
			}
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
	fake.cancelResult = application.ExamSittingView{Sitting: &canceled}
	httpAPI := newFocusedResourceAPI(t, logger, fake, examSittingResource(fake))
	request := httptest.NewRequest(http.MethodPost, examSittingMemberPath(fake.sitting.ExamID, fake.sitting.ID)+"/cancel", strings.NewReader(`{"expected_revision":1,"reason":"Suspected identity substitution"}`))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Idempotency-Key", "cancel-once")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusOK || fake.cancel.PrivateReason != "Suspected identity substitution" || fake.cancel.IdempotencyKey != "cancel-once" {
		t.Fatalf("status = %d command = %#v body = %s", response.Code, fake.cancel, response.Body.String())
	}
	if bytes.Contains(response.Body.Bytes(), []byte("Suspected identity substitution")) || !bytes.Contains(response.Body.Bytes(), []byte(`"reason_code":"manager_canceled"`)) {
		t.Fatalf("unsafe or incomplete response = %s", response.Body.String())
	}
}

func TestExamSittingHTTPLifecycleCommandsAreStrictIdempotentAndKeepReasonsPrivate(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, suffix, body, key string
		assert                  func(*testing.T, *examSittingHTTPFake)
	}{
		{"pause", "/pause", `{"expected_revision":1,"reason":"Investigating candidate access"}`, "pause-once", func(t *testing.T, fake *examSittingHTTPFake) {
			if fake.pause.ExpectedRevision != 1 || fake.pause.PrivateReason != "Investigating candidate access" || fake.pause.IdempotencyKey != "pause-once" {
				t.Fatalf("command = %#v", fake.pause)
			}
		}},
		{"resume", "/resume", `{"expected_revision":2,"reason":"Investigation completed"}`, "resume-once", func(t *testing.T, fake *examSittingHTTPFake) {
			if fake.resume.ExpectedRevision != 2 || fake.resume.PrivateReason != "Investigation completed" || fake.resume.IdempotencyKey != "resume-once" {
				t.Fatalf("command = %#v", fake.resume)
			}
		}},
		{"extend", "/extend", `{"expected_revision":3,"scheduled_end_at":"2026-08-15T15:30:00+02:00","reason":"Compensating for disruption"}`, "extend-once", func(t *testing.T, fake *examSittingHTTPFake) {
			if fake.extend.ExpectedRevision != 3 || fake.extend.PrivateReason != "Compensating for disruption" || fake.extend.IdempotencyKey != "extend-once" || fake.extend.ScheduledEndAt.Location() != time.UTC || fake.extend.ScheduledEndAt.Hour() != 13 {
				t.Fatalf("command = %#v", fake.extend)
			}
		}},
		{"close", "/close", `{"expected_revision":4,"reason":"Authorized early close"}`, "close-once", func(t *testing.T, fake *examSittingHTTPFake) {
			if fake.close.ExpectedRevision != 4 || fake.close.PrivateReason != "Authorized early close" || fake.close.IdempotencyKey != "close-once" {
				t.Fatalf("command = %#v", fake.close)
			}
		}},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			logger, _ := newTestLogger(t)
			fake := newExamSittingHTTPFake(t)
			httpAPI := newFocusedResourceAPI(t, logger, fake, examSittingResource(fake))
			request := httptest.NewRequest(http.MethodPost, examSittingMemberPath(fake.sitting.ExamID, fake.sitting.ID)+test.suffix, strings.NewReader(test.body))
			request.Header.Set("Authorization", "Bearer credential")
			request.Header.Set("Idempotency-Key", test.key)
			response := httptest.NewRecorder()
			httpAPI.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
			}
			test.assert(t, fake)
			if bytes.Contains(response.Body.Bytes(), []byte(`"reason"`)) || bytes.Contains(response.Body.Bytes(), []byte("disruption")) {
				t.Fatalf("private reason exposed: %s", response.Body.String())
			}
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
			request := httptest.NewRequest(http.MethodPost, examSittingMemberPath(fake.sitting.ExamID, fake.sitting.ID)+"/pause", strings.NewReader(body))
			request.Header.Set("Authorization", "Bearer credential")
			request.Header.Set("Idempotency-Key", "invalid-once")
			response := httptest.NewRecorder()
			httpAPI.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest || fake.pause.ExamID.IsValid() {
				t.Fatalf("status=%d command=%#v body=%s", response.Code, fake.pause, response.Body.String())
			}
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
	fake.page = application.ExamSittingPage{Items: []application.ExamSittingView{{Sitting: fake.sitting}, {Sitting: &older}}, HasMore: true}
	httpAPI := newFocusedResourceAPI(t, logger, fake, examSittingResource(fake))
	cursor := encodeExamSittingCursor(examSittingCursor{StartAt: fake.sitting.ScheduledStartAt, ID: model.NewExamSittingID()})
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
	if response.Code != http.StatusOK || fake.list.Limit != 2 || fake.list.ClassID != fake.sitting.ClassID || len(fake.list.States) != 2 ||
		fake.list.OverlapStartAt.Hour() != 7 || fake.list.OverlapEndAt.Hour() != 16 || fake.list.BeforeScheduledStartAt != fake.sitting.ScheduledStartAt || !fake.list.BeforeSittingID.IsValid() {
		t.Fatalf("status = %d query = %#v body = %s", response.Code, fake.list, response.Body.String())
	}
	var page examSittingListResponse
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeExamSittingCursor(page.NextCursor)
	if err != nil || decoded.StartAt != older.ScheduledStartAt || decoded.ID != older.ID {
		t.Fatalf("cursor = %#v err = %v", decoded, err)
	}
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
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
			}
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
	if response.Code != http.StatusOK || fake.get.ExamID != fake.sitting.ExamID || fake.get.SittingID != fake.sitting.ID {
		t.Fatalf("status = %d query = %#v body = %s", response.Code, fake.get, response.Body.String())
	}
}

func TestExamSittingHTTPListsNoShowsWithOpaqueUserCursor(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	fake := newExamSittingHTTPFake(t)
	first, second := model.NewUserID(), model.NewUserID()
	fake.noShowPage = application.ExamSittingNoShowPage{Items: []store.ExamSittingNoShow{
		{CandidateUserID: first}, {CandidateUserID: second}}, HasMore: true}
	httpAPI := newFocusedResourceAPI(t, logger, fake, examSittingResource(fake))
	request := httptest.NewRequest(http.MethodGet, examSittingMemberPath(fake.sitting.ExamID, fake.sitting.ID)+"/no-shows?limit=2", nil)
	request.Header.Set("Authorization", "Bearer credential")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" ||
		fake.noShows.ExamID != fake.sitting.ExamID ||
		fake.noShows.SittingID != fake.sitting.ID || fake.noShows.Limit != 2 {
		t.Fatalf("status=%d query=%#v body=%s", response.Code, fake.noShows, response.Body.String())
	}
	var body examSittingNoShowListResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || len(body.Items) != 2 ||
		body.Items[1].CandidateUserID != second.String() || body.NextCursor == "" {
		t.Fatalf("body=%#v err=%v", body, err)
	}
	decoded, err := decodeExamSittingNoShowCursor(body.NextCursor)
	if err != nil || decoded != second || strings.Contains(body.NextCursor, second.String()) {
		t.Fatalf("cursor=%q decoded=%s err=%v", body.NextCursor, decoded, err)
	}
	invalid := []string{
		"not-base64",
		base64.RawURLEncoding.EncodeToString([]byte(`{"version":2,"candidate_user_id":"` + second.String() + `"}`)),
		base64.RawURLEncoding.EncodeToString([]byte(`{"version":1,"version":1,"candidate_user_id":"` + second.String() + `"}`)),
		base64.RawURLEncoding.EncodeToString([]byte(`{"version":1,"candidate_user_id":"` + second.String() + `","extra":true}`)),
		base64.RawURLEncoding.EncodeToString([]byte(`{"version":1,"candidate_user_id":"` + second.String() + `"}{}`)),
	}
	for _, raw := range invalid {
		if _, decodeErr := decodeExamSittingNoShowCursor(raw); decodeErr == nil {
			t.Errorf("decodeExamSittingNoShowCursor(%q) succeeded", raw)
		}
	}
}

type examSittingHTTPFake struct {
	Application
	principal    model.Principal
	sitting      *model.ExamSitting
	schedule     application.ScheduleExamSittingCommand
	get          application.GetExamSittingQuery
	list         application.ListExamSittingsQuery
	page         application.ExamSittingPage
	update       application.UpdateExamSittingScheduleCommand
	updateCalls  int
	cancel       application.CancelExamSittingCommand
	cancelResult application.ExamSittingView
	pause        application.PauseExamSittingCommand
	resume       application.ResumeExamSittingCommand
	extend       application.ExtendExamSittingCommand
	close        application.CloseExamSittingCommand
	noShows      application.ListExamSittingNoShowsQuery
	noShowPage   application.ExamSittingNoShowPage
}

func newExamSittingHTTPFake(t *testing.T) *examSittingHTTPFake {
	t.Helper()
	at := time.Date(2026, time.August, 15, 9, 30, 0, 123456789, time.UTC)
	sitting, err := model.NewExamSitting(model.NewExamSittingID(), model.NewExamID(), model.NewExamRevisionID(), model.NewClassID(), at.Add(time.Hour), at.Add(3*time.Hour), at)
	if err != nil {
		t.Fatal(err)
	}
	return &examSittingHTTPFake{principal: testExamHTTPPrincipal(), sitting: sitting, cancelResult: application.ExamSittingView{Sitting: sitting}}
}

func (f *examSittingHTTPFake) AuthenticateAccess(context.Context, string) (*model.Principal, error) {
	principal := f.principal
	return &principal, nil
}

func (f *examSittingHTTPFake) AuthenticateBearer(context.Context, string) (*model.Principal, error) {
	principal := f.principal
	return &principal, nil
}

func (f *examSittingHTTPFake) ScheduleExamSitting(_ context.Context, _ application.Invocation, command application.ScheduleExamSittingCommand) (application.ExamSittingView, error) {
	f.schedule = command
	return application.ExamSittingView{Sitting: f.sitting}, nil
}

func (f *examSittingHTTPFake) GetExamSitting(_ context.Context, _ application.Invocation, query application.GetExamSittingQuery) (application.ExamSittingView, error) {
	f.get = query
	return application.ExamSittingView{Sitting: f.sitting}, nil
}

func (f *examSittingHTTPFake) ListExamSittings(_ context.Context, _ application.Invocation, query application.ListExamSittingsQuery) (application.ExamSittingPage, error) {
	f.list = query
	return f.page, nil
}

func (f *examSittingHTTPFake) UpdateExamSittingSchedule(_ context.Context, _ application.Invocation, command application.UpdateExamSittingScheduleCommand) (application.ExamSittingView, error) {
	f.update, f.updateCalls = command, f.updateCalls+1
	return application.ExamSittingView{Sitting: f.sitting}, nil
}

func (f *examSittingHTTPFake) CancelExamSitting(_ context.Context, _ application.Invocation, command application.CancelExamSittingCommand) (application.ExamSittingView, error) {
	f.cancel = command
	return f.cancelResult, nil
}

func (f *examSittingHTTPFake) PauseExamSitting(_ context.Context, _ application.Invocation, command application.PauseExamSittingCommand) (application.ExamSittingView, error) {
	f.pause = command
	return application.ExamSittingView{Sitting: f.sitting}, nil
}

func (f *examSittingHTTPFake) ResumeExamSitting(_ context.Context, _ application.Invocation, command application.ResumeExamSittingCommand) (application.ExamSittingView, error) {
	f.resume = command
	return application.ExamSittingView{Sitting: f.sitting}, nil
}

func (f *examSittingHTTPFake) ExtendExamSitting(_ context.Context, _ application.Invocation, command application.ExtendExamSittingCommand) (application.ExamSittingView, error) {
	f.extend = command
	return application.ExamSittingView{Sitting: f.sitting}, nil
}

func (f *examSittingHTTPFake) CloseExamSitting(_ context.Context, _ application.Invocation, command application.CloseExamSittingCommand) (application.ExamSittingView, error) {
	f.close = command
	return application.ExamSittingView{Sitting: f.sitting}, nil
}

func (f *examSittingHTTPFake) ListExamSittingNoShows(_ context.Context, _ application.Invocation,
	query application.ListExamSittingNoShowsQuery,
) (application.ExamSittingNoShowPage, error) {
	f.noShows = query
	return f.noShowPage, nil
}

func examSittingCollectionPath(examID model.ExamID) string {
	return "/api/v1/exams/" + examID.String() + "/sittings"
}

func examSittingMemberPath(examID model.ExamID, sittingID model.ExamSittingID) string {
	return examSittingCollectionPath(examID) + "/" + sittingID.String()
}
