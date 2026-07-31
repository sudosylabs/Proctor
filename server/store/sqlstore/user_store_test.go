// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestUserRowConversion(t *testing.T) {
	user := &model.User{
		Id: model.NewId(), CreateAt: 1, UpdateAt: 2, DeleteAt: 3,
		Username: "user", Email: "user@example.edu", EmailVerified: true,
		DisplayName: "User", FirstName: "First", LastName: "Last",
		Locale: "en", Timezone: "UTC", LastLoginAt: 4, LastActivityAt: 5,
		DisabledAt: 6,
	}
	row := newUserRow(user)
	if got := row.model(); *got != *user {
		t.Fatalf("row.model() = %#v, want %#v", got, user)
	}
}
