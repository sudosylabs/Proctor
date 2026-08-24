// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/sudosylabs/proctor/server/internal/openapidoc"
)

func TestOpenAPISchemaIsValid(t *testing.T) {
	t.Parallel()

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate schema validation test")
	}
	serverDirectory := filepath.Join(filepath.Dir(currentFile), "..")
	compiled, err := openapidoc.Compile(os.DirFS(filepath.Join(serverDirectory, "openapi")))
	if err != nil {
		t.Fatalf("compile human-authored OpenAPI source: %v", err)
	}
	artifact, err := os.ReadFile(filepath.Join(serverDirectory, "openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(artifact, compiled) {
		t.Fatal("openapi.json is stale; run `make -C server openapi-build`")
	}
}

func openAPIDocumentPath(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate OpenAPI artifact")
	}
	return filepath.Join(filepath.Dir(currentFile), "..", "openapi.json")
}
