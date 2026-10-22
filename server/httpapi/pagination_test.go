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
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

func paginationTestRequest(t *testing.T, target string) operationRequest {
	t.Helper()
	return operationRequest{request: httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)}
}

func TestPageRequestUsesOneLimitAndCursorIdiom(t *testing.T) {
	t.Parallel()
	page, err := paginationTestRequest(t, "/things").pageRequest()
	require.NoError(t, err)
	require.Equal(t, pageRequest{Limit: defaultPageLimit}, page)
	page, err = paginationTestRequest(t, "/things?limit=200&cursor=abc").pageRequest()
	require.NoError(t, err)
	require.Equal(t, pageRequest{Limit: 200, Cursor: "abc"}, page)
	for _, query := range []string{"limit=0", "limit=201", "limit=x", "limit=1&limit=2", "cursor=", "cursor=a&cursor=b"} {
		_, err = paginationTestRequest(t, "/things?"+query).pageRequest()
		require.Error(t, err, "accepted %s", query)
	}
}

func TestNewListResponseOmitsNextCursorOnTheLastPage(t *testing.T) {
	t.Parallel()
	identity := func(value string) string { return value }
	next := func(last string) (string, error) { return "after-" + last, nil }
	last, err := newListResponse([]string(nil), false, identity, next)
	require.NoError(t, err)
	encoded, err := json.Marshal(last)
	require.NoError(t, err)
	require.JSONEq(t, `{"items":[]}`, string(encoded))
	more, err := newListResponse([]string{"a", "b"}, true, identity, next)
	require.NoError(t, err)
	require.Equal(t, listResponse[string]{Items: []string{"a", "b"}, NextCursor: "after-b"}, more)
	_, err = newListResponse([]string{}, true, identity, next)
	require.Error(t, err, "a page that claims more without items made no progress")
}

func TestBoundedListResultPagesTheStableOrder(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)
	type item struct {
		at time.Time
		id string
	}
	items := []item{
		{base, model.NewID()}, {base.Add(2 * time.Second), model.NewID()}, {base.Add(time.Second), model.NewID()},
	}
	key := func(value item) boundedPageKey { return boundedPageKey{At: value.at, ID: value.id} }
	present := func(value item) string { return value.id }

	first, err := boundedListResult(paginationTestRequest(t, "/things?limit=2"), "thing", items, key, present)
	require.NoError(t, err)
	require.Equal(t, []string{items[1].id, items[2].id}, first.Items, "newest first")
	require.NotEmpty(t, first.NextCursor)

	// Removing an item already returned does not move the continuation.
	remaining := []item{items[0], items[2]}
	second, err := boundedListResult(paginationTestRequest(t, "/things?limit=2&cursor="+url.QueryEscape(first.NextCursor)), "thing", remaining, key, present)
	require.NoError(t, err)
	require.Equal(t, []string{items[0].id}, second.Items)
	require.Empty(t, second.NextCursor, "last page")

	_, err = boundedListResult(paginationTestRequest(t, "/things?cursor=not-a-cursor"), "thing", items, key, present)
	require.Error(t, err)

	names := []string{"beta", "alpha", "gamma"}
	byName, err := boundedListResult(paginationTestRequest(t, "/things?limit=1"), "name", names,
		func(name string) boundedPageKey { return boundedPageKey{Name: name, ID: name} }, func(name string) string { return name })
	require.NoError(t, err)
	require.Equal(t, []string{"alpha"}, byName.Items, "ascending by name")
	byName, err = boundedListResult(paginationTestRequest(t, "/things?limit=5&cursor="+url.QueryEscape(byName.NextCursor)), "name", names,
		func(name string) boundedPageKey { return boundedPageKey{Name: name, ID: name} }, func(name string) string { return name })
	require.NoError(t, err)
	require.Equal(t, []string{"beta", "gamma"}, byName.Items)
}

type programmePageHTTPApplication struct {
	programmeHTTPApplication
	query app.ListProgrammesQuery
	page  app.Page[*model.Programme]
}

func (a *programmePageHTTPApplication) ListProgrammes(_ context.Context, _ app.Invocation, query app.ListProgrammesQuery) (app.Page[*model.Programme], error) {
	a.query = query
	return a.page, nil
}

func TestAcademicNameListContinuesAfterTheLastName(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{
		UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewID()),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password",
		AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now(),
	}
	unitID := model.NewAcademicUnitID()
	programme := &model.Programme{ID: model.NewProgrammeID(), AcademicUnitID: unitID, Name: "computer-science", DisplayName: "Computer Science"}
	application := &programmePageHTTPApplication{page: app.Page[*model.Programme]{Items: []*model.Programme{programme}, HasMore: true}}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, programmeResource(application))
	call := func(query string) *httptest.ResponseRecorder {
		request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/academic-units/"+unitID.String()+"/programmes"+query, nil)
		request.Header.Set("Authorization", "Bearer credential")
		response := httptest.NewRecorder()
		httpAPI.ServeHTTP(response, request)
		return response
	}

	response := call("?q=comp&limit=1")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, app.NamePageQuery{Limit: 1}, application.query.NamePageQuery)
	var page listResponse[programmeResponse]
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page))
	require.Len(t, page.Items, 1)
	require.NotContains(t, page.NextCursor, programme.Name, "the cursor is opaque")

	application.page = app.Page[*model.Programme]{Items: []*model.Programme{}}
	response = call("?q=comp&cursor=" + url.QueryEscape(page.NextCursor))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, app.NamePageQuery{AfterName: programme.Name, AfterID: programme.ID.String(), Limit: defaultPageLimit}, application.query.NamePageQuery)
	require.JSONEq(t, `{"items":[]}`, response.Body.String(), "the last page omits next_cursor")
	require.Equal(t, http.StatusBadRequest, call("?cursor=bm90LWEtY3Vyc29y").Code)
}
