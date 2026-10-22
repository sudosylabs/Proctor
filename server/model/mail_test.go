// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func queuedMailDeliveryFixture(at time.Time) *MailDelivery {
	return &MailDelivery{ID: NewMailDeliveryID(), OccurrenceID: NewMailOccurrenceID(), JobID: NewJobID(), TargetUserID: NewUserID(),
		TemplateKey: MailTemplateSystemTest, TemplateDigest: strings.Repeat("a", 64), MaskedRecipient: "a***@example.test",
		State: MailDeliveryQueued, CreatedAt: at, UpdatedAt: at, MessageDate: at, Deadline: at.Add(24 * time.Hour),
		MessageID: "<mail." + NewID() + "@example.test>", EncryptedPayload: json.RawMessage(`{"version":1,"ciphertext":"secret"}`), Revision: 1}
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
		assert.True(t, key.IsValid(), "MailTemplateKey(%q) is invalid", key)
		occurrence := &MailOccurrence{ID: NewMailOccurrenceID(), Kind: MailOccurrenceExamManagement,
			TemplateKey: key, ActorUserID: NewUserID(), CreatedAt: at}
		assert.NoError(t, occurrence.Validate(), "MailOccurrence(%q)", key)
	}
	invalid := &MailOccurrence{ID: NewMailOccurrenceID(), Kind: MailOccurrenceExamManagement,
		TemplateKey: MailTemplateIdentityPasswordChanged, ActorUserID: NewUserID(), CreatedAt: at}
	require.Error(t, invalid.Validate(), "Exam management occurrence accepted an identity-security template")
}

func TestAcademicAdministrationMailMeaningsAreClosed(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	for _, key := range []MailTemplateKey{
		MailTemplateAcademicClassEnrolled,
		MailTemplateAcademicClassEnrollmentEnded,
		MailTemplateAcademicClassTransferred,
		MailTemplateAcademicUnitAssigned,
		MailTemplateAcademicUnitAssignmentEnded,
		MailTemplateAuthorizationScopedRoleAssigned,
		MailTemplateAuthorizationScopedRoleEnded,
		MailTemplateAuthorizationInstitutionRoleAssigned,
		MailTemplateAuthorizationInstitutionRoleEnded,
	} {
		assert.True(t, key.IsValid(), "MailTemplateKey(%q) is invalid", key)
		occurrence := &MailOccurrence{ID: NewMailOccurrenceID(), Kind: MailOccurrenceAcademicAdministration,
			TemplateKey: key, ActorUserID: NewUserID(), CreatedAt: at}
		assert.NoError(t, occurrence.Validate(), "MailOccurrence(%q)", key)
	}
	invalid := &MailOccurrence{ID: NewMailOccurrenceID(), Kind: MailOccurrenceAcademicAdministration,
		TemplateKey: MailTemplateIdentityPasswordChanged, ActorUserID: NewUserID(), CreatedAt: at}
	require.Error(t, invalid.Validate(), "academic administration occurrence accepted an identity-security template")
}

func TestExamSubmissionReceiptMailMeaningsAreClosed(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	for _, key := range []MailTemplateKey{
		MailTemplateExamSubmissionReceived,
		MailTemplateExamSubmissionAutomaticallySealed,
	} {
		assert.True(t, key.IsValid(), "MailTemplateKey(%q) is invalid", key)
		occurrence := &MailOccurrence{ID: NewMailOccurrenceID(), Kind: MailOccurrenceSubmissionReceipt,
			TemplateKey: key, ActorUserID: NewUserID(), CreatedAt: at}
		assert.NoError(t, occurrence.Validate(), "MailOccurrence(%q)", key)
	}
	invalid := &MailOccurrence{ID: NewMailOccurrenceID(), Kind: MailOccurrenceSubmissionReceipt,
		TemplateKey: MailTemplateExamSittingScheduled, ActorUserID: NewUserID(), CreatedAt: at}
	require.Error(t, invalid.Validate(), "Submission receipt occurrence accepted a Sitting schedule template")
}

func TestExamResultReleaseMailMeaningIsClosed(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 21, 13, 0, 0, 0, time.UTC)
	require.True(t, MailTemplateExamResultReleased.IsValid(), "result-release mail template is invalid")
	occurrence := &MailOccurrence{ID: NewMailOccurrenceID(), Kind: MailOccurrenceResultRelease,
		TemplateKey: MailTemplateExamResultReleased, ActorUserID: NewUserID(), CreatedAt: at}
	require.NoError(t, occurrence.Validate())
	occurrence.TemplateKey = MailTemplateExamSubmissionReceived
	require.Error(t, occurrence.Validate(), "result-release occurrence accepted a Submission receipt template")
}

func TestScopedRoleInvitationMailMeaningsAreClosed(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	for _, key := range []MailTemplateKey{
		MailTemplateAccessAcademicUnitRoleInvitation,
		MailTemplateAccessInstitutionRoleInvitation,
	} {
		assert.True(t, key.IsValid(), "MailTemplateKey(%q) is invalid", key)
		occurrence := &MailOccurrence{ID: NewMailOccurrenceID(), Kind: MailOccurrenceInvitation,
			TemplateKey: key, ActorUserID: NewUserID(), CreatedAt: at}
		assert.NoError(t, occurrence.Validate(), "MailOccurrence(%q)", key)
	}
}

func TestMailDeliveryLifecyclePreservesStableRoutingAndDestroysAcceptedPayload(t *testing.T) {
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	queued := queuedMailDeliveryFixture(at)
	require.NoError(t, queued.Validate())
	sending, err := queued.Start(at.Add(time.Second))
	require.NoError(t, err)
	require.Equal(t, queued.MessageID, sending.MessageID)
	require.Equal(t, 1, sending.AttemptCount)
	require.Equal(t, MailDeliverySending, sending.State)
	retrying, err := sending.Retry("mail.transport.temporary", at.Add(2*time.Second))
	require.NoError(t, err)
	second, err := retrying.Start(at.Add(3 * time.Second))
	require.NoError(t, err)
	accepted, err := second.Accept(at.Add(4 * time.Second))
	require.NoError(t, err)
	require.Equal(t, MailDeliveryAccepted, accepted.State)
	require.True(t, accepted.AcceptedAt.Valid)
	require.Empty(t, accepted.EncryptedPayload)
	require.Equal(t, queued.MessageID, accepted.MessageID)
	require.Equal(t, 2, accepted.AttemptCount)
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
			require.NoError(t, err)
			require.Equal(t, MailDeliverySuppressed, expired.State)
			require.Equal(t, "mail.delivery.expired", expired.PublicFailureCode)
			require.Empty(t, expired.EncryptedPayload)
			require.Equal(t, delivery.Revision+1, expired.Revision)
			require.True(t, expired.UpdatedAt.Equal(delivery.Deadline))
		})
	}
	_, err := queued.Expire(queued.Deadline.Add(-time.Nanosecond))
	require.Error(t, err, "Expire() accepted a pre-deadline transition")
}

func TestMailDeliveryOperatorControlCancelsQueuedAndRetriesFailedInPlace(t *testing.T) {
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	queued := queuedMailDeliveryFixture(at)
	canceled, err := queued.Cancel(at.Add(time.Second))
	require.NoError(t, err)
	require.Equal(t, queued.ID, canceled.ID)
	require.Equal(t, queued.MessageID, canceled.MessageID)
	require.Equal(t, MailDeliveryCanceled, canceled.State)
	require.Empty(t, canceled.EncryptedPayload)
	require.Equal(t, queued.Revision+1, canceled.Revision)
	_, err = canceled.Cancel(at.Add(2 * time.Second))
	require.Error(t, err, "Cancel() accepted a terminal delivery")

	failed, err := queued.Start(at.Add(time.Second))
	require.NoError(t, err)
	failed, err = failed.Fail("mail.transport.permanent", at.Add(2*time.Second))
	require.NoError(t, err)
	retried, err := failed.OperatorRetry(at.Add(3 * time.Second))
	require.NoError(t, err)
	require.Equal(t, failed.ID, retried.ID)
	require.Equal(t, failed.MessageID, retried.MessageID)
	require.Equal(t, MailDeliveryQueued, retried.State)
	require.True(t, retried.FailedAt.IsZero())
	require.Equal(t, MailDeliveryOperatorRetryCode, retried.PublicFailureCode)
	require.NotEmpty(t, retried.EncryptedPayload)
	require.Equal(t, failed.Revision+1, retried.Revision)
	_, err = failed.OperatorRetry(failed.Deadline)
	require.Error(t, err, "OperatorRetry() accepted an expired delivery")

	// A delivery whose Job failed while it was still Queued or Sending is
	// requeued the same way; only an attempted delivery carries the marker.
	orphanedQueued, err := queued.OperatorRetry(at.Add(time.Second))
	require.NoError(t, err, "OperatorRetry(queued)")
	require.Equal(t, MailDeliveryQueued, orphanedQueued.State)
	require.Empty(t, orphanedQueued.PublicFailureCode)
	sending, err := queued.Start(at.Add(time.Second))
	require.NoError(t, err)
	orphanedSending, err := sending.OperatorRetry(at.Add(2 * time.Second))
	require.NoError(t, err, "OperatorRetry(sending)")
	require.Equal(t, MailDeliveryQueued, orphanedSending.State)
	require.Equal(t, MailDeliveryOperatorRetryCode, orphanedSending.PublicFailureCode)
	exhausted := sending.Clone()
	exhausted.AttemptCount = MailMaximumAttempts
	_, err = exhausted.OperatorRetry(at.Add(2 * time.Second))
	require.Error(t, err, "OperatorRetry() accepted a delivery without attempts left")
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
				require.NoError(t, err)
			}
			suppressed, err := delivery.Suppress(MailDeliveryDisabledCode, at.Add(3*time.Second))
			require.NoError(t, err)
			require.Equal(t, MailDeliverySuppressed, suppressed.State)
			require.Equal(t, MailDeliveryDisabledCode, suppressed.PublicFailureCode)
			require.Empty(t, suppressed.EncryptedPayload)
			require.False(t, suppressed.AcceptedAt.Valid)
			require.False(t, suppressed.FailedAt.Valid)
			require.Equal(t, delivery.Revision+1, suppressed.Revision)
			_, err = suppressed.Suppress(MailDeliveryDisabledCode, at.Add(4*time.Second))
			require.Error(t, err, "Suppress() accepted terminal delivery")
		})
	}
}

func mustStartMailDelivery(t *testing.T, delivery *MailDelivery, at time.Time) *MailDelivery {
	t.Helper()
	started, err := delivery.Start(at)
	require.NoError(t, err)
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
			require.Error(t, candidate.Validate(), "invalid delivery accepted")
		})
	}
}

func TestMailDeliveryAuditableOmitsRecipientAndCiphertext(t *testing.T) {
	delivery := queuedMailDeliveryFixture(time.Now().UTC())
	encoded, err := json.Marshal(delivery.Auditable())
	require.NoError(t, err)
	for _, secret := range []string{"example.test", "ciphertext", "secret", "message_id"} {
		require.NotContains(t, string(encoded), secret, "audit leaked")
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
		t.Run(string(test.key), func(t *testing.T) {
			occurrence := &MailOccurrence{
				ID: NewMailOccurrenceID(), Kind: test.kind, TemplateKey: test.key,
				ActorUserID: userID, CreatedAt: at,
			}
			require.NoError(t, occurrence.Validate(), "%s occurrence", test.key)
			delivery := queuedMailDeliveryFixture(at)
			delivery.OccurrenceID = occurrence.ID
			delivery.TargetUserID = userID
			delivery.TemplateKey = test.key
			require.NoError(t, delivery.Validate(), "%s delivery", test.key)
		})
	}
}

func TestSittingScheduleMailMeaningsAreClosed(t *testing.T) {
	actorID := NewUserID()
	for _, key := range []MailTemplateKey{
		MailTemplateExamSittingScheduled,
		MailTemplateExamSittingRescheduled,
		MailTemplateExamSittingCanceled,
		MailTemplateExamSittingAssignmentRemoved,
	} {
		require.True(t, key.IsValid(), "Sitting template %q is not in the closed catalog", key)
		occurrence := &MailOccurrence{ID: NewMailOccurrenceID(), Kind: MailOccurrenceSittingSchedule,
			TemplateKey: key, ActorUserID: actorID, CreatedAt: NowUTC()}
		require.NoError(t, occurrence.Validate(), "Sitting occurrence %q", key)
	}
	invalid := &MailOccurrence{ID: NewMailOccurrenceID(), Kind: MailOccurrenceSittingSchedule,
		TemplateKey: MailTemplateIdentityVerifyEmail, ActorUserID: actorID, CreatedAt: NowUTC()}
	require.Error(t, invalid.Validate(), "Sitting occurrence accepted an account-token template")
}
