// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/identityprovider"
)

const (
	ExternalAuthenticationTypeCAS  = "cas"
	ExternalAuthenticationTypeOIDC = "oidc"
)

// ExternalClaimMapping names the provider claims that Proctor is allowed to
// consume. Provider adapters normalize their protocol-specific representation
// before applying this mapping.
type ExternalClaimMapping struct {
	Subject                  string   `json:"Subject"`
	Username                 string   `json:"Username"`
	Email                    string   `json:"Email"`
	EmailVerifiedClaim       string   `json:"EmailVerifiedClaim"`
	FirstName                string   `json:"FirstName"`
	LastName                 string   `json:"LastName"`
	DisplayName              string   `json:"DisplayName"`
	HomeOrganization         string   `json:"HomeOrganization"`
	Affiliation              string   `json:"Affiliation"`
	AllowedHomeOrganizations []string `json:"AllowedHomeOrganizations"`
	TrustEmail               bool     `json:"TrustEmail"`
	MultiFactorAttribute     string   `json:"MultiFactorAttribute"`
	MultiFactorValues        []string `json:"MultiFactorValues"`
}

type CASProvider struct {
	BaseURL          string   `json:"BaseURL"`
	ValidationPath   string   `json:"ValidationPath"`
	Timeout          Duration `json:"Timeout"`
	MaxResponseBytes int64    `json:"MaxResponseBytes"`
}

// OIDCProvider deliberately uses issuer discovery instead of accepting a set
// of independently configured endpoints. This preserves issuer consistency
// and lets standards-compliant providers rotate endpoints and signing keys.
type OIDCProvider struct {
	Issuer           string   `json:"Issuer"`
	ClientID         string   `json:"ClientID"`
	ClientSecret     string   `json:"ClientSecret"`
	Scopes           []string `json:"Scopes"`
	UseUserInfo      bool     `json:"UseUserInfo"`
	Timeout          Duration `json:"Timeout"`
	MaxResponseBytes int64    `json:"MaxResponseBytes"`
}

// ExternalAuthenticationProvider is a discriminated provider definition.
// Exactly one protocol block must match Type. Adding a protocol introduces a
// new block and factory without changing the application login orchestration.
type ExternalAuthenticationProvider struct {
	ID            string               `json:"ID"`
	Type          string               `json:"Type"`
	DisplayName   string               `json:"DisplayName"`
	Enabled       bool                 `json:"Enabled"`
	AutoProvision bool                 `json:"AutoProvision"`
	Claims        ExternalClaimMapping `json:"Claims"`
	CAS           *CASProvider         `json:"CAS"`
	OIDC          *OIDCProvider        `json:"OIDC"`
}

type ExternalAuthentication struct {
	LoginStateTTL Duration                         `json:"LoginStateTTL"`
	Providers     []ExternalAuthenticationProvider `json:"Providers"`
}

func validateExternalAuthentication(
	external ExternalAuthentication,
	add func(string, string),
) {
	if external.LoginStateTTL.Duration < time.Minute ||
		external.LoginStateTTL.Duration > 30*time.Minute {
		add(
			"authentication.external.login_state_ttl",
			"must be between 1m and 30m",
		)
	}
	if len(external.Providers) > identityprovider.MaximumCount {
		add(
			"authentication.external.providers",
			fmt.Sprintf("must contain at most %d providers", identityprovider.MaximumCount),
		)
	}
	seen := make(map[string]struct{}, len(external.Providers))
	for index, provider := range external.Providers {
		prefix := fmt.Sprintf("authentication.external.providers[%d]", index)
		if !validConfigurationName(provider.ID, 64) {
			add(prefix+".id", "must contain 1-64 lowercase letters, numbers, dots, underscores, or hyphens")
		} else if _, exists := seen[provider.ID]; exists {
			add(prefix+".id", "must be unique")
		} else {
			seen[provider.ID] = struct{}{}
		}
		if len(provider.DisplayName) == 0 || len(provider.DisplayName) > 128 ||
			strings.ContainsAny(provider.DisplayName, "\x00\r\n") {
			add(prefix+".display_name", "must contain between 1 and 128 safe characters")
		}
		switch provider.Type {
		case ExternalAuthenticationTypeCAS:
			if provider.CAS == nil {
				add(prefix+".cas", "is required for a cas provider")
			} else {
				validateCASProvider(prefix+".cas", *provider.CAS, add)
			}
			if provider.OIDC != nil {
				add(prefix+".oidc", "must be omitted for a cas provider")
			}
		case ExternalAuthenticationTypeOIDC:
			if provider.OIDC == nil {
				add(prefix+".oidc", "is required for an oidc provider")
			} else {
				validateOIDCProvider(prefix+".oidc", *provider.OIDC, add)
			}
			if provider.CAS != nil {
				add(prefix+".cas", "must be omitted for an oidc provider")
			}
		default:
			add(prefix+".type", "must name a registered provider type")
		}
		validateExternalClaims(
			prefix+".claims",
			provider.Type,
			provider.Claims,
			provider.AutoProvision,
			add,
		)
	}
}

func validateCASProvider(
	prefix string,
	provider CASProvider,
	add func(string, string),
) {
	validateExternalHTTPSURL(prefix+".base_url", provider.BaseURL, add)
	parsed, _ := url.Parse(provider.BaseURL)
	if parsed != nil && (parsed.RawQuery != "" || parsed.Fragment != "") {
		add(prefix+".base_url", "must not contain a query or fragment")
	}
	if provider.ValidationPath == "" ||
		!strings.HasPrefix(provider.ValidationPath, "/") ||
		strings.HasPrefix(provider.ValidationPath, "//") ||
		strings.ContainsAny(provider.ValidationPath, "?#\x00\r\n") {
		add(prefix+".validation_path", "must be an absolute path without a query or fragment")
	}
	validateExternalHTTPBounds(
		prefix,
		provider.Timeout.Duration,
		provider.MaxResponseBytes,
		add,
	)
}

func validateOIDCProvider(
	prefix string,
	provider OIDCProvider,
	add func(string, string),
) {
	validateExternalHTTPSURL(prefix+".issuer", provider.Issuer, add)
	parsed, _ := url.Parse(provider.Issuer)
	if parsed != nil &&
		(parsed.RawQuery != "" || parsed.Fragment != "" ||
			(parsed.Path != "" && strings.HasSuffix(parsed.Path, "/"))) {
		add(prefix+".issuer", "must not contain a query, fragment, or trailing slash")
	}
	if provider.ClientID == "" || len(provider.ClientID) > 512 ||
		strings.ContainsAny(provider.ClientID, "\x00\r\n") {
		add(prefix+".client_id", "must contain between 1 and 512 safe characters")
	}
	if provider.ClientSecret == "" || len(provider.ClientSecret) > 4096 ||
		strings.ContainsAny(provider.ClientSecret, "\x00\r\n") {
		add(prefix+".client_secret", "must contain between 1 and 4096 safe characters")
	}
	seenScopes := make(map[string]struct{}, len(provider.Scopes))
	for index, scope := range provider.Scopes {
		field := fmt.Sprintf("%s.scopes[%d]", prefix, index)
		if scope == "" || len(scope) > 128 ||
			strings.ContainsAny(scope, "\x00\r\n \t") {
			add(field, "must contain between 1 and 128 safe characters")
			continue
		}
		if _, exists := seenScopes[scope]; exists {
			add(field, "must be unique")
		}
		seenScopes[scope] = struct{}{}
	}
	if _, exists := seenScopes["openid"]; !exists {
		add(prefix+".scopes", "must include openid")
	}
	validateExternalHTTPBounds(
		prefix,
		provider.Timeout.Duration,
		provider.MaxResponseBytes,
		add,
	)
}

func validateExternalHTTPBounds(
	prefix string,
	timeout time.Duration,
	maxResponseBytes int64,
	add func(string, string),
) {
	if timeout < time.Second || timeout > 30*time.Second {
		add(prefix+".timeout", "must be between 1s and 30s")
	}
	if maxResponseBytes < 1024 || maxResponseBytes > 2<<20 {
		add(prefix+".max_response_bytes", "must be between 1024 and 2097152")
	}
}

func validateExternalHTTPSURL(
	field string,
	value string,
	add func(string, string),
) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.User != nil ||
		(parsed.Scheme != "https" &&
			!(parsed.Scheme == "http" && isLoopbackHost(parsed.Hostname()))) {
		add(field, "must be an HTTPS URL, or loopback HTTP for development")
	}
}

func validateExternalClaims(
	prefix string,
	providerType string,
	claims ExternalClaimMapping,
	autoProvision bool,
	add func(string, string),
) {
	for _, claim := range []struct {
		field string
		value string
	}{
		{"subject", claims.Subject},
		{"username", claims.Username},
		{"email", claims.Email},
		{"email_verified_claim", claims.EmailVerifiedClaim},
		{"first_name", claims.FirstName},
		{"last_name", claims.LastName},
		{"display_name", claims.DisplayName},
		{"home_organization", claims.HomeOrganization},
		{"affiliation", claims.Affiliation},
		{"multi_factor_attribute", claims.MultiFactorAttribute},
	} {
		if claim.value != "" &&
			claim.value != "user" &&
			!validClaimName(claim.value) {
			add(
				prefix+"."+claim.field,
				"must be user or a valid released-attribute name",
			)
		}
	}
	if claims.Subject == "" {
		add(prefix+".subject", "is required")
	}
	if providerType == ExternalAuthenticationTypeOIDC && claims.Subject != "sub" {
		add(prefix+".subject", "must be sub for an oidc provider")
	}
	if autoProvision {
		if claims.Username == "" {
			add(prefix+".username", "is required when auto_provision is enabled")
		}
		if claims.Email == "" {
			add(prefix+".email", "is required when auto_provision is enabled")
		}
	}
	seenOrganizations := make(map[string]struct{}, len(claims.AllowedHomeOrganizations))
	for index, organization := range claims.AllowedHomeOrganizations {
		organization = strings.ToLower(strings.TrimSpace(organization))
		field := fmt.Sprintf("%s.allowed_home_organizations[%d]", prefix, index)
		if len(organization) == 0 || len(organization) > 255 ||
			strings.ContainsAny(organization, "\x00\r\n @/") {
			add(field, "must be a safe DNS-style organization identifier")
		} else if _, exists := seenOrganizations[organization]; exists {
			add(field, "must be unique")
		} else {
			seenOrganizations[organization] = struct{}{}
		}
	}
	if len(claims.AllowedHomeOrganizations) != 0 && claims.HomeOrganization == "" {
		add(prefix+".home_organization", "is required when allowed_home_organizations is configured")
	}
	seenMFAValues := make(map[string]struct{}, len(claims.MultiFactorValues))
	for index, value := range claims.MultiFactorValues {
		value = strings.TrimSpace(value)
		field := fmt.Sprintf("%s.multi_factor_values[%d]", prefix, index)
		if value == "" || len(value) > 256 || strings.ContainsAny(value, "\x00\r\n") {
			add(field, "must contain between 1 and 256 safe characters")
		} else if _, exists := seenMFAValues[value]; exists {
			add(field, "must be unique")
		} else {
			seenMFAValues[value] = struct{}{}
		}
	}
	if (claims.MultiFactorAttribute == "") != (len(claims.MultiFactorValues) == 0) {
		add(
			prefix+".multi_factor_values",
			"must be configured together with multi_factor_attribute",
		)
	}
}

func validConfigurationName(value string, maximum int) bool {
	if len(value) == 0 || len(value) > maximum {
		return false
	}
	for index, character := range value {
		if (character < 'a' || character > 'z') &&
			(character < '0' || character > '9') &&
			character != '.' && character != '_' && character != '-' {
			return false
		}
		if index == 0 && (character == '.' || character == '_' || character == '-') {
			return false
		}
	}
	return true
}

func validClaimName(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for index, character := range value {
		if (character < 'a' || character > 'z') &&
			(character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') &&
			character != '.' && character != '_' &&
			character != ':' && character != '-' {
			return false
		}
		if index == 0 &&
			((character >= '0' && character <= '9') ||
				character == '.' || character == ':' || character == '-') {
			return false
		}
	}
	return true
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}
