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
			if !errors.As(err, &persisted) {
				t.Fatalf("model() error = %v, want persisted-state error", err)
			}
			if persisted.Entity != "institution" || persisted.Field != test.field {
				t.Fatalf("persisted-state context = %s.%s, want institution.%s", persisted.Entity, persisted.Field, test.field)
			}
		})
	}
}

func TestInstitutionRowConversion(t *testing.T) {
	id, err := model.ParseInstitutionID(model.NewId())
	if err != nil {
		t.Fatal(err)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	if *got != *institution {
		t.Fatalf("row.model() = %#v, want %#v", got, institution)
	}
	if !row.CreatedAt.Equal(institution.CreatedAt) ||
		!row.UpdatedAt.Equal(institution.UpdatedAt) ||
		!row.ArchivedAt.Valid || !row.ArchivedAt.Time.Equal(institution.ArchivedAt.Time) {
		t.Fatalf("row times = %#v", row)
	}
}

func replaceInstitutionRow(row institutionRow, replace func(*institutionRow)) institutionRow {
	replace(&row)
	return row
}
