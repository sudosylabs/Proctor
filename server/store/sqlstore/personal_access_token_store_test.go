// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

func TestPersonalAccessTokenRowRehydrationRejectsInvalidPersistedState(t *testing.T) {
	t.Parallel()

	createdAt := model.TimeFromMillis(1)
	valid := personalAccessTokenRow{
		ID:          model.NewPersonalAccessTokenID().String(),
		CreatedAt:   createdAt,
		UpdatedAt:   createdAt,
		UserID:      model.NewUserID().String(),
		Description: "token",
		TokenHash:   model.HashToken(model.NewCredentialToken()),
		Scopes:      []string{string(model.ActionClassView)},
		ExpiresAt:   createdAt.Add(time.Hour),
	}
	tests := []struct {
		name  string
		row   personalAccessTokenRow
		field string
	}{
		{name: "token id", row: replacePersonalAccessTokenRow(valid, func(row *personalAccessTokenRow) { row.ID = "bad" }), field: "id"},
		{name: "user id", row: replacePersonalAccessTokenRow(valid, func(row *personalAccessTokenRow) { row.UserID = "bad" }), field: "user_id"},
		{name: "optional academic unit id", row: replacePersonalAccessTokenRow(valid, func(row *personalAccessTokenRow) { row.AcademicUnitID = sql.NullString{String: "bad", Valid: true} }), field: "academic_unit_id"},
		{name: "present empty academic unit id", row: replacePersonalAccessTokenRow(valid, func(row *personalAccessTokenRow) { row.AcademicUnitID = sql.NullString{String: "", Valid: true} }), field: "academic_unit_id"},
		{name: "domain state", row: replacePersonalAccessTokenRow(valid, func(row *personalAccessTokenRow) { row.Description = "" }), field: "description"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.row.model()
			var persisted *persistedStateError
			if !errors.As(err, &persisted) {
				t.Fatalf("model() error = %v, want persisted-state error", err)
			}
			if persisted.Entity != "personal_access_token" || persisted.Field != test.field {
				t.Fatalf("persisted-state context = %s.%s, want personal_access_token.%s", persisted.Entity, persisted.Field, test.field)
			}
			if strings.Contains(err.Error(), valid.TokenHash) {
				t.Fatalf("model() error exposed token hash: %v", err)
			}
		})
	}
}

func TestPersonalAccessTokenRowConversion(t *testing.T) {
	unitID := model.NewAcademicUnitID()
	createdAt := model.TimeFromMillis(1)
	row := personalAccessTokenRow{
		ID: model.NewId(), CreatedAt: createdAt, UpdatedAt: createdAt.Add(time.Millisecond), UserID: model.NewId(),
		Description: "token", TokenHash: model.HashToken(model.NewCredentialToken()),
		Scopes: []string{string(model.ActionClassView)}, AcademicUnitID: sql.NullString{String: unitID.String(), Valid: true},
		ExpiresAt: createdAt.Add(2 * time.Millisecond),
	}
	token, err := row.model()
	if err != nil {
		t.Fatal(err)
	}
	if token.AcademicUnitID != unitID || len(token.Scopes) != 1 {
		t.Fatalf("row.model() = %#v", token)
	}
	token.Scopes[0] = "mutated"
	if row.Scopes[0] != string(model.ActionClassView) {
		t.Fatal("row.model() exposed mutable scopes")
	}
}

func replacePersonalAccessTokenRow(row personalAccessTokenRow, replace func(*personalAccessTokenRow)) personalAccessTokenRow {
	replace(&row)
	return row
}
