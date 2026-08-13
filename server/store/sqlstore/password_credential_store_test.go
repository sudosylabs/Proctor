// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"errors"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestPasswordCredentialRowConversion(t *testing.T) {
	credential := &model.PasswordCredential{
		ID:        model.PasswordCredentialID(model.NewId()),
		CreatedAt: model.TimeFromMillis(1), UpdatedAt: model.TimeFromMillis(2),
		ArchivedAt: model.OptionalTimeFromMillis(3),
		UserID:     model.UserID(model.NewId()), PasswordHash: "$argon2id$test",
		PasswordChangedAt: model.TimeFromMillis(4),
	}
	row := newPasswordCredentialRow(credential)
	got, err := row.model()
	if err != nil {
		t.Fatal(err)
	}
	if *got != *credential {
		t.Fatalf("row.model() = %#v, want %#v", got, credential)
	}
}

func TestPasswordCredentialRowRehydrationRejectsInvalidPersistedState(t *testing.T) {
	t.Parallel()
	valid := passwordCredentialRow{
		ID: model.NewPasswordCredentialID().String(), CreatedAt: model.TimeFromMillis(1),
		UpdatedAt: model.TimeFromMillis(2), UserID: model.NewUserID().String(),
		PasswordHash: "$argon2id$test", PasswordChangedAt: model.TimeFromMillis(2),
	}
	tests := []struct {
		name, field string
		mutate      func(*passwordCredentialRow)
	}{
		{name: "credential id", field: "id", mutate: func(row *passwordCredentialRow) { row.ID = "bad" }},
		{name: "user id", field: "user_id", mutate: func(row *passwordCredentialRow) { row.UserID = "bad" }},
		{name: "domain state", field: "password_changed_at", mutate: func(row *passwordCredentialRow) { row.PasswordChangedAt = model.TimeFromMillis(0) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			row := valid
			test.mutate(&row)
			_, err := row.model()
			var persisted *persistedStateError
			if !errors.As(err, &persisted) || persisted.Entity != "password_credential" || persisted.Field != test.field {
				t.Fatalf("model() error = %v, want password_credential.%s persisted-state error", err, test.field)
			}
		})
	}
}
