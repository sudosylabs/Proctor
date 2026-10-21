// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package commands

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// storeConstraintSources are the module directories whose production Go
// string literals may name a Store conflict constraint, and the SQL baseline
// whose constraint and index names translateError reports verbatim.
var (
	storeConstraintSources = []string{"store", "model"}
	storeConstraintSchema  = filepath.Join("migrations", "postgres")
	sqlIdentifier          = regexp.MustCompile(`[a-z][a-z0-9_]*`)
)

// verifyStoreTableConstraints fails when an apperror.StoreTable in the
// application maps a constraint that nothing in the Store can produce: no
// production string literal under store/ or model/ spells it and no SQL
// baseline object carries that name. Such an entry is dead, and a dead entry
// usually means the Store mints the condition under another spelling that
// then falls back to the table's generic code.
func verifyStoreTableConstraints(moduleDir string) error {
	produced, err := producedStoreConstraints(moduleDir)
	if err != nil {
		return err
	}
	var dead []string
	fset := token.NewFileSet()
	err = walkProductionGo(filepath.Join(moduleDir, "app"), func(path string) error {
		file, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			return parseErr
		}
		for _, constraint := range storeTableConstraintKeys(file) {
			if !produced[constraint.name] {
				dead = append(dead, fmt.Sprintf("%s: StoreTable maps constraint %q that the Store never produces",
					relativeSourcePath(fset.Position(constraint.pos).String()), constraint.name))
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(dead) > 0 {
		sort.Strings(dead)
		return errors.New(strings.Join(dead, "\n"))
	}
	return nil
}

type storeTableConstraint struct {
	name string
	pos  token.Pos
}

// storeTableConstraintKeys returns the literal keys of every
// apperror.StoreTable{Constraints: map[...]...{...}} in file.
func storeTableConstraintKeys(file *ast.File) []storeTableConstraint {
	var keys []storeTableConstraint
	ast.Inspect(file, func(node ast.Node) bool {
		table, ok := node.(*ast.CompositeLit)
		if !ok || !isStoreTableType(table.Type) {
			return true
		}
		for _, element := range table.Elts {
			field, ok := element.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			if name, isIdent := field.Key.(*ast.Ident); !isIdent || name.Name != "Constraints" {
				continue
			}
			constraints, ok := field.Value.(*ast.CompositeLit)
			if !ok {
				continue
			}
			for _, entry := range constraints.Elts {
				pair, ok := entry.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if literal, ok := pair.Key.(*ast.BasicLit); ok && literal.Kind == token.STRING {
					if value, err := strconv.Unquote(literal.Value); err == nil {
						keys = append(keys, storeTableConstraint{name: value, pos: literal.Pos()})
					}
				}
			}
		}
		return true
	})
	return keys
}

func isStoreTableType(expression ast.Expr) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "StoreTable" {
		return false
	}
	pkg, ok := selector.X.(*ast.Ident)
	return ok && pkg.Name == "apperror"
}

// producedStoreConstraints collects every name the Store can report as a
// constraint: production string literals and SQL baseline identifiers.
func producedStoreConstraints(moduleDir string) (map[string]bool, error) {
	produced := make(map[string]bool)
	fset := token.NewFileSet()
	for _, directory := range storeConstraintSources {
		err := walkProductionGo(filepath.Join(moduleDir, directory), func(path string) error {
			file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			ast.Inspect(file, func(node ast.Node) bool {
				if literal, ok := node.(*ast.BasicLit); ok && literal.Kind == token.STRING {
					if value, unquoteErr := strconv.Unquote(literal.Value); unquoteErr == nil {
						produced[value] = true
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	schemas, err := filepath.Glob(filepath.Join(moduleDir, storeConstraintSchema, "*.up.sql"))
	if err != nil {
		return nil, err
	}
	for _, schema := range schemas {
		source, readErr := os.ReadFile(filepath.Clean(schema))
		if readErr != nil {
			return nil, fmt.Errorf("read SQL schema %s: %w", schema, readErr)
		}
		for _, identifier := range sqlIdentifier.FindAllString(string(source), -1) {
			produced[identifier] = true
		}
	}
	return produced, nil
}

// walkProductionGo calls visit for every non-test Go file below root; a
// missing root has none.
func walkProductionGo(root string, visit func(string) error) error {
	if _, err := os.Stat(root); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		return visit(path)
	})
}
