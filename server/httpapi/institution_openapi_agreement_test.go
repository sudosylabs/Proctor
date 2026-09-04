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
	"strings"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestInstitutionOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()

	document := readOpenAPIDocument(t)
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.collectResources(model.APIURLSuffix, institutionResource(nil)); err != nil {
		t.Fatal(err)
	}
	suite := openAPIAgreementSuite{
		Operations: []openAPIAgreementOperation{
			{Key: "GET /api/v1/institution", Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/InstitutionOK", SuccessSchema: "InstitutionResponse", PublicErrorCodes: principalContractCodes("resource.not_found", "administration.unavailable")},
			{Key: "PATCH /api/v1/institution", Auth: AuthPrincipalRequired, RequestBodyRef: "#/components/requestBodies/UpdateInstitution", RequestSchema: "UpdateInstitutionRequest", SuccessStatus: "200", SuccessRef: "#/components/responses/InstitutionOK", SuccessSchema: "InstitutionResponse", PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "institution.invalid", "institution.conflict", "administration.unavailable")},
		},
		Schemas: []openAPIAgreementSchema{
			{Name: "InstitutionResponse", DTO: reflect.TypeOf(institutionResponse{}), Required: []string{"id", "create_at", "update_at", "delete_at", "name", "display_name", "description", "exam_capacity"}},
			{Name: "UpdateInstitutionRequest", DTO: reflect.TypeOf(updateInstitutionRequest{})},
			{Name: "ExamCapacityPolicy", DTO: reflect.TypeOf(examCapacityPolicyResponse{}), Required: []string{"resource_maximum_count", "resource_maximum_bytes", "workspace_maximum_entries", "workspace_maximum_file_bytes", "workspace_maximum_total_bytes"}},
		},
	}
	assertOpenAPIAgreement(t, suite, runtimeAPI.Routes())

	patchSchema := document.Components.Schemas["UpdateInstitutionRequest"]
	for _, propertyName := range []string{"name", "display_name", "description"} {
		var property struct {
			Description string `json:"description"`
		}
		if err := json.Unmarshal(patchSchema.Properties[propertyName], &property); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(property.Description, "Omitted or null leaves the value unchanged") ||
			!strings.Contains(property.Description, "empty string is present") {
			t.Errorf("%s semantics = %q", propertyName, property.Description)
		}
	}
}
