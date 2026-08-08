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
		Revision:    1,
		ProgrammeID: programmeID,
		Name:        "year-1",
		DisplayName: "Year 1",
		Description: "First curriculum stage",
	}
	row := newProgrammeLevelRow(level)
	if got := row.model(); *got != *level {
		t.Fatalf("row.model() = %#v, want %#v", got, level)
	}
	if row.CreateAt != 1 || row.UpdateAt != 2 || row.DeleteAt != 3 {
		t.Fatalf("row millis = %#v", row)
	}
}
