// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package mail

import (
	"encoding/json"
	"errors"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/secretseal"
)

type frozenDeliveryPayload struct {
	encrypted      json.RawMessage
	templateDigest string
	messageID      string
}

func freezeDeliveryPayload(sealer *secretseal.Sealer, deliveryID model.MailDeliveryID, from, recipient Address,
	content FrozenContent,
) (frozenDeliveryPayload, error) {
	if sealer == nil || !deliveryID.IsValid() || ValidateAddress(from) != nil || ValidateAddress(recipient) != nil ||
		content.Subject == "" || content.Text == "" && content.HTML == "" {
		return frozenDeliveryPayload{}, errors.New("mail delivery payload input is invalid")
	}
	payload := FrozenPayloadV1{Version: 1, RecipientName: recipient.Name, RecipientAddress: recipient.Address,
		FromName: from.Name, FromAddress: from.Address, Subject: content.Subject, Text: content.Text, HTML: content.HTML,
		AutoSubmitted: "auto-generated", AutoResponseSuppress: "All"}
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
