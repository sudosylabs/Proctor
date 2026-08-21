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
		"PrincipalHasPermissionTo":                              {},
		"PrincipalHasPermissionToInstitution":                   {},
		"PrincipalHasPermissionToAcademicUnit":                  {},
		"PrincipalHasPermissionToClass":                         {},
		"PrincipalHasPermissionToUser":                          {},
		"AuthorizePrincipalTo":                                  {},
		"AuthorizePrincipalToInstitution":                       {},
		"AuthorizePrincipalToAcademicUnit":                      {},
		"AuthorizePrincipalToClass":                             {},
		"AuthorizePrincipalToUser":                              {},
		"UserCanSeeOtherUser":                                   {},
		"GetUserForPrincipal":                                   {},
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
	inspectProductionGoFiles(t, []string{"httpapi", "websocket"}, func(path string, file *ast.File) {
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

func TestApplicationFacadeDoesNotRetainPersistenceLocator(t *testing.T) {
	t.Parallel()

	inspectProductionGoFiles(t, []string{"app"}, func(path string, file *ast.File) {
		ast.Inspect(file, func(node ast.Node) bool {
			switch candidate := node.(type) {
			case *ast.TypeSpec:
				if candidate.Name.Name != "App" {
					break
				}
				structure, ok := candidate.Type.(*ast.StructType)
				if !ok {
					break
				}
				for _, field := range structure.Fields.List {
					selector, ok := field.Type.(*ast.SelectorExpr)
					if ok && selector.Sel.Name == "Store" {
						t.Errorf("application retains root Store field in %s", path)
					}
				}
			case *ast.FuncDecl:
				receiverNames, appReceiver := appReceiverNames(candidate)
				if !appReceiver {
					break
				}
				if candidate.Name.Name == "Store" {
					t.Errorf("application Store accessor remains in %s", path)
				}
				ast.Inspect(candidate.Body, func(bodyNode ast.Node) bool {
					call, ok := bodyNode.(*ast.CallExpr)
					if !ok {
						return true
					}
					selector, ok := call.Fun.(*ast.SelectorExpr)
					receiver, receiverOK := selectorReceiver(selector)
					if ok && receiverOK && selector.Sel.Name == "Store" && receiverNames[receiver] {
						t.Errorf("production application Store traversal remains in %s", path)
					}
					return true
				})
			}
			return true
		})
	})
}

func TestApplicationDoesNotExportFocusedServiceImplementations(t *testing.T) {
	t.Parallel()

	retiredImplementationTypes := map[string]struct{}{
		"AuditService":    {},
		"RealtimeService": {},
	}
	inspectProductionGoFiles(t, []string{"app"}, func(path string, file *ast.File) {
		for _, declaration := range file.Decls {
			generic, ok := declaration.(*ast.GenDecl)
			if !ok || generic.Tok != token.TYPE {
				continue
			}
			for _, specification := range generic.Specs {
				typeSpec, ok := specification.(*ast.TypeSpec)
				if !ok {
					continue
				}
				if _, retired := retiredImplementationTypes[typeSpec.Name.Name]; retired {
					t.Errorf("retired exported application implementation %q remains in %s", typeSpec.Name.Name, path)
				}
			}
		}
	})
}

func appReceiverNames(function *ast.FuncDecl) (map[string]bool, bool) {
	names := map[string]bool{}
	if function == nil || function.Recv == nil || len(function.Recv.List) != 1 {
		return names, false
	}
	receiver := function.Recv.List[0]
	receiverType := receiver.Type
	if pointer, ok := receiverType.(*ast.StarExpr); ok {
		receiverType = pointer.X
	}
	identifier, ok := receiverType.(*ast.Ident)
	if !ok || identifier.Name != "App" {
		return names, false
	}
	for _, name := range receiver.Names {
		names[name.Name] = true
	}
	return names, true
}

func selectorReceiver(selector *ast.SelectorExpr) (string, bool) {
	if selector == nil {
		return "", false
	}
	receiver, ok := selector.X.(*ast.Ident)
	if !ok {
		return "", false
	}
	return receiver.Name, true
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
