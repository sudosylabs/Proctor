// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

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

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type academicAdministrationBatchHTTPApplication struct {
	command application.RunAcademicAdministrationBatchCommand
}

func (a *academicAdministrationBatchHTTPApplication) RunAcademicAdministrationBatch(_ context.Context, _ application.Invocation, command application.RunAcademicAdministrationBatchCommand) (application.AcademicAdministrationBatchResult, error) {
	a.command = command
	return application.AcademicAdministrationBatchResult{Operation: command.Operation, Succeeded: 1, NoOp: 1, Failed: 1,
		Items: []application.AcademicAdministrationBatchItemResult{{Index: 0, Status: application.InvitationBatchItemSucceeded, ResourceID: model.NewClassMemberID().String()},
			{Index: 1, Status: application.InvitationBatchItemNoOp, ResourceID: model.NewClassMemberID().String()},
			{Index: 2, Status: application.InvitationBatchItemFailed, ErrorCode: "class.enrollment_conflict"}}}, nil
}

func TestAcademicAdministrationBatchHTTPIsStrictBoundedAndSafe(t *testing.T) {
	logger, _ := newTestLogger(t)
	applicationFake := &academicAdministrationBatchHTTPApplication{}
	now := time.Now()
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationMultiFactor,
		ClientType: model.SessionClientWeb, AuthenticatedAt: now, MFACompletedAt: model.OptionalTimeFrom(now)}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, academicAdministrationBatchResource(applicationFake))
	classID := model.NewClassID().String()
	userID := model.NewUserID().String()
	previousID := model.NewClassMemberID().String()
	body, _ := json.Marshal(map[string]any{"operation": "class.transfer", "scope_type": "class", "scope_id": classID,
		"items": []map[string]any{{"key": "row-1", "user_id": userID, "relationship_id": previousID}}})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/academic-administration-batches", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer session")
	request.Header.Set("Idempotency-Key", "batch-key")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" ||
		applicationFake.command.Operation != application.AcademicAdministrationClassTransfer || applicationFake.command.ScopeID != classID ||
		applicationFake.command.IdempotencyKey != "batch-key" || len(applicationFake.command.Items) != 1 ||
		applicationFake.command.Items[0].UserID != userID || applicationFake.command.Items[0].RelationshipID != previousID ||
		!strings.Contains(response.Body.String(), `"succeeded":1`) || !strings.Contains(response.Body.String(), `"no_op":1`) ||
		!strings.Contains(response.Body.String(), `"class.enrollment_conflict"`) || strings.Contains(response.Body.String(), userID) {
		t.Fatalf("academic administration batch = %d %s command=%#v", response.Code, response.Body.String(), applicationFake.command)
	}

	unknown := httptest.NewRequest(http.MethodPost, "/api/v1/academic-administration-batches", strings.NewReader(`{"operation":"class.end","scope_type":"class","scope_id":"`+classID+`","items":[],"command":"forbidden"}`))
	unknown.Header.Set("Authorization", "Bearer session")
	unknown.Header.Set("Idempotency-Key", "unknown")
	unknownResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(unknownResponse, unknown)
	if unknownResponse.Code != http.StatusBadRequest {
		t.Fatalf("unknown command envelope = %d %s", unknownResponse.Code, unknownResponse.Body.String())
	}
}
