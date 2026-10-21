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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlatformDoesNotExposeInfrastructureLookup(t *testing.T) {
	t.Parallel()

	allowed := map[string]bool{
		"Start": true, "CheckDependencies": true, "ReloadConfiguration": true, "Close": true,
	}
	root := filepath.Join("..", "platform")
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv == nil || len(function.Recv.List) != 1 || !ast.IsExported(function.Name.Name) {
				continue
			}
			receiver := function.Recv.List[0].Type
			if pointer, ok := receiver.(*ast.StarExpr); ok {
				receiver = pointer.X
			}
			identifier, ok := receiver.(*ast.Ident)
			if ok && identifier.Name == "Service" {
				assert.True(t, allowed[function.Name.Name], "platform Service exposes non-lifecycle method %q in %s", function.Name.Name, path)
			}
		}
		return nil
	})
	require.NoError(t, err)
}

func TestConstructionProjectionRemainsRootPrivate(t *testing.T) {
	t.Parallel()

	allowedFunctions := map[string]bool{
		"acceptPlatform":              true,
		"applicationDependencies":     true,
		"composeConsumers":            true,
		"composeNode":                 true,
		"defaultConsumerConstructors": true,
	}
	allowedTypes := map[string]bool{
		"constructionCapabilities": true,
		"consumerConstructors":     true,
	}
	root := filepath.Join("..")
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || filepath.Base(path) == "runtime_ownership_test.go" || filepath.Base(path) == "composition_test.go" {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		for _, declaration := range file.Decls {
			if generic, ok := declaration.(*ast.GenDecl); ok {
				allowedDeclaration := false
				for _, specification := range generic.Specs {
					typeSpec, ok := specification.(*ast.TypeSpec)
					if ok && allowedTypes[typeSpec.Name.Name] {
						allowedDeclaration = true
					}
				}
				if allowedDeclaration && (filepath.Base(path) == "infrastructure.go" || filepath.Base(path) == "composition.go") {
					continue
				}
			}
			function, ok := declaration.(*ast.FuncDecl)
			if ok && allowedFunctions[function.Name.Name] {
				continue
			}
			ast.Inspect(declaration, func(node ast.Node) bool {
				identifier, ok := node.(*ast.Ident)
				if ok {
					assert.NotEqual(t, "constructionCapabilities", identifier.Name, "construction projection escaped into %s outside its reviewed construction seams", path)
				}
				return true
			})
		}
		return nil
	})
	require.NoError(t, err)
}

func TestConstructionProjectionOmitsLifecycleAuthority(t *testing.T) {
	t.Parallel()

	path := filepath.Join("..", "infrastructure.go")
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	require.NoError(t, err)
	forbidden := map[string]bool{"Close": true, "Shutdown": true, "Start": true, "Stop": true, "Ping": true}
	checked := map[string]bool{}
	for _, declaration := range file.Decls {
		generic, ok := declaration.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, specification := range generic.Specs {
			typeSpec, ok := specification.(*ast.TypeSpec)
			if !ok || (typeSpec.Name.Name != "borrowedCache" && typeSpec.Name.Name != "borrowedMailer" && typeSpec.Name.Name != "borrowedCluster" && typeSpec.Name.Name != "runtimeLogger") {
				continue
			}
			checked[typeSpec.Name.Name] = true
			contract, ok := typeSpec.Type.(*ast.InterfaceType)
			if !ok {
				continue
			}
			for _, method := range contract.Methods.List {
				for _, name := range method.Names {
					assert.False(t, forbidden[name.Name], "borrowed capability %s exposes lifecycle method %s", typeSpec.Name.Name, name.Name)
				}
			}
		}
	}
	for _, name := range []string{"borrowedCache", "borrowedMailer", "borrowedCluster"} {
		assert.True(t, checked[name], "borrowed capability contract %s was removed or made uninspectable", name)
	}

	serverPath := filepath.Join("..", "server.go")
	serverFile, err := parser.ParseFile(token.NewFileSet(), serverPath, nil, 0)
	require.NoError(t, err)
	for _, declaration := range serverFile.Decls {
		generic, ok := declaration.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, specification := range generic.Specs {
			typeSpec, ok := specification.(*ast.TypeSpec)
			if !ok || typeSpec.Name.Name != "runtimeLogger" {
				continue
			}
			checked["runtimeLogger"] = true
			contract := typeSpec.Type.(*ast.InterfaceType)
			for _, method := range contract.Methods.List {
				for _, name := range method.Names {
					assert.False(t, forbidden[name.Name], "runtimeLogger exposes lifecycle method %s", name.Name)
				}
			}
		}
	}
	assert.True(t, checked["runtimeLogger"], "runtimeLogger borrowed contract was removed or made uninspectable")
}

func TestTestingRuntimeIsBehavioralProjection(t *testing.T) {
	t.Parallel()

	file := parseRuntimeOwnershipFile(t, filepath.Join("..", "testhooks.go"))
	want := map[string]string{"Server": "Server", "Application": "App", "Handler": "Handler"}
	for _, declaration := range file.Decls {
		generic, ok := declaration.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, specification := range generic.Specs {
			typeSpec, ok := specification.(*ast.TypeSpec)
			if !ok || typeSpec.Name.Name != "TestingRuntime" {
				continue
			}
			structure := typeSpec.Type.(*ast.StructType)
			require.Len(t, structure.Fields.List, len(want), "TestingRuntime fields")
			for _, field := range structure.Fields.List {
				require.Len(t, field.Names, 1, "TestingRuntime contains an embedded or grouped field")
				name := field.Names[0].Name
				wantType, ok := want[name]
				if !assert.True(t, ok, "TestingRuntime exposes %s outside the behavioral projection", name) {
					continue
				}
				assert.True(t, expressionNamesType(field.Type, wantType), "TestingRuntime.%s has unexpected type", name)
			}
			return
		}
	}
	require.FailNow(t, "TestingRuntime declaration not found")
}

func TestTestingOverridesCannotReplaceCompositionPhases(t *testing.T) {
	t.Parallel()

	file := parseRuntimeOwnershipFile(t, filepath.Join("..", "testhooks.go"))
	for _, declaration := range file.Decls {
		generic, ok := declaration.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, specification := range generic.Specs {
			typeSpec, ok := specification.(*ast.TypeSpec)
			if !ok || typeSpec.Name.Name != "TestingOverrides" {
				continue
			}
			structure := typeSpec.Type.(*ast.StructType)
			for _, field := range structure.Fields.List {
				assert.False(t, containsFunctionType(field.Type), "TestingOverrides exposes function-valued phase override")
			}
			return
		}
	}
	require.FailNow(t, "TestingOverrides declaration not found")
}

func TestNewForTestingDoesNotStartRuntime(t *testing.T) {
	t.Parallel()

	file := parseRuntimeOwnershipFile(t, filepath.Join("..", "testhooks.go"))
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "NewForTesting" {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if ok {
				assert.NotEqual(t, "Start", selector.Sel.Name, "NewForTesting performs hidden runtime startup")
			}
			return true
		})
		return
	}
	require.FailNow(t, "NewForTesting declaration not found")
}

func parseRuntimeOwnershipFile(t *testing.T, path string) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	require.NoError(t, err)
	return file
}

func containsFunctionType(expression ast.Expr) bool {
	found := false
	ast.Inspect(expression, func(node ast.Node) bool {
		if _, ok := node.(*ast.FuncType); ok {
			found = true
			return false
		}
		return true
	})
	return found
}

func expressionNamesType(expression ast.Expr, want string) bool {
	switch value := expression.(type) {
	case *ast.StarExpr:
		return expressionNamesType(value.X, want)
	case *ast.SelectorExpr:
		return value.Sel.Name == want
	case *ast.Ident:
		return value.Name == want
	default:
		return false
	}
}
