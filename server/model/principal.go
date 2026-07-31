// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import "time"

type CredentialType string

const (
	CredentialSessionAccess       CredentialType = "session_access"
	CredentialPersonalAccessToken CredentialType = "personal_access_token"
)

// Principal is the immutable authentication context attached to one request.
// It deliberately excludes roles, permissions, affiliations, and academic
// memberships because authorization must resolve current durable state.
type Principal struct {
	UserId                 string                 `json:"user_id"`
	SessionId              string                 `json:"session_id"`
	CredentialId           string                 `json:"credential_id"`
	CredentialType         CredentialType         `json:"credential_type"`
	AuthenticationMethod   string                 `json:"authentication_method"`
	AuthenticationStrength AuthenticationStrength `json:"authentication_strength"`
	ClientType             SessionClientType      `json:"client_type"`
	AuthenticatedAt        int64                  `json:"authenticated_at"`
	MFACompletedAt         int64                  `json:"mfa_completed_at,omitempty"`
	CredentialScopes       []string               `json:"credential_scopes,omitempty"`
	AcademicUnitId         string                 `json:"academic_unit_id,omitempty"`
}

// AuthenticationTokens contains raw credentials returned exactly once after
// login or refresh. These values must never be persisted, audited, or logged.
type AuthenticationTokens struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	AccessExpiresAt  int64  `json:"access_expires_at"`
	RefreshExpiresAt int64  `json:"refresh_expires_at"`
}

func (p Principal) IsValid() bool {
	if !IsValidId(p.UserId) || !IsValidId(p.CredentialId) ||
		p.AuthenticationMethod == "" || !p.ClientType.IsValid() {
		return false
	}
	switch p.CredentialType {
	case CredentialSessionAccess:
		return IsValidId(p.SessionId) &&
			p.AuthenticationStrength.IsValid() &&
			p.AuthenticatedAt > 0 &&
			len(p.CredentialScopes) == 0 &&
			p.AcademicUnitId == ""
	case CredentialPersonalAccessToken:
		if p.SessionId != "" || p.AuthenticationStrength != "" ||
			p.AuthenticatedAt != 0 || p.MFACompletedAt != 0 ||
			p.ClientType != SessionClientCLI ||
			len(p.CredentialScopes) == 0 ||
			(p.AcademicUnitId != "" && !IsValidId(p.AcademicUnitId)) {
			return false
		}
		seen := make(map[string]struct{}, len(p.CredentialScopes))
		for _, scope := range p.CredentialScopes {
			if !IsKnownAction(scope) {
				return false
			}
			if _, exists := seen[scope]; exists {
				return false
			}
			seen[scope] = struct{}{}
		}
		return true
	default:
		return false
	}
}

func (p Principal) HasStrongAuthentication() bool {
	return p.AuthenticationStrength == AuthenticationMultiFactor
}

func (p Principal) LastAuthenticationAt() int64 {
	if p.MFACompletedAt > p.AuthenticatedAt {
		return p.MFACompletedAt
	}
	return p.AuthenticatedAt
}

func (p Principal) IsRecentlyAuthenticated(now time.Time, maximumAge time.Duration) bool {
	if maximumAge <= 0 {
		return false
	}
	authenticatedAt := p.LastAuthenticationAt()
	current := now.UnixMilli()
	return authenticatedAt > 0 &&
		authenticatedAt <= current &&
		current-authenticatedAt <= maximumAge.Milliseconds()
}
