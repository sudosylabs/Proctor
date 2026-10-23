// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func (s *invitationService) ReplaceInvitation(ctx context.Context, invocation invocation.Invocation, command ReplaceInvitationCommand) (InvitationAdministrationView, error) {
	id := command.ID
	if !id.IsValid() || command.ExpectedRevision < 1 {
		return InvitationAdministrationView{}, apperror.New("request.invalid").WithField("field", "invitation_id")
	}
	visibility, err := s.deps.Authorization.Visibility(ctx, invocation, model.ActionInvitationManage)
	if err != nil {
		return InvitationAdministrationView{}, err
	}
	current, err := s.deps.Store.GetForAdministration(ctx, id, visibility)
	if err != nil {
		return InvitationAdministrationView{}, invitationStoreErrors.Translate(err)
	}
	if !s.deps.Mail.Enabled() {
		return InvitationAdministrationView{}, apperror.New("invitation.mail_unavailable")
	}
	replacement, rawClaim, err := s.prepareReplacement(ctx, invocation, command)
	if err != nil {
		return InvitationAdministrationView{}, err
	}
	actionURL, err := accountCredentialLink(s.deps.PublicURL, "/join", rawClaim)
	if err != nil {
		return InvitationAdministrationView{}, apperror.New("invitation.unavailable").Wrap(err)
	}
	prepared, err := s.deps.Mail.PrepareInvitation(replacement, actionURL)
	if err != nil {
		return InvitationAdministrationView{}, apperror.New("invitation.mail_unavailable").Wrap(err)
	}
	currentAttempt := mutationAttempt{Invocation: invocation, Action: model.ActionInvitationManage,
		Resource: invitationResource(current.Invitation), ScopeType: current.Invitation.ScopeType, ScopeID: current.Invitation.ScopeID,
		Operation: "supersede", Value: current.Invitation.Auditable()}
	replacementAttempt := mutationAttempt{Invocation: invocation, Action: model.ActionInvitationCreate,
		Resource: invitationResource(replacement), ScopeType: replacement.ScopeType, ScopeID: replacement.ScopeID,
		Operation: "replacement_issue", Value: replacement.Auditable()}
	result, err := runAuditedMutation(ctx, s.deps.Audit, currentAttempt, s.deps.Now,
		func(ctx context.Context, currentReference mutationAttemptReference) (*store.InvitationAdministrationRecord, error) {
			return runAuditedMutation(ctx, s.deps.Audit, replacementAttempt,
				func() time.Time { return model.TimeUTC(currentReference.MutationAt) },
				func(ctx context.Context, replacementReference mutationAttemptReference) (*store.InvitationAdministrationRecord, error) {
					return s.deps.Store.Replace(ctx, &store.InvitationReplacement{CurrentID: id, ExpectedRevision: command.ExpectedRevision,
						Replacement: replacement, Lifetime: model.InvitationLifetime, Occurrence: prepared.Occurrence,
						Delivery: prepared.Delivery, DeliveryJob: prepared.Job, ActorUserID: invocation.Principal().UserID,
						CurrentAuditEventID: currentReference.ID, ReplacementAuditEventID: replacementReference.ID,
						AuditAt: currentReference.MutationAt})
				}, invitationStoreErrors.Translate)
		}, invitationStoreErrors.Translate)
	if err != nil {
		return InvitationAdministrationView{}, err
	}
	return invitationAdministrationView(result), nil
}

func (s *invitationService) prepareReplacement(ctx context.Context, invocation invocation.Invocation, command ReplaceInvitationCommand) (*model.Invitation, string, error) {
	purpose := model.InvitationPurpose(strings.TrimSpace(command.Purpose))
	if !purpose.IsValid() {
		return nil, "", apperror.New("request.invalid").WithField("field", "purpose")
	}
	at := model.TimeUTC(s.deps.Now())
	rawClaim := s.deps.NewClaim()
	if !model.IsValidCredentialToken(rawClaim) {
		return nil, "", apperror.New("invitation.unavailable")
	}
	startsAt := model.TimeFromMillis(command.IntendedStartsAt)
	endsAt := model.OptionalTimeFromMillis(command.IntendedEndsAt)
	actor := invocation.Principal().UserID
	switch purpose {
	case model.InvitationPurposeStudentClass:
		classID, err := model.ParseClassID(strings.TrimSpace(command.ClassID.String()))
		if err != nil {
			return nil, "", apperror.New("request.invalid").WithField("field", "class_id").Wrap(err)
		}
		resource := model.Resource{Type: model.ResourceClass, ID: classID.String()}
		for _, action := range []model.Action{model.ActionInvitationCreate, model.ActionClassMembersManage} {
			if err = s.deps.Authorization.Authorize(ctx, invocation, action, resource); err != nil {
				return nil, "", err
			}
		}
		class, err := s.deps.Classes.Get(ctx, classID)
		if err != nil || class.ID != classID {
			return nil, "", invitationStoreErrors.Translate(err)
		}
		period, err := s.deps.Periods.Get(ctx, class.AcademicPeriodID)
		if err != nil || period.ID != class.AcademicPeriodID {
			return nil, "", invitationStoreErrors.Translate(err)
		}
		if startsAt.IsZero() {
			startsAt = period.StartsAt
		}
		value, err := model.NewStudentClassInvitation(model.StudentClassInvitationInput{ID: model.NewInvitationID(),
			TargetEmail: command.TargetEmail, ClassID: class.ID, AcademicPeriodID: period.ID,
			IntendedStartsAt: startsAt, IntendedEndsAt: endsAt,
			Suggestions: model.InvitationProfileSuggestions{Username: command.SuggestedUsername, DisplayName: command.SuggestedDisplayName,
				FirstName: command.SuggestedFirstName, LastName: command.SuggestedLastName, Locale: command.SuggestedLocale, Timezone: command.SuggestedTimezone},
			InviterUserID: actor, ScopeType: model.RoleScopeClass, ScopeID: class.ID.String(),
			ClaimHash: model.HashInvitationClaim(rawClaim), IssuedAt: at})
		if err != nil {
			return nil, "", domainInvalid("invitation.invalid", err)
		}
		return value, rawClaim, nil
	case model.InvitationPurposeTeacherAcademicUnit, model.InvitationPurposeAcademicUnitRole:
		unitID, err := model.ParseAcademicUnitID(strings.TrimSpace(command.AcademicUnitID.String()))
		if err != nil {
			return nil, "", apperror.New("request.invalid").WithField("field", "academic_unit_id").Wrap(err)
		}
		roleID, err := model.ParseRoleID(strings.TrimSpace(command.RoleID.String()))
		if err != nil {
			return nil, "", apperror.New("request.invalid").WithField("field", "role_id").Wrap(err)
		}
		resource := model.Resource{Type: model.ResourceAcademicUnit, ID: unitID.String()}
		required := []model.Action{model.ActionInvitationCreate, model.ActionAcademicUnitMembersManage}
		if purpose == model.InvitationPurposeAcademicUnitRole {
			required[1] = model.ActionRoleBindingManage
		}
		for _, action := range required {
			if err = s.deps.Authorization.Authorize(ctx, invocation, action, resource); err != nil {
				return nil, "", err
			}
		}
		unit, err := s.deps.AcademicUnits.Get(ctx, unitID)
		if err != nil || unit.ID != unitID || unit.IsArchived() {
			return nil, "", invitationStoreErrors.Translate(err)
		}
		role, err := s.deps.Roles.Get(ctx, roleID)
		if err != nil || role.ID != roleID || role.IsArchived() {
			return nil, "", invitationStoreErrors.Translate(err)
		}
		if err = validateInvitationDelegableRole(role, model.RoleScopeAcademicUnit); err != nil {
			return nil, "", err
		}
		if err = s.deps.Authorization.AuthorizeActionDelegation(ctx, invocation, role.Permissions, model.RoleScopeAcademicUnit, unitID.String()); err != nil {
			return nil, "", err
		}
		if startsAt.IsZero() {
			startsAt = at
		}
		if purpose == model.InvitationPurposeTeacherAcademicUnit {
			value, modelErr := model.NewTeacherAcademicUnitInvitation(model.TeacherAcademicUnitInvitationInput{ID: model.NewInvitationID(),
				TargetEmail: command.TargetEmail, AcademicUnitID: unit.ID, RoleID: role.ID, RoleActions: role.Permissions,
				IntendedStartsAt: startsAt, IntendedEndsAt: endsAt,
				Suggestions: model.InvitationProfileSuggestions{Username: command.SuggestedUsername, DisplayName: command.SuggestedDisplayName,
					FirstName: command.SuggestedFirstName, LastName: command.SuggestedLastName, Locale: command.SuggestedLocale, Timezone: command.SuggestedTimezone},
				InviterUserID: actor, ScopeType: model.RoleScopeAcademicUnit, ScopeID: unit.ID.String(),
				ClaimHash: model.HashInvitationClaim(rawClaim), IssuedAt: at})
			if modelErr != nil {
				return nil, "", domainInvalid("invitation.invalid", modelErr)
			}
			return value, rawClaim, nil
		}
		value, modelErr := model.NewScopedRoleInvitation(model.ScopedRoleInvitationInput{ID: model.NewInvitationID(), Purpose: purpose,
			TargetEmail: command.TargetEmail, AcademicUnitID: unit.ID, RoleID: role.ID, RoleActions: role.Permissions,
			IntendedStartsAt: startsAt, IntendedEndsAt: endsAt, InviterUserID: actor,
			ScopeType: model.RoleScopeAcademicUnit, ScopeID: unit.ID.String(), ClaimHash: model.HashInvitationClaim(rawClaim), IssuedAt: at})
		if modelErr != nil {
			return nil, "", domainInvalid("invitation.invalid", modelErr)
		}
		return value, rawClaim, nil
	case model.InvitationPurposeInstitutionRole:
		if err := requireStrongRecentSession(invocation.Principal(), s.deps.Now(), s.deps.RecentAuthenticationTTL); err != nil {
			return nil, "", err
		}
		institutionID, err := model.ParseInstitutionID(strings.TrimSpace(command.InstitutionID.String()))
		if err != nil {
			return nil, "", apperror.New("request.invalid").WithField("field", "institution_id").Wrap(err)
		}
		roleID, err := model.ParseRoleID(strings.TrimSpace(command.RoleID.String()))
		if err != nil {
			return nil, "", apperror.New("request.invalid").WithField("field", "role_id").Wrap(err)
		}
		resource := model.Resource{Type: model.ResourceInstitution, ID: institutionID.String()}
		for _, action := range []model.Action{model.ActionInvitationCreate, model.ActionRoleBindingManage} {
			if err = s.deps.Authorization.Authorize(ctx, invocation, action, resource); err != nil {
				return nil, "", err
			}
		}
		role, err := s.deps.Roles.Get(ctx, roleID)
		if err != nil || role.ID != roleID || role.IsArchived() {
			return nil, "", invitationStoreErrors.Translate(err)
		}
		if err = validateInvitationDelegableRole(role, model.RoleScopeInstitution); err != nil {
			return nil, "", err
		}
		if err = s.deps.Authorization.AuthorizeActionDelegation(ctx, invocation, role.Permissions, model.RoleScopeInstitution, institutionID.String()); err != nil {
			return nil, "", err
		}
		if startsAt.IsZero() {
			startsAt = at
		}
		value, modelErr := model.NewScopedRoleInvitation(model.ScopedRoleInvitationInput{ID: model.NewInvitationID(), Purpose: purpose,
			TargetEmail: command.TargetEmail, RoleID: role.ID, RoleActions: role.Permissions, IntendedStartsAt: startsAt,
			IntendedEndsAt: endsAt, InviterUserID: actor, ScopeType: model.RoleScopeInstitution, ScopeID: institutionID.String(),
			ClaimHash: model.HashInvitationClaim(rawClaim), IssuedAt: at})
		if modelErr != nil {
			return nil, "", domainInvalid("invitation.invalid", modelErr)
		}
		return value, rawClaim, nil
	default:
		return nil, "", apperror.New("request.invalid").WithField("field", "purpose")
	}
}
