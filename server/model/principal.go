// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

type CredentialType string

const (
	CredentialSessionAccess CredentialType = "session_access"
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
	return IsValidId(p.UserId) &&
		IsValidId(p.SessionId) &&
		IsValidId(p.CredentialId) &&
		p.CredentialType == CredentialSessionAccess &&
		p.AuthenticationMethod != "" &&
		p.AuthenticationStrength.IsValid() &&
		p.ClientType.IsValid() &&
		p.AuthenticatedAt > 0
}
