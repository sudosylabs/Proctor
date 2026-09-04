// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestRoleBindingAppliesByDeclaredInheritance(t *testing.T) {
	institutionID := model.NewId()
	parentID := model.NewId()
	childID := model.NewId()
	classID := model.NewId()
	resolved := resolvedAuthorizationResource{
		institutionID: institutionID,
		academicUnitID: map[string]struct{}{
			parentID: {}, childID: {},
		},
		classID: classID,
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
				ScopeType: model.RoleScopeAcademicUnit, ScopeID: model.NewId(),
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
			if got := roleBindingApplies(
				test.binding, test.definition, test.resource, resolved,
			); got != test.want {
				t.Fatalf("roleBindingApplies() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestPersonalAccessTokenIsOnlyAnAuthorizationCeiling(t *testing.T) {
	parentID := model.NewId()
	childID := model.NewId()
	classID := model.NewId()
	resolved := resolvedAuthorizationResource{
		institutionID: model.NewId(),
		academicUnitID: map[string]struct{}{
			parentID: {}, childID: {},
		},
		classID: classID,
	}
	principal := model.Principal{
		CredentialType:   model.CredentialPersonalAccessToken,
		CredentialScopes: []string{string(model.ActionClassView)},
		AcademicUnitID:   model.AcademicUnitID(parentID),
	}
	if !personalAccessTokenAllows(
		principal,
		model.ActionClassView,
		model.Resource{Type: model.ResourceClass, ID: classID},
		resolved,
	) {
		t.Fatal("matching scope and descendant resource were rejected")
	}
	if personalAccessTokenAllows(
		principal,
		model.ActionClassMembersView,
		model.Resource{Type: model.ResourceClass, ID: classID},
		resolved,
	) {
		t.Fatal("an action absent from token scopes was allowed")
	}
	if personalAccessTokenAllows(
		principal,
		model.ActionClassView,
		model.Resource{Type: model.ResourceInstitution, ID: resolved.institutionID},
		resolved,
	) {
		t.Fatal("academic-unit-constrained token was allowed at institution scope")
	}
	principal.AcademicUnitID = model.NewAcademicUnitID()
	if personalAccessTokenAllows(
		principal,
		model.ActionClassView,
		model.Resource{Type: model.ResourceClass, ID: classID},
		resolved,
	) {
		t.Fatal("token constrained to an unrelated academic unit was allowed")
	}
}
