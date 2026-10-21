// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestInstitutionRowRehydrationRejectsInvalidPersistedState(t *testing.T) {
	t.Parallel()

	valid := institutionRow{
		ID:          model.NewInstitutionID().String(),
		CreatedAt:   model.TimeFromMillis(1),
		UpdatedAt:   model.TimeFromMillis(2),
		Revision:    1,
		Name:        "northbridge",
		DisplayName: "Northbridge",
	}

	tests := []struct {
		name  string
		row   institutionRow
		field string
	}{
		{
			name:  "institution id",
			row:   replaceInstitutionRow(valid, func(row *institutionRow) { row.ID = "bad" }),
			field: "id",
		},
		{
			name:  "domain state",
			row:   replaceInstitutionRow(valid, func(row *institutionRow) { row.CreatedAt = model.TimeFromMillis(3) }),
			field: "updated_at",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.row.model()
			var persisted *persistedStateError
			require.ErrorAs(t, err, &persisted, "model() error")
			require.Equal(t, "institution", persisted.Entity, "persisted-state context")
			require.Equal(t, test.field, persisted.Field, "persisted-state context")
		})
	}
}

func TestInstitutionRowConversion(t *testing.T) {
	id, err := model.ParseInstitutionID(model.NewID())
	require.NoError(t, err)
	institution := &model.Institution{
		ID:           id,
		CreatedAt:    model.TimeFromMillis(1),
		UpdatedAt:    model.TimeFromMillis(2),
		ArchivedAt:   model.OptionalTimeFromMillis(3),
		Revision:     7,
		Name:         "northbridge",
		DisplayName:  "Northbridge",
		Description:  "University",
		ExamCapacity: model.DefaultExamCapacityPolicy(),
	}
	row := newInstitutionRow(institution)
	got, err := row.model()
	require.NoError(t, err)
	require.Equal(t, *institution, *got, "row.model() = %#v, want %#v", got, institution)
	require.True(t, row.CreatedAt.Equal(institution.CreatedAt))
	require.True(t, row.UpdatedAt.Equal(institution.UpdatedAt))
	require.True(t, row.ArchivedAt.Valid)
	require.True(t, row.ArchivedAt.Time.Equal(institution.ArchivedAt.Time))
}

func replaceInstitutionRow(row institutionRow, replace func(*institutionRow)) institutionRow {
	replace(&row)
	return row
}
