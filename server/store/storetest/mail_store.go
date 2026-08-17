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
	audits, err := ss.Audit().List(ctx, store.AuditListOptions{Action: string(model.ActionMailManage), Limit: 10})
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
		Delivery:   &model.MailDelivery{ID: deliveryID, OccurrenceID: occurrenceID, JobID: jobID, TargetUserID: user.ID, TemplateKey: model.MailTemplateSystemTest, TemplateDigest: strings.Repeat("a", 64), MaskedRecipient: "o***@example.test", State: model.MailDeliveryQueued, CreatedAt: at, UpdatedAt: at, MessageDate: at, Deadline: at.Add(24 * time.Hour), MessageID: "<mail." + deliveryID.String() + "@example.test>", EncryptedPayload: json.RawMessage(`{"version":1,"ciphertext":"secret"}`), Revision: 1},
		Job:        job,
		AuditEvent: &model.AuditEvent{ActorID: user.ID, Action: string(model.ActionMailManage), Resource: model.Resource{Type: model.ResourceMailDelivery, ID: deliveryID.String()}, ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(), Status: model.AuditStatusSuccess, NodeID: "store-test", ClientType: string(model.SessionClientWeb), AuthMethod: "password", Parameters: parameters},
	}
}
