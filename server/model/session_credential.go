// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import "time"

type SessionCredentialKind string

const (
	SessionCredentialAccess  SessionCredentialKind = "access"
	SessionCredentialRefresh SessionCredentialKind = "refresh"
)

// SessionCredential is one hashed opaque credential for a Session. Refresh
// credentials form a rotation family: UsedAt, ParentID, and ReplacedByID allow
// the application to detect replay and revoke the entire family.
//
// Domain time is UTC time.Time. Optional lifecycle instants use OptionalTime.
// Soft archive uses ArchivedAt (legacy delete_at). TokenHash is excluded from
// JSON and must never be logged or audited.
type SessionCredential struct {
	ID           SessionCredentialID
	CreatedAt    time.Time
	UpdatedAt    time.Time
	ArchivedAt   OptionalTime
	SessionID    SessionID
	Kind         SessionCredentialKind
	TokenHash    string `json:"-"`
	FamilyID     string // valid ID for refresh credentials; empty for access
	ParentID     SessionCredentialID
	ReplacedByID SessionCredentialID
	ExpiresAt    time.Time
	UsedAt       OptionalTime
	RevokedAt    OptionalTime
}

// PrepareCreate applies application-owned lifecycle fields before validation.
// Refresh credentials without a family receive a fresh family identifier.
func (sc *SessionCredential) PrepareCreate(id SessionCredentialID, at time.Time) {
	if sc == nil {
		return
	}
	sc.ID = id
	at = TimeUTC(at)
	sc.CreatedAt = at
	sc.UpdatedAt = at
	sc.ArchivedAt = OptionalTime{}
	sc.ExpiresAt = TimeUTC(sc.ExpiresAt)
	if sc.Kind == SessionCredentialRefresh && sc.FamilyID == "" {
		sc.FamilyID = NewId()
	}
	if sc.UsedAt.Valid {
		sc.UsedAt = sc.UsedAt.UTC()
	}
	if sc.RevokedAt.Valid {
		sc.RevokedAt = sc.RevokedAt.UTC()
	}
}

// PrepareUpdate applies the application-selected transition time.
func (sc *SessionCredential) PrepareUpdate(at time.Time) {
	if sc == nil {
		return
	}
	sc.UpdatedAt = TimeUTC(at)
}

// Validate checks rehydrated session-credential state.
func (sc *SessionCredential) Validate() error {
	const where = "SessionCredential.Validate"
	if sc == nil {
		return invalidModelError(where, "session_credential", "value", "is required", "")
	}
	if !sc.ID.IsValid() {
		return invalidModelError(where, "session_credential", "id", "must be a valid identifier", "")
	}
	details := "id=" + sc.ID.String()
	if sc.CreatedAt.IsZero() || sc.UpdatedAt.IsZero() {
		return invalidModelError(where, "session_credential", "created_at", "must be set", details)
	}
	if sc.UpdatedAt.Before(sc.CreatedAt) {
		return invalidModelError(where, "session_credential", "updated_at", "must not precede created_at", details)
	}
	if !sc.SessionID.IsValid() {
		return invalidModelError(where, "session_credential", "session_id", "must be a valid identifier", details)
	}
	if !sc.Kind.IsValid() {
		return invalidModelError(where, "session_credential", "kind", "has an unknown value", details)
	}
	if !IsValidTokenHash(sc.TokenHash) {
		return invalidModelError(where, "session_credential", "token_hash", "has an invalid format", details)
	}
	if !sc.ExpiresAt.After(sc.CreatedAt) {
		return invalidModelError(where, "session_credential", "expires_at", "must be after create_at", details)
	}
	if sc.Kind == SessionCredentialAccess {
		if sc.FamilyID != "" || !sc.ParentID.IsZero() || !sc.ReplacedByID.IsZero() || sc.UsedAt.Valid {
			return invalidModelError(
				where,
				"session_credential",
				"kind",
				"access credentials cannot contain refresh rotation state",
				details,
			)
		}
	} else {
		if !IsValidId(sc.FamilyID) {
			return invalidModelError(
				where,
				"session_credential",
				"family_id",
				"must be a valid identifier for refresh credentials",
				details,
			)
		}
		if !sc.ParentID.IsZero() && !sc.ParentID.IsValid() {
			return invalidModelError(where, "session_credential", "parent_id", "must be a valid identifier", details)
		}
		if sc.ParentID == sc.ID {
			return invalidModelError(
				where,
				"session_credential",
				"parent_id",
				"must not reference the credential itself",
				details,
			)
		}
		if !sc.ReplacedByID.IsZero() && !sc.ReplacedByID.IsValid() {
			return invalidModelError(
				where,
				"session_credential",
				"replaced_by_id",
				"must be a valid identifier",
				details,
			)
		}
		if sc.ReplacedByID == sc.ID {
			return invalidModelError(
				where,
				"session_credential",
				"replaced_by_id",
				"must not reference the credential itself",
				details,
			)
		}
		if !sc.ReplacedByID.IsZero() && !sc.UsedAt.Valid {
			return invalidModelError(
				where,
				"session_credential",
				"replaced_by_id",
				"requires used_at to be set",
				details,
			)
		}
	}
	if sc.UsedAt.Valid &&
		(sc.UsedAt.Time.Before(sc.CreatedAt) || !sc.UsedAt.Time.Before(sc.ExpiresAt)) {
		return invalidModelError(
			where,
			"session_credential",
			"used_at",
			"must be within the credential lifetime",
			details,
		)
	}
	if sc.ArchivedAt.Valid && sc.ArchivedAt.Time.Before(sc.CreatedAt) {
		return invalidModelError(where, "session_credential", "archived_at", "must not precede created_at", details)
	}
	if sc.RevokedAt.Valid && sc.RevokedAt.Time.Before(sc.CreatedAt) {
		return invalidModelError(where, "session_credential", "revoked_at", "must not precede create_at", details)
	}
	return nil
}

func (k SessionCredentialKind) IsValid() bool {
	return k == SessionCredentialAccess || k == SessionCredentialRefresh
}

// IsExpiredAt reports whether the credential is archived, revoked, or expired.
func (sc *SessionCredential) IsExpiredAt(now time.Time) bool {
	if sc == nil {
		return true
	}
	now = TimeUTC(now)
	return sc.ArchivedAt.Valid || sc.RevokedAt.Valid || !now.Before(sc.ExpiresAt)
}

// Auditable returns a deliberately safe audit projection. The token hash is
// never included.
func (sc *SessionCredential) Auditable() map[string]any {
	if sc == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":             sc.ID.String(),
		"created_at":     MillisFromTime(sc.CreatedAt),
		"updated_at":     MillisFromTime(sc.UpdatedAt),
		"archived_at":    sc.ArchivedAt.Millis(),
		"delete_at":      sc.ArchivedAt.Millis(),
		"session_id":     sc.SessionID.String(),
		"kind":           sc.Kind,
		"family_id":      sc.FamilyID,
		"parent_id":      sc.ParentID.String(),
		"replaced_by_id": sc.ReplacedByID.String(),
		"expires_at":     MillisFromTime(sc.ExpiresAt),
		"used_at":        sc.UsedAt.Millis(),
		"revoked_at":     sc.RevokedAt.Millis(),
	}
}

var _ Auditable = (*SessionCredential)(nil)
