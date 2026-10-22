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
	"github.com/sudosylabs/proctor/server/model"
)

type programmeLevelHTTPApplication struct {
	archiveCommand app.ArchiveProgrammeLevelCommand
	result         *model.ProgrammeLevel
	list           []*model.ProgrammeLevel
	err            error
	createCommand  app.CreateProgrammeLevelCommand
	updateCommand  app.UpdateProgrammeLevelCommand
}

func (a *programmeLevelHTTPApplication) GetProgrammeLevel(context.Context, app.Invocation, app.GetProgrammeLevelQuery) (*model.ProgrammeLevel, error) {
	return a.result, a.err
}
func (a *programmeLevelHTTPApplication) ListProgrammeLevels(context.Context, app.Invocation, app.ListProgrammeLevelsQuery) (app.Page[*model.ProgrammeLevel], error) {
	return app.Page[*model.ProgrammeLevel]{Items: a.list}, a.err
}
func (a *programmeLevelHTTPApplication) CreateProgrammeLevel(_ context.Context, _ app.Invocation, command app.CreateProgrammeLevelCommand) (*model.ProgrammeLevel, error) {
	a.createCommand = command
	return a.result, a.err
}
func (a *programmeLevelHTTPApplication) UpdateProgrammeLevel(_ context.Context, _ app.Invocation, command app.UpdateProgrammeLevelCommand) (*model.ProgrammeLevel, error) {
	a.updateCommand = command
	return a.result, a.err
}
func (a *programmeLevelHTTPApplication) ArchiveProgrammeLevel(_ context.Context, _ app.Invocation, command app.ArchiveProgrammeLevelCommand) error {
	a.archiveCommand = command
	return a.err
}

func TestProgrammeLevelHTTPPreservesMissingProgrammeProblemField(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewID()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now()}
	programmeID := model.NewID()
	levels := &programmeLevelHTTPApplication{err: apperror.New("resource.not_found").WithField("resource", "programme")}
	httpAPI := newFocusedResourceAPI(
		t, logger, classRouteAuthenticator{principal: principal}, programmeLevelResource(levels),
	)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/programmes/"+programmeID+"/levels", nil)
	request.Header.Set("Authorization", "Bearer credential")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusNotFound, response.Code, "status = %d: %s", response.Code, response.Body.String())
	var problem Problem
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &problem))
	require.Equal(t, "resource.not_found", problem.Code)
	require.Equal(t, "programme", problem.Fields["resource"])
}

func TestProgrammeLevelHTTPMapsDTOWithoutPermissionPreflight(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewID()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now()}
	level := &model.ProgrammeLevel{ID: model.ProgrammeLevelID(model.NewID()), ProgrammeID: model.ProgrammeID(model.NewID()), Name: "year-1", DisplayName: "Year 1"}
	levels := &programmeLevelHTTPApplication{result: level}
	httpAPI := newFocusedResourceAPI(
		t, logger, classRouteAuthenticator{principal: principal}, programmeLevelResource(levels),
	)
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/programmes/"+level.ProgrammeID.String()+"/levels", strings.NewReader(`{"name":"year-1","display_name":"Year 1"}`))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusCreated, response.Code, "status = %d: %s", response.Code, response.Body.String())
	require.Equal(t, level.ProgrammeID, levels.createCommand.ProgrammeID)
	require.Equal(t, level.Name, levels.createCommand.Name)
	var body programmeLevelResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Equal(t, level.ID.String(), body.ID)
	require.Equal(t, level.ProgrammeID.String(), body.ProgrammeID)
	invalid := httptest.NewRequest(http.MethodPost, "/api/v1/programmes/"+level.ProgrammeID.String()+"/levels", strings.NewReader(`{"name":"year-1","display_name":"Year 1","unknown":true}`))
	invalid.Header.Set("Authorization", "Bearer credential")
	invalid.Header.Set("Content-Type", "application/json")
	invalidResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(invalidResponse, invalid)
	require.Equal(t, http.StatusBadRequest, invalidResponse.Code, "invalid status = %d: %s", invalidResponse.Code, invalidResponse.Body.String())
}

func TestProgrammeLevelPatchOptionalWireStates(t *testing.T) {
	t.Parallel()
	var body updateProgrammeLevelRequest
	require.NoError(t, json.Unmarshal([]byte(`{"name":null,"display_name":""}`), &body))
	require.Nil(t, body.Name.ValuePointer(), "explicit null must preserve v1 no-op semantics")
	value := body.DisplayName.ValuePointer()
	require.NotNil(t, value)
	require.Empty(t, *value)
}
