// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestProgrammeLevelRowConversion(t *testing.T) {
	level := &model.ProgrammeLevel{
		Id:          model.NewId(),
		CreateAt:    1,
		UpdateAt:    2,
		DeleteAt:    3,
		ProgrammeId: model.NewId(),
		Name:        "year-1",
		DisplayName: "Year 1",
		Description: "First curriculum stage",
	}
	row := newProgrammeLevelRow(level)
	if got := row.model(); *got != *level {
		t.Fatalf("row.model() = %#v, want %#v", got, level)
	}
}
