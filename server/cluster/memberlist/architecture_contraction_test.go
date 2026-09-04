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
)

func TestDiscoveryPolicyRemainsInPrivateMaintenanceModule(t *testing.T) {
	t.Parallel()

	packageDirectory := memberlistPackageDirectory(t)
	files, err := filepath.Glob(filepath.Join(packageDirectory, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
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
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		filename := filepath.Base(path)
		ast.Inspect(parsed, func(node ast.Node) bool {
			switch typed := node.(type) {
			case *ast.FuncDecl:
				if filename == "transport.go" {
					if _, forbidden := legacyTransportMethods[typed.Name.Name]; forbidden {
						t.Errorf("transport.go restored legacy discovery method %s", typed.Name.Name)
					}
				}
			case *ast.SelectorExpr:
				if filename != "discovery_maintenance.go" {
					if identifier, ok := typed.X.(*ast.Ident); ok && identifier.Name == "time" &&
						(typed.Sel.Name == "Now" || typed.Sel.Name == "NewTicker") {
						t.Errorf("%s constructs discovery time or schedule outside the private maintenance module", filename)
					}
				}
				if filename == "transport.go" {
					if _, forbidden := transportStoreCalls[typed.Sel.Name]; forbidden {
						t.Errorf("transport.go directly calls discovery store method %s", typed.Sel.Name)
					}
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
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, imported := range parsed.Imports {
			importPath, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				t.Fatalf("unquote import %s: %v", imported.Path.Value, err)
			}
			lower := strings.ToLower(importPath)
			if importPath == "database/sql" ||
				strings.Contains(lower, "sqlx") ||
				strings.Contains(lower, "postgres") ||
				strings.Contains(lower, "pgx") ||
				strings.Contains(lower, "mysql") ||
				strings.Contains(lower, "sqlite") {
				t.Errorf("%s imports concrete persistence dependency %q", filepath.Base(path), importPath)
			}
		}
	}
}

func memberlistPackageDirectory(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve Memberlist package directory")
	}
	return filepath.Dir(currentFile)
}
