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
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestExamSittingOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	base := "/api/v1/exams/{exam_id}/sittings"
	member := base + "/{exam_sitting_id}"
	suite := openAPIAgreementSuite{
		Operations: []openAPIAgreementOperation{
			{Key: "POST " + base, Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/ScheduleExamSitting", RequestSchema: "ScheduleExamSittingRequest", SuccessStatus: "201", SuccessRef: "#/components/responses/ExamSittingCreated", SuccessSchema: "ExamSittingResponse", PublicErrorCodes: []string{"authorization.request.invalid", "authorization.unavailable", "audit.unavailable", "request.invalid", "resource.not_found", "exam.sitting.invalid", "exam.sitting.conflict", "exam.sitting.unavailable", "exam.archived", "exam.sitting.class_ineligible", "exam.sitting.schedule_outside_period", "exam.sitting.schedule_not_future", "idempotency.key_required", "idempotency.key_invalid", "idempotency.conflict", "idempotency.in_progress"}},
			{Key: "GET " + base, Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/ExamSittingListOK", SuccessSchema: "ExamSittingListResponse", PublicErrorCodes: []string{"authorization.request.invalid", "authorization.unavailable", "request.invalid", "resource.not_found", "exam.sitting.invalid", "exam.sitting.unavailable"}},
			{Key: "GET " + member, Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/ExamSittingOK", SuccessSchema: "ExamSittingResponse", PublicErrorCodes: []string{"authorization.request.invalid", "authorization.unavailable", "request.invalid", "resource.not_found", "exam.sitting.invalid", "exam.sitting.unavailable"}},
			{Key: "PATCH " + member, Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/UpdateExamSittingSchedule", RequestSchema: "UpdateExamSittingScheduleRequest", SuccessStatus: "200", SuccessRef: "#/components/responses/ExamSittingOK", SuccessSchema: "ExamSittingResponse", PublicErrorCodes: []string{"authorization.request.invalid", "authorization.unavailable", "audit.unavailable", "request.invalid", "resource.not_found", "exam.sitting.invalid", "exam.sitting.conflict", "exam.sitting.unavailable", "exam.archived", "exam.sitting.revision_conflict", "exam.sitting.no_changes", "exam.sitting.state_conflict", "exam.sitting.class_ineligible", "exam.sitting.schedule_outside_period", "exam.sitting.schedule_not_future", "idempotency.key_required", "idempotency.key_invalid", "idempotency.conflict", "idempotency.in_progress"}},
			{Key: "POST " + member + "/cancel", Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/CancelExamSitting", RequestSchema: "CancelExamSittingRequest", SuccessStatus: "200", SuccessRef: "#/components/responses/ExamSittingOK", SuccessSchema: "ExamSittingResponse", PublicErrorCodes: []string{"authorization.request.invalid", "authorization.unavailable", "audit.unavailable", "request.invalid", "resource.not_found", "exam.sitting.invalid", "exam.sitting.conflict", "exam.sitting.unavailable", "exam.archived", "exam.sitting.revision_conflict", "exam.sitting.state_conflict", "idempotency.key_required", "idempotency.key_invalid", "idempotency.conflict", "idempotency.in_progress"}},
			{Key: "POST " + member + "/pause", Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/PauseExamSitting", RequestSchema: "ExamSittingManagerTransitionRequest", SuccessStatus: "200", SuccessRef: "#/components/responses/ExamSittingOK", SuccessSchema: "ExamSittingResponse", PublicErrorCodes: []string{"authorization.request.invalid", "authorization.unavailable", "audit.unavailable", "request.invalid", "resource.not_found", "exam.sitting.invalid", "exam.sitting.conflict", "exam.sitting.unavailable", "exam.sitting.revision_conflict", "exam.sitting.state_conflict", "exam.sitting.deadline_reached", "idempotency.key_required", "idempotency.key_invalid", "idempotency.conflict", "idempotency.in_progress", "exam.archived"}},
			{Key: "POST " + member + "/resume", Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/ResumeExamSitting", RequestSchema: "ExamSittingManagerTransitionRequest", SuccessStatus: "200", SuccessRef: "#/components/responses/ExamSittingOK", SuccessSchema: "ExamSittingResponse", PublicErrorCodes: []string{"authorization.request.invalid", "authorization.unavailable", "audit.unavailable", "request.invalid", "resource.not_found", "exam.sitting.invalid", "exam.sitting.conflict", "exam.sitting.unavailable", "exam.archived", "exam.sitting.revision_conflict", "exam.sitting.state_conflict", "exam.sitting.deadline_reached", "idempotency.key_required", "idempotency.key_invalid", "idempotency.conflict", "idempotency.in_progress"}},
			{Key: "POST " + member + "/extend", Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/ExtendExamSitting", RequestSchema: "ExtendExamSittingRequest", SuccessStatus: "200", SuccessRef: "#/components/responses/ExamSittingOK", SuccessSchema: "ExamSittingResponse", PublicErrorCodes: []string{"authorization.request.invalid", "authorization.unavailable", "audit.unavailable", "request.invalid", "resource.not_found", "exam.sitting.invalid", "exam.sitting.conflict", "exam.sitting.unavailable", "exam.archived", "exam.sitting.revision_conflict", "exam.sitting.state_conflict", "exam.sitting.deadline_reached", "exam.sitting.extension_not_later", "exam.sitting.class_ineligible", "exam.sitting.schedule_outside_period", "idempotency.key_required", "idempotency.key_invalid", "idempotency.conflict", "idempotency.in_progress"}},
			{Key: "POST " + member + "/close", Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/CloseExamSitting", RequestSchema: "ExamSittingManagerTransitionRequest", SuccessStatus: "200", SuccessRef: "#/components/responses/ExamSittingOK", SuccessSchema: "ExamSittingResponse", PublicErrorCodes: []string{"authorization.request.invalid", "authorization.unavailable", "audit.unavailable", "request.invalid", "resource.not_found", "exam.sitting.invalid", "exam.sitting.conflict", "exam.sitting.unavailable", "exam.sitting.revision_conflict", "exam.sitting.state_conflict", "exam.sitting.deadline_reached", "idempotency.key_required", "idempotency.key_invalid", "idempotency.conflict", "idempotency.in_progress", "exam.archived"}},
			{Key: "GET " + member + "/no-shows", Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/ExamSittingNoShowListOK", SuccessSchema: "ExamSittingNoShowListResponse", PublicErrorCodes: []string{"authorization.request.invalid", "authorization.unavailable", "request.invalid", "resource.not_found", "exam.sitting.invalid", "exam.sitting.unavailable", "exam.sitting.state_conflict"}},
		},
		Schemas: []openAPIAgreementSchema{
			{Name: "ScheduleExamSittingRequest", DTO: reflect.TypeOf(scheduleExamSittingRequest{}), Required: []string{"exam_revision_id", "class_id", "scheduled_start_at", "scheduled_end_at"}},
			{Name: "UpdateExamSittingScheduleRequest", DTO: reflect.TypeOf(updateExamSittingScheduleRequest{}), Required: []string{"expected_revision"}, NonNullable: []string{"exam_revision_id", "class_id", "scheduled_start_at", "scheduled_end_at"}},
			{Name: "CancelExamSittingRequest", DTO: reflect.TypeOf(cancelExamSittingRequest{}), Required: []string{"expected_revision", "reason"}},
			{Name: "ExamSittingManagerTransitionRequest", DTO: reflect.TypeOf(examSittingManagerTransitionRequest{}), Required: []string{"expected_revision", "reason"}},
			{Name: "ExtendExamSittingRequest", DTO: reflect.TypeOf(extendExamSittingRequest{}), Required: []string{"expected_revision", "scheduled_end_at", "reason"}},
			{Name: "ExamSittingResponse", DTO: reflect.TypeOf(examSittingResponse{}), Required: []string{"id", "exam_id", "exam_revision_id", "class_id", "scheduled_start_at", "scheduled_end_at", "state", "created_at", "updated_at", "revision"}},
			{Name: "ExamSittingListResponse", DTO: reflect.TypeOf(examSittingListResponse{}), Required: []string{"items"}},
			{Name: "ExamSittingNoShowResponse", DTO: reflect.TypeOf(examSittingNoShowResponse{}), Required: []string{"candidate_user_id"}},
			{Name: "ExamSittingNoShowListResponse", DTO: reflect.TypeOf(examSittingNoShowListResponse{}), Required: []string{"items"}},
		},
	}
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	require.NoError(t, runtimeAPI.collectResources(model.APIURLSuffix, examSittingResource(nil)))
	assertOpenAPIAgreement(t, suite, runtimeAPI.Routes())
	assertExamSittingListParameters(t, base)
	assertExamSittingNoShowListParameters(t, member+"/no-shows")
}

func assertExamSittingNoShowListParameters(t *testing.T, path string) {
	t.Helper()
	document := readOpenAPIDocument(t)
	var operation openAPIOperation
	require.NoError(t, json.Unmarshal(document.Paths[path]["get"], &operation))
	want := []string{"limit", "cursor"}
	got := make([]string, 0, len(operation.Parameters))
	for _, parameter := range operation.Parameters {
		got = append(got, parameter.Name)
	}
	assert.True(t, slices.Equal(got, want), "GET %s query parameters = %v, want exactly %v", path, got, want)
}

func examSittingMutationContractCodes(specific ...string) []string {
	common := []string{"request.invalid", "resource.not_found", "exam.sitting.invalid", "exam.sitting.conflict", "exam.sitting.unavailable"}
	common = append(common, specific...)
	return principalMutationContractCodes(append(common,
		"idempotency.key_required", "idempotency.key_invalid", "idempotency.conflict", "idempotency.in_progress", "administration.unavailable")...)
}

func assertExamSittingListParameters(t *testing.T, path string) {
	t.Helper()
	document := readOpenAPIDocument(t)
	var operation openAPIOperation
	require.NoError(t, json.Unmarshal(document.Paths[path]["get"], &operation))
	want := []string{"class_id", "state", "ends_after", "starts_before", "limit", "cursor"}
	got := make([]string, 0, len(operation.Parameters))
	for _, parameter := range operation.Parameters {
		got = append(got, parameter.Name)
	}
	for _, name := range want {
		assert.Contains(t, got, name, "GET %s omits query parameter", path)
	}
	assert.Len(t, got, len(want), "GET %s query parameters", path)
}
