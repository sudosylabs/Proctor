// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestAcademicUnitRowRehydrationRejectsInvalidPersistedState(t *testing.T) {
	t.Parallel()

	valid := academicUnitRow{
		ID:            model.NewAcademicUnitID().String(),
		CreatedAt:     model.TimeFromMillis(1),
		UpdatedAt:     model.TimeFromMillis(2),
		Revision:      1,
		InstitutionID: model.NewInstitutionID().String(),
		Name:          "computing",
		DisplayName:   "Computing",
	}

	tests := []struct {
		name  string
		row   academicUnitRow
		field string
	}{
		{
			name:  "academic unit id",
			row:   replaceAcademicUnitRow(valid, func(row *academicUnitRow) { row.ID = "bad" }),
			field: "id",
		},
		{
			name:  "institution id",
			row:   replaceAcademicUnitRow(valid, func(row *academicUnitRow) { row.InstitutionID = "bad" }),
			field: "institution_id",
		},
		{
			name: "nullable parent id",
			row: replaceAcademicUnitRow(valid, func(row *academicUnitRow) {
				row.ParentID = sql.NullString{String: "bad", Valid: true}
			}),
			field: "parent_id",
		},
		{
			name: "present empty parent id",
			row: replaceAcademicUnitRow(valid, func(row *academicUnitRow) {
				row.ParentID = sql.NullString{Valid: true}
			}),
			field: "parent_id",
		},
		{
			name:  "domain state",
			row:   replaceAcademicUnitRow(valid, func(row *academicUnitRow) { row.ParentID = sql.NullString{String: row.ID, Valid: true} }),
			field: "parent_id",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.row.model()
			var persisted *persistedStateError
			require.ErrorAs(t, err, &persisted, "model() error")
			require.Equal(t, "academic_unit", persisted.Entity, "persisted-state context")
			require.Equal(t, test.field, persisted.Field, "persisted-state context")
		})
	}
}

func TestAcademicUnitRowConversion(t *testing.T) {
	unitID, err := model.ParseAcademicUnitID(model.NewID())
	require.NoError(t, err)
	institutionID, err := model.ParseInstitutionID(model.NewID())
	require.NoError(t, err)
	parentID, err := model.ParseAcademicUnitID(model.NewID())
	require.NoError(t, err)
	unit := &model.AcademicUnit{
		ID:            unitID,
		CreatedAt:     model.TimeFromMillis(1),
		UpdatedAt:     model.TimeFromMillis(2),
		ArchivedAt:    model.OptionalTimeFromMillis(3),
		Revision:      7,
		InstitutionID: institutionID,
		ParentID:      parentID,
		Name:          "computing",
		DisplayName:   "Computing",
		Description:   "School",
	}
	row := newAcademicUnitRow(unit)
	require.True(t, row.ParentID.Valid, "non-empty parent ID became NULL")
	got, err := row.model()
	require.NoError(t, err)
	require.Equal(t, *unit, *got, "row.model() = %#v, want %#v", got, unit)

	unit.ParentID = ""
	row = newAcademicUnitRow(unit)
	require.False(t, row.ParentID.Valid, "empty parent ID did not become NULL")
	got, err = row.model()
	require.NoError(t, err)
	require.Empty(t, got.ParentID)
}

func replaceAcademicUnitRow(row academicUnitRow, replace func(*academicUnitRow)) academicUnitRow {
	replace(&row)
	return row
}
