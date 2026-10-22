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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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
			require.NoError(t, err)
			at := time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC)
			prepared, err := composer.PrepareOperatorTest(NoticePreparation{Recipient: payloadTestUser(at), At: at})
			require.Error(t, err, "invalid content was prepared: prepared")
			require.Nil(t, prepared, "invalid content was prepared: prepared")
		})
	}
}

func TestComposerPreservesRendererFailure(t *testing.T) {
	t.Parallel()
	renderErr := errors.New("renderer unavailable")
	composer, err := NewComposer(&payloadRendererFake{err: renderErr},
		&sittingSenderFake{enabled: true, from: Address{Address: "no-reply@example.test"}}, sittingTestSealer(t))
	require.NoError(t, err)
	at := time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC)
	prepared, err := composer.PrepareOperatorTest(NoticePreparation{Recipient: payloadTestUser(at), At: at})
	require.ErrorIs(t, err, renderErr, "renderer failure was lost: prepared")
	require.Nil(t, prepared, "renderer failure was lost: prepared")
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
	require.NoError(t, err)
	prepared, err := composer.PrepareOperatorTest(NoticePreparation{Recipient: recipient, At: at})
	require.NoError(t, err)
	want := Outbound{From: sender.from, EnvelopeFrom: sender.from.Address,
		To: Address{Name: recipient.DisplayName, Address: recipient.Email}, Subject: content.Subject, Text: content.Text, HTML: content.HTML,
		Headers:   map[string][]string{"Auto-Submitted": {"auto-generated"}, "X-Auto-Response-Suppress": {"All"}},
		MessageID: prepared.Delivery.MessageID, Date: at}
	require.NotContains(t, string(prepared.Delivery.EncryptedPayload), content.Text, "prepared payload exposes content or recipient")
	require.NotContains(t, string(prepared.Delivery.EncryptedPayload), recipient.Email, "prepared payload exposes content or recipient")
	renderer.content = FrozenContent{Subject: "Changed", Text: "Changed"}
	renderer.err = errors.New("renderer must not run during reopening")
	sender.from = Address{Address: "changed@example.test"}
	recipient.Email, recipient.DisplayName = "changed@example.test", "Changed"
	for range 2 {
		message, openErr := OpenDelivery(sealer, prepared.Delivery)
		require.NoError(t, openErr, "reopened message")
		require.Equal(t, want, message, "reopened message")
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
				require.NoError(t, json.Unmarshal(plaintext, &payload))
				test.edit(payload)
				encoded, err := json.Marshal(payload)
				require.NoError(t, err)
				return encoded
			})
			message, err := OpenDelivery(sealer, delivery)
			require.Error(t, err, "invalid payload reopened: message")
			require.Equal(t, test.invalidMsg, errors.Is(err, ErrInvalidMessage), "invalid payload reopened: message")
			require.Equal(t, Outbound{}, message, "invalid payload reopened: message")
			require.NotContains(t, err.Error(), "hidden@example.test", "validation error disclosed frozen content")
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
			require.Error(t, err, "malformed payload reopened: message")
			require.Equal(t, Outbound{}, message, "malformed payload reopened: message")
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
			require.Error(t, err, "invalid delivery reopened: message")
			require.Equal(t, test.invalidMsg, errors.Is(err, ErrInvalidMessage), "invalid delivery reopened: message")
			require.Equal(t, Outbound{}, message, "invalid delivery reopened: message")
		})
	}
	sealer, delivery := preparedPayloadTestDelivery(t)
	_, err := OpenDelivery(nil, delivery)
	require.Error(t, err, "missing sealer was accepted")
	_, err = OpenDelivery(sealer, nil)
	require.Error(t, err, "missing delivery was accepted")
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
				require.Error(t, err, "oversized envelope reopened: message")
				require.NotErrorIs(t, err, ErrInvalidMessage, "oversized envelope reopened: message")
				require.Equal(t, Outbound{}, message, "oversized envelope reopened: message")
				return
			}
			require.NoError(t, err, "valid envelope at limit did not reopen: message=%#v err", message)
			require.Equal(t, delivery.MessageID, message.MessageID, "valid envelope at limit did not reopen: message=%#v err", message)
			require.Equal(t, "Subject", message.Subject, "valid envelope at limit did not reopen: message=%#v err", message)
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
				require.ErrorIs(t, err, ErrInvalidMessage, "oversized Message-ID reopened: message")
				require.Equal(t, Outbound{}, message, "oversized Message-ID reopened: message")
				return
			}
			require.NoError(t, err, "valid Message-ID at limit did not reopen: message=%#v err", message)
			require.Equal(t, delivery.MessageID, message.MessageID, "valid Message-ID at limit did not reopen: message=%#v err", message)
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
	require.NoError(t, err)
	prepared, err := composer.PrepareOperatorTest(NoticePreparation{Recipient: payloadTestUser(at), At: at})
	require.NoError(t, err)
	return sealer, prepared.Delivery
}

// Corruption and format-evolution fixtures belong to Mail, the persisted format
// owner. Jobs tests prepare through Composer and never encode this format.
func rewritePayloadTestDelivery(t *testing.T, sealer *secretseal.Sealer, delivery *model.MailDelivery, edit func([]byte) []byte) {
	t.Helper()
	var envelope secretseal.Envelope
	require.NoError(t, json.Unmarshal(delivery.EncryptedPayload, &envelope))
	binding := secretseal.Binding{Purpose: DeliverySealingPurpose, Owner: delivery.ID.String()}
	plaintext, err := sealer.Open(binding, envelope)
	require.NoError(t, err)
	envelope, err = sealer.Seal(binding, edit(plaintext))
	require.NoError(t, err)
	delivery.EncryptedPayload, err = json.Marshal(envelope)
	require.NoError(t, err)
}
