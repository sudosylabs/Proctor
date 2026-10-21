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

type institutionHTTPApplication struct {
	principal model.Principal
	result    *model.Institution
	command   app.UpdateInstitutionCommand
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
	invocation app.Invocation,
	_ app.GetInstitutionQuery,
) (*model.Institution, error) {
	if invocation.Principal().UserID != a.principal.UserID {
		return nil, apperror.New("request.invalid")
	}
	return a.result, nil
}

func (a *institutionHTTPApplication) UpdateInstitution(
	_ context.Context,
	invocation app.Invocation,
	command app.UpdateInstitutionCommand,
) (*model.Institution, error) {
	if invocation.Principal().UserID != a.principal.UserID {
		return nil, apperror.New("request.invalid")
	}
	a.command = command
	return a.result, nil
}

func TestInstitutionHTTPMapsDTOsWithoutPermissionPreflight(t *testing.T) {
	t.Parallel()

	logger, _ := newTestLogger(t)
	institution := &model.Institution{
		ID: model.InstitutionID(model.NewID()), CreatedAt: model.TimeFromMillis(10), UpdatedAt: model.TimeFromMillis(20),
		Name: "northbridge", DisplayName: "Northbridge University",
		Description: "A university",
	}
	fakeApplication := &institutionHTTPApplication{
		principal: model.Principal{
			UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewID()),
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
	require.Equal(t, http.StatusOK, getResponse.Code, "get status = %d: %s", getResponse.Code, getResponse.Body.String())
	var got institutionResponse
	require.NoError(t, json.Unmarshal(getResponse.Body.Bytes(), &got))
	require.Equal(t, institution.ID.String(), got.ID)
	require.Equal(t, institution.Name, got.Name)
	require.Equal(t, examCapacityPolicyResponseFromModel(model.DefaultExamCapacityPolicy()), got.ExamCapacity)

	patchRequest := httptest.NewRequest(
		http.MethodPatch, "/api/v1/institution",
		strings.NewReader(`{"display_name":"Northbridge","exam_capacity":{"resource_maximum_count":25,"resource_maximum_bytes":20971520,"workspace_maximum_entries":750,"workspace_maximum_file_bytes":20971520,"workspace_maximum_total_bytes":104857600}}`),
	)
	patchRequest.Header.Set("Authorization", "Bearer test-credential")
	patchRequest.Header.Set("Content-Type", "application/json")
	patchResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(patchResponse, patchRequest)
	require.Equal(t, http.StatusOK, patchResponse.Code, "patch status = %d: %s", patchResponse.Code, patchResponse.Body.String())
	require.NotNil(t, fakeApplication.command.DisplayName)
	require.Equal(t, "Northbridge", *fakeApplication.command.DisplayName)
	wantCapacity := model.ExamCapacityPolicy{ResourceMaximumCount: 25, ResourceMaximumBytes: 20 << 20,
		WorkspaceMaximumEntries: 750, WorkspaceMaximumFileBytes: 20 << 20, WorkspaceMaximumTotalBytes: 100 << 20}
	require.NotNil(t, fakeApplication.command.ExamCapacity)
	require.Equal(t, wantCapacity, *fakeApplication.command.ExamCapacity)
}

func TestInstitutionResourceRejectsUnknownPatchFields(t *testing.T) {
	t.Parallel()

	logger, _ := newTestLogger(t)
	fakeApplication := &institutionHTTPApplication{
		principal: model.Principal{
			UserID: model.NewUserID(), SessionID: model.NewSessionID(),
			CredentialID:           model.PrincipalCredentialID(model.NewID()),
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
	require.Equal(t, http.StatusBadRequest, response.Code, "status = %d: %s", response.Code, response.Body.String())
	var problem Problem
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &problem))
	require.Equal(t, "request.invalid", problem.Code, "problem code")
}

func TestOptionalDistinguishesInstitutionPatchWireStates(t *testing.T) {
	t.Parallel()

	var omitted updateInstitutionRequest
	require.NoError(t, json.Unmarshal([]byte(`{}`), &omitted))
	require.False(t, omitted.Name.IsSet())
	require.False(t, omitted.Name.IsNull())
	require.Nil(t, omitted.Name.ValuePointer())

	var explicitNull updateInstitutionRequest
	require.NoError(t, json.Unmarshal([]byte(`{"name":null}`), &explicitNull))
	require.True(t, explicitNull.Name.IsSet())
	require.True(t, explicitNull.Name.IsNull())
	require.Nil(t, explicitNull.Name.ValuePointer())

	var zeroValue updateInstitutionRequest
	require.NoError(t, json.Unmarshal([]byte(`{"name":""}`), &zeroValue))
	value := zeroValue.Name.ValuePointer()
	require.True(t, zeroValue.Name.IsSet(), "zero name = %#v, value", zeroValue.Name)
	require.False(t, zeroValue.Name.IsNull(), "zero name = %#v, value", zeroValue.Name)
	require.NotNil(t, value, "zero name = %#v, value", zeroValue.Name)
	require.Empty(t, *value, "zero name = %#v, value", zeroValue.Name)

	var nullCapacity updateInstitutionRequest
	require.NoError(t, json.Unmarshal([]byte(`{"exam_capacity":null}`), &nullCapacity))
	require.True(t, nullCapacity.ExamCapacity.IsSet())
	require.True(t, nullCapacity.ExamCapacity.IsNull())
	require.Nil(t, nullCapacity.ExamCapacity.ValuePointer())
}
