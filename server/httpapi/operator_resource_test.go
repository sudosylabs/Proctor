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

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/model"
)

func operatorTestPrincipal() model.Principal {
	return model.Principal{
		UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID:   model.PrincipalCredentialID(model.NewID()),
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
	create app.CreateRoleCommand
}

func (f *focusedRoleApplication) ListRoles(context.Context, app.Invocation, app.ListRolesQuery) ([]*model.Role, error) {
	return f.roles, nil
}
func (f *focusedRoleApplication) GetRole(context.Context, app.Invocation, app.GetRoleQuery) (*model.Role, error) {
	return f.roles[0], nil
}
func (f *focusedRoleApplication) CreateRole(_ context.Context, _ app.Invocation, command app.CreateRoleCommand) (*model.Role, error) {
	f.create = command
	return f.roles[0], nil
}
func (f *focusedRoleApplication) UpdateRole(context.Context, app.Invocation, app.UpdateRoleCommand) (*model.Role, error) {
	return f.roles[0], nil
}
func (*focusedRoleApplication) ArchiveRole(context.Context, app.Invocation, app.ArchiveRoleCommand) error {
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
	require.Equal(t, http.StatusUnauthorized, unauthenticated.Code, "unauthenticated status = %d: %s", unauthenticated.Code, unauthenticated.Body.String())
	authenticated := serveOperatorRequest(httpAPI, http.MethodPost, "/api/v1/roles", `{"name":"teacher","display_name":"Teacher","permissions":[]}`, true)
	require.Equal(t, http.StatusCreated, authenticated.Code, "create status/command = %d/%#v: %s", authenticated.Code, roles.create, authenticated.Body.String())
	require.Equal(t, "teacher", roles.create.Name, "create status/command = %d/%#v: %s", authenticated.Code, roles.create, authenticated.Body.String())
}

type focusedRoleBindingApplication struct {
	binding *model.RoleBinding
	end     app.EndRoleBindingCommand
}

func (*focusedRoleBindingApplication) ListRoleBindings(context.Context, app.Invocation, app.ListRoleBindingsQuery) ([]*model.RoleBinding, error) {
	return []*model.RoleBinding{}, nil
}
func (f *focusedRoleBindingApplication) CreateRoleBinding(context.Context, app.Invocation, app.CreateRoleBindingCommand) (*model.RoleBinding, error) {
	return f.binding, nil
}
func (f *focusedRoleBindingApplication) EndRoleBinding(_ context.Context, _ app.Invocation, command app.EndRoleBindingCommand) (*model.RoleBinding, error) {
	f.end = command
	return f.binding, nil
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
	require.Equal(t, http.StatusUnauthorized, unauthenticated.Code, "unauthenticated status = %d: %s", unauthenticated.Code, unauthenticated.Body.String())
	weakAssurance := serveOperatorRequest(httpAPI, http.MethodDelete, path, "", true)
	require.Equal(t, http.StatusForbidden, weakAssurance.Code, "weak-assurance status")
	require.Contains(t, weakAssurance.Body.String(), "authentication.strong_required", "weak-assurance status")
	principal.AuthenticationStrength = model.AuthenticationMultiFactor
	httpAPI = newFocusedResourceAPI(t, logger, &academicUnitHTTPApplication{principal: principal}, roleBindingResource(bindings))
	ended := serveOperatorRequest(httpAPI, http.MethodDelete, path, "", true)
	require.Equal(t, http.StatusOK, ended.Code, "end status/command = %d/%#v: %s", ended.Code, bindings.end, ended.Body.String())
	require.Equal(t, bindingID, bindings.end.ID, "end status/command = %d/%#v: %s", ended.Code, bindings.end, ended.Body.String())
}

type focusedAuditApplication struct {
	query  app.ListAuditEventsQuery
	events []*model.AuditEvent
	err    error
}

func (f *focusedAuditApplication) ListAuditEvents(_ context.Context, _ app.Invocation, query app.ListAuditEventsQuery) ([]*model.AuditEvent, error) {
	f.query = query
	return f.events, f.err
}

func TestAuditResourcePreservesOpaqueCursorAndSafeErrors(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := operatorTestPrincipal()
	last := &model.AuditEvent{ID: model.NewAuditEventID(), CreatedAt: model.TimeFromMillis(100), Action: "role.list", Resource: model.Resource{Type: model.ResourceInstitution, ID: model.NewID()}, Status: model.AuditStatusSuccess}
	audits := &focusedAuditApplication{events: []*model.AuditEvent{last}}
	httpAPI := newFocusedResourceAPI(t, logger, &academicUnitHTTPApplication{principal: principal}, auditResource(audits))

	response := serveOperatorRequest(httpAPI, http.MethodGet, "/api/v1/audits?limit=1", "", true)
	require.Equal(t, http.StatusOK, response.Code, "list status/query = %d/%#v: %s", response.Code, audits.query, response.Body.String())
	require.Equal(t, 1, audits.query.Limit, "list status/query = %d/%#v: %s", response.Code, audits.query, response.Body.String())
	var page listResponse[auditEventResponse]
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page))
	require.NotEmpty(t, page.NextCursor, "full audit page did not return an opaque cursor")
	audits.events = nil
	second := serveOperatorRequest(httpAPI, http.MethodGet, "/api/v1/audits?limit=1&cursor="+url.QueryEscape(page.NextCursor), "", true)
	require.Equal(t, http.StatusOK, second.Code, "second page status/query = %d/%#v: %s", second.Code, audits.query, second.Body.String())
	require.True(t, last.CreatedAt.Equal(audits.query.BeforeCreatedAt), "second page status/query = %d/%#v: %s", second.Code, audits.query, second.Body.String())
	require.Equal(t, last.ID.String(), audits.query.BeforeID, "second page status/query = %d/%#v: %s", second.Code, audits.query, second.Body.String())
	malformed := serveOperatorRequest(httpAPI, http.MethodGet, "/api/v1/audits?cursor=not-a-cursor", "", true)
	assertHTTPProblem(t, malformed, http.StatusBadRequest, "request.invalid")

	audits.err = apperror.New("audit.unavailable")
	errorResponse := serveOperatorRequest(httpAPI, http.MethodGet, "/api/v1/audits", "", true)
	var problem Problem
	require.NoError(t, json.Unmarshal(errorResponse.Body.Bytes(), &problem))
	require.Equal(t, http.StatusServiceUnavailable, errorResponse.Code, "unsafe audit error response = %d: %s", errorResponse.Code, errorResponse.Body.String())
	require.Equal(t, "audit.unavailable", problem.Code, "unsafe audit error response = %d: %s", errorResponse.Code, errorResponse.Body.String())
	require.Equal(t, "The service is temporarily unavailable.", problem.Detail, "unsafe audit error response = %d: %s", errorResponse.Code, errorResponse.Body.String())
}

type focusedJobApplication struct {
	listQuery    app.ListJobsQuery
	attemptQuery app.ListJobAttemptsQuery
	cancel       app.CancelJobCommand
	retry        app.RetryJobCommand
	view         app.JobView
}

func (f *focusedJobApplication) ListJobs(_ context.Context, _ app.Invocation, query app.ListJobsQuery) (app.JobPage, error) {
	f.listQuery = query
	return app.JobPage{Items: []app.JobView{f.view}}, nil
}
func (f *focusedJobApplication) GetJob(context.Context, app.Invocation, app.GetJobQuery) (app.JobView, error) {
	return f.view, nil
}
func (f *focusedJobApplication) ListJobAttempts(_ context.Context, _ app.Invocation, query app.ListJobAttemptsQuery) (app.JobAttemptPage, error) {
	f.attemptQuery = query
	return app.JobAttemptPage{}, nil
}
func (f *focusedJobApplication) CancelJob(_ context.Context, _ app.Invocation, command app.CancelJobCommand) (app.JobView, error) {
	f.cancel = command
	return f.view, nil
}
func (f *focusedJobApplication) RetryJob(_ context.Context, _ app.Invocation, command app.RetryJobCommand) (app.JobView, error) {
	f.retry = command
	return f.view, nil
}

func TestJobResourcePreservesPrivateProjectionAndBoundedControls(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := operatorTestPrincipal()
	now := time.Now()
	jobID := model.NewJobID()
	jobs := &focusedJobApplication{view: app.JobView{
		ID: jobID, Type: model.JobTypeProfilePictureGenerateDefault, Status: model.JobStatusRunning,
		CreatedAt: now, UpdatedAt: now, AvailableAt: now, AttemptCount: 1, MaximumAttempts: 3, Revision: 2,
	}}
	httpAPI := newFocusedResourceAPI(t, logger, &academicUnitHTTPApplication{principal: principal}, jobResource(jobs))

	list := serveOperatorRequest(httpAPI, http.MethodGet, "/api/v1/jobs?limit=1", "", true)
	require.Equal(t, http.StatusOK, list.Code, "list status/query = %d/%#v: %s", list.Code, jobs.listQuery, list.Body.String())
	require.Equal(t, 1, jobs.listQuery.Limit, "list status/query = %d/%#v: %s", list.Code, jobs.listQuery, list.Body.String())
	for _, private := range []string{"command", "checkpoint", "claim_owner", "claim_token", "internal_error"} {
		require.NotContains(t, list.Body.String(), private, "job list exposed")
	}
	cursor, err := encodeJobCursor(jobCursor{CreatedAt: model.FormatRFC3339(now), ID: jobID.String()})
	require.NoError(t, err)
	page := serveOperatorRequest(httpAPI, http.MethodGet, "/api/v1/jobs?cursor="+url.QueryEscape(cursor), "", true)
	require.Equal(t, http.StatusOK, page.Code, "job cursor forwarding = %d query=%#v body=%s", page.Code, jobs.listQuery, page.Body.String())
	require.True(t, jobs.listQuery.BeforeCreatedAt.Equal(model.TimeUTC(now)), "job cursor forwarding = %d query=%#v body=%s", page.Code, jobs.listQuery, page.Body.String())
	require.Equal(t, jobID, jobs.listQuery.BeforeID, "job cursor forwarding = %d query=%#v body=%s", page.Code, jobs.listQuery, page.Body.String())
	malformed := serveOperatorRequest(httpAPI, http.MethodGet, "/api/v1/jobs?cursor=not-a-cursor", "", true)
	assertHTTPProblem(t, malformed, http.StatusBadRequest, "request.invalid")

	attemptCursor, err := encodeJobAttemptCursor(7)
	require.NoError(t, err)
	attempts := serveOperatorRequest(httpAPI, http.MethodGet, "/api/v1/jobs/"+jobID.String()+"/attempts?cursor="+url.QueryEscape(attemptCursor), "", true)
	require.Equal(t, http.StatusOK, attempts.Code, "job attempt cursor forwarding = %d query=%#v body=%s", attempts.Code, jobs.attemptQuery, attempts.Body.String())
	require.Equal(t, jobID, jobs.attemptQuery.JobID, "job attempt cursor forwarding = %d query=%#v body=%s", attempts.Code, jobs.attemptQuery, attempts.Body.String())
	require.Equal(t, 7, jobs.attemptQuery.BeforeNumber, "job attempt cursor forwarding = %d query=%#v body=%s", attempts.Code, jobs.attemptQuery, attempts.Body.String())
	malformedAttempts := serveOperatorRequest(httpAPI, http.MethodGet, "/api/v1/jobs/"+jobID.String()+"/attempts?cursor=not-a-cursor", "", true)
	assertHTTPProblem(t, malformedAttempts, http.StatusBadRequest, "request.invalid")
	for _, action := range []string{"cancel", "retry"} {
		response := serveOperatorRequest(httpAPI, http.MethodPost, "/api/v1/jobs/"+jobID.String()+"/"+action, "", true)
		require.Equal(t, http.StatusOK, response.Code, "%s status = %d: %s", action, response.Code, response.Body.String())
	}
	require.Equal(t, jobID, jobs.cancel.ID, "control commands = %#v/%#v", jobs.cancel, jobs.retry)
	require.Equal(t, jobID, jobs.retry.ID, "control commands = %#v/%#v", jobs.cancel, jobs.retry)
	create := serveOperatorRequest(httpAPI, http.MethodPost, "/api/v1/jobs", `{}`, true)
	require.Equal(t, http.StatusMethodNotAllowed, create.Code, "generic job create status = %d: %s", create.Code, create.Body.String())
}
