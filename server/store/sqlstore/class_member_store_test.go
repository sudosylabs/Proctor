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

func TestClassMemberRowRehydrationRejectsInvalidPersistedState(t *testing.T) {
	t.Parallel()

	valid := classMemberRow{
		ID:               model.NewClassMemberID().String(),
		CreatedAt:        time.Unix(10, 0).UTC(),
		UpdatedAt:        time.Unix(11, 0).UTC(),
		Revision:         1,
		ClassID:          model.NewClassID().String(),
		AcademicPeriodID: model.NewAcademicPeriodID().String(),
		UserID:           model.NewUserID().String(),
		StartAt:          time.Unix(10, 0).UTC(),
	}

	tests := []struct {
		name  string
		row   classMemberRow
		field string
	}{
		{name: "member id", row: replaceClassMemberRow(valid, func(row *classMemberRow) { row.ID = "bad" }), field: "id"},
		{name: "class id", row: replaceClassMemberRow(valid, func(row *classMemberRow) { row.ClassID = "bad" }), field: "class_id"},
		{name: "academic period id", row: replaceClassMemberRow(valid, func(row *classMemberRow) { row.AcademicPeriodID = "bad" }), field: "academic_period_id"},
		{name: "user id", row: replaceClassMemberRow(valid, func(row *classMemberRow) { row.UserID = "bad" }), field: "user_id"},
		{name: "domain state", row: replaceClassMemberRow(valid, func(row *classMemberRow) {
			row.EndAt = sql.NullTime{Time: row.StartAt, Valid: true}
		}), field: "end_at"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.row.model()
			var persisted *persistedStateError
			if !errors.As(err, &persisted) {
				t.Fatalf("model() error = %v, want persisted-state error", err)
			}
			if persisted.Entity != "class_member" || persisted.Field != test.field {
				t.Fatalf("persisted-state context = %s.%s, want class_member.%s", persisted.Entity, persisted.Field, test.field)
			}
		})
	}
}

func TestClassMemberRowRehydrationReturnsValidatedModel(t *testing.T) {
	t.Parallel()

	row := classMemberRow{
		ID:               model.NewClassMemberID().String(),
		CreatedAt:        time.Unix(10, 0).UTC(),
		UpdatedAt:        time.Unix(11, 0).UTC(),
		Revision:         2,
		ClassID:          model.NewClassID().String(),
		AcademicPeriodID: model.NewAcademicPeriodID().String(),
		UserID:           model.NewUserID().String(),
		StartAt:          time.Unix(10, 0).UTC(),
		EndAt:            sql.NullTime{Time: time.Unix(20, 0).UTC(), Valid: true},
	}

	got, err := row.model()
	if err != nil {
		t.Fatal(err)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("rehydrated class member is invalid: %v", err)
	}
	if got.ID.String() != row.ID || got.ClassID.String() != row.ClassID || got.AcademicPeriodID.String() != row.AcademicPeriodID || got.UserID.String() != row.UserID || got.Revision != row.Revision {
		t.Fatalf("model() = %#v, want row identity and relationship fields", got)
	}
}

func replaceClassMemberRow(row classMemberRow, replace func(*classMemberRow)) classMemberRow {
	replace(&row)
	return row
}
