// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func userDisabledStateChangeWithNotice(t *testing.T, input *store.UserDisabledStateChange) *store.UserDisabledStateChange {
	t.Helper()
	key := model.MailTemplateIdentityAccountEnabled
	if input.Disabled {
		key = model.MailTemplateIdentityAccountDisabled
	}
	input.Occurrence, input.Delivery, input.DeliveryJob = securityNoticeMailFixture(t, model.UserID(input.ID), key, input.ChangedAt)
	return input
}

// UserDisabledStateChangeWithNotice equips cross-package integration fixtures
// with the mandatory atomic account-state security notice.
func UserDisabledStateChangeWithNotice(t *testing.T, input *store.UserDisabledStateChange) *store.UserDisabledStateChange {
	t.Helper()
	userDisabledStateChangeWithNotice(t, input)
	input.Delivery, input.DeliveryJob = suppressSecurityNoticeForDisabledMail(t, input.Delivery, input.DeliveryJob)
	return input
}

func sessionRevocationWithNotice(t *testing.T, input *store.SessionRevocation) *store.SessionRevocation {
	t.Helper()
	input.Occurrence, input.Delivery, input.DeliveryJob = securityNoticeMailFixture(t, model.UserID(input.UserID), model.MailTemplateIdentitySessionsRevokedByAdmin, input.RevokedAt)
	return input
}

func userSessionsRevocationWithNotice(t *testing.T, input *store.UserSessionsRevocation) *store.UserSessionsRevocation {
	t.Helper()
	input.Occurrence, input.Delivery, input.DeliveryJob = securityNoticeMailFixture(t, model.UserID(input.UserID), model.MailTemplateIdentitySessionsRevokedByAdmin, input.RevokedAt)
	return input
}

func securityNoticeMailFixture(t *testing.T, userID model.UserID, key model.MailTemplateKey, atMillis int64) (*model.MailOccurrence, *model.MailDelivery, *model.Job) {
	t.Helper()
	at := model.TimeFromMillis(atMillis)
	return userTokenMailFixture(t, userID, model.NewMailOccurrenceID(), model.MailOccurrenceSecurityNotice, key, model.JobTypeMailDeliver, at, at.Add(24*time.Hour))
}

func suppressSecurityNoticeForDisabledMail(t *testing.T, delivery *model.MailDelivery, job *model.Job) (*model.MailDelivery, *model.Job) {
	t.Helper()
	canceled, err := job.RequestCancellation(delivery.CreatedAt)
	requireNoError(t, err)
	suppressed := delivery.Clone()
	suppressed.State = model.MailDeliverySuppressed
	suppressed.PublicFailureCode = model.MailDeliveryDisabledCode
	suppressed.EncryptedPayload = nil
	if err = suppressed.Validate(); err != nil {
		t.Fatal(err)
	}
	return suppressed, canceled
}
