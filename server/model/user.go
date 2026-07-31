// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/public/model/user.go. Authentication secrets
// and external identities are separated from the Proctor user profile.

package model

import (
	"net/mail"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	UserUsernameMaxLength    = 64
	UserEmailMaxLength       = 254
	UserDisplayNameMaxRunes  = 128
	UserPersonalNameMaxRunes = 64
	UserLocaleMaxLength      = 35
	UserTimezoneMaxLength    = 64

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
type User struct {
	Id             string `json:"id"`
	CreateAt       int64  `json:"create_at"`
	UpdateAt       int64  `json:"update_at"`
	DeleteAt       int64  `json:"delete_at"`
	Username       string `json:"username"`
	Email          string `json:"email"`
	EmailVerified  bool   `json:"email_verified"`
	DisplayName    string `json:"display_name"`
	FirstName      string `json:"first_name"`
	LastName       string `json:"last_name"`
	Locale         string `json:"locale"`
	Timezone       string `json:"timezone"`
	LastLoginAt    int64  `json:"last_login_at,omitempty"`
	LastActivityAt int64  `json:"last_activity_at,omitempty"`
	DisabledAt     int64  `json:"disabled_at,omitempty"`
}

type UserPatch struct {
	Username      *string `json:"username,omitempty"`
	Email         *string `json:"email,omitempty"`
	EmailVerified *bool   `json:"email_verified,omitempty"`
	DisplayName   *string `json:"display_name,omitempty"`
	FirstName     *string `json:"first_name,omitempty"`
	LastName      *string `json:"last_name,omitempty"`
	Locale        *string `json:"locale,omitempty"`
	Timezone      *string `json:"timezone,omitempty"`
}

func (u *User) Patch(p *UserPatch) {
	if p.Username != nil {
		u.Username = *p.Username
	}
	if p.Email != nil {
		if strings.ToLower(strings.TrimSpace(*p.Email)) != u.Email &&
			p.EmailVerified == nil {
			u.EmailVerified = false
		}
		u.Email = *p.Email
	}
	if p.EmailVerified != nil {
		u.EmailVerified = *p.EmailVerified
	}
	if p.DisplayName != nil {
		u.DisplayName = *p.DisplayName
	}
	if p.FirstName != nil {
		u.FirstName = *p.FirstName
	}
	if p.LastName != nil {
		u.LastName = *p.LastName
	}
	if p.Locale != nil {
		u.Locale = *p.Locale
	}
	if p.Timezone != nil {
		u.Timezone = *p.Timezone
	}
}

func (u *User) PreSave() {
	if u.Username == "" {
		u.Username = "u" + NewId()
	}
	u.normalize()
	preSave(&u.Id, &u.CreateAt, &u.UpdateAt)
	if u.Locale == "" {
		u.Locale = DefaultLocale
	}
	if u.Timezone == "" {
		u.Timezone = DefaultTimezone
	}
}

func (u *User) PreUpdate() {
	u.normalize()
	preUpdate(&u.UpdateAt)
}

func (u *User) normalize() {
	u.Username = strings.ToLower(SanitizeUnicode(u.Username))
	u.Email = strings.ToLower(strings.TrimSpace(SanitizeUnicode(u.Email)))
	u.DisplayName = SanitizeUnicode(u.DisplayName)
	u.FirstName = SanitizeUnicode(u.FirstName)
	u.LastName = SanitizeUnicode(u.LastName)
	u.Locale = SanitizeUnicode(u.Locale)
	u.Timezone = SanitizeUnicode(u.Timezone)
}

func (u *User) IsValid() *AppError {
	const where = "User.IsValid"
	if appErr := validatePersistentFields(where, "user", u.Id, u.CreateAt, u.UpdateAt); appErr != nil {
		return appErr
	}
	details := "id=" + u.Id
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
	if u.DisabledAt != 0 && u.DisabledAt < u.CreateAt {
		return invalidModelError(where, "user", "disabled_at", "must not precede create_at", details)
	}
	return nil
}

func (u *User) IsActive() bool {
	return u != nil && u.DeleteAt == 0 && u.DisabledAt == 0
}

func (u *User) Auditable() map[string]any {
	fields := auditFields(u.Id, u.CreateAt, u.UpdateAt, u.DeleteAt)
	fields["username"] = u.Username
	fields["email_verified"] = u.EmailVerified
	fields["disabled_at"] = u.DisabledAt
	fields["last_login_at"] = u.LastLoginAt
	return fields
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
