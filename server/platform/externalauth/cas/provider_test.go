// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package cas

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/platform/externalauth"
)

func TestProviderLoginAndAuthentication(t *testing.T) {
	const callbackURL = "https://proctor.example.edu/api/v1/auth/providers/campus-cas/callback"
	const ticket = "ST-opaque-ticket"
	state := model.NewCredentialToken()
	proof := model.NewCredentialToken()
	serviceURL, err := casServiceURL(callbackURL, state)
	require.NoError(t, err)
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		assert.Equal(t, "/cas/p3/serviceValidate", request.URL.Path)
		assert.Equal(t, serviceURL, request.URL.Query().Get("service"))
		assert.Equal(t, ticket, request.URL.Query().Get("ticket"))
		writer.Header().Set("Content-Type", "application/xml")
		_, _ = writer.Write([]byte(`<?xml version="1.0"?>
<cas:serviceResponse xmlns:cas="http://www.yale.edu/tp/cas">
  <cas:authenticationSuccess>
    <cas:user>opaque-subject</cas:user>
    <cas:attributes>
      <cas:uid>student.name</cas:uid>
      <cas:mail>student.name@example.edu</cas:mail>
      <cas:givenName>Student</cas:givenName>
      <cas:sn>Name</cas:sn>
      <cas:schacHomeOrganization>example.edu</cas:schacHomeOrganization>
      <cas:eduPersonAffiliation>student</cas:eduPersonAffiliation>
      <cas:eduPersonAffiliation>member</cas:eduPersonAffiliation>
      <cas:authnContext>mfa</cas:authnContext>
    </cas:attributes>
  </cas:authenticationSuccess>
</cas:serviceResponse>`))
	}))
	defer server.Close()

	created, err := NewFactory().New(testSettings(server.URL + "/cas"))
	require.NoError(t, err)
	provider := created.(*Provider)
	challenge, err := provider.Begin(
		context.Background(),
		externalauth.BeginRequest{
			CallbackURL: callbackURL, State: state, Proof: proof,
		},
	)
	require.NoError(t, err)
	parsed, err := url.Parse(challenge.RedirectURL)
	require.NoError(t, err)
	require.Equal(t, "/cas/login", parsed.Path)
	require.Equal(t, serviceURL, parsed.Query().Get("service"))
	assertion, err := provider.Complete(
		context.Background(),
		externalauth.CompleteRequest{
			CallbackURL: callbackURL, State: state, Proof: proof,
			Callback: model.ExternalAuthenticationCallback{
				Values: map[string][]string{
					"state":  {state},
					"ticket": {ticket},
				},
			},
		},
	)
	require.NoError(t, err)
	require.Equal(t, "campus-cas", assertion.ProviderID)
	require.Equal(t, "opaque-subject", assertion.Subject)
	require.Equal(t, "student.name", assertion.Username)
	require.Equal(t, "student.name@example.edu", assertion.Email)
	require.True(t, assertion.EmailVerified)
	require.Equal(t, model.AuthenticationMultiFactor, assertion.AuthenticationStrength)
	require.Len(t, assertion.Affiliations, 2)
}

func TestProviderRejectsFailureAndUnsafeXML(t *testing.T) {
	tests := []struct {
		name      string
		response  string
		wantError error
	}{
		{
			name: "CAS failure",
			response: `<cas:serviceResponse xmlns:cas="urn:cas">
			  <cas:authenticationFailure code="INVALID_TICKET">rejected</cas:authenticationFailure>
			</cas:serviceResponse>`,
			wantError: externalauth.ErrAuthenticationRejected,
		},
		{
			name:      "document type",
			response:  `<!DOCTYPE serviceResponse><serviceResponse/>`,
			wantError: externalauth.ErrInvalidResponse,
		},
		{
			name:      "missing success",
			response:  `<cas:serviceResponse xmlns:cas="urn:cas"/>`,
			wantError: externalauth.ErrInvalidResponse,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(
				writer http.ResponseWriter,
				_ *http.Request,
			) {
				_, _ = writer.Write([]byte(test.response))
			}))
			defer server.Close()
			created, err := NewFactory().New(testSettings(server.URL))
			require.NoError(t, err)
			provider := created.(*Provider)
			state := model.NewCredentialToken()
			_, err = provider.Complete(
				context.Background(),
				completeRequest(state, "ST-ticket"),
			)
			require.ErrorIs(t, err, test.wantError)
		})
	}
}

func TestProviderRedactsTicketFromTransportErrors(t *testing.T) {
	const ticket = "ST-must-not-appear"
	created, err := NewFactory().New(testSettings("http://127.0.0.1:1"))
	require.NoError(t, err)
	provider := created.(*Provider)
	cause := errors.New("transport included " + ticket)
	provider.client.Transport = roundTripperFunc(func(
		request *http.Request,
	) (*http.Response, error) {
		return nil, errors.New(cause.Error() + " " + request.URL.String())
	})
	state := model.NewCredentialToken()
	_, err = provider.Complete(
		context.Background(),
		completeRequest(state, ticket),
	)
	require.Error(t, err)
	require.NotContains(t, err.Error(), ticket)
}

func completeRequest(
	state string,
	ticket string,
) externalauth.CompleteRequest {
	return externalauth.CompleteRequest{
		CallbackURL: "https://proctor.example.edu/callback",
		State:       state,
		Proof:       model.NewCredentialToken(),
		Callback: model.ExternalAuthenticationCallback{
			Values: map[string][]string{
				"state":  {state},
				"ticket": {ticket},
			},
		},
	}
}

func testSettings(
	baseURL string,
) config.ExternalAuthenticationProvider {
	return config.ExternalAuthenticationProvider{
		ID: "campus-cas", Type: config.ExternalAuthenticationTypeCAS,
		DisplayName: "Campus CAS", Enabled: true, AutoProvision: true,
		CAS: &config.CASProvider{
			BaseURL: baseURL, ValidationPath: "/p3/serviceValidate",
			Timeout:          config.Duration{Duration: 5 * time.Second},
			MaxResponseBytes: 64 * 1024,
		},
		Claims: config.ExternalClaimMapping{
			Subject: "user", Username: "uid", Email: "mail",
			FirstName: "givenName", LastName: "sn",
			HomeOrganization:         "schacHomeOrganization",
			Affiliation:              "eduPersonAffiliation",
			AllowedHomeOrganizations: []string{"example.edu"},
			TrustEmail:               true,
			MultiFactorAttribute:     "authnContext",
			MultiFactorValues:        []string{"mfa"},
		},
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	return f(request)
}

func TestProviderFreshProofRenewsAtLoginAndValidation(t *testing.T) {
	for _, reject := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh ticket", true: "SSO ticket rejected"}[reject], func(t *testing.T) {
			validated := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				validated = true
				assert.Equal(t, "true", r.URL.Query().Get("renew"), "ticket validation did not require primary credentials")
				w.Header().Set("Content-Type", "application/xml")
				body := `<cas:serviceResponse xmlns:cas="urn:cas"><cas:authenticationSuccess><cas:user>same-user</cas:user></cas:authenticationSuccess></cas:serviceResponse>`
				if reject {
					body = `<cas:serviceResponse xmlns:cas="urn:cas"><cas:authenticationFailure code="INVALID_TICKET">renew required</cas:authenticationFailure></cas:serviceResponse>`
				}
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			settings := testSettings(server.URL)
			settings.AutoProvision = false
			settings.Claims.AllowedHomeOrganizations = nil
			created, err := NewFactory().New(settings)
			require.NoError(t, err)
			provider := created.(*Provider)
			now := time.Now()
			provider.now = func() time.Time { return now }
			state := model.NewCredentialToken()
			request := completeRequest(state, "ST-fresh")
			request.AuthenticationStartedAt = now.Add(-time.Second)
			start, err := provider.Begin(context.Background(), externalauth.BeginRequest{CallbackURL: request.CallbackURL, State: state, Proof: request.Proof, FreshAuthentication: true})
			require.NoError(t, err)
			u, _ := url.Parse(start.RedirectURL)
			require.Equal(t, "true", u.Query().Get("renew"), "CAS login did not request new primary proof")
			assertion, err := provider.Complete(context.Background(), request)
			require.True(t, validated, "ticket was not validated")
			if reject {
				require.ErrorIs(t, err, externalauth.ErrAuthenticationRejected)
				require.Nil(t, assertion)
			} else {
				require.NoError(t, err, "fresh CAS proof")
				require.Equal(t, request.AuthenticationStartedAt.UnixMilli(), assertion.AuthenticatedAt, "fresh CAS proof")
			}
		})
	}
}
