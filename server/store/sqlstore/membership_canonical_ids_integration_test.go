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

func TestMembershipCanonicalIDConstraintsRejectNoncanonicalValues(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	ctx := context.Background()

	institution, err := persistence.Institution().Save(ctx, &model.Institution{
		Name: "membership-constraint", DisplayName: "Membership Constraint",
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
	period, err := storetest.SaveAcademicPeriod(ctx, persistence, &model.AcademicPeriod{
		Owner:       model.NewInstitutionAcademicPeriodOwner(institution.ID),
		Name:        "period",
		DisplayName: "Period",
		StartsAt:    model.TimeFromMillis(1),
		EndsAt:      model.TimeFromMillis(model.GetMillis() + 1_000_000),
	})
	require.NoError(t, err)
	class := saveLifecycleClass(t, ctx, persistence, level.ID.String(), period.ID.String(), "constraint-class")
	user := saveIntegrationUser(t, ctx, persistence, &model.User{
		Username: "membership-constraint-user", Email: "membership-constraint@example.edu", DisplayName: "User",
	})
	_, err = persistence.Affiliation().Save(ctx, &model.Affiliation{
		UserID: user.ID, Kind: model.AffiliationStudent, StartsAt: model.TimeFromMillis(1),
	})
	require.NoError(t, err)
	unitMember, err := persistence.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{
		AcademicUnitID: unit.ID, UserID: user.ID, StartsAt: model.TimeFromMillis(1),
	})
	require.NoError(t, err)
	enrollment, err := persistence.ClassMember().SeedEnroll(ctx, &model.ClassMember{
		ClassID: class.ID, UserID: user.ID, StartsAt: model.TimeFromMillis(model.GetMillis()),
	})
	require.NoError(t, err)

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
			require.ErrorAs(t, err, &postgresErr, "constraint error")
			require.Equal(t, "23514", string(postgresErr.Code), "constraint error")
			require.Equal(t, test.constraint, postgresErr.Constraint, "constraint error")
		})
	}
}
