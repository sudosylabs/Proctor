// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package architecture_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestReceiverNamesAreShortAndConsistent keeps one receiver name per type, a
// one-to-three letter abbreviation of the type, in production and test code.
func TestReceiverNamesAreShortAndConsistent(t *testing.T) {
	t.Parallel()

	type receiverUse struct {
		names map[string][]string
	}
	byType := make(map[string]*receiverUse)
	inspectServerGoFiles(t, func(path string, file *ast.File) {
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv == nil || len(function.Recv.List) == 0 {
				continue
			}
			field := function.Recv.List[0]
			if len(field.Names) == 0 || field.Names[0].Name == "_" {
				continue
			}
			name := field.Names[0].Name
			location := path + ":" + function.Name.Name
			if len(name) > 3 || name == "self" || name == "this" {
				t.Errorf("%s uses receiver %q; use a one-to-three letter abbreviation of the type", location, name)
			}
			key := filepath.Dir(path) + "/" + file.Name.Name + "." + receiverTypeName(field.Type)
			use := byType[key]
			if use == nil {
				use = &receiverUse{names: make(map[string][]string)}
				byType[key] = use
			}
			use.names[name] = append(use.names[name], location)
		}
	})
	for key, use := range byType {
		if len(use.names) < 2 {
			continue
		}
		names := make([]string, 0, len(use.names))
		for name := range use.names {
			names = append(names, name)
		}
		sort.Strings(names)
		t.Errorf("%s mixes receiver names %v; use one name for every method", key, names)
	}
}

// idWord matches the initialism ID spelled Id inside a Go identifier, such as
// UserId, IdToken, or sessionIds.
var idWord = regexp.MustCompile(`Ids?([A-Z0-9_]|$)`)

// TestDeclaredIdentifiersSpellIDAsAnInitialism keeps the identifier initialism
// ID upper case in every declared Go name. JSON tags and SQL columns keep
// their snake_case spelling.
func TestDeclaredIdentifiersSpellIDAsAnInitialism(t *testing.T) {
	t.Parallel()

	inspectServerGoFiles(t, func(path string, file *ast.File) {
		check := func(identifier *ast.Ident) {
			if identifier != nil && idWord.MatchString(identifier.Name) {
				t.Errorf("%s declares %q; spell the initialism ID", path, identifier.Name)
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch declared := node.(type) {
			case *ast.FuncDecl:
				check(declared.Name)
			case *ast.TypeSpec:
				check(declared.Name)
			case *ast.ValueSpec:
				for _, name := range declared.Names {
					check(name)
				}
			case *ast.Field:
				for _, name := range declared.Names {
					check(name)
				}
			case *ast.AssignStmt:
				if declared.Tok == token.DEFINE {
					for _, expression := range declared.Lhs {
						if name, ok := expression.(*ast.Ident); ok {
							check(name)
						}
					}
				}
			case *ast.RangeStmt:
				if declared.Tok == token.DEFINE {
					for _, expression := range []ast.Expr{declared.Key, declared.Value} {
						if name, ok := expression.(*ast.Ident); ok {
							check(name)
						}
					}
				}
			}
			return true
		})
	})
}

// TestSQLStoreAdvisoryLocksUseOneHelper keeps named advisory locks on the
// sqlstore helpers so every key shares one hash function and prefix.
func TestSQLStoreAdvisoryLocksUseOneHelper(t *testing.T) {
	t.Parallel()

	inspectProductionGoFiles(t, []string{"store/sqlstore"}, func(path string, file *ast.File) {
		if filepath.Base(path) == "advisory_lock.go" {
			return
		}
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok {
				return true
			}
			if value, isString := stringLiteral(literal); isString && strings.Contains(value, "pg_advisory") {
				t.Errorf("%s spells an advisory lock query; use lockAdvisory, lockAdvisoryShared, or lockCommandIdempotency", path)
			}
			return true
		})
	})
}

func receiverTypeName(expression ast.Expr) string {
	switch typed := expression.(type) {
	case *ast.StarExpr:
		return receiverTypeName(typed.X)
	case *ast.ParenExpr:
		return receiverTypeName(typed.X)
	case *ast.IndexExpr:
		return receiverTypeName(typed.X)
	case *ast.IndexListExpr:
		return receiverTypeName(typed.X)
	case *ast.Ident:
		return typed.Name
	}
	return ""
}

// inspectServerGoFiles parses every server Go file, including tests and every
// build-tag variant.
func inspectServerGoFiles(t *testing.T, inspect func(path string, file *ast.File)) {
	t.Helper()
	err := filepath.WalkDir("..", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != ".." && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "testdata" || entry.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		inspect(path, file)
		return nil
	})
	if err != nil {
		t.Fatalf("inspect server Go files: %v", err)
	}
}
