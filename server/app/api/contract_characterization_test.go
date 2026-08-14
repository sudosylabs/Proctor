// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

// TestV1HTTPContractCharacterization makes any change to the reviewed v1
// operations, their referenced request/response components, or the compiled
// runtime manifest an explicit compatibility decision. The checked-in OpenAPI
// document is the public source of truth for success statuses, headers, DTOs,
// and public errors; the runtime projection proves the production catalog
// still exposes the corresponding method, path, authentication, and migrated
// error metadata.
func TestV1HTTPContractCharacterization(t *testing.T) {
	t.Parallel()

	documentBytes := readOpenAPIDocumentBytes(t)
	var document map[string]json.RawMessage
	if err := json.Unmarshal(documentBytes, &document); err != nil {
		t.Fatal(err)
	}
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.collectResources(model.APIURLSuffix, productionResources(Options{}, browserCookies{}, nil)...); err != nil {
		t.Fatal(err)
	}
	runtimeRoutes, err := json.Marshal(runtimeAPI.Routes())
	if err != nil {
		t.Fatal(err)
	}
	projection := map[string]json.RawMessage{
		"openapi":        document["openapi"],
		"paths":          document["paths"],
		"components":     document["components"],
		"runtime_routes": runtimeRoutes,
	}
	canonical, err := json.Marshal(projection)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(canonical)
	gotDigest := hex.EncodeToString(digest[:])
	const wantDigest = "f258f404cf4b56aa3114174830d668ab97b9123f8acc7d176befaadd93b63680"
	if gotDigest != wantDigest {
		t.Fatalf("v1 HTTP contract digest = %s, want %s", gotDigest, wantDigest)
	}

	var parsed struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(documentBytes, &parsed); err != nil {
		t.Fatal(err)
	}
	operationCount := 0
	for _, item := range parsed.Paths {
		for method := range item {
			if isHTTPMethod(strings.ToUpper(method)) {
				operationCount++
			}
		}
	}
	if operationCount != 122 {
		t.Fatalf("v1 operation count = %d, want 122", operationCount)
	}
	if len(runtimeAPI.Routes()) != operationCount {
		t.Fatalf("runtime route count = %d, want %d", len(runtimeAPI.Routes()), operationCount)
	}
}

func readOpenAPIDocumentBytes(t *testing.T) []byte {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate contract characterization test")
	}
	path := filepath.Join(filepath.Dir(currentFile), "..", "..", "openapi.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
