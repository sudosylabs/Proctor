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
	ActionInstitutionManage        Action = "institution.manage"
	ActionRoleManage               Action = "role.manage"
	ActionAuditView                Action = "audit.view"
	ActionUserView                 Action = "user.view"
	ActionUserManage               Action = "user.manage"
	ActionUserProfilePictureManage Action = "user.profile_picture.manage"
	ActionSessionView              Action = "session.view"
	ActionSessionManage            Action = "session.manage"
	ActionJobView                  Action = "job.view"
	ActionJobManage                Action = "job.manage"
	ActionExamCreate               Action = "exam.create"
	ActionExamCreateOverride       Action = "exam.create.override"
	ActionExamView                 Action = "exam.view"
	ActionExamViewOverride         Action = "exam.view.override"
	ActionExamManage               Action = "exam.manage"
	ActionExamManageOverride       Action = "exam.manage.override"
	ActionExamPublish              Action = "exam.publish"
	ActionExamPublishOverride      Action = "exam.publish.override"

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
	ResourceUser         ResourceType = "user"
	ResourceExam         ResourceType = "exam"
)

// Resource identifies the concrete object against which an action is checked.
type Resource struct {
	Type ResourceType
	ID   string
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
	ActionUserView: {
		Action: ActionUserView, ResourceType: ResourceUser,
		InheritInstitutionScope: true,
	},
	ActionUserManage: {
		Action: ActionUserManage, ResourceType: ResourceUser,
		InheritInstitutionScope: true,
	},
	ActionUserProfilePictureManage: {
		Action: ActionUserProfilePictureManage, ResourceType: ResourceUser,
		InheritInstitutionScope: true,
	},
	ActionSessionView: {
		Action: ActionSessionView, ResourceType: ResourceUser,
		InheritInstitutionScope: true,
	},
	ActionSessionManage: {
		Action: ActionSessionManage, ResourceType: ResourceUser,
		InheritInstitutionScope: true,
	},
	ActionJobView: {
		Action: ActionJobView, ResourceType: ResourceInstitution,
		InheritInstitutionScope: true,
	},
	ActionJobManage: {
		Action: ActionJobManage, ResourceType: ResourceInstitution,
		InheritInstitutionScope: true,
	},
	ActionExamCreate: {
		Action: ActionExamCreate, ResourceType: ResourceAcademicUnit,
		InheritInstitutionScope: true, InheritAcademicUnitScopes: true,
	},
	ActionExamCreateOverride: {
		Action: ActionExamCreateOverride, ResourceType: ResourceAcademicUnit,
		InheritInstitutionScope: true, InheritAcademicUnitScopes: true,
	},
	ActionExamView: {
		Action: ActionExamView, ResourceType: ResourceExam,
		InheritInstitutionScope: true, InheritAcademicUnitScopes: true,
	},
	ActionExamViewOverride: {
		Action: ActionExamViewOverride, ResourceType: ResourceExam,
		InheritInstitutionScope: true, InheritAcademicUnitScopes: true,
	},
	ActionExamManage: {
		Action: ActionExamManage, ResourceType: ResourceExam,
		InheritInstitutionScope: true, InheritAcademicUnitScopes: true,
	},
	ActionExamManageOverride: {
		Action: ActionExamManageOverride, ResourceType: ResourceExam,
		InheritInstitutionScope: true, InheritAcademicUnitScopes: true,
	},
	ActionExamPublish: {
		Action: ActionExamPublish, ResourceType: ResourceExam,
		InheritInstitutionScope: true, InheritAcademicUnitScopes: true,
	},
	ActionExamPublishOverride: {
		Action: ActionExamPublishOverride, ResourceType: ResourceExam,
		InheritInstitutionScope: true, InheritAcademicUnitScopes: true,
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

// Validate checks that the resource identifies a supported authorization target.
func (r Resource) Validate() error {
	const where = "Resource.Validate"
	if !IsValidId(r.ID) {
		return invalidModelError(where, "resource", "id", "must be a valid identifier", "")
	}
	switch r.Type {
	case ResourceInstitution, ResourceAcademicUnit, ResourceClass, ResourceUser, ResourceExam:
		return nil
	default:
		return invalidModelError(where, "resource", "type", "has an unknown value", "id="+r.ID)
	}
}
