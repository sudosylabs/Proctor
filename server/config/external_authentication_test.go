// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package config

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestExternalAuthenticationProviderCountIsBounded(t *testing.T) {
	cfg := Default()
	for index := range 64 {
		cfg.Authentication.External.Providers = append(
			cfg.Authentication.External.Providers,
			validTestCASProvider(fmt.Sprintf("provider-%02d", index)),
		)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("64 external providers: %v", err)
	}

	tooMany := cfg.Clone()
	tooMany.Authentication.External.Providers = append(
		tooMany.Authentication.External.Providers,
		validTestCASProvider("provider-64"),
	)
	if len(cfg.Authentication.External.Providers) != 64 {
		t.Fatal("Clone() exposed the provider list")
	}
	err := tooMany.Validate()
	if err == nil || !strings.Contains(
		err.Error(),
		"authentication.external.providers: must contain at most 64 providers",
	) {
		t.Fatalf("65 external providers error = %v", err)
	}
}

func validTestCASProvider(id string) ExternalAuthenticationProvider {
	return ExternalAuthenticationProvider{
		ID: id, Type: ExternalAuthenticationTypeCAS, DisplayName: id,
		Enabled: true,
		CAS: &CASProvider{
			BaseURL: "http://127.0.0.1:8080/cas", ValidationPath: "/p3/serviceValidate",
			Timeout: Duration{Duration: 5 * time.Second}, MaxResponseBytes: 64 * 1024,
		},
		Claims: ExternalClaimMapping{Subject: "user", Username: "uid", Email: "mail"},
	}
}

func TestExternalAuthenticationConfiguration(t *testing.T) {
	cfg := Default()
	cfg.Authentication.External.Providers = []ExternalAuthenticationProvider{{
		ID: "campus-cas", Type: "cas", DisplayName: "Campus CAS",
		Enabled: true, AutoProvision: true,
		CAS: &CASProvider{
			BaseURL:          "http://127.0.0.1:8080/cas",
			ValidationPath:   "/p3/serviceValidate",
			Timeout:          Duration{Duration: 5 * time.Second},
			MaxResponseBytes: 64 * 1024,
		},
		Claims: ExternalClaimMapping{
			Subject: "user", Username: "uid", Email: "mail",
			TrustEmail:               true,
			HomeOrganization:         "schacHomeOrganization",
			AllowedHomeOrganizations: []string{"example.edu"},
		},
	}}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid CAS configuration: %v", err)
	}
	cloned := cfg.Clone()
	cloned.Authentication.External.Providers[0].
		Claims.AllowedHomeOrganizations[0] = "mutated.example.edu"
	if cfg.Authentication.External.Providers[0].
		Claims.AllowedHomeOrganizations[0] != "example.edu" {
		t.Fatal("Clone() exposed nested provider configuration")
	}

	cfg.Authentication.External.Providers[0].CAS.BaseURL = "http://cas.example.edu"
	cfg.Authentication.External.Providers[0].Claims.Email = ""
	err := cfg.Validate()
	if err == nil ||
		!strings.Contains(err.Error(), "authentication.external.providers[0].cas.base_url") ||
		!strings.Contains(err.Error(), "authentication.external.providers[0].claims.email") {
		t.Fatalf("invalid CAS configuration error = %v", err)
	}
}

func TestOIDCExternalAuthenticationConfigurationAndRedaction(
	t *testing.T,
) {
	cfg := Default()
	cfg.Authentication.External.Providers = []ExternalAuthenticationProvider{{
		ID: "campus-oidc", Type: ExternalAuthenticationTypeOIDC,
		DisplayName: "Campus OIDC", Enabled: true, AutoProvision: true,
		OIDC: &OIDCProvider{
			Issuer:   "https://sso.example.edu/cas/oidc",
			ClientID: "proctor", ClientSecret: "sensitive-secret",
			Scopes:           []string{"openid", "profile", "email"},
			Timeout:          Duration{Duration: 5 * time.Second},
			MaxResponseBytes: 64 * 1024,
		},
		Claims: ExternalClaimMapping{
			Subject: "sub", Username: "preferred_username",
			Email: "email", EmailVerifiedClaim: "email_verified",
		},
	}}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid OIDC configuration: %v", err)
	}
	cloned := cfg.Clone()
	cloned.Authentication.External.Providers[0].OIDC.Scopes[0] = "mutated"
	if cfg.Authentication.External.Providers[0].OIDC.Scopes[0] != "openid" {
		t.Fatal("Clone() exposed OIDC scopes")
	}
	redacted := cfg.Redacted()
	if redacted.Authentication.External.Providers[0].OIDC.ClientSecret !=
		"[redacted]" {
		t.Fatal("Redacted() exposed OIDC client secret")
	}

	cfg.Authentication.External.Providers[0].Claims.Subject = "uid"
	cfg.Authentication.External.Providers[0].OIDC.Scopes = []string{"profile"}
	err := cfg.Validate()
	if err == nil ||
		!strings.Contains(
			err.Error(),
			"authentication.external.providers[0].claims.subject",
		) ||
		!strings.Contains(
			err.Error(),
			"authentication.external.providers[0].oidc.scopes",
		) {
		t.Fatalf("invalid OIDC configuration error = %v", err)
	}
}
