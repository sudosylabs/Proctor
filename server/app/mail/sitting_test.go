// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package mail

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/secretseal"
	"github.com/sudosylabs/proctor/server/store"
)

func TestSittingComposerFreezesAndSelectsRecipientLocales(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC)
	sealer := sittingTestSealer(t)
	composer, err := NewSittingComposer(localizedSittingRenderer{}, &sittingSenderFake{enabled: true,
		from: Address{Name: "Proctor", Address: "no-reply@example.test"}}, sealer, func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	sitting, err := model.NewExamSitting(model.NewExamSittingID(), model.NewExamID(), model.NewExamRevisionID(), model.NewClassID(),
		at.Add(24*time.Hour), at.Add(26*time.Hour), at)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := composer.Prepare(model.NewUserID(), sitting, store.ExamSittingMailScheduled,
		SittingScheduleDetails{ExamTitle: "Algorithms", ClassDisplayName: "Class A"})
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := composer.OpenBundle(prepared.Bundle)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Messages) != 2 || bundle.Messages["en"] == nil || bundle.Messages["fr"] == nil {
		t.Fatalf("frozen locales = %#v", bundle.Messages)
	}
	fanout := &store.ExamSittingMailFanoutSnapshot{Occurrence: prepared.Occurrence, Deadline: at.Add(72 * time.Hour)}
	for _, test := range []struct {
		locale      string
		wantSubject string
	}{{locale: "fr-FR", wantSubject: "fr:exam.sitting_scheduled"}, {locale: "de", wantSubject: "en:exam.sitting_scheduled"}} {
		recipient := &model.User{Username: "student", Email: model.NewId() + "@example.test", DisplayName: "Student",
			EmailVerified: true, Locale: test.locale, Timezone: "UTC"}
		recipient.PrepareCreate(model.NewUserID(), at.Add(-time.Hour))
		delivery, _, prepareErr := composer.PrepareRecipient(fanout, recipient, model.MailTemplateExamSittingScheduled, bundle)
		if prepareErr != nil {
			t.Fatalf("PrepareRecipient(%q): %v", test.locale, prepareErr)
		}
		payload := openSittingTestPayload(t, sealer, delivery)
		if payload.Subject != test.wantSubject {
			t.Fatalf("PrepareRecipient(%q) subject = %q, want %q", test.locale, payload.Subject, test.wantSubject)
		}
	}
}

type localizedSittingRenderer struct{}

func (localizedSittingRenderer) SittingLocales() (string, []string) {
	return "en", []string{"en", "fr"}
}

func (localizedSittingRenderer) Render(request RenderRequest) (FrozenContent, error) {
	details, ok := request.Presentation.(SittingScheduleDetails)
	if !ok {
		return FrozenContent{}, nil
	}
	key, locale := request.Key, request.Locale
	return FrozenContent{Subject: locale + ":" + string(key), Text: locale + ":" + details.ExamTitle,
		HTML: "<p>" + locale + ":" + details.ExamTitle + "</p>"}, nil
}

type sittingSenderFake struct {
	enabled bool
	from    Address
}

func (s *sittingSenderFake) Enabled() bool { return s.enabled }
func (s *sittingSenderFake) From() Address { return s.from }
func (s *sittingSenderFake) Send(context.Context, Outbound) (TransportOutcome, error) {
	return TransportUnknown, nil
}

func sittingTestSealer(t *testing.T) *secretseal.Sealer {
	t.Helper()
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	sealer, err := secretseal.New(secretseal.Settings{EncryptionKey: key, MaximumPlaintext: secretseal.MaximumPlaintextBytes})
	if err != nil {
		t.Fatal(err)
	}
	return sealer
}

func openSittingTestPayload(t *testing.T, sealer *secretseal.Sealer, delivery *model.MailDelivery) FrozenPayloadV1 {
	t.Helper()
	var envelope secretseal.Envelope
	if err := json.Unmarshal(delivery.EncryptedPayload, &envelope); err != nil {
		t.Fatal(err)
	}
	plaintext, err := sealer.Open(secretseal.Binding{Purpose: DeliverySealingPurpose, Owner: delivery.ID.String()}, envelope)
	if err != nil {
		t.Fatal(err)
	}
	var payload FrozenPayloadV1
	if err = json.Unmarshal(plaintext, &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}
