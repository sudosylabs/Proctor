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
	"crypto/sha256"
	"errors"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestRetentionPolicyCurrentCredentialAndAssurancePreserveGrace(t *testing.T) {
	for _, state := range []struct {
		name  string
		query string
	}{
		{"revoked credential", `UPDATE session_credentials SET revoked_at=clock_timestamp() WHERE id=?`},
		{"expired credential", `UPDATE session_credentials SET expires_at=clock_timestamp()-INTERVAL '1 microsecond' WHERE id=?`},
		{"expired assurance", `UPDATE sessions SET authenticated_at=authenticated_at-INTERVAL '2 hours',
			mfa_completed_at=mfa_completed_at-INTERVAL '2 hours',reauthenticated_at=NULL WHERE id=?`},
		{"single factor", `UPDATE sessions SET authentication_strength='single_factor',mfa_completed_at=NULL WHERE id=?`},
	} {
		t.Run(state.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			f := mfaRecoveryFixture(t, ctx)
			s := f.persistence
			institution, err := s.Institution().GetSingleton(ctx)
			if err != nil {
				t.Fatal(err)
			}
			mutation := func(action model.Action) store.RetentionMutation {
				audit, err := s.Audit().Save(ctx, &model.AuditEvent{ActorID: f.principal.UserID, SessionID: f.principal.SessionID,
					Action: string(action), Resource: model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()},
					ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(), Status: model.AuditStatusAttempt, NodeID: "policy-test"})
				if err != nil {
					t.Fatal(err)
				}
				return store.RetentionMutation{Principal: f.principal, RecentAuthenticationTTL: time.Hour,
					AuditEventID: audit.ID.String(), AuditAt: model.MillisFromTime(audit.CreatedAt)}
			}
			command := func(operation, key string) *store.CommandIdempotency {
				return &store.CommandIdempotency{UserID: f.principal.UserID, Operation: operation,
					KeyDigest: sha256.Sum256([]byte(key)), FingerprintVersion: 1, Fingerprint: sha256.Sum256([]byte(key)),
					OutcomeVersion: 1, Retention: time.Hour, Wait: time.Second}
			}
			original := &store.RetentionPolicyReplacement{RetentionMutation: mutation(model.ActionRetentionPolicyManage), ExpectedRevision: 1,
				Settings: model.RetentionPolicySettings{AuditRetentionDays: 1, ExportRetentionDays: 1, DeletionGraceDays: 1}}
			originalCommand := command("retention_policy.replace.v1", "original")
			first, err := s.RetentionPolicy().Replace(ctx, original, originalCommand)
			if err != nil {
				t.Fatal(err)
			}
			preview, err := s.Retention().CreatePreview(ctx, &store.RetentionPreviewCreation{RetentionMutation: mutation(model.ActionRetentionPolicyView),
				PreviewID: model.NewRetentionPreviewID(), ExpectedPolicyRevision: first.Policy.Revision}, command(store.RetentionPreviewOperation, "preview"))
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.Retention().ChangeControl(ctx, &store.RetentionControlChange{RetentionMutation: mutation(model.ActionRetentionCleanupManage),
				ExpectedRevision: 1, ExpectedPolicyRevision: first.Policy.Revision, State: model.RetentionControlEnabled, PreviewID: preview.ID},
				command(store.RetentionControlOperation, "enable"))
			if err != nil {
				t.Fatal(err)
			}
			before, err := s.RetentionPolicy().Get(ctx)
			if err != nil {
				t.Fatal(err)
			}
			control, err := s.Retention().GetControl(ctx)
			if err != nil {
				t.Fatal(err)
			}
			// Age a completed audit, then use the real scheduling operation.
			// Rejected writes must neither cancel grace nor pause live approval.
			target := mutation(model.ActionAuditView)
			scheduledID := target.AuditEventID
			if _, err = s.Audit().Complete(ctx, scheduledID, model.AuditStatusSuccess, "", nil, model.GetMillis()); err != nil {
				t.Fatal(err)
			}
			if _, err = s.GetMaster().Exec(ctx, `UPDATE audit_events SET created_at=clock_timestamp()-INTERVAL '3 days',
				updated_at=clock_timestamp()-INTERVAL '2 days' WHERE id=?`, scheduledID); err != nil {
				t.Fatal(err)
			}
			cleanupAudit, err := s.Audit().Save(ctx, &model.AuditEvent{Action: string(model.ActionRetentionCleanupManage),
				Resource: model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}, ScopeType: model.RoleScopeInstitution,
				ScopeID: institution.ID.String(), Status: model.AuditStatusAttempt, NodeID: "policy-test"})
			if err != nil {
				t.Fatal(err)
			}
			scheduled, err := s.Retention().ReconcileExpiry(ctx, &store.RetentionExpiryReconciliation{Kind: model.RetentionExpiryAudit,
				RecordID: scheduledID, AuditEventID: cleanupAudit.ID.String(), AuditAt: model.MillisFromTime(cleanupAudit.CreatedAt)})
			if err != nil || !scheduled.Scheduled {
				t.Fatalf("audit expiry was not scheduled: %#v, %v", scheduled, err)
			}
			defer func() {
				_, _ = s.GetMaster().Exec(context.Background(), `DELETE FROM retention_expiry_schedules WHERE record_id=?`, scheduledID)
			}()
			var expiresAfter time.Time
			err = s.GetMaster().Get(ctx, &expiresAfter, `SELECT expires_after FROM retention_expiry_schedules WHERE record_id=?`, scheduledID)
			if err != nil {
				t.Fatal(err)
			}
			id := f.principal.SessionID.String()
			if state.name == "revoked credential" || state.name == "expired credential" {
				id = f.principal.CredentialID.String()
			}
			if _, err = s.GetMaster().Exec(ctx, state.query, id); err != nil {
				t.Fatal(err)
			}
			for _, operation := range []string{"change", "no-op", "replay"} {
				t.Run(operation, func(t *testing.T) {
					input := *original
					input.RetentionMutation = mutation(model.ActionRetentionPolicyManage)
					input.ExpectedRevision, input.Settings = before.Revision, before.RetentionPolicySettings
					key := command("retention_policy.replace.v1", operation)
					if operation == "change" {
						input.Settings.DeletionGraceDays++
					} else if operation == "replay" {
						input.ExpectedRevision, input.Settings, key = original.ExpectedRevision, original.Settings, originalCommand
					}
					result, err := s.RetentionPolicy().Replace(ctx, &input, key)
					var conflict *store.ErrConflict
					if result != nil || !errors.As(err, &conflict) || conflict.Resource != "authorization" {
						t.Fatalf("stale request admitted: result=%#v error=%v", result, err)
					}
					after, err := s.RetentionPolicy().Get(ctx)
					if err != nil || !reflect.DeepEqual(after, before) {
						t.Fatalf("rejected write changed policy: %#v, %v", after, err)
					}
					afterControl, err := s.Retention().GetControl(ctx)
					if err != nil || !reflect.DeepEqual(afterControl, control) {
						t.Fatalf("rejected write changed approval: %#v, %v", afterControl, err)
					}
					var afterExpiry time.Time
					if err = s.GetMaster().Get(ctx, &afterExpiry, `SELECT expires_after FROM retention_expiry_schedules WHERE record_id=?`, scheduledID); err != nil || !afterExpiry.Equal(expiresAfter) {
						t.Fatalf("rejected write reset grace: expiry=%s error=%v", afterExpiry, err)
					}
					audit, err := s.Audit().Get(ctx, input.AuditEventID)
					if err != nil || audit.Status != model.AuditStatusAttempt {
						t.Fatalf("rejected write completed audit: %#v, %v", audit, err)
					}
					found, err := s.CommandOutcome().Has(ctx, key)
					if err != nil || found != (operation == "replay") {
						t.Fatalf("rejected write changed retained outcome: found=%v error=%v", found, err)
					}
				})
			}
		})
	}
}

func TestRetentionPolicyExportCeilingDatabaseConstraint(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s := openTestStore(t)
	resetTestStore(t, s)
	institution, err := s.Institution().Save(ctx, &model.Institution{Name: "export-ceiling", DisplayName: "Export Ceiling"})
	if err != nil {
		t.Fatal(err)
	}
	lastAccepted := 0
	for _, days := range []int{0, 1, 7, -1, 8, 36500} {
		t.Run(strconv.Itoa(days), func(t *testing.T) {
			_, err := s.GetMaster().Exec(ctx, `UPDATE retention_policies SET export_retention_days=?,
				submission_retention_days=36500,integrity_retention_days=36500,audit_retention_days=36500,deletion_grace_days=36500 WHERE institution_id=?`, days, institution.ID.String())
			if days >= 0 && days <= 7 {
				if err != nil {
					t.Fatal(err)
				}
				lastAccepted = days
			} else {
				var constraint *pq.Error
				if !errors.As(err, &constraint) || constraint.Code != "23514" || constraint.Constraint != "retention_policies_export_retention_days_check" {
					t.Fatalf("out-of-range export period was not rejected by its CHECK: %v", err)
				}
			}
			policy, err := s.RetentionPolicy().Get(ctx)
			if err != nil || policy.ExportRetentionDays != lastAccepted || policy.SubmissionRetentionDays != 36500 ||
				policy.IntegrityRetentionDays != 36500 || policy.AuditRetentionDays != 36500 || policy.DeletionGraceDays != 36500 {
				t.Fatalf("persisted bounds disagree: %#v, %v", policy, err)
			}
		})
	}
}

func TestRetentionPolicyReplacementKeepsMonotonicMetadata(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "retention-clock", DisplayName: "Retention Clock Institution"})
	if err != nil {
		t.Fatal(err)
	}
	actor := saveIntegrationUser(t, ctx, persistence, &model.User{Username: "retention-administrator",
		Email: "retention-administrator@example.test", DisplayName: "Administrator"})
	role, err := persistence.Role().Save(ctx, &model.Role{Name: model.SystemAdministratorRoleName,
		DisplayName: "System Administrator", Permissions: model.AllActions(), BuiltIn: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = persistence.RoleBinding().Save(ctx, &model.RoleBinding{UserID: actor.ID, RoleID: role.ID,
		ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(), StartsAt: model.NowUTC().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	session, credential, _, _ := saveMFARecoverySQLSession(t, ctx, persistence, actor.ID, 0, false, true)
	principal := mfaSQLPrincipal(session, credential)
	// Policy metadata can originate from another node whose clock was ahead.
	// Keep administrator activity at the real time: only policy metadata is skewed.
	if _, err = persistence.GetMaster().Exec(ctx, `UPDATE retention_policies
        SET updated_at=clock_timestamp()+INTERVAL '1 minute' WHERE institution_id=?`, institution.ID.String()); err != nil {
		t.Fatal(err)
	}
	initial, err := persistence.RetentionPolicy().Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name     string
		settings model.RetentionPolicySettings
		changed  bool
		revision int64
	}{
		{name: "no-op", revision: 1},
		{name: "changed", settings: model.RetentionPolicySettings{SubmissionRetentionDays: 365}, changed: true, revision: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			audit, saveErr := persistence.Audit().Save(ctx, &model.AuditEvent{ActorID: actor.ID, Action: string(model.ActionRetentionPolicyManage),
				Resource: model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}, ScopeType: model.RoleScopeInstitution,
				ScopeID: institution.ID.String(), Status: model.AuditStatusAttempt, NodeID: "store-test"})
			if saveErr != nil {
				t.Fatal(saveErr)
			}
			command := &store.CommandIdempotency{UserID: actor.ID, Operation: "retention_policy.replace.v1",
				KeyDigest: sha256.Sum256([]byte(test.name)), FingerprintVersion: 1,
				Fingerprint: sha256.Sum256([]byte("retention-clock:" + test.name)), OutcomeVersion: 1, Retention: time.Hour, Wait: time.Second}
			result, replaceErr := persistence.RetentionPolicy().Replace(ctx, &store.RetentionPolicyReplacement{RetentionMutation: store.RetentionMutation{Principal: principal, RecentAuthenticationTTL: time.Hour, AuditEventID: audit.ID.String(), AuditAt: model.MillisFromTime(audit.CreatedAt)}, ExpectedRevision: 1, Settings: test.settings}, command)
			if replaceErr != nil {
				t.Fatalf("policy metadata ahead of database clock rejected %s: %v (cause=%v)", test.name, replaceErr, errors.Unwrap(replaceErr))
			}
			if result.Replayed || result.Changed != test.changed || result.Policy.Revision != test.revision ||
				result.Policy.RetentionPolicySettings != test.settings || !result.Policy.UpdatedAt.Equal(initial.UpdatedAt) ||
				!result.Policy.CreatedAt.Equal(initial.CreatedAt) {
				t.Fatalf("non-monotonic policy transition: %#v, previous=%#v", result, initial)
			}
			storedAudit, auditErr := persistence.Audit().Get(ctx, audit.ID.String())
			if auditErr != nil || storedAudit.Status != model.AuditStatusSuccess {
				t.Fatalf("policy transition did not complete audit: %#v, %v", storedAudit, auditErr)
			}
		})
	}
}

func TestPolicyReplacementRechecksAdministratorAfterBindingEnd(t *testing.T) {
	for _, test := range []struct {
		name      string
		action    model.Action
		operation string
		replace   func(context.Context, *SQLStore, model.Principal, *model.AuditEvent, *store.CommandIdempotency) error
		revision  func(context.Context, *SQLStore) (int64, error)
	}{
		{
			name: "retention", action: model.ActionRetentionPolicyManage, operation: "retention_policy.replace.v1",
			replace: func(ctx context.Context, persistence *SQLStore, principal model.Principal, audit *model.AuditEvent, command *store.CommandIdempotency) error {
				_, err := persistence.RetentionPolicy().Replace(ctx, &store.RetentionPolicyReplacement{RetentionMutation: store.RetentionMutation{Principal: principal, RecentAuthenticationTTL: time.Hour, AuditEventID: audit.ID.String(), AuditAt: model.MillisFromTime(audit.CreatedAt)}, ExpectedRevision: 1, Settings: model.RetentionPolicySettings{SubmissionRetentionDays: 365}}, command)
				return err
			},
			revision: func(ctx context.Context, persistence *SQLStore) (int64, error) {
				policy, err := persistence.RetentionPolicy().Get(ctx)
				if err != nil {
					return 0, err
				}
				return policy.Revision, nil
			},
		},
		{
			name: "desktop compatibility", action: model.ActionDesktopCompatibilityPolicyManage, operation: "desktop_compatibility_policy.replace.v1",
			replace: func(ctx context.Context, persistence *SQLStore, principal model.Principal, audit *model.AuditEvent, command *store.CommandIdempotency) error {
				_, err := persistence.DesktopCompatibilityPolicy().Replace(ctx, &store.DesktopCompatibilityPolicyReplacement{ActorID: principal.UserID, ExpectedRevision: 1,
					Settings:     model.DesktopCompatibilityPolicySettings{MinimumDesktopRelease: "2.0.0", RevokedDesktopBuildIDs: []string{}, Availability: model.DesktopAvailabilityReady},
					AuditEventID: audit.ID.String(), AuditAt: model.MillisFromTime(audit.CreatedAt)}, command)
				return err
			},
			revision: func(ctx context.Context, persistence *SQLStore) (int64, error) {
				policy, err := persistence.DesktopCompatibilityPolicy().Get(ctx)
				if err != nil {
					return 0, err
				}
				return policy.Revision, nil
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			persistence := openTestStore(t)
			resetTestStore(t, persistence)
			institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "policy-clock", DisplayName: "Policy Clock Institution"})
			if err != nil {
				t.Fatal(err)
			}
			role, err := persistence.Role().Save(ctx, &model.Role{Name: model.SystemAdministratorRoleName,
				DisplayName: "System Administrator", Permissions: model.AllActions(), BuiltIn: true})
			if err != nil {
				t.Fatal(err)
			}
			var actor *model.User
			var binding *model.RoleBinding
			for _, username := range []string{"remaining-administrator", "revoked-administrator"} {
				user := saveIntegrationUser(t, ctx, persistence, &model.User{Username: username, Email: username + "@example.test", DisplayName: username})
				passwordProofForSQLTest(t, ctx, persistence, user.ID)
				binding, err = persistence.RoleBinding().Save(ctx, &model.RoleBinding{UserID: user.ID, RoleID: role.ID,
					ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(), StartsAt: model.NowUTC().Add(-time.Hour)})
				if err != nil {
					t.Fatal(err)
				}
				actor = user
			}
			session, credential, _, _ := saveMFARecoverySQLSession(t, ctx, persistence, actor.ID, 0, false, true)
			principal := mfaSQLPrincipal(session, credential)
			audit, err := persistence.Audit().Save(ctx, &model.AuditEvent{ActorID: actor.ID, Action: string(test.action),
				Resource: model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}, ScopeType: model.RoleScopeInstitution,
				ScopeID: institution.ID.String(), Status: model.AuditStatusAttempt, NodeID: "store-test"})
			if err != nil {
				t.Fatal(err)
			}
			command := &store.CommandIdempotency{UserID: actor.ID, Operation: test.operation,
				KeyDigest: sha256.Sum256([]byte("serialized-policy-edit")), FingerprintVersion: 1,
				Fingerprint: sha256.Sum256([]byte("serialized-policy-content")), OutcomeVersion: 1, Retention: time.Hour, Wait: time.Second}

			// Hold the real End transaction before the competing Replace starts.
			// The SQL fixture controls only the lock timing; endRoleBinding is the
			// production mutation called by RoleBindingStore.End in this transaction.
			ending, err := persistence.GetMaster().Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = ending.Rollback() }()
			if err = lockSystemAdministratorAuthenticationPaths(ctx, ending); err != nil {
				t.Fatal(err)
			}
			var blockerPID int
			if err = ending.Get(ctx, &blockerPID, `SELECT pg_backend_pid()`); err != nil {
				t.Fatal(err)
			}
			completed := make(chan error, 1)
			go func() { completed <- test.replace(ctx, persistence, principal, audit, command) }()
			waitForBlockedSystemAdministratorAuthenticationPathTransactions(t, ctx, persistence, blockerPID, 1)
			endAt := policyBindingEndAfterWaitingTransaction(t, ctx, ending, blockerPID)
			if _, err = endRoleBinding(ctx, ending, binding.ID.String(), endAt, store.AccessDeploymentCapabilities{}); err != nil {
				t.Fatal(err)
			}
			if err = ending.Commit(); err != nil {
				t.Fatal(err)
			}
			var replaceErr error
			select {
			case replaceErr = <-completed:
			case <-ctx.Done():
				t.Fatal("replacement did not complete after binding End:", ctx.Err())
			}
			var conflict *store.ErrConflict
			if !errors.As(replaceErr, &conflict) || (conflict.Constraint != "actor_not_system_administrator" && conflict.Resource != "authorization") {
				t.Fatalf("replacement admitted ended administrator: %v", replaceErr)
			}
			if revision, readErr := test.revision(ctx, persistence); readErr != nil || revision != 1 {
				t.Fatalf("policy changed after administrator End: revision=%d err=%v", revision, readErr)
			}
			if found, outcomeErr := persistence.CommandOutcome().Has(ctx, command); outcomeErr != nil || found {
				t.Fatalf("revoked administrator recorded an outcome: exists=%v err=%v", found, outcomeErr)
			}
			storedAudit, err := persistence.Audit().Get(ctx, audit.ID.String())
			if err != nil || storedAudit.Status != model.AuditStatusAttempt {
				t.Fatalf("revoked administrator completed audit: event=%#v err=%v", storedAudit, err)
			}
		})
	}
}

func policyBindingEndAfterWaitingTransaction(t *testing.T, ctx context.Context, ending *sqlxTxWrapper, blockerPID int) int64 {
	t.Helper()
	var waitingSince time.Time
	if err := ending.Get(ctx, &waitingSince, `SELECT MIN(xact_start) FROM pg_stat_activity
        WHERE $1=ANY(pg_blocking_pids(pid)) AND query LIKE '%pg_advisory_xact_lock%'`, blockerPID); err != nil {
		t.Fatal(err)
	}
	// End accepts millisecond instants. Ensure its PostgreSQL timestamp is
	// strictly later than Replace's start even within the same millisecond.
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		var databaseNow time.Time
		if err := ending.Get(ctx, &databaseNow, `SELECT clock_timestamp()`); err != nil {
			t.Fatal(err)
		}
		endAt := model.MillisFromTime(databaseNow)
		if model.TimeFromMillis(endAt).After(waitingSince) {
			return endAt
		}
		select {
		case <-ctx.Done():
			t.Fatal("binding End time did not pass transaction start:", ctx.Err())
		case <-ticker.C:
		}
	}
}
