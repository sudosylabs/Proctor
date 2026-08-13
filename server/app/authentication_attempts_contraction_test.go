// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestAuthenticationAttemptAccountingHasOneProductionOwner(t *testing.T) {
	t.Parallel()

	accountingType := reflect.TypeOf((*authenticationAttemptAccounting)(nil))
	for name, service := range map[string]reflect.Type{
		"local login":             reflect.TypeOf(authenticationService{}),
		"account recovery":        reflect.TypeOf(accountTokenService{}),
		"external authentication": reflect.TypeOf(externalAuthenticationService{}),
		"installation bootstrap":  reflect.TypeOf(bootstrapService{}),
	} {
		field, ok := service.FieldByName("attempts")
		if !ok || field.Type != accountingType {
			t.Fatalf("%s does not retain the shared private attempt accounting", name)
		}
	}

	files := parseProductionAppFiles(t)
	constructorCalls := 0
	for name, file := range files {
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			identifier, ok := call.Fun.(*ast.Ident)
			if ok && identifier.Name == "newAuthenticationAttemptAccounting" {
				constructorCalls++
				if name != "construction.go" {
					t.Errorf("%s constructs authentication attempt accounting; only the private construction recipe may construct it", name)
				}
			}
			return true
		})
	}
	if constructorCalls != 1 {
		t.Fatalf("production constructor calls = %d, want one shared accounting instance", constructorCalls)
	}
}

func TestMigratedAuthenticationFlowsDoNotReclaimAttemptMechanics(t *testing.T) {
	t.Parallel()

	files := parseProductionAppFiles(t)
	for _, name := range []string{
		"authentication.go",
		"account_recovery.go",
		"account_recovery_ports.go",
		"external_authentication.go",
		"bootstrap.go",
	} {
		file := files[name]
		if file == nil {
			t.Fatalf("production source %s was not parsed", name)
		}
		forbiddenNamespaces := []string{
			"authentication/attempts/",
			"authentication/login/",
			"authentication/recovery/",
			"authentication/external/",
			"authentication/bootstrap/",
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch candidate := node.(type) {
			case *ast.CallExpr:
				if selector, ok := candidate.Fun.(*ast.SelectorExpr); ok &&
					selector.Sel.Name == "Add" && len(candidate.Args) == 4 {
					t.Errorf("%s performs four-argument counter Add outside authentication_attempts.go", name)
				}
			case *ast.BasicLit:
				if candidate.Kind != token.STRING {
					return true
				}
				value, err := strconv.Unquote(candidate.Value)
				if err != nil {
					return true
				}
				for _, prefix := range forbiddenNamespaces {
					if strings.HasPrefix(value, prefix) {
						t.Errorf("%s constructs attempt-accounting key namespace %q", name, prefix)
					}
				}
			}
			return true
		})
	}
}

func parseProductionAppFiles(t *testing.T) map[string]*ast.File {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	files := make(map[string]*ast.File)
	fileSet := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(fileSet, name, nil, 0)
		if parseErr != nil {
			t.Fatalf("parse production source %s: %v", name, parseErr)
		}
		files[name] = file
	}
	return files
}
