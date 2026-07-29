// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/public/model/session.go. Proctor separates
// hashed credentials from the session and never stores authorization roles in
// the session snapshot.

package model

import "unicode/utf8"

const (
	SessionDeviceIdMaxLength       = 128
	SessionDeviceNameMaxRunes      = 128
	SessionRevocationMaxRunes      = 256
	SessionAuthenticationMaxLength = 64
)

type SessionClientType string

const (
	SessionClientDesktop SessionClientType = "desktop"
	SessionClientCLI     SessionClientType = "cli"
	SessionClientWeb     SessionClientType = "web"
)

type AuthenticationStrength string

const (
	AuthenticationSingleFactor AuthenticationStrength = "single_factor"
	AuthenticationMultiFactor  AuthenticationStrength = "multi_factor"
)

// Session is one revocable authenticated login. It stores authentication
// assurance and safe client metadata but never bearer credentials, role
// bindings, or permission snapshots. SessionCredential owns the hashed access
// and refresh credentials for this session.
type Session struct {
	Id                     string                 `json:"id"`
	CreateAt               int64                  `json:"create_at"`
	UpdateAt               int64                  `json:"update_at"`
	DeleteAt               int64                  `json:"delete_at"`
	UserId                 string                 `json:"user_id"`
	ClientType             SessionClientType      `json:"client_type"`
	DeviceId               string                 `json:"device_id,omitempty"`
	DeviceName             string                 `json:"device_name,omitempty"`
	AuthenticationMethod   string                 `json:"authentication_method"`
	AuthenticationStrength AuthenticationStrength `json:"authentication_strength"`
	AuthenticatedAt        int64                  `json:"authenticated_at"`
	MFACompletedAt         int64                  `json:"mfa_completed_at,omitempty"`
	LastActivityAt         int64                  `json:"last_activity_at"`
	IdleExpiresAt          int64                  `json:"idle_expires_at"`
	ExpiresAt              int64                  `json:"expires_at"`
	RevokedAt              int64                  `json:"revoked_at,omitempty"`
	RevocationReason       string                 `json:"revocation_reason,omitempty"`
}

func (s *Session) PreSave() {
	preSave(&s.Id, &s.CreateAt, &s.UpdateAt)
	if s.AuthenticatedAt == 0 {
		s.AuthenticatedAt = s.CreateAt
	}
	if s.LastActivityAt < s.CreateAt {
		s.LastActivityAt = s.CreateAt
	}
	s.DeviceId = SanitizeUnicode(s.DeviceId)
	s.DeviceName = SanitizeUnicode(s.DeviceName)
	s.AuthenticationMethod = SanitizeUnicode(s.AuthenticationMethod)
	s.RevocationReason = SanitizeUnicode(s.RevocationReason)
}

func (s *Session) PreUpdate() {
	preUpdate(&s.UpdateAt)
	s.DeviceId = SanitizeUnicode(s.DeviceId)
	s.DeviceName = SanitizeUnicode(s.DeviceName)
	s.AuthenticationMethod = SanitizeUnicode(s.AuthenticationMethod)
	s.RevocationReason = SanitizeUnicode(s.RevocationReason)
}

func (s *Session) IsValid() *AppError {
	const where = "Session.IsValid"
	if appErr := validatePersistentFields(where, "session", s.Id, s.CreateAt, s.UpdateAt); appErr != nil {
		return appErr
	}
	details := "id=" + s.Id
	if !IsValidId(s.UserId) {
		return invalidModelError(where, "session", "user_id", "must be a valid identifier", details)
	}
	if !s.ClientType.IsValid() {
		return invalidModelError(where, "session", "client_type", "has an unknown value", details)
	}
	if len(s.DeviceId) > SessionDeviceIdMaxLength {
		return invalidModelError(where, "session", "device_id", "is too long", details)
	}
	if utf8.RuneCountInString(s.DeviceName) > SessionDeviceNameMaxRunes {
		return invalidModelError(where, "session", "device_name", "is too long", details)
	}
	if len(s.AuthenticationMethod) == 0 ||
		len(s.AuthenticationMethod) > SessionAuthenticationMaxLength ||
		!validName.MatchString(s.AuthenticationMethod) {
		return invalidModelError(where, "session", "authentication_method", "has an invalid format", details)
	}
	if !s.AuthenticationStrength.IsValid() {
		return invalidModelError(where, "session", "authentication_strength", "has an unknown value", details)
	}
	if s.AuthenticatedAt <= 0 || s.AuthenticatedAt > s.CreateAt {
		return invalidModelError(where, "session", "authenticated_at", "must be set and not follow create_at", details)
	}
	if s.AuthenticationStrength == AuthenticationMultiFactor &&
		(s.MFACompletedAt < s.AuthenticatedAt || s.MFACompletedAt > s.CreateAt) {
		return invalidModelError(where, "session", "mfa_completed_at", "is inconsistent", details)
	}
	if s.AuthenticationStrength == AuthenticationSingleFactor && s.MFACompletedAt != 0 {
		return invalidModelError(
			where,
			"session",
			"mfa_completed_at",
			"must be empty for single-factor authentication",
			details,
		)
	}
	if s.LastActivityAt < s.CreateAt {
		return invalidModelError(where, "session", "last_activity_at", "must not precede create_at", details)
	}
	if s.ExpiresAt <= s.CreateAt {
		return invalidModelError(where, "session", "expires_at", "must be after create_at", details)
	}
	if s.IdleExpiresAt <= s.LastActivityAt || s.IdleExpiresAt > s.ExpiresAt {
		return invalidModelError(
			where,
			"session",
			"idle_expires_at",
			"must be after last_activity_at and no later than expires_at",
			details,
		)
	}
	if s.RevokedAt != 0 && s.RevokedAt < s.CreateAt {
		return invalidModelError(where, "session", "revoked_at", "must not precede create_at", details)
	}
	if utf8.RuneCountInString(s.RevocationReason) > SessionRevocationMaxRunes {
		return invalidModelError(where, "session", "revocation_reason", "is too long", details)
	}
	if s.RevokedAt == 0 && s.RevocationReason != "" {
		return invalidModelError(
			where,
			"session",
			"revocation_reason",
			"must be empty when the session is not revoked",
			details,
		)
	}
	return nil
}

func (ct SessionClientType) IsValid() bool {
	switch ct {
	case SessionClientDesktop, SessionClientCLI, SessionClientWeb:
		return true
	default:
		return false
	}
}

func (as AuthenticationStrength) IsValid() bool {
	switch as {
	case AuthenticationSingleFactor, AuthenticationMultiFactor:
		return true
	default:
		return false
	}
}

func (s *Session) IsExpiredAt(now int64) bool {
	return s == nil ||
		s.DeleteAt != 0 ||
		s.RevokedAt != 0 ||
		now >= s.IdleExpiresAt ||
		now >= s.ExpiresAt
}

func (s *Session) Auditable() map[string]any {
	fields := auditFields(s.Id, s.CreateAt, s.UpdateAt, s.DeleteAt)
	fields["user_id"] = s.UserId
	fields["client_type"] = s.ClientType
	fields["device_id"] = s.DeviceId
	fields["authentication_method"] = s.AuthenticationMethod
	fields["authentication_strength"] = s.AuthenticationStrength
	fields["authenticated_at"] = s.AuthenticatedAt
	fields["mfa_completed_at"] = s.MFACompletedAt
	fields["last_activity_at"] = s.LastActivityAt
	fields["idle_expires_at"] = s.IdleExpiresAt
	fields["expires_at"] = s.ExpiresAt
	fields["revoked_at"] = s.RevokedAt
	return fields
}

var _ Auditable = (*Session)(nil)
