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

func TestProgrammeLevelRowRehydrationRejectsInvalidPersistedState(t *testing.T) {
	t.Parallel()

	valid := programmeLevelRow{
		ID:          model.NewProgrammeLevelID().String(),
		CreatedAt:   time.Unix(10, 0).UTC(),
		UpdatedAt:   time.Unix(11, 0).UTC(),
		Revision:    1,
		ProgrammeID: model.NewProgrammeID().String(),
		Name:        "year-1",
		DisplayName: "Year 1",
	}
	tests := []struct {
		name  string
		row   programmeLevelRow
		field string
	}{
		{name: "programme level id", row: replaceProgrammeLevelRow(valid, func(row *programmeLevelRow) { row.ID = "bad" }), field: "id"},
		{name: "programme id", row: replaceProgrammeLevelRow(valid, func(row *programmeLevelRow) { row.ProgrammeID = "bad" }), field: "programme_id"},
		{name: "domain state", row: replaceProgrammeLevelRow(valid, func(row *programmeLevelRow) { row.Name = "" }), field: "name"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.row.model()
			var persisted *persistedStateError
			if !errors.As(err, &persisted) {
				t.Fatalf("model() error = %v, want persisted-state error", err)
			}
			if persisted.Entity != "programme_level" || persisted.Field != test.field {
				t.Fatalf("persisted-state context = %s.%s, want programme_level.%s", persisted.Entity, persisted.Field, test.field)
			}
		})
	}
}

func TestProgrammeLevelRowConversion(t *testing.T) {
	id, err := model.ParseProgrammeLevelID(model.NewId())
	if err != nil {
		t.Fatal(err)
	}
	programmeID, err := model.ParseProgrammeID(model.NewId())
	if err != nil {
		t.Fatal(err)
	}
	level := &model.ProgrammeLevel{
		ID:          id,
		CreatedAt:   model.TimeFromMillis(1),
		UpdatedAt:   model.TimeFromMillis(2),
		ArchivedAt:  model.OptionalTimeFromMillis(3),
		Revision:    7,
		ProgrammeID: programmeID,
		Name:        "year-1",
		DisplayName: "Year 1",
		Description: "First curriculum stage",
	}
	row := newProgrammeLevelRow(level)
	got, err := row.model()
	if err != nil {
		t.Fatal(err)
	}
	if *got != *level {
		t.Fatalf("row.model() = %#v, want %#v", got, level)
	}
	if !row.CreatedAt.Equal(level.CreatedAt) ||
		!row.UpdatedAt.Equal(level.UpdatedAt) ||
		!row.ArchivedAt.Valid || !row.ArchivedAt.Time.Equal(level.ArchivedAt.Time) {
		t.Fatalf("row times = %#v", row)
	}
}

func replaceProgrammeLevelRow(row programmeLevelRow, replace func(*programmeLevelRow)) programmeLevelRow {
	replace(&row)
	return row
}
