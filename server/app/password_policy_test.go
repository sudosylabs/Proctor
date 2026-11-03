// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/model"
)

func TestCheckNewPasswordRejectsEachPolicyViolationWithItsOwnCode(t *testing.T) {
	identity := passwordIdentity{Username: "ada.lovelace", Email: "countess+exams@example.edu",
		DisplayName: "Ada Byron", FirstName: "Augusta", LastName: "King"}
	cases := []struct {
		name     string
		password string
		code     string
	}{
		{name: "single repeated character", password: "zzzzzzzzzzzz", code: "authentication.password.pattern_invalid"},
		{name: "repeated short unit", password: "abcabcabcabc", code: "authentication.password.pattern_invalid"},
		{name: "ascending letters", password: "abcdefghijkl", code: "authentication.password.pattern_invalid"},
		{name: "descending letters", password: "lkjihgfedcba", code: "authentication.password.pattern_invalid"},
		{name: "digits wrapping past nine", password: "567890123456", code: "authentication.password.pattern_invalid"},
		{name: "keyboard row", password: "QWERTYUIOP", code: "authentication.password.pattern_invalid"},
		{name: "reversed keyboard row", password: "lkjhgfdsa", code: "authentication.password.pattern_invalid"},
		{name: "common password in any case", password: "BaseBall1234", code: "authentication.password.common_invalid"},
		{name: "common password with digits and symbols", password: "sunshine2026!!", code: "authentication.password.common_invalid"},
		{name: "username", password: "ada.lovelace", code: "authentication.password.identity_invalid"},
		{name: "username inside", password: "my-AdaLovelace-key", code: "authentication.password.identity_invalid"},
		{name: "email local part", password: "Countess-of-numbers", code: "authentication.password.identity_invalid"},
		{name: "display name", password: "adabyron-engine-1843", code: "authentication.password.identity_invalid"},
		{name: "full name", password: "augusta king rules", code: "authentication.password.identity_invalid"},
		{name: "invalid UTF-8", password: string([]byte{0xff, 0xfe, 0xfd}), code: "authentication.password.invalid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkNewPassword(tc.password, identity)
			require.Equal(t, tc.code, apperror.CodeOf(err, ""))
			appErr, ok := apperror.As(err)
			require.True(t, ok)
			require.Equal(t, "password", appErr.Fields()["field"])
		})
	}
}

func TestCheckNewPasswordAcceptsOrdinaryPassphrases(t *testing.T) {
	identity := passwordIdentity{Username: "li", Email: "li@example.edu", DisplayName: "Li Wu", FirstName: "Li", LastName: "Wu"}
	for _, password := range []string{
		"lantern-orchard-copper-tide",
		"correct horse battery staple",
		"Wulinggong-river-at-dawn",
		"9 slow ferries cross the bay",
		"acdefghijklm",
	} {
		t.Run(password, func(t *testing.T) {
			require.NoError(t, checkNewPassword(password, identity))
		})
	}
}

func TestPasswordIdentityOfProjectsTheAccountAttributes(t *testing.T) {
	user := &model.User{Username: "grace", Email: "grace@example.edu", DisplayName: "Rear Admiral", FirstName: "Grace", LastName: "Hopper"}
	require.Equal(t, passwordIdentity{Username: "grace", Email: "grace@example.edu", DisplayName: "Rear Admiral", FirstName: "Grace", LastName: "Hopper"}, passwordIdentityOf(user))
	require.Equal(t, passwordIdentity{}, passwordIdentityOf(nil))
}

func TestPasswordBlocklistIsBundledAndLowercased(t *testing.T) {
	require.Greater(t, len(passwordBlocklist), 7000)
	for entry := range passwordBlocklist {
		require.Equal(t, strings.ToLower(entry), entry)
	}
	_, ok := passwordBlocklist["password"]
	require.True(t, ok)
}
