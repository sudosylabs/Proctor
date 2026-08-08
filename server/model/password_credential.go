// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import "time"

const PasswordHashMaxLength = 1024

// IsValidPasswordHash reports whether value is a non-empty encoded password
// hash within the storage bound.
func IsValidPasswordHash(value string) bool {
	return len(value) > 0 && len(value) <= PasswordHashMaxLength
}

// PasswordCredential contains only an established password hasher's encoded
// output. Plaintext passwords must never be assigned to this model. Keeping it
// separate permits external-only accounts and future credential replacement
// without exposing password material through User serialization.
//
// PasswordHash is deliberately excluded from JSON. Soft archive uses
// ArchivedAt (legacy delete_at).
type PasswordCredential struct {
	ID                PasswordCredentialID
	CreatedAt         time.Time
	UpdatedAt         time.Time
	ArchivedAt        OptionalTime
	UserID            UserID
	PasswordHash      string `json:"-"`
	PasswordChangedAt time.Time
}

// PrepareCreate applies application-owned lifecycle fields before validation.
func (pc *PasswordCredential) PrepareCreate(id PasswordCredentialID, at time.Time) {
	if pc == nil {
		return
	}
	pc.ID = id
	at = TimeUTC(at)
	pc.CreatedAt = at
	pc.UpdatedAt = at
	pc.ArchivedAt = OptionalTime{}
	if pc.PasswordChangedAt.IsZero() {
		pc.PasswordChangedAt = at
	} else {
		pc.PasswordChangedAt = TimeUTC(pc.PasswordChangedAt)
	}
}

// PrepareUpdate applies the application-selected transition time.
func (pc *PasswordCredential) PrepareUpdate(at time.Time) {
	if pc == nil {
		return
	}
	pc.UpdatedAt = TimeUTC(at)
}

// Validate checks rehydrated password-credential state.
func (pc *PasswordCredential) Validate() error {
	const where = "PasswordCredential.Validate"
	if pc == nil {
		return invalidModelError(where, "password_credential", "value", "is required", "")
	}
	if !pc.ID.IsValid() {
		return invalidModelError(where, "password_credential", "id", "must be a valid identifier", "")
	}
	details := "id=" + pc.ID.String()
	if pc.CreatedAt.IsZero() || pc.UpdatedAt.IsZero() {
		return invalidModelError(where, "password_credential", "created_at", "must be set", details)
	}
	if pc.UpdatedAt.Before(pc.CreatedAt) {
		return invalidModelError(where, "password_credential", "updated_at", "must not precede created_at", details)
	}
	if !pc.UserID.IsValid() {
		return invalidModelError(where, "password_credential", "user_id", "must be a valid identifier", details)
	}
	if !IsValidPasswordHash(pc.PasswordHash) {
		return invalidModelError(where, "password_credential", "password_hash", "has an invalid length", details)
	}
	if pc.PasswordChangedAt.IsZero() || pc.PasswordChangedAt.Before(pc.CreatedAt) {
		return invalidModelError(
			where,
			"password_credential",
			"password_changed_at",
			"must not precede create_at",
			details,
		)
	}
	if pc.ArchivedAt.Valid && pc.ArchivedAt.Time.Before(pc.CreatedAt) {
		return invalidModelError(where, "password_credential", "archived_at", "must not precede created_at", details)
	}
	return nil
}

// Auditable returns a deliberately safe audit projection. The password hash is
// never included.
func (pc *PasswordCredential) Auditable() map[string]any {
	if pc == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":                  pc.ID.String(),
		"created_at":          MillisFromTime(pc.CreatedAt),
		"updated_at":          MillisFromTime(pc.UpdatedAt),
		"archived_at":         pc.ArchivedAt.Millis(),
		"delete_at":           pc.ArchivedAt.Millis(),
		"user_id":             pc.UserID.String(),
		"password_changed_at": MillisFromTime(pc.PasswordChangedAt),
	}
}

var _ Auditable = (*PasswordCredential)(nil)
