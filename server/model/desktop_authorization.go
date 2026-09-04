// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

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
const BrowserAuthenticationPurposeInvitationAcceptance BrowserAuthenticationPurpose = "invitation_acceptance"

type BrowserAuthenticationState string

const (
	BrowserAuthenticationStatePending       BrowserAuthenticationState = "pending"
	BrowserAuthenticationStateBound         BrowserAuthenticationState = "bound"
	BrowserAuthenticationStateAuthenticated BrowserAuthenticationState = "authenticated"
	BrowserAuthenticationStateCodeIssued    BrowserAuthenticationState = "code_issued"
	BrowserAuthenticationStateExchanged     BrowserAuthenticationState = "exchanged"
	BrowserAuthenticationStateCompleted     BrowserAuthenticationState = "completed"
	BrowserAuthenticationStateCancelled     BrowserAuthenticationState = "cancelled"
	BrowserAuthenticationStateDenied        BrowserAuthenticationState = "denied"
	BrowserAuthenticationStateExpired       BrowserAuthenticationState = "expired"
)

// DesktopAuthorizationDenialReason is a closed, safe terminal denial. It is
// retained for audit and maintenance, but not as a bearer to the transaction.
type DesktopAuthorizationDenialReason string

const DesktopAuthorizationDenialActiveAttempt DesktopAuthorizationDenialReason = "active_attempt_session_lock"

func (r DesktopAuthorizationDenialReason) IsValid() bool {
	return r == DesktopAuthorizationDenialActiveAttempt
}

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
	InvitationID  InvitationID

	HandleHash                   string `json:"-"`
	BrowserProofHash             string `json:"-"`
	InvitationClaimHash          string `json:"-"`
	StateHash                    string `json:"-"`
	CallbackURL                  string `json:"-"`
	CodeChallenge                string `json:"-"`
	ExpectedAuthenticationMethod string
	ExpectedProviderID           string
	ClientType                   SessionClientType
	DeviceID                     string
	DeviceName                   string
	ProposedPublicJWK            DesktopPublicJWK
	ProposedKeyThumbprint        string
	DesktopRelease               string
	DesktopBuildID               string
	DesktopPlatform              DesktopPlatform
	DesktopArchitecture          DesktopArchitecture
	DesktopRealtimeProtocol      int
	ExpiresAt                    time.Time

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
	DeniedAt                 OptionalTime
	DenialReason             DesktopAuthorizationDenialReason
	ExchangedAt              OptionalTime
	CompletedAt              OptionalTime
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
	if (t.Purpose == BrowserAuthenticationPurposeDesktopAuthorization && t.ClientType != SessionClientDesktop) ||
		(t.Purpose == BrowserAuthenticationPurposeInvitationAcceptance && t.ClientType != SessionClientWeb) ||
		(t.Purpose != BrowserAuthenticationPurposeDesktopAuthorization && t.Purpose != BrowserAuthenticationPurposeInvitationAcceptance) {
		return invalidModelError(where, "browser_authentication_transaction", "purpose", "has an invalid purpose or client type", details)
	}
	// Rehydration accepts the one explicitly supported development origin.
	// Composition is responsible for proving that loopback HTTP was enabled by
	// deployment policy before any such transaction can be created.
	if !t.InstitutionID.IsValid() || ValidateBrowserAuthenticationIssuer(t.Issuer, true) != nil {
		return invalidModelError(where, "browser_authentication_transaction", "issuer", "must identify the pinned installation", details)
	}
	if !t.ExpiresAt.After(t.CreatedAt) ||
		t.ExpiresAt.After(t.CreatedAt.Add(BrowserAuthenticationTransactionLifetime)) {
		return invalidModelError(where, "browser_authentication_transaction", "expires_at", "must be within the browser lifetime", details)
	}
	if t.Purpose == BrowserAuthenticationPurposeInvitationAcceptance {
		return t.validateInvitationAcceptance(details)
	}
	if !t.InvitationID.IsZero() || t.InvitationClaimHash != "" || t.CompletedAt.Valid {
		return invalidModelError(where, "browser_authentication_transaction", "invitation", "must be empty for desktop authorization", details)
	}
	if len(t.DeviceID) > SessionDeviceIdMaxLength ||
		utf8.RuneCountInString(t.DeviceName) > SessionDeviceNameMaxRunes {
		return invalidModelError(where, "browser_authentication_transaction", "device", "exceeds the model bounds", details)
	}
	hasProposedKey := t.ProposedPublicJWK.Validate() == nil &&
		IsValidDPoPKeyThumbprint(t.ProposedKeyThumbprint) &&
		IsValidDesktopRelease(t.DesktopRelease) && IsValidDesktopBuildID(t.DesktopBuildID) &&
		t.DesktopPlatform.IsValid() && t.DesktopArchitecture.IsValid() && t.DesktopRealtimeProtocol > 0
	noProposedKey := t.ProposedPublicJWK == (DesktopPublicJWK{}) && t.ProposedKeyThumbprint == "" &&
		t.DesktopRelease == "" && t.DesktopBuildID == "" && t.DesktopPlatform == "" &&
		t.DesktopArchitecture == "" && t.DesktopRealtimeProtocol == 0
	if hasProposedKey {
		thumbprint, _ := t.ProposedPublicJWK.Thumbprint()
		hasProposedKey = thumbprint == t.ProposedKeyThumbprint
	}
	if t.ExpectedAuthenticationMethod != "" || t.ExpectedProviderID != "" {
		return invalidModelError(where, "browser_authentication_transaction", "expected_authentication", "must be empty for desktop authorization", details)
	}
	switch t.State {
	case BrowserAuthenticationStatePending:
		if !IsValidTokenHash(t.HandleHash) || !IsValidTokenHash(t.BrowserProofHash) ||
			!IsValidTokenHash(t.StateHash) || !IsValidCredentialToken(t.CodeChallenge) ||
			ValidateDesktopAuthorizationCallback(t.CallbackURL) != nil ||
			!hasProposedKey || !t.hasNoDesktopAuthentication() || t.CodeHash != "" || t.CodeExpiresAt.Valid || !t.hasNoDesktopTerminal() {
			return invalidModelError(where, "browser_authentication_transaction", "state", "contains invalid pending state", details)
		}
	case BrowserAuthenticationStateBound:
		if t.HandleHash != "" || !IsValidTokenHash(t.BrowserProofHash) ||
			!IsValidTokenHash(t.StateHash) || !IsValidCredentialToken(t.CodeChallenge) ||
			ValidateDesktopAuthorizationCallback(t.CallbackURL) != nil ||
			!hasProposedKey || !t.hasNoDesktopAuthentication() || t.CodeHash != "" || t.CodeExpiresAt.Valid || !t.hasNoDesktopTerminal() {
			return invalidModelError(where, "browser_authentication_transaction", "state", "contains invalid bound state", details)
		}
	case BrowserAuthenticationStateAuthenticated:
		if t.HandleHash != "" || !IsValidTokenHash(t.BrowserProofHash) ||
			!IsValidTokenHash(t.StateHash) || !IsValidCredentialToken(t.CodeChallenge) ||
			ValidateDesktopAuthorizationCallback(t.CallbackURL) != nil || !hasProposedKey || !t.hasValidDesktopAuthentication() ||
			t.CodeHash != "" || t.CodeExpiresAt.Valid || !t.hasNoDesktopTerminal() {
			return invalidModelError(where, "browser_authentication_transaction", "state", "contains invalid authenticated state", details)
		}
	case BrowserAuthenticationStateCodeIssued:
		if t.HandleHash != "" || t.BrowserProofHash != "" || !IsValidTokenHash(t.StateHash) ||
			ValidateDesktopAuthorizationCallback(t.CallbackURL) != nil || !IsValidCredentialToken(t.CodeChallenge) ||
			!hasProposedKey || !t.hasValidDesktopAuthentication() ||
			!IsValidTokenHash(t.CodeHash) || !t.CodeExpiresAt.Valid || !t.CodeExpiresAt.Time.After(t.UpdatedAt) ||
			t.CodeExpiresAt.Time.After(t.UpdatedAt.Add(DesktopAuthorizationCodeLifetime)) ||
			t.CodeExpiresAt.Time.After(t.ExpiresAt) || !t.hasNoDesktopTerminal() {
			return invalidModelError(where, "browser_authentication_transaction", "state", "contains invalid issued-code state", details)
		}
	case BrowserAuthenticationStateCancelled:
		if t.HandleHash != "" || t.BrowserProofHash != "" || t.StateHash != "" || t.CallbackURL != "" ||
			t.CodeChallenge != "" || t.CodeHash != "" || t.CodeExpiresAt.Valid || !noProposedKey || !t.CancelledAt.Valid ||
			!t.hasNoDesktopAuthentication() || t.CancelledAt.Time != t.UpdatedAt || t.DeniedAt.Valid ||
			t.DenialReason != "" || t.ExchangedAt.Valid || t.ExpiredAt.Valid {
			return invalidModelError(where, "browser_authentication_transaction", "state", "contains invalid cancelled state", details)
		}
	case BrowserAuthenticationStateDenied:
		if t.HandleHash != "" || t.BrowserProofHash != "" || t.StateHash != "" || t.CallbackURL != "" ||
			t.CodeChallenge != "" || t.CodeHash != "" || t.CodeExpiresAt.Valid || !noProposedKey || !t.hasNoDesktopAuthentication() ||
			t.CancelledAt.Valid || !t.DeniedAt.Valid || t.DeniedAt.Time != t.UpdatedAt ||
			!t.DenialReason.IsValid() || t.ExchangedAt.Valid || t.ExpiredAt.Valid {
			return invalidModelError(where, "browser_authentication_transaction", "state", "contains invalid denied state", details)
		}
	case BrowserAuthenticationStateExchanged:
		if t.HandleHash != "" || t.BrowserProofHash != "" || t.StateHash != "" || t.CallbackURL != "" ||
			t.CodeChallenge != "" || t.CodeHash != "" || t.CodeExpiresAt.Valid || !noProposedKey || !t.hasValidDesktopAuthentication() ||
			t.CancelledAt.Valid || t.DeniedAt.Valid || t.DenialReason != "" ||
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
			t.AuthenticationStrength.IsValid() && t.AuthenticatedAt.Valid && !t.AuthenticatedAt.Time.After(t.UpdatedAt) &&
			((t.AuthenticationStrength == AuthenticationSingleFactor && !t.MFACompletedAt.Valid) ||
				(t.AuthenticationStrength == AuthenticationMultiFactor && t.MFACompletedAt.Valid &&
					!t.MFACompletedAt.Time.Before(t.AuthenticatedAt.Time) && !t.MFACompletedAt.Time.After(t.UpdatedAt)))
		if !proofsDestroyed || !noProposedKey || (!noResolvedUser && !resolvedUser) || t.CancelledAt.Valid || t.DeniedAt.Valid || t.DenialReason != "" || t.ExchangedAt.Valid ||
			!t.ExpiredAt.Valid || !t.ExpiredAt.Time.Equal(t.UpdatedAt) || t.UpdatedAt.After(t.ExpiresAt) ||
			(noResolvedUser && !t.UpdatedAt.Equal(t.ExpiresAt)) {
			return invalidModelError(where, "browser_authentication_transaction", "state", "contains invalid expired state", details)
		}
	default:
		return invalidModelError(where, "browser_authentication_transaction", "state", "has an unknown value", details)
	}
	return nil
}

func (t *BrowserAuthenticationTransaction) hasNoDesktopAuthentication() bool {
	return t.UserID.IsZero() && t.AuthenticationMethod == "" && t.AuthenticationProviderID == "" &&
		t.ExternalIdentityID.IsZero() && t.AuthenticationStrength == "" && !t.AuthenticatedAt.Valid && !t.MFACompletedAt.Valid
}

func (t *BrowserAuthenticationTransaction) hasValidDesktopAuthentication() bool {
	if !t.UserID.IsValid() || !validAuthenticationPath(t.AuthenticationMethod, t.AuthenticationProviderID) ||
		!validAuthenticationIdentity(t.AuthenticationProviderID, t.ExternalIdentityID) ||
		!t.AuthenticationStrength.IsValid() || !t.AuthenticatedAt.Valid || t.AuthenticatedAt.Time.After(t.UpdatedAt) {
		return false
	}
	if t.AuthenticationStrength == AuthenticationMultiFactor {
		return t.MFACompletedAt.Valid && !t.MFACompletedAt.Time.Before(t.AuthenticatedAt.Time) &&
			!t.MFACompletedAt.Time.After(t.UpdatedAt)
	}
	return !t.MFACompletedAt.Valid
}

func (t *BrowserAuthenticationTransaction) hasNoDesktopTerminal() bool {
	return !t.CancelledAt.Valid && !t.DeniedAt.Valid && t.DenialReason == "" &&
		!t.ExchangedAt.Valid && !t.ExpiredAt.Valid
}

func (t *BrowserAuthenticationTransaction) validateInvitationAcceptance(details string) error {
	const where = "BrowserAuthenticationTransaction.Validate"
	if !t.InvitationID.IsValid() || t.ExpectedAuthenticationMethod != "" || t.ExpectedProviderID != "" ||
		t.StateHash != "" || t.CallbackURL != "" || t.CodeChallenge != "" || t.DeviceID != "" || t.DeviceName != "" ||
		t.ProposedPublicJWK != (DesktopPublicJWK{}) || t.ProposedKeyThumbprint != "" || t.DesktopRelease != "" ||
		t.DesktopBuildID != "" || t.DesktopPlatform != "" || t.DesktopArchitecture != "" || t.DesktopRealtimeProtocol != 0 ||
		t.AuthenticationMethod != "" || t.AuthenticationProviderID != "" || !t.ExternalIdentityID.IsZero() ||
		t.AuthenticationStrength != "" || t.AuthenticatedAt.Valid || t.MFACompletedAt.Valid || t.CodeHash != "" ||
		t.CodeExpiresAt.Valid || t.DeniedAt.Valid || t.DenialReason != "" || t.ExchangedAt.Valid {
		return invalidModelError(where, "browser_authentication_transaction", "invitation", "contains desktop authorization state", details)
	}
	switch t.State {
	case BrowserAuthenticationStatePending:
		if !IsValidTokenHash(t.HandleHash) || !IsValidTokenHash(t.BrowserProofHash) ||
			!IsValidTokenHash(t.InvitationClaimHash) || !t.UserID.IsZero() || t.CancelledAt.Valid ||
			t.CompletedAt.Valid || t.ExpiredAt.Valid {
			return invalidModelError(where, "browser_authentication_transaction", "state", "contains invalid pending invitation state", details)
		}
	case BrowserAuthenticationStateCompleted:
		if t.HandleHash != "" || t.BrowserProofHash != "" || t.InvitationClaimHash != "" || !t.UserID.IsValid() ||
			t.CancelledAt.Valid || !t.CompletedAt.Valid || !t.CompletedAt.Time.Equal(t.UpdatedAt) || t.ExpiredAt.Valid {
			return invalidModelError(where, "browser_authentication_transaction", "state", "contains invalid completed invitation state", details)
		}
	case BrowserAuthenticationStateCancelled:
		if t.HandleHash != "" || t.BrowserProofHash != "" || t.InvitationClaimHash != "" || !t.UserID.IsZero() ||
			!t.CancelledAt.Valid || !t.CancelledAt.Time.Equal(t.UpdatedAt) || t.CompletedAt.Valid || t.ExpiredAt.Valid {
			return invalidModelError(where, "browser_authentication_transaction", "state", "contains invalid cancelled invitation state", details)
		}
	case BrowserAuthenticationStateExpired:
		if t.HandleHash != "" || t.BrowserProofHash != "" || t.InvitationClaimHash != "" || !t.UserID.IsZero() ||
			t.CancelledAt.Valid || t.CompletedAt.Valid || !t.ExpiredAt.Valid || !t.ExpiredAt.Time.Equal(t.UpdatedAt) ||
			!t.UpdatedAt.Equal(t.ExpiresAt) {
			return invalidModelError(where, "browser_authentication_transaction", "state", "contains invalid expired invitation state", details)
		}
	default:
		return invalidModelError(where, "browser_authentication_transaction", "state", "is invalid for invitation acceptance", details)
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

// ValidateBrowserAuthenticationIssuer validates the installation origin
// pinned into a browser transaction. HTTP is restricted to an explicit
// loopback-development policy; production origins remain HTTPS-only.
func ValidateBrowserAuthenticationIssuer(issuer string, allowLoopbackHTTPDevelopment bool) error {
	if issuer == "" || len(issuer) > ExternalLoginReturnToMaxLength {
		return errors.New("browser authentication issuer is invalid")
	}
	parsed, err := url.Parse(issuer)
	if err != nil || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" {
		return errors.New("browser authentication issuer is invalid")
	}
	if parsed.Scheme == "https" {
		return nil
	}
	if parsed.Scheme != "http" || !allowLoopbackHTTPDevelopment {
		return errors.New("browser authentication issuer requires HTTPS")
	}
	if strings.EqualFold(parsed.Hostname(), "localhost") {
		return nil
	}
	ip := net.ParseIP(parsed.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return errors.New("browser authentication HTTP issuer must be loopback")
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
		"invitation_id": t.InvitationID.String(),
		"client_type":   t.ClientType, "device_id": t.DeviceID,
		"expected_authentication_method": t.ExpectedAuthenticationMethod,
		"expected_provider_id":           t.ExpectedProviderID,
		"user_id":                        t.UserID.String(), "authentication_method": t.AuthenticationMethod,
		"authentication_provider_id": t.AuthenticationProviderID,
		"external_identity_id":       t.ExternalIdentityID.String(),
		"created_at":                 MillisFromTime(t.CreatedAt), "updated_at": MillisFromTime(t.UpdatedAt),
		"expires_at": MillisFromTime(t.ExpiresAt), "authenticated_at": t.AuthenticatedAt.Millis(),
		"mfa_completed_at": t.MFACompletedAt.Millis(),
		"code_expires_at":  t.CodeExpiresAt.Millis(), "cancelled_at": t.CancelledAt.Millis(),
		"denied_at": t.DeniedAt.Millis(), "denial_reason": t.DenialReason,
		"exchanged_at": t.ExchangedAt.Millis(), "completed_at": t.CompletedAt.Millis(), "expired_at": t.ExpiredAt.Millis(),
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
