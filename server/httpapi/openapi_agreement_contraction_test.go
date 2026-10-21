// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResourceOpenAPIAgreementsUseSharedEvaluator(t *testing.T) {
	t.Parallel()

	_, currentFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "locate agreement contraction test")
	files, err := filepath.Glob(filepath.Join(filepath.Dir(currentFile), "*_openapi_agreement_test.go"))
	require.NoError(t, err)
	sort.Strings(files)
	require.NotEmpty(t, files, "no resource OpenAPI agreement suites found")
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
			require.NoError(t, err)
			assert.True(t, bytes.Contains(source, []byte("assertOpenAPIAgreement(")), "resource suite does not use the shared agreement evaluator")
			for _, token := range forbidden {
				assert.False(t, bytes.Contains(source, token), "resource suite restores superseded agreement orchestration %q", token)
			}
		})
	}
}
