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
		Revision:    7,
		Name:        "northbridge",
		DisplayName: "Northbridge",
		Description: "University",
	}
	row := newInstitutionRow(institution)
	got, err := row.model()
	if err != nil {
		t.Fatal(err)
	}
	if *got != *institution {
		t.Fatalf("row.model() = %#v, want %#v", got, institution)
	}
	if !row.CreatedAt.Equal(institution.CreatedAt) ||
		!row.UpdatedAt.Equal(institution.UpdatedAt) ||
		!row.ArchivedAt.Valid || !row.ArchivedAt.Time.Equal(institution.ArchivedAt.Time) {
		t.Fatalf("row times = %#v", row)
	}
}
