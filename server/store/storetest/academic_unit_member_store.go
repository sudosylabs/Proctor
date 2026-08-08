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
		AcademicUnitID: unit.ID, UserID: model.UserID(user.Id), StartsAt: model.TimeFromMillis(start),
	})
	requireNoError(t, err)
	active, err := ss.AcademicUnitMember().ListByAcademicUnit(ctx, unit.ID.String(), start+1)
	requireNoError(t, err)
	if len(active) != 1 || active[0].ID != saved.ID {
		t.Fatalf("ListByAcademicUnit() = %#v", active)
	}
	byUser, err := ss.AcademicUnitMember().ListActiveByUser(ctx, user.Id, start+1)
	requireNoError(t, err)
	if len(byUser) != 1 || byUser[0].AcademicUnitID != unit.ID {
		t.Fatalf("ListActiveByUser() = %#v", byUser)
	}
	_, err = ss.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{
		AcademicUnitID: unit.ID, UserID: model.UserID(user.Id), StartsAt: model.TimeFromMillis(start + 2),
	})
	var conflict *store.ErrConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("duplicate active membership error = %v", err)
	}
	ended, err := ss.AcademicUnitMember().End(ctx, saved.ID.String(), saved.Revision, start+10)
	requireNoError(t, err)
	if ended.EndsAt.Millis() != start+10 {
		t.Fatalf("End() = %#v", ended)
	}
	history, err := ss.AcademicUnitMember().ListByUser(ctx, user.Id)
	requireNoError(t, err)
	if len(history) != 1 || !history[0].EndsAt.Valid {
		t.Fatalf("ListByUser() = %#v", history)
	}
	_, err = ss.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{
		AcademicUnitID: unit.ID, UserID: model.UserID(user.Id), StartsAt: model.TimeFromMillis(start + 10),
	})
	requireNoError(t, err)
	auditedUser := saveUser(t, ctx, ss)
	createAttempt := saveAcademicUnitAuditAttempt(t, ctx, ss, unit.ID.String())
	candidate := &model.AcademicUnitMember{AcademicUnitID: unit.ID, UserID: model.UserID(auditedUser.Id), StartsAt: model.TimeFromMillis(start)}
	candidate.PrepareCreate(model.NewAcademicUnitMemberID(), model.NowUTC())
	created, err := ss.AcademicUnitMember().Create(ctx, &store.AcademicUnitMemberCreation{Member: candidate, AuditEventID: createAttempt.Id, AuditAt: model.GetMillis()})
	requireNoError(t, err)
	endAttempt := saveAcademicUnitAuditAttempt(t, ctx, ss, unit.ID.String())
	endedAudited, err := ss.AcademicUnitMember().EndWithAudit(ctx, &store.AcademicUnitMemberEnd{ID: created.ID.String(), ExpectedRevision: created.Revision, EndAt: start + 20, AuditEventID: endAttempt.Id, AuditAt: model.GetMillis()})
	requireNoError(t, err)
	if endedAudited.Revision != created.Revision+1 {
		t.Fatalf("EndWithAudit() = %#v", endedAudited)
	}
	if _, err := ss.AcademicUnitMember().End(ctx, created.ID.String(), created.Revision, start+21); !store.IsConflict(err) {
		t.Fatalf("stale End() error = %v", err)
	}
}
