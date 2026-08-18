// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"errors"
	"net/url"
	"strings"
	"time"
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

type ExternalAuthenticationPurpose string

const (
	ExternalAuthenticationPurposeLogin   ExternalAuthenticationPurpose = "login"
	ExternalAuthenticationPurposeConnect ExternalAuthenticationPurpose = "connect"
)

func (p ExternalAuthenticationPurpose) IsValid() bool {
	return p == ExternalAuthenticationPurposeLogin || p == ExternalAuthenticationPurposeConnect
}

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
//
// StateHash and BindingHash are deliberately excluded from JSON.
type ExternalLoginState struct {
	ID           ExternalLoginStateID
	CreatedAt    time.Time
	UpdatedAt    time.Time
	Provider     string
	Purpose      ExternalAuthenticationPurpose
	TargetUserID UserID
	AuditEventID string
	StateHash    string `json:"-"`
	BindingHash  string `json:"-"`
	ReturnTo     string
	ClientType   SessionClientType
	DeviceID     string
	DeviceName   string
	ExpiresAt    time.Time
	ConsumedAt   OptionalTime
}

// PrepareCreate applies application-owned lifecycle fields before validation.
func (s *ExternalLoginState) PrepareCreate(id ExternalLoginStateID, at time.Time) {
	if s == nil {
		return
	}
	s.ID = id
	at = TimeUTC(at)
	s.CreatedAt = at
	s.UpdatedAt = at
	s.Provider = strings.ToLower(SanitizeUnicode(s.Provider))
	if s.Purpose == "" {
		s.Purpose = ExternalAuthenticationPurposeLogin
	}
	s.ReturnTo = strings.TrimSpace(SanitizeUnicode(s.ReturnTo))
	s.DeviceID = SanitizeUnicode(s.DeviceID)
	s.DeviceName = SanitizeUnicode(s.DeviceName)
	s.ExpiresAt = TimeUTC(s.ExpiresAt)
}

// Validate checks rehydrated external-login-state state.
func (s *ExternalLoginState) Validate() error {
	const where = "ExternalLoginState.Validate"
	if s == nil {
		return invalidModelError(where, "external_login_state", "value", "is required", "")
	}
	if !s.ID.IsValid() {
		return invalidModelError(where, "external_login_state", "id", "must be a valid identifier", "")
	}
	details := "id=" + s.ID.String()
	if s.CreatedAt.IsZero() || s.UpdatedAt.IsZero() {
		return invalidModelError(where, "external_login_state", "created_at", "must be set", details)
	}
	if s.UpdatedAt.Before(s.CreatedAt) {
		return invalidModelError(where, "external_login_state", "updated_at", "must not precede created_at", details)
	}
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
	if !s.Purpose.IsValid() ||
		(s.Purpose == ExternalAuthenticationPurposeLogin && (!s.TargetUserID.IsZero() || s.AuditEventID != "")) ||
		(s.Purpose == ExternalAuthenticationPurposeConnect && (!s.TargetUserID.IsValid() || !IsValidId(s.AuditEventID))) {
		return invalidModelError(where, "external_login_state", "purpose", "has an invalid target", details)
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
	if len(s.DeviceID) > SessionDeviceIdMaxLength ||
		utf8.RuneCountInString(s.DeviceName) > SessionDeviceNameMaxRunes {
		return invalidModelError(
			where,
			"external_login_state",
			"device",
			"exceeds the configured model bounds",
			details,
		)
	}
	if s.ExpiresAt.IsZero() || !s.ExpiresAt.After(s.CreatedAt) {
		return invalidModelError(
			where,
			"external_login_state",
			"expires_at",
			"must follow create_at",
			details,
		)
	}
	if s.ConsumedAt.Valid &&
		(s.ConsumedAt.Time.Before(s.CreatedAt) || !s.ConsumedAt.Time.Before(s.ExpiresAt)) {
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

// Auditable returns a deliberately safe audit projection. Hashes are never
// included.
func (s *ExternalLoginState) Auditable() map[string]any {
	if s == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":             s.ID.String(),
		"created_at":     MillisFromTime(s.CreatedAt),
		"updated_at":     MillisFromTime(s.UpdatedAt),
		"provider":       s.Provider,
		"purpose":        s.Purpose,
		"target_user_id": s.TargetUserID.String(),
		"client_type":    s.ClientType,
		"expires_at":     MillisFromTime(s.ExpiresAt),
		"consumed_at":    s.ConsumedAt.Millis(),
	}
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
