// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestAcademicUnitMemberStore(t *testing.T, ss store.Store) {
	t.Run("StablePages", func(t *testing.T) { testAcademicUnitMemberPaging(t, ss) })
	t.Run("ActiveIntervalPrecision", func(t *testing.T) { testAcademicUnitMemberActiveIntervalPrecision(t, ss) })
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	unit := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "member-unit")
	user := saveUser(t, ctx, ss)
	start := model.GetMillis() + 1000
	saved, err := ss.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{
		AcademicUnitID: unit.ID, UserID: user.ID, StartsAt: model.TimeFromMillis(start),
	})
	requireNoError(t, err)
	active, err := ss.AcademicUnitMember().ListByAcademicUnit(ctx, unit.ID.String(), start+1)
	requireNoError(t, err)
	if len(active) != 1 || active[0].ID != saved.ID {
		t.Fatalf("ListByAcademicUnit() = %#v", active)
	}
	byUser, err := ss.AcademicUnitMember().ListActiveByUser(ctx, user.ID.String(), model.TimeFromMillis(start+1))
	requireNoError(t, err)
	if len(byUser) != 1 || byUser[0].AcademicUnitID != unit.ID {
		t.Fatalf("ListActiveByUser() = %#v", byUser)
	}
	_, err = ss.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{
		AcademicUnitID: unit.ID, UserID: user.ID, StartsAt: model.TimeFromMillis(start + 2),
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
	history, err := ss.AcademicUnitMember().ListByUser(ctx, user.ID.String())
	requireNoError(t, err)
	if len(history) != 1 || !history[0].EndsAt.Valid {
		t.Fatalf("ListByUser() = %#v", history)
	}
	_, err = ss.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{
		AcademicUnitID: unit.ID, UserID: user.ID, StartsAt: model.TimeFromMillis(start + 10),
	})
	requireNoError(t, err)
	auditedUser := saveUser(t, ctx, ss)
	createAttempt := saveAcademicUnitAuditAttempt(t, ctx, ss, unit.ID.String())
	candidate := &model.AcademicUnitMember{AcademicUnitID: unit.ID, UserID: auditedUser.ID, StartsAt: model.TimeFromMillis(start)}
	candidate.PrepareCreate(model.NewAcademicUnitMemberID(), model.NowUTC())
	createNotice := classMemberPreparedMail(t, &model.ClassMember{UserID: auditedUser.ID},
		model.MailTemplateAcademicUnitAssigned, candidate.CreatedAt)
	created, err := ss.AcademicUnitMember().Create(ctx, &store.AcademicUnitMemberCreation{Member: candidate,
		ExpectedRecipientRevision: auditedUser.Revision, Notice: createNotice,
		AuditEventID: createAttempt.ID.String(), AuditAt: model.GetMillis()})
	requireNoError(t, err)
	requireNoError(t, requireClassMemberMail(t, ctx, ss, createNotice, model.MailTemplateAcademicUnitAssigned))
	endAttempt := saveAcademicUnitAuditAttempt(t, ctx, ss, unit.ID.String())
	endAt := model.TimeFromMillis(start + 20)
	endNotice := classMemberPreparedMail(t, &model.ClassMember{UserID: auditedUser.ID},
		model.MailTemplateAcademicUnitAssignmentEnded, endAt)
	endedAudited, err := ss.AcademicUnitMember().EndWithAudit(ctx, &store.AcademicUnitMemberEnd{ID: created.ID.String(),
		ExpectedRevision: created.Revision, ExpectedRecipientRevision: auditedUser.Revision, Notice: endNotice,
		EndAt: start + 20, AuditEventID: endAttempt.ID.String(), AuditAt: model.GetMillis()})
	requireNoError(t, err)
	requireNoError(t, requireClassMemberMail(t, ctx, ss, endNotice, model.MailTemplateAcademicUnitAssignmentEnded))
	if endedAudited.Revision != created.Revision+1 {
		t.Fatalf("EndWithAudit() = %#v", endedAudited)
	}
	if _, err := ss.AcademicUnitMember().End(ctx, created.ID.String(), created.Revision, start+21); !store.IsConflict(err) {
		t.Fatalf("stale End() error = %v", err)
	}
}

func testAcademicUnitMemberActiveIntervalPrecision(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	unit := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "precise-member-unit")
	user := saveUser(t, ctx, ss)
	start := time.Date(2026, 9, 5, 12, 0, 0, 123200000, time.FixedZone("offset", 3600))
	end := start.Add(time.Second + 500*time.Microsecond)
	member, err := ss.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{
		AcademicUnitID: unit.ID, UserID: user.ID, StartsAt: start, EndsAt: model.OptionalTimeFrom(end),
	})
	requireNoError(t, err)
	for _, test := range []struct {
		name   string
		at     time.Time
		active bool
	}{
		{"before start", start.Add(-time.Microsecond), false},
		{"at start", start, true},
		{"before end", end.Add(-time.Microsecond), true},
		{"submicrosecond before end", end.Add(-time.Nanosecond), true},
		{"at end", end, false},
		{"after end in same millisecond", end.Add(time.Microsecond), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			active, err := ss.AcademicUnitMember().ListActiveByUser(ctx, user.ID.String(), test.at)
			requireNoError(t, err)
			if test.active {
				if len(active) != 1 || active[0].ID != member.ID {
					t.Fatalf("active memberships at %v = %#v, want saved membership", test.at, active)
				}
			} else if len(active) != 0 {
				t.Fatalf("active memberships at %v = %#v, want empty", test.at, active)
			}
		})
	}
}
