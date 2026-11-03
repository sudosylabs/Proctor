// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/lib/pq"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func validateStudentClassInvitationIssue(input *store.StudentClassInvitationIssue) error {
	if input == nil || input.Invitation == nil || input.Occurrence == nil || input.Delivery == nil || input.DeliveryJob == nil ||
		!input.AuditEventID.IsValid() || input.AuditAt.IsZero() {
		return store.NewErrInvalidInput("invitation", "issue", nil)
	}
	if err := input.Invitation.Validate(); err != nil {
		return store.NewErrInvalidInput("invitation", "value", err)
	}
	if input.Invitation.Purpose != model.InvitationPurposeStudentClass || input.Invitation.State != model.InvitationPending ||
		input.Occurrence.Kind != model.MailOccurrenceInvitation || input.Occurrence.TemplateKey != model.MailTemplateAccessStudentClassInvitation ||
		input.Occurrence.ActorUserID != input.Invitation.InviterUserID || input.Delivery.TargetUserID.IsValid() ||
		input.Delivery.TargetInvitationID != input.Invitation.ID || input.Delivery.TemplateKey != input.Occurrence.TemplateKey ||
		input.Delivery.Deadline.After(input.Invitation.ExpiresAt) || input.DeliveryJob.Type != model.JobTypeMailDeliverCredential {
		return store.NewErrInvalidInput("invitation", "issue_relationship", nil)
	}
	if input.Delivery.Validate() != nil || input.Occurrence.Validate() != nil || input.DeliveryJob.Validate() != nil ||
		input.Delivery.State != model.MailDeliveryQueued || input.Delivery.AttemptCount != 0 || input.Delivery.Revision != 1 ||
		input.DeliveryJob.Status != model.JobStatusQueued || input.DeliveryJob.AttemptCount != 0 ||
		input.DeliveryJob.MaximumAttempts != model.MailMaximumAttempts {
		return store.NewErrInvalidInput("invitation", "mail", nil)
	}
	command, err := model.DecodeMailDeliveryCommand(input.DeliveryJob.Command)
	if err != nil || input.Delivery.OccurrenceID != input.Occurrence.ID || input.Delivery.JobID != input.DeliveryJob.ID ||
		!input.Occurrence.CreatedAt.Equal(input.Delivery.CreatedAt) || !input.Delivery.UpdatedAt.Equal(input.Delivery.CreatedAt) ||
		!input.Delivery.MessageDate.Equal(input.Delivery.CreatedAt) || command.DeliveryID != input.Delivery.ID ||
		input.DeliveryJob.DedupeKey != input.Delivery.ID.String() {
		return store.NewErrInvalidInput("invitation", "mail_relationship", err)
	}
	return nil
}

func validateStudentClassInvitationIssueIdempotently(input *store.StudentClassInvitationIssue, command *store.CommandIdempotency) error {
	if command != nil && command.OnboardingImportID.IsValid() && input != nil && input.Invitation != nil &&
		input.Occurrence == nil && input.Delivery == nil && input.DeliveryJob == nil {
		if !input.AuditEventID.IsValid() || input.AuditAt.IsZero() || input.Invitation.Purpose != model.InvitationPurposeStudentClass ||
			input.Invitation.State != model.InvitationPending || input.Invitation.Validate() != nil {
			return store.NewErrInvalidInput("invitation", "onboarding_no_op_issue", nil)
		}
		return nil
	}
	return validateStudentClassInvitationIssue(input)
}

func validateTeacherAcademicUnitInvitationIssue(input *store.TeacherAcademicUnitInvitationIssue) error {
	if input == nil || input.Invitation == nil || input.Occurrence == nil || input.Delivery == nil || input.DeliveryJob == nil ||
		input.Lifetime != model.InvitationLifetime || !input.AuditEventID.IsValid() || input.AuditAt.IsZero() {
		return store.NewErrInvalidInput("invitation", "issue", nil)
	}
	if err := input.Invitation.Validate(); err != nil {
		return store.NewErrInvalidInput("invitation", "value", err)
	}
	if input.Invitation.Purpose != model.InvitationPurposeTeacherAcademicUnit || input.Invitation.State != model.InvitationPending ||
		input.Occurrence.Kind != model.MailOccurrenceInvitation || input.Occurrence.TemplateKey != model.MailTemplateAccessTeacherAcademicUnitInvitation ||
		input.Occurrence.ActorUserID != input.Invitation.InviterUserID || input.Delivery.TargetUserID.IsValid() ||
		input.Delivery.TargetInvitationID != input.Invitation.ID || input.Delivery.TemplateKey != input.Occurrence.TemplateKey ||
		input.Delivery.Deadline.After(input.Invitation.ExpiresAt) || input.DeliveryJob.Type != model.JobTypeMailDeliverCredential {
		return store.NewErrInvalidInput("invitation", "issue_relationship", nil)
	}
	if input.Delivery.Validate() != nil || input.Occurrence.Validate() != nil || input.DeliveryJob.Validate() != nil ||
		input.Delivery.State != model.MailDeliveryQueued || input.Delivery.AttemptCount != 0 || input.Delivery.Revision != 1 ||
		input.DeliveryJob.Status != model.JobStatusQueued || input.DeliveryJob.AttemptCount != 0 || input.DeliveryJob.MaximumAttempts != model.MailMaximumAttempts {
		return store.NewErrInvalidInput("invitation", "mail", nil)
	}
	command, err := model.DecodeMailDeliveryCommand(input.DeliveryJob.Command)
	if err != nil || input.Delivery.OccurrenceID != input.Occurrence.ID || input.Delivery.JobID != input.DeliveryJob.ID ||
		!input.Occurrence.CreatedAt.Equal(input.Delivery.CreatedAt) || !input.Delivery.UpdatedAt.Equal(input.Delivery.CreatedAt) ||
		!input.Delivery.MessageDate.Equal(input.Delivery.CreatedAt) || command.DeliveryID != input.Delivery.ID ||
		input.DeliveryJob.DedupeKey != input.Delivery.ID.String() {
		return store.NewErrInvalidInput("invitation", "mail_relationship", err)
	}
	return nil
}

func validateTeacherAcademicUnitInvitationIssueIdempotently(input *store.TeacherAcademicUnitInvitationIssue, command *store.CommandIdempotency) error {
	if command != nil && command.OnboardingImportID.IsValid() && input != nil && input.Invitation != nil &&
		input.Occurrence == nil && input.Delivery == nil && input.DeliveryJob == nil {
		if input.Lifetime != model.InvitationLifetime || !input.AuditEventID.IsValid() || input.AuditAt.IsZero() ||
			input.Invitation.Purpose != model.InvitationPurposeTeacherAcademicUnit || input.Invitation.State != model.InvitationPending ||
			input.Invitation.Validate() != nil {
			return store.NewErrInvalidInput("invitation", "onboarding_no_op_issue", nil)
		}
		return nil
	}
	return validateTeacherAcademicUnitInvitationIssue(input)
}

func validateScopedRoleInvitationIssue(input *store.ScopedRoleInvitationIssue) error {
	if input == nil || input.Invitation == nil || input.Occurrence == nil || input.Delivery == nil || input.DeliveryJob == nil ||
		input.Lifetime != model.InvitationLifetime || !input.AuditEventID.IsValid() || input.AuditAt.IsZero() {
		return store.NewErrInvalidInput("invitation", "issue", nil)
	}
	if err := input.Invitation.Validate(); err != nil {
		return store.NewErrInvalidInput("invitation", "value", err)
	}
	expectedKey := model.MailTemplateAccessAcademicUnitRoleInvitation
	if input.Invitation.Purpose == model.InvitationPurposeInstitutionRole {
		expectedKey = model.MailTemplateAccessInstitutionRoleInvitation
	}
	if (input.Invitation.Purpose != model.InvitationPurposeAcademicUnitRole && input.Invitation.Purpose != model.InvitationPurposeInstitutionRole) ||
		input.Invitation.State != model.InvitationPending || input.Occurrence.Kind != model.MailOccurrenceInvitation ||
		input.Occurrence.TemplateKey != expectedKey || input.Occurrence.ActorUserID != input.Invitation.InviterUserID ||
		input.Delivery.TargetUserID.IsValid() || input.Delivery.TargetInvitationID != input.Invitation.ID ||
		input.Delivery.TemplateKey != input.Occurrence.TemplateKey || input.Delivery.Deadline.After(input.Invitation.ExpiresAt) ||
		input.DeliveryJob.Type != model.JobTypeMailDeliverCredential {
		return store.NewErrInvalidInput("invitation", "issue_relationship", nil)
	}
	if input.Delivery.Validate() != nil || input.Occurrence.Validate() != nil || input.DeliveryJob.Validate() != nil ||
		input.Delivery.State != model.MailDeliveryQueued || input.Delivery.AttemptCount != 0 || input.Delivery.Revision != 1 ||
		input.DeliveryJob.Status != model.JobStatusQueued || input.DeliveryJob.AttemptCount != 0 || input.DeliveryJob.MaximumAttempts != model.MailMaximumAttempts {
		return store.NewErrInvalidInput("invitation", "mail", nil)
	}
	command, err := model.DecodeMailDeliveryCommand(input.DeliveryJob.Command)
	if err != nil || input.Delivery.OccurrenceID != input.Occurrence.ID || input.Delivery.JobID != input.DeliveryJob.ID ||
		!input.Occurrence.CreatedAt.Equal(input.Delivery.CreatedAt) || !input.Delivery.UpdatedAt.Equal(input.Delivery.CreatedAt) ||
		!input.Delivery.MessageDate.Equal(input.Delivery.CreatedAt) || command.DeliveryID != input.Delivery.ID ||
		input.DeliveryJob.DedupeKey != input.Delivery.ID.String() {
		return store.NewErrInvalidInput("invitation", "mail_relationship", err)
	}
	return nil
}

func validateScopedRoleInvitationIssueIdempotently(input *store.ScopedRoleInvitationIssue, command *store.CommandIdempotency) error {
	if command != nil && command.OnboardingImportID.IsValid() && input != nil && input.Invitation != nil &&
		input.Occurrence == nil && input.Delivery == nil && input.DeliveryJob == nil {
		purpose := input.Invitation.Purpose
		if input.Lifetime != model.InvitationLifetime || !input.AuditEventID.IsValid() || input.AuditAt.IsZero() ||
			(purpose != model.InvitationPurposeAcademicUnitRole && purpose != model.InvitationPurposeInstitutionRole) ||
			input.Invitation.State != model.InvitationPending || input.Invitation.Validate() != nil {
			return store.NewErrInvalidInput("invitation", "onboarding_no_op_issue", nil)
		}
		return nil
	}
	return validateScopedRoleInvitationIssue(input)
}

func teacherInvitationIssueAt(input *store.TeacherAcademicUnitInvitationIssue, invitation *model.Invitation, at time.Time) (*model.MailOccurrence, *model.MailDelivery, *model.Job, error) {
	at = model.TimeUTC(at)
	invitation.CreatedAt, invitation.UpdatedAt, invitation.ExpiresAt = at, at, at.Add(input.Lifetime)
	if err := invitation.Validate(); err != nil {
		return nil, nil, nil, store.NewErrInvalidInput("invitation", "database_lifecycle", err)
	}
	occurrence := *input.Occurrence
	occurrence.CreatedAt = at
	delivery := input.Delivery.Clone()
	delivery.CreatedAt, delivery.UpdatedAt, delivery.MessageDate, delivery.Deadline = at, at, at, invitation.ExpiresAt
	job := *input.DeliveryJob
	job.CreatedAt, job.UpdatedAt, job.AvailableAt = at, at, at
	if err := occurrence.Validate(); err != nil {
		return nil, nil, nil, store.NewErrInvalidInput("invitation", "mail_occurrence_lifecycle", err)
	}
	if err := delivery.Validate(); err != nil {
		return nil, nil, nil, store.NewErrInvalidInput("invitation", "mail_delivery_lifecycle", err)
	}
	if err := job.Validate(); err != nil {
		return nil, nil, nil, store.NewErrInvalidInput("invitation", "mail_job_lifecycle", err)
	}
	return &occurrence, delivery, &job, nil
}

func scopedRoleInvitationIssueAt(input *store.ScopedRoleInvitationIssue, invitation *model.Invitation, at time.Time) (*model.MailOccurrence, *model.MailDelivery, *model.Job, error) {
	at = model.TimeUTC(at)
	invitation.CreatedAt, invitation.UpdatedAt, invitation.ExpiresAt = at, at, at.Add(input.Lifetime)
	if err := invitation.Validate(); err != nil {
		return nil, nil, nil, store.NewErrInvalidInput("invitation", "database_lifecycle", err)
	}
	occurrence := *input.Occurrence
	occurrence.CreatedAt = at
	delivery := input.Delivery.Clone()
	delivery.CreatedAt, delivery.UpdatedAt, delivery.MessageDate, delivery.Deadline = at, at, at, invitation.ExpiresAt
	job := *input.DeliveryJob
	job.CreatedAt, job.UpdatedAt, job.AvailableAt = at, at, at
	if err := occurrence.Validate(); err != nil {
		return nil, nil, nil, store.NewErrInvalidInput("invitation", "mail_occurrence_lifecycle", err)
	}
	if err := delivery.Validate(); err != nil {
		return nil, nil, nil, store.NewErrInvalidInput("invitation", "mail_delivery_lifecycle", err)
	}
	if err := job.Validate(); err != nil {
		return nil, nil, nil, store.NewErrInvalidInput("invitation", "mail_job_lifecycle", err)
	}
	return &occurrence, delivery, &job, nil
}

func validateTeacherAcademicUnitInvitationPackage(ctx context.Context, tx *sqlxTxWrapper, invitation *model.Invitation, at time.Time) error {
	if err := lockAcademicUnitHierarchy(ctx, tx); err != nil {
		return err
	}
	var active bool
	if err := tx.Get(ctx, &active, `SELECT true FROM academic_units WHERE id=? AND archived_at IS NULL FOR SHARE`, invitation.AcademicUnitID.String()); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return store.NewErrConflict("invitation", "invitation_academic_unit", nil)
		}
		return fmt.Errorf("lock Invitation Academic Unit: %w", err)
	}
	var permissions pq.StringArray
	if err := tx.Get(ctx, &permissions, `SELECT permissions FROM roles WHERE id=? AND archived_at IS NULL AND built_in=FALSE FOR SHARE`, invitation.RoleID.String()); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return store.NewErrConflict("invitation", "invitation_role", nil)
		}
		return fmt.Errorf("lock Invitation Role: %w", err)
	}
	current := append([]string(nil), permissions...)
	slices.Sort(current)
	if !slices.Equal(current, invitation.RoleActions) || (invitation.IntendedEndsAt.Valid && !model.TimeUTC(at).Before(invitation.IntendedEndsAt.Time)) {
		return store.NewErrConflict("invitation", "invitation_role_snapshot", nil)
	}
	return nil
}

func validateScopedRoleInvitationPackage(ctx context.Context, tx *sqlxTxWrapper, invitation *model.Invitation, at time.Time) error {
	resourceType := model.ResourceAcademicUnit
	switch invitation.Purpose {
	case model.InvitationPurposeAcademicUnitRole:
		if err := lockAcademicUnitHierarchy(ctx, tx); err != nil {
			return err
		}
		var active bool
		if err := tx.Get(ctx, &active, `SELECT true FROM academic_units WHERE id=? AND archived_at IS NULL FOR SHARE`, invitation.AcademicUnitID.String()); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return store.NewErrConflict("invitation", "invitation_academic_unit", nil)
			}
			return fmt.Errorf("lock scoped Role Invitation Academic Unit: %w", err)
		}
	case model.InvitationPurposeInstitutionRole:
		resourceType = model.ResourceInstitution
		var active bool
		if err := tx.Get(ctx, &active, `SELECT true FROM institutions WHERE id=? AND archived_at IS NULL FOR SHARE`, invitation.ScopeID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return store.NewErrConflict("invitation", "invitation_institution", nil)
			}
			return fmt.Errorf("lock scoped Role Invitation Institution: %w", err)
		}
	default:
		return store.NewErrConflict("invitation", "invitation_purpose", nil)
	}
	var permissions pq.StringArray
	if err := tx.Get(ctx, &permissions, `SELECT permissions FROM roles WHERE id=? AND archived_at IS NULL AND built_in=FALSE FOR SHARE`, invitation.RoleID.String()); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return store.NewErrConflict("invitation", "invitation_role", nil)
		}
		return fmt.Errorf("lock scoped Role Invitation Role: %w", err)
	}
	current := append([]string(nil), permissions...)
	slices.Sort(current)
	if !slices.Equal(current, invitation.RoleActions) || (invitation.IntendedEndsAt.Valid && !model.TimeUTC(at).Before(invitation.IntendedEndsAt.Time)) {
		return store.NewErrConflict("invitation", "invitation_role_snapshot", nil)
	}
	scopeType := model.RoleScopeAcademicUnit
	if resourceType == model.ResourceInstitution {
		scopeType = model.RoleScopeInstitution
	}
	for _, action := range current {
		definition, ok := model.DefinitionForAction(model.Action(action))
		if !ok || definition.RelationshipOnly || !definition.SupportsRoleScope(scopeType) {
			return store.NewErrConflict("invitation", "invitation_role_snapshot", nil)
		}
	}
	return nil
}

func requireScopedRoleInvitationAuthority(ctx context.Context, tx *sqlxTxWrapper, invitation *model.Invitation, at time.Time) error {
	var active bool
	if err := tx.Get(ctx, &active, `SELECT true FROM users WHERE id=? AND archived_at IS NULL AND disabled_at IS NULL FOR SHARE`, invitation.InviterUserID.String()); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return store.NewErrConflict("invitation", "invitation_authority", nil)
		}
		return fmt.Errorf("lock scoped Role Invitation inviter: %w", err)
	}
	actions := append([]string{string(model.ActionInvitationCreate), string(model.ActionRoleBindingManage)}, invitation.RoleActions...)
	scopeType := model.RoleScopeAcademicUnit
	if invitation.Purpose == model.InvitationPurposeInstitutionRole {
		scopeType = model.RoleScopeInstitution
	}
	for index, action := range actions {
		definition, ok := model.DefinitionForAction(model.Action(action))
		if !ok || definition.RelationshipOnly || !definition.SupportsRoleScope(scopeType) {
			return store.NewErrConflict("invitation", "invitation_role_snapshot", nil)
		}
		strictParent := index >= 2 && teacherInvitationProtectedDelegationAction(model.Action(action))
		var authority bool
		var err error
		if invitation.Purpose == model.InvitationPurposeInstitutionRole {
			err = tx.Get(ctx, &authority, `SELECT true FROM role_bindings rb JOIN roles r ON r.id=rb.role_id AND r.archived_at IS NULL
				WHERE rb.user_id=$1 AND rb.archived_at IS NULL AND rb.start_at<=$2 AND (rb.end_at IS NULL OR rb.end_at>$2)
				AND rb.scope_type='institution' AND rb.scope_id=$3 AND $4=ANY(r.permissions)
				ORDER BY r.id,rb.id LIMIT 1 FOR SHARE OF r,rb`, invitation.InviterUserID.String(), model.TimeUTC(at), invitation.ScopeID, action)
		} else {
			err = tx.Get(ctx, &authority, `WITH RECURSIVE ancestors AS (
				SELECT id,parent_id,0 depth FROM academic_units WHERE id=$1 AND archived_at IS NULL
				UNION ALL SELECT au.id,au.parent_id,a.depth+1 FROM academic_units au JOIN ancestors a ON au.id=a.parent_id WHERE au.archived_at IS NULL
			)
			SELECT true FROM role_bindings rb JOIN roles r ON r.id=rb.role_id AND r.archived_at IS NULL
			WHERE rb.user_id=$2 AND rb.archived_at IS NULL AND rb.start_at<=$3 AND (rb.end_at IS NULL OR rb.end_at>$3)
			AND $4=ANY(r.permissions) AND ((rb.scope_type='institution' AND $5) OR
				(rb.scope_type='academic_unit' AND $6 AND rb.scope_id IN (SELECT id FROM ancestors WHERE NOT $7 OR depth>0)))
			ORDER BY r.id,rb.id LIMIT 1 FOR SHARE OF r,rb`, invitation.AcademicUnitID.String(), invitation.InviterUserID.String(), model.TimeUTC(at),
				action, definition.InheritInstitutionScope, definition.InheritAcademicUnitScopes, strictParent)
		}
		if errors.Is(err, sql.ErrNoRows) {
			return store.NewErrConflict("invitation", "invitation_authority", nil)
		}
		if err != nil {
			return fmt.Errorf("lock scoped Role Invitation authority for %s: %w", action, err)
		}
	}
	return nil
}

func requireTeacherAcademicUnitInvitationAuthority(ctx context.Context, tx *sqlxTxWrapper, invitation *model.Invitation, at time.Time) error {
	var active bool
	if err := tx.Get(ctx, &active, `SELECT true FROM users WHERE id=? AND archived_at IS NULL AND disabled_at IS NULL FOR SHARE`, invitation.InviterUserID.String()); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return store.NewErrConflict("invitation", "invitation_authority", nil)
		}
		return fmt.Errorf("lock Invitation inviter: %w", err)
	}
	actions := append([]string{string(model.ActionInvitationCreate), string(model.ActionAcademicUnitMembersManage)}, invitation.RoleActions...)
	for _, action := range actions {
		requirement, err := teacherInvitationDelegationRequirement(model.Action(action))
		if err != nil {
			return err
		}
		var authority bool
		err = tx.Get(ctx, &authority, `WITH RECURSIVE ancestors AS (
			SELECT id,parent_id,0 depth FROM academic_units WHERE id=$1 AND archived_at IS NULL
			UNION ALL SELECT au.id,au.parent_id,a.depth+1 FROM academic_units au JOIN ancestors a ON au.id=a.parent_id WHERE au.archived_at IS NULL
		)
		SELECT true FROM role_bindings rb JOIN roles r ON r.id=rb.role_id AND r.archived_at IS NULL
		WHERE rb.user_id=$2 AND rb.archived_at IS NULL AND rb.start_at<=$3 AND (rb.end_at IS NULL OR rb.end_at>$3)
		AND $4=ANY(r.permissions) AND ((rb.scope_type='institution' AND $5) OR
			(rb.scope_type='academic_unit' AND $6 AND rb.scope_id IN (SELECT id FROM ancestors WHERE NOT $7 OR depth>0)))
		ORDER BY r.id,rb.id LIMIT 1 FOR SHARE OF r,rb`, invitation.AcademicUnitID.String(), invitation.InviterUserID.String(), model.TimeUTC(at),
			action, requirement.Institution, requirement.AcademicUnit, requirement.StrictParent)
		if errors.Is(err, sql.ErrNoRows) {
			return store.NewErrConflict("invitation", "invitation_authority", nil)
		}
		if err != nil {
			return fmt.Errorf("lock teacher Invitation authority for %s: %w", action, err)
		}
	}
	return nil
}

// lockInvitationInviterBeforeHierarchy follows the global administrator-
// authentication fence and keeps Invitation acceptance aligned with the User
// -> mail eligibility singleton -> Class/hierarchy order used by Sitting
// reconciliation. Authority is rechecked against bindings after the hierarchy
// is locked by the purpose-specific guard.
func lockInvitationInviterBeforeHierarchy(ctx context.Context, tx *sqlxTxWrapper, inviterID model.UserID) error {
	var activeInviterID string
	if err := tx.Get(ctx, &activeInviterID, `SELECT id FROM users
		WHERE id=? AND archived_at IS NULL AND disabled_at IS NULL FOR SHARE`, inviterID.String()); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return store.NewErrConflict("invitation", "invitation_authority", nil)
		}
		return fmt.Errorf("lock Invitation inviter before hierarchy: %w", err)
	}
	return nil
}

type teacherInvitationDelegationPlan struct {
	Institution  bool
	AcademicUnit bool
	StrictParent bool
}

func teacherInvitationDelegationRequirement(action model.Action) (teacherInvitationDelegationPlan, error) {
	definition, ok := model.DefinitionForAction(action)
	if !ok || definition.RelationshipOnly || !definition.SupportsRoleScope(model.RoleScopeAcademicUnit) {
		return teacherInvitationDelegationPlan{}, store.NewErrConflict("invitation", "invitation_role_snapshot", nil)
	}
	return teacherInvitationDelegationPlan{
		Institution: definition.InheritInstitutionScope, AcademicUnit: definition.InheritAcademicUnitScopes,
		StrictParent: teacherInvitationProtectedDelegationAction(action),
	}, nil
}

func teacherInvitationProtectedDelegationAction(action model.Action) bool {
	switch action {
	case model.ActionInstitutionManage, model.ActionRoleManage, model.ActionRoleBindingManage,
		model.ActionAccessPolicyManage, model.ActionExternalIdentityManage:
		return true
	default:
		return false
	}
}

func requireInvitationPolicy(ctx context.Context, tx *sqlxTxWrapper) error {
	var allowed bool
	if err := tx.Get(ctx, &allowed, `SELECT invitation_admission_enabled AND invitation_local_credential_enabled AND local_login_enabled FROM access_policies WHERE singleton=1 FOR SHARE`); err != nil {
		return fmt.Errorf("lock invitation access policy: %w", err)
	}
	if !allowed {
		return store.NewErrConflict("invitation", "invitation_policy_disabled", nil)
	}
	return nil
}

func requireExistingUserInvitationPolicy(ctx context.Context, tx *sqlxTxWrapper) error {
	var allowed bool
	if err := tx.Get(ctx, &allowed, `SELECT invitation_admission_enabled FROM access_policies WHERE singleton=1 FOR SHARE`); err != nil {
		return fmt.Errorf("lock existing-User invitation access policy: %w", err)
	}
	if !allowed {
		return store.NewErrConflict("invitation", "invitation_policy_disabled", nil)
	}
	return nil
}

func validateStudentClassInvitationPackage(ctx context.Context, tx *sqlxTxWrapper, invitation *model.Invitation, at time.Time) error {
	if err := lockStudentClassInvitationHierarchy(ctx, tx); err != nil {
		return err
	}
	var row struct {
		AcademicPeriodID string    `db:"academic_period_id"`
		PeriodStartsAt   time.Time `db:"period_starts_at"`
		PeriodEndsAt     time.Time `db:"period_ends_at"`
	}
	if err := tx.Get(ctx, &row, `SELECT c.academic_period_id,ap.start_at period_starts_at,ap.end_at period_ends_at
		FROM classes c
		JOIN programme_levels pl ON pl.id=c.programme_level_id AND pl.archived_at IS NULL
		JOIN programmes p ON p.id=pl.programme_id AND p.archived_at IS NULL
		JOIN academic_units au ON au.id=p.academic_unit_id AND au.archived_at IS NULL
		JOIN academic_periods ap ON ap.id=c.academic_period_id AND ap.archived_at IS NULL
		WHERE c.id=? AND c.archived_at IS NULL FOR SHARE OF c,pl,p,au,ap`, invitation.ClassID.String()); err != nil {
		return translateError("class", invitation.ClassID.String(), err)
	}
	if row.AcademicPeriodID != invitation.AcademicPeriodID.String() || invitation.IntendedStartsAt.Before(row.PeriodStartsAt) ||
		!invitation.IntendedStartsAt.Before(row.PeriodEndsAt) ||
		(invitation.IntendedEndsAt.Valid && invitation.IntendedEndsAt.Time.After(row.PeriodEndsAt)) ||
		!model.TimeUTC(at).Before(row.PeriodEndsAt) {
		return store.NewErrConflict("invitation", "invitation_class_period", nil)
	}
	return nil
}

func lockStudentClassInvitationHierarchy(ctx context.Context, tx sqlxExecutor) error {
	if err := lockAcademicUnitHierarchy(ctx, tx); err != nil {
		return err
	}
	if err := lockProgrammeLifecycle(ctx, tx); err != nil {
		return err
	}
	if err := lockProgrammeLevelLifecycle(ctx, tx); err != nil {
		return err
	}
	if err := lockAcademicPeriodLifecycle(ctx, tx); err != nil {
		return err
	}
	return lockClassLifecycle(ctx, tx)
}

func requireStudentClassInvitationAuthority(ctx context.Context, tx *sqlxTxWrapper, inviterID model.UserID, classID model.ClassID, at time.Time) error {
	var active bool
	if err := tx.Get(ctx, &active, `SELECT true FROM users
		WHERE id=? AND archived_at IS NULL AND disabled_at IS NULL FOR SHARE`, inviterID.String()); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return store.NewErrConflict("invitation", "invitation_authority", nil)
		}
		return fmt.Errorf("lock Invitation inviter: %w", err)
	}
	for _, action := range []model.Action{model.ActionInvitationCreate, model.ActionClassMembersManage} {
		var authority struct {
			RoleID        string `db:"role_id"`
			RoleBindingID string `db:"role_binding_id"`
		}
		err := tx.Get(ctx, &authority, `WITH RECURSIVE class_unit AS (
			SELECT p.academic_unit_id FROM classes c
			JOIN programme_levels pl ON pl.id=c.programme_level_id AND pl.archived_at IS NULL
			JOIN programmes p ON p.id=pl.programme_id AND p.archived_at IS NULL
			WHERE c.id=$1 AND c.archived_at IS NULL
		), ancestors AS (
			SELECT academic_unit_id id FROM class_unit
			UNION ALL SELECT au.parent_id FROM academic_units au JOIN ancestors a ON au.id=a.id
			WHERE au.parent_id IS NOT NULL AND au.archived_at IS NULL
		)
		SELECT r.id role_id,rb.id role_binding_id FROM role_bindings rb
		JOIN roles r ON r.id=rb.role_id AND r.archived_at IS NULL
		WHERE rb.user_id=$2 AND rb.archived_at IS NULL AND rb.start_at<=$3
		AND (rb.end_at IS NULL OR rb.end_at>$3) AND $4=ANY(r.permissions)
		AND (rb.scope_type='institution' OR (rb.scope_type='class' AND rb.scope_id=$1)
			OR (rb.scope_type='academic_unit' AND rb.scope_id IN (SELECT id FROM ancestors)))
		ORDER BY r.id,rb.id LIMIT 1 FOR SHARE OF r,rb`, classID.String(), inviterID.String(), model.TimeUTC(at), string(action))
		if errors.Is(err, sql.ErrNoRows) {
			return store.NewErrConflict("invitation", "invitation_authority", nil)
		}
		if err != nil {
			return fmt.Errorf("lock Invitation authority for %s: %w", action, err)
		}
	}
	return nil
}

func insertInvitation(ctx context.Context, tx *sqlxTxWrapper, invitation *model.Invitation) error {
	row := newInvitationRow(invitation)
	if _, err := tx.NamedExec(ctx, `INSERT INTO invitations (`+invitationColumns+`) VALUES (
		:id,:created_at,:updated_at,:revision,:purpose,:state,:target_email,:class_id,:academic_period_id,:academic_unit_id,:role_id,:role_actions,
		:intended_start_at,:intended_end_at,:suggested_username,:suggested_display_name,:suggested_first_name,
		:suggested_last_name,:suggested_locale,:suggested_timezone,:inviter_user_id,:scope_type,:scope_id,:claim_hash,:expires_at,
		:accepted_at,:accepted_user_id,:accepted_affiliation_id,:accepted_class_member_id,:accepted_academic_unit_member_id,:accepted_role_binding_id)`, &row); err != nil {
		return fmt.Errorf("insert invitation: %w", translateError("invitation", invitation.ID.String(), err))
	}
	return nil
}

// insertInvitationMail inserts one prepared Invitation mail. An encrypted
// payload takes the primary-key fence here, as insertRecoveryMail does.
func insertInvitationMail(ctx context.Context, tx *sqlxTxWrapper, occurrence *model.MailOccurrence, delivery *model.MailDelivery, job *model.Job, payloadKeyID string) error {
	if payloadKeyID != "" {
		if err := requireMailPayloadPrimary(ctx, tx, payloadKeyID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO mail_occurrences (id,kind,template_key,actor_user_id,created_at) VALUES (?,?,?,?,?)`, occurrence.ID.String(), string(occurrence.Kind), string(occurrence.TemplateKey), occurrence.ActorUserID.String(), occurrence.CreatedAt); err != nil {
		return fmt.Errorf("insert invitation mail occurrence: %w", translateError("mail_occurrence", occurrence.ID.String(), err))
	}
	if err := insertPreparedMailJob(ctx, tx, job); err != nil {
		return fmt.Errorf("insert invitation mail job: %w", translateError("job", job.ID.String(), err))
	}
	if _, err := tx.Exec(ctx, `INSERT INTO mail_deliveries (`+mailDeliveryColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, delivery.ID.String(), delivery.OccurrenceID.String(), delivery.JobID.String(), nullableID(delivery.TargetUserID.String()), nullableID(delivery.TargetInvitationID.String()), string(delivery.TemplateKey), delivery.TemplateDigest, delivery.MaskedRecipient, string(delivery.State), delivery.CreatedAt, delivery.UpdatedAt, delivery.MessageDate, delivery.Deadline, delivery.MessageID, delivery.AttemptCount, optionalTimeValue(delivery.AcceptedAt), optionalTimeValue(delivery.FailedAt), delivery.PublicFailureCode, nullableMailPayloadKeyID(payloadKeyID), mailNullableJSON(delivery.EncryptedPayload), delivery.Revision); err != nil {
		return fmt.Errorf("insert invitation mail delivery: %w", translateError("mail_delivery", delivery.ID.String(), err))
	}
	if payloadKeyID != "" {
		return incrementMailPayloadKeyReference(ctx, tx, payloadKeyID)
	}
	return nil
}

func resolveOnboardingInvitationNoOp(ctx context.Context, tx *sqlxTxWrapper, candidate *model.Invitation) (*model.Invitation, bool, error) {
	if candidate == nil {
		return nil, false, store.NewErrInvalidInput("invitation", "onboarding_no_op", nil)
	}
	databaseNow, err := jobDatabaseNow(ctx, tx)
	if err != nil {
		return nil, false, err
	}
	switch candidate.Purpose {
	case model.InvitationPurposeStudentClass:
		err = requireStudentClassInvitationAuthority(ctx, tx, candidate.InviterUserID, candidate.ClassID, databaseNow)
	case model.InvitationPurposeTeacherAcademicUnit:
		err = requireTeacherAcademicUnitInvitationAuthority(ctx, tx, candidate, databaseNow)
	case model.InvitationPurposeAcademicUnitRole, model.InvitationPurposeInstitutionRole:
		err = requireScopedRoleInvitationAuthority(ctx, tx, candidate, databaseNow)
	default:
		return nil, false, store.NewErrInvalidInput("invitation", "purpose", candidate.Purpose)
	}
	if err != nil {
		return nil, false, err
	}
	var pending invitationRow
	switch candidate.Purpose {
	case model.InvitationPurposeStudentClass:
		err = tx.Get(ctx, &pending, `SELECT `+invitationColumns+` FROM invitations WHERE target_email=? AND purpose=? AND academic_period_id=? AND state='pending' FOR UPDATE`,
			candidate.TargetEmail, candidate.Purpose, candidate.AcademicPeriodID.String())
	case model.InvitationPurposeTeacherAcademicUnit:
		err = tx.Get(ctx, &pending, `SELECT `+invitationColumns+` FROM invitations WHERE target_email=? AND purpose=? AND academic_unit_id=? AND role_id=? AND state='pending' FOR UPDATE`,
			candidate.TargetEmail, candidate.Purpose, candidate.AcademicUnitID.String(), candidate.RoleID.String())
	case model.InvitationPurposeAcademicUnitRole, model.InvitationPurposeInstitutionRole:
		err = tx.Get(ctx, &pending, `SELECT `+invitationColumns+` FROM invitations WHERE target_email=? AND purpose=? AND role_id=? AND scope_type=? AND scope_id=? AND state='pending' FOR UPDATE`,
			candidate.TargetEmail, candidate.Purpose, candidate.RoleID.String(), candidate.ScopeType, candidate.ScopeID)
	default:
		return nil, false, store.NewErrInvalidInput("invitation", "purpose", candidate.Purpose)
	}
	if err == nil {
		existing, modelErr := pending.model()
		if modelErr != nil {
			return nil, false, modelErr
		}
		if existing.IsPendingAt(databaseNow) && existing.HasSamePackage(candidate) {
			return existing, true, nil
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	effectiveAt := candidate.EffectiveStartsAt(databaseNow)
	var satisfied bool
	switch candidate.Purpose {
	case model.InvitationPurposeStudentClass:
		err = tx.Get(ctx, &satisfied, `SELECT true FROM users u JOIN class_members cm ON cm.user_id=u.id
			WHERE u.email=? AND u.archived_at IS NULL AND u.disabled_at IS NULL AND cm.archived_at IS NULL
			AND cm.class_id=? AND cm.academic_period_id=? AND cm.start_at<=? AND (cm.end_at IS NULL OR cm.end_at>?)
			LIMIT 1 FOR SHARE OF u,cm`, candidate.TargetEmail, candidate.ClassID.String(), candidate.AcademicPeriodID.String(), effectiveAt, effectiveAt)
	case model.InvitationPurposeTeacherAcademicUnit:
		err = tx.Get(ctx, &satisfied, `SELECT true FROM users u
			JOIN affiliations a ON a.user_id=u.id AND a.kind='teacher'
			JOIN academic_unit_members m ON m.user_id=u.id AND m.academic_unit_id=?
			JOIN role_bindings rb ON rb.user_id=u.id AND rb.role_id=? AND rb.scope_type='academic_unit' AND rb.scope_id=?
			WHERE u.email=? AND u.archived_at IS NULL AND u.disabled_at IS NULL
			AND a.archived_at IS NULL AND a.start_at<=? AND (a.end_at IS NULL OR a.end_at>?)
			AND m.archived_at IS NULL AND m.start_at<=? AND (m.end_at IS NULL OR m.end_at>?)
			AND rb.archived_at IS NULL AND rb.start_at<=? AND (rb.end_at IS NULL OR rb.end_at>?)
			LIMIT 1 FOR SHARE OF u,a,m,rb`, candidate.AcademicUnitID.String(), candidate.RoleID.String(), candidate.AcademicUnitID.String(),
			candidate.TargetEmail, effectiveAt, effectiveAt, effectiveAt, effectiveAt, effectiveAt, effectiveAt)
	case model.InvitationPurposeAcademicUnitRole, model.InvitationPurposeInstitutionRole:
		err = tx.Get(ctx, &satisfied, `SELECT true FROM users u JOIN role_bindings rb ON rb.user_id=u.id
			WHERE u.email=? AND u.archived_at IS NULL AND u.disabled_at IS NULL AND rb.role_id=? AND rb.scope_type=? AND rb.scope_id=?
			AND rb.archived_at IS NULL AND rb.start_at<=? AND (rb.end_at IS NULL OR rb.end_at>?)
			LIMIT 1 FOR SHARE OF u,rb`, candidate.TargetEmail, candidate.RoleID.String(), candidate.ScopeType, candidate.ScopeID, effectiveAt, effectiveAt)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return nil, satisfied, nil
}

func terminalizeElapsedScopedRoleInvitationConflict(ctx context.Context, tx *sqlxTxWrapper, candidate *model.Invitation, at time.Time) error {
	var row invitationRow
	err := tx.Get(ctx, &row, `SELECT `+invitationColumns+` FROM invitations
		WHERE target_email=? AND role_id=? AND scope_type=? AND scope_id=? AND purpose=? AND state='pending'
		FOR UPDATE`, candidate.TargetEmail, candidate.RoleID.String(), candidate.ScopeType, candidate.ScopeID, candidate.Purpose)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("lock conflicting scoped Role Invitation: %w", err)
	}
	invitation, err := row.model()
	if err != nil {
		return err
	}
	if model.TimeUTC(at).Before(invitation.ExpiresAt) && (!invitation.IntendedEndsAt.Valid || model.TimeUTC(at).Before(invitation.IntendedEndsAt.Time)) {
		return nil
	}
	if err = invitation.Expire(invitationTransitionAt(at, invitation)); err != nil {
		return invalidPersistedState("invitation", "expiry", err)
	}
	if err = updateExpiredInvitation(ctx, tx, invitation); err != nil {
		return err
	}
	return suppressInvitationCredentialMail(ctx, tx, invitation.ID, model.MailDeliveryObsoleteCode, at)
}

func terminalizeElapsedTeacherAcademicUnitInvitationConflict(ctx context.Context, tx *sqlxTxWrapper, targetEmail string, unitID model.AcademicUnitID, roleID model.RoleID, at time.Time) error {
	var row invitationRow
	err := tx.Get(ctx, &row, `SELECT `+invitationColumns+` FROM invitations
		WHERE target_email=? AND academic_unit_id=? AND role_id=? AND purpose='teacher_academic_unit' AND state='pending'
		FOR UPDATE`, targetEmail, unitID.String(), roleID.String())
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("lock conflicting teacher Invitation: %w", err)
	}
	invitation, err := row.model()
	if err != nil {
		return err
	}
	if model.TimeUTC(at).Before(invitation.ExpiresAt) && (!invitation.IntendedEndsAt.Valid || model.TimeUTC(at).Before(invitation.IntendedEndsAt.Time)) {
		return nil
	}
	if err = invitation.Expire(invitationTransitionAt(at, invitation)); err != nil {
		return invalidPersistedState("invitation", "expiry", err)
	}
	if err = updateExpiredInvitation(ctx, tx, invitation); err != nil {
		return err
	}
	return suppressInvitationCredentialMail(ctx, tx, invitation.ID, model.MailDeliveryObsoleteCode, at)
}

func terminalizeElapsedStudentClassInvitationConflict(
	ctx context.Context,
	tx *sqlxTxWrapper,
	targetEmail string,
	periodID model.AcademicPeriodID,
	at time.Time,
) error {
	var row invitationRow
	err := tx.Get(ctx, &row, `SELECT `+invitationColumns+` FROM invitations
		WHERE target_email=? AND academic_period_id=? AND purpose='student_class' AND state='pending'
		FOR UPDATE`, targetEmail, periodID.String())
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("lock conflicting Invitation: %w", err)
	}
	invitation, err := row.model()
	if err != nil {
		return err
	}
	if model.TimeUTC(at).Before(invitation.ExpiresAt) &&
		(!invitation.IntendedEndsAt.Valid || model.TimeUTC(at).Before(invitation.IntendedEndsAt.Time)) {
		return nil
	}
	if err = invitation.Expire(invitationTransitionAt(at, invitation)); err != nil {
		return invalidPersistedState("invitation", "expiry", err)
	}
	if err = updateExpiredInvitation(ctx, tx, invitation); err != nil {
		return err
	}
	return suppressInvitationCredentialMail(ctx, tx, invitation.ID, model.MailDeliveryObsoleteCode, at)
}

// invitationTransitionAt returns the instant one PostgreSQL-timed Invitation
// transition records. Callers decide validity at databaseNow first; Invitations
// carry the issuing node's clock, and a transition never records an instant
// before the creation or last modification it supersedes.
func invitationTransitionAt(databaseNow time.Time, invitation *model.Invitation) time.Time {
	return latestTime(latestTime(databaseNow, invitation.CreatedAt), invitation.UpdatedAt)
}

func updateExpiredInvitation(ctx context.Context, tx *sqlxTxWrapper, invitation *model.Invitation) error {
	result, err := tx.Exec(ctx, `UPDATE invitations SET state=?,updated_at=?,revision=? WHERE id=? AND state='pending' AND revision=?`,
		invitation.State, invitation.UpdatedAt, invitation.Revision, invitation.ID.String(), invitation.Revision-1)
	if err != nil {
		return fmt.Errorf("terminalize Invitation: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return store.NewErrConflict("invitation", "invitation_revision", err)
	}
	return nil
}

// ResolveOnboardingInvitationNoOp reports whether issuing the candidate would
// change nothing, returning the equivalent pending Invitation when one exists.
func (s SQLInvitationStore) ResolveOnboardingInvitationNoOp(ctx context.Context, candidate *model.Invitation) (*model.Invitation, bool, error) {
	if candidate == nil || candidate.Validate() != nil {
		return nil, false, store.NewErrInvalidInput("invitation", "onboarding_no_op", nil)
	}
	result, err := runSQLTransaction(ctx, s.GetMaster().Begin, "resolve onboarding invitation no-op", func(ctx context.Context, tx *sqlxTxWrapper) (*struct {
		Invitation *model.Invitation
		NoOp       bool
	}, error) {
		invitation, noOp, err := resolveOnboardingInvitationNoOp(ctx, tx, candidate)
		return &struct {
			Invitation *model.Invitation
			NoOp       bool
		}{invitation, noOp}, err
	})
	if err != nil {
		return nil, false, err
	}
	return result.Invitation, result.NoOp, nil
}

// IssueStudentClass creates a pending student Class Invitation and queues
// its delivery.
func (s SQLInvitationStore) IssueStudentClass(ctx context.Context, input *store.StudentClassInvitationIssue) (*model.Invitation, error) {
	if err := validateStudentClassInvitationIssue(input); err != nil {
		return nil, err
	}
	invitation := *input.Invitation
	payloadKeyID, err := mailPayloadKeyID(input.Delivery.EncryptedPayload)
	if err != nil {
		return nil, store.NewErrInvalidInput("mail_delivery", "encrypted_payload", err)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "student class invitation issue", func(ctx context.Context, tx *sqlxTxWrapper) (*model.Invitation, error) {
		return issueStudentClassInvitation(ctx, tx, input, &invitation, payloadKeyID)
	})
}

// IssueStudentClassIdempotently performs IssueStudentClass under command
// idempotency, replaying the recorded outcome of an earlier identical command.
func (s SQLInvitationStore) IssueStudentClassIdempotently(ctx context.Context, input *store.StudentClassInvitationIssue, command *store.CommandIdempotency) (*store.InvitationCommandResult, error) {
	if err := validateStudentClassInvitationIssueIdempotently(input, command); err != nil || command == nil {
		return nil, store.NewErrInvalidInput("invitation", "idempotent_student_issue", err)
	}
	invitation := *input.Invitation
	payloadKeyID := ""
	hasMail := input.Delivery != nil
	if hasMail {
		var err error
		payloadKeyID, err = mailPayloadKeyID(input.Delivery.EncryptedPayload)
		if err != nil {
			return nil, store.NewErrInvalidInput("mail_delivery", "encrypted_payload", err)
		}
	}
	result, err := runIdempotentMutation(ctx, s.SQLStore, "idempotent student class invitation issue", idempotentMutation[*store.InvitationCommandResult]{
		command: command, auditEventID: input.AuditEventID,
		execute: func(ctx context.Context, tx *sqlxTxWrapper) (*store.InvitationCommandResult, error) {
			if command.OnboardingImportID.IsValid() {
				existing, noOp, noOpErr := resolveOnboardingInvitationNoOp(ctx, tx, &invitation)
				if noOpErr != nil {
					return nil, noOpErr
				}
				if noOp {
					if noOpErr = completeInvitationNoOpAudit(ctx, tx, input.AuditEventID, input.AuditAt, existing); noOpErr != nil {
						return nil, noOpErr
					}
					return &store.InvitationCommandResult{Invitation: existing, NoOp: true}, nil
				}
			}
			if !hasMail {
				return nil, store.NewErrConflict("onboarding_import_row", "mail_required", nil)
			}
			value, executeErr := issueStudentClassInvitation(ctx, tx, input, &invitation, payloadKeyID)
			return &store.InvitationCommandResult{Invitation: value}, executeErr
		},
		encode: encodeInvitationCommandOutcome, decode: decodeInvitationCommandOutcome,
		hydrateReplay:     hydrateInvitationCommandOutcome,
		onboardingOutcome: invitationOnboardingOutcome,
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, value *store.InvitationCommandResult, originalAuditID string) error {
			if value.Duplicate {
				return nil
			}
			if value.NoOp {
				return completeInvitationNoOpReplayAudit(ctx, tx, input.AuditEventID, input.AuditAt, value.Invitation, originalAuditID)
			}
			return completeInvitationReplayAudit(ctx, tx, input.AuditEventID, input.AuditAt, value.Invitation, originalAuditID)
		},
	})
	if err != nil {
		return nil, err
	}
	result.Value.Replayed = result.Replayed
	return result.Value, nil
}

func issueStudentClassInvitation(ctx context.Context, tx *sqlxTxWrapper, input *store.StudentClassInvitationIssue, invitation *model.Invitation, payloadKeyID string) (*model.Invitation, error) {
	if err := requireMailPayloadPrimary(ctx, tx, payloadKeyID); err != nil {
		return nil, err
	}
	if err := requireInvitationPolicy(ctx, tx); err != nil {
		return nil, err
	}
	databaseNow, err := jobDatabaseNow(ctx, tx)
	if err != nil {
		return nil, err
	}
	if !databaseNow.Before(invitation.ExpiresAt) || (invitation.IntendedEndsAt.Valid && !databaseNow.Before(invitation.IntendedEndsAt.Time)) {
		return nil, store.NewErrConflict("invitation", "invitation_expired", nil)
	}
	if err = terminalizeElapsedStudentClassInvitationConflict(ctx, tx, invitation.TargetEmail, invitation.AcademicPeriodID, databaseNow); err != nil {
		return nil, err
	}
	if err = validateStudentClassInvitationPackage(ctx, tx, invitation, databaseNow); err != nil {
		return nil, err
	}
	if err = requireStudentClassInvitationAuthority(ctx, tx, invitation.InviterUserID, invitation.ClassID, databaseNow); err != nil {
		return nil, err
	}
	if err := insertInvitation(ctx, tx, invitation); err != nil {
		return nil, err
	}
	if err := insertInvitationMail(ctx, tx, input.Occurrence, input.Delivery, input.DeliveryJob, payloadKeyID); err != nil {
		return nil, err
	}
	encoded, appErr := model.EncodeAuditData(invitation.Auditable())
	if appErr != nil {
		return nil, appErr
	}
	if _, err := completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt); err != nil {
		return nil, fmt.Errorf("complete invitation issue audit: %w", err)
	}
	return invitation, nil
}

// IssueTeacherAcademicUnit creates a pending teacher Academic Unit Invitation
// and queues its delivery.
func (s SQLInvitationStore) IssueTeacherAcademicUnit(ctx context.Context, input *store.TeacherAcademicUnitInvitationIssue) (*model.Invitation, error) {
	if err := validateTeacherAcademicUnitInvitationIssue(input); err != nil {
		return nil, err
	}
	invitation := *input.Invitation
	payloadKeyID, err := mailPayloadKeyID(input.Delivery.EncryptedPayload)
	if err != nil {
		return nil, store.NewErrInvalidInput("mail_delivery", "encrypted_payload", err)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "teacher academic unit invitation issue", func(ctx context.Context, tx *sqlxTxWrapper) (*model.Invitation, error) {
		return issueTeacherAcademicUnitInvitation(ctx, tx, input, &invitation, payloadKeyID)
	})
}

// IssueTeacherAcademicUnitIdempotently performs IssueTeacherAcademicUnit under
// command idempotency, replaying the recorded outcome of an earlier identical
// command.
func (s SQLInvitationStore) IssueTeacherAcademicUnitIdempotently(ctx context.Context, input *store.TeacherAcademicUnitInvitationIssue, command *store.CommandIdempotency) (*store.InvitationCommandResult, error) {
	if err := validateTeacherAcademicUnitInvitationIssueIdempotently(input, command); err != nil || command == nil {
		return nil, store.NewErrInvalidInput("invitation", "idempotent_teacher_issue", err)
	}
	invitation := *input.Invitation
	payloadKeyID := ""
	hasMail := input.Delivery != nil
	if hasMail {
		var err error
		payloadKeyID, err = mailPayloadKeyID(input.Delivery.EncryptedPayload)
		if err != nil {
			return nil, store.NewErrInvalidInput("mail_delivery", "encrypted_payload", err)
		}
	}
	result, err := runIdempotentMutation(ctx, s.SQLStore, "idempotent teacher academic unit invitation issue", idempotentMutation[*store.InvitationCommandResult]{
		command: command, auditEventID: input.AuditEventID,
		execute: func(ctx context.Context, tx *sqlxTxWrapper) (*store.InvitationCommandResult, error) {
			if command.OnboardingImportID.IsValid() {
				existing, noOp, noOpErr := resolveOnboardingInvitationNoOp(ctx, tx, &invitation)
				if noOpErr != nil {
					return nil, noOpErr
				}
				if noOp {
					if noOpErr = completeInvitationNoOpAudit(ctx, tx, input.AuditEventID, input.AuditAt, existing); noOpErr != nil {
						return nil, noOpErr
					}
					return &store.InvitationCommandResult{Invitation: existing, NoOp: true}, nil
				}
			}
			if !hasMail {
				return nil, store.NewErrConflict("onboarding_import_row", "mail_required", nil)
			}
			value, executeErr := issueTeacherAcademicUnitInvitation(ctx, tx, input, &invitation, payloadKeyID)
			return &store.InvitationCommandResult{Invitation: value}, executeErr
		},
		encode: encodeInvitationCommandOutcome, decode: decodeInvitationCommandOutcome,
		hydrateReplay:     hydrateInvitationCommandOutcome,
		onboardingOutcome: invitationOnboardingOutcome,
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, value *store.InvitationCommandResult, originalAuditID string) error {
			if value.Duplicate {
				return nil
			}
			if value.NoOp {
				return completeInvitationNoOpReplayAudit(ctx, tx, input.AuditEventID, input.AuditAt, value.Invitation, originalAuditID)
			}
			return completeInvitationReplayAudit(ctx, tx, input.AuditEventID, input.AuditAt, value.Invitation, originalAuditID)
		},
	})
	if err != nil {
		return nil, err
	}
	result.Value.Replayed = result.Replayed
	return result.Value, nil
}

func issueTeacherAcademicUnitInvitation(ctx context.Context, tx *sqlxTxWrapper, input *store.TeacherAcademicUnitInvitationIssue, invitation *model.Invitation, payloadKeyID string) (*model.Invitation, error) {
	if err := requireMailPayloadPrimary(ctx, tx, payloadKeyID); err != nil {
		return nil, err
	}
	if err := requireInvitationPolicy(ctx, tx); err != nil {
		return nil, err
	}
	databaseNow, err := jobDatabaseNow(ctx, tx)
	if err != nil {
		return nil, err
	}
	occurrence, delivery, deliveryJob, err := teacherInvitationIssueAt(input, invitation, databaseNow)
	if err != nil {
		return nil, err
	}
	if invitation.IntendedEndsAt.Valid && !databaseNow.Before(invitation.IntendedEndsAt.Time) {
		return nil, store.NewErrConflict("invitation", "invitation_expired", nil)
	}
	if err = terminalizeElapsedTeacherAcademicUnitInvitationConflict(ctx, tx, invitation.TargetEmail, invitation.AcademicUnitID, invitation.RoleID, databaseNow); err != nil {
		return nil, err
	}
	if err = validateTeacherAcademicUnitInvitationPackage(ctx, tx, invitation, databaseNow); err != nil {
		return nil, err
	}
	if err = requireTeacherAcademicUnitInvitationAuthority(ctx, tx, invitation, databaseNow); err != nil {
		return nil, err
	}
	if err = insertInvitation(ctx, tx, invitation); err != nil {
		return nil, err
	}
	if err = insertInvitationMail(ctx, tx, occurrence, delivery, deliveryJob, payloadKeyID); err != nil {
		return nil, err
	}
	encoded, appErr := model.EncodeAuditData(invitation.Auditable())
	if appErr != nil {
		return nil, appErr
	}
	if _, err = completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt); err != nil {
		return nil, fmt.Errorf("complete teacher invitation issue audit: %w", err)
	}
	return invitation, nil
}

// IssueScopedRole creates a pending Academic Unit or Institution Role
// Invitation and queues its delivery.
func (s SQLInvitationStore) IssueScopedRole(ctx context.Context, input *store.ScopedRoleInvitationIssue) (*model.Invitation, error) {
	if err := validateScopedRoleInvitationIssue(input); err != nil {
		return nil, err
	}
	invitation := *input.Invitation
	payloadKeyID, err := mailPayloadKeyID(input.Delivery.EncryptedPayload)
	if err != nil {
		return nil, store.NewErrInvalidInput("mail_delivery", "encrypted_payload", err)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "scoped Role invitation issue", func(ctx context.Context, tx *sqlxTxWrapper) (*model.Invitation, error) {
		return issueScopedRoleInvitation(ctx, tx, input, &invitation, payloadKeyID)
	})
}

// IssueScopedRoleIdempotently performs IssueScopedRole under command
// idempotency, replaying the recorded outcome of an earlier identical command.
func (s SQLInvitationStore) IssueScopedRoleIdempotently(ctx context.Context, input *store.ScopedRoleInvitationIssue, command *store.CommandIdempotency) (*store.InvitationCommandResult, error) {
	if err := validateScopedRoleInvitationIssueIdempotently(input, command); err != nil || command == nil {
		return nil, store.NewErrInvalidInput("invitation", "idempotent_role_issue", err)
	}
	invitation := *input.Invitation
	payloadKeyID := ""
	hasMail := input.Delivery != nil
	if hasMail {
		var err error
		payloadKeyID, err = mailPayloadKeyID(input.Delivery.EncryptedPayload)
		if err != nil {
			return nil, store.NewErrInvalidInput("mail_delivery", "encrypted_payload", err)
		}
	}
	result, err := runIdempotentMutation(ctx, s.SQLStore, "idempotent scoped Role invitation issue", idempotentMutation[*store.InvitationCommandResult]{
		command: command, auditEventID: input.AuditEventID,
		execute: func(ctx context.Context, tx *sqlxTxWrapper) (*store.InvitationCommandResult, error) {
			if command.OnboardingImportID.IsValid() {
				existing, noOp, noOpErr := resolveOnboardingInvitationNoOp(ctx, tx, &invitation)
				if noOpErr != nil {
					return nil, noOpErr
				}
				if noOp {
					if noOpErr = completeInvitationNoOpAudit(ctx, tx, input.AuditEventID, input.AuditAt, existing); noOpErr != nil {
						return nil, noOpErr
					}
					return &store.InvitationCommandResult{Invitation: existing, NoOp: true}, nil
				}
			}
			if !hasMail {
				return nil, store.NewErrConflict("onboarding_import_row", "mail_required", nil)
			}
			value, executeErr := issueScopedRoleInvitation(ctx, tx, input, &invitation, payloadKeyID)
			return &store.InvitationCommandResult{Invitation: value}, executeErr
		},
		encode: encodeInvitationCommandOutcome, decode: decodeInvitationCommandOutcome,
		hydrateReplay:     hydrateInvitationCommandOutcome,
		onboardingOutcome: invitationOnboardingOutcome,
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, value *store.InvitationCommandResult, originalAuditID string) error {
			if value.Duplicate {
				return nil
			}
			if value.NoOp {
				return completeInvitationNoOpReplayAudit(ctx, tx, input.AuditEventID, input.AuditAt, value.Invitation, originalAuditID)
			}
			return completeInvitationReplayAudit(ctx, tx, input.AuditEventID, input.AuditAt, value.Invitation, originalAuditID)
		},
	})
	if err != nil {
		return nil, err
	}
	result.Value.Replayed = result.Replayed
	return result.Value, nil
}

func issueScopedRoleInvitation(ctx context.Context, tx *sqlxTxWrapper, input *store.ScopedRoleInvitationIssue, invitation *model.Invitation, payloadKeyID string) (*model.Invitation, error) {
	if err := requireMailPayloadPrimary(ctx, tx, payloadKeyID); err != nil {
		return nil, err
	}
	if err := requireExistingUserInvitationPolicy(ctx, tx); err != nil {
		return nil, err
	}
	databaseNow, err := jobDatabaseNow(ctx, tx)
	if err != nil {
		return nil, err
	}
	occurrence, delivery, deliveryJob, err := scopedRoleInvitationIssueAt(input, invitation, databaseNow)
	if err != nil {
		return nil, err
	}
	if invitation.IntendedEndsAt.Valid && !databaseNow.Before(invitation.IntendedEndsAt.Time) {
		return nil, store.NewErrConflict("invitation", "invitation_expired", nil)
	}
	if err = terminalizeElapsedScopedRoleInvitationConflict(ctx, tx, invitation, databaseNow); err != nil {
		return nil, err
	}
	if err = validateScopedRoleInvitationPackage(ctx, tx, invitation, databaseNow); err != nil {
		return nil, err
	}
	if err = requireScopedRoleInvitationAuthority(ctx, tx, invitation, databaseNow); err != nil {
		return nil, err
	}
	if err = insertInvitation(ctx, tx, invitation); err != nil {
		return nil, err
	}
	if err = insertInvitationMail(ctx, tx, occurrence, delivery, deliveryJob, payloadKeyID); err != nil {
		return nil, err
	}
	encoded, appErr := model.EncodeAuditData(invitation.Auditable())
	if appErr != nil {
		return nil, appErr
	}
	if _, err = completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt); err != nil {
		return nil, fmt.Errorf("complete scoped Role invitation issue audit: %w", err)
	}
	return invitation, nil
}
