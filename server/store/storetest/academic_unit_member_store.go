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

func TestAcademicUnitMemberStore(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	unit := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "member-unit")
	user := saveUser(t, ctx, ss)
	start := model.GetMillis() + 1000
	saved, err := ss.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{
		AcademicUnitId: unit.ID.String(), UserId: user.Id, StartAt: start,
	})
	requireNoError(t, err)
	active, err := ss.AcademicUnitMember().ListByAcademicUnit(ctx, unit.ID.String(), start+1)
	requireNoError(t, err)
	if len(active) != 1 || active[0].Id != saved.Id {
		t.Fatalf("ListByAcademicUnit() = %#v", active)
	}
	byUser, err := ss.AcademicUnitMember().ListActiveByUser(ctx, user.Id, start+1)
	requireNoError(t, err)
	if len(byUser) != 1 || byUser[0].AcademicUnitId != unit.ID.String() {
		t.Fatalf("ListActiveByUser() = %#v", byUser)
	}
	_, err = ss.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{
		AcademicUnitId: unit.ID.String(), UserId: user.Id, StartAt: start + 2,
	})
	var conflict *store.ErrConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("duplicate active membership error = %v", err)
	}
	ended, err := ss.AcademicUnitMember().End(ctx, saved.Id, saved.Revision, start+10)
	requireNoError(t, err)
	if ended.EndAt != start+10 {
		t.Fatalf("End() = %#v", ended)
	}
	history, err := ss.AcademicUnitMember().ListByUser(ctx, user.Id)
	requireNoError(t, err)
	if len(history) != 1 || history[0].EndAt == 0 {
		t.Fatalf("ListByUser() = %#v", history)
	}
	_, err = ss.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{
		AcademicUnitId: unit.ID.String(), UserId: user.Id, StartAt: start + 10,
	})
	requireNoError(t, err)
	auditedUser := saveUser(t, ctx, ss)
	createAttempt := saveAcademicUnitAuditAttempt(t, ctx, ss, unit.ID.String())
	candidate := &model.AcademicUnitMember{AcademicUnitId: unit.ID.String(), UserId: auditedUser.Id, StartAt: start}
	candidate.PrepareCreate(model.NewId(), model.GetMillis())
	created, err := ss.AcademicUnitMember().Create(ctx, &store.AcademicUnitMemberCreation{Member: candidate, AuditEventID: createAttempt.Id, AuditAt: model.GetMillis()})
	requireNoError(t, err)
	endAttempt := saveAcademicUnitAuditAttempt(t, ctx, ss, unit.ID.String())
	endedAudited, err := ss.AcademicUnitMember().EndWithAudit(ctx, &store.AcademicUnitMemberEnd{ID: created.Id, ExpectedRevision: created.Revision, EndAt: start + 20, AuditEventID: endAttempt.Id, AuditAt: model.GetMillis()})
	requireNoError(t, err)
	if endedAudited.Revision != created.Revision+1 {
		t.Fatalf("EndWithAudit() = %#v", endedAudited)
	}
	if _, err := ss.AcademicUnitMember().End(ctx, created.Id, created.Revision, start+21); !store.IsConflict(err) {
		t.Fatalf("stale End() error = %v", err)
	}
}
