// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
)

func TestResourceOpenAPIAgreementsUseSharedEvaluator(t *testing.T) {
	t.Parallel()

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate agreement contraction test")
	}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(currentFile), "*_openapi_agreement_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	if len(files) == 0 {
		t.Fatal("no resource OpenAPI agreement suites found")
	}
	forbidden := [][]byte{
		[]byte("ApplicationErrorStatuses("),
		[]byte("canonicalIDRoutePattern("),
		[]byte("assertOpenAPIRequestBody("),
		[]byte("assertOpenAPISuccessResponse("),
		[]byte("assertOpenAPIProblemResponse("),
		[]byte("runtimeOperations :="),
		[]byte("documentedOperations :="),
		[]byte("strings.ReplaceAll("),
	}
	for _, path := range files {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Parallel()
			source, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(source, []byte("assertOpenAPIAgreement(")) {
				t.Error("resource suite does not use the shared agreement evaluator")
			}
			for _, token := range forbidden {
				if bytes.Contains(source, token) {
					t.Errorf("resource suite restores superseded agreement orchestration %q", token)
				}
			}
		})
	}
}
