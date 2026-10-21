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
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type invitationHTTPApplication struct {
	InvitationApplication
	issue                 app.IssueStudentClassInvitationCommand
	accept                app.AcceptStudentClassInvitationCommand
	teacherIssue          app.IssueTeacherAcademicUnitInvitationCommand
	teacherAccept         app.AcceptTeacherAcademicUnitInvitationCommand
	unitRoleIssue         app.IssueAcademicUnitRoleInvitationCommand
	unitRoleAccept        app.AcceptAcademicUnitRoleInvitationCommand
	institutionRoleIssue  app.IssueInstitutionRoleInvitationCommand
	institutionRoleAccept app.AcceptInstitutionRoleInvitationCommand
	resend                app.ResendInvitationCommand
	revoke                app.RevokeInvitationCommand
	replace               app.ReplaceInvitationCommand
	list                  app.ListInvitationsQuery
	listMore              bool
	listEmpty             bool
	getID                 string
	acceptance            *app.InvitationAcceptanceView
	batch                 app.RunInvitationBatchCommand
}

func (a *invitationHTTPApplication) RunInvitationBatch(_ context.Context, _ app.Invocation, command app.RunInvitationBatchCommand) (app.InvitationBatchResult, error) {
	a.batch = command
	return app.InvitationBatchResult{Operation: command.Operation, Succeeded: 1, NoOp: 1, Failed: 1,
		Items: []app.InvitationBatchItemResult{
			{Index: 0, Status: app.InvitationBatchItemSucceeded, InvitationID: model.NewInvitationID()},
			{Index: 1, Status: app.InvitationBatchItemNoOp, InvitationID: model.NewInvitationID()},
			{Index: 2, Status: app.InvitationBatchItemFailed, ErrorCode: "invitation.conflict"},
		}}, nil
}

func (a *invitationHTTPApplication) IssueAcademicUnitRoleInvitation(_ context.Context, _ app.Invocation, command app.IssueAcademicUnitRoleInvitationCommand) (app.InvitationView, error) {
	a.unitRoleIssue = command
	return app.InvitationView{ID: model.NewInvitationID(), Purpose: model.InvitationPurposeAcademicUnitRole,
		State: model.InvitationPending, AcademicUnitID: model.AcademicUnitID(command.AcademicUnitID), RoleID: model.RoleID(command.RoleID)}, nil
}
func (a *invitationHTTPApplication) AcceptAcademicUnitRoleInvitation(_ context.Context, _ app.Invocation, command app.AcceptAcademicUnitRoleInvitationCommand) (*app.InvitationAcceptanceView, error) {
	a.unitRoleAccept = command
	return &app.InvitationAcceptanceView{Invitation: app.InvitationView{ID: model.NewInvitationID()},
		User: &model.User{ID: model.NewUserID()}, RoleBinding: &model.RoleBinding{ID: model.NewRoleBindingID()}}, nil
}
func (a *invitationHTTPApplication) IssueInstitutionRoleInvitation(_ context.Context, _ app.Invocation, command app.IssueInstitutionRoleInvitationCommand) (app.InvitationView, error) {
	a.institutionRoleIssue = command
	return app.InvitationView{ID: model.NewInvitationID(), Purpose: model.InvitationPurposeInstitutionRole,
		State: model.InvitationPending, RoleID: model.RoleID(command.RoleID)}, nil
}
func (a *invitationHTTPApplication) AcceptInstitutionRoleInvitation(_ context.Context, _ app.Invocation, command app.AcceptInstitutionRoleInvitationCommand) (*app.InvitationAcceptanceView, error) {
	a.institutionRoleAccept = command
	return &app.InvitationAcceptanceView{Invitation: app.InvitationView{ID: model.NewInvitationID()},
		User: &model.User{ID: model.NewUserID()}, RoleBinding: &model.RoleBinding{ID: model.NewRoleBindingID()}}, nil
}

func (a *invitationHTTPApplication) IssueTeacherAcademicUnitInvitation(_ context.Context, _ app.Invocation, command app.IssueTeacherAcademicUnitInvitationCommand) (app.InvitationView, error) {
	a.teacherIssue = command
	return app.InvitationView{ID: model.NewInvitationID(), Purpose: model.InvitationPurposeTeacherAcademicUnit,
		State: model.InvitationPending, AcademicUnitID: model.AcademicUnitID(command.AcademicUnitID), RoleID: model.RoleID(command.RoleID),
		RoleActions: []string{string(model.ActionAcademicUnitView)}}, nil
}
func (a *invitationHTTPApplication) AcceptTeacherAcademicUnitInvitation(_ context.Context, _ app.Invocation, command app.AcceptTeacherAcademicUnitInvitationCommand) (*app.InvitationAcceptanceView, error) {
	a.teacherAccept = command
	if a.acceptance != nil {
		return a.acceptance, nil
	}
	return &app.InvitationAcceptanceView{Invitation: app.InvitationView{ID: model.NewInvitationID()}, User: &model.User{ID: model.NewUserID()},
		Affiliation: &model.Affiliation{ID: model.NewAffiliationID()}, AcademicUnitMember: &model.AcademicUnitMember{ID: model.NewAcademicUnitMemberID()},
		RoleBinding: &model.RoleBinding{ID: model.NewRoleBindingID()}}, nil
}

func (a *invitationHTTPApplication) IssueStudentClassInvitation(_ context.Context, _ app.Invocation, command app.IssueStudentClassInvitationCommand) (app.InvitationView, error) {
	a.issue = command
	return app.InvitationView{ID: model.NewInvitationID(), Purpose: model.InvitationPurposeStudentClass,
		State: model.InvitationPending, ClassID: model.ClassID(command.ClassID), AcademicPeriodID: model.NewAcademicPeriodID()}, nil
}
func (a *invitationHTTPApplication) AcceptStudentClassInvitation(_ context.Context, _ app.Invocation, command app.AcceptStudentClassInvitationCommand) (*app.InvitationAcceptanceView, error) {
	a.accept = command
	if a.acceptance != nil {
		return a.acceptance, nil
	}
	return &app.InvitationAcceptanceView{User: &model.User{ID: model.NewUserID(), Username: command.Username}}, nil
}
func (a *invitationHTTPApplication) ListInvitations(_ context.Context, _ app.Invocation, query app.ListInvitationsQuery) (app.InvitationAdministrationPage, error) {
	a.list = query
	if a.listEmpty {
		return app.InvitationAdministrationPage{More: a.listMore}, nil
	}
	return app.InvitationAdministrationPage{Items: []app.InvitationAdministrationView{a.administrationView()}, More: a.listMore}, nil
}

func TestInvitationAdministrationRejectsNoProgressPage(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	applicationFake := &invitationHTTPApplication{listMore: true, listEmpty: true}
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewID()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationMultiFactor,
		ClientType: model.SessionClientWeb, AuthenticatedAt: time.Now(), MFACompletedAt: model.OptionalTimeFrom(time.Now())}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, invitationResource(applicationFake))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/invitations", nil)
	request.Header.Set("Authorization", "Bearer session")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusInternalServerError, response.Code, "no-progress status=%d body=%s", response.Code, response.Body.String())
}

func TestInvitationAdministrationHTTPIsBoundedSafeAndRevisionFenced(t *testing.T) {
	logger, _ := newTestLogger(t)
	applicationFake := &invitationHTTPApplication{listMore: true}
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewID()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationMultiFactor,
		ClientType: model.SessionClientWeb, AuthenticatedAt: time.Now(), MFACompletedAt: model.OptionalTimeFrom(time.Now())}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, invitationResource(applicationFake))

	list := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/invitations?purpose=student_class&state=pending&email=student%40example.edu&limit=25&created_after=2023-11-14T22:13:20Z&created_before=2030-03-17T17:46:40Z", nil)
	list.Header.Set("Authorization", "Bearer session")
	listResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(listResponse, list)
	require.Equal(t, http.StatusOK, listResponse.Code, "list Invitations = %d %s query=%#v", listResponse.Code, listResponse.Body.String(), applicationFake.list)
	require.Equal(t, "no-store", listResponse.Header().Get("Cache-Control"), "list Invitations = %d %s query=%#v", listResponse.Code, listResponse.Body.String(), applicationFake.list)
	require.Equal(t, model.InvitationPurposeStudentClass, applicationFake.list.Purpose, "list Invitations = %d %s query=%#v", listResponse.Code, listResponse.Body.String(), applicationFake.list)
	require.Equal(t, model.InvitationPending, applicationFake.list.State, "list Invitations = %d %s query=%#v", listResponse.Code, listResponse.Body.String(), applicationFake.list)
	require.Equal(t, "student@example.edu", applicationFake.list.TargetEmail, "list Invitations = %d %s query=%#v", listResponse.Code, listResponse.Body.String(), applicationFake.list)
	require.Equal(t, 25, applicationFake.list.Limit, "list Invitations = %d %s query=%#v", listResponse.Code, listResponse.Body.String(), applicationFake.list)
	require.False(t, applicationFake.list.CreatedAfter.IsZero(), "list Invitations = %d %s query=%#v", listResponse.Code, listResponse.Body.String(), applicationFake.list)
	require.False(t, applicationFake.list.CreatedBefore.IsZero(), "list Invitations = %d %s query=%#v", listResponse.Code, listResponse.Body.String(), applicationFake.list)
	var listed map[string]any
	require.NoError(t, json.Unmarshal(listResponse.Body.Bytes(), &listed))
	require.NotEmpty(t, listed["next_cursor"])
	for _, forbidden := range []string{"claim", "claim_hash", "encrypted_payload", "rendered", "provider", "message_id", "job_id", "failure_detail"} {
		require.NotContains(t, listResponse.Body.String(), forbidden, "Invitation list leaked")
	}
	cursor, ok := listed["next_cursor"].(string)
	require.True(t, ok)
	require.NotEmpty(t, cursor)
	decodedCursor, err := decodeInvitationCursor(cursor)
	require.NoError(t, err)
	secondPage := httptest.NewRequest(http.MethodGet, "/api/v1/invitations?limit=25&cursor="+url.QueryEscape(cursor), nil)
	secondPage.Header.Set("Authorization", "Bearer session")
	secondPageResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(secondPageResponse, secondPage)
	require.Equal(t, http.StatusOK, secondPageResponse.Code, "second Invitation page = %d %s query=%#v", secondPageResponse.Code, secondPageResponse.Body.String(), applicationFake.list)
	require.Equal(t, decodedCursor.CreatedAt, applicationFake.list.BeforeCreatedAt.Format(time.RFC3339Nano), "second Invitation page = %d %s query=%#v", secondPageResponse.Code, secondPageResponse.Body.String(), applicationFake.list)
	require.Equal(t, decodedCursor.ID, applicationFake.list.BeforeID.String(), "second Invitation page = %d %s query=%#v", secondPageResponse.Code, secondPageResponse.Body.String(), applicationFake.list)

	id := model.NewInvitationID().String()
	detail := httptest.NewRequest(http.MethodGet, "/api/v1/invitations/"+id, nil)
	detail.Header.Set("Authorization", "Bearer session")
	detailResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(detailResponse, detail)
	require.Equal(t, http.StatusOK, detailResponse.Code, "Invitation detail = %d %s id", detailResponse.Code, detailResponse.Body.String())
	require.Equal(t, "no-store", detailResponse.Header().Get("Cache-Control"), "Invitation detail = %d %s id", detailResponse.Code, detailResponse.Body.String())
	require.Equal(t, id, applicationFake.getID, "Invitation detail = %d %s id", detailResponse.Code, detailResponse.Body.String())
	for _, mutation := range []struct {
		path string
		body map[string]any
		code int
	}{
		{path: "/api/v1/invitations/" + id + "/resend", body: map[string]any{"expected_revision": 7}, code: http.StatusOK},
		{path: "/api/v1/invitations/" + id + "/revoke", body: map[string]any{"expected_revision": 8}, code: http.StatusOK},
		{path: "/api/v1/invitations/" + id + "/replacement", body: map[string]any{"expected_revision": 9, "purpose": "student_class", "email": "replacement@example.edu", "class_id": model.NewClassID().String()}, code: http.StatusCreated},
	} {
		t.Run(mutation.path, func(t *testing.T) {
			body, _ := json.Marshal(mutation.body)
			request := newJSONRequest(http.MethodPost, mutation.path, bytes.NewReader(body))
			request.Header.Set("Authorization", "Bearer session")
			response := httptest.NewRecorder()
			httpAPI.ServeHTTP(response, request)
			require.Equal(t, mutation.code, response.Code, "%s = %d %s", mutation.path, response.Code, response.Body.String())
			require.Equal(t, "no-store", response.Header().Get("Cache-Control"), "%s = %d %s", mutation.path, response.Code, response.Body.String())
		})
	}
	require.Equal(t, model.InvitationID(id), applicationFake.resend.ID, "Invitation mutation commands = resend %#v revoke %#v replacement %#v", applicationFake.resend, applicationFake.revoke, applicationFake.replace)
	require.Equal(t, int64(7), applicationFake.resend.ExpectedRevision, "Invitation mutation commands = resend %#v revoke %#v replacement %#v", applicationFake.resend, applicationFake.revoke, applicationFake.replace)
	require.Equal(t, model.InvitationID(id), applicationFake.revoke.ID, "Invitation mutation commands = resend %#v revoke %#v replacement %#v", applicationFake.resend, applicationFake.revoke, applicationFake.replace)
	require.Equal(t, int64(8), applicationFake.revoke.ExpectedRevision, "Invitation mutation commands = resend %#v revoke %#v replacement %#v", applicationFake.resend, applicationFake.revoke, applicationFake.replace)
	require.Equal(t, model.InvitationID(id), applicationFake.replace.ID, "Invitation mutation commands = resend %#v revoke %#v replacement %#v", applicationFake.resend, applicationFake.revoke, applicationFake.replace)
	require.Equal(t, int64(9), applicationFake.replace.ExpectedRevision, "Invitation mutation commands = resend %#v revoke %#v replacement %#v", applicationFake.resend, applicationFake.revoke, applicationFake.replace)
	require.Equal(t, "replacement@example.edu", applicationFake.replace.TargetEmail, "Invitation mutation commands = resend %#v revoke %#v replacement %#v", applicationFake.resend, applicationFake.revoke, applicationFake.replace)
}

func TestInvitationBatchHTTPIsStrictBoundedAndIdempotent(t *testing.T) {
	logger, _ := newTestLogger(t)
	applicationFake := &invitationHTTPApplication{}
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewID()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationMultiFactor,
		ClientType: model.SessionClientWeb, AuthenticatedAt: time.Now(), MFACompletedAt: model.OptionalTimeFrom(time.Now())}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, invitationResource(applicationFake))
	classID := model.NewClassID().String()
	body, _ := json.Marshal(map[string]any{"operation": "student_class.create", "scope_type": "class", "scope_id": classID,
		"items": []map[string]any{{"key": "row-1", "email": "first@example.edu"}, {"key": "row-2", "email": "second@example.edu"}, {"key": "row-3", "email": "third@example.edu"}}})
	request := newJSONRequest(http.MethodPost, "/api/v1/invitation-batches", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer session")
	request.Header.Set("Idempotency-Key", "batch-once")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, "Invitation batch = %d %s command=%#v", response.Code, response.Body.String(), applicationFake.batch)
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"), "Invitation batch = %d %s command=%#v", response.Code, response.Body.String(), applicationFake.batch)
	require.Equal(t, "batch-once", applicationFake.batch.IdempotencyKey, "Invitation batch = %d %s command=%#v", response.Code, response.Body.String(), applicationFake.batch)
	require.Equal(t, classID, applicationFake.batch.ScopeID, "Invitation batch = %d %s command=%#v", response.Code, response.Body.String(), applicationFake.batch)
	require.Equal(t, app.InvitationBatchStudentClassCreate, applicationFake.batch.Operation, "Invitation batch = %d %s command=%#v", response.Code, response.Body.String(), applicationFake.batch)
	require.Len(t, applicationFake.batch.Items, 3, "Invitation batch = %d %s command=%#v", response.Code, response.Body.String(), applicationFake.batch)
	require.Contains(t, response.Body.String(), `"succeeded":1`, "Invitation batch = %d %s command=%#v", response.Code, response.Body.String(), applicationFake.batch)
	require.Contains(t, response.Body.String(), `"no_op":1`, "Invitation batch = %d %s command=%#v", response.Code, response.Body.String(), applicationFake.batch)
	require.Contains(t, response.Body.String(), `"error_code":"invitation.conflict"`, "Invitation batch = %d %s command=%#v", response.Code, response.Body.String(), applicationFake.batch)
	require.NotContains(t, response.Body.String(), "@example.edu", "Invitation batch = %d %s command=%#v", response.Code, response.Body.String(), applicationFake.batch)

	missingKey := newJSONRequest(http.MethodPost, "/api/v1/invitation-batches", bytes.NewReader(body))
	missingKey.Header.Set("Authorization", "Bearer session")
	missingResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(missingResponse, missingKey)
	require.Equal(t, http.StatusBadRequest, missingResponse.Code, "Invitation batch missing key")
	require.Contains(t, missingResponse.Body.String(), "idempotency.key_required", "Invitation batch missing key")

	unknownBody := []byte(`{"operation":"revoke","scope_type":"class","scope_id":"` + classID + `","items":[],"command":"arbitrary"}`)
	unknown := newJSONRequest(http.MethodPost, "/api/v1/invitation-batches", bytes.NewReader(unknownBody))
	unknown.Header.Set("Authorization", "Bearer session")
	unknown.Header.Set("Idempotency-Key", "unknown-field")
	unknownResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(unknownResponse, unknown)
	require.Equal(t, http.StatusBadRequest, unknownResponse.Code, "Invitation batch unknown JSON = %d %s", unknownResponse.Code, unknownResponse.Body.String())
}

func TestInvitationAdministrationHTTPRejectsMalformedCursor(t *testing.T) {
	logger, logs := newTestLogger(t)
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewID()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now()}}, invitationResource(&invitationHTTPApplication{}))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/invitations?cursor=not-a-cursor", nil)
	request.Header.Set("Authorization", "Bearer pat")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusBadRequest, response.Code, "malformed Invitation cursor = %d %s logs=%s", response.Code, response.Body.String(), logs.String())
	require.Contains(t, response.Body.String(), "request.invalid", "malformed Invitation cursor = %d %s logs=%s", response.Code, response.Body.String(), logs.String())
}
func (a *invitationHTTPApplication) GetInvitation(_ context.Context, _ app.Invocation, id model.InvitationID) (app.InvitationAdministrationView, error) {
	a.getID = id.String()
	return a.administrationView(), nil
}
func (a *invitationHTTPApplication) ResendInvitation(_ context.Context, _ app.Invocation, command app.ResendInvitationCommand) (app.InvitationAdministrationView, error) {
	a.resend = command
	return a.administrationView(), nil
}
func (a *invitationHTTPApplication) RevokeInvitation(_ context.Context, _ app.Invocation, command app.RevokeInvitationCommand) (app.InvitationAdministrationView, error) {
	a.revoke = command
	view := a.administrationView()
	view.State = model.InvitationRevoked
	return view, nil
}
func (a *invitationHTTPApplication) ReplaceInvitation(_ context.Context, _ app.Invocation, command app.ReplaceInvitationCommand) (app.InvitationAdministrationView, error) {
	a.replace = command
	return a.administrationView(), nil
}
func (a *invitationHTTPApplication) administrationView() app.InvitationAdministrationView {
	at := model.TimeFromMillis(1_800_000_000_000)
	return app.InvitationAdministrationView{InvitationView: app.InvitationView{ID: model.NewInvitationID(),
		Purpose: model.InvitationPurposeStudentClass, State: model.InvitationPending, ClassID: model.NewClassID(),
		AcademicPeriodID: model.NewAcademicPeriodID(), IntendedStartsAt: at, ExpiresAt: at.Add(7 * 24 * time.Hour)},
		TargetEmail: "student@example.edu", InviterUserID: model.NewUserID(), CreatedAt: at, UpdatedAt: at, Revision: 1,
		Delivery: &app.InvitationDeliveryView{TemplateKey: model.MailTemplateAccessStudentClassInvitation,
			State: model.MailDeliveryQueued, MaskedRecipient: "s***@example.edu", CreatedAt: at, UpdatedAt: at, Deadline: at.Add(7 * 24 * time.Hour)}}
}

func TestInvitationAcceptanceHTTPReturnsOnlyRecordIDsForFreshAndReplay(t *testing.T) {
	for _, replayed := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh", true: "replay"}[replayed], func(t *testing.T) {
			logger, _ := newTestLogger(t)
			userID := model.NewUserID()
			invitationID := model.NewInvitationID()
			affiliationID := model.NewAffiliationID()
			classMemberID := model.NewClassMemberID()
			applicationFake := &invitationHTTPApplication{acceptance: &app.InvitationAcceptanceView{
				Invitation:  app.InvitationView{ID: invitationID},
				User:        &model.User{ID: userID, Username: "student", Email: "student@example.edu", EmailVerified: true, LastLoginAt: model.OptionalTimeFrom(time.Now())},
				Affiliation: &model.Affiliation{ID: affiliationID},
				ClassMember: &model.ClassMember{ID: classMemberID},
				Replayed:    replayed,
			}}
			httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{}, invitationResource(applicationFake))
			body, _ := json.Marshal(map[string]string{"claim": model.NewCredentialToken(), "password": "correct horse battery staple", "username": "student"})
			request := newJSONRequest(http.MethodPost, "/api/v1/invitations/student-class/accept", bytes.NewReader(body))
			response := httptest.NewRecorder()

			httpAPI.ServeHTTP(response, request)

			require.Equal(t, http.StatusOK, response.Code, "accept invitation = %d: %s", response.Code, response.Body.String())
			var projection map[string]any
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &projection))
			expected := map[string]any{
				"user_id":         userID.String(),
				"invitation_id":   invitationID.String(),
				"affiliation_id":  affiliationID.String(),
				"class_member_id": classMemberID.String(),
				"replayed":        replayed,
			}
			require.Equal(t, expected, projection, "acceptance projection")
		})
	}
}

func TestInvitationAcceptanceOpenAPIExposesOnlyRecordIDs(t *testing.T) {
	document := readOpenAPIDocument(t)
	schema := document.Components.Schemas["InvitationAcceptanceResponse"]
	wantFields := []string{"academic_unit_member_id", "affiliation_id", "class_member_id", "invitation_id", "replayed", "role_binding_id", "user_id"}
	wantRequired := []string{"invitation_id", "replayed", "user_id"}
	gotFields := make([]string, 0, len(schema.Properties))
	for field := range schema.Properties {
		gotFields = append(gotFields, field)
	}
	sort.Strings(gotFields)
	sort.Strings(schema.Required)
	require.Equal(t, wantFields, gotFields, "InvitationAcceptanceResponse fields")
	require.Equal(t, wantRequired, schema.Required, "InvitationAcceptanceResponse fields")
}

func TestInvitationHTTPKeepsClaimOutOfIssueResponseAndAcceptsPublicly(t *testing.T) {
	logger, _ := newTestLogger(t)
	applicationFake := &invitationHTTPApplication{}
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewID()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now()}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, invitationResource(applicationFake))
	classID := model.NewClassID().String()
	body, _ := json.Marshal(map[string]any{"email": "student@example.edu", "start_at": "2027-01-15T08:00:00Z",
		"end_at": "2027-05-11T01:46:40.5Z", "suggested_username": "student-one"})
	issue := newJSONRequest(http.MethodPost, "/api/v1/classes/"+classID+"/invitations/student", bytes.NewReader(body))
	issue.Header.Set("Authorization", "Bearer access")
	issueResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(issueResponse, issue)
	require.Equal(t, http.StatusCreated, issueResponse.Code, "issue response=%d %s command=%#v", issueResponse.Code, issueResponse.Body.String(), applicationFake.issue)
	require.NotContains(t, issueResponse.Body.String(), "student@example.edu", "issue response=%d %s command=%#v", issueResponse.Code, issueResponse.Body.String(), applicationFake.issue)
	require.NotContains(t, issueResponse.Body.String(), "claim", "issue response=%d %s command=%#v", issueResponse.Code, issueResponse.Body.String(), applicationFake.issue)
	require.Equal(t, classID, applicationFake.issue.ClassID, "issue response=%d %s command=%#v", issueResponse.Code, issueResponse.Body.String(), applicationFake.issue)
	require.Equal(t, int64(1_800_000_000_000), applicationFake.issue.IntendedStartsAt, "issue response=%d %s command=%#v", issueResponse.Code, issueResponse.Body.String(), applicationFake.issue)
	require.Equal(t, int64(1_810_000_000_500), applicationFake.issue.IntendedEndsAt, "issue response=%d %s command=%#v", issueResponse.Code, issueResponse.Body.String(), applicationFake.issue)
	raw := model.NewCredentialToken()
	acceptBody, _ := json.Marshal(map[string]string{"claim": raw, "password": "correct horse battery staple", "username": "student-one"})
	accept := newJSONRequest(http.MethodPost, "/api/v1/invitations/student-class/accept", bytes.NewReader(acceptBody))
	acceptResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(acceptResponse, accept)
	require.Equal(t, http.StatusOK, acceptResponse.Code, "accept response=%d %s command=%#v", acceptResponse.Code, acceptResponse.Body.String(), applicationFake.accept)
	require.Equal(t, raw, applicationFake.accept.Claim, "accept response=%d %s command=%#v", acceptResponse.Code, acceptResponse.Body.String(), applicationFake.accept)
	require.NotContains(t, acceptResponse.Body.String(), raw, "accept response=%d %s command=%#v", acceptResponse.Code, acceptResponse.Body.String(), applicationFake.accept)
}

func TestTeacherInvitationHTTPFreezesRoleAndReturnsRelationshipIDs(t *testing.T) {
	logger, _ := newTestLogger(t)
	applicationFake := &invitationHTTPApplication{}
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewID()),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now()}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, invitationResource(applicationFake))
	unitID, roleID := model.NewAcademicUnitID().String(), model.NewRoleID().String()
	body, _ := json.Marshal(map[string]string{"email": "teacher@example.edu", "role_id": roleID})
	request := newJSONRequest(http.MethodPost, "/api/v1/academic-units/"+unitID+"/invitations/teacher", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer access")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusCreated, response.Code, "teacher issue response=%d %s command=%#v", response.Code, response.Body.String(), applicationFake.teacherIssue)
	require.Equal(t, unitID, applicationFake.teacherIssue.AcademicUnitID, "teacher issue response=%d %s command=%#v", response.Code, response.Body.String(), applicationFake.teacherIssue)
	require.Equal(t, roleID, applicationFake.teacherIssue.RoleID, "teacher issue response=%d %s command=%#v", response.Code, response.Body.String(), applicationFake.teacherIssue)
	require.NotContains(t, response.Body.String(), "teacher@example.edu", "teacher issue response=%d %s command=%#v", response.Code, response.Body.String(), applicationFake.teacherIssue)
	require.NotContains(t, response.Body.String(), "claim", "teacher issue response=%d %s command=%#v", response.Code, response.Body.String(), applicationFake.teacherIssue)
	raw := model.NewCredentialToken()
	acceptBody, _ := json.Marshal(map[string]string{"claim": raw, "password": "correct horse battery staple", "username": "teacher-one"})
	accept := newJSONRequest(http.MethodPost, "/api/v1/invitations/teacher-academic-unit/accept", bytes.NewReader(acceptBody))
	acceptResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(acceptResponse, accept)
	require.Equal(t, http.StatusOK, acceptResponse.Code, "teacher accept response=%d %s command=%#v", acceptResponse.Code, acceptResponse.Body.String(), applicationFake.teacherAccept)
	require.Equal(t, raw, applicationFake.teacherAccept.Claim, "teacher accept response=%d %s command=%#v", acceptResponse.Code, acceptResponse.Body.String(), applicationFake.teacherAccept)
	require.NotContains(t, acceptResponse.Body.String(), raw, "teacher accept response=%d %s command=%#v", acceptResponse.Code, acceptResponse.Body.String(), applicationFake.teacherAccept)
}

func TestScopedRoleInvitationHTTPRequiresAuthenticatedExistingUserAndInstitutionAssurance(t *testing.T) {
	logger, _ := newTestLogger(t)
	applicationFake := &invitationHTTPApplication{}
	now := time.Now()
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewID()),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationMultiFactor,
		ClientType: model.SessionClientWeb, AuthenticatedAt: now, MFACompletedAt: model.OptionalTimeFrom(now)}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, invitationResource(applicationFake))
	unitID, institutionID, roleID := model.NewAcademicUnitID().String(), model.NewInstitutionID().String(), model.NewRoleID().String()
	body, _ := json.Marshal(map[string]string{"email": "existing@example.edu", "role_id": roleID})
	for _, target := range []struct {
		path    string
		command func() (string, string)
	}{
		{path: "/api/v1/academic-units/" + unitID + "/invitations/role", command: func() (string, string) {
			return applicationFake.unitRoleIssue.AcademicUnitID, applicationFake.unitRoleIssue.RoleID
		}},
		{path: "/api/v1/institutions/" + institutionID + "/invitations/role", command: func() (string, string) {
			return applicationFake.institutionRoleIssue.InstitutionID, applicationFake.institutionRoleIssue.RoleID
		}},
	} {
		t.Run(target.path, func(t *testing.T) {
			request := newJSONRequest(http.MethodPost, target.path, bytes.NewReader(body))
			request.Header.Set("Authorization", "Bearer session")
			response := httptest.NewRecorder()
			httpAPI.ServeHTTP(response, request)
			first, second := target.command()
			require.Equal(t, http.StatusCreated, response.Code, "scoped Role issue %s = %d %s command=%q", target.path, response.Code, response.Body.String(), first)
			require.NotEmpty(t, first, "scoped Role issue %s = %d %s command=%q", target.path, response.Code, response.Body.String(), first)
			require.Equal(t, roleID, second, "scoped Role issue %s = %d %s command=%q", target.path, response.Code, response.Body.String(), first)
			require.NotContains(t, response.Body.String(), "existing@example.edu", "scoped Role issue %s = %d %s command=%q", target.path, response.Code, response.Body.String(), first)
		})
	}
	claim := model.NewCredentialToken()
	for _, target := range []struct {
		path    string
		command func() string
	}{
		{path: "/api/v1/invitations/academic-unit-role/accept", command: func() string { return applicationFake.unitRoleAccept.Claim }},
		{path: "/api/v1/invitations/institution-role/accept", command: func() string { return applicationFake.institutionRoleAccept.Claim }},
	} {
		t.Run(target.path, func(t *testing.T) {
			request := newJSONRequest(http.MethodPost, target.path, bytes.NewReader([]byte(`{"claim":"`+claim+`"}`)))
			request.Header.Set("Authorization", "Bearer session")
			response := httptest.NewRecorder()
			httpAPI.ServeHTTP(response, request)
			require.Equal(t, http.StatusOK, response.Code, "scoped Role accept %s", target.path)
			require.Equal(t, claim, target.command(), "scoped Role accept %s", target.path)
			require.NotContains(t, response.Body.String(), claim, "scoped Role accept %s", target.path)
			require.NotContains(t, response.Body.String(), "email", "scoped Role accept %s", target.path)
		})
	}
	pat := principal
	pat.CredentialType, pat.SessionID = model.CredentialPersonalAccessToken, ""
	pat.CredentialID = model.PrincipalCredentialID(model.NewPersonalAccessTokenID())
	patAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: pat}, invitationResource(applicationFake))
	request := newJSONRequest(http.MethodPost, "/api/v1/institutions/"+institutionID+"/invitations/role", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer pat")
	response := httptest.NewRecorder()
	patAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusUnauthorized, response.Code, "institution Role PAT issue = %d %s", response.Code, response.Body.String())
}
