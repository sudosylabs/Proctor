// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"errors"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestUserTokenRowConversion(t *testing.T) {
	token := &model.UserToken{
		ID:        model.UserTokenID(model.NewId()),
		CreatedAt: model.TimeFromMillis(1), UpdatedAt: model.TimeFromMillis(2),
		ArchivedAt: model.OptionalTimeFromMillis(3),
		UserID:     model.UserID(model.NewId()), Purpose: model.UserTokenPasswordReset,
		TokenHash:  model.HashToken(model.NewCredentialToken()),
		Target:     "student@example.edu",
		ExpiresAt:  model.TimeFromMillis(4),
		ConsumedAt: model.OptionalTimeFromMillis(3),
	}
	row := newUserTokenRow(token)
	got, err := row.model()
	if err != nil {
		t.Fatal(err)
	}
	if *got != *token {
		t.Fatalf("row.model() = %#v, want %#v", got, token)
	}
}

func TestUserTokenRowRehydrationRejectsInvalidPersistedState(t *testing.T) {
	t.Parallel()
	valid := userTokenRow{
		ID: model.NewUserTokenID().String(), CreatedAt: model.TimeFromMillis(1),
		UpdatedAt: model.TimeFromMillis(1), UserID: model.NewUserID().String(),
		Purpose:   model.UserTokenPasswordReset,
		TokenHash: model.HashToken(model.NewCredentialToken()), Target: "student@example.edu",
		ExpiresAt: model.TimeFromMillis(3),
	}
	tests := []struct {
		name, field string
		mutate      func(*userTokenRow)
	}{
		{name: "token id", field: "id", mutate: func(row *userTokenRow) { row.ID = "bad" }},
		{name: "user id", field: "user_id", mutate: func(row *userTokenRow) { row.UserID = "bad" }},
		{name: "domain state", field: "purpose", mutate: func(row *userTokenRow) { row.Purpose = "unknown" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			row := valid
			test.mutate(&row)
			_, err := row.model()
			var persisted *persistedStateError
			if !errors.As(err, &persisted) || persisted.Entity != "user_token" || persisted.Field != test.field {
				t.Fatalf("model() error = %v, want user_token.%s persisted-state error", err, test.field)
			}
		})
	}
}
