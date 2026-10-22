// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package architecture_test

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUserSettingsApplicationBoundary keeps the portable User Settings
// Document independent of delivery adapters, deployment configuration, file
// storage, and examination state. HTTP translation remains in httpapi and the
// desktop-owned interpretation registry remains outside this repository.
func TestUserSettingsApplicationBoundary(t *testing.T) {
	t.Parallel()

	path := filepath.Join("..", "app", "user_settings.go")
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	require.NoError(t, err, "parse %s", path)

	allowed := map[string]struct{}{
		"context":                         {},
		"encoding/json":                   {},
		"errors":                          {},
		"time":                            {},
		serverModule + "/app/apperror":    {},
		serverModule + "/app/idempotency": {},
		serverModule + "/app/invocation":  {},
		serverModule + "/app/realtime":    {},
		serverModule + "/model":           {},
		serverModule + "/store":           {},
	}
	for _, spec := range parsed.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		require.NoError(t, err, "unquote import %s", spec.Path.Value)
		_, ok := allowed[importPath]
		assert.True(t, ok, "User Settings application imports %q outside its focused boundary", importPath)
	}
}
