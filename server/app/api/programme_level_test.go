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
	logger, err := mlog.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logger.Shutdown() })
	principal := model.Principal{UserId: model.NewId(), SessionId: model.NewId(), CredentialId: model.NewId(), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now().UnixMilli()}
	programmeID := model.NewId()
	levels := &programmeLevelHTTPApplication{err: application.NewError("resource.not_found").WithField("resource", "programme")}
	transport := &academicUnitHTTPApplication{principal: principal}
	httpAPI, err := New(Options{
		Logger: logger, Health: academicUnitHTTPHealth{}, Application: transport,
		AcademicUnits: transport, Institutions: transport, Programmes: &programmeHTTPApplication{}, ProgrammeLevels: levels, AcademicPeriods: &academicPeriodHTTPApplication{}, Classes: &classHTTPApplication{}, Affiliations: &affiliationHTTPApplication{}, AcademicUnitMembers: &academicUnitMemberHTTPApplication{}, ClassMembers: &classMemberHTTPApplication{}, UserProfiles: &userProfileHTTPApplication{},
		BuildInfo: BuildInfo{Version: "test"}, PublicURL: "http://localhost:8065", MaxBodyBytes: 1 << 20,
		RecentAuthenticationTTL: time.Minute, NodeID: "node-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = httpAPI.Close() })
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
	logger, err := mlog.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logger.Shutdown() })
	principal := model.Principal{UserId: model.NewId(), SessionId: model.NewId(), CredentialId: model.NewId(), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now().UnixMilli()}
	level := &model.ProgrammeLevel{Id: model.NewId(), ProgrammeId: model.NewId(), Name: "year-1", DisplayName: "Year 1"}
	levels := &programmeLevelHTTPApplication{result: level}
	transport := &academicUnitHTTPApplication{principal: principal}
	httpAPI, err := New(Options{
		Logger: logger, Health: academicUnitHTTPHealth{}, Application: transport,
		AcademicUnits: transport, Institutions: transport, Programmes: &programmeHTTPApplication{}, ProgrammeLevels: levels, AcademicPeriods: &academicPeriodHTTPApplication{}, Classes: &classHTTPApplication{}, Affiliations: &affiliationHTTPApplication{}, AcademicUnitMembers: &academicUnitMemberHTTPApplication{}, ClassMembers: &classMemberHTTPApplication{}, UserProfiles: &userProfileHTTPApplication{},
		BuildInfo: BuildInfo{Version: "test"}, PublicURL: "http://localhost:8065", MaxBodyBytes: 1 << 20,
		RecentAuthenticationTTL: time.Minute, NodeID: "node-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = httpAPI.Close() })
	request := httptest.NewRequest(http.MethodPost, "/api/v1/programmes/"+level.ProgrammeId+"/levels", strings.NewReader(`{"id":"ignored","programme_id":"ignored","name":"year-1","display_name":"Year 1"}`))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if levels.createCommand.ProgrammeID != level.ProgrammeId || levels.createCommand.Name != level.Name {
		t.Fatalf("create command = %#v", levels.createCommand)
	}
	var body programmeLevelResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ID != level.Id || body.ProgrammeID != level.ProgrammeId {
		t.Fatalf("response = %#v", body)
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
