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
	"testing"
	"time"

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
			if !errors.As(err, &persisted) {
				t.Fatalf("model() error = %v, want persisted-state error", err)
			}
			if persisted.Entity != "affiliation" || persisted.Field != test.field {
				t.Fatalf("persisted-state context = %s.%s, want affiliation.%s", persisted.Entity, persisted.Field, test.field)
			}
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
	if err != nil {
		t.Fatal(err)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("rehydrated affiliation is invalid: %v", err)
	}
	if got.ID.String() != row.ID || got.UserID.String() != row.UserID || got.Kind != row.Kind || got.Revision != row.Revision {
		t.Fatalf("model() = %#v, want row identity and relationship fields", got)
	}
}

func replaceAffiliationRow(row affiliationRow, replace func(*affiliationRow)) affiliationRow {
	replace(&row)
	return row
}
