// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestAcademicUnitRowConversion(t *testing.T) {
	unit := &model.AcademicUnit{
		Id:            model.NewId(),
		CreateAt:      1,
		UpdateAt:      2,
		DeleteAt:      3,
		InstitutionId: model.NewId(),
		ParentId:      model.NewId(),
		Name:          "computing",
		DisplayName:   "Computing",
		Description:   "School",
	}
	row := newAcademicUnitRow(unit)
	if !row.ParentID.Valid {
		t.Fatal("non-empty parent ID became NULL")
	}
	if got := row.model(); *got != *unit {
		t.Fatalf("row.model() = %#v, want %#v", got, unit)
	}

	unit.ParentId = ""
	row = newAcademicUnitRow(unit)
	if row.ParentID.Valid {
		t.Fatal("empty parent ID did not become NULL")
	}
}
