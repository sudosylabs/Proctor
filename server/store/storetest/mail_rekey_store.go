// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func testMailRekeyStore(t *testing.T, ctx context.Context, ss store.Store, user *model.User, institution *model.Institution) {
	t.Helper()
	oldInput := mailTestEnqueueFixture(t, user, institution, model.NowUTC().Add(-time.Minute))
	oldDelivery, err := ss.Mail().EnqueueTest(ctx, oldInput)
	requireNoError(t, err)
	rollback := newMailRekeyStartFixture(t, "22222222222222222222222222222222", "11111111111111111111111111111111")
	rollback.AuditEventID, rollback.AuditAt = model.NewAuditEventID().String(), model.GetMillis()
	if _, err = ss.Mail().StartRekey(ctx, rollback); err == nil {
		t.Fatal("invalid rekey audit did not abort start")
	}
	if _, err = ss.Job().Get(ctx, rollback.Job.ID); !store.IsNotFound(err) {
		t.Fatalf("rolled-back rekey Job error = %v", err)
	}
	state, err := ss.Mail().InspectKeyState(ctx)
	requireNoError(t, err)
	if state.RequiredPrimaryKeyID != "" {
		t.Fatalf("rolled-back rekey installed primary fence = %#v", state)
	}
	operation := mailRekeyStartFixture(t, ctx, ss, user, institution, "22222222222222222222222222222222", "11111111111111111111111111111111")
	started, err := ss.Mail().StartRekey(ctx, operation)
	requireNoError(t, err)
	if started.JobID != operation.Job.ID || started.PrimaryKeyID != operation.PrimaryKeyID || started.RetiringKeyID != operation.RetiringKeyID {
		t.Fatalf("StartRekey() = %#v", started)
	}
	assertMailRekeyStartAudit(t, ctx, ss, operation)
	concurrent := mailRekeyStartFixture(t, ctx, ss, user, institution,
		"33333333333333333333333333333333", operation.PrimaryKeyID)
	if _, err = ss.Mail().StartRekey(ctx, concurrent); !store.IsConflict(err) {
		t.Fatalf("concurrent StartRekey() error = %v", err)
	}
	assertMailRekeyAttemptPending(t, ctx, ss, concurrent.AuditEventID)
	request := &store.MailRekeyTargetPageRequest{JobID: operation.Job.ID, PrimaryKeyID: operation.PrimaryKeyID, Limit: 500}
	found, replaced := false, 0
	var replacement *store.MailRekeyReplacement
	for {
		page, listErr := ss.Mail().ListRekeyTargets(ctx, request)
		requireNoError(t, listErr)
		for _, target := range page.Targets {
			if target.Kind != store.MailRekeyTargetDelivery || target.KeyID != operation.RetiringKeyID {
				t.Fatalf("ListRekeyTargets() target = %#v", target)
			}
			found = found || target.ID == oldDelivery.ID.String()
			replacement = &store.MailRekeyReplacement{JobID: operation.Job.ID, Kind: target.Kind,
				ID: target.ID, ExpectedKeyID: target.KeyID, PrimaryKeyID: operation.PrimaryKeyID,
				EncryptedPayload: json.RawMessage(`{"version":1,"key_id":"22222222222222222222222222222222","ciphertext":"rotated"}`)}
			applied, replaceErr := ss.Mail().ReplaceRekeyTarget(ctx, replacement)
			requireNoError(t, replaceErr)
			if !applied {
				t.Fatal("ReplaceRekeyTarget() did not apply")
			}
			replaced++
			request.AfterKind, request.AfterID = target.Kind, target.ID
		}
		if !page.More {
			break
		}
	}
	if !found || replaced == 0 || replacement == nil {
		t.Fatalf("rekey found=%t replaced=%d", found, replaced)
	}
	applied, err := ss.Mail().ReplaceRekeyTarget(ctx, replacement)
	requireNoError(t, err)
	if applied {
		t.Fatal("ReplaceRekeyTarget() was not idempotent")
	}
	proof, err := ss.Mail().ProveRekey(ctx, &store.MailRekeyProofRequest{JobID: operation.Job.ID,
		PrimaryKeyID: operation.PrimaryKeyID, RetiringKeyID: operation.RetiringKeyID})
	requireNoError(t, err)
	if proof.NonPrimaryReferences != 0 || proof.RetiringReferences != 0 || !proof.RetirementSafe {
		t.Fatalf("ProveRekey() = %#v", proof)
	}

	wrongPrimary := mailTestEnqueueFixture(t, user, institution, model.NowUTC())
	if _, err = ss.Mail().EnqueueTest(ctx, wrongPrimary); !store.IsConflict(err) {
		t.Fatalf("old-primary EnqueueTest() error = %v", err)
	}

	claimToken := mustClaimToken(t)
	claim, err := ss.Job().ClaimNext(ctx, &store.JobClaimRequest{Types: []model.JobType{model.JobTypeMailRekey},
		NodeID: "mail-rekey-store-test", ClaimToken: claimToken, LeaseDuration: time.Minute})
	requireNoError(t, err)
	if claim.Job.ID != operation.Job.ID {
		t.Fatalf("claimed rekey Job = %s, want %s", claim.Job.ID, operation.Job.ID)
	}
	_, err = ss.Job().Complete(ctx, &store.JobCompletion{AttemptID: claim.Attempt.ID, ClaimToken: claimToken,
		Kind: store.JobCompletionSucceeded, ResultVersion: 1, Result: json.RawMessage(`{"primary_key_id":"22222222222222222222222222222222","retiring_key_id":"11111111111111111111111111111111","processed":1,"reencrypted":1,"non_primary_references":0,"retiring_references":0,"retirement_safe":true}`)})
	requireNoError(t, err)
	state, err = ss.Mail().InspectKeyState(ctx)
	requireNoError(t, err)
	if state.RequiredPrimaryKeyID != operation.PrimaryKeyID || !state.PrimaryPromotionAllowed {
		t.Fatalf("completed rekey key state = %#v", state)
	}
	cleanup, err := ss.Job().DeleteTerminalHistory(ctx, &store.JobHistoryCleanup{ExcludeJobID: model.NewJobID(),
		Policies: []store.JobRetentionPolicy{{Type: model.JobTypeMailRekey, SucceededCanceledAge: time.Nanosecond,
			FailedAge: time.Nanosecond}}, Limit: 10})
	requireNoError(t, err)
	if cleanup.Deleted != 0 {
		t.Fatalf("cleanup deleted the current promotion proof: %#v", cleanup)
	}
	if _, err = ss.Job().Get(ctx, operation.Job.ID); err != nil {
		t.Fatalf("current promotion proof was not retained: %v", err)
	}

	// A normal restart keeps B as primary; a staged mixed-version restart may
	// promote C only after the A-to-B proof above, while retaining B for reads.
	betweenRotations := mailTestEnqueueFixture(t, user, institution, model.NowUTC())
	betweenRotations.Delivery.EncryptedPayload = json.RawMessage(`{"version":1,"key_id":"22222222222222222222222222222222","ciphertext":"between-rotations"}`)
	if _, err = ss.Mail().EnqueueTest(ctx, betweenRotations); err != nil {
		t.Fatalf("B-primary enqueue after A-to-B proof: %v", err)
	}

	next := mailRekeyStartFixture(t, ctx, ss, user, institution, "33333333333333333333333333333333", operation.PrimaryKeyID)
	_, err = ss.Mail().StartRekey(ctx, next)
	requireNoError(t, err)
	assertMailRekeyStartAudit(t, ctx, ss, next)
	state, err = ss.Mail().InspectKeyState(ctx)
	requireNoError(t, err)
	if state.RequiredPrimaryKeyID != next.PrimaryKeyID || state.PrimaryPromotionAllowed {
		t.Fatalf("B-to-C active key state = %#v", state)
	}
	replacement.ExpectedKeyID = operation.PrimaryKeyID
	replacement.PrimaryKeyID = next.PrimaryKeyID
	replacement.EncryptedPayload = json.RawMessage(`{"version":1,"key_id":"33333333333333333333333333333333","ciphertext":"newer"}`)
	if _, err = ss.Mail().ReplaceRekeyTarget(ctx, replacement); !store.IsConflict(err) {
		t.Fatalf("stale fenced replacement error = %v", err)
	}
	if _, err = ss.Mail().EnqueueTest(ctx, betweenRotations); !store.IsConflict(err) {
		t.Fatalf("B-primary enqueue after C fence error = %v", err)
	}
	page, err := ss.Mail().ListRekeyTargets(ctx, &store.MailRekeyTargetPageRequest{JobID: next.Job.ID,
		PrimaryKeyID: next.PrimaryKeyID, Limit: 500})
	requireNoError(t, err)
	if len(page.Targets) == 0 {
		t.Fatal("B-to-C rekey found no B-primary payloads")
	}
	for _, target := range page.Targets {
		document := json.RawMessage(`{"version":1,"key_id":"33333333333333333333333333333333","ciphertext":"rotated-to-c"}`)
		applied, replaceErr := ss.Mail().ReplaceRekeyTarget(ctx, &store.MailRekeyReplacement{JobID: next.Job.ID,
			Kind: target.Kind, ID: target.ID, ExpectedKeyID: target.KeyID, PrimaryKeyID: next.PrimaryKeyID,
			EncryptedPayload: document})
		requireNoError(t, replaceErr)
		if !applied {
			t.Fatalf("B-to-C replacement did not apply for %#v", target)
		}
	}
	proof, err = ss.Mail().ProveRekey(ctx, &store.MailRekeyProofRequest{JobID: next.Job.ID,
		PrimaryKeyID: next.PrimaryKeyID, RetiringKeyID: next.RetiringKeyID})
	requireNoError(t, err)
	if !proof.RetirementSafe || proof.NonPrimaryReferences != 0 || proof.RetiringReferences != 0 {
		t.Fatalf("B-to-C proof = %#v", proof)
	}
	cleanup, err = ss.Job().DeleteTerminalHistory(ctx, &store.JobHistoryCleanup{ExcludeJobID: model.NewJobID(),
		Policies: []store.JobRetentionPolicy{{Type: model.JobTypeMailRekey, SucceededCanceledAge: time.Nanosecond,
			FailedAge: time.Nanosecond}}, Limit: 10})
	requireNoError(t, err)
	if cleanup.Deleted != 1 {
		t.Fatalf("cleanup did not release the superseded promotion proof: %#v", cleanup)
	}
	nextToken := mustClaimToken(t)
	nextClaim, err := ss.Job().ClaimNext(ctx, &store.JobClaimRequest{Types: []model.JobType{model.JobTypeMailRekey},
		NodeID: "mail-rekey-proof-failure", ClaimToken: nextToken, LeaseDuration: time.Minute})
	requireNoError(t, err)
	if nextClaim.Job.ID != next.Job.ID {
		t.Fatalf("claimed B-to-C rekey Job = %s, want %s", nextClaim.Job.ID, next.Job.ID)
	}
	_, err = ss.Job().Complete(ctx, &store.JobCompletion{AttemptID: nextClaim.Attempt.ID, ClaimToken: nextToken,
		Kind: store.JobCompletionPermanentFailure, PublicErrorCode: "mail.rekey.invariant_failed"})
	requireNoError(t, err)
	unproven := mailRekeyStartFixture(t, ctx, ss, user, institution,
		"44444444444444444444444444444444", next.PrimaryKeyID)
	if _, err = ss.Mail().StartRekey(ctx, unproven); !store.IsConflict(err) {
		t.Fatalf("unproven primary promotion error = %v", err)
	}
	assertMailRekeyAttemptPending(t, ctx, ss, unproven.AuditEventID)
}

func assertMailRekeyStartAudit(t *testing.T, ctx context.Context, ss store.Store, input *store.MailRekeyStart) {
	t.Helper()
	event, err := ss.Audit().Get(ctx, input.AuditEventID)
	requireNoError(t, err)
	var result struct {
		Operation     string `json:"operation"`
		JobID         string `json:"job_id"`
		PrimaryKeyID  string `json:"primary_key_id"`
		RetiringKeyID string `json:"retiring_key_id"`
	}
	if json.Unmarshal(event.Result, &result) != nil || event.Status != model.AuditStatusSuccess ||
		result.Operation != "start_rekey" || result.JobID != input.Job.ID.String() ||
		result.PrimaryKeyID != input.PrimaryKeyID || result.RetiringKeyID != input.RetiringKeyID {
		t.Fatalf("mail rekey start audit = %#v result=%#v", event, result)
	}
}

func assertMailRekeyAttemptPending(t *testing.T, ctx context.Context, ss store.Store, auditID string) {
	t.Helper()
	event, err := ss.Audit().Get(ctx, auditID)
	requireNoError(t, err)
	if event.Status != model.AuditStatusAttempt {
		t.Fatalf("mail rekey failure attempt = %#v", event)
	}
}

func mailRekeyStartFixture(t *testing.T, ctx context.Context, ss store.Store, user *model.User, institution *model.Institution, primaryKeyID, retiringKeyID string) *store.MailRekeyStart {
	t.Helper()
	input := newMailRekeyStartFixture(t, primaryKeyID, retiringKeyID)
	attempt, err := ss.Audit().Save(ctx, &model.AuditEvent{ActorID: user.ID, Action: string(model.ActionMailRekey),
		Resource:  model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()},
		ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(), Status: model.AuditStatusAttempt,
		NodeID: "store-test", ClientType: string(model.SessionClientWeb), AuthMethod: "password"})
	requireNoError(t, err)
	input.AuditEventID, input.AuditAt = attempt.ID.String(), model.GetMillis()
	return input
}

func newMailRekeyStartFixture(t *testing.T, primaryKeyID, retiringKeyID string) *store.MailRekeyStart {
	t.Helper()
	at := model.NowUTC()
	command, err := json.Marshal(map[string]string{"primary_key_id": primaryKeyID, "retiring_key_id": retiringKeyID})
	requireNoError(t, err)
	job, err := model.NewJob(model.NewJobID(), model.JobTypeMailRekey, 1, command,
		"mail-rekey:"+primaryKeyID+":"+retiringKeyID, at, at, 10)
	requireNoError(t, err)
	return &store.MailRekeyStart{PrimaryKeyID: primaryKeyID, RetiringKeyID: retiringKeyID, Job: job}
}
