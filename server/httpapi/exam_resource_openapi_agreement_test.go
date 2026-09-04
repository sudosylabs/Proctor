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

	"github.com/sudosylabs/proctor/server/model"
)

func TestExamResourceOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	readCodes := principalContractCodes("request.invalid", "resource.not_found", "exam.resource.invalid", "exam.resource.unavailable", "administration.unavailable")
	base := "/api/v1/exams/{exam_id}/draft/resources"
	member := base + "/{exam_resource_id}"
	content := member + "/content"
	suite := openAPIAgreementSuite{
		Operations: []openAPIAgreementOperation{
			{Key: "GET " + base, Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/ExamResourceListOK", SuccessSchema: "ExamResourceListResponse", PublicErrorCodes: readCodes},
			{Key: "POST " + base, Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired, SuccessStatus: "201", SuccessRef: "#/components/responses/ExamResourceCreated", SuccessSchema: "ExamResourceResponse", PublicErrorCodes: examResourceMutationContractCodes("exam.resource.invalid_content", "exam.resource.limit", "exam.resource.upload_invalid", "exam.resource.revision_conflict")},
			{Key: "PATCH " + member, Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/EditExamResourceMetadata", RequestSchema: "EditExamResourceMetadataRequest", SuccessStatus: "200", SuccessRef: "#/components/responses/ExamResourceOK", SuccessSchema: "ExamResourceResponse", PublicErrorCodes: examResourceMutationContractCodes("exam.resource.no_changes", "exam.resource.revision_conflict")},
			{Key: "PUT " + base + "/order", Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/ReorderExamResources", RequestSchema: "ReorderExamResourcesRequest", SuccessStatus: "200", SuccessRef: "#/components/responses/ExamResourceListOK", SuccessSchema: "ExamResourceListResponse", PublicErrorCodes: examResourceMutationContractCodes("exam.resource.no_changes", "exam.resource.order_invalid")},
			{Key: "PUT " + content, Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/ExamResourceOK", SuccessSchema: "ExamResourceResponse", PublicErrorCodes: examResourceMutationContractCodes("exam.resource.invalid_content", "exam.resource.upload_invalid", "exam.resource.revision_conflict")},
			{Key: "DELETE " + member, Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/RemoveExamResource", RequestSchema: "RemoveExamResourceRequest", SuccessStatus: "204", SuccessRef: "#/components/responses/ExamResourceRemoved", PublicErrorCodes: examResourceMutationContractCodes("exam.resource.revision_conflict")},
			{Key: "GET " + content, Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/ExamResourceProtectedContent", ExceptionalSuccess: true, PublicErrorCodes: readCodes},
		},
		Schemas: []openAPIAgreementSchema{
			{Name: "ExamResourceUploadMetadata", DTO: reflect.TypeOf(examResourceUploadMetadata{}), Required: []string{"expected_draft_revision", "display_name", "media_type", "size", "sha256"}},
			{Name: "ExamResourceContentReplacementMetadata", DTO: reflect.TypeOf(examResourceContentReplacementMetadata{}), Required: []string{"expected_draft_revision", "media_type", "size", "sha256"}},
			{Name: "EditExamResourceMetadataRequest", DTO: reflect.TypeOf(editExamResourceMetadataRequest{}), Required: []string{"expected_draft_revision"}},
			{Name: "ReorderExamResourcesRequest", DTO: reflect.TypeOf(reorderExamResourcesRequest{}), Required: []string{"expected_draft_revision", "resource_ids"}},
			{Name: "RemoveExamResourceRequest", DTO: reflect.TypeOf(removeExamResourceRequest{}), Required: []string{"expected_draft_revision"}},
			{Name: "ExamResourceResponse", DTO: reflect.TypeOf(examResourceResponse{}), Required: []string{"id", "display_name", "description_markdown", "position", "content_revision_id", "media_type", "size", "sha256", "updated_at", "draft_revision"}},
			{Name: "ExamResourceListResponse", DTO: reflect.TypeOf(examResourceListResponse{}), Required: []string{"items"}},
		},
	}
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.collectResources(model.APIURLSuffix, examResourceHTTPResource(nil)); err != nil {
		t.Fatal(err)
	}
	assertOpenAPIAgreement(t, suite, runtimeAPI.Routes())

	document := readOpenAPIDocument(t)
	assertExamMultipartOperation(t, document, base, "post", "#/components/schemas/ExamResourceUploadMultipart")
	assertExamMultipartOperation(t, document, content, "put", "#/components/schemas/ExamResourceContentReplacementMultipart")
	assertProtectedExamContentResponse(t, document, content, "ExamResourceProtectedContent")
	assertProtectedExamCacheControl(t, "ExamResourceProtectedContent", "private, max-age=300")
	if _, ok := document.Components.Responses["ExamResourceProtectedContent"].Headers["Content-Disposition"]; ok {
		t.Errorf("GET %s protected response documents forbidden Content-Disposition", content)
	}
	operation := decodeExamContentOpenAPIOperation(t, document, content, "get")
	if response304 := operation.Responses["304"]; response304.Ref != "#/components/responses/ExamProtectedContentNotModified" {
		t.Errorf("GET %s 304 response = %#v", content, response304)
	}
}

func examResourceMutationContractCodes(specific ...string) []string {
	common := []string{"request.invalid", "resource.not_found", "exam.archived", "exam.draft.revision_conflict", "exam.resource.invalid", "exam.resource.conflict", "exam.resource.unavailable"}
	common = append(common, specific...)
	return principalMutationContractCodes(append(common,
		"idempotency.key_required", "idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress", "administration.unavailable")...)
}

func assertExamMultipartOperation(t *testing.T, document openAPIDocument, path, method, schemaRef string) {
	t.Helper()
	operation := decodeExamContentOpenAPIOperation(t, document, path, method)
	shape, ok := operation.RequestBody.Content["multipart/form-data"]
	if !ok || shape.Schema.Ref != schemaRef || len(operation.RequestBody.Content) != 1 {
		t.Errorf("%s %s multipart body = %#v, want only %s", method, path, operation.RequestBody.Content, schemaRef)
	}
}

func assertProtectedExamContentResponse(t *testing.T, document openAPIDocument, path, responseName string) {
	t.Helper()
	operation := decodeExamContentOpenAPIOperation(t, document, path, "get")
	if !hasOpenAPIHeaderParameter(operation, "If-None-Match") {
		t.Errorf("GET %s does not document If-None-Match", path)
	}
	response := document.Components.Responses[responseName]
	shape, ok := response.Content["*/*"]
	if !ok || shape.Schema.Type != "string" || shape.Schema.Format != "binary" {
		t.Errorf("GET %s protected binary response = %#v", path, response)
	}
	for _, header := range []string{"ETag", "Cache-Control", "X-Content-Type-Options"} {
		if _, ok := response.Headers[header]; !ok {
			t.Errorf("GET %s protected response does not document %s", path, header)
		}
	}
}

func decodeExamContentOpenAPIOperation(t *testing.T, document openAPIDocument, path, method string) openAPIOperation {
	t.Helper()
	var operation openAPIOperation
	if err := json.Unmarshal(document.Paths[path][method], &operation); err != nil {
		t.Fatalf("decode %s %s: %v", method, path, err)
	}
	return operation
}

func assertProtectedExamCacheControl(t *testing.T, responseName, want string) {
	t.Helper()
	var document struct {
		Components struct {
			Responses map[string]struct {
				Headers map[string]struct {
					Schema struct {
						Const string `json:"const"`
					} `json:"schema"`
				} `json:"headers"`
			} `json:"responses"`
		} `json:"components"`
	}
	if err := json.Unmarshal(readOpenAPIDocumentBytes(t), &document); err != nil {
		t.Fatal(err)
	}
	if got := document.Components.Responses[responseName].Headers["Cache-Control"].Schema.Const; got != want {
		t.Errorf("%s Cache-Control = %q, want %q", responseName, got, want)
	}
}
