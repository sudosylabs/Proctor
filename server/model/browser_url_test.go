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
)

func TestBrowserURLAgreement(t *testing.T) {
	data, err := os.ReadFile("testdata/browser_urls.json")
	if err != nil {
		t.Fatal(err)
	}
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
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
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
				t.Fatal("unknown fixture kind")
			}
			if !fixture.Valid {
				if err == nil {
					t.Fatal("accepted a forbidden URL")
				}
				if canonical != "" || location != (BrowserLocation{}) {
					t.Fatal("retained invalid URL data")
				}
				return
			}
			if err != nil || canonical != fixture.Canonical || location != fixture.Location {
				t.Fatalf("result = %q, %#v, %v; want %q, %#v", canonical, location, err, fixture.Canonical, fixture.Location)
			}
			if fixture.Kind == "navigation" {
				if err := location.Validate(); err != nil {
					t.Fatalf("retained location failed validation: %v", err)
				}
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
			if err != nil {
				t.Fatal(err)
			}
			rule, _, err := policy.Match("https://example.com" + test.path)
			if err != nil || (rule != nil) != test.match {
				t.Fatalf("Match() = %#v, %v", rule, err)
			}
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
				if err != nil {
					t.Fatal(err)
				}
				rule, _, err := policy.Match(test.url)
				if err != nil {
					t.Fatal(err)
				}
				if test.want == "" {
					if rule != nil {
						t.Fatal("matched a hostname lookalike")
					}
				} else if rule == nil || rule.RuleID != test.want || rule.AllowRedirects {
					t.Fatalf("matched %#v, want %s without redirect permission", rule, test.want)
				}
				rules = append(rules[1:], rules[0])
			}
		})
	}
	// The lexical tie-break remains deterministic even before duplicate-predicate validation.
	a, b := rules[0], rules[0]
	a.RuleID, b.RuleID = "a", "b"
	if !browserRulePrecedes(a, b) || browserRulePrecedes(b, a) {
		t.Fatal("lexical tie-break changed")
	}
}

func TestBrowserURLAndPolicyBounds(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "127.1"} {
		_, err := NewBrowserPolicy(true, "start", []BrowserPolicyRule{browserRuleForURLTest("start", host, "/", BrowserPolicyHostExactAndSubdomains)})
		if err == nil {
			t.Fatal("accepted wildcard IP")
		}
	}
	for _, size := range []int{BrowserPolicyPathMaximumBytes, BrowserPolicyPathMaximumBytes + 1} {
		_, err := CanonicalizeBrowserPolicyPath("/" + strings.Repeat("a", size-1))
		if (err == nil) != (size == BrowserPolicyPathMaximumBytes) {
			t.Fatalf("path size %d: %v", size, err)
		}
	}
	base := "https://example.com/"
	for _, suffix := range []string{"", "a"} {
		_, err := CanonicalizeBrowserLocation(base + strings.Repeat("a", BrowserNavigationMaximumCharacters-len(base)) + suffix)
		if (err == nil) != (suffix == "") {
			t.Fatalf("navigation bound: %v", err)
		}
	}
	// Supplementary scalars use two JavaScript code units, not one rune.
	_, err := CanonicalizeBrowserLocation(base + strings.Repeat("😀", (BrowserNavigationMaximumCharacters-len(base))/2+1))
	if err == nil {
		t.Fatal("accepted oversized supplementary-scalar URL")
	}
	rules := make([]BrowserPolicyRule, 128)
	for index := range rules {
		rules[index] = browserRuleForURLTest(fmt.Sprintf("r%03d", index), "example.com", fmt.Sprintf("/p%d", index), BrowserPolicyHostExact)
	}
	if _, err := NewBrowserPolicy(true, "r000", rules); err != nil {
		t.Fatalf("128 short rules: %v", err)
	}
	rules = append(rules, browserRuleForURLTest("r128", "example.com", "/p128", BrowserPolicyHostExact))
	if _, err := NewBrowserPolicy(true, "r000", rules); err == nil {
		t.Fatal("accepted 129 rules")
	}
}

func browserRuleForURLTest(id, host, path string, match BrowserPolicyHostMatch) BrowserPolicyRule {
	return BrowserPolicyRule{RuleID: id, Origin: "https://" + host, PathPrefix: path, HostMatch: match, BlockedNavigationOutcome: BrowserPolicyBlockedNavigationRecord}
}

func TestBrowserActivityMatchingUsesSerializedPathWithoutReinterpretingInputLimit(t *testing.T) {
	policy, err := NewBrowserPolicy(true, "start", []BrowserPolicyRule{browserRuleForURLTest("start", "example.com", "/", BrowserPolicyHostExact)})
	if err != nil {
		t.Fatal(err)
	}
	raw := "https://example.com/" + strings.Repeat("漢", 4000)
	rule, location, err := policy.Match(raw)
	if err != nil || rule == nil {
		t.Fatalf("Match() = %#v, %v", rule, err)
	}
	if len(location.Path) <= BrowserNavigationMaximumCharacters {
		t.Fatal("fixture did not expand the serialized path")
	}
	retainedRule, err := policy.MatchLocation(location)
	if err != nil || retainedRule == nil || retainedRule.RuleID != rule.RuleID {
		t.Fatalf("MatchLocation() = %#v, %v", retainedRule, err)
	}
	location.Path = "/a/%2e%2e/b"
	if _, err := policy.MatchLocation(location); err == nil {
		t.Fatal("accepted a retained path requiring normalization")
	}
}
