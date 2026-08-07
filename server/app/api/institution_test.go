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

type institutionHTTPApplication struct {
	principal model.Principal
	result    *model.Institution
	command   application.UpdateInstitutionCommand
}

func (a *institutionHTTPApplication) AuthenticateAccess(
	context.Context, string,
) (*model.Principal, *model.AppError) {
	principal := a.principal
	return &principal, nil
}

func (a *institutionHTTPApplication) AuthenticateBearer(
	context.Context, string,
) (*model.Principal, *model.AppError) {
	principal := a.principal
	return &principal, nil
}

func (a *institutionHTTPApplication) GetInstitution(
	_ context.Context,
	invocation application.Invocation,
	_ application.GetInstitutionQuery,
) (*model.Institution, error) {
	if invocation.Principal().UserId != a.principal.UserId {
		return nil, application.NewError("request.invalid")
	}
	return a.result, nil
}

func (a *institutionHTTPApplication) UpdateInstitution(
	_ context.Context,
	invocation application.Invocation,
	command application.UpdateInstitutionCommand,
) (*model.Institution, error) {
	if invocation.Principal().UserId != a.principal.UserId {
		return nil, application.NewError("request.invalid")
	}
	a.command = command
	return a.result, nil
}

func TestInstitutionHTTPMapsDTOsWithoutPermissionPreflight(t *testing.T) {
	t.Parallel()

	logger, err := mlog.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logger.Shutdown() })
	institution := &model.Institution{
		Id: model.NewId(), CreateAt: 10, UpdateAt: 20,
		Name: "northbridge", DisplayName: "Northbridge University",
		Description: "A university",
	}
	fakeApplication := &institutionHTTPApplication{
		principal: model.Principal{
			UserId: model.NewId(), SessionId: model.NewId(), CredentialId: model.NewId(),
			CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password",
			AuthenticationStrength: model.AuthenticationSingleFactor,
			ClientType:             model.SessionClientCLI, AuthenticatedAt: time.Now().UnixMilli(),
		},
		result: institution,
	}
	transportApplication := &academicUnitHTTPApplication{principal: fakeApplication.principal}
	httpAPI, err := New(Options{
		Logger: logger, Health: academicUnitHTTPHealth{}, Application: transportApplication,
		AcademicUnits: transportApplication, Institutions: fakeApplication,
		Programmes:          &programmeHTTPApplication{},
		ProgrammeLevels:     &programmeLevelHTTPApplication{},
		AcademicPeriods:     &academicPeriodHTTPApplication{},
		Classes:             &classHTTPApplication{},
		Affiliations:        &affiliationHTTPApplication{},
		AcademicUnitMembers: &academicUnitMemberHTTPApplication{}, ClassMembers: &classMemberHTTPApplication{}, UserProfiles: &userProfileHTTPApplication{}, AccountStates: &accountStateHTTPApplication{}, SessionAdministrations: &sessionAdministrationHTTPApplication{}, Roles: &roleHTTPApplication{},
		BuildInfo: BuildInfo{Version: "test"},
		PublicURL: "http://localhost:8065", MaxBodyBytes: 1 << 20,
		RecentAuthenticationTTL: time.Minute, NodeID: "node-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = httpAPI.Close() })

	getRequest := httptest.NewRequest(http.MethodGet, "/api/v1/institution", nil)
	getRequest.Header.Set("Authorization", "Bearer test-credential")
	getResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(getResponse, getRequest)
	if getResponse.Code != http.StatusOK {
		t.Fatalf("get status = %d: %s", getResponse.Code, getResponse.Body.String())
	}
	var got institutionResponse
	if err := json.Unmarshal(getResponse.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != institution.Id || got.Name != institution.Name {
		t.Fatalf("get response = %#v", got)
	}

	patchRequest := httptest.NewRequest(
		http.MethodPatch, "/api/v1/institution",
		strings.NewReader(`{"display_name":"Northbridge"}`),
	)
	patchRequest.Header.Set("Authorization", "Bearer test-credential")
	patchRequest.Header.Set("Content-Type", "application/json")
	patchResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(patchResponse, patchRequest)
	if patchResponse.Code != http.StatusOK {
		t.Fatalf("patch status = %d: %s", patchResponse.Code, patchResponse.Body.String())
	}
	if fakeApplication.command.DisplayName == nil ||
		*fakeApplication.command.DisplayName != "Northbridge" {
		t.Fatalf("update command = %#v", fakeApplication.command)
	}
}

func TestOptionalDistinguishesInstitutionPatchWireStates(t *testing.T) {
	t.Parallel()

	var omitted updateInstitutionRequest
	if err := json.Unmarshal([]byte(`{}`), &omitted); err != nil {
		t.Fatal(err)
	}
	if omitted.Name.IsSet() || omitted.Name.IsNull() || omitted.Name.ValuePointer() != nil {
		t.Fatalf("omitted name = %#v", omitted.Name)
	}

	var explicitNull updateInstitutionRequest
	if err := json.Unmarshal([]byte(`{"name":null}`), &explicitNull); err != nil {
		t.Fatal(err)
	}
	if !explicitNull.Name.IsSet() || !explicitNull.Name.IsNull() ||
		explicitNull.Name.ValuePointer() != nil {
		t.Fatalf("null name = %#v", explicitNull.Name)
	}

	var zeroValue updateInstitutionRequest
	if err := json.Unmarshal([]byte(`{"name":""}`), &zeroValue); err != nil {
		t.Fatal(err)
	}
	value := zeroValue.Name.ValuePointer()
	if !zeroValue.Name.IsSet() || zeroValue.Name.IsNull() || value == nil || *value != "" {
		t.Fatalf("zero name = %#v, value = %#v", zeroValue.Name, value)
	}
}
