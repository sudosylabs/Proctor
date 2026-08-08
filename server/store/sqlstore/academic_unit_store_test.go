// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestAcademicUnitRowConversion(t *testing.T) {
	unitID, err := model.ParseAcademicUnitID(model.NewId())
	if err != nil {
		t.Fatal(err)
	}
	institutionID, err := model.ParseInstitutionID(model.NewId())
	if err != nil {
		t.Fatal(err)
	}
	parentID, err := model.ParseAcademicUnitID(model.NewId())
	if err != nil {
		t.Fatal(err)
	}
	unit := &model.AcademicUnit{
		ID:            unitID,
		CreatedAt:     model.TimeFromMillis(1),
		UpdatedAt:     model.TimeFromMillis(2),
		ArchivedAt:    model.OptionalTimeFromMillis(3),
		Revision:      7,
		InstitutionID: institutionID,
		ParentID:      parentID,
		Name:          "computing",
		DisplayName:   "Computing",
		Description:   "School",
	}
	row := newAcademicUnitRow(unit)
	if !row.ParentID.Valid {
		t.Fatal("non-empty parent ID became NULL")
	}
	got, err := row.model()
	if err != nil {
		t.Fatal(err)
	}
	if *got != *unit {
		t.Fatalf("row.model() = %#v, want %#v", got, unit)
	}

	unit.ParentID = ""
	row = newAcademicUnitRow(unit)
	if row.ParentID.Valid {
		t.Fatal("empty parent ID did not become NULL")
	}
	got, err = row.model()
	if err != nil {
		t.Fatal(err)
	}
	if got.ParentID != "" {
		t.Fatalf("NULL parent mapped to %q", got.ParentID)
	}
}
