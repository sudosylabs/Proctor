// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"context"
	"errors"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestAffiliationStore(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	user := saveUser(t, ctx, ss)
	start := model.GetMillis() + 1000
	saved, err := ss.Affiliation().Save(ctx, &model.Affiliation{
		UserId: user.Id, Kind: model.AffiliationStudent, StartAt: start,
	})
	requireNoError(t, err)
	if !model.IsValidId(saved.Id) {
		t.Fatalf("Save() = %#v", saved)
	}
	got, err := ss.Affiliation().Get(ctx, saved.Id)
	requireNoError(t, err)
	if *got != *saved {
		t.Fatalf("Get() = %#v, want %#v", got, saved)
	}
	active, err := ss.Affiliation().ListActiveByUser(ctx, user.Id, start+1)
	requireNoError(t, err)
	if len(active) != 1 || active[0].Id != saved.Id {
		t.Fatalf("ListActiveByUser() = %#v", active)
	}
	teacher, err := ss.Affiliation().Save(ctx, &model.Affiliation{UserId: user.Id, Kind: model.AffiliationTeacher, StartAt: start})
	requireNoError(t, err)
	active, err = ss.Affiliation().ListActiveByUser(ctx, user.Id, start+1)
	requireNoError(t, err)
	if len(active) != 2 {
		t.Fatalf("concurrent non-exclusive affiliations = %#v", active)
	}
	_, err = ss.Affiliation().Save(ctx, &model.Affiliation{
		UserId: user.Id, Kind: model.AffiliationStudent, StartAt: start + 2,
	})
	var conflict *store.ErrConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("duplicate active affiliation error = %v", err)
	}
	ended, err := ss.Affiliation().End(ctx, saved.Id, saved.Revision, start+10)
	requireNoError(t, err)
	if ended.EndAt != start+10 {
		t.Fatalf("End() = %#v", ended)
	}
	if _, err := ss.Affiliation().End(ctx, saved.Id, saved.Revision, start+11); !store.IsConflict(err) {
		t.Fatalf("stale End() error = %v", err)
	}
	active, err = ss.Affiliation().ListActiveByUser(ctx, user.Id, start+11)
	requireNoError(t, err)
	if len(active) != 1 || active[0].Id != teacher.Id {
		t.Fatalf("active after end = %#v", active)
	}
	history, err := ss.Affiliation().ListByUser(ctx, user.Id)
	requireNoError(t, err)
	if len(history) != 2 {
		t.Fatalf("ListByUser() = %#v", history)
	}
	next, err := ss.Affiliation().Save(ctx, &model.Affiliation{
		UserId: user.Id, Kind: model.AffiliationStudent, StartAt: start + 10,
	})
	requireNoError(t, err)
	if next.Id == saved.Id {
		t.Fatalf("new effective range reused the old row: %#v", next)
	}
	createAttempt := saveAffiliationAuditAttempt(t, ctx, ss, institution.ID.String(), user.Id)
	candidate := &model.Affiliation{UserId: user.Id, Kind: model.AffiliationStaff, StartAt: start}
	candidate.PrepareCreate(model.NewId(), model.GetMillis())
	created, err := ss.Affiliation().Create(ctx, &store.AffiliationCreation{Affiliation: candidate, AuditEventID: createAttempt.Id, AuditAt: model.GetMillis()})
	requireNoError(t, err)
	completedCreate, err := ss.Audit().Get(ctx, createAttempt.Id)
	requireNoError(t, err)
	if completedCreate.Status != model.AuditStatusSuccess {
		t.Fatalf("create audit = %#v", completedCreate)
	}
	rolledBack := &model.Affiliation{UserId: user.Id, Kind: model.AffiliationExternal, StartAt: start}
	rolledBack.PrepareCreate(model.NewId(), model.GetMillis())
	if _, err := ss.Affiliation().Create(ctx, &store.AffiliationCreation{Affiliation: rolledBack, AuditEventID: model.NewId(), AuditAt: model.GetMillis()}); err == nil {
		t.Fatal("Create() succeeded without an audit attempt")
	}
	if _, err := ss.Affiliation().Get(ctx, rolledBack.Id); !store.IsNotFound(err) {
		t.Fatalf("create survived audit rollback: %v", err)
	}
	endAttempt := saveAffiliationAuditAttempt(t, ctx, ss, institution.ID.String(), user.Id)
	endedWithAudit, err := ss.Affiliation().EndWithAudit(ctx, &store.AffiliationEnd{ID: created.Id, ExpectedRevision: created.Revision, EndAt: start + 20, AuditEventID: endAttempt.Id, AuditAt: model.GetMillis()})
	requireNoError(t, err)
	if endedWithAudit.Revision != created.Revision+1 || endedWithAudit.EndAt != start+20 {
		t.Fatalf("EndWithAudit() = %#v", endedWithAudit)
	}
	completedEnd, err := ss.Audit().Get(ctx, endAttempt.Id)
	requireNoError(t, err)
	if completedEnd.Status != model.AuditStatusSuccess {
		t.Fatalf("end audit = %#v", completedEnd)
	}
	staleAttempt := saveAffiliationAuditAttempt(t, ctx, ss, institution.ID.String(), user.Id)
	if _, err := ss.Affiliation().EndWithAudit(ctx, &store.AffiliationEnd{ID: created.Id, ExpectedRevision: created.Revision, EndAt: start + 21, AuditEventID: staleAttempt.Id, AuditAt: model.GetMillis()}); !store.IsConflict(err) {
		t.Fatalf("stale EndWithAudit() error = %v", err)
	}
	unit, err := ss.AcademicUnit().Save(ctx, &model.AcademicUnit{InstitutionID: institution.ID, Name: "affiliation-unit", DisplayName: "Affiliation Unit"})
	requireNoError(t, err)
	programme, err := ss.Programme().Save(ctx, &model.Programme{AcademicUnitID: unit.ID, Name: "affiliation-programme", DisplayName: "Affiliation Programme"})
	requireNoError(t, err)
	level, err := ss.ProgrammeLevel().Save(ctx, &model.ProgrammeLevel{ProgrammeID: programme.ID, Name: "affiliation-level", DisplayName: "Affiliation Level"})
	requireNoError(t, err)
	period, err := ss.AcademicPeriod().Save(ctx, &model.AcademicPeriod{InstitutionID: institution.ID, Name: "affiliation-period", DisplayName: "Affiliation Period", StartsAt: model.TimeFromMillis(1), EndsAt: model.TimeFromMillis(start + 10_000)})
	requireNoError(t, err)
	class := saveClass(t, ctx, ss, level.ID.String(), period.ID.String(), "affiliation-class")
	enrolledUser := saveUser(t, ctx, ss)
	student, err := ss.Affiliation().Save(ctx, &model.Affiliation{UserId: enrolledUser.Id, Kind: model.AffiliationStudent, StartAt: 1})
	requireNoError(t, err)
	_, err = ss.ClassMember().Enroll(ctx, &model.ClassMember{ClassId: class.ID.String(), UserId: enrolledUser.Id, StartAt: start})
	requireNoError(t, err)
	if _, err := ss.Affiliation().End(ctx, student.Id, student.Revision, start+1); !store.IsConflict(err) {
		t.Fatalf("End() with active enrollment error = %v", err)
	}
}

func saveAffiliationAuditAttempt(t *testing.T, ctx context.Context, ss store.Store, institutionID, userID string) *model.AuditEvent {
	t.Helper()
	attempt, err := ss.Audit().Save(ctx, &model.AuditEvent{Action: string(model.ActionUserManage), Resource: model.Resource{Type: model.ResourceUser, Id: userID}, ScopeType: model.RoleScopeInstitution, ScopeId: institutionID, Status: model.AuditStatusAttempt, NodeId: "test-node"})
	requireNoError(t, err)
	return attempt
}
