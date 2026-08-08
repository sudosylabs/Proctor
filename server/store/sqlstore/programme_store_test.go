// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

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
