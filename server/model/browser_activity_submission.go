// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import "errors"

type BrowserActivitySubmissionState string

const (
	BrowserActivitySubmissionNotApplicable BrowserActivitySubmissionState = "not_applicable"
	BrowserActivitySubmissionComplete      BrowserActivitySubmissionState = "complete"
	BrowserActivitySubmissionGapped        BrowserActivitySubmissionState = "gapped"
)

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

type BrowserActivitySubmission struct {
	State           BrowserActivitySubmissionState
	SourceSessionID BrowserSourceSessionID
	FinalSequence   *int64
	GapReason       BrowserActivitySubmissionGapReason
}

func (value BrowserActivitySubmission) ValidateClient() error {
	if err := value.validate(); err != nil {
		return err
	}
	if value.State == BrowserActivitySubmissionGapped && !value.GapReason.IsClientReason() {
		return errors.New("model: Browser Activity Submission gap reason is server-only")
	}
	return nil
}

func (value BrowserActivitySubmission) Validate() error { return value.validate() }

func (value BrowserActivitySubmission) validate() error {
	switch value.State {
	case BrowserActivitySubmissionNotApplicable:
		if value.SourceSessionID != "" || value.FinalSequence != nil || value.GapReason != "" {
			return errors.New("model: not-applicable Browser Activity Submission contains source state")
		}
	case BrowserActivitySubmissionComplete:
		if !value.SourceSessionID.IsValid() || value.FinalSequence == nil || *value.FinalSequence < 1 || value.GapReason != "" {
			return errors.New("model: incomplete complete Browser Activity Submission")
		}
	case BrowserActivitySubmissionGapped:
		if !value.SourceSessionID.IsValid() || value.FinalSequence != nil && *value.FinalSequence < 1 || !value.GapReason.IsValid() {
			return errors.New("model: invalid gapped Browser Activity Submission")
		}
	default:
		return errors.New("model: invalid Browser Activity Submission state")
	}
	return nil
}

func (value BrowserActivitySubmission) Clone() BrowserActivitySubmission {
	if value.FinalSequence != nil {
		sequence := *value.FinalSequence
		value.FinalSequence = &sequence
	}
	return value
}
