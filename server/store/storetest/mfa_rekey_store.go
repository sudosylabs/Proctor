// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const (
	mfaRekeyFixturePrimary  = "0123456789abcdef0123456789abcdef"
	mfaRekeyFixtureRetiring = "11111111111111111111111111111111"
	mfaRekeyFixtureNext     = "33333333333333333333333333333333"
)

// testMFARekeyStore leaves the fence on mfaRekeyFixtureNext, so it runs last.
func testMFARekeyStore(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	operator := saveUser(t, ctx, ss)
	retiring := seedMFACredentialSealedWith(t, ctx, ss, saveUser(t, ctx, ss).ID, mfaRekeyFixtureRetiring)
	seedMFACredentialSealedWith(t, ctx, ss, saveUser(t, ctx, ss).ID, mfaRekeyFixturePrimary)

	rollback := newMFARekeyStartFixture(t, mfaRekeyFixturePrimary, mfaRekeyFixtureRetiring)
	rollback.AuditEventID, rollback.AuditAt = model.NewAuditEventID(), model.NowUTC()
	_, err := ss.MFA().StartRekey(ctx, rollback)
	require.Error(t, err, "an unknown audit attempt did not abort the start")
	_, err = ss.Job().Get(ctx, rollback.Job.ID)
	require.True(t, store.IsNotFound(err), "rolled-back rekey Job error = %v", err)
	state, err := ss.MFA().InspectKeyState(ctx)
	require.NoError(t, err)
	require.Empty(t, state.RequiredPrimaryKeyID)

	first := mfaRekeyStartFixture(t, ctx, ss, operator, institution, mfaRekeyFixturePrimary, mfaRekeyFixtureRetiring)
	started, err := ss.MFA().StartRekey(ctx, first)
	require.NoError(t, err)
	require.Equal(t, store.MFARekeyOperation{JobID: first.Job.ID, PrimaryKeyID: first.PrimaryKeyID,
		RetiringKeyID: first.RetiringKeyID, CreatedAt: first.Job.CreatedAt}, *started)
	assertRekeyStartAudit(t, ctx, ss, first.AuditEventID, first.Job.ID, first.PrimaryKeyID, first.RetiringKeyID)
	state, err = ss.MFA().InspectKeyState(ctx)
	require.NoError(t, err)
	require.Equal(t, mfaRekeyFixturePrimary, state.RequiredPrimaryKeyID)
	require.Equal(t, first.Job.ID, state.ActiveRekeyJobID)

	concurrent := mfaRekeyStartFixture(t, ctx, ss, operator, institution, mfaRekeyFixtureNext, mfaRekeyFixturePrimary)
	_, err = ss.MFA().StartRekey(ctx, concurrent)
	require.True(t, store.IsConflict(err), "a second rekey while one is queued: error = %v", err)
	assertRekeyAttemptPending(t, ctx, ss, concurrent.AuditEventID)

	_, err = ss.MFA().SeedPending(ctx, mfaCredentialSealedWith(saveUser(t, ctx, ss).ID, mfaRekeyFixtureRetiring))
	require.True(t, store.IsConflict(err), "a secret sealed with the old primary after the fence: error = %v", err)

	page, err := ss.MFA().ListRekeyTargets(ctx, &store.MFARekeyTargetPageRequest{JobID: first.Job.ID, PrimaryKeyID: first.PrimaryKeyID, Limit: 500})
	require.NoError(t, err)
	require.False(t, page.More)
	require.Equal(t, []store.MFARekeyTarget{{CredentialID: retiring.ID, UserID: retiring.UserID, KeyID: mfaRekeyFixtureRetiring,
		EncryptedSecret: retiring.EncryptedSecret}}, page.Targets, "only the secret sealed with another key is a target")
	// #nosec G101 -- A synthetic envelope placeholder, not a credential.
	replacement := &store.MFARekeyReplacement{JobID: first.Job.ID, CredentialID: retiring.ID,
		ExpectedKeyID: mfaRekeyFixtureRetiring, PrimaryKeyID: mfaRekeyFixturePrimary, EncryptedSecret: "resealed-with-primary"}
	applied, err := ss.MFA().ReplaceRekeyTarget(ctx, replacement)
	require.NoError(t, err)
	require.True(t, applied)
	applied, err = ss.MFA().ReplaceRekeyTarget(ctx, replacement)
	require.NoError(t, err)
	require.False(t, applied, "a replayed replacement no longer matches the expected key")
	resealed, err := ss.MFA().GetByUser(ctx, retiring.UserID)
	require.NoError(t, err)
	require.Equal(t, mfaRekeyFixturePrimary, resealed.EncryptionKeyID)
	require.Equal(t, "resealed-with-primary", resealed.EncryptedSecret)
	proof, err := ss.MFA().ProveRekey(ctx, &store.MFARekeyProofRequest{JobID: first.Job.ID, PrimaryKeyID: first.PrimaryKeyID, RetiringKeyID: first.RetiringKeyID})
	require.NoError(t, err)
	require.Equal(t, store.MFARekeyProof{PrimaryKeyID: mfaRekeyFixturePrimary, RetiringKeyID: mfaRekeyFixtureRetiring, RetirementSafe: true}, *proof)

	// A failed rekey proves nothing: the fence may not move to another primary.
	completeMFARekeyJob(t, ctx, ss, first.Job.ID, store.JobCompletionPermanentFailure, nil)
	unproven := mfaRekeyStartFixture(t, ctx, ss, operator, institution, mfaRekeyFixtureNext, mfaRekeyFixturePrimary)
	_, err = ss.MFA().StartRekey(ctx, unproven)
	require.True(t, store.IsConflict(err), "promotion after a failed rekey: error = %v", err)
	assertRekeyAttemptPending(t, ctx, ss, unproven.AuditEventID)

	// Rerunning with the same primary is always allowed and can prove it.
	rerun := mfaRekeyStartFixture(t, ctx, ss, operator, institution, mfaRekeyFixturePrimary, mfaRekeyFixtureRetiring)
	_, err = ss.MFA().StartRekey(ctx, rerun)
	require.NoError(t, err)
	_, err = ss.MFA().ReplaceRekeyTarget(ctx, replacement)
	require.True(t, store.IsConflict(err), "a replacement under the superseded Job: error = %v", err)
	completeMFARekeyJob(t, ctx, ss, rerun.Job.ID, store.JobCompletionSucceeded, json.RawMessage(`{"primary_key_id":"`+mfaRekeyFixturePrimary+
		`","retiring_key_id":"`+mfaRekeyFixtureRetiring+`","processed":0,"resealed":0,"non_primary_references":0,"retiring_references":0,"retirement_safe":true}`))
	cleanup, err := ss.Job().DeleteTerminalHistory(ctx, &store.JobHistoryCleanup{ExcludeJobID: model.NewJobID(),
		Policies: []store.JobRetentionPolicy{{Type: model.JobTypeMFARekey, SucceededCanceledAge: time.Nanosecond, FailedAge: time.Nanosecond}}, Limit: 10})
	require.NoError(t, err)
	require.Equal(t, int64(1), cleanup.Deleted, "history cleanup keeps the fence-holding rekey and removes the superseded one")
	_, err = ss.Job().Get(ctx, rerun.Job.ID)
	require.NoError(t, err)

	state, err = ss.MFA().InspectKeyState(ctx)
	require.NoError(t, err)
	var sealedWithPrimary int64
	for _, usage := range state.Active {
		require.Equal(t, mfaRekeyFixturePrimary, usage.KeyID, "every live secret is sealed with the proven primary")
		sealedWithPrimary = usage.Credentials
	}
	require.GreaterOrEqual(t, sealedWithPrimary, int64(2))
	next := mfaRekeyStartFixture(t, ctx, ss, operator, institution, mfaRekeyFixtureNext, mfaRekeyFixturePrimary)
	_, err = ss.MFA().StartRekey(ctx, next)
	require.NoError(t, err, "promotion after a proven rekey")
	page, err = ss.MFA().ListRekeyTargets(ctx, &store.MFARekeyTargetPageRequest{JobID: next.Job.ID, PrimaryKeyID: next.PrimaryKeyID, Limit: 1})
	require.NoError(t, err)
	require.Len(t, page.Targets, 1)
	require.True(t, page.More, "every live secret now needs the next primary")
	proof, err = ss.MFA().ProveRekey(ctx, &store.MFARekeyProofRequest{JobID: next.Job.ID, PrimaryKeyID: next.PrimaryKeyID, RetiringKeyID: next.RetiringKeyID})
	require.NoError(t, err)
	require.Equal(t, store.MFARekeyProof{PrimaryKeyID: mfaRekeyFixtureNext, RetiringKeyID: mfaRekeyFixturePrimary,
		NonPrimaryReferences: sealedWithPrimary, RetiringReferences: sealedWithPrimary}, *proof, "the retiring key still seals every live secret")
	_, err = ss.MFA().ProveRekey(ctx, &store.MFARekeyProofRequest{JobID: rerun.Job.ID, PrimaryKeyID: rerun.PrimaryKeyID, RetiringKeyID: rerun.RetiringKeyID})
	require.True(t, store.IsConflict(err), "a proof for a superseded Job: error = %v", err)
}

func seedMFACredentialSealedWith(t *testing.T, ctx context.Context, ss store.Store, userID model.UserID, keyID string) *model.MFACredential {
	t.Helper()
	credential, err := ss.MFA().SeedPending(ctx, mfaCredentialSealedWith(userID, keyID))
	require.NoError(t, err)
	return credential
}

func mfaCredentialSealedWith(userID model.UserID, keyID string) *model.MFACredential {
	now := model.NowUTC()
	return &model.MFACredential{UserID: userID, State: model.MFAStatePending, EncryptedSecret: "sealed-with-" + keyID,
		EncryptionKeyID: keyID, PendingExpiresAt: model.OptionalTimeFrom(now.Add(10 * time.Minute)), CreatedAt: now}
}

func completeMFARekeyJob(t *testing.T, ctx context.Context, ss store.Store, jobID model.JobID, kind store.JobCompletionKind, result json.RawMessage) {
	t.Helper()
	token := mustClaimToken(t)
	claim, err := ss.Job().ClaimNext(ctx, &store.JobClaimRequest{Types: []model.JobType{model.JobTypeMFARekey},
		NodeID: "mfa-rekey-store-test", ClaimToken: token, LeaseDuration: time.Minute})
	require.NoError(t, err)
	require.Equal(t, jobID, claim.Job.ID, "claimed rekey Job")
	completion := &store.JobCompletion{AttemptID: claim.Attempt.ID, ClaimToken: token, Kind: kind, Result: result}
	if kind == store.JobCompletionPermanentFailure {
		completion.PublicErrorCode = "mfa.rekey.invariant_failed"
	}
	_, err = ss.Job().Complete(ctx, completion)
	require.NoError(t, err)
}

func assertRekeyStartAudit(t *testing.T, ctx context.Context, ss store.Store, auditID model.AuditEventID, jobID model.JobID, primaryKeyID, retiringKeyID string) {
	t.Helper()
	event, err := ss.Audit().Get(ctx, auditID)
	require.NoError(t, err)
	require.Equal(t, model.AuditStatusSuccess, event.Status)
	var result map[string]string
	require.NoError(t, json.Unmarshal(event.Result, &result))
	require.Equal(t, map[string]string{"operation": "start_rekey", "job_id": jobID.String(),
		"primary_key_id": primaryKeyID, "retiring_key_id": retiringKeyID}, result)
}

func assertRekeyAttemptPending(t *testing.T, ctx context.Context, ss store.Store, auditID model.AuditEventID) {
	t.Helper()
	event, err := ss.Audit().Get(ctx, auditID)
	require.NoError(t, err)
	require.Equal(t, model.AuditStatusAttempt, event.Status)
}

func mfaRekeyStartFixture(t *testing.T, ctx context.Context, ss store.Store, user *model.User, institution *model.Institution, primaryKeyID, retiringKeyID string) *store.MFARekeyStart {
	t.Helper()
	input := newMFARekeyStartFixture(t, primaryKeyID, retiringKeyID)
	attempt, err := ss.Audit().Save(ctx, &model.AuditEvent{ActorID: user.ID, Action: string(model.ActionMFARekey),
		Resource:  model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()},
		ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(), Status: model.AuditStatusAttempt,
		NodeID: "store-test", ClientType: string(model.SessionClientWeb), AuthMethod: "password"})
	require.NoError(t, err)
	input.AuditEventID, input.AuditAt = attempt.ID, model.NowUTC()
	return input
}

func newMFARekeyStartFixture(t *testing.T, primaryKeyID, retiringKeyID string) *store.MFARekeyStart {
	t.Helper()
	at := model.NowUTC()
	command, err := json.Marshal(map[string]string{"primary_key_id": primaryKeyID, "retiring_key_id": retiringKeyID})
	require.NoError(t, err)
	job, err := model.NewJob(model.NewJobID(), model.JobTypeMFARekey, command, "mfa-rekey:"+primaryKeyID+":"+retiringKeyID, at, at, 10)
	require.NoError(t, err)
	return &store.MFARekeyStart{PrimaryKeyID: primaryKeyID, RetiringKeyID: retiringKeyID, Job: job}
}
