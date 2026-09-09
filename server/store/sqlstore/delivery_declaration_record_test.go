// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestRetainedDeliveryDeclarationValidation(t *testing.T) {
	declaration := model.DeclareDeliveryGaps{DeclarationID: model.NewId(), AllocatedThroughSequence: 4, Ranges: []model.SequenceRange{{First: 1, Last: 2}}, Reason: "spool_lost"}
	raw, err := declaration.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	receipt := model.DeliveryGapReceipt{DeclarationID: declaration.DeclarationID, RequestDigest: model.SHA256Fingerprint(raw), DeclarationRevision: 1, SettledThroughSequence: 2}
	valid := deliveryDeclarationRecord{Kind: "gaps", RequestDigest: receipt.RequestDigest, Gaps: &declaration, Receipt: receipt}
	for _, tc := range []struct {
		name   string
		change func(*deliveryDeclarationRecord)
	}{
		{"negative settlement", func(r *deliveryDeclarationRecord) { r.Receipt.SettledThroughSequence = -1 }},
		{"wrong receipt identity", func(r *deliveryDeclarationRecord) { r.Receipt.DeclarationID = model.NewId() }},
		{"wrong request digest", func(r *deliveryDeclarationRecord) { r.RequestDigest = model.SHA256Fingerprint([]byte("other")) }},
		{"wrong kind", func(r *deliveryDeclarationRecord) { r.Kind = "unknown" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := valid
			tc.change(&value)
			raw, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := decodeDeliveryDeclarationRecord(raw); !errors.Is(err, store.ErrInvalidState) {
				t.Fatalf("corruption not internal: %v", err)
			}
		})
	}
	raw, err = json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeDeliveryDeclarationRecord(raw); err != nil {
		t.Fatal(err)
	}
	_, capacityErr := model.ReserveDeliveryMetadata(model.DeliveryMetadataLimitBytes, 1)
	var capacity *model.DeliveryMetadataCapacity
	if !errors.As(capacityErr, &capacity) {
		t.Fatal(capacityErr)
	}
	for _, tc := range []struct {
		name, refusal string
		receipt       model.DeliveryGapReceipt
		capacity      *model.DeliveryMetadataCapacity
		valid         bool
	}{
		{name: "receipt", receipt: receipt, valid: true},
		{name: "refusal", refusal: "sequence_limit", valid: true},
		{name: "capacity", capacity: capacity, valid: true},
		{name: "unknown refusal", refusal: "unknown"},
		{name: "missing receipt"},
		{name: "invalid capacity", capacity: &model.DeliveryMetadataCapacity{}},
		{name: "mixed refusal", refusal: "sequence_limit", receipt: receipt},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateDeliveryDeclarationOutcome(tc.refusal, tc.receipt, tc.capacity)
			if tc.valid && err != nil || !tc.valid && !errors.Is(err, store.ErrInvalidState) {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
}

func TestRetainedNativeResetBinding(t *testing.T) {
	part := model.NewAttemptParticipationID()
	reset := model.NativeSourceReset{Kind: "source_reset", ResetID: model.NewId(), SourceID: model.NativeSourceCapture, PreviousSourceInstanceID: model.NewId(), NewSourceInstanceID: model.NewId(), Reason: "restart", OccurredAt: time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)}
	canonical, err := reset.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	valid := nativeResetRecord{ParticipationID: part, Reset: reset, Digest: model.SHA256Fingerprint(canonical)}
	for _, tc := range []struct {
		name   string
		change func(*nativeResetRecord)
	}{
		{"digest", func(r *nativeResetRecord) { r.Digest = model.SHA256Fingerprint([]byte("other")) }},
		{"participation", func(r *nativeResetRecord) { r.ParticipationID = model.NewAttemptParticipationID() }},
		{"reset identity", func(r *nativeResetRecord) {
			r.Reset.ResetID = model.NewId()
			raw, _ := r.Reset.Canonical()
			r.Digest = model.SHA256Fingerprint(raw)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := valid
			tc.change(&value)
			raw, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := decodeNativeResetRecord(raw, part, reset.ResetID); !errors.Is(err, store.ErrInvalidState) {
				t.Fatalf("corrupt reset: %v", err)
			}
		})
	}
	raw, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeNativeResetRecord(raw, part, reset.ResetID); err != nil {
		t.Fatal(err)
	}
}

func TestRetainedSourceRefusalValidation(t *testing.T) {
	valid := browserSourceRefusalRecord{SourceSessionID: "00000000-0000-4000-8000-000000000001", RequestDigest: model.SHA256Fingerprint([]byte("source")), Code: "exam.browser.source_budget_exhausted"}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*browserSourceRefusalRecord)
	}{
		{"code", func(r *browserSourceRefusalRecord) { r.Code = "unknown" }},
		{"digest", func(r *browserSourceRefusalRecord) { r.RequestDigest = "invalid" }},
		{"missing capacity", func(r *browserSourceRefusalRecord) { r.Code = "exam.delivery.metadata_capacity" }},
		{"invalid capacity", func(r *browserSourceRefusalRecord) {
			r.Code = "exam.delivery.metadata_capacity"
			r.Capacity = &model.DeliveryMetadataCapacity{}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := valid
			tc.change(&value)
			if value.Validate() == nil {
				t.Fatal("invalid refusal accepted")
			}
		})
	}
}
