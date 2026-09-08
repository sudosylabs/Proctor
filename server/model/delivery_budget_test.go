// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestDeliveryMetadataReservations(t *testing.T) {
	used := int64(0)
	owners := 0
	for {
		next, err := ReserveDeliveryMetadata(used, NativeOwnerReservationBytes)
		if err != nil {
			var capacity *DeliveryMetadataCapacity
			if !errors.As(err, &capacity) || next != used || capacity.RemainingReservableBytes != DeliveryMetadataLimitBytes-used {
				t.Fatalf("invalid refusal: %d %v", next, err)
			}
			raw, err := json.Marshal(capacity)
			if err != nil || len(raw) > 1024 {
				t.Fatal("capacity response exceeds wire bound")
			}
			break
		}
		owners++
		used = next
	}
	if owners < 1 || owners > 8 {
		t.Fatalf("unbounded or unusable owner reservation: %d", owners)
	}
	if next, err := ReserveDeliveryMetadata(DeliveryMetadataLimitBytes-1, 1); err != nil || next != DeliveryMetadataLimitBytes {
		t.Fatalf("last byte: %d %v", next, err)
	}
	for _, values := range [][2]int64{{-1, 1}, {0, 0}, {DeliveryMetadataLimitBytes + 1, 1}, {0, DeliveryMetadataLimitBytes + 1}} {
		if _, err := ReserveDeliveryMetadata(values[0], values[1]); err == nil {
			t.Fatalf("invalid reservation accepted: %v", values)
		}
	}
}
func TestDeliveryQuotaBoundariesAndStickyStop(t *testing.T) {
	for _, native := range []bool{false, true} {
		for _, attempt := range []bool{false, true} {
			initial := NewDeliveryQuotaUsage(native, attempt)
			full, err := initial.Charge(initial.RecordLimit, initial.ByteLimit, initial.PositionLimit)
			if err != nil || full.SummaryOnly || full.Validate() != nil {
				t.Fatalf("exact quota: %#v %v", full, err)
			}
			for _, extra := range [][3]int64{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}} {
				exhausted, err := full.Charge(extra[0], extra[1], extra[2])
				if err != nil || !exhausted.SummaryOnly || exhausted.RetainedRecords != full.RetainedRecords || exhausted.RetainedBytes != full.RetainedBytes || exhausted.AllocatedPositions != full.AllocatedPositions {
					t.Fatalf("overflow changed accepted boundary: %#v", exhausted)
				}
				repeated, err := exhausted.Charge(0, 0, 0)
				if err != nil || repeated.StopReason == nil || *repeated.StopReason != *exhausted.StopReason || !repeated.SummaryOnly {
					t.Fatal("summary-only was cleared")
				}
			}
		}
	}
	invalid := NewDeliveryQuotaUsage(true, false)
	invalid.ByteLimit = 9007199254740992
	if invalid.Validate() == nil {
		t.Fatal("unsafe limit accepted")
	}
}
func TestDeliveryRepairHeadroom(t *testing.T) {
	ordinary := DeliveryPendingByteLimit - DeliveryRepairReservationBytes
	if !CanRetainPendingDelivery(ordinary-1, 0, DeliveryAttemptByteLimit, 1, false, false) || CanRetainPendingDelivery(ordinary, 0, DeliveryAttemptByteLimit, 1, false, false) {
		t.Fatal("ordinary pending reservation bypassed")
	}
	if !CanRetainPendingDelivery(ordinary, 0, DeliveryAttemptByteLimit, DeliveryRepairReservationBytes, true, true) {
		t.Fatal("reserved repair space unavailable")
	}
	retained := DeliveryAttemptByteLimit - DeliveryRepairReservationBytes
	if CanRetainPendingDelivery(0, retained, DeliveryAttemptByteLimit, 1, false, true) || !CanRetainPendingDelivery(0, retained, DeliveryAttemptByteLimit, 1, true, true) {
		t.Fatal("retained repair reservation bypassed")
	}
	if CanRetainPendingDelivery(0, 0, 9007199254740992, 1, true, true) {
		t.Fatal("unsafe quota accepted")
	}
}
