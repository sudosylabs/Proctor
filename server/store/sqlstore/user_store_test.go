// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestUserRowConversion(t *testing.T) {
	user := &model.User{
		ID: model.UserID(model.NewId()), CreatedAt: model.TimeFromMillis(1),
		UpdatedAt: model.TimeFromMillis(2), ArchivedAt: model.OptionalTimeFromMillis(3),
		Username: "user", Email: "user@example.edu", EmailVerified: true,
		DisplayName: "User", FirstName: "First", LastName: "Last",
		Locale: "en", Timezone: "UTC", LastLoginAt: model.OptionalTimeFromMillis(4),
		LastActivityAt: model.OptionalTimeFromMillis(5),
		DisabledAt:     model.OptionalTimeFromMillis(6),
	}
	row := newUserRow(user)
	if got := row.model(); *got != *user {
		t.Fatalf("row.model() = %#v, want %#v", got, user)
	}
}
