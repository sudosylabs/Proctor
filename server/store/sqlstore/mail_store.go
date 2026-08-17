// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SQLMailStore struct{ *SQLStore }

type mailDeliveryRow struct {
	ID                string       `db:"id"`
	OccurrenceID      string       `db:"occurrence_id"`
	JobID             string       `db:"job_id"`
	TargetUserID      string       `db:"target_user_id"`
	TemplateKey       string       `db:"template_key"`
	TemplateDigest    string       `db:"template_digest"`
	MaskedRecipient   string       `db:"masked_recipient"`
	State             string       `db:"state"`
	CreatedAt         time.Time    `db:"created_at"`
	UpdatedAt         time.Time    `db:"updated_at"`
	MessageDate       time.Time    `db:"message_date"`
	Deadline          time.Time    `db:"deadline"`
	MessageID         string       `db:"message_id"`
	AttemptCount      int          `db:"attempt_count"`
	AcceptedAt        sql.NullTime `db:"accepted_at"`
	FailedAt          sql.NullTime `db:"failed_at"`
	PublicFailureCode string       `db:"public_failure_code"`
	EncryptedPayload  jsonValue    `db:"encrypted_payload"`
	Revision          int64        `db:"revision"`
}

const mailDeliveryColumns = `id, occurrence_id, job_id, target_user_id, template_key, template_digest, masked_recipient, state, created_at, updated_at, message_date, deadline, message_id, attempt_count, accepted_at, failed_at, public_failure_code, encrypted_payload, revision`

func newSQLMailStore(sqlStore *SQLStore) store.MailStore { return &SQLMailStore{SQLStore: sqlStore} }

func (s SQLMailStore) EnqueueTest(ctx context.Context, input *store.MailTestEnqueue) (*model.MailDelivery, error) {
	if err := validateMailTestEnqueue(input); err != nil {
		return nil, err
	}
	return executeSQLTransaction(ctx, s.GetMaster().Begin, rawSQLTransactionPolicy[*model.MailDelivery](true, func(_ *model.MailDelivery, err error) error { return fmt.Errorf("commit test mail enqueue: %w", err) }), func(ctx context.Context, tx *sqlxTxWrapper) (*model.MailDelivery, error) {
		if _, err := tx.Exec(ctx, `INSERT INTO mail_occurrences (id, kind, template_key, actor_user_id, created_at) VALUES (?, ?, ?, ?, ?)`, input.Occurrence.ID.String(), string(input.Occurrence.Kind), string(input.Occurrence.TemplateKey), input.Occurrence.ActorUserID.String(), input.Occurrence.CreatedAt); err != nil {
			return nil, fmt.Errorf("insert mail occurrence: %w", translateError("mail_occurrence", input.Occurrence.ID.String(), err))
		}
		if _, err := insertQueuedJob(ctx, tx, input.Job, false); err != nil {
			return nil, fmt.Errorf("insert mail delivery job: %w", translateError("job", input.Job.ID.String(), err))
		}
		if _, err := tx.Exec(ctx, `INSERT INTO mail_deliveries (`+mailDeliveryColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, NULL, NULL, '', ?, 1)`, input.Delivery.ID.String(), input.Delivery.OccurrenceID.String(), input.Delivery.JobID.String(), input.Delivery.TargetUserID.String(), string(input.Delivery.TemplateKey), input.Delivery.TemplateDigest, input.Delivery.MaskedRecipient, string(input.Delivery.State), input.Delivery.CreatedAt, input.Delivery.UpdatedAt, input.Delivery.MessageDate, input.Delivery.Deadline, input.Delivery.MessageID, input.Delivery.EncryptedPayload); err != nil {
			return nil, fmt.Errorf("insert mail delivery: %w", translateError("mail_delivery", input.Delivery.ID.String(), err))
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
		default:
			return nil, errors.New("unknown mail completion")
		}
	})
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
		result, err := tx.Exec(ctx, `UPDATE mail_deliveries SET state = ?, updated_at = ?, attempt_count = ?, accepted_at = ?, failed_at = ?, public_failure_code = ?, encrypted_payload = ?, revision = ? WHERE id = ? AND revision = ?`, string(updated.State), updated.UpdatedAt, updated.AttemptCount, optionalTimeValue(updated.AcceptedAt), optionalTimeValue(updated.FailedAt), updated.PublicFailureCode, mailNullableJSON(updated.EncryptedPayload), updated.Revision, id.String(), expectedRevision)
		if err != nil {
			return nil, fmt.Errorf("update mail delivery: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return nil, err
		}
		if affected != 1 {
			return nil, store.NewErrConflict("mail_delivery", "stale_revision", nil)
		}
		return updated, nil
	})
}

func (row mailDeliveryRow) model() (*model.MailDelivery, error) {
	delivery := &model.MailDelivery{ID: model.MailDeliveryID(row.ID), OccurrenceID: model.MailOccurrenceID(row.OccurrenceID), JobID: model.JobID(row.JobID), TargetUserID: model.UserID(row.TargetUserID), TemplateKey: model.MailTemplateKey(row.TemplateKey), TemplateDigest: row.TemplateDigest, MaskedRecipient: row.MaskedRecipient, State: model.MailDeliveryState(row.State), CreatedAt: model.TimeUTC(row.CreatedAt), UpdatedAt: model.TimeUTC(row.UpdatedAt), MessageDate: model.TimeUTC(row.MessageDate), Deadline: model.TimeUTC(row.Deadline), MessageID: row.MessageID, AttemptCount: row.AttemptCount, AcceptedAt: optionalTime(row.AcceptedAt), FailedAt: optionalTime(row.FailedAt), PublicFailureCode: row.PublicFailureCode, EncryptedPayload: append(json.RawMessage(nil), row.EncryptedPayload...), Revision: row.Revision}
	if err := delivery.Validate(); err != nil {
		return nil, invalidPersistedState("mail_delivery", "value", err)
	}
	return delivery, nil
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
