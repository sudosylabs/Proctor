//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"errors"
	"testing"

	"github.com/lib/pq"

	"github.com/sudosylabs/proctor/server/model"
)

func TestProgrammeCanonicalIDConstraintsRejectNoncanonicalValues(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	ctx := context.Background()

	institution, err := persistence.Institution().Save(ctx, &model.Institution{
		Name: "programme-constraint", DisplayName: "Programme Constraint",
	})
	if err != nil {
		t.Fatal(err)
	}
	unit, err := persistence.AcademicUnit().Save(ctx, &model.AcademicUnit{
		InstitutionID: institution.ID, Name: "unit", DisplayName: "Unit",
	})
	if err != nil {
		t.Fatal(err)
	}
	programme, err := persistence.Programme().Save(ctx, &model.Programme{
		AcademicUnitID: unit.ID, Name: "programme", DisplayName: "Programme",
	})
	if err != nil {
		t.Fatal(err)
	}
	level, err := persistence.ProgrammeLevel().Save(ctx, &model.ProgrammeLevel{
		ProgrammeID: programme.ID, Name: "level", DisplayName: "Level",
	})
	if err != nil {
		t.Fatal(err)
	}

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
			if !errors.As(err, &postgresErr) || string(postgresErr.Code) != "23514" || postgresErr.Constraint != test.constraint {
				t.Fatalf("constraint error = %#v, want check violation %s", err, test.constraint)
			}
		})
	}
}
