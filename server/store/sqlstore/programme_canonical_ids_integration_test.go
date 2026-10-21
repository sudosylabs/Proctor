//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"testing"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store/storetest"
)

func TestProgrammeCanonicalIDConstraintsRejectNoncanonicalValues(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	ctx := context.Background()

	institution, err := persistence.Institution().Save(ctx, &model.Institution{
		Name: "programme-constraint", DisplayName: "Programme Constraint",
	})
	require.NoError(t, err)
	unit, err := storetest.SaveAcademicUnit(ctx, persistence, &model.AcademicUnit{
		InstitutionID: institution.ID, Name: "unit", DisplayName: "Unit",
	})
	require.NoError(t, err)
	programme, err := storetest.SaveProgramme(ctx, persistence, &model.Programme{
		AcademicUnitID: unit.ID, Name: "programme", DisplayName: "Programme",
	})
	require.NoError(t, err)
	level, err := storetest.SaveProgrammeLevel(ctx, persistence, &model.ProgrammeLevel{
		ProgrammeID: programme.ID, Name: "level", DisplayName: "Level",
	})
	require.NoError(t, err)

	tests := []struct {
		name       string
		query      string
		id         string
		constraint string
	}{
		{name: "programme id", query: "UPDATE programmes SET id = 'bad' WHERE id = ?", id: programme.ID.String(), constraint: "programmes_id_canonical_check"},
		{name: "academic unit reference", query: "UPDATE programmes SET academic_unit_id = 'bad' WHERE id = ?", id: programme.ID.String(), constraint: "programmes_academic_unit_id_canonical_check"},
		{name: "programme level id", query: "UPDATE programme_levels SET id = 'bad' WHERE id = ?", id: level.ID.String(), constraint: "programme_levels_id_canonical_check"},
		{name: "programme reference", query: "UPDATE programme_levels SET programme_id = 'bad' WHERE id = ?", id: level.ID.String(), constraint: "programme_levels_programme_id_canonical_check"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := persistence.GetMaster().Exec(ctx, test.query, test.id)
			var postgresErr *pq.Error
			require.ErrorAs(t, err, &postgresErr, "constraint error")
			require.Equal(t, "23514", string(postgresErr.Code), "constraint error")
			require.Equal(t, test.constraint, postgresErr.Constraint, "constraint error")
		})
	}
}
