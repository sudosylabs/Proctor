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
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app"
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
	require.Equal(t, http.StatusCreated, response.Code, "create status = %d: %s", response.Code, response.Body.String())
	require.Equal(t, view.Exam.AcademicUnitID, fake.create.AcademicUnitID)
	require.Equal(t, "Systems", fake.create.Title)
	require.Equal(t, "Use **Go**.", fake.create.InstructionsMarkdown)
	require.Equal(t, "exam-retry", fake.create.IdempotencyKey)
	assertExamHTTPResponse(t, response.Body.Bytes(), view)

	get := httptest.NewRequest(http.MethodGet, "/api/v1/exams/"+view.Exam.ID.String(), nil)
	get.Header.Set("Authorization", "Bearer credential")
	got := httptest.NewRecorder()
	httpAPI.ServeHTTP(got, get)
	require.Equal(t, http.StatusOK, got.Code, "get = %d query=%#v body=%s", got.Code, fake.get, got.Body.String())
	require.Equal(t, view.Exam.ID, fake.get.ExamID, "get = %d query=%#v body=%s", got.Code, fake.get, got.Body.String())
	assertExamHTTPResponse(t, got.Body.Bytes(), view)
}

func TestExamHTTPListUsesBoundedCatalogQueryAndSummary(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := testExamHTTPPrincipal()
	unitID, examID := model.NewAcademicUnitID(), model.NewExamID()
	fake := &examHTTPApplication{principal: principal, catalog: app.ExamCatalogPage{Items: []app.ExamSummary{{
		ID: examID, AcademicUnitID: unitID, CreatorUserID: principal.UserID, OwnerUserID: principal.UserID,
		Title: "Systems", UpdatedAt: time.Date(2026, 8, 14, 8, 0, 0, 0, time.UTC), Revision: 2, ManagerCount: 1,
	}}}}
	httpAPI := newFocusedResourceAPI(t, logger, fake, examResource(fake))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/exams?academic_unit_id="+unitID.String()+"&q=systems+design&archive_state=all&limit=25", nil)
	request.Header.Set("Authorization", "Bearer credential")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, "list status = %d: %s", response.Code, response.Body.String())
	require.Equal(t, unitID, fake.list.AcademicUnitID)
	require.Equal(t, "systems design", fake.list.Query)
	require.Equal(t, app.ExamArchiveAll, fake.list.ArchiveFilter)
	require.Equal(t, 25, fake.list.Limit)
	require.True(t, bytes.Contains(response.Body.Bytes(), []byte(`"title":"Systems"`)))
	require.False(t, bytes.Contains(response.Body.Bytes(), []byte("instructions_markdown")))
	require.False(t, bytes.Contains(response.Body.Bytes(), []byte("policy")))
	before := time.Date(2026, 8, 13, 7, 6, 5, 432123789, time.UTC)
	wantBefore := time.Date(2026, 8, 13, 7, 6, 5, 432123000, time.UTC)
	cursor, err := encodeExamCatalogCursor(examCatalogCursor{UpdatedAt: before.Format(time.RFC3339Nano), ExamID: examID.String()})
	require.NoError(t, err)
	second := httptest.NewRequest(http.MethodGet, "/api/v1/exams?cursor="+url.QueryEscape(cursor), nil)
	second.Header.Set("Authorization", "Bearer credential")
	secondResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(secondResponse, second)
	require.Equal(t, http.StatusOK, secondResponse.Code, "catalog cursor forwarding = %d query=%#v body=%s", secondResponse.Code, fake.list, secondResponse.Body.String())
	require.True(t, fake.list.BeforeUpdatedAt.Equal(wantBefore), "catalog cursor forwarding = %d query=%#v body=%s", secondResponse.Code, fake.list, secondResponse.Body.String())
	require.Equal(t, examID, fake.list.BeforeExamID, "catalog cursor forwarding = %d query=%#v body=%s", secondResponse.Code, fake.list, secondResponse.Body.String())
	malformed := httptest.NewRequest(http.MethodGet, "/api/v1/exams?cursor=not-a-cursor", nil)
	malformed.Header.Set("Authorization", "Bearer credential")
	malformedResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(malformedResponse, malformed)
	assertHTTPProblem(t, malformedResponse, http.StatusBadRequest, "request.invalid")
}

func TestExamCatalogCursorRoundTripsAndRejectsInvalidInput(t *testing.T) {
	t.Parallel()
	cursor := examCatalogCursor{UpdatedAt: "2026-08-14T08:00:00.123456789Z", ExamID: model.NewExamID().String()}
	encodedCursor, err := encodeExamCatalogCursor(cursor)
	require.NoError(t, err)
	got, err := decodeExamCatalogCursor(encodedCursor)
	require.NoError(t, err, "cursor")
	require.Equal(t, cursor, got, "cursor")
	_, err = decodeExamCatalogCursor("not-a-cursor")
	require.Error(t, err, "invalid cursor accepted")
	for _, version := range []string{"0", "1"} {
		versioned := []byte(`{"version":` + version + `,"updated_at":"2026-08-14T08:00:00Z","exam_id":"` + cursor.ExamID + `"}`)
		_, err = decodeExamCatalogCursor(base64.RawURLEncoding.EncodeToString(versioned))
		require.Error(t, err, "versioned cursor accepted")
	}
	encoded, _ := json.Marshal(cursor)
	_, err = decodeExamCatalogCursor(base64.RawURLEncoding.EncodeToString(append(encoded, []byte(`{}`)...)))
	require.Error(t, err, "cursor with trailing JSON accepted")
}

func TestExamManagerHTTPListAndMutationsUseStrictBoundedContracts(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := testExamHTTPPrincipal()
	view := testExamHTTPView(t, principal.UserID)
	target := model.NewUserID()
	grantedAt := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)
	manager, err := model.NewExamManager(view.Exam.ID, target, principal.UserID, grantedAt)
	require.NoError(t, err)
	changedExam := view.Exam
	changedExam.Revision = 2
	fake := &examHTTPApplication{principal: principal,
		managerPage:   app.ExamManagerPage{Items: []app.ExamManagerSummary{{Manager: *manager}}},
		managerChange: app.ExamManagerChange{Exam: &changedExam, Manager: manager},
	}
	httpAPI := newFocusedResourceAPI(t, logger, fake, examResource(fake))

	list := httptest.NewRequest(http.MethodGet, "/api/v1/exams/"+view.Exam.ID.String()+"/managers?limit=1", nil)
	list.Header.Set("Authorization", "Bearer credential")
	listed := httptest.NewRecorder()
	httpAPI.ServeHTTP(listed, list)
	require.Equal(t, http.StatusOK, listed.Code, "list = %d query=%#v body=%s", listed.Code, fake.managerList, listed.Body.String())
	require.Equal(t, 1, fake.managerList.Limit, "list = %d query=%#v body=%s", listed.Code, fake.managerList, listed.Body.String())
	require.True(t, bytes.Contains(listed.Body.Bytes(), []byte(target.String())), "list = %d query=%#v body=%s", listed.Code, fake.managerList, listed.Body.String())
	require.False(t, bytes.Contains(listed.Body.Bytes(), []byte("display_name")), "list = %d query=%#v body=%s", listed.Code, fake.managerList, listed.Body.String())
	cursor, err := encodeExamManagerCursor(examManagerCursor{GrantedAt: grantedAt.Format(time.RFC3339Nano), UserID: target.String()})
	require.NoError(t, err)
	second := httptest.NewRequest(http.MethodGet, "/api/v1/exams/"+view.Exam.ID.String()+"/managers?cursor="+url.QueryEscape(cursor), nil)
	second.Header.Set("Authorization", "Bearer credential")
	secondResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(secondResponse, second)
	require.Equal(t, http.StatusOK, secondResponse.Code, "manager cursor forwarding = %d query=%#v body=%s", secondResponse.Code, fake.managerList, secondResponse.Body.String())
	require.True(t, fake.managerList.BeforeGrantedAt.Equal(grantedAt), "manager cursor forwarding = %d query=%#v body=%s", secondResponse.Code, fake.managerList, secondResponse.Body.String())
	require.Equal(t, target, fake.managerList.BeforeUserID, "manager cursor forwarding = %d query=%#v body=%s", secondResponse.Code, fake.managerList, secondResponse.Body.String())
	malformed := httptest.NewRequest(http.MethodGet, "/api/v1/exams/"+view.Exam.ID.String()+"/managers?cursor=not-a-cursor", nil)
	malformed.Header.Set("Authorization", "Bearer credential")
	malformedResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(malformedResponse, malformed)
	assertHTTPProblem(t, malformedResponse, http.StatusBadRequest, "request.invalid")

	add := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/exams/"+view.Exam.ID.String()+"/managers", bytes.NewReader([]byte(`{"user_id":"`+target.String()+`","expected_revision":1}`)))
	add.Header.Set("Authorization", "Bearer credential")
	add.Header.Set("Content-Type", "application/json")
	add.Header.Set("Idempotency-Key", "add-manager")
	added := httptest.NewRecorder()
	httpAPI.ServeHTTP(added, add)
	require.Equal(t, http.StatusCreated, added.Code, "add = %d command=%#v body=%s", added.Code, fake.managerCommand, added.Body.String())
	require.Equal(t, target, fake.managerCommand.UserID, "add = %d command=%#v body=%s", added.Code, fake.managerCommand, added.Body.String())
	require.Equal(t, int64(1), fake.managerCommand.ExpectedExamRevision, "add = %d command=%#v body=%s", added.Code, fake.managerCommand, added.Body.String())
	require.Equal(t, "add-manager", fake.managerCommand.IdempotencyKey, "add = %d command=%#v body=%s", added.Code, fake.managerCommand, added.Body.String())

	invalidDelete := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/api/v1/exams/"+view.Exam.ID.String()+"/managers/"+target.String(), bytes.NewReader([]byte(`{"expected_revision":2,"unexpected":true}`)))
	invalidDelete.Header.Set("Authorization", "Bearer credential")
	invalidDelete.Header.Set("Content-Type", "application/json")
	invalidDelete.Header.Set("Idempotency-Key", "remove-manager")
	invalid := httptest.NewRecorder()
	httpAPI.ServeHTTP(invalid, invalidDelete)
	require.Equal(t, http.StatusBadRequest, invalid.Code, "strict DELETE = %d: %s", invalid.Code, invalid.Body.String())
}

func TestExamManagerCursorIsUnversionedAndStrict(t *testing.T) {
	t.Parallel()
	cursor := examManagerCursor{GrantedAt: "2026-08-14T10:00:00Z", UserID: model.NewUserID().String()}
	encodedCursor, err := encodeExamManagerCursor(cursor)
	require.NoError(t, err)
	decoded, err := decodeExamManagerCursor(encodedCursor)
	require.NoError(t, err, "cursor")
	require.Equal(t, cursor, decoded, "cursor")
	versioned := []byte(`{"version":1,"granted_at":"2026-08-14T10:00:00Z","user_id":"` + cursor.UserID + `"}`)
	_, err = decodeExamManagerCursor(base64.RawURLEncoding.EncodeToString(versioned))
	require.Error(t, err, "versioned Manager cursor accepted")
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
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/exams/"+view.Exam.ID.String()+"/archive", bytes.NewReader([]byte(`{"expected_revision":1}`)))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "archive-once")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, "archive status = %d: %s", response.Code, response.Body.String())
	require.Equal(t, view.Exam.ID, fake.archive.ExamID)
	require.Equal(t, int64(1), fake.archive.ExpectedExamRevision)
	require.Equal(t, "archive-once", fake.archive.IdempotencyKey)
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
	require.Equal(t, http.StatusBadRequest, response.Code, "response = %d: %s", response.Code, response.Body.String())
	require.True(t, bytes.Contains(response.Body.Bytes(), []byte(`"code":"idempotency.key_required"`)), "response = %d: %s", response.Code, response.Body.String())
}

func TestExamHTTPPatchDraftPreservesOmittedAndExplicitEmptyFields(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := testExamHTTPPrincipal()
	view := testExamHTTPView(t, principal.UserID)
	fake := &examHTTPApplication{principal: principal, view: view}
	httpAPI := newFocusedResourceAPI(t, logger, fake, examResource(fake))
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPatch, "/api/v1/exams/"+view.Exam.ID.String()+"/draft", bytes.NewReader([]byte(`{"expected_revision":1,"instructions_markdown":""}`)))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "clear-instructions")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, "patch status = %d: %s", response.Code, response.Body.String())
	require.Equal(t, view.Exam.ID, fake.edit.ExamID)
	require.Equal(t, int64(1), fake.edit.ExpectedDraftRevision)
	require.Nil(t, fake.edit.Title)
	require.NotNil(t, fake.edit.InstructionsMarkdown)
	require.Empty(t, *fake.edit.InstructionsMarkdown)
	require.Equal(t, "clear-instructions", fake.edit.IdempotencyKey)
}

func TestExamHTTPPatchDraftTreatsNullAsUnchangedAndRequiresAValue(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := testExamHTTPPrincipal()
	view := testExamHTTPView(t, principal.UserID)
	fake := &examHTTPApplication{principal: principal, view: view}
	httpAPI := newFocusedResourceAPI(t, logger, fake, examResource(fake))
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPatch, "/api/v1/exams/"+view.Exam.ID.String()+"/draft", bytes.NewReader([]byte(`{"expected_revision":1,"title":null,"instructions_markdown":null}`)))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "null-fields")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusBadRequest, response.Code, "response = %d: %s", response.Code, response.Body.String())
	require.True(t, bytes.Contains(response.Body.Bytes(), []byte(`"code":"request.invalid"`)), "response = %d: %s", response.Code, response.Body.String())
}

func TestExamHTTPPatchDraftRequiresIdempotencyKey(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := testExamHTTPPrincipal()
	view := testExamHTTPView(t, principal.UserID)
	fake := &examHTTPApplication{principal: principal, view: view}
	httpAPI := newFocusedResourceAPI(t, logger, fake, examResource(fake))
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPatch, "/api/v1/exams/"+view.Exam.ID.String()+"/draft", bytes.NewReader([]byte(`{"expected_revision":1,"title":"Algorithms"}`)))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusBadRequest, response.Code, "response = %d: %s", response.Code, response.Body.String())
	require.True(t, bytes.Contains(response.Body.Bytes(), []byte(`"code":"idempotency.key_required"`)), "response = %d: %s", response.Code, response.Body.String())
}

func TestExamHTTPConfigureDraftFocusLossUsesTypedApplicationCommand(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := testExamHTTPPrincipal()
	view := testExamHTTPView(t, principal.UserID)
	fake := &examHTTPApplication{principal: principal, view: view}
	httpAPI := newFocusedResourceAPI(t, logger, fake, examResource(fake))
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/api/v1/exams/"+view.Exam.ID.String()+"/draft/policies/focus-loss", bytes.NewReader([]byte(`{"expected_revision":1,"enabled":false,"minimum_duration_milliseconds":500,"incident_count":1,"window_milliseconds":10000,"outcome":"flag"}`)))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "focus-loss-policy")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, "put status = %d: %s", response.Code, response.Body.String())
	command := fake.configureFocusLoss
	require.Equal(t, view.Exam.ID, command.ExamID)
	require.Equal(t, int64(1), command.ExpectedDraftRevision)
	require.False(t, command.Enabled)
	require.Equal(t, 500*time.Millisecond, command.MinimumDuration)
	require.Equal(t, 1, command.IncidentCount)
	require.Equal(t, 10*time.Second, command.Window)
	require.Equal(t, model.IntegrityOutcomeFlag, command.Outcome)
	require.Equal(t, "focus-loss-policy", command.IdempotencyKey)
}

func TestExamHTTPConfigureDraftFocusLossRequiresExplicitEnabledAndRejectsRawPolicy(t *testing.T) {
	t.Parallel()
	principal := testExamHTTPPrincipal()
	view := testExamHTTPView(t, principal.UserID)
	for name, body := range map[string]string{
		"enabled omitted":   `{"expected_revision":1,"minimum_duration_milliseconds":500,"incident_count":1,"window_milliseconds":10000,"outcome":"flag"}`,
		"raw policy member": `{"expected_revision":1,"enabled":true,"minimum_duration_milliseconds":500,"incident_count":1,"window_milliseconds":10000,"outcome":"flag","connection_loss":{"outcome":"flag"}}`,
		"duplicate enabled": `{"expected_revision":1,"enabled":true,"enabled":false,"minimum_duration_milliseconds":500,"incident_count":1,"window_milliseconds":10000,"outcome":"flag"}`,
		"duplicate outcome": `{"expected_revision":1,"enabled":true,"minimum_duration_milliseconds":500,"incident_count":1,"window_milliseconds":10000,"outcome":"flag","outcome":"flag_and_suspend"}`,
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
			require.Equal(t, http.StatusBadRequest, response.Code, "response = %d: %s", response.Code, response.Body.String())
			require.True(t, bytes.Contains(response.Body.Bytes(), []byte(`"code":"request.invalid"`)), "response = %d: %s", response.Code, response.Body.String())
			require.False(t, fake.configureFocusLoss.ExamID.IsValid())
		})
	}
}

func TestExamHTTPResponseIsBoundedAndContainsTypedPolicy(t *testing.T) {
	t.Parallel()
	view := testExamHTTPView(t, model.NewUserID())
	encoded, err := json.Marshal(examResponseFromView(view))
	require.NoError(t, err)
	var document map[string]any
	require.NoError(t, json.Unmarshal(encoded, &document))
	_, exists := document["managers"]
	require.False(t, exists, "bounded view exposed manager collection")
	exam := document["exam"].(map[string]any)
	for _, field := range []string{"created_at", "updated_at"} {
		value, ok := exam[field].(string)
		require.True(t, ok, "%s = %#v, want RFC3339 string", field, exam[field])
		_, err = time.Parse(time.RFC3339Nano, value)
		require.NoError(t, err, "parse %s", field)
	}
	_, present := exam["archived_at"]
	require.False(t, present, "archived_at = %#v, want omitted for active exam", exam["archived_at"])
	for _, legacy := range []string{"create_at", "update_at", "delete_at"} {
		_, exists := exam[legacy]
		require.False(t, exists, "response exposed legacy timestamp field %q", legacy)
	}
	draft := document["draft"].(map[string]any)
	_, err = time.Parse(time.RFC3339Nano, draft["updated_at"].(string))
	require.NoError(t, err)
	policy := draft["policy"].(map[string]any)
	capacity := draft["capacity"].(map[string]any)
	require.NotNil(t, policy["native"])
	require.Equal(t, float64(1), document["manager_count"])
	require.Equal(t, float64(0), draft["resource_count"])
	require.Equal(t, false, draft["has_starter_workspace"])
	require.Equal(t, float64(model.ExamResourceDefaultMaximumCount), capacity["resource_maximum_count"])
	require.Equal(t, float64(model.ExamWorkspaceDefaultMaximumEntries), capacity["workspace_maximum_entries"])
}

type examHTTPApplication struct {
	Application
	principal              model.Principal
	view                   app.ExamView
	create                 app.CreateExamCommand
	edit                   app.EditExamDraftTextCommand
	configureFocusLoss     app.ConfigureExamDraftFocusLossCommand
	configureNativePolicy  app.ConfigureExamDraftNativePolicyCommand
	configureBrowserPolicy app.ConfigureExamDraftBrowserPolicyCommand
	list                   app.ListExamsQuery
	catalog                app.ExamCatalogPage
	archive                app.ArchiveExamCommand
	archived               model.Exam
	get                    app.GetExamQuery
	managerList            app.ListExamManagersQuery
	managerCommand         app.AddExamManagerCommand
	managerPage            app.ExamManagerPage
	managerChange          app.ExamManagerChange
}

func (a *examHTTPApplication) ListExamManagers(_ context.Context, _ app.Invocation, query app.ListExamManagersQuery) (app.ExamManagerPage, error) {
	a.managerList = query
	return a.managerPage, nil
}
func (a *examHTTPApplication) AddExamManager(_ context.Context, _ app.Invocation, command app.AddExamManagerCommand) (app.ExamManagerChange, error) {
	a.managerCommand = command
	return a.managerChange, nil
}
func (a *examHTTPApplication) RemoveExamManager(_ context.Context, _ app.Invocation, command app.RemoveExamManagerCommand) (app.ExamManagerChange, error) {
	a.managerCommand = command
	return a.managerChange, nil
}
func (a *examHTTPApplication) TransferExamOwnership(_ context.Context, _ app.Invocation, command app.TransferExamOwnershipCommand) (app.ExamManagerChange, error) {
	a.managerCommand = command
	return a.managerChange, nil
}

func (a *examHTTPApplication) ListExams(_ context.Context, _ app.Invocation, query app.ListExamsQuery) (app.ExamCatalogPage, error) {
	a.list = query
	return a.catalog, nil
}
func (a *examHTTPApplication) ArchiveExam(_ context.Context, _ app.Invocation, command app.ArchiveExamCommand) (model.Exam, error) {
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
func (a *examHTTPApplication) CreateExam(_ context.Context, _ app.Invocation, command app.CreateExamCommand) (app.ExamView, error) {
	a.create = command
	return a.view, nil
}
func (a *examHTTPApplication) GetExam(_ context.Context, _ app.Invocation, query app.GetExamQuery) (app.ExamView, error) {
	a.get = query
	return a.view, nil
}
func (a *examHTTPApplication) EditExamDraftText(_ context.Context, _ app.Invocation, command app.EditExamDraftTextCommand) (app.ExamView, error) {
	a.edit = command
	return a.view, nil
}
func (a *examHTTPApplication) ConfigureExamDraftNativePolicy(_ context.Context, _ app.Invocation, command app.ConfigureExamDraftNativePolicyCommand) (app.ExamView, error) {
	a.configureNativePolicy = command
	return a.view, nil
}

func (a *examHTTPApplication) ConfigureExamDraftFocusLoss(_ context.Context, _ app.Invocation, command app.ConfigureExamDraftFocusLossCommand) (app.ExamView, error) {
	a.configureFocusLoss = command
	return a.view, nil
}

func (a *examHTTPApplication) ConfigureExamDraftBrowserPolicy(_ context.Context, _ app.Invocation, command app.ConfigureExamDraftBrowserPolicyCommand) (app.ExamView, error) {
	a.configureBrowserPolicy = command
	return a.view, nil
}

func testExamHTTPPrincipal() model.Principal {
	return model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewID()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientWeb, AuthenticatedAt: time.Now().UTC()}
}

func testExamHTTPView(t *testing.T, userID model.UserID) app.ExamView {
	t.Helper()
	at := time.Now().UTC()
	exam, err := model.NewExam(model.NewExamID(), model.NewAcademicUnitID(), userID, at)
	require.NoError(t, err)
	draft, err := model.NewExamDraft(exam.ID, "Systems", "Use **Go**.", model.DefaultExamPolicySet(), at)
	require.NoError(t, err)
	return app.ExamView{Exam: *exam, Draft: *draft, OwnerUserID: userID, ManagerCount: 1}
}

func assertExamHTTPResponse(t *testing.T, data []byte, view app.ExamView) {
	t.Helper()
	var got examResponse
	require.NoError(t, json.Unmarshal(data, &got))
	require.Equal(t, view.Exam.ID.String(), got.Exam.ID)
	require.Equal(t, view.Draft.Title, got.Draft.Title)
	require.Equal(t, view.OwnerUserID.String(), got.OwnerUserID)
	require.Equal(t, 1, got.ManagerCount)
}
