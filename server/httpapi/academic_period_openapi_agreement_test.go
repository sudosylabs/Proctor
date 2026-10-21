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
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestAcademicPeriodOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	require.NoError(t, runtimeAPI.collectResources(model.APIURLSuffix, academicPeriodResource(nil)))
	suite := openAPIAgreementSuite{
		Operations: []openAPIAgreementOperation{
			{Key: "GET /api/v1/academic-periods", Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/AcademicPeriodListOK", SuccessSchema: "AcademicPeriodListResponse", PublicErrorCodes: principalContractCodes("request.invalid", "resource.not_found", "administration.unavailable")},
			{Key: "POST /api/v1/academic-periods", Auth: AuthPrincipalRequired, Idempotency: IdempotencyOptional, RequestBodyRef: "#/components/requestBodies/CreateAcademicPeriod", RequestSchema: "CreateAcademicPeriodRequest", SuccessStatus: "201", SuccessRef: "#/components/responses/AcademicPeriodCreated", SuccessSchema: "AcademicPeriodResponse", PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "academic_period.invalid", "academic_period.conflict", "administration.unavailable", "idempotency.key_invalid", "idempotency.conflict", "idempotency.in_progress")},
			{Key: "GET /api/v1/academic-periods/{academic_period_id}", Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/AcademicPeriodOK", SuccessSchema: "AcademicPeriodResponse", PublicErrorCodes: principalContractCodes("request.invalid", "resource.not_found", "administration.unavailable")},
			{Key: "PATCH /api/v1/academic-periods/{academic_period_id}", Auth: AuthPrincipalRequired, RequestBodyRef: "#/components/requestBodies/UpdateAcademicPeriod", RequestSchema: "UpdateAcademicPeriodRequest", SuccessStatus: "200", SuccessRef: "#/components/responses/AcademicPeriodOK", SuccessSchema: "AcademicPeriodResponse", PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "academic_period.invalid", "academic_period.conflict", "administration.unavailable")},
			{Key: "DELETE /api/v1/academic-periods/{academic_period_id}", Auth: AuthPrincipalRequired, SuccessStatus: "204", SuccessRef: "#/components/responses/AcademicPeriodArchived", PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "academic_period.conflict", "administration.unavailable")},
		},
		Schemas: []openAPIAgreementSchema{
			{Name: "AcademicPeriodResponse", DTO: reflect.TypeOf(academicPeriodResponse{}), Required: []string{"revision", "id", "created_at", "updated_at", "owner_type", "owner_id", "name", "display_name", "description", "start_at", "end_at"}},
			{Name: "CreateAcademicPeriodRequest", DTO: reflect.TypeOf(createAcademicPeriodRequest{}), Required: []string{"owner_type", "owner_id", "name", "display_name", "start_at", "end_at"}},
			{Name: "UpdateAcademicPeriodRequest", DTO: reflect.TypeOf(updateAcademicPeriodRequest{}), NonNullable: []string{"expected_revision"}},
		},
	}
	assertOpenAPIAgreement(t, suite, runtimeAPI.Routes())

	document := readOpenAPIDocument(t)
	var listOperation openAPIOperation
	require.NoError(t, json.Unmarshal(document.Paths[model.APIURLSuffix+"/academic-periods"]["get"], &listOperation))
	require.Equal(t, []string{"limit", "q"}, academicStructureQueryParameterNames(listOperation.Parameters), "Academic Period list query parameters")
	list := document.Components.Schemas["AcademicPeriodListResponse"]
	require.Equal(t, "array", list.Type)
	require.Equal(t, "#/components/schemas/AcademicPeriodResponse", list.Items.Ref)
	require.Equal(t, "#/components/headers/NoStore", document.Components.Responses["AcademicPeriodArchived"].Headers["Cache-Control"].Ref)
}

func academicStructureQueryParameterNames(parameters []openAPIParameter) []string {
	names := make([]string, 0, len(parameters))
	for _, parameter := range parameters {
		if parameter.In == "query" {
			names = append(names, parameter.Name)
		}
	}
	sort.Strings(names)
	return names
}
