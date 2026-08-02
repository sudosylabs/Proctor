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

func TestClassMemberStore(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture := saveClassFixture(t, ctx, ss)
	firstClass := saveClass(t, ctx, ss, fixture.level.Id, fixture.period.Id, "class-member-a")
	secondClass := saveClass(t, ctx, ss, fixture.level.Id, fixture.period.Id, "class-member-b")
	user := saveUser(t, ctx, ss)
	start := model.GetMillis() + 1000
	_, err := ss.Affiliation().Save(ctx, &model.Affiliation{UserId: user.Id, Kind: model.AffiliationStudent, StartAt: start - 1})
	requireNoError(t, err)

	first, err := ss.ClassMember().Enroll(ctx, &model.ClassMember{
		ClassId: firstClass.Id, AcademicPeriodId: model.NewId(),
		UserId: user.Id, StartAt: start,
	})
	requireNoError(t, err)
	if first.Previous != nil || first.Membership.AcademicPeriodId != fixture.period.Id {
		t.Fatalf("first Enroll() = %#v", first)
	}
	_, err = ss.ClassMember().Enroll(ctx, &model.ClassMember{
		ClassId: firstClass.Id, UserId: user.Id, StartAt: start + 1,
	})
	var conflict *store.ErrConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("duplicate enrollment error = %v", err)
	}
	transfer, err := ss.ClassMember().Enroll(ctx, &model.ClassMember{
		ClassId: secondClass.Id, UserId: user.Id, StartAt: start + 10,
	})
	requireNoError(t, err)
	if transfer.Previous == nil ||
		transfer.Previous.Id != first.Membership.Id ||
		transfer.Previous.EndAt != start+10 {
		t.Fatalf("transfer Enroll() = %#v", transfer)
	}
	active, err := ss.ClassMember().ListActiveByUser(ctx, user.Id, start+11)
	requireNoError(t, err)
	if len(active) != 1 || active[0].Id != transfer.Membership.Id {
		t.Fatalf("ListActiveByUser() = %#v", active)
	}
	history, err := ss.ClassMember().ListByUser(ctx, user.Id)
	requireNoError(t, err)
	if len(history) != 2 {
		t.Fatalf("ListByUser() = %#v", history)
	}
	ended, err := ss.ClassMember().End(ctx, transfer.Membership.Id, start+20)
	requireNoError(t, err)
	if ended.EndAt != start+20 {
		t.Fatalf("End() = %#v", ended)
	}
	_, err = ss.ClassMember().Enroll(ctx, &model.ClassMember{
		ClassId: firstClass.Id, UserId: user.Id, StartAt: start + 5,
	})
	if !errors.As(err, &conflict) {
		t.Fatalf("backdated overlapping enrollment error = %v", err)
	}
}
