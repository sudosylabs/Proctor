// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/public/model/permission.go. Proctor keeps
// stable, action-oriented permission identifiers while defining inheritance
// explicitly for its institution and academic hierarchy.

package model

import (
	"slices"
	"sort"
)

// Action is a stable authorization contract. Actions describe domain
// capabilities, never HTTP methods or route names.
type Action string

const (
	ActionInstitutionManage         Action = "institution.manage"
	ActionRoleManage                Action = "role.manage"
	ActionAuditView                 Action = "audit.view"
	ActionAcademicAuditView         Action = "academic_audit.view"
	ActionUserView                  Action = "user.view"
	ActionUserManage                Action = "user.manage"
	ActionUserProfilePictureManage  Action = "user.profile_picture.manage"
	ActionSessionView               Action = "session.view"
	ActionSessionManage             Action = "session.manage"
	ActionJobView                   Action = "job.view"
	ActionJobManage                 Action = "job.manage"
	ActionMailView                  Action = "mail.view"
	ActionMailManage                Action = "mail.manage"
	ActionMailRekey                 Action = "mail.rekey"
	ActionExamCreate                Action = "exam.create"
	ActionExamCreateOverride        Action = "exam.create.override"
	ActionExamView                  Action = "exam.view"
	ActionExamViewOverride          Action = "exam.view.override"
	ActionExamManage                Action = "exam.manage"
	ActionExamManageOverride        Action = "exam.manage.override"
	ActionExamPublish               Action = "exam.publish"
	ActionExamPublishOverride       Action = "exam.publish.override"
	ActionExamSittingCreate         Action = "exam.sitting.create"
	ActionExamSittingCreateOverride Action = "exam.sitting.create.override"
	ActionExamSittingView           Action = "exam.sitting.view"
	ActionExamSittingViewOverride   Action = "exam.sitting.view.override"
	ActionExamSittingManage         Action = "exam.sitting.manage"
	ActionExamSittingManageOverride Action = "exam.sitting.manage.override"
	ActionExamSittingParticipate    Action = "exam.sitting.participate"
	ActionSubmissionView            Action = "submission.view"
	ActionSubmissionViewOverride    Action = "submission.view.override"
	ActionSubmissionReview          Action = "submission.review"
	ActionSubmissionReviewOverride  Action = "submission.review.override"
	ActionSubmissionRelease         Action = "submission.release"
	ActionSubmissionReleaseOverride Action = "submission.release.override"

	ActionAcademicUnitView          Action = "academic_unit.view"
	ActionAcademicUnitManage        Action = "academic_unit.manage"
	ActionAcademicUnitMembersView   Action = "academic_unit.members.view"
	ActionAcademicUnitMembersManage Action = "academic_unit.members.manage"
	ActionAcademicPeriodView        Action = "academic_period.view"
	ActionAcademicPeriodManage      Action = "academic_period.manage"
	ActionProgrammeView             Action = "programme.view"
	ActionProgrammeManage           Action = "programme.manage"
	ActionProgrammeLevelView        Action = "programme_level.view"
	ActionProgrammeLevelManage      Action = "programme_level.manage"

	ActionClassView                 Action = "class.view"
	ActionClassManage               Action = "class.manage"
	ActionClassMembersView          Action = "class.members.view"
	ActionClassMembersManage        Action = "class.members.manage"
	ActionAcademicProgressionManage Action = "academic.progression.manage"

	ActionAccessPolicyView       Action = "access_policy.view"
	ActionAccessPolicyManage     Action = "access_policy.manage"
	ActionInvitationView         Action = "invitation.view"
	ActionInvitationCreate       Action = "invitation.create"
	ActionInvitationManage       Action = "invitation.manage"
	ActionOnboardingBatchView    Action = "onboarding_batch.view"
	ActionOnboardingBatchManage  Action = "onboarding_batch.manage"
	ActionExternalIdentityManage Action = "external_identity.manage"
	ActionRoleView               Action = "role.view"
	ActionRoleBindingView        Action = "role_binding.view"
	ActionRoleBindingManage      Action = "role_binding.manage"
)

// ResourceType identifies an authorization target.
type ResourceType string

const (
	ResourceInstitution    ResourceType = "institution"
	ResourceAcademicUnit   ResourceType = "academic_unit"
	ResourceProgramme      ResourceType = "programme"
	ResourceProgrammeLevel ResourceType = "programme_level"
	ResourceAcademicPeriod ResourceType = "academic_period"
	ResourceClass          ResourceType = "class"
	ResourceUser           ResourceType = "user"
	ResourceExam           ResourceType = "exam"
	ResourceExamSitting    ResourceType = "exam_sitting"
	ResourceSubmission     ResourceType = "submission"
	ResourceMailDelivery   ResourceType = "mail_delivery"
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
	CompatibleResourceTypes   []ResourceType
	InheritInstitutionScope   bool
	InheritAcademicUnitScopes bool
	// RelationshipOnly marks a recognized application/realtime capability that
	// must be established from current domain state and can never be granted by
	// a reusable Role or Personal Access Token scope.
	RelationshipOnly bool
	// PersonalAccessTokenForbidden keeps sensitive administration behind an
	// interactive Session even though the action remains grantable to Roles.
	PersonalAccessTokenForbidden bool
}

// AcceptsResource reports whether the action may authorize the actual target
// resource or an owning scope used for a create or bounded collection.
func (d ActionDefinition) AcceptsResource(resourceType ResourceType) bool {
	return d.ResourceType == resourceType || slices.Contains(d.CompatibleResourceTypes, resourceType)
}

var actionDefinitions = map[Action]ActionDefinition{
	ActionInstitutionManage: {
		Action: ActionInstitutionManage, ResourceType: ResourceInstitution,
		InheritInstitutionScope: true,
	},
	ActionRoleManage: {
		Action: ActionRoleManage, ResourceType: ResourceInstitution,
		InheritInstitutionScope: true, PersonalAccessTokenForbidden: true,
	},
	ActionRoleView: {
		Action: ActionRoleView, ResourceType: ResourceInstitution,
		InheritInstitutionScope: true, PersonalAccessTokenForbidden: true,
	},
	ActionRoleBindingView: {
		Action: ActionRoleBindingView, ResourceType: ResourceInstitution,
		CompatibleResourceTypes: []ResourceType{ResourceAcademicUnit, ResourceClass},
		InheritInstitutionScope: true, InheritAcademicUnitScopes: true, PersonalAccessTokenForbidden: true,
	},
	ActionRoleBindingManage: {
		Action: ActionRoleBindingManage, ResourceType: ResourceInstitution,
		CompatibleResourceTypes: []ResourceType{ResourceAcademicUnit, ResourceClass},
		InheritInstitutionScope: true, InheritAcademicUnitScopes: true, PersonalAccessTokenForbidden: true,
	},
	ActionAuditView: {
		Action: ActionAuditView, ResourceType: ResourceInstitution,
		InheritInstitutionScope: true,
	},
	ActionAcademicAuditView: {
		Action: ActionAcademicAuditView, ResourceType: ResourceAcademicUnit,
		InheritInstitutionScope: true, InheritAcademicUnitScopes: true,
	},
	ActionUserView: {
		Action: ActionUserView, ResourceType: ResourceUser,
		InheritInstitutionScope: true, InheritAcademicUnitScopes: true,
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
	ActionMailView: {
		Action: ActionMailView, ResourceType: ResourceInstitution,
		InheritInstitutionScope: true,
	},
	ActionMailManage: {
		Action: ActionMailManage, ResourceType: ResourceInstitution,
		InheritInstitutionScope: true,
	},
	ActionMailRekey: {
		Action: ActionMailRekey, ResourceType: ResourceInstitution,
		InheritInstitutionScope: true, PersonalAccessTokenForbidden: true,
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
	ActionExamSittingCreate: {
		Action: ActionExamSittingCreate, ResourceType: ResourceExam,
		InheritInstitutionScope: true, InheritAcademicUnitScopes: true,
	},
	ActionExamSittingCreateOverride: {
		Action: ActionExamSittingCreateOverride, ResourceType: ResourceExam,
		InheritInstitutionScope: true, InheritAcademicUnitScopes: true,
	},
	ActionExamSittingView: {
		Action: ActionExamSittingView, ResourceType: ResourceExamSitting,
		InheritInstitutionScope: true, InheritAcademicUnitScopes: true,
	},
	ActionExamSittingViewOverride: {
		Action: ActionExamSittingViewOverride, ResourceType: ResourceExamSitting,
		InheritInstitutionScope: true, InheritAcademicUnitScopes: true,
	},
	ActionExamSittingManage: {
		Action: ActionExamSittingManage, ResourceType: ResourceExamSitting,
		InheritInstitutionScope: true, InheritAcademicUnitScopes: true,
	},
	ActionExamSittingManageOverride: {
		Action: ActionExamSittingManageOverride, ResourceType: ResourceExamSitting,
		InheritInstitutionScope: true, InheritAcademicUnitScopes: true,
	},
	ActionExamSittingParticipate: {
		Action: ActionExamSittingParticipate, ResourceType: ResourceExamSitting,
		RelationshipOnly: true,
	},
	ActionSubmissionView: {
		Action: ActionSubmissionView, ResourceType: ResourceSubmission,
		InheritInstitutionScope: true, InheritAcademicUnitScopes: true,
	},
	ActionSubmissionViewOverride: {
		Action: ActionSubmissionViewOverride, ResourceType: ResourceSubmission,
		InheritInstitutionScope: true, InheritAcademicUnitScopes: true,
	},
	ActionSubmissionReview: {
		Action: ActionSubmissionReview, ResourceType: ResourceSubmission,
		InheritInstitutionScope: true, InheritAcademicUnitScopes: true,
	},
	ActionSubmissionReviewOverride: {
		Action: ActionSubmissionReviewOverride, ResourceType: ResourceSubmission,
		InheritInstitutionScope: true, InheritAcademicUnitScopes: true,
	},
	ActionSubmissionRelease: {
		Action: ActionSubmissionRelease, ResourceType: ResourceSubmission,
		InheritInstitutionScope: true, InheritAcademicUnitScopes: true,
	},
	ActionSubmissionReleaseOverride: {
		Action: ActionSubmissionReleaseOverride, ResourceType: ResourceSubmission,
		InheritInstitutionScope: true, InheritAcademicUnitScopes: true,
	},
	ActionAcademicUnitView: {
		Action: ActionAcademicUnitView, ResourceType: ResourceAcademicUnit,
		CompatibleResourceTypes: []ResourceType{ResourceInstitution},
		InheritInstitutionScope: true, InheritAcademicUnitScopes: true,
	},
	ActionAcademicUnitManage: {
		Action: ActionAcademicUnitManage, ResourceType: ResourceAcademicUnit,
		CompatibleResourceTypes: []ResourceType{ResourceInstitution},
		InheritInstitutionScope: true, InheritAcademicUnitScopes: true,
	},
	ActionAcademicUnitMembersView: {
		Action: ActionAcademicUnitMembersView, ResourceType: ResourceAcademicUnit,
		InheritInstitutionScope: true, InheritAcademicUnitScopes: true,
	},
	ActionAcademicUnitMembersManage: {
		Action: ActionAcademicUnitMembersManage, ResourceType: ResourceAcademicUnit,
		InheritInstitutionScope: true, InheritAcademicUnitScopes: true,
	},
	ActionAcademicPeriodView: {
		Action: ActionAcademicPeriodView, ResourceType: ResourceAcademicPeriod,
		InheritInstitutionScope: true, InheritAcademicUnitScopes: true,
	},
	ActionAcademicPeriodManage: {
		Action: ActionAcademicPeriodManage, ResourceType: ResourceAcademicPeriod,
		InheritInstitutionScope: true, InheritAcademicUnitScopes: true,
	},
	ActionProgrammeView: {
		Action: ActionProgrammeView, ResourceType: ResourceProgramme,
		CompatibleResourceTypes: []ResourceType{ResourceAcademicUnit},
		InheritInstitutionScope: true, InheritAcademicUnitScopes: true,
	},
	ActionProgrammeManage: {
		Action: ActionProgrammeManage, ResourceType: ResourceProgramme,
		CompatibleResourceTypes: []ResourceType{ResourceAcademicUnit},
		InheritInstitutionScope: true, InheritAcademicUnitScopes: true,
	},
	ActionProgrammeLevelView: {
		Action: ActionProgrammeLevelView, ResourceType: ResourceProgrammeLevel,
		CompatibleResourceTypes: []ResourceType{ResourceProgramme},
		InheritInstitutionScope: true, InheritAcademicUnitScopes: true,
	},
	ActionProgrammeLevelManage: {
		Action: ActionProgrammeLevelManage, ResourceType: ResourceProgrammeLevel,
		CompatibleResourceTypes: []ResourceType{ResourceProgramme},
		InheritInstitutionScope: true, InheritAcademicUnitScopes: true,
	},
	ActionClassView: {
		Action: ActionClassView, ResourceType: ResourceClass,
		CompatibleResourceTypes: []ResourceType{ResourceProgrammeLevel, ResourceAcademicUnit},
		InheritInstitutionScope: true, InheritAcademicUnitScopes: true,
	},
	ActionClassManage: {
		Action: ActionClassManage, ResourceType: ResourceClass,
		CompatibleResourceTypes: []ResourceType{ResourceProgrammeLevel},
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
	ActionAcademicProgressionManage: {
		Action: ActionAcademicProgressionManage, ResourceType: ResourceClass,
		InheritInstitutionScope: true, InheritAcademicUnitScopes: true,
	},
	ActionAccessPolicyView: {
		Action: ActionAccessPolicyView, ResourceType: ResourceInstitution,
		InheritInstitutionScope: true, PersonalAccessTokenForbidden: true,
	},
	ActionAccessPolicyManage: {
		Action: ActionAccessPolicyManage, ResourceType: ResourceInstitution,
		InheritInstitutionScope: true, PersonalAccessTokenForbidden: true,
	},
	ActionInvitationView: {
		Action: ActionInvitationView, ResourceType: ResourceInstitution,
		CompatibleResourceTypes: []ResourceType{ResourceAcademicUnit, ResourceClass},
		InheritInstitutionScope: true, InheritAcademicUnitScopes: true,
	},
	ActionInvitationCreate: {
		Action: ActionInvitationCreate, ResourceType: ResourceInstitution,
		CompatibleResourceTypes: []ResourceType{ResourceAcademicUnit, ResourceClass},
		InheritInstitutionScope: true, InheritAcademicUnitScopes: true,
	},
	ActionInvitationManage: {
		Action: ActionInvitationManage, ResourceType: ResourceInstitution,
		CompatibleResourceTypes: []ResourceType{ResourceAcademicUnit, ResourceClass},
		InheritInstitutionScope: true, InheritAcademicUnitScopes: true,
	},
	ActionOnboardingBatchView: {
		Action: ActionOnboardingBatchView, ResourceType: ResourceInstitution,
		CompatibleResourceTypes: []ResourceType{ResourceAcademicUnit},
		InheritInstitutionScope: true, InheritAcademicUnitScopes: true,
	},
	ActionOnboardingBatchManage: {
		Action: ActionOnboardingBatchManage, ResourceType: ResourceInstitution,
		CompatibleResourceTypes: []ResourceType{ResourceAcademicUnit},
		InheritInstitutionScope: true, InheritAcademicUnitScopes: true,
	},
	ActionExternalIdentityManage: {
		Action: ActionExternalIdentityManage, ResourceType: ResourceUser,
		InheritInstitutionScope: true, PersonalAccessTokenForbidden: true,
	},
}

func DefinitionForAction(action Action) (ActionDefinition, bool) {
	definition, ok := actionDefinitions[action]
	return definition, ok
}

// AllActions returns every role-grantable action currently recognized by the
// authorization evaluator in stable order. Relationship-only capabilities are
// deliberately excluded. Installation bootstrap uses this closed registry to
// construct the protected system-administrator role.
func AllActions() []string {
	actions := make([]string, 0, len(actionDefinitions))
	for action, definition := range actionDefinitions {
		if definition.RelationshipOnly {
			continue
		}
		actions = append(actions, string(action))
	}
	sort.Strings(actions)
	return actions
}

func IsKnownAction(action string) bool {
	_, ok := actionDefinitions[Action(action)]
	return ok
}

// IsGrantableAction reports whether an action may appear in reusable Roles or
// Personal Access Token scopes. Relationship-only capabilities remain known to
// strict protocol registries while never becoming transferable permission.
func IsGrantableAction(action string) bool {
	definition, ok := actionDefinitions[Action(action)]
	return ok && !definition.RelationshipOnly
}

// IsPersonalAccessTokenAction reports whether a registered role-grantable
// action may also appear in a PAT credential ceiling.
func IsPersonalAccessTokenAction(action string) bool {
	definition, ok := actionDefinitions[Action(action)]
	return ok && !definition.RelationshipOnly && !definition.PersonalAccessTokenForbidden
}

// Validate checks that the resource identifies a supported authorization target.
func (r Resource) Validate() error {
	const where = "Resource.Validate"
	if !IsValidId(r.ID) {
		return invalidModelError(where, "resource", "id", "must be a valid identifier", "")
	}
	switch r.Type {
	case ResourceInstitution, ResourceAcademicUnit, ResourceProgramme, ResourceProgrammeLevel, ResourceAcademicPeriod, ResourceClass, ResourceUser, ResourceExam, ResourceExamSitting, ResourceSubmission, ResourceMailDelivery:
		return nil
	default:
		return invalidModelError(where, "resource", "type", "has an unknown value", "id="+r.ID)
	}
}
