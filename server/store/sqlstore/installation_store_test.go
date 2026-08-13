// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"errors"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

func TestInstallationStateRowRehydrationRejectsInvalidPersistedState(t *testing.T) {
	t.Parallel()

	valid := installationStateRow{
		InitializedAt:       time.Unix(10, 0).UTC(),
		InstitutionID:       model.NewInstitutionID().String(),
		AdministratorUserID: model.NewUserID().String(),
	}
	tests := []struct {
		name  string
		row   installationStateRow
		field string
	}{
		{name: "institution id", row: replaceInstallationStateRow(valid, func(row *installationStateRow) { row.InstitutionID = "bad" }), field: "institution_id"},
		{name: "administrator user id", row: replaceInstallationStateRow(valid, func(row *installationStateRow) { row.AdministratorUserID = "bad" }), field: "administrator_user_id"},
		{name: "domain state", row: replaceInstallationStateRow(valid, func(row *installationStateRow) { row.InitializedAt = time.Time{} }), field: "initialized_at"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.row.model()
			var persisted *persistedStateError
			if !errors.As(err, &persisted) {
				t.Fatalf("model() error = %v, want persisted-state error", err)
			}
			if persisted.Entity != "installation" || persisted.Field != test.field {
				t.Fatalf("persisted-state context = %s.%s, want installation.%s", persisted.Entity, persisted.Field, test.field)
			}
		})
	}
}

func TestInstallationStateRowRehydrationReturnsValidatedModel(t *testing.T) {
	t.Parallel()

	row := installationStateRow{
		InitializedAt:       time.Unix(10, 0).UTC(),
		InstitutionID:       model.NewInstitutionID().String(),
		AdministratorUserID: model.NewUserID().String(),
	}
	state, err := row.model()
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Validate(); err != nil {
		t.Fatalf("rehydrated state is invalid: %v", err)
	}
	if state.InstitutionID.String() != row.InstitutionID || state.AdministratorUserID.String() != row.AdministratorUserID {
		t.Fatalf("model() = %#v, want persisted identities", state)
	}
}

func replaceInstallationStateRow(row installationStateRow, replace func(*installationStateRow)) installationStateRow {
	replace(&row)
	return row
}
