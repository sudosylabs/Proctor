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

type classHTTPApplication struct {
	result        *model.Class
	list          []*model.Class
	createCommand application.CreateClassCommand
	searchErr     error
}

type classRouteAuthenticator struct {
	principal model.Principal
	err       error
}

func (a classRouteAuthenticator) AuthenticateAccess(context.Context, string) (*model.Principal, error) {
	if a.err != nil {
		return nil, a.err
	}
	principal := a.principal
	return &principal, nil
}

func (a classRouteAuthenticator) AuthenticateBearer(context.Context, string) (*model.Principal, error) {
	if a.err != nil {
		return nil, a.err
	}
	principal := a.principal
	return &principal, nil
}

func TestClassResourceRunsThroughRoutingKernelWithNarrowDependencies(t *testing.T) {
	t.Parallel()

	logger, _ := newTestLogger(t)
	principal := model.Principal{
		UserID:                 model.NewUserID(),
		SessionID:              model.NewSessionID(),
		CredentialID:           model.PrincipalCredentialID(model.NewId()),
		CredentialType:         model.CredentialSessionAccess,
		AuthenticationMethod:   "password",
		AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType:             model.SessionClientCLI,
		AuthenticatedAt:        time.Now(),
	}
	class := &model.Class{
		ID:               model.ClassID(model.NewId()),
		ProgrammeLevelID: model.ProgrammeLevelID(model.NewId()),
		AcademicPeriodID: model.AcademicPeriodID(model.NewId()),
		Name:             "class-a",
		DisplayName:      "Class A",
	}
	classes := &classHTTPApplication{result: class}
	httpAPI := newFocusedResourceAPI(
		t,
		logger,
		classRouteAuthenticator{principal: principal},
		classResource(classes),
	)

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/programme-levels/"+class.ProgrammeLevelID.String()+"/classes",
		strings.NewReader(`{"academic_period_id":"`+class.AcademicPeriodID.String()+`","name":"class-a","display_name":"Class A"}`),
	)
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if classes.createCommand.ProgrammeLevelID != class.ProgrammeLevelID.String() ||
		classes.createCommand.AcademicPeriodID != class.AcademicPeriodID.String() {
		t.Fatalf("command = %#v", classes.createCommand)
	}
	var body classResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ID != class.ID.String() {
		t.Fatalf("response = %#v", body)
	}

	invalid := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/programme-levels/"+class.ProgrammeLevelID.String()+"/classes",
		strings.NewReader(`{"academic_period_id":"`+class.AcademicPeriodID.String()+`","name":"class-a","display_name":"Class A","unknown":true}`),
	)
	invalid.Header.Set("Authorization", "Bearer credential")
	invalid.Header.Set("Content-Type", "application/json")
	invalidResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid status = %d: %s", invalidResponse.Code, invalidResponse.Body.String())
	}

	archive := httptest.NewRequest(
		http.MethodDelete,
		"/api/v1/classes/"+class.ID.String(),
		nil,
	)
	archive.Header.Set("Authorization", "Bearer credential")
	archiveResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(archiveResponse, archive)
	if archiveResponse.Code != http.StatusNoContent ||
		archiveResponse.Header().Get("Cache-Control") != "no-store" ||
		archiveResponse.Body.Len() != 0 {
		t.Fatalf(
			"archive status/cache/body = %d/%q/%q",
			archiveResponse.Code,
			archiveResponse.Header().Get("Cache-Control"),
			archiveResponse.Body.String(),
		)
	}
}

func TestClassResourceFailsClosedForUndeclaredApplicationError(t *testing.T) {
	t.Parallel()

	logger, _ := newTestLogger(t)
	principal := model.Principal{
		UserID:                 model.NewUserID(),
		SessionID:              model.NewSessionID(),
		CredentialID:           model.PrincipalCredentialID(model.NewId()),
		CredentialType:         model.CredentialSessionAccess,
		AuthenticationMethod:   "password",
		AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType:             model.SessionClientCLI,
		AuthenticatedAt:        time.Now(),
	}
	httpAPI := newFocusedResourceAPI(
		t,
		logger,
		classRouteAuthenticator{principal: principal},
		classResource(&classHTTPApplication{searchErr: application.NewError("class.invalid")}),
	)

	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/academic-units/"+model.NewId()+"/classes",
		nil,
	)
	request.Header.Set("Authorization", "Bearer credential")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	var problem Problem
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != "internal" {
		t.Fatalf("problem code = %q, want internal", problem.Code)
	}
}

func TestClassResourceFailsClosedForUndeclaredAuthenticationError(t *testing.T) {
	t.Parallel()

	logger, _ := newTestLogger(t)
	httpAPI := newFocusedResourceAPI(
		t,
		logger,
		classRouteAuthenticator{err: application.NewError("user.conflict")},
		classResource(&classHTTPApplication{}),
	)
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/classes/"+model.NewId(),
		nil,
	)
	request.Header.Set("Authorization", "Bearer credential")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	var problem Problem
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != "internal" {
		t.Fatalf("problem code = %q, want internal", problem.Code)
	}
}

func newFocusedResourceAPI(
	t *testing.T,
	logger Logger,
	authenticator Authenticator,
	resources ...resource,
) *API {
	t.Helper()
	cookies, err := newBrowserCookies("http://localhost:8065")
	if err != nil {
		t.Fatal(err)
	}
	httpAPI := &API{
		authenticator:           authenticator,
		logger:                  logger,
		localizer:               newTestLocalizer(t),
		cookies:                 cookies,
		recentAuthenticationTTL: time.Minute,
	}
	if err := httpAPI.buildRoutingKernel(
		model.APIURLSuffix,
		1<<20,
		func() error {
			return httpAPI.collectResources(model.APIURLSuffix, resources...)
		},
	); err != nil {
		t.Fatal(err)
	}
	return httpAPI
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
	httpAPI, err := New(Options{Logger: logger, Localizer: newTestLocalizer(t), Health: academicUnitHTTPHealth{}, Application: transport, AcademicUnits: transport, Institutions: transport, Programmes: &programmeHTTPApplication{}, ProgrammeLevels: &programmeLevelHTTPApplication{}, AcademicPeriods: &academicPeriodHTTPApplication{}, Classes: classes, Affiliations: &affiliationHTTPApplication{}, AcademicUnitMembers: &academicUnitMemberHTTPApplication{}, ClassMembers: &classMemberHTTPApplication{}, UserProfiles: &userProfileHTTPApplication{}, AccountStates: &accountStateHTTPApplication{}, SessionAdministrations: &sessionAdministrationHTTPApplication{}, Roles: &roleHTTPApplication{}, RoleBindings: &roleBindingHTTPApplication{}, AuditListings: &auditListingHTTPApplication{}, Bootstrap: &bootstrapHTTPApplication{}, BuildInfo: BuildInfo{Version: "test"}, PublicURL: "http://localhost:8065", MaxBodyBytes: 1 << 20, RecentAuthenticationTTL: time.Minute, NodeID: "node-a"})
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
	httpAPI, err := New(Options{Logger: logger, Localizer: newTestLocalizer(t), Health: academicUnitHTTPHealth{}, Application: transport, AcademicUnits: transport, Institutions: transport, Programmes: &programmeHTTPApplication{}, ProgrammeLevels: &programmeLevelHTTPApplication{}, AcademicPeriods: &academicPeriodHTTPApplication{}, Classes: classes, Affiliations: &affiliationHTTPApplication{}, AcademicUnitMembers: &academicUnitMemberHTTPApplication{}, ClassMembers: &classMemberHTTPApplication{}, UserProfiles: &userProfileHTTPApplication{}, AccountStates: &accountStateHTTPApplication{}, SessionAdministrations: &sessionAdministrationHTTPApplication{}, Roles: &roleHTTPApplication{}, RoleBindings: &roleBindingHTTPApplication{}, AuditListings: &auditListingHTTPApplication{}, Bootstrap: &bootstrapHTTPApplication{}, BuildInfo: BuildInfo{Version: "test"}, PublicURL: "http://localhost:8065", MaxBodyBytes: 1 << 20, RecentAuthenticationTTL: time.Minute, NodeID: "node-a"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = httpAPI.Close() })
	classCreatePath := "/api/v1/programme-levels/{programme_level_id:" + canonicalIDRoutePattern() + "}/classes"
	var classCreate Route
	for _, route := range httpAPI.Routes() {
		if route.Method == http.MethodPost && route.Path == classCreatePath {
			classCreate = route
			break
		}
	}
	if len(classCreate.ErrorCodes) == 0 {
		t.Fatal("production Class route has no kernel-owned public error contract")
	}
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
