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
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
)

type academicUnitMemberHTTPApplication struct {
	pageQuery     app.ListAcademicUnitMembersPageQuery
	pageResult    *app.AcademicUnitMemberPage
	result        *model.AcademicUnitMember
	values        []*model.AcademicUnitMember
	createCommand app.CreateAcademicUnitMemberCommand
	endCommand    app.EndAcademicUnitMemberCommand
}

func TestAcademicUnitMemberHistoryQueryIncludesEndedMemberships(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequestWithContext(t.Context(), "GET", "/api/v1/academic-units/unit/members?history=true", nil)
	query, _, err := membershipPageQuery(request, model.Resource{Type: model.ResourceAcademicUnit, ID: model.NewID()})
	require.NoError(t, err)
	require.NotNil(t, query.History)
	require.True(t, *query.History, "history")
}

func TestAcademicUnitMemberHistoryQueryRejectsInvalidBoolean(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest("GET", "/api/v1/academic-units/unit/members?history=invalid", nil)
	_, _, err := membershipPageQuery(request, model.Resource{Type: model.ResourceAcademicUnit, ID: model.NewID()})
	require.Error(t, err, "invalid history query was accepted")
}

func TestAcademicUnitMemberFalseHistoryQueryUsesActiveAt(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequestWithContext(t.Context(), "GET", "/api/v1/academic-units/unit/members?history=false&active_at=1970-01-01T00:00:00.123Z", nil)
	query, _, err := membershipPageQuery(request, model.Resource{Type: model.ResourceAcademicUnit, ID: model.NewID()})
	require.NoError(t, err)
	require.NotNil(t, query.ActiveAt)
	require.Equal(t, int64(123), *query.ActiveAt, "active at")
}

func (a *academicUnitMemberHTTPApplication) CreateAcademicUnitMember(_ context.Context, _ invocation.Invocation, command app.CreateAcademicUnitMemberCommand) (*model.AcademicUnitMember, error) {
	a.createCommand = command
	return a.result, nil
}
func (a *academicUnitMemberHTTPApplication) EndAcademicUnitMember(_ context.Context, _ invocation.Invocation, command app.EndAcademicUnitMemberCommand) (*model.AcademicUnitMember, error) {
	a.endCommand = command
	return a.result, nil
}

func TestAcademicUnitMemberResourceRunsThroughRoutingKernel(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewID()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now()}
	unitID, userID := model.NewID(), model.NewID()
	member := &model.AcademicUnitMember{ID: model.NewAcademicUnitMemberID(), AcademicUnitID: model.AcademicUnitID(unitID), UserID: model.UserID(userID), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100), StartsAt: model.TimeFromMillis(100)}
	members := &academicUnitMemberHTTPApplication{result: member}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, academicUnitMemberResource(members))

	create := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/academic-units/"+unitID+"/members", strings.NewReader(`{"user_id":"`+userID+`","start_at":"1970-01-01T00:00:00.1Z"}`))
	create.Header.Set("Authorization", "Bearer credential")
	create.Header.Set("Content-Type", "application/json")
	created := httptest.NewRecorder()
	httpAPI.ServeHTTP(created, create)
	require.Equal(t, http.StatusCreated, created.Code, "create status = %d: %s", created.Code, created.Body.String())
	require.Equal(t, model.AcademicUnitID(unitID), members.createCommand.AcademicUnitID)
	require.Equal(t, userID, members.createCommand.UserID)
	var body map[string]any
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &body))
	_, exposed := body["revision"]
	require.False(t, exposed, "persistence revision leaked into DTO: %#v", body)

	list := httptest.NewRequest(http.MethodGet, "/api/v1/academic-units/"+unitID+"/members?history=true", nil)
	list.Header.Set("Authorization", "Bearer credential")
	listed := httptest.NewRecorder()
	httpAPI.ServeHTTP(listed, list)
	require.Equal(t, http.StatusOK, listed.Code, "list status/query = %d/%#v: %s", listed.Code, members.pageQuery, listed.Body.String())
	require.Equal(t, model.AcademicUnitID(unitID), members.pageQuery.AcademicUnitID)
	require.NotNil(t, members.pageQuery.History)
	require.True(t, *members.pageQuery.History, "history listing")
	require.JSONEq(t, `{"items":[]}`, listed.Body.String(), "empty collection envelope")
}

func TestAcademicUnitMemberResourceRejectsInvalidQueryAndMissingPrincipal(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewID()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now()}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, academicUnitMemberResource(&academicUnitMemberHTTPApplication{}))

	invalid := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/academic-units/"+model.NewID()+"/members?history=invalid", nil)
	invalid.Header.Set("Authorization", "Bearer credential")
	invalidResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(invalidResponse, invalid)
	require.Equal(t, http.StatusBadRequest, invalidResponse.Code, "invalid query status = %d: %s", invalidResponse.Code, invalidResponse.Body.String())

	unauthenticated := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/academic-units/"+model.NewID()+"/members", nil)
	unauthenticatedResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(unauthenticatedResponse, unauthenticated)
	require.Equal(t, http.StatusUnauthorized, unauthenticatedResponse.Code, "unauthenticated status = %d: %s", unauthenticatedResponse.Code, unauthenticatedResponse.Body.String())
}

func (a *academicUnitMemberHTTPApplication) ListAcademicUnitMembersPage(_ context.Context, _ invocation.Invocation, query app.ListAcademicUnitMembersPageQuery) (*app.AcademicUnitMemberPage, error) {
	a.pageQuery = query
	if a.pageResult != nil {
		return a.pageResult, nil
	}
	return &app.AcademicUnitMemberPage{}, nil
}
