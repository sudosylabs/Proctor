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

func TestInstallationStateRowRehydrationRejectsInvalidPersistedState(t *testing.T) {
	t.Parallel()

	valid := installationStateRow{
		InitializedAt:       time.Unix(10, 0).UTC(),
		InstitutionID:       model.NewInstitutionID().String(),
		AdministratorUserID: model.NewUserID().String(),
	}
	tests := []struct {
		name  string
		row   installationStateRow
		field string
	}{
		{name: "institution id", row: replaceInstallationStateRow(valid, func(row *installationStateRow) { row.InstitutionID = "bad" }), field: "institution_id"},
		{name: "administrator user id", row: replaceInstallationStateRow(valid, func(row *installationStateRow) { row.AdministratorUserID = "bad" }), field: "administrator_user_id"},
		{name: "domain state", row: replaceInstallationStateRow(valid, func(row *installationStateRow) { row.InitializedAt = time.Time{} }), field: "initialized_at"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.row.model()
			var persisted *persistedStateError
			require.ErrorAs(t, err, &persisted, "model() error")
			require.Equal(t, "installation", persisted.Entity, "persisted-state context")
			require.Equal(t, test.field, persisted.Field, "persisted-state context")
		})
	}
}

func TestInstallationStateRowRehydrationReturnsValidatedModel(t *testing.T) {
	t.Parallel()

	row := installationStateRow{
		InitializedAt:       time.Unix(10, 0).UTC(),
		InstitutionID:       model.NewInstitutionID().String(),
		AdministratorUserID: model.NewUserID().String(),
	}
	state, err := row.model()
	require.NoError(t, err)
	require.NoError(t, state.Validate())
	require.Equal(t, row.InstitutionID, state.InstitutionID.String(), "model() = %#v, want persisted identities", state)
	require.Equal(t, row.AdministratorUserID, state.AdministratorUserID.String(), "model() = %#v, want persisted identities", state)
}

func replaceInstallationStateRow(row installationStateRow, replace func(*installationStateRow)) installationStateRow {
	replace(&row)
	return row
}
