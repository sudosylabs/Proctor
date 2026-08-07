// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/public/model/mfa_secret.go and user MFA
// fields. Proctor separates MFA from the user profile, encrypts TOTP secrets,
// stores recovery-code hashes independently, and records replay prevention.

package model

import "unicode/utf8"

const (
	MFAEncryptedSecretMaxLength = 4096
	MFAEncryptionKeyIDLength    = 16
	MFARecoveryCodeMaxCount     = 20
)

type MFAState string

const (
	MFAStatePending MFAState = "pending"
	MFAStateActive  MFAState = "active"
)

type MFACredential struct {
	Id               string   `json:"id"`
	CreateAt         int64    `json:"create_at"`
	UpdateAt         int64    `json:"update_at"`
	DeleteAt         int64    `json:"delete_at"`
	UserId           string   `json:"user_id"`
	State            MFAState `json:"state"`
	EncryptedSecret  string   `json:"-"`
	EncryptionKeyId  string   `json:"-"`
	PendingExpiresAt int64    `json:"pending_expires_at,omitempty"`
	EnabledAt        int64    `json:"enabled_at,omitempty"`
	LastUsedTimeStep int64    `json:"-"`
}

func (m *MFACredential) PreSave() {
	preSave(&m.Id, &m.CreateAt, &m.UpdateAt)
	m.EncryptedSecret = SanitizeUnicode(m.EncryptedSecret)
	m.EncryptionKeyId = SanitizeUnicode(m.EncryptionKeyId)
}

func (m *MFACredential) PreUpdate() {
	preUpdate(&m.UpdateAt)
	m.EncryptedSecret = SanitizeUnicode(m.EncryptedSecret)
	m.EncryptionKeyId = SanitizeUnicode(m.EncryptionKeyId)
}

func (m *MFACredential) IsValid() error {
	const where = "MFACredential.IsValid"
	if appErr := validatePersistentFields(
		where, "mfa_credential", m.Id, m.CreateAt, m.UpdateAt,
	); appErr != nil {
		return appErr
	}
	details := "id=" + m.Id
	if !IsValidId(m.UserId) {
		return invalidModelError(where, "mfa_credential", "user_id", "must be a valid identifier", details)
	}
	if utf8.RuneCountInString(m.EncryptedSecret) == 0 ||
		len(m.EncryptedSecret) > MFAEncryptedSecretMaxLength {
		return invalidModelError(where, "mfa_credential", "encrypted_secret", "has an invalid length", details)
	}
	if len(m.EncryptionKeyId) != MFAEncryptionKeyIDLength {
		return invalidModelError(where, "mfa_credential", "encryption_key_id", "has an invalid format", details)
	}
	switch m.State {
	case MFAStatePending:
		if m.PendingExpiresAt <= m.CreateAt || m.EnabledAt != 0 || m.LastUsedTimeStep != 0 {
			return invalidModelError(where, "mfa_credential", "state", "has inconsistent pending fields", details)
		}
	case MFAStateActive:
		if m.PendingExpiresAt != 0 || m.EnabledAt < m.CreateAt || m.LastUsedTimeStep <= 0 {
			return invalidModelError(where, "mfa_credential", "state", "has inconsistent active fields", details)
		}
	default:
		return invalidModelError(where, "mfa_credential", "state", "has an unknown value", details)
	}
	return nil
}

func (m *MFACredential) IsPendingAt(now int64) bool {
	return m != nil && m.DeleteAt == 0 && m.State == MFAStatePending &&
		now < m.PendingExpiresAt
}

func (m *MFACredential) IsActive() bool {
	return m != nil && m.DeleteAt == 0 && m.State == MFAStateActive
}

func (m *MFACredential) Auditable() map[string]any {
	fields := auditFields(m.Id, m.CreateAt, m.UpdateAt, m.DeleteAt)
	fields["user_id"] = m.UserId
	fields["state"] = m.State
	fields["pending_expires_at"] = m.PendingExpiresAt
	fields["enabled_at"] = m.EnabledAt
	return fields
}

type MFARecoveryCode struct {
	Id       string `json:"id"`
	CreateAt int64  `json:"create_at"`
	UpdateAt int64  `json:"update_at"`
	DeleteAt int64  `json:"delete_at"`
	UserId   string `json:"user_id"`
	CodeHash string `json:"-"`
	UsedAt   int64  `json:"used_at,omitempty"`
}

func (c *MFARecoveryCode) PreSave() {
	preSave(&c.Id, &c.CreateAt, &c.UpdateAt)
}

func (c *MFARecoveryCode) IsValid() error {
	const where = "MFARecoveryCode.IsValid"
	if appErr := validatePersistentFields(
		where, "mfa_recovery_code", c.Id, c.CreateAt, c.UpdateAt,
	); appErr != nil {
		return appErr
	}
	details := "id=" + c.Id
	if !IsValidId(c.UserId) {
		return invalidModelError(where, "mfa_recovery_code", "user_id", "must be a valid identifier", details)
	}
	if !IsValidTokenHash(c.CodeHash) {
		return invalidModelError(where, "mfa_recovery_code", "code_hash", "has an invalid format", details)
	}
	if c.UsedAt != 0 && c.UsedAt < c.CreateAt {
		return invalidModelError(where, "mfa_recovery_code", "used_at", "must not precede create_at", details)
	}
	return nil
}

func (c *MFARecoveryCode) Auditable() map[string]any {
	fields := auditFields(c.Id, c.CreateAt, c.UpdateAt, c.DeleteAt)
	fields["user_id"] = c.UserId
	fields["used_at"] = c.UsedAt
	return fields
}

type MFASetup struct {
	Secret          string `json:"secret"`
	ProvisioningURI string `json:"provisioning_uri"`
	ExpiresAt       int64  `json:"expires_at"`
}

type MFAActivation struct {
	RecoveryCodes []string `json:"recovery_codes"`
}

type MFAStatus struct {
	Enabled                bool  `json:"enabled"`
	Pending                bool  `json:"pending"`
	PendingExpiresAt       int64 `json:"pending_expires_at,omitempty"`
	RecoveryCodesRemaining int   `json:"recovery_codes_remaining"`
}

var _ Auditable = (*MFACredential)(nil)
var _ Auditable = (*MFARecoveryCode)(nil)
