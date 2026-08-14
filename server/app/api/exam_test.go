// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	if policy["schema_version"] != float64(1) || document["manager_count"] != float64(1) || draft["resource_count"] != float64(0) || draft["has_starter_workspace"] != false {
		t.Fatalf("response = %s", encoded)
	}
}

type examHTTPApplication struct {
	Application
	principal          model.Principal
	view               application.ExamView
	create             application.CreateExamCommand
	edit               application.EditExamDraftTextCommand
	configureFocusLoss application.ConfigureExamDraftFocusLossCommand
	get                application.GetExamQuery
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
