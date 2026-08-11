// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package architecture_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

func TestTransportCannotOwnApplicationAuthorizationCompatibility(t *testing.T) {
	t.Parallel()

	forbiddenTransportIdentifiers := map[string]struct{}{
		"DecisionReceipt":   {},
		"PermissionChecker": {},
		"requirePermission": {},
	}
	forbiddenApplicationFunctions := map[string]struct{}{
		"PrincipalHasPermissionToSystem":                        {},
		"principalHasPermissionToResourceForRequest":            {},
		"PrincipalHasPermissionToAcademicUnitForRequest":        {},
		"PrincipalHasPermissionToClassForRequest":               {},
		"PrincipalHasPermissionToProgrammeForRequest":           {},
		"PrincipalHasPermissionToProgrammeLevelForRequest":      {},
		"PrincipalHasPermissionToClassAdministrationForRequest": {},
		"PrincipalHasPermissionToAffiliationForRequest":         {},
		"PrincipalHasPermissionToAcademicUnitMemberForRequest":  {},
		"PrincipalHasPermissionToUserForRequest":                {},
	}
	inspectProductionGoFiles(t, []string{"app/api", "websocket"}, func(path string, file *ast.File) {
		ast.Inspect(file, func(node ast.Node) bool {
			switch candidate := node.(type) {
			case *ast.Ident:
				if _, forbidden := forbiddenTransportIdentifiers[candidate.Name]; forbidden {
					t.Errorf("transport authorization compatibility identifier %q remains in %s", candidate.Name, path)
				}
			case *ast.FuncDecl:
				if transportAuthorizationName(candidate.Name.Name) {
					t.Errorf("transport-owned authorization function %q remains in %s", candidate.Name.Name, path)
				}
			case *ast.Field:
				for _, name := range candidate.Names {
					if transportAuthorizationName(name.Name) {
						t.Errorf("transport-owned authorization contract %q remains in %s", name.Name, path)
					}
				}
			case *ast.SelectorExpr:
				if transportAuthorizationName(candidate.Sel.Name) {
					t.Errorf("transport-owned authorization call %q remains in %s", candidate.Sel.Name, path)
				}
			}
			return true
		})
	})

	inspectProductionGoFiles(t, []string{"app"}, func(path string, file *ast.File) {
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if _, forbidden := forbiddenApplicationFunctions[function.Name.Name]; forbidden {
				t.Errorf("request-shaped application permission compatibility %q remains in %s", function.Name.Name, path)
			}
		}
	})
}

func transportAuthorizationName(name string) bool {
	switch name {
	case "Can", "Authorize", "CheckAccess", "CheckPermission", "HasPermission", "requirePermission":
		return true
	default:
		return strings.HasPrefix(name, "PrincipalHasPermission") ||
			strings.HasPrefix(name, "AuthorizePrincipalTo")
	}
}

func inspectProductionGoFiles(
	t *testing.T,
	directories []string,
	inspect func(path string, file *ast.File),
) {
	t.Helper()
	for _, directory := range directories {
		root := filepath.Join("..", directory)
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			inspect(path, file)
			return nil
		})
		if err != nil {
			t.Fatalf("inspect production Go files under %s: %v", directory, err)
		}
	}
}
