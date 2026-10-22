// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
)

type classHTTPApplication struct {
	updateCommand  app.UpdateClassCommand
	archiveCommand app.ArchiveClassCommand
	result         *model.Class
	list           []*model.Class
	createCommand  app.CreateClassCommand
	searchErr      error
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
		CredentialID:           model.PrincipalCredentialID(model.NewID()),
		CredentialType:         model.CredentialSessionAccess,
		AuthenticationMethod:   "password",
		AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType:             model.SessionClientCLI,
		AuthenticatedAt:        time.Now(),
	}
	class := &model.Class{
		ID:               model.ClassID(model.NewID()),
		ProgrammeLevelID: model.ProgrammeLevelID(model.NewID()),
		AcademicPeriodID: model.AcademicPeriodID(model.NewID()),
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
	require.Equal(t, http.StatusCreated, response.Code, "status = %d: %s", response.Code, response.Body.String())
	require.Equal(t, class.ProgrammeLevelID, classes.createCommand.ProgrammeLevelID)
	require.Equal(t, class.AcademicPeriodID.String(), classes.createCommand.AcademicPeriodID)
	var body classResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Equal(t, class.ID.String(), body.ID)

	invalid := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/programme-levels/"+class.ProgrammeLevelID.String()+"/classes",
		strings.NewReader(`{"academic_period_id":"`+class.AcademicPeriodID.String()+`","name":"class-a","display_name":"Class A","unknown":true}`),
	)
	invalid.Header.Set("Authorization", "Bearer credential")
	invalid.Header.Set("Content-Type", "application/json")
	invalidResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(invalidResponse, invalid)
	require.Equal(t, http.StatusBadRequest, invalidResponse.Code, "invalid status = %d: %s", invalidResponse.Code, invalidResponse.Body.String())

	archive := httptest.NewRequest(
		http.MethodDelete,
		"/api/v1/classes/"+class.ID.String(),
		nil,
	)
	archive.Header.Set("Authorization", "Bearer credential")
	archiveResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(archiveResponse, archive)
	require.Equal(t, http.StatusNoContent, archiveResponse.Code, "archive status/cache/body = %d/%q/%q", archiveResponse.Code, archiveResponse.Header().Get("Cache-Control"), archiveResponse.Body.String())
	require.Equal(t, "no-store", archiveResponse.Header().Get("Cache-Control"), "archive status/cache/body = %d/%q/%q", archiveResponse.Code, archiveResponse.Header().Get("Cache-Control"), archiveResponse.Body.String())
	require.Equal(t, 0, archiveResponse.Body.Len(), "archive status/cache/body = %d/%q/%q", archiveResponse.Code, archiveResponse.Header().Get("Cache-Control"), archiveResponse.Body.String())
}

func TestClassResourceFailsClosedForUndeclaredApplicationError(t *testing.T) {
	t.Parallel()

	logger, _ := newTestLogger(t)
	principal := model.Principal{
		UserID:                 model.NewUserID(),
		SessionID:              model.NewSessionID(),
		CredentialID:           model.PrincipalCredentialID(model.NewID()),
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
		classResource(&classHTTPApplication{searchErr: apperror.New("class.invalid")}),
	)

	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/academic-units/"+model.NewID()+"/classes",
		nil,
	)
	request.Header.Set("Authorization", "Bearer credential")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusInternalServerError, response.Code, "status = %d: %s", response.Code, response.Body.String())
	var problem Problem
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &problem))
	require.Equal(t, "server.internal", problem.Code, "problem code")
}

func TestClassResourceFailsClosedForUndeclaredAuthenticationError(t *testing.T) {
	t.Parallel()

	logger, _ := newTestLogger(t)
	httpAPI := newFocusedResourceAPI(
		t,
		logger,
		classRouteAuthenticator{err: apperror.New("user.conflict")},
		classResource(&classHTTPApplication{}),
	)
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/classes/"+model.NewID(),
		nil,
	)
	request.Header.Set("Authorization", "Bearer credential")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusInternalServerError, response.Code, "status = %d: %s", response.Code, response.Body.String())
	var problem Problem
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &problem))
	require.Equal(t, "server.internal", problem.Code, "problem code")
}

func newFocusedResourceAPI(
	t *testing.T,
	logger Logger,
	authenticator Authenticator,
	resources ...apiResource,
) *API {
	t.Helper()
	cookies, err := newBrowserCookies("http://localhost:8065")
	require.NoError(t, err)
	httpAPI := &API{
		authenticator:           authenticator,
		logger:                  logger,
		localizer:               newTestLocalizer(t),
		cookies:                 cookies,
		browserRequests:         browserRequestPolicy{origin: "http://localhost:8065"},
		recentAuthenticationTTL: time.Minute,
	}
	require.NoError(t, httpAPI.buildRoutingKernel(
		model.APIURLSuffix,
		1<<20,
		func() error {
			return httpAPI.collectResources(model.APIURLSuffix, resources...)
		},
	))
	return httpAPI
}

func (a *classHTTPApplication) GetClass(context.Context, invocation.Invocation, app.GetClassQuery) (*model.Class, error) {
	return a.result, nil
}
func (a *classHTTPApplication) ListClasses(context.Context, invocation.Invocation, app.ListClassesQuery) (app.Page[*model.Class], error) {
	return app.Page[*model.Class]{Items: a.list}, nil
}
func (a *classHTTPApplication) SearchClasses(context.Context, invocation.Invocation, app.SearchClassesQuery) (app.Page[*model.Class], error) {
	return app.Page[*model.Class]{Items: a.list}, a.searchErr
}

func TestClassSearchMapsMissingAcademicUnit(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewID()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now()}
	classes := &classHTTPApplication{searchErr: apperror.New("resource.not_found").WithField("resource", "academic_unit")}
	transport := &academicUnitHTTPApplication{principal: principal}
	options := validHTTPOptions(t)
	options.Logger = logger
	options.Application = transport
	options.AcademicUnits = transport
	options.Institutions = transport
	options.Classes = classes
	httpAPI, err := New(options)
	require.NoError(t, err)
	t.Cleanup(func() { _ = httpAPI.Close() })
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/academic-units/"+model.NewID()+"/classes", nil)
	request.Header.Set("Authorization", "Bearer credential")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusNotFound, response.Code, "status = %d: %s", response.Code, response.Body.String())
}
func (a *classHTTPApplication) CreateClass(_ context.Context, _ invocation.Invocation, command app.CreateClassCommand) (*model.Class, error) {
	a.createCommand = command
	return a.result, nil
}
func (a *classHTTPApplication) UpdateClass(_ context.Context, _ invocation.Invocation, command app.UpdateClassCommand) (*model.Class, error) {
	a.updateCommand = command
	return a.result, nil
}
func (a *classHTTPApplication) ArchiveClass(_ context.Context, _ invocation.Invocation, command app.ArchiveClassCommand) error {
	a.archiveCommand = command
	return nil
}

func TestClassHTTPMapsBothParentsFromRouteAndBody(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewID()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now()}
	class := &model.Class{ID: model.ClassID(model.NewID()), ProgrammeLevelID: model.ProgrammeLevelID(model.NewID()), AcademicPeriodID: model.AcademicPeriodID(model.NewID()), Name: "class-a", DisplayName: "Class A"}
	classes := &classHTTPApplication{result: class}
	transport := &academicUnitHTTPApplication{principal: principal}
	options := validHTTPOptions(t)
	options.Logger = logger
	options.Application = transport
	options.AcademicUnits = transport
	options.Institutions = transport
	options.Classes = classes
	httpAPI, err := New(options)
	require.NoError(t, err)
	t.Cleanup(func() { _ = httpAPI.Close() })
	classCreatePath := "/api/v1/programme-levels/{programme_level_id:" + canonicalIDRoutePattern() + "}/classes"
	var classCreate Route
	for _, route := range httpAPI.Routes() {
		if route.Method == http.MethodPost && route.Path == classCreatePath {
			classCreate = route
			break
		}
	}
	require.NotEmpty(t, classCreate.ErrorCodes, "production Class route has no kernel-owned public error contract")
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/programme-levels/"+class.ProgrammeLevelID.String()+"/classes", strings.NewReader(`{"academic_period_id":"`+class.AcademicPeriodID.String()+`","name":"class-a","display_name":"Class A"}`))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusCreated, response.Code, "status = %d: %s", response.Code, response.Body.String())
	require.Equal(t, class.ProgrammeLevelID, classes.createCommand.ProgrammeLevelID)
	require.Equal(t, class.AcademicPeriodID.String(), classes.createCommand.AcademicPeriodID)
	var body classResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Equal(t, class.ID.String(), body.ID)
	require.Equal(t, class.AcademicPeriodID.String(), body.AcademicPeriodID)
}

func TestClassPatchOptionalWireStates(t *testing.T) {
	t.Parallel()
	var body updateClassRequest
	require.NoError(t, json.Unmarshal([]byte(`{"academic_period_id":null,"programme_level_id":""}`), &body))
	require.Nil(t, body.AcademicPeriodID.ValuePointer(), "null must remain a no-op")
	value := body.ProgrammeLevelID.ValuePointer()
	require.NotNil(t, value)
	require.Empty(t, *value)
}
