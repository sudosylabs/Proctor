// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestExamStarterWorkspaceOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	readCodes := principalContractCodes("request.invalid", "resource.not_found", "exam.starter_workspace.invalid", "exam.starter_workspace.unavailable", "administration.unavailable")
	base := "/api/v1/exams/{exam_id}/draft/starter-workspace"
	entry := base + "/entries/{starter_workspace_entry_id}"
	content := base + "/files/{starter_workspace_entry_id}/content"
	suite := openAPIAgreementSuite{
		Operations: []openAPIAgreementOperation{
			{Key: "GET " + base, Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/ExamStarterWorkspaceListOK", SuccessSchema: "ExamStarterWorkspaceListResponse", PublicErrorCodes: readCodes},
			{Key: "POST " + base + "/directories", Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/CreateExamStarterWorkspaceDirectory", RequestSchema: "CreateExamStarterWorkspaceDirectoryRequest", SuccessStatus: "201", SuccessRef: "#/components/responses/ExamStarterWorkspaceEntryCreated", SuccessSchema: "ExamStarterWorkspaceEntryResponse", PublicErrorCodes: examStarterWorkspaceMutationContractCodes("exam.starter_workspace.path_conflict", "exam.starter_workspace.parent_not_found", "exam.starter_workspace.entry_limit")},
			{Key: "POST " + base + "/files", Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired, SuccessStatus: "201", SuccessRef: "#/components/responses/ExamStarterWorkspaceEntryCreated", SuccessSchema: "ExamStarterWorkspaceEntryResponse", PublicErrorCodes: examStarterWorkspaceMutationContractCodes("exam.starter_workspace.path_conflict", "exam.starter_workspace.parent_not_found", "exam.starter_workspace.entry_limit", "exam.starter_workspace.total_size_limit", "exam.starter_workspace.upload_expired", "exam.starter_workspace.object_conflict")},
			{Key: "PATCH " + entry, Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/MoveExamStarterWorkspaceEntry", RequestSchema: "MoveExamStarterWorkspaceEntryRequest", SuccessStatus: "200", SuccessRef: "#/components/responses/ExamStarterWorkspaceEntryOK", SuccessSchema: "ExamStarterWorkspaceEntryResponse", PublicErrorCodes: examStarterWorkspaceMutationContractCodes("exam.starter_workspace.path_conflict", "exam.starter_workspace.parent_not_found", "exam.starter_workspace.no_changes", "exam.starter_workspace.invalid_move")},
			{Key: "PUT " + content, Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/ExamStarterWorkspaceEntryOK", SuccessSchema: "ExamStarterWorkspaceEntryResponse", PublicErrorCodes: examStarterWorkspaceMutationContractCodes("exam.starter_workspace.total_size_limit", "exam.starter_workspace.upload_expired", "exam.starter_workspace.object_conflict", "exam.starter_workspace.content_conflict", "exam.starter_workspace.entry_kind")},
			{Key: "DELETE " + entry, Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/RemoveExamStarterWorkspaceEntry", RequestSchema: "RemoveExamStarterWorkspaceEntryRequest", SuccessStatus: "204", SuccessRef: "#/components/responses/ExamStarterWorkspaceEntryRemoved", PublicErrorCodes: examStarterWorkspaceMutationContractCodes("exam.starter_workspace.directory_not_empty")},
			{Key: "GET " + content, Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/ExamStarterWorkspaceProtectedContent", ExceptionalSuccess: true, PublicErrorCodes: readCodes},
		},
		Schemas: []openAPIAgreementSchema{
			{Name: "CreateExamStarterWorkspaceDirectoryRequest", DTO: reflect.TypeOf(createExamStarterWorkspaceDirectoryRequest{}), Required: []string{"expected_draft_revision", "path"}},
			{Name: "ExamStarterWorkspaceFileUploadMetadata", DTO: reflect.TypeOf(examStarterWorkspaceFileUploadMetadata{}), Required: []string{"expected_draft_revision", "path", "media_type", "size", "sha256"}},
			{Name: "ExamStarterWorkspaceFileReplacementMetadata", DTO: reflect.TypeOf(examStarterWorkspaceFileReplacementMetadata{}), Required: []string{"expected_draft_revision", "expected_content_version", "media_type", "size", "sha256"}},
			{Name: "MoveExamStarterWorkspaceEntryRequest", DTO: reflect.TypeOf(moveExamStarterWorkspaceEntryRequest{}), Required: []string{"expected_draft_revision", "path"}},
			{Name: "RemoveExamStarterWorkspaceEntryRequest", DTO: reflect.TypeOf(removeExamStarterWorkspaceEntryRequest{}), Required: []string{"expected_draft_revision"}},
			{Name: "ExamStarterWorkspaceEntryResponse", DTO: reflect.TypeOf(examStarterWorkspaceEntryResponse{}), Required: []string{"id", "kind", "path", "created_at", "updated_at", "draft_revision"}},
			{Name: "ExamStarterWorkspaceListItemResponse", DTO: reflect.TypeOf(examStarterWorkspaceListItemResponse{}), Required: []string{"id", "kind", "path", "created_at", "updated_at"}},
			{Name: "ExamStarterWorkspaceListResponse", DTO: reflect.TypeOf(examStarterWorkspaceListResponse{}), Required: []string{"items"}},
		},
	}
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.collectResources(model.APIURLSuffix, examStarterWorkspaceHTTPResource(nil)); err != nil {
		t.Fatal(err)
	}
	assertOpenAPIAgreement(t, suite, runtimeAPI.Routes())

	document := readOpenAPIDocument(t)
	assertExamMultipartOperation(t, document, base+"/files", "post", "#/components/schemas/ExamStarterWorkspaceFileUploadMultipart")
	assertExamMultipartOperation(t, document, content, "put", "#/components/schemas/ExamStarterWorkspaceFileReplacementMultipart")
	assertProtectedExamContentResponse(t, document, content, "ExamStarterWorkspaceProtectedContent")
	assertProtectedExamCacheControl(t, "ExamStarterWorkspaceProtectedContent", "private, no-store")
	if _, ok := document.Components.Responses["ExamStarterWorkspaceProtectedContent"].Headers["Content-Disposition"]; ok {
		t.Errorf("GET %s protected response documents forbidden Content-Disposition", content)
	}
	operation := decodeExamContentOpenAPIOperation(t, document, content, "get")
	if response304 := operation.Responses["304"]; response304.Ref != "#/components/responses/ExamStarterWorkspaceContentNotModified" {
		t.Errorf("GET %s 304 response = %#v", content, response304)
	}
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
		var shape openAPISchemaShape
		if err := json.Unmarshal(document.Components.Schemas[target.schema].Properties[target.property], &shape); err != nil {
			t.Fatalf("decode %s.%s: %v", target.schema, target.property, err)
		}
		if shape.Ref != wantRef {
			t.Errorf("%s.%s reference = %q, want %q", target.schema, target.property, shape.Ref, wantRef)
		}
	}

	var raw struct {
		Components struct {
			Schemas map[string]struct {
				Type    string `json:"type"`
				Pattern string `json:"pattern"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(readOpenAPIDocumentBytes(t), &raw); err != nil {
		t.Fatal(err)
	}
	token := raw.Components.Schemas["WorkspaceContentVersion"]
	if token.Type != "string" || token.Pattern != `^[A-Za-z0-9_-]{26}$` {
		t.Errorf("WorkspaceContentVersion schema = %#v", token)
	}
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
	if err := json.Unmarshal(readOpenAPIDocumentBytes(t), &document); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ExamStarterWorkspaceEntryResponse", "ExamStarterWorkspaceListItemResponse"} {
		schema := document.Components.Schemas[name]
		if len(schema.OneOf) != 2 {
			t.Errorf("%s oneOf variants = %d, want file and directory", name, len(schema.OneOf))
		}
		encoded, err := json.Marshal(schema.OneOf)
		if err != nil {
			t.Fatal(err)
		}
		text := string(encoded)
		for _, invariant := range []string{`"const":"file"`, `"const":"directory"`, `"content_version"`, `"media_type"`, `"size"`, `"sha256"`, `"not"`} {
			if !strings.Contains(text, invariant) {
				t.Errorf("%s variants omit %s", name, invariant)
			}
		}
	}
	mutation := document.Components.Schemas["ExamStarterWorkspaceEntryResponse"]
	list := document.Components.Schemas["ExamStarterWorkspaceListItemResponse"]
	if !slices.Contains(mutation.Required, "draft_revision") || slices.Contains(list.Required, "draft_revision") {
		t.Errorf("draft_revision requirement mutation=%v list=%v", mutation.Required, list.Required)
	}
}

func examStarterWorkspaceMutationContractCodes(specific ...string) []string {
	common := []string{"request.invalid", "resource.not_found", "exam.archived", "exam.draft.revision_conflict", "exam.starter_workspace.invalid", "exam.starter_workspace.conflict", "exam.starter_workspace.unavailable"}
	common = append(common, specific...)
	return principalMutationContractCodes(append(common,
		"idempotency.key_required", "idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress", "administration.unavailable")...)
}
