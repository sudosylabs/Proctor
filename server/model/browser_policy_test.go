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
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBrowserPolicyCanonicalizationAndMatching(t *testing.T) {
	policy, err := NewBrowserPolicy(true, "start", []BrowserPolicyRule{
		{RuleID: "sub", Origin: "https://EXAMPLE.com:443/", PathPrefix: "/exam/", HostMatch: BrowserPolicyHostExactAndSubdomains, AllowRedirects: true, BlockedNavigationOutcome: BrowserPolicyBlockedNavigationRecord},
		{RuleID: "start", Origin: "https://library.example", PathPrefix: "/start/%7euser", HostMatch: BrowserPolicyHostExact, BlockedNavigationOutcome: BrowserPolicyBlockedNavigationRecord},
	})
	require.NoError(t, err)
	require.Equal(t, "start", policy.Rules[0].RuleID)
	require.Equal(t, "https://library.example", policy.Rules[0].Origin)
	require.Equal(t, "/start/%7euser", policy.Rules[0].PathPrefix)
	require.Equal(t, "https://example.com", policy.Rules[1].Origin)
	require.Equal(t, "/exam", policy.Rules[1].PathPrefix)
	rule, location, err := policy.Match("https://child.example.com/exam/part?q=secret#fragment")
	require.NoError(t, err, "Match() = %#v, %#v", rule, location)
	require.NotNil(t, rule, "Match() = %#v, %#v", rule, location)
	require.Equal(t, "sub", rule.RuleID, "Match() = %#v, %#v", rule, location)
	require.Equal(t, "/exam/part", location.Path)
	require.Equal(t, "child.example.com", location.Host)
	require.Empty(t, location.Port)
	rule, _, err = policy.Match("https://child.example.com/examination")
	require.NoError(t, err, "path boundary Match()")
	require.Nil(t, rule, "path boundary Match()")
	for input, want := range map[string]string{
		"https://127.1":            "127.0.0.1",
		"https://2130706433":       "127.0.0.1",
		"https://0177.0.0.1":       "127.0.0.1",
		"https://0x7f.0x0.0x0.0x1": "127.0.0.1",
	} {
		location, canonicalErr := CanonicalizeBrowserLocation(input)
		assert.NoError(t, canonicalErr, "CanonicalizeBrowserLocation(%q) = %#v, %v; want host", input, location, canonicalErr)
		assert.Equal(t, want, location.Host, "CanonicalizeBrowserLocation(%q) = %#v, %v; want host", input, location, canonicalErr)
	}
}

func TestBrowserPolicyStrictCanonicalCodec(t *testing.T) {
	disabled := DisabledBrowserPolicy()
	encoded, err := EncodeBrowserPolicy(disabled)
	require.NoError(t, err)
	want := []byte(`{"enabled":false}`)
	require.True(t, bytes.Equal(encoded, want), "encoded = %s, want %s", encoded, want)
	decoded, err := DecodeBrowserPolicy(encoded)
	require.NoError(t, err, "DecodeBrowserPolicy() = %#v", decoded)
	require.False(t, decoded.Enabled, "DecodeBrowserPolicy() = %#v", decoded)
	invalid := [][]byte{
		[]byte(`{"schema_version":1,"enabled":false,"rules":[]}`),
		[]byte(`{"schema_version":1,"schema_version":1,"enabled":false}`),
		[]byte(`{ "schema_version":1,"enabled":false}`),
		[]byte(`{"schema_version":1,"enabled":true,"start_rule_id":"x","rules":[]}`),
	}
	for _, value := range invalid {
		_, err = DecodeBrowserPolicy(value)
		assert.Error(t, err, "DecodeBrowserPolicy(%s) succeeded", value)
	}
}

func TestBrowserPolicyDocumentParserPreservesSchemaAndCanonicalSize(t *testing.T) {
	parsed, err := ParseBrowserPolicyDocument([]byte(`{ "enabled": false }`))
	require.NoError(t, err, "ParseBrowserPolicyDocument(reordered disabled) = %#v", parsed)
	require.False(t, parsed.Enabled, "ParseBrowserPolicyDocument(reordered disabled) = %#v", parsed)
	require.Equal(t, BrowserPolicySchemaVersion, parsed.SchemaVersion, "ParseBrowserPolicyDocument(reordered disabled) = %#v", parsed)
	for _, document := range []string{
		`null`, `{}`, `{"enabled":null}`, `{"schema_version":1}`,
		`{"schema_version":0,"enabled":false}`,
		`{"schema_version":2,"enabled":false}`,
		`{"schema_version":1,"enabled":null}`,
		`{"schema_version":1,"enabled":false,"rules":[]}`,
	} {
		_, err = ParseBrowserPolicyDocument([]byte(document))
		assert.Error(t, err, "ParseBrowserPolicyDocument(%s) succeeded", document)
	}

	policy, err := NewBrowserPolicy(true, "start", []BrowserPolicyRule{{
		RuleID: "start", Origin: "https://example.edu", PathPrefix: "/",
		HostMatch: BrowserPolicyHostExact, BlockedNavigationOutcome: BrowserPolicyBlockedNavigationRecord,
	}})
	require.NoError(t, err)
	encoded, err := EncodeBrowserPolicy(policy)
	require.NoError(t, err)
	// Fill the total limit with individually bounded, distinct rules.
	for index := 1; index < 15; index++ {
		policy.Rules = append(policy.Rules, BrowserPolicyRule{
			RuleID: fmt.Sprintf("z%02d", index), Origin: "https://example.edu",
			PathPrefix: fmt.Sprintf("/%02d", index) + strings.Repeat("a", 2040),
			HostMatch:  BrowserPolicyHostExact, BlockedNavigationOutcome: BrowserPolicyBlockedNavigationRecord,
		})
	}
	encoded, err = EncodeBrowserPolicy(policy)
	require.NoError(t, err)
	policy.Rules[0].PathPrefix += strings.Repeat("a", BrowserPolicyMaximumBytes-len(encoded))
	encoded, err = EncodeBrowserPolicy(policy)
	require.NoError(t, err, "near-limit canonical Browser Policy size")
	require.Len(t, encoded, BrowserPolicyMaximumBytes, "near-limit canonical Browser Policy size")
	var jsonbText bytes.Buffer
	err = json.Indent(&jsonbText, encoded, "", "  ")
	require.NoError(t, err)
	require.Greater(t, jsonbText.Len(), BrowserPolicyMaximumBytes, "expanded Browser Policy size")
	parsed, err = ParseBrowserPolicyDocument(jsonbText.Bytes())
	require.NoError(t, err, "ParseBrowserPolicyDocument(expanded near-limit) = %#v", parsed)
	require.Equal(t, policy.Rules[0].PathPrefix, parsed.Rules[0].PathPrefix, "ParseBrowserPolicyDocument(expanded near-limit) = %#v", parsed)
}

func TestBrowserPolicyRejectsUnsafeURLsAndDuplicateMatches(t *testing.T) {
	unsafe := []string{"http://example.com", "https://user@example.com", "https://example.com/path", "https://example.com\\evil", "https://example.com?secret=x",
		"https://256.1", "https://1.2.3.4294967295", "https://1.2.3.4.5", "https://09.0.0.1"}
	for _, value := range unsafe {
		_, err := CanonicalizeBrowserPolicyOrigin(value)
		assert.Error(t, err, "CanonicalizeBrowserPolicyOrigin(%q) succeeded", value)
	}
	for _, value := range []string{"exam", "/exam?secret", "/exam#fragment", "/bad%2", "/bad\\path"} {
		_, err := CanonicalizeBrowserPolicyPath(value)
		assert.Error(t, err, "CanonicalizeBrowserPolicyPath(%q) succeeded", value)
	}
	_, err := NewBrowserPolicy(true, "a", []BrowserPolicyRule{
		{RuleID: "a", Origin: "https://example.com", PathPrefix: "/", HostMatch: BrowserPolicyHostExact, BlockedNavigationOutcome: BrowserPolicyBlockedNavigationRecord},
		{RuleID: "b", Origin: "https://example.com", PathPrefix: "/", HostMatch: BrowserPolicyHostExact, BlockedNavigationOutcome: BrowserPolicyBlockedNavigationRecord},
	})
	require.Error(t, err, "NewBrowserPolicy() accepted duplicate match rules")
}

func TestBrowserPolicyInstitutionHTTPException(t *testing.T) {
	for _, test := range []struct {
		pin, origin string
		valid       bool
	}{
		{"https://institution.example", "http://INSTITUTION.example:80/", true},
		{"https://institution.example:443", "http://institution.example", true},
		{"https://institution.example:8443", "http://institution.example:8443", true},
		{"https://institution.example:8443", "http://institution.example", false},
		{"https://institution.example", "http://institution.example:443", false},
		{"https://institution.example", "http://other.example", false},
		{"http://institution.example", "http://institution.example", false},
	} {
		t.Run(test.origin, func(t *testing.T) {
			policy, err := NewBrowserPolicy(true, "institution", []BrowserPolicyRule{{RuleID: "institution", Origin: test.origin, PathPrefix: "/exam", HostMatch: BrowserPolicyHostExact, BlockedNavigationOutcome: BrowserPolicyBlockedNavigationIntegrityEvidence, InstitutionHTTPException: true}})
			require.NoError(t, err)
			err = policy.ValidateInstitutionOrigin(test.pin)
			assert.Equal(t, test.valid, err == nil, "pin %q rule %q validity=%v: %v", test.pin, test.origin, test.valid, err)
			raw, err := EncodeBrowserPolicy(policy)
			require.NoError(t, err)
			require.NotContains(t, string(raw), "schema_version", "wrong browser policy wire envelope")
			require.Contains(t, string(raw), `"institution_http_exception":true`, "wrong browser policy wire envelope")
			decoded, err := DecodeBrowserPolicy(raw)
			require.NoError(t, err)
			digest, err := BrowserPolicyDigest(decoded)
			require.NoError(t, err, "policy digest does not cover exact canonical policy")
			require.Equal(t, SHA256Fingerprint(raw), digest, "policy digest does not cover exact canonical policy")
			rule, _, err := policy.Match(policy.Rules[0].Origin + "/exam/part?ignored=private#fragment")
			require.NoError(t, err, "explicit HTTP rule failed")
			require.NotNil(t, rule, "explicit HTTP rule failed")
			require.Equal(t, BrowserPolicyBlockedNavigationIntegrityEvidence, rule.BlockedNavigationOutcome, "explicit HTTP rule failed")
			opposite := strings.Replace(policy.Rules[0].Origin, "http:", "https:", 1) + "/exam/part"
			rule, _, err = policy.Match(opposite)
			require.NoError(t, err, "HTTP exception matched an unconfigured scheme")
			require.Nil(t, rule, "HTTP exception matched an unconfigured scheme")
		})
	}
	for _, test := range []struct {
		origin    string
		exception bool
	}{{"https://institution.example", true}, {"http://institution.example", false}} {
		t.Run(test.origin, func(t *testing.T) {
			_, err := NewBrowserPolicy(true, "institution", []BrowserPolicyRule{{RuleID: "institution", Origin: test.origin, PathPrefix: "/", HostMatch: BrowserPolicyHostExact, BlockedNavigationOutcome: BrowserPolicyBlockedNavigationRecord, InstitutionHTTPException: test.exception}})
			require.Error(t, err, "accepted mismatched scheme/exception")
		})
	}
}

func TestBrowserPolicyRequiresExplicitExceptionField(t *testing.T) {
	policy, err := NewBrowserPolicy(true, "start", []BrowserPolicyRule{{RuleID: "start", Origin: "https://example.edu", PathPrefix: "/", HostMatch: BrowserPolicyHostExact, BlockedNavigationOutcome: BrowserPolicyBlockedNavigationRecord}})
	require.NoError(t, err)
	raw, err := EncodeBrowserPolicy(policy)
	require.NoError(t, err)
	for _, bad := range []string{strings.Replace(string(raw), `"institution_http_exception":false,`, "", 1), strings.Replace(string(raw), `"institution_http_exception":false`, `"institution_http_exception":null`, 1), strings.Replace(string(raw), `"enabled":true`, `"enabled":true,"schema_version":1`, 1)} {
		_, err := ParseBrowserPolicyDocument([]byte(bad))
		require.Error(t, err, "accepted old or incomplete rule codec")
	}
}

func TestBrowserPolicyCanonicalAgreementFixtures(t *testing.T) {
	raw, err := os.ReadFile("testdata/browser_policies.json")
	require.NoError(t, err)
	var fixtures struct {
		Cases []struct {
			Name              string          `json:"name"`
			InstitutionOrigin string          `json:"institution_origin"`
			Policy            json.RawMessage `json:"policy"`
			Canonical         string          `json:"canonical"`
			Digest            string          `json:"digest"`
		} `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(raw, &fixtures))
	for _, fixture := range fixtures.Cases {
		t.Run(fixture.Name, func(t *testing.T) {
			policy, err := ParseBrowserPolicyDocument(fixture.Policy)
			require.NoError(t, err)
			require.NoError(t, policy.ValidateInstitutionOrigin(fixture.InstitutionOrigin))
			encoded, err := EncodeBrowserPolicy(policy)
			require.NoError(t, err, "canonical")
			require.Equal(t, fixture.Canonical, string(encoded), "canonical")
			digest, err := BrowserPolicyDigest(policy)
			require.NoError(t, err, "digest")
			require.Equal(t, fixture.Digest, digest, "digest")
		})
	}
}
