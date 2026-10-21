// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBrowserURLAgreement(t *testing.T) {
	data, err := os.ReadFile("testdata/browser_urls.json")
	require.NoError(t, err)
	var fixtures struct {
		Cases []struct {
			Name      string          `json:"name"`
			Kind      string          `json:"kind"`
			Input     string          `json:"input"`
			Valid     bool            `json:"valid"`
			Canonical string          `json:"canonical"`
			Location  BrowserLocation `json:"location"`
		} `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(data, &fixtures))
	for _, fixture := range fixtures.Cases {
		t.Run(fixture.Name, func(t *testing.T) {
			var err error
			var canonical string
			var location BrowserLocation
			switch fixture.Kind {
			case "origin":
				canonical, err = CanonicalizeBrowserPolicyOrigin(fixture.Input)
			case "prefix":
				canonical, err = CanonicalizeBrowserPolicyPath(fixture.Input)
			case "navigation":
				location, err = CanonicalizeBrowserLocation(fixture.Input)
			default:
				require.FailNow(t, "unknown fixture kind")
			}
			if !fixture.Valid {
				require.Error(t, err, "accepted a forbidden URL")
				require.Empty(t, canonical, "retained invalid URL data")
				require.Equal(t, BrowserLocation{}, location, "retained invalid URL data")
				return
			}
			require.NoError(t, err, "result")
			require.Equal(t, fixture.Canonical, canonical, "result")
			require.Equal(t, fixture.Location, location, "result")
			if fixture.Kind == "navigation" {
				require.NoError(t, location.Validate())
			}
		})
	}
}

func TestBrowserPolicyMatchingPreservesPathIdentity(t *testing.T) {
	for _, test := range []struct {
		prefix, path string
		match        bool
	}{
		{"/~user", "/%7euser", false}, {"/%7euser", "/%7euser", true}, {"/%7Euser", "/%7euser", false},
		{"/a/b", "/a//b", false}, {"/docs", "/docs/chapter", true}, {"/docs", "/docstring", false},
		{"/a//b", "/a//b", true}, {"/docs", "/docs/?q=private#private", true},
	} {
		t.Run(test.prefix+" versus "+test.path, func(t *testing.T) {
			policy, err := NewBrowserPolicy(true, "start", []BrowserPolicyRule{browserRuleForURLTest("start", "example.com", test.prefix, BrowserPolicyHostExact)})
			require.NoError(t, err)
			rule, _, err := policy.Match("https://example.com" + test.path)
			require.NoError(t, err, "Match() = %#v", rule)
			require.Equal(t, test.match, rule != nil, "Match() = %#v", rule)
		})
	}
}

func TestBrowserPolicySpecificityIgnoresArrayOrder(t *testing.T) {
	rules := []BrowserPolicyRule{
		browserRuleForURLTest("a_root", "example.com", "/", BrowserPolicyHostExactAndSubdomains),
		browserRuleForURLTest("z_private", "example.com", "/private", BrowserPolicyHostExactAndSubdomains),
		browserRuleForURLTest("exact", "child.example.com", "/private", BrowserPolicyHostExact),
		browserRuleForURLTest("longer", "child.example.com", "/long", BrowserPolicyHostExactAndSubdomains),
		browserRuleForURLTest("shorter", "example.com", "/long", BrowserPolicyHostExactAndSubdomains),
	}
	rules[0].AllowRedirects = true
	for _, test := range []struct{ url, want string }{
		{"https://example.com/private/page", "z_private"},
		{"https://child.example.com/private/page", "exact"},
		{"https://sub.child.example.com/long/page", "longer"},
		{"https://lookalikeexample.com/", ""},
	} {
		t.Run(test.url, func(t *testing.T) {
			for range len(rules) {
				policy, err := NewBrowserPolicy(true, "a_root", rules)
				require.NoError(t, err)
				rule, _, err := policy.Match(test.url)
				require.NoError(t, err)
				if test.want == "" {
					require.Nil(t, rule, "matched a hostname lookalike")
				} else {
					require.NotNil(t, rule, "want %s", test.want)
					require.Equal(t, test.want, rule.RuleID)
					require.False(t, rule.AllowRedirects, "%s matched with redirect permission", test.want)
				}
				rules = append(rules[1:], rules[0])
			}
		})
	}
	// The lexical tie-break remains deterministic even before duplicate-predicate validation.
	a, b := rules[0], rules[0]
	a.RuleID, b.RuleID = "a", "b"
	require.True(t, browserRulePrecedes(a, b), "lexical tie-break changed")
	require.False(t, browserRulePrecedes(b, a), "lexical tie-break changed")
}

func TestBrowserURLAndPolicyBounds(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "127.1"} {
		_, err := NewBrowserPolicy(true, "start", []BrowserPolicyRule{browserRuleForURLTest("start", host, "/", BrowserPolicyHostExactAndSubdomains)})
		require.Error(t, err, "accepted wildcard IP")
	}
	for _, size := range []int{BrowserPolicyPathMaximumBytes, BrowserPolicyPathMaximumBytes + 1} {
		_, err := CanonicalizeBrowserPolicyPath("/" + strings.Repeat("a", size-1))
		require.Equal(t, size == BrowserPolicyPathMaximumBytes, err == nil, "path size %d: %v", size, err)
	}
	base := "https://example.com/"
	for _, suffix := range []string{"", "a"} {
		_, err := CanonicalizeBrowserLocation(base + strings.Repeat("a", BrowserNavigationMaximumCharacters-len(base)) + suffix)
		require.Equal(t, suffix == "", err == nil, "navigation bound: %v", err)
	}
	// Supplementary scalars use two JavaScript code units, not one rune.
	_, err := CanonicalizeBrowserLocation(base + strings.Repeat("😀", (BrowserNavigationMaximumCharacters-len(base))/2+1))
	require.Error(t, err, "accepted oversized supplementary-scalar URL")
	rules := make([]BrowserPolicyRule, 128)
	for index := range rules {
		rules[index] = browserRuleForURLTest(fmt.Sprintf("r%03d", index), "example.com", fmt.Sprintf("/p%d", index), BrowserPolicyHostExact)
	}
	_, err = NewBrowserPolicy(true, "r000", rules)
	require.NoError(t, err)
	rules = append(rules, browserRuleForURLTest("r128", "example.com", "/p128", BrowserPolicyHostExact))
	_, err = NewBrowserPolicy(true, "r000", rules)
	require.Error(t, err, "accepted 129 rules")
}

func browserRuleForURLTest(id, host, path string, match BrowserPolicyHostMatch) BrowserPolicyRule {
	return BrowserPolicyRule{RuleID: id, Origin: "https://" + host, PathPrefix: path, HostMatch: match, BlockedNavigationOutcome: BrowserPolicyBlockedNavigationRecord}
}

func TestBrowserActivityMatchingUsesSerializedPathWithoutReinterpretingInputLimit(t *testing.T) {
	policy, err := NewBrowserPolicy(true, "start", []BrowserPolicyRule{browserRuleForURLTest("start", "example.com", "/", BrowserPolicyHostExact)})
	require.NoError(t, err)
	raw := "https://example.com/" + strings.Repeat("漢", 4000)
	rule, location, err := policy.Match(raw)
	require.NoError(t, err, "Match()")
	require.NotNil(t, rule, "Match()")
	require.Greater(t, len(location.Path), BrowserNavigationMaximumCharacters, "fixture did not expand the serialized path")
	retainedRule, err := policy.MatchLocation(location)
	require.NoError(t, err, "MatchLocation()")
	require.NotNil(t, retainedRule, "MatchLocation()")
	require.Equal(t, rule.RuleID, retainedRule.RuleID, "MatchLocation()")
	location.Path = "/a/%2e%2e/b"
	_, err = policy.MatchLocation(location)
	require.Error(t, err, "accepted a retained path requiring normalization")
}
