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
	"github.com/sudosylabs/proctor/server/model"
	"time"
)

// Accepted delivery mutations hold Sitting then Attempt. A late inventory
// change never edits a historical finalization or inherits its release. The
// current aggregate becomes a new draft revision; exact retries do not enter.
func invalidateDeliveryReviewInventory(ctx context.Context, tx *sqlxTxWrapper, attempt model.ExamAttemptID) error {
	var sub struct {
		ID      string    `db:"id"`
		Sitting string    `db:"exam_sitting_id"`
		Now     time.Time `db:"now"`
	}
	err := tx.Get(ctx, &sub, `SELECT sub.id,a.exam_sitting_id,clock_timestamp() AS now FROM exam_submissions sub JOIN exam_attempts a ON a.id=sub.exam_attempt_id WHERE a.id=? AND sub.sealed=true AND sub.integrity_retired_at IS NULL`, attempt.String())
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE exam_attempt_delivery_budgets SET review_inventory_revision=review_inventory_revision+1 WHERE exam_attempt_id=?`, attempt.String()); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE submission_reviews SET state='draft',release_state='withheld',revision=revision+1,delivery_inventory_revision=(SELECT review_inventory_revision FROM exam_attempt_delivery_budgets WHERE exam_attempt_id=?),updated_at=GREATEST(updated_at,?),flag_count=0,evidence_count=0,discrepancy_count=0,evidence_inventory_digest=NULL,finalized_at=NULL,finalized_by_user_id=NULL,released_at=NULL,released_by_user_id=NULL WHERE submission_id=?`, attempt.String(), sub.Now, sub.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM submission_review_release_preparations WHERE submission_id=?`, sub.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE submission_review_waivers SET inventory_invalidated=true WHERE submission_id=?`, sub.ID); err != nil {
		return err
	}
	return invalidateSittingRecordsForIntegrity(ctx, tx, model.ExamSittingID(sub.Sitting), sub.Now)
}

func staleBrowserGroupDecision(ctx context.Context, tx *sqlxTxWrapper, attempt model.ExamAttemptID, flag string) error {
	// A source can be interpreted by a gap declaration or expiry as well as
	// append. Invalidate before marking a previously finalized decision stale.
	if err := invalidateDeliveryReviewInventory(ctx, tx, attempt); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE integrity_review_decisions SET inventory_stale=true WHERE integrity_flag_id=? AND NOT inventory_stale`, flag)
	return err
}
