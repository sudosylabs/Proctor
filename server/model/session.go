// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/public/model/session.go. Proctor separates
// hashed credentials from the session and never stores authorization roles in
// the session snapshot.

package model

import (
	"time"
	"unicode/utf8"
)

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
//
// Domain time is UTC time.Time. Optional lifecycle instants use OptionalTime.
// Soft archive uses the explicit optional ArchivedAt instant.
// AuthenticationProviderID and ExternalIdentityID retain the exact immutable
// provider path that established an external Session. Both are empty for a
// local Session. AuthenticationMethod describes the local method or protocol.
type Session struct {
	ID                       SessionID
	CreatedAt                time.Time
	UpdatedAt                time.Time
	ArchivedAt               OptionalTime
	UserID                   UserID
	ClientType               SessionClientType
	DeviceID                 string
	DeviceName               string
	AuthenticationMethod     string
	AuthenticationProviderID string
	ExternalIdentityID       ExternalIdentityID
	AuthenticationStrength   AuthenticationStrength
	AuthenticatedAt          time.Time
	MFACompletedAt           OptionalTime
	LastActivityAt           time.Time
	IdleExpiresAt            time.Time
	ExpiresAt                time.Time
	RevokedAt                OptionalTime
	RevocationReason         string
}

// PrepareCreate applies application-owned lifecycle fields before validation.
func (s *Session) PrepareCreate(id SessionID, at time.Time) {
	if s == nil {
		return
	}
	s.ID = id
	at = TimeUTC(at)
	s.CreatedAt = at
	s.UpdatedAt = at
	s.ArchivedAt = OptionalTime{}
	if s.AuthenticatedAt.IsZero() {
		s.AuthenticatedAt = at
	} else {
		s.AuthenticatedAt = TimeUTC(s.AuthenticatedAt)
	}
	if s.LastActivityAt.IsZero() || s.LastActivityAt.Before(at) {
		s.LastActivityAt = at
	} else {
		s.LastActivityAt = TimeUTC(s.LastActivityAt)
	}
	s.IdleExpiresAt = TimeUTC(s.IdleExpiresAt)
	s.ExpiresAt = TimeUTC(s.ExpiresAt)
	if s.MFACompletedAt.Valid {
		s.MFACompletedAt = s.MFACompletedAt.UTC()
	}
	if s.RevokedAt.Valid {
		s.RevokedAt = s.RevokedAt.UTC()
	}
	s.DeviceID = SanitizeUnicode(s.DeviceID)
	s.DeviceName = SanitizeUnicode(s.DeviceName)
	s.AuthenticationMethod = SanitizeUnicode(s.AuthenticationMethod)
	s.AuthenticationProviderID = SanitizeUnicode(s.AuthenticationProviderID)
	s.RevocationReason = SanitizeUnicode(s.RevocationReason)
}

// PrepareUpdate applies the application-selected transition time and normalizes
// mutable metadata fields.
func (s *Session) PrepareUpdate(at time.Time) {
	if s == nil {
		return
	}
	s.UpdatedAt = TimeUTC(at)
	s.DeviceID = SanitizeUnicode(s.DeviceID)
	s.DeviceName = SanitizeUnicode(s.DeviceName)
	s.AuthenticationMethod = SanitizeUnicode(s.AuthenticationMethod)
	s.AuthenticationProviderID = SanitizeUnicode(s.AuthenticationProviderID)
	s.RevocationReason = SanitizeUnicode(s.RevocationReason)
}

// Validate checks rehydrated session state.
func (s *Session) Validate() error {
	const where = "Session.Validate"
	if s == nil {
		return invalidModelError(where, "session", "value", "is required", "")
	}
	if !s.ID.IsValid() {
		return invalidModelError(where, "session", "id", "must be a valid identifier", "")
	}
	details := "id=" + s.ID.String()
	if s.CreatedAt.IsZero() || s.UpdatedAt.IsZero() {
		return invalidModelError(where, "session", "created_at", "must be set", details)
	}
	if s.UpdatedAt.Before(s.CreatedAt) {
		return invalidModelError(where, "session", "updated_at", "must not precede created_at", details)
	}
	if !s.UserID.IsValid() {
		return invalidModelError(where, "session", "user_id", "must be a valid identifier", details)
	}
	if !s.ClientType.IsValid() {
		return invalidModelError(where, "session", "client_type", "has an unknown value", details)
	}
	if len(s.DeviceID) > SessionDeviceIdMaxLength {
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
	if s.AuthenticationProviderID == "" && s.AuthenticationMethod != "password" {
		return invalidModelError(where, "session", "authentication_provider_id", "is required for an external authentication method", details)
	}
	if s.AuthenticationProviderID != "" && !IsValidIdentityProviderID(s.AuthenticationProviderID) {
		return invalidModelError(where, "session", "authentication_provider_id", "has an invalid format", details)
	}
	if s.AuthenticationProviderID == "" && !s.ExternalIdentityID.IsZero() {
		return invalidModelError(where, "session", "external_identity_id", "must be empty for local authentication", details)
	}
	if s.AuthenticationProviderID != "" && !s.ExternalIdentityID.IsValid() {
		return invalidModelError(where, "session", "external_identity_id", "is required for external authentication", details)
	}
	if !s.AuthenticationStrength.IsValid() {
		return invalidModelError(where, "session", "authentication_strength", "has an unknown value", details)
	}
	if s.AuthenticatedAt.IsZero() || s.AuthenticatedAt.After(s.CreatedAt) {
		return invalidModelError(where, "session", "authenticated_at", "must be set and not follow create_at", details)
	}
	if s.AuthenticationStrength == AuthenticationMultiFactor {
		if !s.MFACompletedAt.Valid ||
			s.MFACompletedAt.Time.Before(s.AuthenticatedAt) ||
			s.MFACompletedAt.Time.After(s.UpdatedAt) {
			return invalidModelError(where, "session", "mfa_completed_at", "is inconsistent", details)
		}
	}
	if s.AuthenticationStrength == AuthenticationSingleFactor && s.MFACompletedAt.Valid {
		return invalidModelError(
			where,
			"session",
			"mfa_completed_at",
			"must be empty for single-factor authentication",
			details,
		)
	}
	if s.LastActivityAt.Before(s.CreatedAt) {
		return invalidModelError(where, "session", "last_activity_at", "must not precede create_at", details)
	}
	if !s.ExpiresAt.After(s.CreatedAt) {
		return invalidModelError(where, "session", "expires_at", "must be after create_at", details)
	}
	if !s.IdleExpiresAt.After(s.LastActivityAt) || s.IdleExpiresAt.After(s.ExpiresAt) {
		return invalidModelError(
			where,
			"session",
			"idle_expires_at",
			"must be after last_activity_at and no later than expires_at",
			details,
		)
	}
	if s.ArchivedAt.Valid && s.ArchivedAt.Time.Before(s.CreatedAt) {
		return invalidModelError(where, "session", "archived_at", "must not precede created_at", details)
	}
	if s.RevokedAt.Valid && s.RevokedAt.Time.Before(s.CreatedAt) {
		return invalidModelError(where, "session", "revoked_at", "must not precede create_at", details)
	}
	if utf8.RuneCountInString(s.RevocationReason) > SessionRevocationMaxRunes {
		return invalidModelError(where, "session", "revocation_reason", "is too long", details)
	}
	if !s.RevokedAt.Valid && s.RevocationReason != "" {
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

// IsExpiredAt reports whether the session is archived, revoked, idle-expired,
// or absolutely expired at now.
func (s *Session) IsExpiredAt(now time.Time) bool {
	if s == nil {
		return true
	}
	now = TimeUTC(now)
	return s.ArchivedAt.Valid ||
		s.RevokedAt.Valid ||
		!now.Before(s.IdleExpiresAt) ||
		!now.Before(s.ExpiresAt)
}

// Auditable returns a deliberately safe audit projection.
func (s *Session) Auditable() map[string]any {
	if s == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":                         s.ID.String(),
		"created_at":                 MillisFromTime(s.CreatedAt),
		"updated_at":                 MillisFromTime(s.UpdatedAt),
		"archived_at":                s.ArchivedAt.Millis(),
		"user_id":                    s.UserID.String(),
		"client_type":                s.ClientType,
		"device_id":                  s.DeviceID,
		"authentication_method":      s.AuthenticationMethod,
		"authentication_provider_id": s.AuthenticationProviderID,
		"external_identity_id":       s.ExternalIdentityID.String(),
		"authentication_strength":    s.AuthenticationStrength,
		"authenticated_at":           MillisFromTime(s.AuthenticatedAt),
		"mfa_completed_at":           s.MFACompletedAt.Millis(),
		"last_activity_at":           MillisFromTime(s.LastActivityAt),
		"idle_expires_at":            MillisFromTime(s.IdleExpiresAt),
		"expires_at":                 MillisFromTime(s.ExpiresAt),
		"revoked_at":                 s.RevokedAt.Millis(),
	}
}

var _ Auditable = (*Session)(nil)
