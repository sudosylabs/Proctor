// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type classMemberHTTPApplication struct {
	result        *model.ClassEnrollment
	ended         *model.ClassMember
	values        []*model.ClassMember
	listQuery     application.ListClassMembersQuery
	enrollCommand application.EnrollClassMemberCommand
	endCommand    application.EndClassMemberCommand
}

func (a *classMemberHTTPApplication) ListClassMembers(_ context.Context, _ application.Invocation, query application.ListClassMembersQuery) ([]*model.ClassMember, error) {
	a.listQuery = query
	return a.values, nil
}

func (a *classMemberHTTPApplication) EnrollClassMember(_ context.Context, _ application.Invocation, command application.EnrollClassMemberCommand) (*model.ClassEnrollment, error) {
	a.enrollCommand = command
	return a.result, nil
}

func (a *classMemberHTTPApplication) EndClassMember(_ context.Context, _ application.Invocation, command application.EndClassMemberCommand) (*model.ClassMember, error) {
	a.endCommand = command
	return a.ended, nil
}

func TestClassMemberHTTPUsesDTOAndRouteOwnedClass(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now()}
	classID, userID := model.NewId(), model.NewId()
	member := &model.ClassMember{
		ID: model.NewClassMemberID(), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100),
		Revision: 1, ClassID: model.ClassID(classID), AcademicPeriodID: model.NewAcademicPeriodID(),
		UserID: model.UserID(userID), StartsAt: model.TimeFromMillis(100),
	}
	classMembers := &classMemberHTTPApplication{result: &model.ClassEnrollment{Membership: member}}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, classMemberResource(classMembers))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/classes/"+classID+"/members", strings.NewReader(`{"id":"ignored","class_id":"ignored","academic_period_id":"ignored","user_id":"`+userID+`","start_at":100}`))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if classMembers.enrollCommand.ClassID != classID || classMembers.enrollCommand.UserID != userID {
		t.Fatalf("command = %#v", classMembers.enrollCommand)
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	membership := body["membership"].(map[string]any)
	if _, exposed := membership["revision"]; exposed {
		t.Fatalf("persistence revision leaked into DTO: %#v", membership)
	}
}

func TestClassMemberResourceForwardsHistoryAndEndsMembership(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now()}
	classID := model.NewId()
	member := &model.ClassMember{ID: model.NewClassMemberID(), ClassID: model.ClassID(classID), AcademicPeriodID: model.NewAcademicPeriodID(), UserID: model.NewUserID(), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100), StartsAt: model.TimeFromMillis(100)}
	members := &classMemberHTTPApplication{ended: member}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, classMemberResource(members))

	list := httptest.NewRequest(http.MethodGet, "/api/v1/classes/"+classID+"/members?history=true", nil)
	list.Header.Set("Authorization", "Bearer credential")
	listed := httptest.NewRecorder()
	httpAPI.ServeHTTP(listed, list)
	if listed.Code != http.StatusOK || members.listQuery.ClassID != classID || members.listQuery.ActiveAt != 0 {
		t.Fatalf("list status/query = %d/%#v: %s", listed.Code, members.listQuery, listed.Body.String())
	}

	end := httptest.NewRequest(http.MethodDelete, "/api/v1/class-members/"+member.ID.String(), nil)
	end.Header.Set("Authorization", "Bearer credential")
	ended := httptest.NewRecorder()
	httpAPI.ServeHTTP(ended, end)
	if ended.Code != http.StatusOK || members.endCommand.ID != member.ID.String() || ended.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("end status/command/cache = %d/%#v/%q: %s", ended.Code, members.endCommand, ended.Header().Get("Cache-Control"), ended.Body.String())
	}
}

func TestClassMemberHistoryQueryIsForwarded(t *testing.T) {
	t.Parallel()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/classes/class/members?history=true", nil)
	recorder := httptest.NewRecorder()
	activeAt, ok := queryActiveAt(recorder, request)
	if !ok || activeAt != 0 {
		t.Fatalf("active at = %d, ok = %v; want 0, true", activeAt, ok)
	}
}
