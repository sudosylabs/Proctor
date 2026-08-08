// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestClassRowConversion(t *testing.T) {
	id, err := model.ParseClassID(model.NewId())
	if err != nil {
		t.Fatal(err)
	}
	levelID, err := model.ParseProgrammeLevelID(model.NewId())
	if err != nil {
		t.Fatal(err)
	}
	periodID, err := model.ParseAcademicPeriodID(model.NewId())
	if err != nil {
		t.Fatal(err)
	}
	class := &model.Class{
		ID:               id,
		CreatedAt:        model.TimeFromMillis(1),
		UpdatedAt:        model.TimeFromMillis(2),
		ArchivedAt:       model.OptionalTimeFromMillis(3),
		Revision:         4,
		ProgrammeLevelID: levelID,
		AcademicPeriodID: periodID,
		Name:             "class-a",
		DisplayName:      "Class A",
		Description:      "Student roster",
	}
	row := newClassRow(class)
	if got := row.model(); *got != *class {
		t.Fatalf("row.model() = %#v, want %#v", got, class)
	}
	if row.CreateAt != 1 || row.UpdateAt != 2 || row.DeleteAt != 3 || row.Revision != 4 {
		t.Fatalf("row millis = %#v", row)
	}
}
