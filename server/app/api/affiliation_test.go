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

type affiliationHTTPApplication struct {
	result        *model.Affiliation
	values        []*model.Affiliation
	createCommand application.CreateAffiliationCommand
	endCommand    application.EndAffiliationCommand
}

func (a *affiliationHTTPApplication) ListAffiliations(context.Context, application.Invocation, application.ListAffiliationsQuery) ([]*model.Affiliation, error) {
	return a.values, nil
}
func (a *affiliationHTTPApplication) CreateAffiliation(_ context.Context, _ application.Invocation, command application.CreateAffiliationCommand) (*model.Affiliation, error) {
	a.createCommand = command
	return a.result, nil
}
func (a *affiliationHTTPApplication) EndAffiliation(_ context.Context, _ application.Invocation, command application.EndAffiliationCommand) (*model.Affiliation, error) {
	a.endCommand = command
	return a.result, nil
}

func TestAffiliationHTTPUsesDTOAndRouteOwnedUser(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now()}
	userID := model.NewId()
	created := &model.Affiliation{
		ID: model.NewAffiliationID(), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100),
		Revision: 1, UserID: model.UserID(userID), Kind: model.AffiliationTeacher, StartsAt: model.TimeFromMillis(100),
	}
	affiliations := &affiliationHTTPApplication{result: created}
	transport := &academicUnitHTTPApplication{principal: principal}
	httpAPI, err := New(Options{Logger: logger, Health: academicUnitHTTPHealth{}, Application: transport, AcademicUnits: transport, Institutions: transport, Programmes: &programmeHTTPApplication{}, ProgrammeLevels: &programmeLevelHTTPApplication{}, AcademicPeriods: &academicPeriodHTTPApplication{}, Classes: &classHTTPApplication{}, Affiliations: affiliations, AcademicUnitMembers: &academicUnitMemberHTTPApplication{}, ClassMembers: &classMemberHTTPApplication{}, UserProfiles: &userProfileHTTPApplication{}, AccountStates: &accountStateHTTPApplication{}, SessionAdministrations: &sessionAdministrationHTTPApplication{}, Roles: &roleHTTPApplication{}, RoleBindings: &roleBindingHTTPApplication{}, AuditListings: &auditListingHTTPApplication{}, Bootstrap: &bootstrapHTTPApplication{}, BuildInfo: BuildInfo{Version: "test"}, PublicURL: "http://localhost:8065", MaxBodyBytes: 1 << 20, RecentAuthenticationTTL: time.Minute, NodeID: "node-a"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = httpAPI.Close() })
	request := httptest.NewRequest(http.MethodPost, "/api/v1/users/"+userID+"/affiliations", strings.NewReader(`{"id":"ignored","user_id":"ignored","kind":"teacher","start_at":100}`))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if affiliations.createCommand.UserID != userID || affiliations.createCommand.Kind != model.AffiliationTeacher {
		t.Fatalf("command = %#v", affiliations.createCommand)
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if _, exposed := body["revision"]; exposed {
		t.Fatalf("persistence revision leaked into compatibility DTO: %#v", body)
	}
}
