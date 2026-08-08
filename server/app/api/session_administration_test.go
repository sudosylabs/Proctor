// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/mlog"
	"github.com/sudosylabs/proctor/server/model"
)

func TestAdminSessionListUsesApplicationQueryAndOmitsCredentials(t *testing.T) {
	t.Parallel()
	logger, err := mlog.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logger.Shutdown() })
	principal := model.Principal{
		UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password",
		AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType:             model.SessionClientCLI, AuthenticatedAt: time.Now(),
	}
	userID := model.NewId()
	sessionID := model.NewId()
	sessions := &sessionAdministrationHTTPApplication{
		list: []*model.Session{{
			ID: model.SessionID(sessionID), UserID: model.UserID(userID), ClientType: model.SessionClientCLI,
			AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
			AuthenticatedAt: model.TimeFromMillis(100), CreatedAt: model.TimeFromMillis(100),
			UpdatedAt: model.TimeFromMillis(100), LastActivityAt: model.TimeFromMillis(100),
			IdleExpiresAt: model.TimeFromMillis(200), ExpiresAt: model.TimeFromMillis(300),
		}},
	}
	transport := &academicUnitHTTPApplication{principal: principal}
	httpAPI, err := New(Options{
		Logger: logger, Health: academicUnitHTTPHealth{}, Application: transport,
		AcademicUnits: transport, Institutions: transport, Programmes: &programmeHTTPApplication{},
		ProgrammeLevels: &programmeLevelHTTPApplication{}, AcademicPeriods: &academicPeriodHTTPApplication{},
		Classes: &classHTTPApplication{}, Affiliations: &affiliationHTTPApplication{},
		AcademicUnitMembers: &academicUnitMemberHTTPApplication{}, ClassMembers: &classMemberHTTPApplication{},
		UserProfiles: &userProfileHTTPApplication{}, AccountStates: &accountStateHTTPApplication{},
		SessionAdministrations: sessions, Roles: &roleHTTPApplication{}, RoleBindings: &roleBindingHTTPApplication{}, AuditListings: &auditListingHTTPApplication{}, Bootstrap: &bootstrapHTTPApplication{}, BuildInfo: BuildInfo{Version: "test"},
		PublicURL: "http://localhost:8065", MaxBodyBytes: 1 << 20, RecentAuthenticationTTL: time.Minute, NodeID: "node-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = httpAPI.Close() })
	request := httptest.NewRequest(http.MethodGet, "/api/v1/users/"+userID+"/sessions?include_revoked=true", nil)
	request.Header.Set("Authorization", "Bearer credential")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if sessions.listQuery.UserID != userID || !sessions.listQuery.IncludeRevoked {
		t.Fatalf("query = %#v", sessions.listQuery)
	}
	var body []map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body) != 1 || body[0]["id"] != sessionID {
		t.Fatalf("body = %#v", body)
	}
	for _, key := range []string{"token", "access_token", "refresh_token", "credential"} {
		if _, exposed := body[0][key]; exposed {
			t.Fatalf("credential field %q exposed: %#v", key, body[0])
		}
	}
}

func TestAdminSessionRevokeUsesApplicationCommand(t *testing.T) {
	t.Parallel()
	logger, err := mlog.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logger.Shutdown() })
	principal := model.Principal{
		UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password",
		AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType:             model.SessionClientCLI, AuthenticatedAt: time.Now(),
	}
	userID := model.NewId()
	sessionID := model.NewId()
	sessions := &sessionAdministrationHTTPApplication{}
	transport := &academicUnitHTTPApplication{principal: principal}
	httpAPI, err := New(Options{
		Logger: logger, Health: academicUnitHTTPHealth{}, Application: transport,
		AcademicUnits: transport, Institutions: transport, Programmes: &programmeHTTPApplication{},
		ProgrammeLevels: &programmeLevelHTTPApplication{}, AcademicPeriods: &academicPeriodHTTPApplication{},
		Classes: &classHTTPApplication{}, Affiliations: &affiliationHTTPApplication{},
		AcademicUnitMembers: &academicUnitMemberHTTPApplication{}, ClassMembers: &classMemberHTTPApplication{},
		UserProfiles: &userProfileHTTPApplication{}, AccountStates: &accountStateHTTPApplication{},
		SessionAdministrations: sessions, Roles: &roleHTTPApplication{}, RoleBindings: &roleBindingHTTPApplication{}, AuditListings: &auditListingHTTPApplication{}, Bootstrap: &bootstrapHTTPApplication{}, BuildInfo: BuildInfo{Version: "test"},
		PublicURL: "http://localhost:8065", MaxBodyBytes: 1 << 20, RecentAuthenticationTTL: time.Minute, NodeID: "node-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = httpAPI.Close() })
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/users/"+userID+"/sessions/"+sessionID, nil)
	request.Header.Set("Authorization", "Bearer credential")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if sessions.revokeCommand.UserID != userID || sessions.revokeCommand.SessionID != sessionID {
		t.Fatalf("command = %#v", sessions.revokeCommand)
	}
}
