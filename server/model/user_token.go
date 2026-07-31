// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/public/model/token.go. Proctor stores only a
// token hash and persists explicit expiry and consumption timestamps.

package model

import "strings"

type UserTokenPurpose string

const (
	UserTokenPasswordReset     UserTokenPurpose = "password_reset"
	UserTokenEmailVerification UserTokenPurpose = "email_verification"
)

// UserToken is a short-lived, single-use credential for one explicit account
// operation. Invitation tokens need their own invitation model because they
// may exist before a User; they must not overload this type with generic data.
type UserToken struct {
	Id         string           `json:"id"`
	CreateAt   int64            `json:"create_at"`
	UpdateAt   int64            `json:"update_at"`
	DeleteAt   int64            `json:"delete_at"`
	UserId     string           `json:"user_id"`
	Purpose    UserTokenPurpose `json:"purpose"`
	TokenHash  string           `json:"-"`
	Target     string           `json:"-"`
	ExpiresAt  int64            `json:"expires_at"`
	ConsumedAt int64            `json:"consumed_at,omitempty"`
}

func (t *UserToken) PreSave() {
	t.Target = strings.ToLower(strings.TrimSpace(SanitizeUnicode(t.Target)))
	preSave(&t.Id, &t.CreateAt, &t.UpdateAt)
}

func (t *UserToken) PreUpdate() {
	t.Target = strings.ToLower(strings.TrimSpace(SanitizeUnicode(t.Target)))
	preUpdate(&t.UpdateAt)
}

func (t *UserToken) IsValid() *AppError {
	const where = "UserToken.IsValid"
	if appErr := validatePersistentFields(
		where,
		"user_token",
		t.Id,
		t.CreateAt,
		t.UpdateAt,
	); appErr != nil {
		return appErr
	}
	details := "id=" + t.Id
	if !IsValidId(t.UserId) {
		return invalidModelError(where, "user_token", "user_id", "must be a valid identifier", details)
	}
	if !t.Purpose.IsValid() {
		return invalidModelError(where, "user_token", "purpose", "has an unknown value", details)
	}
	if !IsValidTokenHash(t.TokenHash) {
		return invalidModelError(where, "user_token", "token_hash", "has an invalid format", details)
	}
	if !IsValidEmail(t.Target) {
		return invalidModelError(
			where,
			"user_token",
			"target",
			"must be a normalized email address",
			details,
		)
	}
	if t.ExpiresAt <= t.CreateAt {
		return invalidModelError(where, "user_token", "expires_at", "must be after create_at", details)
	}
	if t.ConsumedAt != 0 && (t.ConsumedAt < t.CreateAt || t.ConsumedAt >= t.ExpiresAt) {
		return invalidModelError(where, "user_token", "consumed_at", "must be within the token lifetime", details)
	}
	return nil
}

func (p UserTokenPurpose) IsValid() bool {
	return p == UserTokenPasswordReset || p == UserTokenEmailVerification
}

func (t *UserToken) IsActiveAt(now int64) bool {
	return t != nil &&
		t.DeleteAt == 0 &&
		t.ConsumedAt == 0 &&
		now < t.ExpiresAt
}

func (t *UserToken) Auditable() map[string]any {
	fields := auditFields(t.Id, t.CreateAt, t.UpdateAt, t.DeleteAt)
	fields["user_id"] = t.UserId
	fields["purpose"] = t.Purpose
	fields["expires_at"] = t.ExpiresAt
	fields["consumed_at"] = t.ConsumedAt
	return fields
}

var _ Auditable = (*UserToken)(nil)
