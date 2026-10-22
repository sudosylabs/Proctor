// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"bytes"
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

type academicAdministrationBatchHTTPApplication struct {
	command app.RunAcademicAdministrationBatchCommand
}

func (a *academicAdministrationBatchHTTPApplication) RunAcademicAdministrationBatch(_ context.Context, _ invocation.Invocation, command app.RunAcademicAdministrationBatchCommand) (app.AcademicAdministrationBatchResult, error) {
	a.command = command
	return app.AcademicAdministrationBatchResult{Operation: command.Operation, Succeeded: 1, NoOp: 1, Failed: 1,
		Items: []app.AcademicAdministrationBatchItemResult{{Index: 0, Status: app.InvitationBatchItemSucceeded, ResourceID: model.NewClassMemberID().String()},
			{Index: 1, Status: app.InvitationBatchItemNoOp, ResourceID: model.NewClassMemberID().String()},
			{Index: 2, Status: app.InvitationBatchItemFailed, ErrorCode: "class.enrollment_conflict"}}}, nil
}

func TestAcademicAdministrationBatchHTTPIsStrictBoundedAndSafe(t *testing.T) {
	logger, _ := newTestLogger(t)
	applicationFake := &academicAdministrationBatchHTTPApplication{}
	now := time.Now()
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewID()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationMultiFactor,
		ClientType: model.SessionClientWeb, AuthenticatedAt: now, MFACompletedAt: model.OptionalTimeFrom(now)}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, academicAdministrationBatchResource(applicationFake))
	classID := model.NewClassID().String()
	userID := model.NewUserID().String()
	previousID := model.NewClassMemberID().String()
	body, _ := json.Marshal(map[string]any{"operation": "class.transfer", "scope_type": "class", "scope_id": classID,
		"items": []map[string]any{{"key": "row-1", "user_id": userID, "relationship_id": previousID}}})
	request := newJSONRequest(http.MethodPost, "/api/v1/academic-administration-batches", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer session")
	request.Header.Set("Idempotency-Key", "batch-key")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, "academic administration batch = %d %s command=%#v", response.Code, response.Body.String(), applicationFake.command)
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"), "academic administration batch = %d %s command=%#v", response.Code, response.Body.String(), applicationFake.command)
	require.Equal(t, app.AcademicAdministrationClassTransfer, applicationFake.command.Operation, "academic administration batch = %d %s command=%#v", response.Code, response.Body.String(), applicationFake.command)
	require.Equal(t, classID, applicationFake.command.ScopeID, "academic administration batch = %d %s command=%#v", response.Code, response.Body.String(), applicationFake.command)
	require.Equal(t, "batch-key", applicationFake.command.IdempotencyKey, "academic administration batch = %d %s command=%#v", response.Code, response.Body.String(), applicationFake.command)
	require.Len(t, applicationFake.command.Items, 1, "academic administration batch = %d %s command=%#v", response.Code, response.Body.String(), applicationFake.command)
	require.Equal(t, model.UserID(userID), applicationFake.command.Items[0].UserID, "academic administration batch = %d %s command=%#v", response.Code, response.Body.String(), applicationFake.command)
	require.Equal(t, previousID, applicationFake.command.Items[0].RelationshipID, "academic administration batch = %d %s command=%#v", response.Code, response.Body.String(), applicationFake.command)
	require.Contains(t, response.Body.String(), `"succeeded":1`, "academic administration batch = %d %s command=%#v", response.Code, response.Body.String(), applicationFake.command)
	require.Contains(t, response.Body.String(), `"no_op":1`, "academic administration batch = %d %s command=%#v", response.Code, response.Body.String(), applicationFake.command)
	require.Contains(t, response.Body.String(), `"class.enrollment_conflict"`, "academic administration batch = %d %s command=%#v", response.Code, response.Body.String(), applicationFake.command)
	require.NotContains(t, response.Body.String(), userID, "academic administration batch = %d %s command=%#v", response.Code, response.Body.String(), applicationFake.command)

	unknown := newJSONRequest(http.MethodPost, "/api/v1/academic-administration-batches", strings.NewReader(`{"operation":"class.end","scope_type":"class","scope_id":"`+classID+`","items":[],"command":"forbidden"}`))
	unknown.Header.Set("Authorization", "Bearer session")
	unknown.Header.Set("Idempotency-Key", "unknown")
	unknownResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(unknownResponse, unknown)
	require.Equal(t, http.StatusBadRequest, unknownResponse.Code, "unknown command envelope = %d %s", unknownResponse.Code, unknownResponse.Body.String())
}
