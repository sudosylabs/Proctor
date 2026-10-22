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

func TestProgrammeArchiveSerializesWithLevelCreation(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	ctx := context.Background()
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "northbridge", DisplayName: "Northbridge"})
	require.NoError(t, err)
	unit, err := storetest.SaveAcademicUnit(ctx, persistence, &model.AcademicUnit{InstitutionID: institution.ID, Name: "computing", DisplayName: "Computing"})
	require.NoError(t, err)

	for iteration := 0; iteration < 20; iteration++ {
		programme, err := storetest.SaveProgramme(ctx, persistence, &model.Programme{AcademicUnitID: unit.ID, Name: fmt.Sprintf("programme-%d", iteration), DisplayName: "Programme"})
		require.NoError(t, err)
		attempt, err := persistence.Audit().Save(ctx, &model.AuditEvent{
			Action: string(model.ActionAcademicUnitManage), Resource: model.Resource{Type: model.ResourceAcademicUnit, ID: unit.ID.String()},
			ScopeType: model.RoleScopeAcademicUnit, ScopeID: unit.ID.String(),
			Status: model.AuditStatusAttempt, NodeID: "test-node",
		})
		require.NoError(t, err)

		start := make(chan struct{})
		var wait sync.WaitGroup
		wait.Add(2)
		var archiveErr, levelErr error
		go func() {
			defer wait.Done()
			<-start
			_, archiveErr = persistence.Programme().Archive(ctx, &store.ProgrammeArchive{ID: programme.ID, ArchiveAt: model.NowUTC(), AuditEventID: attempt.ID, AuditAt: model.NowUTC()})
		}()
		go func() {
			defer wait.Done()
			<-start
			_, levelErr = storetest.SaveProgrammeLevel(ctx, persistence, &model.ProgrammeLevel{ProgrammeID: programme.ID, Name: "year-1", DisplayName: "Year 1"})
		}()
		close(start)
		wait.Wait()

		require.NotEqual(t, levelErr == nil, archiveErr == nil, "iteration %d must have exactly one winner: archive=%v level=%v", iteration, archiveErr, levelErr)
		if archiveErr == nil && levelErr != nil {
			var reference *store.ErrReference
			require.ErrorAs(t, levelErr, &reference, "iteration %d archive won but level error", iteration)
		}
		if levelErr == nil {
			require.True(t, store.IsConflict(archiveErr), "iteration %d level creation won but archive error", iteration)
		}
	}
}
