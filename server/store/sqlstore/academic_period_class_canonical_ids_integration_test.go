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
	"errors"
	"testing"

	"github.com/lib/pq"

	"github.com/sudosylabs/proctor/server/model"
)

func TestAcademicPeriodClassCanonicalIDConstraintsRejectNoncanonicalValues(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	ctx := context.Background()

	institution, err := persistence.Institution().Save(ctx, &model.Institution{
		Name: "period-class-constraint", DisplayName: "Period Class Constraint",
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
	period, err := persistence.AcademicPeriod().Save(ctx, &model.AcademicPeriod{
		Owner:       model.NewInstitutionAcademicPeriodOwner(institution.ID),
		Name:        "period",
		DisplayName: "Period",
		StartsAt:    model.TimeFromMillis(1),
		EndsAt:      model.TimeFromMillis(model.GetMillis() + 1_000_000),
	})
	if err != nil {
		t.Fatal(err)
	}
	class, err := persistence.Class().Save(ctx, &model.Class{
		ProgrammeLevelID: level.ID, AcademicPeriodID: period.ID, Name: "class-a", DisplayName: "Class A",
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
		{name: "academic period id", query: "UPDATE academic_periods SET id = 'bad' WHERE id = ?", id: period.ID.String(), constraint: "academic_periods_id_canonical_check"},
		{name: "academic period institution id", query: "UPDATE academic_periods SET institution_id = 'bad' WHERE id = ?", id: period.ID.String(), constraint: "academic_periods_institution_id_canonical_check"},
		{name: "academic period academic unit id", query: "UPDATE academic_periods SET owner_type = 'academic_unit', institution_id = NULL, academic_unit_id = 'bad' WHERE id = ?", id: period.ID.String(), constraint: "academic_periods_academic_unit_id_canonical_check"},
		{name: "class id", query: "UPDATE classes SET id = 'bad' WHERE id = ?", id: class.ID.String(), constraint: "classes_id_canonical_check"},
		{name: "class programme level id", query: "UPDATE classes SET programme_level_id = 'bad' WHERE id = ?", id: class.ID.String(), constraint: "classes_programme_level_id_canonical_check"},
		{name: "class academic period id", query: "UPDATE classes SET academic_period_id = 'bad' WHERE id = ?", id: class.ID.String(), constraint: "classes_academic_period_id_canonical_check"},
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
