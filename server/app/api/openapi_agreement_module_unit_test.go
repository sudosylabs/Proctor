// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestOpenAPIAgreementEvaluatorAcceptsNormalizedRouteWithoutMutatingInputs(t *testing.T) {
	t.Parallel()

	routes, suite := syntheticOpenAPIAgreementInputs()
	wantRoutes := cloneAgreementRoutes(routes)
	wantSuite := cloneAgreementSuite(suite)

	violations, err := evaluateOpenAPIAgreement(syntheticOpenAPIDocument(t), routes, suite)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("violations = %v", violations)
	}
	if !reflect.DeepEqual(routes, wantRoutes) {
		t.Fatalf("runtime routes were mutated: got %#v, want %#v", routes, wantRoutes)
	}
	if !reflect.DeepEqual(suite.Operations, wantSuite.Operations) || !reflect.DeepEqual(suite.Schemas, wantSuite.Schemas) {
		t.Fatalf("suite was mutated: got %#v, want %#v", suite, wantSuite)
	}
}

func TestOpenAPISchemaAgreementAllowsNullForRequiredResponsePointer(t *testing.T) {
	t.Parallel()
	violations := evaluateOpenAPIShapeAgreement(
		nil,
		openAPIDocument{},
		"schema Response.archived_at",
		openAPISchemaShape{Type: []any{"string", "null"}},
		reflect.TypeOf((*string)(nil)),
		false,
		false,
		true,
		false,
		nil,
		nil,
	)
	if len(violations) != 0 {
		t.Fatalf("violations = %v", violations)
	}
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
	if len(violations) != 0 {
		t.Fatalf("violations = %v", violations)
	}
}

func TestOpenAPIAgreementEvaluatorRejectsMalformedDocument(t *testing.T) {
	t.Parallel()

	routes, suite := syntheticOpenAPIAgreementInputs()
	if _, err := evaluateOpenAPIAgreement([]byte(`{"paths":`), routes, suite); err == nil || !strings.Contains(err.Error(), "decode document") {
		t.Fatalf("error = %v, want malformed-document error", err)
	}
	if _, err := evaluateOpenAPIAgreement([]byte(`{}`), routes, suite); err == nil || !strings.Contains(err.Error(), "paths are missing") {
		t.Fatalf("error = %v, want missing-paths error", err)
	}
}

func TestOpenAPIAgreementEvaluatorRejectsAmbiguousSelection(t *testing.T) {
	t.Parallel()

	routes, suite := syntheticOpenAPIAgreementInputs()
	routes = append(routes, cloneAgreementRoutes(routes)...)
	if _, err := evaluateOpenAPIAgreement(syntheticOpenAPIDocument(t), routes, suite); err == nil || !strings.Contains(err.Error(), "runtime selection is ambiguous") {
		t.Fatalf("error = %v, want ambiguous runtime-selection error", err)
	}

	var document map[string]any
	if err := json.Unmarshal(syntheticOpenAPIDocument(t), &document); err != nil {
		t.Fatal(err)
	}
	pathItem := document["paths"].(map[string]any)["/things/{thing_id}"].(map[string]any)
	pathItem["GET"] = pathItem["get"]
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	routes, suite = syntheticOpenAPIAgreementInputs()
	if _, err := evaluateOpenAPIAgreement(encoded, routes, suite); err == nil || !strings.Contains(err.Error(), "document selection is ambiguous") {
		t.Fatalf("error = %v, want ambiguous document-selection error", err)
	}
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
			if _, err := evaluateOpenAPIAgreement([]byte(`{}`), nil, test.suite); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestOpenAPIAgreementEvaluatorOrdersMultipleDisagreements(t *testing.T) {
	t.Parallel()

	routes, suite := syntheticOpenAPIAgreementInputs()
	routes[0].Auth = AuthPublic
	routes[0].ErrorCodes = []string{"request.invalid"}

	var document map[string]any
	if err := json.Unmarshal(syntheticOpenAPIDocument(t), &document); err != nil {
		t.Fatal(err)
	}
	operation := document["paths"].(map[string]any)["/things/{thing_id}"].(map[string]any)["get"].(map[string]any)
	operation["x-proctor-auth"] = string(AuthPublic)
	operation["x-proctor-error-codes"] = []any{"request.invalid"}
	operation["security"] = []any{}
	operation["responses"].(map[string]any)["200"] = map[string]any{"$ref": "#/components/responses/Wrong"}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}

	violations, err := evaluateOpenAPIAgreement(encoded, routes, suite)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) < 5 {
		t.Fatalf("violations = %v, want multiple independent disagreements", violations)
	}
	got := make([]string, len(violations))
	for index, violation := range violations {
		got[index] = violation.String()
	}
	want := append([]string(nil), got...)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("violations are not deterministic:\n got %v\nwant %v", got, want)
	}
	if !strings.Contains(strings.Join(got, "\n"), "runtime error codes") ||
		!strings.Contains(strings.Join(got, "\n"), "document error codes") {
		t.Fatalf("violations do not cover both runtime and document error parity: %v", got)
	}
}

func TestOpenAPIAgreementEvaluatorSelectorNarrowsCombinedResource(t *testing.T) {
	t.Parallel()

	routes, suite := syntheticOpenAPIAgreementInputs()
	routes = append(routes, Route{Method: "GET", Path: "/unrelated", Auth: AuthPublic})
	suite.OperationSelector = func(_ string, path string) bool {
		return strings.HasPrefix(path, "/things/")
	}

	var document map[string]any
	if err := json.Unmarshal(syntheticOpenAPIDocument(t), &document); err != nil {
		t.Fatal(err)
	}
	document["paths"].(map[string]any)["/unrelated"] = map[string]any{
		"get": map[string]any{"x-proctor-auth": "public", "security": []any{}, "responses": map[string]any{}},
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}

	violations, err := evaluateOpenAPIAgreement(encoded, routes, suite)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("violations = %v", violations)
	}
}

func TestOpenAPIAgreementEvaluatorLeavesExceptionalSuccessContentLocal(t *testing.T) {
	t.Parallel()

	routes, suite := syntheticOpenAPIAgreementInputs()
	suite.Operations[0].ExceptionalSuccess = true
	suite.Operations[0].SuccessSchema = ""

	var document map[string]any
	if err := json.Unmarshal(syntheticOpenAPIDocument(t), &document); err != nil {
		t.Fatal(err)
	}
	response := document["components"].(map[string]any)["responses"].(map[string]any)["ThingOK"].(map[string]any)
	response["content"] = map[string]any{"image/webp": map[string]any{"schema": map[string]any{"type": "string", "format": "binary"}}}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}

	violations, err := evaluateOpenAPIAgreement(encoded, routes, suite)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("violations = %v", violations)
	}

	suite.Operations[0].ExceptionalSuccess = false
	violations, err = evaluateOpenAPIAgreement(encoded, routes, suite)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 1 || violations[0].Check != "success schema" {
		t.Fatalf("default ordinary-success violations = %v", violations)
	}
}

func TestOpenAPIAgreementEvaluatorAllowsInlineExceptionalSuccess(t *testing.T) {
	t.Parallel()

	routes, suite := syntheticOpenAPIAgreementInputs()
	suite.Operations[0].ExceptionalSuccess = true
	suite.Operations[0].SuccessRef = ""
	suite.Operations[0].SuccessSchema = ""

	var document map[string]any
	if err := json.Unmarshal(syntheticOpenAPIDocument(t), &document); err != nil {
		t.Fatal(err)
	}
	operation := document["paths"].(map[string]any)["/things/{thing_id}"].(map[string]any)["get"].(map[string]any)
	operation["responses"] = map[string]any{"101": map[string]any{"description": "Switching Protocols"}, "404": operation["responses"].(map[string]any)["404"]}
	suite.Operations[0].SuccessStatus = "101"
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	violations, err := evaluateOpenAPIAgreement(encoded, routes, suite)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("violations = %v", violations)
	}

	delete(operation["responses"].(map[string]any), "101")
	encoded, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	violations, err = evaluateOpenAPIAgreement(encoded, routes, suite)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 1 || violations[0].Check != "success status" {
		t.Fatalf("missing inline status violations = %v", violations)
	}
}

func syntheticOpenAPIAgreementInputs() ([]Route, openAPIAgreementSuite) {
	codes := []string{"resource.not_found"}
	return []Route{{
			Method: "GET", Path: "/things/{thing_id:[0-9]+}", Auth: AuthPrincipalRequired,
			ErrorCodes: append([]string(nil), codes...),
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
        "x-proctor-error-codes": ["resource.not_found"],
        "security": [{"bearerAuth": []}, {"sessionCookie": []}],
        "responses": {
          "200": {"$ref": "#/components/responses/ThingOK"},
          "404": {"$ref": "#/components/responses/NotFound"}
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
      "NotFound": {
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
