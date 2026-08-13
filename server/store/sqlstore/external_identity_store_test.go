// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"errors"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestExternalIdentityRowConversion(t *testing.T) {
	identity := &model.ExternalIdentity{
		ID:        model.ExternalIdentityID(model.NewId()),
		CreatedAt: model.TimeFromMillis(1), UpdatedAt: model.TimeFromMillis(2),
		ArchivedAt: model.OptionalTimeFromMillis(3),
		UserID:     model.UserID(model.NewId()), Provider: "campus-cas",
		Subject: "opaque-subject", LastSeenAt: model.OptionalTimeFromMillis(4),
	}
	row := newExternalIdentityRow(identity)
	got, err := row.model()
	if err != nil {
		t.Fatal(err)
	}
	if *got != *identity {
		t.Fatalf("row.model() = %#v, want %#v", got, identity)
	}
}

func TestExternalIdentityRowRehydrationRejectsInvalidPersistedState(t *testing.T) {
	t.Parallel()
	valid := externalIdentityRow{
		ID: model.NewExternalIdentityID().String(), CreatedAt: model.TimeFromMillis(1),
		UpdatedAt: model.TimeFromMillis(2), UserID: model.NewUserID().String(),
		Provider: "campus-cas", Subject: "opaque-subject",
	}
	tests := []struct {
		name, field string
		mutate      func(*externalIdentityRow)
	}{
		{name: "identity id", field: "id", mutate: func(row *externalIdentityRow) { row.ID = "bad" }},
		{name: "user id", field: "user_id", mutate: func(row *externalIdentityRow) { row.UserID = "bad" }},
		{name: "domain state", field: "provider", mutate: func(row *externalIdentityRow) { row.Provider = "BAD PROVIDER" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			row := valid
			test.mutate(&row)
			_, err := row.model()
			var persisted *persistedStateError
			if !errors.As(err, &persisted) || persisted.Entity != "external_identity" || persisted.Field != test.field {
				t.Fatalf("model() error = %v, want external_identity.%s persisted-state error", err, test.field)
			}
		})
	}
}
