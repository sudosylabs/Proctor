// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestOpenAPISchemaIsValid(t *testing.T) {
	t.Parallel()

	documentPath := openAPIDocumentPath(t)
	validateOpenAPIFile(t, documentPath)

	encoded, err := os.ReadFile(documentPath)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{
			"missing operation ID",
			func(candidate map[string]any) {
				operationAt(candidate, "/health/live", "get")["operationId"] = ""
			},
		},
		{
			"unresolved component reference",
			func(candidate map[string]any) {
				operationAt(candidate, "/health/live", "get")["responses"] = map[string]any{
					"200": map[string]any{"$ref": "#/components/responses/Missing"},
				}
			},
		},
		{
			"missing path parameter",
			func(candidate map[string]any) {
				delete(objectAt(candidate, "paths", "/api/v1/roles/{role_id}"), "parameters")
			},
		},
		{
			"malformed required properties",
			func(candidate map[string]any) {
				objectAt(candidate, "components", "schemas", "HealthResponse")["required"] = "status"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := cloneOpenAPIDocument(t, document)
			test.mutate(candidate)
			candidatePath := filepath.Join(t.TempDir(), "openapi.json")
			candidateJSON, err := json.Marshal(candidate)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(candidatePath, candidateJSON, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := loadAndValidateOpenAPI(candidatePath); err == nil {
				t.Fatal("schema drift was accepted")
			}
		})
	}
}

func validateOpenAPIFile(t *testing.T, path string) {
	t.Helper()
	if err := loadAndValidateOpenAPI(path); err != nil {
		t.Fatalf("checked-in OpenAPI document is invalid: %v", err)
	}
}

func loadAndValidateOpenAPI(path string) error {
	document, err := openapi3.NewLoader().LoadFromFile(path)
	if err != nil {
		return err
	}
	if err := document.Validate(context.Background()); err != nil {
		return err
	}
	operationIDs := make(map[string]string)
	for path, pathItem := range document.Paths.Map() {
		for method, operation := range pathItem.Operations() {
			location := method + " " + path
			if operation.OperationID == "" {
				return fmt.Errorf("%s operationId is required", location)
			}
			if prior, exists := operationIDs[operation.OperationID]; exists {
				return fmt.Errorf("operationId %q is shared by %s and %s", operation.OperationID, prior, location)
			}
			operationIDs[operation.OperationID] = location
			if operation.Security == nil {
				return fmt.Errorf("%s security is required", location)
			}
			if _, ok := operation.Extensions["x-proctor-auth"].(string); !ok {
				return fmt.Errorf("%s x-proctor-auth is required", location)
			}
			if _, ok := operation.Extensions["x-proctor-error-codes"].([]any); !ok {
				return fmt.Errorf("%s x-proctor-error-codes must be an array", location)
			}
		}
	}
	return nil
}

func openAPIDocumentPath(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate schema validation test")
	}
	return filepath.Join(filepath.Dir(currentFile), "..", "..", "openapi.json")
}

func cloneOpenAPIDocument(t *testing.T, document map[string]any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var cloned map[string]any
	if err := json.Unmarshal(encoded, &cloned); err != nil {
		t.Fatal(err)
	}
	return cloned
}

func objectAt(root map[string]any, path ...string) map[string]any {
	var current any = root
	for _, key := range path {
		current = current.(map[string]any)[key]
	}
	return current.(map[string]any)
}

func operationAt(root map[string]any, path, method string) map[string]any {
	return objectAt(root, "paths", path, method)
}
