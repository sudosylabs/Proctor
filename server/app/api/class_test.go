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

type classHTTPApplication struct {
	result        *model.Class
	list          []*model.Class
	createCommand application.CreateClassCommand
	searchErr     error
}

func (a *classHTTPApplication) GetClass(context.Context, application.Invocation, application.GetClassQuery) (*model.Class, error) {
	return a.result, nil
}
func (a *classHTTPApplication) ListClasses(context.Context, application.Invocation, application.ListClassesQuery) ([]*model.Class, error) {
	return a.list, nil
}
func (a *classHTTPApplication) SearchClasses(context.Context, application.Invocation, application.SearchClassesQuery) ([]*model.Class, error) {
	return a.list, a.searchErr
}

func TestClassSearchMapsMissingAcademicUnit(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now()}
	classes := &classHTTPApplication{searchErr: application.NewError("resource.not_found").WithField("resource", "academic_unit")}
	transport := &academicUnitHTTPApplication{principal: principal}
	httpAPI, err := New(Options{Logger: logger, Health: academicUnitHTTPHealth{}, Application: transport, AcademicUnits: transport, Institutions: transport, Programmes: &programmeHTTPApplication{}, ProgrammeLevels: &programmeLevelHTTPApplication{}, AcademicPeriods: &academicPeriodHTTPApplication{}, Classes: classes, Affiliations: &affiliationHTTPApplication{}, AcademicUnitMembers: &academicUnitMemberHTTPApplication{}, ClassMembers: &classMemberHTTPApplication{}, UserProfiles: &userProfileHTTPApplication{}, AccountStates: &accountStateHTTPApplication{}, SessionAdministrations: &sessionAdministrationHTTPApplication{}, Roles: &roleHTTPApplication{}, RoleBindings: &roleBindingHTTPApplication{}, AuditListings: &auditListingHTTPApplication{}, Bootstrap: &bootstrapHTTPApplication{}, BuildInfo: BuildInfo{Version: "test"}, PublicURL: "http://localhost:8065", MaxBodyBytes: 1 << 20, RecentAuthenticationTTL: time.Minute, NodeID: "node-a"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = httpAPI.Close() })
	request := httptest.NewRequest(http.MethodGet, "/api/v1/academic-units/"+model.NewId()+"/classes", nil)
	request.Header.Set("Authorization", "Bearer credential")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
}
func (a *classHTTPApplication) CreateClass(_ context.Context, _ application.Invocation, command application.CreateClassCommand) (*model.Class, error) {
	a.createCommand = command
	return a.result, nil
}
func (a *classHTTPApplication) UpdateClass(context.Context, application.Invocation, application.UpdateClassCommand) (*model.Class, error) {
	return a.result, nil
}
func (*classHTTPApplication) ArchiveClass(context.Context, application.Invocation, application.ArchiveClassCommand) error {
	return nil
}

func TestClassHTTPMapsBothParentsAndIgnoresServerOwnedFields(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now()}
	class := &model.Class{ID: model.ClassID(model.NewId()), ProgrammeLevelID: model.ProgrammeLevelID(model.NewId()), AcademicPeriodID: model.AcademicPeriodID(model.NewId()), Name: "class-a", DisplayName: "Class A"}
	classes := &classHTTPApplication{result: class}
	transport := &academicUnitHTTPApplication{principal: principal}
	httpAPI, err := New(Options{Logger: logger, Health: academicUnitHTTPHealth{}, Application: transport, AcademicUnits: transport, Institutions: transport, Programmes: &programmeHTTPApplication{}, ProgrammeLevels: &programmeLevelHTTPApplication{}, AcademicPeriods: &academicPeriodHTTPApplication{}, Classes: classes, Affiliations: &affiliationHTTPApplication{}, AcademicUnitMembers: &academicUnitMemberHTTPApplication{}, ClassMembers: &classMemberHTTPApplication{}, UserProfiles: &userProfileHTTPApplication{}, AccountStates: &accountStateHTTPApplication{}, SessionAdministrations: &sessionAdministrationHTTPApplication{}, Roles: &roleHTTPApplication{}, RoleBindings: &roleBindingHTTPApplication{}, AuditListings: &auditListingHTTPApplication{}, Bootstrap: &bootstrapHTTPApplication{}, BuildInfo: BuildInfo{Version: "test"}, PublicURL: "http://localhost:8065", MaxBodyBytes: 1 << 20, RecentAuthenticationTTL: time.Minute, NodeID: "node-a"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = httpAPI.Close() })
	request := httptest.NewRequest(http.MethodPost, "/api/v1/programme-levels/"+class.ProgrammeLevelID.String()+"/classes", strings.NewReader(`{"id":"ignored","programme_level_id":"ignored","academic_period_id":"`+class.AcademicPeriodID.String()+`","name":"class-a","display_name":"Class A"}`))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if classes.createCommand.ProgrammeLevelID != class.ProgrammeLevelID.String() || classes.createCommand.AcademicPeriodID != class.AcademicPeriodID.String() {
		t.Fatalf("command = %#v", classes.createCommand)
	}
	var body classResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ID != class.ID.String() || body.AcademicPeriodID != class.AcademicPeriodID.String() {
		t.Fatalf("response = %#v", body)
	}
}

func TestClassPatchOptionalWireStates(t *testing.T) {
	t.Parallel()
	var body updateClassRequest
	if err := json.Unmarshal([]byte(`{"academic_period_id":null,"programme_level_id":""}`), &body); err != nil {
		t.Fatal(err)
	}
	if body.AcademicPeriodID.ValuePointer() != nil {
		t.Fatal("null must remain a no-op")
	}
	value := body.ProgrammeLevelID.ValuePointer()
	if value == nil || *value != "" {
		t.Fatalf("programme level = %#v", value)
	}
}
