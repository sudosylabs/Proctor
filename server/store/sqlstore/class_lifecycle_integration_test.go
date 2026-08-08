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

func TestClassArchiveSerializesWithDependentCreation(t *testing.T) {
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
	level, err := persistence.ProgrammeLevel().Save(ctx, &model.ProgrammeLevel{ProgrammeID: programme.ID, Name: "year-1", DisplayName: "Year 1"})
	if err != nil {
		t.Fatal(err)
	}
	period, err := persistence.AcademicPeriod().Save(ctx, &model.AcademicPeriod{InstitutionID: institution.ID, Name: "2026", DisplayName: "2026", StartsAt: model.TimeFromMillis(100), EndsAt: model.TimeFromMillis(10_000_000)})
	if err != nil {
		t.Fatal(err)
	}
	userID := model.NewId()
	user, err := persistence.User().Save(ctx, &model.User{Username: "user-" + userID, Email: userID + "@example.edu", DisplayName: "User"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.Affiliation().Save(ctx, &model.Affiliation{UserID: user.ID, Kind: model.AffiliationStudent, StartsAt: model.TimeFromMillis(1)}); err != nil {
		t.Fatal(err)
	}
	role, err := persistence.Role().Save(ctx, &model.Role{Name: "class-reader", DisplayName: "Class Reader", Permissions: []string{string(model.ActionClassView)}})
	if err != nil {
		t.Fatal(err)
	}

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
				_, archiveErr = persistence.Class().ArchiveWithAudit(ctx, &store.ClassArchive{ID: class.ID.String(), ExpectedAcademicUnitID: unit.ID.String(), ExpectedRevision: class.Revision, ArchiveAt: model.GetMillis(), AuditEventID: attempt.Id, AuditAt: model.GetMillis()})
			}()
			go func() {
				defer wait.Done()
				<-start
				_, dependentErr = persistence.ClassMember().Enroll(ctx, &model.ClassMember{ClassID: class.ID, UserID: user.ID, StartsAt: model.TimeFromMillis(baseStart + int64(iteration*10))})
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
				_, archiveErr = persistence.Class().ArchiveWithAudit(ctx, &store.ClassArchive{ID: class.ID.String(), ExpectedAcademicUnitID: unit.ID.String(), ExpectedRevision: class.Revision, ArchiveAt: model.GetMillis(), AuditEventID: attempt.Id, AuditAt: model.GetMillis()})
			}()
			go func() {
				defer wait.Done()
				<-start
				_, dependentErr = persistence.RoleBinding().Save(ctx, &model.RoleBinding{UserID: user.ID, RoleId: role.Id, ScopeType: model.RoleScopeClass, ScopeId: class.ID.String(), StartAt: model.GetMillis()})
			}()
			close(start)
			wait.Wait()
			assertClassLifecycleWinner(t, iteration, archiveErr, dependentErr)
		}
	})
}

func assertClassLifecycleWinner(t *testing.T, iteration int, archiveErr, dependentErr error) {
	t.Helper()
	if (archiveErr == nil) == (dependentErr == nil) {
		t.Fatalf("iteration %d must have exactly one winner: archive=%v dependent=%v", iteration, archiveErr, dependentErr)
	}
	if archiveErr == nil {
		var reference *store.ErrReference
		if !store.IsNotFound(dependentErr) && !errors.As(dependentErr, &reference) {
			t.Fatalf("iteration %d archive won but dependent error = %v", iteration, dependentErr)
		}
	}
	if dependentErr == nil && !store.IsConflict(archiveErr) {
		t.Fatalf("iteration %d dependent won but archive error = %v", iteration, archiveErr)
	}
}

func saveLifecycleClass(t *testing.T, ctx context.Context, persistence store.Store, levelID, periodID, name string) *model.Class {
	t.Helper()
	class, err := persistence.Class().Save(ctx, &model.Class{ProgrammeLevelID: model.ProgrammeLevelID(levelID), AcademicPeriodID: model.AcademicPeriodID(periodID), Name: name, DisplayName: name})
	if err != nil {
		t.Fatal(err)
	}
	return class
}
func saveLifecycleClassAudit(t *testing.T, ctx context.Context, persistence store.Store, unitID string) *model.AuditEvent {
	t.Helper()
	attempt, err := persistence.Audit().Save(ctx, &model.AuditEvent{Action: string(model.ActionAcademicUnitManage), Resource: model.Resource{Type: model.ResourceAcademicUnit, Id: unitID}, ScopeType: model.RoleScopeAcademicUnit, ScopeId: unitID, Status: model.AuditStatusAttempt, NodeId: "test-node"})
	if err != nil {
		t.Fatal(err)
	}
	return attempt
}
