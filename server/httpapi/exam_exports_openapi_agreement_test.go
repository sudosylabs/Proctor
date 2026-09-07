// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"reflect"
	"strings"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestExamExportsOpenAPIAgreesWithRuntime(t *testing.T) {
	read := principalContractCodes("request.invalid", "resource.not_found", "exam.invalid", "exam.unavailable", "exam.export.not_found", "exam.export.conflict", "exam.export.unavailable", "administration.unavailable")
	mutation := principalMutationContractCodes("request.invalid", "resource.not_found", "exam.invalid", "exam.unavailable", "exam.export.not_found", "exam.export.conflict", "exam.export.unavailable", "exam.export.policy_unconfigured", "exam.export.limit", "exam.export.source_retired", "exam.export.scope_changed", "idempotency.key_required", "idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress", "administration.unavailable")
	suite := openAPIAgreementSuite{Schemas: []openAPIAgreementSchema{
		{Name: "CreateExamExportRequest", DTO: reflect.TypeOf(createExamExportRequest{}), Required: []string{"categories"}, NonNullable: []string{"categories"}},
		{Name: "ExamExportResponse", DTO: reflect.TypeOf(examExportResponse{}), Required: []string{"id", "exam_id", "exam_sitting_id", "categories", "state", "policy_revision", "created_at", "expires_at", "construction_deadline", "submission_count", "file_count", "source_bytes"}},
	}, OperationSelector: func(_ string, path string) bool { return strings.Contains(path, "/exports") }}
	for _, sub := range []string{"", "/submissions/{submission_id}"} {
		base := "/api/v1/exams/{exam_id}/sittings/{exam_sitting_id}" + sub + "/exports"
		suite.Operations = append(suite.Operations,
			openAPIAgreementOperation{Key: "POST " + base, Auth: AuthSessionRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/CreateExamExport", RequestSchema: "CreateExamExportRequest", SuccessStatus: "202", SuccessRef: "#/components/responses/ExamExportAccepted", SuccessSchema: "ExamExportResponse", PublicErrorCodes: mutation},
			openAPIAgreementOperation{Key: "GET " + base + "/{exam_export_id}", Auth: AuthSessionRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/ExamExportOK", SuccessSchema: "ExamExportResponse", PublicErrorCodes: read},
			openAPIAgreementOperation{Key: "GET " + base + "/{exam_export_id}/content", Auth: AuthSessionRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/ExamExportArchive", ExceptionalSuccess: true, PublicErrorCodes: append(append([]string(nil), read...), "exam.export.expired", "exam.export.not_ready")})
	}
	api := newRoutingTestAPI(model.APIURLSuffix)
	if err := api.collectResources(model.APIURLSuffix, examExportsResource(nil)); err != nil {
		t.Fatal(err)
	}
	assertOpenAPIAgreement(t, suite, api.Routes())
	doc := readOpenAPIDocument(t)
	for _, name := range []string{"ExamExportAccepted", "ExamExportOK", "ExamExportArchive"} {
		if doc.Components.Responses[name].Headers["Cache-Control"].Ref != "#/components/headers/PrivateNoStore" {
			t.Fatalf("%s misses private no-store", name)
		}
	}
}
