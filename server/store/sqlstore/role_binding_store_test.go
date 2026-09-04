// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"errors"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

func TestRoleBindingRowRehydrationRejectsInvalidPersistedState(t *testing.T) {
	valid := roleBindingRow{ID: model.NewRoleBindingID().String(), CreatedAt: time.Unix(10, 0).UTC(), UpdatedAt: time.Unix(11, 0).UTC(), UserID: model.NewUserID().String(), RoleID: model.NewRoleID().String(), ScopeType: model.RoleScopeInstitution, ScopeID: model.NewInstitutionID().String(), StartAt: time.Unix(10, 0).UTC()}
	tests := []struct {
		name, field string
		row         roleBindingRow
	}{
		{name: "id", field: "id", row: replaceRoleBindingRow(valid, func(row *roleBindingRow) { row.ID = "bad" })},
		{name: "user", field: "user_id", row: replaceRoleBindingRow(valid, func(row *roleBindingRow) { row.UserID = "bad" })},
		{name: "role", field: "role_id", row: replaceRoleBindingRow(valid, func(row *roleBindingRow) { row.RoleID = "bad" })},
		{name: "scope", field: "scope_id", row: replaceRoleBindingRow(valid, func(row *roleBindingRow) { row.ScopeID = "bad" })},
		{name: "domain", field: "scope_type", row: replaceRoleBindingRow(valid, func(row *roleBindingRow) { row.ScopeType = "unknown" })},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.row.model()
			var persisted *persistedStateError
			if !errors.As(err, &persisted) || persisted.Entity != "role_binding" || persisted.Field != test.field {
				t.Fatalf("model() error = %v, want role_binding.%s persisted-state error", err, test.field)
			}
		})
	}
}

func replaceRoleBindingRow(row roleBindingRow, replace func(*roleBindingRow)) roleBindingRow {
	replace(&row)
	return row
}
