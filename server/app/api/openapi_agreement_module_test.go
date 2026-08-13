// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

type openAPIDocument struct {
	OpenAPI    string                                `json:"openapi"`
	Paths      map[string]map[string]json.RawMessage `json:"paths"`
	Components struct {
		Schemas       map[string]openAPISchema      `json:"schemas"`
		Responses     map[string]openAPIResponse    `json:"responses"`
		RequestBodies map[string]openAPIRequestBody `json:"requestBodies"`
	} `json:"components"`
}

type openAPIOperation struct {
	OperationID string                      `json:"operationId"`
	Auth        AuthRequirement             `json:"x-proctor-auth"`
	ErrorCodes  []string                    `json:"x-proctor-error-codes"`
	Security    []map[string][]string       `json:"security"`
	Parameters  []openAPIParameter          `json:"parameters"`
	RequestBody openAPIRequestBody          `json:"requestBody"`
	Responses   map[string]openAPIReference `json:"responses"`
}

type openAPIParameter struct {
	Name     string `json:"name"`
	In       string `json:"in"`
	Required bool   `json:"required"`
}

type openAPIReference struct {
	Ref string `json:"$ref"`
}

type openAPISchema struct {
	Type                 any                        `json:"type"`
	Required             []string                   `json:"required"`
	Properties           map[string]json.RawMessage `json:"properties"`
	AdditionalProperties any                        `json:"additionalProperties"`
	Items                openAPIReference           `json:"items"`
}

type openAPISchemaShape struct {
	Ref                  string                     `json:"$ref"`
	Type                 any                        `json:"type"`
	Format               string                     `json:"format"`
	Required             []string                   `json:"required"`
	Properties           map[string]json.RawMessage `json:"properties"`
	Items                *openAPISchemaShape        `json:"items"`
	AdditionalProperties json.RawMessage            `json:"additionalProperties"`
}

type openAPIResponse struct {
	Headers map[string]openAPIReference `json:"headers"`
	Content map[string]struct {
		Schema openAPISchemaShape `json:"schema"`
	} `json:"content"`
}

type openAPIRequestBody struct {
	Ref     string `json:"$ref"`
	Content map[string]struct {
		Schema openAPISchemaShape `json:"schema"`
	} `json:"content"`
}

// openAPIAgreementSuite is an independently reviewed expectation. It must not
// be generated from either the runtime manifest or the OpenAPI document.
type openAPIAgreementSuite struct {
	Operations []openAPIAgreementOperation
	Schemas    []openAPIAgreementSchema
	// OperationSelector is only for a combined runtime resource whose manifest
	// contains independently owned operation families. It may narrow candidates
	// but cannot add to or generate the independently declared Operations.
	OperationSelector func(method string, path string) bool
}

type openAPIAgreementOperation struct {
	Key            string
	Auth           AuthRequirement
	RequestBodyRef string
	RequestSchema  string
	SuccessStatus  string
	SuccessRef     string
	SuccessSchema  string
	// ExceptionalSuccess leaves binary or otherwise non-ordinary content
	// assertions beside the resource suite. Status and component-reference
	// agreement remain mandatory in the shared evaluator.
	ExceptionalSuccess bool
	PublicErrorCodes   []string
}

type openAPIAgreementSchema struct {
	Name     string
	DTO      reflect.Type
	Required []string
}

type openAPIAgreementViolation struct {
	Target string
	Check  string
	Detail string
}

func (violation openAPIAgreementViolation) String() string {
	return violation.Target + " " + violation.Check + ": " + violation.Detail
}

// assertOpenAPIAgreement is the only testing-aware part of the shared module.
// Configuration and document failures stop the suite immediately; independent
// contract disagreements are all reported in stable order.
func assertOpenAPIAgreement(t *testing.T, suite openAPIAgreementSuite, runtimeRoutes []Route) {
	t.Helper()
	encoded, err := readOpenAPIDocumentFile()
	if err != nil {
		t.Fatalf("read checked-in OpenAPI document: %v", err)
	}
	violations, err := evaluateOpenAPIAgreement(encoded, runtimeRoutes, suite)
	if err != nil {
		t.Fatalf("evaluate OpenAPI agreement: %v", err)
	}
	for _, violation := range violations {
		t.Errorf("%s", violation)
	}
}

func readOpenAPIDocument(t *testing.T) openAPIDocument {
	t.Helper()
	encoded, err := readOpenAPIDocumentFile()
	if err != nil {
		t.Fatalf("read checked-in OpenAPI document: %v", err)
	}
	var document openAPIDocument
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatalf("decode checked-in OpenAPI document: %v", err)
	}
	return document
}

func readOpenAPIDocumentFile() ([]byte, error) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		return nil, fmt.Errorf("locate agreement test")
	}
	documentPath := filepath.Join(filepath.Dir(currentFile), "..", "..", "openapi.json")
	return os.ReadFile(documentPath)
}

// evaluateOpenAPIAgreement is pure with respect to its inputs. In particular,
// it never sorts or otherwise rewrites caller-owned routes or expectations.
func evaluateOpenAPIAgreement(
	encoded []byte,
	runtimeRoutes []Route,
	suite openAPIAgreementSuite,
) ([]openAPIAgreementViolation, error) {
	expected, targetPaths, err := validateOpenAPIAgreementSuite(suite)
	if err != nil {
		return nil, err
	}

	var document openAPIDocument
	if err := json.Unmarshal(encoded, &document); err != nil {
		return nil, fmt.Errorf("decode document: %w", err)
	}
	if document.Paths == nil {
		return nil, fmt.Errorf("decode document: paths are missing")
	}

	runtimeOperations := make(map[string]Route, len(runtimeRoutes))
	for _, route := range runtimeRoutes {
		path := normalizeRuntimeRoutePath(route.Path)
		if suite.OperationSelector != nil && !suite.OperationSelector(route.Method, path) {
			continue
		}
		key := route.Method + " " + path
		if _, exists := runtimeOperations[key]; exists {
			return nil, fmt.Errorf("runtime selection is ambiguous for %q", key)
		}
		runtimeOperations[key] = route
	}

	documentedOperations := make(map[string]openAPIOperation, len(expected))
	for path, pathItem := range document.Paths {
		if _, selected := targetPaths[path]; !selected && suite.OperationSelector == nil {
			continue
		}
		for method, raw := range pathItem {
			upperMethod := strings.ToUpper(method)
			if !isHTTPMethod(upperMethod) {
				continue
			}
			if suite.OperationSelector != nil && !suite.OperationSelector(upperMethod, path) {
				continue
			}
			key := upperMethod + " " + path
			if _, exists := documentedOperations[key]; exists {
				return nil, fmt.Errorf("document selection is ambiguous for %q", key)
			}
			var operation openAPIOperation
			if err := json.Unmarshal(raw, &operation); err != nil {
				return nil, fmt.Errorf("decode document operation %s: %w", key, err)
			}
			documentedOperations[key] = operation
		}
	}

	violations := make([]openAPIAgreementViolation, 0)
	for key := range runtimeOperations {
		if _, exists := expected[key]; !exists {
			violations = appendAgreementViolation(violations, key, "operation set", "unexpected runtime operation")
		}
	}
	for key := range documentedOperations {
		if _, exists := expected[key]; !exists {
			violations = appendAgreementViolation(violations, key, "operation set", "unexpected documented operation")
		}
	}

	for key, contract := range expected {
		route, runtimeExists := runtimeOperations[key]
		if !runtimeExists {
			violations = appendAgreementViolation(violations, key, "operation set", "runtime operation is missing")
		}
		operation, documentExists := documentedOperations[key]
		if !documentExists {
			violations = appendAgreementViolation(violations, key, "operation set", "documented operation is missing")
		}
		if !runtimeExists || !documentExists {
			continue
		}

		if route.Auth != contract.Auth {
			violations = appendAgreementViolation(violations, key, "runtime auth", fmt.Sprintf("got %q, want %q", route.Auth, contract.Auth))
		}
		if operation.Auth != contract.Auth {
			violations = appendAgreementViolation(violations, key, "document auth", fmt.Sprintf("got %q, want %q", operation.Auth, contract.Auth))
		}
		wantSecurity, _ := securityForAuth(contract.Auth, contract.method())
		if !reflect.DeepEqual(operation.Security, wantSecurity) {
			violations = appendAgreementViolation(violations, key, "security", fmt.Sprintf("got %#v, want %#v", operation.Security, wantSecurity))
		}
		if operation.RequestBody.Ref != contract.RequestBodyRef {
			violations = appendAgreementViolation(violations, key, "request reference", fmt.Sprintf("got %q, want %q", operation.RequestBody.Ref, contract.RequestBodyRef))
		}
		response, successExists := operation.Responses[contract.SuccessStatus]
		if !successExists {
			violations = appendAgreementViolation(violations, key, "success status", fmt.Sprintf("%s response is missing", contract.SuccessStatus))
		} else if response.Ref != contract.SuccessRef {
			violations = appendAgreementViolation(violations, key, "success reference", fmt.Sprintf("got %q at %s, want %q", response.Ref, contract.SuccessStatus, contract.SuccessRef))
		}
		violations = evaluateOpenAPIRequestAgreement(violations, document, key, contract)
		violations = evaluateOpenAPISuccessAgreement(violations, document, key, contract)
		violations = compareStringSet(violations, key, "runtime error codes", route.ErrorCodes, contract.PublicErrorCodes)
		violations = compareStringSet(violations, key, "document error codes", operation.ErrorCodes, contract.PublicErrorCodes)
		violations = evaluateOpenAPIProblemAgreement(violations, document, key, operation)
	}

	for _, schema := range suite.Schemas {
		violations = evaluateOpenAPISchemaAgreement(violations, document, schema)
	}

	sort.Slice(violations, func(i, j int) bool {
		if violations[i].Target != violations[j].Target {
			return violations[i].Target < violations[j].Target
		}
		if violations[i].Check != violations[j].Check {
			return violations[i].Check < violations[j].Check
		}
		return violations[i].Detail < violations[j].Detail
	})
	return violations, nil
}

func validateOpenAPIAgreementSuite(
	suite openAPIAgreementSuite,
) (map[string]openAPIAgreementOperation, map[string]struct{}, error) {
	if len(suite.Operations) == 0 {
		return nil, nil, fmt.Errorf("suite declares no operations")
	}
	expected := make(map[string]openAPIAgreementOperation, len(suite.Operations))
	targetPaths := make(map[string]struct{}, len(suite.Operations))
	statuses := openAPIErrorStatuses()
	for index, contract := range suite.Operations {
		method, path, ok := strings.Cut(contract.Key, " ")
		if !ok || method == "" || path == "" || !isHTTPMethod(method) || method != strings.ToUpper(method) {
			return nil, nil, fmt.Errorf("operation %d has invalid key %q", index, contract.Key)
		}
		if normalizeRuntimeRoutePath(path) != path || !validOpenAPIPath(path) {
			return nil, nil, fmt.Errorf("operation %q has invalid normalized path", contract.Key)
		}
		if _, exists := expected[contract.Key]; exists {
			return nil, nil, fmt.Errorf("operation %q is declared more than once", contract.Key)
		}
		if _, err := securityForAuth(contract.Auth, method); err != nil {
			return nil, nil, fmt.Errorf("operation %q: %w", contract.Key, err)
		}
		if contract.SuccessStatus == "" || contract.SuccessRef == "" && !contract.ExceptionalSuccess {
			return nil, nil, fmt.Errorf("operation %q has no identifiable success response", contract.Key)
		}
		status, err := strconv.Atoi(contract.SuccessStatus)
		if err != nil || status < 100 || status > 599 {
			return nil, nil, fmt.Errorf("operation %q has invalid success status %q", contract.Key, contract.SuccessStatus)
		}
		if contract.SuccessRef != "" && !strings.HasPrefix(contract.SuccessRef, "#/components/responses/") {
			return nil, nil, fmt.Errorf("operation %q has invalid success reference %q", contract.Key, contract.SuccessRef)
		}
		if (contract.RequestBodyRef == "") != (contract.RequestSchema == "") {
			return nil, nil, fmt.Errorf("operation %q request expectation has no identifiable target", contract.Key)
		}
		if contract.RequestBodyRef != "" && !strings.HasPrefix(contract.RequestBodyRef, "#/components/requestBodies/") {
			return nil, nil, fmt.Errorf("operation %q has invalid request reference %q", contract.Key, contract.RequestBodyRef)
		}
		if contract.ExceptionalSuccess && contract.SuccessSchema != "" {
			return nil, nil, fmt.Errorf("operation %q exceptional success must not declare an ordinary schema", contract.Key)
		}
		if suite.OperationSelector != nil && !suite.OperationSelector(method, path) {
			return nil, nil, fmt.Errorf("operation selector excludes declared operation %q", contract.Key)
		}
		seenCodes := make(map[string]struct{}, len(contract.PublicErrorCodes))
		for _, code := range contract.PublicErrorCodes {
			if _, exists := seenCodes[code]; exists {
				return nil, nil, fmt.Errorf("operation %q declares public error %q more than once", contract.Key, code)
			}
			seenCodes[code] = struct{}{}
			if _, exists := statuses[code]; !exists {
				return nil, nil, fmt.Errorf("operation %q has unmapped public error %q", contract.Key, code)
			}
		}
		expected[contract.Key] = contract
		targetPaths[path] = struct{}{}
	}
	seenSchemas := make(map[string]struct{}, len(suite.Schemas))
	for index, schema := range suite.Schemas {
		if schema.Name == "" || schema.DTO == nil {
			return nil, nil, fmt.Errorf("schema %d has no identifiable OpenAPI target or DTO", index)
		}
		dto := schema.DTO
		for dto.Kind() == reflect.Pointer {
			dto = dto.Elem()
		}
		if dto.Kind() != reflect.Struct {
			return nil, nil, fmt.Errorf("schema %q DTO must be a struct, got %s", schema.Name, schema.DTO)
		}
		if _, exists := seenSchemas[schema.Name]; exists {
			return nil, nil, fmt.Errorf("schema %q is declared more than once", schema.Name)
		}
		seenSchemas[schema.Name] = struct{}{}
	}
	for key, contract := range expected {
		if contract.RequestSchema == "" {
			continue
		}
		if _, exists := seenSchemas[contract.RequestSchema]; !exists {
			return nil, nil, fmt.Errorf("operation %q request schema %q has no DTO agreement", key, contract.RequestSchema)
		}
	}
	return expected, targetPaths, nil
}

func (contract openAPIAgreementOperation) method() string {
	method, _, _ := strings.Cut(contract.Key, " ")
	return method
}

func normalizeRuntimeRoutePath(path string) string {
	var normalized strings.Builder
	for index := 0; index < len(path); {
		if path[index] != '{' {
			normalized.WriteByte(path[index])
			index++
			continue
		}
		start := index
		colon := strings.IndexByte(path[start:], ':')
		if colon < 0 {
			normalized.WriteString(path[start:])
			break
		}
		colon += start
		depth := 1
		end := colon + 1
		for ; end < len(path) && depth > 0; end++ {
			switch path[end] {
			case '{':
				depth++
			case '}':
				depth--
			}
		}
		if depth != 0 {
			normalized.WriteString(path[start:])
			break
		}
		normalized.WriteString(path[start:colon])
		normalized.WriteByte('}')
		index = end
	}
	return normalized.String()
}

func validOpenAPIPath(path string) bool {
	if !strings.HasPrefix(path, "/") || strings.ContainsAny(path, " \t\r\n") {
		return false
	}
	for index := 0; index < len(path); index++ {
		switch path[index] {
		case '{':
			end := strings.IndexByte(path[index+1:], '}')
			if end < 1 {
				return false
			}
			end += index + 1
			name := path[index+1 : end]
			if strings.ContainsAny(name, "{}:") {
				return false
			}
			index = end
		case '}':
			return false
		}
	}
	return true
}

func securityForAuth(auth AuthRequirement, method string) ([]map[string][]string, error) {
	switch auth {
	case AuthPublic:
		return []map[string][]string{}, nil
	case AuthRefreshCredentialRequired:
		return []map[string][]string{{"refreshBearerAuth": {}}, {"refreshCookie": {}, "csrfToken": {}}}, nil
	case AuthPrincipalRequired, AuthSessionRequired, AuthStrongSessionRequired,
		AuthRecentSessionRequired, AuthStrongRecentSessionRequired:
		security := []map[string][]string{{"bearerAuth": {}}}
		if method == http.MethodGet {
			return append(security, map[string][]string{"sessionCookie": {}}), nil
		}
		return append(security, map[string][]string{"sessionCookie": {}, "csrfToken": {}}), nil
	default:
		return nil, fmt.Errorf("authentication intent is missing or unknown: %q", auth)
	}
}

func evaluateOpenAPIRequestAgreement(
	violations []openAPIAgreementViolation,
	document openAPIDocument,
	key string,
	contract openAPIAgreementOperation,
) []openAPIAgreementViolation {
	if contract.RequestBodyRef == "" {
		return violations
	}
	name := strings.TrimPrefix(contract.RequestBodyRef, "#/components/requestBodies/")
	requestBody, exists := document.Components.RequestBodies[name]
	if !exists {
		return appendAgreementViolation(violations, key, "request schema", fmt.Sprintf("component %q is missing", name))
	}
	want := "#/components/schemas/" + contract.RequestSchema
	got := requestBody.Content["application/json"].Schema.Ref
	if got != want {
		violations = appendAgreementViolation(violations, key, "request schema", fmt.Sprintf("got %q, want %q", got, want))
	}
	return violations
}

func evaluateOpenAPISuccessAgreement(
	violations []openAPIAgreementViolation,
	document openAPIDocument,
	key string,
	contract openAPIAgreementOperation,
) []openAPIAgreementViolation {
	if contract.ExceptionalSuccess {
		return violations
	}
	name := strings.TrimPrefix(contract.SuccessRef, "#/components/responses/")
	response, exists := document.Components.Responses[name]
	if !exists {
		return appendAgreementViolation(violations, key, "success schema", fmt.Sprintf("component %q is missing", name))
	}
	if contract.SuccessSchema == "" {
		if len(response.Content) != 0 {
			violations = appendAgreementViolation(violations, key, "success schema", "no-content response declares content")
		}
		return violations
	}
	want := "#/components/schemas/" + contract.SuccessSchema
	got := response.Content["application/json"].Schema.Ref
	if got != want {
		violations = appendAgreementViolation(violations, key, "success schema", fmt.Sprintf("got %q, want %q", got, want))
	}
	return violations
}

func evaluateOpenAPIProblemAgreement(
	violations []openAPIAgreementViolation,
	document openAPIDocument,
	key string,
	operation openAPIOperation,
) []openAPIAgreementViolation {
	statuses := openAPIErrorStatuses()
	checked := make(map[int]struct{})
	for _, code := range operation.ErrorCodes {
		status, mapped := statuses[code]
		if !mapped {
			violations = appendAgreementViolation(violations, key, "error status", fmt.Sprintf("documented code %q is unmapped", code))
			continue
		}
		if _, exists := checked[status]; exists {
			continue
		}
		checked[status] = struct{}{}
		reference, exists := operation.Responses[strconv.Itoa(status)]
		if !exists || !strings.HasPrefix(reference.Ref, "#/components/responses/") {
			violations = appendAgreementViolation(violations, key, "Problem Details", fmt.Sprintf("%d response does not identify a component", status))
			continue
		}
		name := strings.TrimPrefix(reference.Ref, "#/components/responses/")
		response, exists := document.Components.Responses[name]
		if !exists {
			violations = appendAgreementViolation(violations, key, "Problem Details", fmt.Sprintf("%d response component %q is missing", status, name))
			continue
		}
		content, exists := response.Content["application/problem+json"]
		if !exists || content.Schema.Ref != "#/components/schemas/ProblemDetails" {
			violations = appendAgreementViolation(violations, key, "Problem Details", fmt.Sprintf("%d response does not use ProblemDetails", status))
		}
		if response.Headers["Cache-Control"].Ref != "#/components/headers/NoStore" {
			violations = appendAgreementViolation(violations, key, "Problem Details", fmt.Sprintf("%d response does not require no-store", status))
		}
	}
	return violations
}

func openAPIErrorStatuses() map[string]int {
	statuses := ApplicationErrorStatuses()
	statuses["authentication.credential_ambiguous"] = http.StatusBadRequest
	statuses["authentication.csrf.invalid"] = http.StatusForbidden
	statuses["not_live"] = http.StatusServiceUnavailable
	statuses["not_ready"] = http.StatusServiceUnavailable
	return statuses
}

func compareStringSet(
	violations []openAPIAgreementViolation,
	target string,
	check string,
	got []string,
	want []string,
) []openAPIAgreementViolation {
	gotCopy := append([]string(nil), got...)
	wantCopy := append([]string(nil), want...)
	sort.Strings(gotCopy)
	sort.Strings(wantCopy)
	if !reflect.DeepEqual(gotCopy, wantCopy) {
		violations = appendAgreementViolation(violations, target, check, fmt.Sprintf("got %v, want %v", gotCopy, wantCopy))
	}
	return violations
}

func appendAgreementViolation(
	violations []openAPIAgreementViolation,
	target string,
	check string,
	detail string,
) []openAPIAgreementViolation {
	return append(violations, openAPIAgreementViolation{Target: target, Check: check, Detail: detail})
}
