// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestAcademicPeriodRowConversion(t *testing.T) {
	id, err := model.ParseAcademicPeriodID(model.NewId())
	if err != nil {
		t.Fatal(err)
	}
	institutionID, err := model.ParseInstitutionID(model.NewId())
	if err != nil {
		t.Fatal(err)
	}
	period := &model.AcademicPeriod{
		ID:            id,
		CreatedAt:     model.TimeFromMillis(1),
		UpdatedAt:     model.TimeFromMillis(2),
		ArchivedAt:    model.OptionalTimeFromMillis(3),
		Revision:      7,
		InstitutionID: institutionID,
		Name:          "2026-2027",
		DisplayName:   "Academic Year 2026-2027",
		Description:   "Primary academic year",
		StartsAt:      model.TimeFromMillis(4),
		EndsAt:        model.TimeFromMillis(5),
	}
	row := newAcademicPeriodRow(period)
	got, err := row.model()
	if err != nil {
		t.Fatal(err)
	}
	if *got != *period {
		t.Fatalf("row.model() = %#v, want %#v", got, period)
	}
	if !row.CreatedAt.Equal(period.CreatedAt) ||
		!row.UpdatedAt.Equal(period.UpdatedAt) ||
		!row.ArchivedAt.Valid || !row.ArchivedAt.Time.Equal(period.ArchivedAt.Time) ||
		!row.StartAt.Equal(period.StartsAt) || !row.EndAt.Equal(period.EndsAt) {
		t.Fatalf("row times = %#v", row)
	}
}
