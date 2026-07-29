// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

type SessionCredentialKind string

const (
	SessionCredentialAccess  SessionCredentialKind = "access"
	SessionCredentialRefresh SessionCredentialKind = "refresh"
)

// SessionCredential is one hashed opaque credential for a Session. Refresh
// credentials form a rotation family: UsedAt, ParentId, and ReplacedById allow
// the application to detect replay and revoke the entire family.
type SessionCredential struct {
	Id           string                `json:"id"`
	CreateAt     int64                 `json:"create_at"`
	UpdateAt     int64                 `json:"update_at"`
	DeleteAt     int64                 `json:"delete_at"`
	SessionId    string                `json:"session_id"`
	Kind         SessionCredentialKind `json:"kind"`
	TokenHash    string                `json:"-"`
	FamilyId     string                `json:"family_id,omitempty"`
	ParentId     string                `json:"parent_id,omitempty"`
	ReplacedById string                `json:"replaced_by_id,omitempty"`
	ExpiresAt    int64                 `json:"expires_at"`
	UsedAt       int64                 `json:"used_at,omitempty"`
	RevokedAt    int64                 `json:"revoked_at,omitempty"`
}

func (sc *SessionCredential) PreSave() {
	preSave(&sc.Id, &sc.CreateAt, &sc.UpdateAt)
	if sc.Kind == SessionCredentialRefresh && sc.FamilyId == "" {
		sc.FamilyId = NewId()
	}
}

func (sc *SessionCredential) PreUpdate() {
	preUpdate(&sc.UpdateAt)
}

func (sc *SessionCredential) IsValid() *AppError {
	const where = "SessionCredential.IsValid"
	if appErr := validatePersistentFields(
		where,
		"session_credential",
		sc.Id,
		sc.CreateAt,
		sc.UpdateAt,
	); appErr != nil {
		return appErr
	}
	details := "id=" + sc.Id
	if !IsValidId(sc.SessionId) {
		return invalidModelError(where, "session_credential", "session_id", "must be a valid identifier", details)
	}
	if !sc.Kind.IsValid() {
		return invalidModelError(where, "session_credential", "kind", "has an unknown value", details)
	}
	if !IsValidTokenHash(sc.TokenHash) {
		return invalidModelError(where, "session_credential", "token_hash", "has an invalid format", details)
	}
	if sc.ExpiresAt <= sc.CreateAt {
		return invalidModelError(where, "session_credential", "expires_at", "must be after create_at", details)
	}
	if sc.Kind == SessionCredentialAccess {
		if sc.FamilyId != "" || sc.ParentId != "" || sc.ReplacedById != "" || sc.UsedAt != 0 {
			return invalidModelError(
				where,
				"session_credential",
				"kind",
				"access credentials cannot contain refresh rotation state",
				details,
			)
		}
	} else {
		if !IsValidId(sc.FamilyId) {
			return invalidModelError(
				where,
				"session_credential",
				"family_id",
				"must be a valid identifier for refresh credentials",
				details,
			)
		}
		if sc.ParentId != "" && !IsValidId(sc.ParentId) {
			return invalidModelError(where, "session_credential", "parent_id", "must be a valid identifier", details)
		}
		if sc.ParentId == sc.Id {
			return invalidModelError(
				where,
				"session_credential",
				"parent_id",
				"must not reference the credential itself",
				details,
			)
		}
		if sc.ReplacedById != "" && !IsValidId(sc.ReplacedById) {
			return invalidModelError(
				where,
				"session_credential",
				"replaced_by_id",
				"must be a valid identifier",
				details,
			)
		}
		if sc.ReplacedById == sc.Id {
			return invalidModelError(
				where,
				"session_credential",
				"replaced_by_id",
				"must not reference the credential itself",
				details,
			)
		}
		if sc.ReplacedById != "" && sc.UsedAt == 0 {
			return invalidModelError(
				where,
				"session_credential",
				"replaced_by_id",
				"requires used_at to be set",
				details,
			)
		}
	}
	if sc.UsedAt != 0 && (sc.UsedAt < sc.CreateAt || sc.UsedAt >= sc.ExpiresAt) {
		return invalidModelError(
			where,
			"session_credential",
			"used_at",
			"must be within the credential lifetime",
			details,
		)
	}
	if sc.RevokedAt != 0 && sc.RevokedAt < sc.CreateAt {
		return invalidModelError(where, "session_credential", "revoked_at", "must not precede create_at", details)
	}
	return nil
}

func (k SessionCredentialKind) IsValid() bool {
	return k == SessionCredentialAccess || k == SessionCredentialRefresh
}

func (sc *SessionCredential) IsExpiredAt(now int64) bool {
	return sc == nil || sc.DeleteAt != 0 || sc.RevokedAt != 0 || now >= sc.ExpiresAt
}

func (sc *SessionCredential) Auditable() map[string]any {
	fields := auditFields(sc.Id, sc.CreateAt, sc.UpdateAt, sc.DeleteAt)
	fields["session_id"] = sc.SessionId
	fields["kind"] = sc.Kind
	fields["family_id"] = sc.FamilyId
	fields["parent_id"] = sc.ParentId
	fields["replaced_by_id"] = sc.ReplacedById
	fields["expires_at"] = sc.ExpiresAt
	fields["used_at"] = sc.UsedAt
	fields["revoked_at"] = sc.RevokedAt
	return fields
}

var _ Auditable = (*SessionCredential)(nil)
