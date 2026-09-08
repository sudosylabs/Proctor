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

// Every durable audit foreign key is considered here. The schema conformance
// test guards this inventory: adding an owner requires an explicit retention
// decision. Foreign keys remain the final concurrent-insert deletion fence.
const auditHardReferences = `EXISTS(SELECT 1 FROM external_login_states WHERE audit_event_id=a.id)
 OR EXISTS(SELECT 1 FROM exam_attempt_manager_end_actions WHERE audit_event_id=a.id)
 OR EXISTS(SELECT 1 FROM exam_sitting_private_actions WHERE audit_event_id=a.id)
 OR EXISTS(SELECT 1 FROM exam_sitting_live_corrections WHERE audit_event_id=a.id)
 OR EXISTS(SELECT 1 FROM exam_attempt_correction_acknowledgements WHERE audit_event_id=a.id)
 OR EXISTS(SELECT 1 FROM command_outcomes WHERE original_audit_event_id=a.id)
 OR EXISTS(SELECT 1 FROM exam_starter_workspace_objects WHERE retired_by_audit_event_id=a.id)
 OR EXISTS(SELECT 1 FROM exam_attempt_workspace_objects WHERE retired_by_audit_event_id=a.id)
 OR EXISTS(SELECT 1 FROM submission_review_waivers WHERE audit_event_id=a.id)`

// Released hold history may expire with its audit, after both events and the
// release itself have aged. Live retry outcomes still protect both events.
const releasedHoldExpiry = `h.released_at IS NOT NULL AND h.released_at<=bounds.cutoff
 AND EXISTS(SELECT 1 FROM audit_events ha WHERE ha.id=h.creation_audit_event_id AND ha.status<>'attempt' AND ha.updated_at<=bounds.cutoff)
 AND EXISTS(SELECT 1 FROM audit_events ha WHERE ha.id=h.release_audit_event_id AND ha.status<>'attempt' AND ha.updated_at<=bounds.cutoff)
 AND NOT EXISTS(SELECT 1 FROM command_outcomes co WHERE co.original_audit_event_id IN (h.creation_audit_event_id,h.release_audit_event_id))`

// Resource ownership is recovered from authoritative identities, never from a
// JSON audit projection. A broad record is protected by any intersecting hold.
const terminalCleanupAudit = `a.status<>'attempt' AND a.actor_id IS NULL AND a.session_id IS NULL AND a.action='retention_cleanup.manage'`

const auditExpiryFacts = `SELECT a.id,a.created_at AS scan_at, (` + terminalCleanupAudit + `) AS cleanup_audit,
 GREATEST(a.updated_at,(SELECT max(reconciled_at) FROM administrator_recovery_records WHERE audit_event_id=a.id),
   (SELECT max(released_at) FROM retention_holds WHERE creation_audit_event_id=a.id OR release_audit_event_id=a.id)) AS event_at,
 COALESCE(e.id,'') AS exam_id,COALESCE(sit.id,'') AS sitting_id,COALESCE(sub.id,'') AS submission_id,
 (a.status='attempt' OR EXISTS(SELECT 1 FROM exam_sittings pending_sitting WHERE pending_sitting.exam_id=e.id
  AND (sit.id IS NULL OR pending_sitting.id=sit.id) AND (pending_sitting.state NOT IN ('closed','canceled') OR
   (EXISTS(SELECT 1 FROM exam_submissions us JOIN exam_attempts ua ON ua.id=us.exam_attempt_id WHERE ua.exam_sitting_id=pending_sitting.id AND us.integrity_retired_at IS NULL)
    AND NOT EXISTS(SELECT 1 FROM exam_sitting_records_completions c WHERE c.exam_sitting_id=pending_sitting.id AND c.completed_at IS NOT NULL AND c.stale_at IS NULL AND c.evidence_revision=c.completed_evidence_revision))))) AS unfinished,
 (` + auditHardReferences + `
 OR EXISTS(SELECT 1 FROM administrator_recovery_records WHERE audit_event_id=a.id AND reconciled_at IS NULL)
 OR EXISTS(SELECT 1 FROM retention_holds h WHERE (h.creation_audit_event_id=a.id OR h.release_audit_event_id=a.id) AND NOT (` + releasedHoldExpiry + `))) AS referenced,
 EXISTS(SELECT 1 FROM retention_holds h WHERE h.exam_id=e.id AND h.released_at IS NULL AND
   (sit.id IS NULL OR h.exam_sitting_id IS NULL OR h.exam_sitting_id=sit.id) AND
   (sub.id IS NULL OR h.submission_id IS NULL OR h.submission_id=sub.id)) AS held,
 false AS purge_pending,
 EXISTS(SELECT 1 FROM retention_source_protections sp JOIN exam_submissions es ON es.id=sp.submission_id JOIN exam_attempts ea ON ea.id=es.exam_attempt_id
   WHERE ea.exam_id=e.id AND (sit.id IS NULL OR ea.exam_sitting_id=sit.id) AND (sub.id IS NULL OR es.id=sub.id) AND sp.expires_at>bounds.at) AS source_protected
 FROM audit_events a CROSS JOIN bounds
 LEFT JOIN exam_submissions sub ON a.resource_type='submission' AND sub.id=a.resource_id
 LEFT JOIN exam_attempts attempt ON attempt.id=sub.exam_attempt_id
 LEFT JOIN exam_sittings sit ON sit.id=CASE WHEN a.resource_type='exam_sitting' THEN a.resource_id ELSE attempt.exam_sitting_id END
 LEFT JOIN exams e ON e.id=CASE WHEN a.resource_type='exam' THEN a.resource_id ELSE sit.exam_id END`

const receiptExpiryFacts = `SELECT r.id,r.scheduled_at AS scan_at,false AS cleanup_audit,COALESCE(r.retired_at,r.cancelled_at,r.scheduled_at) AS event_at,
 r.exam_id,r.exam_sitting_id AS sitting_id,r.submission_id,
 (r.state='grace' OR (r.state='retired' AND CASE r.category WHEN 'work' THEN sub.work_retired_at IS NULL WHEN 'integrity' THEN sub.integrity_retired_at IS NULL WHEN 'browser_activity' THEN (SELECT browser_retired_at IS NULL FROM exam_attempt_delivery_budgets WHERE exam_attempt_id=sub.exam_attempt_id) ELSE (SELECT security_retired_at IS NULL FROM exam_attempt_delivery_budgets WHERE exam_attempt_id=sub.exam_attempt_id) END)) AS unfinished,
 false AS referenced,
 EXISTS(SELECT 1 FROM retention_holds h WHERE h.exam_id=r.exam_id AND h.released_at IS NULL AND
  (h.exam_sitting_id IS NULL OR h.exam_sitting_id=r.exam_sitting_id) AND (h.submission_id IS NULL OR h.submission_id=r.submission_id)) AS held,
 EXISTS(SELECT 1 FROM retention_purge_objects po WHERE po.retirement_id=r.id AND
  (NOT po.writer_finished OR po.absence_verified_at IS NULL OR po.last_absence_observed_at IS NULL OR po.verify_after>bounds.at)) AS purge_pending,
 EXISTS(SELECT 1 FROM retention_source_protections sp WHERE sp.submission_id=r.submission_id AND sp.category=r.category AND sp.expires_at>bounds.at) AS source_protected
 FROM retention_retirements r JOIN exam_submissions sub ON sub.id=r.submission_id CROSS JOIN bounds`

const expiryBounds = `WITH bounds AS (SELECT ?::timestamptz AS at,?::timestamptz AS cutoff)`

type retentionExpiryFactsRow struct {
	CleanupAudit    bool          `db:"cleanup_audit"`
	ID              string        `db:"id"`
	ScanAt          time.Time     `db:"scan_at"`
	EventAt         time.Time     `db:"event_at"`
	ExamID          string        `db:"exam_id"`
	SittingID       string        `db:"sitting_id"`
	SubmissionID    string        `db:"submission_id"`
	Unfinished      bool          `db:"unfinished"`
	Referenced      bool          `db:"referenced"`
	Held            bool          `db:"held"`
	PurgePending    bool          `db:"purge_pending"`
	SourceProtected bool          `db:"source_protected"`
	ScheduledAt     sql.NullTime  `db:"scheduled_at"`
	ExpiresAfter    sql.NullTime  `db:"expires_after"`
	PolicyRevision  sql.NullInt64 `db:"policy_revision"`
	ControlRevision sql.NullInt64 `db:"control_revision"`
}

func expiryFactsQuery(kind model.RetentionExpiryKind) string {
	if kind == model.RetentionExpiryAudit {
		return auditExpiryFacts
	}
	return receiptExpiryFacts
}

func expiryCutoff(policy *model.RetentionPolicy, at time.Time) time.Time {
	return at.Add(-time.Duration(policy.AuditRetentionDays) * 24 * time.Hour)
}

func (f retentionExpiryFactsRow) record(kind model.RetentionExpiryKind, policy *model.RetentionPolicy, at time.Time) (model.RetentionExpiryRecord, error) {
	r := model.RetentionExpiryRecord{Kind: kind, ID: f.ID, EventAt: model.TimeUTC(f.EventAt), ScheduledAt: optionalTime(f.ScheduledAt), ExpiresAfter: optionalTime(f.ExpiresAfter)}
	if policy.AuditRetentionDays > 0 {
		r.EligibleAt = model.OptionalTimeFrom(r.EventAt.Add(time.Duration(policy.AuditRetentionDays) * 24 * time.Hour))
	}
	switch {
	case f.Unfinished:
		r.Blocker = model.RetentionExpiryUnfinished
	case f.Held:
		r.Blocker = model.RetentionExpiryHeld
	case f.PurgePending:
		r.Blocker = model.RetentionExpiryPurgePending
	case f.Referenced:
		r.Blocker = model.RetentionExpiryReferenced
	case f.SourceProtected:
		r.Blocker = model.RetentionExpirySourceProtected
	case policy.AuditRetentionDays == 0:
		r.Blocker = model.RetentionExpiryUnconfigured
	case at.Before(r.EligibleAt.Time):
		r.Blocker = model.RetentionExpiryDeadline
	}
	return r, r.Validate()
}

func (s *SQLRetentionStore) ListExpiryRecords(ctx context.Context, options store.RetentionExpiryListOptions) (*store.RetentionExpiryPage, error) {
	if !options.Kind.IsValid() || (options.CleanupAudit && options.Kind != model.RetentionExpiryAudit) || options.Limit < 1 || options.Limit > model.RetentionMaximumPageSize || (options.AfterID != "" && !model.IsValidId(options.AfterID)) {
		return nil, store.NewErrInvalidInput("retention_expiry", "page", nil)
	}
	policy, err := getRetentionPolicy(ctx, s.GetMaster(), "")
	if err != nil {
		return nil, err
	}
	var at time.Time
	if err = s.GetMaster().Get(ctx, &at, `SELECT clock_timestamp()`); err != nil {
		return nil, err
	}
	at = model.TimeUTC(at)
	before := model.TimeUTC(options.Before)
	if before.IsZero() {
		before = at
	}
	if before.After(at) {
		return nil, store.NewErrInvalidInput("retention_expiry", "before", nil)
	}
	query := expiryBounds + `, facts AS (` + expiryFactsQuery(options.Kind) + `)
  SELECT f.*,s.scheduled_at,s.expires_after,s.policy_revision,s.control_revision FROM facts f
  LEFT JOIN retention_expiry_schedules s ON s.record_kind=? AND s.record_id=f.id
  WHERE f.id>? AND f.scan_at<=? AND f.cleanup_audit=? ORDER BY f.id LIMIT ?`
	var rows []retentionExpiryFactsRow
	if err = s.GetMaster().Select(ctx, &rows, query, at, expiryCutoff(policy, at), options.Kind, options.AfterID, before, options.CleanupAudit, options.Limit+1); err != nil {
		return nil, err
	}
	page := &store.RetentionExpiryPage{InstitutionID: policy.InstitutionID, Before: before, Items: make([]model.RetentionExpiryRecord, 0, min(len(rows), options.Limit)), HasMore: len(rows) > options.Limit}
	if page.HasMore {
		rows = rows[:options.Limit]
	}
	for _, row := range rows {
		record, err := row.record(options.Kind, policy, at)
		if err != nil {
			return nil, invalidPersistedState("retention_expiry", "record", err)
		}
		page.Items = append(page.Items, record)
	}
	return page, nil
}

func getExpiryFacts(ctx context.Context, tx *sqlxTxWrapper, kind model.RetentionExpiryKind, id string, policy *model.RetentionPolicy, at time.Time) (retentionExpiryFactsRow, error) {
	var row retentionExpiryFactsRow
	err := tx.Get(ctx, &row, expiryBounds+`, facts AS (`+expiryFactsQuery(kind)+`)
 SELECT f.*,s.scheduled_at,s.expires_after,s.policy_revision,s.control_revision FROM facts f
 LEFT JOIN retention_expiry_schedules s ON s.record_kind=? AND s.record_id=f.id WHERE f.id=?`, at, expiryCutoff(policy, at), kind, id)
	return row, err
}

func lockExpiryOwner(ctx context.Context, tx *sqlxTxWrapper, row retentionExpiryFactsRow) error {
	for _, item := range []struct{ query, id string }{
		{`SELECT id FROM exams WHERE id=? FOR UPDATE`, row.ExamID},
		{`SELECT id FROM exam_sittings WHERE id=? FOR UPDATE`, row.SittingID},
		{`SELECT id FROM exam_submissions WHERE id=? FOR UPDATE`, row.SubmissionID},
	} {
		if item.id != "" {
			var id string
			if err := tx.Get(ctx, &id, item.query, item.id); err != nil {
				return err
			}
		}
	}
	return nil
}

type retentionExpiryAuditResult struct {
	Kind            model.RetentionExpiryKind `json:"record_kind"`
	RecordID        string                    `json:"record_id"`
	PolicyRevision  int64                     `json:"policy_revision"`
	ControlRevision int64                     `json:"control_revision"`
	store.RetentionExpiryResult
}

func (s *SQLRetentionStore) ReconcileExpiry(ctx context.Context, input *store.RetentionExpiryReconciliation) (*store.RetentionExpiryResult, error) {
	if input == nil || !input.Kind.IsValid() || !model.IsValidId(input.RecordID) || !model.IsValidId(input.AuditEventID) || input.RecordID == input.AuditEventID || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("retention_expiry", "reconciliation", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "retention expiry", func(ctx context.Context, tx *sqlxTxWrapper) (*store.RetentionExpiryResult, error) {
		policy, err := getRetentionPolicy(ctx, tx, "FOR UPDATE OF p,i")
		if err != nil {
			return nil, err
		}
		control, err := getRetentionControl(ctx, tx, policy.InstitutionID, true)
		if err != nil {
			return nil, err
		}
		// The worker audit is validated before any effect. A committed unknown
		// outcome can be replayed without extending grace or repeating deletion.
		var audit struct {
			Status string `db:"status"`
			Result []byte `db:"result"`
		}
		err = tx.Get(ctx, &audit, `SELECT status,result FROM audit_events WHERE id=? AND action=? AND resource_type='institution' AND resource_id=? AND actor_id IS NULL AND session_id IS NULL FOR UPDATE`, input.AuditEventID, string(model.ActionRetentionCleanupManage), policy.InstitutionID.String())
		if err != nil {
			return nil, translateError("retention_expiry", "audit", err)
		}
		if audit.Status != "attempt" {
			var previous retentionExpiryAuditResult
			if audit.Status != "success" || json.Unmarshal(audit.Result, &previous) != nil || previous.Kind != input.Kind || previous.RecordID != input.RecordID || previous.PolicyRevision < 1 || previous.ControlRevision < 1 {
				return nil, store.NewErrConflict("retention_expiry", "audit", nil)
			}
			return &previous.RetentionExpiryResult, nil
		}
		var at time.Time
		if err = tx.Get(ctx, &at, `SELECT clock_timestamp()`); err != nil {
			return nil, err
		}
		row, err := getExpiryFacts(ctx, tx, input.Kind, input.RecordID, policy, at)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		result := &store.RetentionExpiryResult{}
		if err == nil {
			if row.CleanupAudit {
				return nil, store.NewErrConflict("retention_expiry", "cleanup_audit_requires_batch", nil)
			}
			if err = lockExpiryOwner(ctx, tx, row); err != nil {
				return nil, err
			}
			target := `SELECT id FROM audit_events WHERE id=? FOR UPDATE`
			if input.Kind == model.RetentionExpiryReceipt {
				target = `SELECT id FROM retention_retirements WHERE id=? FOR UPDATE`
			}
			var id string
			if err = tx.Get(ctx, &id, target, input.RecordID); err != nil {
				return nil, err
			}
			if err = tx.Get(ctx, &at, `SELECT clock_timestamp()`); err != nil {
				return nil, err
			}
			at = model.TimeUTC(at)
			row, err = getExpiryFacts(ctx, tx, input.Kind, input.RecordID, policy, at)
			if err != nil {
				return nil, err
			}
			record, err := row.record(input.Kind, policy, at)
			if err != nil {
				return nil, err
			}
			permitted := control.Permits(policy) && record.Blocker == model.RetentionExpiryEligible
			if row.ScheduledAt.Valid && (!permitted || row.PolicyRevision.Int64 != policy.Revision || row.ControlRevision.Int64 != control.Revision) {
				if _, err = tx.Exec(ctx, `DELETE FROM retention_expiry_schedules WHERE record_kind=? AND record_id=?`, input.Kind, input.RecordID); err != nil {
					return nil, err
				}
				result.Cancelled = true
				row.ScheduledAt.Valid = false
			}
			if permitted {
				if !row.ScheduledAt.Valid {
					_, err = tx.Exec(ctx, `INSERT INTO retention_expiry_schedules(record_kind,record_id,policy_revision,control_revision,scheduled_at,expires_after) VALUES(?,?,?,?,?,?)`, input.Kind, input.RecordID, policy.Revision, control.Revision, at, at.Add(time.Duration(policy.DeletionGraceDays)*24*time.Hour))
					if err != nil {
						return nil, err
					}
					result.Scheduled = true
				} else if !at.Before(row.ExpiresAfter.Time) {
					if err = expireRetentionRecord(ctx, tx, input.Kind, input.RecordID, policy, at); err != nil {
						return nil, err
					}
					result.Expired = true
				}
			}
		}
		// No content from the removed row is copied into the cleanup audit.
		encoded, err := json.Marshal(retentionExpiryAuditResult{Kind: input.Kind, RecordID: input.RecordID, PolicyRevision: policy.Revision, ControlRevision: control.Revision, RetentionExpiryResult: *result})
		if err != nil {
			return nil, err
		}
		if _, err = completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt); err != nil {
			return nil, err
		}
		return result, nil
	})
}

func expireRetentionRecord(ctx context.Context, tx *sqlxTxWrapper, kind model.RetentionExpiryKind, id string, policy *model.RetentionPolicy, at time.Time) error {
	if kind == model.RetentionExpiryAudit {
		if _, err := tx.Exec(ctx, `DELETE FROM administrator_recovery_records WHERE audit_event_id=? AND reconciled_at IS NOT NULL AND reconciled_at<=?`, id, expiryCutoff(policy, at)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, expiryBounds+` DELETE FROM retention_holds h USING bounds WHERE (h.creation_audit_event_id=? OR h.release_audit_event_id=?) AND `+releasedHoldExpiry, at, expiryCutoff(policy, at), id, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM audit_events WHERE id=? AND status<>'attempt'`, id); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(ctx, `DELETE FROM retention_purge_objects WHERE retirement_id=? AND writer_finished AND absence_verified_at IS NOT NULL AND last_absence_observed_at IS NOT NULL AND verify_after<=?`, id, at); err != nil {
			return err
		}
		// FK-owned notices have the same lifetime as their receipt. All immutable
		// Submission identities and category markers survive this history deletion.
		if _, err := tx.Exec(ctx, `DELETE FROM retention_retirements WHERE id=? AND state IN ('cancelled','retired')`, id); err != nil {
			return err
		}
	}
	_, err := tx.Exec(ctx, `DELETE FROM retention_expiry_schedules WHERE record_kind=? AND record_id=?`, kind, id)
	return err
}

func countRetentionExpiry(ctx context.Context, tx *sqlxTxWrapper, policy *model.RetentionPolicy, at time.Time) (model.RetentionExpiryCounts, model.RetentionExpiryCounts, error) {
	var result [2]model.RetentionExpiryCounts
	for i, kind := range []model.RetentionExpiryKind{model.RetentionExpiryAudit, model.RetentionExpiryReceipt} {
		// PostgreSQL counts the bounded scalar reasons; no application allocation
		// scales with audit volume and no audit JSON is fetched for a preview.
		query := expiryBounds + `, facts AS (` + expiryFactsQuery(kind) + `), classified AS (SELECT CASE
   WHEN unfinished THEN 'unfinished' WHEN held THEN 'held' WHEN purge_pending THEN 'purge_pending'
   WHEN referenced THEN 'referenced' WHEN source_protected THEN 'source_protected'
   WHEN ?=0 THEN 'unconfigured' WHEN event_at>? THEN 'deadline' ELSE 'eligible' END AS reason FROM facts)
   SELECT reason,count(*) AS count FROM classified GROUP BY reason`
		var rows []struct {
			Reason string `db:"reason"`
			Count  int64  `db:"count"`
		}
		if err := tx.Select(ctx, &rows, query, at, expiryCutoff(policy, at), policy.AuditRetentionDays, expiryCutoff(policy, at)); err != nil {
			return result[0], result[1], err
		}
		c := &result[i]
		for _, row := range rows {
			c.Total += row.Count
			switch row.Reason {
			case "unfinished":
				c.Unfinished += row.Count
			case "held":
				c.Held += row.Count
			case "purge_pending":
				c.PurgePending += row.Count
			case "referenced":
				c.Referenced += row.Count
			case "source_protected":
				c.SourceProtected += row.Count
			case "unconfigured":
				c.Unconfigured += row.Count
			case "deadline":
				c.AwaitingDeadline += row.Count
			case "eligible":
				c.Eligible += row.Count
			default:
				return result[0], result[1], errors.New("invalid retention expiry count")
			}
		}
		if err := c.Validate(); err != nil {
			return result[0], result[1], err
		}
	}
	return result[0], result[1], nil
}

// Called only while the owning Exam/Sitting is already locked by a hold or a
// newly accepted integrity event. It takes no policy lock in reverse order.
func cancelScopeExpirySchedules(ctx context.Context, tx *sqlxTxWrapper, scope model.RetentionHoldScope) error {
	_, err := tx.Exec(ctx, `DELETE FROM retention_expiry_schedules s WHERE
  (s.record_kind='receipt' AND EXISTS(SELECT 1 FROM retention_retirements r WHERE r.id=s.record_id AND r.exam_id=? AND (?='' OR r.exam_sitting_id=?) AND (?='' OR r.submission_id=?))) OR
  (s.record_kind='audit' AND EXISTS(SELECT 1 FROM audit_events a WHERE a.id=s.record_id AND
   ((a.resource_type='exam' AND a.resource_id=?) OR
    (a.resource_type='exam_sitting' AND a.resource_id IN (SELECT id FROM exam_sittings WHERE exam_id=? AND (?='' OR id=?))) OR
    (a.resource_type='submission' AND a.resource_id IN (SELECT sub.id FROM exam_submissions sub JOIN exam_attempts attempt ON attempt.id=sub.exam_attempt_id
      WHERE attempt.exam_id=? AND (?='' OR attempt.exam_sitting_id=?) AND (?='' OR sub.id=?))))))`,
		scope.ExamID.String(), scope.SittingID.String(), scope.SittingID.String(), scope.SubmissionID.String(), scope.SubmissionID.String(), scope.ExamID.String(), scope.ExamID.String(), scope.SittingID.String(), scope.SittingID.String(), scope.ExamID.String(), scope.SittingID.String(), scope.SittingID.String(), scope.SubmissionID.String(), scope.SubmissionID.String())
	return err
}
