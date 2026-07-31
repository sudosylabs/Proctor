// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestInstitutionRowConversion(t *testing.T) {
	institution := &model.Institution{
		Id:          model.NewId(),
		CreateAt:    1,
		UpdateAt:    2,
		DeleteAt:    3,
		Name:        "northbridge",
		DisplayName: "Northbridge",
		Description: "University",
	}
	row := newInstitutionRow(institution)
	if got := row.model(); *got != *institution {
		t.Fatalf("row.model() = %#v, want %#v", got, institution)
	}
}
