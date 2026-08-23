// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"reflect"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestExamRevisionOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	base := "/api/v1/exams/{exam_id}/revisions"
	member := base + "/{exam_revision_id}"
	readCodes := principalContractCodes("request.invalid", "resource.not_found", "exam.invalid", "exam.unavailable", "administration.unavailable")
	mutationCodes := principalMutationContractCodes(
		"request.invalid", "resource.not_found", "exam.invalid", "exam.archived", "exam.conflict",
		"exam.draft.revision_conflict", "exam.revision.no_changes", "exam.revision.capacity_exceeded", "exam.unavailable",
		"idempotency.key_required", "idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress",
		"administration.unavailable",
	)
	suite := openAPIAgreementSuite{
		Operations: []openAPIAgreementOperation{
			{Key: "POST " + base, Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/PublishExamRevision", RequestSchema: "PublishExamRevisionRequest", SuccessStatus: "201", SuccessRef: "#/components/responses/ExamRevisionCreated", SuccessSchema: "ExamRevisionResponse", PublicErrorCodes: mutationCodes},
			{Key: "GET " + base, Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/ExamRevisionListOK", SuccessSchema: "ExamRevisionListResponse", PublicErrorCodes: readCodes},
			{Key: "GET " + member, Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/ExamRevisionOK", SuccessSchema: "ExamRevisionResponse", PublicErrorCodes: readCodes},
		},
		Schemas: []openAPIAgreementSchema{
			{Name: "PublishExamRevisionRequest", DTO: reflect.TypeOf(publishExamRevisionRequest{}), Required: []string{"expected_draft_revision"}},
			{Name: "ExamRevisionResponse", DTO: reflect.TypeOf(examRevisionResponse{}), Required: []string{"id", "exam_id", "number", "source_draft_revision", "title", "policy_schema_version", "policy_digest", "execution_profile_digest", "capacity", "starter_workspace_digest", "content_digest", "resource_count", "starter_workspace_entry_count", "starter_workspace_total_bytes", "published_by_user_id", "published_at", "publication_kind"}},
			{Name: "ExamRevisionListResponse", DTO: reflect.TypeOf(examRevisionListResponse{}), Required: []string{"items"}},
		},
	}
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.collectResources(model.APIURLSuffix, examRevisionResource(nil)); err != nil {
		t.Fatal(err)
	}
	assertOpenAPIAgreement(t, suite, runtimeAPI.Routes())
}
