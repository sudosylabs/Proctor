// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestUserRowConversion(t *testing.T) {
	user := &model.User{
		ID: model.NewUserID(), CreatedAt: model.TimeFromMillis(1),
		UpdatedAt: model.TimeFromMillis(2), ArchivedAt: model.OptionalTimeFromMillis(3),
		Revision: 1,
		Username: "user", Email: "user@example.edu", EmailVerified: true,
		DisplayName: "User", FirstName: "First", LastName: "Last",
		Locale: "en", Timezone: "UTC", LastLoginAt: model.OptionalTimeFromMillis(4),
		LastActivityAt:            model.OptionalTimeFromMillis(5),
		DisabledAt:                model.OptionalTimeFromMillis(6),
		DefaultProfilePictureSeed: strings.Repeat("a", model.ProfilePictureSeedLength),
	}
	row := newUserRow(user)
	got, err := row.model()
	if err != nil {
		t.Fatal(err)
	}
	if *got != *user {
		t.Fatalf("row.model() = %#v, want %#v", got, user)
	}
}

func TestUserRowRehydrationRejectsInvalidPersistedState(t *testing.T) {
	t.Parallel()
	valid := userRow{
		ID: model.NewUserID().String(), CreatedAt: model.TimeFromMillis(1),
		UpdatedAt: model.TimeFromMillis(2), Revision: 1, Username: "user",
		Email: "user@example.edu", Locale: "en", Timezone: "UTC",
		DefaultProfilePictureSeed: strings.Repeat("a", model.ProfilePictureSeedLength),
	}
	tests := []struct {
		name, field string
		mutate      func(*userRow)
	}{
		{name: "user id", field: "id", mutate: func(row *userRow) { row.ID = "bad" }},
		{name: "default picture file id", field: "default_profile_picture_file_id", mutate: func(row *userRow) { row.DefaultProfilePictureFileID = sql.NullString{String: "bad", Valid: true} }},
		{name: "custom picture file id", field: "custom_profile_picture_file_id", mutate: func(row *userRow) { row.CustomProfilePictureFileID = sql.NullString{Valid: true} }},
		{name: "domain state", field: "email", mutate: func(row *userRow) { row.Email = "invalid" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			row := valid
			test.mutate(&row)
			_, err := row.model()
			var persisted *persistedStateError
			if !errors.As(err, &persisted) || persisted.Entity != "user" || persisted.Field != test.field {
				t.Fatalf("model() error = %v, want user.%s persisted-state error", err, test.field)
			}
		})
	}
}
