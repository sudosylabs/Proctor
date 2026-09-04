// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"reflect"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestAuditListingOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	suite := openAPIAgreementSuite{
		Operations: []openAPIAgreementOperation{
			{
				Key: "GET /api/v1/audits", Auth: AuthPrincipalRequired,
				SuccessStatus: "200", SuccessRef: "#/components/responses/AuditListOK", SuccessSchema: "AuditListResponse",
				PublicErrorCodes: principalContractCodes("audit.query.invalid", "audit.unavailable"),
			},
		},
		Schemas: []openAPIAgreementSchema{
			{
				Name: "AuditEventResponse", DTO: reflect.TypeOf(auditEventResponse{}),
				Required: []string{"id", "create_at", "update_at", "action", "resource", "status"},
			},
			{
				Name: "AuditListResponse", DTO: reflect.TypeOf(auditListResponse{}),
				Required: []string{"events"},
			},
		},
	}
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.collectResources(model.APIURLSuffix, auditResource(nil)); err != nil {
		t.Fatal(err)
	}
	assertOpenAPIAgreement(t, suite, runtimeAPI.Routes())
}
