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
	"strings"
	"time"

	"github.com/lib/pq"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// AcceptScopedRole consumes the pending Academic Unit or Institution Role
// Invitation named by the claim hash for an existing User, granting its Role
// Binding, or replays an accepted one.
func (s SQLInvitationStore) AcceptScopedRole(ctx context.Context, input *store.ScopedRoleInvitationAcceptance) (*store.ScopedRoleInvitationAcceptanceResult, error) {
	if err := validateScopedRoleInvitationAcceptance(input); err != nil {
		return nil, err
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "scoped Role invitation acceptance", func(ctx context.Context, tx *sqlxTxWrapper) (*store.ScopedRoleInvitationAcceptanceResult, error) {
		var row invitationRow
		if err := tx.Get(ctx, &row, `SELECT `+invitationColumns+` FROM invitations WHERE claim_hash=? FOR UPDATE`, input.ClaimHash); err != nil {
			return nil, translateError("invitation", "claim", err)
		}
		invitation, err := row.model()
		if err != nil || (invitation.Purpose != model.InvitationPurposeAcademicUnitRole && invitation.Purpose != model.InvitationPurposeInstitutionRole) {
			return nil, store.NewErrConflict("invitation", "invitation_not_pending", err)
		}
		if invitation.State == model.InvitationAccepted {
			if invitation.AcceptedUserID != input.UserID {
				return nil, store.NewErrConflict("invitation", "invitation_not_pending", nil)
			}
			result, replayErr := replayScopedRoleInvitation(ctx, tx, invitation)
			if replayErr != nil {
				return nil, replayErr
			}
			if replayErr = completeScopedRoleInvitationAcceptanceAudit(ctx, tx, input, invitation); replayErr != nil {
				return nil, replayErr
			}
			if replayErr = completeBrowserInvitationTransaction(ctx, tx, input.BrowserTransaction, invitation, result.User.ID); replayErr != nil {
				return nil, replayErr
			}
			return result, nil
		}
		databaseNow, err := jobDatabaseNow(ctx, tx)
		if err != nil {
			return nil, err
		}
		if invitation.State != model.InvitationPending || !databaseNow.Before(invitation.ExpiresAt) ||
			(invitation.IntendedEndsAt.Valid && !databaseNow.Before(invitation.IntendedEndsAt.Time)) {
			return nil, store.NewErrConflict("invitation", "invitation_not_pending", nil)
		}
		if err = lockSystemAdministratorAuthenticationPaths(ctx, tx); err != nil {
			return nil, err
		}
		if err = requireExistingUserInvitationPolicy(ctx, tx); err != nil {
			return nil, err
		}
		if err = lockInvitationInviterBeforeHierarchy(ctx, tx, invitation.InviterUserID); err != nil {
			return nil, err
		}
		var persistedUser userRow
		if err = tx.Get(ctx, &persistedUser, `SELECT `+strings.Join(userSliceColumns(), ",")+` FROM users
			WHERE id=? AND archived_at IS NULL AND disabled_at IS NULL FOR UPDATE`, input.UserID.String()); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, store.NewErrConflict("invitation", "invitation_user_unavailable", nil)
			}
			return nil, fmt.Errorf("lock scoped Role Invitation User: %w", err)
		}
		user, err := persistedUser.model()
		if err != nil {
			return nil, err
		}
		if err = validateScopedRoleInvitationPackage(ctx, tx, invitation, databaseNow); err != nil {
			return nil, err
		}
		if err = requireScopedRoleInvitationAuthority(ctx, tx, invitation, databaseNow); err != nil {
			return nil, err
		}
		effectiveStart := invitation.EffectiveStartsAt(databaseNow)
		binding, err := ensureScopedRoleInvitationBinding(ctx, tx, invitation, input.RoleBinding, user.ID, effectiveStart, databaseNow)
		if err != nil {
			return nil, err
		}
		if err = invitation.AcceptScopedRole(user.ID, binding.ID, invitationTransitionAt(databaseNow, invitation)); err != nil {
			return nil, store.NewErrConflict("invitation", "invitation_not_pending", err)
		}
		result, err := tx.Exec(ctx, `UPDATE invitations SET state=?,accepted_at=?,accepted_user_id=?,accepted_role_binding_id=?,updated_at=?,revision=?
			WHERE id=? AND state='pending' AND revision=?`, invitation.State, invitation.AcceptedAt.Time, user.ID.String(), binding.ID.String(),
			invitation.UpdatedAt, invitation.Revision, invitation.ID.String(), invitation.Revision-1)
		if err != nil {
			return nil, fmt.Errorf("consume scoped Role Invitation: %w", err)
		}
		if affected, affectedErr := result.RowsAffected(); affectedErr != nil || affected != 1 {
			return nil, store.NewErrConflict("invitation", "invitation_revision", affectedErr)
		}
		if err = suppressInvitationCredentialMail(ctx, tx, invitation.ID, model.MailDeliveryObsoleteCode, databaseNow); err != nil {
			return nil, err
		}
		if err = completeScopedRoleInvitationAcceptanceAudit(ctx, tx, input, invitation); err != nil {
			return nil, err
		}
		if err = completeBrowserInvitationTransaction(ctx, tx, input.BrowserTransaction, invitation, user.ID); err != nil {
			return nil, err
		}
		return &store.ScopedRoleInvitationAcceptanceResult{Invitation: invitation, User: user, RoleBinding: binding}, nil
	})
}

func validateScopedRoleInvitationAcceptance(input *store.ScopedRoleInvitationAcceptance) error {
	if input == nil || !model.IsValidTokenHash(input.ClaimHash) || !input.UserID.IsValid() || input.RoleBinding == nil ||
		!input.AuditEventID.IsValid() || input.AuditAt.IsZero() {
		return store.NewErrInvalidInput("invitation", "acceptance", nil)
	}
	if !slices.Equal(input.RequiredActions, []model.Action{model.ActionInvitationCreate, model.ActionRoleBindingManage}) ||
		input.RoleBinding.UserID != input.UserID || !input.RoleBinding.OriginInvitationID.IsValid() ||
		input.RoleBinding.OriginAcademicUnitMemberID.IsValid() || input.RoleBinding.Validate() != nil {
		return store.NewErrInvalidInput("invitation", "acceptance_relationship", nil)
	}
	return nil
}

func completeScopedRoleInvitationAcceptanceAudit(ctx context.Context, tx *sqlxTxWrapper,
	input *store.ScopedRoleInvitationAcceptance, invitation *model.Invitation,
) error {
	encoded, err := model.EncodeAuditData(invitation.Auditable())
	if err != nil {
		return err
	}
	if _, err = completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt); err != nil {
		return fmt.Errorf("complete scoped Role Invitation acceptance audit: %w", err)
	}
	return nil
}

func ensureScopedRoleInvitationBinding(ctx context.Context, tx *sqlxTxWrapper, invitation *model.Invitation, prepared *model.RoleBinding,
	userID model.UserID, effectiveStart, at time.Time,
) (*model.RoleBinding, error) {
	var row roleBindingRow
	err := tx.Get(ctx, &row, `SELECT id,created_at,updated_at,archived_at,user_id,role_id,origin_invitation_id,origin_academic_unit_member_id,scope_type,scope_id,start_at,end_at
		FROM role_bindings WHERE user_id=? AND role_id=? AND scope_type=? AND scope_id=? AND archived_at IS NULL
		AND start_at<=? AND (end_at IS NULL OR end_at>?) ORDER BY start_at DESC,id LIMIT 1 FOR UPDATE`,
		userID.String(), invitation.RoleID.String(), invitation.ScopeType, invitation.ScopeID, effectiveStart, effectiveStart)
	if err == nil {
		existing, modelErr := row.model()
		if modelErr != nil {
			return nil, modelErr
		}
		if !invitationEffectiveIntervalCovered(existing.StartsAt, existing.EndsAt, effectiveStart, invitation.IntendedEndsAt) {
			return nil, store.NewErrConflict("invitation", "invitation_role_binding_conflict", nil)
		}
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("find scoped Invitation Role Binding: %w", err)
	}
	candidate := *prepared
	candidate.UserID, candidate.RoleID, candidate.OriginInvitationID = userID, invitation.RoleID, invitation.ID
	candidate.OriginAcademicUnitMemberID = ""
	candidate.ScopeType, candidate.ScopeID, candidate.StartsAt, candidate.EndsAt = invitation.ScopeType, invitation.ScopeID, effectiveStart, invitation.IntendedEndsAt
	candidate.CreatedAt, candidate.UpdatedAt, candidate.ArchivedAt = model.TimeUTC(at), model.TimeUTC(at), model.OptionalTime{}
	if err = candidate.Validate(); err != nil {
		return nil, store.NewErrInvalidInput("invitation", "role_binding", err)
	}
	if err = insertRoleBinding(ctx, tx, &candidate); err != nil {
		return nil, err
	}
	return &candidate, nil
}

func replayScopedRoleInvitation(ctx context.Context, tx *sqlxTxWrapper, invitation *model.Invitation) (*store.ScopedRoleInvitationAcceptanceResult, error) {
	user, err := invitationAcceptanceUser(ctx, tx, invitation.AcceptedUserID)
	if err != nil {
		return nil, err
	}
	var row roleBindingRow
	if err = tx.Get(ctx, &row, `SELECT id,created_at,updated_at,archived_at,user_id,role_id,origin_invitation_id,origin_academic_unit_member_id,scope_type,scope_id,start_at,end_at
		FROM role_bindings WHERE id=? AND user_id=?`, invitation.AcceptedRoleBindingID.String(), user.ID.String()); err != nil {
		return nil, translateError("role_binding", invitation.AcceptedRoleBindingID.String(), err)
	}
	binding, err := row.model()
	if err != nil {
		return nil, err
	}
	return &store.ScopedRoleInvitationAcceptanceResult{Invitation: invitation, User: user, RoleBinding: binding, Replayed: true}, nil
}

func validateTeacherAcademicUnitInvitationAcceptance(input *store.TeacherAcademicUnitInvitationAcceptance) error {
	if input == nil || !model.IsValidTokenHash(input.ClaimHash) || input.AcceptedAt.IsZero() || input.User == nil || input.Settings == nil ||
		input.PasswordCredential == nil || input.DefaultProfilePictureJob == nil || input.Affiliation == nil || input.AcademicUnitMember == nil ||
		input.RoleBinding == nil || input.Occurrence == nil || input.Delivery == nil || input.DeliveryJob == nil || input.AuditEvent == nil {
		return store.NewErrInvalidInput("invitation", "acceptance", nil)
	}
	if !slices.Equal(input.RequiredActions, []model.Action{model.ActionInvitationCreate, model.ActionAcademicUnitMembersManage}) ||
		input.User.Validate() != nil || !input.User.EmailVerified || validateInitialUserSettingsDocument(input.User, input.Settings) != nil ||
		input.PasswordCredential.UserID != input.User.ID || input.PasswordCredential.Validate() != nil ||
		validateUserDefaultProfilePictureJob(input.User, input.DefaultProfilePictureJob) != nil ||
		input.Affiliation.UserID != input.User.ID || input.Affiliation.Kind != model.AffiliationTeacher || input.Affiliation.Validate() != nil ||
		input.AcademicUnitMember.UserID != input.User.ID || input.AcademicUnitMember.Validate() != nil ||
		input.RoleBinding.UserID != input.User.ID || input.RoleBinding.OriginInvitationID.IsZero() ||
		input.RoleBinding.OriginAcademicUnitMemberID != input.AcademicUnitMember.ID || input.RoleBinding.Validate() != nil {
		return store.NewErrInvalidInput("invitation", "acceptance_artifacts", nil)
	}
	if input.Occurrence.Kind != model.MailOccurrenceInvitation || input.Occurrence.TemplateKey != model.MailTemplateAccessInvitationAccepted ||
		input.Delivery.TargetUserID != input.User.ID || input.Delivery.TargetInvitationID.IsValid() || input.Delivery.TemplateKey != input.Occurrence.TemplateKey ||
		input.DeliveryJob.Type != model.JobTypeMailDeliver || input.Occurrence.Validate() != nil || input.Delivery.Validate() != nil || input.DeliveryJob.Validate() != nil {
		return store.NewErrInvalidInput("invitation", "acceptance_mail", nil)
	}
	queued := input.Delivery.State == model.MailDeliveryQueued && len(input.Delivery.EncryptedPayload) > 0 && input.DeliveryJob.Status == model.JobStatusQueued
	suppressed := input.Delivery.State == model.MailDeliverySuppressed && input.Delivery.PublicFailureCode == model.MailDeliveryDisabledCode && len(input.Delivery.EncryptedPayload) == 0 && input.DeliveryJob.Status == model.JobStatusCanceled
	if (!queued && !suppressed) || input.AuditEvent.Action != string(model.ActionInvitationAccept) || input.AuditEvent.Resource.Type != model.ResourceAcademicUnit || input.AuditEvent.ScopeType != model.RoleScopeAcademicUnit {
		return store.NewErrInvalidInput("invitation", "acceptance_relationship", nil)
	}
	return nil
}

func validateStudentClassInvitationAcceptance(input *store.StudentClassInvitationAcceptance) error {
	if input == nil || !model.IsValidTokenHash(input.ClaimHash) || input.AcceptedAt.IsZero() || input.User == nil || input.Settings == nil ||
		input.PasswordCredential == nil || input.DefaultProfilePictureJob == nil || input.Affiliation == nil || input.ClassMember == nil ||
		input.Occurrence == nil || input.Delivery == nil || input.DeliveryJob == nil || input.AuditEvent == nil {
		return store.NewErrInvalidInput("invitation", "acceptance", nil)
	}
	if len(input.RequiredActions) != 2 || input.RequiredActions[0] != model.ActionInvitationCreate || input.RequiredActions[1] != model.ActionClassMembersManage {
		return store.NewErrInvalidInput("invitation", "required_actions", nil)
	}
	if err := input.User.Validate(); err != nil || !input.User.EmailVerified || input.User.Email == "" {
		return store.NewErrInvalidInput("invitation", "user", err)
	}
	if err := validateInitialUserSettingsDocument(input.User, input.Settings); err != nil {
		return err
	}
	if input.PasswordCredential.UserID != input.User.ID || input.PasswordCredential.Validate() != nil ||
		validateUserDefaultProfilePictureJob(input.User, input.DefaultProfilePictureJob) != nil {
		return store.NewErrInvalidInput("invitation", "user_artifacts", nil)
	}
	if input.Affiliation.UserID != input.User.ID || input.Affiliation.Kind != model.AffiliationStudent || input.Affiliation.Validate() != nil ||
		input.ClassMember.UserID != input.User.ID || input.ClassMember.Validate() != nil {
		return store.NewErrInvalidInput("invitation", "relationship_artifacts", nil)
	}
	if input.Occurrence.Kind != model.MailOccurrenceInvitation || input.Occurrence.TemplateKey != model.MailTemplateAccessInvitationAccepted ||
		input.Delivery.TargetUserID != input.User.ID || input.Delivery.TargetInvitationID.IsValid() ||
		input.Delivery.TemplateKey != input.Occurrence.TemplateKey || input.DeliveryJob.Type != model.JobTypeMailDeliver {
		return store.NewErrInvalidInput("invitation", "acceptance_mail", nil)
	}
	if input.Occurrence.Validate() != nil || input.Delivery.Validate() != nil || input.DeliveryJob.Validate() != nil {
		return store.NewErrInvalidInput("invitation", "acceptance_mail", nil)
	}
	queued := input.Delivery.State == model.MailDeliveryQueued && input.Delivery.PublicFailureCode == "" && len(input.Delivery.EncryptedPayload) > 0 &&
		input.DeliveryJob.Status == model.JobStatusQueued && input.DeliveryJob.Revision == 1 && !input.DeliveryJob.CompletedAt.Valid
	suppressedDisabled := input.Delivery.State == model.MailDeliverySuppressed && input.Delivery.PublicFailureCode == model.MailDeliveryDisabledCode && len(input.Delivery.EncryptedPayload) == 0 &&
		input.DeliveryJob.Status == model.JobStatusCanceled && input.DeliveryJob.Revision == 2 && input.DeliveryJob.CompletedAt.Valid &&
		input.DeliveryJob.CompletedAt.Time.Equal(input.Delivery.CreatedAt) && input.DeliveryJob.PublicErrorCode == "job.canceled"
	if !queued && !suppressedDisabled {
		return store.NewErrInvalidInput("invitation", "acceptance_mail_lifecycle", nil)
	}
	command, err := model.DecodeMailDeliveryCommand(input.DeliveryJob.Command)
	if err != nil || input.Delivery.OccurrenceID != input.Occurrence.ID || input.Delivery.JobID != input.DeliveryJob.ID ||
		command.DeliveryID != input.Delivery.ID || input.DeliveryJob.DedupeKey != input.Delivery.ID.String() {
		return store.NewErrInvalidInput("invitation", "acceptance_mail_relationship", err)
	}
	if !input.AuditEvent.ID.IsZero() || input.AuditEvent.Status != model.AuditStatusSuccess || input.AuditEvent.Action != string(model.ActionInvitationAccept) ||
		input.AuditEvent.Resource.Type != model.ResourceClass || input.AuditEvent.ScopeType != model.RoleScopeClass {
		return store.NewErrInvalidInput("invitation", "acceptance_audit", nil)
	}
	return nil
}

func invitationAcceptanceUser(ctx context.Context, tx *sqlxTxWrapper, id model.UserID) (*model.User, error) {
	var row userRow
	if err := tx.Get(ctx, &row, `SELECT `+strings.Join(userSliceColumns(), ",")+` FROM users WHERE id=?`, id.String()); err != nil {
		return nil, translateError("user", id.String(), err)
	}
	return row.model()
}

func resolveInvitationUser(ctx context.Context, tx *sqlxTxWrapper, invitation *model.Invitation, input *store.StudentClassInvitationAcceptance, at time.Time) (*model.User, error) {
	if err := lockInvitationMailbox(ctx, tx, invitation.TargetEmail); err != nil {
		return nil, err
	}
	var row userRow
	err := tx.Get(ctx, &row, `SELECT `+strings.Join(userSliceColumns(), ",")+` FROM users WHERE email=? AND archived_at IS NULL FOR UPDATE`, invitation.TargetEmail)
	if err == nil {
		user, modelErr := row.model()
		if modelErr != nil {
			return nil, modelErr
		}
		if user.DisabledAt.Valid {
			return nil, store.NewErrConflict("invitation", "invitation_user_disabled", nil)
		}
		if !user.EmailVerified {
			mailEligibilityRevision, revisionErr := advanceUserMailEligibilityRevision(ctx, tx)
			if revisionErr != nil {
				return nil, revisionErr
			}
			// at is PostgreSQL time while the User may carry a leading node clock.
			if _, updateErr := tx.Exec(ctx, `UPDATE users SET email_verified=TRUE,mail_eligibility_revision=?,updated_at=GREATEST(updated_at,?),revision=revision+1 WHERE id=?`, mailEligibilityRevision, at, user.ID.String()); updateErr != nil {
				return nil, fmt.Errorf("verify invited user email: %w", updateErr)
			}
			user.EmailVerified = true
			user.UpdatedAt = latestTime(user.UpdatedAt, at)
			user.Revision++
		}
		var hasPassword bool
		if err = tx.Get(ctx, &hasPassword, `SELECT EXISTS(SELECT 1 FROM password_credentials WHERE user_id=? AND archived_at IS NULL)`, user.ID.String()); err != nil {
			return nil, fmt.Errorf("find invited user password: %w", err)
		}
		if !hasPassword {
			credential := *input.PasswordCredential
			credential.UserID = user.ID
			if err = credential.Validate(); err != nil {
				return nil, store.NewErrInvalidInput("invitation", "password_credential", err)
			}
			if err = insertPasswordCredential(ctx, tx, &credential); err != nil {
				return nil, err
			}
		}
		return user, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("resolve invited user: %w", err)
	}
	user := *input.User
	if user.Email != invitation.TargetEmail || !user.EmailVerified {
		return nil, store.NewErrConflict("invitation", "invitation_mailbox_mismatch", nil)
	}
	if err := insertUser(ctx, tx, &user); err != nil {
		return nil, err
	}
	if err := insertUserSettingsDocument(ctx, tx, input.Settings); err != nil {
		return nil, err
	}
	if err := insertPasswordCredential(ctx, tx, input.PasswordCredential); err != nil {
		return nil, err
	}
	if _, err := insertQueuedJob(ctx, tx, input.DefaultProfilePictureJob, false); err != nil {
		return nil, fmt.Errorf("enqueue invited user default picture: %w", err)
	}
	return &user, nil
}

func lockInvitationMailbox(ctx context.Context, tx sqlxExecutor, email string) error {
	if err := lockAdvisory(ctx, tx, advisoryLockKey("invitation-mailbox", email)); err != nil {
		return fmt.Errorf("lock Invitation mailbox: %w", err)
	}
	return nil
}

func ensureInvitationStudentAffiliation(ctx context.Context, tx *sqlxTxWrapper, prepared *model.Affiliation, userID model.UserID, effectiveStart time.Time) (*model.Affiliation, error) {
	if err := lockAffiliationLifecycle(ctx, tx); err != nil {
		return nil, err
	}
	var row affiliationRow
	err := tx.Get(ctx, &row, `SELECT id,created_at,updated_at,archived_at,revision,user_id,kind,start_at,end_at FROM affiliations
		WHERE user_id=? AND kind='student' AND archived_at IS NULL AND start_at<=? AND (end_at IS NULL OR end_at>?)
		ORDER BY start_at DESC,id LIMIT 1 FOR UPDATE`, userID.String(), effectiveStart, effectiveStart)
	if err == nil {
		return row.model()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("find student affiliation: %w", err)
	}
	candidate := *prepared
	candidate.UserID = userID
	candidate.StartsAt = effectiveStart
	candidate.EndsAt = model.OptionalTime{}
	if err := candidate.Validate(); err != nil {
		return nil, store.NewErrInvalidInput("invitation", "affiliation", err)
	}
	if err := ensureAffiliationRangeAvailable(ctx, tx, &candidate); err != nil {
		return nil, err
	}
	row = newAffiliationRow(&candidate)
	if _, err := tx.NamedExec(ctx, `INSERT INTO affiliations (id,created_at,updated_at,archived_at,revision,user_id,kind,start_at,end_at)
		VALUES (:id,:created_at,:updated_at,:archived_at,:revision,:user_id,:kind,:start_at,:end_at)`, &row); err != nil {
		return nil, fmt.Errorf("insert invitation affiliation: %w", translateError("affiliation", candidate.ID.String(), err))
	}
	return &candidate, nil
}

func ensureInvitationClassMember(ctx context.Context, tx *sqlxTxWrapper, invitation *model.Invitation, prepared *model.ClassMember, userID model.UserID, effectiveStart time.Time) (*model.ClassMember, error) {
	if err := lockClassLifecycle(ctx, tx); err != nil {
		return nil, err
	}
	if err := lockClassEnrollment(ctx, tx, userID.String(), invitation.AcademicPeriodID.String()); err != nil {
		return nil, err
	}
	var row classMemberRow
	err := tx.Get(ctx, &row, `SELECT id,created_at,updated_at,archived_at,revision,class_id,academic_period_id,user_id,start_at,end_at
		FROM class_members WHERE user_id=? AND academic_period_id=? AND archived_at IS NULL
		AND (end_at IS NULL OR end_at>?) AND (CAST(? AS timestamptz) IS NULL OR start_at<?)
		ORDER BY start_at DESC,id LIMIT 1 FOR UPDATE`, userID.String(), invitation.AcademicPeriodID.String(), effectiveStart,
		NullTimeFromOptional(invitation.IntendedEndsAt), NullTimeFromOptional(invitation.IntendedEndsAt))
	if err == nil {
		existing, modelErr := row.model()
		if modelErr != nil {
			return nil, modelErr
		}
		if existing.ClassID != invitation.ClassID || existing.StartsAt.After(effectiveStart) ||
			(invitation.IntendedEndsAt.Valid && existing.EndsAt.Valid && existing.EndsAt.Time.Before(invitation.IntendedEndsAt.Time)) {
			return nil, store.NewErrConflict("invitation", "invitation_class_membership_conflict", nil)
		}
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("find invitation class membership: %w", err)
	}
	candidate := *prepared
	candidate.UserID = userID
	candidate.ClassID = invitation.ClassID
	candidate.AcademicPeriodID = invitation.AcademicPeriodID
	candidate.StartsAt = effectiveStart
	candidate.EndsAt = invitation.IntendedEndsAt
	if err = candidate.Validate(); err != nil {
		return nil, store.NewErrInvalidInput("invitation", "class_member", err)
	}
	audienceRevisions, err := advanceClassMailAudienceRevisions(ctx, tx, candidate.ClassID)
	if err != nil {
		return nil, err
	}
	row = newClassMemberRow(&candidate)
	row.MailAudienceRevision = audienceRevisions[candidate.ClassID]
	if _, err := tx.NamedExec(ctx, `INSERT INTO class_members (id,created_at,updated_at,archived_at,revision,mail_audience_revision,class_id,academic_period_id,user_id,start_at,end_at)
		VALUES (:id,:created_at,:updated_at,:archived_at,:revision,:mail_audience_revision,:class_id,:academic_period_id,:user_id,:start_at,:end_at)`, &row); err != nil {
		return nil, fmt.Errorf("insert invitation class membership: %w", translateError("class_member", candidate.ID.String(), err))
	}
	return &candidate, nil
}

func invitationAcceptedRelationships(ctx context.Context, tx *sqlxTxWrapper, invitation *model.Invitation, userID model.UserID) (*model.Affiliation, *model.ClassMember, error) {
	var storedAffiliation affiliationRow
	if err := tx.Get(ctx, &storedAffiliation, `SELECT id,created_at,updated_at,archived_at,revision,user_id,kind,start_at,end_at FROM affiliations
		WHERE id=? AND user_id=?`, invitation.AcceptedAffiliationID.String(), userID.String()); err != nil {
		return nil, nil, translateError("affiliation", userID.String(), err)
	}
	affiliation, err := storedAffiliation.model()
	if err != nil {
		return nil, nil, err
	}
	var memberRow classMemberRow
	if err = tx.Get(ctx, &memberRow, `SELECT id,created_at,updated_at,archived_at,revision,class_id,academic_period_id,user_id,start_at,end_at FROM class_members
		WHERE id=? AND user_id=? AND class_id=? AND academic_period_id=?`,
		invitation.AcceptedClassMemberID.String(), userID.String(), invitation.ClassID.String(), invitation.AcademicPeriodID.String()); err != nil {
		return nil, nil, translateError("class_member", userID.String(), err)
	}
	member, err := memberRow.model()
	return affiliation, member, err
}

func ensureInvitationTeacherAffiliation(ctx context.Context, tx *sqlxTxWrapper, prepared *model.Affiliation, userID model.UserID, effectiveStart time.Time) (*model.Affiliation, error) {
	if err := lockAffiliationLifecycle(ctx, tx); err != nil {
		return nil, err
	}
	var row affiliationRow
	err := tx.Get(ctx, &row, `SELECT id,created_at,updated_at,archived_at,revision,user_id,kind,start_at,end_at FROM affiliations
		WHERE user_id=? AND kind='teacher' AND archived_at IS NULL AND start_at<=? AND (end_at IS NULL OR end_at>?)
		ORDER BY start_at DESC,id LIMIT 1 FOR UPDATE`, userID.String(), effectiveStart, effectiveStart)
	if err == nil {
		return row.model()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("find teacher affiliation: %w", err)
	}
	candidate := *prepared
	candidate.UserID, candidate.StartsAt, candidate.EndsAt = userID, effectiveStart, model.OptionalTime{}
	if err = candidate.Validate(); err != nil {
		return nil, store.NewErrInvalidInput("invitation", "affiliation", err)
	}
	if err = ensureAffiliationRangeAvailable(ctx, tx, &candidate); err != nil {
		return nil, err
	}
	row = newAffiliationRow(&candidate)
	if _, err = tx.NamedExec(ctx, `INSERT INTO affiliations (id,created_at,updated_at,archived_at,revision,user_id,kind,start_at,end_at)
		VALUES (:id,:created_at,:updated_at,:archived_at,:revision,:user_id,:kind,:start_at,:end_at)`, &row); err != nil {
		return nil, fmt.Errorf("insert teacher affiliation: %w", translateError("affiliation", candidate.ID.String(), err))
	}
	return &candidate, nil
}

func ensureInvitationAcademicUnitMember(ctx context.Context, tx *sqlxTxWrapper, invitation *model.Invitation, prepared *model.AcademicUnitMember, userID model.UserID, effectiveStart time.Time) (*model.AcademicUnitMember, error) {
	if err := lockAcademicUnitMemberLifecycle(ctx, tx); err != nil {
		return nil, err
	}
	if err := lockAcademicUnitMember(ctx, tx, invitation.AcademicUnitID.String(), userID.String()); err != nil {
		return nil, err
	}
	var row academicUnitMemberRow
	err := tx.Get(ctx, &row, `SELECT id,created_at,updated_at,archived_at,revision,academic_unit_id,user_id,start_at,end_at FROM academic_unit_members
		WHERE academic_unit_id=? AND user_id=? AND archived_at IS NULL AND start_at<=? AND (end_at IS NULL OR end_at>?)
		ORDER BY start_at DESC,id LIMIT 1 FOR UPDATE`, invitation.AcademicUnitID.String(), userID.String(), effectiveStart, effectiveStart)
	if err == nil {
		existing, modelErr := row.model()
		if modelErr != nil {
			return nil, modelErr
		}
		if !invitationEffectiveIntervalCovered(existing.StartsAt, existing.EndsAt, effectiveStart, invitation.IntendedEndsAt) {
			return nil, store.NewErrConflict("invitation", "invitation_academic_unit_membership_conflict", nil)
		}
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("find teacher Academic Unit membership: %w", err)
	}
	candidate := *prepared
	candidate.UserID, candidate.AcademicUnitID, candidate.StartsAt, candidate.EndsAt = userID, invitation.AcademicUnitID, effectiveStart, invitation.IntendedEndsAt
	if err = candidate.Validate(); err != nil {
		return nil, store.NewErrInvalidInput("invitation", "academic_unit_member", err)
	}
	if err = ensureAcademicUnitMemberRangeAvailable(ctx, tx, &candidate); err != nil {
		return nil, err
	}
	row = newAcademicUnitMemberRow(&candidate)
	if _, err = tx.NamedExec(ctx, `INSERT INTO academic_unit_members (id,created_at,updated_at,archived_at,revision,academic_unit_id,user_id,start_at,end_at)
		VALUES (:id,:created_at,:updated_at,:archived_at,:revision,:academic_unit_id,:user_id,:start_at,:end_at)`, &row); err != nil {
		return nil, fmt.Errorf("insert teacher Academic Unit membership: %w", translateError("academic_unit_member", candidate.ID.String(), err))
	}
	return &candidate, nil
}

func ensureInvitationRoleBinding(ctx context.Context, tx *sqlxTxWrapper, invitation *model.Invitation, prepared *model.RoleBinding, userID model.UserID, memberID model.AcademicUnitMemberID, effectiveStart time.Time) (*model.RoleBinding, error) {
	var row roleBindingRow
	err := tx.Get(ctx, &row, `SELECT id,created_at,updated_at,archived_at,user_id,role_id,origin_invitation_id,origin_academic_unit_member_id,scope_type,scope_id,start_at,end_at FROM role_bindings
		WHERE user_id=? AND role_id=? AND scope_type='academic_unit' AND scope_id=? AND archived_at IS NULL
		AND start_at<=? AND (end_at IS NULL OR end_at>?) ORDER BY start_at DESC,id LIMIT 1 FOR UPDATE`,
		userID.String(), invitation.RoleID.String(), invitation.AcademicUnitID.String(), effectiveStart, effectiveStart)
	if err == nil {
		existing, modelErr := row.model()
		if modelErr != nil {
			return nil, modelErr
		}
		if !invitationEffectiveIntervalCovered(existing.StartsAt, existing.EndsAt, effectiveStart, invitation.IntendedEndsAt) {
			return nil, store.NewErrConflict("invitation", "invitation_role_binding_conflict", nil)
		}
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("find teacher Role Binding: %w", err)
	}
	candidate := *prepared
	candidate.UserID, candidate.RoleID, candidate.OriginInvitationID = userID, invitation.RoleID, invitation.ID
	candidate.OriginAcademicUnitMemberID = memberID
	candidate.ScopeType, candidate.ScopeID, candidate.StartsAt, candidate.EndsAt = model.RoleScopeAcademicUnit, invitation.AcademicUnitID.String(), effectiveStart, invitation.IntendedEndsAt
	if err = candidate.Validate(); err != nil {
		return nil, store.NewErrInvalidInput("invitation", "role_binding", err)
	}
	if err = insertRoleBinding(ctx, tx, &candidate); err != nil {
		return nil, err
	}
	return &candidate, nil
}

func invitationEffectiveIntervalCovered(existingStart time.Time, existingEnd model.OptionalTime, requiredStart time.Time, requiredEnd model.OptionalTime) bool {
	if existingStart.After(requiredStart) {
		return false
	}
	if !requiredEnd.Valid {
		return !existingEnd.Valid
	}
	return !existingEnd.Valid || !existingEnd.Time.Before(requiredEnd.Time)
}

func replayTeacherAcademicUnitInvitation(ctx context.Context, tx *sqlxTxWrapper, invitation *model.Invitation) (*store.TeacherAcademicUnitInvitationAcceptanceResult, error) {
	user, err := invitationAcceptanceUser(ctx, tx, invitation.AcceptedUserID)
	if err != nil {
		return nil, err
	}
	var storedAffiliation affiliationRow
	if err = tx.Get(ctx, &storedAffiliation, `SELECT id,created_at,updated_at,archived_at,revision,user_id,kind,start_at,end_at FROM affiliations WHERE id=? AND user_id=?`, invitation.AcceptedAffiliationID.String(), user.ID.String()); err != nil {
		return nil, translateError("affiliation", invitation.AcceptedAffiliationID.String(), err)
	}
	affiliation, err := storedAffiliation.model()
	if err != nil {
		return nil, err
	}
	var memberRow academicUnitMemberRow
	if err = tx.Get(ctx, &memberRow, `SELECT id,created_at,updated_at,archived_at,revision,academic_unit_id,user_id,start_at,end_at FROM academic_unit_members WHERE id=? AND user_id=?`, invitation.AcceptedAcademicUnitMemberID.String(), user.ID.String()); err != nil {
		return nil, translateError("academic_unit_member", invitation.AcceptedAcademicUnitMemberID.String(), err)
	}
	member, err := memberRow.model()
	if err != nil {
		return nil, err
	}
	var bindingRow roleBindingRow
	if err = tx.Get(ctx, &bindingRow, `SELECT id,created_at,updated_at,archived_at,user_id,role_id,origin_invitation_id,origin_academic_unit_member_id,scope_type,scope_id,start_at,end_at FROM role_bindings WHERE id=? AND user_id=?`, invitation.AcceptedRoleBindingID.String(), user.ID.String()); err != nil {
		return nil, translateError("role_binding", invitation.AcceptedRoleBindingID.String(), err)
	}
	binding, err := bindingRow.model()
	if err != nil {
		return nil, err
	}
	return &store.TeacherAcademicUnitInvitationAcceptanceResult{Invitation: invitation, User: user, Affiliation: affiliation, AcademicUnitMember: member, RoleBinding: binding, Replayed: true}, nil
}

// AcceptStudentClass consumes the pending student Class Invitation named by
// the claim hash, granting the student Affiliation and Class membership, or
// replays an accepted one.
func (s SQLInvitationStore) AcceptStudentClass(ctx context.Context, input *store.StudentClassInvitationAcceptance) (*store.StudentClassInvitationAcceptanceResult, error) {
	if err := validateStudentClassInvitationAcceptance(input); err != nil {
		return nil, err
	}
	payloadKeyID, err := mailPayloadKeyID(input.Delivery.EncryptedPayload)
	if err != nil {
		return nil, store.NewErrInvalidInput("mail_delivery", "encrypted_payload", err)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "student class invitation acceptance", func(ctx context.Context, tx *sqlxTxWrapper) (*store.StudentClassInvitationAcceptanceResult, error) {
		return acceptStudentClassInvitation(ctx, tx, input, payloadKeyID)
	})
}

// acceptStudentClassInvitation consumes a claimed student Class Invitation inside the acceptance transaction.
func acceptStudentClassInvitation(ctx context.Context, tx *sqlxTxWrapper, input *store.StudentClassInvitationAcceptance, payloadKeyID string) (*store.StudentClassInvitationAcceptanceResult, error) {
	row, err := lockInvitationRowByClaim(ctx, tx, input.ClaimHash)
	if err != nil {
		return nil, err
	}
	invitation, err := row.model()
	if err != nil {
		return nil, err
	}
	if invitation.State == model.InvitationAccepted {
		return replayStudentClassInvitationAcceptance(ctx, tx, input, invitation)
	}
	var databaseNow time.Time
	if err = tx.Get(ctx, &databaseNow, `SELECT clock_timestamp()`); err != nil {
		return nil, fmt.Errorf("read invitation database time: %w", err)
	}
	databaseNow = model.TimeUTC(databaseNow)
	if err = requirePendingInvitation(invitation, databaseNow); err != nil {
		return nil, err
	}
	if err = lockStudentClassInvitationAcceptancePaths(ctx, tx, invitation, payloadKeyID); err != nil {
		return nil, err
	}
	user, err := resolveInvitationUser(ctx, tx, invitation, input, databaseNow)
	if err != nil {
		return nil, err
	}
	if err = validateStudentClassInvitationPackage(ctx, tx, invitation, databaseNow); err != nil {
		return nil, err
	}
	if err = requireStudentClassInvitationAuthority(ctx, tx, invitation.InviterUserID, invitation.ClassID, databaseNow); err != nil {
		return nil, err
	}
	effectiveStart := invitation.EffectiveStartsAt(databaseNow)
	affiliation, err := ensureInvitationStudentAffiliation(ctx, tx, input.Affiliation, user.ID, effectiveStart)
	if err != nil {
		return nil, err
	}
	member, err := ensureInvitationClassMember(ctx, tx, invitation, input.ClassMember, user.ID, effectiveStart)
	if err != nil {
		return nil, err
	}
	if err = consumeStudentClassInvitation(ctx, tx, invitation, user.ID, affiliation.ID, member.ID, databaseNow); err != nil {
		return nil, err
	}
	if err = recordStudentClassInvitationAcceptance(ctx, tx, input, invitation, user.ID, payloadKeyID); err != nil {
		return nil, err
	}
	return &store.StudentClassInvitationAcceptanceResult{Invitation: invitation, User: user, Affiliation: affiliation, ClassMember: member}, nil
}

// lockInvitationRowByClaim reads the Invitation that owns a claim hash under a row lock.
func lockInvitationRowByClaim(ctx context.Context, tx *sqlxTxWrapper, claimHash string) (invitationRow, error) {
	var row invitationRow
	if err := tx.Get(ctx, &row, `SELECT `+invitationColumns+` FROM invitations WHERE claim_hash=? FOR UPDATE`, claimHash); err != nil {
		return invitationRow{}, translateError("invitation", "claim", err)
	}
	return row, nil
}

// requirePendingInvitation rejects an Invitation that is no longer pending or whose admission window has closed.
func requirePendingInvitation(invitation *model.Invitation, databaseNow time.Time) error {
	if invitation.State != model.InvitationPending || !databaseNow.Before(invitation.ExpiresAt) ||
		(invitation.IntendedEndsAt.Valid && !databaseNow.Before(invitation.IntendedEndsAt.Time)) {
		return store.NewErrConflict("invitation", "invitation_not_pending", nil)
	}
	return nil
}

// replayStudentClassInvitationAcceptance returns the recorded result of an already accepted student Class Invitation.
func replayStudentClassInvitationAcceptance(ctx context.Context, tx *sqlxTxWrapper, input *store.StudentClassInvitationAcceptance, invitation *model.Invitation) (*store.StudentClassInvitationAcceptanceResult, error) {
	user, err := invitationAcceptanceUser(ctx, tx, invitation.AcceptedUserID)
	if err != nil {
		return nil, err
	}
	affiliation, member, err := invitationAcceptedRelationships(ctx, tx, invitation, user.ID)
	if err != nil {
		return nil, err
	}
	if err = completeBrowserInvitationTransaction(ctx, tx, input.BrowserTransaction, invitation, user.ID); err != nil {
		return nil, err
	}
	return &store.StudentClassInvitationAcceptanceResult{Invitation: invitation, User: user, Affiliation: affiliation, ClassMember: member, Replayed: true}, nil
}

// lockStudentClassInvitationAcceptancePaths takes the authentication, policy, inviter and mail key locks a student acceptance needs.
func lockStudentClassInvitationAcceptancePaths(ctx context.Context, tx *sqlxTxWrapper, invitation *model.Invitation, payloadKeyID string) error {
	if err := lockSystemAdministratorAuthenticationPaths(ctx, tx); err != nil {
		return err
	}
	if err := requireInvitationPolicy(ctx, tx); err != nil {
		return err
	}
	if err := lockInvitationInviterBeforeHierarchy(ctx, tx, invitation.InviterUserID); err != nil {
		return err
	}
	if payloadKeyID != "" {
		if err := requireMailPayloadPrimary(ctx, tx, payloadKeyID); err != nil {
			return err
		}
	}
	return nil
}

// consumeStudentClassInvitation marks a pending student Class Invitation accepted under its revision fence.
func consumeStudentClassInvitation(ctx context.Context, tx *sqlxTxWrapper, invitation *model.Invitation, userID model.UserID, affiliationID model.AffiliationID, memberID model.ClassMemberID, databaseNow time.Time) error {
	if err := invitation.Accept(userID, affiliationID, memberID, invitationTransitionAt(databaseNow, invitation)); err != nil {
		return store.NewErrConflict("invitation", "invitation_not_pending", err)
	}
	if result, err := tx.Exec(ctx, `UPDATE invitations SET state=?,accepted_at=?,accepted_user_id=?,accepted_affiliation_id=?,accepted_class_member_id=?,updated_at=?,revision=? WHERE id=? AND state='pending' AND revision=?`,
		invitation.State, invitation.AcceptedAt.Time, userID.String(), affiliationID.String(), memberID.String(), invitation.UpdatedAt, invitation.Revision, invitation.ID.String(), invitation.Revision-1); err != nil {
		return fmt.Errorf("consume invitation: %w", err)
	} else if affected, affectedErr := result.RowsAffected(); affectedErr != nil || affected != 1 {
		return store.NewErrConflict("invitation", "invitation_revision", affectedErr)
	}
	return suppressInvitationCredentialMail(ctx, tx, invitation.ID, model.MailDeliveryObsoleteCode, databaseNow)
}

// recordStudentClassInvitationAcceptance queues the acceptance mail, inserts the audit event and completes the browser transaction.
func recordStudentClassInvitationAcceptance(ctx context.Context, tx *sqlxTxWrapper, input *store.StudentClassInvitationAcceptance, invitation *model.Invitation, userID model.UserID, payloadKeyID string) error {
	occurrence := *input.Occurrence
	occurrence.ActorUserID = invitation.InviterUserID
	delivery := *input.Delivery
	delivery.TargetUserID = userID
	delivery.TargetInvitationID = ""
	if err := insertInvitationMail(ctx, tx, &occurrence, &delivery, input.DeliveryJob, payloadKeyID); err != nil {
		return err
	}
	event, err := invitationAcceptanceAuditEvent(input.AuditEvent, invitation, userID)
	if err != nil {
		return err
	}
	if _, err := insertAuditEvent(ctx, tx, event); err != nil {
		return fmt.Errorf("insert invitation acceptance audit: %w", err)
	}
	return completeBrowserInvitationTransaction(ctx, tx, input.BrowserTransaction, invitation, userID)
}

// invitationAcceptanceAuditEvent clones the prepared acceptance audit event with the accepting User and the accepted Invitation.
func invitationAcceptanceAuditEvent(prepared *model.AuditEvent, invitation *model.Invitation, userID model.UserID) (*model.AuditEvent, error) {
	event := prepared.Clone()
	event.ActorID = userID
	encoded, appErr := model.EncodeAuditData(invitation.Auditable())
	if appErr != nil {
		return nil, appErr
	}
	event.Result = encoded
	return event, nil
}

// AcceptTeacherAcademicUnit consumes the pending teacher Academic Unit
// Invitation named by the claim hash, granting the teacher Affiliation,
// membership and Role Binding, or replays an accepted one.
func (s SQLInvitationStore) AcceptTeacherAcademicUnit(ctx context.Context, input *store.TeacherAcademicUnitInvitationAcceptance) (*store.TeacherAcademicUnitInvitationAcceptanceResult, error) {
	if err := validateTeacherAcademicUnitInvitationAcceptance(input); err != nil {
		return nil, err
	}
	payloadKeyID, err := mailPayloadKeyID(input.Delivery.EncryptedPayload)
	if err != nil {
		return nil, store.NewErrInvalidInput("mail_delivery", "encrypted_payload", err)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "teacher academic unit invitation acceptance", func(ctx context.Context, tx *sqlxTxWrapper) (*store.TeacherAcademicUnitInvitationAcceptanceResult, error) {
		return acceptTeacherAcademicUnitInvitation(ctx, tx, input, payloadKeyID)
	})
}

// acceptTeacherAcademicUnitInvitation consumes a claimed teacher Academic Unit Invitation inside the acceptance transaction.
func acceptTeacherAcademicUnitInvitation(ctx context.Context, tx *sqlxTxWrapper, input *store.TeacherAcademicUnitInvitationAcceptance, payloadKeyID string) (*store.TeacherAcademicUnitInvitationAcceptanceResult, error) {
	row, err := lockInvitationRowByClaim(ctx, tx, input.ClaimHash)
	if err != nil {
		return nil, err
	}
	invitation, err := row.model()
	if err != nil || invitation.Purpose != model.InvitationPurposeTeacherAcademicUnit {
		return nil, store.NewErrConflict("invitation", "invitation_not_pending", err)
	}
	if invitation.State == model.InvitationAccepted {
		return replayTeacherAcademicUnitInvitationAcceptance(ctx, tx, input, invitation)
	}
	databaseNow, err := jobDatabaseNow(ctx, tx)
	if err != nil {
		return nil, err
	}
	if err = requirePendingInvitation(invitation, databaseNow); err != nil {
		return nil, err
	}
	existingUser, err := lockTeacherAcademicUnitInvitationAcceptancePaths(ctx, tx, invitation, payloadKeyID)
	if err != nil {
		return nil, err
	}
	studentArtifacts := &store.StudentClassInvitationAcceptance{User: input.User, Settings: input.Settings,
		PasswordCredential: input.PasswordCredential, DefaultProfilePictureJob: input.DefaultProfilePictureJob}
	user, err := resolveInvitationUser(ctx, tx, invitation, studentArtifacts, databaseNow)
	if err != nil {
		return nil, err
	}
	if err = validateTeacherAcademicUnitInvitationPackage(ctx, tx, invitation, databaseNow); err != nil {
		return nil, err
	}
	if err = requireTeacherAcademicUnitInvitationAuthority(ctx, tx, invitation, databaseNow); err != nil {
		return nil, err
	}
	effectiveStart := invitation.EffectiveStartsAt(databaseNow)
	affiliation, err := ensureInvitationTeacherAffiliation(ctx, tx, input.Affiliation, user.ID, effectiveStart)
	if err != nil {
		return nil, err
	}
	member, err := ensureInvitationAcademicUnitMember(ctx, tx, invitation, input.AcademicUnitMember, user.ID, effectiveStart)
	if err != nil {
		return nil, err
	}
	binding, err := ensureInvitationRoleBinding(ctx, tx, invitation, input.RoleBinding, user.ID, member.ID, effectiveStart)
	if err != nil {
		return nil, err
	}
	if err = consumeTeacherAcademicUnitInvitation(ctx, tx, invitation, user.ID, affiliation.ID, member.ID, binding.ID, databaseNow); err != nil {
		return nil, err
	}
	if err = recordTeacherAcademicUnitInvitationAcceptance(ctx, tx, input, invitation, user.ID, existingUser, payloadKeyID); err != nil {
		return nil, err
	}
	return &store.TeacherAcademicUnitInvitationAcceptanceResult{Invitation: invitation, User: user, Affiliation: affiliation, AcademicUnitMember: member, RoleBinding: binding}, nil
}

// replayTeacherAcademicUnitInvitationAcceptance returns the recorded result of an already accepted teacher Invitation.
func replayTeacherAcademicUnitInvitationAcceptance(ctx context.Context, tx *sqlxTxWrapper, input *store.TeacherAcademicUnitInvitationAcceptance, invitation *model.Invitation) (*store.TeacherAcademicUnitInvitationAcceptanceResult, error) {
	result, replayErr := replayTeacherAcademicUnitInvitation(ctx, tx, invitation)
	if replayErr != nil {
		return nil, replayErr
	}
	if replayErr = completeBrowserInvitationTransaction(ctx, tx, input.BrowserTransaction, invitation, result.User.ID); replayErr != nil {
		return nil, replayErr
	}
	return result, nil
}

// lockTeacherAcademicUnitInvitationAcceptancePaths takes the locks a teacher acceptance needs and reports whether the invited User already exists.
func lockTeacherAcademicUnitInvitationAcceptancePaths(ctx context.Context, tx *sqlxTxWrapper, invitation *model.Invitation, payloadKeyID string) (bool, error) {
	if err := lockSystemAdministratorAuthenticationPaths(ctx, tx); err != nil {
		return false, err
	}
	if err := requireInvitationPolicy(ctx, tx); err != nil {
		return false, err
	}
	if err := lockInvitationInviterBeforeHierarchy(ctx, tx, invitation.InviterUserID); err != nil {
		return false, err
	}
	if err := lockInvitationMailbox(ctx, tx, invitation.TargetEmail); err != nil {
		return false, err
	}
	var existingUser bool
	if err := tx.Get(ctx, &existingUser, `SELECT EXISTS(SELECT 1 FROM users WHERE email=? AND archived_at IS NULL)`, invitation.TargetEmail); err != nil {
		return false, fmt.Errorf("inspect invited teacher user: %w", err)
	}
	if !existingUser && payloadKeyID != "" {
		if err := requireMailPayloadPrimary(ctx, tx, payloadKeyID); err != nil {
			return false, err
		}
	}
	return existingUser, nil
}

// consumeTeacherAcademicUnitInvitation marks a pending teacher Invitation accepted under its revision fence.
func consumeTeacherAcademicUnitInvitation(ctx context.Context, tx *sqlxTxWrapper, invitation *model.Invitation, userID model.UserID,
	affiliationID model.AffiliationID, memberID model.AcademicUnitMemberID, bindingID model.RoleBindingID, databaseNow time.Time,
) error {
	if err := invitation.AcceptTeacherAcademicUnit(userID, affiliationID, memberID, bindingID, invitationTransitionAt(databaseNow, invitation)); err != nil {
		return store.NewErrConflict("invitation", "invitation_not_pending", err)
	}
	result, err := tx.Exec(ctx, `UPDATE invitations SET state=?,accepted_at=?,accepted_user_id=?,accepted_affiliation_id=?,accepted_academic_unit_member_id=?,accepted_role_binding_id=?,updated_at=?,revision=? WHERE id=? AND state='pending' AND revision=?`,
		invitation.State, invitation.AcceptedAt.Time, userID.String(), affiliationID.String(), memberID.String(), bindingID.String(), invitation.UpdatedAt, invitation.Revision, invitation.ID.String(), invitation.Revision-1)
	if err != nil {
		return fmt.Errorf("consume teacher Invitation: %w", err)
	}
	if affected, affectedErr := result.RowsAffected(); affectedErr != nil || affected != 1 {
		return store.NewErrConflict("invitation", "invitation_revision", affectedErr)
	}
	return suppressInvitationCredentialMail(ctx, tx, invitation.ID, model.MailDeliveryObsoleteCode, databaseNow)
}

// recordTeacherAcademicUnitInvitationAcceptance queues a new User's acceptance mail, inserts the audit event and completes the browser transaction.
func recordTeacherAcademicUnitInvitationAcceptance(ctx context.Context, tx *sqlxTxWrapper, input *store.TeacherAcademicUnitInvitationAcceptance,
	invitation *model.Invitation, userID model.UserID, existingUser bool, payloadKeyID string,
) error {
	if !existingUser {
		occurrence := *input.Occurrence
		occurrence.ActorUserID = invitation.InviterUserID
		delivery := *input.Delivery
		delivery.TargetUserID, delivery.TargetInvitationID = userID, ""
		if err := insertInvitationMail(ctx, tx, &occurrence, &delivery, input.DeliveryJob, payloadKeyID); err != nil {
			return err
		}
	}
	event, err := invitationAcceptanceAuditEvent(input.AuditEvent, invitation, userID)
	if err != nil {
		return err
	}
	if _, err = insertAuditEvent(ctx, tx, event); err != nil {
		return fmt.Errorf("insert teacher Invitation acceptance audit: %w", err)
	}
	return completeBrowserInvitationTransaction(ctx, tx, input.BrowserTransaction, invitation, userID)
}

func newInvitationRow(invitation *model.Invitation) invitationRow {
	roleActions := append([]string{}, invitation.RoleActions...)
	return invitationRow{
		ID: invitation.ID.String(), CreatedAt: invitation.CreatedAt, UpdatedAt: invitation.UpdatedAt,
		Revision: invitation.Revision, Purpose: invitation.Purpose, State: invitation.State,
		TargetEmail: invitation.TargetEmail, ClassID: nullableID(invitation.ClassID.String()), AcademicPeriodID: nullableID(invitation.AcademicPeriodID.String()),
		AcademicUnitID: nullableID(invitation.AcademicUnitID.String()), RoleID: nullableID(invitation.RoleID.String()),
		RoleActions:      pq.StringArray(roleActions),
		IntendedStartsAt: invitation.IntendedStartsAt, IntendedEndsAt: NullTimeFromOptional(invitation.IntendedEndsAt),
		SuggestedUsername: invitation.Suggestions.Username, SuggestedDisplayName: invitation.Suggestions.DisplayName,
		SuggestedFirstName: invitation.Suggestions.FirstName, SuggestedLastName: invitation.Suggestions.LastName,
		SuggestedLocale: invitation.Suggestions.Locale, SuggestedTimezone: invitation.Suggestions.Timezone, InviterUserID: invitation.InviterUserID.String(),
		ScopeType: invitation.ScopeType, ScopeID: invitation.ScopeID, ClaimHash: invitation.ClaimHash,
		ExpiresAt: invitation.ExpiresAt, AcceptedAt: NullTimeFromOptional(invitation.AcceptedAt),
		AcceptedUserID: nullableID(invitation.AcceptedUserID.String()), AcceptedAffiliationID: nullableID(invitation.AcceptedAffiliationID.String()), AcceptedClassMemberID: nullableID(invitation.AcceptedClassMemberID.String()),
		AcceptedAcademicUnitMemberID: nullableID(invitation.AcceptedAcademicUnitMemberID.String()), AcceptedRoleBindingID: nullableID(invitation.AcceptedRoleBindingID.String()),
	}
}
