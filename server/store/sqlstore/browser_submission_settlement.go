// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

// Each source owns a nonrefundable metadata reservation. This bounds the whole
// Attempt inventory even across Participation generations and retirement stubs.
const maximumAttemptBrowserSources = model.DeliveryMetadataLimitBytes / model.BrowserOwnerReservationBytes

func updateBrowserSourceSettlement(ctx context.Context, tx *sqlxTxWrapper, status *model.BrowserSourceStatus) error {
	var unresolved bool
	if err := tx.Get(ctx, &unresolved, `SELECT EXISTS(SELECT 1 FROM browser_activity_events WHERE source_session_id=?::uuid AND interpretation_state=2)`, string(status.SourceSessionID)); err != nil {
		return err
	}
	pending, incomplete, err := model.BrowserSourceSettlementFacts(*status, unresolved)
	if err != nil {
		return invalidPersistedState("browser_submission", "settlement", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE browser_activity_sources SET settlement_pending=?,settlement_incomplete=? WHERE id=?::uuid`, pending, incomplete, string(status.SourceSessionID)); err != nil {
		return err
	}
	var counts struct {
		Sources    int64 `db:"sources"`
		Pending    int64 `db:"pending"`
		Incomplete int64 `db:"incomplete"`
	}
	if err := tx.Get(ctx, &counts, `SELECT count(*) AS sources,count(*) FILTER(WHERE settlement_pending) AS pending,count(*) FILTER(WHERE settlement_incomplete) AS incomplete FROM browser_activity_sources WHERE exam_attempt_id=?`, status.AttemptID.String()); err != nil {
		return err
	}
	if counts.Sources > maximumAttemptBrowserSources {
		return invalidPersistedState("browser_submission", "value", model.ErrDeliveryInvalid)
	}
	state := "settled"
	switch {
	case counts.Sources == 0:
		state = "not_applicable"
	case counts.Pending > 0:
		state = "pending"
	case counts.Incomplete > 0:
		state = "incomplete"
	}
	_, err = tx.Exec(ctx, `UPDATE exam_attempt_delivery_budgets SET browser_inventory_revision=browser_inventory_revision+1,browser_state=?,browser_source_count=?,browser_pending_count=?,browser_incomplete_count=? WHERE exam_attempt_id=? AND (browser_state,browser_source_count,browser_pending_count,browser_incomplete_count) IS DISTINCT FROM (?,?,?,?)`, state, counts.Sources, counts.Pending, counts.Incomplete, status.AttemptID.String(), state, counts.Sources, counts.Pending, counts.Incomplete)
	return err
}
func markBrowserInventoryChanged(ctx context.Context, tx *sqlxTxWrapper, attempt model.ExamAttemptID) error {
	_, err := tx.Exec(ctx, `UPDATE exam_attempt_delivery_budgets SET browser_inventory_revision=browser_inventory_revision+1 WHERE exam_attempt_id=?`, attempt.String())
	if err != nil {
		return err
	}
	return invalidateDeliveryReviewInventory(ctx, tx, attempt)
}
func browserSubmissionSettlement(ctx context.Context, tx *sqlxTxWrapper, attempt model.ExamAttemptID) (model.BrowserSubmissionSettlement, error) {
	var row struct {
		State      string `db:"browser_state"`
		Revision   int64  `db:"browser_inventory_revision"`
		Sources    int64  `db:"browser_source_count"`
		Pending    int64  `db:"browser_pending_count"`
		Incomplete int64  `db:"browser_incomplete_count"`
	}
	if err := tx.Get(ctx, &row, `SELECT browser_state,browser_inventory_revision,browser_source_count,browser_pending_count,browser_incomplete_count FROM exam_attempt_delivery_budgets WHERE exam_attempt_id=?`, attempt.String()); err != nil {
		return model.BrowserSubmissionSettlement{}, err
	}
	value := model.BrowserSubmissionSettlement{State: row.State, InventoryRevision: row.Revision, SourceCount: row.Sources, PendingSourceCount: row.Pending, IncompleteSourceCount: row.Incomplete}
	return value, validatePersistedModel("browser_submission", value)
}
func settleSubmissionBrowserSources(ctx context.Context, tx *sqlxTxWrapper, attempt model.ExamAttemptID, reason model.DeliveryCloseReason, at time.Time) (int64, model.BrowserSubmissionSettlement, error) {
	var ids []string
	if err := tx.Select(ctx, &ids, `SELECT id::text FROM browser_activity_sources WHERE exam_attempt_id=? ORDER BY participation_id,start_ordinal LIMIT ? FOR UPDATE`, attempt.String(), maximumAttemptBrowserSources+1); err != nil {
		return 0, model.BrowserSubmissionSettlement{}, err
	}
	if int64(len(ids)) > maximumAttemptBrowserSources {
		return 0, model.BrowserSubmissionSettlement{}, invalidPersistedState("browser_submission", "value", model.ErrDeliveryInvalid)
	}
	for _, id := range ids {
		source := model.BrowserSourceSessionID(id)
		if err := closeBrowserSource(ctx, tx, source, reason, at); err != nil {
			return 0, model.BrowserSubmissionSettlement{}, err
		}
		if _, err := settleBrowserSource(ctx, tx, source); err != nil {
			return 0, model.BrowserSubmissionSettlement{}, err
		}
	}
	value, err := browserSubmissionSettlement(ctx, tx, attempt)
	// Browser delivery uncertainty has its own changing inventory. It is not an
	// immutable Focus Loss discrepancy or an automatically generated Flag.
	return 0, value, err
}

// Exhaustion is a settlement transition. The caller holds Sitting then Attempt;
// every affected source, pending-byte charge and Review changes in that transaction.
// An empty Participation selects every source in the Attempt.
func settleExhaustedBrowserSources(ctx context.Context, tx *sqlxTxWrapper, attempt model.ExamAttemptID, part model.AttemptParticipationID) error {
	where := "exam_attempt_id=?"
	args := []any{attempt.String()}
	if !part.IsZero() {
		where += " AND participation_id=?"
		args = append(args, part.String())
	}
	var ids []string
	selectionArgs := append(append([]any{}, args...), maximumAttemptBrowserSources+1)
	if err := tx.Select(ctx, &ids, `SELECT id::text FROM browser_activity_sources WHERE `+where+` ORDER BY participation_id,start_ordinal LIMIT ? FOR UPDATE`, selectionArgs...); err != nil {
		return err
	}
	if int64(len(ids)) > maximumAttemptBrowserSources {
		return invalidPersistedState("browser_delivery", "sources", model.ErrDeliveryInvalid)
	}
	var before, after int64
	if err := tx.Get(ctx, &before, `SELECT browser_inventory_revision FROM exam_attempt_delivery_budgets WHERE exam_attempt_id=?`, attempt.String()); err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `UPDATE browser_activity_sources SET terminal_missing_through_sequence=allocated_through_sequence WHERE `+where+` AND terminal_missing_through_sequence<>allocated_through_sequence`, args...)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := settleBrowserSource(ctx, tx, model.BrowserSourceSessionID(id)); err != nil {
			return err
		}
	}
	if changed > 0 {
		return markBrowserInventoryChanged(ctx, tx, attempt)
	}
	if err := tx.Get(ctx, &after, `SELECT browser_inventory_revision FROM exam_attempt_delivery_budgets WHERE exam_attempt_id=?`, attempt.String()); err != nil {
		return err
	}
	if after != before {
		return invalidateDeliveryReviewInventory(ctx, tx, attempt)
	}
	return nil
}
