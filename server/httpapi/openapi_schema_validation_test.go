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
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/internal/openapidoc"
)

func TestOpenAPISchemaIsValid(t *testing.T) {
	t.Parallel()

	_, currentFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "locate schema validation test")
	serverDirectory := filepath.Join(filepath.Dir(currentFile), "..")
	compiled, err := openapidoc.Compile(os.DirFS(filepath.Join(serverDirectory, "openapi")))
	require.NoError(t, err)
	artifact, err := os.ReadFile(filepath.Join(serverDirectory, "openapi.json"))
	require.NoError(t, err)
	require.True(t, bytes.Equal(artifact, compiled), "openapi.json is stale; run `make -C server openapi-build`")
}

func openAPIDocumentPath(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "locate OpenAPI artifact")
	return filepath.Join(filepath.Dir(currentFile), "..", "openapi.json")
}
