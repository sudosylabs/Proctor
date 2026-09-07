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

func (s *SQLRetentionStore) ReconcileCleanupAuditExpiry(ctx context.Context, input *store.RetentionCleanupAuditReconciliation) (*store.RetentionCleanupAuditResult, error) {
	if input == nil || input.Limit < 1 || input.Limit > 100 || input.Before.IsZero() || (input.AfterID != "" && !model.IsValidId(input.AfterID)) || input.Audit == nil || !input.Audit.ID.IsZero() ||
		!input.Audit.ActorID.IsZero() || !input.Audit.SessionID.IsZero() || input.Audit.Action != string(model.ActionRetentionCleanupManage) || input.Audit.Status != model.AuditStatusAttempt ||
		input.Audit.Resource.Type != model.ResourceInstitution || input.Audit.ScopeType != model.RoleScopeInstitution || input.Audit.ScopeID != input.Audit.Resource.ID || len(input.Audit.Result) > 0 || len(input.Audit.PriorState) > 0 {
		return nil, store.NewErrInvalidInput("retention_expiry", "cleanup_batch", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "cleanup audit retention", func(ctx context.Context, tx *sqlxTxWrapper) (*store.RetentionCleanupAuditResult, error) {
		policy, err := getRetentionPolicy(ctx, tx, "FOR UPDATE OF p,i")
		if err != nil {
			return nil, err
		}
		control, err := getRetentionControl(ctx, tx, policy.InstitutionID, true)
		if err != nil {
			return nil, err
		}
		if input.Audit.Resource.ID != policy.InstitutionID.String() {
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
		var ids []string
		if err = tx.Select(ctx, &ids, `SELECT a.id FROM audit_events a WHERE `+terminalCleanupAudit+` AND a.id>? AND a.created_at<=? ORDER BY a.id LIMIT ?`, input.AfterID, input.Before, input.Limit+1); err != nil {
			return nil, err
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
				return nil, err
			}
			if err = lockExpiryOwner(ctx, tx, row); err != nil {
				return nil, err
			}
			var locked string
			if err = tx.Get(ctx, &locked, `SELECT id FROM audit_events WHERE id=? FOR UPDATE`, id); err != nil {
				return nil, err
			}
		}
		if err = tx.Get(ctx, &at, `SELECT clock_timestamp()`); err != nil {
			return nil, err
		}
		at = model.TimeUTC(at)
		type plannedChange struct {
			id                       string
			cancel, schedule, expire bool
		}
		changes := make([]plannedChange, 0, len(ids))
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
			result.AfterID = id
			permitted := control.Permits(policy) && record.Blocker == model.RetentionExpiryEligible
			change := plannedChange{id: id}
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
		// Reserving the critical attempt inside this named transaction avoids
		// self-generating no-op history for every protected/future row scanned.
		result.Changed = len(changes)
		if len(changes) == 0 {
			return result, nil
		}
		audit, err := insertAuditEventAt(ctx, tx, input.Audit, at)
		if err != nil {
			return nil, err
		}
		for _, change := range changes {
			if change.cancel {
				if _, err = tx.Exec(ctx, `DELETE FROM retention_expiry_schedules WHERE record_kind='audit' AND record_id=?`, change.id); err != nil {
					return nil, err
				}
				result.Cancelled++
			}
			if change.schedule {
				if _, err = tx.Exec(ctx, `INSERT INTO retention_expiry_schedules(record_kind,record_id,policy_revision,control_revision,scheduled_at,expires_after) VALUES('audit',?,?,?,?,?)`, change.id, policy.Revision, control.Revision, at, at.Add(time.Duration(policy.DeletionGraceDays)*24*time.Hour)); err != nil {
					return nil, err
				}
				result.Scheduled++
			}
			if change.expire {
				if err = expireRetentionRecord(ctx, tx, model.RetentionExpiryAudit, change.id, policy, at); err != nil {
					return nil, err
				}
				result.Expired++
			}
		}
		encoded, err := model.EncodeAuditData(map[string]any{"operation": "retention.expire_cleanup_history", "policy_revision": policy.Revision, "control_revision": control.Revision, "examined": result.Examined, "scheduled": result.Scheduled, "cancelled": result.Cancelled, "expired": result.Expired})
		if err != nil {
			return nil, err
		}
		if _, err = completeAuditEvent(ctx, tx, audit.ID.String(), model.AuditStatusSuccess, "", encoded, model.MillisFromTime(at)); err != nil {
			return nil, err
		}
		return result, nil
	})
}
