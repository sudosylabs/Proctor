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
	"github.com/sudosylabs/proctor/server/mlog"
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
	logger, err := mlog.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logger.Shutdown() })
	principal := model.Principal{UserId: model.NewId(), SessionId: model.NewId(), CredentialId: model.NewId(), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now().UnixMilli()}
	classID, userID := model.NewId(), model.NewId()
	member := &model.ClassMember{
		ID: model.NewClassMemberID(), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100),
		Revision: 1, ClassID: model.ClassID(classID), AcademicPeriodID: model.NewAcademicPeriodID(),
		UserID: model.UserID(userID), StartsAt: model.TimeFromMillis(100),
	}
	classMembers := &classMemberHTTPApplication{result: &model.ClassEnrollment{Membership: member}}
	transport := &academicUnitHTTPApplication{principal: principal}
	httpAPI, err := New(Options{Logger: logger, Health: academicUnitHTTPHealth{}, Application: transport, AcademicUnits: transport, Institutions: transport, Programmes: &programmeHTTPApplication{}, ProgrammeLevels: &programmeLevelHTTPApplication{}, AcademicPeriods: &academicPeriodHTTPApplication{}, Classes: &classHTTPApplication{}, Affiliations: &affiliationHTTPApplication{}, AcademicUnitMembers: &academicUnitMemberHTTPApplication{}, ClassMembers: classMembers, UserProfiles: &userProfileHTTPApplication{}, AccountStates: &accountStateHTTPApplication{}, SessionAdministrations: &sessionAdministrationHTTPApplication{}, Roles: &roleHTTPApplication{}, RoleBindings: &roleBindingHTTPApplication{}, AuditListings: &auditListingHTTPApplication{}, Bootstrap: &bootstrapHTTPApplication{}, BuildInfo: BuildInfo{Version: "test"}, PublicURL: "http://localhost:8065", MaxBodyBytes: 1 << 20, RecentAuthenticationTTL: time.Minute, NodeID: "node-a"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = httpAPI.Close() })
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

func TestClassMemberHistoryQueryIsForwarded(t *testing.T) {
	t.Parallel()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/classes/class/members?history=true", nil)
	recorder := httptest.NewRecorder()
	activeAt, ok := queryActiveAt(recorder, request)
	if !ok || activeAt != 0 {
		t.Fatalf("active at = %d, ok = %v; want 0, true", activeAt, ok)
	}
}
