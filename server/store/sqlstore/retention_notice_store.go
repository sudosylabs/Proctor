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
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type retentionNoticeRow struct {
	RetirementID  string       `db:"retirement_id"`
	RecipientID   string       `db:"recipient_user_id"`
	ExamID        string       `db:"exam_id"`
	SittingID     string       `db:"exam_sitting_id"`
	SubmissionID  string       `db:"submission_id"`
	Category      string       `db:"category"`
	State         string       `db:"state"`
	CreatedAt     time.Time    `db:"created_at"`
	RetireAfter   time.Time    `db:"retire_after"`
	CancelledAt   sql.NullTime `db:"cancelled_at"`
	DeliveryState string       `db:"delivery_state"`
	DeliveryCode  string       `db:"delivery_error_code"`
}

const retentionNoticeSelect = `SELECT n.retirement_id,n.recipient_user_id,r.exam_id,r.exam_sitting_id,r.submission_id,r.category,r.state,
	n.created_at,r.retire_after,n.cancelled_at,n.delivery_state,n.delivery_error_code FROM retention_notices n JOIN retention_retirements r ON r.id=n.retirement_id`

func listRetentionNotices(ctx context.Context, executor sqlxExecutor, query string, args ...any) ([]model.RetentionNotice, error) {
	var rows []retentionNoticeRow
	if err := executor.Select(ctx, &rows, query, args...); err != nil {
		return nil, err
	}
	items := make([]model.RetentionNotice, 0, len(rows))
	for _, row := range rows {
		n := model.RetentionNotice{RetirementID: model.RetentionRetirementID(row.RetirementID), RecipientUserID: model.UserID(row.RecipientID), Scope: model.RetentionHoldScope{ExamID: model.ExamID(row.ExamID), SittingID: model.ExamSittingID(row.SittingID), SubmissionID: model.SubmissionID(row.SubmissionID)}, Category: model.RetentionCategory(row.Category), State: model.RetentionRetirementState(row.State), CreatedAt: model.TimeUTC(row.CreatedAt), RetireAfter: model.TimeUTC(row.RetireAfter), CancelledAt: optionalTime(row.CancelledAt), DeliveryState: row.DeliveryState, DeliveryErrorCode: row.DeliveryCode}
		if !n.RetirementID.IsValid() || !n.RecipientUserID.IsValid() || n.Scope.Validate() != nil || !n.Category.IsValid() {
			return nil, invalidPersistedState("retention_notice", "identity", errors.New("invalid notice"))
		}
		items = append(items, n)
	}
	return items, nil
}

func (s *SQLRetentionStore) ListNotices(ctx context.Context, userID model.UserID, after model.RetentionRetirementID, limit int) ([]model.RetentionNotice, error) {
	if !userID.IsValid() || (!after.IsZero() && !after.IsValid()) || limit < 1 || limit > model.RetentionMaximumPageSize+1 {
		return nil, store.NewErrInvalidInput("retention_notice", "page", nil)
	}
	return listRetentionNotices(ctx, s.GetMaster(), retentionNoticeSelect+` WHERE n.recipient_user_id=? AND n.retirement_id>? ORDER BY n.retirement_id LIMIT ?`, userID.String(), after.String(), limit)
}

func (s *SQLRetentionStore) ListPendingNotices(ctx context.Context, limit int) ([]model.RetentionNotice, error) {
	if limit < 1 || limit > model.RetentionMaximumPageSize {
		return nil, store.NewErrInvalidInput("retention_notice", "limit", nil)
	}
	return listRetentionNotices(ctx, s.GetMaster(), retentionNoticeSelect+` WHERE n.delivery_state='pending' AND n.cancelled_at IS NULL AND r.state='grace'
		ORDER BY n.retirement_id,n.recipient_user_id LIMIT ?`, limit)
}

func (s *SQLRetentionStore) CompleteNotice(ctx context.Context, input *store.RetentionNoticeMail) error {
	if input == nil || !input.RetirementID.IsValid() || !input.RecipientUserID.IsValid() ||
		(input.Mail == nil && input.FailureCode != "mail.preparation_unavailable" && input.FailureCode != "mail.recipient_unavailable") || (input.Mail != nil && input.FailureCode != "") {
		return store.NewErrInvalidInput("retention_notice", "delivery", nil)
	}
	_, err := runSQLTransaction(ctx, s.GetMaster().Begin, "retention notice delivery", func(ctx context.Context, tx *sqlxTxWrapper) (bool, error) {
		var retireAfter time.Time
		if err := tx.Get(ctx, &retireAfter, `SELECT retire_after FROM retention_retirements WHERE id=? FOR UPDATE`, input.RetirementID.String()); err != nil {
			return false, translateError("retention_notice", input.RetirementID.String(), err)
		}
		var current struct {
			State           string       `db:"delivery_state"`
			CancelledAt     sql.NullTime `db:"cancelled_at"`
			RetirementState string       `db:"state"`
		}
		if err := tx.Get(ctx, &current, `SELECT n.delivery_state,n.cancelled_at,r.state FROM retention_notices n JOIN retention_retirements r ON r.id=n.retirement_id
			WHERE n.retirement_id=? AND n.recipient_user_id=? FOR UPDATE OF n`, input.RetirementID.String(), input.RecipientUserID.String()); err != nil {
			return false, translateError("retention_notice", input.RetirementID.String(), err)
		}
		if current.State != "pending" {
			return false, nil
		}
		var at time.Time
		if err := tx.Get(ctx, &at, `SELECT clock_timestamp()`); err != nil {
			return false, err
		}
		state, code, deliveryID := "failed", input.FailureCode, ""
		if current.CancelledAt.Valid || current.RetirementState != "grace" || !retireAfter.After(at) {
			state, code = "suppressed", model.MailDeliveryObsoleteCode
		} else if input.Mail != nil {
			p := input.Mail
			if p.Delivery == nil || p.Occurrence == nil || p.Job == nil || p.Delivery.TargetUserID != input.RecipientUserID || p.Occurrence.ActorUserID != input.RecipientUserID ||
				p.Occurrence.TemplateKey != model.MailTemplateExamRetentionScheduled || p.Delivery.Deadline.After(retireAfter) {
				return false, store.NewErrInvalidInput("retention_notice", "mail", nil)
			}
			if err := validateRecoveryMail(p.Occurrence, p.Delivery, p.Job); err != nil {
				return false, err
			}
			keyID, err := mailPayloadKeyID(p.Delivery.EncryptedPayload)
			if err != nil {
				return false, err
			}
			if err := insertRecoveryMail(ctx, tx, p.Occurrence, p.Delivery, p.Job, keyID); err != nil {
				return false, err
			}
			state, code, deliveryID = string(p.Delivery.State), p.Delivery.PublicFailureCode, p.Delivery.ID.String()
		}
		_, err := tx.Exec(ctx, `UPDATE retention_notices SET delivery_state=?,delivery_error_code=?,mail_delivery_id=? WHERE retirement_id=? AND recipient_user_id=?`, state, code, nullableID(deliveryID), input.RetirementID.String(), input.RecipientUserID.String())
		return true, err
	})
	return err
}

// Start delivery shares the retirement fence with cancellation. An already
// started SMTP operation can finish, but no later retry sends a stale date.
func lockRetentionNoticeMail(ctx context.Context, tx *sqlxTxWrapper, id model.MailDeliveryID) (bool, error) {
	var retirementID string
	if err := tx.Get(ctx, &retirementID, `SELECT retirement_id FROM retention_notices WHERE mail_delivery_id=?`, id.String()); err != nil {
		if isNoRows(err) {
			return false, nil
		}
		return false, err
	}
	var idValue string
	if err := tx.Get(ctx, &idValue, `SELECT id FROM retention_retirements WHERE id=? FOR SHARE`, retirementID); err != nil {
		return false, err
	}
	var relevant bool
	err := tx.Get(ctx, &relevant, `SELECT EXISTS(SELECT 1 FROM retention_notices n JOIN retention_retirements r ON r.id=n.retirement_id
		WHERE n.mail_delivery_id=? AND n.cancelled_at IS NULL AND r.state='grace' AND r.retire_after>clock_timestamp())`, id.String())
	return relevant, err
}
