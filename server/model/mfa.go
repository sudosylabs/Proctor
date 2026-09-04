// ---------------------------------------------------------------------------------------------
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and NOTICE in the server module root for
// license and attribution information.
// SPDX-License-Identifier: Apache-2.0
// ---------------------------------------------------------------------------------------------
//
// Adapted from Mattermost server/public/model/mfa_secret.go and user MFA
// fields. Proctor separates MFA from the user profile, encrypts TOTP secrets,
// stores recovery-code hashes independently, and records replay prevention.

package model

import (
	"encoding/hex"
	"time"
	"unicode/utf8"
)

const (
	MFAEncryptedSecretMaxLength = 4096
	MFAEncryptionKeyIDLength    = 32
	MFARecoveryCodeMaxCount     = 20
)

// MFAState is the durable enrollment phase of an MFA credential.
type MFAState string

const (
	MFAStatePending MFAState = "pending"
	MFAStateActive  MFAState = "active"
)

// MFACredential is one encrypted TOTP enrollment for a user. EncryptedSecret
// and EncryptionKeyID must never be serialized, logged, or audited.
//
// Domain time is UTC time.Time. Optional lifecycle instants use OptionalTime.
type MFACredential struct {
	ID               MFACredentialID
	CreatedAt        time.Time
	UpdatedAt        time.Time
	ArchivedAt       OptionalTime
	UserID           UserID
	State            MFAState
	EncryptedSecret  string `json:"-"`
	EncryptionKeyID  string `json:"-"`
	PendingExpiresAt OptionalTime
	ActivatedAt      OptionalTime
	LastUsedTimeStep int64 `json:"-"`
}

// PrepareCreate applies application-owned lifecycle fields before validation.
func (m *MFACredential) PrepareCreate(id MFACredentialID, at time.Time) {
	if m == nil {
		return
	}
	m.ID = id
	at = TimeUTC(at)
	m.CreatedAt = at
	m.UpdatedAt = at
	m.ArchivedAt = OptionalTime{}
	m.EncryptedSecret = SanitizeUnicode(m.EncryptedSecret)
	m.EncryptionKeyID = SanitizeUnicode(m.EncryptionKeyID)
	if m.PendingExpiresAt.Valid {
		m.PendingExpiresAt = m.PendingExpiresAt.UTC()
	}
	if m.ActivatedAt.Valid {
		m.ActivatedAt = m.ActivatedAt.UTC()
	}
}

// PrepareUpdate applies the application-selected transition time and
// normalizes encryption material fields.
func (m *MFACredential) PrepareUpdate(at time.Time) {
	if m == nil {
		return
	}
	m.UpdatedAt = TimeUTC(at)
	m.EncryptedSecret = SanitizeUnicode(m.EncryptedSecret)
	m.EncryptionKeyID = SanitizeUnicode(m.EncryptionKeyID)
	if m.PendingExpiresAt.Valid {
		m.PendingExpiresAt = m.PendingExpiresAt.UTC()
	}
	if m.ActivatedAt.Valid {
		m.ActivatedAt = m.ActivatedAt.UTC()
	}
}

// Validate checks rehydrated MFA credential state.
func (m *MFACredential) Validate() error {
	const where = "MFACredential.Validate"
	if m == nil {
		return invalidModelError(where, "mfa_credential", "value", "is required", "")
	}
	if !m.ID.IsValid() {
		return invalidModelError(where, "mfa_credential", "id", "must be a valid identifier", "")
	}
	details := "id=" + m.ID.String()
	if m.CreatedAt.IsZero() || m.UpdatedAt.IsZero() {
		return invalidModelError(where, "mfa_credential", "created_at", "must be set", details)
	}
	if m.UpdatedAt.Before(m.CreatedAt) {
		return invalidModelError(where, "mfa_credential", "updated_at", "must not precede created_at", details)
	}
	if !m.UserID.IsValid() {
		return invalidModelError(where, "mfa_credential", "user_id", "must be a valid identifier", details)
	}
	if utf8.RuneCountInString(m.EncryptedSecret) == 0 ||
		len(m.EncryptedSecret) > MFAEncryptedSecretMaxLength {
		return invalidModelError(where, "mfa_credential", "encrypted_secret", "has an invalid length", details)
	}
	decodedKeyID, keyIDErr := hex.DecodeString(m.EncryptionKeyID)
	if len(m.EncryptionKeyID) != MFAEncryptionKeyIDLength || keyIDErr != nil ||
		hex.EncodeToString(decodedKeyID) != m.EncryptionKeyID {
		return invalidModelError(where, "mfa_credential", "encryption_key_id", "has an invalid format", details)
	}
	if m.ArchivedAt.Valid && m.ArchivedAt.Time.Before(m.CreatedAt) {
		return invalidModelError(where, "mfa_credential", "archived_at", "must not precede created_at", details)
	}
	switch m.State {
	case MFAStatePending:
		if !m.PendingExpiresAt.Valid || !m.PendingExpiresAt.Time.After(m.CreatedAt) ||
			m.ActivatedAt.Valid || m.LastUsedTimeStep != 0 {
			return invalidModelError(where, "mfa_credential", "state", "has inconsistent pending fields", details)
		}
	case MFAStateActive:
		if m.PendingExpiresAt.Valid ||
			!m.ActivatedAt.Valid || m.ActivatedAt.Time.Before(m.CreatedAt) ||
			m.LastUsedTimeStep <= 0 {
			return invalidModelError(where, "mfa_credential", "state", "has inconsistent active fields", details)
		}
	default:
		return invalidModelError(where, "mfa_credential", "state", "has an unknown value", details)
	}
	return nil
}

// IsPendingAt reports whether the credential is an unarchived, unexpired
// pending enrollment at now.
func (m *MFACredential) IsPendingAt(now time.Time) bool {
	if m == nil || m.ArchivedAt.Valid || m.State != MFAStatePending {
		return false
	}
	now = TimeUTC(now)
	return m.PendingExpiresAt.Valid && now.Before(m.PendingExpiresAt.Time)
}

// IsActive reports whether the credential is an unarchived active enrollment.
func (m *MFACredential) IsActive() bool {
	return m != nil && !m.ArchivedAt.Valid && m.State == MFAStateActive
}

// Auditable returns a deliberately safe audit projection. Encrypted secrets
// and key identifiers are never included.
func (m *MFACredential) Auditable() map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":                 m.ID.String(),
		"created_at":         MillisFromTime(m.CreatedAt),
		"updated_at":         MillisFromTime(m.UpdatedAt),
		"archived_at":        m.ArchivedAt.Millis(),
		"user_id":            m.UserID.String(),
		"state":              m.State,
		"pending_expires_at": m.PendingExpiresAt.Millis(),
		"activated_at":       m.ActivatedAt.Millis(),
	}
}

// MFARecoveryCode is one hashed single-use recovery credential. CodeHash is
// deliberately excluded from JSON. Soft archive uses ArchivedAt.
type MFARecoveryCode struct {
	ID         MFARecoveryCodeID
	CreatedAt  time.Time
	UpdatedAt  time.Time
	ArchivedAt OptionalTime
	UserID     UserID
	CodeHash   string `json:"-"`
	ConsumedAt OptionalTime
}

// PrepareCreate applies application-owned lifecycle fields before validation.
func (c *MFARecoveryCode) PrepareCreate(id MFARecoveryCodeID, at time.Time) {
	if c == nil {
		return
	}
	c.ID = id
	at = TimeUTC(at)
	c.CreatedAt = at
	c.UpdatedAt = at
	c.ArchivedAt = OptionalTime{}
	if c.ConsumedAt.Valid {
		c.ConsumedAt = c.ConsumedAt.UTC()
	}
}

// PrepareUpdate applies the application-selected transition time.
func (c *MFARecoveryCode) PrepareUpdate(at time.Time) {
	if c == nil {
		return
	}
	c.UpdatedAt = TimeUTC(at)
	if c.ConsumedAt.Valid {
		c.ConsumedAt = c.ConsumedAt.UTC()
	}
}

// Validate checks rehydrated MFA recovery-code state.
func (c *MFARecoveryCode) Validate() error {
	const where = "MFARecoveryCode.Validate"
	if c == nil {
		return invalidModelError(where, "mfa_recovery_code", "value", "is required", "")
	}
	if !c.ID.IsValid() {
		return invalidModelError(where, "mfa_recovery_code", "id", "must be a valid identifier", "")
	}
	details := "id=" + c.ID.String()
	if c.CreatedAt.IsZero() || c.UpdatedAt.IsZero() {
		return invalidModelError(where, "mfa_recovery_code", "created_at", "must be set", details)
	}
	if c.UpdatedAt.Before(c.CreatedAt) {
		return invalidModelError(where, "mfa_recovery_code", "updated_at", "must not precede created_at", details)
	}
	if !c.UserID.IsValid() {
		return invalidModelError(where, "mfa_recovery_code", "user_id", "must be a valid identifier", details)
	}
	if !IsValidTokenHash(c.CodeHash) {
		return invalidModelError(where, "mfa_recovery_code", "code_hash", "has an invalid format", details)
	}
	if c.ConsumedAt.Valid && c.ConsumedAt.Time.Before(c.CreatedAt) {
		return invalidModelError(where, "mfa_recovery_code", "consumed_at", "must not precede created_at", details)
	}
	if c.ArchivedAt.Valid && c.ArchivedAt.Time.Before(c.CreatedAt) {
		return invalidModelError(where, "mfa_recovery_code", "archived_at", "must not precede created_at", details)
	}
	return nil
}

// Auditable returns a deliberately safe audit projection. Code hashes are
// never included.
func (c *MFARecoveryCode) Auditable() map[string]any {
	if c == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":          c.ID.String(),
		"created_at":  MillisFromTime(c.CreatedAt),
		"updated_at":  MillisFromTime(c.UpdatedAt),
		"archived_at": c.ArchivedAt.Millis(),
		"user_id":     c.UserID.String(),
		"consumed_at": c.ConsumedAt.Millis(),
	}
}

var _ Auditable = (*MFACredential)(nil)
var _ Auditable = (*MFARecoveryCode)(nil)
