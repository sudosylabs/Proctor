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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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
	require.NoError(t, err)
	require.Equal(t, input.Delivery.ID, created.ID)
	require.Equal(t, model.MailDeliveryQueued, created.State)
	require.Equal(t, string(input.Delivery.EncryptedPayload), string(created.EncryptedPayload))
	stored, err := ss.Mail().GetDelivery(ctx, created.ID)
	require.NoError(t, err)
	require.Equal(t, created.MessageID, stored.MessageID)
	require.Equal(t, "o***@example.test", stored.MaskedRecipient)
	require.Equal(t, user.ID, stored.TargetUserID)
	audits, err := ss.Audit().List(ctx, store.AuditListOptions{
		Action: string(model.ActionMailManage), Limit: 10,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	require.NoError(t, err)
	require.Len(t, audits, 1)
	require.Equal(t, model.ResourceMailDelivery, audits[0].Resource.Type)
	require.Equal(t, created.ID.String(), audits[0].Resource.ID)

	sending, err := ss.Mail().StartDelivery(ctx, &store.MailDeliveryStart{ID: created.ID, ExpectedRevision: created.Revision, At: at.Add(time.Second)})
	require.NoError(t, err)
	require.Equal(t, model.MailDeliverySending, sending.State)
	require.Equal(t, 1, sending.AttemptCount)
	_, err = ss.Mail().StartDelivery(ctx, &store.MailDeliveryStart{ID: created.ID, ExpectedRevision: created.Revision, At: at.Add(2 * time.Second)})
	require.True(t, store.IsConflict(err), "StartDelivery(stale) error")
	retrying, err := ss.Mail().CompleteDelivery(ctx, &store.MailDeliveryCompletion{DeliveryID: created.ID, ExpectedRevision: sending.Revision, Kind: store.MailDeliveryCompletionRetry, PublicFailureCode: "mail.transport.temporary", At: at.Add(2 * time.Second)})
	require.NoError(t, err)
	require.Equal(t, model.MailDeliveryQueued, retrying.State)
	require.NotEmpty(t, retrying.EncryptedPayload)
	second, err := ss.Mail().StartDelivery(ctx, &store.MailDeliveryStart{ID: created.ID, ExpectedRevision: retrying.Revision, At: at.Add(3 * time.Second)})
	require.NoError(t, err)
	accepted, err := ss.Mail().CompleteDelivery(ctx, &store.MailDeliveryCompletion{DeliveryID: created.ID, ExpectedRevision: second.Revision, Kind: store.MailDeliveryCompletionAccepted, At: at.Add(4 * time.Second)})
	require.NoError(t, err)
	require.Equal(t, model.MailDeliveryAccepted, accepted.State)
	require.Empty(t, accepted.EncryptedPayload)
	require.True(t, accepted.AcceptedAt.Valid)
	require.Equal(t, created.MessageID, accepted.MessageID)
	expiryInput := mailTestEnqueueFixture(t, user, institution, at.Add(5*time.Second))
	expiryQueued, err := ss.Mail().EnqueueTest(ctx, expiryInput)
	require.NoError(t, err)
	expired, err := ss.Mail().CompleteDelivery(ctx, &store.MailDeliveryCompletion{
		DeliveryID: expiryQueued.ID, ExpectedRevision: expiryQueued.Revision,
		Kind: store.MailDeliveryCompletionExpired, At: expiryQueued.Deadline,
	})
	require.NoError(t, err)
	require.Equal(t, model.MailDeliverySuppressed, expired.State)
	require.Equal(t, model.MailDeliveryExpiredCode, expired.PublicFailureCode)
	require.Empty(t, expired.EncryptedPayload)
	expirySendingInput := mailTestEnqueueFixture(t, user, institution, at.Add(6*time.Second))
	expirySending, err := ss.Mail().EnqueueTest(ctx, expirySendingInput)
	require.NoError(t, err)
	expirySending, err = ss.Mail().StartDelivery(ctx, &store.MailDeliveryStart{ID: expirySending.ID, ExpectedRevision: expirySending.Revision, At: expirySending.CreatedAt.Add(time.Second)})
	require.NoError(t, err)
	expired, err = ss.Mail().CompleteDelivery(ctx, &store.MailDeliveryCompletion{
		DeliveryID: expirySending.ID, ExpectedRevision: expirySending.Revision,
		Kind: store.MailDeliveryCompletionExpired, At: expirySending.Deadline,
	})
	require.NoError(t, err)
	require.Equal(t, model.MailDeliverySuppressed, expired.State)
	require.Equal(t, model.MailDeliveryExpiredCode, expired.PublicFailureCode)
	require.Empty(t, expired.EncryptedPayload)

	mismatchedAudit := mailTestEnqueueFixture(t, user, institution, at.Add(7*time.Second))
	mismatchedAudit.AuditEvent.Resource.ID = model.NewMailDeliveryID().String()
	_, err = ss.Mail().EnqueueTest(ctx, mismatchedAudit)
	require.Error(t, err, "mismatched audit relationship did not abort enqueue")
	_, err = ss.Mail().GetDelivery(ctx, mismatchedAudit.Delivery.ID)
	require.True(t, store.IsNotFound(err), "mismatched-audit delivery error")
	_, err = ss.Job().Get(ctx, mismatchedAudit.Job.ID)
	require.True(t, store.IsNotFound(err), "mismatched-audit job error")
	nonInitial := mailTestEnqueueFixture(t, user, institution, at.Add(7*time.Second))
	nonInitial.Delivery.AttemptCount = 1
	nonInitial.Delivery.PublicFailureCode = "mail.transport.temporary"
	nonInitial.Delivery.Revision = 2
	require.NoError(t, nonInitial.Delivery.Validate(), "non-initial fixture is not otherwise valid")
	_, err = ss.Mail().EnqueueTest(ctx, nonInitial)
	require.Error(t, err, "non-initial delivery did not abort enqueue")

	rollback := mailTestEnqueueFixture(t, user, institution, at.Add(10*time.Second))
	rollback.AuditEvent.NodeID = ""
	_, err = ss.Mail().EnqueueTest(ctx, rollback)
	require.Error(t, err, "invalid audit did not abort enqueue")
	_, err = ss.Mail().GetDelivery(ctx, rollback.Delivery.ID)
	require.True(t, store.IsNotFound(err), "rolled-back delivery error")
	_, err = ss.Job().Get(ctx, rollback.Job.ID)
	require.True(t, store.IsNotFound(err), "rolled-back job error")
	testMailOperatorStore(t, ctx, ss, user, institution)

	ordinaryPermit, err := ss.Mail().AcquireSendPermit(ctx, store.MailSendOrdinary)
	require.NoError(t, err)
	require.NotNil(t, ordinaryPermit)
	require.True(t, ordinaryPermit.Allowed)
	require.Equal(t, int64(0), int64(ordinaryPermit.RetryAfter))

	outstandingInput := mailTestEnqueueFixture(t, user, institution, model.NowUTC().Add(-time.Minute))
	outstanding, err := ss.Mail().EnqueueTest(ctx, outstandingInput)
	require.NoError(t, err)
	maintenance, err := ss.Mail().SuppressOutstanding(ctx, &store.MailOutstandingSuppression{Code: model.MailDeliveryDisabledCode, Limit: 1})
	require.NoError(t, err)
	require.Equal(t, 1, maintenance.Affected)
	require.Len(t, maintenance.Deliveries, 1)
	require.Equal(t, model.MailDeliverySuppressed, maintenance.Deliveries[0].State)
	require.Equal(t, model.MailDeliveryDisabledCode, maintenance.Deliveries[0].PublicFailureCode)
	outstanding, err = ss.Mail().GetDelivery(ctx, outstanding.ID)
	require.NoError(t, err)
	require.Equal(t, model.MailDeliverySuppressed, outstanding.State)
	require.Equal(t, model.MailDeliveryDisabledCode, outstanding.PublicFailureCode)
	require.Empty(t, outstanding.EncryptedPayload)
	outstandingJob, err := ss.Job().Get(ctx, outstanding.JobID)
	require.NoError(t, err)
	require.Equal(t, model.JobStatusCanceled, outstandingJob.Status)

	failedInput := mailTestEnqueueFixture(t, user, institution, model.NowUTC().Add(-30*time.Second))
	failed, err := ss.Mail().EnqueueTest(ctx, failedInput)
	require.NoError(t, err)
	failed, err = ss.Mail().StartDelivery(ctx, &store.MailDeliveryStart{ID: failed.ID, ExpectedRevision: failed.Revision, At: failed.CreatedAt.Add(time.Second)})
	require.NoError(t, err)
	failed, err = ss.Mail().CompleteDelivery(ctx, &store.MailDeliveryCompletion{
		DeliveryID: failed.ID, ExpectedRevision: failed.Revision, Kind: store.MailDeliveryCompletionFailed,
		PublicFailureCode: "mail.transport.permanent", At: failed.CreatedAt.Add(2 * time.Second),
	})
	require.NoError(t, err)
	_, err = ss.Mail().SuppressOutstanding(ctx, &store.MailOutstandingSuppression{Code: model.MailDeliveryDisabledCode, Limit: 500})
	require.NoError(t, err)
	failed, err = ss.Mail().GetDelivery(ctx, failed.ID)
	require.NoError(t, err)
	require.Equal(t, model.MailDeliverySuppressed, failed.State)
	require.Empty(t, failed.EncryptedPayload)
	require.Equal(t, model.MailDeliveryDisabledCode, failed.PublicFailureCode)

	expiredInput := mailTestEnqueueFixture(t, user, institution, model.NowUTC().Add(-25*time.Hour))
	expiredByMaintenance, err := ss.Mail().EnqueueTest(ctx, expiredInput)
	require.NoError(t, err)
	maintenance, err = ss.Mail().SuppressExpired(ctx, 10)
	require.NoError(t, err)
	require.GreaterOrEqual(t, maintenance.Affected, 1)
	require.Len(t, maintenance.Deliveries, maintenance.Affected)
	for _, delivery := range maintenance.Deliveries {
		require.Equal(t, model.MailDeliverySuppressed, delivery.State)
		require.Equal(t, model.MailDeliveryExpiredCode, delivery.PublicFailureCode)
		require.GreaterOrEqual(t, int64(delivery.ProcessingLatency), int64(0))
	}
	expiredByMaintenance, err = ss.Mail().GetDelivery(ctx, expiredByMaintenance.ID)
	require.NoError(t, err)
	require.Equal(t, model.MailDeliverySuppressed, expiredByMaintenance.State)
	require.Equal(t, model.MailDeliveryExpiredCode, expiredByMaintenance.PublicFailureCode)
	require.Empty(t, expiredByMaintenance.EncryptedPayload)
	sendingInput := mailTestEnqueueFixture(t, user, institution, model.NowUTC().Add(-2*time.Second))
	sendingFixture, err := ss.Mail().EnqueueTest(ctx, sendingInput)
	require.NoError(t, err)
	sendingToken := mustClaimToken(t)
	sendingClaim, err := ss.Job().ClaimNext(ctx, &store.JobClaimRequest{Types: []model.JobType{model.JobTypeMailDeliver}, NodeID: "mail-snapshot-test", ClaimToken: sendingToken, LeaseDuration: time.Minute})
	require.NoError(t, err)
	require.Equal(t, sendingFixture.JobID, sendingClaim.Job.ID, "claimed Job")
	_, err = ss.Mail().StartDelivery(ctx, &store.MailDeliveryStart{ID: sendingFixture.ID, ExpectedRevision: sendingFixture.Revision, At: model.NowUTC()})
	require.NoError(t, err)
	queueInput := mailTestEnqueueFixture(t, user, institution, model.NowUTC().Add(-time.Second))
	_, err = ss.Mail().EnqueueTest(ctx, queueInput)
	require.NoError(t, err)

	snapshot, err := ss.Mail().QueueSnapshot(ctx)
	require.NoError(t, err)
	require.NotNil(t, snapshot)
	require.NotEmpty(t, snapshot.Counts)
	states := make(map[model.MailDeliveryState]time.Time)
	for _, count := range snapshot.Counts {
		states[count.State] = count.OldestObservedAt
	}
	require.False(t, states[model.MailDeliveryQueued].IsZero())
	require.False(t, states[model.MailDeliverySending].IsZero())
	_, err = ss.Mail().SuppressOutstanding(ctx, &store.MailOutstandingSuppression{Code: model.MailDeliveryDisabledCode, Limit: 500})
	require.NoError(t, err)
	backoffInput := mailTestEnqueueFixture(t, user, institution, model.NowUTC().Add(-time.Minute))
	backoff, err := ss.Mail().EnqueueTest(ctx, backoffInput)
	require.NoError(t, err)
	backoffToken := mustClaimToken(t)
	backoffClaim, err := ss.Job().ClaimNext(ctx, &store.JobClaimRequest{Types: []model.JobType{model.JobTypeMailDeliver}, NodeID: "mail-backoff-test", ClaimToken: backoffToken, LeaseDuration: time.Minute})
	require.NoError(t, err)
	require.Equal(t, backoff.JobID, backoffClaim.Job.ID, "claimed Job")
	backoff, err = ss.Mail().StartDelivery(ctx, &store.MailDeliveryStart{ID: backoff.ID, ExpectedRevision: backoff.Revision, At: model.NowUTC()})
	require.NoError(t, err)
	_, err = ss.Mail().CompleteDelivery(ctx, &store.MailDeliveryCompletion{DeliveryID: backoff.ID, ExpectedRevision: backoff.Revision, Kind: store.MailDeliveryCompletionRetry, PublicFailureCode: "mail.transport.temporary", At: model.NowUTC()})
	require.NoError(t, err)
	_, err = ss.Job().Complete(ctx, &store.JobCompletion{AttemptID: backoffClaim.Attempt.ID, ClaimToken: backoffToken, Kind: store.JobCompletionRetryableFailure, RetryDelay: 30 * time.Minute, PublicErrorCode: "mail.transport.temporary"})
	require.NoError(t, err)
	backoffSnapshot, err := ss.Mail().QueueSnapshot(ctx)
	require.NoError(t, err)
	require.Empty(t, backoffSnapshot.Counts)
	require.True(t, backoffSnapshot.OldestQueuedAt.IsZero())
	keyIDs, err := ss.Mail().ActivePayloadKeyIDs(ctx)
	require.NoError(t, err)
	for _, keyID := range keyIDs {
		require.Equal(t, strings.Repeat("1", 32), keyID, "ActivePayloadKeyIDs() = %#v", keyIDs)
	}
	testMailRekeyStore(t, ctx, ss, user, institution)
}

func testMailOperatorStore(t *testing.T, ctx context.Context, ss store.Store, user *model.User, institution *model.Institution) {
	at := model.NowUTC().Add(-5 * time.Second)
	firstInput := mailTestEnqueueFixture(t, user, institution, at)
	first, err := ss.Mail().EnqueueTest(ctx, firstInput)
	require.NoError(t, err)
	secondInput := mailTestEnqueueFixture(t, user, institution, at.Add(time.Second))
	second, err := ss.Mail().EnqueueTest(ctx, secondInput)
	require.NoError(t, err)

	page, err := ss.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{
		States: []model.MailDeliveryState{model.MailDeliveryQueued}, TemplateKeys: []model.MailTemplateKey{model.MailTemplateSystemTest},
		CreatedAfter: at.Add(-time.Nanosecond), CreatedBefore: at.Add(2 * time.Second), Limit: 1,
	})
	require.NoError(t, err)
	require.Len(t, page, 1)
	require.Equal(t, second.ID, page[0].ID)
	next, err := ss.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{
		States: []model.MailDeliveryState{model.MailDeliveryQueued}, TemplateKeys: []model.MailTemplateKey{model.MailTemplateSystemTest},
		CreatedAfter: at.Add(-time.Nanosecond), CreatedBefore: at.Add(2 * time.Second),
		BeforeCreatedAt: page[0].CreatedAt, BeforeID: page[0].ID, Limit: 10,
	})
	require.NoError(t, err)
	require.Len(t, next, 1)
	require.Equal(t, first.ID, next[0].ID)
	_, err = ss.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{States: []model.MailDeliveryState{"secret-state"}, Limit: 10})
	require.Error(t, err, "ListDeliveries accepted an unknown state")

	cancelAudit := saveMailMutationAudit(t, ctx, ss, institution, second, "cancel")
	canceled, err := ss.Mail().CancelDelivery(ctx, &store.MailDeliveryMutation{ID: second.ID, ExpectedRevision: second.Revision, AuditEventID: cancelAudit.ID, AuditAt: model.NowUTC()})
	require.NoError(t, err)
	require.Equal(t, model.MailDeliveryCanceled, canceled.State)
	require.Empty(t, canceled.EncryptedPayload)
	require.Equal(t, second.ID, canceled.ID)
	require.Equal(t, second.MessageID, canceled.MessageID)
	canceledJob, err := ss.Job().Get(ctx, canceled.JobID)
	require.NoError(t, err)
	require.Equal(t, model.JobStatusCanceled, canceledJob.Status)
	_, err = ss.Mail().CancelDelivery(ctx, &store.MailDeliveryMutation{ID: second.ID, ExpectedRevision: second.Revision, AuditEventID: cancelAudit.ID, AuditAt: model.NowUTC()})
	require.True(t, store.IsConflict(err), "CancelDelivery(stale) error")
	mismatchInput := mailTestEnqueueFixture(t, user, institution, at.Add(1500*time.Millisecond))
	mismatch, err := ss.Mail().EnqueueTest(ctx, mismatchInput)
	require.NoError(t, err)
	mismatchAudit := saveMailMutationAudit(t, ctx, ss, institution, second, "cancel")
	_, err = ss.Mail().CancelDelivery(ctx, &store.MailDeliveryMutation{ID: mismatch.ID, ExpectedRevision: mismatch.Revision, AuditEventID: mismatchAudit.ID, AuditAt: model.NowUTC()})
	require.True(t, store.IsConflict(err), "CancelDelivery(mismatched audit) error")
	unchanged, err := ss.Mail().GetDelivery(ctx, mismatch.ID)
	require.NoError(t, err)
	require.Equal(t, model.MailDeliveryQueued, unchanged.State)
	require.Equal(t, mismatch.Revision, unchanged.Revision)
	require.NotEmpty(t, unchanged.EncryptedPayload)

	retryInput := mailTestEnqueueFixture(t, user, institution, at.Add(2*time.Second))
	retryTarget, err := ss.Mail().EnqueueTest(ctx, retryInput)
	require.NoError(t, err)
	var failed *model.MailDelivery
	for attempts := 0; attempts < 20; attempts++ {
		token := mustClaimToken(t)
		claim, claimErr := ss.Job().ClaimNext(ctx, &store.JobClaimRequest{Types: []model.JobType{model.JobTypeMailDeliver}, NodeID: "mail-operator-test", ClaimToken: token, LeaseDuration: time.Minute})
		require.NoError(t, claimErr)
		if claim.Job.ID != retryTarget.JobID {
			_, claimErr = ss.Job().Complete(ctx, &store.JobCompletion{AttemptID: claim.Attempt.ID, ClaimToken: token, Kind: store.JobCompletionSucceeded, Result: json.RawMessage(`{}`)})
			require.NoError(t, claimErr)
			continue
		}
		failed, err = ss.Mail().StartDelivery(ctx, &store.MailDeliveryStart{ID: retryTarget.ID, ExpectedRevision: retryTarget.Revision, At: model.NowUTC()})
		require.NoError(t, err)
		// The reporting node's clock leads the database; the later operator
		// retry at PostgreSQL time must not move the delivery backwards.
		failed, err = ss.Mail().CompleteDelivery(ctx, &store.MailDeliveryCompletion{DeliveryID: failed.ID, ExpectedRevision: failed.Revision, Kind: store.MailDeliveryCompletionFailed, PublicFailureCode: "mail.transport.permanent", At: model.NowUTC().Add(time.Minute)})
		require.NoError(t, err)
		_, err = ss.Job().Complete(ctx, &store.JobCompletion{AttemptID: claim.Attempt.ID, ClaimToken: token, Kind: store.JobCompletionPermanentFailure, PublicErrorCode: "mail.transport.permanent"})
		require.NoError(t, err)
		break
	}
	require.NotNil(t, failed, "did not claim retry delivery Job")
	retryAudit := saveMailMutationAudit(t, ctx, ss, institution, failed, "retry")
	retried, err := ss.Mail().RetryDelivery(ctx, &store.MailDeliveryMutation{ID: failed.ID, ExpectedRevision: failed.Revision, AuditEventID: retryAudit.ID, AuditAt: model.NowUTC()})
	require.NoError(t, err)
	require.Equal(t, model.MailDeliveryQueued, retried.State)
	require.Equal(t, failed.ID, retried.ID)
	require.Equal(t, failed.MessageID, retried.MessageID)
	require.NotEmpty(t, retried.EncryptedPayload)
	retriedJob, err := ss.Job().Get(ctx, retried.JobID)
	require.NoError(t, err)
	require.Equal(t, model.JobStatusQueued, retriedJob.Status)

	// A delivery whose Job ran out of attempts while it was still Sending is
	// not stranded: the operator retry requeues both.
	orphanTarget, err := ss.Mail().EnqueueTest(ctx, mailTestEnqueueFixture(t, user, institution, at.Add(3*time.Second)))
	require.NoError(t, err)
	var orphan *model.MailDelivery
	for attempts := 0; attempts < 20; attempts++ {
		token := mustClaimToken(t)
		claim, claimErr := ss.Job().ClaimNext(ctx, &store.JobClaimRequest{Types: []model.JobType{model.JobTypeMailDeliver}, NodeID: "mail-operator-test", ClaimToken: token, LeaseDuration: time.Minute})
		require.NoError(t, claimErr)
		if claim.Job.ID != orphanTarget.JobID {
			_, claimErr = ss.Job().Complete(ctx, &store.JobCompletion{AttemptID: claim.Attempt.ID, ClaimToken: token, Kind: store.JobCompletionSucceeded, Result: json.RawMessage(`{}`)})
			require.NoError(t, claimErr)
			continue
		}
		orphan, err = ss.Mail().StartDelivery(ctx, &store.MailDeliveryStart{ID: orphanTarget.ID, ExpectedRevision: orphanTarget.Revision, At: model.NowUTC()})
		require.NoError(t, err)
		_, err = ss.Job().Complete(ctx, &store.JobCompletion{AttemptID: claim.Attempt.ID, ClaimToken: token, Kind: store.JobCompletionPermanentFailure, PublicErrorCode: "job.lease_expired"})
		require.NoError(t, err)
		break
	}
	require.NotNil(t, orphan, "did not claim orphaned delivery Job")
	require.Equal(t, model.MailDeliverySending, orphan.State)
	orphanAudit := saveMailMutationAudit(t, ctx, ss, institution, orphan, "retry")
	recovered, err := ss.Mail().RetryDelivery(ctx, &store.MailDeliveryMutation{ID: orphan.ID, ExpectedRevision: orphan.Revision, AuditEventID: orphanAudit.ID, AuditAt: model.NowUTC()})
	require.NoError(t, err, "operator retry of a Sending delivery whose Job failed")
	require.Equal(t, model.MailDeliveryQueued, recovered.State)
	recoveredJob, err := ss.Job().Get(ctx, recovered.JobID)
	require.NoError(t, err)
	require.Equal(t, model.JobStatusQueued, recoveredJob.Status)
}

func saveMailMutationAudit(t *testing.T, ctx context.Context, ss store.Store, institution *model.Institution, delivery *model.MailDelivery, operation string) *model.AuditEvent {
	t.Helper()
	parameters, err := model.EncodeAuditData(map[string]any{"operation": operation, "value": delivery.Auditable()})
	require.NoError(t, err)
	event, err := ss.Audit().Save(ctx, &model.AuditEvent{Action: string(model.ActionMailManage), Resource: model.Resource{Type: model.ResourceMailDelivery, ID: delivery.ID.String()}, ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(), Status: model.AuditStatusAttempt, NodeID: "mail-operator-test", Parameters: parameters})
	require.NoError(t, err)
	return event
}

func mailTestEnqueueFixture(t *testing.T, user *model.User, institution *model.Institution, at time.Time) *store.MailTestEnqueue {
	t.Helper()
	occurrenceID, deliveryID, jobID := model.NewMailOccurrenceID(), model.NewMailDeliveryID(), model.NewJobID()
	command, err := model.EncodeMailDeliveryCommand(model.MailDeliveryCommand{DeliveryID: deliveryID})
	require.NoError(t, err)
	job, err := model.NewJob(jobID, model.JobTypeMailDeliver, command, deliveryID.String(), at, at, model.MailMaximumAttempts)
	require.NoError(t, err)
	parameters, err := model.EncodeAuditData(map[string]string{"delivery_id": deliveryID.String(), "template_key": string(model.MailTemplateSystemTest)})
	require.NoError(t, err)
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
