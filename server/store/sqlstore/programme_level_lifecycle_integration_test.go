//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestProgrammeLevelArchiveSerializesWithClassCreation(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	ctx := context.Background()
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "northbridge", DisplayName: "Northbridge"})
	if err != nil {
		t.Fatal(err)
	}
	unit, err := persistence.AcademicUnit().Save(ctx, &model.AcademicUnit{InstitutionID: institution.ID, Name: "computing", DisplayName: "Computing"})
	if err != nil {
		t.Fatal(err)
	}
	programme, err := persistence.Programme().Save(ctx, &model.Programme{AcademicUnitID: unit.ID, Name: "computer-science", DisplayName: "Computer Science"})
	if err != nil {
		t.Fatal(err)
	}
	period, err := persistence.AcademicPeriod().Save(ctx, &model.AcademicPeriod{InstitutionID: institution.ID, Name: "2026-2027", DisplayName: "2026-2027", StartsAt: model.TimeFromMillis(1_800_000_000_000), EndsAt: model.TimeFromMillis(1_830_000_000_000)})
	if err != nil {
		t.Fatal(err)
	}

	for iteration := 0; iteration < 20; iteration++ {
		level, err := persistence.ProgrammeLevel().Save(ctx, &model.ProgrammeLevel{ProgrammeID: programme.ID, Name: fmt.Sprintf("level-%d", iteration), DisplayName: "Level"})
		if err != nil {
			t.Fatal(err)
		}
		attempt, err := persistence.Audit().Save(ctx, &model.AuditEvent{Action: string(model.ActionAcademicUnitManage), Resource: model.Resource{Type: model.ResourceAcademicUnit, Id: unit.ID.String()}, ScopeType: model.RoleScopeAcademicUnit, ScopeId: unit.ID.String(), Status: model.AuditStatusAttempt, NodeId: "test-node"})
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
			_, archiveErr = persistence.ProgrammeLevel().ArchiveWithAudit(ctx, &store.ProgrammeLevelArchive{ID: level.ID.String(), ArchiveAt: model.GetMillis(), AuditEventID: attempt.Id, AuditAt: model.GetMillis()})
		}()
		go func() {
			defer wait.Done()
			<-start
			_, classErr = persistence.Class().Save(ctx, &model.Class{ProgrammeLevelID: level.ID, AcademicPeriodID: period.ID, Name: "class-a", DisplayName: "Class A"})
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
