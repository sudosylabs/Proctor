// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package mail

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/secretseal"
)

// ErrInvalidMessage identifies frozen content or metadata that cannot form a
// valid outbound message. Reopening failures otherwise describe an unavailable
// or unsupported payload. Neither error class includes frozen content.
var ErrInvalidMessage = errors.New("mail message is invalid")

// frozenPayloadV1 is the private persisted representation shared by direct
// composition, fan-out composition, and delivery reopening.
type frozenPayloadV1 struct {
	Version              int    `json:"version"`
	RecipientName        string `json:"recipient_name"`
	RecipientAddress     string `json:"recipient_address"`
	FromName             string `json:"from_name"`
	FromAddress          string `json:"from_address"`
	Subject              string `json:"subject"`
	Text                 string `json:"text"`
	HTML                 string `json:"html"`
	AutoSubmitted        string `json:"auto_submitted"`
	AutoResponseSuppress string `json:"auto_response_suppress"`
}

type frozenDeliveryPayload struct {
	encrypted      json.RawMessage
	templateDigest string
	messageID      string
}

func freezeDeliveryPayload(sealer *secretseal.Sealer, deliveryID model.MailDeliveryID, from, recipient Address,
	content FrozenContent,
) (frozenDeliveryPayload, error) {
	if sealer == nil || !deliveryID.IsValid() {
		return frozenDeliveryPayload{}, errors.New("mail delivery payload input is invalid")
	}
	payload := frozenPayloadV1{Version: 1, RecipientName: recipient.Name, RecipientAddress: recipient.Address,
		FromName: from.Name, FromAddress: from.Address, Subject: content.Subject, Text: content.Text, HTML: content.HTML,
		AutoSubmitted: "auto-generated", AutoResponseSuppress: "All"}
	if err := payload.validate(); err != nil {
		return frozenDeliveryPayload{}, err
	}
	plaintext, err := json.Marshal(payload)
	if err != nil || len(plaintext) > model.MailRenderedPayloadMaximumBytes {
		return frozenDeliveryPayload{}, errors.New("rendered mail payload is invalid")
	}
	envelope, err := sealer.Seal(secretseal.Binding{Purpose: DeliverySealingPurpose, Owner: deliveryID.String()}, plaintext)
	if err != nil {
		return frozenDeliveryPayload{}, err
	}
	encrypted, err := json.Marshal(envelope)
	if err != nil {
		return frozenDeliveryPayload{}, err
	}
	return frozenDeliveryPayload{encrypted: encrypted, templateDigest: Digest(content.Subject, content.Text, content.HTML),
		messageID: StableMessageID(deliveryID, from.Address)}, nil
}

// OpenDelivery reopens and validates one immutable prepared delivery for Sender.
// Recipient, sender, content, and automatic-response headers come only from the
// frozen payload; Message-ID and Date retain the delivery's original metadata.
// It neither resolves current recipient details nor renders templates again.
func OpenDelivery(sealer *secretseal.Sealer, delivery *model.MailDelivery) (Outbound, error) {
	var envelope secretseal.Envelope
	if sealer == nil || delivery == nil || !delivery.ID.IsValid() || len(delivery.EncryptedPayload) == 0 ||
		len(delivery.EncryptedPayload) > model.MailEncryptedPayloadMaximumBytes ||
		json.Unmarshal(delivery.EncryptedPayload, &envelope) != nil {
		return Outbound{}, errors.New("encrypted mail payload is invalid")
	}
	plaintext, err := sealer.Open(secretseal.Binding{Purpose: DeliverySealingPurpose, Owner: delivery.ID.String()}, envelope)
	if err != nil {
		return Outbound{}, err
	}
	if len(plaintext) > model.MailRenderedPayloadMaximumBytes {
		return Outbound{}, errors.New("rendered mail payload is invalid")
	}
	var payload frozenPayloadV1
	decoder := json.NewDecoder(bytes.NewReader(plaintext))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&payload) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return Outbound{}, errors.New("frozen mail payload is invalid")
	}
	if err = payload.validate(); err != nil {
		return Outbound{}, err
	}
	if delivery.MessageID == "" || len(delivery.MessageID) > model.MailMessageIDMaximumBytes ||
		strings.ContainsAny(delivery.MessageID, "\x00\r\n") || delivery.MessageDate.IsZero() {
		return Outbound{}, ErrInvalidMessage
	}
	return Outbound{
		From: Address{Name: payload.FromName, Address: payload.FromAddress}, EnvelopeFrom: payload.FromAddress,
		To:      Address{Name: payload.RecipientName, Address: payload.RecipientAddress},
		Subject: payload.Subject, Text: payload.Text, HTML: payload.HTML,
		Headers:   map[string][]string{"Auto-Submitted": {payload.AutoSubmitted}, "X-Auto-Response-Suppress": {payload.AutoResponseSuppress}},
		MessageID: delivery.MessageID, Date: delivery.MessageDate,
	}, nil
}

func (p frozenPayloadV1) validate() error {
	if p.Version != 1 || p.RecipientAddress == "" || p.FromAddress == "" || p.Subject == "" ||
		(p.Text == "" && p.HTML == "") || p.AutoSubmitted != "auto-generated" || p.AutoResponseSuppress != "All" {
		return errors.New("frozen mail payload is invalid")
	}
	if ValidateAddress(Address{Name: p.FromName, Address: p.FromAddress}) != nil ||
		ValidateAddress(Address{Name: p.RecipientName, Address: p.RecipientAddress}) != nil ||
		strings.ContainsAny(p.Subject, "\x00\r\n") {
		return ErrInvalidMessage
	}
	return nil
}
