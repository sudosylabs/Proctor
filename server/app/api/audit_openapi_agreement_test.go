// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestAuditListingOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	document := readOpenAPIDocument(t)
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.collectResources(model.APIURLSuffix, auditResource(nil)); err != nil {
		t.Fatal(err)
	}
	runtimeOperations := make(map[string]AuthRequirement)
	for _, route := range runtimeAPI.Routes() {
		if route.Path != model.APIURLSuffix+"/audits" {
			continue
		}
		runtimeOperations[route.Method+" "+route.Path] = route.Auth
	}
	expected := map[string]openAPIOperationContract{
		"GET /api/v1/audits": {
			successStatus: "200", successRef: "#/components/responses/AuditListOK",
			successSchema: "AuditListResponse",
			errorCodes:    principalContractCodes("audit.query.invalid", "audit.unavailable"),
		},
	}
	statuses := ApplicationErrorStatuses()
	statuses["authentication.credential_ambiguous"] = http.StatusBadRequest
	statuses["authentication.csrf.invalid"] = http.StatusForbidden
	documented := make(map[string]AuthRequirement)
	for path, item := range document.Paths {
		if path != "/api/v1/audits" {
			continue
		}
		for method, raw := range item {
			upper := strings.ToUpper(method)
			if !isHTTPMethod(upper) {
				continue
			}
			key := upper + " " + path
			var operation openAPIOperation
			if err := json.Unmarshal(raw, &operation); err != nil {
				t.Fatal(err)
			}
			documented[key] = operation.Auth
			contract, exists := expected[key]
			if !exists {
				t.Fatalf("unexpected operation %s", key)
			}
			assertPrincipalSecurity(t, key, upper, operation.Security)
			if operation.Responses[contract.successStatus].Ref != contract.successRef {
				t.Errorf("%s success ref does not agree", key)
			}
			assertOpenAPISuccessResponse(t, document, key, contract)
			got, want := append([]string(nil), operation.ErrorCodes...), append([]string(nil), contract.errorCodes...)
			sort.Strings(got)
			sort.Strings(want)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s error codes = %v, want %v", key, got, want)
			}
			for _, code := range operation.ErrorCodes {
				status, exists := statuses[code]
				if !exists {
					t.Errorf("%s unmapped code %q", key, code)
					continue
				}
				assertOpenAPIProblemResponse(t, document, key, status, operation.Responses[strconv.Itoa(status)])
			}
		}
	}
	if !reflect.DeepEqual(documented, runtimeOperations) {
		t.Fatalf("documented=%v runtime=%v", documented, runtimeOperations)
	}
	assertOpenAPISchemaMatchesDTO(
		t, document, "AuditEventResponse", reflect.TypeOf(auditEventResponse{}),
		[]string{"id", "create_at", "update_at", "action", "resource", "status"},
	)
	assertOpenAPISchemaMatchesDTO(
		t, document, "AuditListResponse", reflect.TypeOf(auditListResponse{}),
		[]string{"events"},
	)
}
