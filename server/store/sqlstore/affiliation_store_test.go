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

func TestAffiliationRowRehydrationRejectsInvalidPersistedState(t *testing.T) {
	t.Parallel()

	valid := affiliationRow{
		ID:        model.NewAffiliationID().String(),
		CreatedAt: time.Unix(10, 0).UTC(),
		UpdatedAt: time.Unix(11, 0).UTC(),
		Revision:  1,
		UserID:    model.NewUserID().String(),
		Kind:      model.AffiliationStudent,
		StartAt:   time.Unix(10, 0).UTC(),
	}

	tests := []struct {
		name  string
		row   affiliationRow
		field string
	}{
		{name: "affiliation id", row: replaceAffiliationRow(valid, func(row *affiliationRow) { row.ID = "bad" }), field: "id"},
		{name: "user id", row: replaceAffiliationRow(valid, func(row *affiliationRow) { row.UserID = "bad" }), field: "user_id"},
		{name: "domain state", row: replaceAffiliationRow(valid, func(row *affiliationRow) { row.Kind = "unknown" }), field: "kind"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.row.model()
			var persisted *persistedStateError
			require.ErrorAs(t, err, &persisted, "model() error")
			require.Equal(t, "affiliation", persisted.Entity, "persisted-state context")
			require.Equal(t, test.field, persisted.Field, "persisted-state context")
		})
	}
}

func TestAffiliationRowRehydrationReturnsValidatedModel(t *testing.T) {
	t.Parallel()

	row := affiliationRow{
		ID:         model.NewAffiliationID().String(),
		CreatedAt:  time.Unix(10, 0).UTC(),
		UpdatedAt:  time.Unix(11, 0).UTC(),
		ArchivedAt: sql.NullTime{},
		Revision:   2,
		UserID:     model.NewUserID().String(),
		Kind:       model.AffiliationTeacher,
		StartAt:    time.Unix(10, 0).UTC(),
		EndAt:      sql.NullTime{Time: time.Unix(20, 0).UTC(), Valid: true},
	}

	got, err := row.model()
	require.NoError(t, err)
	require.NoError(t, got.Validate())
	require.Equal(t, row.ID, got.ID.String(), "model() = %#v, want row identity and relationship fields", got)
	require.Equal(t, row.UserID, got.UserID.String(), "model() = %#v, want row identity and relationship fields", got)
	require.Equal(t, row.Kind, got.Kind, "model() = %#v, want row identity and relationship fields", got)
	require.Equal(t, row.Revision, got.Revision, "model() = %#v, want row identity and relationship fields", got)
}

func replaceAffiliationRow(row affiliationRow, replace func(*affiliationRow)) affiliationRow {
	replace(&row)
	return row
}
