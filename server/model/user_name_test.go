// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var controlCharacterNames = map[string]string{
	"line feed":       "Alex\nBcc: attacker@example.test",
	"carriage return": "Alex\rMorgan",
	"tab":             "Alex\tMorgan",
	"nul":             "Alex\x00",
	"delete":          "Alex\x7f",
	"c1 next line":    "Alex\u0085Morgan",
}

func TestUserRejectsControlCharactersInProfileNames(t *testing.T) {
	t.Parallel()

	at := time.UnixMilli(1_700_000_000_000).UTC()
	fields := map[string]func(*User, string){
		"display_name": func(u *User, value string) { u.DisplayName = value },
		"first_name":   func(u *User, value string) { u.FirstName = value },
		"last_name":    func(u *User, value string) { u.LastName = value },
	}
	for field, set := range fields {
		for name, value := range controlCharacterNames {
			t.Run(field+"/"+name, func(t *testing.T) {
				t.Parallel()
				user := &User{Username: "alex.morgan", Email: "alex.morgan@example.edu"}
				set(user, value)
				user.PrepareCreate(NewUserID(), at)
				require.ErrorContains(t, user.Validate(), field)
			})
		}
	}
}

func TestUserAcceptsFormatCharactersThatScriptsNeed(t *testing.T) {
	t.Parallel()

	user := &User{Username: "alex.morgan", Email: "alex.morgan@example.edu", DisplayName: "\u0645\u06cc\u200c\u0631\u0648\u0645"}
	user.PrepareCreate(NewUserID(), time.UnixMilli(1_700_000_000_000).UTC())
	require.NoError(t, user.Validate())
}

func TestInvitationSuggestionsRejectControlCharactersInProfileNames(t *testing.T) {
	t.Parallel()

	for name, value := range controlCharacterNames {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for _, suggestions := range []InvitationProfileSuggestions{
				{DisplayName: value}, {FirstName: value}, {LastName: value},
			} {
				require.Error(t, validateInvitationSuggestions(suggestions))
			}
		})
	}
	require.NoError(t, validateInvitationSuggestions(InvitationProfileSuggestions{DisplayName: "Student One"}))
}
