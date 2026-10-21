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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEveryDeclaredActionIsCatalogued pairs with the architecture test that
// forbids audit action literals outside model: every model.Action constant
// must be a permission definition or an event-only audit action, so any
// action an audit event can carry is declared in the catalog.
func TestEveryDeclaredActionIsCatalogued(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	declared := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		source, err := os.ReadFile(name)
		require.NoError(t, err)
		file, err := parser.ParseFile(token.NewFileSet(), name, source, 0)
		require.NoError(t, err)
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
					require.True(t, ok, "%s: Action constant %s is not a string literal", name, constant.Name)
					action, err := strconv.Unquote(literal.Value)
					require.NoError(t, err)
					declared++
					assert.True(t, IsAuditAction(action), "%s: Action constant %s = %q is not in the audit action catalog", name, constant.Name, action)
				}
			}
		}
	}
	require.Equal(t, len(AuditActions()), declared, "declared")
}

func TestEventOnlyAuditActionsAreNeverPermissions(t *testing.T) {
	for action := range eventAuditActions {
		assert.False(t, IsKnownAction(string(action)), "event-only audit action %q is also a known action", action)
		assert.False(t, IsGrantableAction(string(action)), "event-only audit action %q is also grantable", action)
	}
}

func TestAcademicAuditActionsAreTheDeclaredAcademicCatalog(t *testing.T) {
	academic := AcademicAuditActions()
	for _, action := range []Action{
		ActionAcademicAuditView, ActionAcademicUnitManage, ActionClassMembersManage,
		ActionInvitationCreate, ActionOnboardingBatchManage, ActionRoleBindingManage,
		ActionUserView, ActionUserSearch,
	} {
		assert.Contains(t, academic, string(action), "academic audit catalog omits")
	}
	for _, action := range []Action{
		ActionAuditView, ActionUserManage, ActionRoleManage, ActionExamManage,
		ActionAuthenticationLogin, ActionInvitationAccept,
	} {
		assert.NotContains(t, academic, string(action), "academic audit catalog admits")
	}
	require.Len(t, academic, 25)
	require.True(t, slices.IsSorted(academic))
}
