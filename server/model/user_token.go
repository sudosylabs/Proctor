// ---------------------------------------------------------------------------------------------
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and NOTICE in the server module root for
// license and attribution information.
// SPDX-License-Identifier: Apache-2.0
// ---------------------------------------------------------------------------------------------
//
// Adapted from Mattermost server/public/model/token.go. Proctor stores only a
// token hash and persists explicit expiry and consumption timestamps.

package model

import (
	"strings"
	"time"
)

// UserTokenPurpose identifies the single account operation a user token serves.
type UserTokenPurpose string

const (
	UserTokenPasswordReset     UserTokenPurpose = "password_reset"
	UserTokenEmailVerification UserTokenPurpose = "email_verification"
)

// UserToken is a short-lived, single-use credential for one explicit account
// operation. Invitation tokens need their own invitation model because they
// may exist before a User; they must not overload this type with generic data.
//
// TokenHash and Target are deliberately excluded from JSON. Soft archive uses
// ArchivedAt.
type UserToken struct {
	ID         UserTokenID
	CreatedAt  time.Time
	UpdatedAt  time.Time
	ArchivedAt OptionalTime
	UserID     UserID
	Purpose    UserTokenPurpose
	TokenHash  string `json:"-"`
	Target     string `json:"-"`
	ExpiresAt  time.Time
	ConsumedAt OptionalTime
}

// PrepareCreate applies application-owned lifecycle fields before validation.
func (t *UserToken) PrepareCreate(id UserTokenID, at time.Time) {
	if t == nil {
		return
	}
	t.Target = strings.ToLower(strings.TrimSpace(SanitizeUnicode(t.Target)))
	t.ID = id
	at = TimeUTC(at)
	t.CreatedAt = at
	t.UpdatedAt = at
	t.ArchivedAt = OptionalTime{}
	t.ExpiresAt = TimeUTC(t.ExpiresAt)
}

// PrepareUpdate applies the application-selected transition time and
// normalizes the target email.
func (t *UserToken) PrepareUpdate(at time.Time) {
	if t == nil {
		return
	}
	t.Target = strings.ToLower(strings.TrimSpace(SanitizeUnicode(t.Target)))
	t.UpdatedAt = TimeUTC(at)
}

// Validate checks rehydrated user-token state.
func (t *UserToken) Validate() error {
	const where = "UserToken.Validate"
	if t == nil {
		return invalidModelError(where, "user_token", "value", "is required", "")
	}
	if !t.ID.IsValid() {
		return invalidModelError(where, "user_token", "id", "must be a valid identifier", "")
	}
	details := "id=" + t.ID.String()
	if t.CreatedAt.IsZero() || t.UpdatedAt.IsZero() {
		return invalidModelError(where, "user_token", "created_at", "must be set", details)
	}
	if t.UpdatedAt.Before(t.CreatedAt) {
		return invalidModelError(where, "user_token", "updated_at", "must not precede created_at", details)
	}
	if !t.UserID.IsValid() {
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
	if t.ExpiresAt.IsZero() || !t.ExpiresAt.After(t.CreatedAt) {
		return invalidModelError(where, "user_token", "expires_at", "must be after create_at", details)
	}
	if t.ConsumedAt.Valid &&
		(t.ConsumedAt.Time.Before(t.CreatedAt) || !t.ConsumedAt.Time.Before(t.ExpiresAt)) {
		return invalidModelError(where, "user_token", "consumed_at", "must be within the token lifetime", details)
	}
	if t.ArchivedAt.Valid && t.ArchivedAt.Time.Before(t.CreatedAt) {
		return invalidModelError(where, "user_token", "archived_at", "must not precede created_at", details)
	}
	return nil
}

// IsValid reports whether the purpose is a recognized account operation.
func (p UserTokenPurpose) IsValid() bool {
	return p == UserTokenPasswordReset || p == UserTokenEmailVerification
}

// IsActiveAt reports whether the token is unarchived, unconsumed, and unexpired
// at now.
func (t *UserToken) IsActiveAt(now time.Time) bool {
	if t == nil || t.ArchivedAt.Valid || t.ConsumedAt.Valid {
		return false
	}
	now = TimeUTC(now)
	return now.Before(t.ExpiresAt)
}

// Auditable returns a deliberately safe audit projection. Token hashes and
// target emails are never included.
func (t *UserToken) Auditable() map[string]any {
	if t == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":          t.ID.String(),
		"created_at":  MillisFromTime(t.CreatedAt),
		"updated_at":  MillisFromTime(t.UpdatedAt),
		"archived_at": t.ArchivedAt.Millis(),
		"user_id":     t.UserID.String(),
		"purpose":     t.Purpose,
		"expires_at":  MillisFromTime(t.ExpiresAt),
		"consumed_at": t.ConsumedAt.Millis(),
	}
}

var _ Auditable = (*UserToken)(nil)
