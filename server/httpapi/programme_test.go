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
	"github.com/sudosylabs/proctor/server/model"
)

type programmeHTTPApplication struct {
	archiveCommand app.ArchiveProgrammeCommand
	result         *model.Programme
	list           []*model.Programme
	createCommand  app.CreateProgrammeCommand
	updateCommand  app.UpdateProgrammeCommand
}

func (a *programmeHTTPApplication) GetProgramme(context.Context, app.Invocation, app.GetProgrammeQuery) (*model.Programme, error) {
	return a.result, nil
}
func (a *programmeHTTPApplication) ListProgrammes(context.Context, app.Invocation, app.ListProgrammesQuery) ([]*model.Programme, error) {
	return a.list, nil
}
func (a *programmeHTTPApplication) CreateProgramme(_ context.Context, _ app.Invocation, command app.CreateProgrammeCommand) (*model.Programme, error) {
	a.createCommand = command
	return a.result, nil
}
func (a *programmeHTTPApplication) UpdateProgramme(_ context.Context, _ app.Invocation, command app.UpdateProgrammeCommand) (*model.Programme, error) {
	a.updateCommand = command
	return a.result, nil
}
func (a *programmeHTTPApplication) ArchiveProgramme(_ context.Context, _ app.Invocation, command app.ArchiveProgrammeCommand) error {
	a.archiveCommand = command
	return nil
}

func TestProgrammeHTTPMapsDTOWithoutPermissionPreflight(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{
		UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewID()),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password",
		AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType:             model.SessionClientCLI, AuthenticatedAt: time.Now(),
	}
	programme := &model.Programme{ID: model.ProgrammeID(model.NewID()), AcademicUnitID: model.AcademicUnitID(model.NewID()), Name: "computer-science", DisplayName: "Computer Science"}
	programmes := &programmeHTTPApplication{result: programme}
	httpAPI := newFocusedResourceAPI(
		t, logger, classRouteAuthenticator{principal: principal}, programmeResource(programmes),
	)

	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/academic-units/"+programme.AcademicUnitID.String()+"/programmes", strings.NewReader(`{"name":"computer-science","display_name":"Computer Science"}`))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusCreated, response.Code, "status = %d: %s", response.Code, response.Body.String())
	require.Equal(t, programme.AcademicUnitID, programmes.createCommand.AcademicUnitID)
	require.Equal(t, programme.Name, programmes.createCommand.Name)
	var body programmeResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Equal(t, programme.ID.String(), body.ID)
	require.Equal(t, programme.AcademicUnitID.String(), body.AcademicUnitID)
}

func TestProgrammeResourceRejectsInvalidQueryLimit(t *testing.T) {
	t.Parallel()

	logger, _ := newTestLogger(t)
	principal := model.Principal{
		UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID:           model.PrincipalCredentialID(model.NewID()),
		CredentialType:         model.CredentialSessionAccess,
		AuthenticationMethod:   "password",
		AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType:             model.SessionClientCLI,
		AuthenticatedAt:        time.Now(),
	}
	programmes := &programmeHTTPApplication{}
	httpAPI := newFocusedResourceAPI(
		t, logger, classRouteAuthenticator{principal: principal}, programmeResource(programmes),
	)
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/academic-units/"+model.NewID()+"/programmes?limit=invalid",
		nil,
	)
	request.Header.Set("Authorization", "Bearer credential")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusBadRequest, response.Code, "status = %d: %s", response.Code, response.Body.String())
	var problem Problem
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &problem))
	require.Equal(t, "request.invalid", problem.Code, "problem code")
}

func TestProgrammePatchOptionalWireStates(t *testing.T) {
	t.Parallel()
	var body updateProgrammeRequest
	require.NoError(t, json.Unmarshal([]byte(`{"name":null,"display_name":""}`), &body))
	require.Nil(t, body.Name.ValuePointer(), "explicit null must preserve v1 no-op semantics")
	value := body.DisplayName.ValuePointer()
	require.NotNil(t, value)
	require.Empty(t, *value)
}
