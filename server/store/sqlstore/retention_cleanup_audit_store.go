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
	"github.com/sudosylabs/proctor/server/store"
)

// ReconcileCleanupAuditExpiry schedules, cancels or expires the next bounded
// page of terminal retention cleanup audit records under the current policy
// and control, recording one audit event when anything changes.
func (s *SQLRetentionStore) ReconcileCleanupAuditExpiry(ctx context.Context, input *store.RetentionCleanupAuditReconciliation) (*store.RetentionCleanupAuditResult, error) {
	if input == nil || input.Limit < 1 || input.Limit > 100 || input.Before.IsZero() || (input.AfterID != "" && !input.AfterID.IsValid()) || input.AuditEvent == nil || !input.AuditEvent.ID.IsZero() ||
		!input.AuditEvent.ActorID.IsZero() || !input.AuditEvent.SessionID.IsZero() || input.AuditEvent.Action != string(model.ActionRetentionCleanupManage) || input.AuditEvent.Status != model.AuditStatusAttempt ||
		input.AuditEvent.Resource.Type != model.ResourceInstitution || input.AuditEvent.ScopeType != model.RoleScopeInstitution || input.AuditEvent.ScopeID != input.AuditEvent.Resource.ID || len(input.AuditEvent.Result) > 0 || len(input.AuditEvent.PriorState) > 0 {
		return nil, store.NewErrInvalidInput("retention_expiry", "cleanup_batch", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "cleanup audit retention", func(ctx context.Context, tx *sqlxTxWrapper) (*store.RetentionCleanupAuditResult, error) {
		return reconcileCleanupAuditExpiry(ctx, tx, input)
	})
}

// cleanupAuditChange is the schedule change one cleanup audit record needs.
type cleanupAuditChange struct {
	id                       string
	cancel, schedule, expire bool
}

// reconcileCleanupAuditExpiry locks the retention fence and the page of
// cleanup audit records, plans each record's change at PostgreSQL time, and
// applies the changes under one audit event.
func reconcileCleanupAuditExpiry(ctx context.Context, tx *sqlxTxWrapper, input *store.RetentionCleanupAuditReconciliation) (*store.RetentionCleanupAuditResult, error) {
	policy, err := getRetentionPolicy(ctx, tx, "FOR UPDATE OF p,i")
	if err != nil {
		return nil, err
	}
	control, err := getRetentionControl(ctx, tx, policy.InstitutionID, true)
	if err != nil {
		return nil, err
	}
	if input.AuditEvent.Resource.ID != policy.InstitutionID.String() {
		return nil, store.NewErrInvalidInput("retention_expiry", "institution", nil)
	}
	var at time.Time
	if err = tx.Get(ctx, &at, `SELECT clock_timestamp()`); err != nil {
		return nil, err
	}
	at = model.TimeUTC(at)
	if input.Before.After(at) {
		return nil, store.NewErrInvalidInput("retention_expiry", "before", nil)
	}
	ids, result, err := lockCleanupAuditPage(ctx, tx, input, policy, at)
	if err != nil {
		return nil, err
	}
	if err = tx.Get(ctx, &at, `SELECT clock_timestamp()`); err != nil {
		return nil, err
	}
	at = model.TimeUTC(at)
	changes, err := planCleanupAuditChanges(ctx, tx, ids, policy, control, at, result)
	if err != nil {
		return nil, err
	}
	// Reserving the critical attempt inside this named transaction avoids
	// self-generating no-op history for every protected/future row scanned.
	result.Changed = len(changes)
	if len(changes) == 0 {
		return result, nil
	}
	audit, err := insertAuditEventAt(ctx, tx, input.AuditEvent, at)
	if err != nil {
		return nil, err
	}
	if err = applyCleanupAuditChanges(ctx, tx, changes, policy, control, at, result); err != nil {
		return nil, err
	}
	encoded, err := model.EncodeAuditData(map[string]any{"operation": "retention.expire_cleanup_history", "policy_revision": policy.Revision, "control_revision": control.Revision, "examined": result.Examined, "scheduled": result.Scheduled, "canceled": result.Canceled, "expired": result.Expired})
	if err != nil {
		return nil, err
	}
	if _, err = completeAuditEvent(ctx, tx, audit.ID, model.AuditStatusSuccess, "", encoded, at); err != nil {
		return nil, err
	}
	return result, nil
}

// lockCleanupAuditPage selects the next page of terminal cleanup audit
// records and locks each record's examination owner, then the record.
func lockCleanupAuditPage(ctx context.Context, tx *sqlxTxWrapper, input *store.RetentionCleanupAuditReconciliation, policy *model.RetentionPolicy, at time.Time) ([]string, *store.RetentionCleanupAuditResult, error) {
	var ids []string
	if err := tx.Select(ctx, &ids, `SELECT a.id FROM audit_events a WHERE `+terminalCleanupAudit+` AND a.id>? AND a.created_at<=? ORDER BY a.id LIMIT ?`, input.AfterID, input.Before, input.Limit+1); err != nil {
		return nil, nil, err
	}
	result := &store.RetentionCleanupAuditResult{Before: input.Before, AfterID: input.AfterID, HasMore: len(ids) > input.Limit}
	if result.HasMore {
		ids = ids[:input.Limit]
	}
	// All targets share the policy/control fence. Resolve each examination
	// owner before locking its audit so holds and late records cannot race a
	// final expiry. No record's audit JSON crosses this bounded operation.
	for _, id := range ids {
		row, err := getExpiryFacts(ctx, tx, model.RetentionExpiryAudit, id, policy, at)
		if err != nil {
			return nil, nil, err
		}
		if err = lockExpiryOwner(ctx, tx, row); err != nil {
			return nil, nil, err
		}
		var locked string
		if err = tx.Get(ctx, &locked, `SELECT id FROM audit_events WHERE id=? FOR UPDATE`, id); err != nil {
			return nil, nil, err
		}
	}
	return ids, result, nil
}

// planCleanupAuditChanges examines each locked record and plans whether its
// expiry schedule is canceled, created or carried out.
func planCleanupAuditChanges(ctx context.Context, tx *sqlxTxWrapper, ids []string, policy *model.RetentionPolicy, control *model.RetentionControl, at time.Time, result *store.RetentionCleanupAuditResult) ([]cleanupAuditChange, error) {
	changes := make([]cleanupAuditChange, 0, len(ids))
	for _, id := range ids {
		row, err := getExpiryFacts(ctx, tx, model.RetentionExpiryAudit, id, policy, at)
		if err != nil {
			return nil, err
		}
		record, err := row.record(model.RetentionExpiryAudit, policy, at)
		if err != nil {
			return nil, err
		}
		result.Examined++
		result.AfterID = model.AuditEventID(id)
		permitted := control.Permits(policy) && record.Blocker == model.RetentionExpiryEligible
		change := cleanupAuditChange{id: id}
		if row.ScheduledAt.Valid && (!permitted || row.PolicyRevision.Int64 != policy.Revision || row.ControlRevision.Int64 != control.Revision) {
			change.cancel = true
			row.ScheduledAt.Valid = false
		}
		if permitted {
			if !row.ScheduledAt.Valid {
				change.schedule = true
			} else if !at.Before(row.ExpiresAfter.Time) {
				change.expire = true
			}
		}
		if change.cancel || change.schedule || change.expire {
			changes = append(changes, change)
		}
	}
	return changes, nil
}

// applyCleanupAuditChanges cancels, creates or carries out each planned
// expiry schedule and counts the changes.
func applyCleanupAuditChanges(ctx context.Context, tx *sqlxTxWrapper, changes []cleanupAuditChange, policy *model.RetentionPolicy, control *model.RetentionControl, at time.Time, result *store.RetentionCleanupAuditResult) error {
	for _, change := range changes {
		if change.cancel {
			if _, err := tx.Exec(ctx, `DELETE FROM retention_expiry_schedules WHERE record_kind='audit' AND record_id=?`, change.id); err != nil {
				return err
			}
			result.Canceled++
		}
		if change.schedule {
			if _, err := tx.Exec(ctx, `INSERT INTO retention_expiry_schedules(record_kind,record_id,policy_revision,control_revision,scheduled_at,expires_after) VALUES('audit',?,?,?,?,?)`, change.id, policy.Revision, control.Revision, at, at.Add(time.Duration(policy.DeletionGraceDays)*24*time.Hour)); err != nil {
				return err
			}
			result.Scheduled++
		}
		if change.expire {
			if err := expireRetentionRecord(ctx, tx, model.RetentionExpiryAudit, change.id, policy, at); err != nil {
				return err
			}
			result.Expired++
		}
	}
	return nil
}
