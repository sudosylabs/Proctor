// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestMailStore(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	institution := saveInstitution(t, ctx, ss)
	at := model.NowUTC().Add(-time.Minute)

	input := mailTestEnqueueFixture(t, user, institution, at)
	created, err := ss.Mail().EnqueueTest(ctx, input)
	requireNoError(t, err)
	if created.ID != input.Delivery.ID || created.State != model.MailDeliveryQueued || string(created.EncryptedPayload) != string(input.Delivery.EncryptedPayload) {
		t.Fatalf("EnqueueTest() = %#v", created)
	}
	stored, err := ss.Mail().GetDelivery(ctx, created.ID)
	requireNoError(t, err)
	if stored.MessageID != created.MessageID || stored.MaskedRecipient != "o***@example.test" || stored.TargetUserID != user.ID {
		t.Fatalf("GetDelivery() = %#v", stored)
	}
	audits, err := ss.Audit().List(ctx, store.AuditListOptions{
		Action: string(model.ActionMailManage), Limit: 10,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	requireNoError(t, err)
	if len(audits) != 1 || audits[0].Resource.Type != model.ResourceMailDelivery || audits[0].Resource.ID != created.ID.String() {
		t.Fatalf("mail audits = %#v", audits)
	}

	sending, err := ss.Mail().StartDelivery(ctx, created.ID, created.Revision, at.Add(time.Second))
	requireNoError(t, err)
	if sending.State != model.MailDeliverySending || sending.AttemptCount != 1 {
		t.Fatalf("StartDelivery() = %#v", sending)
	}
	if _, err = ss.Mail().StartDelivery(ctx, created.ID, created.Revision, at.Add(2*time.Second)); !store.IsConflict(err) {
		t.Fatalf("StartDelivery(stale) error = %v", err)
	}
	retrying, err := ss.Mail().CompleteDelivery(ctx, &store.MailDeliveryCompletion{DeliveryID: created.ID, ExpectedRevision: sending.Revision, Kind: store.MailDeliveryCompletionRetry, PublicFailureCode: "mail.transport.temporary", At: at.Add(2 * time.Second)})
	requireNoError(t, err)
	if retrying.State != model.MailDeliveryQueued || len(retrying.EncryptedPayload) == 0 {
		t.Fatalf("retry = %#v", retrying)
	}
	second, err := ss.Mail().StartDelivery(ctx, created.ID, retrying.Revision, at.Add(3*time.Second))
	requireNoError(t, err)
	accepted, err := ss.Mail().CompleteDelivery(ctx, &store.MailDeliveryCompletion{DeliveryID: created.ID, ExpectedRevision: second.Revision, Kind: store.MailDeliveryCompletionAccepted, At: at.Add(4 * time.Second)})
	requireNoError(t, err)
	if accepted.State != model.MailDeliveryAccepted || len(accepted.EncryptedPayload) != 0 || !accepted.AcceptedAt.Valid || accepted.MessageID != created.MessageID {
		t.Fatalf("accepted = %#v", accepted)
	}
	expiryInput := mailTestEnqueueFixture(t, user, institution, at.Add(5*time.Second))
	expiryQueued, err := ss.Mail().EnqueueTest(ctx, expiryInput)
	requireNoError(t, err)
	expired, err := ss.Mail().CompleteDelivery(ctx, &store.MailDeliveryCompletion{
		DeliveryID: expiryQueued.ID, ExpectedRevision: expiryQueued.Revision,
		Kind: store.MailDeliveryCompletionExpired, At: expiryQueued.Deadline,
	})
	requireNoError(t, err)
	if expired.State != model.MailDeliverySuppressed || expired.PublicFailureCode != model.MailDeliveryExpiredCode || len(expired.EncryptedPayload) != 0 {
		t.Fatalf("expired = %#v", expired)
	}
	expirySendingInput := mailTestEnqueueFixture(t, user, institution, at.Add(6*time.Second))
	expirySending, err := ss.Mail().EnqueueTest(ctx, expirySendingInput)
	requireNoError(t, err)
	expirySending, err = ss.Mail().StartDelivery(ctx, expirySending.ID, expirySending.Revision, expirySending.CreatedAt.Add(time.Second))
	requireNoError(t, err)
	expired, err = ss.Mail().CompleteDelivery(ctx, &store.MailDeliveryCompletion{
		DeliveryID: expirySending.ID, ExpectedRevision: expirySending.Revision,
		Kind: store.MailDeliveryCompletionExpired, At: expirySending.Deadline,
	})
	requireNoError(t, err)
	if expired.State != model.MailDeliverySuppressed || expired.PublicFailureCode != model.MailDeliveryExpiredCode || len(expired.EncryptedPayload) != 0 {
		t.Fatalf("expired sending = %#v", expired)
	}

	mismatchedAudit := mailTestEnqueueFixture(t, user, institution, at.Add(7*time.Second))
	mismatchedAudit.AuditEvent.Resource.ID = model.NewMailDeliveryID().String()
	if _, err = ss.Mail().EnqueueTest(ctx, mismatchedAudit); err == nil {
		t.Fatal("mismatched audit relationship did not abort enqueue")
	}
	if _, err = ss.Mail().GetDelivery(ctx, mismatchedAudit.Delivery.ID); !store.IsNotFound(err) {
		t.Fatalf("mismatched-audit delivery error = %v", err)
	}
	if _, err = ss.Job().Get(ctx, mismatchedAudit.Job.ID); !store.IsNotFound(err) {
		t.Fatalf("mismatched-audit job error = %v", err)
	}
	nonInitial := mailTestEnqueueFixture(t, user, institution, at.Add(7*time.Second))
	nonInitial.Delivery.AttemptCount = 1
	nonInitial.Delivery.PublicFailureCode = "mail.transport.temporary"
	nonInitial.Delivery.Revision = 2
	if nonInitial.Delivery.Validate() != nil {
		t.Fatal("non-initial fixture is not otherwise valid")
	}
	if _, err = ss.Mail().EnqueueTest(ctx, nonInitial); err == nil {
		t.Fatal("non-initial delivery did not abort enqueue")
	}

	rollback := mailTestEnqueueFixture(t, user, institution, at.Add(10*time.Second))
	rollback.AuditEvent.NodeID = ""
	if _, err = ss.Mail().EnqueueTest(ctx, rollback); err == nil {
		t.Fatal("invalid audit did not abort enqueue")
	}
	if _, err = ss.Mail().GetDelivery(ctx, rollback.Delivery.ID); !store.IsNotFound(err) {
		t.Fatalf("rolled-back delivery error = %v", err)
	}
	if _, err = ss.Job().Get(ctx, rollback.Job.ID); !store.IsNotFound(err) {
		t.Fatalf("rolled-back job error = %v", err)
	}
	testMailOperatorStore(t, ctx, ss, user, institution)

	ordinaryPermit, err := ss.Mail().AcquireSendPermit(ctx, store.MailSendOrdinary)
	requireNoError(t, err)
	if ordinaryPermit == nil || !ordinaryPermit.Allowed || ordinaryPermit.RetryAfter != 0 {
		t.Fatalf("AcquireSendPermit() = %#v", ordinaryPermit)
	}

	outstandingInput := mailTestEnqueueFixture(t, user, institution, model.NowUTC().Add(-time.Minute))
	outstanding, err := ss.Mail().EnqueueTest(ctx, outstandingInput)
	requireNoError(t, err)
	maintenance, err := ss.Mail().SuppressOutstanding(ctx, model.MailDeliveryDisabledCode, 1)
	requireNoError(t, err)
	if maintenance.Affected != 1 || len(maintenance.Deliveries) != 1 ||
		maintenance.Deliveries[0].State != model.MailDeliverySuppressed ||
		maintenance.Deliveries[0].PublicFailureCode != model.MailDeliveryDisabledCode {
		t.Fatalf("SuppressOutstanding() = %#v", maintenance)
	}
	outstanding, err = ss.Mail().GetDelivery(ctx, outstanding.ID)
	requireNoError(t, err)
	if outstanding.State != model.MailDeliverySuppressed || outstanding.PublicFailureCode != model.MailDeliveryDisabledCode || len(outstanding.EncryptedPayload) != 0 {
		t.Fatalf("disabled delivery = %#v", outstanding)
	}
	outstandingJob, err := ss.Job().Get(ctx, outstanding.JobID)
	requireNoError(t, err)
	if outstandingJob.Status != model.JobStatusCanceled {
		t.Fatalf("disabled delivery Job = %#v", outstandingJob)
	}

	failedInput := mailTestEnqueueFixture(t, user, institution, model.NowUTC().Add(-30*time.Second))
	failed, err := ss.Mail().EnqueueTest(ctx, failedInput)
	requireNoError(t, err)
	failed, err = ss.Mail().StartDelivery(ctx, failed.ID, failed.Revision, failed.CreatedAt.Add(time.Second))
	requireNoError(t, err)
	failed, err = ss.Mail().CompleteDelivery(ctx, &store.MailDeliveryCompletion{
		DeliveryID: failed.ID, ExpectedRevision: failed.Revision, Kind: store.MailDeliveryCompletionFailed,
		PublicFailureCode: "mail.transport.permanent", At: failed.CreatedAt.Add(2 * time.Second),
	})
	requireNoError(t, err)
	maintenance, err = ss.Mail().SuppressOutstanding(ctx, model.MailDeliveryDisabledCode, 500)
	requireNoError(t, err)
	failed, err = ss.Mail().GetDelivery(ctx, failed.ID)
	requireNoError(t, err)
	if failed.State != model.MailDeliverySuppressed || len(failed.EncryptedPayload) != 0 || failed.PublicFailureCode != model.MailDeliveryDisabledCode {
		t.Fatalf("disabled failed delivery = %#v", failed)
	}

	expiredInput := mailTestEnqueueFixture(t, user, institution, model.NowUTC().Add(-25*time.Hour))
	expiredByMaintenance, err := ss.Mail().EnqueueTest(ctx, expiredInput)
	requireNoError(t, err)
	maintenance, err = ss.Mail().SuppressExpired(ctx, 10)
	requireNoError(t, err)
	if maintenance.Affected < 1 || len(maintenance.Deliveries) != maintenance.Affected {
		t.Fatalf("SuppressExpired() = %#v", maintenance)
	}
	for _, delivery := range maintenance.Deliveries {
		if delivery.State != model.MailDeliverySuppressed || delivery.PublicFailureCode != model.MailDeliveryExpiredCode || delivery.ProcessingLatency < 0 {
			t.Fatalf("expired maintenance observation = %#v", delivery)
		}
	}
	expiredByMaintenance, err = ss.Mail().GetDelivery(ctx, expiredByMaintenance.ID)
	requireNoError(t, err)
	if expiredByMaintenance.State != model.MailDeliverySuppressed || expiredByMaintenance.PublicFailureCode != model.MailDeliveryExpiredCode || len(expiredByMaintenance.EncryptedPayload) != 0 {
		t.Fatalf("maintenance expiry = %#v", expiredByMaintenance)
	}
	sendingInput := mailTestEnqueueFixture(t, user, institution, model.NowUTC().Add(-2*time.Second))
	sendingFixture, err := ss.Mail().EnqueueTest(ctx, sendingInput)
	if err != nil {
		t.Fatalf("enqueue sending snapshot fixture: %v", err)
	}
	sendingToken := mustClaimToken(t)
	sendingClaim, err := ss.Job().ClaimNext(ctx, &store.JobClaimRequest{Types: []model.JobType{model.JobTypeMailDeliver}, NodeID: "mail-snapshot-test", ClaimToken: sendingToken, LeaseDuration: time.Minute})
	requireNoError(t, err)
	if sendingClaim.Job.ID != sendingFixture.JobID {
		t.Fatalf("claimed Job %s, want sending Job %s", sendingClaim.Job.ID, sendingFixture.JobID)
	}
	if _, err = ss.Mail().StartDelivery(ctx, sendingFixture.ID, sendingFixture.Revision, model.NowUTC()); err != nil {
		t.Fatalf("start sending snapshot fixture: %v", err)
	}
	queueInput := mailTestEnqueueFixture(t, user, institution, model.NowUTC().Add(-time.Second))
	if _, err = ss.Mail().EnqueueTest(ctx, queueInput); err != nil {
		t.Fatalf("enqueue queued snapshot fixture: %v", err)
	}

	snapshot, err := ss.Mail().QueueSnapshot(ctx)
	requireNoError(t, err)
	if snapshot == nil || len(snapshot.Counts) == 0 {
		t.Fatalf("QueueSnapshot() = %#v", snapshot)
	}
	states := make(map[model.MailDeliveryState]time.Time)
	for _, count := range snapshot.Counts {
		states[count.State] = count.OldestObservedAt
	}
	if states[model.MailDeliveryQueued].IsZero() || states[model.MailDeliverySending].IsZero() {
		t.Fatalf("per-state queue ages = %#v", snapshot.Counts)
	}
	if _, err := ss.Mail().SuppressOutstanding(ctx, model.MailDeliveryDisabledCode, 500); err != nil {
		t.Fatalf("suppress before backoff snapshot: %v", err)
	}
	backoffInput := mailTestEnqueueFixture(t, user, institution, model.NowUTC().Add(-time.Minute))
	backoff, err := ss.Mail().EnqueueTest(ctx, backoffInput)
	if err != nil {
		t.Fatalf("enqueue scheduled-backoff fixture: %v", err)
	}
	backoffToken := mustClaimToken(t)
	backoffClaim, err := ss.Job().ClaimNext(ctx, &store.JobClaimRequest{Types: []model.JobType{model.JobTypeMailDeliver}, NodeID: "mail-backoff-test", ClaimToken: backoffToken, LeaseDuration: time.Minute})
	requireNoError(t, err)
	if backoffClaim.Job.ID != backoff.JobID {
		t.Fatalf("claimed Job %s, want backoff Job %s", backoffClaim.Job.ID, backoff.JobID)
	}
	backoff, err = ss.Mail().StartDelivery(ctx, backoff.ID, backoff.Revision, model.NowUTC())
	requireNoError(t, err)
	_, err = ss.Mail().CompleteDelivery(ctx, &store.MailDeliveryCompletion{DeliveryID: backoff.ID, ExpectedRevision: backoff.Revision, Kind: store.MailDeliveryCompletionRetry, PublicFailureCode: "mail.transport.temporary", At: model.NowUTC()})
	requireNoError(t, err)
	_, err = ss.Job().Complete(ctx, &store.JobCompletion{AttemptID: backoffClaim.Attempt.ID, ClaimToken: backoffToken, Kind: store.JobCompletionRetryableFailure, RetryDelay: 30 * time.Minute, PublicErrorCode: "mail.transport.temporary"})
	requireNoError(t, err)
	backoffSnapshot, err := ss.Mail().QueueSnapshot(ctx)
	requireNoError(t, err)
	if len(backoffSnapshot.Counts) != 0 || !backoffSnapshot.OldestQueuedAt.IsZero() {
		t.Fatalf("scheduled backoff counted as eligible delay: %#v", backoffSnapshot)
	}
	keyIDs, err := ss.Mail().ActivePayloadKeyIDs(ctx)
	requireNoError(t, err)
	for _, keyID := range keyIDs {
		if keyID != strings.Repeat("1", 32) {
			t.Fatalf("ActivePayloadKeyIDs() = %#v", keyIDs)
		}
	}
	testMailRekeyStore(t, ctx, ss, user, institution)
}

func testMailOperatorStore(t *testing.T, ctx context.Context, ss store.Store, user *model.User, institution *model.Institution) {
	at := model.NowUTC().Add(-5 * time.Second)
	firstInput := mailTestEnqueueFixture(t, user, institution, at)
	first, err := ss.Mail().EnqueueTest(ctx, firstInput)
	requireNoError(t, err)
	secondInput := mailTestEnqueueFixture(t, user, institution, at.Add(time.Second))
	second, err := ss.Mail().EnqueueTest(ctx, secondInput)
	requireNoError(t, err)

	page, err := ss.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{
		States: []model.MailDeliveryState{model.MailDeliveryQueued}, TemplateKeys: []model.MailTemplateKey{model.MailTemplateSystemTest},
		CreatedAfter: at.Add(-time.Nanosecond), CreatedBefore: at.Add(2 * time.Second), Limit: 1,
	})
	requireNoError(t, err)
	if len(page) != 1 || page[0].ID != second.ID {
		t.Fatalf("ListDeliveries(first page) = %#v", page)
	}
	next, err := ss.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{
		States: []model.MailDeliveryState{model.MailDeliveryQueued}, TemplateKeys: []model.MailTemplateKey{model.MailTemplateSystemTest},
		CreatedAfter: at.Add(-time.Nanosecond), CreatedBefore: at.Add(2 * time.Second),
		BeforeCreatedAt: page[0].CreatedAt, BeforeID: page[0].ID, Limit: 10,
	})
	requireNoError(t, err)
	if len(next) != 1 || next[0].ID != first.ID {
		t.Fatalf("ListDeliveries(next page) = %#v", next)
	}
	if _, err = ss.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{States: []model.MailDeliveryState{"secret-state"}, Limit: 10}); err == nil {
		t.Fatal("ListDeliveries accepted an unknown state")
	}

	cancelAudit := saveMailMutationAudit(t, ctx, ss, institution, second, "cancel")
	canceled, err := ss.Mail().CancelDelivery(ctx, &store.MailDeliveryMutation{ID: second.ID, ExpectedRevision: second.Revision, AuditEventID: cancelAudit.ID.String(), AuditAt: model.GetMillis()})
	requireNoError(t, err)
	if canceled.State != model.MailDeliveryCanceled || len(canceled.EncryptedPayload) != 0 || canceled.ID != second.ID || canceled.MessageID != second.MessageID {
		t.Fatalf("CancelDelivery() = %#v", canceled)
	}
	canceledJob, err := ss.Job().Get(ctx, canceled.JobID)
	requireNoError(t, err)
	if canceledJob.Status != model.JobStatusCanceled {
		t.Fatalf("canceled delivery Job = %#v", canceledJob)
	}
	if _, err = ss.Mail().CancelDelivery(ctx, &store.MailDeliveryMutation{ID: second.ID, ExpectedRevision: second.Revision, AuditEventID: cancelAudit.ID.String(), AuditAt: model.GetMillis()}); !store.IsConflict(err) {
		t.Fatalf("CancelDelivery(stale) error = %v", err)
	}
	mismatchInput := mailTestEnqueueFixture(t, user, institution, at.Add(1500*time.Millisecond))
	mismatch, err := ss.Mail().EnqueueTest(ctx, mismatchInput)
	requireNoError(t, err)
	mismatchAudit := saveMailMutationAudit(t, ctx, ss, institution, second, "cancel")
	if _, err = ss.Mail().CancelDelivery(ctx, &store.MailDeliveryMutation{ID: mismatch.ID, ExpectedRevision: mismatch.Revision, AuditEventID: mismatchAudit.ID.String(), AuditAt: model.GetMillis()}); !store.IsConflict(err) {
		t.Fatalf("CancelDelivery(mismatched audit) error = %v", err)
	}
	unchanged, err := ss.Mail().GetDelivery(ctx, mismatch.ID)
	requireNoError(t, err)
	if unchanged.State != model.MailDeliveryQueued || unchanged.Revision != mismatch.Revision || len(unchanged.EncryptedPayload) == 0 {
		t.Fatalf("mismatched-audit delivery changed = %#v", unchanged)
	}

	retryInput := mailTestEnqueueFixture(t, user, institution, at.Add(2*time.Second))
	retryTarget, err := ss.Mail().EnqueueTest(ctx, retryInput)
	requireNoError(t, err)
	var failed *model.MailDelivery
	for attempts := 0; attempts < 20; attempts++ {
		token := mustClaimToken(t)
		claim, claimErr := ss.Job().ClaimNext(ctx, &store.JobClaimRequest{Types: []model.JobType{model.JobTypeMailDeliver}, NodeID: "mail-operator-test", ClaimToken: token, LeaseDuration: time.Minute})
		requireNoError(t, claimErr)
		if claim.Job.ID != retryTarget.JobID {
			_, claimErr = ss.Job().Complete(ctx, &store.JobCompletion{AttemptID: claim.Attempt.ID, ClaimToken: token, Kind: store.JobCompletionSucceeded, ResultVersion: 1, Result: json.RawMessage(`{}`)})
			requireNoError(t, claimErr)
			continue
		}
		failed, err = ss.Mail().StartDelivery(ctx, retryTarget.ID, retryTarget.Revision, model.NowUTC())
		requireNoError(t, err)
		failed, err = ss.Mail().CompleteDelivery(ctx, &store.MailDeliveryCompletion{DeliveryID: failed.ID, ExpectedRevision: failed.Revision, Kind: store.MailDeliveryCompletionFailed, PublicFailureCode: "mail.transport.permanent", At: model.NowUTC()})
		requireNoError(t, err)
		_, err = ss.Job().Complete(ctx, &store.JobCompletion{AttemptID: claim.Attempt.ID, ClaimToken: token, Kind: store.JobCompletionPermanentFailure, PublicErrorCode: "mail.transport.permanent"})
		requireNoError(t, err)
		break
	}
	if failed == nil {
		t.Fatal("did not claim retry delivery Job")
	}
	retryAudit := saveMailMutationAudit(t, ctx, ss, institution, failed, "retry")
	retried, err := ss.Mail().RetryDelivery(ctx, &store.MailDeliveryMutation{ID: failed.ID, ExpectedRevision: failed.Revision, AuditEventID: retryAudit.ID.String(), AuditAt: model.GetMillis()})
	requireNoError(t, err)
	if retried.State != model.MailDeliveryQueued || retried.ID != failed.ID || retried.MessageID != failed.MessageID || len(retried.EncryptedPayload) == 0 {
		t.Fatalf("RetryDelivery() = %#v", retried)
	}
	retriedJob, err := ss.Job().Get(ctx, retried.JobID)
	requireNoError(t, err)
	if retriedJob.Status != model.JobStatusQueued {
		t.Fatalf("retried delivery Job = %#v", retriedJob)
	}
}

func saveMailMutationAudit(t *testing.T, ctx context.Context, ss store.Store, institution *model.Institution, delivery *model.MailDelivery, operation string) *model.AuditEvent {
	t.Helper()
	parameters, err := model.EncodeAuditData(map[string]any{"operation": operation, "value": delivery.Auditable()})
	requireNoError(t, err)
	event, err := ss.Audit().Save(ctx, &model.AuditEvent{Action: string(model.ActionMailManage), Resource: model.Resource{Type: model.ResourceMailDelivery, ID: delivery.ID.String()}, ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(), Status: model.AuditStatusAttempt, NodeID: "mail-operator-test", Parameters: parameters})
	requireNoError(t, err)
	return event
}

func mailTestEnqueueFixture(t *testing.T, user *model.User, institution *model.Institution, at time.Time) *store.MailTestEnqueue {
	t.Helper()
	occurrenceID, deliveryID, jobID := model.NewMailOccurrenceID(), model.NewMailDeliveryID(), model.NewJobID()
	command, err := model.EncodeMailDeliveryCommand(model.MailDeliveryCommandV1{DeliveryID: deliveryID})
	requireNoError(t, err)
	job, err := model.NewJob(jobID, model.JobTypeMailDeliver, 1, command, deliveryID.String(), at, at, model.MailMaximumAttempts)
	requireNoError(t, err)
	parameters, err := model.EncodeAuditData(map[string]string{"delivery_id": deliveryID.String(), "template_key": string(model.MailTemplateSystemTest)})
	requireNoError(t, err)
	return &store.MailTestEnqueue{
		Occurrence: &model.MailOccurrence{ID: occurrenceID, Kind: model.MailOccurrenceOperatorTest, TemplateKey: model.MailTemplateSystemTest, ActorUserID: user.ID, CreatedAt: at},
		Delivery:   &model.MailDelivery{ID: deliveryID, OccurrenceID: occurrenceID, JobID: jobID, TargetUserID: user.ID, TemplateKey: model.MailTemplateSystemTest, TemplateDigest: strings.Repeat("a", 64), MaskedRecipient: "o***@example.test", State: model.MailDeliveryQueued, CreatedAt: at, UpdatedAt: at, MessageDate: at, Deadline: at.Add(24 * time.Hour), MessageID: "<mail." + deliveryID.String() + "@example.test>", EncryptedPayload: json.RawMessage(`{"version":1,"key_id":"11111111111111111111111111111111","ciphertext":"secret"}`), Revision: 1},
		Job:        job,
		AuditEvent: &model.AuditEvent{ActorID: user.ID, Action: string(model.ActionMailManage), Resource: model.Resource{Type: model.ResourceMailDelivery, ID: deliveryID.String()}, ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(), Status: model.AuditStatusSuccess, NodeID: "store-test", ClientType: string(model.SessionClientWeb), AuthMethod: "password", Parameters: parameters},
	}
}

// MailTestEnqueueFixtureForSQLTest exposes the normal conformance fixture to
// PostgreSQL-only lock-order tests without duplicating the aggregate input.
func MailTestEnqueueFixtureForSQLTest(t *testing.T, user *model.User, institution *model.Institution, at time.Time) *store.MailTestEnqueue {
	t.Helper()
	return mailTestEnqueueFixture(t, user, institution, at)
}
