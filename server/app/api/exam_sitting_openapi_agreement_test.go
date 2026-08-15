// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestExamSittingOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	base := "/api/v1/exams/{exam_id}/sittings"
	member := base + "/{exam_sitting_id}"
	readCodes := principalContractCodes("request.invalid", "resource.not_found", "exam.sitting.invalid", "exam.sitting.unavailable", "administration.unavailable")
	suite := openAPIAgreementSuite{
		Operations: []openAPIAgreementOperation{
			{Key: "POST " + base, Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/ScheduleExamSitting", RequestSchema: "ScheduleExamSittingRequest", SuccessStatus: "201", SuccessRef: "#/components/responses/ExamSittingCreated", SuccessSchema: "ExamSittingResponse", PublicErrorCodes: examSittingMutationContractCodes("exam.archived", "exam.sitting.class_ineligible", "exam.sitting.schedule_outside_period", "exam.sitting.schedule_not_future")},
			{Key: "GET " + base, Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/ExamSittingListOK", SuccessSchema: "ExamSittingListResponse", PublicErrorCodes: readCodes},
			{Key: "GET " + member, Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/ExamSittingOK", SuccessSchema: "ExamSittingResponse", PublicErrorCodes: readCodes},
			{Key: "PATCH " + member, Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/UpdateExamSittingSchedule", RequestSchema: "UpdateExamSittingScheduleRequest", SuccessStatus: "200", SuccessRef: "#/components/responses/ExamSittingOK", SuccessSchema: "ExamSittingResponse", PublicErrorCodes: examSittingMutationContractCodes("exam.archived", "exam.sitting.revision_conflict", "exam.sitting.no_changes", "exam.sitting.state_conflict", "exam.sitting.class_ineligible", "exam.sitting.schedule_outside_period", "exam.sitting.schedule_not_future")},
			{Key: "POST " + member + "/cancel", Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/CancelExamSitting", RequestSchema: "CancelExamSittingRequest", SuccessStatus: "200", SuccessRef: "#/components/responses/ExamSittingOK", SuccessSchema: "ExamSittingResponse", PublicErrorCodes: examSittingMutationContractCodes("exam.archived", "exam.sitting.revision_conflict", "exam.sitting.state_conflict")},
			{Key: "POST " + member + "/pause", Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/PauseExamSitting", RequestSchema: "ExamSittingManagerTransitionRequest", SuccessStatus: "200", SuccessRef: "#/components/responses/ExamSittingOK", SuccessSchema: "ExamSittingResponse", PublicErrorCodes: examSittingMutationContractCodes("exam.sitting.revision_conflict", "exam.sitting.state_conflict", "exam.sitting.deadline_reached")},
			{Key: "POST " + member + "/resume", Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/ResumeExamSitting", RequestSchema: "ExamSittingManagerTransitionRequest", SuccessStatus: "200", SuccessRef: "#/components/responses/ExamSittingOK", SuccessSchema: "ExamSittingResponse", PublicErrorCodes: examSittingMutationContractCodes("exam.archived", "exam.sitting.revision_conflict", "exam.sitting.state_conflict", "exam.sitting.deadline_reached")},
			{Key: "POST " + member + "/extend", Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/ExtendExamSitting", RequestSchema: "ExtendExamSittingRequest", SuccessStatus: "200", SuccessRef: "#/components/responses/ExamSittingOK", SuccessSchema: "ExamSittingResponse", PublicErrorCodes: examSittingMutationContractCodes("exam.archived", "exam.sitting.revision_conflict", "exam.sitting.state_conflict", "exam.sitting.deadline_reached", "exam.sitting.extension_not_later", "exam.sitting.class_ineligible", "exam.sitting.schedule_outside_period")},
			{Key: "POST " + member + "/close", Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/CloseExamSitting", RequestSchema: "ExamSittingManagerTransitionRequest", SuccessStatus: "200", SuccessRef: "#/components/responses/ExamSittingOK", SuccessSchema: "ExamSittingResponse", PublicErrorCodes: examSittingMutationContractCodes("exam.sitting.revision_conflict", "exam.sitting.state_conflict", "exam.sitting.deadline_reached")},
			{Key: "GET " + member + "/no-shows", Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/ExamSittingNoShowListOK", SuccessSchema: "ExamSittingNoShowListResponse", PublicErrorCodes: principalContractCodes("request.invalid", "resource.not_found", "exam.sitting.invalid", "exam.sitting.unavailable", "exam.sitting.state_conflict", "administration.unavailable")},
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
	if err := runtimeAPI.collectResources(model.APIURLSuffix, examSittingResource(nil)); err != nil {
		t.Fatal(err)
	}
	assertOpenAPIAgreement(t, suite, runtimeAPI.Routes())
	assertExamSittingListParameters(t, base)
	assertExamSittingNoShowListParameters(t, member+"/no-shows")
}

func assertExamSittingNoShowListParameters(t *testing.T, path string) {
	t.Helper()
	document := readOpenAPIDocument(t)
	var operation openAPIOperation
	if err := json.Unmarshal(document.Paths[path]["get"], &operation); err != nil {
		t.Fatal(err)
	}
	want := []string{"limit", "cursor"}
	got := make([]string, 0, len(operation.Parameters))
	for _, parameter := range operation.Parameters {
		got = append(got, parameter.Name)
	}
	if !slices.Equal(got, want) {
		t.Errorf("GET %s query parameters = %v, want exactly %v", path, got, want)
	}
}

func examSittingMutationContractCodes(specific ...string) []string {
	common := []string{"request.invalid", "resource.not_found", "exam.sitting.invalid", "exam.sitting.conflict", "exam.sitting.unavailable"}
	common = append(common, specific...)
	return principalMutationContractCodes(append(common,
		"idempotency.key_required", "idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress", "administration.unavailable")...)
}

func assertExamSittingListParameters(t *testing.T, path string) {
	t.Helper()
	document := readOpenAPIDocument(t)
	var operation openAPIOperation
	if err := json.Unmarshal(document.Paths[path]["get"], &operation); err != nil {
		t.Fatal(err)
	}
	want := []string{"class_id", "state", "ends_after", "starts_before", "limit", "cursor"}
	got := make([]string, 0, len(operation.Parameters))
	for _, parameter := range operation.Parameters {
		got = append(got, parameter.Name)
	}
	for _, name := range want {
		if !slices.Contains(got, name) {
			t.Errorf("GET %s omits query parameter %q: %v", path, name, got)
		}
	}
	if len(got) != len(want) {
		t.Errorf("GET %s query parameters = %v, want exactly %v", path, got, want)
	}
}
