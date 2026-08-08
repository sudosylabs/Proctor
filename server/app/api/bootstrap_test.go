// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/mlog"
	"github.com/sudosylabs/proctor/server/model"
)

func TestBootstrapStatusExposesOnlyInitializedFlag(t *testing.T) {
	t.Parallel()
	logger, err := mlog.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logger.Shutdown() })
	transport := &academicUnitHTTPApplication{principal: model.Principal{
		UserId: model.NewId(), SessionId: model.NewId(), CredentialId: model.NewId(),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password",
		AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType:             model.SessionClientCLI, AuthenticatedAt: time.Now().UnixMilli(),
	}}
	httpAPI, err := New(Options{
		Logger: logger, Health: academicUnitHTTPHealth{}, Application: transport,
		AcademicUnits: transport, Institutions: transport, Programmes: &programmeHTTPApplication{},
		ProgrammeLevels: &programmeLevelHTTPApplication{}, AcademicPeriods: &academicPeriodHTTPApplication{},
		Classes: &classHTTPApplication{}, Affiliations: &affiliationHTTPApplication{},
		AcademicUnitMembers: &academicUnitMemberHTTPApplication{}, ClassMembers: &classMemberHTTPApplication{},
		UserProfiles: &userProfileHTTPApplication{}, AccountStates: &accountStateHTTPApplication{},
		SessionAdministrations: &sessionAdministrationHTTPApplication{}, Roles: &roleHTTPApplication{},
		RoleBindings: &roleBindingHTTPApplication{}, AuditListings: &auditListingHTTPApplication{},
		Bootstrap: &bootstrapHTTPApplication{status: &model.InstallationStatus{Initialized: true}},
		BuildInfo: BuildInfo{Version: "test"}, PublicURL: "http://localhost:8065",
		MaxBodyBytes: 1 << 20, RecentAuthenticationTTL: time.Minute, NodeID: "node-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = httpAPI.Close() })
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/bootstrap", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["initialized"] != true {
		t.Fatalf("body = %#v", body)
	}
	for _, key := range []string{"institution_id", "administrator_user_id", "state", "role", "password"} {
		if _, exists := body[key]; exists {
			t.Fatalf("status leaked %q: %#v", key, body)
		}
	}
}

func TestBootstrapResponseDTOPreservesHistoricalEnvelope(t *testing.T) {
	t.Parallel()
	institutionID := model.NewId()
	adminID := model.NewId()
	roleID := model.NewId()
	bindingID := model.NewId()
	result := &model.InstallationBootstrapResult{
		State: &model.InstallationState{
			InitializedAt: 100, InstitutionId: institutionID, AdministratorUserId: adminID,
		},
		Institution: &model.Institution{
			ID: model.InstitutionID(institutionID), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100),
			Name: "northbridge", DisplayName: "Northbridge",
		},
		Administrator: &model.User{
			ID: model.UserID(adminID), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100),
			Username: "admin", Email: "admin@example.com",
			Locale: "en", Timezone: "UTC",
		},
		Role: &model.Role{
			Id: roleID, CreateAt: 100, UpdateAt: 100, Name: model.SystemAdministratorRoleName,
			DisplayName: "System Administrator", BuiltIn: true,
			Permissions: []string{string(model.ActionUserView)},
		},
		RoleBinding: &model.RoleBinding{
			Id: bindingID, CreateAt: 100, UpdateAt: 100, UserId: adminID, RoleId: roleID,
			ScopeType: model.RoleScopeInstitution, ScopeId: institutionID, StartAt: 100,
		},
	}
	encoded, err := json.Marshal(installationBootstrapResponseFromModel(result))
	if err != nil {
		t.Fatal(err)
	}
	var decoded model.InstallationBootstrapResult
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.State == nil || decoded.State.InstitutionId != institutionID ||
		decoded.Administrator == nil || decoded.Administrator.ID.String() != adminID ||
		decoded.Role == nil || decoded.Role.Name != model.SystemAdministratorRoleName || !decoded.Role.BuiltIn ||
		decoded.RoleBinding == nil || decoded.RoleBinding.ScopeType != model.RoleScopeInstitution {
		t.Fatalf("decoded historical envelope = %#v from %s", decoded, encoded)
	}
}

func TestBootstrapUsesApplicationCommand(t *testing.T) {
	t.Parallel()
	logger, err := mlog.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logger.Shutdown() })
	rec := &recordingBootstrap{
		result: &model.InstallationBootstrapResult{
			State: &model.InstallationState{
				InitializedAt: 1, InstitutionId: model.NewId(), AdministratorUserId: model.NewId(),
			},
		},
	}
	transport := &academicUnitHTTPApplication{principal: model.Principal{
		UserId: model.NewId(), SessionId: model.NewId(), CredentialId: model.NewId(),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password",
		AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType:             model.SessionClientCLI, AuthenticatedAt: time.Now().UnixMilli(),
	}}
	httpAPI, err := New(Options{
		Logger: logger, Health: academicUnitHTTPHealth{}, Application: transport,
		AcademicUnits: transport, Institutions: transport, Programmes: &programmeHTTPApplication{},
		ProgrammeLevels: &programmeLevelHTTPApplication{}, AcademicPeriods: &academicPeriodHTTPApplication{},
		Classes: &classHTTPApplication{}, Affiliations: &affiliationHTTPApplication{},
		AcademicUnitMembers: &academicUnitMemberHTTPApplication{}, ClassMembers: &classMemberHTTPApplication{},
		UserProfiles: &userProfileHTTPApplication{}, AccountStates: &accountStateHTTPApplication{},
		SessionAdministrations: &sessionAdministrationHTTPApplication{}, Roles: &roleHTTPApplication{},
		RoleBindings: &roleBindingHTTPApplication{}, AuditListings: &auditListingHTTPApplication{},
		Bootstrap: rec,
		BuildInfo: BuildInfo{Version: "test"}, PublicURL: "http://localhost:8065",
		MaxBodyBytes: 1 << 20, RecentAuthenticationTTL: time.Minute, NodeID: "node-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = httpAPI.Close() })
	body, _ := json.Marshal(map[string]any{
		"institution":   map[string]any{"name": "northbridge", "display_name": "Northbridge"},
		"administrator": map[string]any{"username": "admin", "email": "admin@example.com"},
		"password":      "correct-horse-battery",
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/bootstrap", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.RemoteAddr = "127.0.0.1:1234"
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if rec.command.InstitutionName != "northbridge" || rec.command.AdministratorUsername != "admin" ||
		rec.command.Password != "correct-horse-battery" || rec.command.Source == "" {
		t.Fatalf("command = %#v", rec.command)
	}
}

type recordingBootstrap struct {
	command application.BootstrapInstallationCommand
	result  *model.InstallationBootstrapResult
}

func (r *recordingBootstrap) GetInstallationStatus(context.Context, application.GetInstallationStatusQuery) (*model.InstallationStatus, error) {
	return &model.InstallationStatus{Initialized: false}, nil
}

func (r *recordingBootstrap) BootstrapInstallation(_ context.Context, _ application.Invocation, command application.BootstrapInstallationCommand) (*model.InstallationBootstrapResult, error) {
	r.command = command
	return r.result, nil
}
