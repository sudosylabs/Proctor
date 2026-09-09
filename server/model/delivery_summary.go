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
	"time"
)

type UnretainedDeliverySummary struct {
	firstUnretainedAtSpelling string
	lastUnretainedAtSpelling  string

	SummarySequence       int64      `json:"summary_sequence"`
	UnretainedRecordCount int64      `json:"unretained_record_count"`
	CountComplete         bool       `json:"count_complete"`
	FirstUnretainedAt     *time.Time `json:"first_unretained_at"`
	LastUnretainedAt      *time.Time `json:"last_unretained_at"`
}

func (s UnretainedDeliverySummary) Validate() error {
	if s.SummarySequence <= 0 || !securitySafeInt(s.SummarySequence) || !securitySafeInt(s.UnretainedRecordCount) || s.FirstUnretainedAt != nil && !securityInstant(*s.FirstUnretainedAt) || s.LastUnretainedAt != nil && !securityInstant(*s.LastUnretainedAt) {
		return ErrDeliveryInvalid
	}
	return nil
}
func (s UnretainedDeliverySummary) Canonical() ([]byte, error) {
	if s.Validate() != nil {
		return nil, ErrDeliveryInvalid
	}
	return encodeCanonicalExamDocument(s)
}

// Compare returns true only for exact current replay. The Store separately
// fences ownership, expiry and changed-summary rate before replacing its slot.
func (s UnretainedDeliverySummary) Compare(next UnretainedDeliverySummary) (bool, error) {
	priorRaw, err := s.Canonical()
	if err != nil {
		return false, err
	}
	nextRaw, err := next.Canonical()
	if err != nil {
		return false, err
	}
	if s.SummarySequence == next.SummarySequence {
		if bytes.Equal(priorRaw, nextRaw) {
			return true, nil
		}
		return false, ErrDeliveryConflict
	}
	if next.SummarySequence < s.SummarySequence || next.UnretainedRecordCount < s.UnretainedRecordCount || !s.CountComplete && next.CountComplete {
		return false, ErrDeliveryConflict
	}
	return false, nil
}

// SaturatingDeliveryCount preserves uncertainty after losing exact precision.
func SaturatingDeliveryCount(count, additional int64, complete bool) (int64, bool, error) {
	if !securitySafeInt(count) || !securitySafeInt(additional) {
		return 0, false, ErrDeliveryInvalid
	}
	const maximum int64 = 9007199254740991
	if additional > maximum-count {
		return maximum, false, nil
	}
	return count + additional, complete, nil
}

func (v UnretainedDeliverySummary) MarshalJSON() ([]byte, error) {
	type wire UnretainedDeliverySummary
	return json.Marshal(struct {
		*wire
		FirstUnretainedAt *securityJSONInstant `json:"first_unretained_at"`
		LastUnretainedAt  *securityJSONInstant `json:"last_unretained_at"`
	}{wire: (*wire)(&v), FirstUnretainedAt: optionalSecurityJSONInstant(v.FirstUnretainedAt, v.firstUnretainedAtSpelling), LastUnretainedAt: optionalSecurityJSONInstant(v.LastUnretainedAt, v.lastUnretainedAtSpelling)})
}
func (v *UnretainedDeliverySummary) UnmarshalJSON(raw []byte) error {
	if v == nil {
		return ErrDeliveryInvalid
	}
	type wire UnretainedDeliverySummary
	var decoded wire
	value := struct {
		*wire
		FirstUnretainedAt *securityJSONInstant `json:"first_unretained_at"`
		LastUnretainedAt  *securityJSONInstant `json:"last_unretained_at"`
	}{wire: &decoded}
	if decodeClosedDeliveryDeclaration(raw, &value, 2048) != nil {
		return ErrDeliveryInvalid
	}
	candidate := UnretainedDeliverySummary(decoded)
	if value.FirstUnretainedAt != nil {
		candidate.FirstUnretainedAt = &value.FirstUnretainedAt.Time
		candidate.firstUnretainedAtSpelling = value.FirstUnretainedAt.spelling
	}
	if value.LastUnretainedAt != nil {
		candidate.LastUnretainedAt = &value.LastUnretainedAt.Time
		candidate.lastUnretainedAtSpelling = value.LastUnretainedAt.spelling
	}
	if candidate.Validate() != nil {
		return ErrDeliveryInvalid
	}
	*v = candidate
	return nil
}
