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
	"encoding/json"
	"errors"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func (s *sqlExamAttemptStore) ListExpiredDeliveries(ctx context.Context, limit int) ([]store.DeliveryExpiryDue, error) {
	if limit < 1 || limit > 200 {
		return nil, store.NewErrInvalidInput("delivery_expiry", "limit", nil)
	}
	var rows []struct {
		Family  string `db:"family"`
		Source  string `db:"source"`
		Attempt string `db:"attempt"`
		Part    string `db:"part"`
		Sitting string `db:"sitting"`
		Class   string `db:"class"`
	}
	err := s.GetMaster().Select(ctx, &rows, `SELECT d.family,d.source,d.attempt,d.part,a.exam_sitting_id AS sitting,sit.class_id AS class FROM (
 (SELECT 'native' AS family,delivery_stream_id AS source,exam_attempt_id AS attempt,participation_id AS part,upload_expires_at FROM exam_attempt_security_owners WHERE upload_expires_at<=clock_timestamp() AND NOT upload_expired ORDER BY upload_expires_at,delivery_stream_id LIMIT ?)
 UNION ALL
 (SELECT 'browser' AS family,id::text AS source,exam_attempt_id AS attempt,participation_id AS part,upload_expires_at FROM browser_activity_sources WHERE upload_expires_at<=clock_timestamp() AND NOT upload_expired ORDER BY upload_expires_at,id LIMIT ?)
 ) d JOIN exam_attempts a ON a.id=d.attempt JOIN exam_sittings sit ON sit.id=a.exam_sitting_id
 ORDER BY d.upload_expires_at,d.family,d.source LIMIT ?`, limit, limit, limit)
	if err != nil {
		return nil, err
	}
	result := make([]store.DeliveryExpiryDue, 0, len(rows))
	for _, r := range rows {
		v := store.DeliveryExpiryDue{Family: r.Family, SourceID: r.Source, AttemptID: model.ExamAttemptID(r.Attempt), ParticipationID: model.AttemptParticipationID(r.Part), SittingID: model.ExamSittingID(r.Sitting), ClassID: model.ClassID(r.Class)}
		if !v.Valid() {
			return nil, model.ErrDeliveryInvalid
		}
		result = append(result, v)
	}
	return result, nil
}
func (s *sqlExamAttemptStore) ExpireDelivery(ctx context.Context, input *store.DeliveryExpiry) (bool, error) {
	if input == nil || !input.Due.Valid() || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return false, store.NewErrInvalidInput("delivery_expiry", "input", nil)
	}
	d := input.Due
	return runSQLTransaction(ctx, s.GetMaster().Begin, "expire closed delivery", func(ctx context.Context, tx *sqlxTxWrapper) (bool, error) {
		var id string
		if err := tx.Get(ctx, &id, `SELECT s.id FROM exam_sittings s JOIN exam_attempts a ON a.exam_sitting_id=s.id WHERE s.id=? AND s.class_id=? AND a.id=? FOR UPDATE OF s`, d.SittingID.String(), d.ClassID.String(), d.AttemptID.String()); err != nil {
			return false, err
		}
		if err := tx.Get(ctx, &id, `SELECT id FROM exam_attempts WHERE id=? FOR UPDATE`, d.AttemptID.String()); err != nil {
			return false, err
		}
		var now time.Time
		if err := tx.Get(ctx, &now, `SELECT clock_timestamp()`); err != nil {
			return false, err
		}
		now = now.UTC().Truncate(time.Millisecond)
		changed, err := expireDeliveryOwner(ctx, tx, d, now)
		if err != nil {
			return false, err
		}
		data, err := model.EncodeAuditData(map[string]any{"family": d.Family, "source_id": d.SourceID, "exam_attempt_id": d.AttemptID.String(), "expired": changed})
		if err != nil {
			return false, err
		}
		if _, err := completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", data, input.AuditAt); err != nil {
			return false, err
		}
		return changed, nil
	})
}

// The caller holds Sitting then Attempt and owns an audit in this transaction.
func expireDeliveryOwner(ctx context.Context, tx *sqlxTxWrapper, d store.DeliveryExpiryDue, now time.Time) (bool, error) {
	var row struct {
		Raw       []byte       `db:"closure_canonical"`
		Allocated int64        `db:"allocated_through_sequence"`
		Expired   bool         `db:"upload_expired"`
		Expires   sql.NullTime `db:"upload_expires_at"`
	}
	query := `SELECT closure_canonical,allocated_through_sequence,upload_expired,upload_expires_at FROM exam_attempt_security_owners WHERE delivery_stream_id=? AND exam_attempt_id=? AND participation_id=? FOR UPDATE`
	if d.Family == "browser" {
		query = `SELECT closure_canonical,allocated_through_sequence,upload_expired,upload_expires_at FROM browser_activity_sources WHERE id=?::uuid AND exam_attempt_id=? AND participation_id=? FOR UPDATE`
	}
	err := tx.Get(ctx, &row, query, d.SourceID, d.AttemptID.String(), d.ParticipationID.String())
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	changed := err == nil && !row.Expired && row.Expires.Valid && !now.Before(row.Expires.Time)
	if changed {
		if d.Family == "browser" {
			var before, after int64
			if err := tx.Get(ctx, &before, `SELECT browser_inventory_revision FROM exam_attempt_delivery_budgets WHERE exam_attempt_id=?`, d.AttemptID.String()); err != nil {
				return false, err
			}

			if _, err := settleBrowserSource(ctx, tx, model.BrowserSourceSessionID(d.SourceID)); err != nil {
				return false, err
			}
			if _, err := tx.Exec(ctx, `UPDATE browser_activity_sources SET upload_expired=true WHERE id=?::uuid`, d.SourceID); err != nil {
				return false, err
			}
			if err := tx.Get(ctx, &after, `SELECT browser_inventory_revision FROM exam_attempt_delivery_budgets WHERE exam_attempt_id=?`, d.AttemptID.String()); err != nil {
				return false, err
			}
			if after != before {
				if err := invalidateDeliveryReviewInventory(ctx, tx, d.AttemptID); err != nil {
					return false, err
				}
			}
		} else {
			var closure model.DeliveryClosure
			if json.Unmarshal(row.Raw, &closure) != nil {
				return false, model.ErrDeliveryInvalid
			}
			closure, err = closure.Expire(true, now)
			if err != nil {
				return false, err
			}
			raw, err := canonicalPreflightValue(closure)
			if err != nil {
				return false, err
			}
			if _, err := tx.Exec(ctx, `UPDATE exam_attempt_security_owners SET closure_canonical=?,terminal_missing_through_sequence=allocated_through_sequence,upload_expired=true WHERE participation_id=?`, raw, d.ParticipationID.String()); err != nil {
				return false, err
			}
			var binding []byte
			if err := tx.Get(ctx, &binding, `SELECT binding_canonical FROM exam_attempt_security_owners WHERE participation_id=?`, d.ParticipationID.String()); err != nil {
				return false, err
			}
			if err := advanceNativeInterpretation(ctx, tx, nativeDeliveryOwner{Binding: binding, ParticipationID: d.ParticipationID.String(), Allocated: row.Allocated, TerminalThrough: row.Allocated, Closure: closure, Now: now}); err != nil {
				return false, err
			}
		}
	}
	return changed, nil
}

// Reconciliation cannot retire content based on a completion that an overdue
// source would invalidate. Its own audit covers this finite expiry pass.
func settleRetiringDeliveries(ctx context.Context, tx *sqlxTxWrapper, submission model.SubmissionID, now time.Time) (int, error) {
	attempt, err := lockRetiringDeliveryAttempt(ctx, tx, submission)
	if err != nil {
		return 0, err
	}
	var rows []struct {
		Family string `db:"family"`
		Source string `db:"source"`
		Part   string `db:"part"`
	}
	if err := tx.Select(ctx, &rows, `SELECT 'browser' AS family,id::text AS source,participation_id AS part FROM browser_activity_sources WHERE exam_attempt_id=? AND upload_expires_at<=? AND NOT upload_expired UNION ALL SELECT 'native' AS family,delivery_stream_id AS source,participation_id AS part FROM exam_attempt_security_owners WHERE exam_attempt_id=? AND upload_expires_at<=? AND NOT upload_expired LIMIT 201`, attempt.String(), now, attempt.String(), now); err != nil {
		return 0, err
	}
	if len(rows) > 200 {
		return 0, model.ErrDeliveryInvalid
	}
	var before, after int
	if err := tx.Get(ctx, &before, `SELECT count(*) FROM retention_retirements WHERE submission_id=? AND state='grace'`, submission.String()); err != nil {
		return 0, err
	}
	for _, row := range rows {
		if _, err := expireDeliveryOwner(ctx, tx, store.DeliveryExpiryDue{Family: row.Family, SourceID: row.Source, AttemptID: attempt, ParticipationID: model.AttemptParticipationID(row.Part)}, now.UTC().Truncate(time.Millisecond)); err != nil {
			return 0, err
		}
	}
	if err := tx.Get(ctx, &after, `SELECT count(*) FROM retention_retirements WHERE submission_id=? AND state='grace'`, submission.String()); err != nil {
		return 0, err
	}
	return before - after, nil
}
