// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"encoding/json"

	"github.com/sudosylabs/proctor/server/model"
)

// Delivery declarations share a durable receipt shape across both families.
// Validation here applies to retained records, after a receipt has been issued.
type deliveryDeclarationRecord struct {
	Kind          string                          `json:"kind"`
	RequestDigest string                          `json:"request_digest"`
	Gaps          *model.DeclareDeliveryGaps      `json:"gaps"`
	Final         *model.FinalDeliveryDeclaration `json:"final"`
	Receipt       model.DeliveryGapReceipt        `json:"receipt"`
}

func decodeDeliveryDeclarationRecord(raw []byte) (deliveryDeclarationRecord, error) {
	var record deliveryDeclarationRecord
	invalid := func() (deliveryDeclarationRecord, error) {
		return record, invalidPersistedState("delivery_declaration", "record", model.ErrDeliveryInvalid)
	}
	if json.Unmarshal(raw, &record) != nil || record.Receipt.Validate() != nil {
		return invalid()
	}
	var canonical []byte
	var err error
	var id string
	switch {
	case record.Kind == "gaps" && record.Gaps != nil && record.Final == nil:
		canonical, err = record.Gaps.Canonical()
		id = record.Gaps.DeclarationID
	case record.Kind == "final" && record.Final != nil && record.Gaps == nil:
		canonical, err = record.Final.Canonical()
		id = record.Final.DeclarationID
	default:
		return invalid()
	}
	if err != nil || record.RequestDigest != model.SHA256Fingerprint(canonical) || record.Receipt.RequestDigest != record.RequestDigest || record.Receipt.DeclarationID != id {
		return invalid()
	}
	return record, nil
}

func validateDeliveryDeclarationOutcome(refusal string, receipt model.DeliveryGapReceipt, capacity *model.DeliveryMetadataCapacity) error {
	invalid := func() error {
		return invalidPersistedState("delivery_declaration", "outcome", model.ErrDeliveryInvalid)
	}
	if capacity != nil {
		if refusal != "" || receipt != (model.DeliveryGapReceipt{}) || capacity.Validate() != nil {
			return invalid()
		}
	} else if refusal != "" {
		if (refusal != "sequence_limit" && refusal != "detail_budget_exhausted") || receipt != (model.DeliveryGapReceipt{}) {
			return invalid()
		}
	} else if receipt.Validate() != nil {
		return invalid()
	}
	return nil
}
