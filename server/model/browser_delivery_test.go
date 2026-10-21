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

	"github.com/stretchr/testify/require"
)

func TestBrowserStartClosedTransitions(t *testing.T) {
	id := BrowserSourceSessionID("00000000-0000-4000-8000-000000000001")
	for _, raw := range []string{`{"kind":"initial","reason":null}`, `{"kind":"initial","predecessor_source_session_id":null}`, `{"kind":"runtime_reset","predecessor_source_session_id":"` + string(id) + `"}`, `{"kind":"policy_correction","predecessor_source_session_id":"` + string(id) + `","reason":"spool_lost"}`, `{"kind":"initial","kind":"initial"}`} {
		var transition BrowserStartTransition
		require.Error(t, json.Unmarshal([]byte(raw), &transition), "accepted closed transition %s", raw)
	}
	for _, transition := range []BrowserStartTransition{{Kind: "initial"}, {Kind: "policy_correction", PredecessorSourceSessionID: id}, {Kind: "runtime_reset", PredecessorSourceSessionID: id, Reason: BrowserSourceResetSpoolUnavailable}} {
		raw, err := json.Marshal(transition)
		require.NoError(t, err)
		var decoded BrowserStartTransition
		require.NoError(t, json.Unmarshal(raw, &decoded), "transition round trip %s", raw)
		require.Equal(t, transition, decoded, "transition round trip %s", raw)
	}
}
func TestBrowserEventCanonicalCodecIsClosedAndPackingIndependent(t *testing.T) {
	revision := NewExamRevisionID()
	event := BrowserActivityEvent{Sequence: 1, Kind: BrowserActivityOpened, PolicyRevisionID: revision, ClientOccurredAt: time.Date(2026, 9, 8, 1, 0, 0, 0, time.UTC)}
	raw, err := event.Canonical()
	require.NoError(t, err)
	require.NotContains(t, string(raw), "schema_version")
	require.NotContains(t, string(raw), "location")
	var decoded BrowserActivityEvent
	require.NoError(t, json.Unmarshal(raw, &decoded))
	digest, err := event.Fingerprint()
	require.NoError(t, err, "invalid event fingerprint")
	require.True(t, IsValidSHA256Fingerprint(digest), "invalid event fingerprint")
	otherDigest, err := decoded.Fingerprint()
	require.NoError(t, err, "canonical replay changed event identity")
	require.Equal(t, digest, otherDigest, "canonical replay changed event identity")
	for _, field := range []string{`"location":null`, `"matched_rule_id":null`, `"block_reason":null`, `"received_at":"2026-09-08T01:00:00Z"`, `"page_title":"private"`, `"redirect_from_sequence":1`} {
		invalid := bytes.Replace(raw, []byte("{"), []byte("{"+field+","), 1)
		require.Error(t, json.Unmarshal(invalid, &decoded), "accepted forbidden event field %s", field)
	}
	batch := BrowserActivityBatch{SourceSessionID: "00000000-0000-4000-8000-000000000001", ParticipationID: NewAttemptParticipationID(), Generation: 1, PolicyRevisionID: revision, PolicyDigest: "sha256:" + strings.Repeat("a", 64), Events: []BrowserActivityEvent{event}}
	packed, err := json.Marshal(batch)
	require.NoError(t, err)
	var unpacked BrowserActivityBatch
	require.NoError(t, json.Unmarshal(packed, &unpacked))
	repackedDigest, err := unpacked.Events[0].Fingerprint()
	require.NoError(t, err, "batch packing altered event fingerprint")
	require.Equal(t, digest, repackedDigest, "batch packing altered event fingerprint")
}
func TestBrowserRedirectUsesPreviousValidatedHop(t *testing.T) {
	policy, err := NewBrowserPolicy(true, "start", []BrowserPolicyRule{
		{RuleID: "start", Origin: "https://example.edu", PathPrefix: "/", HostMatch: BrowserPolicyHostExact, AllowRedirects: true, BlockedNavigationOutcome: BrowserPolicyBlockedNavigationRecord},
		{RuleID: "private", Origin: "https://example.edu", PathPrefix: "/private", HostMatch: BrowserPolicyHostExact, BlockedNavigationOutcome: BrowserPolicyBlockedNavigationIntegrityEvidence},
	})
	require.NoError(t, err)
	revision := NewExamRevisionID()
	at := time.Date(2026, 9, 8, 1, 0, 0, 0, time.UTC)
	sourceRule, targetRule := "private", "start"
	previous := int64(1)
	prior := BrowserActivityEvent{Sequence: 1, Kind: BrowserActivityTopNavigation, PolicyRevisionID: revision, ClientOccurredAt: at, Location: &BrowserLocation{Scheme: "https", Host: "example.edu", Path: "/private/page"}, MatchedRuleID: &sourceRule}
	redirect := BrowserActivityEvent{Sequence: 2, Kind: BrowserActivityTopRedirect, PolicyRevisionID: revision, ClientOccurredAt: at, Location: &BrowserLocation{Scheme: "https", Host: "example.edu", Path: "/public"}, MatchedRuleID: &targetRule, RedirectFromSequence: &previous}
	_, err = ValidateBrowserPolicyEvent(redirect, policy, &prior)
	require.Error(t, err, "allowed destination bypassed previous hop redirect denial")
	verified, err := ValidateBrowserPolicyEvent(redirect, policy, nil)
	require.NoError(t, err, "missing previous hop was invented")
	require.False(t, verified, "missing previous hop was invented")
	blockedReason := BrowserBlockRedirectNotAllowed
	redirect.Kind = BrowserActivityBlockedNavigation
	redirect.BlockReason = &blockedReason
	redirect.MatchedRuleID = &sourceRule
	verified, err = ValidateBrowserPolicyEvent(redirect, policy, &prior)
	require.NoError(t, err)
	require.True(t, verified)
	redirect.MatchedRuleID = &targetRule
	_, err = ValidateBrowserPolicyEvent(redirect, policy, &prior)
	require.Error(t, err, "destination rule substituted for source rule")
	redirect.MatchedRuleID = &sourceRule
	prior.Location.Path = "/public"
	prior.MatchedRuleID = &targetRule
	_, err = ValidateBrowserPolicyEvent(redirect, policy, &prior)
	require.Error(t, err, "allowed previous hop falsely reported redirect denial")
}
