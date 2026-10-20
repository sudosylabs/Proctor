// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestEveryDeclaredActionIsCatalogued pairs with the architecture test that
// forbids audit action literals outside model: every model.Action constant
// must be a permission definition or an event-only audit action, so any
// action an audit event can carry is declared in the catalog.
func TestEveryDeclaredActionIsCatalogued(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	declared := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, source, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range file.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.CONST {
				continue
			}
			for _, spec := range general.Specs {
				value := spec.(*ast.ValueSpec)
				typeName, ok := value.Type.(*ast.Ident)
				if !ok || typeName.Name != "Action" {
					continue
				}
				for index, constant := range value.Names {
					literal, ok := value.Values[index].(*ast.BasicLit)
					if !ok {
						t.Fatalf("%s: Action constant %s is not a string literal", name, constant.Name)
					}
					action, err := strconv.Unquote(literal.Value)
					if err != nil {
						t.Fatal(err)
					}
					declared++
					if !IsAuditAction(action) {
						t.Errorf("%s: Action constant %s = %q is not in the audit action catalog", name, constant.Name, action)
					}
				}
			}
		}
	}
	if catalogued := len(AuditActions()); declared != catalogued {
		t.Fatalf("declared %d Action constants, catalogued %d audit actions", declared, catalogued)
	}
}

func TestEventOnlyAuditActionsAreNeverPermissions(t *testing.T) {
	for action := range eventAuditActions {
		if IsKnownAction(string(action)) || IsGrantableAction(string(action)) {
			t.Errorf("event-only audit action %q is also a permission", action)
		}
	}
}

func TestAcademicAuditActionsAreTheDeclaredAcademicCatalog(t *testing.T) {
	academic := AcademicAuditActions()
	for _, action := range []Action{
		ActionAcademicAuditView, ActionAcademicUnitManage, ActionClassMembersManage,
		ActionInvitationCreate, ActionOnboardingBatchManage, ActionRoleBindingManage,
		ActionUserView, ActionUserSearch,
	} {
		if !slices.Contains(academic, string(action)) {
			t.Errorf("academic audit catalog omits %q", action)
		}
	}
	for _, action := range []Action{
		ActionAuditView, ActionUserManage, ActionRoleManage, ActionExamManage,
		ActionAuthenticationLogin, ActionInvitationAccept,
	} {
		if slices.Contains(academic, string(action)) {
			t.Errorf("academic audit catalog admits %q", action)
		}
	}
	if len(academic) != 25 || !slices.IsSorted(academic) {
		t.Fatalf("academic audit catalog = %v", academic)
	}
}
