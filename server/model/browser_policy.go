// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/idna"
)

const (
	BrowserPolicySchemaVersion = 1
	BrowserPolicyMaximumRules  = 64
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

const BrowserPolicyBlockedNavigationRecord BrowserPolicyBlockedNavigationOutcome = "record"

type BrowserPolicyRule struct {
	RuleID                   string
	Origin                   string
	PathPrefix               string
	HostMatch                BrowserPolicyHostMatch
	AllowRedirects           bool
	BlockedNavigationOutcome BrowserPolicyBlockedNavigationOutcome
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
	policy := BrowserPolicy{SchemaVersion: BrowserPolicySchemaVersion, Enabled: enabled, StartRuleID: startRuleID,
		Rules: append([]BrowserPolicyRule(nil), rules...)}
	if !enabled {
		policy.StartRuleID = ""
		policy.Rules = nil
	}
	for index := range policy.Rules {
		origin, err := CanonicalizeBrowserPolicyOrigin(policy.Rules[index].Origin)
		if err != nil {
			return BrowserPolicy{}, fmt.Errorf("model: browser policy rule %q origin: %w", policy.Rules[index].RuleID, err)
		}
		prefix, err := CanonicalizeBrowserPolicyPath(policy.Rules[index].PathPrefix)
		if err != nil {
			return BrowserPolicy{}, fmt.Errorf("model: browser policy rule %q path: %w", policy.Rules[index].RuleID, err)
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
			rule.BlockedNavigationOutcome != BrowserPolicyBlockedNavigationRecord {
			return errors.New("model: invalid canonical Browser Policy rule")
		}
		origin, err := CanonicalizeBrowserPolicyOrigin(rule.Origin)
		if err != nil || origin != rule.Origin {
			return errors.New("model: invalid canonical Browser Policy origin")
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

func CanonicalizeBrowserPolicyOrigin(value string) (string, error) {
	if invalidBrowserURLText(value) {
		return "", errors.New("origin contains forbidden characters")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawPath != "" {
		return "", errors.New("origin must be an absolute HTTPS origin")
	}
	host, port, err := canonicalBrowserHostPort(parsed)
	if err != nil {
		return "", err
	}
	return "https://" + browserHostPort(host, port), nil
}

func CanonicalizeBrowserPolicyPath(value string) (string, error) {
	if value == "" || value[0] != '/' || invalidBrowserURLText(value) || strings.ContainsAny(value, "?#") {
		return "", errors.New("path prefix must be an absolute path without query or fragment")
	}
	normalized, err := normalizeBrowserPercentEncoding(value)
	if err != nil {
		return "", err
	}
	cleaned := path.Clean(normalized)
	if cleaned == "." {
		cleaned = "/"
	}
	if cleaned[0] != '/' {
		cleaned = "/" + cleaned
	}
	return cleaned, nil
}

func CanonicalizeBrowserLocation(value string) (BrowserLocation, error) {
	if invalidBrowserURLText(value) {
		return BrowserLocation{}, errors.New("URL contains forbidden characters")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return BrowserLocation{}, errors.New("URL must be absolute HTTPS")
	}
	host, port, err := canonicalBrowserHostPort(parsed)
	if err != nil {
		return BrowserLocation{}, err
	}
	urlPath := parsed.EscapedPath()
	if urlPath == "" {
		urlPath = "/"
	}
	urlPath, err = CanonicalizeBrowserPolicyPath(urlPath)
	if err != nil {
		return BrowserLocation{}, err
	}
	return BrowserLocation{Scheme: "https", Host: host, Port: port, Path: urlPath}, nil
}

func canonicalBrowserHostPort(parsed *url.URL) (string, string, error) {
	return canonicalBrowserHostPortWithDefault(parsed, "443")
}

func canonicalBrowserHostPortWithDefault(parsed *url.URL, defaultPort string) (string, string, error) {
	host := parsed.Hostname()
	if host == "" {
		return "", "", errors.New("host is required")
	}
	if address := net.ParseIP(host); address != nil {
		host = address.String()
	} else if browserNumericHost(host) {
		var err error
		host, err = canonicalBrowserIPv4(host)
		if err != nil {
			return "", "", err
		}
	} else {
		ascii, err := idna.Lookup.ToASCII(strings.ToLower(host))
		if err != nil || ascii == "" || strings.HasSuffix(ascii, ".") {
			return "", "", errors.New("host is invalid")
		}
		host = strings.ToLower(ascii)
	}
	port := parsed.Port()
	if port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 || strconv.Itoa(number) != port {
			return "", "", errors.New("port is invalid")
		}
		if port == defaultPort {
			port = ""
		}
	}
	return host, port, nil
}

// canonicalBrowserIPv4 implements the IPv4-number grammar used by WHATWG URL
// hosts so the server and the embedded browser agree on non-dotted-decimal
// spellings before policy matching.
func canonicalBrowserIPv4(host string) (string, error) {
	parts := strings.Split(host, ".")
	if len(parts) > 1 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	if len(parts) < 1 || len(parts) > 4 {
		return "", errors.New("numeric host is invalid")
	}
	numbers := make([]uint64, len(parts))
	for index, part := range parts {
		if part == "" {
			return "", errors.New("numeric host is invalid")
		}
		radix, digits := 10, part
		if len(digits) >= 2 && (strings.HasPrefix(digits, "0x") || strings.HasPrefix(digits, "0X")) {
			radix, digits = 16, digits[2:]
		} else if len(digits) >= 2 && digits[0] == '0' {
			radix, digits = 8, digits[1:]
		}
		if digits == "" {
			digits = "0"
		}
		number, err := strconv.ParseUint(digits, radix, 32)
		if err != nil {
			return "", errors.New("numeric host is invalid")
		}
		numbers[index] = number
	}
	for _, number := range numbers[:len(numbers)-1] {
		if number > 255 {
			return "", errors.New("numeric host is invalid")
		}
	}
	lastLimit := uint64(1) << (8 * (5 - len(numbers)))
	if numbers[len(numbers)-1] >= lastLimit {
		return "", errors.New("numeric host is invalid")
	}
	address := numbers[len(numbers)-1]
	for index, number := range numbers[:len(numbers)-1] {
		address += number << (8 * (3 - index))
	}
	return net.IPv4(byte(address>>24), byte(address>>16), byte(address>>8), byte(address)).String(), nil
}

func browserNumericHost(host string) bool {
	host = strings.TrimSuffix(host, ".")
	last := host
	if index := strings.LastIndexByte(host, '.'); index >= 0 {
		last = host[index+1:]
	}
	if last == "" {
		return false
	}
	if strings.HasPrefix(last, "0x") || strings.HasPrefix(last, "0X") {
		if len(last) == 2 {
			return false
		}
		for _, character := range last[2:] {
			if !strings.ContainsRune("0123456789abcdefABCDEF", character) {
				return false
			}
		}
		return true
	}
	for _, character := range last {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func browserHostPort(host, port string) string {
	if port != "" {
		return net.JoinHostPort(host, port)
	}
	if strings.Contains(host, ":") {
		return "[" + host + "]"
	}
	return host
}

func invalidBrowserURLText(value string) bool {
	if value == "" || !utf8.ValidString(value) || strings.ContainsRune(value, '\\') {
		return true
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return true
		}
	}
	return false
}

func normalizeBrowserPercentEncoding(value string) (string, error) {
	var builder strings.Builder
	for index := 0; index < len(value); index++ {
		if value[index] != '%' {
			builder.WriteByte(value[index])
			continue
		}
		if index+2 >= len(value) {
			return "", errors.New("malformed percent encoding")
		}
		high, low := fromHex(value[index+1]), fromHex(value[index+2])
		if high < 0 || low < 0 {
			return "", errors.New("malformed percent encoding")
		}
		decoded := byte(high<<4 | low)
		if isBrowserUnreserved(decoded) {
			builder.WriteByte(decoded)
		} else {
			const upper = "0123456789ABCDEF"
			builder.WriteByte('%')
			builder.WriteByte(upper[decoded>>4])
			builder.WriteByte(upper[decoded&15])
		}
		index += 2
	}
	return builder.String(), nil
}

func fromHex(value byte) int {
	switch {
	case value >= '0' && value <= '9':
		return int(value - '0')
	case value >= 'a' && value <= 'f':
		return int(value-'a') + 10
	case value >= 'A' && value <= 'F':
		return int(value-'A') + 10
	default:
		return -1
	}
}

func isBrowserUnreserved(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || strings.ContainsRune("-._~", rune(value))
}

func (policy BrowserPolicy) Match(rawURL string) (*BrowserPolicyRule, BrowserLocation, error) {
	if err := policy.Validate(); err != nil {
		return nil, BrowserLocation{}, err
	}
	location, err := CanonicalizeBrowserLocation(rawURL)
	if err != nil {
		return nil, BrowserLocation{}, err
	}
	if !policy.Enabled {
		return nil, location, nil
	}
	var selected *BrowserPolicyRule
	for index := range policy.Rules {
		rule := &policy.Rules[index]
		parsed, _ := url.Parse(rule.Origin)
		ruleHost, rulePort, _ := canonicalBrowserHostPort(parsed)
		if rulePort != location.Port || !browserHostMatches(location.Host, ruleHost, rule.HostMatch) || !browserPathMatches(location.Path, rule.PathPrefix) {
			continue
		}
		if selected == nil || browserRulePrecedes(*rule, *selected) {
			selected = rule
		}
	}
	if selected == nil {
		return nil, location, nil
	}
	copy := *selected
	return &copy, location, nil
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
		encoded, err = json.Marshal(struct {
			SchemaVersion int  `json:"schema_version"`
			Enabled       bool `json:"enabled"`
		}{policy.SchemaVersion, false})
	} else {
		rules := make([]browserPolicyRuleWire, len(policy.Rules))
		for index, rule := range policy.Rules {
			rules[index] = browserPolicyRuleWire(rule)
		}
		encoded, err = json.Marshal(struct {
			SchemaVersion int                     `json:"schema_version"`
			Enabled       bool                    `json:"enabled"`
			StartRuleID   string                  `json:"start_rule_id"`
			Rules         []browserPolicyRuleWire `json:"rules"`
		}{policy.SchemaVersion, true, policy.StartRuleID, rules})
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
	if err := rejectDuplicateJSONFields(data); err != nil {
		return BrowserPolicy{}, err
	}
	var discriminator struct {
		SchemaVersion *int  `json:"schema_version"`
		Enabled       *bool `json:"enabled"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&discriminator); err != nil || requireJSONEOF(decoder) != nil ||
		discriminator.SchemaVersion == nil || discriminator.Enabled == nil {
		return BrowserPolicy{}, errors.New("model: invalid Browser Policy envelope")
	}
	if !*discriminator.Enabled {
		var envelope struct {
			SchemaVersion *int  `json:"schema_version"`
			Enabled       *bool `json:"enabled"`
		}
		decoder = json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&envelope); err != nil || requireJSONEOF(decoder) != nil ||
			envelope.SchemaVersion == nil || *envelope.SchemaVersion != BrowserPolicySchemaVersion ||
			envelope.Enabled == nil || *envelope.Enabled {
			return BrowserPolicy{}, errors.New("model: invalid disabled Browser Policy")
		}
		policy := DisabledBrowserPolicy()
		canonical, _ := EncodeBrowserPolicy(policy)
		if requireCanonical && !bytes.Equal(data, canonical) {
			return BrowserPolicy{}, errors.New("model: Browser Policy is not canonical")
		}
		return policy, nil
	}
	var wire struct {
		SchemaVersion *int                    `json:"schema_version"`
		Enabled       *bool                   `json:"enabled"`
		StartRuleID   string                  `json:"start_rule_id"`
		Rules         []browserPolicyRuleWire `json:"rules"`
	}
	decoder = json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return BrowserPolicy{}, err
	}
	if err := requireJSONEOF(decoder); err != nil {
		return BrowserPolicy{}, err
	}
	if wire.SchemaVersion == nil || wire.Enabled == nil {
		return BrowserPolicy{}, errors.New("model: invalid Browser Policy envelope")
	}
	rules := make([]BrowserPolicyRule, len(wire.Rules))
	for index, rule := range wire.Rules {
		rules[index] = BrowserPolicyRule(rule)
	}
	policy := BrowserPolicy{SchemaVersion: *wire.SchemaVersion, Enabled: *wire.Enabled, StartRuleID: wire.StartRuleID, Rules: rules}
	if err := policy.Validate(); err != nil {
		return BrowserPolicy{}, err
	}
	canonical, _ := EncodeBrowserPolicy(policy)
	if requireCanonical && !bytes.Equal(data, canonical) {
		return BrowserPolicy{}, errors.New("model: Browser Policy is not canonical")
	}
	return policy, nil
}

func BrowserPolicyDigest(policy BrowserPolicy) (string, error) {
	encoded, err := EncodeBrowserPolicy(policy)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}
