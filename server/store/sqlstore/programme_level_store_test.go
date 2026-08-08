// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

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
