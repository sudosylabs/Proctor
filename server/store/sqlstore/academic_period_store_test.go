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
	"time"

	"github.com/stretchr/testify/require"

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
			require.ErrorAs(t, err, &persisted, "model() error")
			require.Equal(t, "academic_period", persisted.Entity, "persisted-state context")
			require.Equal(t, test.field, persisted.Field, "persisted-state context")
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
	require.NoError(t, err)
	require.NoError(t, got.Validate())
	require.Equal(t, row.ID, got.ID.String(), "model() = %#v, want row identity and ownership fields", got)
	require.Equal(t, row.InstitutionID.String, got.Owner.InstitutionID.String(), "model() = %#v, want row identity and ownership fields", got)
	require.Equal(t, row.Revision, got.Revision, "model() = %#v, want row identity and ownership fields", got)
}

func replaceAcademicPeriodRow(row academicPeriodRow, replace func(*academicPeriodRow)) academicPeriodRow {
	replace(&row)
	return row
}
