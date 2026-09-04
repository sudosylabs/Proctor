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
)

// TestUserSettingsApplicationBoundary keeps the portable User Settings
// Document independent of delivery adapters, deployment configuration, file
// storage, and examination state. HTTP translation remains in httpapi and the
// desktop-owned interpretation registry remains outside this repository.
func TestUserSettingsApplicationBoundary(t *testing.T) {
	t.Parallel()

	path := filepath.Join("..", "app", "user_settings.go")
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	allowed := map[string]struct{}{
		"context":                      {},
		"encoding/json":                {},
		"errors":                       {},
		"time":                         {},
		serverModule + "/app/realtime": {},
		serverModule + "/model":        {},
		serverModule + "/store":        {},
	}
	for _, spec := range parsed.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			t.Fatalf("unquote import %s: %v", spec.Path.Value, err)
		}
		if _, ok := allowed[importPath]; !ok {
			t.Errorf("User Settings application imports %q outside its focused boundary", importPath)
		}
	}
}
