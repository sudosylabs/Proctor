// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package mail

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/secretseal"
)

func TestComposerRejectsInvalidFrozenContent(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		content FrozenContent
	}{
		{"empty subject", FrozenContent{Text: "body"}},
		{"empty alternatives", FrozenContent{Subject: "subject"}},
		{"subject header injection", FrozenContent{Subject: "subject\r\nBcc: hidden@example.test", Text: "body"}},
		{"subject NUL", FrozenContent{Subject: "subject\x00", Text: "body"}},
		{"oversized payload", FrozenContent{Subject: "subject", Text: strings.Repeat("x", model.MailRenderedPayloadMaximumBytes)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			composer, err := NewComposer(&payloadRendererFake{content: test.content},
				&sittingSenderFake{enabled: true, from: Address{Address: "no-reply@example.test"}}, sittingTestSealer(t))
			if err != nil {
				t.Fatal(err)
			}
			at := time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC)
			prepared, err := composer.PrepareOperatorTest(NoticePreparation{Recipient: payloadTestUser(at), At: at})
			if err == nil || prepared != nil {
				t.Fatalf("invalid content was prepared: prepared=%#v err=%v", prepared, err)
			}
		})
	}
}

func TestComposerPreservesRendererFailure(t *testing.T) {
	t.Parallel()
	renderErr := errors.New("renderer unavailable")
	composer, err := NewComposer(&payloadRendererFake{err: renderErr},
		&sittingSenderFake{enabled: true, from: Address{Address: "no-reply@example.test"}}, sittingTestSealer(t))
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC)
	prepared, err := composer.PrepareOperatorTest(NoticePreparation{Recipient: payloadTestUser(at), At: at})
	if !errors.Is(err, renderErr) || prepared != nil {
		t.Fatalf("renderer failure was lost: prepared=%#v err=%v", prepared, err)
	}
}

func TestOpenDeliveryPreservesPreparedContent(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC)
	sealer := sittingTestSealer(t)
	content := FrozenContent{Subject: "Frozen subject", Text: "Frozen text", HTML: "<p>Frozen HTML</p>"}
	renderer := &payloadRendererFake{content: content}
	sender := &sittingSenderFake{enabled: true, from: Address{Name: "Proctor", Address: "no-reply@example.test"}}
	recipient := payloadTestUser(at)
	composer, err := NewComposer(renderer, sender, sealer)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := composer.PrepareOperatorTest(NoticePreparation{Recipient: recipient, At: at})
	if err != nil {
		t.Fatal(err)
	}
	want := Outbound{From: sender.from, EnvelopeFrom: sender.from.Address,
		To: Address{Name: recipient.DisplayName, Address: recipient.Email}, Subject: content.Subject, Text: content.Text, HTML: content.HTML,
		Headers:   map[string][]string{"Auto-Submitted": {"auto-generated"}, "X-Auto-Response-Suppress": {"All"}},
		MessageID: prepared.Delivery.MessageID, Date: at}
	if strings.Contains(string(prepared.Delivery.EncryptedPayload), content.Text) ||
		strings.Contains(string(prepared.Delivery.EncryptedPayload), recipient.Email) {
		t.Fatal("prepared payload exposes content or recipient")
	}
	renderer.content = FrozenContent{Subject: "Changed", Text: "Changed"}
	renderer.err = errors.New("renderer must not run during reopening")
	sender.from = Address{Address: "changed@example.test"}
	recipient.Email, recipient.DisplayName = "changed@example.test", "Changed"
	for range 2 {
		message, openErr := OpenDelivery(sealer, prepared.Delivery)
		if openErr != nil || !reflect.DeepEqual(message, want) {
			t.Fatalf("reopened message=%#v err=%v, want=%#v", message, openErr, want)
		}
		// A transport may modify its own headers without changing the next retry.
		message.Headers["Auto-Submitted"][0] = "changed"
	}
}

func TestOpenDeliveryRejectsInvalidPayloadVersionAndMeaning(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		edit       func(map[string]any)
		invalidMsg bool
	}{
		{"unsupported version", func(p map[string]any) { p["version"] = 2 }, false},
		{"missing version", func(p map[string]any) { delete(p, "version") }, false},
		{"missing recipient", func(p map[string]any) { delete(p, "recipient_address") }, false},
		{"missing sender", func(p map[string]any) { delete(p, "from_address") }, false},
		{"missing subject", func(p map[string]any) { delete(p, "subject") }, false},
		{"missing alternatives", func(p map[string]any) { delete(p, "text"); delete(p, "html") }, false},
		{"incorrect auto submitted", func(p map[string]any) { p["auto_submitted"] = "no" }, false},
		{"incorrect suppression", func(p map[string]any) { p["auto_response_suppress"] = "None" }, false},
		{"unknown field", func(p map[string]any) { p["extra"] = "unsupported" }, false},
		{"invalid sender", func(p map[string]any) { p["from_address"] = "invalid" }, true},
		{"invalid recipient", func(p map[string]any) { p["recipient_address"] = "invalid" }, true},
		{"sender name injection", func(p map[string]any) { p["from_name"] = "Sender\r\nBcc: hidden@example.test" }, true},
		{"recipient name injection", func(p map[string]any) { p["recipient_name"] = "Recipient\x00" }, true},
		{"subject injection", func(p map[string]any) { p["subject"] = "Subject\r\nBcc: hidden@example.test" }, true},
		{"oversized plaintext", func(p map[string]any) { p["text"] = strings.Repeat("x", model.MailRenderedPayloadMaximumBytes) }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			sealer, delivery := preparedPayloadTestDelivery(t)
			rewritePayloadTestDelivery(t, sealer, delivery, func(plaintext []byte) []byte {
				var payload map[string]any
				if err := json.Unmarshal(plaintext, &payload); err != nil {
					t.Fatal(err)
				}
				test.edit(payload)
				encoded, err := json.Marshal(payload)
				if err != nil {
					t.Fatal(err)
				}
				return encoded
			})
			message, err := OpenDelivery(sealer, delivery)
			if err == nil || errors.Is(err, ErrInvalidMessage) != test.invalidMsg || !reflect.DeepEqual(message, Outbound{}) {
				t.Fatalf("invalid payload reopened: message=%#v err=%v", message, err)
			}
			if strings.Contains(err.Error(), "hidden@example.test") {
				t.Fatal("validation error disclosed frozen content")
			}
		})
	}
}

func TestOpenDeliveryRejectsMalformedAndTrailingJSON(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		edit func([]byte) []byte
	}{
		{"truncated document", func(p []byte) []byte { return p[:len(p)-1] }},
		{"extra object", func(p []byte) []byte { return append(p, []byte(` {}`)...) }},
		{"extra scalar", func(p []byte) []byte { return append(p, []byte(` true`)...) }},
		{"extra null", func(p []byte) []byte { return append(p, []byte(` null`)...) }},
		{"malformed trailing JSON", func(p []byte) []byte { return append(p, []byte(` {`)...) }},
		{"trailing garbage", func(p []byte) []byte { return append(p, []byte(` invalid`)...) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			sealer, delivery := preparedPayloadTestDelivery(t)
			rewritePayloadTestDelivery(t, sealer, delivery, test.edit)
			message, err := OpenDelivery(sealer, delivery)
			if err == nil || !reflect.DeepEqual(message, Outbound{}) {
				t.Fatalf("malformed payload reopened: message=%#v err=%v", message, err)
			}
		})
	}
}

func TestOpenDeliveryRejectsUnavailableEnvelopeAndMetadata(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		edit       func(*model.MailDelivery)
		invalidMsg bool
	}{
		{"missing ciphertext", func(d *model.MailDelivery) { d.EncryptedPayload = nil }, false},
		{"invalid envelope", func(d *model.MailDelivery) { d.EncryptedPayload = json.RawMessage(`{`) }, false},
		{"corrupt ciphertext", func(d *model.MailDelivery) { d.EncryptedPayload = json.RawMessage(`{"ciphertext":"corrupt"}`) }, false},
		{"wrong owner", func(d *model.MailDelivery) { d.ID = model.NewMailDeliveryID() }, false},
		{"missing message ID", func(d *model.MailDelivery) { d.MessageID = "" }, true},
		{"injected message ID", func(d *model.MailDelivery) { d.MessageID += "\r\nBcc: hidden@example.test" }, true},
		{"missing message date", func(d *model.MailDelivery) { d.MessageDate = time.Time{} }, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			sealer, delivery := preparedPayloadTestDelivery(t)
			test.edit(delivery)
			message, err := OpenDelivery(sealer, delivery)
			if err == nil || errors.Is(err, ErrInvalidMessage) != test.invalidMsg || !reflect.DeepEqual(message, Outbound{}) {
				t.Fatalf("invalid delivery reopened: message=%#v err=%v", message, err)
			}
		})
	}
	sealer, delivery := preparedPayloadTestDelivery(t)
	if _, err := OpenDelivery(nil, delivery); err == nil {
		t.Fatal("missing sealer was accepted")
	}
	if _, err := OpenDelivery(sealer, nil); err == nil {
		t.Fatal("missing delivery was accepted")
	}
}

func TestOpenDeliveryBoundsEncryptedPayload(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		size    int
		wantErr bool
	}{
		{"at limit", model.MailEncryptedPayloadMaximumBytes, false},
		{"above limit", model.MailEncryptedPayloadMaximumBytes + 1, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			sealer, delivery := preparedPayloadTestDelivery(t)
			// JSON whitespace leaves the authentic envelope unchanged, so only
			// its persisted byte limit can reject the larger representation.
			delivery.EncryptedPayload = append(delivery.EncryptedPayload,
				strings.Repeat(" ", test.size-len(delivery.EncryptedPayload))...)
			message, err := OpenDelivery(sealer, delivery)
			if test.wantErr {
				if err == nil || errors.Is(err, ErrInvalidMessage) || !reflect.DeepEqual(message, Outbound{}) {
					t.Fatalf("oversized envelope reopened: message=%#v err=%v", message, err)
				}
				return
			}
			if err != nil || message.MessageID != delivery.MessageID || message.Subject != "Subject" {
				t.Fatalf("valid envelope at limit did not reopen: message=%#v err=%v", message, err)
			}
		})
	}
}

func TestOpenDeliveryBoundsMessageID(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		size    int
		wantErr bool
	}{
		{"at limit", model.MailMessageIDMaximumBytes, false},
		{"above limit", model.MailMessageIDMaximumBytes + 1, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			sealer, delivery := preparedPayloadTestDelivery(t)
			delivery.MessageID = "<" + strings.Repeat("a", test.size-len("<@example.test>")) + "@example.test>"
			message, err := OpenDelivery(sealer, delivery)
			if test.wantErr {
				if !errors.Is(err, ErrInvalidMessage) || !reflect.DeepEqual(message, Outbound{}) {
					t.Fatalf("oversized Message-ID reopened: message=%#v err=%v", message, err)
				}
				return
			}
			if err != nil || message.MessageID != delivery.MessageID {
				t.Fatalf("valid Message-ID at limit did not reopen: message=%#v err=%v", message, err)
			}
		})
	}
}

type payloadRendererFake struct {
	content FrozenContent
	err     error
}

func (r *payloadRendererFake) Render(RenderRequest) (FrozenContent, error) { return r.content, r.err }

func payloadTestUser(at time.Time) *model.User {
	user := &model.User{Username: "operator", Email: "operator@example.test", DisplayName: "Operator", EmailVerified: true,
		Locale: "en", Timezone: "UTC"}
	user.PrepareCreate(model.NewUserID(), at.Add(-time.Hour))
	return user
}

func preparedPayloadTestDelivery(t *testing.T) (*secretseal.Sealer, *model.MailDelivery) {
	t.Helper()
	at := time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC)
	sealer := sittingTestSealer(t)
	composer, err := NewComposer(&payloadRendererFake{content: FrozenContent{Subject: "Subject", Text: "Text", HTML: "<p>HTML</p>"}},
		&sittingSenderFake{enabled: true, from: Address{Name: "Proctor", Address: "no-reply@example.test"}}, sealer)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := composer.PrepareOperatorTest(NoticePreparation{Recipient: payloadTestUser(at), At: at})
	if err != nil {
		t.Fatal(err)
	}
	return sealer, prepared.Delivery
}

// Corruption and format-evolution fixtures belong to Mail, the persisted format
// owner. Jobs tests prepare through Composer and never encode this format.
func rewritePayloadTestDelivery(t *testing.T, sealer *secretseal.Sealer, delivery *model.MailDelivery, edit func([]byte) []byte) {
	t.Helper()
	var envelope secretseal.Envelope
	if err := json.Unmarshal(delivery.EncryptedPayload, &envelope); err != nil {
		t.Fatal(err)
	}
	binding := secretseal.Binding{Purpose: DeliverySealingPurpose, Owner: delivery.ID.String()}
	plaintext, err := sealer.Open(binding, envelope)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err = sealer.Seal(binding, edit(plaintext))
	if err != nil {
		t.Fatal(err)
	}
	delivery.EncryptedPayload, err = json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
}
