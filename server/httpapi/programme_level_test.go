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

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type programmeLevelHTTPApplication struct {
	result        *model.ProgrammeLevel
	list          []*model.ProgrammeLevel
	err           error
	createCommand application.CreateProgrammeLevelCommand
	updateCommand application.UpdateProgrammeLevelCommand
}

func (a *programmeLevelHTTPApplication) GetProgrammeLevel(context.Context, application.Invocation, application.GetProgrammeLevelQuery) (*model.ProgrammeLevel, error) {
	return a.result, a.err
}
func (a *programmeLevelHTTPApplication) ListProgrammeLevels(context.Context, application.Invocation, application.ListProgrammeLevelsQuery) ([]*model.ProgrammeLevel, error) {
	return a.list, a.err
}
func (a *programmeLevelHTTPApplication) CreateProgrammeLevel(_ context.Context, _ application.Invocation, command application.CreateProgrammeLevelCommand) (*model.ProgrammeLevel, error) {
	a.createCommand = command
	return a.result, a.err
}
func (a *programmeLevelHTTPApplication) UpdateProgrammeLevel(_ context.Context, _ application.Invocation, command application.UpdateProgrammeLevelCommand) (*model.ProgrammeLevel, error) {
	a.updateCommand = command
	return a.result, a.err
}
func (a *programmeLevelHTTPApplication) ArchiveProgrammeLevel(context.Context, application.Invocation, application.ArchiveProgrammeLevelCommand) error {
	return a.err
}

func TestProgrammeLevelHTTPPreservesMissingProgrammeProblemField(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now()}
	programmeID := model.NewId()
	levels := &programmeLevelHTTPApplication{err: application.NewError("resource.not_found").WithField("resource", "programme")}
	httpAPI := newFocusedResourceAPI(
		t, logger, classRouteAuthenticator{principal: principal}, programmeLevelResource(levels),
	)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/programmes/"+programmeID+"/levels", nil)
	request.Header.Set("Authorization", "Bearer credential")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	var problem Problem
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != "resource.not_found" || problem.Fields["resource"] != "programme" {
		t.Fatalf("problem = %#v", problem)
	}
}

func TestProgrammeLevelHTTPMapsDTOWithoutPermissionPreflight(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now()}
	level := &model.ProgrammeLevel{ID: model.ProgrammeLevelID(model.NewId()), ProgrammeID: model.ProgrammeID(model.NewId()), Name: "year-1", DisplayName: "Year 1"}
	levels := &programmeLevelHTTPApplication{result: level}
	httpAPI := newFocusedResourceAPI(
		t, logger, classRouteAuthenticator{principal: principal}, programmeLevelResource(levels),
	)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/programmes/"+level.ProgrammeID.String()+"/levels", strings.NewReader(`{"id":"ignored","programme_id":"ignored","name":"year-1","display_name":"Year 1"}`))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if levels.createCommand.ProgrammeID != level.ProgrammeID.String() || levels.createCommand.Name != level.Name {
		t.Fatalf("create command = %#v", levels.createCommand)
	}
	var body programmeLevelResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ID != level.ID.String() || body.ProgrammeID != level.ProgrammeID.String() {
		t.Fatalf("response = %#v", body)
	}
	invalid := httptest.NewRequest(http.MethodPost, "/api/v1/programmes/"+level.ProgrammeID.String()+"/levels", strings.NewReader(`{"name":"year-1","display_name":"Year 1","unknown":true}`))
	invalid.Header.Set("Authorization", "Bearer credential")
	invalid.Header.Set("Content-Type", "application/json")
	invalidResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid status = %d: %s", invalidResponse.Code, invalidResponse.Body.String())
	}
}

func TestProgrammeLevelPatchOptionalWireStates(t *testing.T) {
	t.Parallel()
	var body updateProgrammeLevelRequest
	if err := json.Unmarshal([]byte(`{"name":null,"display_name":""}`), &body); err != nil {
		t.Fatal(err)
	}
	if body.Name.ValuePointer() != nil {
		t.Fatal("explicit null must preserve v1 no-op semantics")
	}
	value := body.DisplayName.ValuePointer()
	if value == nil || *value != "" {
		t.Fatalf("display name = %#v", value)
	}
}
