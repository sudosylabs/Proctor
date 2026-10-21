// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
				assert.Fail(t, fmt.Sprintf("%s contains direct transaction lifecycle call %s; use runSQLTransaction", name, selector.Sel.Name))
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
			returnsPair := function.Type.Results != nil && function.Type.Results.NumFields() == 2
			if !assert.True(t, returnsPair, "%s: %s.model must return (value, error) so malformed persisted state fails closed", name, receiver) {
				continue
			}
			second := function.Type.Results.List[1].Type
			identifier, ok := second.(*ast.Ident)
			if assert.True(t, ok, "%s: %s.model second result must be error", name, receiver) {
				assert.Equal(t, "error", identifier.Name, "%s: %s.model second result must be error", name, receiver)
			}
		}
	}
}

func TestSQLStoreUsesSinglePreReleaseBaseline(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir("../../migrations/postgres")
	require.NoError(t, err)
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			files = append(files, entry.Name())
		}
	}
	want := []string{"000001_baseline.down.sql", "000001_baseline.up.sql"}
	assert.True(t, slices.Equal(files, want), "pre-release migration files = %v; want the single rewritable baseline %v", files, want)
}

func TestDirectorySearchIndexesCoverEverySearchableCatalog(t *testing.T) {
	t.Parallel()

	baseline, err := os.ReadFile("../../migrations/postgres/000001_baseline.up.sql")
	require.NoError(t, err)
	source := string(baseline)
	for _, required := range []string{
		"CREATE EXTENSION IF NOT EXISTS pg_trgm;",
		"academic_units_directory_search_idx",
		"programmes_directory_search_idx",
		"programme_levels_directory_search_idx",
		"academic_periods_directory_search_idx",
		"classes_directory_search_idx",
		"users_directory_search_idx",
		"exam_drafts_title_search_idx",
	} {
		assert.Contains(t, source, required)
	}
}

func sqlstoreProductionSyntax(t *testing.T) map[string]*ast.File {
	t.Helper()
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	files := make(map[string]*ast.File)
	set := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(set, filepath.Clean(name), nil, 0)
		require.NoError(t, err, "parse %s", name)
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
