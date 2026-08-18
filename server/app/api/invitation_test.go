// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type invitationHTTPApplication struct {
	issue      application.IssueStudentClassInvitationCommand
	accept     application.AcceptStudentClassInvitationCommand
	acceptance *application.InvitationAcceptanceView
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
	wantFields := []string{"affiliation_id", "class_member_id", "invitation_id", "replayed", "user_id"}
	gotFields := make([]string, 0, len(schema.Properties))
	for field := range schema.Properties {
		gotFields = append(gotFields, field)
	}
	sort.Strings(gotFields)
	sort.Strings(schema.Required)
	if !reflect.DeepEqual(gotFields, wantFields) || !reflect.DeepEqual(schema.Required, wantFields) {
		t.Fatalf("InvitationAcceptanceResponse fields=%v required=%v, want only %v", gotFields, schema.Required, wantFields)
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
