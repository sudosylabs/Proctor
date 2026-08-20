// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type invitationHTTPApplication struct {
	InvitationApplication
	issue                 application.IssueStudentClassInvitationCommand
	accept                application.AcceptStudentClassInvitationCommand
	teacherIssue          application.IssueTeacherAcademicUnitInvitationCommand
	teacherAccept         application.AcceptTeacherAcademicUnitInvitationCommand
	unitRoleIssue         application.IssueAcademicUnitRoleInvitationCommand
	unitRoleAccept        application.AcceptAcademicUnitRoleInvitationCommand
	institutionRoleIssue  application.IssueInstitutionRoleInvitationCommand
	institutionRoleAccept application.AcceptInstitutionRoleInvitationCommand
	resend                application.ResendInvitationCommand
	revoke                application.RevokeInvitationCommand
	replace               application.ReplaceInvitationCommand
	list                  application.ListInvitationsQuery
	listMore              bool
	getID                 string
	acceptance            *application.InvitationAcceptanceView
	batch                 application.RunInvitationBatchCommand
}

func (a *invitationHTTPApplication) RunInvitationBatch(_ context.Context, _ application.Invocation, command application.RunInvitationBatchCommand) (application.InvitationBatchResult, error) {
	a.batch = command
	return application.InvitationBatchResult{Operation: command.Operation, Succeeded: 1, NoOp: 1, Failed: 1,
		Items: []application.InvitationBatchItemResult{
			{Index: 0, Status: application.InvitationBatchItemSucceeded, InvitationID: model.NewInvitationID()},
			{Index: 1, Status: application.InvitationBatchItemNoOp, InvitationID: model.NewInvitationID()},
			{Index: 2, Status: application.InvitationBatchItemFailed, ErrorCode: "invitation.conflict"},
		}}, nil
}

func (a *invitationHTTPApplication) IssueAcademicUnitRoleInvitation(_ context.Context, _ application.Invocation, command application.IssueAcademicUnitRoleInvitationCommand) (application.InvitationView, error) {
	a.unitRoleIssue = command
	return application.InvitationView{ID: model.NewInvitationID(), Purpose: model.InvitationPurposeAcademicUnitRole,
		State: model.InvitationPending, AcademicUnitID: model.AcademicUnitID(command.AcademicUnitID), RoleID: model.RoleID(command.RoleID)}, nil
}
func (a *invitationHTTPApplication) AcceptAcademicUnitRoleInvitation(_ context.Context, _ application.Invocation, command application.AcceptAcademicUnitRoleInvitationCommand) (*application.InvitationAcceptanceView, error) {
	a.unitRoleAccept = command
	return &application.InvitationAcceptanceView{Invitation: application.InvitationView{ID: model.NewInvitationID()},
		User: &model.User{ID: model.NewUserID()}, RoleBinding: &model.RoleBinding{ID: model.NewRoleBindingID()}}, nil
}
func (a *invitationHTTPApplication) IssueInstitutionRoleInvitation(_ context.Context, _ application.Invocation, command application.IssueInstitutionRoleInvitationCommand) (application.InvitationView, error) {
	a.institutionRoleIssue = command
	return application.InvitationView{ID: model.NewInvitationID(), Purpose: model.InvitationPurposeInstitutionRole,
		State: model.InvitationPending, RoleID: model.RoleID(command.RoleID)}, nil
}
func (a *invitationHTTPApplication) AcceptInstitutionRoleInvitation(_ context.Context, _ application.Invocation, command application.AcceptInstitutionRoleInvitationCommand) (*application.InvitationAcceptanceView, error) {
	a.institutionRoleAccept = command
	return &application.InvitationAcceptanceView{Invitation: application.InvitationView{ID: model.NewInvitationID()},
		User: &model.User{ID: model.NewUserID()}, RoleBinding: &model.RoleBinding{ID: model.NewRoleBindingID()}}, nil
}

func (a *invitationHTTPApplication) IssueTeacherAcademicUnitInvitation(_ context.Context, _ application.Invocation, command application.IssueTeacherAcademicUnitInvitationCommand) (application.InvitationView, error) {
	a.teacherIssue = command
	return application.InvitationView{ID: model.NewInvitationID(), Purpose: model.InvitationPurposeTeacherAcademicUnit,
		State: model.InvitationPending, AcademicUnitID: model.AcademicUnitID(command.AcademicUnitID), RoleID: model.RoleID(command.RoleID),
		RoleActions: []string{string(model.ActionAcademicUnitView)}}, nil
}
func (a *invitationHTTPApplication) AcceptTeacherAcademicUnitInvitation(_ context.Context, _ application.Invocation, command application.AcceptTeacherAcademicUnitInvitationCommand) (*application.InvitationAcceptanceView, error) {
	a.teacherAccept = command
	if a.acceptance != nil {
		return a.acceptance, nil
	}
	return &application.InvitationAcceptanceView{Invitation: application.InvitationView{ID: model.NewInvitationID()}, User: &model.User{ID: model.NewUserID()},
		Affiliation: &model.Affiliation{ID: model.NewAffiliationID()}, AcademicUnitMember: &model.AcademicUnitMember{ID: model.NewAcademicUnitMemberID()},
		RoleBinding: &model.RoleBinding{ID: model.NewRoleBindingID()}}, nil
}

func (a *invitationHTTPApplication) IssueStudentClassInvitation(_ context.Context, _ application.Invocation, command application.IssueStudentClassInvitationCommand) (application.InvitationView, error) {
	a.issue = command
	return application.InvitationView{ID: model.NewInvitationID(), Purpose: model.InvitationPurposeStudentClass,
		State: model.InvitationPending, ClassID: model.ClassID(command.ClassID), AcademicPeriodID: model.NewAcademicPeriodID()}, nil
}
func (a *invitationHTTPApplication) AcceptStudentClassInvitation(_ context.Context, _ application.Invocation, command application.AcceptStudentClassInvitationCommand) (*application.InvitationAcceptanceView, error) {
	a.accept = command
	if a.acceptance != nil {
		return a.acceptance, nil
	}
	return &application.InvitationAcceptanceView{User: &model.User{ID: model.NewUserID(), Username: command.Username}}, nil
}
func (a *invitationHTTPApplication) ListInvitations(_ context.Context, _ application.Invocation, query application.ListInvitationsQuery) (application.InvitationAdministrationPage, error) {
	a.list = query
	return application.InvitationAdministrationPage{Items: []application.InvitationAdministrationView{a.administrationView()}, More: a.listMore}, nil
}

func TestInvitationAdministrationHTTPIsBoundedSafeAndRevisionFenced(t *testing.T) {
	logger, _ := newTestLogger(t)
	applicationFake := &invitationHTTPApplication{listMore: true}
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationMultiFactor,
		ClientType: model.SessionClientWeb, AuthenticatedAt: time.Now(), MFACompletedAt: model.OptionalTimeFrom(time.Now())}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, invitationResource(applicationFake))

	list := httptest.NewRequest(http.MethodGet, "/api/v1/invitations?purpose=student_class&state=pending&email=student%40example.edu&limit=25&created_after=1700000000000&created_before=1900000000000", nil)
	list.Header.Set("Authorization", "Bearer session")
	listResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(listResponse, list)
	if listResponse.Code != http.StatusOK || listResponse.Header().Get("Cache-Control") != "no-store" ||
		applicationFake.list.Purpose != model.InvitationPurposeStudentClass ||
		applicationFake.list.State != model.InvitationPending || applicationFake.list.TargetEmail != "student@example.edu" ||
		applicationFake.list.Limit != 25 || applicationFake.list.CreatedAfter.IsZero() || applicationFake.list.CreatedBefore.IsZero() {
		t.Fatalf("list Invitations = %d %s query=%#v", listResponse.Code, listResponse.Body.String(), applicationFake.list)
	}
	var listed map[string]any
	if err := json.Unmarshal(listResponse.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode Invitation list: %v", err)
	}
	if listed["next_cursor"] == "" {
		t.Fatalf("Invitation list cursor missing: %#v", listed)
	}
	for _, forbidden := range []string{"claim", "claim_hash", "encrypted_payload", "rendered", "provider", "message_id", "job_id", "failure_detail"} {
		if strings.Contains(listResponse.Body.String(), forbidden) {
			t.Fatalf("Invitation list leaked %q: %s", forbidden, listResponse.Body.String())
		}
	}
	cursor, ok := listed["next_cursor"].(string)
	if !ok || cursor == "" {
		t.Fatalf("Invitation list cursor = %#v", listed["next_cursor"])
	}
	secondPage := httptest.NewRequest(http.MethodGet, "/api/v1/invitations?limit=25&cursor="+url.QueryEscape(cursor), nil)
	secondPage.Header.Set("Authorization", "Bearer session")
	secondPageResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(secondPageResponse, secondPage)
	if secondPageResponse.Code != http.StatusOK || applicationFake.list.BeforeCreatedAt.IsZero() || !applicationFake.list.BeforeID.IsValid() {
		t.Fatalf("second Invitation page = %d %s query=%#v", secondPageResponse.Code, secondPageResponse.Body.String(), applicationFake.list)
	}

	id := model.NewInvitationID().String()
	detail := httptest.NewRequest(http.MethodGet, "/api/v1/invitations/"+id, nil)
	detail.Header.Set("Authorization", "Bearer session")
	detailResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(detailResponse, detail)
	if detailResponse.Code != http.StatusOK || detailResponse.Header().Get("Cache-Control") != "no-store" || applicationFake.getID != id {
		t.Fatalf("Invitation detail = %d %s id=%q", detailResponse.Code, detailResponse.Body.String(), applicationFake.getID)
	}
	for _, mutation := range []struct {
		path string
		body map[string]any
		code int
	}{
		{path: "/api/v1/invitations/" + id + "/resend", body: map[string]any{"expected_revision": 7}, code: http.StatusOK},
		{path: "/api/v1/invitations/" + id + "/revoke", body: map[string]any{"expected_revision": 8}, code: http.StatusOK},
		{path: "/api/v1/invitations/" + id + "/replacement", body: map[string]any{"expected_revision": 9, "purpose": "student_class", "email": "replacement@example.edu", "class_id": model.NewClassID().String()}, code: http.StatusCreated},
	} {
		body, _ := json.Marshal(mutation.body)
		request := httptest.NewRequest(http.MethodPost, mutation.path, bytes.NewReader(body))
		request.Header.Set("Authorization", "Bearer session")
		response := httptest.NewRecorder()
		httpAPI.ServeHTTP(response, request)
		if response.Code != mutation.code || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s = %d %s", mutation.path, response.Code, response.Body.String())
		}
	}
	if applicationFake.resend.ID != id || applicationFake.resend.ExpectedRevision != 7 ||
		applicationFake.revoke.ID != id || applicationFake.revoke.ExpectedRevision != 8 ||
		applicationFake.replace.ID != id || applicationFake.replace.ExpectedRevision != 9 ||
		applicationFake.replace.TargetEmail != "replacement@example.edu" {
		t.Fatalf("Invitation mutation commands = resend %#v revoke %#v replacement %#v", applicationFake.resend, applicationFake.revoke, applicationFake.replace)
	}
}

func TestInvitationBatchHTTPIsStrictBoundedAndIdempotent(t *testing.T) {
	logger, _ := newTestLogger(t)
	applicationFake := &invitationHTTPApplication{}
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationMultiFactor,
		ClientType: model.SessionClientWeb, AuthenticatedAt: time.Now(), MFACompletedAt: model.OptionalTimeFrom(time.Now())}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, invitationResource(applicationFake))
	classID := model.NewClassID().String()
	body, _ := json.Marshal(map[string]any{"operation": "student_class.create", "scope_type": "class", "scope_id": classID,
		"items": []map[string]any{{"key": "row-1", "email": "first@example.edu"}, {"key": "row-2", "email": "second@example.edu"}, {"key": "row-3", "email": "third@example.edu"}}})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/invitation-batches", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer session")
	request.Header.Set("Idempotency-Key", "batch-once")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" ||
		applicationFake.batch.IdempotencyKey != "batch-once" || applicationFake.batch.ScopeID != classID ||
		applicationFake.batch.Operation != application.InvitationBatchStudentClassCreate || len(applicationFake.batch.Items) != 3 ||
		!strings.Contains(response.Body.String(), `"succeeded":1`) || !strings.Contains(response.Body.String(), `"no_op":1`) ||
		!strings.Contains(response.Body.String(), `"error_code":"invitation.conflict"`) || strings.Contains(response.Body.String(), "@example.edu") {
		t.Fatalf("Invitation batch = %d %s command=%#v", response.Code, response.Body.String(), applicationFake.batch)
	}

	missingKey := httptest.NewRequest(http.MethodPost, "/api/v1/invitation-batches", bytes.NewReader(body))
	missingKey.Header.Set("Authorization", "Bearer session")
	missingResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(missingResponse, missingKey)
	if missingResponse.Code != http.StatusBadRequest || !strings.Contains(missingResponse.Body.String(), "idempotency.key_required") {
		t.Fatalf("Invitation batch missing key = %d %s", missingResponse.Code, missingResponse.Body.String())
	}

	unknownBody := []byte(`{"operation":"revoke","scope_type":"class","scope_id":"` + classID + `","items":[],"command":"arbitrary"}`)
	unknown := httptest.NewRequest(http.MethodPost, "/api/v1/invitation-batches", bytes.NewReader(unknownBody))
	unknown.Header.Set("Authorization", "Bearer session")
	unknown.Header.Set("Idempotency-Key", "unknown-field")
	unknownResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(unknownResponse, unknown)
	if unknownResponse.Code != http.StatusBadRequest {
		t.Fatalf("Invitation batch unknown JSON = %d %s", unknownResponse.Code, unknownResponse.Body.String())
	}
}

func TestInvitationAdministrationHTTPRejectsMalformedCursor(t *testing.T) {
	logger, logs := newTestLogger(t)
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now()}}, invitationResource(&invitationHTTPApplication{}))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/invitations?cursor=not-a-cursor", nil)
	request.Header.Set("Authorization", "Bearer pat")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "invitation.query.invalid") {
		t.Fatalf("malformed Invitation cursor = %d %s logs=%s", response.Code, response.Body.String(), logs.String())
	}
}
func (a *invitationHTTPApplication) GetInvitation(_ context.Context, _ application.Invocation, id string) (application.InvitationAdministrationView, error) {
	a.getID = id
	return a.administrationView(), nil
}
func (a *invitationHTTPApplication) ResendInvitation(_ context.Context, _ application.Invocation, command application.ResendInvitationCommand) (application.InvitationAdministrationView, error) {
	a.resend = command
	return a.administrationView(), nil
}
func (a *invitationHTTPApplication) RevokeInvitation(_ context.Context, _ application.Invocation, command application.RevokeInvitationCommand) (application.InvitationAdministrationView, error) {
	a.revoke = command
	view := a.administrationView()
	view.State = model.InvitationRevoked
	return view, nil
}
func (a *invitationHTTPApplication) ReplaceInvitation(_ context.Context, _ application.Invocation, command application.ReplaceInvitationCommand) (application.InvitationAdministrationView, error) {
	a.replace = command
	return a.administrationView(), nil
}
func (a *invitationHTTPApplication) administrationView() application.InvitationAdministrationView {
	at := model.TimeFromMillis(1_800_000_000_000)
	return application.InvitationAdministrationView{InvitationView: application.InvitationView{ID: model.NewInvitationID(),
		Purpose: model.InvitationPurposeStudentClass, State: model.InvitationPending, ClassID: model.NewClassID(),
		AcademicPeriodID: model.NewAcademicPeriodID(), IntendedStartsAt: at, ExpiresAt: at.Add(7 * 24 * time.Hour)},
		TargetEmail: "student@example.edu", InviterUserID: model.NewUserID(), CreatedAt: at, UpdatedAt: at, Revision: 1,
		Delivery: &application.InvitationDeliveryView{TemplateKey: model.MailTemplateAccessStudentClassInvitation,
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
			applicationFake := &invitationHTTPApplication{acceptance: &application.InvitationAcceptanceView{
				Invitation:  application.InvitationView{ID: invitationID},
				User:        &model.User{ID: userID, Username: "student", Email: "student@example.edu", EmailVerified: true, LastLoginAt: model.OptionalTimeFrom(time.Now())},
				Affiliation: &model.Affiliation{ID: affiliationID},
				ClassMember: &model.ClassMember{ID: classMemberID},
				Replayed:    replayed,
			}}
			httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{}, invitationResource(applicationFake))
			body, _ := json.Marshal(map[string]string{"claim": model.NewCredentialToken(), "password": "correct horse battery staple", "username": "student"})
			request := httptest.NewRequest(http.MethodPost, "/api/v1/invitations/student-class/accept", bytes.NewReader(body))
			response := httptest.NewRecorder()

			httpAPI.ServeHTTP(response, request)

			if response.Code != http.StatusOK {
				t.Fatalf("accept invitation = %d: %s", response.Code, response.Body.String())
			}
			var projection map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &projection); err != nil {
				t.Fatalf("decode acceptance response: %v", err)
			}
			expected := map[string]any{
				"user_id":         userID.String(),
				"invitation_id":   invitationID.String(),
				"affiliation_id":  affiliationID.String(),
				"class_member_id": classMemberID.String(),
				"replayed":        replayed,
			}
			if !reflect.DeepEqual(projection, expected) {
				t.Fatalf("acceptance projection = %#v, want %#v", projection, expected)
			}
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
	if !reflect.DeepEqual(gotFields, wantFields) || !reflect.DeepEqual(schema.Required, wantRequired) {
		t.Fatalf("InvitationAcceptanceResponse fields=%v required=%v, want fields %v required %v", gotFields, schema.Required, wantFields, wantRequired)
	}
}

func TestInvitationHTTPKeepsClaimOutOfIssueResponseAndAcceptsPublicly(t *testing.T) {
	logger, _ := newTestLogger(t)
	applicationFake := &invitationHTTPApplication{}
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now()}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, invitationResource(applicationFake))
	classID := model.NewClassID().String()
	body, _ := json.Marshal(map[string]any{"email": "student@example.edu", "start_at": int64(1_800_000_000_000),
		"end_at": int64(1_810_000_000_000), "suggested_username": "student-one"})
	issue := httptest.NewRequest(http.MethodPost, "/api/v1/classes/"+classID+"/invitations/student", bytes.NewReader(body))
	issue.Header.Set("Authorization", "Bearer access")
	issueResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(issueResponse, issue)
	if issueResponse.Code != http.StatusCreated || strings.Contains(issueResponse.Body.String(), "student@example.edu") ||
		strings.Contains(issueResponse.Body.String(), "claim") || applicationFake.issue.ClassID != classID {
		t.Fatalf("issue response=%d %s command=%#v", issueResponse.Code, issueResponse.Body.String(), applicationFake.issue)
	}
	raw := model.NewCredentialToken()
	acceptBody, _ := json.Marshal(map[string]string{"claim": raw, "password": "correct horse battery staple", "username": "student-one"})
	accept := httptest.NewRequest(http.MethodPost, "/api/v1/invitations/student-class/accept", bytes.NewReader(acceptBody))
	acceptResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(acceptResponse, accept)
	if acceptResponse.Code != http.StatusOK || applicationFake.accept.Claim != raw || strings.Contains(acceptResponse.Body.String(), raw) {
		t.Fatalf("accept response=%d %s command=%#v", acceptResponse.Code, acceptResponse.Body.String(), applicationFake.accept)
	}
}

func TestTeacherInvitationHTTPFreezesRoleAndReturnsRelationshipIDs(t *testing.T) {
	logger, _ := newTestLogger(t)
	applicationFake := &invitationHTTPApplication{}
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now()}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, invitationResource(applicationFake))
	unitID, roleID := model.NewAcademicUnitID().String(), model.NewRoleID().String()
	body, _ := json.Marshal(map[string]string{"email": "teacher@example.edu", "role_id": roleID})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/academic-units/"+unitID+"/invitations/teacher", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer access")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || applicationFake.teacherIssue.AcademicUnitID != unitID || applicationFake.teacherIssue.RoleID != roleID ||
		strings.Contains(response.Body.String(), "teacher@example.edu") || strings.Contains(response.Body.String(), "claim") {
		t.Fatalf("teacher issue response=%d %s command=%#v", response.Code, response.Body.String(), applicationFake.teacherIssue)
	}
	raw := model.NewCredentialToken()
	acceptBody, _ := json.Marshal(map[string]string{"claim": raw, "password": "correct horse battery staple", "username": "teacher-one"})
	accept := httptest.NewRequest(http.MethodPost, "/api/v1/invitations/teacher-academic-unit/accept", bytes.NewReader(acceptBody))
	acceptResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(acceptResponse, accept)
	if acceptResponse.Code != http.StatusOK || applicationFake.teacherAccept.Claim != raw || strings.Contains(acceptResponse.Body.String(), raw) {
		t.Fatalf("teacher accept response=%d %s command=%#v", acceptResponse.Code, acceptResponse.Body.String(), applicationFake.teacherAccept)
	}
}

func TestScopedRoleInvitationHTTPRequiresAuthenticatedExistingUserAndInstitutionAssurance(t *testing.T) {
	logger, _ := newTestLogger(t)
	applicationFake := &invitationHTTPApplication{}
	now := time.Now()
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()),
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
		request := httptest.NewRequest(http.MethodPost, target.path, bytes.NewReader(body))
		request.Header.Set("Authorization", "Bearer session")
		response := httptest.NewRecorder()
		httpAPI.ServeHTTP(response, request)
		if first, second := target.command(); response.Code != http.StatusCreated || first == "" || second != roleID || strings.Contains(response.Body.String(), "existing@example.edu") {
			t.Fatalf("scoped Role issue %s = %d %s command=%q/%q", target.path, response.Code, response.Body.String(), first, second)
		}
	}
	claim := model.NewCredentialToken()
	for _, target := range []struct {
		path    string
		command func() string
	}{
		{path: "/api/v1/invitations/academic-unit-role/accept", command: func() string { return applicationFake.unitRoleAccept.Claim }},
		{path: "/api/v1/invitations/institution-role/accept", command: func() string { return applicationFake.institutionRoleAccept.Claim }},
	} {
		request := httptest.NewRequest(http.MethodPost, target.path, bytes.NewReader([]byte(`{"claim":"`+claim+`"}`)))
		request.Header.Set("Authorization", "Bearer session")
		response := httptest.NewRecorder()
		httpAPI.ServeHTTP(response, request)
		if response.Code != http.StatusOK || target.command() != claim || strings.Contains(response.Body.String(), claim) || strings.Contains(response.Body.String(), "email") {
			t.Fatalf("scoped Role accept %s = %d %s", target.path, response.Code, response.Body.String())
		}
	}
	pat := principal
	pat.CredentialType, pat.SessionID = model.CredentialPersonalAccessToken, ""
	pat.CredentialID = model.PrincipalCredentialID(model.NewPersonalAccessTokenID())
	patAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: pat}, invitationResource(applicationFake))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/institutions/"+institutionID+"/invitations/role", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer pat")
	response := httptest.NewRecorder()
	patAPI.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("institution Role PAT issue = %d %s", response.Code, response.Body.String())
	}
}
