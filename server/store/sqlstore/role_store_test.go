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

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestRoleRowRehydrationRejectsInvalidPersistedState(t *testing.T) {
	valid := roleRow{ID: model.NewRoleID().String(), CreatedAt: time.Unix(10, 0).UTC(), UpdatedAt: time.Unix(11, 0).UTC(), Name: "reviewer", DisplayName: "Reviewer", Permissions: pq.StringArray{"audit.view"}}
	tests := []struct {
		name, field string
		row         roleRow
	}{
		{name: "id", field: "id", row: replaceRoleRow(valid, func(row *roleRow) { row.ID = "bad" })},
		{name: "domain", field: "permissions", row: replaceRoleRow(valid, func(row *roleRow) { row.Permissions = pq.StringArray{"invalid"} })},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.row.model()
			var persisted *persistedStateError
			require.ErrorAs(t, err, &persisted, "model() error")
			require.Equal(t, "role", persisted.Entity, "model() error")
			require.Equal(t, test.field, persisted.Field, "model() error")
		})
	}
}

func replaceRoleRow(row roleRow, replace func(*roleRow)) roleRow { replace(&row); return row }
