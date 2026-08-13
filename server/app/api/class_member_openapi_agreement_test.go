// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestClassMemberOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	suite := openAPIAgreementSuite{
		Operations: []openAPIAgreementOperation{
			{
				Key: "GET /api/v1/classes/{class_id}/members", Auth: AuthPrincipalRequired,
				SuccessStatus: "200", SuccessRef: "#/components/responses/ClassMemberListOK", SuccessSchema: "ClassMemberListResponse",
				PublicErrorCodes: principalContractCodes("request.invalid", "resource.not_found", "administration.unavailable"),
			},
			{
				Key: "POST /api/v1/classes/{class_id}/members", Auth: AuthPrincipalRequired,
				RequestBodyRef: "#/components/requestBodies/EnrollClassMember", RequestSchema: "EnrollClassMemberRequest",
				SuccessStatus: "201", SuccessRef: "#/components/responses/ClassMemberEnrolled", SuccessSchema: "ClassEnrollmentResponse",
				PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "class_member.invalid", "class_member.student_affiliation_required", "class.enrollment_conflict", "administration.unavailable"),
			},
			{
				Key: "DELETE /api/v1/class-members/{class_member_id}", Auth: AuthPrincipalRequired,
				SuccessStatus: "200", SuccessRef: "#/components/responses/ClassMemberEnded", SuccessSchema: "ClassMemberResponse",
				PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "class.enrollment_conflict", "administration.unavailable"),
			},
		},
		Schemas: []openAPIAgreementSchema{
			{
				Name: "ClassMemberResponse", DTO: reflect.TypeOf(classMemberResponse{}),
				Required: []string{"id", "create_at", "update_at", "delete_at", "class_id", "academic_period_id", "user_id", "start_at"},
			},
			{
				Name: "EnrollClassMemberRequest", DTO: reflect.TypeOf(enrollClassMemberRequest{}),
				Required: []string{"user_id"},
			},
			{
				Name: "ClassEnrollmentResponse", DTO: reflect.TypeOf(classEnrollmentResponse{}),
				Required: []string{"membership"},
			},
		},
	}
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.collectResources(model.APIURLSuffix, classMemberResource(&classMemberHTTPApplication{})); err != nil {
		t.Fatal(err)
	}
	assertOpenAPIAgreement(t, suite, runtimeAPI.Routes())

	// Enrollment responses retain both the new membership and an optional
	// historical membership; the ordinary DTO agreement above verifies both.
	// Roster discovery also keeps its explicit point-in-time/history controls.
	document := readOpenAPIDocument(t)
	var listOperation struct {
		Parameters []struct {
			Name string `json:"name"`
			In   string `json:"in"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal(document.Paths["/api/v1/classes/{class_id}/members"]["get"], &listOperation); err != nil {
		t.Fatal(err)
	}
	queryParameters := map[string]bool{}
	for _, parameter := range listOperation.Parameters {
		if parameter.In == "query" {
			queryParameters[parameter.Name] = true
		}
	}
	if !queryParameters["active_at"] || !queryParameters["history"] {
		t.Fatalf("list query parameters = %#v, want active_at and history", queryParameters)
	}

	// This v1 relationship collection remains a legacy bare array.
	list := document.Components.Schemas["ClassMemberListResponse"]
	if list.Type != "array" || list.Items.Ref != "#/components/schemas/ClassMemberResponse" {
		t.Fatalf("ClassMemberListResponse = %#v", list)
	}
}
