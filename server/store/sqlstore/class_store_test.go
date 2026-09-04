// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"errors"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

func TestClassRowRehydrationRejectsInvalidPersistedState(t *testing.T) {
	t.Parallel()

	valid := classRow{
		ID:               model.NewClassID().String(),
		CreatedAt:        time.Unix(10, 0).UTC(),
		UpdatedAt:        time.Unix(11, 0).UTC(),
		Revision:         1,
		ProgrammeLevelID: model.NewProgrammeLevelID().String(),
		AcademicPeriodID: model.NewAcademicPeriodID().String(),
		Name:             "class-a",
		DisplayName:      "Class A",
	}

	tests := []struct {
		name  string
		row   classRow
		field string
	}{
		{name: "class id", row: replaceClassRow(valid, func(row *classRow) { row.ID = "bad" }), field: "id"},
		{name: "programme level id", row: replaceClassRow(valid, func(row *classRow) { row.ProgrammeLevelID = "bad" }), field: "programme_level_id"},
		{name: "academic period id", row: replaceClassRow(valid, func(row *classRow) { row.AcademicPeriodID = "bad" }), field: "academic_period_id"},
		{name: "domain state", row: replaceClassRow(valid, func(row *classRow) { row.Revision = 0 }), field: "revision"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.row.model()
			var persisted *persistedStateError
			if !errors.As(err, &persisted) {
				t.Fatalf("model() error = %v, want persisted-state error", err)
			}
			if persisted.Entity != "class" || persisted.Field != test.field {
				t.Fatalf("persisted-state context = %s.%s, want class.%s", persisted.Entity, persisted.Field, test.field)
			}
		})
	}
}

func TestClassRowRehydrationReturnsValidatedModel(t *testing.T) {
	t.Parallel()

	row := classRow{
		ID:               model.NewClassID().String(),
		CreatedAt:        time.Unix(10, 0).UTC(),
		UpdatedAt:        time.Unix(11, 0).UTC(),
		Revision:         2,
		ProgrammeLevelID: model.NewProgrammeLevelID().String(),
		AcademicPeriodID: model.NewAcademicPeriodID().String(),
		Name:             "class-a",
		DisplayName:      "Class A",
		Description:      "Description",
	}

	got, err := row.model()
	if err != nil {
		t.Fatal(err)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("rehydrated class is invalid: %v", err)
	}
	if got.ID.String() != row.ID || got.ProgrammeLevelID.String() != row.ProgrammeLevelID || got.AcademicPeriodID.String() != row.AcademicPeriodID || got.Revision != row.Revision {
		t.Fatalf("model() = %#v, want row identity and lineage fields", got)
	}
}

func replaceClassRow(row classRow, replace func(*classRow)) classRow {
	replace(&row)
	return row
}
