// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"encoding/json"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// Callers hold the Sitting and Submission fences. Delivery takes Sitting before
// Attempt, so retirement cannot race an upload or erase newly accepted evidence.
func lockRetiringDeliveryAttempt(ctx context.Context, tx *sqlxTxWrapper, submission model.SubmissionID) (model.ExamAttemptID, error) {
	var id string
	if err := tx.Get(ctx, &id, `SELECT a.id FROM exam_attempts a JOIN exam_submissions sub ON sub.exam_attempt_id=a.id WHERE sub.id=? FOR UPDATE OF a`, submission.String()); err != nil {
		return "", err
	}
	return model.ParseExamAttemptID(id)
}
func retireSubmissionBrowserActivity(ctx context.Context, tx *sqlxTxWrapper, submission model.SubmissionID, at time.Time) error {
	attempt, err := lockRetiringDeliveryAttempt(ctx, tx, submission)
	if err != nil {
		return err
	}
	var rows []struct {
		ID      string `db:"id"`
		Closure []byte `db:"closure_canonical"`
	}
	if err := tx.Select(ctx, &rows, `SELECT id::text,closure_canonical FROM browser_activity_sources WHERE exam_attempt_id=? ORDER BY participation_id,start_ordinal LIMIT ? FOR UPDATE`, attempt.String(), maximumAttemptBrowserSources+1); err != nil {
		return err
	}
	if int64(len(rows)) > maximumAttemptBrowserSources {
		return invalidPersistedState("delivery_retirement", "value", model.ErrDeliveryInvalid)
	}
	for _, row := range rows {
		var closure model.DeliveryClosure
		if json.Unmarshal(row.Closure, &closure) != nil || closure.Validate(false) != nil || closure.ClosedAt == nil {
			return invalidPersistedState("delivery_retirement", "value", model.ErrDeliveryInvalid)
		}
		if closure.UploadExpiresAt == nil || closure.UploadExpiresAt.After(at) {
			expiry := at.UTC().Truncate(time.Millisecond)
			closure.UploadExpiresAt = &expiry
		}
		raw, err := canonicalPreflightValue(closure)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE browser_activity_sources SET closure_canonical=? WHERE id=?::uuid`, raw, row.ID); err != nil {
			return err
		}
		status, err := browserSourceStatus(ctx, tx, model.BrowserSourceSessionID(row.ID))
		if err != nil {
			return err
		}
		if err := updateBrowserSourceSettlement(ctx, tx, status); err != nil {
			return err
		}
	}
	// Keep only the ownership selector needed to refuse late retries. No policy,
	// location, receipt, reset reason, timing inventory or content is copied here.
	if _, err := tx.Exec(ctx, `INSERT INTO browser_delivery_retired_sources(source_session_id,exam_attempt_id,participation_id,candidate_user_id,registration_id,key_thumbprint,start_ordinal) SELECT id,exam_attempt_id,participation_id,candidate_user_id,registration_id,key_thumbprint,start_ordinal FROM browser_activity_sources WHERE exam_attempt_id=?`, attempt.String()); err != nil {
		return err
	}
	for _, q := range []string{
		`DELETE FROM browser_delivery_declarations WHERE source_session_id IN (SELECT id FROM browser_activity_sources WHERE exam_attempt_id=?)`,
		`DELETE FROM browser_activity_events WHERE exam_attempt_id=?`,
		`DELETE FROM browser_activity_sources WHERE exam_attempt_id=?`,
	} {
		if _, err := tx.Exec(ctx, q, attempt.String()); err != nil {
			return err
		}
	}
	if err := deleteDeliveryRetryCopies(ctx, tx, attempt, true); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE exam_attempt_delivery_budgets SET browser_retired_at=?,browser_pending_bytes=0 WHERE exam_attempt_id=? AND browser_retired_at IS NULL`, at, attempt.String())
	return err
}
func retireSubmissionSecurityOperational(ctx context.Context, tx *sqlxTxWrapper, submission model.SubmissionID, at time.Time) error {
	attempt, err := lockRetiringDeliveryAttempt(ctx, tx, submission)
	if err != nil {
		return err
	}
	for _, table := range []string{"exam_native_occurrences", "exam_native_delivery_records", "exam_native_delivery_batches", "exam_native_delivery_declarations", "exam_native_source_resets"} {
		if _, err := tx.Exec(ctx, `DELETE FROM `+table+` WHERE participation_id IN (SELECT participation_id FROM exam_attempt_security_owners WHERE exam_attempt_id=?)`, attempt.String()); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM command_outcomes WHERE (operation IN (?,?) AND user_id=(SELECT candidate_user_id FROM exam_attempts WHERE id=?) AND original_audit_event_id IN (SELECT id FROM audit_events WHERE resource_id=(SELECT exam_sitting_id FROM exam_attempts WHERE id=?))) OR (operation=? AND original_audit_event_id IN (SELECT id FROM audit_events WHERE result->>'exam_attempt_id'=?))`, store.SecurityPreflightPrepareOperation, store.SecurityPreflightReportOperation, attempt.String(), attempt.String(), store.ExamAttemptConnectOperation, attempt.String()); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM exam_security_preflights WHERE attempt_id=? OR (candidate_user_id=(SELECT candidate_user_id FROM exam_attempts WHERE id=?) AND sitting_id=(SELECT exam_sitting_id FROM exam_attempts WHERE id=?))`, attempt.String(), attempt.String(), attempt.String()); err != nil {
		return err
	}
	// The fixed owner remains for lifetime quotas and current-key authorization;
	// health, occurrence, reset and gap documents have a separate expiry purpose.
	if _, err := tx.Exec(ctx, `UPDATE exam_attempt_security_owners SET binding_canonical='{}',latest_report_canonical='{}',control_ledger_canonical='{}',control_body_canonical=NULL,closure_canonical=NULL,upload_expired=true,summary_canonical=NULL,summary_received_at=NULL WHERE exam_attempt_id=?`, attempt.String()); err != nil {
		return err
	}
	if err := deleteDeliveryRetryCopies(ctx, tx, attempt, false); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE exam_attempt_delivery_budgets SET security_retired_at=?,native_pending_bytes=0 WHERE exam_attempt_id=? AND security_retired_at IS NULL`, at, attempt.String())
	return err
}
func deleteDeliveryRetryCopies(ctx context.Context, tx *sqlxTxWrapper, attempt model.ExamAttemptID, browser bool) error {
	// Export outcomes retain an identity only and follow independent archive expiry.
	operations := []string{store.NativeDeliveryAppendOperation, store.NativeDeliveryGapsOperation, store.NativeDeliveryFinalOperation, store.NativeDeliverySummaryOperation}
	if browser {
		operations = []string{store.BrowserDeliveryAppendOperation, store.BrowserDeliveryGapsOperation, store.BrowserDeliveryFinalOperation, store.BrowserDeliverySummaryOperation}
	}
	for _, op := range operations {
		if _, err := tx.Exec(ctx, `DELETE FROM command_outcomes WHERE operation=? AND original_audit_event_id IN (SELECT id FROM audit_events WHERE result->>'stream_id' IN (SELECT delivery_stream_id FROM exam_attempt_security_owners WHERE exam_attempt_id=?) OR result->>'source_session_id' IN (SELECT source_session_id::text FROM browser_delivery_retired_sources WHERE exam_attempt_id=?))`, op, attempt.String(), attempt.String()); err != nil {
			return err
		}
	}
	family := "native"
	if browser {
		family = "browser"
	}
	_, err := tx.Exec(ctx, `DELETE FROM command_outcomes WHERE operation=? AND original_audit_event_id IN (SELECT id FROM audit_events WHERE result->>'exam_attempt_id'=? AND result->>'family'=?)`, store.DeliveryStopDetailsOperation, attempt.String(), family)
	return err
}
