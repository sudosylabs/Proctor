// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package cas

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/platform/externalauth"
)

type Factory struct{}

func NewFactory() Factory {
	return Factory{}
}

func (Factory) Type() string {
	return config.ExternalAuthenticationTypeCAS
}

func (Factory) New(
	settings config.ExternalAuthenticationProvider,
) (externalauth.Provider, error) {
	if settings.CAS == nil {
		return nil, errors.New("CAS settings are missing")
	}
	base, err := url.Parse(settings.CAS.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse base URL: %w", err)
	}
	loginURL := *base
	loginURL.Path = strings.TrimSuffix(base.Path, "/") + "/login"
	validateURL := *base
	validateURL.Path = strings.TrimSuffix(base.Path, "/") +
		settings.CAS.ValidationPath
	return &Provider{
		mapper:      externalauth.NewProfileMapper(settings),
		loginURL:    &loginURL,
		validateURL: &validateURL,
		client: externalauth.NewHTTPClient(
			settings.CAS.Timeout.Duration,
			settings.CAS.MaxResponseBytes,
		),
		maximumResponseBytes: settings.CAS.MaxResponseBytes,
		now:                  time.Now,
	}, nil
}

type Provider struct {
	mapper               *externalauth.ProfileMapper
	loginURL             *url.URL
	validateURL          *url.URL
	client               *http.Client
	maximumResponseBytes int64
	now                  func() time.Time
}

func (p *Provider) Descriptor() model.ExternalAuthenticationProvider {
	return p.mapper.Descriptor()
}

func (p *Provider) AutoProvision() bool {
	return p.mapper.AutoProvision()
}

func (p *Provider) State(
	callback model.ExternalAuthenticationCallback,
) (string, error) {
	state, err := callback.SingleValue("state", 256)
	if err != nil || !model.IsValidCredentialToken(state) {
		return "", externalauth.Rejected(
			"resolve CAS callback state",
			model.ErrInvalidExternalAuthenticationCallback,
		)
	}
	return state, nil
}

func (p *Provider) Begin(
	_ context.Context,
	request externalauth.BeginRequest,
) (*externalauth.BeginResponse, error) {
	serviceURL, err := casServiceURL(request.CallbackURL, request.State)
	if err != nil || !model.IsValidCredentialToken(request.Proof) {
		return nil, externalauth.InvalidResponse(
			"construct CAS login",
			errors.New("invalid external authentication transaction"),
		)
	}
	loginURL := *p.loginURL
	query := loginURL.Query()
	query.Set("service", serviceURL)
	loginURL.RawQuery = query.Encode()
	return &externalauth.BeginResponse{RedirectURL: loginURL.String()}, nil
}

func (p *Provider) Complete(
	ctx context.Context,
	request externalauth.CompleteRequest,
) (*model.ExternalAuthenticationAssertion, error) {
	callbackState, err := p.State(request.Callback)
	if err != nil ||
		subtle.ConstantTimeCompare(
			[]byte(callbackState),
			[]byte(request.State),
		) != 1 {
		return nil, externalauth.Rejected(
			"validate CAS callback",
			model.ErrInvalidExternalAuthenticationCallback,
		)
	}
	ticket, err := request.Callback.SingleValue("ticket", 2048)
	if err != nil {
		return nil, externalauth.Rejected(
			"validate CAS callback",
			err,
		)
	}
	serviceURL, err := casServiceURL(request.CallbackURL, request.State)
	if err != nil {
		return nil, externalauth.InvalidResponse(
			"construct CAS validation",
			err,
		)
	}
	validationURL := *p.validateURL
	query := validationURL.Query()
	query.Set("service", serviceURL)
	query.Set("ticket", ticket)
	validationURL.RawQuery = query.Encode()

	httpRequest, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		validationURL.String(),
		nil,
	)
	if err != nil {
		return nil, externalauth.InvalidResponse(
			"construct CAS validation",
			err,
		)
	}
	httpRequest.Header.Set("Accept", "application/xml, text/xml")
	response, err := p.client.Do(httpRequest)
	if err != nil {
		return nil, externalauth.Unavailable(
			"validate CAS ticket",
			err,
		)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, response.Body)
		return nil, externalauth.Unavailable(
			"validate CAS ticket",
			fmt.Errorf("provider status %d", response.StatusCode),
		)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, externalauth.InvalidResponse(
			"read CAS validation",
			err,
		)
	}
	if int64(len(body)) > p.maximumResponseBytes {
		return nil, externalauth.InvalidResponse(
			"read CAS validation",
			externalauth.ErrResponseTooLarge,
		)
	}
	if bytes.Contains(bytes.ToUpper(body), []byte("<!DOCTYPE")) {
		return nil, externalauth.InvalidResponse(
			"decode CAS validation",
			errors.New("document type is not allowed"),
		)
	}
	var envelope serviceResponse
	decoder := xml.NewDecoder(bytes.NewReader(body))
	decoder.Strict = true
	if err := decoder.Decode(&envelope); err != nil {
		return nil, externalauth.InvalidResponse(
			"decode CAS validation",
			err,
		)
	}
	if envelope.Failure != nil {
		return nil, externalauth.Rejected(
			"validate CAS ticket",
			errors.New("CAS rejected authentication"),
		)
	}
	if envelope.Success == nil {
		return nil, externalauth.InvalidResponse(
			"decode CAS validation",
			errors.New("missing authentication result"),
		)
	}
	values := make(
		map[string][]string,
		len(envelope.Success.Attributes.Values)+1,
	)
	values["user"] = []string{strings.TrimSpace(envelope.Success.User)}
	for _, attribute := range envelope.Success.Attributes.Values {
		name := attribute.XMLName.Local
		value := strings.TrimSpace(attribute.Value)
		if name == "" || value == "" || len(value) > 4096 ||
			len(values) >= 256 {
			continue
		}
		values[name] = append(values[name], value)
	}
	emailVerified := p.mapperEmailVerified(values)
	return p.mapper.Assertion(
		values,
		emailVerified,
		p.now().UnixMilli(),
	)
}

func (p *Provider) mapperEmailVerified(values map[string][]string) bool {
	// CAS does not define a standard email-verification assertion. An operator
	// may explicitly trust released email data or map a boolean-like attribute.
	claims := p.mapperClaims()
	if claims.TrustEmail {
		return true
	}
	if claims.EmailVerifiedClaim == "" {
		return false
	}
	for _, value := range values[claims.EmailVerifiedClaim] {
		if strings.EqualFold(strings.TrimSpace(value), "true") {
			return true
		}
	}
	return false
}

func (p *Provider) mapperClaims() config.ExternalClaimMapping {
	return p.mapper.Claims()
}

func casServiceURL(callbackURL string, state string) (string, error) {
	if !model.IsValidCredentialToken(state) {
		return "", errors.New("CAS state is invalid")
	}
	parsed, err := url.Parse(callbackURL)
	if err != nil || parsed.Host == "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.User != nil || parsed.Fragment != "" {
		return "", errors.New("CAS callback URL is invalid")
	}
	query := parsed.Query()
	query.Set("state", state)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

type serviceResponse struct {
	Success *authenticationSuccess `xml:"authenticationSuccess"`
	Failure *authenticationFailure `xml:"authenticationFailure"`
}

type authenticationSuccess struct {
	User       string     `xml:"user"`
	Attributes attributes `xml:"attributes"`
}

type authenticationFailure struct {
	Code string `xml:"code,attr"`
}

type attributes struct {
	Values []attribute `xml:",any"`
}

type attribute struct {
	XMLName xml.Name
	Value   string `xml:",chardata"`
}

var (
	_ externalauth.Factory  = Factory{}
	_ externalauth.Provider = (*Provider)(nil)
)
