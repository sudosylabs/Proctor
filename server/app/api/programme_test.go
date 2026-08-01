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
	"github.com/sudosylabs/proctor/server/mlog"
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
	logger, err := mlog.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logger.Shutdown() })
	principal := model.Principal{
		UserId: model.NewId(), SessionId: model.NewId(), CredentialId: model.NewId(),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password",
		AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType:             model.SessionClientCLI, AuthenticatedAt: time.Now().UnixMilli(),
	}
	programme := &model.Programme{Id: model.NewId(), AcademicUnitId: model.NewId(), Name: "computer-science", DisplayName: "Computer Science"}
	programmes := &programmeHTTPApplication{result: programme}
	transport := &academicUnitHTTPApplication{principal: principal}
	httpAPI, err := New(Options{
		Logger: logger, Health: academicUnitHTTPHealth{}, Application: transport,
		AcademicUnits: transport, Institutions: transport, Programmes: programmes,
		BuildInfo: BuildInfo{Version: "test"}, PublicURL: "http://localhost:8065",
		MaxBodyBytes: 1 << 20, RecentAuthenticationTTL: time.Minute, NodeID: "node-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = httpAPI.Close() })

	request := httptest.NewRequest(http.MethodPost, "/api/v1/academic-units/"+programme.AcademicUnitId+"/programmes", strings.NewReader(`{"id":"ignored","academic_unit_id":"ignored","name":"computer-science","display_name":"Computer Science"}`))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if programmes.createCommand.AcademicUnitID != programme.AcademicUnitId || programmes.createCommand.Name != programme.Name {
		t.Fatalf("create command = %#v", programmes.createCommand)
	}
	var body programmeResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ID != programme.Id || body.AcademicUnitID != programme.AcademicUnitId {
		t.Fatalf("response = %#v", body)
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
