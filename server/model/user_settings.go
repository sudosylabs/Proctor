// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"fmt"
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
	return UserSettingsRevision(NewId())
}

func ParseUserSettingsRevision(value string) (UserSettingsRevision, error) {
	if !IsValidId(value) {
		return "", fmt.Errorf("model: invalid user settings revision")
	}
	return UserSettingsRevision(value), nil
}

func (revision UserSettingsRevision) IsValid() bool  { return IsValidId(string(revision)) }
func (revision UserSettingsRevision) String() string { return string(revision) }

// UserSettingsDocument is the server-authoritative exact portable
// presentation-preference source for one User. Source is deliberately omitted
// from generic JSON so logs and audit helpers cannot expose it accidentally.
type UserSettingsDocument struct {
	UserID        UserID               `json:"user_id"`
	Source        string               `json:"-"`
	FormatVersion int                  `json:"format_version"`
	Revision      UserSettingsRevision `json:"revision"`
	CreatedAt     time.Time            `json:"created_at"`
	UpdatedAt     time.Time            `json:"updated_at"`
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

func (document *UserSettingsDocument) Validate() error {
	const where = "UserSettingsDocument.Validate"
	if document == nil {
		return invalidModelError(where, "user_settings_document", "value", "is required", "")
	}
	if !document.UserID.IsValid() {
		return invalidModelError(where, "user_settings_document", "user_id", "must be a valid identifier", "")
	}
	details := "user_id=" + document.UserID.String()
	if document.FormatVersion <= 0 {
		return invalidModelError(where, "user_settings_document", "format_version", "must be positive", details)
	}
	if !document.Revision.IsValid() {
		return invalidModelError(where, "user_settings_document", "revision", "must be a valid opaque token", details)
	}
	if document.CreatedAt.IsZero() || document.UpdatedAt.IsZero() {
		return invalidModelError(where, "user_settings_document", "created_at", "must be set", details)
	}
	if document.UpdatedAt.Before(document.CreatedAt) {
		return invalidModelError(where, "user_settings_document", "updated_at", "must not precede created_at", details)
	}
	if !utf8.ValidString(document.Source) {
		return invalidModelError(where, "user_settings_document", "source", "must be valid UTF-8", details)
	}
	if len(document.Source) > UserSettingsSourceMaxBytes {
		return invalidModelError(where, "user_settings_document", "source", "is too large", details)
	}
	return nil
}

func (document *UserSettingsDocument) Clone() *UserSettingsDocument {
	if document == nil {
		return nil
	}
	clone := *document
	return &clone
}
