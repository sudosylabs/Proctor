// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package config

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestExternalAuthenticationProviderCountIsBounded(t *testing.T) {
	cfg := Default()
	for index := range 64 {
		cfg.Authentication.External.Providers = append(
			cfg.Authentication.External.Providers,
			validTestCASProvider(fmt.Sprintf("provider-%02d", index)),
		)
	}
	require.NoError(t, cfg.Validate())

	tooMany := cfg.Clone()
	tooMany.Authentication.External.Providers = append(
		tooMany.Authentication.External.Providers,
		validTestCASProvider("provider-64"),
	)
	require.Len(t, cfg.Authentication.External.Providers, 64, "Clone() exposed the provider list")
	err := tooMany.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "authentication.external.providers: must contain at most 64 providers")
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
	require.NoError(t, cfg.Validate())
	cloned := cfg.Clone()
	cloned.Authentication.External.Providers[0].
		Claims.AllowedHomeOrganizations[0] = "mutated.example.edu"
	require.Equal(t, "example.edu", cfg.Authentication.External.Providers[0].
		Claims.AllowedHomeOrganizations[0], "Clone() exposed nested provider configuration")

	cfg.Authentication.External.Providers[0].CAS.BaseURL = "http://cas.example.edu"
	cfg.Authentication.External.Providers[0].Claims.Email = ""
	err := cfg.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "authentication.external.providers[0].cas.base_url")
	require.Contains(t, err.Error(), "authentication.external.providers[0].claims.email")
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
	require.NoError(t, cfg.Validate())
	cloned := cfg.Clone()
	cloned.Authentication.External.Providers[0].OIDC.Scopes[0] = "mutated"
	require.Equal(t, "openid", cfg.Authentication.External.Providers[0].OIDC.Scopes[0], "Clone() exposed OIDC scopes")
	redacted := cfg.Redacted()
	require.Equal(t, "[redacted]", redacted.Authentication.External.Providers[0].OIDC.ClientSecret, "Redacted() exposed OIDC client secret")

	cfg.Authentication.External.Providers[0].Claims.Subject = "uid"
	cfg.Authentication.External.Providers[0].OIDC.Scopes = []string{"profile"}
	err := cfg.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "authentication.external.providers[0].claims.subject")
	require.Contains(t, err.Error(), "authentication.external.providers[0].oidc.scopes")
}
