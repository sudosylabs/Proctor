// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

func operatorTestPrincipal() model.Principal {
	return model.Principal{
		UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID:   model.PrincipalCredentialID(model.NewId()),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password",
		AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType:             model.SessionClientCLI, AuthenticatedAt: time.Now(),
	}
}

func serveOperatorRequest(api *API, method, path, body string, authenticated bool) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if authenticated {
		request.Header.Set("Authorization", "Bearer credential")
	}
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	return response
}

type focusedRoleApplication struct {
	roles  []*model.Role
	create application.CreateRoleCommand
}

func (fake *focusedRoleApplication) ListRoles(context.Context, application.Invocation, application.ListRolesQuery) ([]*model.Role, error) {
	return fake.roles, nil
}
func (fake *focusedRoleApplication) GetRole(context.Context, application.Invocation, application.GetRoleQuery) (*model.Role, error) {
	return fake.roles[0], nil
}
func (fake *focusedRoleApplication) CreateRole(_ context.Context, _ application.Invocation, command application.CreateRoleCommand) (*model.Role, error) {
	fake.create = command
	return fake.roles[0], nil
}
func (fake *focusedRoleApplication) UpdateRole(context.Context, application.Invocation, application.UpdateRoleCommand) (*model.Role, error) {
	return fake.roles[0], nil
}
func (*focusedRoleApplication) ArchiveRole(context.Context, application.Invocation, application.ArchiveRoleCommand) error {
	return nil
}

func TestRoleResourceUsesKernelAuthenticationAndTypedOperation(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := operatorTestPrincipal()
	role := &model.Role{ID: model.NewRoleID(), Name: "teacher", DisplayName: "Teacher", Permissions: []string{}}
	roles := &focusedRoleApplication{roles: []*model.Role{role}}
	httpAPI := newFocusedResourceAPI(t, logger, &academicUnitHTTPApplication{principal: principal}, roleResource(roles))

	unauthenticated := serveOperatorRequest(httpAPI, http.MethodGet, "/api/v1/roles", "", false)
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d: %s", unauthenticated.Code, unauthenticated.Body.String())
	}
	authenticated := serveOperatorRequest(httpAPI, http.MethodPost, "/api/v1/roles", `{"name":"teacher","display_name":"Teacher","permissions":[]}`, true)
	if authenticated.Code != http.StatusCreated || roles.create.Name != "teacher" {
		t.Fatalf("create status/command = %d/%#v: %s", authenticated.Code, roles.create, authenticated.Body.String())
	}
}

type focusedRoleBindingApplication struct {
	binding *model.RoleBinding
	end     application.EndRoleBindingCommand
}

func (*focusedRoleBindingApplication) ListRoleBindings(context.Context, application.Invocation, application.ListRoleBindingsQuery) ([]*model.RoleBinding, error) {
	return []*model.RoleBinding{}, nil
}
func (fake *focusedRoleBindingApplication) CreateRoleBinding(context.Context, application.Invocation, application.CreateRoleBindingCommand) (*model.RoleBinding, error) {
	return fake.binding, nil
}
func (fake *focusedRoleBindingApplication) EndRoleBinding(_ context.Context, _ application.Invocation, command application.EndRoleBindingCommand) (*model.RoleBinding, error) {
	fake.end = command
	return fake.binding, nil
}

func TestRoleBindingResourceOwnsPathCommandAndAuthentication(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := operatorTestPrincipal()
	bindingID := model.NewRoleBindingID()
	bindings := &focusedRoleBindingApplication{binding: &model.RoleBinding{ID: bindingID}}
	httpAPI := newFocusedResourceAPI(t, logger, &academicUnitHTTPApplication{principal: principal}, roleBindingResource(bindings))
	path := "/api/v1/role-bindings/" + bindingID.String()

	unauthenticated := serveOperatorRequest(httpAPI, http.MethodDelete, path, "", false)
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d: %s", unauthenticated.Code, unauthenticated.Body.String())
	}
	weakAssurance := serveOperatorRequest(httpAPI, http.MethodDelete, path, "", true)
	if weakAssurance.Code != http.StatusForbidden || !strings.Contains(weakAssurance.Body.String(), "authentication.strong_required") {
		t.Fatalf("weak-assurance status = %d: %s", weakAssurance.Code, weakAssurance.Body.String())
	}
	principal.AuthenticationStrength = model.AuthenticationMultiFactor
	httpAPI = newFocusedResourceAPI(t, logger, &academicUnitHTTPApplication{principal: principal}, roleBindingResource(bindings))
	ended := serveOperatorRequest(httpAPI, http.MethodDelete, path, "", true)
	if ended.Code != http.StatusOK || bindings.end.ID != bindingID.String() {
		t.Fatalf("end status/command = %d/%#v: %s", ended.Code, bindings.end, ended.Body.String())
	}
}

type focusedAuditApplication struct {
	query  application.ListAuditEventsQuery
	events []*model.AuditEvent
	err    error
}

func (fake *focusedAuditApplication) ListAuditEvents(_ context.Context, _ application.Invocation, query application.ListAuditEventsQuery) ([]*model.AuditEvent, error) {
	fake.query = query
	return fake.events, fake.err
}

func TestAuditResourcePreservesOpaqueCursorAndSafeErrors(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := operatorTestPrincipal()
	last := &model.AuditEvent{ID: model.NewAuditEventID(), CreatedAt: model.TimeFromMillis(100), Action: "role.list", Resource: model.Resource{Type: model.ResourceInstitution, ID: model.NewId()}, Status: model.AuditStatusSuccess}
	audits := &focusedAuditApplication{events: []*model.AuditEvent{last}}
	httpAPI := newFocusedResourceAPI(t, logger, &academicUnitHTTPApplication{principal: principal}, auditResource(audits))

	response := serveOperatorRequest(httpAPI, http.MethodGet, "/api/v1/audits?limit=1", "", true)
	if response.Code != http.StatusOK || audits.query.Limit != 1 {
		t.Fatalf("list status/query = %d/%#v: %s", response.Code, audits.query, response.Body.String())
	}
	var page auditListResponse
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.NextCursor == "" {
		t.Fatal("full audit page did not return an opaque cursor")
	}
	audits.events = nil
	second := serveOperatorRequest(httpAPI, http.MethodGet, "/api/v1/audits?limit=1&cursor="+url.QueryEscape(page.NextCursor), "", true)
	if second.Code != http.StatusOK || audits.query.BeforeTime != model.MillisFromTime(last.CreatedAt) || audits.query.BeforeID != last.ID.String() {
		t.Fatalf("second page status/query = %d/%#v: %s", second.Code, audits.query, second.Body.String())
	}
	malformed := serveOperatorRequest(httpAPI, http.MethodGet, "/api/v1/audits?cursor=not-a-cursor", "", true)
	assertHTTPProblem(t, malformed, http.StatusBadRequest, "audit.query.invalid")

	audits.err = application.NewError("audit.unavailable")
	errorResponse := serveOperatorRequest(httpAPI, http.MethodGet, "/api/v1/audits", "", true)
	var problem Problem
	if err := json.Unmarshal(errorResponse.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if errorResponse.Code != http.StatusInternalServerError || problem.Code != "audit.unavailable" ||
		problem.Detail != "An unexpected error occurred." {
		t.Fatalf("unsafe audit error response = %d: %s", errorResponse.Code, errorResponse.Body.String())
	}
}

type focusedJobApplication struct {
	listQuery    application.ListJobsQuery
	attemptQuery application.ListJobAttemptsQuery
	cancel       application.CancelJobCommand
	retry        application.RetryJobCommand
	view         application.JobView
}

func (fake *focusedJobApplication) ListJobs(_ context.Context, _ application.Invocation, query application.ListJobsQuery) (application.JobPage, error) {
	fake.listQuery = query
	return application.JobPage{Items: []application.JobView{fake.view}}, nil
}
func (fake *focusedJobApplication) GetJob(context.Context, application.Invocation, application.GetJobQuery) (application.JobView, error) {
	return fake.view, nil
}
func (fake *focusedJobApplication) ListJobAttempts(_ context.Context, _ application.Invocation, query application.ListJobAttemptsQuery) (application.JobAttemptPage, error) {
	fake.attemptQuery = query
	return application.JobAttemptPage{}, nil
}
func (fake *focusedJobApplication) CancelJob(_ context.Context, _ application.Invocation, command application.CancelJobCommand) (application.JobView, error) {
	fake.cancel = command
	return fake.view, nil
}
func (fake *focusedJobApplication) RetryJob(_ context.Context, _ application.Invocation, command application.RetryJobCommand) (application.JobView, error) {
	fake.retry = command
	return fake.view, nil
}

func TestJobResourcePreservesPrivateProjectionAndBoundedControls(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := operatorTestPrincipal()
	now := time.Now()
	jobID := model.NewJobID()
	jobs := &focusedJobApplication{view: application.JobView{
		ID: jobID, Type: model.JobTypeProfilePictureGenerateDefault, Status: model.JobStatusRunning,
		CreatedAt: now, UpdatedAt: now, AvailableAt: now, AttemptCount: 1, MaximumAttempts: 3, Revision: 2,
	}}
	httpAPI := newFocusedResourceAPI(t, logger, &academicUnitHTTPApplication{principal: principal}, jobResource(jobs))

	list := serveOperatorRequest(httpAPI, http.MethodGet, "/api/v1/jobs?limit=1", "", true)
	if list.Code != http.StatusOK || jobs.listQuery.Limit != 1 {
		t.Fatalf("list status/query = %d/%#v: %s", list.Code, jobs.listQuery, list.Body.String())
	}
	for _, private := range []string{"command", "checkpoint", "claim_owner", "claim_token", "internal_error"} {
		if strings.Contains(list.Body.String(), private) {
			t.Fatalf("job list exposed %q: %s", private, list.Body.String())
		}
	}
	cursor, err := encodeJobCursor(jobCursor{CreatedAt: model.MillisFromTime(now), ID: jobID.String()})
	if err != nil {
		t.Fatal(err)
	}
	page := serveOperatorRequest(httpAPI, http.MethodGet, "/api/v1/jobs?cursor="+url.QueryEscape(cursor), "", true)
	if page.Code != http.StatusOK || !jobs.listQuery.BeforeCreatedAt.Equal(model.TimeFromMillis(model.MillisFromTime(now))) || jobs.listQuery.BeforeID != jobID {
		t.Fatalf("job cursor forwarding = %d query=%#v body=%s", page.Code, jobs.listQuery, page.Body.String())
	}
	malformed := serveOperatorRequest(httpAPI, http.MethodGet, "/api/v1/jobs?cursor=not-a-cursor", "", true)
	assertHTTPProblem(t, malformed, http.StatusBadRequest, "job.query.invalid")

	attemptCursor, err := encodeJobAttemptCursor(7)
	if err != nil {
		t.Fatal(err)
	}
	attempts := serveOperatorRequest(httpAPI, http.MethodGet, "/api/v1/jobs/"+jobID.String()+"/attempts?cursor="+url.QueryEscape(attemptCursor), "", true)
	if attempts.Code != http.StatusOK || jobs.attemptQuery.JobID != jobID || jobs.attemptQuery.BeforeNumber != 7 {
		t.Fatalf("job attempt cursor forwarding = %d query=%#v body=%s", attempts.Code, jobs.attemptQuery, attempts.Body.String())
	}
	malformedAttempts := serveOperatorRequest(httpAPI, http.MethodGet, "/api/v1/jobs/"+jobID.String()+"/attempts?cursor=not-a-cursor", "", true)
	assertHTTPProblem(t, malformedAttempts, http.StatusBadRequest, "job.query.invalid")
	for _, action := range []string{"cancel", "retry"} {
		response := serveOperatorRequest(httpAPI, http.MethodPost, "/api/v1/jobs/"+jobID.String()+"/"+action, "", true)
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d: %s", action, response.Code, response.Body.String())
		}
	}
	if jobs.cancel.ID != jobID || jobs.retry.ID != jobID {
		t.Fatalf("control commands = %#v/%#v", jobs.cancel, jobs.retry)
	}
	create := serveOperatorRequest(httpAPI, http.MethodPost, "/api/v1/jobs", `{}`, true)
	if create.Code != http.StatusMethodNotAllowed {
		t.Fatalf("generic job create status = %d: %s", create.Code, create.Body.String())
	}
}
