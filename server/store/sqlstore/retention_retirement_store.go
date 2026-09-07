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

type retentionRetirementRow struct {
	ID                 string         `db:"id"`
	ExamID             string         `db:"exam_id"`
	SittingID          string         `db:"exam_sitting_id"`
	SubmissionID       string         `db:"submission_id"`
	Category           string         `db:"category"`
	State              string         `db:"state"`
	PolicyRevision     int64          `db:"policy_revision"`
	ControlRevision    int64          `db:"control_revision"`
	CompletionRevision int64          `db:"completion_revision"`
	ScheduledAt        time.Time      `db:"scheduled_at"`
	RetireAfter        time.Time      `db:"retire_after"`
	RetiredAt          sql.NullTime   `db:"retired_at"`
	CancelledAt        sql.NullTime   `db:"cancelled_at"`
	CancellationReason sql.NullString `db:"cancellation_reason"`
	PurgePending       int64          `db:"purge_pending"`
	PurgeVerified      int64          `db:"purge_verified"`
}

const retentionRetirementQuery = `SELECT r.id,r.exam_id,r.exam_sitting_id,r.submission_id,r.category,r.state,r.policy_revision,r.control_revision,
		r.completion_revision,r.scheduled_at,r.retire_after,r.retired_at,r.cancelled_at,r.cancellation_reason,
		(SELECT count(*) FROM retention_purge_objects WHERE retirement_id=r.id AND absence_verified_at IS NULL) AS purge_pending,
		(SELECT count(*) FROM retention_purge_objects WHERE retirement_id=r.id AND absence_verified_at IS NOT NULL) AS purge_verified
		FROM retention_retirements r`

func getActiveRetirement(ctx context.Context, executor sqlxExecutor, id model.SubmissionID, category model.RetentionCategory) (*model.RetentionRetirement, error) {
	var row retentionRetirementRow
	err := executor.Get(ctx, &row, retentionRetirementQuery+` WHERE r.submission_id=? AND r.category=? AND r.state<>'cancelled'`, id.String(), category)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return row.retirement()
}

func (row retentionRetirementRow) retirement() (*model.RetentionRetirement, error) {
	r := &model.RetentionRetirement{ID: model.RetentionRetirementID(row.ID),
		Scope:    model.RetentionHoldScope{ExamID: model.ExamID(row.ExamID), SittingID: model.ExamSittingID(row.SittingID), SubmissionID: model.SubmissionID(row.SubmissionID)},
		Category: model.RetentionCategory(row.Category), State: model.RetentionRetirementState(row.State), PolicyRevision: row.PolicyRevision,
		ControlRevision: row.ControlRevision, CompletionRevision: row.CompletionRevision,
		ScheduledAt: model.TimeUTC(row.ScheduledAt), RetireAfter: model.TimeUTC(row.RetireAfter),
		RetiredAt: optionalTime(row.RetiredAt), CancelledAt: optionalTime(row.CancelledAt), CancellationReason: row.CancellationReason.String,
		PurgePending: row.PurgePending, PurgeVerified: row.PurgeVerified}
	if err := r.Validate(); err != nil {
		return nil, invalidPersistedState("retention_retirement", "value", err)
	}
	return r, nil
}

func (s *SQLRetentionStore) ReconcileSubmission(ctx context.Context, input *store.RetentionReconciliation) (*store.RetentionReconciliationResult, error) {
	if input == nil || !input.SubmissionID.IsValid() || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("retention_retirement", "reconciliation", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "retention reconciliation", func(ctx context.Context, tx *sqlxTxWrapper) (*store.RetentionReconciliationResult, error) {
		policy, err := getRetentionPolicy(ctx, tx, "FOR UPDATE OF p,i")
		if err != nil {
			return nil, err
		}
		control, err := getRetentionControl(ctx, tx, policy.InstitutionID, true)
		if err != nil {
			return nil, err
		}
		var facts retentionFactsRow
		if err = tx.Get(ctx, &facts, retentionFactsQuery+` AND sub.id=?`, policy.InstitutionID.String(), input.SubmissionID.String()); err != nil {
			return nil, translateError("submission", input.SubmissionID.String(), err)
		}
		var id string
		if err = tx.Get(ctx, &id, `SELECT id FROM exams WHERE id=? FOR UPDATE`, facts.ExamID); err != nil {
			return nil, err
		}
		if err = tx.Get(ctx, &id, `SELECT id FROM exam_sittings WHERE id=? FOR UPDATE`, facts.SittingID); err != nil {
			return nil, err
		}
		if err = tx.Get(ctx, &id, `SELECT id FROM exam_submissions WHERE id=? FOR UPDATE`, input.SubmissionID.String()); err != nil {
			return nil, err
		}
		var at time.Time
		if err = tx.Get(ctx, &at, `SELECT clock_timestamp()`); err != nil {
			return nil, err
		}
		at = model.TimeUTC(at)
		// Re-read after the same Exam/Sitting fences used by holds, completion,
		// late evidence, and export source selection.
		if err = tx.Get(ctx, &facts, retentionFactsQuery+` AND sub.id=?`, policy.InstitutionID.String(), input.SubmissionID.String()); err != nil {
			return nil, err
		}
		result := &store.RetentionReconciliationResult{}
		records := facts.records()
		// Integrity retires first. Work may never disappear while its retained
		// integrity still depends on it, even when both grace periods have ended.
		for _, index := range []int{1, 0} {
			record := records[index]
			eligibility, err := record.Eligibility(policy, at)
			if err != nil {
				return nil, err
			}
			retirement, err := getActiveRetirement(ctx, tx, record.Scope.SubmissionID, record.Category)
			if err != nil {
				return nil, err
			}
			if record.RetiredAt.Valid {
				continue
			}
			if retirement != nil && retirement.State == model.RetentionRetirementGrace &&
				(!control.Permits(policy) || eligibility.Blocker != model.RetentionBlockerNone ||
					retirement.PolicyRevision != policy.Revision || retirement.ControlRevision != control.Revision || retirement.CompletionRevision != record.CompletionRevision) {
				if err := cancelOneRetirement(ctx, tx, retirement.ID, at, "eligibility_changed"); err != nil {
					return nil, err
				}
				result.Cancelled++
				retirement = nil
			}
			if !control.Permits(policy) || eligibility.Blocker != model.RetentionBlockerNone {
				continue
			}
			if retirement == nil {
				if err = scheduleRetention(ctx, tx, policy, control, record, at); err != nil {
					return nil, err
				}
				result.Scheduled++
				continue
			}
			if retirement.State != model.RetentionRetirementGrace || at.Before(retirement.RetireAfter) {
				continue
			}
			if record.Category == model.RetentionCategoryWork && record.HasIntegrity {
				var integrityRetired bool
				if err = tx.Get(ctx, &integrityRetired, `SELECT integrity_retired_at IS NOT NULL FROM exam_submissions WHERE id=?`, input.SubmissionID.String()); err != nil {
					return nil, err
				}
				if !integrityRetired {
					continue
				}
			}
			if err = commitRetention(ctx, tx, retirement, at); err != nil {
				return nil, err
			}
			result.Retired++
		}
		encoded, err := model.EncodeAuditData(map[string]any{"submission_id": input.SubmissionID.String(), "policy_revision": policy.Revision,
			"control_revision": control.Revision, "scheduled": result.Scheduled, "cancelled": result.Cancelled, "retired": result.Retired})
		if err != nil {
			return nil, err
		}
		if _, err = completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt); err != nil {
			return nil, err
		}
		return result, nil
	})
}

func cancelOneRetirement(ctx context.Context, tx *sqlxTxWrapper, id model.RetentionRetirementID, at time.Time, reason string) error {
	_, err := tx.Exec(ctx, `WITH cancelled AS (UPDATE retention_retirements SET state='cancelled',cancelled_at=GREATEST(scheduled_at,?),cancellation_reason=?
		WHERE id=? AND state='grace' RETURNING id)
		UPDATE retention_notices SET cancelled_at=GREATEST(created_at,?),delivery_state=CASE WHEN mail_delivery_id IS NULL AND delivery_state='pending' THEN 'suppressed' ELSE delivery_state END WHERE retirement_id IN (SELECT id FROM cancelled) AND cancelled_at IS NULL`, at, reason, id.String(), at)
	return err
}

func scheduleRetention(ctx context.Context, tx *sqlxTxWrapper, policy *model.RetentionPolicy, control *model.RetentionControl, record model.RetentionRecord, at time.Time) error {
	id := model.NewRetentionRetirementID()
	_, err := tx.Exec(ctx, `INSERT INTO retention_retirements(id,institution_id,exam_id,exam_sitting_id,submission_id,category,state,
		policy_revision,control_revision,completion_revision,scheduled_at,retire_after) VALUES (?,?,?,?,?,?,'grace',?,?,?,?,?)`,
		id.String(), policy.InstitutionID.String(), record.Scope.ExamID.String(), record.Scope.SittingID.String(), record.Scope.SubmissionID.String(), record.Category,
		policy.Revision, control.Revision, record.CompletionRevision, at, at.Add(time.Duration(policy.DeletionGraceDays)*24*time.Hour))
	if err != nil {
		return err
	}
	// The notice is a durable product record, independent of SMTP availability.
	// Managers and operators are resolved at schedule time. Candidate inclusion
	// is a deliberate Institution policy option, initially false.
	_, err = tx.Exec(ctx, `INSERT INTO retention_notices(retirement_id,recipient_user_id,created_at)
		SELECT ?,u.id,? FROM users u WHERE u.archived_at IS NULL AND u.disabled_at IS NULL AND (
		 EXISTS(SELECT 1 FROM exam_managers m JOIN exams e ON e.id=m.exam_id
		  JOIN academic_unit_members um ON um.academic_unit_id=e.academic_unit_id AND um.user_id=m.user_id
		  WHERE m.exam_id=? AND m.user_id=u.id AND um.archived_at IS NULL AND um.start_at<=? AND (um.end_at IS NULL OR um.end_at>?)) OR
		 EXISTS(SELECT 1 FROM role_bindings b JOIN roles r ON r.id=b.role_id WHERE b.user_id=u.id AND b.scope_type='institution'
		  AND b.scope_id=? AND b.archived_at IS NULL AND b.start_at<=? AND (b.end_at IS NULL OR b.end_at>?) AND r.name=? AND r.built_in=true AND r.archived_at IS NULL) OR
		 (? AND EXISTS(SELECT 1 FROM exam_submissions sub JOIN exam_attempts a ON a.id=sub.exam_attempt_id WHERE sub.id=? AND a.candidate_user_id=u.id)))`,
		id.String(), at, record.Scope.ExamID.String(), at, at, policy.InstitutionID.String(), at, at, model.SystemAdministratorRoleName, policy.CandidateNotices, record.Scope.SubmissionID.String())
	return err
}

func commitRetention(ctx context.Context, tx *sqlxTxWrapper, retirement *model.RetentionRetirement, at time.Time) error {
	// This named transition is the only ordinary SQL path authorizing the
	// immutability guards' deletion exception. The receipt commits with removal.
	_, err := tx.Exec(ctx, `UPDATE retention_retirements SET state='retired',retired_at=? WHERE id=? AND state='grace'`, at, retirement.ID.String())
	if err != nil {
		return err
	}
	if retirement.Category == model.RetentionCategoryIntegrity {
		return retireSubmissionIntegrity(ctx, tx, retirement.Scope.SubmissionID, at)
	}
	return retireSubmissionWork(ctx, tx, retirement, at)
}

func retireSubmissionIntegrity(ctx context.Context, tx *sqlxTxWrapper, id model.SubmissionID, at time.Time) error {
	// Private Review text and derived inventories expire with their source
	// evidence. Delete children first while the retirement receipt is visible
	// to the database's immutable-record guards.
	// Export creation retains only an Export ID; its independently expiring
	// archive and exact retry survive source retirement without copying content.
	queries := []string{
		`DELETE FROM submission_review_inventory_evidence WHERE submission_review_id IN (SELECT id FROM submission_reviews WHERE submission_id=?)`,
		`DELETE FROM submission_review_inventory_flags WHERE submission_review_id IN (SELECT id FROM submission_reviews WHERE submission_id=?)`,
		`DELETE FROM submission_review_inventory_discrepancies WHERE submission_id=?`,
		`DELETE FROM integrity_review_decisions WHERE submission_review_id IN (SELECT id FROM submission_reviews WHERE submission_id=?)`,
		`DELETE FROM submission_reviews WHERE submission_id=?`,
		`DELETE FROM submission_review_waivers WHERE submission_id=?`,
		`DELETE FROM integrity_discrepancies WHERE submission_id=?`,
		`DELETE FROM exam_attempt_focus_loss_evaluations WHERE exam_attempt_id=(SELECT exam_attempt_id FROM exam_submissions WHERE id=?)`,
		`DELETE FROM exam_attempt_suspensions WHERE exam_attempt_id=(SELECT exam_attempt_id FROM exam_submissions WHERE id=?)`,
		`DELETE FROM integrity_evidence WHERE exam_attempt_id=(SELECT exam_attempt_id FROM exam_submissions WHERE id=?)`,
		`DELETE FROM integrity_flags WHERE exam_attempt_id=(SELECT exam_attempt_id FROM exam_submissions WHERE id=?)`,
		`DELETE FROM browser_activity_events WHERE exam_attempt_id=(SELECT exam_attempt_id FROM exam_submissions WHERE id=?)`,
		`DELETE FROM exam_attempt_manager_end_actions WHERE exam_attempt_id=(SELECT exam_attempt_id FROM exam_submissions WHERE id=?)`,
		`DELETE FROM command_outcomes WHERE operation <> '` + store.ExamExportCreateOperation + `' AND original_audit_event_id IN (SELECT id FROM audit_events WHERE resource_type='submission' AND resource_id=?)`,
	}
	for _, query := range queries {
		if _, err := tx.Exec(ctx, query, id.String()); err != nil {
			return err
		}
	}
	// Clear the immutable header's integrity payload before deleting its source
	// foreign key. The explicit state keeps manager projections from inventing
	// a settled result or reproducing the former unresolved count.
	_, err := tx.Exec(ctx, `UPDATE exam_submissions SET integrity_retired_at=?,integrity_state='retired',
		final_focus_loss_sequence=NULL,unresolved_integrity_count=NULL,browser_activity_state=NULL,
		browser_activity_source_session_id=NULL,browser_activity_final_sequence=NULL,browser_activity_gap_reason=NULL
		WHERE id=? AND integrity_retired_at IS NULL`, at, id.String())
	if err != nil {
		return err
	}
	// A source's high-water marks, reset reason and predecessor chain are also
	// integrity data. Delete the whole Attempt chain in one statement so its
	// internal predecessor references disappear with their owners.
	_, err = tx.Exec(ctx, `DELETE FROM browser_activity_sources WHERE exam_attempt_id=(SELECT exam_attempt_id FROM exam_submissions WHERE id=?)`, id.String())
	return err
}

func retireSubmissionWork(ctx context.Context, tx *sqlxTxWrapper, retirement *model.RetentionRetirement, at time.Time) error {
	id := retirement.Scope.SubmissionID.String()
	// Content metadata already carries the immutable managed object identity;
	// key selection remains owned by File Content, never by SQL or HTTP.
	_, err := tx.Exec(ctx, `INSERT INTO retention_purge_objects(retirement_id,object_id,writer_finished)
		SELECT ?,o.id,o.content_version IS NOT NULL FROM exam_attempt_workspace_objects o JOIN exam_submissions sub ON sub.workspace_id=o.workspace_id
		WHERE sub.id=? AND o.storage_origin='attempt' ON CONFLICT DO NOTHING`, retirement.ID.String(), id)
	if err != nil {
		return err
	}
	queries := []string{
		`DELETE FROM exam_submission_manifest_entries WHERE submission_id=?`,
		`DELETE FROM exam_attempt_workspace_entries WHERE workspace_id=(SELECT workspace_id FROM exam_submissions WHERE id=?)`,
		`DELETE FROM exam_attempt_workspace_journal WHERE workspace_id=(SELECT workspace_id FROM exam_submissions WHERE id=?)`,
		`DELETE FROM exam_attempt_workspace_objects WHERE workspace_id=(SELECT workspace_id FROM exam_submissions WHERE id=?) AND storage_origin='starter'`,
		`DELETE FROM command_outcomes WHERE operation <> '` + store.ExamExportCreateOperation + `' AND original_audit_event_id IN (SELECT e.id FROM audit_events e JOIN exam_submissions sub ON sub.id=?
		 WHERE (e.resource_type='submission' AND e.resource_id=sub.id) OR e.result->>'workspace_id'=sub.workspace_id OR
		 e.result->>'exam_attempt_workspace_id'=sub.workspace_id OR e.result->>'exam_submission_id'=sub.id OR e.result->>'exam_attempt_id'=sub.exam_attempt_id)`,
	}
	for _, query := range queries {
		if _, err = tx.Exec(ctx, query, id); err != nil {
			return err
		}
	}
	// Empty integrity has no retained payload to dispose of, but work removal
	// still closes the late-ingestion path permanently.
	_, err = tx.Exec(ctx, `UPDATE exam_submissions SET work_retired_at=?,integrity_retired_at=COALESCE(integrity_retired_at,?),workspace_cursor=NULL,manifest_digest=NULL,manifest_entry_count=NULL,manifest_total_file_bytes=NULL,integrity_state='retired',final_focus_loss_sequence=NULL,unresolved_integrity_count=NULL,browser_activity_state=NULL,browser_activity_source_session_id=NULL,browser_activity_final_sequence=NULL,browser_activity_gap_reason=NULL WHERE id=? AND work_retired_at IS NULL`, at, at, id)
	return err
}

func (s *SQLRetentionStore) BeginPurgeBatch(ctx context.Context, limit int) ([]store.RetentionPurgeObject, error) {
	if limit < 1 || limit > model.RetentionMaximumPageSize {
		return nil, store.NewErrInvalidInput("retention_purge", "limit", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "begin retention purge batch", func(ctx context.Context, tx *sqlxTxWrapper) ([]store.RetentionPurgeObject, error) {
		var rows []struct {
			RetirementID string `db:"retirement_id"`
			ObjectID     string `db:"object_id"`
		}
		err := tx.Select(ctx, &rows, `WITH candidates AS MATERIALIZED (
			SELECT p.retirement_id,p.object_id,p.verify_after FROM retention_purge_objects p
			JOIN retention_retirements r ON r.id=p.retirement_id JOIN exam_submissions sub ON sub.id=r.submission_id
			WHERE p.absence_verified_at IS NULL AND p.verify_after<=clock_timestamp() AND r.state='retired' AND sub.work_retired_at IS NOT NULL
			AND NOT EXISTS(SELECT 1 FROM exam_attempt_workspace_entries WHERE current_object_id=p.object_id)
			AND NOT EXISTS(SELECT 1 FROM exam_submission_manifest_entries WHERE workspace_object_id=p.object_id)
			ORDER BY p.verify_after,p.retirement_id,p.object_id LIMIT ? FOR UPDATE OF p SKIP LOCKED
		), deferred AS (
			UPDATE retention_purge_objects p SET verify_after=clock_timestamp()+interval '1 hour'
			FROM candidates c WHERE p.retirement_id=c.retirement_id AND p.object_id=c.object_id
			RETURNING p.retirement_id,p.object_id
		)
		SELECT d.retirement_id,d.object_id FROM deferred d JOIN candidates c USING(retirement_id,object_id)
		ORDER BY c.verify_after,d.retirement_id,d.object_id`, limit)
		if err != nil {
			return nil, err
		}
		result := make([]store.RetentionPurgeObject, len(rows))
		for i, row := range rows {
			result[i] = store.RetentionPurgeObject{RetirementID: model.RetentionRetirementID(row.RetirementID), ObjectID: model.AttemptWorkspaceObjectID(row.ObjectID)}
			if !result[i].RetirementID.IsValid() || !result[i].ObjectID.IsValid() {
				return nil, invalidPersistedState("retention_purge", "identity", errors.New("invalid identity"))
			}
		}
		return result, nil
	})
}

func (s *SQLRetentionStore) CompletePurge(ctx context.Context, input *store.RetentionPurgeCompletion) error {
	if input == nil || !input.RetirementID.IsValid() || !input.ObjectID.IsValid() {
		return store.NewErrInvalidInput("retention_purge", "completion", nil)
	}
	_, err := runSQLTransaction(ctx, s.GetMaster().Begin, "retention purge completion", func(ctx context.Context, tx *sqlxTxWrapper) (bool, error) {
		var row struct {
			ObjectID       string `db:"object_id"`
			WriterFinished bool   `db:"writer_finished"`
		}
		err := tx.Get(ctx, &row, `SELECT p.object_id,p.writer_finished FROM retention_purge_objects p JOIN retention_retirements r ON r.id=p.retirement_id
			WHERE p.retirement_id=? AND p.object_id=? AND r.state='retired' FOR UPDATE OF p`, input.RetirementID.String(), input.ObjectID.String())
		if err != nil {
			return false, translateError("retention_purge", input.ObjectID.String(), err)
		}
		id := row.ObjectID
		if !row.WriterFinished {
			// An expired upload reservation does not prove an outstanding
			// remote write cannot finish late. Keep its exact-key reference
			// and repeat observations; never claim final purge for uncertainty.
			_, err = tx.Exec(ctx, `UPDATE retention_purge_objects SET last_absence_observed_at=clock_timestamp(),verify_after=clock_timestamp()+interval '1 hour'
				WHERE retirement_id=? AND object_id=?`, input.RetirementID.String(), id)
			return true, err
		}
		var referenced bool
		if err = tx.Get(ctx, &referenced, `SELECT EXISTS(SELECT 1 FROM exam_attempt_workspace_entries WHERE current_object_id=?)
			OR EXISTS(SELECT 1 FROM exam_submission_manifest_entries WHERE workspace_object_id=?)`, id, id); err != nil {
			return false, err
		}
		if referenced {
			return false, store.NewErrConflict("retention_purge", "object_referenced", nil)
		}
		if _, err = tx.Exec(ctx, `DELETE FROM exam_attempt_workspace_objects WHERE id=? AND storage_origin='attempt'
			AND NOT EXISTS(SELECT 1 FROM exam_attempt_workspace_entries WHERE current_object_id=?)
			AND NOT EXISTS(SELECT 1 FROM exam_submission_manifest_entries WHERE workspace_object_id=?)`, id, id, id); err != nil {
			return false, err
		}
		_, err = tx.Exec(ctx, `UPDATE retention_purge_objects SET absence_verified_at=COALESCE(absence_verified_at,clock_timestamp()),last_absence_observed_at=clock_timestamp(),verify_after=clock_timestamp() WHERE retirement_id=? AND object_id=?`, input.RetirementID.String(), id)
		return true, err
	})
	return err
}

var _ store.RetentionStore = (*SQLRetentionStore)(nil)
