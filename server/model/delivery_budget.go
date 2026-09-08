// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import "errors"

const (
	DeliveryParticipationRecordLimit  int64 = 50_000
	DeliveryAttemptRecordLimit        int64 = 200_000
	DeliveryParticipationByteLimit    int64 = 32 * 1024 * 1024
	DeliveryAttemptByteLimit          int64 = 128 * 1024 * 1024
	NativeParticipationPositionLimit  int64 = 20_000
	NativeAttemptPositionLimit        int64 = 80_000
	BrowserParticipationPositionLimit int64 = 50_000
	BrowserAttemptPositionLimit       int64 = 200_000
	DeliveryPendingByteLimit          int64 = 2 * 1024 * 1024
	DeliveryRepairReservationBytes    int64 = 320 * 1024
	DeliveryMetadataLimitBytes        int64 = 2 * 1024 * 1024
	DeliveryMissingIntervalLimit      int64 = 4096
	BrowserFlagGroupLimit             int64 = 256
	BrowserEvidenceRecordLimit        int64 = 10_000
	BrowserEvidenceByteLimit          int64 = 8 * 1024 * 1024
	SecurityControlReceiptLimit             = 128
	SecurityControlReceiptMaxBytes          = 2048
	// Compact control receipts hold bounded outcomes and references to separately
	// charged immutable reset facts. They never copy a full coverage snapshot.
	SecurityControlCacheMaxBytes = SecurityControlReceiptLimit*SecurityControlReceiptMaxBytes + SecurityControlReceiptLimit + 1
	// Reserve each closed stored slot at its byte ceiling, including an envelope
	// allowance. Canonical data, not SQL/index overhead, is the charged quantity.
	NativeOwnerReservationBytes  int64 = 128*1024 + 2*64*1024 + SecurityControlCacheMaxBytes + 16*1024 + 3*2048 + 4096 + 512
	BrowserOwnerReservationBytes int64 = 16*1024 + 4*2048 + 4096 + 512
)

type DeliveryMetadataCapacity struct {
	ControlMetadataBytes      int64 `json:"control_metadata_bytes"`
	ControlMetadataLimitBytes int64 `json:"control_metadata_limit_bytes"`
	RequiredReservationBytes  int64 `json:"required_reservation_bytes"`
	RemainingReservableBytes  int64 `json:"remaining_reservable_bytes"`
}

func (e *DeliveryMetadataCapacity) Error() string { return "delivery metadata capacity is exhausted" }
func ReserveDeliveryMetadata(used, required int64) (int64, error) {
	if used < 0 || used > DeliveryMetadataLimitBytes || required <= 0 || required > DeliveryMetadataLimitBytes {
		return used, errors.New("delivery metadata reservation is invalid")
	}
	remaining := DeliveryMetadataLimitBytes - used
	if required > remaining {
		return used, &DeliveryMetadataCapacity{ControlMetadataBytes: used, ControlMetadataLimitBytes: DeliveryMetadataLimitBytes, RequiredReservationBytes: required, RemainingReservableBytes: remaining}
	}
	return used + required, nil
}

type DeliveryStopReason string

const (
	DeliveryStopRecords            DeliveryStopReason = "records"
	DeliveryStopBytes              DeliveryStopReason = "bytes"
	DeliveryStopPositions          DeliveryStopReason = "positions"
	DeliveryStopMetadata           DeliveryStopReason = "metadata"
	DeliveryStopLocalLossInventory DeliveryStopReason = "local_loss_inventory_exhausted"
)

type DeliveryQuotaUsage struct {
	RetainedRecords    int64               `json:"retained_records"`
	RetainedBytes      int64               `json:"retained_bytes"`
	AllocatedPositions int64               `json:"allocated_positions"`
	RecordLimit        int64               `json:"record_limit"`
	ByteLimit          int64               `json:"byte_limit"`
	PositionLimit      int64               `json:"position_limit"`
	SummaryOnly        bool                `json:"summary_only"`
	StopReason         *DeliveryStopReason `json:"stop_reason"`
}

func NewDeliveryQuotaUsage(native, attempt bool) DeliveryQuotaUsage {
	usage := DeliveryQuotaUsage{RecordLimit: DeliveryParticipationRecordLimit, ByteLimit: DeliveryParticipationByteLimit, PositionLimit: BrowserParticipationPositionLimit}
	if native {
		usage.PositionLimit = NativeParticipationPositionLimit
	}
	if attempt {
		usage.RecordLimit = DeliveryAttemptRecordLimit
		usage.ByteLimit = DeliveryAttemptByteLimit
		usage.PositionLimit = BrowserAttemptPositionLimit
		if native {
			usage.PositionLimit = NativeAttemptPositionLimit
		}
	}
	return usage
}
func (q DeliveryQuotaUsage) Validate() error {
	if !securitySafeInt(q.RetainedRecords) || !securitySafeInt(q.RetainedBytes) || !securitySafeInt(q.AllocatedPositions) || !securitySafeInt(q.RecordLimit) || !securitySafeInt(q.ByteLimit) || !securitySafeInt(q.PositionLimit) || q.RecordLimit < 1 || q.ByteLimit < 1 || q.PositionLimit < 1 || q.RetainedRecords > q.RecordLimit || q.RetainedBytes > q.ByteLimit || q.AllocatedPositions > q.PositionLimit || q.SummaryOnly != (q.StopReason != nil) {
		return errors.New("delivery quota is invalid")
	}
	if q.StopReason != nil {
		switch *q.StopReason {
		case DeliveryStopRecords, DeliveryStopBytes, DeliveryStopPositions, DeliveryStopMetadata, DeliveryStopLocalLossInventory:
		default:
			return errors.New("delivery stop reason is invalid")
		}
	}
	return nil
}

// Charge atomically computes a prospective charge; callers persist it only
// with the corresponding records/receipt. Exhaustion freezes all boundaries.
func (q DeliveryQuotaUsage) Charge(records, bytes, positions int64) (DeliveryQuotaUsage, error) {
	if q.Validate() != nil || !securitySafeInt(records) || !securitySafeInt(bytes) || !securitySafeInt(positions) {
		return q, errors.New("delivery charge is invalid")
	}
	if q.SummaryOnly {
		return q, nil
	}
	var reason DeliveryStopReason
	switch {
	case records > q.RecordLimit-q.RetainedRecords:
		reason = DeliveryStopRecords
	case bytes > q.ByteLimit-q.RetainedBytes:
		reason = DeliveryStopBytes
	case positions > q.PositionLimit-q.AllocatedPositions:
		reason = DeliveryStopPositions
	}
	if reason != "" {
		q.SummaryOnly = true
		q.StopReason = &reason
		return q, nil
	}
	q.RetainedRecords += records
	q.RetainedBytes += bytes
	q.AllocatedPositions += positions
	return q, nil
}

// Pending admission reserves repair headroom in both pending and retained pools.
func CanRetainPendingDelivery(pending, retained, retainedLimit, bytes int64, repair, recoverableGap bool) bool {
	if !securitySafeInt(pending) || !securitySafeInt(retained) || !securitySafeInt(retainedLimit) || !securitySafeInt(bytes) || pending > DeliveryPendingByteLimit || retained > retainedLimit {
		return false
	}
	pendingLimit := DeliveryPendingByteLimit
	if !repair {
		pendingLimit -= DeliveryRepairReservationBytes
		if recoverableGap {
			retainedLimit -= DeliveryRepairReservationBytes
		}
	}
	return bytes <= pendingLimit-pending && bytes <= retainedLimit-retained
}

func (e DeliveryMetadataCapacity) Validate() error {
	if e.ControlMetadataLimitBytes != DeliveryMetadataLimitBytes || e.ControlMetadataBytes < 0 || e.ControlMetadataBytes > DeliveryMetadataLimitBytes || e.RequiredReservationBytes <= 0 || e.RequiredReservationBytes > DeliveryMetadataLimitBytes || e.RemainingReservableBytes != DeliveryMetadataLimitBytes-e.ControlMetadataBytes || e.RequiredReservationBytes <= e.RemainingReservableBytes {
		return errors.New("invalid metadata capacity refusal")
	}
	return nil
}
