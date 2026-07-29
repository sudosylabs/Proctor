// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/public/model/permission.go. Proctor keeps
// stable, action-oriented permission identifiers while defining inheritance
// explicitly for its institution and academic hierarchy.

package model

import "sort"

// Action is a stable authorization contract. Actions describe domain
// capabilities, never HTTP methods or route names.
type Action string

const (
	ActionInstitutionManage Action = "institution.manage"
	ActionRoleManage        Action = "role.manage"
	ActionAuditView         Action = "audit.view"

	ActionAcademicUnitView   Action = "academic_unit.view"
	ActionAcademicUnitManage Action = "academic_unit.manage"

	ActionClassView          Action = "class.view"
	ActionClassMembersView   Action = "class.members.view"
	ActionClassMembersManage Action = "class.members.manage"
)

// ResourceType identifies an authorization target.
type ResourceType string

const (
	ResourceInstitution  ResourceType = "institution"
	ResourceAcademicUnit ResourceType = "academic_unit"
	ResourceClass        ResourceType = "class"
)

// Resource identifies the concrete object against which an action is checked.
type Resource struct {
	Type ResourceType `json:"type"`
	Id   string       `json:"id"`
}

// ActionDefinition controls which resource an action accepts and which parent
// role scopes may grant it. It is deliberately closed and code-reviewed:
// syntactically valid unknown role permissions may survive a downgrade, but
// they never authorize an operation until this registry recognizes them.
type ActionDefinition struct {
	Action                    Action
	ResourceType              ResourceType
	InheritInstitutionScope   bool
	InheritAcademicUnitScopes bool
}

var actionDefinitions = map[Action]ActionDefinition{
	ActionInstitutionManage: {
		Action: ActionInstitutionManage, ResourceType: ResourceInstitution,
		InheritInstitutionScope: true,
	},
	ActionRoleManage: {
		Action: ActionRoleManage, ResourceType: ResourceInstitution,
		InheritInstitutionScope: true,
	},
	ActionAuditView: {
		Action: ActionAuditView, ResourceType: ResourceInstitution,
		InheritInstitutionScope: true,
	},
	ActionAcademicUnitView: {
		Action: ActionAcademicUnitView, ResourceType: ResourceAcademicUnit,
		InheritInstitutionScope: true, InheritAcademicUnitScopes: true,
	},
	ActionAcademicUnitManage: {
		Action: ActionAcademicUnitManage, ResourceType: ResourceAcademicUnit,
		InheritInstitutionScope: true, InheritAcademicUnitScopes: true,
	},
	ActionClassView: {
		Action: ActionClassView, ResourceType: ResourceClass,
		InheritInstitutionScope: true, InheritAcademicUnitScopes: true,
	},
	ActionClassMembersView: {
		Action: ActionClassMembersView, ResourceType: ResourceClass,
		InheritInstitutionScope: true, InheritAcademicUnitScopes: true,
	},
	ActionClassMembersManage: {
		Action: ActionClassMembersManage, ResourceType: ResourceClass,
		InheritInstitutionScope: true, InheritAcademicUnitScopes: true,
	},
}

func DefinitionForAction(action Action) (ActionDefinition, bool) {
	definition, ok := actionDefinitions[action]
	return definition, ok
}

// AllActions returns every action currently recognized by the authorization
// evaluator in stable order. The installation bootstrap uses this closed,
// code-reviewed registry to construct the protected system-administrator role.
func AllActions() []string {
	actions := make([]string, 0, len(actionDefinitions))
	for action := range actionDefinitions {
		actions = append(actions, string(action))
	}
	sort.Strings(actions)
	return actions
}

func IsKnownAction(action string) bool {
	_, ok := actionDefinitions[Action(action)]
	return ok
}

func (r Resource) IsValid() bool {
	if !IsValidId(r.Id) {
		return false
	}
	switch r.Type {
	case ResourceInstitution, ResourceAcademicUnit, ResourceClass:
		return true
	default:
		return false
	}
}
