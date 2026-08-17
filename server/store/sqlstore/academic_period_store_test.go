// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

func TestAcademicPeriodRowRehydrationRejectsInvalidPersistedState(t *testing.T) {
	t.Parallel()

	valid := academicPeriodRow{
		ID:            model.NewAcademicPeriodID().String(),
		CreatedAt:     time.Unix(10, 0).UTC(),
		UpdatedAt:     time.Unix(11, 0).UTC(),
		Revision:      1,
		OwnerType:     string(model.ResourceInstitution),
		InstitutionID: sql.NullString{String: model.NewInstitutionID().String(), Valid: true},
		Name:          "period",
		DisplayName:   "Period",
		StartAt:       time.Unix(20, 0).UTC(),
		EndAt:         time.Unix(30, 0).UTC(),
	}

	tests := []struct {
		name  string
		row   academicPeriodRow
		field string
	}{
		{name: "period id", row: replaceAcademicPeriodRow(valid, func(row *academicPeriodRow) { row.ID = "bad" }), field: "id"},
		{name: "institution id", row: replaceAcademicPeriodRow(valid, func(row *academicPeriodRow) { row.InstitutionID.String = "bad" }), field: "institution_id"},
		{name: "ambiguous owner", row: replaceAcademicPeriodRow(valid, func(row *academicPeriodRow) {
			row.AcademicUnitID = sql.NullString{String: model.NewAcademicUnitID().String(), Valid: true}
		}), field: "owner"},
		{name: "domain state", row: replaceAcademicPeriodRow(valid, func(row *academicPeriodRow) { row.EndAt = row.StartAt }), field: "end_at"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.row.model()
			var persisted *persistedStateError
			if !errors.As(err, &persisted) {
				t.Fatalf("model() error = %v, want persisted-state error", err)
			}
			if persisted.Entity != "academic_period" || persisted.Field != test.field {
				t.Fatalf("persisted-state context = %s.%s, want academic_period.%s", persisted.Entity, persisted.Field, test.field)
			}
		})
	}
}

func TestAcademicPeriodRowRehydrationReturnsValidatedModel(t *testing.T) {
	t.Parallel()

	row := academicPeriodRow{
		ID:            model.NewAcademicPeriodID().String(),
		CreatedAt:     time.Unix(10, 0).UTC(),
		UpdatedAt:     time.Unix(11, 0).UTC(),
		Revision:      2,
		OwnerType:     string(model.ResourceInstitution),
		InstitutionID: sql.NullString{String: model.NewInstitutionID().String(), Valid: true},
		Name:          "period",
		DisplayName:   "Period",
		Description:   "Description",
		StartAt:       time.Unix(20, 0).UTC(),
		EndAt:         time.Unix(30, 0).UTC(),
	}

	got, err := row.model()
	if err != nil {
		t.Fatal(err)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("rehydrated academic period is invalid: %v", err)
	}
	if got.ID.String() != row.ID || got.Owner.InstitutionID.String() != row.InstitutionID.String || got.Revision != row.Revision {
		t.Fatalf("model() = %#v, want row identity and ownership fields", got)
	}
}

func replaceAcademicPeriodRow(row academicPeriodRow, replace func(*academicPeriodRow)) academicPeriodRow {
	replace(&row)
	return row
}
