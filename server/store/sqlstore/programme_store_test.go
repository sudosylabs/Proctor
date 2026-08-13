// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"errors"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

func TestProgrammeRowRehydrationRejectsInvalidPersistedState(t *testing.T) {
	t.Parallel()

	valid := programmeRow{
		ID:             model.NewProgrammeID().String(),
		CreatedAt:      time.Unix(10, 0).UTC(),
		UpdatedAt:      time.Unix(11, 0).UTC(),
		Revision:       1,
		AcademicUnitID: model.NewAcademicUnitID().String(),
		Name:           "computer-science",
		DisplayName:    "Computer Science",
	}
	tests := []struct {
		name  string
		row   programmeRow
		field string
	}{
		{name: "programme id", row: replaceProgrammeRow(valid, func(row *programmeRow) { row.ID = "bad" }), field: "id"},
		{name: "academic unit id", row: replaceProgrammeRow(valid, func(row *programmeRow) { row.AcademicUnitID = "bad" }), field: "academic_unit_id"},
		{name: "domain state", row: replaceProgrammeRow(valid, func(row *programmeRow) { row.Name = "" }), field: "name"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.row.model()
			var persisted *persistedStateError
			if !errors.As(err, &persisted) {
				t.Fatalf("model() error = %v, want persisted-state error", err)
			}
			if persisted.Entity != "programme" || persisted.Field != test.field {
				t.Fatalf("persisted-state context = %s.%s, want programme.%s", persisted.Entity, persisted.Field, test.field)
			}
		})
	}
}

func TestProgrammeRowConversion(t *testing.T) {
	id, err := model.ParseProgrammeID(model.NewId())
	if err != nil {
		t.Fatal(err)
	}
	unitID, err := model.ParseAcademicUnitID(model.NewId())
	if err != nil {
		t.Fatal(err)
	}
	programme := &model.Programme{
		ID:             id,
		CreatedAt:      model.TimeFromMillis(1),
		UpdatedAt:      model.TimeFromMillis(2),
		ArchivedAt:     model.OptionalTimeFromMillis(3),
		Revision:       7,
		AcademicUnitID: unitID,
		Name:           "computer-science",
		DisplayName:    "Computer Science",
		Description:    "Course of study",
	}
	row := newProgrammeRow(programme)
	got, err := row.model()
	if err != nil {
		t.Fatal(err)
	}
	if *got != *programme {
		t.Fatalf("row.model() = %#v, want %#v", got, programme)
	}
	if !row.CreatedAt.Equal(programme.CreatedAt) ||
		!row.UpdatedAt.Equal(programme.UpdatedAt) ||
		!row.ArchivedAt.Valid || !row.ArchivedAt.Time.Equal(programme.ArchivedAt.Time) {
		t.Fatalf("row times = %#v", row)
	}
}

func replaceProgrammeRow(row programmeRow, replace func(*programmeRow)) programmeRow {
	replace(&row)
	return row
}
