// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/public/model/user_access_token.go. Proctor
// stores only a token hash and requires expiry and explicit scopes.

package model

import (
	"time"
	"unicode/utf8"
)

const (
	PersonalAccessTokenDescriptionMaxRunes = 255
	PersonalAccessTokenScopeMaxCount       = 128
)

// PersonalAccessToken is a long-lived, explicitly scoped credential for human
// CLI and automation use. It is not a Session and never stores the raw token.
// AcademicUnitID optionally narrows every scope to one unit and its authorized
// descendants.
//
// Domain time is UTC time.Time. Optional lifecycle instants use OptionalTime.
// Soft archive uses ArchivedAt (legacy delete_at). TokenHash is excluded from
// JSON and must never be logged or audited.
type PersonalAccessToken struct {
	ID             PersonalAccessTokenID
	CreatedAt      time.Time
	UpdatedAt      time.Time
	ArchivedAt     OptionalTime
	UserID         UserID
	Description    string
	TokenHash      string `json:"-"`
	Scopes         []string
	AcademicUnitID AcademicUnitID // zero when unconstrained
	ExpiresAt      time.Time
	LastUsedAt     OptionalTime
	DisabledAt     OptionalTime
	RevokedAt      OptionalTime
}

// PersonalAccessTokenCreation contains the raw credential returned exactly
// once. Credential must never be persisted, logged, or audited.
type PersonalAccessTokenCreation struct {
	Token      *PersonalAccessToken `json:"token"`
	Credential string               `json:"credential"`
}

// PrepareCreate applies application-owned lifecycle fields before validation.
func (t *PersonalAccessToken) PrepareCreate(id PersonalAccessTokenID, at time.Time) {
	if t == nil {
		return
	}
	t.ID = id
	at = TimeUTC(at)
	t.CreatedAt = at
	t.UpdatedAt = at
	t.ArchivedAt = OptionalTime{}
	t.Description = SanitizeUnicode(t.Description)
	t.Scopes = cloneStrings(t.Scopes)
	t.ExpiresAt = TimeUTC(t.ExpiresAt)
	if t.LastUsedAt.Valid {
		t.LastUsedAt = t.LastUsedAt.UTC()
	}
	if t.DisabledAt.Valid {
		t.DisabledAt = t.DisabledAt.UTC()
	}
	if t.RevokedAt.Valid {
		t.RevokedAt = t.RevokedAt.UTC()
	}
}

// PrepareUpdate applies the application-selected transition time and normalizes
// description and scopes.
func (t *PersonalAccessToken) PrepareUpdate(at time.Time) {
	if t == nil {
		return
	}
	t.UpdatedAt = TimeUTC(at)
	t.Description = SanitizeUnicode(t.Description)
	t.Scopes = cloneStrings(t.Scopes)
}

// Validate checks rehydrated personal-access-token state.
func (t *PersonalAccessToken) Validate() error {
	const where = "PersonalAccessToken.Validate"
	if t == nil {
		return invalidModelError(where, "personal_access_token", "value", "is required", "")
	}
	if !t.ID.IsValid() {
		return invalidModelError(where, "personal_access_token", "id", "must be a valid identifier", "")
	}
	details := "id=" + t.ID.String()
	if t.CreatedAt.IsZero() || t.UpdatedAt.IsZero() {
		return invalidModelError(where, "personal_access_token", "created_at", "must be set", details)
	}
	if t.UpdatedAt.Before(t.CreatedAt) {
		return invalidModelError(where, "personal_access_token", "updated_at", "must not precede created_at", details)
	}
	if !t.UserID.IsValid() {
		return invalidModelError(
			where,
			"personal_access_token",
			"user_id",
			"must be a valid identifier",
			details,
		)
	}
	if utf8.RuneCountInString(t.Description) == 0 ||
		utf8.RuneCountInString(t.Description) > PersonalAccessTokenDescriptionMaxRunes {
		return invalidModelError(
			where,
			"personal_access_token",
			"description",
			"has an invalid length",
			details,
		)
	}
	if !IsValidTokenHash(t.TokenHash) {
		return invalidModelError(
			where,
			"personal_access_token",
			"token_hash",
			"has an invalid format",
			details,
		)
	}
	if len(t.Scopes) == 0 || len(t.Scopes) > PersonalAccessTokenScopeMaxCount {
		return invalidModelError(
			where,
			"personal_access_token",
			"scopes",
			"has an invalid number of values",
			details,
		)
	}
	seen := make(map[string]struct{}, len(t.Scopes))
	for _, scope := range t.Scopes {
		if len(scope) > RolePermissionMaxLength || !validPermission.MatchString(scope) {
			return invalidModelError(
				where,
				"personal_access_token",
				"scopes",
				"contains an invalid action",
				details,
			)
		}
		if _, exists := seen[scope]; exists {
			return invalidModelError(
				where,
				"personal_access_token",
				"scopes",
				"contains a duplicate action",
				details,
			)
		}
		seen[scope] = struct{}{}
	}
	if !t.AcademicUnitID.IsZero() && !t.AcademicUnitID.IsValid() {
		return invalidModelError(
			where,
			"personal_access_token",
			"academic_unit_id",
			"must be empty or a valid identifier",
			details,
		)
	}
	if !t.ExpiresAt.After(t.CreatedAt) {
		return invalidModelError(
			where,
			"personal_access_token",
			"expires_at",
			"must be after create_at",
			details,
		)
	}
	if t.LastUsedAt.Valid &&
		(t.LastUsedAt.Time.Before(t.CreatedAt) || !t.LastUsedAt.Time.Before(t.ExpiresAt)) {
		return invalidModelError(
			where,
			"personal_access_token",
			"last_used_at",
			"must be within the token lifetime",
			details,
		)
	}
	if t.ArchivedAt.Valid && t.ArchivedAt.Time.Before(t.CreatedAt) {
		return invalidModelError(where, "personal_access_token", "archived_at", "must not precede created_at", details)
	}
	if t.RevokedAt.Valid && t.RevokedAt.Time.Before(t.CreatedAt) {
		return invalidModelError(
			where,
			"personal_access_token",
			"revoked_at",
			"must not precede create_at",
			details,
		)
	}
	if t.DisabledAt.Valid && t.DisabledAt.Time.Before(t.CreatedAt) {
		return invalidModelError(
			where,
			"personal_access_token",
			"disabled_at",
			"must not precede create_at",
			details,
		)
	}
	return nil
}

// IsActiveAt reports whether the token is usable at now.
func (t *PersonalAccessToken) IsActiveAt(now time.Time) bool {
	if t == nil {
		return false
	}
	now = TimeUTC(now)
	return !t.ArchivedAt.Valid &&
		!t.DisabledAt.Valid &&
		!t.RevokedAt.Valid &&
		now.Before(t.ExpiresAt)
}

// Auditable returns a deliberately safe audit projection. The token hash is
// never included.
func (t *PersonalAccessToken) Auditable() map[string]any {
	if t == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":               t.ID.String(),
		"created_at":       MillisFromTime(t.CreatedAt),
		"updated_at":       MillisFromTime(t.UpdatedAt),
		"archived_at":      t.ArchivedAt.Millis(),
		"delete_at":        t.ArchivedAt.Millis(),
		"user_id":          t.UserID.String(),
		"description":      t.Description,
		"scopes":           cloneStrings(t.Scopes),
		"academic_unit_id": t.AcademicUnitID.String(),
		"expires_at":       MillisFromTime(t.ExpiresAt),
		"last_used_at":     t.LastUsedAt.Millis(),
		"disabled_at":      t.DisabledAt.Millis(),
		"revoked_at":       t.RevokedAt.Millis(),
	}
}

var _ Auditable = (*PersonalAccessToken)(nil)
