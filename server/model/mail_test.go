// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func queuedMailDeliveryFixture(at time.Time) *MailDelivery {
	return &MailDelivery{ID: NewMailDeliveryID(), OccurrenceID: NewMailOccurrenceID(), JobID: NewJobID(), TargetUserID: NewUserID(),
		TemplateKey: MailTemplateSystemTest, TemplateDigest: strings.Repeat("a", 64), MaskedRecipient: "a***@example.test",
		State: MailDeliveryQueued, CreatedAt: at, UpdatedAt: at, MessageDate: at, Deadline: at.Add(24 * time.Hour),
		MessageID: "<mail." + NewId() + "@example.test>", EncryptedPayload: json.RawMessage(`{"version":1,"ciphertext":"secret"}`), Revision: 1}
}

func TestExamManagerMailMeaningsAreClosed(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	for _, key := range []MailTemplateKey{
		MailTemplateExamManagerAdded,
		MailTemplateExamManagerRemoved,
		MailTemplateExamOwnershipTransferredToYou,
		MailTemplateExamOwnershipTransferredFromYou,
	} {
		if !key.IsValid() {
			t.Errorf("MailTemplateKey(%q) is invalid", key)
		}
		occurrence := &MailOccurrence{ID: NewMailOccurrenceID(), Kind: MailOccurrenceExamManagement,
			TemplateKey: key, ActorUserID: NewUserID(), CreatedAt: at}
		if err := occurrence.Validate(); err != nil {
			t.Errorf("MailOccurrence(%q): %v", key, err)
		}
	}
	invalid := &MailOccurrence{ID: NewMailOccurrenceID(), Kind: MailOccurrenceExamManagement,
		TemplateKey: MailTemplateIdentityPasswordChanged, ActorUserID: NewUserID(), CreatedAt: at}
	if err := invalid.Validate(); err == nil {
		t.Fatal("Exam management occurrence accepted an identity-security template")
	}
}

func TestScopedRoleInvitationMailMeaningsAreClosed(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	for _, key := range []MailTemplateKey{
		MailTemplateAccessAcademicUnitRoleInvitation,
		MailTemplateAccessInstitutionRoleInvitation,
	} {
		if !key.IsValid() {
			t.Errorf("MailTemplateKey(%q) is invalid", key)
		}
		occurrence := &MailOccurrence{ID: NewMailOccurrenceID(), Kind: MailOccurrenceInvitation,
			TemplateKey: key, ActorUserID: NewUserID(), CreatedAt: at}
		if err := occurrence.Validate(); err != nil {
			t.Errorf("MailOccurrence(%q): %v", key, err)
		}
	}
}

func TestMailDeliveryLifecyclePreservesStableRoutingAndDestroysAcceptedPayload(t *testing.T) {
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	queued := queuedMailDeliveryFixture(at)
	if err := queued.Validate(); err != nil {
		t.Fatal(err)
	}
	sending, err := queued.Start(at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if sending.MessageID != queued.MessageID || sending.AttemptCount != 1 || sending.State != MailDeliverySending {
		t.Fatalf("Start() = %#v", sending)
	}
	retrying, err := sending.Retry("mail.transport.temporary", at.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	second, err := retrying.Start(at.Add(3 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := second.Accept(at.Add(4 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if accepted.State != MailDeliveryAccepted || !accepted.AcceptedAt.Valid || len(accepted.EncryptedPayload) != 0 || accepted.MessageID != queued.MessageID || accepted.AttemptCount != 2 {
		t.Fatalf("Accept() = %#v", accepted)
	}
}

func TestMailDeliveryExpiryTerminatesQueuedOrSendingAndDestroysPayload(t *testing.T) {
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	queued := queuedMailDeliveryFixture(at)
	for name, delivery := range map[string]*MailDelivery{
		"queued":  queued,
		"sending": mustStartMailDelivery(t, queued, at.Add(time.Second)),
	} {
		t.Run(name, func(t *testing.T) {
			expired, err := delivery.Expire(delivery.Deadline)
			if err != nil {
				t.Fatal(err)
			}
			if expired.State != MailDeliverySuppressed || expired.PublicFailureCode != "mail.delivery.expired" ||
				len(expired.EncryptedPayload) != 0 || expired.Revision != delivery.Revision+1 || !expired.UpdatedAt.Equal(delivery.Deadline) {
				t.Fatalf("Expire() = %#v", expired)
			}
		})
	}
	if _, err := queued.Expire(queued.Deadline.Add(-time.Nanosecond)); err == nil {
		t.Fatal("Expire() accepted a pre-deadline transition")
	}
}

func TestMailDeliveryOperatorControlCancelsQueuedAndRetriesFailedInPlace(t *testing.T) {
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	queued := queuedMailDeliveryFixture(at)
	canceled, err := queued.Cancel(at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if canceled.ID != queued.ID || canceled.MessageID != queued.MessageID ||
		canceled.State != MailDeliveryCanceled || len(canceled.EncryptedPayload) != 0 ||
		canceled.Revision != queued.Revision+1 {
		t.Fatalf("Cancel() = %#v", canceled)
	}
	if _, err = canceled.Cancel(at.Add(2 * time.Second)); err == nil {
		t.Fatal("Cancel() accepted a terminal delivery")
	}

	failed, err := queued.Start(at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	failed, err = failed.Fail("mail.transport.permanent", at.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	retried, err := failed.OperatorRetry(at.Add(3 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if retried.ID != failed.ID || retried.MessageID != failed.MessageID ||
		retried.State != MailDeliveryQueued || !retried.FailedAt.IsZero() ||
		retried.PublicFailureCode != MailDeliveryOperatorRetryCode ||
		len(retried.EncryptedPayload) == 0 || retried.Revision != failed.Revision+1 {
		t.Fatalf("OperatorRetry() = %#v", retried)
	}
	if _, err = failed.OperatorRetry(failed.Deadline); err == nil {
		t.Fatal("OperatorRetry() accepted an expired delivery")
	}
}

func TestMailDeliverySuppressionDestroysRecoverablePayload(t *testing.T) {
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	for _, state := range []MailDeliveryState{MailDeliveryQueued, MailDeliverySending, MailDeliveryFailed} {
		t.Run(string(state), func(t *testing.T) {
			delivery := queuedMailDeliveryFixture(at)
			if state != MailDeliveryQueued {
				delivery = mustStartMailDelivery(t, delivery, at.Add(time.Second))
			}
			if state == MailDeliveryFailed {
				var err error
				delivery, err = delivery.Fail("mail.transport.permanent", at.Add(2*time.Second))
				if err != nil {
					t.Fatal(err)
				}
			}
			suppressed, err := delivery.Suppress(MailDeliveryDisabledCode, at.Add(3*time.Second))
			if err != nil {
				t.Fatal(err)
			}
			if suppressed.State != MailDeliverySuppressed || suppressed.PublicFailureCode != MailDeliveryDisabledCode ||
				len(suppressed.EncryptedPayload) != 0 || suppressed.AcceptedAt.Valid || suppressed.FailedAt.Valid ||
				suppressed.Revision != delivery.Revision+1 {
				t.Fatalf("Suppress() = %#v", suppressed)
			}
			if _, err = suppressed.Suppress(MailDeliveryDisabledCode, at.Add(4*time.Second)); err == nil {
				t.Fatal("Suppress() accepted terminal delivery")
			}
		})
	}
}

func mustStartMailDelivery(t *testing.T, delivery *MailDelivery, at time.Time) *MailDelivery {
	t.Helper()
	started, err := delivery.Start(at)
	if err != nil {
		t.Fatal(err)
	}
	return started
}

func TestMailDeliveryValidationRejectsUnboundedOrLeakyLifecycle(t *testing.T) {
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	for name, mutate := range map[string]func(*MailDelivery){
		"bad digest":     func(d *MailDelivery) { d.TemplateDigest = "bad" },
		"bad message id": func(d *MailDelivery) { d.MessageID = "recipient@example.test" },
		"recipient local part remains visible": func(d *MailDelivery) {
			d.MaskedRecipient = "operator*@example.test"
		},
		"recipient domain contains mask": func(d *MailDelivery) {
			d.MaskedRecipient = "operator@example.test*"
		},
		"payload too large": func(d *MailDelivery) {
			d.EncryptedPayload = json.RawMessage(`"` + strings.Repeat("x", MailEncryptedPayloadMaximumBytes) + `"`)
		},
		"accepted retains payload": func(d *MailDelivery) { d.State = MailDeliveryAccepted; d.AcceptedAt = OptionalTimeFrom(at) },
		"queued loses payload":     func(d *MailDelivery) { d.EncryptedPayload = nil },
		"message date drifts":      func(d *MailDelivery) { d.MessageDate = at.Add(time.Second) },
		"sending before attempt":   func(d *MailDelivery) { d.State = MailDeliverySending },
		"queued retry lacks code":  func(d *MailDelivery) { d.AttemptCount = 1 },
		"accepted before attempt": func(d *MailDelivery) {
			d.State = MailDeliveryAccepted
			d.AcceptedAt = OptionalTimeFrom(at)
			d.EncryptedPayload = nil
		},
		"failed before attempt": func(d *MailDelivery) {
			d.State = MailDeliveryFailed
			d.FailedAt = OptionalTimeFrom(at)
			d.PublicFailureCode = "mail.transport.permanent"
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := queuedMailDeliveryFixture(at)
			mutate(candidate)
			if candidate.Validate() == nil {
				t.Fatal("invalid delivery accepted")
			}
		})
	}
}

func TestMailDeliveryAuditableOmitsRecipientAndCiphertext(t *testing.T) {
	delivery := queuedMailDeliveryFixture(time.Now().UTC())
	encoded, err := json.Marshal(delivery.Auditable())
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"example.test", "ciphertext", "secret", "message_id"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("audit leaked %q: %s", secret, encoded)
		}
	}
}

func TestIdentityMailCatalogAcceptsCredentialAndSecurityOccurrences(t *testing.T) {
	at := time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC)
	userID := NewUserID()
	for _, test := range []struct {
		key  MailTemplateKey
		kind MailOccurrenceKind
	}{
		{MailTemplateIdentityVerifyEmail, MailOccurrenceAccountToken},
		{MailTemplateIdentityEmailChangeVerifyNew, MailOccurrenceAccountToken},
		{MailTemplateIdentityEmailChangeWarningOld, MailOccurrenceSecurityNotice},
		{MailTemplateIdentityEmailVerifiedByAdmin, MailOccurrenceSecurityNotice},
		{MailTemplateIdentityPasswordReset, MailOccurrenceAccountToken},
		{MailTemplateIdentityPasswordChanged, MailOccurrenceSecurityNotice},
		{MailTemplateIdentityAccountDisabled, MailOccurrenceSecurityNotice},
		{MailTemplateIdentityAccountEnabled, MailOccurrenceSecurityNotice},
		{MailTemplateIdentitySessionsRevokedByAdmin, MailOccurrenceSecurityNotice},
		{MailTemplateIdentityMFAEnabled, MailOccurrenceSecurityNotice},
		{MailTemplateIdentityMFADisabled, MailOccurrenceSecurityNotice},
		{MailTemplateIdentityMFARecoveryCodesRegenerated, MailOccurrenceSecurityNotice},
		{MailTemplateIdentityPersonalAccessTokenCreated, MailOccurrenceSecurityNotice},
		{MailTemplateIdentityPersonalAccessTokenEnabled, MailOccurrenceSecurityNotice},
		{MailTemplateIdentityPersonalAccessTokenDisabled, MailOccurrenceSecurityNotice},
		{MailTemplateIdentityPersonalAccessTokenRevoked, MailOccurrenceSecurityNotice},
	} {
		occurrence := &MailOccurrence{
			ID: NewMailOccurrenceID(), Kind: test.kind, TemplateKey: test.key,
			ActorUserID: userID, CreatedAt: at,
		}
		if err := occurrence.Validate(); err != nil {
			t.Fatalf("%s occurrence: %v", test.key, err)
		}
		delivery := queuedMailDeliveryFixture(at)
		delivery.OccurrenceID = occurrence.ID
		delivery.TargetUserID = userID
		delivery.TemplateKey = test.key
		if err := delivery.Validate(); err != nil {
			t.Fatalf("%s delivery: %v", test.key, err)
		}
	}
}

func TestSittingScheduleMailMeaningsAreClosed(t *testing.T) {
	actorID := NewUserID()
	for _, key := range []MailTemplateKey{
		MailTemplateExamSittingScheduled,
		MailTemplateExamSittingRescheduled,
		MailTemplateExamSittingCancelled,
		MailTemplateExamSittingAssignmentRemoved,
	} {
		if !key.IsValid() {
			t.Fatalf("Sitting template %q is not in the closed catalog", key)
		}
		occurrence := &MailOccurrence{ID: NewMailOccurrenceID(), Kind: MailOccurrenceSittingSchedule,
			TemplateKey: key, ActorUserID: actorID, CreatedAt: NowUTC()}
		if err := occurrence.Validate(); err != nil {
			t.Fatalf("Sitting occurrence %q: %v", key, err)
		}
	}
	invalid := &MailOccurrence{ID: NewMailOccurrenceID(), Kind: MailOccurrenceSittingSchedule,
		TemplateKey: MailTemplateIdentityVerifyEmail, ActorUserID: actorID, CreatedAt: NowUTC()}
	if err := invalid.Validate(); err == nil {
		t.Fatal("Sitting occurrence accepted an account-token template")
	}
}
