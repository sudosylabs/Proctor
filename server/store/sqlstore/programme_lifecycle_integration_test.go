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

func TestProgrammeArchiveSerializesWithLevelCreation(t *testing.T) {
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

	for iteration := 0; iteration < 20; iteration++ {
		programme, err := persistence.Programme().Save(ctx, &model.Programme{AcademicUnitID: unit.ID, Name: fmt.Sprintf("programme-%d", iteration), DisplayName: "Programme"})
		if err != nil {
			t.Fatal(err)
		}
		attempt, err := persistence.Audit().Save(ctx, &model.AuditEvent{
			Action: string(model.ActionAcademicUnitManage), Resource: model.Resource{Type: model.ResourceAcademicUnit, Id: unit.ID.String()},
			ScopeType: model.RoleScopeAcademicUnit, ScopeId: unit.ID.String(),
			Status: model.AuditStatusAttempt, NodeId: "test-node",
		})
		if err != nil {
			t.Fatal(err)
		}

		start := make(chan struct{})
		var wait sync.WaitGroup
		wait.Add(2)
		var archiveErr, levelErr error
		go func() {
			defer wait.Done()
			<-start
			_, archiveErr = persistence.Programme().ArchiveWithAudit(ctx, &store.ProgrammeArchive{ID: programme.ID.String(), ArchiveAt: model.GetMillis(), AuditEventID: attempt.Id, AuditAt: model.GetMillis()})
		}()
		go func() {
			defer wait.Done()
			<-start
			_, levelErr = persistence.ProgrammeLevel().Save(ctx, &model.ProgrammeLevel{ProgrammeID: programme.ID, Name: "year-1", DisplayName: "Year 1"})
		}()
		close(start)
		wait.Wait()

		if (archiveErr == nil) == (levelErr == nil) {
			t.Fatalf("iteration %d must have exactly one winner: archive=%v level=%v", iteration, archiveErr, levelErr)
		}
		if archiveErr == nil && levelErr != nil {
			var reference *store.ErrReference
			if !errors.As(levelErr, &reference) {
				t.Fatalf("iteration %d archive won but level error = %v", iteration, levelErr)
			}
		}
		if levelErr == nil && !store.IsConflict(archiveErr) {
			t.Fatalf("iteration %d level creation won but archive error = %v", iteration, archiveErr)
		}
	}
}
