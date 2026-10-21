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

type MailDeliveryCommandV1 struct {
	DeliveryID MailDeliveryID `json:"delivery_id"`
}

func EncodeMailDeliveryCommand(command MailDeliveryCommandV1) (json.RawMessage, error) {
	if !command.DeliveryID.IsValid() {
		return nil, invalidModelError("EncodeMailDeliveryCommand", "mail_job", "", "invalid mail delivery command", "")
	}
	return json.Marshal(command)
}

func DecodeMailDeliveryCommand(version int, raw json.RawMessage) (MailDeliveryCommandV1, error) {
	var command MailDeliveryCommandV1
	if version != 1 || len(raw) == 0 {
		return command, invalidModelError("DecodeMailDeliveryCommand", "mail_job", "", "unsupported mail delivery command", "")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&command); err != nil || decoder.Decode(&struct{}{}) == nil || !command.DeliveryID.IsValid() {
		return MailDeliveryCommandV1{}, invalidModelError("DecodeMailDeliveryCommand", "mail_job", "", "invalid mail delivery command", "")
	}
	return command, nil
}
