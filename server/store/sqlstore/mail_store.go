// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SQLMailStore struct{ *SQLStore }

type mailDeliveryRow struct {
	ID                string         `db:"id"`
	OccurrenceID      string         `db:"occurrence_id"`
	JobID             string         `db:"job_id"`
	TargetUserID      string         `db:"target_user_id"`
	TemplateKey       string         `db:"template_key"`
	TemplateDigest    string         `db:"template_digest"`
	MaskedRecipient   string         `db:"masked_recipient"`
	State             string         `db:"state"`
	CreatedAt         time.Time      `db:"created_at"`
	UpdatedAt         time.Time      `db:"updated_at"`
	MessageDate       time.Time      `db:"message_date"`
	Deadline          time.Time      `db:"deadline"`
	MessageID         string         `db:"message_id"`
	AttemptCount      int            `db:"attempt_count"`
	AcceptedAt        sql.NullTime   `db:"accepted_at"`
	FailedAt          sql.NullTime   `db:"failed_at"`
	PublicFailureCode string         `db:"public_failure_code"`
	PayloadKeyID      sql.NullString `db:"payload_key_id"`
	EncryptedPayload  jsonValue      `db:"encrypted_payload"`
	Revision          int64          `db:"revision"`
}

const mailDeliveryColumns = `id, occurrence_id, job_id, target_user_id, template_key, template_digest, masked_recipient, state, created_at, updated_at, message_date, deadline, message_id, attempt_count, accepted_at, failed_at, public_failure_code, payload_key_id, encrypted_payload, revision`

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
		if _, err := tx.Exec(ctx, `INSERT INTO mail_occurrences (id, kind, template_key, actor_user_id, created_at) VALUES (?, ?, ?, ?, ?)`, input.Occurrence.ID.String(), string(input.Occurrence.Kind), string(input.Occurrence.TemplateKey), input.Occurrence.ActorUserID.String(), input.Occurrence.CreatedAt); err != nil {
			return nil, fmt.Errorf("insert mail occurrence: %w", translateError("mail_occurrence", input.Occurrence.ID.String(), err))
		}
		if _, err := insertQueuedJob(ctx, tx, input.Job, false); err != nil {
			return nil, fmt.Errorf("insert mail delivery job: %w", translateError("job", input.Job.ID.String(), err))
		}
		if _, err := tx.Exec(ctx, `INSERT INTO mail_deliveries (`+mailDeliveryColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, NULL, NULL, '', ?, ?, 1)`, input.Delivery.ID.String(), input.Delivery.OccurrenceID.String(), input.Delivery.JobID.String(), input.Delivery.TargetUserID.String(), string(input.Delivery.TemplateKey), input.Delivery.TemplateDigest, input.Delivery.MaskedRecipient, string(input.Delivery.State), input.Delivery.CreatedAt, input.Delivery.UpdatedAt, input.Delivery.MessageDate, input.Delivery.Deadline, input.Delivery.MessageID, payloadKeyID, input.Delivery.EncryptedPayload); err != nil {
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
	return s.mutateDelivery(ctx, id, expectedRevision, func(current *model.MailDelivery) (*model.MailDelivery, error) { return current.Start(at) })
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
		if err = validateMailMutationAudit(ctx, tx, input.AuditEventID, current.ID); err != nil {
			return nil, err
		}
		job, err := getJob(ctx, tx, current.JobID, true)
		if err != nil {
			return nil, err
		}
		if job.Type != model.JobTypeMailDeliver || job.DedupeKey != current.ID.String() {
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
	delivery := &model.MailDelivery{ID: model.MailDeliveryID(row.ID), OccurrenceID: model.MailOccurrenceID(row.OccurrenceID), JobID: model.JobID(row.JobID), TargetUserID: model.UserID(row.TargetUserID), TemplateKey: model.MailTemplateKey(row.TemplateKey), TemplateDigest: row.TemplateDigest, MaskedRecipient: row.MaskedRecipient, State: model.MailDeliveryState(row.State), CreatedAt: model.TimeUTC(row.CreatedAt), UpdatedAt: model.TimeUTC(row.UpdatedAt), MessageDate: model.TimeUTC(row.MessageDate), Deadline: model.TimeUTC(row.Deadline), MessageID: row.MessageID, AttemptCount: row.AttemptCount, AcceptedAt: optionalTime(row.AcceptedAt), FailedAt: optionalTime(row.FailedAt), PublicFailureCode: row.PublicFailureCode, EncryptedPayload: append(json.RawMessage(nil), row.EncryptedPayload...), Revision: row.Revision}
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
