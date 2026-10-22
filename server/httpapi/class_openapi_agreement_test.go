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

func TestClassOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	runtimeAPI := newFocusedResourceAPI(
		t,
		logger,
		classRouteAuthenticator{principal: model.Principal{}},
		classResource(&classHTTPApplication{}),
	)
	suite := openAPIAgreementSuite{
		Operations: []openAPIAgreementOperation{
			{Key: "GET /api/v1/academic-units/{academic_unit_id}/classes", Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/ClassListOK", SuccessSchema: "ClassListResponse", PublicErrorCodes: principalContractCodes("request.invalid", "resource.not_found", "administration.unavailable")},
			{Key: "GET /api/v1/programme-levels/{programme_level_id}/classes", Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/ClassListOK", SuccessSchema: "ClassListResponse", PublicErrorCodes: principalContractCodes("request.invalid", "resource.not_found", "administration.unavailable")},
			{Key: "POST /api/v1/programme-levels/{programme_level_id}/classes", Auth: AuthPrincipalRequired, RequestBodyRef: "#/components/requestBodies/CreateClass", RequestSchema: "CreateClassRequest", SuccessStatus: "201", SuccessRef: "#/components/responses/ClassCreated", SuccessSchema: "ClassResponse", PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "class.invalid", "class.conflict", "administration.unavailable")},
			{Key: "GET /api/v1/classes/{class_id}", Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/ClassOK", SuccessSchema: "ClassResponse", PublicErrorCodes: principalContractCodes("request.invalid", "resource.not_found", "administration.unavailable")},
			{Key: "PATCH /api/v1/classes/{class_id}", Auth: AuthPrincipalRequired, RequestBodyRef: "#/components/requestBodies/UpdateClass", RequestSchema: "UpdateClassRequest", SuccessStatus: "200", SuccessRef: "#/components/responses/ClassOK", SuccessSchema: "ClassResponse", PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "class.invalid", "class.conflict", "administration.unavailable")},
			{Key: "DELETE /api/v1/classes/{class_id}", Auth: AuthPrincipalRequired, RequestBodyRef: "#/components/requestBodies/ArchiveAcademic", RequestSchema: "ArchiveAcademicRequest", SuccessStatus: "204", SuccessRef: "#/components/responses/ClassArchived", PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "class.conflict", "administration.unavailable")},
		},
		Schemas: []openAPIAgreementSchema{
			{Name: "ArchiveAcademicRequest", DTO: reflect.TypeOf(archiveAcademicRequest{}), NonNullable: []string{"expected_revision"}},
			{Name: "ClassResponse", DTO: reflect.TypeOf(classResponse{}), Required: []string{"revision", "id", "created_at", "updated_at", "programme_level_id", "academic_period_id", "name", "display_name", "description"}},
			{Name: "CreateClassRequest", DTO: reflect.TypeOf(createClassRequest{}), Required: []string{"academic_period_id", "name", "display_name"}},
			{Name: "UpdateClassRequest", DTO: reflect.TypeOf(updateClassRequest{}), NonNullable: []string{"expected_revision"}},
		},
	}
	assertOpenAPIAgreement(t, suite, runtimeAPI.Routes())

	document := readOpenAPIDocument(t)
	var searchOperation openAPIOperation
	path := model.APIURLSuffix + "/academic-units/{academic_unit_id}/classes"
	require.NoError(t, json.Unmarshal(document.Paths[path]["get"], &searchOperation))
	require.Equal(t, []string{"cursor", "limit", "q"}, academicStructureQueryParameterNames(searchOperation.Parameters), "Class search query parameters")
	list := document.Components.Schemas["ClassListResponse"]
	requireOpenAPIListEnvelope(t, list, "#/components/schemas/ClassResponse")
	require.Equal(t, "#/components/headers/NoStore", document.Components.Responses["ClassArchived"].Headers["Cache-Control"].Ref)
}
