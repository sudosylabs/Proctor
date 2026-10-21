// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestRoleBindingAppliesByDeclaredInheritance(t *testing.T) {
	institutionID := model.NewID()
	parentID := model.NewID()
	childID := model.NewID()
	classID := model.NewID()
	resolved := resolvedAuthorizationResource{
		institutionID: model.InstitutionID(institutionID),
		academicUnitID: map[model.AcademicUnitID]struct{}{
			model.AcademicUnitID(parentID): {}, model.AcademicUnitID(childID): {},
		},
		classID: model.ClassID(classID),
	}
	classDefinition, _ := model.DefinitionForAction(model.ActionClassView)
	institutionDefinition, _ := model.DefinitionForAction(model.ActionInstitutionManage)
	tests := []struct {
		name       string
		binding    *model.RoleBinding
		definition model.ActionDefinition
		resource   model.Resource
		want       bool
	}{
		{
			name: "institution role inherits to class",
			binding: &model.RoleBinding{
				ScopeType: model.RoleScopeInstitution, ScopeID: institutionID,
			},
			definition: classDefinition,
			resource:   model.Resource{Type: model.ResourceClass, ID: classID},
			want:       true,
		},
		{
			name: "ancestor academic unit inherits to class",
			binding: &model.RoleBinding{
				ScopeType: model.RoleScopeAcademicUnit, ScopeID: parentID,
			},
			definition: classDefinition,
			resource:   model.Resource{Type: model.ResourceClass, ID: classID},
			want:       true,
		},
		{
			name: "unrelated academic unit does not inherit",
			binding: &model.RoleBinding{
				ScopeType: model.RoleScopeAcademicUnit, ScopeID: model.NewID(),
			},
			definition: classDefinition,
			resource:   model.Resource{Type: model.ResourceClass, ID: classID},
		},
		{
			name: "class binding applies only to exact class",
			binding: &model.RoleBinding{
				ScopeType: model.RoleScopeClass, ScopeID: classID,
			},
			definition: classDefinition,
			resource:   model.Resource{Type: model.ResourceClass, ID: classID},
			want:       true,
		},
		{
			name: "lower scope cannot manage institution",
			binding: &model.RoleBinding{
				ScopeType: model.RoleScopeAcademicUnit, ScopeID: childID,
			},
			definition: institutionDefinition,
			resource: model.Resource{
				Type: model.ResourceInstitution, ID: institutionID,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, roleBindingApplies(
				test.binding, test.definition, test.resource, resolved,
			), "roleBindingApplies()")
		})
	}
}

func TestPersonalAccessTokenIsOnlyAnAuthorizationCeiling(t *testing.T) {
	parentID := model.NewID()
	childID := model.NewID()
	classID := model.NewID()
	resolved := resolvedAuthorizationResource{
		institutionID: model.NewInstitutionID(),
		academicUnitID: map[model.AcademicUnitID]struct{}{
			model.AcademicUnitID(parentID): {}, model.AcademicUnitID(childID): {},
		},
		classID: model.ClassID(classID),
	}
	principal := model.Principal{
		CredentialType:   model.CredentialPersonalAccessToken,
		CredentialScopes: []string{string(model.ActionClassView)},
		AcademicUnitID:   model.AcademicUnitID(parentID),
	}
	require.True(t, personalAccessTokenAllows(
		principal,
		model.ActionClassView,
		model.Resource{Type: model.ResourceClass, ID: classID},
		resolved,
	), "matching scope and descendant resource were rejected")
	require.False(t, personalAccessTokenAllows(
		principal,
		model.ActionClassMembersView,
		model.Resource{Type: model.ResourceClass, ID: classID},
		resolved,
	), "an action absent from token scopes was allowed")
	require.False(t, personalAccessTokenAllows(
		principal,
		model.ActionClassView,
		model.Resource{Type: model.ResourceInstitution, ID: resolved.institutionID.String()},
		resolved,
	), "academic-unit-constrained token was allowed at institution scope")
	principal.AcademicUnitID = model.NewAcademicUnitID()
	require.False(t, personalAccessTokenAllows(
		principal,
		model.ActionClassView,
		model.Resource{Type: model.ResourceClass, ID: classID},
		resolved,
	), "token constrained to an unrelated academic unit was allowed")
}
