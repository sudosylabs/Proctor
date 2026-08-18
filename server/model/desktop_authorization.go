// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrInvalidDesktopAuthorizationCallback = errors.New("desktop authorization callback is invalid")

const pkceUnreserved = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~"

const (
	BrowserAuthenticationTransactionLifetime = 5 * time.Minute
	DesktopAuthorizationCodeLifetime         = time.Minute
	BrowserAuthenticationRetention           = 24 * time.Hour
	DesktopAuthorizationEphemeralPortMinimum = 49152
)

type BrowserAuthenticationPurpose string

const BrowserAuthenticationPurposeDesktopAuthorization BrowserAuthenticationPurpose = "desktop_authorization"

type BrowserAuthenticationState string

const (
	BrowserAuthenticationStatePending    BrowserAuthenticationState = "pending"
	BrowserAuthenticationStateCodeIssued BrowserAuthenticationState = "code_issued"
	BrowserAuthenticationStateExchanged  BrowserAuthenticationState = "exchanged"
	BrowserAuthenticationStateCancelled  BrowserAuthenticationState = "cancelled"
	BrowserAuthenticationStateExpired    BrowserAuthenticationState = "expired"
)

// BrowserAuthenticationTransaction is one purpose-bound browser handoff. All
// bearer values are persisted only as hashes; the Desktop PKCE verifier and
// provider credentials never enter this aggregate.
type BrowserAuthenticationTransaction struct {
	ID            BrowserAuthenticationTransactionID
	CreatedAt     time.Time
	UpdatedAt     time.Time
	Purpose       BrowserAuthenticationPurpose
	State         BrowserAuthenticationState
	InstitutionID InstitutionID
	Issuer        string

	HandleHash                   string `json:"-"`
	BrowserProofHash             string `json:"-"`
	StateHash                    string `json:"-"`
	CallbackURL                  string `json:"-"`
	CodeChallenge                string `json:"-"`
	ExpectedAuthenticationMethod string
	ExpectedProviderID           string

	ClientType SessionClientType
	DeviceID   string
	DeviceName string
	ExpiresAt  time.Time

	UserID                   UserID
	AuthenticationMethod     string
	AuthenticationProviderID string
	ExternalIdentityID       ExternalIdentityID
	AuthenticationStrength   AuthenticationStrength
	AuthenticatedAt          OptionalTime
	MFACompletedAt           OptionalTime
	CodeHash                 string `json:"-"`
	CodeExpiresAt            OptionalTime
	CancelledAt              OptionalTime
	ExchangedAt              OptionalTime
	ExpiredAt                OptionalTime
}

func (t *BrowserAuthenticationTransaction) PrepareCreate(id BrowserAuthenticationTransactionID, at time.Time) {
	if t == nil {
		return
	}
	t.ID = id
	at = TimeUTC(at)
	t.CreatedAt = at
	t.UpdatedAt = at
	t.State = BrowserAuthenticationStatePending
	t.ExpiresAt = TimeUTC(t.ExpiresAt)
	t.DeviceID = SanitizeUnicode(t.DeviceID)
	t.DeviceName = SanitizeUnicode(t.DeviceName)
}

func (t *BrowserAuthenticationTransaction) PrepareCodeIssued(
	userID UserID,
	method string,
	providerID string,
	externalIdentityID ExternalIdentityID,
	strength AuthenticationStrength,
	authenticatedAt time.Time,
	mfaCompletedAt OptionalTime,
	codeHash string,
	codeExpiresAt time.Time,
	at time.Time,
) {
	if t == nil {
		return
	}
	at = TimeUTC(at)
	t.UpdatedAt = at
	t.State = BrowserAuthenticationStateCodeIssued
	t.HandleHash = ""
	t.BrowserProofHash = ""
	t.UserID = userID
	t.AuthenticationMethod = method
	t.AuthenticationProviderID = providerID
	t.ExternalIdentityID = externalIdentityID
	t.AuthenticationStrength = strength
	t.AuthenticatedAt = OptionalTimeFrom(authenticatedAt)
	t.MFACompletedAt = mfaCompletedAt
	t.CodeHash = codeHash
	t.CodeExpiresAt = OptionalTimeFrom(codeExpiresAt)
}

func (t *BrowserAuthenticationTransaction) PrepareCancelled(at time.Time) {
	if t == nil {
		return
	}
	at = TimeUTC(at)
	t.UpdatedAt = at
	t.State = BrowserAuthenticationStateCancelled
	t.HandleHash, t.BrowserProofHash, t.StateHash = "", "", ""
	t.CallbackURL, t.CodeChallenge, t.CodeHash = "", "", ""
	t.CodeExpiresAt = OptionalTime{}
	t.CancelledAt = OptionalTimeFrom(at)
}

func (t *BrowserAuthenticationTransaction) PrepareExchanged(at time.Time) {
	if t == nil {
		return
	}
	at = TimeUTC(at)
	t.UpdatedAt = at
	t.State = BrowserAuthenticationStateExchanged
	t.StateHash, t.CallbackURL, t.CodeChallenge, t.CodeHash = "", "", "", ""
	t.CodeExpiresAt = OptionalTime{}
	t.ExchangedAt = OptionalTimeFrom(at)
}

// PrepareExpired destroys every remaining bearer proof at the authoritative
// transaction deadline while retaining only safe diagnostic metadata.
func (t *BrowserAuthenticationTransaction) PrepareExpired(observedAt time.Time) {
	if t == nil || (t.State != BrowserAuthenticationStatePending && t.State != BrowserAuthenticationStateCodeIssued) {
		return
	}
	deadline := t.ExpiresAt
	if t.State == BrowserAuthenticationStateCodeIssued && t.CodeExpiresAt.Valid && t.CodeExpiresAt.Time.Before(deadline) {
		deadline = t.CodeExpiresAt.Time
	}
	if TimeUTC(observedAt).Before(deadline) {
		return
	}
	t.UpdatedAt = deadline
	t.State = BrowserAuthenticationStateExpired
	t.HandleHash, t.BrowserProofHash, t.StateHash = "", "", ""
	t.CallbackURL, t.CodeChallenge, t.CodeHash = "", "", ""
	t.CodeExpiresAt = OptionalTime{}
	t.ExpiredAt = OptionalTimeFrom(deadline)
}

func (t *BrowserAuthenticationTransaction) Validate() error {
	const where = "BrowserAuthenticationTransaction.Validate"
	if t == nil {
		return invalidModelError(where, "browser_authentication_transaction", "value", "is required", "")
	}
	if !t.ID.IsValid() {
		return invalidModelError(where, "browser_authentication_transaction", "id", "must be valid", "")
	}
	details := "id=" + t.ID.String()
	if t.CreatedAt.IsZero() || t.UpdatedAt.IsZero() || t.UpdatedAt.Before(t.CreatedAt) {
		return invalidModelError(where, "browser_authentication_transaction", "timestamps", "must be ordered and set", details)
	}
	if t.Purpose != BrowserAuthenticationPurposeDesktopAuthorization ||
		t.ClientType != SessionClientDesktop {
		return invalidModelError(where, "browser_authentication_transaction", "purpose", "must be a desktop authorization", details)
	}
	// Rehydration accepts the one explicitly supported development origin.
	// Composition is responsible for proving that loopback HTTP was enabled by
	// deployment policy before any such transaction can be created.
	if !t.InstitutionID.IsValid() || ValidateDesktopAuthorizationIssuer(t.Issuer, true) != nil {
		return invalidModelError(where, "browser_authentication_transaction", "issuer", "must identify the pinned installation", details)
	}
	if !validAuthenticationPath(t.ExpectedAuthenticationMethod, t.ExpectedProviderID) {
		return invalidModelError(where, "browser_authentication_transaction", "expected_authentication", "must identify one exact authentication path", details)
	}
	if len(t.DeviceID) > SessionDeviceIdMaxLength ||
		utf8.RuneCountInString(t.DeviceName) > SessionDeviceNameMaxRunes {
		return invalidModelError(where, "browser_authentication_transaction", "device", "exceeds the model bounds", details)
	}
	if !t.ExpiresAt.After(t.CreatedAt) ||
		t.ExpiresAt.After(t.CreatedAt.Add(BrowserAuthenticationTransactionLifetime)) {
		return invalidModelError(where, "browser_authentication_transaction", "expires_at", "must be within the browser lifetime", details)
	}
	switch t.State {
	case BrowserAuthenticationStatePending:
		if !IsValidTokenHash(t.HandleHash) || !IsValidTokenHash(t.BrowserProofHash) ||
			!IsValidTokenHash(t.StateHash) || !IsValidCredentialToken(t.CodeChallenge) ||
			ValidateDesktopAuthorizationCallback(t.CallbackURL) != nil ||
			!t.UserID.IsZero() || t.AuthenticationMethod != "" || t.AuthenticationProviderID != "" || !t.ExternalIdentityID.IsZero() ||
			t.AuthenticationStrength != "" || t.AuthenticatedAt.Valid || t.MFACompletedAt.Valid || t.CodeHash != "" ||
			t.CodeExpiresAt.Valid || t.CancelledAt.Valid || t.ExchangedAt.Valid || t.ExpiredAt.Valid {
			return invalidModelError(where, "browser_authentication_transaction", "state", "contains invalid pending state", details)
		}
	case BrowserAuthenticationStateCodeIssued:
		if t.HandleHash != "" || t.BrowserProofHash != "" || !IsValidTokenHash(t.StateHash) ||
			ValidateDesktopAuthorizationCallback(t.CallbackURL) != nil || !IsValidCredentialToken(t.CodeChallenge) ||
			!t.UserID.IsValid() || !validAuthenticationPath(t.AuthenticationMethod, t.AuthenticationProviderID) ||
			!validAuthenticationIdentity(t.AuthenticationProviderID, t.ExternalIdentityID) ||
			t.AuthenticationMethod != t.ExpectedAuthenticationMethod || t.AuthenticationProviderID != t.ExpectedProviderID ||
			!t.AuthenticationStrength.IsValid() || !t.AuthenticatedAt.Valid || t.AuthenticatedAt.Time.After(t.UpdatedAt) ||
			(t.AuthenticationStrength == AuthenticationMultiFactor && (!t.MFACompletedAt.Valid || t.MFACompletedAt.Time.Before(t.AuthenticatedAt.Time) || t.MFACompletedAt.Time.After(t.UpdatedAt))) ||
			(t.AuthenticationStrength == AuthenticationSingleFactor && t.MFACompletedAt.Valid) ||
			!IsValidTokenHash(t.CodeHash) || !t.CodeExpiresAt.Valid || !t.CodeExpiresAt.Time.After(t.UpdatedAt) ||
			t.CodeExpiresAt.Time.After(t.UpdatedAt.Add(DesktopAuthorizationCodeLifetime)) ||
			t.CodeExpiresAt.Time.After(t.ExpiresAt) || t.CancelledAt.Valid || t.ExchangedAt.Valid || t.ExpiredAt.Valid {
			return invalidModelError(where, "browser_authentication_transaction", "state", "contains invalid issued-code state", details)
		}
	case BrowserAuthenticationStateCancelled:
		if t.HandleHash != "" || t.BrowserProofHash != "" || t.StateHash != "" || t.CallbackURL != "" ||
			t.CodeChallenge != "" || t.CodeHash != "" || t.CodeExpiresAt.Valid || !t.CancelledAt.Valid ||
			t.MFACompletedAt.Valid || t.CancelledAt.Time != t.UpdatedAt || t.ExchangedAt.Valid || t.ExpiredAt.Valid {
			return invalidModelError(where, "browser_authentication_transaction", "state", "contains invalid cancelled state", details)
		}
	case BrowserAuthenticationStateExchanged:
		if t.HandleHash != "" || t.BrowserProofHash != "" || t.StateHash != "" || t.CallbackURL != "" ||
			t.CodeChallenge != "" || t.CodeHash != "" || t.CodeExpiresAt.Valid || !t.UserID.IsValid() ||
			!validAuthenticationPath(t.AuthenticationMethod, t.AuthenticationProviderID) ||
			!validAuthenticationIdentity(t.AuthenticationProviderID, t.ExternalIdentityID) ||
			!t.AuthenticationStrength.IsValid() || !t.AuthenticatedAt.Valid || t.CancelledAt.Valid ||
			(t.AuthenticationStrength == AuthenticationMultiFactor && !t.MFACompletedAt.Valid) ||
			(t.AuthenticationStrength == AuthenticationSingleFactor && t.MFACompletedAt.Valid) ||
			!t.ExchangedAt.Valid || t.ExchangedAt.Time != t.UpdatedAt || t.ExpiredAt.Valid {
			return invalidModelError(where, "browser_authentication_transaction", "state", "contains invalid exchanged state", details)
		}
	case BrowserAuthenticationStateExpired:
		proofsDestroyed := t.HandleHash == "" && t.BrowserProofHash == "" && t.StateHash == "" &&
			t.CallbackURL == "" && t.CodeChallenge == "" && t.CodeHash == "" && !t.CodeExpiresAt.Valid
		noResolvedUser := t.UserID.IsZero() && t.AuthenticationMethod == "" && t.AuthenticationProviderID == "" && t.ExternalIdentityID.IsZero() &&
			t.AuthenticationStrength == "" && !t.AuthenticatedAt.Valid && !t.MFACompletedAt.Valid
		resolvedUser := t.UserID.IsValid() && validAuthenticationPath(t.AuthenticationMethod, t.AuthenticationProviderID) &&
			validAuthenticationIdentity(t.AuthenticationProviderID, t.ExternalIdentityID) &&
			t.AuthenticationMethod == t.ExpectedAuthenticationMethod && t.AuthenticationProviderID == t.ExpectedProviderID &&
			t.AuthenticationStrength.IsValid() && t.AuthenticatedAt.Valid && !t.AuthenticatedAt.Time.After(t.UpdatedAt) &&
			((t.AuthenticationStrength == AuthenticationSingleFactor && !t.MFACompletedAt.Valid) ||
				(t.AuthenticationStrength == AuthenticationMultiFactor && t.MFACompletedAt.Valid &&
					!t.MFACompletedAt.Time.Before(t.AuthenticatedAt.Time) && !t.MFACompletedAt.Time.After(t.UpdatedAt)))
		if !proofsDestroyed || (!noResolvedUser && !resolvedUser) || t.CancelledAt.Valid || t.ExchangedAt.Valid ||
			!t.ExpiredAt.Valid || !t.ExpiredAt.Time.Equal(t.UpdatedAt) || t.UpdatedAt.After(t.ExpiresAt) ||
			(noResolvedUser && !t.UpdatedAt.Equal(t.ExpiresAt)) {
			return invalidModelError(where, "browser_authentication_transaction", "state", "contains invalid expired state", details)
		}
	default:
		return invalidModelError(where, "browser_authentication_transaction", "state", "has an unknown value", details)
	}
	return nil
}

func validAuthenticationPath(method, providerID string) bool {
	if method == "password" {
		return providerID == ""
	}
	return method != "" && len(method) <= SessionAuthenticationMaxLength &&
		validName.MatchString(method) && IsValidIdentityProviderID(providerID)
}

func validAuthenticationIdentity(providerID string, identityID ExternalIdentityID) bool {
	if providerID == "" {
		return identityID.IsZero()
	}
	return identityID.IsValid()
}

// ValidateDesktopAuthorizationIssuer validates the installation origin pinned
// into a native-public-client transaction. HTTP is restricted to an explicit
// loopback-development policy; production origins remain HTTPS-only.
func ValidateDesktopAuthorizationIssuer(issuer string, allowLoopbackHTTPDevelopment bool) error {
	if issuer == "" || len(issuer) > ExternalLoginReturnToMaxLength {
		return errors.New("desktop authorization issuer is invalid")
	}
	parsed, err := url.Parse(issuer)
	if err != nil || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" {
		return errors.New("desktop authorization issuer is invalid")
	}
	if parsed.Scheme == "https" {
		return nil
	}
	if parsed.Scheme != "http" || !allowLoopbackHTTPDevelopment {
		return errors.New("desktop authorization issuer requires HTTPS")
	}
	if strings.EqualFold(parsed.Hostname(), "localhost") {
		return nil
	}
	ip := net.ParseIP(parsed.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return errors.New("desktop authorization HTTP issuer must be loopback")
	}
	return nil
}

func (t *BrowserAuthenticationTransaction) Auditable() map[string]any {
	if t == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id": t.ID.String(), "purpose": t.Purpose, "state": t.State,
		"institution_id": t.InstitutionID.String(), "issuer": t.Issuer,
		"client_type": t.ClientType, "device_id": t.DeviceID,
		"expected_authentication_method": t.ExpectedAuthenticationMethod,
		"expected_provider_id":           t.ExpectedProviderID,
		"user_id":                        t.UserID.String(), "authentication_method": t.AuthenticationMethod,
		"authentication_provider_id": t.AuthenticationProviderID,
		"external_identity_id":       t.ExternalIdentityID.String(),
		"created_at":                 MillisFromTime(t.CreatedAt), "updated_at": MillisFromTime(t.UpdatedAt),
		"expires_at": MillisFromTime(t.ExpiresAt), "authenticated_at": t.AuthenticatedAt.Millis(),
		"mfa_completed_at": t.MFACompletedAt.Millis(),
		"code_expires_at":  t.CodeExpiresAt.Millis(), "cancelled_at": t.CancelledAt.Millis(),
		"exchanged_at": t.ExchangedAt.Millis(), "expired_at": t.ExpiredAt.Millis(),
	}
}

var _ Auditable = (*BrowserAuthenticationTransaction)(nil)

func IsValidPKCEVerifier(verifier string) bool {
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}
	for _, char := range verifier {
		if !strings.ContainsRune(pkceUnreserved, char) {
			return false
		}
	}
	return true
}

func PKCES256Challenge(verifier string) string {
	digest := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

// ValidateDesktopAuthorizationCallback accepts only one exact native-public-
// client loopback destination. The single path segment is a 256-bit random
// value generated by the Desktop Client; query parameters belong only to the
// server's terminal redirect and therefore cannot be registered here.
func ValidateDesktopAuthorizationCallback(callback string) error {
	parsed, err := url.Parse(callback)
	if err != nil || parsed.Scheme != "http" || parsed.Opaque != "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery ||
		parsed.Fragment != "" || parsed.RawPath != "" {
		return ErrInvalidDesktopAuthorizationCallback
	}
	host, portText, err := net.SplitHostPort(parsed.Host)
	if err != nil || (host != "127.0.0.1" && host != "::1") {
		return ErrInvalidDesktopAuthorizationCallback
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < DesktopAuthorizationEphemeralPortMinimum || port > 65535 {
		return ErrInvalidDesktopAuthorizationCallback
	}
	pathToken := strings.TrimPrefix(parsed.Path, "/")
	if parsed.Path != "/"+pathToken || !IsValidCredentialToken(pathToken) {
		return ErrInvalidDesktopAuthorizationCallback
	}
	canonical := "http://" + net.JoinHostPort(host, portText) + "/" + pathToken
	if callback != canonical {
		return ErrInvalidDesktopAuthorizationCallback
	}
	return nil
}
