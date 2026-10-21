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
	"fmt"
	"sync"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/storetest"
)

func TestAcademicPeriodArchiveSerializesWithClassCreation(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	ctx := context.Background()
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "northbridge", DisplayName: "Northbridge"})
	if err != nil {
		t.Fatal(err)
	}
	unit, err := storetest.SaveAcademicUnit(ctx, persistence, &model.AcademicUnit{InstitutionID: institution.ID, Name: "computing", DisplayName: "Computing"})
	if err != nil {
		t.Fatal(err)
	}
	programme, err := storetest.SaveProgramme(ctx, persistence, &model.Programme{AcademicUnitID: unit.ID, Name: "computer-science", DisplayName: "Computer Science"})
	if err != nil {
		t.Fatal(err)
	}
	level, err := storetest.SaveProgrammeLevel(ctx, persistence, &model.ProgrammeLevel{ProgrammeID: programme.ID, Name: "year-1", DisplayName: "Year 1"})
	if err != nil {
		t.Fatal(err)
	}

	for iteration := 0; iteration < 20; iteration++ {
		period, err := storetest.SaveAcademicPeriod(ctx, persistence, &model.AcademicPeriod{Owner: model.NewInstitutionAcademicPeriodOwner(institution.ID), Name: fmt.Sprintf("period-%d", iteration), DisplayName: "Period", StartsAt: model.TimeFromMillis(int64(1_800_000_000_000 + iteration*1_000_000)), EndsAt: model.TimeFromMillis(int64(1_800_000_500_000 + iteration*1_000_000))})
		if err != nil {
			t.Fatal(err)
		}
		attempt, err := persistence.Audit().Save(ctx, &model.AuditEvent{Action: string(model.ActionInstitutionManage), Resource: model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}, ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(), Status: model.AuditStatusAttempt, NodeID: "test-node"})
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		var wait sync.WaitGroup
		wait.Add(2)
		var archiveErr, classErr error
		go func() {
			defer wait.Done()
			<-start
			_, archiveErr = persistence.AcademicPeriod().Archive(ctx, &store.AcademicPeriodArchive{ID: period.ID, ArchiveAt: model.GetMillis(), AuditEventID: attempt.ID, AuditAt: model.GetMillis()})
		}()
		go func() {
			defer wait.Done()
			<-start
			_, classErr = storetest.SaveClass(ctx, persistence, &model.Class{ProgrammeLevelID: level.ID, AcademicPeriodID: period.ID, Name: "class-a", DisplayName: "Class A"})
		}()
		close(start)
		wait.Wait()
		if (archiveErr == nil) == (classErr == nil) {
			t.Fatalf("iteration %d must have exactly one winner: archive=%v class=%v", iteration, archiveErr, classErr)
		}
		if archiveErr == nil {
			var reference *store.ErrReference
			if !errors.As(classErr, &reference) {
				t.Fatalf("iteration %d archive won but class error = %v", iteration, classErr)
			}
		}
		if classErr == nil && !store.IsConflict(archiveErr) {
			t.Fatalf("iteration %d class creation won but archive error = %v", iteration, archiveErr)
		}
	}
}
