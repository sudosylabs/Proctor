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

func TestAcademicPeriodArchiveSerializesWithClassCreation(t *testing.T) {
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

	for iteration := 0; iteration < 20; iteration++ {
		period, err := storetest.SaveAcademicPeriod(ctx, persistence, &model.AcademicPeriod{Owner: model.NewInstitutionAcademicPeriodOwner(institution.ID), Name: fmt.Sprintf("period-%d", iteration), DisplayName: "Period", StartsAt: model.TimeFromMillis(int64(1_800_000_000_000 + iteration*1_000_000)), EndsAt: model.TimeFromMillis(int64(1_800_000_500_000 + iteration*1_000_000))})
		require.NoError(t, err)
		attempt, err := persistence.Audit().Save(ctx, &model.AuditEvent{Action: string(model.ActionInstitutionManage), Resource: model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}, ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(), Status: model.AuditStatusAttempt, NodeID: "test-node"})
		require.NoError(t, err)
		start := make(chan struct{})
		var wait sync.WaitGroup
		wait.Add(2)
		var archiveErr, classErr error
		go func() {
			defer wait.Done()
			<-start
			_, archiveErr = persistence.AcademicPeriod().Archive(ctx, &store.AcademicPeriodArchive{ID: period.ID, ArchiveAt: model.NowUTC(), AuditEventID: attempt.ID, AuditAt: model.NowUTC()})
		}()
		go func() {
			defer wait.Done()
			<-start
			_, classErr = storetest.SaveClass(ctx, persistence, &model.Class{ProgrammeLevelID: level.ID, AcademicPeriodID: period.ID, Name: "class-a", DisplayName: "Class A"})
		}()
		close(start)
		wait.Wait()
		require.NotEqual(t, classErr == nil, archiveErr == nil, "iteration %d must have exactly one winner: archive=%v class=%v", iteration, archiveErr, classErr)
		if archiveErr == nil {
			var reference *store.ErrReference
			require.ErrorAs(t, classErr, &reference, "iteration %d archive won but class error", iteration)
		}
		if classErr == nil {
			require.True(t, store.IsConflict(archiveErr), "iteration %d class creation won but archive error", iteration)
		}
	}
}
