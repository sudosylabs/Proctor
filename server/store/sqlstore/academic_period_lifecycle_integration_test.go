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

func TestAcademicPeriodArchiveSerializesWithClassCreation(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	ctx := context.Background()
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "northbridge", DisplayName: "Northbridge"})
	if err != nil {
		t.Fatal(err)
	}
	unit, err := persistence.AcademicUnit().Save(ctx, &model.AcademicUnit{InstitutionID: institution.ID.String(), Name: "computing", DisplayName: "Computing"})
	if err != nil {
		t.Fatal(err)
	}
	programme, err := persistence.Programme().Save(ctx, &model.Programme{AcademicUnitID: unit.ID, Name: "computer-science", DisplayName: "Computer Science"})
	if err != nil {
		t.Fatal(err)
	}
	level, err := persistence.ProgrammeLevel().Save(ctx, &model.ProgrammeLevel{ProgrammeID: programme.ID, Name: "year-1", DisplayName: "Year 1"})
	if err != nil {
		t.Fatal(err)
	}

	for iteration := 0; iteration < 20; iteration++ {
		period, err := persistence.AcademicPeriod().Save(ctx, &model.AcademicPeriod{InstitutionId: institution.ID.String(), Name: fmt.Sprintf("period-%d", iteration), DisplayName: "Period", StartAt: int64(1_800_000_000_000 + iteration*1_000_000), EndAt: int64(1_800_000_500_000 + iteration*1_000_000)})
		if err != nil {
			t.Fatal(err)
		}
		attempt, err := persistence.Audit().Save(ctx, &model.AuditEvent{Action: string(model.ActionInstitutionManage), Resource: model.Resource{Type: model.ResourceInstitution, Id: institution.ID.String()}, ScopeType: model.RoleScopeInstitution, ScopeId: institution.ID.String(), Status: model.AuditStatusAttempt, NodeId: "test-node"})
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
			_, archiveErr = persistence.AcademicPeriod().ArchiveWithAudit(ctx, &store.AcademicPeriodArchive{ID: period.Id, ArchiveAt: model.GetMillis(), AuditEventID: attempt.Id, AuditAt: model.GetMillis()})
		}()
		go func() {
			defer wait.Done()
			<-start
			_, classErr = persistence.Class().Save(ctx, &model.Class{ProgrammeLevelId: level.ID.String(), AcademicPeriodId: period.Id, Name: "class-a", DisplayName: "Class A"})
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
