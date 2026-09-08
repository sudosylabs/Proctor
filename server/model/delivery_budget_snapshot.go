// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
package model

import "time"

type DeliveryFamilyBudget struct {
	Participation    DeliveryQuotaUsage `json:"participation"`
	Attempt          DeliveryQuotaUsage `json:"attempt"`
	PendingBytes     int64              `json:"pending_bytes"`
	PendingByteLimit int64              `json:"pending_byte_limit"`
}
type DeliveryBudgetSnapshot struct {
	ParticipationID          AttemptParticipationID `json:"participation_id"`
	Generation               int64                  `json:"generation"`
	Native                   DeliveryFamilyBudget   `json:"native"`
	Browser                  DeliveryFamilyBudget   `json:"browser"`
	BrowserFlagGroups        int64                  `json:"browser_flag_groups"`
	BrowserEvidenceRecords   int64                  `json:"browser_evidence_records"`
	BrowserEvidenceBytes     int64                  `json:"browser_evidence_bytes"`
	ExplicitMissingIntervals int64                  `json:"explicit_missing_intervals"`
	ControlMetadataBytes     int64                  `json:"control_metadata_bytes"`
	ServerTime               time.Time              `json:"server_time"`
}

func (s DeliveryBudgetSnapshot) Validate() error {
	if !s.ParticipationID.IsValid() || s.Generation < 1 || !securitySafeInt(s.Generation) || !securityInstant(s.ServerTime) {
		return ErrDeliveryInvalid
	}
	for _, f := range []struct {
		value  DeliveryFamilyBudget
		native bool
	}{{s.Native, true}, {s.Browser, false}} {
		if f.value.PendingByteLimit != DeliveryPendingByteLimit || f.value.PendingBytes < 0 || f.value.PendingBytes > f.value.PendingByteLimit || f.value.PendingBytes > f.value.Attempt.RetainedBytes {
			return ErrDeliveryInvalid
		}
		for _, q := range []struct {
			value   DeliveryQuotaUsage
			attempt bool
		}{{f.value.Participation, false}, {f.value.Attempt, true}} {
			expected := NewDeliveryQuotaUsage(f.native, q.attempt)
			if q.value.Validate() != nil || q.value.RecordLimit != expected.RecordLimit || q.value.ByteLimit != expected.ByteLimit || q.value.PositionLimit != expected.PositionLimit {
				return ErrDeliveryInvalid
			}
		}
		if f.value.Participation.RetainedRecords > f.value.Attempt.RetainedRecords || f.value.Participation.RetainedBytes > f.value.Attempt.RetainedBytes || f.value.Participation.AllocatedPositions > f.value.Attempt.AllocatedPositions {
			return ErrDeliveryInvalid
		}
	}
	for _, pair := range [][2]int64{{s.BrowserFlagGroups, BrowserFlagGroupLimit}, {s.BrowserEvidenceRecords, BrowserEvidenceRecordLimit}, {s.BrowserEvidenceBytes, BrowserEvidenceByteLimit}, {s.ExplicitMissingIntervals, DeliveryMissingIntervalLimit}, {s.ControlMetadataBytes, DeliveryMetadataLimitBytes}} {
		if pair[0] < 0 || pair[0] > pair[1] {
			return ErrDeliveryInvalid
		}
	}
	return nil
}

type StopDeliveryDetails struct {
	Family string             `json:"family"`
	Reason DeliveryStopReason `json:"reason"`
}

func (s StopDeliveryDetails) Validate() error {
	if (s.Family != "native" && s.Family != "browser") || s.Reason != DeliveryStopLocalLossInventory {
		return ErrDeliveryInvalid
	}
	return nil
}

// Exactly one family's affected status collection is populated. Both keys are
// present so the response remains closed and its ownership can be validated.
type StopDeliveryDetailsResult struct {
	Budget  DeliveryBudgetSnapshot      `json:"budget"`
	Native  *NativeSecurityStreamStatus `json:"native"`
	Browser []BrowserSourceStatus       `json:"browser"`
}

func (s StopDeliveryDetailsResult) Validate(family string) error {
	if s.Budget.Validate() != nil || s.Browser == nil || len(s.Browser) > BrowserSourceMaximumPerParticipation {
		return ErrDeliveryInvalid
	}
	switch family {
	case "native":
		if s.Native == nil || s.Native.Validate() != nil || s.Native.DetailMode != "summary_only" || len(s.Browser) != 0 || !s.Budget.Native.Participation.SummaryOnly {
			return ErrDeliveryInvalid
		}
	case "browser":
		if s.Native != nil || !s.Budget.Browser.Participation.SummaryOnly {
			return ErrDeliveryInvalid
		}
		for _, v := range s.Browser {
			if v.Validate() != nil || v.ParticipationID != s.Budget.ParticipationID || v.Generation != s.Budget.Generation || v.DetailMode != "summary_only" {
				return ErrDeliveryInvalid
			}
		}
	default:
		return ErrDeliveryInvalid
	}
	return nil
}
