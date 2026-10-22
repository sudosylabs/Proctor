// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"bytes"
	"encoding/json"
)

type MailDeliveryCommand struct {
	DeliveryID MailDeliveryID `json:"delivery_id"`
}

func EncodeMailDeliveryCommand(command MailDeliveryCommand) (json.RawMessage, error) {
	if !command.DeliveryID.IsValid() {
		return nil, invalidModelError("EncodeMailDeliveryCommand", "mail_job", "", "invalid mail delivery command", "")
	}
	return json.Marshal(command)
}

func DecodeMailDeliveryCommand(raw json.RawMessage) (MailDeliveryCommand, error) {
	var command MailDeliveryCommand
	if len(raw) == 0 {
		return command, invalidModelError("DecodeMailDeliveryCommand", "mail_job", "", "unsupported mail delivery command", "")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&command); err != nil || decoder.Decode(&struct{}{}) == nil || !command.DeliveryID.IsValid() {
		return MailDeliveryCommand{}, invalidModelError("DecodeMailDeliveryCommand", "mail_job", "", "invalid mail delivery command", "")
	}
	return command, nil
}
