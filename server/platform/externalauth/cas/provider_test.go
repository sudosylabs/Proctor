// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package cas

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

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
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.URL.Path != "/cas/p3/serviceValidate" ||
			request.URL.Query().Get("service") != serviceURL ||
			request.URL.Query().Get("ticket") != ticket {
			t.Errorf("validation request = %s", request.URL.String())
		}
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
	if err != nil {
		t.Fatal(err)
	}
	provider := created.(*Provider)
	challenge, err := provider.Begin(
		context.Background(),
		externalauth.BeginRequest{
			CallbackURL: callbackURL, State: state, Proof: proof,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(challenge.RedirectURL)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Path != "/cas/login" ||
		parsed.Query().Get("service") != serviceURL {
		t.Fatalf("Begin() = %q", challenge.RedirectURL)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	if assertion.ProviderId != "campus-cas" ||
		assertion.Subject != "opaque-subject" ||
		assertion.Username != "student.name" ||
		assertion.Email != "student.name@example.edu" ||
		!assertion.EmailVerified ||
		assertion.AuthenticationStrength != model.AuthenticationMultiFactor ||
		len(assertion.Affiliations) != 2 {
		t.Fatalf("Complete() = %#v", assertion)
	}
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
			if err != nil {
				t.Fatal(err)
			}
			provider := created.(*Provider)
			state := model.NewCredentialToken()
			if _, err := provider.Complete(
				context.Background(),
				completeRequest(state, "ST-ticket"),
			); !errors.Is(err, test.wantError) {
				t.Fatalf("Complete() error = %v", err)
			}
		})
	}
}

func TestProviderRedactsTicketFromTransportErrors(t *testing.T) {
	const ticket = "ST-must-not-appear"
	created, err := NewFactory().New(testSettings("http://127.0.0.1:1"))
	if err != nil {
		t.Fatal(err)
	}
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
	if err == nil || strings.Contains(err.Error(), ticket) {
		t.Fatalf("Complete() error leaked ticket: %v", err)
	}
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
