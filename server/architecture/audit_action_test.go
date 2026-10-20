// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package architecture_test

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

// TestAuditActionsComeFromTheModelCatalog keeps audit action names declared
// once, as model.Action constants in the audit action catalog. Production code
// outside model may not spell an audit action as a string literal, convert a
// literal to model.Action, or declare its own model.Action constant; any
// audit event action written as a literal must therefore be both declared and
// catalogued before it can be emitted.
func TestAuditActionsComeFromTheModelCatalog(t *testing.T) {
	t.Parallel()

	declared := make(map[string]struct{})
	for _, action := range model.AuditActions() {
		declared[action] = struct{}{}
	}
	inspectProductionGoFiles(t, []string{"."}, func(path string, file *ast.File) {
		slashed := filepath.ToSlash(path)
		if strings.HasPrefix(slashed, "../model/") || strings.HasPrefix(slashed, "../store/storetest/") ||
			strings.HasPrefix(slashed, "../cmd/ptool/") {
			return
		}
		modelName := importName(file, "github.com/sudosylabs/proctor/server/model")
		ast.Inspect(file, func(node ast.Node) bool {
			switch candidate := node.(type) {
			case *ast.BasicLit:
				if value, ok := stringLiteral(candidate); ok {
					if _, isAction := declared[value]; isAction {
						t.Errorf("%s spells audit action %q as a literal; use its model.Action constant", path, value)
					}
				}
			case *ast.CallExpr:
				if modelName != "" && len(candidate.Args) == 1 && isModelAction(candidate.Fun, modelName) {
					if _, ok := candidate.Args[0].(*ast.BasicLit); ok {
						t.Errorf("%s converts a literal to model.Action; declare it in the model audit action catalog", path)
					}
				}
			case *ast.GenDecl:
				if candidate.Tok != token.CONST || modelName == "" {
					return true
				}
				for _, spec := range candidate.Specs {
					value, ok := spec.(*ast.ValueSpec)
					if ok && value.Type != nil && isModelAction(value.Type, modelName) {
						t.Errorf("%s declares model.Action constant %s outside model", path, value.Names[0].Name)
					}
				}
			case *ast.CompositeLit:
				if !isAuditEventType(candidate.Type, modelName) {
					return true
				}
				for _, element := range candidate.Elts {
					field, ok := element.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					if key, ok := field.Key.(*ast.Ident); ok && key.Name == "Action" {
						if _, literal := field.Value.(*ast.BasicLit); literal {
							t.Errorf("%s writes a literal AuditEvent.Action; use a model.Action constant", path)
						}
					}
				}
			}
			return true
		})
	})
}

func importName(file *ast.File, importPath string) string {
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || path != importPath {
			continue
		}
		if spec.Name != nil {
			return spec.Name.Name
		}
		return filepath.Base(importPath)
	}
	return ""
}

func stringLiteral(literal *ast.BasicLit) (string, bool) {
	if literal.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(literal.Value)
	return value, err == nil
}

func isModelAction(expression ast.Expr, modelName string) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Action" {
		return false
	}
	qualifier, ok := selector.X.(*ast.Ident)
	return ok && qualifier.Name == modelName
}

func isAuditEventType(expression ast.Expr, modelName string) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "AuditEvent" || modelName == "" {
		return false
	}
	qualifier, ok := selector.X.(*ast.Ident)
	return ok && qualifier.Name == modelName
}
