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
			require.ErrorAs(t, err, &persisted, "model() error")
			require.Equal(t, "academic_unit_member", persisted.Entity, "persisted-state context")
			require.Equal(t, test.field, persisted.Field, "persisted-state context")
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
	require.NoError(t, err)
	require.NoError(t, got.Validate())
	require.Equal(t, row.ID, got.ID.String(), "model() = %#v, want row identity and relationship fields", got)
	require.Equal(t, row.AcademicUnitID, got.AcademicUnitID.String(), "model() = %#v, want row identity and relationship fields", got)
	require.Equal(t, row.UserID, got.UserID.String(), "model() = %#v, want row identity and relationship fields", got)
	require.Equal(t, row.Revision, got.Revision, "model() = %#v, want row identity and relationship fields", got)
}

func replaceAcademicUnitMemberRow(row academicUnitMemberRow, replace func(*academicUnitMemberRow)) academicUnitMemberRow {
	replace(&row)
	return row
}
