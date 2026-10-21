// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestRoleHTTPCreateUsesApplicationCommandWithoutPreflight(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{
		UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewID()),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password",
		AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType:             model.SessionClientCLI, AuthenticatedAt: time.Now(),
	}
	roleID := model.NewID()
	roles := &roleHTTPApplication{result: &model.Role{ID: model.RoleID(roleID), Name: "teacher", DisplayName: "Teacher",
		Permissions: []string{string(model.ActionClassView)},
	}}
	transport := &academicUnitHTTPApplication{principal: principal}
	options := validHTTPOptions(t)
	options.Logger = logger
	options.Application = transport
	options.AcademicUnits = transport
	options.Institutions = transport
	options.Roles = roles
	httpAPI, err := New(options)
	require.NoError(t, err)
	t.Cleanup(func() { _ = httpAPI.Close() })
	body, _ := json.Marshal(map[string]any{
		"name": "teacher", "display_name": "Teacher",
		"permissions": []string{string(model.ActionClassView)},
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/roles", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusCreated, response.Code, "status = %d: %s", response.Code, response.Body.String())
	require.Equal(t, "teacher", roles.createCommand.Name)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	require.Equal(t, roleID, payload["id"])
	require.Equal(t, "teacher", payload["name"])
}

func TestRoleResponsePreservesExistingWireShape(t *testing.T) {
	t.Parallel()
	role := &model.Role{
		ID: model.NewRoleID(), CreatedAt: model.TimeFromMillis(10), UpdatedAt: model.TimeFromMillis(20), Name: "teacher",
		DisplayName: "Teacher", Description: "desc",
		Permissions: []string{string(model.ActionClassView)}, BuiltIn: false,
	}
	want, err := json.Marshal(roleResponse{
		ID: role.ID.String(), CreatedAt: "1970-01-01T00:00:00.01Z", UpdatedAt: "1970-01-01T00:00:00.02Z",
		Name: "teacher", DisplayName: "Teacher", Description: "desc",
		Permissions: []string{string(model.ActionClassView)}, BuiltIn: false,
	})
	require.NoError(t, err)
	got, err := json.Marshal(roleResponseFromModel(nil, role))
	require.NoError(t, err)
	require.Equal(t, string(want), string(got), "response")
}
