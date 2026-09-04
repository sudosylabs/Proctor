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
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SQLMailStore struct{ *SQLStore }

type mailDeliveryRow struct {
	ID                 string         `db:"id"`
	OccurrenceID       string         `db:"occurrence_id"`
	JobID              string         `db:"job_id"`
	TargetUserID       sql.NullString `db:"target_user_id"`
	TargetInvitationID sql.NullString `db:"target_invitation_id"`
	TemplateKey        string         `db:"template_key"`
	TemplateDigest     string         `db:"template_digest"`
	MaskedRecipient    string         `db:"masked_recipient"`
	State              string         `db:"state"`
	CreatedAt          time.Time      `db:"created_at"`
	UpdatedAt          time.Time      `db:"updated_at"`
	MessageDate        time.Time      `db:"message_date"`
	Deadline           time.Time      `db:"deadline"`
	MessageID          string         `db:"message_id"`
	AttemptCount       int            `db:"attempt_count"`
	AcceptedAt         sql.NullTime   `db:"accepted_at"`
	FailedAt           sql.NullTime   `db:"failed_at"`
	PublicFailureCode  string         `db:"public_failure_code"`
	PayloadKeyID       sql.NullString `db:"payload_key_id"`
	EncryptedPayload   jsonValue      `db:"encrypted_payload"`
	Revision           int64          `db:"revision"`
}

const mailDeliveryColumns = `id, occurrence_id, job_id, target_user_id, target_invitation_id, template_key, template_digest, masked_recipient, state, created_at, updated_at, message_date, deadline, message_id, attempt_count, accepted_at, failed_at, public_failure_code, payload_key_id, encrypted_payload, revision`

func newSQLMailStore(sqlStore *SQLStore) store.MailStore { return &SQLMailStore{SQLStore: sqlStore} }

func (s SQLMailStore) EnqueueTest(ctx context.Context, input *store.MailTestEnqueue) (*model.MailDelivery, error) {
	if err := validateMailTestEnqueue(input); err != nil {
		return nil, err
	}
	payloadKeyID, err := mailPayloadKeyID(input.Delivery.EncryptedPayload)
	if err != nil {
		return nil, store.NewErrInvalidInput("mail_delivery", "encrypted_payload", err)
	}
	return executeSQLTransaction(ctx, s.GetMaster().Begin, rawSQLTransactionPolicy[*model.MailDelivery](true, func(_ *model.MailDelivery, err error) error { return fmt.Errorf("commit test mail enqueue: %w", err) }), func(ctx context.Context, tx *sqlxTxWrapper) (*model.MailDelivery, error) {
		if err := requireMailPayloadPrimary(ctx, tx, payloadKeyID); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO mail_occurrences (id, kind, template_key, actor_user_id, created_at) VALUES (?, ?, ?, ?, ?)`, input.Occurrence.ID.String(), string(input.Occurrence.Kind), string(input.Occurrence.TemplateKey), input.Occurrence.ActorUserID.String(), input.Occurrence.CreatedAt); err != nil {
			return nil, fmt.Errorf("insert mail occurrence: %w", translateError("mail_occurrence", input.Occurrence.ID.String(), err))
		}
		if _, err := insertQueuedJob(ctx, tx, input.Job, false); err != nil {
			return nil, fmt.Errorf("insert mail delivery job: %w", translateError("job", input.Job.ID.String(), err))
		}
		if _, err := tx.Exec(ctx, `INSERT INTO mail_deliveries (`+mailDeliveryColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, NULL, NULL, '', ?, ?, 1)`, input.Delivery.ID.String(), input.Delivery.OccurrenceID.String(), input.Delivery.JobID.String(), nullableID(input.Delivery.TargetUserID.String()), nullableID(input.Delivery.TargetInvitationID.String()), string(input.Delivery.TemplateKey), input.Delivery.TemplateDigest, input.Delivery.MaskedRecipient, string(input.Delivery.State), input.Delivery.CreatedAt, input.Delivery.UpdatedAt, input.Delivery.MessageDate, input.Delivery.Deadline, input.Delivery.MessageID, payloadKeyID, input.Delivery.EncryptedPayload); err != nil {
			return nil, fmt.Errorf("insert mail delivery: %w", translateError("mail_delivery", input.Delivery.ID.String(), err))
		}
		if err := incrementMailPayloadKeyReference(ctx, tx, payloadKeyID); err != nil {
			return nil, err
		}
		if _, err := insertAuditEvent(ctx, tx, input.AuditEvent); err != nil {
			return nil, fmt.Errorf("insert test mail audit: %w", err)
		}
		return input.Delivery.Clone(), nil
	})
}

// requireMailPayloadPrimary takes a shared lock on the durable primary-key
// fence for the lifetime of the caller's transaction. StartRekey takes the
// corresponding exclusive lock, so every encrypted insertion is ordered
// wholly before or wholly after promotion: an old-primary transaction cannot
// commit behind the rekey page scan or zero-reference proof.
func requireMailPayloadPrimary(ctx context.Context, tx *sqlxTxWrapper, payloadKeyID string) error {
	var requiredPrimary sql.NullString
	if err := tx.Get(ctx, &requiredPrimary, `SELECT required_primary_key_id FROM mail_key_state WHERE singleton = TRUE FOR SHARE`); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return invalidPersistedState("mail_rekey", "key_state", errors.New("mail primary-key fence is missing"))
		}
		return fmt.Errorf("lock mail primary-key fence: %w", err)
	}
	if requiredPrimary.Valid && requiredPrimary.String != payloadKeyID {
		return store.NewErrConflict("mail_delivery", "stale_primary_key", nil)
	}
	return nil
}

// insertPreparedMailJob persists either claimable mail work or the exact
// terminal canceled Job paired with a disabled, ciphertext-free delivery.
func insertPreparedMailJob(ctx context.Context, executor sqlxExecutor, job *model.Job) error {
	if job.Status == model.JobStatusQueued {
		_, err := insertQueuedJob(ctx, executor, job, false)
		return err
	}
	if job.Status != model.JobStatusCanceled || !job.CompletedAt.Valid {
		return store.NewErrInvalidInput("job", "prepared_mail", nil)
	}
	_, err := executor.Exec(ctx, `INSERT INTO jobs (`+jobColumns+`) VALUES (?, ?, ?, ?, ?, ?, NULL, ?, ?, ?, NULL, NULL, NULL, NULL, ?, ?, ?, ?, ?, ?, NULL, NULL, NULL, ?)`,
		job.ID.String(), string(job.Type), string(job.Status), job.CreatedAt, job.UpdatedAt, job.AvailableAt,
		job.CompletedAt.Time, job.CommandVersion, job.Command, job.PublicErrorCode, job.DedupeKey,
		string(job.DedupePolicy), job.AttemptCount, job.MaximumAttempts, job.WorkReserved, job.Revision)
	return err
}

func validateMailTestEnqueue(input *store.MailTestEnqueue) error {
	if input == nil || input.Occurrence == nil || input.Delivery == nil || input.Job == nil || input.AuditEvent == nil {
		return store.NewErrInvalidInput("mail_test", "value", nil)
	}
	if err := input.Occurrence.Validate(); err != nil {
		return store.NewErrInvalidInput("mail_occurrence", "value", err)
	}
	if err := input.Delivery.Validate(); err != nil {
		return store.NewErrInvalidInput("mail_delivery", "value", err)
	}
	if err := input.Job.Validate(); err != nil {
		return store.NewErrInvalidInput("job", "value", err)
	}
	if !input.AuditEvent.ID.IsZero() || input.AuditEvent.Status != model.AuditStatusSuccess ||
		input.AuditEvent.ActorID != input.Occurrence.ActorUserID ||
		input.AuditEvent.Action != string(model.ActionMailManage) ||
		input.AuditEvent.Resource.Type != model.ResourceMailDelivery ||
		input.AuditEvent.Resource.ID != input.Delivery.ID.String() ||
		input.AuditEvent.ScopeType != model.RoleScopeInstitution || !model.IsValidId(input.AuditEvent.ScopeID) {
		return store.NewErrInvalidInput("audit_event", "value", nil)
	}
	command, err := model.DecodeMailDeliveryCommand(input.Job.CommandVersion, input.Job.Command)
	if err != nil || input.Occurrence.Kind != model.MailOccurrenceOperatorTest || input.Delivery.State != model.MailDeliveryQueued ||
		input.Job.Type != model.JobTypeMailDeliver || input.Job.Status != model.JobStatusQueued || input.Job.AttemptCount != 0 ||
		input.Job.Revision != 1 || input.Job.DedupePolicy != model.JobDedupeActive || input.Job.MaximumAttempts != model.MailMaximumAttempts ||
		input.Job.StartedAt.Valid || input.Job.CompletedAt.Valid || input.Job.PublicErrorCode != "" || len(input.Job.Checkpoint) != 0 || len(input.Job.Result) != 0 || input.Job.Progress != nil || input.Job.WorkReserved != 0 ||
		input.Delivery.AttemptCount != 0 || input.Delivery.Revision != 1 || input.Delivery.PublicFailureCode != "" || input.Delivery.AcceptedAt.Valid || input.Delivery.FailedAt.Valid || len(input.Delivery.EncryptedPayload) == 0 ||
		input.Delivery.OccurrenceID != input.Occurrence.ID || input.Delivery.JobID != input.Job.ID ||
		input.Delivery.TargetUserID != input.Occurrence.ActorUserID || input.Delivery.TemplateKey != input.Occurrence.TemplateKey ||
		!input.Occurrence.CreatedAt.Equal(input.Delivery.CreatedAt) || !input.Delivery.UpdatedAt.Equal(input.Delivery.CreatedAt) || !input.Delivery.MessageDate.Equal(input.Delivery.CreatedAt) ||
		!input.Job.CreatedAt.Equal(input.Delivery.CreatedAt) || !input.Job.UpdatedAt.Equal(input.Delivery.CreatedAt) || !input.Job.AvailableAt.Equal(input.Delivery.CreatedAt) ||
		command.DeliveryID != input.Delivery.ID || input.Job.DedupeKey != input.Delivery.ID.String() {
		return store.NewErrInvalidInput("mail_test", "relationship", err)
	}
	return nil
}

func (s SQLMailStore) GetDelivery(ctx context.Context, id model.MailDeliveryID) (*model.MailDelivery, error) {
	if !id.IsValid() {
		return nil, store.NewErrInvalidInput("mail_delivery", "id", nil)
	}
	var row mailDeliveryRow
	if err := s.GetMaster().Get(ctx, &row, `SELECT `+mailDeliveryColumns+` FROM mail_deliveries WHERE id = ?`, id.String()); err != nil {
		return nil, translateError("mail_delivery", id.String(), err)
	}
	return row.model()
}

func (s SQLMailStore) ListDeliveries(ctx context.Context, options store.MailDeliveryListOptions) ([]*model.MailDelivery, error) {
	if options.Limit < 1 || options.Limit > 200 || len(options.States) > 6 || len(options.TemplateKeys) > 64 ||
		(options.BeforeCreatedAt.IsZero() != options.BeforeID.IsZero()) ||
		(!options.BeforeID.IsZero() && !options.BeforeID.IsValid()) ||
		(!options.CreatedAfter.IsZero() && !options.CreatedBefore.IsZero() && !options.CreatedAfter.Before(options.CreatedBefore)) {
		return nil, store.NewErrInvalidInput("mail_delivery", "list", nil)
	}
	conditions := make([]string, 0, 5)
	arguments := make([]any, 0, len(options.States)+len(options.TemplateKeys)+8)
	if len(options.States) > 0 {
		placeholders := make([]string, 0, len(options.States))
		seen := make(map[model.MailDeliveryState]struct{}, len(options.States))
		for _, state := range options.States {
			if !state.IsValid() {
				return nil, store.NewErrInvalidInput("mail_delivery", "state", nil)
			}
			if _, duplicate := seen[state]; duplicate {
				continue
			}
			seen[state] = struct{}{}
			placeholders = append(placeholders, "?")
			arguments = append(arguments, string(state))
		}
		conditions = append(conditions, "state IN ("+strings.Join(placeholders, ", ")+")")
	}
	if len(options.TemplateKeys) > 0 {
		placeholders := make([]string, 0, len(options.TemplateKeys))
		seen := make(map[model.MailTemplateKey]struct{}, len(options.TemplateKeys))
		for _, key := range options.TemplateKeys {
			if !key.IsValid() {
				return nil, store.NewErrInvalidInput("mail_delivery", "template_key", nil)
			}
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			placeholders = append(placeholders, "?")
			arguments = append(arguments, string(key))
		}
		conditions = append(conditions, "template_key IN ("+strings.Join(placeholders, ", ")+")")
	}
	if !options.CreatedAfter.IsZero() {
		conditions = append(conditions, "created_at >= ?")
		arguments = append(arguments, model.TimeUTC(options.CreatedAfter))
	}
	if !options.CreatedBefore.IsZero() {
		conditions = append(conditions, "created_at < ?")
		arguments = append(arguments, model.TimeUTC(options.CreatedBefore))
	}
	if !options.BeforeCreatedAt.IsZero() {
		conditions = append(conditions, "(created_at < ? OR (created_at = ? AND id < ?))")
		arguments = append(arguments, model.TimeUTC(options.BeforeCreatedAt), model.TimeUTC(options.BeforeCreatedAt), options.BeforeID.String())
	}
	query := `SELECT ` + mailDeliveryColumns + ` FROM mail_deliveries`
	if len(conditions) > 0 {
		query += ` WHERE ` + strings.Join(conditions, ` AND `)
	}
	query += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	arguments = append(arguments, options.Limit)
	var rows []mailDeliveryRow
	if err := s.GetMaster().Select(ctx, &rows, query, arguments...); err != nil {
		return nil, fmt.Errorf("list mail deliveries: %w", err)
	}
	result := make([]*model.MailDelivery, 0, len(rows))
	for index := range rows {
		delivery, err := rows[index].model()
		if err != nil {
			return nil, err
		}
		result = append(result, delivery)
	}
	return result, nil
}

func (s SQLMailStore) StartDelivery(ctx context.Context, id model.MailDeliveryID, expectedRevision int64, at time.Time) (*model.MailDelivery, error) {
	if !id.IsValid() || expectedRevision <= 0 || at.IsZero() {
		return nil, store.NewErrInvalidInput("mail_delivery", "start", nil)
	}
	return executeSQLTransaction(ctx, s.GetMaster().Begin, rawSQLTransactionPolicy[*model.MailDelivery](true, func(_ *model.MailDelivery, err error) error {
		return fmt.Errorf("commit mail delivery start: %w", err)
	}), func(ctx context.Context, tx *sqlxTxWrapper) (*model.MailDelivery, error) {
		// Read immutable routing fields before taking the purpose advisory lock.
		// Issuance takes the same lock before suppressing superseded deliveries;
		// this ordering prevents Start from deadlocking with or escaping reissue.
		var route struct {
			OccurrenceID       string         `db:"occurrence_id"`
			TargetUserID       sql.NullString `db:"target_user_id"`
			TargetInvitationID sql.NullString `db:"target_invitation_id"`
			TemplateKey        string         `db:"template_key"`
		}
		if err := tx.Get(ctx, &route, `SELECT occurrence_id,target_user_id,target_invitation_id,template_key FROM mail_deliveries WHERE id=?`, id.String()); err != nil {
			return nil, translateError("mail_delivery", id.String(), err)
		}
		templateKey := model.MailTemplateKey(route.TemplateKey)
		purpose, credential := recoveryTokenPurpose(templateKey)
		if credential {
			if !route.TargetUserID.Valid {
				return nil, invalidPersistedState("mail_delivery", "target_user_id", errors.New("credential delivery has no user target"))
			}
			if err := lockUserTokenPurpose(ctx, tx, route.TargetUserID.String, purpose); err != nil {
				return nil, err
			}
		}
		invitationCredential := isInvitationCredentialTemplate(templateKey)
		if invitationCredential {
			if !route.TargetInvitationID.Valid {
				return nil, invalidPersistedState("mail_delivery", "target_invitation_id", errors.New("invitation credential delivery has no Invitation target"))
			}
			if _, err := lockActiveInvitationMail(ctx, tx, route.TargetInvitationID.String); err != nil {
				return nil, err
			}
		}
		var sittingFence *sittingMailDeliveryFence
		if isSittingScheduleMailTemplate(templateKey) {
			if !route.TargetUserID.Valid {
				return nil, invalidPersistedState("mail_delivery", "target_user_id", errors.New("Sitting delivery has no user target"))
			}
			fence, fenceErr := lockSittingMailDeliveryFence(ctx, tx, id)
			if fenceErr != nil {
				return nil, fenceErr
			}
			sittingFence = fence
		}
		databaseNow, err := jobDatabaseNow(ctx, tx)
		if err != nil {
			return nil, err
		}
		var row mailDeliveryRow
		if err := tx.Get(ctx, &row, `SELECT `+mailDeliveryColumns+` FROM mail_deliveries WHERE id=? FOR UPDATE`, id.String()); err != nil {
			return nil, translateError("mail_delivery", id.String(), err)
		}
		current, err := row.model()
		if err != nil {
			return nil, err
		}
		if current.Revision != expectedRevision {
			return nil, store.NewErrConflict("mail_delivery", "stale_revision", nil)
		}
		if credential {
			relevant, relevanceErr := activeRecoveryTokenMail(ctx, tx, current, purpose, databaseNow)
			if relevanceErr != nil {
				return nil, relevanceErr
			}
			if !relevant {
				transitionAt := databaseNow
				if transitionAt.Before(current.UpdatedAt) {
					transitionAt = current.UpdatedAt
				}
				updated, suppressErr := current.Suppress(model.MailDeliveryObsoleteCode, transitionAt)
				if suppressErr != nil {
					return nil, invalidPersistedState("mail_delivery", "suppression", suppressErr)
				}
				if err = updateMailDelivery(ctx, tx, current, updated); err != nil {
					return nil, err
				}
				return updated, nil
			}
		}
		if invitationCredential {
			relevant, relevanceErr := activeInvitationMail(ctx, tx, current, databaseNow)
			if relevanceErr != nil {
				return nil, relevanceErr
			}
			if !relevant {
				transitionAt := databaseNow
				if transitionAt.Before(current.UpdatedAt) {
					transitionAt = current.UpdatedAt
				}
				updated, suppressErr := current.Suppress(model.MailDeliveryObsoleteCode, transitionAt)
				if suppressErr != nil {
					return nil, invalidPersistedState("mail_delivery", "suppression", suppressErr)
				}
				if err = updateMailDelivery(ctx, tx, current, updated); err != nil {
					return nil, err
				}
				return updated, nil
			}
		}
		if sittingFence != nil {
			relevant, relevanceErr := sittingFence.relevant(ctx, tx, current.ID)
			if relevanceErr != nil {
				return nil, relevanceErr
			}
			if !relevant {
				transitionAt := sittingFence.Now
				if transitionAt.Before(current.UpdatedAt) {
					transitionAt = current.UpdatedAt
				}
				if err = clearExactSittingDesiredDelivery(ctx, tx, sittingFence, current.ID, transitionAt); err != nil {
					return nil, err
				}
				updated, suppressErr := current.Suppress(model.MailDeliveryObsoleteCode, transitionAt)
				if suppressErr != nil {
					return nil, invalidPersistedState("mail_delivery", "suppression", suppressErr)
				}
				if err = updateMailDelivery(ctx, tx, current, updated); err != nil {
					return nil, err
				}
				return updated, nil
			}
		}
		startAt := at
		if credential || invitationCredential || sittingFence != nil {
			startAt = databaseNow
			if startAt.Before(current.UpdatedAt) {
				startAt = current.UpdatedAt
			}
		}
		updated, err := current.Start(startAt)
		if err != nil {
			return nil, store.NewErrConflict("mail_delivery", "invalid_transition", err)
		}
		if err = updateMailDelivery(ctx, tx, current, updated); err != nil {
			return nil, err
		}
		return updated, nil
	})
}

func recoveryTokenPurpose(key model.MailTemplateKey) (model.UserTokenPurpose, bool) {
	switch key {
	case model.MailTemplateIdentityVerifyEmail:
		return model.UserTokenEmailVerification, true
	case model.MailTemplateIdentityEmailChangeVerifyNew:
		return model.UserTokenEmailVerification, true
	case model.MailTemplateIdentityPasswordReset:
		return model.UserTokenPasswordReset, true
	default:
		return "", false
	}
}

// suppressInvitationCredentialMail terminates recoverable credential delivery
// work while the caller still owns the transaction that made the Invitation
// irrelevant. The Invitation mutation, ciphertext destruction, payload-key
// reference decrement, and Job cancellation therefore commit atomically.
func suppressInvitationCredentialMail(
	ctx context.Context,
	tx *sqlxTxWrapper,
	invitationID model.InvitationID,
	publicCode string,
	at time.Time,
) error {
	if tx == nil || !invitationID.IsValid() || at.IsZero() {
		return store.NewErrInvalidInput("mail_delivery", "target_suppression", nil)
	}
	var rows []mailDeliveryRow
	if err := tx.Select(ctx, &rows, `SELECT `+mailDeliveryColumns+` FROM mail_deliveries
		WHERE target_invitation_id=? AND template_key IN (?,?,?,?) ORDER BY id FOR UPDATE`,
		invitationID.String(), string(model.MailTemplateAccessStudentClassInvitation), string(model.MailTemplateAccessTeacherAcademicUnitInvitation),
		string(model.MailTemplateAccessAcademicUnitRoleInvitation), string(model.MailTemplateAccessInstitutionRoleInvitation)); err != nil {
		return fmt.Errorf("lock target credential deliveries: %w", err)
	}
	for index := range rows {
		current, err := rows[index].model()
		if err != nil {
			return err
		}
		switch current.State {
		case model.MailDeliveryAccepted, model.MailDeliverySuppressed, model.MailDeliveryCanceled:
			continue
		case model.MailDeliveryQueued, model.MailDeliverySending, model.MailDeliveryFailed:
		default:
			return invalidPersistedState("mail_delivery", "state", errors.New("target credential delivery has an unknown lifecycle"))
		}
		job, err := getJob(ctx, tx, current.JobID, true)
		if err != nil {
			return err
		}
		if job.Type != model.JobTypeMailDeliverCredential || job.DedupeKey != current.ID.String() {
			return invalidPersistedState("mail_delivery", "job", errors.New("target credential delivery Job relationship is invalid"))
		}
		transitionAt := model.TimeUTC(at)
		if transitionAt.Before(current.UpdatedAt) {
			transitionAt = current.UpdatedAt
		}
		if transitionAt.Before(job.UpdatedAt) {
			transitionAt = job.UpdatedAt
		}
		updated, err := current.Suppress(publicCode, transitionAt)
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

func activeRecoveryTokenMail(ctx context.Context, executor sqlxExecutor, delivery *model.MailDelivery, purpose model.UserTokenPurpose, at time.Time) (bool, error) {
	var tokenID string
	if err := executor.Get(ctx, &tokenID, `
		SELECT t.id FROM user_tokens t
		JOIN users u ON u.id=t.user_id
		WHERE t.id=? AND t.user_id=? AND t.purpose=? AND t.archived_at IS NULL AND t.consumed_at IS NULL
		  AND t.expires_at>? AND u.archived_at IS NULL AND u.disabled_at IS NULL AND u.email=t.target
		FOR SHARE OF t,u`, delivery.OccurrenceID.String(), delivery.TargetUserID.String(), purpose, model.TimeUTC(at)); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("check recovery mail relevance: %w", err)
	}
	return tokenID != "", nil
}

func lockActiveInvitationMail(ctx context.Context, executor sqlxExecutor, invitationID string) (bool, error) {
	var invitation struct {
		Pending        bool           `db:"pending"`
		Purpose        string         `db:"purpose"`
		AcademicUnitID sql.NullString `db:"academic_unit_id"`
		RoleID         sql.NullString `db:"role_id"`
		RoleActions    pq.StringArray `db:"role_actions"`
	}
	if err := executor.Get(ctx, &invitation, `SELECT state='pending' pending,purpose,academic_unit_id,role_id,role_actions
		FROM invitations WHERE id=? FOR SHARE`, invitationID); err != nil {
		return false, translateError("invitation", invitationID, err)
	}
	expectedPurpose := model.InvitationPurpose(invitation.Purpose)
	if expectedPurpose == model.InvitationPurposeStudentClass {
		return invitation.Pending, nil
	}
	if (expectedPurpose != model.InvitationPurposeTeacherAcademicUnit && expectedPurpose != model.InvitationPurposeAcademicUnitRole && expectedPurpose != model.InvitationPurposeInstitutionRole) ||
		!invitation.RoleID.Valid ||
		(expectedPurpose != model.InvitationPurposeInstitutionRole && !invitation.AcademicUnitID.Valid) {
		return false, invalidPersistedState("invitation", "role_package", errors.New("Role Invitation package is incomplete"))
	}
	// Invitation acceptance takes these locks in the same order. Academic Unit
	// hierarchy changes share the advisory lock, while Role updates/archival
	// take an exclusive row lock. Holding the exact package lineage through the
	// delivery transition prevents a credential from starting against a stale
	// Unit or permission snapshot.
	packageActive := true
	if expectedPurpose == model.InvitationPurposeInstitutionRole {
		if err := executor.Get(ctx, &packageActive, `SELECT EXISTS(
			SELECT 1 FROM institutions WHERE id=(SELECT scope_id FROM invitations WHERE id=?) AND archived_at IS NULL FOR SHARE)`, invitationID); err != nil {
			return false, fmt.Errorf("lock institution Role Invitation mail lineage: %w", err)
		}
	} else {
		if err := lockAcademicUnitHierarchy(ctx, executor); err != nil {
			return false, err
		}
		if err := executor.Get(ctx, &packageActive, `SELECT EXISTS(
			SELECT 1 FROM academic_units WHERE id=? AND archived_at IS NULL FOR SHARE)`, invitation.AcademicUnitID.String); err != nil {
			return false, fmt.Errorf("lock Academic Unit Role Invitation mail lineage: %w", err)
		}
	}
	var currentPermissions pq.StringArray
	if err := executor.Get(ctx, &currentPermissions, `SELECT permissions FROM roles
		WHERE id=? AND archived_at IS NULL AND built_in=FALSE FOR SHARE`, invitation.RoleID.String); err != nil {
		if isNoRows(err) {
			return false, nil
		}
		return false, fmt.Errorf("lock teacher Invitation Role mail lineage: %w", err)
	}
	current := append([]string(nil), currentPermissions...)
	slices.Sort(current)
	return invitation.Pending && packageActive && slices.Equal(current, []string(invitation.RoleActions)), nil
}

func activeInvitationMail(ctx context.Context, executor sqlxExecutor, delivery *model.MailDelivery, at time.Time) (bool, error) {
	if delivery == nil || !delivery.TargetInvitationID.IsValid() || !isInvitationCredentialTemplate(delivery.TemplateKey) {
		return false, invalidPersistedState("mail_delivery", "invitation_target", errors.New("invitation credential delivery target is invalid"))
	}
	var active bool
	if delivery.TemplateKey == model.MailTemplateAccessStudentClassInvitation {
		if err := executor.Get(ctx, &active, `SELECT EXISTS(
		SELECT 1 FROM invitations i
		JOIN classes c ON c.id=i.class_id AND c.archived_at IS NULL
		JOIN academic_periods ap ON ap.id=i.academic_period_id AND ap.archived_at IS NULL
		WHERE i.id=? AND i.state='pending' AND i.expires_at>? AND (i.intended_end_at IS NULL OR i.intended_end_at>?)
		  AND ap.end_at>? AND c.academic_period_id=i.academic_period_id
	)`, delivery.TargetInvitationID.String(), model.TimeUTC(at), model.TimeUTC(at), model.TimeUTC(at)); err != nil {
			return false, fmt.Errorf("check student Invitation mail relevance: %w", err)
		}
		return active, nil
	}
	if delivery.TemplateKey == model.MailTemplateAccessInstitutionRoleInvitation {
		if err := executor.Get(ctx, &active, `SELECT EXISTS(
			SELECT 1 FROM invitations i JOIN institutions institution ON institution.id=i.scope_id AND institution.archived_at IS NULL
			JOIN roles r ON r.id=i.role_id AND r.archived_at IS NULL AND r.built_in=FALSE
			WHERE i.id=? AND i.purpose='institution_role' AND i.state='pending' AND i.expires_at>?
			AND (i.intended_end_at IS NULL OR i.intended_end_at>?)
			AND (SELECT array_agg(value ORDER BY value) FROM unnest(r.permissions) value)=i.role_actions
		)`, delivery.TargetInvitationID.String(), model.TimeUTC(at), model.TimeUTC(at)); err != nil {
			return false, fmt.Errorf("check institution Role Invitation mail relevance: %w", err)
		}
		return active, nil
	}
	purpose := model.InvitationPurposeTeacherAcademicUnit
	if delivery.TemplateKey == model.MailTemplateAccessAcademicUnitRoleInvitation {
		purpose = model.InvitationPurposeAcademicUnitRole
	}
	if err := executor.Get(ctx, &active, `SELECT EXISTS(
		SELECT 1 FROM invitations i
		JOIN academic_units au ON au.id=i.academic_unit_id AND au.archived_at IS NULL
		JOIN roles r ON r.id=i.role_id AND r.archived_at IS NULL AND r.built_in=FALSE
		WHERE i.id=? AND i.purpose=? AND i.state='pending' AND i.expires_at>?
		AND (i.intended_end_at IS NULL OR i.intended_end_at>?)
		AND (SELECT array_agg(value ORDER BY value) FROM unnest(r.permissions) value)=i.role_actions
	)`, delivery.TargetInvitationID.String(), purpose, model.TimeUTC(at), model.TimeUTC(at)); err != nil {
		return false, fmt.Errorf("check teacher Invitation mail relevance: %w", err)
	}
	return active, nil
}

func isInvitationCredentialTemplate(key model.MailTemplateKey) bool {
	return key == model.MailTemplateAccessStudentClassInvitation || key == model.MailTemplateAccessTeacherAcademicUnitInvitation ||
		key == model.MailTemplateAccessAcademicUnitRoleInvitation || key == model.MailTemplateAccessInstitutionRoleInvitation
}

func (s SQLMailStore) CompleteDelivery(ctx context.Context, input *store.MailDeliveryCompletion) (*model.MailDelivery, error) {
	if input == nil || !input.DeliveryID.IsValid() || input.ExpectedRevision <= 0 || input.At.IsZero() {
		return nil, store.NewErrInvalidInput("mail_delivery", "complete", nil)
	}
	return s.mutateDelivery(ctx, input.DeliveryID, input.ExpectedRevision, func(current *model.MailDelivery) (*model.MailDelivery, error) {
		switch input.Kind {
		case store.MailDeliveryCompletionAccepted:
			if input.PublicFailureCode != "" {
				return nil, errors.New("accepted mail has a failure code")
			}
			return current.Accept(input.At)
		case store.MailDeliveryCompletionRetry:
			return current.Retry(input.PublicFailureCode, input.At)
		case store.MailDeliveryCompletionFailed:
			return current.Fail(input.PublicFailureCode, input.At)
		case store.MailDeliveryCompletionExpired:
			if input.PublicFailureCode != "" {
				return nil, errors.New("expired mail completion selects its fixed failure code")
			}
			return current.Expire(input.At)
		case store.MailDeliveryCompletionSuppress:
			return current.Suppress(input.PublicFailureCode, input.At)
		default:
			return nil, errors.New("unknown mail completion")
		}
	})
}

func (s SQLMailStore) CancelDelivery(ctx context.Context, input *store.MailDeliveryMutation) (*model.MailDelivery, error) {
	return s.mutateOperatorDelivery(ctx, input, "cancel", func(delivery *model.MailDelivery, job *model.Job, at time.Time) (*model.MailDelivery, *model.Job, error) {
		if job.Status != model.JobStatusQueued {
			return nil, nil, errors.New("mail delivery job is not queued")
		}
		updatedDelivery, err := delivery.Cancel(at)
		if err != nil {
			return nil, nil, err
		}
		updatedJob, err := job.RequestCancellation(at)
		return updatedDelivery, updatedJob, err
	})
}

func (s SQLMailStore) RetryDelivery(ctx context.Context, input *store.MailDeliveryMutation) (*model.MailDelivery, error) {
	return s.mutateOperatorDelivery(ctx, input, "retry", func(delivery *model.MailDelivery, job *model.Job, at time.Time) (*model.MailDelivery, *model.Job, error) {
		if job.Status != model.JobStatusFailed {
			return nil, nil, errors.New("mail delivery job is not failed")
		}
		updatedDelivery, err := delivery.OperatorRetry(at)
		if err != nil {
			return nil, nil, err
		}
		updatedJob, err := job.ExplicitRetry(at)
		return updatedDelivery, updatedJob, err
	})
}

func (s SQLMailStore) mutateOperatorDelivery(ctx context.Context, input *store.MailDeliveryMutation, operation string, transition func(*model.MailDelivery, *model.Job, time.Time) (*model.MailDelivery, *model.Job, error)) (*model.MailDelivery, error) {
	if input == nil || !input.ID.IsValid() || input.ExpectedRevision <= 0 || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("mail_delivery", operation, nil)
	}
	return executeSQLTransaction(ctx, s.GetMaster().Begin, rawSQLTransactionPolicy[*model.MailDelivery](true, func(_ *model.MailDelivery, err error) error { return err }), func(ctx context.Context, tx *sqlxTxWrapper) (*model.MailDelivery, error) {
		var route struct {
			TargetUserID       sql.NullString `db:"target_user_id"`
			TargetInvitationID sql.NullString `db:"target_invitation_id"`
			TemplateKey        string         `db:"template_key"`
		}
		if err := tx.Get(ctx, &route, `SELECT target_user_id,target_invitation_id,template_key FROM mail_deliveries WHERE id=?`, input.ID.String()); err != nil {
			return nil, translateError("mail_delivery", input.ID.String(), err)
		}
		templateKey := model.MailTemplateKey(route.TemplateKey)
		purpose, credential := recoveryTokenPurpose(templateKey)
		if credential {
			if !route.TargetUserID.Valid {
				return nil, invalidPersistedState("mail_delivery", "target_user_id", errors.New("credential delivery has no user target"))
			}
			if err := lockUserTokenPurpose(ctx, tx, route.TargetUserID.String, purpose); err != nil {
				return nil, err
			}
		}
		invitationCredential := isInvitationCredentialTemplate(templateKey)
		if invitationCredential {
			if !route.TargetInvitationID.Valid {
				return nil, invalidPersistedState("mail_delivery", "target_invitation_id", errors.New("invitation credential delivery has no Invitation target"))
			}
			if _, err := lockActiveInvitationMail(ctx, tx, route.TargetInvitationID.String); err != nil {
				return nil, err
			}
		}
		var sittingFence *sittingMailDeliveryFence
		if operation == "retry" && isSittingScheduleMailTemplate(templateKey) {
			fence, fenceErr := lockSittingMailDeliveryFence(ctx, tx, input.ID)
			if fenceErr != nil {
				return nil, fenceErr
			}
			sittingFence = fence
		}
		databaseNow, err := jobDatabaseNow(ctx, tx)
		if err != nil {
			return nil, err
		}
		var row mailDeliveryRow
		if err = tx.Get(ctx, &row, `SELECT `+mailDeliveryColumns+` FROM mail_deliveries WHERE id = ? FOR UPDATE`, input.ID.String()); err != nil {
			return nil, translateError("mail_delivery", input.ID.String(), err)
		}
		current, err := row.model()
		if err != nil {
			return nil, err
		}
		if current.Revision != input.ExpectedRevision {
			return nil, store.NewErrConflict("mail_delivery", "stale_revision", nil)
		}
		if operation == "retry" && credential {
			relevant, relevanceErr := activeRecoveryTokenMail(ctx, tx, current, purpose, databaseNow)
			if relevanceErr != nil {
				return nil, relevanceErr
			}
			if !relevant {
				return nil, store.NewErrConflict("mail_delivery", "obsolete", nil)
			}
		}
		if operation == "retry" && invitationCredential {
			relevant, relevanceErr := activeInvitationMail(ctx, tx, current, databaseNow)
			if relevanceErr != nil {
				return nil, relevanceErr
			}
			if !relevant {
				return nil, store.NewErrConflict("mail_delivery", "obsolete", nil)
			}
		}
		if sittingFence != nil {
			relevant, relevanceErr := sittingFence.relevant(ctx, tx, current.ID)
			if relevanceErr != nil {
				return nil, relevanceErr
			}
			if !relevant {
				return nil, store.NewErrConflict("mail_delivery", "obsolete", nil)
			}
		}
		if err = validateMailMutationAudit(ctx, tx, input.AuditEventID, current.ID); err != nil {
			return nil, err
		}
		job, err := getJob(ctx, tx, current.JobID, true)
		if err != nil {
			return nil, err
		}
		if !isMailDeliveryJobType(job.Type) || job.DedupeKey != current.ID.String() {
			return nil, store.NewErrConflict("mail_delivery", "job_mismatch", nil)
		}
		updated, updatedJob, err := transition(current, job, databaseNow)
		if err != nil {
			return nil, store.NewErrConflict("mail_delivery", "invalid_transition", err)
		}
		if err = updateMailDelivery(ctx, tx, current, updated); err != nil {
			return nil, err
		}
		if err = updateJob(ctx, tx, updatedJob); err != nil {
			return nil, err
		}
		result, err := model.EncodeAuditData(updated.Auditable())
		if err != nil {
			return nil, err
		}
		if _, err = completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", result, input.AuditAt); err != nil {
			return nil, fmt.Errorf("complete mail %s audit: %w", operation, err)
		}
		return updated, nil
	})
}

func isMailDeliveryJobType(jobType model.JobType) bool {
	return jobType == model.JobTypeMailDeliver || jobType == model.JobTypeMailDeliverCredential
}

func validateMailMutationAudit(ctx context.Context, tx *sqlxTxWrapper, auditID string, deliveryID model.MailDeliveryID) error {
	var audit struct {
		Action       string             `db:"action"`
		ResourceType model.ResourceType `db:"resource_type"`
		ResourceID   string             `db:"resource_id"`
		Status       model.AuditStatus  `db:"status"`
	}
	if err := tx.Get(ctx, &audit, `SELECT action, resource_type, resource_id, status FROM audit_events WHERE id = ? FOR UPDATE`, auditID); err != nil {
		return translateError("audit_event", auditID, err)
	}
	if audit.Action != string(model.ActionMailManage) || audit.ResourceType != model.ResourceMailDelivery ||
		audit.ResourceID != deliveryID.String() || audit.Status != model.AuditStatusAttempt {
		return store.NewErrConflict("mail_delivery", "audit_mismatch", nil)
	}
	return nil
}

func (s SQLMailStore) mutateDelivery(ctx context.Context, id model.MailDeliveryID, expectedRevision int64, transition func(*model.MailDelivery) (*model.MailDelivery, error)) (*model.MailDelivery, error) {
	return executeSQLTransaction(ctx, s.GetMaster().Begin, rawSQLTransactionPolicy[*model.MailDelivery](true, func(_ *model.MailDelivery, err error) error {
		return fmt.Errorf("commit mail delivery transition: %w", err)
	}), func(ctx context.Context, tx *sqlxTxWrapper) (*model.MailDelivery, error) {
		var route struct {
			TemplateKey string `db:"template_key"`
		}
		if err := tx.Get(ctx, &route, `SELECT template_key FROM mail_deliveries WHERE id=?`, id.String()); err != nil {
			return nil, translateError("mail_delivery", id.String(), err)
		}
		var sittingFence *sittingMailDeliveryFence
		if isSittingScheduleMailTemplate(model.MailTemplateKey(route.TemplateKey)) {
			fence, err := lockSittingMailDeliveryFence(ctx, tx, id)
			if err != nil {
				return nil, err
			}
			sittingFence = fence
		}
		var row mailDeliveryRow
		if err := tx.Get(ctx, &row, `SELECT `+mailDeliveryColumns+` FROM mail_deliveries WHERE id = ? FOR UPDATE`, id.String()); err != nil {
			return nil, translateError("mail_delivery", id.String(), err)
		}
		current, err := row.model()
		if err != nil {
			return nil, err
		}
		if current.Revision != expectedRevision {
			return nil, store.NewErrConflict("mail_delivery", "stale_revision", nil)
		}
		updated, err := transition(current)
		if err != nil {
			return nil, store.NewErrConflict("mail_delivery", "invalid_transition", err)
		}
		if err = updateMailDelivery(ctx, tx, current, updated); err != nil {
			return nil, err
		}
		if sittingFence != nil && updated.State == model.MailDeliveryAccepted {
			if err = markSittingMailCommunicated(ctx, tx, sittingFence, updated.UpdatedAt); err != nil {
				return nil, fmt.Errorf("record communicated Sitting mail: %w", err)
			}
		}
		return updated, nil
	})
}

func updateMailDelivery(ctx context.Context, tx *sqlxTxWrapper, current, updated *model.MailDelivery) error {
	if current == nil || updated == nil || current.ID != updated.ID {
		return invalidPersistedState("mail_delivery", "transition", errors.New("mail delivery transition snapshots are invalid"))
	}
	currentPayloadKeyID, err := mailPayloadKeyID(current.EncryptedPayload)
	if err != nil {
		return invalidPersistedState("mail_delivery", "encrypted_payload", err)
	}
	payloadKeyID, err := mailPayloadKeyID(updated.EncryptedPayload)
	if err != nil {
		return invalidPersistedState("mail_delivery", "encrypted_payload", err)
	}
	result, err := tx.Exec(ctx, `UPDATE mail_deliveries SET state = ?, updated_at = ?, attempt_count = ?, accepted_at = ?, failed_at = ?, public_failure_code = ?, payload_key_id = ?, encrypted_payload = ?, revision = ? WHERE id = ? AND revision = ?`, string(updated.State), updated.UpdatedAt, updated.AttemptCount, optionalTimeValue(updated.AcceptedAt), optionalTimeValue(updated.FailedAt), updated.PublicFailureCode, nullableMailPayloadKeyID(payloadKeyID), mailNullableJSON(updated.EncryptedPayload), updated.Revision, updated.ID.String(), updated.Revision-1)
	if err != nil {
		return fmt.Errorf("update mail delivery: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return store.NewErrConflict("mail_delivery", "stale_revision", nil)
	}
	if currentPayloadKeyID != payloadKeyID {
		if currentPayloadKeyID == "" || payloadKeyID != "" {
			return invalidPersistedState("mail_delivery", "payload_key_id", errors.New("mail delivery transition changed an active payload key"))
		}
		if err := decrementMailPayloadKeyReference(ctx, tx, currentPayloadKeyID); err != nil {
			return err
		}
	}
	return nil
}

func incrementMailPayloadKeyReference(ctx context.Context, tx *sqlxTxWrapper, keyID string) error {
	if keyID == "" {
		return invalidPersistedState("mail_delivery", "payload_key_id", errors.New("active payload key id is missing"))
	}
	if _, err := tx.Exec(ctx, `INSERT INTO mail_payload_keys(key_id,active_references) VALUES(?,1)
		ON CONFLICT(key_id) DO UPDATE SET active_references=mail_payload_keys.active_references+1`, keyID); err != nil {
		return fmt.Errorf("increment mail payload key reference: %w", err)
	}
	return nil
}

func decrementMailPayloadKeyReference(ctx context.Context, tx *sqlxTxWrapper, keyID string) error {
	var remaining int64
	if err := tx.Get(ctx, &remaining, `UPDATE mail_payload_keys SET active_references=active_references-1
		WHERE key_id=? AND active_references>0 RETURNING active_references`, keyID); err != nil {
		return invalidPersistedState("mail_delivery", "payload_key_id", fmt.Errorf("decrement mail payload key reference: %w", err))
	}
	if remaining == 0 {
		result, err := tx.Exec(ctx, `DELETE FROM mail_payload_keys WHERE key_id=? AND active_references=0`, keyID)
		if err != nil {
			return fmt.Errorf("delete unused mail payload key reference: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil || affected != 1 {
			return invalidPersistedState("mail_delivery", "payload_key_id", errors.New("unused payload key reference was not deleted"))
		}
	}
	return nil
}

func (row mailDeliveryRow) model() (*model.MailDelivery, error) {
	delivery := &model.MailDelivery{ID: model.MailDeliveryID(row.ID), OccurrenceID: model.MailOccurrenceID(row.OccurrenceID), JobID: model.JobID(row.JobID), TargetUserID: model.UserID(row.TargetUserID.String), TargetInvitationID: model.InvitationID(row.TargetInvitationID.String), TemplateKey: model.MailTemplateKey(row.TemplateKey), TemplateDigest: row.TemplateDigest, MaskedRecipient: row.MaskedRecipient, State: model.MailDeliveryState(row.State), CreatedAt: model.TimeUTC(row.CreatedAt), UpdatedAt: model.TimeUTC(row.UpdatedAt), MessageDate: model.TimeUTC(row.MessageDate), Deadline: model.TimeUTC(row.Deadline), MessageID: row.MessageID, AttemptCount: row.AttemptCount, AcceptedAt: optionalTime(row.AcceptedAt), FailedAt: optionalTime(row.FailedAt), PublicFailureCode: row.PublicFailureCode, EncryptedPayload: append(json.RawMessage(nil), row.EncryptedPayload...), Revision: row.Revision}
	if err := delivery.Validate(); err != nil {
		return nil, invalidPersistedState("mail_delivery", "value", err)
	}
	payloadKeyID, err := mailPayloadKeyID(delivery.EncryptedPayload)
	if err != nil || row.PayloadKeyID.Valid != (payloadKeyID != "") || (row.PayloadKeyID.Valid && row.PayloadKeyID.String != payloadKeyID) {
		return nil, invalidPersistedState("mail_delivery", "payload_key_id", errors.New("payload key reference does not match encrypted payload"))
	}
	return delivery, nil
}

func mailPayloadKeyID(payload json.RawMessage) (string, error) {
	if len(payload) == 0 {
		return "", nil
	}
	var reference struct {
		KeyID string `json:"key_id"`
	}
	if err := json.Unmarshal(payload, &reference); err != nil {
		return "", errors.New("encrypted payload envelope is invalid")
	}
	decoded, err := hex.DecodeString(reference.KeyID)
	if err != nil || len(decoded) != 16 || len(reference.KeyID) != 32 || strings.ToLower(reference.KeyID) != reference.KeyID {
		return "", errors.New("encrypted payload key id is invalid")
	}
	return reference.KeyID, nil
}

func nullableMailPayloadKeyID(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func optionalTimeValue(value model.OptionalTime) any {
	if !value.Valid {
		return nil
	}
	return value.Time
}
func mailNullableJSON(value json.RawMessage) any {
	if len(value) == 0 {
		return nil
	}
	return value
}
