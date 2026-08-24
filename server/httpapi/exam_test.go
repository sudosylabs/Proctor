// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

func TestExamHTTPCreateAndGetUseApplicationFacade(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := testExamHTTPPrincipal()
	view := testExamHTTPView(t, principal.UserID)
	fake := &examHTTPApplication{principal: principal, view: view}
	httpAPI := newFocusedResourceAPI(t, logger, fake, examResource(fake))

	body := []byte(`{"academic_unit_id":"` + view.Exam.AcademicUnitID.String() + `","title":"Systems","instructions_markdown":"Use **Go**."}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/exams", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "exam-retry")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", response.Code, response.Body.String())
	}
	if fake.create.AcademicUnitID != view.Exam.AcademicUnitID || fake.create.Title != "Systems" || fake.create.InstructionsMarkdown != "Use **Go**." || fake.create.IdempotencyKey != "exam-retry" {
		t.Fatalf("create command = %#v", fake.create)
	}
	assertExamHTTPResponse(t, response.Body.Bytes(), view)

	get := httptest.NewRequest(http.MethodGet, "/api/v1/exams/"+view.Exam.ID.String(), nil)
	get.Header.Set("Authorization", "Bearer credential")
	got := httptest.NewRecorder()
	httpAPI.ServeHTTP(got, get)
	if got.Code != http.StatusOK || fake.get.ExamID != view.Exam.ID {
		t.Fatalf("get = %d query=%#v body=%s", got.Code, fake.get, got.Body.String())
	}
	assertExamHTTPResponse(t, got.Body.Bytes(), view)
}

func TestExamHTTPListUsesBoundedCatalogQueryAndSummary(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := testExamHTTPPrincipal()
	unitID, examID := model.NewAcademicUnitID(), model.NewExamID()
	fake := &examHTTPApplication{principal: principal, catalog: application.ExamCatalogPage{Items: []application.ExamSummary{{
		ID: examID, AcademicUnitID: unitID, CreatorUserID: principal.UserID, OwnerUserID: principal.UserID,
		Title: "Systems", UpdatedAt: time.Date(2026, 8, 14, 8, 0, 0, 0, time.UTC), Revision: 2, ManagerCount: 1,
	}}}}
	httpAPI := newFocusedResourceAPI(t, logger, fake, examResource(fake))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/exams?academic_unit_id="+unitID.String()+"&q=systems+design&archive_state=all&limit=25", nil)
	request.Header.Set("Authorization", "Bearer credential")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("list status = %d: %s", response.Code, response.Body.String())
	}
	if fake.list.AcademicUnitID != unitID || fake.list.Query != "systems design" || fake.list.ArchiveFilter != application.ExamArchiveAll || fake.list.Limit != 25 {
		t.Fatalf("list query = %#v", fake.list)
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"title":"Systems"`)) || bytes.Contains(response.Body.Bytes(), []byte("instructions_markdown")) || bytes.Contains(response.Body.Bytes(), []byte("policy")) {
		t.Fatalf("unsafe list response = %s", response.Body.String())
	}
	before := time.Date(2026, 8, 13, 7, 6, 5, 432, time.UTC)
	cursor, err := encodeExamCatalogCursor(examCatalogCursor{UpdatedAt: before.Format(time.RFC3339Nano), ExamID: examID.String()})
	if err != nil {
		t.Fatal(err)
	}
	second := httptest.NewRequest(http.MethodGet, "/api/v1/exams?cursor="+url.QueryEscape(cursor), nil)
	second.Header.Set("Authorization", "Bearer credential")
	secondResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(secondResponse, second)
	if secondResponse.Code != http.StatusOK || !fake.list.BeforeUpdatedAt.Equal(before) || fake.list.BeforeExamID != examID {
		t.Fatalf("catalog cursor forwarding = %d query=%#v body=%s", secondResponse.Code, fake.list, secondResponse.Body.String())
	}
	malformed := httptest.NewRequest(http.MethodGet, "/api/v1/exams?cursor=not-a-cursor", nil)
	malformed.Header.Set("Authorization", "Bearer credential")
	malformedResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(malformedResponse, malformed)
	assertHTTPProblem(t, malformedResponse, http.StatusBadRequest, "request.invalid")
}

func TestExamCatalogCursorRoundTripsAndRejectsInvalidInput(t *testing.T) {
	t.Parallel()
	cursor := examCatalogCursor{Version: 1, UpdatedAt: "2026-08-14T08:00:00.123456789Z", ExamID: model.NewExamID().String()}
	encodedCursor, err := encodeExamCatalogCursor(cursor)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeExamCatalogCursor(encodedCursor)
	if err != nil || got != cursor {
		t.Fatalf("cursor = %#v, %v", got, err)
	}
	if _, err := decodeExamCatalogCursor("not-a-cursor"); err == nil {
		t.Fatal("invalid cursor accepted")
	}
	unsupported := []byte(`{"version":2,"updated_at":"2026-08-14T08:00:00Z","exam_id":"` + cursor.ExamID + `"}`)
	if _, err := decodeExamCatalogCursor(base64.RawURLEncoding.EncodeToString(unsupported)); err == nil {
		t.Fatal("unsupported cursor version accepted")
	}
	legacy := []byte(`{"updated_at":"2026-08-14T08:00:00Z","exam_id":"` + cursor.ExamID + `"}`)
	if decoded, err := decodeExamCatalogCursor(base64.RawURLEncoding.EncodeToString(legacy)); err != nil || decoded.Version != 0 {
		t.Fatalf("legacy versionless cursor = %#v, %v", decoded, err)
	}
	encoded, _ := json.Marshal(cursor)
	if _, err := decodeExamCatalogCursor(base64.RawURLEncoding.EncodeToString(append(encoded, []byte(`{}`)...))); err == nil {
		t.Fatal("cursor with trailing JSON accepted")
	}
}

func TestExamManagerHTTPListAndMutationsUseStrictBoundedContracts(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := testExamHTTPPrincipal()
	view := testExamHTTPView(t, principal.UserID)
	target := model.NewUserID()
	grantedAt := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)
	manager, err := model.NewExamManager(view.Exam.ID, target, principal.UserID, grantedAt)
	if err != nil {
		t.Fatal(err)
	}
	changedExam := view.Exam
	changedExam.Revision = 2
	fake := &examHTTPApplication{principal: principal,
		managerPage:   application.ExamManagerPage{Items: []application.ExamManagerSummary{{Manager: *manager}}},
		managerChange: application.ExamManagerChange{Exam: &changedExam, Manager: manager},
	}
	httpAPI := newFocusedResourceAPI(t, logger, fake, examResource(fake))

	list := httptest.NewRequest(http.MethodGet, "/api/v1/exams/"+view.Exam.ID.String()+"/managers?limit=1", nil)
	list.Header.Set("Authorization", "Bearer credential")
	listed := httptest.NewRecorder()
	httpAPI.ServeHTTP(listed, list)
	if listed.Code != http.StatusOK || fake.managerList.Limit != 1 || !bytes.Contains(listed.Body.Bytes(), []byte(target.String())) || bytes.Contains(listed.Body.Bytes(), []byte("display_name")) {
		t.Fatalf("list = %d query=%#v body=%s", listed.Code, fake.managerList, listed.Body.String())
	}
	cursor, err := encodeExamManagerCursor(examManagerCursor{GrantedAt: grantedAt.Format(time.RFC3339Nano), UserID: target.String()})
	if err != nil {
		t.Fatal(err)
	}
	second := httptest.NewRequest(http.MethodGet, "/api/v1/exams/"+view.Exam.ID.String()+"/managers?cursor="+url.QueryEscape(cursor), nil)
	second.Header.Set("Authorization", "Bearer credential")
	secondResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(secondResponse, second)
	if secondResponse.Code != http.StatusOK || !fake.managerList.BeforeGrantedAt.Equal(grantedAt) || fake.managerList.BeforeUserID != target {
		t.Fatalf("manager cursor forwarding = %d query=%#v body=%s", secondResponse.Code, fake.managerList, secondResponse.Body.String())
	}
	malformed := httptest.NewRequest(http.MethodGet, "/api/v1/exams/"+view.Exam.ID.String()+"/managers?cursor=not-a-cursor", nil)
	malformed.Header.Set("Authorization", "Bearer credential")
	malformedResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(malformedResponse, malformed)
	assertHTTPProblem(t, malformedResponse, http.StatusBadRequest, "request.invalid")

	add := httptest.NewRequest(http.MethodPost, "/api/v1/exams/"+view.Exam.ID.String()+"/managers", bytes.NewReader([]byte(`{"user_id":"`+target.String()+`","expected_exam_revision":1}`)))
	add.Header.Set("Authorization", "Bearer credential")
	add.Header.Set("Content-Type", "application/json")
	add.Header.Set("Idempotency-Key", "add-manager")
	added := httptest.NewRecorder()
	httpAPI.ServeHTTP(added, add)
	if added.Code != http.StatusCreated || fake.managerCommand.UserID != target || fake.managerCommand.ExpectedExamRevision != 1 || fake.managerCommand.IdempotencyKey != "add-manager" {
		t.Fatalf("add = %d command=%#v body=%s", added.Code, fake.managerCommand, added.Body.String())
	}

	invalidDelete := httptest.NewRequest(http.MethodDelete, "/api/v1/exams/"+view.Exam.ID.String()+"/managers/"+target.String(), bytes.NewReader([]byte(`{"expected_exam_revision":2,"unexpected":true}`)))
	invalidDelete.Header.Set("Authorization", "Bearer credential")
	invalidDelete.Header.Set("Content-Type", "application/json")
	invalidDelete.Header.Set("Idempotency-Key", "remove-manager")
	invalid := httptest.NewRecorder()
	httpAPI.ServeHTTP(invalid, invalidDelete)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("strict DELETE = %d: %s", invalid.Code, invalid.Body.String())
	}
}

func TestExamManagerCursorIsVersionedAndStrict(t *testing.T) {
	t.Parallel()
	cursor := examManagerCursor{Version: 1, GrantedAt: "2026-08-14T10:00:00Z", UserID: model.NewUserID().String()}
	encodedCursor, err := encodeExamManagerCursor(cursor)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeExamManagerCursor(encodedCursor)
	if err != nil || decoded != cursor {
		t.Fatalf("cursor = %#v, %v", decoded, err)
	}
	unsupported := []byte(`{"version":2,"granted_at":"2026-08-14T10:00:00Z","user_id":"` + cursor.UserID + `"}`)
	if _, err := decodeExamManagerCursor(base64.RawURLEncoding.EncodeToString(unsupported)); err == nil {
		t.Fatal("unsupported Manager cursor accepted")
	}
}

func TestExamHTTPArchiveUsesRequiredIdempotentApplicationCommand(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := testExamHTTPPrincipal()
	view := testExamHTTPView(t, principal.UserID)
	archived := view.Exam
	_ = archived.Archive(time.Now().UTC().Add(time.Minute))
	fake := &examHTTPApplication{principal: principal, archived: archived}
	httpAPI := newFocusedResourceAPI(t, logger, fake, examResource(fake))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/exams/"+view.Exam.ID.String()+"/archive", bytes.NewReader([]byte(`{"expected_exam_revision":1}`)))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "archive-once")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("archive status = %d: %s", response.Code, response.Body.String())
	}
	if fake.archive.ExamID != view.Exam.ID || fake.archive.ExpectedExamRevision != 1 || fake.archive.IdempotencyKey != "archive-once" {
		t.Fatalf("archive command = %#v", fake.archive)
	}
}

func TestExamHTTPCreateRequiresIdempotencyKey(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := testExamHTTPPrincipal()
	fake := &examHTTPApplication{principal: principal, view: testExamHTTPView(t, principal.UserID)}
	httpAPI := newFocusedResourceAPI(t, logger, fake, examResource(fake))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/exams", bytes.NewReader([]byte(`{"academic_unit_id":"`+model.NewAcademicUnitID().String()+`","title":"Systems"}`)))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !bytes.Contains(response.Body.Bytes(), []byte(`"code":"idempotency.key_required"`)) {
		t.Fatalf("response = %d: %s", response.Code, response.Body.String())
	}
}

func TestExamHTTPPatchDraftPreservesOmittedAndExplicitEmptyFields(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := testExamHTTPPrincipal()
	view := testExamHTTPView(t, principal.UserID)
	fake := &examHTTPApplication{principal: principal, view: view}
	httpAPI := newFocusedResourceAPI(t, logger, fake, examResource(fake))
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/exams/"+view.Exam.ID.String()+"/draft", bytes.NewReader([]byte(`{"expected_draft_revision":1,"instructions_markdown":""}`)))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "clear-instructions")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("patch status = %d: %s", response.Code, response.Body.String())
	}
	if fake.edit.ExamID != view.Exam.ID || fake.edit.ExpectedDraftRevision != 1 || fake.edit.Title != nil || fake.edit.InstructionsMarkdown == nil || *fake.edit.InstructionsMarkdown != "" || fake.edit.IdempotencyKey != "clear-instructions" {
		t.Fatalf("edit command = %#v", fake.edit)
	}
}

func TestExamHTTPPatchDraftTreatsNullAsUnchangedAndRequiresAValue(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := testExamHTTPPrincipal()
	view := testExamHTTPView(t, principal.UserID)
	fake := &examHTTPApplication{principal: principal, view: view}
	httpAPI := newFocusedResourceAPI(t, logger, fake, examResource(fake))
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/exams/"+view.Exam.ID.String()+"/draft", bytes.NewReader([]byte(`{"expected_draft_revision":1,"title":null,"instructions_markdown":null}`)))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "null-fields")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !bytes.Contains(response.Body.Bytes(), []byte(`"code":"request.invalid"`)) {
		t.Fatalf("response = %d: %s", response.Code, response.Body.String())
	}
}

func TestExamHTTPPatchDraftRequiresIdempotencyKey(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := testExamHTTPPrincipal()
	view := testExamHTTPView(t, principal.UserID)
	fake := &examHTTPApplication{principal: principal, view: view}
	httpAPI := newFocusedResourceAPI(t, logger, fake, examResource(fake))
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/exams/"+view.Exam.ID.String()+"/draft", bytes.NewReader([]byte(`{"expected_draft_revision":1,"title":"Algorithms"}`)))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !bytes.Contains(response.Body.Bytes(), []byte(`"code":"idempotency.key_required"`)) {
		t.Fatalf("response = %d: %s", response.Code, response.Body.String())
	}
}

func TestExamHTTPConfigureDraftFocusLossUsesTypedApplicationCommand(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := testExamHTTPPrincipal()
	view := testExamHTTPView(t, principal.UserID)
	fake := &examHTTPApplication{principal: principal, view: view}
	httpAPI := newFocusedResourceAPI(t, logger, fake, examResource(fake))
	request := httptest.NewRequest(http.MethodPut, "/api/v1/exams/"+view.Exam.ID.String()+"/draft/policies/focus-loss", bytes.NewReader([]byte(`{"expected_draft_revision":1,"enabled":false,"minimum_duration_milliseconds":500,"incident_count":1,"window_milliseconds":10000,"outcome":"flag"}`)))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "focus-loss-policy")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("put status = %d: %s", response.Code, response.Body.String())
	}
	command := fake.configureFocusLoss
	if command.ExamID != view.Exam.ID || command.ExpectedDraftRevision != 1 || command.Enabled || command.MinimumDuration != 500*time.Millisecond || command.IncidentCount != 1 || command.Window != 10*time.Second || command.Outcome != model.IntegrityOutcomeFlag || command.IdempotencyKey != "focus-loss-policy" {
		t.Fatalf("configure focus loss command = %#v", command)
	}
}

func TestExamHTTPConfigureDraftFocusLossRequiresExplicitEnabledAndRejectsRawPolicy(t *testing.T) {
	t.Parallel()
	principal := testExamHTTPPrincipal()
	view := testExamHTTPView(t, principal.UserID)
	for name, body := range map[string]string{
		"enabled omitted":   `{"expected_draft_revision":1,"minimum_duration_milliseconds":500,"incident_count":1,"window_milliseconds":10000,"outcome":"flag"}`,
		"raw policy member": `{"expected_draft_revision":1,"enabled":true,"minimum_duration_milliseconds":500,"incident_count":1,"window_milliseconds":10000,"outcome":"flag","connection_loss":{"outcome":"flag"}}`,
		"duplicate enabled": `{"expected_draft_revision":1,"enabled":true,"enabled":false,"minimum_duration_milliseconds":500,"incident_count":1,"window_milliseconds":10000,"outcome":"flag"}`,
		"duplicate outcome": `{"expected_draft_revision":1,"enabled":true,"minimum_duration_milliseconds":500,"incident_count":1,"window_milliseconds":10000,"outcome":"flag","outcome":"flag_and_suspend"}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			logger, _ := newTestLogger(t)
			fake := &examHTTPApplication{principal: principal, view: view}
			httpAPI := newFocusedResourceAPI(t, logger, fake, examResource(fake))
			request := httptest.NewRequest(http.MethodPut, "/api/v1/exams/"+view.Exam.ID.String()+"/draft/policies/focus-loss", bytes.NewReader([]byte(body)))
			request.Header.Set("Authorization", "Bearer credential")
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Idempotency-Key", "invalid-focus-loss-policy")
			response := httptest.NewRecorder()
			httpAPI.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest || !bytes.Contains(response.Body.Bytes(), []byte(`"code":"request.invalid"`)) {
				t.Fatalf("response = %d: %s", response.Code, response.Body.String())
			}
			if fake.configureFocusLoss.ExamID.IsValid() {
				t.Fatalf("application received invalid request: %#v", fake.configureFocusLoss)
			}
		})
	}
}

func TestExamHTTPConfigureDraftExecutionProfileUsesTypedApplicationCommand(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := testExamHTTPPrincipal()
	view := testExamHTTPView(t, principal.UserID)
	fake := &examHTTPApplication{principal: principal, view: view}
	httpAPI := newFocusedResourceAPI(t, logger, fake, examResource(fake))
	request := httptest.NewRequest(http.MethodPut, "/api/v1/exams/"+view.Exam.ID.String()+"/draft/execution-profile", bytes.NewReader([]byte(`{"expected_draft_revision":1,"enabled":true,"image":"golang-1.24","network":"allowlist"}`)))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "execution-profile")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("put status = %d: %s", response.Code, response.Body.String())
	}
	command := fake.configureExecutionProfile
	if command.ExamID != view.Exam.ID || command.ExpectedDraftRevision != 1 || !command.Enabled || command.Image != "golang-1.24" || command.Network != model.ExecutionNetworkAllowlist || command.IdempotencyKey != "execution-profile" {
		t.Fatalf("configure execution profile command = %#v", command)
	}
}

func TestExamHTTPConfigureDraftExecutionProfileRejectsAmbiguousOrInvalidProfiles(t *testing.T) {
	t.Parallel()
	principal := testExamHTTPPrincipal()
	view := testExamHTTPView(t, principal.UserID)
	for name, body := range map[string]string{
		"enabled omitted":     `{"expected_draft_revision":1,"image":"golang-1.24","network":"none"}`,
		"duplicate enabled":   `{"expected_draft_revision":1,"enabled":true,"enabled":false,"image":"golang-1.24","network":"none"}`,
		"disabled capability": `{"expected_draft_revision":1,"enabled":false,"image":"golang-1.24","network":"none"}`,
		"unknown network":     `{"expected_draft_revision":1,"enabled":true,"image":"golang-1.24","network":"open"}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			logger, _ := newTestLogger(t)
			fake := &examHTTPApplication{principal: principal, view: view}
			httpAPI := newFocusedResourceAPI(t, logger, fake, examResource(fake))
			request := httptest.NewRequest(http.MethodPut, "/api/v1/exams/"+view.Exam.ID.String()+"/draft/execution-profile", bytes.NewReader([]byte(body)))
			request.Header.Set("Authorization", "Bearer credential")
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Idempotency-Key", "invalid-execution-profile")
			response := httptest.NewRecorder()
			httpAPI.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest || !bytes.Contains(response.Body.Bytes(), []byte(`"code":"request.invalid"`)) {
				t.Fatalf("response = %d: %s", response.Code, response.Body.String())
			}
			if fake.configureExecutionProfile.ExamID.IsValid() {
				t.Fatalf("application received invalid request: %#v", fake.configureExecutionProfile)
			}
		})
	}
}

func TestExamHTTPResponseIsBoundedAndContainsTypedPolicy(t *testing.T) {
	t.Parallel()
	view := testExamHTTPView(t, model.NewUserID())
	encoded, err := json.Marshal(examResponseFromView(view))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	if _, exists := document["managers"]; exists {
		t.Fatal("bounded view exposed manager collection")
	}
	exam := document["exam"].(map[string]any)
	for _, field := range []string{"created_at", "updated_at"} {
		value, ok := exam[field].(string)
		if !ok {
			t.Fatalf("%s = %#v, want RFC3339 string", field, exam[field])
		}
		if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
			t.Fatalf("parse %s: %v", field, err)
		}
	}
	if exam["archived_at"] != nil {
		t.Fatalf("archived_at = %#v, want null for active exam", exam["archived_at"])
	}
	for _, legacy := range []string{"create_at", "update_at", "delete_at"} {
		if _, exists := exam[legacy]; exists {
			t.Fatalf("response exposed legacy timestamp field %q", legacy)
		}
	}
	draft := document["draft"].(map[string]any)
	if _, err := time.Parse(time.RFC3339Nano, draft["updated_at"].(string)); err != nil {
		t.Fatalf("parse draft updated_at: %v", err)
	}
	policy := draft["policy"].(map[string]any)
	capacity := draft["capacity"].(map[string]any)
	if policy["schema_version"] != float64(1) || document["manager_count"] != float64(1) || draft["resource_count"] != float64(0) || draft["has_starter_workspace"] != false {
		t.Fatalf("response = %s", encoded)
	}
	if capacity["resource_maximum_count"] != float64(model.ExamResourceDefaultMaximumCount) ||
		capacity["workspace_maximum_entries"] != float64(model.ExamWorkspaceDefaultMaximumEntries) {
		t.Fatalf("response capacity = %#v", capacity)
	}
}

type examHTTPApplication struct {
	Application
	principal                 model.Principal
	view                      application.ExamView
	create                    application.CreateExamCommand
	edit                      application.EditExamDraftTextCommand
	configureFocusLoss        application.ConfigureExamDraftFocusLossCommand
	configureExecutionProfile application.ConfigureExamDraftExecutionProfileCommand
	executionImages           []application.ExamExecutionImage
	list                      application.ListExamsQuery
	catalog                   application.ExamCatalogPage
	archive                   application.ArchiveExamCommand
	archived                  model.Exam
	get                       application.GetExamQuery
	managerList               application.ListExamManagersQuery
	managerCommand            application.AddExamManagerCommand
	managerPage               application.ExamManagerPage
	managerChange             application.ExamManagerChange
}

func (a *examHTTPApplication) ListExamManagers(_ context.Context, _ application.Invocation, query application.ListExamManagersQuery) (application.ExamManagerPage, error) {
	a.managerList = query
	return a.managerPage, nil
}
func (a *examHTTPApplication) AddExamManager(_ context.Context, _ application.Invocation, command application.AddExamManagerCommand) (application.ExamManagerChange, error) {
	a.managerCommand = command
	return a.managerChange, nil
}
func (a *examHTTPApplication) RemoveExamManager(_ context.Context, _ application.Invocation, command application.RemoveExamManagerCommand) (application.ExamManagerChange, error) {
	a.managerCommand = command
	return a.managerChange, nil
}
func (a *examHTTPApplication) TransferExamOwnership(_ context.Context, _ application.Invocation, command application.TransferExamOwnershipCommand) (application.ExamManagerChange, error) {
	a.managerCommand = command
	return a.managerChange, nil
}

func (a *examHTTPApplication) ListExams(_ context.Context, _ application.Invocation, query application.ListExamsQuery) (application.ExamCatalogPage, error) {
	a.list = query
	return a.catalog, nil
}
func (a *examHTTPApplication) ArchiveExam(_ context.Context, _ application.Invocation, command application.ArchiveExamCommand) (model.Exam, error) {
	a.archive = command
	return a.archived, nil
}

func (a *examHTTPApplication) AuthenticateAccess(context.Context, string) (*model.Principal, error) {
	principal := a.principal
	return &principal, nil
}
func (a *examHTTPApplication) AuthenticateBearer(context.Context, string) (*model.Principal, error) {
	principal := a.principal
	return &principal, nil
}
func (a *examHTTPApplication) CreateExam(_ context.Context, _ application.Invocation, command application.CreateExamCommand) (application.ExamView, error) {
	a.create = command
	return a.view, nil
}
func (a *examHTTPApplication) GetExam(_ context.Context, _ application.Invocation, query application.GetExamQuery) (application.ExamView, error) {
	a.get = query
	return a.view, nil
}
func (a *examHTTPApplication) EditExamDraftText(_ context.Context, _ application.Invocation, command application.EditExamDraftTextCommand) (application.ExamView, error) {
	a.edit = command
	return a.view, nil
}
func (a *examHTTPApplication) ConfigureExamDraftFocusLoss(_ context.Context, _ application.Invocation, command application.ConfigureExamDraftFocusLossCommand) (application.ExamView, error) {
	a.configureFocusLoss = command
	return a.view, nil
}

func (a *examHTTPApplication) ConfigureExamDraftExecutionProfile(_ context.Context, _ application.Invocation, command application.ConfigureExamDraftExecutionProfileCommand) (application.ExamView, error) {
	a.configureExecutionProfile = command
	return a.view, nil
}

func (a *examHTTPApplication) ListExamExecutionImages(_ context.Context, _ application.Invocation, _ application.GetExamQuery) ([]application.ExamExecutionImage, error) {
	return append([]application.ExamExecutionImage(nil), a.executionImages...), nil
}

func testExamHTTPPrincipal() model.Principal {
	return model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientWeb, AuthenticatedAt: time.Now().UTC()}
}

func testExamHTTPView(t *testing.T, userID model.UserID) application.ExamView {
	t.Helper()
	at := time.Now().UTC()
	exam, err := model.NewExam(model.NewExamID(), model.NewAcademicUnitID(), userID, at)
	if err != nil {
		t.Fatal(err)
	}
	draft, err := model.NewExamDraft(exam.ID, "Systems", "Use **Go**.", model.DefaultExamPolicySet(), at)
	if err != nil {
		t.Fatal(err)
	}
	return application.ExamView{Exam: *exam, Draft: *draft, OwnerUserID: userID, ManagerCount: 1}
}

func assertExamHTTPResponse(t *testing.T, data []byte, view application.ExamView) {
	t.Helper()
	var got examResponse
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Exam.ID != view.Exam.ID.String() || got.Draft.Title != view.Draft.Title || got.OwnerUserID != view.OwnerUserID.String() || got.ManagerCount != 1 {
		t.Fatalf("response = %#v", got)
	}
}
