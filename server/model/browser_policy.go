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
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strings"
)

const (
	BrowserPolicySchemaVersion = 1
	BrowserPolicyMaximumRules  = 128
	BrowserPolicyMaximumBytes  = 32 * 1024
	// browserPolicyMaximumDocumentBytes bounds a semantically equivalent
	// database/jsonb representation. PostgreSQL inserts insignificant spacing
	// when rendering jsonb, while BrowserPolicyMaximumBytes applies to the
	// canonical encoding that is digested and delivered.
	browserPolicyMaximumDocumentBytes = 64 * 1024
)

type BrowserPolicyHostMatch string

const (
	BrowserPolicyHostExact              BrowserPolicyHostMatch = "exact"
	BrowserPolicyHostExactAndSubdomains BrowserPolicyHostMatch = "exact_and_subdomains"
)

func (value BrowserPolicyHostMatch) IsValid() bool {
	return value == BrowserPolicyHostExact || value == BrowserPolicyHostExactAndSubdomains
}

type BrowserPolicyBlockedNavigationOutcome string

const (
	BrowserPolicyBlockedNavigationRecord            BrowserPolicyBlockedNavigationOutcome = "record"
	BrowserPolicyBlockedNavigationIntegrityEvidence BrowserPolicyBlockedNavigationOutcome = "integrity_evidence"
)

type BrowserPolicyRule struct {
	RuleID                   string
	Origin                   string
	PathPrefix               string
	HostMatch                BrowserPolicyHostMatch
	AllowRedirects           bool
	BlockedNavigationOutcome BrowserPolicyBlockedNavigationOutcome
	InstitutionHTTPException bool
}

type BrowserPolicy struct {
	SchemaVersion int
	Enabled       bool
	StartRuleID   string
	Rules         []BrowserPolicyRule
}

type BrowserLocation struct {
	Scheme string `json:"scheme"`
	Host   string `json:"host"`
	Port   string `json:"port,omitempty"`
	Path   string `json:"path"`
}

func DisabledBrowserPolicy() BrowserPolicy {
	return BrowserPolicy{SchemaVersion: BrowserPolicySchemaVersion}
}

func NewBrowserPolicy(enabled bool, startRuleID string, rules []BrowserPolicyRule) (BrowserPolicy, error) {
	if len(rules) > BrowserPolicyMaximumRules || !enabled && (startRuleID != "" || len(rules) != 0) {
		return BrowserPolicy{}, errors.New("model: invalid Browser Policy fields or rule count")
	}
	policy := BrowserPolicy{SchemaVersion: BrowserPolicySchemaVersion, Enabled: enabled, StartRuleID: startRuleID,
		Rules: append([]BrowserPolicyRule(nil), rules...)}
	if !enabled {
		policy.StartRuleID = ""
		policy.Rules = nil
	}
	for index := range policy.Rules {
		origin, err := canonicalizeBrowserPolicyRuleOrigin(policy.Rules[index].Origin, policy.Rules[index].InstitutionHTTPException)
		if err != nil {
			return BrowserPolicy{}, fmt.Errorf("model: browser policy rule %d origin: %w", index, err)
		}
		prefix, err := CanonicalizeBrowserPolicyPath(policy.Rules[index].PathPrefix)
		if err != nil {
			return BrowserPolicy{}, fmt.Errorf("model: browser policy rule %d path: %w", index, err)
		}
		policy.Rules[index].Origin = origin
		policy.Rules[index].PathPrefix = prefix
	}
	slices.SortFunc(policy.Rules, func(left, right BrowserPolicyRule) int { return strings.Compare(left.RuleID, right.RuleID) })
	if err := policy.Validate(); err != nil {
		return BrowserPolicy{}, err
	}
	return policy, nil
}

func (policy BrowserPolicy) Validate() error {
	if policy.SchemaVersion != BrowserPolicySchemaVersion {
		return errors.New("model: unsupported Browser Policy schema version")
	}
	if !policy.Enabled {
		if policy.StartRuleID != "" || len(policy.Rules) != 0 {
			return errors.New("model: disabled Browser Policy contains enabled fields")
		}
		return nil
	}
	if !validBrowserPolicyRuleID(policy.StartRuleID) || len(policy.Rules) < 1 || len(policy.Rules) > BrowserPolicyMaximumRules {
		return errors.New("model: invalid enabled Browser Policy")
	}
	ids := make(map[string]struct{}, len(policy.Rules))
	matchKeys := make(map[string]struct{}, len(policy.Rules))
	previous := ""
	for _, rule := range policy.Rules {
		if !validBrowserPolicyRuleID(rule.RuleID) || rule.RuleID <= previous || !rule.HostMatch.IsValid() ||
			!slices.Contains([]BrowserPolicyBlockedNavigationOutcome{BrowserPolicyBlockedNavigationRecord, BrowserPolicyBlockedNavigationIntegrityEvidence}, rule.BlockedNavigationOutcome) {
			return errors.New("model: invalid canonical Browser Policy rule")
		}
		origin, err := canonicalizeBrowserPolicyRuleOrigin(rule.Origin, rule.InstitutionHTTPException)
		if err != nil || origin != rule.Origin {
			return errors.New("model: invalid canonical Browser Policy origin")
		}
		parsed, _ := url.Parse(origin)
		if net.ParseIP(parsed.Hostname()) != nil && rule.HostMatch != BrowserPolicyHostExact {
			return errors.New("model: IP Browser Policy rule requires exact host match")
		}
		prefix, err := CanonicalizeBrowserPolicyPath(rule.PathPrefix)
		if err != nil || prefix != rule.PathPrefix {
			return errors.New("model: invalid canonical Browser Policy path")
		}
		if _, exists := ids[rule.RuleID]; exists {
			return errors.New("model: duplicate Browser Policy rule identity")
		}
		key := rule.Origin + "\x00" + rule.PathPrefix + "\x00" + string(rule.HostMatch)
		if _, exists := matchKeys[key]; exists {
			return errors.New("model: duplicate Browser Policy match rule")
		}
		ids[rule.RuleID], matchKeys[key], previous = struct{}{}, struct{}{}, rule.RuleID
	}
	if _, exists := ids[policy.StartRuleID]; !exists {
		return errors.New("model: Browser Policy start rule does not exist")
	}
	encoded, err := EncodeBrowserPolicy(policy)
	if err != nil || len(encoded) > BrowserPolicyMaximumBytes {
		return errors.New("model: Browser Policy exceeds its canonical size limit")
	}
	return nil
}

func (policy BrowserPolicy) Clone() BrowserPolicy {
	policy.Rules = append([]BrowserPolicyRule(nil), policy.Rules...)
	return policy
}

func validBrowserPolicyRuleID(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for _, character := range []byte(value) {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func (policy BrowserPolicy) Match(rawURL string) (*BrowserPolicyRule, BrowserLocation, error) {
	if err := policy.Validate(); err != nil {
		return nil, BrowserLocation{}, err
	}
	location, err := CanonicalizeBrowserLocation(rawURL)
	if err != nil {
		return nil, BrowserLocation{}, err
	}
	return policy.matchLocation(location), location, nil
}

// MatchLocation validates and selects a rule for already-minimized activity.
// Its serialized path can be longer than the original navigation's UTF-16
// input after browser escaping. Reconstructing a URL would apply the wrong
// bound and could reinterpret bytes that the browser already serialized.
func (policy BrowserPolicy) MatchLocation(location BrowserLocation) (*BrowserPolicyRule, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	if err := location.Validate(); err != nil {
		return nil, err
	}
	return policy.matchLocation(location), nil
}

func (policy BrowserPolicy) matchLocation(location BrowserLocation) *BrowserPolicyRule {
	if !policy.Enabled {
		return nil
	}
	var selected *BrowserPolicyRule
	for index := range policy.Rules {
		rule := &policy.Rules[index]
		parsed, _ := url.Parse(rule.Origin)
		defaultPort := "443"
		if parsed.Scheme == "http" {
			defaultPort = "80"
		}
		ruleHost, rulePort, _ := canonicalBrowserHostPortWithDefault(parsed, defaultPort)
		if parsed.Scheme != location.Scheme || rulePort != location.Port || !browserHostMatches(location.Host, ruleHost, rule.HostMatch) || !browserPathMatches(location.Path, rule.PathPrefix) {
			continue
		}
		if selected == nil || browserRulePrecedes(*rule, *selected) {
			selected = rule
		}
	}
	if selected == nil {
		return nil
	}
	copy := *selected
	return &copy
}

func browserHostMatches(actual, rule string, match BrowserPolicyHostMatch) bool {
	return actual == rule || match == BrowserPolicyHostExactAndSubdomains && strings.HasSuffix(actual, "."+rule)
}

func browserPathMatches(actual, prefix string) bool {
	return actual == prefix || prefix == "/" || strings.HasPrefix(actual, prefix+"/")
}

func browserRulePrecedes(candidate, current BrowserPolicyRule) bool {
	if len(candidate.PathPrefix) != len(current.PathPrefix) {
		return len(candidate.PathPrefix) > len(current.PathPrefix)
	}
	if candidate.HostMatch != current.HostMatch {
		return candidate.HostMatch == BrowserPolicyHostExact
	}
	candidateURL, _ := url.Parse(candidate.Origin)
	currentURL, _ := url.Parse(current.Origin)
	if len(candidateURL.Hostname()) != len(currentURL.Hostname()) {
		return len(candidateURL.Hostname()) > len(currentURL.Hostname())
	}
	return candidate.RuleID < current.RuleID
}

type browserPolicyRuleWire struct {
	RuleID                   string                                `json:"rule_id"`
	Origin                   string                                `json:"origin"`
	PathPrefix               string                                `json:"path_prefix"`
	HostMatch                BrowserPolicyHostMatch                `json:"host_match"`
	AllowRedirects           bool                                  `json:"allow_redirects"`
	BlockedNavigationOutcome BrowserPolicyBlockedNavigationOutcome `json:"blocked_navigation_outcome"`
	InstitutionHTTPException bool                                  `json:"institution_http_exception"`
}

func EncodeBrowserPolicy(policy BrowserPolicy) ([]byte, error) {
	if policy.SchemaVersion != BrowserPolicySchemaVersion {
		return nil, errors.New("model: unsupported Browser Policy schema version")
	}
	var encoded []byte
	var err error
	if !policy.Enabled {
		if policy.StartRuleID != "" || len(policy.Rules) != 0 {
			return nil, errors.New("model: disabled Browser Policy contains enabled fields")
		}
		encoded, err = encodeCanonicalExamDocument(struct {
			Enabled bool `json:"enabled"`
		}{false})
	} else {
		rules := make([]browserPolicyRuleWire, len(policy.Rules))
		for index, rule := range policy.Rules {
			rules[index] = browserPolicyRuleWire(rule)
		}
		encoded, err = encodeCanonicalExamDocument(struct {
			Enabled     bool                    `json:"enabled"`
			StartRuleID string                  `json:"start_rule_id"`
			Rules       []browserPolicyRuleWire `json:"rules"`
		}{true, policy.StartRuleID, rules})
	}
	if err != nil || len(encoded) > BrowserPolicyMaximumBytes {
		return nil, errors.New("model: Browser Policy encoding failed or exceeded its limit")
	}
	return encoded, nil
}

func DecodeBrowserPolicy(data []byte) (BrowserPolicy, error) {
	return decodeBrowserPolicy(data, true)
}

// ParseBrowserPolicyDocument validates a JSON object without requiring its
// insignificant whitespace or member order to match the canonical wire
// encoding. PostgreSQL jsonb normalizes both when it stores a document.
func ParseBrowserPolicyDocument(data []byte) (BrowserPolicy, error) {
	return decodeBrowserPolicy(data, false)
}

func decodeBrowserPolicy(data []byte, requireCanonical bool) (BrowserPolicy, error) {
	maximumBytes := BrowserPolicyMaximumBytes
	if !requireCanonical {
		maximumBytes = browserPolicyMaximumDocumentBytes
	}
	if len(data) == 0 || len(data) > maximumBytes {
		return BrowserPolicy{}, errors.New("model: invalid Browser Policy size")
	}
	if err := validateExamDocumentJSON(data); err != nil {
		return BrowserPolicy{}, err
	}

	var members map[string]json.RawMessage
	if json.Unmarshal(data, &members) != nil || members["enabled"] == nil || bytes.Equal(bytes.TrimSpace(members["enabled"]), []byte("null")) {
		return BrowserPolicy{}, errors.New("model: Browser Policy enabled is required")
	}
	var wire struct {
		Enabled     bool                    `json:"enabled"`
		StartRuleID string                  `json:"start_rule_id"`
		Rules       []browserPolicyRuleWire `json:"rules"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return BrowserPolicy{}, err
	}
	if wire.Enabled {
		if len(members) != 3 || members["start_rule_id"] == nil || wire.Rules == nil {
			return BrowserPolicy{}, errors.New("model: enabled Browser Policy requires its rules")
		}
	} else if len(members) != 1 {
		return BrowserPolicy{}, errors.New("model: disabled Browser Policy has enabled fields")
	}
	rules := make([]BrowserPolicyRule, len(wire.Rules))
	for i, rule := range wire.Rules {
		rules[i] = BrowserPolicyRule(rule)
	}
	policy := BrowserPolicy{SchemaVersion: BrowserPolicySchemaVersion, Enabled: wire.Enabled, StartRuleID: wire.StartRuleID, Rules: rules}
	if err := policy.Validate(); err != nil {
		return BrowserPolicy{}, err
	}
	canonical, err := EncodeBrowserPolicy(policy)
	if err != nil {
		return BrowserPolicy{}, err
	}
	if requireCanonical && !bytes.Equal(data, canonical) {
		return BrowserPolicy{}, errors.New("model: Browser Policy is not canonical")
	}
	return policy, nil
}

func (rule *browserPolicyRuleWire) UnmarshalJSON(raw []byte) error {
	if rule == nil {
		return errors.New("model: nil browser rule")
	}
	type wire browserPolicyRuleWire
	var decoded wire
	if err := decodeClosedDeliveryDeclaration(raw, &decoded, BrowserPolicyMaximumBytes); err != nil {
		return err
	}
	*rule = browserPolicyRuleWire(decoded)
	return nil
}

// ValidateInstitutionOrigin checks the installation-owned HTTPS pin separately
// from the portable policy shape. HTTP navigation never authorizes credential
// transport and cannot be enabled by a caller-supplied institution identity.
func (policy BrowserPolicy) ValidateInstitutionOrigin(institutionOrigin string) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	for _, rule := range policy.Rules {
		if !rule.InstitutionHTTPException {
			continue
		}
		pin, err := CanonicalizeBrowserPolicyOrigin(institutionOrigin)
		if err != nil {
			return errors.New("model: institution HTTP exception needs a supported HTTPS origin")
		}
		origin, _ := url.Parse(pin)
		candidate, _ := url.Parse(rule.Origin)
		host, port, err := canonicalBrowserHostPortWithDefault(origin, "443")
		if err != nil {
			return err
		}
		candidateHost, candidatePort, err := canonicalBrowserHostPortWithDefault(candidate, "80")
		if err != nil {
			return err
		}
		if host != candidateHost || port != candidatePort {
			return errors.New("model: HTTP exception differs from the pinned institution origin")
		}
	}
	return nil
}

func BrowserPolicyDigest(policy BrowserPolicy) (string, error) {
	encoded, err := EncodeBrowserPolicy(policy)
	if err != nil {
		return "", err
	}
	return SHA256Fingerprint(encoded), nil
}

// MayCreateIntegrityEvidence is derived from the delivered rules, never a
// candidate assertion. Disabled collection cannot produce browser evidence.
func (policy BrowserPolicy) MayCreateIntegrityEvidence() bool {
	if !policy.Enabled {
		return false
	}
	for _, rule := range policy.Rules {
		if rule.BlockedNavigationOutcome == BrowserPolicyBlockedNavigationIntegrityEvidence {
			return true
		}
	}
	return false
}
