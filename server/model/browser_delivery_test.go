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
	"strings"
	"testing"
	"time"
)

func TestBrowserStartClosedTransitions(t *testing.T) {
	id := BrowserSourceSessionID("00000000-0000-4000-8000-000000000001")
	for _, raw := range []string{`{"kind":"initial","reason":null}`, `{"kind":"initial","predecessor_source_session_id":null}`, `{"kind":"runtime_reset","predecessor_source_session_id":"` + string(id) + `"}`, `{"kind":"policy_correction","predecessor_source_session_id":"` + string(id) + `","reason":"spool_lost"}`, `{"kind":"initial","kind":"initial"}`} {
		var transition BrowserStartTransition
		if json.Unmarshal([]byte(raw), &transition) == nil {
			t.Fatalf("accepted closed transition %s", raw)
		}
	}
	for _, transition := range []BrowserStartTransition{{Kind: "initial"}, {Kind: "policy_correction", PredecessorSourceSessionID: id}, {Kind: "runtime_reset", PredecessorSourceSessionID: id, Reason: BrowserSourceResetSpoolUnavailable}} {
		raw, err := json.Marshal(transition)
		if err != nil {
			t.Fatal(err)
		}
		var decoded BrowserStartTransition
		if err := json.Unmarshal(raw, &decoded); err != nil || decoded != transition {
			t.Fatalf("transition round trip %s: %v", raw, err)
		}
	}
}
func TestBrowserEventCanonicalCodecIsClosedAndPackingIndependent(t *testing.T) {
	revision := NewExamRevisionID()
	event := BrowserActivityEvent{Sequence: 1, Kind: BrowserActivityOpened, PolicyRevisionID: revision, ClientOccurredAt: time.Date(2026, 9, 8, 1, 0, 0, 0, time.UTC)}
	raw, err := event.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "schema_version") || strings.Contains(string(raw), "location") {
		t.Fatalf("lifecycle retained navigation fields: %s", raw)
	}
	var decoded BrowserActivityEvent
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	digest, err := event.Fingerprint()
	if err != nil || !IsValidSHA256Fingerprint(digest) {
		t.Fatal("invalid event fingerprint")
	}
	otherDigest, err := decoded.Fingerprint()
	if err != nil || otherDigest != digest {
		t.Fatal("canonical replay changed event identity")
	}
	for _, field := range []string{`"location":null`, `"matched_rule_id":null`, `"block_reason":null`, `"received_at":"2026-09-08T01:00:00Z"`, `"page_title":"private"`, `"redirect_from_sequence":1`} {
		invalid := bytes.Replace(raw, []byte("{"), []byte("{"+field+","), 1)
		if json.Unmarshal(invalid, &decoded) == nil {
			t.Fatalf("accepted forbidden event field %s", field)
		}
	}
	batch := BrowserActivityBatch{SourceSessionID: "00000000-0000-4000-8000-000000000001", ParticipationID: NewAttemptParticipationID(), Generation: 1, PolicyRevisionID: revision, PolicyDigest: "sha256:" + strings.Repeat("a", 64), Events: []BrowserActivityEvent{event}}
	packed, err := json.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}
	var unpacked BrowserActivityBatch
	if err := json.Unmarshal(packed, &unpacked); err != nil {
		t.Fatal(err)
	}
	repackedDigest, err := unpacked.Events[0].Fingerprint()
	if err != nil || repackedDigest != digest {
		t.Fatal("batch packing altered event fingerprint")
	}
}
func TestBrowserRedirectUsesPreviousValidatedHop(t *testing.T) {
	policy, err := NewBrowserPolicy(true, "start", []BrowserPolicyRule{
		{RuleID: "start", Origin: "https://example.edu", PathPrefix: "/", HostMatch: BrowserPolicyHostExact, AllowRedirects: true, BlockedNavigationOutcome: BrowserPolicyBlockedNavigationRecord},
		{RuleID: "private", Origin: "https://example.edu", PathPrefix: "/private", HostMatch: BrowserPolicyHostExact, BlockedNavigationOutcome: BrowserPolicyBlockedNavigationIntegrityEvidence},
	})
	if err != nil {
		t.Fatal(err)
	}
	revision := NewExamRevisionID()
	at := time.Date(2026, 9, 8, 1, 0, 0, 0, time.UTC)
	sourceRule, targetRule := "private", "start"
	previous := int64(1)
	prior := BrowserActivityEvent{Sequence: 1, Kind: BrowserActivityTopNavigation, PolicyRevisionID: revision, ClientOccurredAt: at, Location: &BrowserLocation{Scheme: "https", Host: "example.edu", Path: "/private/page"}, MatchedRuleID: &sourceRule}
	redirect := BrowserActivityEvent{Sequence: 2, Kind: BrowserActivityTopRedirect, PolicyRevisionID: revision, ClientOccurredAt: at, Location: &BrowserLocation{Scheme: "https", Host: "example.edu", Path: "/public"}, MatchedRuleID: &targetRule, RedirectFromSequence: &previous}
	if _, err := ValidateBrowserPolicyEvent(redirect, policy, &prior); err == nil {
		t.Fatal("allowed destination bypassed previous hop redirect denial")
	}
	verified, err := ValidateBrowserPolicyEvent(redirect, policy, nil)
	if err != nil || verified {
		t.Fatal("missing previous hop was invented")
	}
	blockedReason := BrowserBlockRedirectNotAllowed
	redirect.Kind = BrowserActivityBlockedNavigation
	redirect.BlockReason = &blockedReason
	redirect.MatchedRuleID = &sourceRule
	verified, err = ValidateBrowserPolicyEvent(redirect, policy, &prior)
	if err != nil || !verified {
		t.Fatalf("correct source rule denial rejected: %v", err)
	}
	redirect.MatchedRuleID = &targetRule
	if _, err := ValidateBrowserPolicyEvent(redirect, policy, &prior); err == nil {
		t.Fatal("destination rule substituted for source rule")
	}
	redirect.MatchedRuleID = &sourceRule
	prior.Location.Path = "/public"
	prior.MatchedRuleID = &targetRule
	if _, err := ValidateBrowserPolicyEvent(redirect, policy, &prior); err == nil {
		t.Fatal("allowed previous hop falsely reported redirect denial")
	}
}
