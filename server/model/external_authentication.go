// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"errors"
	"net/url"
	"strings"
	"unicode/utf8"
)

const (
	ExternalLoginReturnToMaxLength = 2048
	ExternalHomeOrgMaxLength       = 255
	ExternalCallbackMaxFields      = 32
	ExternalCallbackMaxValues      = 8
	ExternalCallbackMaxKeyLength   = 128
	ExternalCallbackMaxValueLength = 8192
)

var ErrInvalidExternalAuthenticationCallback = errors.New(
	"external authentication callback is invalid",
)

// ExternalAuthenticationProvider is the safe public description of one
// operator-configured identity provider. Protocol endpoints and claim mapping
// remain deployment configuration and are never exposed here.
type ExternalAuthenticationProvider struct {
	Id          string `json:"id"`
	DisplayName string `json:"display_name"`
	Type        string `json:"type"`
}

// ExternalAuthenticationStart carries the provider redirect plus the raw
// browser-binding value that the HTTP boundary places in an HttpOnly cookie.
// The binding is never serialized or persisted in recoverable form.
type ExternalAuthenticationStart struct {
	RedirectURL string `json:"redirect_url"`
	Binding     string `json:"-"`
	ExpiresAt   int64  `json:"expires_at"`
}

// ExternalAuthenticationCompletion is returned internally after a successful
// provider callback. Tokens follow the ordinary one-time session contract.
type ExternalAuthenticationCompletion struct {
	User     *User
	Session  *Session
	Tokens   *AuthenticationTokens
	ReturnTo string
}

// ExternalAuthenticationAssertion is the protocol-neutral result produced by
// a trusted provider adapter. Subject is opaque and must only be persisted in
// ExternalIdentity; it must never be returned, logged, or audited.
type ExternalAuthenticationAssertion struct {
	ProviderId             string
	Subject                string
	Username               string
	Email                  string
	EmailVerified          bool
	DisplayName            string
	FirstName              string
	LastName               string
	HomeOrganization       string
	Affiliations           []string
	AuthenticationStrength AuthenticationStrength
	AuthenticatedAt        int64
}

// ExternalAuthenticationCallback is a bounded, transport-neutral view of the
// values returned by an identity provider. Protocol adapters, not the API or
// application orchestration, own the meaning of ticket, code, error, and
// future protocol-specific fields.
type ExternalAuthenticationCallback struct {
	Values map[string][]string
}

func (c ExternalAuthenticationCallback) SingleValue(
	name string,
	maximumLength int,
) (string, error) {
	values := c.Values[name]
	if len(values) != 1 || values[0] == "" ||
		len(values[0]) > maximumLength ||
		strings.ContainsAny(values[0], "\x00\r\n") {
		return "", ErrInvalidExternalAuthenticationCallback
	}
	return values[0], nil
}

func (c ExternalAuthenticationCallback) OptionalSingleValue(
	name string,
	maximumLength int,
) (string, error) {
	values, exists := c.Values[name]
	if !exists {
		return "", nil
	}
	if len(values) != 1 || len(values[0]) > maximumLength ||
		strings.ContainsAny(values[0], "\x00\r\n") {
		return "", ErrInvalidExternalAuthenticationCallback
	}
	return values[0], nil
}

// ExternalLoginState binds one provider redirect to the browser that initiated
// it. Only token hashes are durable. State is consumed exactly once before the
// one-time provider credential is validated, closing concurrent replay races.
type ExternalLoginState struct {
	Id          string            `json:"id"`
	CreateAt    int64             `json:"create_at"`
	UpdateAt    int64             `json:"update_at"`
	Provider    string            `json:"provider"`
	StateHash   string            `json:"-"`
	BindingHash string            `json:"-"`
	ReturnTo    string            `json:"return_to"`
	ClientType  SessionClientType `json:"client_type"`
	DeviceId    string            `json:"device_id,omitempty"`
	DeviceName  string            `json:"device_name,omitempty"`
	ExpiresAt   int64             `json:"expires_at"`
	ConsumedAt  int64             `json:"consumed_at,omitempty"`
}

func (s *ExternalLoginState) PreSave() {
	preSave(&s.Id, &s.CreateAt, &s.UpdateAt)
	s.Provider = strings.ToLower(SanitizeUnicode(s.Provider))
	s.ReturnTo = strings.TrimSpace(SanitizeUnicode(s.ReturnTo))
	s.DeviceId = SanitizeUnicode(s.DeviceId)
	s.DeviceName = SanitizeUnicode(s.DeviceName)
}

func (s *ExternalLoginState) IsValid() *AppError {
	const where = "ExternalLoginState.IsValid"
	if appErr := validatePersistentFields(
		where,
		"external_login_state",
		s.Id,
		s.CreateAt,
		s.UpdateAt,
	); appErr != nil {
		return appErr
	}
	details := "id=" + s.Id
	if len(s.Provider) == 0 ||
		len(s.Provider) > IdentityProviderMaxLength ||
		!validIdentityProvider.MatchString(s.Provider) {
		return invalidModelError(
			where,
			"external_login_state",
			"provider",
			"has an invalid format",
			details,
		)
	}
	if !IsValidTokenHash(s.StateHash) || !IsValidTokenHash(s.BindingHash) {
		return invalidModelError(
			where,
			"external_login_state",
			"token_hash",
			"must contain valid credential hashes",
			details,
		)
	}
	if !IsSafeRelativeURL(s.ReturnTo) {
		return invalidModelError(
			where,
			"external_login_state",
			"return_to",
			"must be a safe relative URL",
			details,
		)
	}
	if !s.ClientType.IsValid() || s.ClientType == SessionClientCLI {
		return invalidModelError(
			where,
			"external_login_state",
			"client_type",
			"must be desktop or web",
			details,
		)
	}
	if len(s.DeviceId) > SessionDeviceIdMaxLength ||
		utf8.RuneCountInString(s.DeviceName) > SessionDeviceNameMaxRunes {
		return invalidModelError(
			where,
			"external_login_state",
			"device",
			"exceeds the configured model bounds",
			details,
		)
	}
	if s.ExpiresAt <= s.CreateAt {
		return invalidModelError(
			where,
			"external_login_state",
			"expires_at",
			"must follow create_at",
			details,
		)
	}
	if s.ConsumedAt != 0 &&
		(s.ConsumedAt < s.CreateAt || s.ConsumedAt >= s.ExpiresAt) {
		return invalidModelError(
			where,
			"external_login_state",
			"consumed_at",
			"must fall within the active lifetime",
			details,
		)
	}
	return nil
}

func (s *ExternalLoginState) Auditable() map[string]any {
	fields := auditFields(s.Id, s.CreateAt, s.UpdateAt, 0)
	fields["provider"] = s.Provider
	fields["client_type"] = s.ClientType
	fields["expires_at"] = s.ExpiresAt
	fields["consumed_at"] = s.ConsumedAt
	return fields
}

// IsSafeRelativeURL accepts a local absolute-path reference without authority,
// backslashes, fragments, or control characters.
func IsSafeRelativeURL(value string) bool {
	if value == "" || len(value) > ExternalLoginReturnToMaxLength ||
		!strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") ||
		strings.ContainsAny(value, "\\#\x00\r\n") {
		return false
	}
	parsed, err := url.ParseRequestURI(value)
	return err == nil &&
		parsed.IsAbs() == false &&
		parsed.Host == "" &&
		parsed.Fragment == ""
}

var _ Auditable = (*ExternalLoginState)(nil)
