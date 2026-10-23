// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestExamStarterWorkspaceOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	base := "/api/v1/exams/{exam_id}/draft/starter-workspace"
	entry := base + "/entries/{starter_workspace_entry_id}"
	content := base + "/files/{starter_workspace_entry_id}/content"
	suite := openAPIAgreementSuite{
		Operations: []openAPIAgreementOperation{
			{Key: "GET " + base, Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/ExamStarterWorkspaceListOK", SuccessSchema: "ExamStarterWorkspaceListResponse", PublicErrorCodes: []string{"authorization.request.invalid", "authorization.unavailable", "request.invalid", "resource.not_found", "exam.starter_workspace.invalid", "exam.starter_workspace.unavailable"}},
			{Key: "POST " + base + "/directories", Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/CreateExamStarterWorkspaceDirectory", RequestSchema: "CreateExamStarterWorkspaceDirectoryRequest", SuccessStatus: "201", SuccessRef: "#/components/responses/ExamStarterWorkspaceEntryCreated", SuccessSchema: "ExamStarterWorkspaceEntryResponse", PublicErrorCodes: []string{"authorization.request.invalid", "authorization.unavailable", "audit.unavailable", "request.invalid", "resource.not_found", "exam.archived", "exam.draft.revision_conflict", "exam.starter_workspace.invalid", "exam.starter_workspace.conflict", "exam.starter_workspace.unavailable", "idempotency.key_required", "idempotency.key_invalid", "idempotency.conflict", "idempotency.in_progress", "exam.starter_workspace.path_conflict", "exam.starter_workspace.parent_not_found", "exam.starter_workspace.entry_limit"}},
			{Key: "POST " + base + "/files", Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired, SuccessStatus: "201", SuccessRef: "#/components/responses/ExamStarterWorkspaceEntryCreated", SuccessSchema: "ExamStarterWorkspaceEntryResponse", PublicErrorCodes: []string{"authorization.request.invalid", "authorization.unavailable", "audit.unavailable", "request.invalid", "resource.not_found", "exam.archived", "exam.draft.revision_conflict", "exam.starter_workspace.invalid", "exam.starter_workspace.conflict", "exam.starter_workspace.unavailable", "idempotency.key_required", "idempotency.key_invalid", "idempotency.conflict", "idempotency.in_progress", "exam.starter_workspace.path_conflict", "exam.starter_workspace.parent_not_found", "exam.starter_workspace.entry_limit", "exam.starter_workspace.total_size_limit", "exam.starter_workspace.upload_expired", "exam.starter_workspace.object_conflict"}},
			{Key: "PATCH " + entry, Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/MoveExamStarterWorkspaceEntry", RequestSchema: "MoveExamStarterWorkspaceEntryRequest", SuccessStatus: "200", SuccessRef: "#/components/responses/ExamStarterWorkspaceEntryOK", SuccessSchema: "ExamStarterWorkspaceEntryResponse", PublicErrorCodes: []string{"authorization.request.invalid", "authorization.unavailable", "audit.unavailable", "request.invalid", "resource.not_found", "exam.archived", "exam.draft.revision_conflict", "exam.starter_workspace.invalid", "exam.starter_workspace.conflict", "exam.starter_workspace.unavailable", "idempotency.key_required", "idempotency.key_invalid", "idempotency.conflict", "idempotency.in_progress", "exam.starter_workspace.path_conflict", "exam.starter_workspace.parent_not_found", "exam.starter_workspace.no_changes", "exam.starter_workspace.move_conflict"}},
			{Key: "PUT " + content, Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/ExamStarterWorkspaceEntryOK", SuccessSchema: "ExamStarterWorkspaceEntryResponse", PublicErrorCodes: []string{"authorization.request.invalid", "authorization.unavailable", "audit.unavailable", "request.invalid", "resource.not_found", "exam.archived", "exam.draft.revision_conflict", "exam.starter_workspace.invalid", "exam.starter_workspace.conflict", "exam.starter_workspace.unavailable", "idempotency.key_required", "idempotency.key_invalid", "idempotency.conflict", "idempotency.in_progress", "exam.starter_workspace.total_size_limit", "exam.starter_workspace.upload_expired", "exam.starter_workspace.object_conflict", "exam.starter_workspace.content_conflict", "exam.starter_workspace.entry_kind"}},
			{Key: "DELETE " + entry, Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/RemoveExamStarterWorkspaceEntry", RequestSchema: "RemoveExamStarterWorkspaceEntryRequest", SuccessStatus: "204", SuccessRef: "#/components/responses/ExamStarterWorkspaceEntryRemoved", PublicErrorCodes: []string{"authorization.request.invalid", "authorization.unavailable", "audit.unavailable", "request.invalid", "resource.not_found", "exam.archived", "exam.draft.revision_conflict", "exam.starter_workspace.invalid", "exam.starter_workspace.conflict", "exam.starter_workspace.unavailable", "idempotency.key_required", "idempotency.key_invalid", "idempotency.conflict", "idempotency.in_progress", "exam.starter_workspace.directory_not_empty"}},
			{Key: "GET " + content, Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/ExamStarterWorkspaceProtectedContent", ExceptionalSuccess: true, PublicErrorCodes: []string{"authorization.request.invalid", "authorization.unavailable", "request.invalid", "resource.not_found", "exam.starter_workspace.invalid", "exam.starter_workspace.unavailable"}},
		},
		Schemas: []openAPIAgreementSchema{
			{Name: "CreateExamStarterWorkspaceDirectoryRequest", DTO: reflect.TypeOf(createExamStarterWorkspaceDirectoryRequest{}), Required: []string{"expected_revision", "path"}},
			{Name: "ExamStarterWorkspaceFileUploadMetadata", DTO: reflect.TypeOf(examStarterWorkspaceFileUploadMetadata{}), Required: []string{"expected_revision", "path", "media_type", "size", "sha256"}},
			{Name: "ExamStarterWorkspaceFileReplacementMetadata", DTO: reflect.TypeOf(examStarterWorkspaceFileReplacementMetadata{}), Required: []string{"expected_revision", "expected_content_version", "media_type", "size", "sha256"}},
			{Name: "MoveExamStarterWorkspaceEntryRequest", DTO: reflect.TypeOf(moveExamStarterWorkspaceEntryRequest{}), Required: []string{"expected_revision", "path"}},
			{Name: "RemoveExamStarterWorkspaceEntryRequest", DTO: reflect.TypeOf(removeExamStarterWorkspaceEntryRequest{}), Required: []string{"expected_revision"}},
			{Name: "ExamStarterWorkspaceEntryResponse", DTO: reflect.TypeOf(examStarterWorkspaceEntryResponse{}), Required: []string{"id", "kind", "path", "created_at", "updated_at", "draft_revision"}},
			{Name: "ExamStarterWorkspaceListItemResponse", DTO: reflect.TypeOf(examStarterWorkspaceListItemResponse{}), Required: []string{"id", "kind", "path", "created_at", "updated_at"}},
			{Name: "ExamStarterWorkspaceListResponse", DTO: reflect.TypeOf(listResponse[examStarterWorkspaceListItemResponse]{}), Required: []string{"items"}},
		},
	}
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	require.NoError(t, runtimeAPI.collectResources(model.APIURLSuffix, examStarterWorkspaceHTTPResource(nil)))
	assertOpenAPIAgreement(t, suite, runtimeAPI.Routes())

	document := readOpenAPIDocument(t)
	assertExamMultipartOperation(t, document, base+"/files", "post", "#/components/schemas/ExamStarterWorkspaceFileUploadMultipart")
	assertExamMultipartOperation(t, document, content, "put", "#/components/schemas/ExamStarterWorkspaceFileReplacementMultipart")
	assertProtectedExamContentResponse(t, document, content, "ExamStarterWorkspaceProtectedContent")
	assertProtectedExamCacheControl(t, "ExamStarterWorkspaceProtectedContent", "private, no-store")
	_, ok := document.Components.Responses["ExamStarterWorkspaceProtectedContent"].Headers["Content-Disposition"]
	assert.False(t, ok, "GET %s protected response documents forbidden Content-Disposition", content)
	operation := decodeExamContentOpenAPIOperation(t, document, content, "get")
	response304 := operation.Responses["304"]
	assert.Equal(t, "#/components/responses/ExamStarterWorkspaceContentNotModified", response304.Ref, "GET %s 304 response = %#v", content, response304)
	assertWorkspaceContentVersionOpenAPI(t, document)
	assertWorkspaceEntryVariantsOpenAPI(t)
}

func assertWorkspaceContentVersionOpenAPI(t *testing.T, document openAPIDocument) {
	t.Helper()
	const wantRef = "#/components/schemas/WorkspaceContentVersion"
	for _, target := range []struct {
		schema   string
		property string
	}{
		{schema: "ExamStarterWorkspaceFileReplacementMetadata", property: "expected_content_version"},
		{schema: "ExamStarterWorkspaceEntryResponse", property: "content_version"},
		{schema: "ExamStarterWorkspaceListItemResponse", property: "content_version"},
	} {
		t.Run(target.schema, func(t *testing.T) {
			var shape openAPISchemaShape
			require.NoError(t, json.Unmarshal(document.Components.Schemas[target.schema].Properties[target.property], &shape), "decode %s.%s", target.schema, target.property)
			assert.Equal(t, wantRef, shape.Ref, "%s.%s reference", target.schema, target.property)
		})
	}

	var raw struct {
		Components struct {
			Schemas map[string]struct {
				Type    any    `json:"type"`
				Pattern string `json:"pattern"`
			} `json:"schemas"`
		} `json:"components"`
	}
	require.NoError(t, json.Unmarshal(readOpenAPIDocumentBytes(t), &raw))
	token := raw.Components.Schemas["WorkspaceContentVersion"]
	assert.Equal(t, "string", token.Type)
	assert.Equal(t, `^[A-Za-z0-9_-]{26}$`, token.Pattern)
}

func assertWorkspaceEntryVariantsOpenAPI(t *testing.T) {
	t.Helper()
	var document struct {
		Components struct {
			Schemas map[string]struct {
				Required []string          `json:"required"`
				OneOf    []json.RawMessage `json:"oneOf"`
			} `json:"schemas"`
		} `json:"components"`
	}
	require.NoError(t, json.Unmarshal(readOpenAPIDocumentBytes(t), &document))
	for _, name := range []string{"ExamStarterWorkspaceEntryResponse", "ExamStarterWorkspaceListItemResponse"} {
		schema := document.Components.Schemas[name]
		assert.Len(t, schema.OneOf, 2, "%s oneOf variants", name)
		encoded, err := json.Marshal(schema.OneOf)
		require.NoError(t, err)
		text := string(encoded)
		for _, invariant := range []string{`"const":"file"`, `"const":"directory"`, `"content_version"`, `"media_type"`, `"size"`, `"sha256"`, `"not"`} {
			assert.Contains(t, text, invariant, "%s variants omit", name)
		}
	}
	mutation := document.Components.Schemas["ExamStarterWorkspaceEntryResponse"]
	list := document.Components.Schemas["ExamStarterWorkspaceListItemResponse"]
	assert.Contains(t, mutation.Required, "draft_revision", "draft_revision requirement mutation")
	assert.NotContains(t, list.Required, "draft_revision", "draft_revision requirement mutation")
}

func examStarterWorkspaceMutationContractCodes(specific ...string) []string {
	common := []string{"request.invalid", "resource.not_found", "exam.archived", "exam.draft.revision_conflict", "exam.starter_workspace.invalid", "exam.starter_workspace.conflict", "exam.starter_workspace.unavailable"}
	common = append(common, specific...)
	return principalMutationContractCodes(append(common,
		"idempotency.key_required", "idempotency.key_invalid", "idempotency.conflict", "idempotency.in_progress", "administration.unavailable")...)
}
