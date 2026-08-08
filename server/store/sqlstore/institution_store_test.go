// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestInstitutionRowConversion(t *testing.T) {
	id, err := model.ParseInstitutionID(model.NewId())
	if err != nil {
		t.Fatal(err)
	}
	institution := &model.Institution{
		ID:          id,
		CreatedAt:   model.TimeFromMillis(1),
		UpdatedAt:   model.TimeFromMillis(2),
		ArchivedAt:  model.OptionalTimeFromMillis(3),
		Revision:    1,
		Name:        "northbridge",
		DisplayName: "Northbridge",
		Description: "University",
	}
	row := newInstitutionRow(institution)
	if got := row.model(); *got != *institution {
		t.Fatalf("row.model() = %#v, want %#v", got, institution)
	}
	if row.CreateAt != 1 || row.UpdateAt != 2 || row.DeleteAt != 3 {
		t.Fatalf("row millis = %#v", row)
	}
}
