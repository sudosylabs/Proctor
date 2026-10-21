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
	"fmt"
)

const SittingMailExpansionPageSize = 200

func SittingMailExpansionDedupeKey(occurrenceID MailOccurrenceID) (string, error) {
	if !occurrenceID.IsValid() {
		return "", invalidModelError("SittingMailExpansionDedupeKey", "sitting_mail_job", "", "invalid Sitting mail occurrence", "")
	}
	return fmt.Sprintf("sitting-mail:%s", occurrenceID), nil
}

type SittingMailExpansionCommandV1 struct {
	OccurrenceID MailOccurrenceID `json:"occurrence_id"`
}

type SittingMailExpansionCheckpointV1 struct {
	AfterUserID UserID `json:"after_user_id,omitempty"`
	Expanded    int64  `json:"expanded"`
	Suppressed  int64  `json:"suppressed"`
	// Undeliverable counts recipients whose own account cannot be addressed;
	// they are skipped so later recipients still receive mail.
	Undeliverable int64 `json:"undeliverable"`
}

type SittingMailExpansionResultV1 struct {
	Expanded      int64 `json:"expanded"`
	Suppressed    int64 `json:"suppressed"`
	Undeliverable int64 `json:"undeliverable"`
}

func EncodeSittingMailExpansionCommand(value SittingMailExpansionCommandV1) (json.RawMessage, error) {
	if !value.OccurrenceID.IsValid() {
		return nil, invalidModelError("EncodeSittingMailExpansionCommand", "sitting_mail_job", "", "invalid Sitting mail expansion command", "")
	}
	return json.Marshal(value)
}

func DecodeSittingMailExpansionCommand(version int, raw json.RawMessage) (SittingMailExpansionCommandV1, error) {
	var value SittingMailExpansionCommandV1
	if version != 1 || decodeStrictSittingMail(raw, &value) != nil || !value.OccurrenceID.IsValid() {
		return SittingMailExpansionCommandV1{}, invalidModelError("DecodeSittingMailExpansionCommand", "sitting_mail_job", "", "invalid Sitting mail expansion command", "")
	}
	return value, nil
}

func EncodeSittingMailExpansionCheckpoint(value SittingMailExpansionCheckpointV1) (json.RawMessage, error) {
	if (!value.AfterUserID.IsZero() && !value.AfterUserID.IsValid()) || value.Expanded < 0 || value.Suppressed < 0 || value.Undeliverable < 0 {
		return nil, invalidModelError("EncodeSittingMailExpansionCheckpoint", "sitting_mail_job", "", "invalid Sitting mail expansion checkpoint", "")
	}
	return json.Marshal(value)
}

func DecodeSittingMailExpansionCheckpoint(version int, raw json.RawMessage) (SittingMailExpansionCheckpointV1, error) {
	var value SittingMailExpansionCheckpointV1
	if version != 1 || decodeStrictSittingMail(raw, &value) != nil ||
		(!value.AfterUserID.IsZero() && !value.AfterUserID.IsValid()) || value.Expanded < 0 || value.Suppressed < 0 || value.Undeliverable < 0 {
		return SittingMailExpansionCheckpointV1{}, invalidModelError("DecodeSittingMailExpansionCheckpoint", "sitting_mail_job", "", "invalid Sitting mail expansion checkpoint", "")
	}
	return value, nil
}

func EncodeSittingMailExpansionResult(value SittingMailExpansionResultV1) (json.RawMessage, error) {
	if value.Expanded < 0 || value.Suppressed < 0 || value.Undeliverable < 0 {
		return nil, invalidModelError("EncodeSittingMailExpansionResult", "sitting_mail_job", "", "invalid Sitting mail expansion result", "")
	}
	return json.Marshal(value)
}

func decodeStrictSittingMail(raw json.RawMessage, target any) error {
	if len(raw) == 0 {
		return invalidModelError("decodeStrictSittingMail", "sitting_mail_job", "", "empty document", "")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) == nil {
		return invalidModelError("decodeStrictSittingMail", "sitting_mail_job", "", "trailing document", "")
	}
	return nil
}
