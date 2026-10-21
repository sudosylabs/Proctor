// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package memberlist

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiscoveryPolicyRemainsInPrivateMaintenanceModule(t *testing.T) {
	t.Parallel()

	packageDirectory := memberlistPackageDirectory(t)
	files, err := filepath.Glob(filepath.Join(packageDirectory, "*.go"))
	require.NoError(t, err)
	legacyTransportMethods := map[string]struct{}{
		"advertiseDiscovery": {},
		"collectSeeds":       {},
		"maintainDiscovery":  {},
	}
	transportStoreCalls := map[string]struct{}{
		"Upsert":        {},
		"ListLive":      {},
		"Delete":        {},
		"DeleteExpired": {},
	}

	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		require.NoError(t, err, "parse %s", path)
		filename := filepath.Base(path)
		ast.Inspect(parsed, func(node ast.Node) bool {
			switch typed := node.(type) {
			case *ast.FuncDecl:
				if filename == "transport.go" {
					_, forbidden := legacyTransportMethods[typed.Name.Name]
					assert.False(t, forbidden, "transport.go restored legacy discovery method %s", typed.Name.Name)
				}
			case *ast.SelectorExpr:
				if filename != "discovery_maintenance.go" {
					identifier, ok := typed.X.(*ast.Ident)
					if ok && identifier.Name == "time" {
						assert.NotEqual(t, "Now", typed.Sel.Name, "%s constructs discovery time or schedule outside the private maintenance module", filename)
						assert.NotEqual(t, "NewTicker", typed.Sel.Name, "%s constructs discovery time or schedule outside the private maintenance module", filename)
					}
				}
				if filename == "transport.go" {
					_, forbidden := transportStoreCalls[typed.Sel.Name]
					assert.False(t, forbidden, "transport.go directly calls discovery store method %s", typed.Sel.Name)
				}
			}
			return true
		})
	}
}

func TestMemberlistProductionDoesNotImportSQLDrivers(t *testing.T) {
	t.Parallel()

	packageDirectory := memberlistPackageDirectory(t)
	files, err := filepath.Glob(filepath.Join(packageDirectory, "*.go"))
	require.NoError(t, err)
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		require.NoError(t, err, "parse %s", path)
		for _, imported := range parsed.Imports {
			importPath, err := strconv.Unquote(imported.Path.Value)
			require.NoError(t, err, "unquote import %s", imported.Path.Value)
			lower := strings.ToLower(importPath)
			assert.NotEqual(t, "database/sql", importPath)
			assert.NotContains(t, lower, "sqlx")
			assert.NotContains(t, lower, "postgres")
			assert.NotContains(t, lower, "pgx")
			assert.NotContains(t, lower, "mysql")
			assert.NotContains(t, lower, "sqlite")
		}
	}
}

func memberlistPackageDirectory(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "resolve Memberlist package directory")
	return filepath.Dir(currentFile)
}
