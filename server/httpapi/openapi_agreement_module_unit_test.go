// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAPIAgreementEvaluatorAcceptsNormalizedRouteWithoutMutatingInputs(t *testing.T) {
	t.Parallel()

	routes, suite := syntheticOpenAPIAgreementInputs()
	wantRoutes := cloneAgreementRoutes(routes)
	wantSuite := cloneAgreementSuite(suite)

	violations, err := evaluateOpenAPIAgreement(syntheticOpenAPIDocument(t), routes, suite)
	require.NoError(t, err)
	require.Empty(t, violations)
	require.Equal(t, wantRoutes, routes, "runtime routes were mutated")
	require.Equal(t, wantSuite.Operations, suite.Operations, "suite was mutated: got %#v, want %#v", suite, wantSuite)
	require.Equal(t, wantSuite.Schemas, suite.Schemas, "suite was mutated: got %#v, want %#v", suite, wantSuite)
}

func TestOpenAPISchemaAgreementAllowsNullForRequiredResponsePointer(t *testing.T) {
	t.Parallel()
	violations := evaluateOpenAPIShapeAgreement(
		nil,
		openAPIDocument{},
		"schema Response.display_name",
		openAPISchemaShape{Type: []any{"string", "null"}},
		reflect.TypeOf((*string)(nil)),
		false,
		false,
		true,
		false,
		nil,
		nil,
	)
	require.Empty(t, violations)
}

func TestOpenAPISchemaAgreementAllowsPresenceAwareNonNullRequestField(t *testing.T) {
	t.Parallel()
	violations := evaluateOpenAPIShapeAgreement(
		nil,
		openAPIDocument{},
		"schema Request.class_id",
		openAPISchemaShape{Type: "string"},
		reflect.TypeOf(Optional[string]{}),
		true,
		true,
		false,
		true,
		nil,
		nil,
	)
	require.Empty(t, violations)
}

func TestOpenAPIAgreementEvaluatorRejectsMalformedDocument(t *testing.T) {
	t.Parallel()

	routes, suite := syntheticOpenAPIAgreementInputs()
	_, err := evaluateOpenAPIAgreement([]byte(`{"paths":`), routes, suite)
	require.Error(t, err)
	require.Contains(t, err.Error(), "decode document")
	_, err = evaluateOpenAPIAgreement([]byte(`{}`), routes, suite)
	require.Error(t, err)
	require.Contains(t, err.Error(), "paths are missing")
}

func TestOpenAPIAgreementEvaluatorRejectsAmbiguousSelection(t *testing.T) {
	t.Parallel()

	routes, suite := syntheticOpenAPIAgreementInputs()
	routes = append(routes, cloneAgreementRoutes(routes)...)
	_, err := evaluateOpenAPIAgreement(syntheticOpenAPIDocument(t), routes, suite)
	require.Error(t, err)
	require.Contains(t, err.Error(), "runtime selection is ambiguous")

	var document map[string]any
	require.NoError(t, json.Unmarshal(syntheticOpenAPIDocument(t), &document))
	pathItem := document["paths"].(map[string]any)["/things/{thing_id}"].(map[string]any)
	pathItem["GET"] = pathItem["get"]
	encoded, err := json.Marshal(document)
	require.NoError(t, err)
	routes, suite = syntheticOpenAPIAgreementInputs()
	_, err = evaluateOpenAPIAgreement(encoded, routes, suite)
	require.Error(t, err)
	require.Contains(t, err.Error(), "document selection is ambiguous")
}

func TestOpenAPIAgreementEvaluatorRejectsMalformedConfiguration(t *testing.T) {
	t.Parallel()

	_, valid := syntheticOpenAPIAgreementInputs()
	tests := []struct {
		name  string
		suite openAPIAgreementSuite
		want  string
	}{
		{name: "no operations", suite: openAPIAgreementSuite{}, want: "no operations"},
		{name: "invalid key", suite: suiteWithOperation(valid, func(operation *openAPIAgreementOperation) { operation.Key = "get things" }), want: "invalid key"},
		{name: "unnormalized key", suite: suiteWithOperation(valid, func(operation *openAPIAgreementOperation) { operation.Key = "GET /things/{thing_id:[0-9]+}" }), want: "invalid normalized path"},
		{name: "missing auth", suite: suiteWithOperation(valid, func(operation *openAPIAgreementOperation) { operation.Auth = "" }), want: "authentication intent"},
		{name: "unmapped error", suite: suiteWithOperation(valid, func(operation *openAPIAgreementOperation) { operation.PublicErrorCodes = []string{"unknown.code"} }), want: "unmapped public error"},
		{name: "request without schema", suite: suiteWithOperation(valid, func(operation *openAPIAgreementOperation) {
			operation.RequestBodyRef = "#/components/requestBodies/Thing"
		}), want: "no identifiable target"},
		{
			name: "request schema without DTO agreement",
			suite: func() openAPIAgreementSuite {
				value := cloneAgreementSuite(valid)
				value.Operations[0].RequestBodyRef = "#/components/requestBodies/Thing"
				value.Operations[0].RequestSchema = "ThingRequest"
				value.Schemas = nil
				return value
			}(),
			want: "has no DTO agreement",
		},
		{name: "exceptional with ordinary schema", suite: suiteWithOperation(valid, func(operation *openAPIAgreementOperation) { operation.ExceptionalSuccess = true }), want: "must not declare an ordinary schema"},
		{
			name: "selector excludes declaration",
			suite: func() openAPIAgreementSuite {
				value := cloneAgreementSuite(valid)
				value.OperationSelector = func(string, string) bool { return false }
				return value
			}(),
			want: "selector excludes",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := evaluateOpenAPIAgreement([]byte(`{}`), nil, test.suite)
			require.Error(t, err)
			require.Contains(t, err.Error(), test.want)
		})
	}
}

func TestOpenAPIAgreementEvaluatorOrdersMultipleDisagreements(t *testing.T) {
	t.Parallel()

	routes, suite := syntheticOpenAPIAgreementInputs()
	routes[0].Auth = AuthPublic
	routes[0].ErrorCodes = []string{"request.invalid"}

	var document map[string]any
	require.NoError(t, json.Unmarshal(syntheticOpenAPIDocument(t), &document))
	operation := document["paths"].(map[string]any)["/things/{thing_id}"].(map[string]any)["get"].(map[string]any)
	operation["x-proctor-auth"] = string(AuthPublic)
	operation["x-proctor-error-codes"] = []any{"request.invalid"}
	operation["security"] = []any{}
	operation["responses"].(map[string]any)["200"] = map[string]any{"$ref": "#/components/responses/Wrong"}
	encoded, err := json.Marshal(document)
	require.NoError(t, err)

	violations, err := evaluateOpenAPIAgreement(encoded, routes, suite)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(violations), 5, "violations")
	got := make([]string, len(violations))
	for index, violation := range violations {
		got[index] = violation.String()
	}
	want := append([]string(nil), got...)
	sort.Strings(want)
	require.Equal(t, want, got, "violations are not deterministic:\n got")
	require.Contains(t, strings.Join(got, "\n"), "runtime error codes")
	require.Contains(t, strings.Join(got, "\n"), "document error codes")
}

func TestOpenAPIAgreementEvaluatorSelectorNarrowsCombinedResource(t *testing.T) {
	t.Parallel()

	routes, suite := syntheticOpenAPIAgreementInputs()
	routes = append(routes, Route{Method: "GET", Path: "/unrelated", Auth: AuthPublic})
	suite.OperationSelector = func(_ string, path string) bool {
		return strings.HasPrefix(path, "/things/")
	}

	var document map[string]any
	require.NoError(t, json.Unmarshal(syntheticOpenAPIDocument(t), &document))
	document["paths"].(map[string]any)["/unrelated"] = map[string]any{
		"get": map[string]any{"x-proctor-auth": "public", "security": []any{}, "responses": map[string]any{}},
	}
	encoded, err := json.Marshal(document)
	require.NoError(t, err)

	violations, err := evaluateOpenAPIAgreement(encoded, routes, suite)
	require.NoError(t, err)
	require.Empty(t, violations)
}

func TestOpenAPIAgreementEvaluatorLeavesExceptionalSuccessContentLocal(t *testing.T) {
	t.Parallel()

	routes, suite := syntheticOpenAPIAgreementInputs()
	suite.Operations[0].ExceptionalSuccess = true
	suite.Operations[0].SuccessSchema = ""

	var document map[string]any
	require.NoError(t, json.Unmarshal(syntheticOpenAPIDocument(t), &document))
	response := document["components"].(map[string]any)["responses"].(map[string]any)["ThingOK"].(map[string]any)
	response["content"] = map[string]any{"image/webp": map[string]any{"schema": map[string]any{"type": "string", "format": "binary"}}}
	encoded, err := json.Marshal(document)
	require.NoError(t, err)

	violations, err := evaluateOpenAPIAgreement(encoded, routes, suite)
	require.NoError(t, err)
	require.Empty(t, violations)

	suite.Operations[0].ExceptionalSuccess = false
	violations, err = evaluateOpenAPIAgreement(encoded, routes, suite)
	require.NoError(t, err)
	require.Len(t, violations, 1)
	require.Equal(t, "success schema", violations[0].Check)
}

func TestOpenAPIAgreementEvaluatorAllowsInlineExceptionalSuccess(t *testing.T) {
	t.Parallel()

	routes, suite := syntheticOpenAPIAgreementInputs()
	suite.Operations[0].ExceptionalSuccess = true
	suite.Operations[0].SuccessRef = ""
	suite.Operations[0].SuccessSchema = ""

	var document map[string]any
	require.NoError(t, json.Unmarshal(syntheticOpenAPIDocument(t), &document))
	operation := document["paths"].(map[string]any)["/things/{thing_id}"].(map[string]any)["get"].(map[string]any)
	responses := operation["responses"].(map[string]any)
	delete(responses, "200")
	responses["101"] = map[string]any{"description": "Switching Protocols"}
	suite.Operations[0].SuccessStatus = "101"
	encoded, err := json.Marshal(document)
	require.NoError(t, err)
	violations, err := evaluateOpenAPIAgreement(encoded, routes, suite)
	require.NoError(t, err)
	require.Empty(t, violations)

	delete(operation["responses"].(map[string]any), "101")
	encoded, err = json.Marshal(document)
	require.NoError(t, err)
	violations, err = evaluateOpenAPIAgreement(encoded, routes, suite)
	require.NoError(t, err)
	require.Len(t, violations, 1)
	require.Equal(t, "success status", violations[0].Check)
}

func syntheticOpenAPIAgreementInputs() ([]Route, openAPIAgreementSuite) {
	codes := []string{"resource.not_found"}
	return []Route{{
			Method: "GET", Path: "/things/{thing_id:[0-9]+}", Auth: AuthPrincipalRequired,
			ErrorCodes: routeErrorCodes(AuthPrincipalRequired, http.MethodGet, codes),
		}}, openAPIAgreementSuite{Operations: []openAPIAgreementOperation{{
			Key: "GET /things/{thing_id}", Auth: AuthPrincipalRequired,
			SuccessStatus: "200", SuccessRef: "#/components/responses/ThingOK", SuccessSchema: "ThingResponse",
			PublicErrorCodes: append([]string(nil), codes...),
		}}}
}

func syntheticOpenAPIDocument(t *testing.T) []byte {
	t.Helper()
	return []byte(`{
  "openapi": "3.1.0",
  "paths": {
    "/things/{thing_id}": {
      "get": {
        "operationId": "getThing",
        "x-proctor-auth": "principal_required",
        "x-proctor-error-codes": ["resource.not_found", "authentication.required", "authentication.token_invalid", "authentication.credential_ambiguous", "authentication.dpop.invalid", "authentication.dpop.replayed", "authentication.dpop.use_nonce", "authentication.dpop.unavailable"],
        "security": [{"bearerAuth": []}, {"dpopAuth": []}, {"sessionCookie": []}],
        "responses": {
          "200": {"$ref": "#/components/responses/ThingOK"},
          "400": {"$ref": "#/components/responses/BadRequest"},
          "401": {"$ref": "#/components/responses/Unauthorized"},
          "404": {"$ref": "#/components/responses/NotFound"},
          "503": {"$ref": "#/components/responses/ServiceUnavailable"}
        }
      }
    }
  },
  "components": {
    "schemas": {
      "ThingResponse": {"type": "object", "additionalProperties": false, "properties": {}},
      "ProblemDetails": {"type": "object", "additionalProperties": false, "properties": {}}
    },
    "requestBodies": {},
    "responses": {
      "ThingOK": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/ThingResponse"}}}},
      "BadRequest": {
        "headers": {"Cache-Control": {"$ref": "#/components/headers/NoStore"}},
        "content": {"application/problem+json": {"schema": {"$ref": "#/components/schemas/ProblemDetails"}}}
      },
      "NotFound": {
        "headers": {"Cache-Control": {"$ref": "#/components/headers/NoStore"}},
        "content": {"application/problem+json": {"schema": {"$ref": "#/components/schemas/ProblemDetails"}}}
      },
      "Unauthorized": {
        "headers": {"Cache-Control": {"$ref": "#/components/headers/NoStore"}},
        "content": {"application/problem+json": {"schema": {"$ref": "#/components/schemas/ProblemDetails"}}}
      },
      "ServiceUnavailable": {
        "headers": {"Cache-Control": {"$ref": "#/components/headers/NoStore"}},
        "content": {"application/problem+json": {"schema": {"$ref": "#/components/schemas/ProblemDetails"}}}
      }
    }
  }
}`)
}

func suiteWithOperation(
	suite openAPIAgreementSuite,
	change func(*openAPIAgreementOperation),
) openAPIAgreementSuite {
	result := cloneAgreementSuite(suite)
	change(&result.Operations[0])
	return result
}

func cloneAgreementSuite(suite openAPIAgreementSuite) openAPIAgreementSuite {
	result := suite
	result.Operations = append([]openAPIAgreementOperation(nil), suite.Operations...)
	for index := range result.Operations {
		result.Operations[index].PublicErrorCodes = append([]string(nil), suite.Operations[index].PublicErrorCodes...)
	}
	result.Schemas = append([]openAPIAgreementSchema(nil), suite.Schemas...)
	for index := range result.Schemas {
		result.Schemas[index].Required = append([]string(nil), suite.Schemas[index].Required...)
	}
	return result
}

func cloneAgreementRoutes(routes []Route) []Route {
	result := append([]Route(nil), routes...)
	for index := range result {
		result[index].ErrorCodes = append([]string(nil), routes[index].ErrorCodes...)
	}
	return result
}
