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

func TestProgrammeLevelRowRehydrationRejectsInvalidPersistedState(t *testing.T) {
	t.Parallel()

	valid := programmeLevelRow{
		ID:          model.NewProgrammeLevelID().String(),
		CreatedAt:   time.Unix(10, 0).UTC(),
		UpdatedAt:   time.Unix(11, 0).UTC(),
		Revision:    1,
		ProgrammeID: model.NewProgrammeID().String(),
		Name:        "year-1",
		DisplayName: "Year 1",
	}
	tests := []struct {
		name  string
		row   programmeLevelRow
		field string
	}{
		{name: "programme level id", row: replaceProgrammeLevelRow(valid, func(row *programmeLevelRow) { row.ID = "bad" }), field: "id"},
		{name: "programme id", row: replaceProgrammeLevelRow(valid, func(row *programmeLevelRow) { row.ProgrammeID = "bad" }), field: "programme_id"},
		{name: "domain state", row: replaceProgrammeLevelRow(valid, func(row *programmeLevelRow) { row.Name = "" }), field: "name"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.row.model()
			var persisted *persistedStateError
			require.ErrorAs(t, err, &persisted, "model() error")
			require.Equal(t, "programme_level", persisted.Entity, "persisted-state context")
			require.Equal(t, test.field, persisted.Field, "persisted-state context")
		})
	}
}

func TestProgrammeLevelRowConversion(t *testing.T) {
	id, err := model.ParseProgrammeLevelID(model.NewID())
	require.NoError(t, err)
	programmeID, err := model.ParseProgrammeID(model.NewID())
	require.NoError(t, err)
	level := &model.ProgrammeLevel{
		ID:          id,
		CreatedAt:   model.TimeFromMillis(1),
		UpdatedAt:   model.TimeFromMillis(2),
		ArchivedAt:  model.OptionalTimeFromMillis(3),
		Revision:    7,
		ProgrammeID: programmeID,
		Name:        "year-1",
		DisplayName: "Year 1",
		Description: "First curriculum stage",
	}
	row := newProgrammeLevelRow(level)
	got, err := row.model()
	require.NoError(t, err)
	require.Equal(t, *level, *got, "row.model() = %#v, want %#v", got, level)
	require.True(t, row.CreatedAt.Equal(level.CreatedAt))
	require.True(t, row.UpdatedAt.Equal(level.UpdatedAt))
	require.True(t, row.ArchivedAt.Valid)
	require.True(t, row.ArchivedAt.Time.Equal(level.ArchivedAt.Time))
}

func replaceProgrammeLevelRow(row programmeLevelRow, replace func(*programmeLevelRow)) programmeLevelRow {
	replace(&row)
	return row
}
