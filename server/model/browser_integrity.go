// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
package model

import "time"

// BrowserIntegrityEvidence is an immutable minimized copy. Ordinary Browser
// Activity retirement cannot erase it or become an authorization prerequisite.
type BrowserIntegrityEvidence struct {
	SourceSessionID  BrowserSourceSessionID `json:"source_session_id"`
	PolicyRevisionID ExamRevisionID         `json:"policy_revision_id"`
	RuleID           string                 `json:"rule_id"`
	Event            BrowserActivityEvent   `json:"event"`
}

func (v BrowserIntegrityEvidence) Validate() error {
	if !v.SourceSessionID.IsValid() || !v.PolicyRevisionID.IsValid() || v.Event.PolicyRevisionID != v.PolicyRevisionID || v.Event.ValidateClientRecord() != nil || v.Event.Kind != BrowserActivityBlockedNavigation || v.Event.MatchedRuleID == nil || *v.Event.MatchedRuleID != v.RuleID {
		return ErrDeliveryInvalid
	}
	return nil
}

// Qualification follows full frozen-policy and redirect provenance validation.
// A rule-less block has no invented attribution and cannot create a Flag.
func BrowserIntegrityRule(event BrowserActivityEvent, policy BrowserPolicy) *BrowserPolicyRule {
	if event.Kind != BrowserActivityBlockedNavigation || event.MatchedRuleID == nil {
		return nil
	}
	for _, rule := range policy.Rules {
		if rule.RuleID == *event.MatchedRuleID && rule.BlockedNavigationOutcome == BrowserPolicyBlockedNavigationIntegrityEvidence {
			return &rule
		}
	}
	return nil
}

type BrowserIntegrityGroup struct {
	ParticipationID         AttemptParticipationID `json:"participation_id"`
	PolicyRevisionID        ExamRevisionID         `json:"policy_revision_id"`
	RuleID                  string                 `json:"rule_id"`
	OverflowFirstReceivedAt *time.Time             `json:"overflow_first_received_at"`
	OverflowLastReceivedAt  *time.Time             `json:"overflow_last_received_at"`
}
type BrowserIntegrityOverflow struct {
	ValidatedEventCount int64     `json:"validated_event_count"`
	FirstReceivedAt     time.Time `json:"first_received_at"`
	LastReceivedAt      time.Time `json:"last_received_at"`
	Reason              string    `json:"reason"`
}

func (v BrowserIntegrityGroup) Validate() error {
	if !v.ParticipationID.IsValid() || !v.PolicyRevisionID.IsValid() || !validBrowserPolicyRuleID(v.RuleID) || (v.OverflowFirstReceivedAt == nil) != (v.OverflowLastReceivedAt == nil) {
		return ErrDeliveryInvalid
	}
	if v.OverflowFirstReceivedAt != nil && (v.OverflowFirstReceivedAt.IsZero() || v.OverflowLastReceivedAt.Before(*v.OverflowFirstReceivedAt)) {
		return ErrDeliveryInvalid
	}
	return nil
}
func (v BrowserIntegrityOverflow) Validate() error {
	if v.ValidatedEventCount < 1 || !securitySafeInt(v.ValidatedEventCount) || v.FirstReceivedAt.IsZero() || v.LastReceivedAt.Before(v.FirstReceivedAt) || v.Reason != "group_capacity" {
		return ErrDeliveryInvalid
	}
	return nil
}
func (v BrowserIntegrityEvidence) Clone() BrowserIntegrityEvidence {
	if v.Event.Location != nil {
		x := *v.Event.Location
		v.Event.Location = &x
	}
	if v.Event.MatchedRuleID != nil {
		x := *v.Event.MatchedRuleID
		v.Event.MatchedRuleID = &x
	}
	if v.Event.BlockReason != nil {
		x := *v.Event.BlockReason
		v.Event.BlockReason = &x
	}
	if v.Event.RedirectFromSequence != nil {
		x := *v.Event.RedirectFromSequence
		v.Event.RedirectFromSequence = &x
	}
	return v
}
func (v BrowserIntegrityGroup) Clone() BrowserIntegrityGroup {
	if v.OverflowFirstReceivedAt != nil {
		x := *v.OverflowFirstReceivedAt
		v.OverflowFirstReceivedAt = &x
	}
	if v.OverflowLastReceivedAt != nil {
		x := *v.OverflowLastReceivedAt
		v.OverflowLastReceivedAt = &x
	}
	return v
}
