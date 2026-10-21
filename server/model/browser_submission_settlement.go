// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

// BrowserSubmissionSettlement covers every source ever created for an Attempt.
// A pending source may also have terminal loss; counts deliberately describe
// independent facts, while pending takes precedence in the aggregate state.
type BrowserSubmissionSettlement struct {
	State                 string `json:"state"`
	InventoryRevision     int64  `json:"inventory_revision"`
	SourceCount           int64  `json:"source_count"`
	PendingSourceCount    int64  `json:"pending_source_count"`
	IncompleteSourceCount int64  `json:"incomplete_source_count"`
}

func (v BrowserSubmissionSettlement) Validate() error {
	if v.InventoryRevision < 1 || !securitySafeInt(v.InventoryRevision) || !securitySafeInt(v.SourceCount) || v.PendingSourceCount < 0 || v.PendingSourceCount > v.SourceCount || v.IncompleteSourceCount < 0 || v.IncompleteSourceCount > v.SourceCount {
		return ErrDeliveryInvalid
	}
	expected := "settled"
	switch {
	case v.SourceCount == 0:
		expected = "not_applicable"
	case v.PendingSourceCount > 0:
		expected = "pending"
	case v.IncompleteSourceCount > 0:
		expected = "incomplete"
	}
	if v.State != expected {
		return ErrDeliveryInvalid
	}
	return nil
}
func (v BrowserSubmissionSettlement) Clone() BrowserSubmissionSettlement { return v }

// BrowserSourceSettlementFacts excludes live interaction authority. Completion
// requires a declared final boundary and actual receipts, not merely settlement
// through explicit missing positions. Omission and unresolved provenance remain
// visible even after all accepted records have arrived.
func BrowserSourceSettlementFacts(status BrowserSourceStatus, unresolvedProvenance bool) (pending, incomplete bool, err error) {
	if status.Validate() != nil {
		return false, false, ErrDeliveryInvalid
	}
	beforeDeadline := status.Closure.UploadExpiresAt == nil || status.ServerTime.Before(*status.Closure.UploadExpiresAt)
	pending = beforeDeadline && (status.Closure.FinalSequence == nil || len(status.MissingRanges) > 0 && status.DetailMode != "summary_only")
	incomplete = status.SettledThrough > status.HighestContiguous || status.Closure.UnknownTail || unresolvedProvenance || status.TerminalMissingThrough > status.HighestContiguous
	if status.Summary != nil {
		incomplete = incomplete || status.Summary.UnretainedRecordCount > 0 || !status.Summary.CountComplete
	}
	if status.Closure.FinalSequence != nil && !pending && status.HighestContiguous < *status.Closure.FinalSequence {
		incomplete = true
	}
	return pending, incomplete, nil
}
