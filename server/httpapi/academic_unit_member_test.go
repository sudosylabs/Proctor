// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

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

type academicUnitMemberHTTPApplication struct {
	result        *model.AcademicUnitMember
	values        []*model.AcademicUnitMember
	listQuery     application.ListAcademicUnitMembersQuery
	createCommand application.CreateAcademicUnitMemberCommand
	endCommand    application.EndAcademicUnitMemberCommand
}

func TestAcademicUnitMemberHistoryQueryIncludesEndedMemberships(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest("GET", "/api/v1/academic-units/unit/members?active_at=123&history=true", nil)
	recorder := httptest.NewRecorder()
	activeAt, ok := queryActiveAt(recorder, request)
	if !ok {
		t.Fatalf("query rejected: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if activeAt != 0 {
		t.Fatalf("active at = %d, want 0 for complete history", activeAt)
	}
}

func TestAcademicUnitMemberHistoryQueryRejectsInvalidBoolean(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest("GET", "/api/v1/academic-units/unit/members?history=invalid", nil)
	recorder := httptest.NewRecorder()
	if _, ok := queryActiveAt(recorder, request); ok {
		t.Fatal("invalid history query was accepted")
	}
	if recorder.Code != 400 {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}

func TestAcademicUnitMemberFalseHistoryQueryUsesActiveAt(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest("GET", "/api/v1/academic-units/unit/members?history=false&active_at=123", nil)
	recorder := httptest.NewRecorder()
	activeAt, ok := queryActiveAt(recorder, request)
	if !ok || activeAt != 123 {
		t.Fatalf("active at = %d, ok = %v; want 123, true", activeAt, ok)
	}
}

func (a *academicUnitMemberHTTPApplication) ListAcademicUnitMembers(_ context.Context, _ application.Invocation, query application.ListAcademicUnitMembersQuery) ([]*model.AcademicUnitMember, error) {
	a.listQuery = query
	return a.values, nil
}
func (a *academicUnitMemberHTTPApplication) CreateAcademicUnitMember(_ context.Context, _ application.Invocation, command application.CreateAcademicUnitMemberCommand) (*model.AcademicUnitMember, error) {
	a.createCommand = command
	return a.result, nil
}
func (a *academicUnitMemberHTTPApplication) EndAcademicUnitMember(_ context.Context, _ application.Invocation, command application.EndAcademicUnitMemberCommand) (*model.AcademicUnitMember, error) {
	a.endCommand = command
	return a.result, nil
}

func TestAcademicUnitMemberResourceRunsThroughRoutingKernel(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now()}
	unitID, userID := model.NewId(), model.NewId()
	member := &model.AcademicUnitMember{ID: model.NewAcademicUnitMemberID(), AcademicUnitID: model.AcademicUnitID(unitID), UserID: model.UserID(userID), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100), StartsAt: model.TimeFromMillis(100)}
	members := &academicUnitMemberHTTPApplication{result: member}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, academicUnitMemberResource(members))

	create := httptest.NewRequest(http.MethodPost, "/api/v1/academic-units/"+unitID+"/members", strings.NewReader(`{"academic_unit_id":"ignored","user_id":"`+userID+`","start_at":100}`))
	create.Header.Set("Authorization", "Bearer credential")
	create.Header.Set("Content-Type", "application/json")
	created := httptest.NewRecorder()
	httpAPI.ServeHTTP(created, create)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", created.Code, created.Body.String())
	}
	if members.createCommand.AcademicUnitID != unitID || members.createCommand.UserID != userID {
		t.Fatalf("command = %#v", members.createCommand)
	}
	var body map[string]any
	if err := json.Unmarshal(created.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if _, exposed := body["revision"]; exposed {
		t.Fatalf("persistence revision leaked into DTO: %#v", body)
	}

	list := httptest.NewRequest(http.MethodGet, "/api/v1/academic-units/"+unitID+"/members?history=true", nil)
	list.Header.Set("Authorization", "Bearer credential")
	listed := httptest.NewRecorder()
	httpAPI.ServeHTTP(listed, list)
	if listed.Code != http.StatusOK || members.listQuery.AcademicUnitID != unitID || members.listQuery.ActiveAt != 0 {
		t.Fatalf("list status/query = %d/%#v: %s", listed.Code, members.listQuery, listed.Body.String())
	}
}

func TestAcademicUnitMemberResourceRejectsInvalidQueryAndMissingPrincipal(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now()}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, academicUnitMemberResource(&academicUnitMemberHTTPApplication{}))

	invalid := httptest.NewRequest(http.MethodGet, "/api/v1/academic-units/"+model.NewId()+"/members?history=invalid", nil)
	invalid.Header.Set("Authorization", "Bearer credential")
	invalidResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid query status = %d: %s", invalidResponse.Code, invalidResponse.Body.String())
	}

	unauthenticated := httptest.NewRequest(http.MethodGet, "/api/v1/academic-units/"+model.NewId()+"/members", nil)
	unauthenticatedResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(unauthenticatedResponse, unauthenticated)
	if unauthenticatedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d: %s", unauthenticatedResponse.Code, unauthenticatedResponse.Body.String())
	}
}
