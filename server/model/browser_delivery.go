// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"encoding/json"
	"net/url"
	"time"
)

const BrowserCorrectionStartLimit = 32
const BrowserRuntimeResetStartLimit = 16

type BrowserStartTransition struct {
	Kind                       string                   `json:"kind"`
	PredecessorSourceSessionID BrowserSourceSessionID   `json:"predecessor_source_session_id,omitempty"`
	Reason                     BrowserSourceResetReason `json:"reason,omitempty"`
}

func (value BrowserStartTransition) Validate() error {
	switch value.Kind {
	case "initial":
		if value.PredecessorSourceSessionID != "" || value.Reason != "" {
			return ErrDeliveryInvalid
		}
	case "policy_correction":
		if !value.PredecessorSourceSessionID.IsValid() || value.Reason != "" {
			return ErrDeliveryInvalid
		}
	case "runtime_reset":
		if !value.PredecessorSourceSessionID.IsValid() || !value.Reason.IsValid() {
			return ErrDeliveryInvalid
		}
	default:
		return ErrDeliveryInvalid
	}
	return nil
}
func (value *BrowserStartTransition) UnmarshalJSON(raw []byte) error {
	type wire BrowserStartTransition
	var decoded wire
	if value == nil || decodeClosedDeliveryDeclaration(raw, &decoded, 2048) != nil {
		return ErrDeliveryInvalid
	}
	candidate := BrowserStartTransition(decoded)
	if candidate.Validate() != nil {
		return ErrDeliveryInvalid
	}
	*value = candidate
	return nil
}

type BrowserSourceStart struct {
	ParticipationID  AttemptParticipationID `json:"participation_id"`
	Generation       int64                  `json:"generation"`
	SourceSessionID  BrowserSourceSessionID `json:"source_session_id"`
	PolicyRevisionID ExamRevisionID         `json:"policy_revision_id"`
	PolicyDigest     string                 `json:"policy_digest"`
	Transition       BrowserStartTransition `json:"transition"`
}

func (value BrowserSourceStart) Validate() error {
	if !value.ParticipationID.IsValid() || value.Generation < 1 || !securitySafeInt(value.Generation) || !value.SourceSessionID.IsValid() || !value.PolicyRevisionID.IsValid() || !IsValidSHA256Fingerprint(value.PolicyDigest) || value.Transition.Validate() != nil || value.Transition.PredecessorSourceSessionID == value.SourceSessionID {
		return ErrDeliveryInvalid
	}
	return nil
}
func (value BrowserSourceStart) Canonical() ([]byte, error) {
	if value.Validate() != nil {
		return nil, ErrDeliveryInvalid
	}
	return encodeCanonicalExamDocument(value)
}
func (value *BrowserSourceStart) UnmarshalJSON(raw []byte) error {
	type wire BrowserSourceStart
	var decoded wire
	if value == nil || decodeClosedDeliveryDeclaration(raw, &decoded, 8192) != nil {
		return ErrDeliveryInvalid
	}
	candidate := BrowserSourceStart(decoded)
	if candidate.Validate() != nil {
		return ErrDeliveryInvalid
	}
	*value = candidate
	return nil
}

type BrowserEventReceipt struct {
	Sequence    int64     `json:"sequence"`
	EventDigest string    `json:"event_digest"`
	ReceivedAt  time.Time `json:"received_at"`
}

func (value BrowserEventReceipt) Validate() error {
	if value.Sequence < 1 || value.Sequence > BrowserParticipationPositionLimit || !IsValidSHA256Fingerprint(value.EventDigest) || !securityInstant(value.ReceivedAt) {
		return ErrDeliveryInvalid
	}
	return nil
}

type BrowserDeliveryProgress struct {
	HighestContiguous      int64           `json:"highest_contiguous_sequence"`
	SettledThrough         int64           `json:"settled_through_sequence"`
	HighestSeen            int64           `json:"highest_seen_sequence"`
	AllocatedThrough       int64           `json:"allocated_through_sequence"`
	TerminalMissingThrough int64           `json:"terminal_missing_through_sequence"`
	MissingRanges          []SequenceRange `json:"missing_ranges"`
	MissingRangesTruncated bool            `json:"missing_ranges_truncated"`
}

func (value BrowserDeliveryProgress) Validate() error {
	if value.HighestContiguous < 0 || value.SettledThrough < value.HighestContiguous || value.HighestSeen < value.HighestContiguous || value.AllocatedThrough < value.HighestSeen || value.AllocatedThrough < value.SettledThrough || value.AllocatedThrough > BrowserParticipationPositionLimit || value.TerminalMissingThrough < 0 || value.TerminalMissingThrough > value.AllocatedThrough || ValidateSequenceRanges(value.MissingRanges, 32) != nil {
		return ErrDeliveryInvalid
	}
	for _, r := range value.MissingRanges {
		if r.First <= value.SettledThrough || r.Last > value.AllocatedThrough {
			return ErrDeliveryInvalid
		}
	}
	return nil
}

type BrowserSourceStatus struct {
	BrowserDeliveryProgress
	SourceSessionID             BrowserSourceSessionID     `json:"source_session_id"`
	AttemptID                   ExamAttemptID              `json:"attempt_id"`
	ParticipationID             AttemptParticipationID     `json:"participation_id"`
	Generation                  int64                      `json:"generation"`
	PolicyRevisionID            ExamRevisionID             `json:"policy_revision_id"`
	PolicyDigest                string                     `json:"policy_digest"`
	PredecessorSourceSessionID  *BrowserSourceSessionID    `json:"predecessor_source_session_id"`
	StartTransition             string                     `json:"start_transition"`
	RuntimeResetReason          *BrowserSourceResetReason  `json:"runtime_reset_reason"`
	StartedAt                   time.Time                  `json:"started_at"`
	Closure                     DeliveryClosure            `json:"closure"`
	DeclarationRevision         int64                      `json:"declaration_revision"`
	DetailMode                  string                     `json:"detail_mode"`
	BudgetScope                 *string                    `json:"budget_scope"`
	SummaryOnlyReason           *DeliveryStopReason        `json:"summary_only_reason"`
	RemainingCorrectionStarts   int64                      `json:"remaining_correction_starts"`
	RemainingRuntimeResetStarts int64                      `json:"remaining_runtime_reset_starts"`
	Summary                     *UnretainedDeliverySummary `json:"summary"`
	ServerTime                  time.Time                  `json:"server_time"`
}

func (value BrowserSourceStatus) Validate() error {
	if value.BrowserDeliveryProgress.Validate() != nil || !value.SourceSessionID.IsValid() || !value.AttemptID.IsValid() || !value.ParticipationID.IsValid() || value.Generation < 1 || !securitySafeInt(value.Generation) || !value.PolicyRevisionID.IsValid() || !IsValidSHA256Fingerprint(value.PolicyDigest) || !securityInstant(value.StartedAt) || !securityInstant(value.ServerTime) || value.Closure.Validate(false) != nil || !securitySafeInt(value.DeclarationRevision) || value.RemainingCorrectionStarts < 0 || value.RemainingCorrectionStarts > BrowserCorrectionStartLimit || value.RemainingRuntimeResetStarts < 0 || value.RemainingRuntimeResetStarts > BrowserRuntimeResetStartLimit {
		return ErrDeliveryInvalid
	}
	transition := BrowserStartTransition{Kind: value.StartTransition}
	if value.PredecessorSourceSessionID != nil {
		transition.PredecessorSourceSessionID = *value.PredecessorSourceSessionID
	}
	if value.RuntimeResetReason != nil {
		transition.Reason = *value.RuntimeResetReason
	}
	if transition.Validate() != nil {
		return ErrDeliveryInvalid
	}
	switch value.DetailMode {
	case "collecting":
		if value.BudgetScope != nil || value.SummaryOnlyReason != nil {
			return ErrDeliveryInvalid
		}
	case "summary_only":
		if value.BudgetScope == nil || (*value.BudgetScope != "attempt" && *value.BudgetScope != "participation") || value.SummaryOnlyReason == nil {
			return ErrDeliveryInvalid
		}
		quota := NewDeliveryQuotaUsage(false, false)
		quota.SummaryOnly = true
		quota.StopReason = value.SummaryOnlyReason
		if quota.Validate() != nil {
			return ErrDeliveryInvalid
		}
	default:
		return ErrDeliveryInvalid
	}
	if value.Summary != nil && value.Summary.Validate() != nil {
		return ErrDeliveryInvalid
	}
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > 16*1024 {
		return ErrDeliveryInvalid
	}
	return nil
}

// ValidateBrowserPolicyEvent separates verified navigation consequences from a
// missing prior hop. An unresolved redirect is retained as uncertainty, never
// converted into validated integrity evidence by guessing its source rule.
func ValidateBrowserPolicyEvent(event BrowserActivityEvent, policy BrowserPolicy, prior *BrowserActivityEvent) (bool, error) {
	if event.ValidateClientRecord() != nil || policy.Validate() != nil || !policy.Enabled {
		return false, ErrDeliveryInvalid
	}
	if event.Kind == BrowserActivityOpened || event.Kind == BrowserActivityClosed {
		return true, nil
	}
	var sourceRule *BrowserPolicyRule
	if event.RedirectFromSequence != nil && prior != nil {
		if prior.Sequence != *event.RedirectFromSequence || prior.PolicyRevisionID != event.PolicyRevisionID || (prior.Kind != BrowserActivityTopNavigation && prior.Kind != BrowserActivityTopRedirect) || prior.Location == nil || prior.MatchedRuleID == nil {
			return false, ErrDeliveryConflict
		}
		var err error
		sourceRule, err = policy.MatchLocation(*prior.Location)
		if err != nil || sourceRule == nil || sourceRule.RuleID != *prior.MatchedRuleID {
			return false, ErrDeliveryConflict
		}
	}
	if event.BlockReason != nil && *event.BlockReason == BrowserBlockRedirectNotAllowed {
		if event.MatchedRuleID == nil {
			return false, ErrDeliveryConflict
		}
		if prior == nil {
			return false, nil
		}
		if sourceRule == nil || sourceRule.AllowRedirects || sourceRule.RuleID != *event.MatchedRuleID {
			return false, ErrDeliveryConflict
		}
		return true, nil
	}
	if sourceRule != nil && !sourceRule.AllowRedirects {
		return false, ErrDeliveryConflict
	}
	if event.BlockReason != nil && *event.BlockReason == BrowserBlockInvalidURL {
		return event.RedirectFromSequence == nil || prior != nil, nil
	}
	schemeAllowed := event.Location.Scheme == "https"
	if event.Location.Scheme == "http" {
		for _, r := range policy.Rules {
			if r.InstitutionHTTPException {
				schemeAllowed = true
				break
			}
		}
	}
	if event.BlockReason != nil && *event.BlockReason == BrowserBlockSchemeNotAllowed {
		if schemeAllowed {
			return false, ErrDeliveryConflict
		}
		return event.RedirectFromSequence == nil || prior != nil, nil
	}
	if !schemeAllowed {
		return false, ErrDeliveryConflict
	}
	selected, err := policy.MatchLocation(*event.Location)
	if err != nil {
		return false, err
	}
	if event.Kind != BrowserActivityBlockedNavigation {
		if selected == nil || event.MatchedRuleID == nil || selected.RuleID != *event.MatchedRuleID {
			return false, ErrDeliveryConflict
		}
	} else {
		originMatched := false
		for _, rule := range policy.Rules {
			parsed, _ := url.Parse(rule.Origin)
			port := "443"
			if parsed.Scheme == "http" {
				port = "80"
			}
			host, explicit, _ := canonicalBrowserHostPortWithDefault(parsed, port)
			if parsed.Scheme == event.Location.Scheme && explicit == event.Location.Port && browserHostMatches(event.Location.Host, host, rule.HostMatch) {
				originMatched = true
				break
			}
		}
		if selected != nil || event.MatchedRuleID != nil || event.BlockReason == nil {
			return false, ErrDeliveryConflict
		}
		if *event.BlockReason == BrowserBlockOriginNotAllowed && originMatched || *event.BlockReason == BrowserBlockPathNotAllowed && !originMatched {
			return false, ErrDeliveryConflict
		}
	}
	return event.RedirectFromSequence == nil || prior != nil, nil
}

type BrowserActivityBatch struct {
	SourceSessionID  BrowserSourceSessionID `json:"source_session_id"`
	ParticipationID  AttemptParticipationID `json:"participation_id"`
	Generation       int64                  `json:"generation"`
	PolicyRevisionID ExamRevisionID         `json:"policy_revision_id"`
	PolicyDigest     string                 `json:"policy_digest"`
	Events           []BrowserActivityEvent `json:"events"`
}

func (value BrowserActivityBatch) Validate() error {
	if !value.SourceSessionID.IsValid() || !value.ParticipationID.IsValid() || value.Generation < 1 || !securitySafeInt(value.Generation) || !value.PolicyRevisionID.IsValid() || !IsValidSHA256Fingerprint(value.PolicyDigest) || len(value.Events) < 1 || len(value.Events) > 64 {
		return ErrDeliveryInvalid
	}
	for i, event := range value.Events {
		if event.ValidateClientRecord() != nil || event.PolicyRevisionID != value.PolicyRevisionID || i > 0 && event.Sequence <= value.Events[i-1].Sequence {
			return ErrDeliveryInvalid
		}
	}
	raw, err := encodeCanonicalExamDocument(value)
	if err != nil || len(raw) > BrowserActivityAppendMaximumBytes {
		return ErrDeliveryInvalid
	}
	return nil
}
func (value *BrowserActivityBatch) UnmarshalJSON(raw []byte) error {
	type wire BrowserActivityBatch
	var decoded wire
	if value == nil || decodeClosedDeliveryDeclaration(raw, &decoded, BrowserActivityAppendMaximumBytes) != nil {
		return ErrDeliveryInvalid
	}
	candidate := BrowserActivityBatch(decoded)
	if candidate.Validate() != nil {
		return ErrDeliveryInvalid
	}
	*value = candidate
	return nil
}

// BrowserDeliveryGapResult preserves the declaration receipt alongside current progress.
type BrowserDeliveryGapResult struct {
	Receipt DeliveryGapReceipt  `json:"receipt"`
	Status  BrowserSourceStatus `json:"status"`
}
type BrowserDeliverySummaryResult struct {
	Summary UnretainedDeliverySummary `json:"summary"`
	Status  BrowserSourceStatus       `json:"status"`
}
type BrowserReceiptPage struct {
	Receipts     []BrowserEventReceipt `json:"receipts"`
	NextSequence *int64                `json:"next_sequence"`
}
