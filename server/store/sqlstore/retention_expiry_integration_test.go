//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/storetest"
)

func TestRetentionExpiryStore(t *testing.T) {
	s := openTestStore(t)
	resetPristineTestStore(t, s)
	storetest.TestRetentionExpiryStore(t, s, retentionExpirySQLProbe(s))
}
func TestRetentionReceiptExpiryStore(t *testing.T) {
	s := openTestStore(t)
	resetTestStore(t, s)
	storetest.TestRetentionReceiptExpiryStore(t, s, retentionSQLProbe(s), retentionExpirySQLProbe(s))
}

func retentionExpirySQLProbe(s *SQLStore) storetest.RetentionExpirySQLProbe {
	return storetest.RetentionExpirySQLProbe{
		DatabaseTime: func(t *testing.T, ctx context.Context) time.Time {
			t.Helper()
			var at time.Time
			if err := s.GetMaster().Get(ctx, &at, `SELECT clock_timestamp()`); err != nil {
				t.Fatal(err)
			}
			return model.TimeUTC(at)
		},
		AgeCleanupHistory: func(t *testing.T, ctx context.Context) {
			t.Helper()
			if _, err := s.GetMaster().Exec(ctx, `UPDATE audit_events a SET created_at=clock_timestamp()-interval '100 days',updated_at=clock_timestamp()-interval '99 days' WHERE `+terminalCleanupAudit); err != nil {
				t.Fatal(err)
			}
		},
		ExpireCleanupGrace: func(t *testing.T, ctx context.Context) {
			t.Helper()
			if _, err := s.GetMaster().Exec(ctx, `UPDATE retention_expiry_schedules SET scheduled_at=clock_timestamp()-interval '2 days',expires_after=clock_timestamp()-interval '1 day' WHERE record_kind='audit' AND record_id IN (SELECT a.id FROM audit_events a WHERE `+terminalCleanupAudit+`)`); err != nil {
				t.Fatal(err)
			}
		},
		CleanupAuditCount: func(t *testing.T, ctx context.Context) int {
			t.Helper()
			var count int
			if err := s.GetMaster().Get(ctx, &count, `SELECT count(*) FROM audit_events a WHERE `+terminalCleanupAudit); err != nil {
				t.Fatal(err)
			}
			return count
		},

		ConcurrentPeer: newSQLRetentionStore(s),
		AgeAudit: func(t *testing.T, ctx context.Context, id string) {
			t.Helper()
			_, err := s.GetMaster().Exec(ctx, `UPDATE audit_events SET created_at=clock_timestamp()-interval '100 days',updated_at=clock_timestamp()-interval '99 days' WHERE id=?`, id)
			if err != nil {
				t.Fatal(err)
			}
		},
		ExpireGrace: func(t *testing.T, ctx context.Context, kind model.RetentionExpiryKind, id string) {
			t.Helper()
			_, err := s.GetMaster().Exec(ctx, `UPDATE retention_expiry_schedules SET scheduled_at=clock_timestamp()-interval '2 days',expires_after=clock_timestamp()-interval '1 day' WHERE record_kind=? AND record_id=?`, kind, id)
			if err != nil {
				t.Fatal(err)
			}
		},
		PinAudit: func(t *testing.T, ctx context.Context, id string, user model.UserID) func() {
			t.Helper()
			_, err := s.GetMaster().Exec(ctx, `INSERT INTO command_outcomes(user_id,operation,key_digest,fingerprint_version,fingerprint,outcome_version,outcome,original_audit_event_id,created_at,expires_at)
    VALUES(?,'retention.expiry.reference',decode(repeat('01',32),'hex'),1,decode(repeat('02',32),'hex'),1,'{}',?,clock_timestamp(),clock_timestamp()+interval '1 day')`, user.String(), id)
			if err != nil {
				t.Fatal(err)
			}
			return func() {
				if _, err := s.GetMaster().Exec(ctx, `DELETE FROM command_outcomes WHERE operation='retention.expiry.reference' AND original_audit_event_id=?`, id); err != nil {
					t.Fatal(err)
				}
			}
		},
		RejectCompletion: func(t *testing.T, ctx context.Context, id string) func() {
			t.Helper()
			if !model.IsValidId(id) {
				t.Fatal("invalid audit fixture identity")
			}
			_, err := s.GetMaster().Exec(ctx, `ALTER TABLE audit_events ADD CONSTRAINT test_reject_expiry_completion CHECK (id<>'`+id+`' OR status<>'success') NOT VALID`)
			if err != nil {
				t.Fatal(err)
			}
			return func() {
				if _, err := s.GetMaster().Exec(ctx, `ALTER TABLE audit_events DROP CONSTRAINT test_reject_expiry_completion`); err != nil {
					t.Fatal(err)
				}
			}
		},
		AgeReceipt: func(t *testing.T, ctx context.Context, id model.RetentionRetirementID) {
			t.Helper()
			_, err := s.GetMaster().Exec(ctx, `UPDATE retention_retirements SET scheduled_at=clock_timestamp()-interval '103 days',retire_after=clock_timestamp()-interval '102 days',retired_at=CASE WHEN state='retired' THEN clock_timestamp()-interval '101 days' END,cancelled_at=CASE WHEN state='cancelled' THEN clock_timestamp()-interval '101 days' END WHERE id=?`, id.String())
			if err != nil {
				t.Fatal(err)
			}
		},
		FinishReceiptPurge: func(t *testing.T, ctx context.Context, id model.RetentionRetirementID) {
			t.Helper()
			_, err := s.GetMaster().Exec(ctx, `UPDATE retention_purge_objects SET writer_finished=true,last_absence_observed_at=clock_timestamp()-interval '1 hour',absence_verified_at=clock_timestamp()-interval '1 hour',verify_after=clock_timestamp()-interval '1 minute' WHERE retirement_id=?`, id.String())
			if err != nil {
				t.Fatal(err)
			}
		},
		AssertMarkers: func(t *testing.T, ctx context.Context, id model.SubmissionID) {
			t.Helper()
			var retired bool
			err := s.GetMaster().Get(ctx, &retired, `SELECT work_retired_at IS NOT NULL AND integrity_retired_at IS NOT NULL FROM exam_submissions WHERE id=?`, id.String())
			if err != nil || !retired {
				t.Fatalf("permanent markers missing: %v", err)
			}
		},
	}
}

func TestRetentionExpiryForeignKeyInventory(t *testing.T) {
	s := openTestStore(t)
	var actual []string
	err := s.GetMaster().Select(context.Background(), &actual, `SELECT c.conrelid::regclass::text||'.'||a.attname FROM pg_constraint c JOIN pg_attribute a ON a.attrelid=c.conrelid AND a.attnum=ANY(c.conkey) WHERE c.contype='f' AND c.confrelid='audit_events'::regclass`)
	if err != nil {
		t.Fatal(err)
	}
	expected := []string{"external_login_states.audit_event_id", "exam_attempt_manager_end_actions.audit_event_id", "exam_sitting_private_actions.audit_event_id", "exam_sitting_live_corrections.audit_event_id", "exam_attempt_correction_acknowledgements.audit_event_id", "command_outcomes.original_audit_event_id", "exam_starter_workspace_objects.retired_by_audit_event_id", "exam_attempt_workspace_objects.retired_by_audit_event_id", "administrator_recovery_records.audit_event_id", "submission_review_waivers.audit_event_id", "retention_holds.creation_audit_event_id", "retention_holds.release_audit_event_id"}
	sort.Strings(actual)
	sort.Strings(expected)
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("audit reference inventory changed: actual=%v expected=%v; update retention protection and grace cancellation", actual, expected)
	}
	var triggers int
	err = s.GetMaster().Get(context.Background(), &triggers, `SELECT count(*) FROM pg_trigger WHERE tgfoid='cancel_referenced_audit_expiry'::regproc AND NOT tgisinternal`)
	if err != nil || triggers != len(expected) {
		t.Fatalf("audit reference cancellation triggers=%d error=%v", triggers, err)
	}
}

func TestRetentionExpiryCompletedHistoryDoesNotPinAuditForever(t *testing.T) {
	s := openTestStore(t)
	resetTestStore(t, s)
	storetest.TestRetentionReceiptExpiryStore(t, s, retentionSQLProbe(s), retentionExpirySQLProbe(s))
	ctx := context.Background()
	probe := retentionExpirySQLProbe(s)
	policy, err := s.RetentionPolicy().Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var hold struct {
		ID         string `db:"id"`
		CreationID string `db:"creation_audit_event_id"`
		ReleaseID  string `db:"release_audit_event_id"`
		UserID     string `db:"created_by_user_id"`
	}
	if err = s.GetMaster().Get(ctx, &hold, `SELECT id,creation_audit_event_id,release_audit_event_id,created_by_user_id FROM retention_holds WHERE released_at IS NOT NULL LIMIT 1`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{hold.CreationID, hold.ReleaseID} {
		probe.AgeAudit(t, ctx, id)
	}
	if _, err = s.GetMaster().Exec(ctx, `DELETE FROM command_outcomes WHERE original_audit_event_id IN (?,?)`, hold.CreationID, hold.ReleaseID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetMaster().Exec(ctx, `UPDATE retention_holds SET created_at=clock_timestamp()-interval '100 days',released_at=clock_timestamp()-interval '99 days' WHERE id=?`, hold.ID); err != nil {
		t.Fatal(err)
	}
	reconcile := func(id string, expected store.RetentionExpiryResult) {
		a, err := s.Audit().Save(ctx, &model.AuditEvent{Action: string(model.ActionRetentionCleanupManage), Resource: model.Resource{Type: model.ResourceInstitution, ID: policy.InstitutionID.String()}, ScopeType: model.RoleScopeInstitution, ScopeID: policy.InstitutionID.String(), Status: model.AuditStatusAttempt, NodeID: "expiry-history-test"})
		if err != nil {
			t.Fatal(err)
		}
		r, err := s.Retention().ReconcileExpiry(ctx, &store.RetentionExpiryReconciliation{Kind: model.RetentionExpiryAudit, RecordID: id, AuditEventID: a.ID.String(), AuditAt: model.GetMillis()})
		if err != nil || r == nil || *r != expected {
			t.Fatalf("history expiry=%#v expected=%#v error=%v", r, expected, err)
		}
	}
	reconcile(hold.CreationID, store.RetentionExpiryResult{Scheduled: true})
	probe.ExpireGrace(t, ctx, model.RetentionExpiryAudit, hold.CreationID)
	reconcile(hold.CreationID, store.RetentionExpiryResult{Expired: true})
	var count int
	if err = s.GetMaster().Get(ctx, &count, `SELECT count(*) FROM retention_holds WHERE id=?`, hold.ID); err != nil || count != 0 {
		t.Fatalf("released history retained count=%d error=%v", count, err)
	}
	reconcile(hold.ReleaseID, store.RetentionExpiryResult{Scheduled: true})
	probe.ExpireGrace(t, ctx, model.RetentionExpiryAudit, hold.ReleaseID)
	reconcile(hold.ReleaseID, store.RetentionExpiryResult{Expired: true})
	// Offline recovery reconciliation is a terminal proof, not a permanent
	// reason to retain the detailed audit. Its own reconciliation time matters.
	a, err := s.Audit().Save(ctx, &model.AuditEvent{Action: "authentication.administrator_recovery", Resource: model.Resource{Type: model.ResourceUser, ID: hold.UserID}, ScopeType: model.RoleScopeInstitution, ScopeID: policy.InstitutionID.String(), Status: model.AuditStatusSuccess, NodeID: "expiry-history-test"})
	if err != nil {
		t.Fatal(err)
	}
	probe.AgeAudit(t, ctx, a.ID.String())
	recordID := model.NewId()
	if _, err = s.GetMaster().Exec(ctx, `INSERT INTO administrator_recovery_records(id,created_at,institution_id,user_id,local_login_enabled,password_rotated,reconciled_at,audit_event_id) VALUES(?,clock_timestamp()-interval '100 days',?,?,false,true,clock_timestamp(),?)`, recordID, policy.InstitutionID.String(), hold.UserID, a.ID.String()); err != nil {
		t.Fatal(err)
	}
	reconcile(a.ID.String(), store.RetentionExpiryResult{})
	if _, err = s.GetMaster().Exec(ctx, `UPDATE administrator_recovery_records SET reconciled_at=clock_timestamp()-interval '98 days' WHERE id=?`, recordID); err != nil {
		t.Fatal(err)
	}
	reconcile(a.ID.String(), store.RetentionExpiryResult{Scheduled: true})
	probe.ExpireGrace(t, ctx, model.RetentionExpiryAudit, a.ID.String())
	reconcile(a.ID.String(), store.RetentionExpiryResult{Expired: true})
	if err = s.GetMaster().Get(ctx, &count, `SELECT count(*) FROM administrator_recovery_records WHERE id=?`, recordID); err != nil || count != 0 {
		t.Fatalf("completed recovery evidence pinned audit count=%d error=%v", count, err)
	}
	if _, err = s.Audit().Get(ctx, a.ID.String()); !store.IsNotFound(err) {
		t.Fatalf("completed recovery audit retained: %v", err)
	}
}

func TestRetentionCleanupAuditExpiryStore(t *testing.T) {
	s := openTestStore(t)
	resetPristineTestStore(t, s)
	storetest.TestRetentionCleanupAuditExpiryStore(t, s, retentionExpirySQLProbe(s))
}
func TestRetentionAuditUnfinishedExamScope(t *testing.T) {
	s := openTestStore(t)
	resetTestStore(t, s)
	storetest.TestRetentionAuditUnfinishedExamScope(t, s, retentionExpirySQLProbe(s))
}
