// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/public/model/user.go. Authentication secrets
// and external identities are separated from the Proctor user profile.

package model

import (
	"crypto/rand"
	"encoding/hex"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	UserUsernameMaxLength    = 64
	UserEmailMaxLength       = 254
	UserDisplayNameMaxRunes  = 128
	UserPersonalNameMaxRunes = 64
	UserLocaleMaxLength      = 35
	UserTimezoneMaxLength    = 64
	ProfilePictureSeedLength = 64

	DefaultLocale   = "en"
	DefaultTimezone = "UTC"
)

var (
	validUsername = regexp.MustCompile(`^[a-z0-9._-]+$`)
	validLocale   = regexp.MustCompile(`^[A-Za-z0-9]+(?:-[A-Za-z0-9]+)*$`)
)

var restrictedUsernames = map[string]struct{}{
	"all":     {},
	"proctor": {},
	"system":  {},
}

// User is one login-capable account. It intentionally does not contain a
// permanent teacher/student enum: Affiliation, AcademicUnitMember, ClassMember,
// and RoleBinding describe the user's contextual academic relationships.
//
// Password hashes and external-provider subjects live in separate credential
// and identity models so an account can use more than one authentication
// method without overloading the profile.
//
// Domain time is UTC time.Time. Optional lifecycle instants use OptionalTime.
// Soft archive uses ArchivedAt. Revision supports optimistic
// concurrency on profile updates.
type User struct {
	ID                          UserID
	CreatedAt                   time.Time
	UpdatedAt                   time.Time
	ArchivedAt                  OptionalTime
	Revision                    int64
	Username                    string
	Email                       string
	EmailVerified               bool
	DisplayName                 string
	FirstName                   string
	LastName                    string
	Locale                      string
	Timezone                    string
	LastLoginAt                 OptionalTime
	LastActivityAt              OptionalTime
	DisabledAt                  OptionalTime
	DefaultProfilePictureSeed   string
	DefaultProfilePictureFileID FileEntryID
	CustomProfilePictureFileID  FileEntryID
	ProfilePictureChangedAt     OptionalTime
}

// PrepareCreate applies application-owned lifecycle fields before validation.
func (u *User) PrepareCreate(id UserID, at time.Time) {
	if u == nil {
		return
	}
	if u.Username == "" {
		u.Username = "u" + NewId()
	}
	u.normalize()
	u.ID = id
	at = TimeUTC(at)
	u.CreatedAt = at
	u.UpdatedAt = at
	u.ArchivedAt = OptionalTime{}
	if u.Revision <= 0 {
		u.Revision = 1
	}
	if u.Locale == "" {
		u.Locale = DefaultLocale
	}
	if u.Timezone == "" {
		u.Timezone = DefaultTimezone
	}
	if u.DefaultProfilePictureSeed == "" {
		u.DefaultProfilePictureSeed = newProfilePictureSeed()
	}
}

// PrepareUpdate applies the application-selected transition time and normalizes
// profile fields. Callers that need optimistic concurrency must manage
// Revision separately.
func (u *User) PrepareUpdate(at time.Time) {
	if u == nil {
		return
	}
	u.UpdatedAt = TimeUTC(at)
	u.normalize()
	if u.DefaultProfilePictureSeed == "" {
		u.DefaultProfilePictureSeed = newProfilePictureSeed()
	}
}

// UserProfileChanges carries only the fields that the generic profile command
// owns. Mailbox and verification state intentionally have no representation
// here; they change only through the named UserToken aggregates.
type UserProfileChanges struct {
	Username    *string `json:"username,omitempty"`
	DisplayName *string `json:"display_name,omitempty"`
	FirstName   *string `json:"first_name,omitempty"`
	LastName    *string `json:"last_name,omitempty"`
	Locale      *string `json:"locale,omitempty"`
	Timezone    *string `json:"timezone,omitempty"`
}

// ApplyProfileChanges applies the non-nil generic profile fields from changes.
func (u *User) ApplyProfileChanges(changes *UserProfileChanges) {
	if u == nil || changes == nil {
		return
	}
	if changes.Username != nil {
		u.Username = *changes.Username
	}
	if changes.DisplayName != nil {
		u.DisplayName = *changes.DisplayName
	}
	if changes.FirstName != nil {
		u.FirstName = *changes.FirstName
	}
	if changes.LastName != nil {
		u.LastName = *changes.LastName
	}
	if changes.Locale != nil {
		u.Locale = *changes.Locale
	}
	if changes.Timezone != nil {
		u.Timezone = *changes.Timezone
	}
}

func (u *User) normalize() {
	if u == nil {
		return
	}
	u.Username = strings.ToLower(SanitizeUnicode(u.Username))
	u.Email = strings.ToLower(strings.TrimSpace(SanitizeUnicode(u.Email)))
	u.DisplayName = SanitizeUnicode(u.DisplayName)
	u.FirstName = SanitizeUnicode(u.FirstName)
	u.LastName = SanitizeUnicode(u.LastName)
	u.Locale = SanitizeUnicode(u.Locale)
	u.Timezone = SanitizeUnicode(u.Timezone)
}

// Validate checks rehydrated user state.
func (u *User) Validate() error {
	const where = "User.Validate"
	if u == nil {
		return invalidModelError(where, "user", "value", "is required", "")
	}
	if !u.ID.IsValid() {
		return invalidModelError(where, "user", "id", "must be a valid identifier", "")
	}
	details := "id=" + u.ID.String()
	if u.CreatedAt.IsZero() || u.UpdatedAt.IsZero() {
		return invalidModelError(where, "user", "created_at", "must be set", details)
	}
	if u.UpdatedAt.Before(u.CreatedAt) {
		return invalidModelError(where, "user", "updated_at", "must not precede created_at", details)
	}
	if u.Revision <= 0 {
		return invalidModelError(where, "user", "revision", "must be positive", details)
	}
	if !IsValidUsername(u.Username) {
		return invalidModelError(where, "user", "username", "has an invalid format", details)
	}
	if !IsValidEmail(u.Email) {
		return invalidModelError(where, "user", "email", "has an invalid format", details)
	}
	if utf8.RuneCountInString(u.DisplayName) > UserDisplayNameMaxRunes {
		return invalidModelError(where, "user", "display_name", "is too long", details)
	}
	if utf8.RuneCountInString(u.FirstName) > UserPersonalNameMaxRunes {
		return invalidModelError(where, "user", "first_name", "is too long", details)
	}
	if utf8.RuneCountInString(u.LastName) > UserPersonalNameMaxRunes {
		return invalidModelError(where, "user", "last_name", "is too long", details)
	}
	if len(u.Locale) == 0 || len(u.Locale) > UserLocaleMaxLength || !validLocale.MatchString(u.Locale) {
		return invalidModelError(where, "user", "locale", "has an invalid format", details)
	}
	if len(u.Timezone) == 0 || len(u.Timezone) > UserTimezoneMaxLength {
		return invalidModelError(where, "user", "timezone", "has an invalid length", details)
	}
	if u.ArchivedAt.Valid && u.ArchivedAt.Time.Before(u.CreatedAt) {
		return invalidModelError(where, "user", "archived_at", "must not precede created_at", details)
	}
	if u.DisabledAt.Valid && u.DisabledAt.Time.Before(u.CreatedAt) {
		return invalidModelError(where, "user", "disabled_at", "must not precede create_at", details)
	}
	if u.LastLoginAt.Valid && u.LastLoginAt.Time.Before(u.CreatedAt) {
		return invalidModelError(where, "user", "last_login_at", "must not precede created_at", details)
	}
	if u.LastActivityAt.Valid && u.LastActivityAt.Time.Before(u.CreatedAt) {
		return invalidModelError(where, "user", "last_activity_at", "must not precede created_at", details)
	}
	if len(u.DefaultProfilePictureSeed) != ProfilePictureSeedLength {
		return invalidModelError(where, "user", "default_profile_picture_seed", "must be a 256-bit hexadecimal seed", details)
	}
	if _, err := hex.DecodeString(u.DefaultProfilePictureSeed); err != nil {
		return invalidModelError(where, "user", "default_profile_picture_seed", "must be a 256-bit hexadecimal seed", details)
	}
	if !u.DefaultProfilePictureFileID.IsZero() && !u.DefaultProfilePictureFileID.IsValid() {
		return invalidModelError(where, "user", "default_profile_picture_file_id", "must be a valid identifier when set", details)
	}
	if !u.CustomProfilePictureFileID.IsZero() && !u.CustomProfilePictureFileID.IsValid() {
		return invalidModelError(where, "user", "custom_profile_picture_file_id", "must be a valid identifier when set", details)
	}
	if u.ProfilePictureChangedAt.Valid && u.ProfilePictureChangedAt.Time.Before(u.CreatedAt) {
		return invalidModelError(where, "user", "profile_picture_changed_at", "must not precede created_at", details)
	}
	return nil
}

func newProfilePictureSeed() string {
	value := make([]byte, ProfilePictureSeedLength/2)
	if _, err := rand.Read(value); err != nil {
		panic("model: operating system random source failed: " + err.Error())
	}
	return hex.EncodeToString(value)
}

// IsActive reports whether the account is not archived and not disabled.
func (u *User) IsActive() bool {
	return u != nil && !u.ArchivedAt.Valid && !u.DisabledAt.Valid
}

// IsArchived reports soft archive state.
func (u *User) IsArchived() bool {
	return u != nil && u.ArchivedAt.Valid
}

// Auditable returns a deliberately safe audit projection. Profile PII is
// omitted; lifecycle times remain as Unix milliseconds for wire compatibility.
func (u *User) Auditable() map[string]any {
	if u == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":             u.ID.String(),
		"created_at":     MillisFromTime(u.CreatedAt),
		"updated_at":     MillisFromTime(u.UpdatedAt),
		"archived_at":    u.ArchivedAt.Millis(),
		"revision":       u.Revision,
		"username":       u.Username,
		"email_verified": u.EmailVerified,
		"disabled_at":    u.DisabledAt.Millis(),
		"last_login_at":  u.LastLoginAt.Millis(),
	}
}

// IsValidUsername validates the normalized public username.
func IsValidUsername(value string) bool {
	if len(value) == 0 || len(value) > UserUsernameMaxLength || !validUsername.MatchString(value) {
		return false
	}
	_, restricted := restrictedUsernames[value]
	return !restricted
}

// IsValidEmail accepts only a normalized plain mailbox address, not a display
// name containing an address.
func IsValidEmail(value string) bool {
	if value == "" || len(value) > UserEmailMaxLength || strings.ToLower(value) != value {
		return false
	}
	address, err := mail.ParseAddress(value)
	return err == nil && address.Name == "" && address.Address == value
}

var _ Auditable = (*User)(nil)
