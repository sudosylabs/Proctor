// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestClassRowConversion(t *testing.T) {
	class := &model.Class{
		Id:               model.NewId(),
		CreateAt:         1,
		UpdateAt:         2,
		DeleteAt:         3,
		ProgrammeLevelId: model.NewId(),
		AcademicPeriodId: model.NewId(),
		Name:             "class-a",
		DisplayName:      "Class A",
		Description:      "Student roster",
	}
	row := newClassRow(class)
	if got := row.model(); *got != *class {
		t.Fatalf("row.model() = %#v, want %#v", got, class)
	}
}
