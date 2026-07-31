// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestProgrammeRowConversion(t *testing.T) {
	programme := &model.Programme{
		Id:             model.NewId(),
		CreateAt:       1,
		UpdateAt:       2,
		DeleteAt:       3,
		AcademicUnitId: model.NewId(),
		Name:           "computer-science",
		DisplayName:    "Computer Science",
		Description:    "Course of study",
	}
	row := newProgrammeRow(programme)
	if got := row.model(); *got != *programme {
		t.Fatalf("row.model() = %#v, want %#v", got, programme)
	}
}
