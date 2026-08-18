// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SQLInvitationStore struct{ *SQLStore }

const invitationTerminalRetention = 90 * 24 * time.Hour

type invitationRow struct {
	ID                    string                  `db:"id"`
	CreatedAt             time.Time               `db:"created_at"`
	UpdatedAt             time.Time               `db:"updated_at"`
	Revision              int64                   `db:"revision"`
	Purpose               model.InvitationPurpose `db:"purpose"`
	State                 model.InvitationState   `db:"state"`
	TargetEmail           string                  `db:"target_email"`
	ClassID               string                  `db:"class_id"`
	AcademicPeriodID      string                  `db:"academic_period_id"`
	IntendedStartsAt      time.Time               `db:"intended_start_at"`
	IntendedEndsAt        sql.NullTime            `db:"intended_end_at"`
	SuggestedUsername     string                  `db:"suggested_username"`
	SuggestedDisplayName  string                  `db:"suggested_display_name"`
	SuggestedFirstName    string                  `db:"suggested_first_name"`
	SuggestedLastName     string                  `db:"suggested_last_name"`
	SuggestedLocale       string                  `db:"suggested_locale"`
	InviterUserID         string                  `db:"inviter_user_id"`
	ScopeType             model.RoleScopeType     `db:"scope_type"`
	ScopeID               string                  `db:"scope_id"`
	ClaimHash             string                  `db:"claim_hash"`
	ExpiresAt             time.Time               `db:"expires_at"`
	AcceptedAt            sql.NullTime            `db:"accepted_at"`
	AcceptedUserID        sql.NullString          `db:"accepted_user_id"`
	AcceptedAffiliationID sql.NullString          `db:"accepted_affiliation_id"`
	AcceptedClassMemberID sql.NullString          `db:"accepted_class_member_id"`
}

const invitationColumns = `id,created_at,updated_at,revision,purpose,state,target_email,class_id,academic_period_id,intended_start_at,intended_end_at,suggested_username,suggested_display_name,suggested_first_name,suggested_last_name,suggested_locale,inviter_user_id,scope_type,scope_id,claim_hash,expires_at,accepted_at,accepted_user_id,accepted_affiliation_id,accepted_class_member_id`

func newSQLInvitationStore(sqlStore *SQLStore) store.InvitationStore {
	return &SQLInvitationStore{SQLStore: sqlStore}
}

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
		if err = validateStudentClassInvitationPackage(ctx, tx, &invitation, databaseNow); err != nil {
			return nil, err
		}
		if err = requireStudentClassInvitationAuthority(ctx, tx, invitation.InviterUserID, invitation.ClassID, databaseNow); err != nil {
			return nil, err
		}
		if err := insertInvitation(ctx, tx, &invitation); err != nil {
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
		return &invitation, nil
	})
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
	if isNoRows(err) {
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
	if err = invitation.Expire(at); err != nil {
		return invalidPersistedState("invitation", "expiry", err)
	}
	if err = updateExpiredInvitation(ctx, tx, invitation); err != nil {
		return err
	}
	return suppressInvitationCredentialMail(ctx, tx, invitation.ID, model.MailDeliveryObsoleteCode, at)
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

func (s SQLInvitationStore) Get(ctx context.Context, id model.InvitationID) (*model.Invitation, error) {
	if !id.IsValid() {
		return nil, store.NewErrInvalidInput("invitation", "id", nil)
	}
	var row invitationRow
	if err := s.GetMaster().Get(ctx, &row, `SELECT `+invitationColumns+` FROM invitations WHERE id=?`, id.String()); err != nil {
		return nil, translateError("invitation", id.String(), err)
	}
	return row.model()
}

func (s SQLInvitationStore) GetByClaimHash(ctx context.Context, claimHash string) (*model.Invitation, error) {
	if !model.IsValidTokenHash(claimHash) {
		return nil, store.NewErrInvalidInput("invitation", "claim_hash", nil)
	}
	var row invitationRow
	if err := s.GetMaster().Get(ctx, &row, `SELECT `+invitationColumns+` FROM invitations WHERE claim_hash=?`, claimHash); err != nil {
		return nil, translateError("invitation", "claim", err)
	}
	return row.model()
}

func (s SQLInvitationStore) Maintain(ctx context.Context, limit int) (*store.InvitationMaintenanceResult, error) {
	if limit < 1 || limit > 500 {
		return nil, store.NewErrInvalidInput("invitation", "maintenance_limit", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "invitation maintenance", func(ctx context.Context, tx *sqlxTxWrapper) (*store.InvitationMaintenanceResult, error) {
		now, err := jobDatabaseNow(ctx, tx)
		if err != nil {
			return nil, err
		}
		rows := make([]invitationRow, 0, limit)
		if err = tx.Select(ctx, &rows, `SELECT `+invitationColumns+` FROM invitations
			WHERE state='pending' AND (expires_at<=? OR (intended_end_at IS NOT NULL AND intended_end_at<=?))
			ORDER BY LEAST(expires_at,COALESCE(intended_end_at,expires_at)),id FOR UPDATE SKIP LOCKED LIMIT ?`, now, now, limit); err != nil {
			return nil, fmt.Errorf("lock elapsed Invitations: %w", err)
		}
		for index := range rows {
			invitation, modelErr := rows[index].model()
			if modelErr != nil {
				return nil, modelErr
			}
			if modelErr = invitation.Expire(now); modelErr != nil {
				return nil, invalidPersistedState("invitation", "expiry", modelErr)
			}
			if err = updateExpiredInvitation(ctx, tx, invitation); err != nil {
				return nil, err
			}
			if err = suppressInvitationCredentialMail(ctx, tx, invitation.ID, model.MailDeliveryObsoleteCode, now); err != nil {
				return nil, err
			}
		}
		cutoff := now.Add(-invitationTerminalRetention)
		purged, err := tx.Exec(ctx, `WITH candidates AS (
			SELECT id FROM invitations WHERE state<>'pending' AND updated_at<=? ORDER BY updated_at,id FOR UPDATE SKIP LOCKED LIMIT ?)
			DELETE FROM invitations i USING candidates c WHERE i.id=c.id`, cutoff, limit)
		if err != nil {
			return nil, fmt.Errorf("purge Invitations: %w", err)
		}
		purgedCount, err := purged.RowsAffected()
		if err != nil {
			return nil, err
		}
		var more bool
		if err = tx.Get(ctx, &more, `SELECT EXISTS(SELECT 1 FROM invitations WHERE
			(state='pending' AND (expires_at<=? OR (intended_end_at IS NOT NULL AND intended_end_at<=?))) OR
			(state<>'pending' AND updated_at<=?))`, now, now, cutoff); err != nil {
			return nil, fmt.Errorf("inspect Invitation maintenance continuation: %w", err)
		}
		return &store.InvitationMaintenanceResult{Expired: len(rows), Purged: int(purgedCount), More: more}, nil
	})
}

func (s SQLInvitationStore) AcceptStudentClass(ctx context.Context, input *store.StudentClassInvitationAcceptance) (*store.StudentClassInvitationAcceptanceResult, error) {
	if err := validateStudentClassInvitationAcceptance(input); err != nil {
		return nil, err
	}
	payloadKeyID, err := mailPayloadKeyID(input.Delivery.EncryptedPayload)
	if err != nil {
		return nil, store.NewErrInvalidInput("mail_delivery", "encrypted_payload", err)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "student class invitation acceptance", func(ctx context.Context, tx *sqlxTxWrapper) (*store.StudentClassInvitationAcceptanceResult, error) {
		var row invitationRow
		if err := tx.Get(ctx, &row, `SELECT `+invitationColumns+` FROM invitations WHERE claim_hash=? FOR UPDATE`, input.ClaimHash); err != nil {
			return nil, translateError("invitation", "claim", err)
		}
		invitation, err := row.model()
		if err != nil {
			return nil, err
		}
		if invitation.State == model.InvitationAccepted {
			user, err := invitationAcceptanceUser(ctx, tx, invitation.AcceptedUserID)
			if err != nil {
				return nil, err
			}
			affiliation, member, err := invitationAcceptedRelationships(ctx, tx, invitation, user.ID)
			if err != nil {
				return nil, err
			}
			return &store.StudentClassInvitationAcceptanceResult{Invitation: invitation, User: user, Affiliation: affiliation, ClassMember: member, Replayed: true}, nil
		}
		var databaseNow time.Time
		if err := tx.Get(ctx, &databaseNow, `SELECT clock_timestamp()`); err != nil {
			return nil, fmt.Errorf("read invitation database time: %w", err)
		}
		databaseNow = model.TimeUTC(databaseNow)
		if invitation.State != model.InvitationPending || !databaseNow.Before(invitation.ExpiresAt) ||
			(invitation.IntendedEndsAt.Valid && !databaseNow.Before(invitation.IntendedEndsAt.Time)) {
			return nil, store.NewErrConflict("invitation", "invitation_not_pending", nil)
		}
		if err := requireInvitationPolicy(ctx, tx); err != nil {
			return nil, err
		}
		if err := validateStudentClassInvitationPackage(ctx, tx, invitation, databaseNow); err != nil {
			return nil, err
		}
		if err := requireStudentClassInvitationAuthority(ctx, tx, invitation.InviterUserID, invitation.ClassID, databaseNow); err != nil {
			return nil, err
		}
		if payloadKeyID != "" {
			if err := requireMailPayloadPrimary(ctx, tx, payloadKeyID); err != nil {
				return nil, err
			}
		}
		user, err := resolveInvitationUser(ctx, tx, invitation, input, databaseNow)
		if err != nil {
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
		if err := invitation.Accept(user.ID, affiliation.ID, member.ID, databaseNow); err != nil {
			return nil, store.NewErrConflict("invitation", "invitation_not_pending", err)
		}
		if result, err := tx.Exec(ctx, `UPDATE invitations SET state=?,accepted_at=?,accepted_user_id=?,accepted_affiliation_id=?,accepted_class_member_id=?,updated_at=?,revision=? WHERE id=? AND state='pending' AND revision=?`,
			invitation.State, invitation.AcceptedAt.Time, user.ID.String(), affiliation.ID.String(), member.ID.String(), invitation.UpdatedAt, invitation.Revision, invitation.ID.String(), invitation.Revision-1); err != nil {
			return nil, fmt.Errorf("consume invitation: %w", err)
		} else if affected, affectedErr := result.RowsAffected(); affectedErr != nil || affected != 1 {
			return nil, store.NewErrConflict("invitation", "invitation_revision", affectedErr)
		}
		if err := suppressInvitationCredentialMail(ctx, tx, invitation.ID, model.MailDeliveryObsoleteCode, databaseNow); err != nil {
			return nil, err
		}
		occurrence := *input.Occurrence
		occurrence.ActorUserID = invitation.InviterUserID
		delivery := *input.Delivery
		delivery.TargetUserID = user.ID
		delivery.TargetInvitationID = ""
		if err := insertInvitationMail(ctx, tx, &occurrence, &delivery, input.DeliveryJob, payloadKeyID); err != nil {
			return nil, err
		}
		event := input.AuditEvent.Clone()
		event.ActorID = user.ID
		encoded, appErr := model.EncodeAuditData(invitation.Auditable())
		if appErr != nil {
			return nil, appErr
		}
		event.Result = encoded
		if _, err := insertAuditEvent(ctx, tx, event); err != nil {
			return nil, fmt.Errorf("insert invitation acceptance audit: %w", err)
		}
		return &store.StudentClassInvitationAcceptanceResult{Invitation: invitation, User: user, Affiliation: affiliation, ClassMember: member}, nil
	})
}

func validateStudentClassInvitationAcceptance(input *store.StudentClassInvitationAcceptance) error {
	if input == nil || !model.IsValidTokenHash(input.ClaimHash) || input.AcceptedAt <= 0 || input.User == nil || input.Settings == nil ||
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
	command, err := model.DecodeMailDeliveryCommand(input.DeliveryJob.CommandVersion, input.DeliveryJob.Command)
	if err != nil || input.Delivery.OccurrenceID != input.Occurrence.ID || input.Delivery.JobID != input.DeliveryJob.ID ||
		command.DeliveryID != input.Delivery.ID || input.DeliveryJob.DedupeKey != input.Delivery.ID.String() {
		return store.NewErrInvalidInput("invitation", "acceptance_mail_relationship", err)
	}
	if !input.AuditEvent.ID.IsZero() || input.AuditEvent.Status != model.AuditStatusSuccess || input.AuditEvent.Action != "invitation.accept" ||
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
			if _, updateErr := tx.Exec(ctx, `UPDATE users SET email_verified=TRUE,updated_at=?,revision=revision+1 WHERE id=?`, at, user.ID.String()); updateErr != nil {
				return nil, fmt.Errorf("verify invited user email: %w", updateErr)
			}
			user.EmailVerified = true
			user.UpdatedAt = at
			user.Revision++
		}
		var hasPassword bool
		if err := tx.Get(ctx, &hasPassword, `SELECT EXISTS(SELECT 1 FROM password_credentials WHERE user_id=? AND archived_at IS NULL)`, user.ID.String()); err != nil {
			return nil, fmt.Errorf("find invited user password: %w", err)
		}
		if !hasPassword {
			credential := *input.PasswordCredential
			credential.UserID = user.ID
			if err := credential.Validate(); err != nil {
				return nil, store.NewErrInvalidInput("invitation", "password_credential", err)
			}
			if err := insertPasswordCredential(ctx, tx, &credential); err != nil {
				return nil, err
			}
		}
		return user, nil
	}
	if !isNoRows(err) {
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
	if !isNoRows(err) {
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
	if !isNoRows(err) {
		return nil, fmt.Errorf("find invitation class membership: %w", err)
	}
	candidate := *prepared
	candidate.UserID = userID
	candidate.ClassID = invitation.ClassID
	candidate.AcademicPeriodID = invitation.AcademicPeriodID
	candidate.StartsAt = effectiveStart
	candidate.EndsAt = invitation.IntendedEndsAt
	if err := candidate.Validate(); err != nil {
		return nil, store.NewErrInvalidInput("invitation", "class_member", err)
	}
	row = newClassMemberRow(&candidate)
	if _, err := tx.NamedExec(ctx, `INSERT INTO class_members (id,created_at,updated_at,archived_at,revision,class_id,academic_period_id,user_id,start_at,end_at)
		VALUES (:id,:created_at,:updated_at,:archived_at,:revision,:class_id,:academic_period_id,:user_id,:start_at,:end_at)`, &row); err != nil {
		return nil, fmt.Errorf("insert invitation class membership: %w", translateError("class_member", candidate.ID.String(), err))
	}
	return &candidate, nil
}

func invitationAcceptedRelationships(ctx context.Context, tx *sqlxTxWrapper, invitation *model.Invitation, userID model.UserID) (*model.Affiliation, *model.ClassMember, error) {
	var affiliationRow affiliationRow
	if err := tx.Get(ctx, &affiliationRow, `SELECT id,created_at,updated_at,archived_at,revision,user_id,kind,start_at,end_at FROM affiliations
		WHERE id=? AND user_id=?`, invitation.AcceptedAffiliationID.String(), userID.String()); err != nil {
		return nil, nil, translateError("affiliation", userID.String(), err)
	}
	affiliation, err := affiliationRow.model()
	if err != nil {
		return nil, nil, err
	}
	var memberRow classMemberRow
	if err := tx.Get(ctx, &memberRow, `SELECT id,created_at,updated_at,archived_at,revision,class_id,academic_period_id,user_id,start_at,end_at FROM class_members
		WHERE id=? AND user_id=? AND class_id=? AND academic_period_id=?`,
		invitation.AcceptedClassMemberID.String(), userID.String(), invitation.ClassID.String(), invitation.AcademicPeriodID.String()); err != nil {
		return nil, nil, translateError("class_member", userID.String(), err)
	}
	member, err := memberRow.model()
	return affiliation, member, err
}

func validateStudentClassInvitationIssue(input *store.StudentClassInvitationIssue) error {
	if input == nil || input.Invitation == nil || input.Occurrence == nil || input.Delivery == nil || input.DeliveryJob == nil ||
		!model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
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
	command, err := model.DecodeMailDeliveryCommand(input.DeliveryJob.CommandVersion, input.DeliveryJob.Command)
	if err != nil || input.Delivery.OccurrenceID != input.Occurrence.ID || input.Delivery.JobID != input.DeliveryJob.ID ||
		!input.Occurrence.CreatedAt.Equal(input.Delivery.CreatedAt) || !input.Delivery.UpdatedAt.Equal(input.Delivery.CreatedAt) ||
		!input.Delivery.MessageDate.Equal(input.Delivery.CreatedAt) || command.DeliveryID != input.Delivery.ID ||
		input.DeliveryJob.DedupeKey != input.Delivery.ID.String() {
		return store.NewErrInvalidInput("invitation", "mail_relationship", err)
	}
	return nil
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
	if err := lockSystemAdministratorAuthenticationPaths(ctx, tx); err != nil {
		return err
	}
	var active bool
	if err := tx.Get(ctx, &active, `SELECT true FROM users
		WHERE id=? AND archived_at IS NULL AND disabled_at IS NULL FOR SHARE`, inviterID.String()); err != nil {
		if isNoRows(err) {
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
		if isNoRows(err) {
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
		:id,:created_at,:updated_at,:revision,:purpose,:state,:target_email,:class_id,:academic_period_id,
		:intended_start_at,:intended_end_at,:suggested_username,:suggested_display_name,:suggested_first_name,
		:suggested_last_name,:suggested_locale,:inviter_user_id,:scope_type,:scope_id,:claim_hash,:expires_at,
		:accepted_at,:accepted_user_id,:accepted_affiliation_id,:accepted_class_member_id)`, &row); err != nil {
		return fmt.Errorf("insert invitation: %w", translateError("invitation", invitation.ID.String(), err))
	}
	return nil
}

func insertInvitationMail(ctx context.Context, tx *sqlxTxWrapper, occurrence *model.MailOccurrence, delivery *model.MailDelivery, job *model.Job, payloadKeyID string) error {
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

func newInvitationRow(invitation *model.Invitation) invitationRow {
	return invitationRow{
		ID: invitation.ID.String(), CreatedAt: invitation.CreatedAt, UpdatedAt: invitation.UpdatedAt,
		Revision: invitation.Revision, Purpose: invitation.Purpose, State: invitation.State,
		TargetEmail: invitation.TargetEmail, ClassID: invitation.ClassID.String(), AcademicPeriodID: invitation.AcademicPeriodID.String(),
		IntendedStartsAt: invitation.IntendedStartsAt, IntendedEndsAt: NullTimeFromOptional(invitation.IntendedEndsAt),
		SuggestedUsername: invitation.Suggestions.Username, SuggestedDisplayName: invitation.Suggestions.DisplayName,
		SuggestedFirstName: invitation.Suggestions.FirstName, SuggestedLastName: invitation.Suggestions.LastName,
		SuggestedLocale: invitation.Suggestions.Locale, InviterUserID: invitation.InviterUserID.String(),
		ScopeType: invitation.ScopeType, ScopeID: invitation.ScopeID, ClaimHash: invitation.ClaimHash,
		ExpiresAt: invitation.ExpiresAt, AcceptedAt: NullTimeFromOptional(invitation.AcceptedAt),
		AcceptedUserID: nullableID(invitation.AcceptedUserID.String()), AcceptedAffiliationID: nullableID(invitation.AcceptedAffiliationID.String()), AcceptedClassMemberID: nullableID(invitation.AcceptedClassMemberID.String()),
	}
}

func (row invitationRow) model() (*model.Invitation, error) {
	invitation := &model.Invitation{
		ID: model.InvitationID(row.ID), CreatedAt: model.TimeUTC(row.CreatedAt), UpdatedAt: model.TimeUTC(row.UpdatedAt),
		Revision: row.Revision, Purpose: row.Purpose, State: row.State, TargetEmail: row.TargetEmail,
		ClassID: model.ClassID(row.ClassID), AcademicPeriodID: model.AcademicPeriodID(row.AcademicPeriodID),
		IntendedStartsAt: model.TimeUTC(row.IntendedStartsAt), IntendedEndsAt: optionalTime(row.IntendedEndsAt),
		Suggestions:   model.InvitationProfileSuggestions{Username: row.SuggestedUsername, DisplayName: row.SuggestedDisplayName, FirstName: row.SuggestedFirstName, LastName: row.SuggestedLastName, Locale: row.SuggestedLocale},
		InviterUserID: model.UserID(row.InviterUserID), ScopeType: row.ScopeType, ScopeID: row.ScopeID,
		ClaimHash: row.ClaimHash, ExpiresAt: model.TimeUTC(row.ExpiresAt), AcceptedAt: optionalTime(row.AcceptedAt),
		AcceptedUserID: model.UserID(row.AcceptedUserID.String), AcceptedAffiliationID: model.AffiliationID(row.AcceptedAffiliationID.String), AcceptedClassMemberID: model.ClassMemberID(row.AcceptedClassMemberID.String),
	}
	if err := invitation.Validate(); err != nil {
		return nil, invalidPersistedState("invitation", "value", err)
	}
	return invitation, nil
}
