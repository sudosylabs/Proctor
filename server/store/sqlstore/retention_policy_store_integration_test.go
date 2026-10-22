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
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/storetest"
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
			require.NoError(t, err)
			mutation := func(action model.Action) store.RetentionMutation {
				audit, err := s.Audit().Save(ctx, &model.AuditEvent{ActorID: f.principal.UserID, SessionID: f.principal.SessionID,
					Action: string(action), Resource: model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()},
					ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(), Status: model.AuditStatusAttempt, NodeID: "policy-test"})
				require.NoError(t, err)
				return store.RetentionMutation{Principal: f.principal, RecentAuthenticationTTL: time.Hour,
					AuditEventID: audit.ID, AuditAt: model.MillisFromTime(audit.CreatedAt)}
			}
			command := func(operation, key string) *store.CommandIdempotency {
				return &store.CommandIdempotency{UserID: f.principal.UserID, Operation: operation,
					KeyDigest: sha256.Sum256([]byte(key)), Fingerprint: sha256.Sum256([]byte(key)), Retention: time.Hour, Wait: time.Second}
			}
			original := &store.RetentionPolicyReplacement{RetentionMutation: mutation(model.ActionRetentionPolicyManage), ExpectedRevision: 1,
				Settings: model.RetentionPolicySettings{AuditRetentionDays: 1, ExportRetentionDays: 1, DeletionGraceDays: 1}}
			originalCommand := command("retention_policy.replace.v1", "original")
			first, err := s.RetentionPolicy().Replace(ctx, original, originalCommand)
			require.NoError(t, err)
			preview, err := s.Retention().CreatePreview(ctx, &store.RetentionPreviewCreation{RetentionMutation: mutation(model.ActionRetentionPolicyView),
				PreviewID: model.NewRetentionPreviewID(), ExpectedPolicyRevision: first.Policy.Revision}, command(store.RetentionPreviewOperation, "preview"))
			require.NoError(t, err)
			_, err = s.Retention().ChangeControl(ctx, &store.RetentionControlChange{RetentionMutation: mutation(model.ActionRetentionCleanupManage),
				ExpectedRevision: 1, ExpectedPolicyRevision: first.Policy.Revision, State: model.RetentionControlEnabled, PreviewID: preview.ID},
				command(store.RetentionControlOperation, "enable"))
			require.NoError(t, err)
			before, err := s.RetentionPolicy().Get(ctx)
			require.NoError(t, err)
			control, err := s.Retention().GetControl(ctx)
			require.NoError(t, err)
			// Age a completed audit, then use the real scheduling operation.
			// Rejected writes must neither cancel grace nor pause live approval.
			target := mutation(model.ActionAuditView)
			scheduledID := target.AuditEventID
			_, err = s.Audit().Complete(ctx, scheduledID, model.AuditStatusSuccess, "", nil, model.GetMillis())
			require.NoError(t, err)
			_, err = s.GetMaster().Exec(ctx, `UPDATE audit_events SET created_at=clock_timestamp()-INTERVAL '3 days',
				updated_at=clock_timestamp()-INTERVAL '2 days' WHERE id=?`, scheduledID)
			require.NoError(t, err)
			cleanupAudit, err := s.Audit().Save(ctx, &model.AuditEvent{Action: string(model.ActionRetentionCleanupManage),
				Resource: model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}, ScopeType: model.RoleScopeInstitution,
				ScopeID: institution.ID.String(), Status: model.AuditStatusAttempt, NodeID: "policy-test"})
			require.NoError(t, err)
			scheduled, err := s.Retention().ReconcileExpiry(ctx, &store.RetentionExpiryReconciliation{Kind: model.RetentionExpiryAudit,
				RecordID: scheduledID.String(), AuditEventID: cleanupAudit.ID, AuditAt: model.MillisFromTime(cleanupAudit.CreatedAt)})
			require.NoError(t, err, "audit expiry was not scheduled: %#v", scheduled)
			require.True(t, scheduled.Scheduled, "audit expiry was not scheduled: %#v", scheduled)
			defer func() {
				_, _ = s.GetMaster().Exec(context.Background(), `DELETE FROM retention_expiry_schedules WHERE record_id=?`, scheduledID)
			}()
			var expiresAfter time.Time
			err = s.GetMaster().Get(ctx, &expiresAfter, `SELECT expires_after FROM retention_expiry_schedules WHERE record_id=?`, scheduledID)
			require.NoError(t, err)
			id := f.principal.SessionID.String()
			if state.name == "revoked credential" || state.name == "expired credential" {
				id = f.principal.CredentialID.String()
			}
			_, err = s.GetMaster().Exec(ctx, state.query, id)
			require.NoError(t, err)
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
					require.Nil(t, result, "stale request admitted: result")
					require.ErrorAs(t, err, &conflict, "stale request admitted: result")
					require.Equal(t, "authorization", conflict.Resource, "stale request admitted: result")
					after, err := s.RetentionPolicy().Get(ctx)
					require.NoError(t, err, "rejected write changed policy")
					require.Equal(t, before, after, "rejected write changed policy")
					afterControl, err := s.Retention().GetControl(ctx)
					require.NoError(t, err, "rejected write changed approval")
					require.Equal(t, control, afterControl, "rejected write changed approval")
					var afterExpiry time.Time
					err = s.GetMaster().Get(ctx, &afterExpiry, `SELECT expires_after FROM retention_expiry_schedules WHERE record_id=?`, scheduledID)
					require.NoError(t, err, "rejected write reset grace: expiry=%s error", afterExpiry)
					require.True(t, afterExpiry.Equal(expiresAfter), "rejected write reset grace: expiry=%s error", afterExpiry)
					audit, err := s.Audit().Get(ctx, input.AuditEventID)
					require.NoError(t, err, "rejected write completed audit: %#v", audit)
					require.Equal(t, model.AuditStatusAttempt, audit.Status, "rejected write completed audit: %#v", audit)
					found, err := s.CommandOutcome().Has(ctx, key)
					require.NoError(t, err, "rejected write changed retained outcome: found")
					require.Equal(t, operation == "replay", found, "rejected write changed retained outcome: found")
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
	require.NoError(t, err)
	lastAccepted := 0
	for _, days := range []int{0, 1, 7, -1, 8, 36500} {
		t.Run(strconv.Itoa(days), func(t *testing.T) {
			_, err := s.GetMaster().Exec(ctx, `UPDATE retention_policies SET export_retention_days=?,
				submission_retention_days=36500,integrity_retention_days=36500,audit_retention_days=36500,deletion_grace_days=36500 WHERE institution_id=?`, days, institution.ID.String())
			if days >= 0 && days <= 7 {
				require.NoError(t, err)
				lastAccepted = days
			} else {
				var constraint *pq.Error
				require.ErrorAs(t, err, &constraint)
				require.Equal(t, "23514", string(constraint.Code))
				require.Equal(t, "retention_policies_export_retention_days_check", constraint.Constraint)
			}
			policy, err := s.RetentionPolicy().Get(ctx)
			require.NoError(t, err, "persisted bounds disagree: %#v", policy)
			require.Equal(t, lastAccepted, policy.ExportRetentionDays, "persisted bounds disagree: %#v", policy)
			require.Equal(t, 36500, policy.SubmissionRetentionDays, "persisted bounds disagree: %#v", policy)
			require.Equal(t, 36500, policy.IntegrityRetentionDays, "persisted bounds disagree: %#v", policy)
			require.Equal(t, 36500, policy.AuditRetentionDays, "persisted bounds disagree: %#v", policy)
			require.Equal(t, 36500, policy.DeletionGraceDays, "persisted bounds disagree: %#v", policy)
		})
	}
}

func TestRetentionPolicyReplacementKeepsMonotonicMetadata(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "retention-clock", DisplayName: "Retention Clock Institution"})
	require.NoError(t, err)
	actor := saveIntegrationUser(t, ctx, persistence, &model.User{Username: "retention-administrator",
		Email: "retention-administrator@example.test", DisplayName: "Administrator"})
	role, err := storetest.SaveRole(ctx, persistence, &model.Role{Name: model.SystemAdministratorRoleName,
		DisplayName: "System Administrator", Permissions: model.AllActions(), BuiltIn: true})
	require.NoError(t, err)
	_, err = storetest.SaveRoleBinding(ctx, persistence, &model.RoleBinding{UserID: actor.ID, RoleID: role.ID,
		ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(), StartsAt: model.NowUTC().Add(-time.Hour)})
	require.NoError(t, err)
	session, credential, _, _ := saveMFARecoverySQLSession(t, ctx, persistence, actor.ID, 0, false, true)
	principal := mfaSQLPrincipal(session, credential)
	// Policy metadata can originate from another node whose clock was ahead.
	// Keep administrator activity at the real time: only policy metadata is skewed.
	_, err = persistence.GetMaster().Exec(ctx, `UPDATE retention_policies
        SET updated_at=clock_timestamp()+INTERVAL '1 minute' WHERE institution_id=?`, institution.ID.String())
	require.NoError(t, err)
	initial, err := persistence.RetentionPolicy().Get(ctx)
	require.NoError(t, err)
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
			require.NoError(t, saveErr)
			command := &store.CommandIdempotency{UserID: actor.ID, Operation: "retention_policy.replace.v1",
				KeyDigest:   sha256.Sum256([]byte(test.name)),
				Fingerprint: sha256.Sum256([]byte("retention-clock:" + test.name)), Retention: time.Hour, Wait: time.Second}
			result, replaceErr := persistence.RetentionPolicy().Replace(ctx, &store.RetentionPolicyReplacement{RetentionMutation: store.RetentionMutation{Principal: principal, RecentAuthenticationTTL: time.Hour, AuditEventID: audit.ID, AuditAt: model.MillisFromTime(audit.CreatedAt)}, ExpectedRevision: 1, Settings: test.settings}, command)
			require.NoError(t, replaceErr, "policy metadata ahead of database clock rejected")
			require.False(t, result.Replayed, "non-monotonic policy transition: %#v, previous=%#v", result, initial)
			require.Equal(t, test.changed, result.Changed, "non-monotonic policy transition: %#v, previous=%#v", result, initial)
			require.Equal(t, test.revision, result.Policy.Revision, "non-monotonic policy transition: %#v, previous=%#v", result, initial)
			require.Equal(t, test.settings, result.Policy.RetentionPolicySettings, "non-monotonic policy transition: %#v, previous=%#v", result, initial)
			require.True(t, result.Policy.UpdatedAt.Equal(initial.UpdatedAt), "non-monotonic policy transition: %#v, previous=%#v", result, initial)
			require.True(t, result.Policy.CreatedAt.Equal(initial.CreatedAt), "non-monotonic policy transition: %#v, previous=%#v", result, initial)
			storedAudit, auditErr := persistence.Audit().Get(ctx, audit.ID)
			require.NoError(t, auditErr, "policy transition did not complete audit: %#v", storedAudit)
			require.Equal(t, model.AuditStatusSuccess, storedAudit.Status, "policy transition did not complete audit: %#v", storedAudit)
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
				_, err := persistence.RetentionPolicy().Replace(ctx, &store.RetentionPolicyReplacement{RetentionMutation: store.RetentionMutation{Principal: principal, RecentAuthenticationTTL: time.Hour, AuditEventID: audit.ID, AuditAt: model.MillisFromTime(audit.CreatedAt)}, ExpectedRevision: 1, Settings: model.RetentionPolicySettings{SubmissionRetentionDays: 365}}, command)
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
					AuditEventID: audit.ID, AuditAt: model.MillisFromTime(audit.CreatedAt)}, command)
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
			require.NoError(t, err)
			role, err := storetest.SaveRole(ctx, persistence, &model.Role{Name: model.SystemAdministratorRoleName,
				DisplayName: "System Administrator", Permissions: model.AllActions(), BuiltIn: true})
			require.NoError(t, err)
			var actor *model.User
			var binding *model.RoleBinding
			for _, username := range []string{"remaining-administrator", "revoked-administrator"} {
				user := saveIntegrationUser(t, ctx, persistence, &model.User{Username: username, Email: username + "@example.test", DisplayName: username})
				passwordProofForSQLTest(t, ctx, persistence, user.ID)
				binding, err = storetest.SaveRoleBinding(ctx, persistence, &model.RoleBinding{UserID: user.ID, RoleID: role.ID,
					ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(), StartsAt: model.NowUTC().Add(-time.Hour)})
				require.NoError(t, err)
				actor = user
			}
			session, credential, _, _ := saveMFARecoverySQLSession(t, ctx, persistence, actor.ID, 0, false, true)
			principal := mfaSQLPrincipal(session, credential)
			audit, err := persistence.Audit().Save(ctx, &model.AuditEvent{ActorID: actor.ID, Action: string(test.action),
				Resource: model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}, ScopeType: model.RoleScopeInstitution,
				ScopeID: institution.ID.String(), Status: model.AuditStatusAttempt, NodeID: "store-test"})
			require.NoError(t, err)
			command := &store.CommandIdempotency{UserID: actor.ID, Operation: test.operation,
				KeyDigest:   sha256.Sum256([]byte("serialized-policy-edit")),
				Fingerprint: sha256.Sum256([]byte("serialized-policy-content")), Retention: time.Hour, Wait: time.Second}

			// Hold the real End transaction before the competing Replace starts.
			// The SQL fixture controls only the lock timing; endRoleBinding is the
			// production mutation called by RoleBindingStore.End in this transaction.
			ending, err := persistence.GetMaster().Begin(ctx)
			require.NoError(t, err)
			defer func() { _ = ending.Rollback() }()
			err = lockSystemAdministratorAuthenticationPaths(ctx, ending)
			require.NoError(t, err)
			var blockerPID int
			err = ending.Get(ctx, &blockerPID, `SELECT pg_backend_pid()`)
			require.NoError(t, err)
			completed := make(chan error, 1)
			go func() { completed <- test.replace(ctx, persistence, principal, audit, command) }()
			waitForBlockedSystemAdministratorAuthenticationPathTransactions(t, ctx, persistence, blockerPID, 1)
			endAt := policyBindingEndAfterWaitingTransaction(t, ctx, ending, blockerPID)
			_, err = endRoleBinding(ctx, ending, binding.ID.String(), endAt, store.AccessDeploymentCapabilities{})
			require.NoError(t, err)
			err = ending.Commit()
			require.NoError(t, err)
			var replaceErr error
			select {
			case replaceErr = <-completed:
			case <-ctx.Done():
				require.FailNow(t, fmt.Sprint("replacement did not complete after binding End:", ctx.Err()))
			}
			var conflict *store.ErrConflict
			require.ErrorAs(t, replaceErr, &conflict)
			require.False(t, conflict.Constraint != "actor_not_system_administrator" && conflict.Resource != "authorization")
			revision, readErr := test.revision(ctx, persistence)
			require.NoError(t, readErr, "policy changed after administrator End: revision")
			require.Equal(t, int64(1), revision, "policy changed after administrator End: revision")
			found, outcomeErr := persistence.CommandOutcome().Has(ctx, command)
			require.NoError(t, outcomeErr, "revoked administrator recorded an outcome: exists")
			require.False(t, found, "revoked administrator recorded an outcome: exists")
			storedAudit, err := persistence.Audit().Get(ctx, audit.ID)
			require.NoError(t, err, "revoked administrator completed audit: event=%#v err", storedAudit)
			require.Equal(t, model.AuditStatusAttempt, storedAudit.Status, "revoked administrator completed audit: event=%#v err", storedAudit)
		})
	}
}

func policyBindingEndAfterWaitingTransaction(t *testing.T, ctx context.Context, ending *sqlxTxWrapper, blockerPID int) int64 {
	t.Helper()
	var waitingSince time.Time
	require.NoError(t, ending.Get(ctx, &waitingSince, `SELECT MIN(xact_start) FROM pg_stat_activity
        WHERE $1=ANY(pg_blocking_pids(pid)) AND query LIKE '%pg_advisory_xact_lock%'`, blockerPID))
	// End accepts millisecond instants. Ensure its PostgreSQL timestamp is
	// strictly later than Replace's start even within the same millisecond.
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		var databaseNow time.Time
		require.NoError(t, ending.Get(ctx, &databaseNow, `SELECT clock_timestamp()`))
		endAt := model.MillisFromTime(databaseNow)
		if model.TimeFromMillis(endAt).After(waitingSince) {
			return endAt
		}
		select {
		case <-ctx.Done():
			require.FailNow(t, fmt.Sprint("binding End time did not pass transaction start:", ctx.Err()))
		case <-ticker.C:
		}
	}
}
