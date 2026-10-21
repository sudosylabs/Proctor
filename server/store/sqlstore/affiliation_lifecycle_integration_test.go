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
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/storetest"
)

func TestStudentAffiliationEndSerializesWithEnrollment(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	ctx := context.Background()
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "affiliation-race", DisplayName: "Affiliation Race"})
	require.NoError(t, err)
	unit, err := storetest.SaveAcademicUnit(ctx, persistence, &model.AcademicUnit{InstitutionID: institution.ID, Name: "unit", DisplayName: "Unit"})
	require.NoError(t, err)
	programme, err := storetest.SaveProgramme(ctx, persistence, &model.Programme{AcademicUnitID: unit.ID, Name: "programme", DisplayName: "Programme"})
	require.NoError(t, err)
	level, err := storetest.SaveProgrammeLevel(ctx, persistence, &model.ProgrammeLevel{ProgrammeID: programme.ID, Name: "level", DisplayName: "Level"})
	require.NoError(t, err)
	period, err := storetest.SaveAcademicPeriod(ctx, persistence, &model.AcademicPeriod{Owner: model.NewInstitutionAcademicPeriodOwner(institution.ID), Name: "period", DisplayName: "Period", StartsAt: model.TimeFromMillis(1), EndsAt: model.TimeFromMillis(model.GetMillis() + 1_000_000)})
	require.NoError(t, err)
	class := saveLifecycleClass(t, ctx, persistence, level.ID.String(), period.ID.String(), "affiliation-race-class")
	for iteration := 0; iteration < 20; iteration++ {
		userID := model.NewID()
		user := saveIntegrationUser(t, ctx, persistence, &model.User{Username: "affiliation-" + userID, Email: userID + "@example.edu", DisplayName: "User"})
		affiliation, err := persistence.Affiliation().Save(ctx, &model.Affiliation{UserID: user.ID, Kind: model.AffiliationStudent, StartsAt: model.TimeFromMillis(1)})
		require.NoError(t, err)
		attempt, err := persistence.Audit().Save(ctx, &model.AuditEvent{Action: string(model.ActionUserManage), Resource: model.Resource{Type: model.ResourceUser, ID: user.ID.String()}, ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(), Status: model.AuditStatusAttempt, NodeID: "test-node"})
		require.NoError(t, err)
		start := make(chan struct{})
		var wait sync.WaitGroup
		wait.Add(2)
		var endErr, enrollErr error
		go func() {
			defer wait.Done()
			<-start
			_, endErr = persistence.Affiliation().End(ctx, &store.AffiliationEnd{ID: affiliation.ID, ExpectedRevision: affiliation.Revision, EndAt: model.GetMillis() + 100, AuditEventID: attempt.ID, AuditAt: model.GetMillis()})
		}()
		go func() {
			defer wait.Done()
			<-start
			_, enrollErr = persistence.ClassMember().SeedEnroll(ctx, &model.ClassMember{ClassID: class.ID, UserID: user.ID, StartsAt: model.TimeFromMillis(model.GetMillis())})
		}()
		close(start)
		wait.Wait()
		require.NotEqual(t, enrollErr == nil, endErr == nil, "iteration %d must have exactly one winner: end=%v enroll=%v", iteration, endErr, enrollErr)
		if endErr != nil {
			require.True(t, store.IsConflict(endErr), "iteration %d end error", iteration)
		}
		if enrollErr != nil {
			require.True(t, store.IsConflict(enrollErr), "iteration %d enrollment error", iteration)
		}
	}
}
