// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Adapted from Mattermost server/channels/store/sqlstore/token_store.go and
// user_store.go. Proctor binds every purpose-specific token to its target
// email and consumes the token, account mutation, session revocation, and
// terminal security audit in one PostgreSQL transaction.

package sqlstore

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/lib/pq"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SQLUserTokenStore struct {
	*SQLStore
	tokensQuery sq.SelectBuilder
}

const (
	userTokenAuditEmailVerificationRequest  = "authentication.email_verification.request"
	userTokenAuditEmailVerificationComplete = "authentication.email_verification.complete"
	userTokenAuditPasswordResetRequest      = "authentication.password_reset.request"
	userTokenAuditPasswordResetComplete     = "authentication.password_reset.complete"
	userEmailChangeTokenLifetimeMinimum     = 5 * time.Minute
	userEmailChangeTokenLifetimeMaximum     = 30 * 24 * time.Hour
	userEmailChangeWarningLifetimeMinimum   = time.Minute
	userEmailChangeWarningLifetimeMaximum   = 24 * time.Hour
)

func validateUserEmailMail(userID model.UserID, occurrence *model.MailOccurrence, delivery *model.MailDelivery, job *model.Job, kind model.MailOccurrenceKind, key model.MailTemplateKey) (string, error) {
	if occurrence == nil || delivery == nil || job == nil || occurrence.Kind != kind || occurrence.TemplateKey != key ||
		occurrence.ActorUserID != userID || delivery.TargetUserID != userID || delivery.TemplateKey != key {
		return "", store.NewErrInvalidInput("user_email", "mail", nil)
	}
	expectedJobType := model.JobTypeMailDeliver
	if kind == model.MailOccurrenceAccountToken {
		expectedJobType = model.JobTypeMailDeliverCredential
	}
	if job.Type != expectedJobType {
		return "", store.NewErrInvalidInput("user_email", "mail_job_type", nil)
	}
	if err := validateRecoveryMail(occurrence, delivery, job); err != nil {
		return "", err
	}
	payloadKeyID, err := mailPayloadKeyID(delivery.EncryptedPayload)
	if err != nil {
		return "", store.NewErrInvalidInput("mail_delivery", "encrypted_payload", err)
	}
	return payloadKeyID, nil
}

func (s SQLUserTokenStore) ChangeEmail(ctx context.Context, input *store.UserEmailChange) (*store.UserEmailChangeResult, error) {
	if input == nil || !input.UserID.IsValid() || input.ExpectedRevision <= 0 || !model.IsValidEmail(input.NewEmail) ||
		input.NewEmail != strings.ToLower(strings.TrimSpace(model.SanitizeUnicode(input.NewEmail))) || input.Token == nil || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 ||
		input.TokenLifetime < userEmailChangeTokenLifetimeMinimum || input.TokenLifetime > userEmailChangeTokenLifetimeMaximum ||
		input.WarningLifetime < userEmailChangeWarningLifetimeMinimum || input.WarningLifetime > userEmailChangeWarningLifetimeMaximum ||
		input.TokenLifetime%time.Millisecond != 0 || input.WarningLifetime%time.Millisecond != 0 {
		return nil, store.NewErrInvalidInput("user_email", "change", nil)
	}
	token := *input.Token
	if token.Validate() != nil || token.UserID != input.UserID || token.Purpose != model.UserTokenEmailVerification || token.Target != input.NewEmail {
		return nil, store.NewErrInvalidInput("user_email", "change", nil)
	}
	warningKey, err := validateUserEmailMail(input.UserID, input.WarningOccurrence, input.WarningDelivery, input.WarningJob, model.MailOccurrenceSecurityNotice, model.MailTemplateIdentityEmailChangeWarningOld)
	if err != nil {
		return nil, err
	}
	verificationKey, err := validateUserEmailMail(input.UserID, input.VerificationOccurrence, input.VerificationDelivery, input.VerificationJob, model.MailOccurrenceAccountToken, model.MailTemplateIdentityEmailChangeVerifyNew)
	if err != nil || input.VerificationOccurrence.ID.String() != token.ID.String() {
		return nil, store.NewErrInvalidInput("user_email", "verification", err)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "user email change", func(ctx context.Context, tx *sqlxTxWrapper) (*store.UserEmailChangeResult, error) {
		for _, keyID := range []string{warningKey, verificationKey} {
			if keyID != "" {
				if err := requireMailPayloadPrimary(ctx, tx, keyID); err != nil {
					return nil, err
				}
			}
		}
		if err := lockUserTokenPurpose(ctx, tx, input.UserID.String(), model.UserTokenEmailVerification); err != nil {
			return nil, err
		}
		var databaseNow time.Time
		if err := tx.Get(ctx, &databaseNow, `SELECT clock_timestamp()`); err != nil {
			return nil, fmt.Errorf("read user email change database time: %w", err)
		}
		at := model.TimeUTC(databaseNow)
		token.CreatedAt, token.UpdatedAt = at, at
		token.ArchivedAt, token.ConsumedAt = model.OptionalTime{}, model.OptionalTime{}
		token.ExpiresAt = at.Add(input.TokenLifetime)
		if validationErr := token.Validate(); validationErr != nil {
			return nil, store.NewErrInvalidInput("user_email", "token_lifetime", validationErr)
		}
		warningOccurrence, warningDelivery, warningJob, err := recoveryMailAtWithLifetime(
			input.WarningOccurrence, input.WarningDelivery, input.WarningJob, at, input.WarningLifetime,
		)
		if err != nil {
			return nil, err
		}
		verificationOccurrence, verificationDelivery, verificationJob, err := recoveryMailAtWithLifetime(
			input.VerificationOccurrence, input.VerificationDelivery, input.VerificationJob, at, input.TokenLifetime,
		)
		if err != nil {
			return nil, err
		}
		user, err := lockActiveUserForEmailTransition(ctx, tx, input.UserID)
		if err != nil {
			return nil, err
		}
		if user.Revision != input.ExpectedRevision || user.Email == input.NewEmail {
			return nil, store.NewErrConflict("user", "email_revision", nil)
		}
		mailEligibilityRevision, err := advanceUserMailEligibilityRevision(ctx, tx)
		if err != nil {
			return nil, err
		}
		var priorIDs []string
		if err = tx.Select(ctx, &priorIDs, `SELECT id FROM user_tokens WHERE user_id=? AND purpose=? AND archived_at IS NULL AND consumed_at IS NULL FOR UPDATE`, input.UserID.String(), model.UserTokenEmailVerification); err != nil {
			return nil, fmt.Errorf("lock prior email tokens: %w", err)
		}
		if _, err = tx.Exec(ctx, `UPDATE user_tokens SET updated_at=?,archived_at=? WHERE user_id=? AND purpose=? AND archived_at IS NULL AND consumed_at IS NULL`, at, at, input.UserID.String(), model.UserTokenEmailVerification); err != nil {
			return nil, fmt.Errorf("invalidate prior email tokens: %w", err)
		}
		result, err := tx.Exec(ctx, `UPDATE users SET email=?,email_verified=false,mail_eligibility_revision=?,updated_at=?,revision=revision+1 WHERE id=? AND revision=? AND archived_at IS NULL AND disabled_at IS NULL`, input.NewEmail, mailEligibilityRevision, at, input.UserID.String(), input.ExpectedRevision)
		if err != nil {
			return nil, translateError("user", input.UserID.String(), err)
		}
		if affected, affectedErr := result.RowsAffected(); affectedErr != nil || affected != 1 {
			return nil, store.NewErrConflict("user", "email_revision", affectedErr)
		}
		if err = insertUserToken(ctx, tx, &token); err != nil {
			return nil, err
		}
		if err = suppressSupersededTokenMail(ctx, tx, priorIDs, at); err != nil {
			return nil, err
		}
		if err = insertRecoveryMail(ctx, tx, warningOccurrence, warningDelivery, warningJob, warningKey); err != nil {
			return nil, err
		}
		if err = insertRecoveryMail(ctx, tx, verificationOccurrence, verificationDelivery, verificationJob, verificationKey); err != nil {
			return nil, err
		}
		updated, err := getUserByID(ctx, tx, input.UserID.String())
		if err != nil {
			return nil, err
		}
		encoded, err := model.EncodeAuditData(updated.Auditable())
		if err != nil {
			return nil, err
		}
		if _, err = completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt); err != nil {
			return nil, fmt.Errorf("complete user email change audit: %w", err)
		}
		return &store.UserEmailChangeResult{User: updated, Token: &token}, nil
	})
}

func (s SQLUserTokenStore) VerifyEmailPrivileged(ctx context.Context, input *store.PrivilegedEmailVerification) (*model.User, error) {
	if input == nil || !input.UserID.IsValid() || input.ExpectedRevision <= 0 || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("user_email", "privileged_verification", nil)
	}
	keyID, err := validateUserEmailMail(input.UserID, input.Occurrence, input.Delivery, input.Job, model.MailOccurrenceSecurityNotice, model.MailTemplateIdentityEmailVerifiedByAdmin)
	if err != nil {
		return nil, err
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "privileged email verification", func(ctx context.Context, tx *sqlxTxWrapper) (*model.User, error) {
		if keyID != "" {
			if err := requireMailPayloadPrimary(ctx, tx, keyID); err != nil {
				return nil, err
			}
		}
		if err := lockUserTokenPurpose(ctx, tx, input.UserID.String(), model.UserTokenEmailVerification); err != nil {
			return nil, err
		}
		user, err := lockActiveUserForEmailTransition(ctx, tx, input.UserID)
		if err != nil {
			return nil, err
		}
		if user.Revision != input.ExpectedRevision || user.EmailVerified {
			return nil, store.NewErrConflict("user", "email_revision", nil)
		}
		mailEligibilityRevision, err := advanceUserMailEligibilityRevision(ctx, tx)
		if err != nil {
			return nil, err
		}
		var priorIDs []string
		if err = tx.Select(ctx, &priorIDs, `SELECT id FROM user_tokens WHERE user_id=? AND purpose=? AND archived_at IS NULL AND consumed_at IS NULL FOR UPDATE`, input.UserID.String(), model.UserTokenEmailVerification); err != nil {
			return nil, err
		}
		at := input.Occurrence.CreatedAt
		if _, err = tx.Exec(ctx, `UPDATE user_tokens SET updated_at=?,archived_at=? WHERE user_id=? AND purpose=? AND archived_at IS NULL AND consumed_at IS NULL`, at, at, input.UserID.String(), model.UserTokenEmailVerification); err != nil {
			return nil, err
		}
		result, err := tx.Exec(ctx, `UPDATE users SET email_verified=true,mail_eligibility_revision=?,updated_at=?,revision=revision+1 WHERE id=? AND revision=? AND archived_at IS NULL AND disabled_at IS NULL`, mailEligibilityRevision, at, input.UserID.String(), input.ExpectedRevision)
		if err != nil {
			return nil, translateError("user", input.UserID.String(), err)
		}
		if affected, affectedErr := result.RowsAffected(); affectedErr != nil || affected != 1 {
			return nil, store.NewErrConflict("user", "email_revision", affectedErr)
		}
		if err = suppressSupersededTokenMail(ctx, tx, priorIDs, at); err != nil {
			return nil, err
		}
		if err = insertRecoveryMail(ctx, tx, input.Occurrence, input.Delivery, input.Job, keyID); err != nil {
			return nil, err
		}
		updated, err := getUserByID(ctx, tx, input.UserID.String())
		if err != nil {
			return nil, err
		}
		encoded, err := model.EncodeAuditData(updated.Auditable())
		if err != nil {
			return nil, err
		}
		if _, err = completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt); err != nil {
			return nil, err
		}
		return updated, nil
	})
}

func lockActiveUserForEmailTransition(ctx context.Context, tx *sqlxTxWrapper, id model.UserID) (*model.User, error) {
	var row userRow
	if err := tx.Get(ctx, &row, `SELECT `+strings.Join(userSliceColumns(), ",")+` FROM users WHERE id=? AND archived_at IS NULL AND disabled_at IS NULL FOR UPDATE`, id.String()); err != nil {
		return nil, translateError("user", id.String(), err)
	}
	return row.model()
}

func validateRecoveryMail(occurrence *model.MailOccurrence, delivery *model.MailDelivery, job *model.Job) error {
	if occurrence == nil || delivery == nil || job == nil {
		return store.NewErrInvalidInput("user_token", "mail", nil)
	}
	if err := occurrence.Validate(); err != nil {
		return store.NewErrInvalidInput("mail_occurrence", "value", err)
	}
	if err := delivery.Validate(); err != nil {
		return store.NewErrInvalidInput("mail_delivery", "value", err)
	}
	if err := job.Validate(); err != nil {
		return store.NewErrInvalidInput("job", "value", err)
	}
	command, err := model.DecodeMailDeliveryCommand(job.CommandVersion, job.Command)
	if err != nil || delivery.AttemptCount != 0 || delivery.Revision != 1 || delivery.AcceptedAt.Valid || delivery.FailedAt.Valid ||
		delivery.OccurrenceID != occurrence.ID || delivery.JobID != job.ID || delivery.TemplateKey != occurrence.TemplateKey ||
		delivery.TargetUserID != occurrence.ActorUserID || delivery.TargetInvitationID.IsValid() ||
		!occurrence.CreatedAt.Equal(delivery.CreatedAt) || !delivery.UpdatedAt.Equal(delivery.CreatedAt) || !delivery.MessageDate.Equal(delivery.CreatedAt) ||
		job.AttemptCount != 0 || job.DedupePolicy != model.JobDedupeActive || job.MaximumAttempts != model.MailMaximumAttempts || job.StartedAt.Valid ||
		len(job.Checkpoint) != 0 || len(job.Result) != 0 || job.Progress != nil || job.WorkReserved != 0 ||
		!job.CreatedAt.Equal(delivery.CreatedAt) || !job.UpdatedAt.Equal(delivery.CreatedAt) || !job.AvailableAt.Equal(delivery.CreatedAt) ||
		command.DeliveryID != delivery.ID || job.DedupeKey != delivery.ID.String() {
		return store.NewErrInvalidInput("user_token", "mail_relationship", err)
	}
	queued := delivery.State == model.MailDeliveryQueued && delivery.PublicFailureCode == "" && len(delivery.EncryptedPayload) > 0 &&
		job.Status == model.JobStatusQueued && job.Revision == 1 && !job.CompletedAt.Valid && job.PublicErrorCode == ""
	suppressedTerminal := delivery.State == model.MailDeliverySuppressed &&
		(delivery.PublicFailureCode == model.MailDeliveryDisabledCode || delivery.PublicFailureCode == model.MailDeliveryRecipientIneligibleCode) && len(delivery.EncryptedPayload) == 0 &&
		job.Status == model.JobStatusCanceled && job.Revision == 2 && job.CompletedAt.Valid && job.CompletedAt.Time.Equal(delivery.CreatedAt) && job.PublicErrorCode == "job.canceled"
	if !queued && !suppressedTerminal {
		return store.NewErrInvalidInput("user_token", "mail_lifecycle", nil)
	}
	return nil
}

func insertRecoveryMail(ctx context.Context, tx *sqlxTxWrapper, occurrence *model.MailOccurrence, delivery *model.MailDelivery, job *model.Job, payloadKeyID string) error {
	if _, err := tx.Exec(ctx, `INSERT INTO mail_occurrences (id, kind, template_key, actor_user_id, created_at) VALUES (?, ?, ?, ?, ?)`, occurrence.ID.String(), string(occurrence.Kind), string(occurrence.TemplateKey), occurrence.ActorUserID.String(), occurrence.CreatedAt); err != nil {
		return fmt.Errorf("insert recovery mail occurrence: %w", translateError("mail_occurrence", occurrence.ID.String(), err))
	}
	if err := insertPreparedMailJob(ctx, tx, job); err != nil {
		return fmt.Errorf("insert recovery mail job: %w", translateError("job", job.ID.String(), err))
	}
	if _, err := tx.Exec(ctx, `INSERT INTO mail_deliveries (`+mailDeliveryColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, delivery.ID.String(), delivery.OccurrenceID.String(), delivery.JobID.String(), nullableID(delivery.TargetUserID.String()), nullableID(delivery.TargetInvitationID.String()), string(delivery.TemplateKey), delivery.TemplateDigest, delivery.MaskedRecipient, string(delivery.State), delivery.CreatedAt, delivery.UpdatedAt, delivery.MessageDate, delivery.Deadline, delivery.MessageID, delivery.AttemptCount, optionalTimeValue(delivery.AcceptedAt), optionalTimeValue(delivery.FailedAt), delivery.PublicFailureCode, nullableMailPayloadKeyID(payloadKeyID), mailNullableJSON(delivery.EncryptedPayload), delivery.Revision); err != nil {
		return fmt.Errorf("insert recovery mail delivery: %w", translateError("mail_delivery", delivery.ID.String(), err))
	}
	if payloadKeyID != "" {
		if err := incrementMailPayloadKeyReference(ctx, tx, payloadKeyID); err != nil {
			return err
		}
	}
	return nil
}

func recoveryMailAt(occurrence *model.MailOccurrence, delivery *model.MailDelivery, job *model.Job, at time.Time) (*model.MailOccurrence, *model.MailDelivery, *model.Job, error) {
	if err := validateRecoveryMail(occurrence, delivery, job); err != nil {
		return nil, nil, nil, err
	}
	lifetime := delivery.Deadline.Sub(delivery.CreatedAt)
	return recoveryMailAtWithLifetime(occurrence, delivery, job, at, lifetime)
}

func recoveryMailAtWithLifetime(occurrence *model.MailOccurrence, delivery *model.MailDelivery, job *model.Job, at time.Time, lifetime time.Duration) (*model.MailOccurrence, *model.MailDelivery, *model.Job, error) {
	if err := validateRecoveryMail(occurrence, delivery, job); err != nil {
		return nil, nil, nil, err
	}
	if lifetime <= 0 {
		return nil, nil, nil, store.NewErrInvalidInput("mail_delivery", "deadline", nil)
	}
	rebasedOccurrence := *occurrence
	rebasedOccurrence.CreatedAt = model.TimeUTC(at)
	rebasedDelivery := *delivery
	rebasedDelivery.CreatedAt = model.TimeUTC(at)
	rebasedDelivery.UpdatedAt = model.TimeUTC(at)
	rebasedDelivery.MessageDate = model.TimeUTC(at)
	rebasedDelivery.Deadline = model.TimeUTC(at).Add(lifetime)
	rebasedJob := *job
	rebasedJob.CreatedAt = model.TimeUTC(at)
	rebasedJob.UpdatedAt = model.TimeUTC(at)
	rebasedJob.AvailableAt = model.TimeUTC(at)
	if rebasedJob.CompletedAt.Valid {
		rebasedJob.CompletedAt = model.OptionalTimeFrom(at)
	}
	if err := validateRecoveryMail(&rebasedOccurrence, &rebasedDelivery, &rebasedJob); err != nil {
		return nil, nil, nil, err
	}
	return &rebasedOccurrence, &rebasedDelivery, &rebasedJob, nil
}

func suppressSupersededTokenMail(ctx context.Context, tx *sqlxTxWrapper, occurrenceIDs []string, at time.Time) error {
	if len(occurrenceIDs) == 0 {
		return nil
	}
	var rows []mailDeliveryRow
	if err := tx.Select(ctx, &rows, `SELECT `+mailDeliveryColumns+` FROM mail_deliveries WHERE occurrence_id = ANY(?) FOR UPDATE`, pq.Array(occurrenceIDs)); err != nil {
		return fmt.Errorf("lock superseded token mail: %w", err)
	}
	for index := range rows {
		current, err := rows[index].model()
		if err != nil {
			return err
		}
		if current.State == model.MailDeliveryAccepted || current.State == model.MailDeliverySuppressed || current.State == model.MailDeliveryCanceled {
			continue
		}
		job, err := getJob(ctx, tx, current.JobID, true)
		if err != nil {
			return err
		}
		if job.Type != model.JobTypeMailDeliverCredential || job.DedupeKey != current.ID.String() {
			return invalidPersistedState("mail_delivery", "job", fmt.Errorf("superseded token delivery job relationship is invalid"))
		}
		transitionAt := model.TimeUTC(at)
		if transitionAt.Before(current.UpdatedAt) {
			transitionAt = current.UpdatedAt
		}
		if transitionAt.Before(job.UpdatedAt) {
			transitionAt = job.UpdatedAt
		}
		updated, err := current.Suppress(model.MailDeliveryObsoleteCode, transitionAt)
		if err != nil {
			return store.NewErrConflict("mail_delivery", "invalid_transition", err)
		}
		if err = updateMailDelivery(ctx, tx, current, updated); err != nil {
			return err
		}
		if job.Status == model.JobStatusQueued || job.Status == model.JobStatusRunning {
			updatedJob, cancelErr := job.RequestCancellation(transitionAt)
			if cancelErr != nil {
				return store.NewErrConflict("job", "invalid_transition", cancelErr)
			}
			if err = updateJob(ctx, tx, updatedJob); err != nil {
				return err
			}
		}
	}
	return nil
}

type userTokenRow struct {
	ID         string                 `db:"id"`
	CreatedAt  time.Time              `db:"created_at"`
	UpdatedAt  time.Time              `db:"updated_at"`
	ArchivedAt sql.NullTime           `db:"archived_at"`
	UserID     string                 `db:"user_id"`
	Purpose    model.UserTokenPurpose `db:"purpose"`
	TokenHash  string                 `db:"token_hash"`
	Target     string                 `db:"target"`
	ExpiresAt  time.Time              `db:"expires_at"`
	ConsumedAt sql.NullTime           `db:"consumed_at"`
}

func userTokenSliceColumns() []string {
	return []string{
		"user_tokens.id",
		"user_tokens.created_at",
		"user_tokens.updated_at",
		"user_tokens.archived_at",
		"user_tokens.user_id",
		"user_tokens.purpose",
		"user_tokens.token_hash",
		"user_tokens.target",
		"user_tokens.expires_at",
		"user_tokens.consumed_at",
	}
}

func newSQLUserTokenStore(sqlStore *SQLStore) store.UserTokenStore {
	s := &SQLUserTokenStore{SQLStore: sqlStore}
	s.tokensQuery = s.getQueryBuilder().
		Select(userTokenSliceColumns()...).
		From("user_tokens")
	return s
}

func (s SQLUserTokenStore) Issue(
	ctx context.Context,
	input *store.UserTokenMailIssue,
) (*model.UserToken, error) {
	if input == nil || input.Token == nil || input.AuditEvent == nil || input.Occurrence == nil || input.Delivery == nil || input.Job == nil {
		return nil, store.NewErrInvalidInput("user_token", "issue", nil)
	}
	candidate := *input.Token
	if err := candidate.Validate(); err != nil {
		return nil, err
	}
	if input.AuditEvent.Resource.Type != model.ResourceUser ||
		input.AuditEvent.Resource.ID != candidate.UserID.String() ||
		input.AuditEvent.Status != model.AuditStatusSuccess ||
		input.AuditEvent.ScopeType != model.RoleScopeInstitution || !model.IsValidId(input.AuditEvent.ScopeID) ||
		input.Occurrence.ID.String() != candidate.ID.String() || input.Occurrence.Kind != model.MailOccurrenceAccountToken ||
		input.Occurrence.ActorUserID != candidate.UserID || input.Delivery.TargetUserID != candidate.UserID ||
		input.Delivery.OccurrenceID != input.Occurrence.ID || input.Delivery.JobID != input.Job.ID ||
		input.Delivery.Deadline.After(candidate.ExpiresAt) || input.Delivery.State != model.MailDeliveryQueued ||
		len(input.Delivery.EncryptedPayload) == 0 || input.Job.Type != model.JobTypeMailDeliverCredential || input.Job.Status != model.JobStatusQueued {
		return nil, store.NewErrInvalidInput("user_token", "audit_event", nil)
	}
	if candidate.Purpose == model.UserTokenEmailVerification && input.Occurrence.TemplateKey != model.MailTemplateIdentityVerifyEmail ||
		candidate.Purpose == model.UserTokenPasswordReset && input.Occurrence.TemplateKey != model.MailTemplateIdentityPasswordReset {
		return nil, store.NewErrInvalidInput("user_token", "mail_template", nil)
	}
	expectedAuditAction := userTokenAuditEmailVerificationRequest
	if candidate.Purpose == model.UserTokenPasswordReset {
		expectedAuditAction = userTokenAuditPasswordResetRequest
	}
	if input.AuditEvent.Action != expectedAuditAction {
		return nil, store.NewErrInvalidInput("user_token", "audit_action", nil)
	}
	if err := validateRecoveryMail(input.Occurrence, input.Delivery, input.Job); err != nil {
		return nil, err
	}
	payloadKeyID, err := mailPayloadKeyID(input.Delivery.EncryptedPayload)
	if err != nil {
		return nil, store.NewErrInvalidInput("mail_delivery", "encrypted_payload", err)
	}

	return runSQLTransaction(ctx, s.GetMaster().Begin, "user token issue", func(ctx context.Context, tx *sqlxTxWrapper) (*model.UserToken, error) {
		if err := requireMailPayloadPrimary(ctx, tx, payloadKeyID); err != nil {
			return nil, err
		}
		if candidate.Purpose == model.UserTokenPasswordReset {
			if err := requireCurrentLocalLogin(ctx, tx); err != nil {
				return nil, err
			}
		}
		if err := lockUserTokenPurpose(
			ctx, tx, candidate.UserID.String(), candidate.Purpose,
		); err != nil {
			return nil, err
		}
		if err := lockEligibleUserTokenIssueTarget(ctx, tx, &candidate); err != nil {
			return nil, err
		}
		var priorIDs []string
		if err := tx.Select(ctx, &priorIDs, `SELECT id FROM user_tokens WHERE user_id = ? AND purpose = ? AND archived_at IS NULL AND consumed_at IS NULL FOR UPDATE`, candidate.UserID.String(), candidate.Purpose); err != nil {
			return nil, fmt.Errorf("lock prior user tokens: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE user_tokens
			   SET updated_at = ?, archived_at = ?
			 WHERE user_id = ? AND purpose = ?
			   AND archived_at IS NULL AND consumed_at IS NULL`,
			candidate.CreatedAt,
			candidate.CreatedAt,
			candidate.UserID.String(),
			candidate.Purpose,
		); err != nil {
			return nil, fmt.Errorf("invalidate prior user tokens: %w", err)
		}
		if err := insertUserToken(ctx, tx, &candidate); err != nil {
			return nil, err
		}
		if err := suppressSupersededTokenMail(ctx, tx, priorIDs, candidate.CreatedAt); err != nil {
			return nil, err
		}
		if err := insertRecoveryMail(ctx, tx, input.Occurrence, input.Delivery, input.Job, payloadKeyID); err != nil {
			return nil, err
		}
		if _, err := insertAuditEvent(ctx, tx, input.AuditEvent); err != nil {
			return nil, fmt.Errorf("audit user token issue: %w", err)
		}
		return &candidate, nil
	})
}

func (s SQLUserTokenStore) Get(ctx context.Context, id model.UserTokenID) (*model.UserToken, error) {
	if !id.IsValid() {
		return nil, store.NewErrInvalidInput("user_token", "id", nil)
	}
	var row userTokenRow
	if err := s.GetMaster().GetBuilder(ctx, &row, s.tokensQuery.Where(sq.Eq{"user_tokens.id": id.String()})); err != nil {
		return nil, translateError("user_token", id.String(), err)
	}
	return row.model()
}

func (s SQLUserTokenStore) GetByHash(
	ctx context.Context,
	tokenHash string,
	purpose model.UserTokenPurpose,
) (*model.UserToken, error) {
	if !model.IsValidTokenHash(tokenHash) || !purpose.IsValid() {
		return nil, store.NewErrInvalidInput("user_token", "lookup", nil)
	}
	var row userTokenRow
	query := s.tokensQuery.Where(sq.Eq{
		"user_tokens.token_hash": tokenHash,
		"user_tokens.purpose":    purpose,
	})
	if err := s.GetMaster().GetBuilder(ctx, &row, query); err != nil {
		return nil, translateError("user_token", "", err)
	}
	return row.model()
}

func (s SQLUserTokenStore) ConsumeEmailVerification(
	ctx context.Context,
	tokenHash string,
	now int64,
	auditEvent *model.AuditEvent,
) (*store.EmailVerificationResult, error) {
	if !model.IsValidTokenHash(tokenHash) || now <= 0 || auditEvent == nil {
		return nil, store.NewErrInvalidInput("user_token", "consume", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "email verification", func(ctx context.Context, tx *sqlxTxWrapper) (*store.EmailVerificationResult, error) {
		if err := lockUserTokenPurposeByHash(ctx, tx, tokenHash, model.UserTokenEmailVerification); err != nil {
			return nil, err
		}
		databaseNow, err := jobDatabaseNow(ctx, tx)
		if err != nil {
			return nil, err
		}
		at := databaseNow.Truncate(time.Millisecond)
		token, err := lockActiveUserToken(
			ctx, tx, tokenHash, model.UserTokenEmailVerification, databaseNow,
		)
		if err != nil {
			return nil, err
		}
		user, err := lockTokenUser(ctx, tx, token)
		if err != nil {
			return nil, err
		}
		mailEligibilityRevision, err := advanceUserMailEligibilityRevision(ctx, tx)
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `
		UPDATE users
		   SET updated_at = ?, email_verified = true, mail_eligibility_revision = ?, revision = revision + 1
		 WHERE id = ? AND archived_at IS NULL AND disabled_at IS NULL`,
			at, mailEligibilityRevision, user.ID,
		); err != nil {
			return nil, fmt.Errorf("verify user email: %w", err)
		}
		if err := consumeUserTokens(
			ctx, tx, token.UserID, token.Purpose, at,
		); err != nil {
			return nil, err
		}
		event, err := tokenAuditEvent(auditEvent, token.UserID, userTokenAuditEmailVerificationComplete)
		if err != nil {
			return nil, err
		}
		if _, err := insertAuditEventAt(ctx, tx, event, at); err != nil {
			return nil, fmt.Errorf("audit email verification: %w", err)
		}
		token.ConsumedAt = sql.NullTime{Time: at, Valid: true}
		token.UpdatedAt = at
		verified, err := user.model()
		if err != nil {
			return nil, err
		}
		verified.UpdatedAt = at
		verified.EmailVerified = true
		verified.Revision++
		rehydratedToken, err := token.model()
		if err != nil {
			return nil, err
		}
		return &store.EmailVerificationResult{Token: rehydratedToken, User: verified}, nil
	})
}

func (s SQLUserTokenStore) ConsumePasswordReset(
	ctx context.Context,
	input *store.PasswordResetCompletion,
) (*store.PasswordResetResult, error) {
	if input == nil || !model.IsValidTokenHash(input.TokenHash) ||
		!model.IsValidPasswordHash(input.PasswordHash) || input.At <= 0 || input.AuditEvent == nil ||
		!input.RevocationReason.IsValid() || input.Occurrence == nil || input.Delivery == nil || input.Job == nil {
		return nil, store.NewErrInvalidInput("user_token", "password_reset", nil)
	}
	if input.Occurrence.Kind != model.MailOccurrenceSecurityNotice || input.Occurrence.TemplateKey != model.MailTemplateIdentityPasswordChanged ||
		input.Delivery.TemplateKey != model.MailTemplateIdentityPasswordChanged || input.Job.Type != model.JobTypeMailDeliver {
		return nil, store.NewErrInvalidInput("user_token", "password_reset_mail", nil)
	}
	if err := validateRecoveryMail(input.Occurrence, input.Delivery, input.Job); err != nil {
		return nil, err
	}
	payloadKeyID, err := mailPayloadKeyID(input.Delivery.EncryptedPayload)
	if err != nil {
		return nil, store.NewErrInvalidInput("mail_delivery", "encrypted_payload", err)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "password reset", func(ctx context.Context, tx *sqlxTxWrapper) (*store.PasswordResetResult, error) {
		if payloadKeyID != "" {
			if err := requireMailPayloadPrimary(ctx, tx, payloadKeyID); err != nil {
				return nil, err
			}
		}
		if err := requireCurrentLocalLogin(ctx, tx); err != nil {
			return nil, err
		}
		if err := lockUserTokenPurposeByHash(ctx, tx, input.TokenHash, model.UserTokenPasswordReset); err != nil {
			return nil, err
		}
		databaseNow, err := jobDatabaseNow(ctx, tx)
		if err != nil {
			return nil, err
		}
		at := databaseNow.Truncate(time.Millisecond)
		occurrence, delivery, job, err := recoveryMailAt(input.Occurrence, input.Delivery, input.Job, at)
		if err != nil {
			return nil, err
		}
		token, err := lockActiveUserToken(
			ctx, tx, input.TokenHash, model.UserTokenPasswordReset, databaseNow,
		)
		if err != nil {
			return nil, err
		}
		user, err := lockTokenUser(ctx, tx, token)
		if err != nil {
			return nil, err
		}
		var credential passwordCredentialRow
		if err := tx.Get(ctx, &credential, `
		SELECT id, created_at, updated_at, archived_at, user_id,
		       password_hash, password_changed_at
		  FROM password_credentials
		 WHERE user_id = ? AND archived_at IS NULL
		 FOR UPDATE`,
			token.UserID,
		); err != nil {
			return nil, translateError("password_credential", token.UserID, err)
		}
		credential.PasswordHash = input.PasswordHash
		credential.PasswordChangedAt = at
		credential.UpdatedAt = at
		if _, err := tx.Exec(ctx, `
		UPDATE password_credentials
		   SET updated_at = ?, password_hash = ?, password_changed_at = ?
		 WHERE id = ? AND user_id = ? AND archived_at IS NULL`,
			at, input.PasswordHash, at, credential.ID, token.UserID,
		); err != nil {
			return nil, fmt.Errorf("update reset password: %w", err)
		}
		if err := lockUserSessions(ctx, tx, token.UserID); err != nil {
			return nil, err
		}
		sessionRows, hashes, err := revokeAllUserSessionsAt(
			ctx, tx, token.UserID, at, input.RevocationReason,
		)
		if err != nil {
			return nil, err
		}
		if err := consumeUserTokens(
			ctx, tx, token.UserID, token.Purpose, at,
		); err != nil {
			return nil, err
		}
		event, err := tokenAuditEvent(input.AuditEvent, token.UserID, userTokenAuditPasswordResetComplete)
		if err != nil {
			return nil, err
		}
		if _, err := insertAuditEventAt(ctx, tx, event, at); err != nil {
			return nil, fmt.Errorf("audit password reset: %w", err)
		}
		if occurrence.ActorUserID.String() != token.UserID || delivery.TargetUserID.String() != token.UserID {
			return nil, store.NewErrInvalidInput("user_token", "password_reset_mail_target", nil)
		}
		if err := insertRecoveryMail(ctx, tx, occurrence, delivery, job, payloadKeyID); err != nil {
			return nil, err
		}
		token.ConsumedAt = sql.NullTime{Time: at, Valid: true}
		token.UpdatedAt = at
		rehydratedUser, err := user.model()
		if err != nil {
			return nil, err
		}
		revokedSessions, err := revokedSessionModelsAt(sessionRows, at, input.RevocationReason)
		if err != nil {
			return nil, err
		}
		rehydratedToken, err := token.model()
		if err != nil {
			return nil, err
		}
		rehydratedCredential, err := credential.model()
		if err != nil {
			return nil, err
		}
		return &store.PasswordResetResult{
			Token:               rehydratedToken,
			User:                rehydratedUser,
			PasswordCredential:  rehydratedCredential,
			RevokedSessions:     revokedSessions,
			RevokedAccessHashes: hashes,
		}, nil
	})
}

func insertUserToken(
	ctx context.Context,
	executor sqlxExecutor,
	token *model.UserToken,
) error {
	row := newUserTokenRow(token)
	if _, err := executor.NamedExec(ctx, `
		INSERT INTO user_tokens (
			id, created_at, updated_at, archived_at, user_id, purpose,
			token_hash, target, expires_at, consumed_at
		) VALUES (
			:id, :created_at, :updated_at, :archived_at, :user_id, :purpose,
			:token_hash, :target, :expires_at, :consumed_at
		)`, &row); err != nil {
		return fmt.Errorf(
			"save user token: %w",
			translateError("user_token", token.ID.String(), err),
		)
	}
	return nil
}

func lockUserTokenPurpose(
	ctx context.Context,
	executor sqlxExecutor,
	userID string,
	purpose model.UserTokenPurpose,
) error {
	if _, err := executor.Exec(
		ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		"proctor:user-token:"+userID+":"+string(purpose),
	); err != nil {
		return fmt.Errorf("lock user token purpose: %w", err)
	}
	return nil
}

func lockUserTokenPurposeByHash(
	ctx context.Context,
	executor sqlxExecutor,
	tokenHash string,
	purpose model.UserTokenPurpose,
) error {
	var userID string
	if err := executor.Get(ctx, &userID, `SELECT user_id FROM user_tokens WHERE token_hash = ? AND purpose = ?`, tokenHash, purpose); err != nil {
		return translateError("user_token", "", err)
	}
	return lockUserTokenPurpose(ctx, executor, userID, purpose)
}

func lockEligibleUserTokenIssueTarget(
	ctx context.Context,
	executor sqlxExecutor,
	token *model.UserToken,
) error {
	var userID string
	if err := executor.Get(ctx, &userID, `
		SELECT id
		  FROM users
		 WHERE id = ? AND email = ?
		   AND archived_at IS NULL AND disabled_at IS NULL
		 FOR SHARE`, token.UserID.String(), token.Target); err != nil {
		if err == sql.ErrNoRows {
			return store.NewErrNotFound("user_token", "").Wrap(err)
		}
		return fmt.Errorf("lock user token issue target: %w", err)
	}
	if token.Purpose != model.UserTokenPasswordReset {
		return nil
	}
	var credentialID string
	if err := executor.Get(ctx, &credentialID, `
		SELECT id
		  FROM password_credentials
		 WHERE user_id = ? AND archived_at IS NULL
		 FOR SHARE`, userID); err != nil {
		if err == sql.ErrNoRows {
			return store.NewErrNotFound("user_token", "").Wrap(err)
		}
		return fmt.Errorf("lock password reset credential: %w", err)
	}
	return nil
}

func lockActiveUserToken(
	ctx context.Context,
	executor sqlxExecutor,
	tokenHash string,
	purpose model.UserTokenPurpose,
	at time.Time,
) (*userTokenRow, error) {
	var row userTokenRow
	if err := executor.Get(ctx, &row, `
		SELECT id, created_at, updated_at, archived_at, user_id, purpose,
		       token_hash, target, expires_at, consumed_at
		  FROM user_tokens
		 WHERE token_hash = ? AND purpose = ?
		   AND archived_at IS NULL AND consumed_at IS NULL AND expires_at > ?
		 FOR UPDATE`,
		tokenHash, purpose, model.TimeUTC(at),
	); err != nil {
		return nil, translateError("user_token", "", err)
	}
	return &row, nil
}

func lockTokenUser(
	ctx context.Context,
	executor sqlxExecutor,
	token *userTokenRow,
) (*userRow, error) {
	var user userRow
	if err := executor.Get(ctx, &user, `
		SELECT id, created_at, updated_at, archived_at, revision, username, email,
		       email_verified, display_name, first_name, last_name, locale,
		       timezone, last_login_at, last_activity_at, disabled_at
		       , default_profile_picture_seed, default_profile_picture_file_id,
		       custom_profile_picture_file_id, profile_picture_changed_at
		  FROM users
		 WHERE id = ? AND email = ? AND archived_at IS NULL AND disabled_at IS NULL
		 FOR UPDATE`,
		token.UserID, token.Target,
	); err != nil {
		return nil, translateError("user_token", "", err)
	}
	return &user, nil
}

func consumeUserTokens(
	ctx context.Context,
	executor sqlxExecutor,
	userID string,
	purpose model.UserTokenPurpose,
	at time.Time,
) error {
	at = model.TimeUTC(at)
	result, err := executor.Exec(ctx, `
		UPDATE user_tokens
		   SET updated_at = ?, consumed_at = ?
		 WHERE user_id = ? AND purpose = ?
		   AND archived_at IS NULL AND consumed_at IS NULL`,
		at, at, userID, purpose,
	)
	if err != nil {
		return fmt.Errorf("consume user tokens: %w", err)
	}
	if err := requireAffected(result, "user_token", userID); err != nil {
		return err
	}
	return nil
}

func tokenAuditEvent(
	event *model.AuditEvent,
	userID string,
	expectedAction string,
) (*model.AuditEvent, error) {
	if event == nil || event.Status != model.AuditStatusSuccess || event.Action != expectedAction ||
		event.ScopeType != model.RoleScopeInstitution || !model.IsValidId(event.ScopeID) {
		return nil, store.NewErrInvalidInput("user_token", "audit_event", nil)
	}
	candidate := event.Clone()
	if candidate.Resource.Type == "" {
		candidate.Resource.Type = model.ResourceUser
	}
	if candidate.Resource.Type != model.ResourceUser {
		return nil, store.NewErrInvalidInput("user_token", "audit_resource", nil)
	}
	if candidate.Resource.ID != "" && candidate.Resource.ID != userID {
		return nil, store.NewErrInvalidInput("user_token", "audit_resource_id", nil)
	}
	candidate.Resource.ID = userID
	return candidate, nil
}

func newUserTokenRow(token *model.UserToken) userTokenRow {
	return userTokenRow{
		ID:         token.ID.String(),
		CreatedAt:  UTCTime(token.CreatedAt),
		UpdatedAt:  UTCTime(token.UpdatedAt),
		ArchivedAt: NullTimeFromOptional(token.ArchivedAt),
		UserID:     token.UserID.String(),
		Purpose:    token.Purpose,
		TokenHash:  token.TokenHash,
		Target:     token.Target,
		ExpiresAt:  UTCTime(token.ExpiresAt),
		ConsumedAt: NullTimeFromOptional(token.ConsumedAt),
	}
}

func (row userTokenRow) model() (*model.UserToken, error) {
	id, err := parsePersistedID("user_token", "id", row.ID, model.ParseUserTokenID)
	if err != nil {
		return nil, err
	}
	userID, err := parsePersistedID("user_token", "user_id", row.UserID, model.ParseUserID)
	if err != nil {
		return nil, err
	}
	value := &model.UserToken{
		ID:         id,
		CreatedAt:  row.CreatedAt.UTC(),
		UpdatedAt:  row.UpdatedAt.UTC(),
		ArchivedAt: OptionalTimeFromNullTime(row.ArchivedAt),
		UserID:     userID,
		Purpose:    row.Purpose,
		TokenHash:  row.TokenHash,
		Target:     row.Target,
		ExpiresAt:  row.ExpiresAt.UTC(),
		ConsumedAt: OptionalTimeFromNullTime(row.ConsumedAt),
	}
	if err := validatePersistedModel("user_token", value); err != nil {
		return nil, err
	}
	return value, nil
}

var _ store.UserTokenStore = (*SQLUserTokenStore)(nil)
