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

func TestProgrammeRowRehydrationRejectsInvalidPersistedState(t *testing.T) {
	t.Parallel()

	valid := programmeRow{
		ID:             model.NewProgrammeID().String(),
		CreatedAt:      time.Unix(10, 0).UTC(),
		UpdatedAt:      time.Unix(11, 0).UTC(),
		Revision:       1,
		AcademicUnitID: model.NewAcademicUnitID().String(),
		Name:           "computer-science",
		DisplayName:    "Computer Science",
	}
	tests := []struct {
		name  string
		row   programmeRow
		field string
	}{
		{name: "programme id", row: replaceProgrammeRow(valid, func(row *programmeRow) { row.ID = "bad" }), field: "id"},
		{name: "academic unit id", row: replaceProgrammeRow(valid, func(row *programmeRow) { row.AcademicUnitID = "bad" }), field: "academic_unit_id"},
		{name: "domain state", row: replaceProgrammeRow(valid, func(row *programmeRow) { row.Name = "" }), field: "name"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.row.model()
			var persisted *persistedStateError
			require.ErrorAs(t, err, &persisted, "model() error")
			require.Equal(t, "programme", persisted.Entity, "persisted-state context")
			require.Equal(t, test.field, persisted.Field, "persisted-state context")
		})
	}
}

func TestProgrammeRowConversion(t *testing.T) {
	id, err := model.ParseProgrammeID(model.NewID())
	require.NoError(t, err)
	unitID, err := model.ParseAcademicUnitID(model.NewID())
	require.NoError(t, err)
	programme := &model.Programme{
		ID:             id,
		CreatedAt:      model.TimeFromMillis(1),
		UpdatedAt:      model.TimeFromMillis(2),
		ArchivedAt:     model.OptionalTimeFromMillis(3),
		Revision:       7,
		AcademicUnitID: unitID,
		Name:           "computer-science",
		DisplayName:    "Computer Science",
		Description:    "Course of study",
	}
	row := newProgrammeRow(programme)
	got, err := row.model()
	require.NoError(t, err)
	require.Equal(t, *programme, *got, "row.model() = %#v, want %#v", got, programme)
	require.True(t, row.CreatedAt.Equal(programme.CreatedAt))
	require.True(t, row.UpdatedAt.Equal(programme.UpdatedAt))
	require.True(t, row.ArchivedAt.Valid)
	require.True(t, row.ArchivedAt.Time.Equal(programme.ArchivedAt.Time))
}

func replaceProgrammeRow(row programmeRow, replace func(*programmeRow)) programmeRow {
	replace(&row)
	return row
}
