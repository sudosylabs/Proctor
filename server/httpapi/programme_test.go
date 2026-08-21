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

type programmeHTTPApplication struct {
	result        *model.Programme
	list          []*model.Programme
	createCommand application.CreateProgrammeCommand
	updateCommand application.UpdateProgrammeCommand
}

func (a *programmeHTTPApplication) GetProgramme(context.Context, application.Invocation, application.GetProgrammeQuery) (*model.Programme, error) {
	return a.result, nil
}
func (a *programmeHTTPApplication) ListProgrammes(context.Context, application.Invocation, application.ListProgrammesQuery) ([]*model.Programme, error) {
	return a.list, nil
}
func (a *programmeHTTPApplication) CreateProgramme(_ context.Context, _ application.Invocation, command application.CreateProgrammeCommand) (*model.Programme, error) {
	a.createCommand = command
	return a.result, nil
}
func (a *programmeHTTPApplication) UpdateProgramme(_ context.Context, _ application.Invocation, command application.UpdateProgrammeCommand) (*model.Programme, error) {
	a.updateCommand = command
	return a.result, nil
}
func (a *programmeHTTPApplication) ArchiveProgramme(context.Context, application.Invocation, application.ArchiveProgrammeCommand) error {
	return nil
}

func TestProgrammeHTTPMapsDTOWithoutPermissionPreflight(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{
		UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password",
		AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType:             model.SessionClientCLI, AuthenticatedAt: time.Now(),
	}
	programme := &model.Programme{ID: model.ProgrammeID(model.NewId()), AcademicUnitID: model.AcademicUnitID(model.NewId()), Name: "computer-science", DisplayName: "Computer Science"}
	programmes := &programmeHTTPApplication{result: programme}
	httpAPI := newFocusedResourceAPI(
		t, logger, classRouteAuthenticator{principal: principal}, programmeResource(programmes),
	)

	request := httptest.NewRequest(http.MethodPost, "/api/v1/academic-units/"+programme.AcademicUnitID.String()+"/programmes", strings.NewReader(`{"id":"ignored","academic_unit_id":"ignored","name":"computer-science","display_name":"Computer Science"}`))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if programmes.createCommand.AcademicUnitID != programme.AcademicUnitID.String() || programmes.createCommand.Name != programme.Name {
		t.Fatalf("create command = %#v", programmes.createCommand)
	}
	var body programmeResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ID != programme.ID.String() || body.AcademicUnitID != programme.AcademicUnitID.String() {
		t.Fatalf("response = %#v", body)
	}
}

func TestProgrammeResourceRejectsInvalidQueryLimit(t *testing.T) {
	t.Parallel()

	logger, _ := newTestLogger(t)
	principal := model.Principal{
		UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID:           model.PrincipalCredentialID(model.NewId()),
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
		"/api/v1/academic-units/"+model.NewId()+"/programmes?limit=invalid",
		nil,
	)
	request.Header.Set("Authorization", "Bearer credential")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	var problem Problem
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != "request.invalid" {
		t.Fatalf("problem code = %q, want request.invalid", problem.Code)
	}
}

func TestProgrammePatchOptionalWireStates(t *testing.T) {
	t.Parallel()
	var body updateProgrammeRequest
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
