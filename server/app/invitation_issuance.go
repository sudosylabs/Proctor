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
	"github.com/sudosylabs/proctor/server/app/idempotency"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func (s *invitationService) IssueStudentClassInvitation(ctx context.Context, invocation invocation.Invocation, command IssueStudentClassInvitationCommand) (InvitationView, error) {
	classID := strings.TrimSpace(command.ClassID.String())
	parsedClassID, err := model.ParseClassID(classID)
	if err != nil {
		return InvitationView{}, apperror.New("request.invalid").WithField("field", "class_id").Wrap(err)
	}
	resource := model.Resource{Type: model.ResourceClass, ID: classID}
	if err = s.deps.Authorization.Authorize(ctx, invocation, model.ActionInvitationCreate, resource); err != nil {
		return InvitationView{}, err
	}
	if err = s.deps.Authorization.Authorize(ctx, invocation, model.ActionClassMembersManage, resource); err != nil {
		return InvitationView{}, err
	}
	class, err := s.deps.Classes.Get(ctx, parsedClassID)
	if err != nil {
		return InvitationView{}, invitationStoreErrors.Translate(err)
	}
	period, err := s.deps.Periods.Get(ctx, class.AcademicPeriodID)
	if err != nil {
		return InvitationView{}, invitationStoreErrors.Translate(err)
	}
	if class.ID != parsedClassID || period.ID != class.AcademicPeriodID {
		return InvitationView{}, apperror.New("invitation.class_period_conflict")
	}
	semantic := command
	semantic.IdempotencyKey = ""
	idempotency, err := idempotency.Prepare(invocation, "invitation.student_class.issue", command.IdempotencyKey, semantic)
	if err != nil {
		return InvitationView{}, err
	}
	bindOnboardingImportCommand(idempotency, command.onboardingImportID, command.onboardingImportRowNumber)
	attempt := mutationAttempt{Invocation: invocation, Action: model.ActionInvitationCreate, Resource: resource,
		ScopeType: model.RoleScopeClass, ScopeID: class.ID.String(), Operation: "issue_student_class",
		Value: map[string]any{"purpose": model.InvitationPurposeStudentClass, "class_id": class.ID.String(), "academic_period_id": period.ID.String()}}
	if recovered, ok, recoverErr := s.recoverInvitationIssueOutcome(ctx, idempotency, attempt); ok {
		return recovered, recoverErr
	}
	issuedAt := model.TimeUTC(s.deps.Now())
	startsAt := model.TimeFromMillis(command.IntendedStartsAt)
	if startsAt.IsZero() {
		startsAt = period.StartsAt
	}
	placeholderClaimHash := strings.Repeat("0", model.TokenHashLength)
	if command.onboardingImportID.IsValid() && !command.batchDuplicate {
		candidate, candidateErr := newStudentClassInvitationValue(invocation, command, class.ID, period.ID, startsAt, issuedAt, placeholderClaimHash)
		if candidateErr != nil {
			return InvitationView{}, candidateErr
		}
		if view, handled, noOpErr := s.resolveOnboardingInvitationNoOp(ctx, invocation, attempt, candidate, idempotency); handled {
			return view, noOpErr
		}
	}
	if !s.deps.Mail.Enabled() {
		return InvitationView{}, apperror.New("invitation.mail_unavailable")
	}
	if command.batchDuplicate {
		candidate, candidateErr := newStudentClassInvitationValue(invocation, command, class.ID, period.ID, startsAt, issuedAt, placeholderClaimHash)
		if candidateErr != nil {
			return InvitationView{}, candidateErr
		}
		attempt.Value = candidate.Auditable()
		return s.recordDuplicateInvitationView(ctx, idempotency, attempt,
			store.InvitationBatchDuplicate{Candidate: candidate}, command.batchCanonicalKey)
	}
	rawClaim := s.deps.NewClaim()
	if !model.IsValidCredentialToken(rawClaim) {
		return InvitationView{}, apperror.New("invitation.unavailable")
	}
	invitation, err := newStudentClassInvitationValue(invocation, command, class.ID, period.ID, startsAt, issuedAt, model.HashInvitationClaim(rawClaim))
	if err != nil {
		return InvitationView{}, err
	}
	actionURL, err := accountCredentialLink(s.deps.PublicURL, "/join", rawClaim)
	if err != nil {
		return InvitationView{}, apperror.New("invitation.unavailable").Wrap(err)
	}
	prepared, err := s.deps.Mail.PrepareInvitation(invitation, actionURL)
	if err != nil {
		return InvitationView{}, apperror.New("invitation.mail_unavailable").Wrap(err)
	}
	attempt.Value = invitation.Auditable()
	created, err := runAuditedMutation(ctx, s.deps.Audit, attempt,
		func() time.Time { return issuedAt }, func(ctx context.Context, reference mutationAttemptReference) (*store.InvitationCommandResult, error) {
			return s.storeStudentClassInvitation(ctx, idempotency, invitation, prepared, reference)
		}, invitationStoreErrors.Translate)
	if err != nil {
		return InvitationView{}, err
	}
	return issuedInvitationView(created), nil
}

// newStudentClassInvitationValue builds the Student Class Invitation for the command with the given claim hash.
func newStudentClassInvitationValue(invocation invocation.Invocation, command IssueStudentClassInvitationCommand, classID model.ClassID,
	periodID model.AcademicPeriodID, startsAt, issuedAt time.Time, claimHash string) (*model.Invitation, error) {
	value, err := model.NewStudentClassInvitation(model.StudentClassInvitationInput{
		ID: model.NewInvitationID(), TargetEmail: command.TargetEmail, ClassID: classID, AcademicPeriodID: periodID,
		IntendedStartsAt: startsAt, IntendedEndsAt: model.OptionalTimeFromMillis(command.IntendedEndsAt),
		Suggestions: model.InvitationProfileSuggestions{Username: command.SuggestedUsername, DisplayName: command.SuggestedDisplayName,
			FirstName: command.SuggestedFirstName, LastName: command.SuggestedLastName, Locale: command.SuggestedLocale, Timezone: command.SuggestedTimezone},
		InviterUserID: invocation.Principal().UserID, ScopeType: model.RoleScopeClass, ScopeID: classID.String(),
		ClaimHash: claimHash, IssuedAt: issuedAt,
	})
	if err != nil {
		return nil, domainInvalid("invitation.invalid", err)
	}
	return value, nil
}

// storeStudentClassInvitation records the audited Student Class Invitation issue, idempotently when a key is present.
func (s *invitationService) storeStudentClassInvitation(ctx context.Context, idempotency *store.CommandIdempotency, invitation *model.Invitation,
	prepared *store.PreparedMail, reference mutationAttemptReference) (*store.InvitationCommandResult, error) {
	input := &store.StudentClassInvitationIssue{Invitation: invitation, Occurrence: prepared.Occurrence,
		Delivery: prepared.Delivery, DeliveryJob: prepared.Job, AuditEventID: reference.ID, AuditAt: reference.MutationAt}
	if idempotency != nil {
		value, storeErr := s.deps.Store.IssueStudentClassIdempotently(ctx, input, idempotency)
		if storeErr == nil && value.Duplicate {
			return nil, apperror.New("onboarding_batch.duplicate")
		}
		return value, storeErr
	}
	value, storeErr := s.deps.Store.IssueStudentClass(ctx, input)
	return &store.InvitationCommandResult{Invitation: value}, storeErr
}

func (s *invitationService) IssueTeacherAcademicUnitInvitation(ctx context.Context, invocation invocation.Invocation, command IssueTeacherAcademicUnitInvitationCommand) (InvitationView, error) {
	unitID, err := model.ParseAcademicUnitID(strings.TrimSpace(command.AcademicUnitID.String()))
	if err != nil {
		return InvitationView{}, apperror.New("request.invalid").WithField("field", "academic_unit_id").Wrap(err)
	}
	roleID, err := model.ParseRoleID(strings.TrimSpace(command.RoleID.String()))
	if err != nil {
		return InvitationView{}, apperror.New("request.invalid").WithField("field", "role_id").Wrap(err)
	}
	resource := model.Resource{Type: model.ResourceAcademicUnit, ID: unitID.String()}
	if err = s.deps.Authorization.Authorize(ctx, invocation, model.ActionInvitationCreate, resource); err != nil {
		return InvitationView{}, err
	}
	if err = s.deps.Authorization.Authorize(ctx, invocation, model.ActionAcademicUnitMembersManage, resource); err != nil {
		return InvitationView{}, err
	}
	unit, err := s.deps.AcademicUnits.Get(ctx, unitID)
	if err != nil || unit.ID != unitID || unit.IsArchived() {
		return InvitationView{}, invitationStoreErrors.Translate(err)
	}
	role, err := s.deps.Roles.Get(ctx, roleID)
	if err != nil || role.ID != roleID || role.IsArchived() {
		return InvitationView{}, invitationStoreErrors.Translate(err)
	}
	if err = validateInvitationDelegableRole(role, model.RoleScopeAcademicUnit); err != nil {
		return InvitationView{}, err
	}
	if err = s.deps.Authorization.AuthorizeActionDelegation(ctx, invocation, role.Permissions, model.RoleScopeAcademicUnit, unitID.String()); err != nil {
		return InvitationView{}, err
	}
	semantic := command
	semantic.IdempotencyKey = ""
	idempotency, err := idempotency.Prepare(invocation, "invitation.teacher_academic_unit.issue", command.IdempotencyKey, semantic)
	if err != nil {
		return InvitationView{}, err
	}
	bindOnboardingImportCommand(idempotency, command.onboardingImportID, command.onboardingImportRowNumber)
	attempt := mutationAttempt{Invocation: invocation, Action: model.ActionInvitationCreate, Resource: resource,
		ScopeType: model.RoleScopeAcademicUnit, ScopeID: unit.ID.String(), Operation: "issue_teacher_academic_unit",
		Value: map[string]any{"purpose": model.InvitationPurposeTeacherAcademicUnit, "academic_unit_id": unit.ID.String(), "role_id": role.ID.String()}}
	if recovered, ok, recoverErr := s.recoverInvitationIssueOutcome(ctx, idempotency, attempt); ok {
		return recovered, recoverErr
	}
	issuedAt := model.TimeUTC(s.deps.Now())
	startsAt := model.TimeFromMillis(command.IntendedStartsAt)
	if startsAt.IsZero() {
		startsAt = issuedAt
	}
	placeholderClaimHash := strings.Repeat("0", model.TokenHashLength)
	if command.onboardingImportID.IsValid() && !command.batchDuplicate {
		candidate, candidateErr := newTeacherAcademicUnitInvitationValue(invocation, command, unit.ID, role, startsAt, issuedAt, placeholderClaimHash)
		if candidateErr != nil {
			return InvitationView{}, candidateErr
		}
		if view, handled, noOpErr := s.resolveOnboardingInvitationNoOp(ctx, invocation, attempt, candidate, idempotency); handled {
			return view, noOpErr
		}
	}
	if !s.deps.Mail.Enabled() {
		return InvitationView{}, apperror.New("invitation.mail_unavailable")
	}
	if command.batchDuplicate {
		candidate, candidateErr := newTeacherAcademicUnitInvitationValue(invocation, command, unit.ID, role, startsAt, issuedAt, placeholderClaimHash)
		if candidateErr != nil {
			return InvitationView{}, candidateErr
		}
		attempt.Value = candidate.Auditable()
		return s.recordDuplicateInvitationView(ctx, idempotency, attempt,
			store.InvitationBatchDuplicate{Candidate: candidate}, command.batchCanonicalKey)
	}
	rawClaim := s.deps.NewClaim()
	if !model.IsValidCredentialToken(rawClaim) {
		return InvitationView{}, apperror.New("invitation.unavailable")
	}
	invitation, err := newTeacherAcademicUnitInvitationValue(invocation, command, unit.ID, role, startsAt, issuedAt, model.HashInvitationClaim(rawClaim))
	if err != nil {
		return InvitationView{}, err
	}
	actionURL, err := accountCredentialLink(s.deps.PublicURL, "/join", rawClaim)
	if err != nil {
		return InvitationView{}, apperror.New("invitation.unavailable").Wrap(err)
	}
	prepared, err := s.deps.Mail.PrepareInvitation(invitation, actionURL)
	if err != nil {
		return InvitationView{}, apperror.New("invitation.mail_unavailable").Wrap(err)
	}
	attempt.Value = invitation.Auditable()
	created, err := runAuditedMutation(ctx, s.deps.Audit, attempt,
		func() time.Time { return issuedAt }, func(ctx context.Context, reference mutationAttemptReference) (*store.InvitationCommandResult, error) {
			return s.storeTeacherAcademicUnitInvitation(ctx, idempotency, invitation, prepared, reference)
		}, invitationStoreErrors.Translate)
	if err != nil {
		return InvitationView{}, err
	}
	return issuedInvitationView(created), nil
}

// newTeacherAcademicUnitInvitationValue builds the Teacher Academic Unit Invitation for the command with the given claim hash.
func newTeacherAcademicUnitInvitationValue(invocation invocation.Invocation, command IssueTeacherAcademicUnitInvitationCommand, unitID model.AcademicUnitID,
	role *model.Role, startsAt, issuedAt time.Time, claimHash string) (*model.Invitation, error) {
	value, err := model.NewTeacherAcademicUnitInvitation(model.TeacherAcademicUnitInvitationInput{
		ID: model.NewInvitationID(), TargetEmail: command.TargetEmail, AcademicUnitID: unitID, RoleID: role.ID,
		RoleActions: role.Permissions, IntendedStartsAt: startsAt, IntendedEndsAt: model.OptionalTimeFromMillis(command.IntendedEndsAt),
		Suggestions: model.InvitationProfileSuggestions{Username: command.SuggestedUsername, DisplayName: command.SuggestedDisplayName,
			FirstName: command.SuggestedFirstName, LastName: command.SuggestedLastName, Locale: command.SuggestedLocale, Timezone: command.SuggestedTimezone},
		InviterUserID: invocation.Principal().UserID, ScopeType: model.RoleScopeAcademicUnit, ScopeID: unitID.String(),
		ClaimHash: claimHash, IssuedAt: issuedAt,
	})
	if err != nil {
		return nil, domainInvalid("invitation.invalid", err)
	}
	return value, nil
}

// storeTeacherAcademicUnitInvitation records the audited Teacher Academic Unit Invitation issue, idempotently when a
// key is present.
func (s *invitationService) storeTeacherAcademicUnitInvitation(ctx context.Context, idempotency *store.CommandIdempotency, invitation *model.Invitation,
	prepared *store.PreparedMail, reference mutationAttemptReference) (*store.InvitationCommandResult, error) {
	input := &store.TeacherAcademicUnitInvitationIssue{Invitation: invitation,
		Lifetime:   model.InvitationLifetime,
		Occurrence: prepared.Occurrence, Delivery: prepared.Delivery, DeliveryJob: prepared.Job,
		AuditEventID: reference.ID, AuditAt: reference.MutationAt}
	if idempotency != nil {
		value, storeErr := s.deps.Store.IssueTeacherAcademicUnitIdempotently(ctx, input, idempotency)
		if storeErr == nil && value.Duplicate {
			return nil, apperror.New("onboarding_batch.duplicate")
		}
		return value, storeErr
	}
	value, storeErr := s.deps.Store.IssueTeacherAcademicUnit(ctx, input)
	return &store.InvitationCommandResult{Invitation: value}, storeErr
}

// resolveOnboardingInvitationNoOp completes an onboarding import row as a no-op when the store reports that the
// candidate Invitation would change nothing; handled reports whether the caller must return the view and error.
func (s *invitationService) resolveOnboardingInvitationNoOp(ctx context.Context, invocation invocation.Invocation, attempt mutationAttempt,
	candidate *model.Invitation, idempotency *store.CommandIdempotency) (view InvitationView, handled bool, err error) {
	_, noOp, noOpErr := s.deps.Store.ResolveOnboardingInvitationNoOp(ctx, candidate)
	if noOpErr != nil {
		return InvitationView{}, true, invitationStoreErrors.Translate(noOpErr)
	}
	if noOp {
		view, err = s.completeOnboardingInvitationNoOp(ctx, invocation, attempt, candidate, idempotency)
		return view, true, err
	}
	return InvitationView{}, false, nil
}

// issuedInvitationView maps an Invitation issue result to its view, carrying replay and no-op markers.
func issuedInvitationView(created *store.InvitationCommandResult) InvitationView {
	view := invitationView(created.Invitation)
	view.Replayed = created.Replayed
	view.NoOp = created.NoOp
	return view
}

func (s *invitationService) IssueAcademicUnitRoleInvitation(ctx context.Context, invocation invocation.Invocation, command IssueAcademicUnitRoleInvitationCommand) (InvitationView, error) {
	unitID, err := model.ParseAcademicUnitID(strings.TrimSpace(command.AcademicUnitID.String()))
	if err != nil {
		return InvitationView{}, apperror.New("request.invalid").WithField("field", "academic_unit_id").Wrap(err)
	}
	roleID, err := model.ParseRoleID(strings.TrimSpace(command.RoleID.String()))
	if err != nil {
		return InvitationView{}, apperror.New("request.invalid").WithField("field", "role_id").Wrap(err)
	}
	resource := model.Resource{Type: model.ResourceAcademicUnit, ID: unitID.String()}
	for _, action := range []model.Action{model.ActionInvitationCreate, model.ActionRoleBindingManage} {
		if err = s.deps.Authorization.Authorize(ctx, invocation, action, resource); err != nil {
			return InvitationView{}, err
		}
	}
	unit, err := s.deps.AcademicUnits.Get(ctx, unitID)
	if err != nil || unit.ID != unitID || unit.IsArchived() {
		return InvitationView{}, invitationStoreErrors.Translate(err)
	}
	role, err := s.deps.Roles.Get(ctx, roleID)
	if err != nil || role.ID != roleID || role.IsArchived() {
		return InvitationView{}, invitationStoreErrors.Translate(err)
	}
	if err = validateInvitationDelegableRole(role, model.RoleScopeAcademicUnit); err != nil {
		return InvitationView{}, err
	}
	if err = s.deps.Authorization.AuthorizeActionDelegation(ctx, invocation, role.Permissions, model.RoleScopeAcademicUnit, unitID.String()); err != nil {
		return InvitationView{}, err
	}
	semantic := command
	semantic.IdempotencyKey = ""
	idempotency, err := idempotency.Prepare(invocation, "invitation.academic_unit_role.issue", command.IdempotencyKey, semantic)
	if err != nil {
		return InvitationView{}, err
	}
	bindOnboardingImportCommand(idempotency, command.onboardingImportID, command.onboardingImportRowNumber)
	return s.issueAuthorizedScopedRole(ctx, invocation, authorizedScopedRoleInvitationIssue{
		targetEmail: command.TargetEmail, purpose: model.InvitationPurposeAcademicUnitRole,
		resource: resource, scopeType: model.RoleScopeAcademicUnit, scopeID: unit.ID.String(), operation: "issue_academic_unit_role",
		academicUnitID: unit.ID, role: role, intendedStartsAt: command.IntendedStartsAt, intendedEndsAt: command.IntendedEndsAt,
		idempotency: idempotency, batchDuplicate: command.batchDuplicate, batchCanonicalKey: command.batchCanonicalKey,
		onboardingImportID: command.onboardingImportID, onboardingImportRowNumber: command.onboardingImportRowNumber,
	})
}

func (s *invitationService) IssueInstitutionRoleInvitation(ctx context.Context, invocation invocation.Invocation, command IssueInstitutionRoleInvitationCommand) (InvitationView, error) {
	if err := requireStrongRecentSession(invocation.Principal(), s.deps.Now(), s.deps.RecentAuthenticationTTL); err != nil {
		return InvitationView{}, err
	}
	institutionID, err := model.ParseInstitutionID(strings.TrimSpace(command.InstitutionID.String()))
	if err != nil {
		return InvitationView{}, apperror.New("request.invalid").WithField("field", "institution_id").Wrap(err)
	}
	roleID, err := model.ParseRoleID(strings.TrimSpace(command.RoleID.String()))
	if err != nil {
		return InvitationView{}, apperror.New("request.invalid").WithField("field", "role_id").Wrap(err)
	}
	resource := model.Resource{Type: model.ResourceInstitution, ID: institutionID.String()}
	for _, action := range []model.Action{model.ActionInvitationCreate, model.ActionRoleBindingManage} {
		if err = s.deps.Authorization.Authorize(ctx, invocation, action, resource); err != nil {
			return InvitationView{}, err
		}
	}
	role, err := s.deps.Roles.Get(ctx, roleID)
	if err != nil || role.ID != roleID || role.IsArchived() {
		return InvitationView{}, invitationStoreErrors.Translate(err)
	}
	if err = validateInvitationDelegableRole(role, model.RoleScopeInstitution); err != nil {
		return InvitationView{}, err
	}
	if err = s.deps.Authorization.AuthorizeActionDelegation(ctx, invocation, role.Permissions, model.RoleScopeInstitution, institutionID.String()); err != nil {
		return InvitationView{}, err
	}
	semantic := command
	semantic.IdempotencyKey = ""
	idempotency, err := idempotency.Prepare(invocation, "invitation.institution_role.issue", command.IdempotencyKey, semantic)
	if err != nil {
		return InvitationView{}, err
	}
	bindOnboardingImportCommand(idempotency, command.onboardingImportID, command.onboardingImportRowNumber)
	return s.issueAuthorizedScopedRole(ctx, invocation, authorizedScopedRoleInvitationIssue{
		targetEmail: command.TargetEmail, purpose: model.InvitationPurposeInstitutionRole,
		resource: resource, scopeType: model.RoleScopeInstitution, scopeID: institutionID.String(), operation: "issue_institution_role",
		role: role, intendedStartsAt: command.IntendedStartsAt, intendedEndsAt: command.IntendedEndsAt,
		idempotency: idempotency, batchDuplicate: command.batchDuplicate, batchCanonicalKey: command.batchCanonicalKey,
		onboardingImportID: command.onboardingImportID, onboardingImportRowNumber: command.onboardingImportRowNumber,
	})
}

func (s *invitationService) issueAuthorizedScopedRole(ctx context.Context, invocation invocation.Invocation, issue authorizedScopedRoleInvitationIssue) (InvitationView, error) {
	attempt := mutationAttempt{Invocation: invocation, Action: model.ActionInvitationCreate, Resource: issue.resource,
		ScopeType: issue.scopeType, ScopeID: issue.scopeID, Operation: issue.operation,
		Value: map[string]any{"purpose": issue.purpose, "academic_unit_id": issue.academicUnitID.String(), "role_id": issue.role.ID.String()}}
	if recovered, ok, recoverErr := s.recoverInvitationIssueOutcome(ctx, issue.idempotency, attempt); ok {
		return recovered, recoverErr
	}
	issuedAt := model.TimeUTC(s.deps.Now())
	startsAt := model.TimeFromMillis(issue.intendedStartsAt)
	if startsAt.IsZero() {
		startsAt = issuedAt
	}
	if issue.onboardingImportID.IsValid() && !issue.batchDuplicate {
		candidate, candidateErr := model.NewScopedRoleInvitation(model.ScopedRoleInvitationInput{
			ID: model.NewInvitationID(), Purpose: issue.purpose,
			TargetEmail: issue.targetEmail, AcademicUnitID: issue.academicUnitID, RoleID: issue.role.ID, RoleActions: issue.role.Permissions,
			IntendedStartsAt: startsAt, IntendedEndsAt: model.OptionalTimeFromMillis(issue.intendedEndsAt),
			InviterUserID: invocation.Principal().UserID, ScopeType: issue.scopeType, ScopeID: issue.scopeID,
			ClaimHash: strings.Repeat("0", model.TokenHashLength), IssuedAt: issuedAt,
		})
		if candidateErr != nil {
			return InvitationView{}, domainInvalid("invitation.invalid", candidateErr)
		}
		_, noOp, noOpErr := s.deps.Store.ResolveOnboardingInvitationNoOp(ctx, candidate)
		if noOpErr != nil {
			return InvitationView{}, invitationStoreErrors.Translate(noOpErr)
		}
		if noOp {
			return s.completeOnboardingInvitationNoOp(ctx, invocation, attempt, candidate, issue.idempotency)
		}
	}
	if !s.deps.Mail.Enabled() {
		return InvitationView{}, apperror.New("invitation.mail_unavailable")
	}
	if issue.batchDuplicate {
		candidate, candidateErr := model.NewScopedRoleInvitation(model.ScopedRoleInvitationInput{
			ID: model.NewInvitationID(), Purpose: issue.purpose,
			TargetEmail: issue.targetEmail, AcademicUnitID: issue.academicUnitID, RoleID: issue.role.ID, RoleActions: issue.role.Permissions,
			IntendedStartsAt: startsAt, IntendedEndsAt: model.OptionalTimeFromMillis(issue.intendedEndsAt),
			InviterUserID: invocation.Principal().UserID, ScopeType: issue.scopeType, ScopeID: issue.scopeID,
			ClaimHash: strings.Repeat("0", model.TokenHashLength), IssuedAt: issuedAt,
		})
		if candidateErr != nil {
			return InvitationView{}, domainInvalid("invitation.invalid", candidateErr)
		}
		attempt.Value = candidate.Auditable()
		return s.recordDuplicateInvitationView(ctx, issue.idempotency, attempt,
			store.InvitationBatchDuplicate{Candidate: candidate}, issue.batchCanonicalKey)
	}
	rawClaim := s.deps.NewClaim()
	if !model.IsValidCredentialToken(rawClaim) {
		return InvitationView{}, apperror.New("invitation.unavailable")
	}
	invitation, err := model.NewScopedRoleInvitation(model.ScopedRoleInvitationInput{
		ID: model.NewInvitationID(), Purpose: issue.purpose,
		TargetEmail: issue.targetEmail, AcademicUnitID: issue.academicUnitID, RoleID: issue.role.ID, RoleActions: issue.role.Permissions,
		IntendedStartsAt: startsAt, IntendedEndsAt: model.OptionalTimeFromMillis(issue.intendedEndsAt),
		InviterUserID: invocation.Principal().UserID, ScopeType: issue.scopeType, ScopeID: issue.scopeID,
		ClaimHash: model.HashInvitationClaim(rawClaim), IssuedAt: issuedAt,
	})
	if err != nil {
		return InvitationView{}, domainInvalid("invitation.invalid", err)
	}
	actionURL, err := accountCredentialLink(s.deps.PublicURL, "/join", rawClaim)
	if err != nil {
		return InvitationView{}, apperror.New("invitation.unavailable").Wrap(err)
	}
	prepared, err := s.deps.Mail.PrepareInvitation(invitation, actionURL)
	if err != nil {
		return InvitationView{}, apperror.New("invitation.mail_unavailable").Wrap(err)
	}
	attempt.Value = invitation.Auditable()
	created, err := runAuditedMutation(ctx, s.deps.Audit, attempt,
		func() time.Time { return issuedAt }, func(ctx context.Context, reference mutationAttemptReference) (*store.InvitationCommandResult, error) {
			input := &store.ScopedRoleInvitationIssue{Invitation: invitation, Lifetime: model.InvitationLifetime,
				Occurrence: prepared.Occurrence, Delivery: prepared.Delivery, DeliveryJob: prepared.Job,
				AuditEventID: reference.ID, AuditAt: reference.MutationAt}
			if issue.idempotency != nil {
				value, storeErr := s.deps.Store.IssueScopedRoleIdempotently(ctx, input, issue.idempotency)
				if storeErr == nil && value.Duplicate {
					return nil, apperror.New("onboarding_batch.duplicate")
				}
				return value, storeErr
			}
			value, storeErr := s.deps.Store.IssueScopedRole(ctx, input)
			return &store.InvitationCommandResult{Invitation: value}, storeErr
		}, invitationStoreErrors.Translate)
	if err != nil {
		return InvitationView{}, err
	}
	view := invitationView(created.Invitation)
	view.Replayed = created.Replayed
	view.NoOp = created.NoOp
	return view, nil
}

func (s *invitationService) completeOnboardingInvitationNoOp(ctx context.Context, invocation invocation.Invocation, attempt mutationAttempt,
	candidate *model.Invitation, idempotency *store.CommandIdempotency) (InvitationView, error) {
	if candidate == nil || idempotency == nil || !idempotency.OnboardingImportID.IsValid() {
		return InvitationView{}, apperror.New("invitation.unavailable")
	}
	attempt.Value = candidate.Auditable()
	created, err := runAuditedMutation(ctx, s.deps.Audit, attempt,
		func() time.Time { return candidate.CreatedAt }, func(ctx context.Context, reference mutationAttemptReference) (*store.InvitationCommandResult, error) {
			switch candidate.Purpose {
			case model.InvitationPurposeStudentClass:
				return s.deps.Store.IssueStudentClassIdempotently(ctx, &store.StudentClassInvitationIssue{Invitation: candidate,
					AuditEventID: reference.ID, AuditAt: reference.MutationAt}, idempotency)
			case model.InvitationPurposeTeacherAcademicUnit:
				return s.deps.Store.IssueTeacherAcademicUnitIdempotently(ctx, &store.TeacherAcademicUnitInvitationIssue{Invitation: candidate,
					Lifetime: model.InvitationLifetime, AuditEventID: reference.ID, AuditAt: reference.MutationAt}, idempotency)
			case model.InvitationPurposeAcademicUnitRole, model.InvitationPurposeInstitutionRole:
				return s.deps.Store.IssueScopedRoleIdempotently(ctx, &store.ScopedRoleInvitationIssue{Invitation: candidate,
					Lifetime: model.InvitationLifetime, AuditEventID: reference.ID, AuditAt: reference.MutationAt}, idempotency)
			default:
				return nil, store.NewErrInvalidInput("invitation", "purpose", candidate.Purpose)
			}
		}, invitationStoreErrors.Translate)
	if err != nil {
		return InvitationView{}, err
	}
	view := invitationView(created.Invitation)
	view.Replayed = created.Replayed
	view.NoOp = created.NoOp
	return view, nil
}

func validateInvitationDelegableRole(role *model.Role, scopeType model.RoleScopeType) error {
	if role == nil || role.IsArchived() || role.BuiltIn || role.Name == model.SystemAdministratorRoleName || len(role.Permissions) == 0 {
		return apperror.New("invitation.role_not_delegable")
	}
	for _, action := range role.Permissions {
		definition, ok := model.DefinitionForAction(model.Action(action))
		if !ok || definition.RelationshipOnly || !definition.SupportsRoleScope(scopeType) {
			return apperror.New("invitation.role_not_delegable")
		}
	}
	return nil
}
