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
)

func TestBrowserPolicyCanonicalizationAndMatching(t *testing.T) {
	policy, err := NewBrowserPolicy(true, "start", []BrowserPolicyRule{
		{RuleID: "sub", Origin: "https://EXAMPLE.com:443/", PathPrefix: "/exam/../exam", HostMatch: BrowserPolicyHostExactAndSubdomains, AllowRedirects: true, BlockedNavigationOutcome: BrowserPolicyBlockedNavigationRecord},
		{RuleID: "start", Origin: "https://bücher.example", PathPrefix: "/start/%7euser", HostMatch: BrowserPolicyHostExact, BlockedNavigationOutcome: BrowserPolicyBlockedNavigationRecord},
	})
	if err != nil {
		t.Fatalf("NewBrowserPolicy() error = %v", err)
	}
	if policy.Rules[0].RuleID != "start" || policy.Rules[0].Origin != "https://xn--bcher-kva.example" || policy.Rules[0].PathPrefix != "/start/~user" ||
		policy.Rules[1].Origin != "https://example.com" || policy.Rules[1].PathPrefix != "/exam" {
		t.Fatalf("canonical policy = %#v", policy)
	}
	rule, location, err := policy.Match("https://child.example.com/exam/part?q=secret#fragment")
	if err != nil || rule == nil || rule.RuleID != "sub" {
		t.Fatalf("Match() = %#v, %#v, %v", rule, location, err)
	}
	if location.Path != "/exam/part" || location.Host != "child.example.com" || location.Port != "" {
		t.Fatalf("minimized location = %#v", location)
	}
	if rule, _, err = policy.Match("https://child.example.com/examination"); err != nil || rule != nil {
		t.Fatalf("path boundary Match() = %#v, %v", rule, err)
	}
	for input, want := range map[string]string{
		"https://127.1":            "127.0.0.1",
		"https://2130706433":       "127.0.0.1",
		"https://0177.0.0.1":       "127.0.0.1",
		"https://0x7f.0x0.0x0.0x1": "127.0.0.1",
	} {
		location, canonicalErr := CanonicalizeBrowserLocation(input)
		if canonicalErr != nil || location.Host != want {
			t.Errorf("CanonicalizeBrowserLocation(%q) = %#v, %v; want host %q", input, location, canonicalErr, want)
		}
	}
}

func TestBrowserPolicyStrictCanonicalCodec(t *testing.T) {
	disabled := DisabledBrowserPolicy()
	encoded, err := EncodeBrowserPolicy(disabled)
	if err != nil {
		t.Fatalf("EncodeBrowserPolicy() error = %v", err)
	}
	if want := []byte(`{"schema_version":1,"enabled":false}`); !bytes.Equal(encoded, want) {
		t.Fatalf("encoded = %s, want %s", encoded, want)
	}
	decoded, err := DecodeBrowserPolicy(encoded)
	if err != nil || decoded.Enabled {
		t.Fatalf("DecodeBrowserPolicy() = %#v, %v", decoded, err)
	}
	invalid := [][]byte{
		[]byte(`{"schema_version":1,"enabled":false,"rules":[]}`),
		[]byte(`{"schema_version":1,"schema_version":1,"enabled":false}`),
		[]byte(`{ "schema_version":1,"enabled":false}`),
		[]byte(`{"schema_version":1,"enabled":true,"start_rule_id":"x","rules":[]}`),
	}
	for _, value := range invalid {
		if _, err = DecodeBrowserPolicy(value); err == nil {
			t.Errorf("DecodeBrowserPolicy(%s) succeeded", value)
		}
	}
}

func TestBrowserPolicyDocumentParserPreservesSchemaAndCanonicalSize(t *testing.T) {
	parsed, err := ParseBrowserPolicyDocument([]byte(`{"enabled": false, "schema_version": 1}`))
	if err != nil || parsed.Enabled || parsed.SchemaVersion != BrowserPolicySchemaVersion {
		t.Fatalf("ParseBrowserPolicyDocument(reordered disabled) = %#v, %v", parsed, err)
	}
	for _, document := range []string{
		`null`, `{}`, `{"enabled":false}`, `{"schema_version":1}`,
		`{"schema_version":0,"enabled":false}`,
		`{"schema_version":2,"enabled":false}`,
		`{"schema_version":1,"enabled":null}`,
		`{"schema_version":1,"enabled":false,"rules":[]}`,
	} {
		if _, err = ParseBrowserPolicyDocument([]byte(document)); err == nil {
			t.Errorf("ParseBrowserPolicyDocument(%s) succeeded", document)
		}
	}

	policy, err := NewBrowserPolicy(true, "start", []BrowserPolicyRule{{
		RuleID: "start", Origin: "https://example.edu", PathPrefix: "/",
		HostMatch: BrowserPolicyHostExact, BlockedNavigationOutcome: BrowserPolicyBlockedNavigationRecord,
	}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeBrowserPolicy(policy)
	if err != nil {
		t.Fatal(err)
	}
	policy.Rules[0].PathPrefix += strings.Repeat("a", BrowserPolicyMaximumBytes-len(encoded))
	encoded, err = EncodeBrowserPolicy(policy)
	if err != nil || len(encoded) != BrowserPolicyMaximumBytes {
		t.Fatalf("near-limit canonical Browser Policy size = %d, %v", len(encoded), err)
	}
	var jsonbText bytes.Buffer
	if err = json.Indent(&jsonbText, encoded, "", "  "); err != nil {
		t.Fatal(err)
	}
	if jsonbText.Len() <= BrowserPolicyMaximumBytes {
		t.Fatalf("expanded Browser Policy size = %d", jsonbText.Len())
	}
	parsed, err = ParseBrowserPolicyDocument(jsonbText.Bytes())
	if err != nil || parsed.Rules[0].PathPrefix != policy.Rules[0].PathPrefix {
		t.Fatalf("ParseBrowserPolicyDocument(expanded near-limit) = %#v, %v", parsed, err)
	}
}

func TestBrowserPolicyRejectsUnsafeURLsAndDuplicateMatches(t *testing.T) {
	unsafe := []string{"http://example.com", "https://user@example.com", "https://example.com/path", "https://example.com\\evil", "https://example.com?secret=x",
		"https://256.1", "https://1.2.3.4294967295", "https://1.2.3.4.5", "https://09.0.0.1"}
	for _, value := range unsafe {
		if _, err := CanonicalizeBrowserPolicyOrigin(value); err == nil {
			t.Errorf("CanonicalizeBrowserPolicyOrigin(%q) succeeded", value)
		}
	}
	for _, value := range []string{"exam", "/exam?secret", "/exam#fragment", "/bad%2", "/bad\\path"} {
		if _, err := CanonicalizeBrowserPolicyPath(value); err == nil {
			t.Errorf("CanonicalizeBrowserPolicyPath(%q) succeeded", value)
		}
	}
	_, err := NewBrowserPolicy(true, "a", []BrowserPolicyRule{
		{RuleID: "a", Origin: "https://example.com", PathPrefix: "/", HostMatch: BrowserPolicyHostExact, BlockedNavigationOutcome: BrowserPolicyBlockedNavigationRecord},
		{RuleID: "b", Origin: "https://example.com", PathPrefix: "/", HostMatch: BrowserPolicyHostExact, BlockedNavigationOutcome: BrowserPolicyBlockedNavigationRecord},
	})
	if err == nil {
		t.Fatal("NewBrowserPolicy() accepted duplicate match rules")
	}
}
