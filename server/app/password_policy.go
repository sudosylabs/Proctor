// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
//
// The new-password policy follows NIST SP 800-63B section 3.1.1.2: it imposes
// no composition rules and no periodic rotation, and instead refuses values
// that are commonly used, expected, or derived from the account itself. The
// blocklist is bundled data (see NOTICE); no password ever leaves the process.

package app

import (
	"context"
	_ "embed"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

//go:embed password_blocklist.txt
var passwordBlocklistSource string

// passwordBlocklist holds the bundled common passwords, lowercased.
var passwordBlocklist = func() map[string]struct{} {
	entries := strings.Split(passwordBlocklistSource, "\n")
	set := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if entry = strings.ToLower(strings.TrimSpace(entry)); entry != "" {
			set[entry] = struct{}{}
		}
	}
	return set
}()

// passwordIdentityMinimumRunes is the shortest account attribute the policy
// looks for inside a password. Shorter attributes, such as two-letter given
// names, would reject ordinary passphrases by coincidence.
const passwordIdentityMinimumRunes = 4

// passwordKeyboardRows are the unshifted QWERTY rows; a password that is a run
// of one of them, in either direction, is a simple sequence.
var passwordKeyboardRows = []string{"qwertyuiop", "asdfghjkl", "zxcvbnm"}

// passwordIdentity is the account context a new password must not repeat.
// Empty fields are ignored.
type passwordIdentity struct {
	Username    string
	Email       string
	DisplayName string
	FirstName   string
	LastName    string
}

func passwordIdentityOf(user *model.User) passwordIdentity {
	if user == nil {
		return passwordIdentity{}
	}
	return passwordIdentity{Username: user.Username, Email: user.Email, DisplayName: user.DisplayName,
		FirstName: user.FirstName, LastName: user.LastName}
}

// checkNewPassword applies the content policy to a password a User is about
// to set. The length bounds stay with the hasher, which also applies them to
// a login rehash; this check runs only where a new password is chosen.
// Each rejection is a distinct public code so clients can explain it.
func checkNewPassword(password string, identity passwordIdentity) error {
	if !utf8.ValidString(password) {
		return apperror.Invalid("authentication.password.invalid", "password")
	}
	folded := strings.ToLower(password)
	if isSimplePasswordPattern(folded) {
		return apperror.Invalid("authentication.password.pattern_invalid", "password")
	}
	if isCommonPassword(folded) {
		return apperror.Invalid("authentication.password.common_invalid", "password")
	}
	if containsPasswordIdentity(folded, identity) {
		return apperror.Invalid("authentication.password.identity_invalid", "password")
	}
	return nil
}

// isCommonPassword reports whether the password, or the password without a
// trailing run of digits and symbols ("password2024!"), is on the blocklist.
func isCommonPassword(folded string) bool {
	if _, ok := passwordBlocklist[folded]; ok {
		return true
	}
	base := strings.TrimRightFunc(folded, func(r rune) bool { return !unicode.IsLetter(r) })
	if base == folded || utf8.RuneCountInString(base) < passwordIdentityMinimumRunes {
		return false
	}
	_, ok := passwordBlocklist[base]
	return ok
}

// isSimplePasswordPattern reports a single repeated character, a repetition
// of a unit of at most three characters, a run of consecutive code points in
// either direction (digits wrap from 9 to 0), or a keyboard-row run.
func isSimplePasswordPattern(folded string) bool {
	runes := []rune(folded)
	if len(runes) < 2 {
		return len(runes) == 1
	}
	for unit := 1; unit <= 3 && unit < len(runes); unit++ {
		if repeatsUnit(runes, unit) {
			return true
		}
	}
	if isConsecutiveRun(runes, 1) || isConsecutiveRun(runes, -1) {
		return true
	}
	for _, row := range passwordKeyboardRows {
		if strings.Contains(row, folded) || strings.Contains(reverseString(row), folded) {
			return true
		}
	}
	return false
}

func repeatsUnit(runes []rune, unit int) bool {
	for index := unit; index < len(runes); index++ {
		if runes[index] != runes[index-unit] {
			return false
		}
	}
	return true
}

func isConsecutiveRun(runes []rune, step rune) bool {
	for index := 1; index < len(runes); index++ {
		previous, current := runes[index-1], runes[index]
		if current == previous+step {
			continue
		}
		if step == 1 && previous == '9' && current == '0' || step == -1 && previous == '0' && current == '9' {
			continue
		}
		return false
	}
	return true
}

func reverseString(value string) string {
	runes := []rune(value)
	for left, right := 0, len(runes)-1; left < right; left, right = left+1, right-1 {
		runes[left], runes[right] = runes[right], runes[left]
	}
	return string(runes)
}

// containsPasswordIdentity compares letters and digits only, so separators
// and spacing ("Ada Lovelace" against "ada.lovelace!") do not hide a match.
func containsPasswordIdentity(folded string, identity passwordIdentity) bool {
	compactPassword := compactPasswordText(folded)
	if compactPassword == "" {
		return false
	}
	localPart := identity.Email
	if at := strings.LastIndex(localPart, "@"); at >= 0 {
		localPart = localPart[:at]
	}
	if plus := strings.Index(localPart, "+"); plus > 0 {
		localPart = localPart[:plus]
	}
	fullName := strings.TrimSpace(identity.FirstName + " " + identity.LastName)
	for _, attribute := range []string{identity.Username, localPart, identity.DisplayName, fullName} {
		fragment := compactPasswordText(strings.ToLower(attribute))
		if utf8.RuneCountInString(fragment) >= passwordIdentityMinimumRunes && strings.Contains(compactPassword, fragment) {
			return true
		}
	}
	return false
}

func compactPasswordText(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return -1
	}, value)
}

// ReuseCheck returns the Store-side check that refuses password when it
// matches one of the User's configured number of recent passwords. The Store
// runs it inside the credential transaction, so each comparison takes an
// ordinary password work permit there.
func (h *passwordHasher) ReuseCheck(password string) store.PasswordReuseCheck {
	if h.historyCount == 0 {
		return store.PasswordReuseCheck{}
	}
	return store.PasswordReuseCheck{Limit: h.historyCount, Matcher: passwordReuseMatcher{hasher: h, password: password}}
}

// passwordReuseMatcher compares one candidate password with retained hashes.
type passwordReuseMatcher struct {
	hasher   *passwordHasher
	password string
}

func (m passwordReuseMatcher) Matches(ctx context.Context, encoded string) (bool, error) {
	err := m.hasher.Verify(ctx, encoded, m.password)
	if errors.Is(err, ErrPasswordMismatch) {
		return false, nil
	}
	return err == nil, err
}

// passwordReused is the public refusal for a password the history check matched.
func passwordReused() error {
	return apperror.Invalid("authentication.password.reused", "password")
}
