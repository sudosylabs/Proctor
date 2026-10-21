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

type affiliationHTTPApplication struct {
	result        *model.Affiliation
	values        []*model.Affiliation
	createCommand app.CreateAffiliationCommand
	endCommand    app.EndAffiliationCommand
	listErr       error
}

func (a *affiliationHTTPApplication) ListAffiliations(context.Context, app.Invocation, app.ListAffiliationsQuery) ([]*model.Affiliation, error) {
	return a.values, a.listErr
}
func (a *affiliationHTTPApplication) CreateAffiliation(_ context.Context, _ app.Invocation, command app.CreateAffiliationCommand) (*model.Affiliation, error) {
	a.createCommand = command
	return a.result, nil
}
func (a *affiliationHTTPApplication) EndAffiliation(_ context.Context, _ app.Invocation, command app.EndAffiliationCommand) (*model.Affiliation, error) {
	a.endCommand = command
	return a.result, nil
}

func TestAffiliationHTTPUsesDTOAndRouteOwnedUser(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewID()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now()}
	userID := model.NewID()
	created := &model.Affiliation{
		ID: model.NewAffiliationID(), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100),
		Revision: 1, UserID: model.UserID(userID), Kind: model.AffiliationTeacher, StartsAt: model.TimeFromMillis(100),
	}
	affiliations := &affiliationHTTPApplication{result: created}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, affiliationResource(affiliations))
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/users/"+userID+"/affiliations", strings.NewReader(`{"kind":"teacher","start_at":"1970-01-01T00:00:00.1Z"}`))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusCreated, response.Code, "status = %d: %s", response.Code, response.Body.String())
	require.Equal(t, model.UserID(userID), affiliations.createCommand.UserID)
	require.Equal(t, model.AffiliationTeacher, affiliations.createCommand.Kind)
	var body map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	_, exposed := body["revision"]
	require.False(t, exposed, "persistence revision leaked into compatibility DTO: %#v", body)
}

func TestAffiliationResourceSerializesAllowedAuthorizationFailure(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewID()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now()}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, affiliationResource(&affiliationHTTPApplication{listErr: apperror.New("authorization.denied")}))

	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/users/"+model.NewID()+"/affiliations", nil)
	request.Header.Set("Authorization", "Bearer credential")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusForbidden, response.Code, "status = %d: %s", response.Code, response.Body.String())
	var problem Problem
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &problem))
	require.Equal(t, "authorization.denied", problem.Code, "problem = %#v body=%s", problem, response.Body.String())
}

func TestAffiliationResourceFailsClosedForUndeclaredError(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewID()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now()}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, affiliationResource(&affiliationHTTPApplication{listErr: apperror.New("user.conflict")}))

	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/users/"+model.NewID()+"/affiliations", nil)
	request.Header.Set("Authorization", "Bearer credential")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusInternalServerError, response.Code, "status = %d: %s", response.Code, response.Body.String())
	var problem Problem
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &problem))
	require.Equal(t, "server.internal", problem.Code)
}

func TestAffiliationResourceRejectsInvalidBodyAndIdentifier(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewID()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now()}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, affiliationResource(&affiliationHTTPApplication{}))

	invalidBody := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/users/"+model.NewID()+"/affiliations", strings.NewReader(`{"kind":"teacher","unknown":true}`))
	invalidBody.Header.Set("Authorization", "Bearer credential")
	invalidBody.Header.Set("Content-Type", "application/json")
	bodyResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(bodyResponse, invalidBody)
	require.Equal(t, http.StatusBadRequest, bodyResponse.Code, "body status = %d: %s", bodyResponse.Code, bodyResponse.Body.String())

	invalidID := httptest.NewRequest(http.MethodGet, "/api/v1/users/not-a-canonical-id/affiliations", nil)
	invalidID.Header.Set("Authorization", "Bearer credential")
	idResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(idResponse, invalidID)
	require.Equal(t, http.StatusNotFound, idResponse.Code, "identifier status = %d: %s", idResponse.Code, idResponse.Body.String())
}
