// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeliveryMetadataReservations(t *testing.T) {
	used := int64(0)
	owners := 0
	for {
		next, err := ReserveDeliveryMetadata(used, NativeOwnerReservationBytes)
		if err != nil {
			var capacity *DeliveryMetadataCapacity
			require.ErrorAs(t, err, &capacity, "invalid refusal")
			require.Equal(t, used, next, "invalid refusal")
			require.Equal(t, DeliveryMetadataLimitBytes-used, capacity.RemainingReservableBytes, "invalid refusal")
			raw, err := json.Marshal(capacity)
			require.NoError(t, err, "capacity response exceeds wire bound")
			require.LessOrEqual(t, len(raw), 1024, "capacity response exceeds wire bound")
			break
		}
		owners++
		used = next
	}
	require.GreaterOrEqual(t, owners, 1, "unbounded or unusable owner reservation")
	require.LessOrEqual(t, owners, 8, "unbounded or unusable owner reservation")
	next, err := ReserveDeliveryMetadata(DeliveryMetadataLimitBytes-1, 1)
	require.NoError(t, err, "last byte")
	require.Equal(t, DeliveryMetadataLimitBytes, next, "last byte")
	for _, values := range [][2]int64{{-1, 1}, {0, 0}, {DeliveryMetadataLimitBytes + 1, 1}, {0, DeliveryMetadataLimitBytes + 1}} {
		_, err := ReserveDeliveryMetadata(values[0], values[1])
		require.Error(t, err, "invalid reservation accepted: %v", values)
	}
}
func TestDeliveryQuotaBoundariesAndStickyStop(t *testing.T) {
	for _, native := range []bool{false, true} {
		for _, attempt := range []bool{false, true} {
			initial := NewDeliveryQuotaUsage(native, attempt)
			full, err := initial.Charge(initial.RecordLimit, initial.ByteLimit, initial.PositionLimit)
			require.NoError(t, err, "exact quota: %#v", full)
			require.False(t, full.SummaryOnly, "exact quota: %#v", full)
			require.NoError(t, full.Validate(), "exact quota: %#v", full)
			for _, extra := range [][3]int64{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}} {
				exhausted, err := full.Charge(extra[0], extra[1], extra[2])
				require.NoError(t, err)
				require.True(t, exhausted.SummaryOnly)
				require.Equal(t, full.RetainedRecords, exhausted.RetainedRecords)
				require.Equal(t, full.RetainedBytes, exhausted.RetainedBytes)
				require.Equal(t, full.AllocatedPositions, exhausted.AllocatedPositions)
				repeated, err := exhausted.Charge(0, 0, 0)
				require.NoError(t, err, "summary-only was cleared")
				require.NotNil(t, repeated.StopReason, "summary-only was cleared")
				require.Equal(t, *exhausted.StopReason, *repeated.StopReason, "summary-only was cleared")
				require.True(t, repeated.SummaryOnly, "summary-only was cleared")
			}
		}
	}
	invalid := NewDeliveryQuotaUsage(true, false)
	invalid.ByteLimit = 9007199254740992
	require.Error(t, invalid.Validate(), "unsafe limit accepted")
}
func TestDeliveryRepairHeadroom(t *testing.T) {
	ordinary := DeliveryPendingByteLimit - DeliveryRepairReservationBytes
	require.True(t, CanRetainPendingDelivery(ordinary-1, 0, DeliveryAttemptByteLimit, 1, false, false), "ordinary pending reservation bypassed")
	require.False(t, CanRetainPendingDelivery(ordinary, 0, DeliveryAttemptByteLimit, 1, false, false), "ordinary pending reservation bypassed")
	require.True(t, CanRetainPendingDelivery(ordinary, 0, DeliveryAttemptByteLimit, DeliveryRepairReservationBytes, true, true), "reserved repair space unavailable")
	retained := DeliveryAttemptByteLimit - DeliveryRepairReservationBytes
	require.False(t, CanRetainPendingDelivery(0, retained, DeliveryAttemptByteLimit, 1, false, true), "retained repair reservation bypassed")
	require.True(t, CanRetainPendingDelivery(0, retained, DeliveryAttemptByteLimit, 1, true, true), "retained repair reservation bypassed")
	require.False(t, CanRetainPendingDelivery(0, 0, 9007199254740992, 1, true, true), "unsafe quota accepted")
}
