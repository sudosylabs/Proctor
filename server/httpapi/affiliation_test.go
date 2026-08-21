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

type affiliationHTTPApplication struct {
	result        *model.Affiliation
	values        []*model.Affiliation
	createCommand application.CreateAffiliationCommand
	endCommand    application.EndAffiliationCommand
	listErr       error
}

func (a *affiliationHTTPApplication) ListAffiliations(context.Context, application.Invocation, application.ListAffiliationsQuery) ([]*model.Affiliation, error) {
	return a.values, a.listErr
}
func (a *affiliationHTTPApplication) CreateAffiliation(_ context.Context, _ application.Invocation, command application.CreateAffiliationCommand) (*model.Affiliation, error) {
	a.createCommand = command
	return a.result, nil
}
func (a *affiliationHTTPApplication) EndAffiliation(_ context.Context, _ application.Invocation, command application.EndAffiliationCommand) (*model.Affiliation, error) {
	a.endCommand = command
	return a.result, nil
}

func TestAffiliationHTTPUsesDTOAndRouteOwnedUser(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now()}
	userID := model.NewId()
	created := &model.Affiliation{
		ID: model.NewAffiliationID(), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100),
		Revision: 1, UserID: model.UserID(userID), Kind: model.AffiliationTeacher, StartsAt: model.TimeFromMillis(100),
	}
	affiliations := &affiliationHTTPApplication{result: created}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, affiliationResource(affiliations))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/users/"+userID+"/affiliations", strings.NewReader(`{"id":"ignored","user_id":"ignored","kind":"teacher","start_at":100}`))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if affiliations.createCommand.UserID != userID || affiliations.createCommand.Kind != model.AffiliationTeacher {
		t.Fatalf("command = %#v", affiliations.createCommand)
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if _, exposed := body["revision"]; exposed {
		t.Fatalf("persistence revision leaked into compatibility DTO: %#v", body)
	}
}

func TestAffiliationResourceSerializesAllowedAuthorizationFailure(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now()}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, affiliationResource(&affiliationHTTPApplication{listErr: application.NewError("authorization.denied")}))

	request := httptest.NewRequest(http.MethodGet, "/api/v1/users/"+model.NewId()+"/affiliations", nil)
	request.Header.Set("Authorization", "Bearer credential")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	var problem Problem
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != "authorization.denied" {
		t.Fatalf("problem = %#v body=%s", problem, response.Body.String())
	}
}

func TestAffiliationResourceFailsClosedForUndeclaredError(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now()}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, affiliationResource(&affiliationHTTPApplication{listErr: application.NewError("user.conflict")}))

	request := httptest.NewRequest(http.MethodGet, "/api/v1/users/"+model.NewId()+"/affiliations", nil)
	request.Header.Set("Authorization", "Bearer credential")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	var problem Problem
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != "internal" {
		t.Fatalf("problem = %#v", problem)
	}
}

func TestAffiliationResourceRejectsInvalidBodyAndIdentifier(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now()}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, affiliationResource(&affiliationHTTPApplication{}))

	invalidBody := httptest.NewRequest(http.MethodPost, "/api/v1/users/"+model.NewId()+"/affiliations", strings.NewReader(`{"kind":"teacher","unknown":true}`))
	invalidBody.Header.Set("Authorization", "Bearer credential")
	invalidBody.Header.Set("Content-Type", "application/json")
	bodyResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(bodyResponse, invalidBody)
	if bodyResponse.Code != http.StatusBadRequest {
		t.Fatalf("body status = %d: %s", bodyResponse.Code, bodyResponse.Body.String())
	}

	invalidID := httptest.NewRequest(http.MethodGet, "/api/v1/users/not-a-canonical-id/affiliations", nil)
	invalidID.Header.Set("Authorization", "Bearer credential")
	idResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(idResponse, invalidID)
	if idResponse.Code != http.StatusNotFound {
		t.Fatalf("identifier status = %d: %s", idResponse.Code, idResponse.Body.String())
	}
}
