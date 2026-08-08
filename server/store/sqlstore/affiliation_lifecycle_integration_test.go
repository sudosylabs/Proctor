//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"sync"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestStudentAffiliationEndSerializesWithEnrollment(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	ctx := context.Background()
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "affiliation-race", DisplayName: "Affiliation Race"})
	if err != nil {
		t.Fatal(err)
	}
	unit, err := persistence.AcademicUnit().Save(ctx, &model.AcademicUnit{InstitutionID: institution.ID, Name: "unit", DisplayName: "Unit"})
	if err != nil {
		t.Fatal(err)
	}
	programme, err := persistence.Programme().Save(ctx, &model.Programme{AcademicUnitID: unit.ID, Name: "programme", DisplayName: "Programme"})
	if err != nil {
		t.Fatal(err)
	}
	level, err := persistence.ProgrammeLevel().Save(ctx, &model.ProgrammeLevel{ProgrammeID: programme.ID, Name: "level", DisplayName: "Level"})
	if err != nil {
		t.Fatal(err)
	}
	period, err := persistence.AcademicPeriod().Save(ctx, &model.AcademicPeriod{InstitutionID: institution.ID, Name: "period", DisplayName: "Period", StartsAt: model.TimeFromMillis(1), EndsAt: model.TimeFromMillis(model.GetMillis() + 1_000_000)})
	if err != nil {
		t.Fatal(err)
	}
	class := saveLifecycleClass(t, ctx, persistence, level.ID.String(), period.ID.String(), "affiliation-race-class")
	for iteration := 0; iteration < 20; iteration++ {
		userID := model.NewId()
		user, err := persistence.User().Save(ctx, &model.User{Username: "affiliation-" + userID, Email: userID + "@example.edu", DisplayName: "User"})
		if err != nil {
			t.Fatal(err)
		}
		affiliation, err := persistence.Affiliation().Save(ctx, &model.Affiliation{UserId: user.Id, Kind: model.AffiliationStudent, StartAt: 1})
		if err != nil {
			t.Fatal(err)
		}
		attempt, err := persistence.Audit().Save(ctx, &model.AuditEvent{Action: string(model.ActionUserManage), Resource: model.Resource{Type: model.ResourceUser, Id: user.Id}, ScopeType: model.RoleScopeInstitution, ScopeId: institution.ID.String(), Status: model.AuditStatusAttempt, NodeId: "test-node"})
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		var wait sync.WaitGroup
		wait.Add(2)
		var endErr, enrollErr error
		go func() {
			defer wait.Done()
			<-start
			_, endErr = persistence.Affiliation().EndWithAudit(ctx, &store.AffiliationEnd{ID: affiliation.Id, ExpectedRevision: affiliation.Revision, EndAt: model.GetMillis() + 100, AuditEventID: attempt.Id, AuditAt: model.GetMillis()})
		}()
		go func() {
			defer wait.Done()
			<-start
			_, enrollErr = persistence.ClassMember().Enroll(ctx, &model.ClassMember{ClassId: class.ID.String(), UserId: user.Id, StartAt: model.GetMillis()})
		}()
		close(start)
		wait.Wait()
		if (endErr == nil) == (enrollErr == nil) {
			t.Fatalf("iteration %d must have exactly one winner: end=%v enroll=%v", iteration, endErr, enrollErr)
		}
		if endErr != nil && !store.IsConflict(endErr) {
			t.Fatalf("iteration %d end error = %v", iteration, endErr)
		}
		if enrollErr != nil && !store.IsConflict(enrollErr) {
			t.Fatalf("iteration %d enrollment error = %v", iteration, enrollErr)
		}
	}
}
