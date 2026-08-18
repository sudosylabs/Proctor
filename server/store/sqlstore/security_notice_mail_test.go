// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

func TestValidateSecurityNoticeMailAcceptsMFANoticeFamilies(t *testing.T) {
	t.Parallel()

	keys := []model.MailTemplateKey{
		model.MailTemplateIdentityMFAEnabled,
		model.MailTemplateIdentityMFADisabled,
		model.MailTemplateIdentityMFARecoveryCodesRegenerated,
	}
	for _, key := range keys {
		key := key
		t.Run(string(key), func(t *testing.T) {
			t.Parallel()
			userID, occurrence, delivery, job, at := securityNoticeFixture(t, key)
			payloadKeyID, err := validateSecurityNoticeMail(userID, occurrence, delivery, job, key, at)
			if err != nil {
				t.Fatalf("validateSecurityNoticeMail() error = %v", err)
			}
			if payloadKeyID != strings.Repeat("a", 32) {
				t.Fatalf("payload key ID = %q", payloadKeyID)
			}
		})
	}
}

func TestValidateSecurityNoticeMailRejectsMismatchedLifecycle(t *testing.T) {
	t.Parallel()

	userID, occurrence, delivery, job, at := securityNoticeFixture(t, model.MailTemplateIdentityMFAEnabled)
	delivery.Deadline = delivery.Deadline.Add(time.Millisecond)
	if _, err := validateSecurityNoticeMail(userID, occurrence, delivery, job, model.MailTemplateIdentityMFAEnabled, at); err == nil {
		t.Fatal("validateSecurityNoticeMail() accepted a non-canonical notice lifetime")
	}
}

func securityNoticeFixture(t *testing.T, key model.MailTemplateKey) (model.UserID, *model.MailOccurrence, *model.MailDelivery, *model.Job, int64) {
	t.Helper()

	at := model.TimeFromMillis(model.MillisFromTime(model.NowUTC()))
	userID := model.NewUserID()
	occurrenceID := model.NewMailOccurrenceID()
	deliveryID := model.NewMailDeliveryID()
	command, err := model.EncodeMailDeliveryCommand(model.MailDeliveryCommandV1{DeliveryID: deliveryID})
	if err != nil {
		t.Fatal(err)
	}
	job, err := model.NewJob(model.NewJobID(), model.JobTypeMailDeliver, 1, command, deliveryID.String(), at, at, model.MailMaximumAttempts)
	if err != nil {
		t.Fatal(err)
	}
	occurrence := &model.MailOccurrence{ID: occurrenceID, Kind: model.MailOccurrenceSecurityNotice, TemplateKey: key, ActorUserID: userID, CreatedAt: at}
	delivery := &model.MailDelivery{
		ID: deliveryID, OccurrenceID: occurrenceID, JobID: job.ID, TargetUserID: userID,
		TemplateKey: key, TemplateDigest: strings.Repeat("a", 64), MaskedRecipient: "u***@example.test",
		State: model.MailDeliveryQueued, CreatedAt: at, UpdatedAt: at, MessageDate: at, Deadline: at.Add(securityNoticeLifetime),
		MessageID: "<mail." + deliveryID.String() + "@example.test>", EncryptedPayload: json.RawMessage(`{"key_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`), Revision: 1,
	}
	if err = occurrence.Validate(); err != nil {
		t.Fatal(err)
	}
	if err = delivery.Validate(); err != nil {
		t.Fatal(err)
	}
	return userID, occurrence, delivery, job, model.MillisFromTime(at)
}
