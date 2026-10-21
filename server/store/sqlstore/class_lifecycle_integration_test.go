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
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/storetest"
)

func TestClassArchiveSerializesWithDependentCreation(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	ctx := context.Background()
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "northbridge", DisplayName: "Northbridge"})
	require.NoError(t, err)
	unit, err := storetest.SaveAcademicUnit(ctx, persistence, &model.AcademicUnit{InstitutionID: institution.ID, Name: "computing", DisplayName: "Computing"})
	require.NoError(t, err)
	programme, err := storetest.SaveProgramme(ctx, persistence, &model.Programme{AcademicUnitID: unit.ID, Name: "computer-science", DisplayName: "Computer Science"})
	require.NoError(t, err)
	level, err := storetest.SaveProgrammeLevel(ctx, persistence, &model.ProgrammeLevel{ProgrammeID: programme.ID, Name: "year-1", DisplayName: "Year 1"})
	require.NoError(t, err)
	period, err := storetest.SaveAcademicPeriod(ctx, persistence, &model.AcademicPeriod{Owner: model.NewInstitutionAcademicPeriodOwner(institution.ID), Name: "2026", DisplayName: "2026", StartsAt: model.TimeFromMillis(100), EndsAt: model.TimeFromMillis(10_000_000)})
	require.NoError(t, err)
	userID := model.NewID()
	user := saveIntegrationUser(t, ctx, persistence, &model.User{Username: "user-" + userID, Email: userID + "@example.edu", DisplayName: "User"})
	_, err = persistence.Affiliation().Save(ctx, &model.Affiliation{UserID: user.ID, Kind: model.AffiliationStudent, StartsAt: model.TimeFromMillis(1)})
	require.NoError(t, err)
	role, err := storetest.SaveRole(ctx, persistence, &model.Role{Name: "class-reader", DisplayName: "Class Reader", Permissions: []string{string(model.ActionClassView)}})
	require.NoError(t, err)

	t.Run("enrollment", func(t *testing.T) {
		baseStart := model.GetMillis() + 1_000
		for iteration := 0; iteration < 20; iteration++ {
			class := saveLifecycleClass(t, ctx, persistence, level.ID.String(), period.ID.String(), fmt.Sprintf("enrollment-%d", iteration))
			attempt := saveLifecycleClassAudit(t, ctx, persistence, unit.ID.String())
			start := make(chan struct{})
			var wait sync.WaitGroup
			wait.Add(2)
			var archiveErr, dependentErr error
			go func() {
				defer wait.Done()
				<-start
				_, archiveErr = persistence.Class().Archive(ctx, &store.ClassArchive{ID: class.ID, ExpectedAcademicUnitID: unit.ID, ExpectedRevision: class.Revision, ArchiveAt: model.GetMillis(), AuditEventID: attempt.ID, AuditAt: model.GetMillis()})
			}()
			go func() {
				defer wait.Done()
				<-start
				_, dependentErr = persistence.ClassMember().SeedEnroll(ctx, &model.ClassMember{ClassID: class.ID, UserID: user.ID, StartsAt: model.TimeFromMillis(baseStart + int64(iteration*10))})
			}()
			close(start)
			wait.Wait()
			assertClassLifecycleWinner(t, iteration, archiveErr, dependentErr)
		}
	})
	t.Run("role_binding", func(t *testing.T) {
		for iteration := 0; iteration < 20; iteration++ {
			class := saveLifecycleClass(t, ctx, persistence, level.ID.String(), period.ID.String(), fmt.Sprintf("binding-%d", iteration))
			attempt := saveLifecycleClassAudit(t, ctx, persistence, unit.ID.String())
			start := make(chan struct{})
			var wait sync.WaitGroup
			wait.Add(2)
			var archiveErr, dependentErr error
			go func() {
				defer wait.Done()
				<-start
				_, archiveErr = persistence.Class().Archive(ctx, &store.ClassArchive{ID: class.ID, ExpectedAcademicUnitID: unit.ID, ExpectedRevision: class.Revision, ArchiveAt: model.GetMillis(), AuditEventID: attempt.ID, AuditAt: model.GetMillis()})
			}()
			go func() {
				defer wait.Done()
				<-start
				_, dependentErr = storetest.SaveRoleBinding(ctx, persistence, &model.RoleBinding{UserID: user.ID, RoleID: role.ID, ScopeType: model.RoleScopeClass, ScopeID: class.ID.String(), StartsAt: model.TimeFromMillis(model.GetMillis())})
			}()
			close(start)
			wait.Wait()
			assertClassLifecycleWinner(t, iteration, archiveErr, dependentErr)
		}
	})
}

func assertClassLifecycleWinner(t *testing.T, iteration int, archiveErr, dependentErr error) {
	t.Helper()
	require.NotEqual(t, dependentErr == nil, archiveErr == nil, "iteration %d must have exactly one winner: archive=%v dependent=%v", iteration, archiveErr, dependentErr)
	if archiveErr == nil {
		var reference *store.ErrReference
		if !store.IsNotFound(dependentErr) {
			require.ErrorAs(t, dependentErr, &reference, "iteration %d archive won but dependent error", iteration)
		}
	}
	if dependentErr == nil {
		require.True(t, store.IsConflict(archiveErr), "iteration %d dependent won but archive error", iteration)
	}
}

func saveLifecycleClass(t *testing.T, ctx context.Context, persistence store.Store, levelID, periodID, name string) *model.Class {
	t.Helper()
	class, err := storetest.SaveClass(ctx, persistence, &model.Class{ProgrammeLevelID: model.ProgrammeLevelID(levelID), AcademicPeriodID: model.AcademicPeriodID(periodID), Name: name, DisplayName: name})
	require.NoError(t, err)
	return class
}
func saveLifecycleClassAudit(t *testing.T, ctx context.Context, persistence store.Store, unitID string) *model.AuditEvent {
	t.Helper()
	attempt, err := persistence.Audit().Save(ctx, &model.AuditEvent{Action: string(model.ActionAcademicUnitManage), Resource: model.Resource{Type: model.ResourceAcademicUnit, ID: unitID}, ScopeType: model.RoleScopeAcademicUnit, ScopeID: unitID, Status: model.AuditStatusAttempt, NodeID: "test-node"})
	require.NoError(t, err)
	return attempt
}
