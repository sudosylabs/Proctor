// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

type BrowserActivitySubmissionGapReason string

const (
	BrowserActivityGapSpoolOverflow       BrowserActivitySubmissionGapReason = "spool_overflow"
	BrowserActivityGapSpoolCorrupt        BrowserActivitySubmissionGapReason = "spool_corrupt"
	BrowserActivityGapSpoolKeyUnavailable BrowserActivitySubmissionGapReason = "spool_key_unavailable"
	BrowserActivityGapDeliveryIncomplete  BrowserActivitySubmissionGapReason = "delivery_incomplete"
	BrowserActivityGapSourceNotFinalized  BrowserActivitySubmissionGapReason = "source_not_finalized"
)

func (reason BrowserActivitySubmissionGapReason) IsClientReason() bool {
	return reason == BrowserActivityGapSpoolOverflow || reason == BrowserActivityGapSpoolCorrupt ||
		reason == BrowserActivityGapSpoolKeyUnavailable || reason == BrowserActivityGapDeliveryIncomplete
}

func (reason BrowserActivitySubmissionGapReason) IsValid() bool {
	return reason.IsClientReason() || reason == BrowserActivityGapSourceNotFinalized
}
