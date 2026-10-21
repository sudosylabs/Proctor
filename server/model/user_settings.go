// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"time"
	"unicode/utf8"
)

const (
	UserSettingsFormatVersion1        = 1
	UserSettingsInitialSource         = "{}\n"
	UserSettingsSourceMaxBytes        = 256 * 1024
	UserSettingsNestingMaxDepth       = 8
	UserSettingsPathMaximumCount      = 2048
	UserSettingsKeyMaxBytes           = 256
	UserSettingsStringMaxBytes        = 16 * 1024
	UserSettingsCollectionMaxElements = 256
)

// UserSettingsRevision is an opaque comparison token for one exact current
// User Settings Document source. It is not an ordering, time, or content hash.
type UserSettingsRevision string

func NewUserSettingsRevision() UserSettingsRevision {
	return UserSettingsRevision(NewID())
}

func ParseUserSettingsRevision(value string) (UserSettingsRevision, error) {
	if !IsValidOpaqueID(value) {
		return "", invalidModelError("ParseUserSettingsRevision", "user_settings", "", "invalid user settings revision", "")
	}
	return UserSettingsRevision(value), nil
}

func (r UserSettingsRevision) IsValid() bool  { return IsValidOpaqueID(string(r)) }
func (r UserSettingsRevision) String() string { return string(r) }

// UserSettingsDocument is the server-authoritative exact portable
// presentation-preference source for one User. Source is deliberately omitted
// from generic JSON so logs and audit helpers cannot expose it accidentally.
type UserSettingsDocument struct {
	UserID        UserID
	Source        string `json:"-"`
	FormatVersion int
	Revision      UserSettingsRevision
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func NewUserSettingsDocument(userID UserID, revision UserSettingsRevision, at time.Time) (*UserSettingsDocument, error) {
	at = TimeUTC(at)
	document := &UserSettingsDocument{
		UserID:        userID,
		Source:        UserSettingsInitialSource,
		FormatVersion: UserSettingsFormatVersion1,
		Revision:      revision,
		CreatedAt:     at,
		UpdatedAt:     at,
	}
	if err := document.Validate(); err != nil {
		return nil, err
	}
	return document, nil
}

func (d *UserSettingsDocument) Validate() error {
	const where = "UserSettingsDocument.Validate"
	if d == nil {
		return invalidModelError(where, "user_settings_document", "value", "is required", "")
	}
	if !d.UserID.IsValid() {
		return invalidModelError(where, "user_settings_document", "user_id", "must be a valid identifier", "")
	}
	details := "user_id=" + d.UserID.String()
	if d.FormatVersion <= 0 {
		return invalidModelError(where, "user_settings_document", "format_version", "must be positive", details)
	}
	if !d.Revision.IsValid() {
		return invalidModelError(where, "user_settings_document", "revision", "must be a valid opaque token", details)
	}
	if d.CreatedAt.IsZero() || d.UpdatedAt.IsZero() {
		return invalidModelError(where, "user_settings_document", "created_at", "must be set", details)
	}
	if d.UpdatedAt.Before(d.CreatedAt) {
		return invalidModelError(where, "user_settings_document", "updated_at", "must not precede created_at", details)
	}
	if !utf8.ValidString(d.Source) {
		return invalidModelError(where, "user_settings_document", "source", "must be valid UTF-8", details)
	}
	if len(d.Source) > UserSettingsSourceMaxBytes {
		return invalidModelError(where, "user_settings_document", "source", "is too large", details)
	}
	return nil
}

func (d *UserSettingsDocument) Clone() *UserSettingsDocument {
	if d == nil {
		return nil
	}
	clone := *d
	return &clone
}
