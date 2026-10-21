// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestClassRowRehydrationRejectsInvalidPersistedState(t *testing.T) {
	t.Parallel()

	valid := classRow{
		ID:               model.NewClassID().String(),
		CreatedAt:        time.Unix(10, 0).UTC(),
		UpdatedAt:        time.Unix(11, 0).UTC(),
		Revision:         1,
		ProgrammeLevelID: model.NewProgrammeLevelID().String(),
		AcademicPeriodID: model.NewAcademicPeriodID().String(),
		Name:             "class-a",
		DisplayName:      "Class A",
	}

	tests := []struct {
		name  string
		row   classRow
		field string
	}{
		{name: "class id", row: replaceClassRow(valid, func(row *classRow) { row.ID = "bad" }), field: "id"},
		{name: "programme level id", row: replaceClassRow(valid, func(row *classRow) { row.ProgrammeLevelID = "bad" }), field: "programme_level_id"},
		{name: "academic period id", row: replaceClassRow(valid, func(row *classRow) { row.AcademicPeriodID = "bad" }), field: "academic_period_id"},
		{name: "domain state", row: replaceClassRow(valid, func(row *classRow) { row.Revision = 0 }), field: "revision"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.row.model()
			var persisted *persistedStateError
			require.ErrorAs(t, err, &persisted, "model() error")
			require.Equal(t, "class", persisted.Entity, "persisted-state context")
			require.Equal(t, test.field, persisted.Field, "persisted-state context")
		})
	}
}

func TestClassRowRehydrationReturnsValidatedModel(t *testing.T) {
	t.Parallel()

	row := classRow{
		ID:               model.NewClassID().String(),
		CreatedAt:        time.Unix(10, 0).UTC(),
		UpdatedAt:        time.Unix(11, 0).UTC(),
		Revision:         2,
		ProgrammeLevelID: model.NewProgrammeLevelID().String(),
		AcademicPeriodID: model.NewAcademicPeriodID().String(),
		Name:             "class-a",
		DisplayName:      "Class A",
		Description:      "Description",
	}

	got, err := row.model()
	require.NoError(t, err)
	require.NoError(t, got.Validate())
	require.Equal(t, row.ID, got.ID.String(), "model() = %#v, want row identity and lineage fields", got)
	require.Equal(t, row.ProgrammeLevelID, got.ProgrammeLevelID.String(), "model() = %#v, want row identity and lineage fields", got)
	require.Equal(t, row.AcademicPeriodID, got.AcademicPeriodID.String(), "model() = %#v, want row identity and lineage fields", got)
	require.Equal(t, row.Revision, got.Revision, "model() = %#v, want row identity and lineage fields", got)
}

func replaceClassRow(row classRow, replace func(*classRow)) classRow {
	replace(&row)
	return row
}
