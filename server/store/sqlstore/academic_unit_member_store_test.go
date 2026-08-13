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

func TestAcademicUnitMemberRowRehydrationRejectsInvalidPersistedState(t *testing.T) {
	t.Parallel()

	valid := academicUnitMemberRow{
		ID:             model.NewAcademicUnitMemberID().String(),
		CreatedAt:      time.Unix(10, 0).UTC(),
		UpdatedAt:      time.Unix(11, 0).UTC(),
		Revision:       1,
		AcademicUnitID: model.NewAcademicUnitID().String(),
		UserID:         model.NewUserID().String(),
		StartAt:        time.Unix(10, 0).UTC(),
	}

	tests := []struct {
		name  string
		row   academicUnitMemberRow
		field string
	}{
		{name: "member id", row: replaceAcademicUnitMemberRow(valid, func(row *academicUnitMemberRow) { row.ID = "bad" }), field: "id"},
		{name: "academic unit id", row: replaceAcademicUnitMemberRow(valid, func(row *academicUnitMemberRow) { row.AcademicUnitID = "bad" }), field: "academic_unit_id"},
		{name: "user id", row: replaceAcademicUnitMemberRow(valid, func(row *academicUnitMemberRow) { row.UserID = "bad" }), field: "user_id"},
		{name: "domain state", row: replaceAcademicUnitMemberRow(valid, func(row *academicUnitMemberRow) {
			row.EndAt = sql.NullTime{Time: row.StartAt, Valid: true}
		}), field: "end_at"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.row.model()
			var persisted *persistedStateError
			if !errors.As(err, &persisted) {
				t.Fatalf("model() error = %v, want persisted-state error", err)
			}
			if persisted.Entity != "academic_unit_member" || persisted.Field != test.field {
				t.Fatalf("persisted-state context = %s.%s, want academic_unit_member.%s", persisted.Entity, persisted.Field, test.field)
			}
		})
	}
}

func TestAcademicUnitMemberRowRehydrationReturnsValidatedModel(t *testing.T) {
	t.Parallel()

	row := academicUnitMemberRow{
		ID:             model.NewAcademicUnitMemberID().String(),
		CreatedAt:      time.Unix(10, 0).UTC(),
		UpdatedAt:      time.Unix(11, 0).UTC(),
		Revision:       2,
		AcademicUnitID: model.NewAcademicUnitID().String(),
		UserID:         model.NewUserID().String(),
		StartAt:        time.Unix(10, 0).UTC(),
		EndAt:          sql.NullTime{Time: time.Unix(20, 0).UTC(), Valid: true},
	}

	got, err := row.model()
	if err != nil {
		t.Fatal(err)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("rehydrated academic unit member is invalid: %v", err)
	}
	if got.ID.String() != row.ID || got.AcademicUnitID.String() != row.AcademicUnitID || got.UserID.String() != row.UserID || got.Revision != row.Revision {
		t.Fatalf("model() = %#v, want row identity and relationship fields", got)
	}
}

func replaceAcademicUnitMemberRow(row academicUnitMemberRow, replace func(*academicUnitMemberRow)) academicUnitMemberRow {
	replace(&row)
	return row
}
