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

func testMailRekeyStore(t *testing.T, ctx context.Context, ss store.Store, user *model.User, institution *model.Institution) {
	t.Helper()
	oldInput := mailTestEnqueueFixture(t, user, institution, model.NowUTC().Add(-time.Minute))
	oldDelivery, err := ss.Mail().EnqueueTest(ctx, oldInput)
	require.NoError(t, err)
	rollback := newMailRekeyStartFixture(t, "22222222222222222222222222222222", "11111111111111111111111111111111")
	rollback.AuditEventID, rollback.AuditAt = model.NewAuditEventID(), model.GetMillis()
	_, err = ss.Mail().StartRekey(ctx, rollback)
	require.Error(t, err, "invalid rekey audit did not abort start")
	_, err = ss.Job().Get(ctx, rollback.Job.ID)
	require.True(t, store.IsNotFound(err), "rolled-back rekey Job error")
	state, err := ss.Mail().InspectKeyState(ctx)
	require.NoError(t, err)
	require.Empty(t, state.RequiredPrimaryKeyID)
	operation := mailRekeyStartFixture(t, ctx, ss, user, institution, "22222222222222222222222222222222", "11111111111111111111111111111111")
	started, err := ss.Mail().StartRekey(ctx, operation)
	require.NoError(t, err)
	require.Equal(t, operation.Job.ID, started.JobID)
	require.Equal(t, operation.PrimaryKeyID, started.PrimaryKeyID)
	require.Equal(t, operation.RetiringKeyID, started.RetiringKeyID)
	assertMailRekeyStartAudit(t, ctx, ss, operation)
	concurrent := mailRekeyStartFixture(t, ctx, ss, user, institution,
		"33333333333333333333333333333333", operation.PrimaryKeyID)
	_, err = ss.Mail().StartRekey(ctx, concurrent)
	require.True(t, store.IsConflict(err), "concurrent StartRekey() error")
	assertMailRekeyAttemptPending(t, ctx, ss, concurrent.AuditEventID)
	request := &store.MailRekeyTargetPageRequest{JobID: operation.Job.ID, PrimaryKeyID: operation.PrimaryKeyID, Limit: 500}
	found, replaced := false, 0
	var replacement *store.MailRekeyReplacement
	for {
		page, listErr := ss.Mail().ListRekeyTargets(ctx, request)
		require.NoError(t, listErr)
		for _, target := range page.Targets {
			require.Equal(t, store.MailRekeyTargetDelivery, target.Kind)
			require.Equal(t, operation.RetiringKeyID, target.KeyID)
			found = found || target.ID == oldDelivery.ID.String()
			replacement = &store.MailRekeyReplacement{JobID: operation.Job.ID, Kind: target.Kind,
				ID: target.ID, ExpectedKeyID: target.KeyID, PrimaryKeyID: operation.PrimaryKeyID,
				EncryptedPayload: json.RawMessage(`{"version":1,"key_id":"22222222222222222222222222222222","ciphertext":"rotated"}`)}
			applied, replaceErr := ss.Mail().ReplaceRekeyTarget(ctx, replacement)
			require.NoError(t, replaceErr)
			require.True(t, applied, "ReplaceRekeyTarget() did not apply")
			replaced++
			request.AfterKind, request.AfterID = target.Kind, target.ID
		}
		if !page.More {
			break
		}
	}
	require.True(t, found, "rekey found")
	require.NotEqual(t, 0, replaced, "rekey found")
	require.NotNil(t, replacement, "rekey found")
	applied, err := ss.Mail().ReplaceRekeyTarget(ctx, replacement)
	require.NoError(t, err)
	require.False(t, applied, "ReplaceRekeyTarget() was not idempotent")
	proof, err := ss.Mail().ProveRekey(ctx, &store.MailRekeyProofRequest{JobID: operation.Job.ID,
		PrimaryKeyID: operation.PrimaryKeyID, RetiringKeyID: operation.RetiringKeyID})
	require.NoError(t, err)
	require.Equal(t, int64(0), proof.NonPrimaryReferences)
	require.Equal(t, int64(0), proof.RetiringReferences)
	require.True(t, proof.RetirementSafe)

	wrongPrimary := mailTestEnqueueFixture(t, user, institution, model.NowUTC())
	_, err = ss.Mail().EnqueueTest(ctx, wrongPrimary)
	require.True(t, store.IsConflict(err), "old-primary EnqueueTest() error")

	claimToken := mustClaimToken(t)
	claim, err := ss.Job().ClaimNext(ctx, &store.JobClaimRequest{Types: []model.JobType{model.JobTypeMailRekey},
		NodeID: "mail-rekey-store-test", ClaimToken: claimToken, LeaseDuration: time.Minute})
	require.NoError(t, err)
	require.Equal(t, operation.Job.ID, claim.Job.ID, "claimed rekey Job")
	_, err = ss.Job().Complete(ctx, &store.JobCompletion{AttemptID: claim.Attempt.ID, ClaimToken: claimToken,
		Kind: store.JobCompletionSucceeded, Result: json.RawMessage(`{"primary_key_id":"22222222222222222222222222222222","retiring_key_id":"11111111111111111111111111111111","processed":1,"reencrypted":1,"non_primary_references":0,"retiring_references":0,"retirement_safe":true}`)})
	require.NoError(t, err)
	state, err = ss.Mail().InspectKeyState(ctx)
	require.NoError(t, err)
	require.Equal(t, operation.PrimaryKeyID, state.RequiredPrimaryKeyID)
	require.True(t, state.PrimaryPromotionAllowed)
	cleanup, err := ss.Job().DeleteTerminalHistory(ctx, &store.JobHistoryCleanup{ExcludeJobID: model.NewJobID(),
		Policies: []store.JobRetentionPolicy{{Type: model.JobTypeMailRekey, SucceededCanceledAge: time.Nanosecond,
			FailedAge: time.Nanosecond}}, Limit: 10})
	require.NoError(t, err)
	require.Equal(t, int64(0), cleanup.Deleted)
	_, err = ss.Job().Get(ctx, operation.Job.ID)
	require.NoError(t, err)

	// A normal restart keeps B as primary; a staged mixed-version restart may
	// promote C only after the A-to-B proof above, while retaining B for reads.
	betweenRotations := mailTestEnqueueFixture(t, user, institution, model.NowUTC())
	betweenRotations.Delivery.EncryptedPayload = json.RawMessage(`{"version":1,"key_id":"22222222222222222222222222222222","ciphertext":"between-rotations"}`)
	_, err = ss.Mail().EnqueueTest(ctx, betweenRotations)
	require.NoError(t, err)

	next := mailRekeyStartFixture(t, ctx, ss, user, institution, "33333333333333333333333333333333", operation.PrimaryKeyID)
	_, err = ss.Mail().StartRekey(ctx, next)
	require.NoError(t, err)
	assertMailRekeyStartAudit(t, ctx, ss, next)
	state, err = ss.Mail().InspectKeyState(ctx)
	require.NoError(t, err)
	require.Equal(t, next.PrimaryKeyID, state.RequiredPrimaryKeyID)
	require.False(t, state.PrimaryPromotionAllowed)
	replacement.ExpectedKeyID = operation.PrimaryKeyID
	replacement.PrimaryKeyID = next.PrimaryKeyID
	replacement.EncryptedPayload = json.RawMessage(`{"version":1,"key_id":"33333333333333333333333333333333","ciphertext":"newer"}`)
	_, err = ss.Mail().ReplaceRekeyTarget(ctx, replacement)
	require.True(t, store.IsConflict(err), "stale fenced replacement error")
	_, err = ss.Mail().EnqueueTest(ctx, betweenRotations)
	require.True(t, store.IsConflict(err), "B-primary enqueue after C fence error")
	page, err := ss.Mail().ListRekeyTargets(ctx, &store.MailRekeyTargetPageRequest{JobID: next.Job.ID,
		PrimaryKeyID: next.PrimaryKeyID, Limit: 500})
	require.NoError(t, err)
	require.NotEmpty(t, page.Targets, "B-to-C rekey found no B-primary payloads")
	for _, target := range page.Targets {
		document := json.RawMessage(`{"version":1,"key_id":"33333333333333333333333333333333","ciphertext":"rotated-to-c"}`)
		applied, replaceErr := ss.Mail().ReplaceRekeyTarget(ctx, &store.MailRekeyReplacement{JobID: next.Job.ID,
			Kind: target.Kind, ID: target.ID, ExpectedKeyID: target.KeyID, PrimaryKeyID: next.PrimaryKeyID,
			EncryptedPayload: document})
		require.NoError(t, replaceErr)
		require.True(t, applied, "B-to-C replacement did not apply for %#v", target)
	}
	proof, err = ss.Mail().ProveRekey(ctx, &store.MailRekeyProofRequest{JobID: next.Job.ID,
		PrimaryKeyID: next.PrimaryKeyID, RetiringKeyID: next.RetiringKeyID})
	require.NoError(t, err)
	require.True(t, proof.RetirementSafe)
	require.Equal(t, int64(0), proof.NonPrimaryReferences)
	require.Equal(t, int64(0), proof.RetiringReferences)
	cleanup, err = ss.Job().DeleteTerminalHistory(ctx, &store.JobHistoryCleanup{ExcludeJobID: model.NewJobID(),
		Policies: []store.JobRetentionPolicy{{Type: model.JobTypeMailRekey, SucceededCanceledAge: time.Nanosecond,
			FailedAge: time.Nanosecond}}, Limit: 10})
	require.NoError(t, err)
	require.Equal(t, int64(1), cleanup.Deleted)
	nextToken := mustClaimToken(t)
	nextClaim, err := ss.Job().ClaimNext(ctx, &store.JobClaimRequest{Types: []model.JobType{model.JobTypeMailRekey},
		NodeID: "mail-rekey-proof-failure", ClaimToken: nextToken, LeaseDuration: time.Minute})
	require.NoError(t, err)
	require.Equal(t, next.Job.ID, nextClaim.Job.ID, "claimed B-to-C rekey Job")
	_, err = ss.Job().Complete(ctx, &store.JobCompletion{AttemptID: nextClaim.Attempt.ID, ClaimToken: nextToken,
		Kind: store.JobCompletionPermanentFailure, PublicErrorCode: "mail.rekey.invariant_failed"})
	require.NoError(t, err)
	unproven := mailRekeyStartFixture(t, ctx, ss, user, institution,
		"44444444444444444444444444444444", next.PrimaryKeyID)
	_, err = ss.Mail().StartRekey(ctx, unproven)
	require.True(t, store.IsConflict(err), "unproven primary promotion error")
	assertMailRekeyAttemptPending(t, ctx, ss, unproven.AuditEventID)
}

func assertMailRekeyStartAudit(t *testing.T, ctx context.Context, ss store.Store, input *store.MailRekeyStart) {
	t.Helper()
	event, err := ss.Audit().Get(ctx, input.AuditEventID)
	require.NoError(t, err)
	var result struct {
		Operation     string `json:"operation"`
		JobID         string `json:"job_id"`
		PrimaryKeyID  string `json:"primary_key_id"`
		RetiringKeyID string `json:"retiring_key_id"`
	}
	require.NoError(t, json.Unmarshal(event.Result, &result), "mail rekey start audit = %#v result=%#v", event, result)
	require.Equal(t, model.AuditStatusSuccess, event.Status, "mail rekey start audit = %#v result=%#v", event, result)
	require.Equal(t, "start_rekey", result.Operation, "mail rekey start audit = %#v result=%#v", event, result)
	require.Equal(t, input.Job.ID.String(), result.JobID, "mail rekey start audit = %#v result=%#v", event, result)
	require.Equal(t, input.PrimaryKeyID, result.PrimaryKeyID, "mail rekey start audit = %#v result=%#v", event, result)
	require.Equal(t, input.RetiringKeyID, result.RetiringKeyID, "mail rekey start audit = %#v result=%#v", event, result)
}

func assertMailRekeyAttemptPending(t *testing.T, ctx context.Context, ss store.Store, auditID model.AuditEventID) {
	t.Helper()
	event, err := ss.Audit().Get(ctx, auditID)
	require.NoError(t, err)
	require.Equal(t, model.AuditStatusAttempt, event.Status)
}

func mailRekeyStartFixture(t *testing.T, ctx context.Context, ss store.Store, user *model.User, institution *model.Institution, primaryKeyID, retiringKeyID string) *store.MailRekeyStart {
	t.Helper()
	input := newMailRekeyStartFixture(t, primaryKeyID, retiringKeyID)
	attempt, err := ss.Audit().Save(ctx, &model.AuditEvent{ActorID: user.ID, Action: string(model.ActionMailRekey),
		Resource:  model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()},
		ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(), Status: model.AuditStatusAttempt,
		NodeID: "store-test", ClientType: string(model.SessionClientWeb), AuthMethod: "password"})
	require.NoError(t, err)
	input.AuditEventID, input.AuditAt = attempt.ID, model.GetMillis()
	return input
}

func newMailRekeyStartFixture(t *testing.T, primaryKeyID, retiringKeyID string) *store.MailRekeyStart {
	t.Helper()
	at := model.NowUTC()
	command, err := json.Marshal(map[string]string{"primary_key_id": primaryKeyID, "retiring_key_id": retiringKeyID})
	require.NoError(t, err)
	job, err := model.NewJob(model.NewJobID(), model.JobTypeMailRekey, command,
		"mail-rekey:"+primaryKeyID+":"+retiringKeyID, at, at, 10)
	require.NoError(t, err)
	return &store.MailRekeyStart{PrimaryKeyID: primaryKeyID, RetiringKeyID: retiringKeyID, Job: job}
}
