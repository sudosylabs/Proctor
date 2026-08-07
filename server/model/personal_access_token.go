// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/public/model/user_access_token.go. Proctor
// stores only a token hash and requires expiry and explicit scopes.

package model

import "unicode/utf8"

const (
	PersonalAccessTokenDescriptionMaxRunes = 255
	PersonalAccessTokenScopeMaxCount       = 128
)

// PersonalAccessToken is a long-lived, explicitly scoped credential for human
// CLI and automation use. It is not a Session and never stores the raw token.
// AcademicUnitId optionally narrows every scope to one unit and its authorized
// descendants.
type PersonalAccessToken struct {
	Id             string   `json:"id"`
	CreateAt       int64    `json:"create_at"`
	UpdateAt       int64    `json:"update_at"`
	DeleteAt       int64    `json:"delete_at"`
	UserId         string   `json:"user_id"`
	Description    string   `json:"description"`
	TokenHash      string   `json:"-"`
	Scopes         []string `json:"scopes"`
	AcademicUnitId string   `json:"academic_unit_id,omitempty"`
	ExpiresAt      int64    `json:"expires_at"`
	LastUsedAt     int64    `json:"last_used_at,omitempty"`
	DisabledAt     int64    `json:"disabled_at,omitempty"`
	RevokedAt      int64    `json:"revoked_at,omitempty"`
}

// PersonalAccessTokenCreation contains the raw credential returned exactly
// once. Credential must never be persisted, logged, or audited.
type PersonalAccessTokenCreation struct {
	Token      *PersonalAccessToken `json:"token"`
	Credential string               `json:"credential"`
}

func (t *PersonalAccessToken) PreSave() {
	preSave(&t.Id, &t.CreateAt, &t.UpdateAt)
	t.Description = SanitizeUnicode(t.Description)
	t.Scopes = cloneStrings(t.Scopes)
}

func (t *PersonalAccessToken) PreUpdate() {
	preUpdate(&t.UpdateAt)
	t.Description = SanitizeUnicode(t.Description)
	t.Scopes = cloneStrings(t.Scopes)
}

func (t *PersonalAccessToken) IsValid() error {
	const where = "PersonalAccessToken.IsValid"
	if appErr := validatePersistentFields(
		where,
		"personal_access_token",
		t.Id,
		t.CreateAt,
		t.UpdateAt,
	); appErr != nil {
		return appErr
	}
	details := "id=" + t.Id
	if !IsValidId(t.UserId) {
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
	if t.AcademicUnitId != "" && !IsValidId(t.AcademicUnitId) {
		return invalidModelError(
			where,
			"personal_access_token",
			"academic_unit_id",
			"must be empty or a valid identifier",
			details,
		)
	}
	if t.ExpiresAt <= t.CreateAt {
		return invalidModelError(
			where,
			"personal_access_token",
			"expires_at",
			"must be after create_at",
			details,
		)
	}
	if t.LastUsedAt != 0 && (t.LastUsedAt < t.CreateAt || t.LastUsedAt >= t.ExpiresAt) {
		return invalidModelError(
			where,
			"personal_access_token",
			"last_used_at",
			"must be within the token lifetime",
			details,
		)
	}
	if t.RevokedAt != 0 && t.RevokedAt < t.CreateAt {
		return invalidModelError(
			where,
			"personal_access_token",
			"revoked_at",
			"must not precede create_at",
			details,
		)
	}
	if t.DisabledAt != 0 && t.DisabledAt < t.CreateAt {
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

func (t *PersonalAccessToken) IsActiveAt(now int64) bool {
	return t != nil &&
		t.DeleteAt == 0 &&
		t.DisabledAt == 0 &&
		t.RevokedAt == 0 &&
		now < t.ExpiresAt
}

func (t *PersonalAccessToken) Auditable() map[string]any {
	fields := auditFields(t.Id, t.CreateAt, t.UpdateAt, t.DeleteAt)
	fields["user_id"] = t.UserId
	fields["description"] = t.Description
	fields["scopes"] = cloneStrings(t.Scopes)
	fields["academic_unit_id"] = t.AcademicUnitId
	fields["expires_at"] = t.ExpiresAt
	fields["last_used_at"] = t.LastUsedAt
	fields["disabled_at"] = t.DisabledAt
	fields["revoked_at"] = t.RevokedAt
	return fields
}

var _ Auditable = (*PersonalAccessToken)(nil)
