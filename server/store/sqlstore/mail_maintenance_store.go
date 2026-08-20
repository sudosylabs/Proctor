// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const (
	mailMaintenanceMaximumBatch = 500
	mailSendTokensPerSecond     = 10.0
	mailSendBurstTokens         = 20.0
	mailCredentialReserveTokens = 4.0
)

func (s SQLMailStore) AcquireSendPermit(ctx context.Context, class store.MailSendClass) (*store.MailSendPermit, error) {
	if class != store.MailSendCredential && class != store.MailSendOrdinary {
		return nil, store.NewErrInvalidInput("mail_send_rate_limit", "class", nil)
	}
	return executeSQLTransaction(ctx, s.GetMaster().Begin, rawSQLTransactionPolicy[*store.MailSendPermit](true, func(_ *store.MailSendPermit, err error) error {
		return fmt.Errorf("commit mail send permit: %w", err)
	}), func(ctx context.Context, tx *sqlxTxWrapper) (*store.MailSendPermit, error) {
		now, err := jobDatabaseNow(ctx, tx)
		if err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO mail_send_rate_limit (singleton,tokens,updated_at) VALUES (TRUE,?,?) ON CONFLICT (singleton) DO NOTHING`, mailSendBurstTokens, now); err != nil {
			return nil, fmt.Errorf("initialize mail send limiter: %w", err)
		}
		var row struct {
			Tokens    float64   `db:"tokens"`
			UpdatedAt time.Time `db:"updated_at"`
		}
		if err = tx.Get(ctx, &row, `SELECT tokens,updated_at FROM mail_send_rate_limit WHERE singleton=TRUE FOR UPDATE`); err != nil {
			return nil, fmt.Errorf("lock mail send limiter: %w", err)
		}
		elapsed := now.Sub(row.UpdatedAt).Seconds()
		if elapsed < 0 {
			elapsed = 0
		}
		tokens := math.Min(mailSendBurstTokens, row.Tokens+elapsed*mailSendTokensPerSecond)
		minimum := 1.0
		if class == store.MailSendOrdinary {
			minimum += mailCredentialReserveTokens
		}
		permit := &store.MailSendPermit{Allowed: tokens >= minimum}
		if permit.Allowed {
			tokens--
		} else {
			permit.RetryAfter = time.Duration(math.Ceil((minimum - tokens) / mailSendTokensPerSecond * float64(time.Second)))
			if permit.RetryAfter < time.Millisecond {
				permit.RetryAfter = time.Millisecond
			}
		}
		if _, err = tx.Exec(ctx, `UPDATE mail_send_rate_limit SET tokens=?,updated_at=? WHERE singleton=TRUE`, tokens, now); err != nil {
			return nil, fmt.Errorf("update mail send limiter: %w", err)
		}
		return permit, nil
	})
}

func (s SQLMailStore) SuppressOutstanding(ctx context.Context, code string, limit int) (*store.MailMaintenanceResult, error) {
	if code != model.MailDeliveryDisabledCode || !validMailMaintenanceLimit(limit) {
		return nil, store.NewErrInvalidInput("mail_delivery", "suppress_outstanding", nil)
	}
	return s.suppressDeliveryPage(ctx, code, limit,
		`state IN ('queued','sending','failed')`, "suppress outstanding mail")
}

func (s SQLMailStore) SuppressExpired(ctx context.Context, limit int) (*store.MailMaintenanceResult, error) {
	if !validMailMaintenanceLimit(limit) {
		return nil, store.NewErrInvalidInput("mail_delivery", "suppress_expired", nil)
	}
	return s.suppressDeliveryPage(ctx, model.MailDeliveryExpiredCode, limit,
		`state IN ('queued','sending','failed') AND deadline<=statement_timestamp()`, "suppress expired mail")
}

func (s SQLMailStore) suppressDeliveryPage(ctx context.Context, code string, limit int, predicate, operation string) (*store.MailMaintenanceResult, error) {
	return executeSQLTransaction(ctx, s.GetMaster().Begin, rawSQLTransactionPolicy[*store.MailMaintenanceResult](true, func(_ *store.MailMaintenanceResult, err error) error {
		return fmt.Errorf("commit %s: %w", operation, err)
	}), func(ctx context.Context, tx *sqlxTxWrapper) (*store.MailMaintenanceResult, error) {
		now, err := jobDatabaseNow(ctx, tx)
		if err != nil {
			return nil, err
		}
		rows := make([]mailDeliveryRow, 0, limit+1)
		query := `SELECT ` + mailDeliveryColumns + ` FROM mail_deliveries WHERE ` + predicate + ` ORDER BY deadline,id LIMIT ? FOR UPDATE SKIP LOCKED`
		if err = tx.Select(ctx, &rows, query, limit+1); err != nil {
			return nil, fmt.Errorf("select mail suppression page: %w", err)
		}
		result := &store.MailMaintenanceResult{More: len(rows) > limit}
		if len(rows) > limit {
			rows = rows[:limit]
		}
		for _, row := range rows {
			current, modelErr := row.model()
			if modelErr != nil {
				return nil, modelErr
			}
			job, modelErr := getJob(ctx, tx, current.JobID, true)
			if modelErr != nil {
				return nil, modelErr
			}
			if !isMailDeliveryJobType(job.Type) || job.DedupeKey != current.ID.String() {
				return nil, invalidPersistedState("mail_delivery", "job", fmt.Errorf("mail delivery job relationship is invalid"))
			}
			transitionAt := model.TimeUTC(now)
			if transitionAt.Before(current.UpdatedAt) {
				transitionAt = current.UpdatedAt
			}
			if transitionAt.Before(job.UpdatedAt) {
				transitionAt = job.UpdatedAt
			}
			updated, modelErr := current.Suppress(code, transitionAt)
			if modelErr != nil {
				return nil, invalidPersistedState("mail_delivery", "suppression", modelErr)
			}
			if _, err = tx.Exec(ctx, `UPDATE mail_deliveries SET state=?,updated_at=?,accepted_at=NULL,failed_at=NULL,public_failure_code=?,payload_key_id=NULL,encrypted_payload=NULL,revision=? WHERE id=? AND revision=?`,
				string(updated.State), updated.UpdatedAt, updated.PublicFailureCode, updated.Revision, updated.ID.String(), current.Revision); err != nil {
				return nil, fmt.Errorf("suppress mail delivery: %w", err)
			}
			payloadKeyID, keyErr := mailPayloadKeyID(current.EncryptedPayload)
			if keyErr != nil {
				return nil, invalidPersistedState("mail_delivery", "payload_key_id", keyErr)
			}
			if err = decrementMailPayloadKeyReference(ctx, tx, payloadKeyID); err != nil {
				return nil, err
			}
			if job.Status == model.JobStatusQueued || job.Status == model.JobStatusRunning {
				canceled, cancelErr := job.RequestCancellation(transitionAt)
				if cancelErr != nil {
					return nil, invalidPersistedState("mail_delivery", "job cancellation", cancelErr)
				}
				if err = updateJob(ctx, tx, canceled); err != nil {
					return nil, fmt.Errorf("cancel suppressed mail job: %w", err)
				}
			}
			result.Affected++
			result.Deliveries = append(result.Deliveries, store.MailMaintenanceDelivery{
				TemplateKey: updated.TemplateKey, State: updated.State, PublicFailureCode: updated.PublicFailureCode,
				AttemptCount: updated.AttemptCount, ProcessingLatency: updated.UpdatedAt.Sub(updated.CreatedAt),
			})
		}
		return result, nil
	})
}

func (s SQLMailStore) CleanupTerminal(ctx context.Context, limit int) (*store.MailMaintenanceResult, error) {
	if !validMailMaintenanceLimit(limit) {
		return nil, store.NewErrInvalidInput("mail_delivery", "cleanup_terminal", nil)
	}
	return executeSQLTransaction(ctx, s.GetMaster().Begin, rawSQLTransactionPolicy[*store.MailMaintenanceResult](true, func(_ *store.MailMaintenanceResult, err error) error {
		return fmt.Errorf("commit terminal mail cleanup: %w", err)
	}), func(ctx context.Context, tx *sqlxTxWrapper) (*store.MailMaintenanceResult, error) {
		var rows []struct {
			ID           string         `db:"id"`
			OccurrenceID string         `db:"occurrence_id"`
			PayloadKeyID sql.NullString `db:"payload_key_id"`
		}
		if err := tx.Select(ctx, &rows, `SELECT id,occurrence_id,payload_key_id FROM mail_deliveries
			WHERE (state IN ('accepted','suppressed','canceled') AND updated_at<=statement_timestamp()-INTERVAL '90 days')
			   OR (state='failed' AND updated_at<=statement_timestamp()-INTERVAL '180 days')
			ORDER BY updated_at,id LIMIT ? FOR UPDATE SKIP LOCKED`, limit+1); err != nil {
			return nil, fmt.Errorf("select terminal mail cleanup page: %w", err)
		}
		result := &store.MailMaintenanceResult{More: len(rows) > limit}
		if len(rows) > limit {
			rows = rows[:limit]
		}
		for _, row := range rows {
			if row.PayloadKeyID.Valid {
				if err := decrementMailPayloadKeyReference(ctx, tx, row.PayloadKeyID.String); err != nil {
					return nil, err
				}
			}
			if _, err := tx.Exec(ctx, `UPDATE exam_sitting_mail_recipients SET desired_occurrence_id=NULL,desired_delivery_id=NULL,
				desired_sitting_revision=NULL,desired_template_key=NULL
				WHERE desired_delivery_id=? AND desired_occurrence_id=?`, row.ID, row.OccurrenceID); err != nil {
				return nil, fmt.Errorf("clear retained Sitting mail projection: %w", err)
			}
			if _, err := tx.Exec(ctx, `DELETE FROM mail_deliveries WHERE id=?`, row.ID); err != nil {
				return nil, fmt.Errorf("delete terminal mail delivery: %w", err)
			}
			if _, err := tx.Exec(ctx, `DELETE FROM exam_sitting_mail_fanouts f WHERE f.occurrence_id=? AND f.completed_at IS NOT NULL
				AND NOT EXISTS (SELECT 1 FROM mail_deliveries d WHERE d.occurrence_id=f.occurrence_id)`, row.OccurrenceID); err != nil {
				return nil, fmt.Errorf("delete retained Sitting mail fan-out: %w", err)
			}
			if _, err := tx.Exec(ctx, `DELETE FROM mail_occurrences o WHERE o.id=?
				AND NOT EXISTS (SELECT 1 FROM mail_deliveries d WHERE d.occurrence_id=o.id)
				AND NOT EXISTS (SELECT 1 FROM exam_sitting_mail_fanouts f WHERE f.occurrence_id=o.id)`, row.OccurrenceID); err != nil {
				return nil, fmt.Errorf("delete orphan mail occurrence: %w", err)
			}
			result.Affected++
		}
		remaining := limit - result.Affected
		if remaining > 0 {
			var emptyFanouts []struct {
				OccurrenceID string `db:"occurrence_id"`
			}
			if err := tx.Select(ctx, &emptyFanouts, `SELECT f.occurrence_id FROM exam_sitting_mail_fanouts f
				WHERE f.completed_at IS NOT NULL AND NOT EXISTS (
					SELECT 1 FROM mail_deliveries d WHERE d.occurrence_id=f.occurrence_id)
				AND ((f.terminal_reason IN ('failed','orphaned') AND f.completed_at<=statement_timestamp()-INTERVAL '180 days')
				  OR (f.terminal_reason NOT IN ('failed','orphaned') AND f.completed_at<=statement_timestamp()-INTERVAL '90 days'))
				ORDER BY f.completed_at,f.occurrence_id LIMIT ? FOR UPDATE OF f SKIP LOCKED`, remaining+1); err != nil {
				return nil, fmt.Errorf("select empty retained Sitting mail fan-outs: %w", err)
			}
			if len(emptyFanouts) > remaining {
				result.More = true
				emptyFanouts = emptyFanouts[:remaining]
			}
			for _, fanout := range emptyFanouts {
				if _, err := tx.Exec(ctx, `UPDATE exam_sitting_mail_recipients SET desired_occurrence_id=NULL,desired_delivery_id=NULL,
					desired_sitting_revision=NULL,desired_template_key=NULL WHERE desired_occurrence_id=?`, fanout.OccurrenceID); err != nil {
					return nil, fmt.Errorf("clear empty retained Sitting mail projection: %w", err)
				}
				if _, err := tx.Exec(ctx, `DELETE FROM exam_sitting_mail_fanouts WHERE occurrence_id=?`, fanout.OccurrenceID); err != nil {
					return nil, fmt.Errorf("delete empty retained Sitting mail fan-out: %w", err)
				}
				if _, err := tx.Exec(ctx, `DELETE FROM mail_occurrences WHERE id=? AND NOT EXISTS (
					SELECT 1 FROM mail_deliveries WHERE occurrence_id=?)`, fanout.OccurrenceID, fanout.OccurrenceID); err != nil {
					return nil, fmt.Errorf("delete empty retained Sitting mail occurrence: %w", err)
				}
				result.Affected++
			}
		}
		return result, nil
	})
}

func (s SQLMailStore) QueueSnapshot(ctx context.Context) (*store.MailQueueSnapshot, error) {
	var rows []struct {
		TemplateKey       string    `db:"template_key"`
		State             string    `db:"state"`
		PublicFailureCode string    `db:"public_failure_code"`
		EligibleAt        time.Time `db:"eligible_at"`
	}
	if err := s.GetMaster().Select(ctx, &rows, `
		WITH eligible AS (
			(SELECT d.template_key,d.state,d.public_failure_code,j.available_at AS eligible_at
			   FROM jobs j JOIN mail_deliveries d ON d.job_id=j.id
			  WHERE j.type IN ('mail.deliver','mail.deliver_credential') AND j.status='queued' AND d.state='queued'
			    AND j.available_at<=statement_timestamp()
			  ORDER BY j.available_at,j.id LIMIT 501)
			UNION ALL
			(SELECT d.template_key,d.state,d.public_failure_code,d.updated_at AS eligible_at
			   FROM jobs j JOIN mail_deliveries d ON d.job_id=j.id
			  WHERE j.type IN ('mail.deliver','mail.deliver_credential') AND j.status IN ('running','cancel_requested') AND d.state='sending'
			  ORDER BY d.updated_at,d.id LIMIT 501)
		)
		SELECT template_key,state,public_failure_code,eligible_at
		  FROM eligible ORDER BY eligible_at,template_key,state LIMIT 501`); err != nil {
		return nil, fmt.Errorf("read bounded eligible mail queue: %w", err)
	}
	result := &store.MailQueueSnapshot{More: len(rows) > mailMaintenanceMaximumBatch}
	if result.More {
		rows = rows[:mailMaintenanceMaximumBatch]
	}
	type countKey struct {
		TemplateKey, State, Code string
	}
	counts := make(map[countKey]int64)
	oldestObserved := make(map[countKey]time.Time)
	for _, row := range rows {
		key := countKey{TemplateKey: row.TemplateKey, State: row.State, Code: row.PublicFailureCode}
		counts[key]++
		if oldestObserved[key].IsZero() || row.EligibleAt.Before(oldestObserved[key]) {
			oldestObserved[key] = model.TimeUTC(row.EligibleAt)
		}
		if row.State == string(model.MailDeliveryQueued) {
			if result.OldestQueuedAt.IsZero() || row.EligibleAt.Before(result.OldestQueuedAt) {
				result.OldestQueuedAt = model.TimeUTC(row.EligibleAt)
			}
		}
	}
	result.Counts = make([]store.MailQueueCount, 0, len(counts))
	for key, count := range counts {
		item := store.MailQueueCount{TemplateKey: model.MailTemplateKey(key.TemplateKey), State: model.MailDeliveryState(key.State), PublicFailureCode: key.Code, Count: count, OldestObservedAt: oldestObserved[key]}
		result.Counts = append(result.Counts, item)
	}
	sort.Slice(result.Counts, func(i, j int) bool {
		left, right := result.Counts[i], result.Counts[j]
		if left.TemplateKey != right.TemplateKey {
			return left.TemplateKey < right.TemplateKey
		}
		if left.State != right.State {
			return left.State < right.State
		}
		return left.PublicFailureCode < right.PublicFailureCode
	})
	return result, nil
}

func (s SQLMailStore) ActivePayloadKeyIDs(ctx context.Context) ([]string, error) {
	var keyIDs []string
	if err := s.GetMaster().Select(ctx, &keyIDs, `SELECT key_id FROM mail_payload_keys ORDER BY key_id LIMIT 10`); err != nil {
		return nil, fmt.Errorf("read active mail payload key ids: %w", err)
	}
	if len(keyIDs) > 9 {
		return nil, invalidPersistedState("mail_delivery", "encrypted_payload", fmt.Errorf("active payload key ring exceeds bound"))
	}
	for _, keyID := range keyIDs {
		if len(keyID) != 32 {
			return nil, invalidPersistedState("mail_delivery", "encrypted_payload", fmt.Errorf("active payload key id is invalid"))
		}
	}
	return keyIDs, nil
}

func validMailMaintenanceLimit(limit int) bool {
	return limit > 0 && limit <= mailMaintenanceMaximumBatch
}
