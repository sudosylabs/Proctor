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

	"github.com/stretchr/testify/require"

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
				PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "class_member.invalid", "class_member.student_affiliation_required", "class.enrollment_conflict", "mail.unavailable", "administration.unavailable"),
			},
			{
				Key: "DELETE /api/v1/class-members/{class_member_id}", Auth: AuthPrincipalRequired,
				SuccessStatus: "200", SuccessRef: "#/components/responses/ClassMemberEnded", SuccessSchema: "ClassMemberResponse",
				PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "class.enrollment_conflict", "mail.unavailable", "administration.unavailable"),
			},
		},
		Schemas: []openAPIAgreementSchema{
			{
				Name: "ClassMemberResponse", DTO: reflect.TypeOf(classMemberResponse{}),
				Required: []string{"id", "created_at", "updated_at", "class_id", "academic_period_id", "user_id", "start_at"},
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
	require.NoError(t, runtimeAPI.collectResources(model.APIURLSuffix, classMemberResource(&classMemberHTTPApplication{})))
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
	require.NoError(t, json.Unmarshal(document.Paths["/api/v1/classes/{class_id}/members"]["get"], &listOperation))
	queryParameters := map[string]bool{}
	for _, parameter := range listOperation.Parameters {
		if parameter.In == "query" {
			queryParameters[parameter.Name] = true
		}
	}
	require.True(t, queryParameters["active_at"], "list query parameters = %#v, want active_at and history", queryParameters)
	require.True(t, queryParameters["history"], "list query parameters = %#v, want active_at and history", queryParameters)

	// This v1 relationship collection remains a legacy bare array.
	list := document.Components.Schemas["ClassMemberListResponse"]
	requireOpenAPIListEnvelope(t, list, "#/components/schemas/ClassMemberResponse")
}
