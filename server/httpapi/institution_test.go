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

type institutionHTTPApplication struct {
	principal model.Principal
	result    *model.Institution
	command   application.UpdateInstitutionCommand
}

func (a *institutionHTTPApplication) AuthenticateAccess(
	context.Context, string,
) (*model.Principal, error) {
	principal := a.principal
	return &principal, nil
}

func (a *institutionHTTPApplication) AuthenticateBearer(
	context.Context, string,
) (*model.Principal, error) {
	principal := a.principal
	return &principal, nil
}

func (a *institutionHTTPApplication) GetInstitution(
	_ context.Context,
	invocation application.Invocation,
	_ application.GetInstitutionQuery,
) (*model.Institution, error) {
	if invocation.Principal().UserID != a.principal.UserID {
		return nil, application.NewError("request.invalid")
	}
	return a.result, nil
}

func (a *institutionHTTPApplication) UpdateInstitution(
	_ context.Context,
	invocation application.Invocation,
	command application.UpdateInstitutionCommand,
) (*model.Institution, error) {
	if invocation.Principal().UserID != a.principal.UserID {
		return nil, application.NewError("request.invalid")
	}
	a.command = command
	return a.result, nil
}

func TestInstitutionHTTPMapsDTOsWithoutPermissionPreflight(t *testing.T) {
	t.Parallel()

	logger, _ := newTestLogger(t)
	institution := &model.Institution{
		ID: model.InstitutionID(model.NewId()), CreatedAt: model.TimeFromMillis(10), UpdatedAt: model.TimeFromMillis(20),
		Name: "northbridge", DisplayName: "Northbridge University",
		Description: "A university",
	}
	fakeApplication := &institutionHTTPApplication{
		principal: model.Principal{
			UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()),
			CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password",
			AuthenticationStrength: model.AuthenticationSingleFactor,
			ClientType:             model.SessionClientCLI, AuthenticatedAt: time.Now(),
		},
		result: institution,
	}
	httpAPI := newFocusedResourceAPI(t, logger, fakeApplication, institutionResource(fakeApplication))

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
	if got.ID != institution.ID.String() || got.Name != institution.Name {
		t.Fatalf("get response = %#v", got)
	}
	if got.ExamCapacity != (examCapacityPolicyResponseFromModel(model.DefaultExamCapacityPolicy())) {
		t.Fatalf("get Exam capacity = %#v", got.ExamCapacity)
	}

	patchRequest := httptest.NewRequest(
		http.MethodPatch, "/api/v1/institution",
		strings.NewReader(`{"display_name":"Northbridge","exam_capacity":{"resource_maximum_count":25,"resource_maximum_bytes":20971520,"workspace_maximum_entries":750,"workspace_maximum_file_bytes":20971520,"workspace_maximum_total_bytes":104857600}}`),
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
	wantCapacity := model.ExamCapacityPolicy{ResourceMaximumCount: 25, ResourceMaximumBytes: 20 << 20,
		WorkspaceMaximumEntries: 750, WorkspaceMaximumFileBytes: 20 << 20, WorkspaceMaximumTotalBytes: 100 << 20}
	if fakeApplication.command.ExamCapacity == nil || *fakeApplication.command.ExamCapacity != wantCapacity {
		t.Fatalf("update Exam capacity = %#v", fakeApplication.command.ExamCapacity)
	}
}

func TestInstitutionResourceRejectsUnknownPatchFields(t *testing.T) {
	t.Parallel()

	logger, _ := newTestLogger(t)
	fakeApplication := &institutionHTTPApplication{
		principal: model.Principal{
			UserID: model.NewUserID(), SessionID: model.NewSessionID(),
			CredentialID:           model.PrincipalCredentialID(model.NewId()),
			CredentialType:         model.CredentialSessionAccess,
			AuthenticationMethod:   "password",
			AuthenticationStrength: model.AuthenticationSingleFactor,
			ClientType:             model.SessionClientCLI,
			AuthenticatedAt:        time.Now(),
		},
	}
	httpAPI := newFocusedResourceAPI(
		t, logger, fakeApplication, institutionResource(fakeApplication),
	)
	request := httptest.NewRequest(
		http.MethodPatch, "/api/v1/institution",
		strings.NewReader(`{"display_name":"Northbridge","unknown":true}`),
	)
	request.Header.Set("Authorization", "Bearer test-credential")
	request.Header.Set("Content-Type", "application/json")
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

	var nullCapacity updateInstitutionRequest
	if err := json.Unmarshal([]byte(`{"exam_capacity":null}`), &nullCapacity); err != nil {
		t.Fatal(err)
	}
	if !nullCapacity.ExamCapacity.IsSet() || !nullCapacity.ExamCapacity.IsNull() || nullCapacity.ExamCapacity.ValuePointer() != nil {
		t.Fatalf("null Exam capacity = %#v", nullCapacity.ExamCapacity)
	}
}
