// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestRetentionPolicyStore(t *testing.T, stores store.Store) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	policy, err := stores.RetentionPolicy().Get(ctx)
	require.True(t, store.IsNotFound(err), "Get before bootstrap")
	require.Nil(t, policy, "Get before bootstrap")
	bootstrap, err := stores.Installation().Bootstrap(ctx, testInstallationBootstrap(812))
	require.NoError(t, err)
	initial, err := stores.RetentionPolicy().Get(ctx)
	require.NoError(t, err)
	require.NoError(t, initial.Validate(), "bootstrap retention policy")
	require.Equal(t, bootstrap.Institution.ID, initial.InstitutionID, "bootstrap retention policy")
	require.Equal(t, int64(1), initial.Revision, "bootstrap retention policy")
	require.Equal(t, model.RetentionPolicySettings{}, initial.RetentionPolicySettings, "bootstrap retention policy")
	require.True(t, initial.CreatedAt.Equal(initial.UpdatedAt), "bootstrap retention policy")
	require.Equal(t, bootstrap.RetentionPolicy, initial, "bootstrap retention policy")

	settings := model.RetentionPolicySettings{SubmissionRetentionDays: 365, IntegrityRetentionDays: 180,
		AuditRetentionDays: 730, ExportRetentionDays: 1, DeletionGraceDays: 7}
	principal := saveRecordsPrincipal(t, ctx, stores, bootstrap.Administrator.ID, true)
	replacement := &store.RetentionPolicyReplacement{RetentionMutation: store.RetentionMutation{Principal: principal, RecentAuthenticationTTL: time.Hour},
		ExpectedRevision: initial.Revision, Settings: settings}
	prepareRetentionPolicyAttempt(t, ctx, stores, bootstrap.Institution.ID, replacement)
	command := retentionPolicyCommand(replacement.Principal.UserID, "replace")
	result, err := stores.RetentionPolicy().Replace(ctx, replacement, command)
	require.NoError(t, err, "initial retention replacement")
	require.False(t, result.Replayed)
	require.True(t, result.Changed)
	require.Equal(t, int64(2), result.Policy.Revision)
	require.Equal(t, settings, result.Policy.RetentionPolicySettings)
	require.True(t, result.Policy.CreatedAt.Equal(initial.CreatedAt))
	require.False(t, result.Policy.UpdatedAt.Before(initial.UpdatedAt))
	assertRetentionPolicyAudit(t, ctx, stores, replacement, result, "")
	assertRetentionPolicyOutcome(t, ctx, stores, command, true)
	persisted, err := stores.RetentionPolicy().Get(ctx)
	require.NoError(t, err)
	require.Equal(t, result.Policy, persisted, "persisted policy")

	replayInput := &store.RetentionPolicyReplacement{RetentionMutation: replacement.RetentionMutation, ExpectedRevision: initial.Revision, Settings: settings}
	prepareRetentionPolicyAttempt(t, ctx, stores, bootstrap.Institution.ID, replayInput)
	replay, err := stores.RetentionPolicy().Replace(ctx, replayInput, command)
	require.NoError(t, err, "exact retention replay")
	require.True(t, replay.Replayed, "exact replay = %#v, original = %#v", replay, result)
	require.True(t, replay.Changed, "exact replay = %#v, original = %#v", replay, result)
	require.Equal(t, result.Policy, replay.Policy, "exact replay = %#v, original = %#v", replay, result)
	assertRetentionPolicyAudit(t, ctx, stores, replayInput, replay, replacement.AuditEventID)

	conflictInput := *replayInput
	conflictInput.Settings.ExportRetentionDays++
	prepareRetentionPolicyAttempt(t, ctx, stores, bootstrap.Institution.ID, &conflictInput)
	conflictCommand := *command
	conflictCommand.Fingerprint = sha256.Sum256([]byte("different retention policy content"))
	_, err = stores.RetentionPolicy().Replace(ctx, &conflictInput, &conflictCommand)
	var keyConflict *store.ErrIdempotencyConflict
	require.ErrorAs(t, err, &keyConflict)
	assertRetentionPolicyAttemptPending(t, ctx, stores, conflictInput.AuditEventID)

	staleInput := *replayInput
	prepareRetentionPolicyAttempt(t, ctx, stores, bootstrap.Institution.ID, &staleInput)
	staleCommand := retentionPolicyCommand(staleInput.Principal.UserID, "different-key-stale-revision")
	_, err = stores.RetentionPolicy().Replace(ctx, &staleInput, staleCommand)
	var revisionConflict *store.ErrRetentionPolicyRevisionConflict
	require.ErrorAs(t, err, &revisionConflict)
	require.Equal(t, result.Policy.Revision, revisionConflict.CurrentRevision)
	assertRetentionPolicyOutcome(t, ctx, stores, staleCommand, false)
	assertRetentionPolicyAttemptPending(t, ctx, stores, staleInput.AuditEventID)

	noOp := &store.RetentionPolicyReplacement{RetentionMutation: replacement.RetentionMutation, ExpectedRevision: result.Policy.Revision, Settings: settings}
	prepareRetentionPolicyAttempt(t, ctx, stores, bootstrap.Institution.ID, noOp)
	noOpCommand := retentionPolicyCommand(noOp.Principal.UserID, "no-op")
	noOpResult, err := stores.RetentionPolicy().Replace(ctx, noOp, noOpCommand)
	require.NoError(t, err, "retention no-op")
	require.False(t, noOpResult.Replayed, "no-op changed revision or timestamps: %#v, prior", noOpResult)
	require.False(t, noOpResult.Changed, "no-op changed revision or timestamps: %#v, prior", noOpResult)
	require.Equal(t, result.Policy, noOpResult.Policy, "no-op changed revision or timestamps: %#v, prior", noOpResult)
	assertRetentionPolicyAudit(t, ctx, stores, noOp, noOpResult, "")
	assertRetentionPolicyOutcome(t, ctx, stores, noOpCommand, true)

	missingAudit := &store.RetentionPolicyReplacement{RetentionMutation: replacement.RetentionMutation, ExpectedRevision: result.Policy.Revision,
		Settings: settings}
	missingAudit.AuditEventID, missingAudit.AuditAt = model.NewAuditEventID(), model.NowUTC()
	missingAudit.Settings.SubmissionRetentionDays++
	missingAuditCommand := retentionPolicyCommand(missingAudit.Principal.UserID, "missing-audit")
	_, err = stores.RetentionPolicy().Replace(ctx, missingAudit, missingAuditCommand)
	require.Error(t, err, "replacement succeeded without an audit attempt")
	assertRetentionPolicyOutcome(t, ctx, stores, missingAuditCommand, false)
	persisted, err = stores.RetentionPolicy().Get(ctx)
	require.NoError(t, err)
	require.Equal(t, result.Policy, persisted, "policy survived audit rollback")
	// Retrying the same command after a fresh audit proves the failed attempt
	// did not reserve an outcome or consume the expected revision.
	prepareRetentionPolicyAttempt(t, ctx, stores, bootstrap.Institution.ID, missingAudit)
	recovered, err := stores.RetentionPolicy().Replace(ctx, missingAudit, missingAuditCommand)
	require.NoError(t, err, "retention recovery after missing audit")
	require.False(t, recovered.Replayed)
	require.True(t, recovered.Changed)
	require.Equal(t, result.Policy.Revision+1, recovered.Policy.Revision)
	assertRetentionPolicyAudit(t, ctx, stores, missingAudit, recovered, "")

	testRetentionPolicyConcurrentEditors(t, ctx, stores, bootstrap, recovered.Policy)
	testRetentionPolicyCurrentCredentialAndAssurance(t, ctx, stores, bootstrap)
	testRetentionPolicyActiveAdministratorGuard(t, ctx, stores, bootstrap)
	testRetentionPolicyReplacementInstitution(t, ctx, stores, bootstrap)
}

func testRetentionPolicyCurrentCredentialAndAssurance(t *testing.T, ctx context.Context, stores store.Store,
	bootstrap *model.InstallationBootstrapResult,
) {
	t.Helper()
	principal := saveRecordsPrincipal(t, ctx, stores, bootstrap.Administrator.ID, true)
	current, err := stores.RetentionPolicy().Get(ctx)
	require.NoError(t, err)
	original := &store.RetentionPolicyReplacement{RetentionMutation: store.RetentionMutation{Principal: principal, RecentAuthenticationTTL: time.Hour},
		ExpectedRevision: current.Revision, Settings: current.RetentionPolicySettings}
	original.Settings.AuditRetentionDays++
	prepareRetentionPolicyAttempt(t, ctx, stores, bootstrap.Institution.ID, original)
	originalCommand := retentionPolicyCommand(principal.UserID, "assurance-original")
	first, err := stores.RetentionPolicy().Replace(ctx, original, originalCommand)
	require.NoError(t, err)

	for _, state := range []string{"single factor", "expired assurance", "revoked Session"} {
		t.Run(state, func(t *testing.T) {
			session, credentials, _ := newSession(principal.UserID.String())
			if state != "single factor" {
				session.AuthenticationStrength = model.AuthenticationMultiFactor
				if state == "expired assurance" {
					session.AuthenticatedAt = session.AuthenticatedAt.Add(-2 * time.Hour)
				}
				session.MFACompletedAt = model.OptionalTimeFrom(session.AuthenticatedAt)
			}
			saved, credentials, saveErr := SaveSession(ctx, stores, testSessionCreation(t, ctx, stores, session, credentials, 10))
			require.NoError(t, saveErr)
			// Keep the request snapshot strong and fresh. The stored Session and
			// exact credential must decide even when a caller has stale facts.
			stale := principal
			stale.SessionID, stale.CredentialID = saved.ID, model.PrincipalCredentialID(credentials[0].ID)
			if state == "revoked Session" {
				_, revokeErr := stores.Session().Withdraw(ctx, &store.SessionWithdrawal{ID: saved.ID, UserID: principal.UserID, RevokedAt: model.NowUTC(), Reason: model.SessionRevocationInactiveUser})
				require.NoError(t, revokeErr)
			}
			for _, operation := range []string{"change", "no-op", "replay"} {
				t.Run(operation, func(t *testing.T) {
					input := *original
					input.Principal = stale
					input.ExpectedRevision, input.Settings = first.Policy.Revision, first.Policy.RetentionPolicySettings
					command := retentionPolicyCommand(principal.UserID, state+"/"+operation)
					if operation == "change" {
						input.Settings.SubmissionRetentionDays++
					} else if operation == "replay" {
						input.ExpectedRevision, input.Settings = original.ExpectedRevision, original.Settings
						command = originalCommand
					}
					prepareRetentionPolicyAttempt(t, ctx, stores, bootstrap.Institution.ID, &input)
					result, replaceErr := stores.RetentionPolicy().Replace(ctx, &input, command)
					var conflict *store.ErrConflict
					require.Nil(t, result, "stale credential or assurance admitted %s: result=%#v error", operation, result)
					require.ErrorAs(t, replaceErr, &conflict, "stale credential or assurance admitted %s: result=%#v error", operation, result)
					require.Equal(t, "authorization", conflict.Resource, "stale credential or assurance admitted %s: result=%#v error", operation, result)
					assertRetentionPolicyAttemptPending(t, ctx, stores, input.AuditEventID)
					assertRetentionPolicyOutcome(t, ctx, stores, command, operation == "replay")
					persisted, getErr := stores.RetentionPolicy().Get(ctx)
					require.NoError(t, getErr)
					require.Equal(t, first.Policy, persisted, "rejected %s changed policy", operation)
				})
			}
		})
	}
}

func testRetentionPolicyReplacementInstitution(t *testing.T, ctx context.Context, stores store.Store,
	bootstrap *model.InstallationBootstrapResult,
) {
	t.Helper()
	require.NoError(t, stores.Institution().Archive(ctx, &store.InstitutionArchive{ID: bootstrap.Institution.ID, ArchiveAt: model.NowUTC()}))
	policy, err := stores.RetentionPolicy().Get(ctx)
	require.True(t, store.IsNotFound(err), "Get without an active Institution")
	require.Nil(t, policy, "Get without an active Institution")
	replacement, err := stores.Institution().Save(ctx, &model.Institution{Name: "replacement-retention-institution", DisplayName: "Replacement Institution"})
	require.NoError(t, err)
	policy, err = stores.RetentionPolicy().Get(ctx)
	require.NoError(t, err)
	require.Equal(t, replacement.ID, policy.InstitutionID, "replacement Institution inherited archived policy: %#v, Institution=%#v", policy, replacement)
	require.Equal(t, int64(1), policy.Revision, "replacement Institution inherited archived policy: %#v, Institution=%#v", policy, replacement)
	require.Equal(t, model.RetentionPolicySettings{}, policy.RetentionPolicySettings, "replacement Institution inherited archived policy: %#v, Institution=%#v", policy, replacement)
	require.True(t, policy.CreatedAt.Equal(replacement.CreatedAt), "replacement Institution inherited archived policy: %#v, Institution=%#v", policy, replacement)
	require.True(t, policy.UpdatedAt.Equal(policy.CreatedAt), "replacement Institution inherited archived policy: %#v, Institution=%#v", policy, replacement)
}

func testRetentionPolicyConcurrentEditors(t *testing.T, ctx context.Context, stores store.Store,
	bootstrap *model.InstallationBootstrapResult, current *model.RetentionPolicy,
) {
	t.Helper()
	other := saveUserWithPassword(t, ctx, stores)
	_, err := SaveRoleBinding(ctx, stores, &model.RoleBinding{UserID: other.ID, RoleID: bootstrap.Role.ID,
		ScopeType: model.RoleScopeInstitution, ScopeID: bootstrap.Institution.ID.String(), StartsAt: model.NowUTC().Add(-time.Hour)})
	require.NoError(t, err)
	inputs := make([]*store.RetentionPolicyReplacement, 2)
	commands := make([]*store.CommandIdempotency, 2)
	for index, actorID := range []model.UserID{bootstrap.Administrator.ID, other.ID} {
		input := &store.RetentionPolicyReplacement{RetentionMutation: store.RetentionMutation{Principal: saveRecordsPrincipal(t, ctx, stores, actorID, true), RecentAuthenticationTTL: time.Hour}, ExpectedRevision: current.Revision, Settings: current.RetentionPolicySettings}
		input.Settings.ExportRetentionDays += index + 1
		prepareRetentionPolicyAttempt(t, ctx, stores, bootstrap.Institution.ID, input)
		inputs[index] = input
		commands[index] = retentionPolicyCommand(actorID, "concurrent-edit")
	}
	type editResult struct {
		index  int
		result *store.RetentionPolicyReplacementResult
		err    error
	}
	start := make(chan struct{})
	completed := make(chan editResult, len(inputs))
	for index, input := range inputs {
		go func() {
			<-start
			result, editErr := stores.RetentionPolicy().Replace(ctx, input, commands[index])
			completed <- editResult{index: index, result: result, err: editErr}
		}()
	}
	close(start)
	winners, losers := 0, 0
	var winner *model.RetentionPolicy
	for range inputs {
		var completedEdit editResult
		select {
		case completedEdit = <-completed:
		case <-ctx.Done():
			require.FailNow(t, fmt.Sprint("concurrent retention editors did not complete:", ctx.Err()))
		}
		input, command := inputs[completedEdit.index], commands[completedEdit.index]
		if completedEdit.err == nil {
			winners++
			result := completedEdit.result
			require.False(t, result.Replayed)
			require.True(t, result.Changed)
			require.Equal(t, current.Revision+1, result.Policy.Revision)
			require.Equal(t, input.Settings, result.Policy.RetentionPolicySettings)
			winner = result.Policy
			assertRetentionPolicyAudit(t, ctx, stores, input, result, "")
			assertRetentionPolicyOutcome(t, ctx, stores, command, true)
			continue
		}
		var conflict *store.ErrRetentionPolicyRevisionConflict
		require.ErrorAs(t, completedEdit.err, &conflict)
		require.Equal(t, current.Revision+1, conflict.CurrentRevision)
		losers++
		assertRetentionPolicyAttemptPending(t, ctx, stores, input.AuditEventID)
		assertRetentionPolicyOutcome(t, ctx, stores, command, false)
	}
	require.Equal(t, 1, winners, "concurrent edits: winners")
	require.Equal(t, 1, losers, "concurrent edits: winners")
	persisted, err := stores.RetentionPolicy().Get(ctx)
	require.NoError(t, err)
	require.Equal(t, winner, persisted, "persisted concurrent winner")
}

func testRetentionPolicyActiveAdministratorGuard(t *testing.T, ctx context.Context, stores store.Store,
	bootstrap *model.InstallationBootstrapResult,
) {
	t.Helper()
	current, err := stores.RetentionPolicy().Get(ctx)
	require.NoError(t, err)
	for _, test := range []struct {
		name     string
		binding  bool
		ended    bool
		disabled bool
	}{
		{name: "ordinary user"},
		{name: "ended administrator", binding: true, ended: true},
		{name: "disabled administrator", binding: true, disabled: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			actor := saveUserWithPassword(t, ctx, stores)
			if test.binding {
				binding, saveErr := SaveRoleBinding(ctx, stores, &model.RoleBinding{UserID: actor.ID, RoleID: bootstrap.Role.ID,
					ScopeType: model.RoleScopeInstitution, ScopeID: bootstrap.Institution.ID.String(), StartsAt: model.NowUTC().Add(-time.Hour)})
				require.NoError(t, saveErr)
				if test.ended {
					_, endErr := EndRoleBinding(ctx, stores, binding.ID, model.NowUTC())
					require.NoError(t, endErr)
				}
			}
			principal := saveRecordsPrincipal(t, ctx, stores, actor.ID, true)
			if test.disabled {
				audit := saveUserProfileAuditAttempt(t, ctx, stores, actor.ID.String())
				_, disableErr := stores.User().SetDisabled(ctx, userDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
					ID: actor.ID, ExpectedRevision: actor.Revision, Disabled: true, ChangedAt: model.NowUTC(),
					RevocationReason: model.SessionRevocationAccountDisabled, AuditEventID: audit.ID, AuditAt: audit.CreatedAt,
				}))
				require.NoError(t, disableErr)
			}
			input := &store.RetentionPolicyReplacement{RetentionMutation: store.RetentionMutation{Principal: principal, RecentAuthenticationTTL: time.Hour}, ExpectedRevision: current.Revision, Settings: current.RetentionPolicySettings}
			input.Settings.DeletionGraceDays++
			prepareRetentionPolicyAttempt(t, ctx, stores, bootstrap.Institution.ID, input)
			command := retentionPolicyCommand(actor.ID, "unauthorized")
			_, replaceErr := stores.RetentionPolicy().Replace(ctx, input, command)
			var conflict *store.ErrConflict
			require.ErrorAs(t, replaceErr, &conflict)
			require.Equal(t, "authorization", conflict.Resource)
			assertRetentionPolicyAttemptPending(t, ctx, stores, input.AuditEventID)
			assertRetentionPolicyOutcome(t, ctx, stores, command, false)
			persisted, getErr := stores.RetentionPolicy().Get(ctx)
			require.NoError(t, getErr)
			require.Equal(t, current, persisted, "unauthorized edit changed policy")
		})
	}
}

func retentionPolicyCommand(actorID model.UserID, identity string) *store.CommandIdempotency {
	return &store.CommandIdempotency{UserID: actorID, Operation: "retention_policy.replace",
		KeyDigest:   sha256.Sum256([]byte("retention-policy-key:" + identity)),
		Fingerprint: sha256.Sum256([]byte("retention-policy-command:" + identity)),
		Retention:   time.Hour, Wait: time.Second}
}

func prepareRetentionPolicyAttempt(t *testing.T, ctx context.Context, stores store.Store, institutionID model.InstitutionID,
	replacement *store.RetentionPolicyReplacement,
) {
	t.Helper()
	event, err := stores.Audit().Save(ctx, &model.AuditEvent{ActorID: replacement.Principal.UserID, SessionID: replacement.Principal.SessionID,
		Action: string(model.ActionRetentionPolicyManage), Resource: model.Resource{Type: model.ResourceInstitution, ID: institutionID.String()},
		ScopeType: model.RoleScopeInstitution, ScopeID: institutionID.String(), Status: model.AuditStatusAttempt,
		NodeID: "store-test", ClientType: string(model.SessionClientWeb), AuthMethod: "password"})
	require.NoError(t, err)
	replacement.AuditEventID, replacement.AuditAt = event.ID, event.CreatedAt
}

func assertRetentionPolicyAudit(t *testing.T, ctx context.Context, stores store.Store, input *store.RetentionPolicyReplacement,
	outcome *store.RetentionPolicyReplacementResult, originalAuditID model.AuditEventID,
) {
	t.Helper()
	event, err := stores.Audit().Get(ctx, input.AuditEventID)
	require.NoError(t, err)
	require.Equal(t, model.AuditStatusSuccess, event.Status)
	var result map[string]any
	require.NoError(t, json.Unmarshal(event.Result, &result))
	require.Equal(t, "replace", result["operation"])
	require.Equal(t, outcome.Changed, result["changed"])
	require.Equal(t, float64(input.ExpectedRevision), result["expected_revision"])
	require.Equal(t, float64(outcome.Policy.Revision), result["resulting_revision"])
	require.Equal(t, float64(outcome.Policy.SubmissionRetentionDays), result["submission_retention_days"])
	require.Equal(t, float64(outcome.Policy.IntegrityRetentionDays), result["integrity_retention_days"])
	require.Equal(t, float64(outcome.Policy.AuditRetentionDays), result["audit_retention_days"])
	require.Equal(t, float64(outcome.Policy.ExportRetentionDays), result["export_retention_days"])
	require.Equal(t, float64(outcome.Policy.DeletionGraceDays), result["deletion_grace_days"])
	require.Equal(t, false, result["automatic_deletion_enabled"])
	if originalAuditID != "" {
		require.Equal(t, true, result["idempotency_replayed"])
		require.Equal(t, originalAuditID.String(), result["original_audit_event_id"])
	}
}

func assertRetentionPolicyAttemptPending(t *testing.T, ctx context.Context, stores store.Store, auditID model.AuditEventID) {
	t.Helper()
	event, err := stores.Audit().Get(ctx, auditID)
	require.NoError(t, err)
	require.Equal(t, model.AuditStatusAttempt, event.Status)
}

func assertRetentionPolicyOutcome(t *testing.T, ctx context.Context, stores store.Store, command *store.CommandIdempotency, want bool) {
	t.Helper()
	found, err := stores.CommandOutcome().Has(ctx, command)
	require.NoError(t, err)
	require.Equal(t, want, found, "retention command outcome exists")
}
