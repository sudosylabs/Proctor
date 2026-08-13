// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestSQLStoreTransactionLifecycleHasOneOwner(t *testing.T) {
	t.Parallel()

	files := sqlstoreProductionSyntax(t)
	allowed := map[string]bool{
		"sqlx_wrapper.go":          true,
		"transaction_execution.go": true,
	}
	for name, file := range files {
		if allowed[name] {
			continue
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch selector.Sel.Name {
			case "Begin", "Commit", "Rollback":
				t.Errorf("%s contains direct transaction lifecycle call %s; use runSQLTransaction", name, selector.Sel.Name)
			}
			return true
		})
	}
}

func TestSQLStoreRowProjectionsCanReportCorruption(t *testing.T) {
	t.Parallel()

	for name, file := range sqlstoreProductionSyntax(t) {
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv == nil || function.Name.Name != "model" || len(function.Recv.List) != 1 {
				continue
			}
			receiver := receiverTypeName(function.Recv.List[0].Type)
			if !strings.HasSuffix(receiver, "Row") {
				continue
			}
			if receiver == "clusterDiscoveryNodeRow" {
				// Cluster discovery node IDs are bounded adapter identifiers, not
				// domain entity IDs. ListLive validates this Store contract after
				// projection, so typed-ID rehydration is deliberately inapplicable.
				continue
			}
			if function.Type.Results == nil || function.Type.Results.NumFields() != 2 {
				t.Errorf("%s: %s.model must return (value, error) so malformed persisted state fails closed", name, receiver)
				continue
			}
			second := function.Type.Results.List[1].Type
			identifier, ok := second.(*ast.Ident)
			if !ok || identifier.Name != "error" {
				t.Errorf("%s: %s.model second result must be error", name, receiver)
			}
		}
	}
}

func TestSQLStoreUsesSinglePreReleaseBaseline(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir("../../migrations/postgres")
	if err != nil {
		t.Fatal(err)
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			files = append(files, entry.Name())
		}
	}
	want := []string{"000001_baseline.down.sql", "000001_baseline.up.sql"}
	if !slices.Equal(files, want) {
		t.Errorf("pre-release migration files = %v; want the single rewritable baseline %v", files, want)
	}
}

func sqlstoreProductionSyntax(t *testing.T) map[string]*ast.File {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	files := make(map[string]*ast.File)
	set := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(set, filepath.Clean(name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files[name] = file
	}
	return files
}

func receiverTypeName(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.StarExpr:
		return receiverTypeName(value.X)
	default:
		return ""
	}
}
