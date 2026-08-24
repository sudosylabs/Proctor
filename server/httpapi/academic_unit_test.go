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

type academicUnitHTTPApplication struct {
	Application
	principal      model.Principal
	unit           *model.AcademicUnit
	created        *model.AcademicUnit
	createCommand  application.CreateAcademicUnitCommand
	updateCommand  application.UpdateAcademicUnitCommand
	archiveCommand application.ArchiveAcademicUnitCommand
	getErr         error
}

func (a *academicUnitHTTPApplication) AuthenticateAccess(
	context.Context, string,
) (*model.Principal, error) {
	principal := a.principal
	return &principal, nil
}

func (a *academicUnitHTTPApplication) AuthenticateBearer(
	context.Context, string,
) (*model.Principal, error) {
	principal := a.principal
	return &principal, nil
}

func (a *academicUnitHTTPApplication) GetAcademicUnit(
	_ context.Context,
	invocation application.Invocation,
	query application.GetAcademicUnitQuery,
) (*model.AcademicUnit, error) {
	if invocation.Principal().UserID != a.principal.UserID || query.ID != a.unit.ID.String() {
		return nil, application.NewError("request.invalid")
	}
	if a.getErr != nil {
		return nil, a.getErr
	}
	return a.unit, nil
}

func (a *academicUnitHTTPApplication) ListAcademicUnits(
	context.Context,
	application.Invocation,
	application.ListAcademicUnitsQuery,
) ([]*model.AcademicUnit, error) {
	return nil, nil
}

func (a *academicUnitHTTPApplication) SearchAcademicUnits(
	context.Context,
	application.Invocation,
	application.SearchAcademicUnitsQuery,
) ([]*model.AcademicUnit, error) {
	return nil, nil
}

func (a *academicUnitHTTPApplication) CreateAcademicUnit(
	_ context.Context,
	invocation application.Invocation,
	command application.CreateAcademicUnitCommand,
) (*model.AcademicUnit, error) {
	if invocation.Principal().UserID != a.principal.UserID {
		return nil, application.NewError("request.invalid")
	}
	a.createCommand = command
	return a.created, nil
}

func (a *academicUnitHTTPApplication) UpdateAcademicUnit(
	_ context.Context,
	invocation application.Invocation,
	command application.UpdateAcademicUnitCommand,
) (*model.AcademicUnit, error) {
	if invocation.Principal().UserID != a.principal.UserID {
		return nil, application.NewError("request.invalid")
	}
	a.updateCommand = command
	return a.unit, nil
}

func (a *academicUnitHTTPApplication) ArchiveAcademicUnit(
	_ context.Context,
	invocation application.Invocation,
	command application.ArchiveAcademicUnitCommand,
) error {
	if invocation.Principal().UserID != a.principal.UserID {
		return application.NewError("request.invalid")
	}
	a.archiveCommand = command
	return nil
}

type academicUnitHTTPHealth struct{}

func (academicUnitHTTPHealth) Live() bool  { return true }
func (academicUnitHTTPHealth) Ready() bool { return true }

func TestAcademicUnitResponsePreservesExistingWireShape(t *testing.T) {
	t.Parallel()

	unitID := model.NewId()
	institutionID := model.NewId()
	parentID := model.NewId()
	unit := &model.AcademicUnit{
		ID: model.AcademicUnitID(unitID), CreatedAt: model.TimeFromMillis(10),
		UpdatedAt:     model.TimeFromMillis(20),
		InstitutionID: model.InstitutionID(institutionID),
		ParentID:      model.AcademicUnitID(parentID),
		Name:          "computing", DisplayName: "Computing", Description: "School",
	}
	want, err := json.Marshal(academicUnitResponse{
		ID: unitID, CreateAt: 10, UpdateAt: 20, DeleteAt: 0,
		InstitutionID: institutionID, ParentID: parentID,
		Name: "computing", DisplayName: "Computing", Description: "School",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(academicUnitResponseFromModel(unit))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("response = %s, want existing wire shape %s", got, want)
	}
}

func TestAcademicUnitCollectionMappingReturnsJSONArrayForNoResults(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(academicUnitResponsesFromModels(nil))
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != "[]" {
		t.Fatalf("empty collection = %s, want []", encoded)
	}
}

func TestAcademicUnitHTTPReadMapsDTOWithoutPermissionPreflight(t *testing.T) {
	t.Parallel()

	logger, _ := newTestLogger(t)
	unit := &model.AcademicUnit{
		ID: model.AcademicUnitID(model.NewId()), CreatedAt: model.TimeFromMillis(10), UpdatedAt: model.TimeFromMillis(20),
		InstitutionID: model.InstitutionID(model.NewId()), Name: "computing", DisplayName: "Computing",
	}
	application := &academicUnitHTTPApplication{
		principal: model.Principal{
			UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()),
			CredentialType:         model.CredentialSessionAccess,
			AuthenticationMethod:   "password",
			AuthenticationStrength: model.AuthenticationSingleFactor,
			ClientType:             model.SessionClientCLI, AuthenticatedAt: time.Now(),
		},
		unit: unit,
	}
	httpAPI := newFocusedResourceAPI(t, logger, application, academicUnitResource(application))

	request := httptest.NewRequest(
		http.MethodGet, "/api/v1/academic-units/"+unit.ID.String(), nil,
	)
	request.Header.Set("Authorization", "Bearer test-credential")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	want, err := json.Marshal(academicUnitResponseFromModel(unit))
	if err != nil {
		t.Fatal(err)
	}
	var got academicUnitResponse
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != string(want) {
		t.Fatalf("response = %s, want %s", encoded, want)
	}
}

func TestAcademicUnitHTTPErrorUsesProblemDetailsContract(t *testing.T) {
	t.Parallel()

	logger, _ := newTestLogger(t)
	unit := &model.AcademicUnit{ID: model.AcademicUnitID(model.NewId())}
	fakeApplication := &academicUnitHTTPApplication{
		principal: model.Principal{
			UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()),
			CredentialType:         model.CredentialSessionAccess,
			AuthenticationMethod:   "password",
			AuthenticationStrength: model.AuthenticationSingleFactor,
			ClientType:             model.SessionClientCLI,
			AuthenticatedAt:        time.Now(),
		},
		unit: unit,
		getErr: application.NewError("resource.not_found").
			WithField("resource", "academic_unit"),
	}
	httpAPI := newFocusedResourceAPI(t, logger, fakeApplication, academicUnitResource(fakeApplication))

	request := httptest.NewRequest(http.MethodGet, "/api/v1/academic-units/"+unit.ID.String(), nil)
	request.Header.Set("Authorization", "Bearer test-credential")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Content-Type") != "application/problem+json" ||
		response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("problem headers = %#v", response.Header())
	}
	var problem Problem
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != "resource.not_found" || problem.Status != http.StatusNotFound ||
		problem.Type != "https://proctor.sudosylabs.com/problems/resource.not_found" ||
		problem.Fields["resource"] != "academic_unit" {
		t.Fatalf("problem = %#v", problem)
	}
}

func TestAcademicUnitHTTPCreateMapsCommandWithoutPermissionPreflight(t *testing.T) {
	t.Parallel()

	parentID := model.NewId()
	tests := []struct {
		name         string
		path         string
		wantParentID string
	}{
		{name: "root", path: "/api/v1/academic-units"},
		{
			name: "child", path: "/api/v1/academic-units/" + parentID + "/children",
			wantParentID: parentID,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			logger, _ := newTestLogger(t)
			created := &model.AcademicUnit{
				ID: model.AcademicUnitID(model.NewId()), CreatedAt: model.TimeFromMillis(10), UpdatedAt: model.TimeFromMillis(10),
				InstitutionID: model.InstitutionID(model.NewId()), ParentID: model.AcademicUnitID(tt.wantParentID),
				Name: "computing", DisplayName: "Computing", Description: "School",
			}
			fakeApplication := &academicUnitHTTPApplication{
				principal: model.Principal{
					UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()),
					CredentialType:       model.CredentialSessionAccess,
					AuthenticationMethod: "password", ClientType: model.SessionClientCLI,
					AuthenticationStrength: model.AuthenticationSingleFactor,
					AuthenticatedAt:        time.Now(),
				},
				unit: created, created: created,
			}
			options := validHTTPOptions(t)
			options.Logger = logger
			options.Application = fakeApplication
			options.AcademicUnits = fakeApplication
			options.Institutions = fakeApplication
			httpAPI, err := New(options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = httpAPI.Close() })

			request := httptest.NewRequest(
				http.MethodPost, tt.path,
				strings.NewReader(`{
					"id":"client-owned-id",
					"create_at":1,
					"update_at":2,
					"delete_at":3,
					"institution_id":"client-institution",
					"parent_id":"client-parent",
					"name":"computing",
					"display_name":"Computing",
					"description":"School"
				}`),
			)
			request.Header.Set("Authorization", "Bearer test-credential")
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			httpAPI.ServeHTTP(response, request)
			if response.Code != http.StatusCreated {
				t.Fatalf("status = %d: %s", response.Code, response.Body.String())
			}
			wantCommand := application.CreateAcademicUnitCommand{
				ParentID: tt.wantParentID, Name: "computing",
				DisplayName: "Computing", Description: "School",
			}
			if fakeApplication.createCommand != wantCommand {
				t.Fatalf("command = %#v, want %#v", fakeApplication.createCommand, wantCommand)
			}
			var got academicUnitResponse
			if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.ID != created.ID.String() || got.ParentID != tt.wantParentID {
				t.Fatalf("response = %#v", got)
			}
		})
	}
}

func TestAcademicUnitHTTPMutationsMapCommandsWithoutPermissionPreflight(t *testing.T) {
	t.Parallel()

	unit := &model.AcademicUnit{
		ID: model.AcademicUnitID(model.NewId()), CreatedAt: model.TimeFromMillis(10), UpdatedAt: model.TimeFromMillis(20),
		InstitutionID: model.InstitutionID(model.NewId()), ParentID: model.AcademicUnitID(model.NewId()),
		Name: "computing", DisplayName: "Computing", Description: "School",
	}
	fakeApplication := &academicUnitHTTPApplication{
		principal: model.Principal{
			UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()),
			CredentialType:       model.CredentialSessionAccess,
			AuthenticationMethod: "password", ClientType: model.SessionClientCLI,
			AuthenticationStrength: model.AuthenticationSingleFactor,
			AuthenticatedAt:        time.Now(),
		},
		unit: unit,
	}
	logger, _ := newTestLogger(t)
	options := validHTTPOptions(t)
	options.Logger = logger
	options.Application = fakeApplication
	options.AcademicUnits = fakeApplication
	options.Institutions = fakeApplication
	httpAPI, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = httpAPI.Close() })

	parentID := model.NewId()
	name := "engineering"
	patchRequest := httptest.NewRequest(
		http.MethodPatch, "/api/v1/academic-units/"+unit.ID.String(),
		strings.NewReader(`{"parent_id":"`+parentID+`","name":"`+name+`"}`),
	)
	patchRequest.Header.Set("Authorization", "Bearer test-credential")
	patchRequest.Header.Set("Content-Type", "application/json")
	patchResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(patchResponse, patchRequest)
	if patchResponse.Code != http.StatusOK {
		t.Fatalf("patch status = %d: %s", patchResponse.Code, patchResponse.Body.String())
	}
	if fakeApplication.updateCommand.ID != unit.ID.String() ||
		fakeApplication.updateCommand.ParentID == nil ||
		*fakeApplication.updateCommand.ParentID != parentID ||
		fakeApplication.updateCommand.Name == nil ||
		*fakeApplication.updateCommand.Name != name {
		t.Fatalf("update command = %#v", fakeApplication.updateCommand)
	}

	archiveRequest := httptest.NewRequest(
		http.MethodDelete, "/api/v1/academic-units/"+unit.ID.String(), nil,
	)
	archiveRequest.Header.Set("Authorization", "Bearer test-credential")
	archiveResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(archiveResponse, archiveRequest)
	if archiveResponse.Code != http.StatusNoContent {
		t.Fatalf("archive status = %d: %s", archiveResponse.Code, archiveResponse.Body.String())
	}
	if archiveResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("archive cache control = %q", archiveResponse.Header().Get("Cache-Control"))
	}
	if fakeApplication.archiveCommand.ID != unit.ID.String() {
		t.Fatalf("archive command = %#v", fakeApplication.archiveCommand)
	}
}
