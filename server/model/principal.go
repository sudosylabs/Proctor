// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"errors"
	"time"
)

type CredentialType string

// PrincipalCredentialID identifies the concrete credential that established
// a principal. It is intentionally distinct from persistence IDs because the
// credential may be either a session credential or a personal access token.
type PrincipalCredentialID string

func (id PrincipalCredentialID) IsValid() bool { return IsValidId(string(id)) }

func (id PrincipalCredentialID) String() string { return string(id) }

const (
	CredentialSessionAccess       CredentialType = "session_access"
	CredentialPersonalAccessToken CredentialType = "personal_access_token"
)

// Principal is the immutable authentication context attached to one request.
// It deliberately excludes roles, permissions, affiliations, and academic
// memberships because authorization must resolve current durable state.
type Principal struct {
	UserID                   UserID
	SessionID                SessionID
	CredentialID             PrincipalCredentialID
	CredentialType           CredentialType
	AuthenticationMethod     string
	AuthenticationProviderID string
	ExternalIdentityID       ExternalIdentityID
	AuthenticationStrength   AuthenticationStrength
	ClientType               SessionClientType
	DesktopRegistrationID    DesktopRegistrationID
	DPoPKeyThumbprint        string
	RegisteredDesktopKey     bool
	DesktopRelease           string
	DesktopBuildID           string
	DesktopPlatform          DesktopPlatform
	DesktopArchitecture      DesktopArchitecture
	DesktopRealtimeProtocol  int
	AuthenticatedAt          time.Time
	MFACompletedAt           OptionalTime
	CredentialScopes         []string
	AcademicUnitID           AcademicUnitID
}

// AuthenticationTokens contains raw credentials returned exactly once after
// login or refresh. These values must never be persisted, audited, or logged.
type AuthenticationTokens struct {
	TokenType        string
	AccessToken      string
	RefreshToken     string
	AccessExpiresAt  time.Time
	RefreshExpiresAt time.Time
}

// Validate checks that the immutable authentication context is internally
// consistent for its credential type.
func (p Principal) Validate() error {
	if !p.UserID.IsValid() || !p.CredentialID.IsValid() ||
		p.AuthenticationMethod == "" || !p.ClientType.IsValid() {
		return errors.New("model: principal identity is invalid")
	}
	switch p.CredentialType {
	case CredentialSessionAccess:
		if !p.SessionID.IsValid() ||
			!p.AuthenticationStrength.IsValid() ||
			p.AuthenticatedAt.IsZero() ||
			len(p.CredentialScopes) != 0 ||
			!p.AcademicUnitID.IsZero() {
			return errors.New("model: session principal is invalid")
		}
		if p.RegisteredDesktopKey {
			if p.ClientType != SessionClientDesktop || !p.DesktopRegistrationID.IsValid() ||
				!IsValidDPoPKeyThumbprint(p.DPoPKeyThumbprint) || !IsValidDesktopRelease(p.DesktopRelease) ||
				!IsValidDesktopBuildID(p.DesktopBuildID) || !p.DesktopPlatform.IsValid() ||
				!p.DesktopArchitecture.IsValid() || p.DesktopRealtimeProtocol < 1 {
				return errors.New("model: registered Desktop-key principal is invalid")
			}
		} else if !p.DesktopRegistrationID.IsZero() || p.DPoPKeyThumbprint != "" || p.DesktopRelease != "" ||
			p.DesktopBuildID != "" || p.DesktopPlatform != "" || p.DesktopArchitecture != "" || p.DesktopRealtimeProtocol != 0 {
			return errors.New("model: unproved Desktop-key principal is invalid")
		}
		if (p.AuthenticationMethod == "password" && (p.AuthenticationProviderID != "" || !p.ExternalIdentityID.IsZero())) ||
			(p.AuthenticationMethod != "password" &&
				(!IsValidIdentityProviderID(p.AuthenticationProviderID) || !p.ExternalIdentityID.IsValid())) {
			return errors.New("model: session principal authentication provider is invalid")
		}
		return nil
	case CredentialPersonalAccessToken:
		if !p.SessionID.IsZero() || p.AuthenticationStrength != "" ||
			p.AuthenticationProviderID != "" ||
			!p.ExternalIdentityID.IsZero() ||
			!p.AuthenticatedAt.IsZero() || p.MFACompletedAt.Valid ||
			p.ClientType != SessionClientCLI || p.RegisteredDesktopKey || !p.DesktopRegistrationID.IsZero() || p.DPoPKeyThumbprint != "" ||
			p.DesktopRelease != "" || p.DesktopBuildID != "" || p.DesktopPlatform != "" || p.DesktopArchitecture != "" || p.DesktopRealtimeProtocol != 0 ||
			len(p.CredentialScopes) == 0 ||
			(!p.AcademicUnitID.IsZero() && !p.AcademicUnitID.IsValid()) {
			return errors.New("model: personal access token principal is invalid")
		}
		seen := make(map[string]struct{}, len(p.CredentialScopes))
		for _, scope := range p.CredentialScopes {
			if !IsPersonalAccessTokenAction(scope) {
				return errors.New("model: personal access token scope is invalid")
			}
			if _, exists := seen[scope]; exists {
				return errors.New("model: personal access token scope is duplicated")
			}
			seen[scope] = struct{}{}
		}
		return nil
	default:
		return errors.New("model: principal credential type is invalid")
	}
}

func (p Principal) HasStrongAuthentication() bool {
	return p.AuthenticationStrength == AuthenticationMultiFactor
}

// HasRegisteredDesktopKey reports the request-scoped proof assurance. Merely
// holding a Desktop Session snapshot is insufficient; the current request must
// have passed DPoP verification.
func (p Principal) HasRegisteredDesktopKey() bool {
	return p.RegisteredDesktopKey && p.ClientType == SessionClientDesktop &&
		p.DesktopRegistrationID.IsValid() && IsValidDPoPKeyThumbprint(p.DPoPKeyThumbprint)
}

func (p Principal) LastAuthenticationAt() time.Time {
	if p.MFACompletedAt.Valid && p.MFACompletedAt.Time.After(p.AuthenticatedAt) {
		return p.MFACompletedAt.Time
	}
	return p.AuthenticatedAt
}

func (p Principal) IsRecentlyAuthenticated(now time.Time, maximumAge time.Duration) bool {
	if maximumAge <= 0 {
		return false
	}
	authenticatedAt := p.LastAuthenticationAt()
	now = TimeUTC(now)
	return !authenticatedAt.IsZero() &&
		!authenticatedAt.After(now) &&
		now.Sub(authenticatedAt) <= maximumAge
}
