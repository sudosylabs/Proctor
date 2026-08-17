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

func TestMembershipCanonicalIDConstraintsRejectNoncanonicalValues(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	ctx := context.Background()

	institution, err := persistence.Institution().Save(ctx, &model.Institution{
		Name: "membership-constraint", DisplayName: "Membership Constraint",
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
	class := saveLifecycleClass(t, ctx, persistence, level.ID.String(), period.ID.String(), "constraint-class")
	user := saveIntegrationUser(t, ctx, persistence, &model.User{
		Username: "membership-constraint-user", Email: "membership-constraint@example.edu", DisplayName: "User",
	})
	if _, err := persistence.Affiliation().Save(ctx, &model.Affiliation{
		UserID: user.ID, Kind: model.AffiliationStudent, StartsAt: model.TimeFromMillis(1),
	}); err != nil {
		t.Fatal(err)
	}
	unitMember, err := persistence.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{
		AcademicUnitID: unit.ID, UserID: user.ID, StartsAt: model.TimeFromMillis(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	enrollment, err := persistence.ClassMember().Enroll(ctx, &model.ClassMember{
		ClassID: class.ID, UserID: user.ID, StartsAt: model.TimeFromMillis(model.GetMillis()),
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
		{name: "academic unit member id", query: "UPDATE academic_unit_members SET id = 'bad' WHERE id = ?", id: unitMember.ID.String(), constraint: "academic_unit_members_id_canonical_check"},
		{name: "academic unit id", query: "UPDATE academic_unit_members SET academic_unit_id = 'bad' WHERE id = ?", id: unitMember.ID.String(), constraint: "academic_unit_members_academic_unit_id_canonical_check"},
		{name: "academic unit member user id", query: "UPDATE academic_unit_members SET user_id = 'bad' WHERE id = ?", id: unitMember.ID.String(), constraint: "academic_unit_members_user_id_canonical_check"},
		{name: "class member id", query: "UPDATE class_members SET id = 'bad' WHERE id = ?", id: enrollment.Membership.ID.String(), constraint: "class_members_id_canonical_check"},
		{name: "class id", query: "UPDATE class_members SET class_id = 'bad' WHERE id = ?", id: enrollment.Membership.ID.String(), constraint: "class_members_class_id_canonical_check"},
		{name: "academic period id", query: "UPDATE class_members SET academic_period_id = 'bad' WHERE id = ?", id: enrollment.Membership.ID.String(), constraint: "class_members_academic_period_id_canonical_check"},
		{name: "class member user id", query: "UPDATE class_members SET user_id = 'bad' WHERE id = ?", id: enrollment.Membership.ID.String(), constraint: "class_members_user_id_canonical_check"},
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
