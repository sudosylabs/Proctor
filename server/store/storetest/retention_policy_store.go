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
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestRetentionPolicyStore(t *testing.T, stores store.Store) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if policy, err := stores.RetentionPolicy().Get(ctx); !store.IsNotFound(err) || policy != nil {
		t.Fatalf("Get before bootstrap = %#v, %v", policy, err)
	}
	bootstrap, err := stores.Installation().Bootstrap(ctx, testInstallationBootstrap(812))
	requireNoError(t, err)
	initial, err := stores.RetentionPolicy().Get(ctx)
	requireNoError(t, err)
	if initial.Validate() != nil || initial.InstitutionID != bootstrap.Institution.ID || initial.Revision != 1 ||
		initial.RetentionPolicySettings != (model.RetentionPolicySettings{}) ||
		!initial.CreatedAt.Equal(initial.UpdatedAt) || !reflect.DeepEqual(initial, bootstrap.RetentionPolicy) {
		t.Fatalf("bootstrap retention policy = %#v, bootstrap result = %#v", initial, bootstrap.RetentionPolicy)
	}

	settings := model.RetentionPolicySettings{SubmissionRetentionDays: 365, IntegrityRetentionDays: 180,
		AuditRetentionDays: 730, ExportRetentionDays: 1, DeletionGraceDays: 7}
	principal := saveRecordsPrincipal(t, ctx, stores, bootstrap.Administrator.ID, true)
	replacement := &store.RetentionPolicyReplacement{RetentionMutation: store.RetentionMutation{Principal: principal, RecentAuthenticationTTL: time.Hour},
		ExpectedRevision: initial.Revision, Settings: settings}
	prepareRetentionPolicyAttempt(t, ctx, stores, bootstrap.Institution.ID, replacement)
	command := retentionPolicyCommand(replacement.Principal.UserID, "replace")
	result, err := stores.RetentionPolicy().Replace(ctx, replacement, command)
	if err != nil {
		t.Fatalf("initial retention replacement: %v (cause=%v, initial time=%s)", err, errors.Unwrap(err), initial.UpdatedAt)
	}
	if result.Replayed || !result.Changed || result.Policy.Revision != 2 || result.Policy.RetentionPolicySettings != settings ||
		!result.Policy.CreatedAt.Equal(initial.CreatedAt) || result.Policy.UpdatedAt.Before(initial.UpdatedAt) {
		t.Fatalf("replacement = %#v", result)
	}
	assertRetentionPolicyAudit(t, ctx, stores, replacement, result, "")
	assertRetentionPolicyOutcome(t, ctx, stores, command, true)
	persisted, err := stores.RetentionPolicy().Get(ctx)
	requireNoError(t, err)
	if !reflect.DeepEqual(persisted, result.Policy) {
		t.Fatalf("persisted policy = %#v, result = %#v", persisted, result.Policy)
	}

	replayInput := &store.RetentionPolicyReplacement{RetentionMutation: replacement.RetentionMutation, ExpectedRevision: initial.Revision, Settings: settings}
	prepareRetentionPolicyAttempt(t, ctx, stores, bootstrap.Institution.ID, replayInput)
	replay, err := stores.RetentionPolicy().Replace(ctx, replayInput, command)
	if err != nil {
		t.Fatalf("exact retention replay: %v (cause=%v)", err, errors.Unwrap(err))
	}
	if !replay.Replayed || !replay.Changed || !reflect.DeepEqual(replay.Policy, result.Policy) {
		t.Fatalf("exact replay = %#v, original = %#v", replay, result)
	}
	assertRetentionPolicyAudit(t, ctx, stores, replayInput, replay, replacement.AuditEventID)

	conflictInput := *replayInput
	conflictInput.Settings.ExportRetentionDays++
	prepareRetentionPolicyAttempt(t, ctx, stores, bootstrap.Institution.ID, &conflictInput)
	conflictCommand := *command
	conflictCommand.Fingerprint = sha256.Sum256([]byte("different retention policy content"))
	_, err = stores.RetentionPolicy().Replace(ctx, &conflictInput, &conflictCommand)
	var keyConflict *store.ErrIdempotencyConflict
	if !errors.As(err, &keyConflict) {
		t.Fatalf("same key with different content error = %v", err)
	}
	assertRetentionPolicyAttemptPending(t, ctx, stores, conflictInput.AuditEventID)

	staleInput := *replayInput
	prepareRetentionPolicyAttempt(t, ctx, stores, bootstrap.Institution.ID, &staleInput)
	staleCommand := retentionPolicyCommand(staleInput.Principal.UserID, "different-key-stale-revision")
	_, err = stores.RetentionPolicy().Replace(ctx, &staleInput, staleCommand)
	var revisionConflict *store.ErrRetentionPolicyRevisionConflict
	if !errors.As(err, &revisionConflict) || revisionConflict.CurrentRevision != result.Policy.Revision {
		t.Fatalf("different key with stale revision error = %v", err)
	}
	assertRetentionPolicyOutcome(t, ctx, stores, staleCommand, false)
	assertRetentionPolicyAttemptPending(t, ctx, stores, staleInput.AuditEventID)

	noOp := &store.RetentionPolicyReplacement{RetentionMutation: replacement.RetentionMutation, ExpectedRevision: result.Policy.Revision, Settings: settings}
	prepareRetentionPolicyAttempt(t, ctx, stores, bootstrap.Institution.ID, noOp)
	noOpCommand := retentionPolicyCommand(noOp.Principal.UserID, "no-op")
	noOpResult, err := stores.RetentionPolicy().Replace(ctx, noOp, noOpCommand)
	if err != nil {
		t.Fatalf("retention no-op: %v (cause=%v, previous time=%s)", err, errors.Unwrap(err), result.Policy.UpdatedAt)
	}
	if noOpResult.Replayed || noOpResult.Changed || !reflect.DeepEqual(noOpResult.Policy, result.Policy) {
		t.Fatalf("no-op changed revision or timestamps: %#v, prior=%#v", noOpResult, result.Policy)
	}
	assertRetentionPolicyAudit(t, ctx, stores, noOp, noOpResult, "")
	assertRetentionPolicyOutcome(t, ctx, stores, noOpCommand, true)

	missingAudit := &store.RetentionPolicyReplacement{RetentionMutation: replacement.RetentionMutation, ExpectedRevision: result.Policy.Revision,
		Settings: settings}
	missingAudit.AuditEventID, missingAudit.AuditAt = model.NewId(), model.GetMillis()
	missingAudit.Settings.SubmissionRetentionDays++
	missingAuditCommand := retentionPolicyCommand(missingAudit.Principal.UserID, "missing-audit")
	if _, err = stores.RetentionPolicy().Replace(ctx, missingAudit, missingAuditCommand); err == nil {
		t.Fatal("replacement succeeded without an audit attempt")
	}
	assertRetentionPolicyOutcome(t, ctx, stores, missingAuditCommand, false)
	persisted, err = stores.RetentionPolicy().Get(ctx)
	requireNoError(t, err)
	if !reflect.DeepEqual(persisted, result.Policy) {
		t.Fatalf("policy survived audit rollback: %#v, prior=%#v", persisted, result.Policy)
	}
	// Retrying the same command after a fresh audit proves the failed attempt
	// did not reserve an outcome or consume the expected revision.
	prepareRetentionPolicyAttempt(t, ctx, stores, bootstrap.Institution.ID, missingAudit)
	recovered, err := stores.RetentionPolicy().Replace(ctx, missingAudit, missingAuditCommand)
	if err != nil {
		t.Fatalf("retention recovery after missing audit: %v (cause=%v)", err, errors.Unwrap(err))
	}
	if recovered.Replayed || !recovered.Changed || recovered.Policy.Revision != result.Policy.Revision+1 {
		t.Fatalf("recovery after missing audit = %#v", recovered)
	}
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
	requireNoError(t, err)
	original := &store.RetentionPolicyReplacement{RetentionMutation: store.RetentionMutation{Principal: principal, RecentAuthenticationTTL: time.Hour},
		ExpectedRevision: current.Revision, Settings: current.RetentionPolicySettings}
	original.Settings.AuditRetentionDays++
	prepareRetentionPolicyAttempt(t, ctx, stores, bootstrap.Institution.ID, original)
	originalCommand := retentionPolicyCommand(principal.UserID, "assurance-original")
	first, err := stores.RetentionPolicy().Replace(ctx, original, originalCommand)
	requireNoError(t, err)

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
			saved, credentials, saveErr := stores.Session().Save(ctx, testSessionCreation(t, ctx, stores, session, credentials, 10))
			requireNoError(t, saveErr)
			// Keep the request snapshot strong and fresh. The stored Session and
			// exact credential must decide even when a caller has stale facts.
			stale := principal
			stale.SessionID, stale.CredentialID = saved.ID, model.PrincipalCredentialID(credentials[0].ID)
			if state == "revoked Session" {
				_, revokeErr := stores.Session().Revoke(ctx, saved.ID.String(), principal.UserID.String(), model.GetMillis(), model.SessionRevocationUserLogout)
				requireNoError(t, revokeErr)
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
					if result != nil || !errors.As(replaceErr, &conflict) || conflict.Resource != "authorization" {
						t.Fatalf("stale credential or assurance admitted %s: result=%#v error=%v", operation, result, replaceErr)
					}
					assertRetentionPolicyAttemptPending(t, ctx, stores, input.AuditEventID)
					assertRetentionPolicyOutcome(t, ctx, stores, command, operation == "replay")
					persisted, getErr := stores.RetentionPolicy().Get(ctx)
					requireNoError(t, getErr)
					if !reflect.DeepEqual(persisted, first.Policy) {
						t.Fatalf("rejected %s changed policy: %#v", operation, persisted)
					}
				})
			}
		})
	}
}

func testRetentionPolicyReplacementInstitution(t *testing.T, ctx context.Context, stores store.Store,
	bootstrap *model.InstallationBootstrapResult,
) {
	t.Helper()
	requireNoError(t, stores.Institution().Archive(ctx, bootstrap.Institution.ID.String(), model.GetMillis()))
	if policy, err := stores.RetentionPolicy().Get(ctx); !store.IsNotFound(err) || policy != nil {
		t.Fatalf("Get without an active Institution = %#v, %v", policy, err)
	}
	replacement, err := stores.Institution().Save(ctx, &model.Institution{Name: "replacement-retention-institution", DisplayName: "Replacement Institution"})
	requireNoError(t, err)
	policy, err := stores.RetentionPolicy().Get(ctx)
	requireNoError(t, err)
	if policy.InstitutionID != replacement.ID || policy.Revision != 1 || policy.RetentionPolicySettings != (model.RetentionPolicySettings{}) ||
		!policy.CreatedAt.Equal(replacement.CreatedAt) || !policy.UpdatedAt.Equal(policy.CreatedAt) {
		t.Fatalf("replacement Institution inherited archived policy: %#v, Institution=%#v", policy, replacement)
	}
}

func testRetentionPolicyConcurrentEditors(t *testing.T, ctx context.Context, stores store.Store,
	bootstrap *model.InstallationBootstrapResult, current *model.RetentionPolicy,
) {
	t.Helper()
	other := saveUserWithPassword(t, ctx, stores)
	_, err := stores.RoleBinding().Save(ctx, &model.RoleBinding{UserID: other.ID, RoleID: bootstrap.Role.ID,
		ScopeType: model.RoleScopeInstitution, ScopeID: bootstrap.Institution.ID.String(), StartsAt: model.NowUTC().Add(-time.Hour)})
	requireNoError(t, err)
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
			t.Fatal("concurrent retention editors did not complete:", ctx.Err())
		}
		input, command := inputs[completedEdit.index], commands[completedEdit.index]
		if completedEdit.err == nil {
			winners++
			result := completedEdit.result
			if result.Replayed || !result.Changed || result.Policy.Revision != current.Revision+1 || result.Policy.RetentionPolicySettings != input.Settings {
				t.Fatalf("concurrent winner = %#v", result)
			}
			winner = result.Policy
			assertRetentionPolicyAudit(t, ctx, stores, input, result, "")
			assertRetentionPolicyOutcome(t, ctx, stores, command, true)
			continue
		}
		var conflict *store.ErrRetentionPolicyRevisionConflict
		if !errors.As(completedEdit.err, &conflict) || conflict.CurrentRevision != current.Revision+1 {
			t.Fatalf("concurrent edit error = %v", completedEdit.err)
		}
		losers++
		assertRetentionPolicyAttemptPending(t, ctx, stores, input.AuditEventID)
		assertRetentionPolicyOutcome(t, ctx, stores, command, false)
	}
	if winners != 1 || losers != 1 {
		t.Fatalf("concurrent edits: winners=%d conflicts=%d", winners, losers)
	}
	persisted, err := stores.RetentionPolicy().Get(ctx)
	requireNoError(t, err)
	if !reflect.DeepEqual(persisted, winner) {
		t.Fatalf("persisted concurrent winner = %#v, returned=%#v", persisted, winner)
	}
}

func testRetentionPolicyActiveAdministratorGuard(t *testing.T, ctx context.Context, stores store.Store,
	bootstrap *model.InstallationBootstrapResult,
) {
	t.Helper()
	current, err := stores.RetentionPolicy().Get(ctx)
	requireNoError(t, err)
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
				binding, saveErr := stores.RoleBinding().Save(ctx, &model.RoleBinding{UserID: actor.ID, RoleID: bootstrap.Role.ID,
					ScopeType: model.RoleScopeInstitution, ScopeID: bootstrap.Institution.ID.String(), StartsAt: model.NowUTC().Add(-time.Hour)})
				requireNoError(t, saveErr)
				if test.ended {
					_, endErr := stores.RoleBinding().End(ctx, binding.ID.String(), model.GetMillis())
					requireNoError(t, endErr)
				}
			}
			principal := saveRecordsPrincipal(t, ctx, stores, actor.ID, true)
			if test.disabled {
				audit := saveUserProfileAuditAttempt(t, ctx, stores, actor.ID.String())
				_, disableErr := stores.User().SetDisabledWithAudit(ctx, userDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
					ID: actor.ID.String(), ExpectedRevision: actor.Revision, Disabled: true, ChangedAt: model.GetMillis(),
					RevocationReason: model.SessionRevocationAccountDisabled, AuditEventID: audit.ID.String(), AuditAt: model.MillisFromTime(audit.CreatedAt),
				}))
				requireNoError(t, disableErr)
			}
			input := &store.RetentionPolicyReplacement{RetentionMutation: store.RetentionMutation{Principal: principal, RecentAuthenticationTTL: time.Hour}, ExpectedRevision: current.Revision, Settings: current.RetentionPolicySettings}
			input.Settings.DeletionGraceDays++
			prepareRetentionPolicyAttempt(t, ctx, stores, bootstrap.Institution.ID, input)
			command := retentionPolicyCommand(actor.ID, "unauthorized")
			_, replaceErr := stores.RetentionPolicy().Replace(ctx, input, command)
			var conflict *store.ErrConflict
			if !errors.As(replaceErr, &conflict) || conflict.Resource != "authorization" {
				t.Fatalf("inactive administrator replacement error = %v", replaceErr)
			}
			assertRetentionPolicyAttemptPending(t, ctx, stores, input.AuditEventID)
			assertRetentionPolicyOutcome(t, ctx, stores, command, false)
			persisted, getErr := stores.RetentionPolicy().Get(ctx)
			requireNoError(t, getErr)
			if !reflect.DeepEqual(persisted, current) {
				t.Fatalf("unauthorized edit changed policy = %#v, prior=%#v", persisted, current)
			}
		})
	}
}

func retentionPolicyCommand(actorID model.UserID, identity string) *store.CommandIdempotency {
	return &store.CommandIdempotency{UserID: actorID, Operation: "retention_policy.replace.v1",
		KeyDigest: sha256.Sum256([]byte("retention-policy-key:" + identity)), FingerprintVersion: 1,
		Fingerprint: sha256.Sum256([]byte("retention-policy-command:" + identity)), OutcomeVersion: 1,
		Retention: time.Hour, Wait: time.Second}
}

func prepareRetentionPolicyAttempt(t *testing.T, ctx context.Context, stores store.Store, institutionID model.InstitutionID,
	replacement *store.RetentionPolicyReplacement,
) {
	t.Helper()
	event, err := stores.Audit().Save(ctx, &model.AuditEvent{ActorID: replacement.Principal.UserID, SessionID: replacement.Principal.SessionID,
		Action: string(model.ActionRetentionPolicyManage), Resource: model.Resource{Type: model.ResourceInstitution, ID: institutionID.String()},
		ScopeType: model.RoleScopeInstitution, ScopeID: institutionID.String(), Status: model.AuditStatusAttempt,
		NodeID: "store-test", ClientType: string(model.SessionClientWeb), AuthMethod: "password"})
	requireNoError(t, err)
	replacement.AuditEventID, replacement.AuditAt = event.ID.String(), model.MillisFromTime(event.CreatedAt)
}

func assertRetentionPolicyAudit(t *testing.T, ctx context.Context, stores store.Store, input *store.RetentionPolicyReplacement,
	outcome *store.RetentionPolicyReplacementResult, originalAuditID string,
) {
	t.Helper()
	event, err := stores.Audit().Get(ctx, input.AuditEventID)
	requireNoError(t, err)
	if event.Status != model.AuditStatusSuccess {
		t.Fatalf("retention audit status = %q", event.Status)
	}
	var result map[string]any
	requireNoError(t, json.Unmarshal(event.Result, &result))
	if result["operation"] != "replace" || result["changed"] != outcome.Changed ||
		result["expected_revision"] != float64(input.ExpectedRevision) || result["resulting_revision"] != float64(outcome.Policy.Revision) ||
		result["submission_retention_days"] != float64(outcome.Policy.SubmissionRetentionDays) ||
		result["integrity_retention_days"] != float64(outcome.Policy.IntegrityRetentionDays) ||
		result["audit_retention_days"] != float64(outcome.Policy.AuditRetentionDays) ||
		result["export_retention_days"] != float64(outcome.Policy.ExportRetentionDays) ||
		result["deletion_grace_days"] != float64(outcome.Policy.DeletionGraceDays) || result["automatic_deletion_enabled"] != false {
		t.Fatalf("retention audit result = %#v", result)
	}
	if originalAuditID != "" && (result["idempotency_replayed"] != true || result["original_audit_event_id"] != originalAuditID) {
		t.Fatalf("replay audit lost original identity: %#v", result)
	}
}

func assertRetentionPolicyAttemptPending(t *testing.T, ctx context.Context, stores store.Store, auditID string) {
	t.Helper()
	event, err := stores.Audit().Get(ctx, auditID)
	requireNoError(t, err)
	if event.Status != model.AuditStatusAttempt {
		t.Fatalf("failed replacement completed audit: %#v", event)
	}
}

func assertRetentionPolicyOutcome(t *testing.T, ctx context.Context, stores store.Store, command *store.CommandIdempotency, want bool) {
	t.Helper()
	found, err := stores.CommandOutcome().Has(ctx, command)
	requireNoError(t, err)
	if found != want {
		t.Fatalf("retention command outcome exists = %v, want %v", found, want)
	}
}
