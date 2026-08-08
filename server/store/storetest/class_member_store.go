// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestClassMemberStore(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture := saveClassFixture(t, ctx, ss)
	firstClass := saveClass(t, ctx, ss, fixture.level.ID.String(), fixture.period.Id, "class-member-a")
	secondClass := saveClass(t, ctx, ss, fixture.level.ID.String(), fixture.period.Id, "class-member-b")
	nextPeriod := saveAcademicPeriod(t, ctx, ss, fixture.institution.ID.String(), "class-member-next-period", fixture.period.EndAt+1)
	nextClass := saveClass(t, ctx, ss, fixture.level.ID.String(), nextPeriod.Id, "class-member-next")
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
	if first.Membership.Revision != 1 {
		t.Fatalf("first enrollment revision = %d, want 1", first.Membership.Revision)
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
		transfer.Previous.EndAt != start+10 ||
		transfer.Previous.Revision != first.Membership.Revision+1 {
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
	ended, err := ss.ClassMember().End(ctx, transfer.Membership.Id, transfer.Membership.Revision, start+20)
	requireNoError(t, err)
	if ended.EndAt != start+20 {
		t.Fatalf("End() = %#v", ended)
	}
	if _, err := ss.ClassMember().End(ctx, transfer.Membership.Id, transfer.Membership.Revision, start+21); !store.IsConflict(err) {
		t.Fatalf("stale End() error = %v", err)
	}
	_, err = ss.ClassMember().Enroll(ctx, &model.ClassMember{
		ClassId: firstClass.Id, UserId: user.Id, StartAt: start + 5,
	})
	if !errors.As(err, &conflict) {
		t.Fatalf("backdated overlapping enrollment error = %v", err)
	}

	t.Run("AuditedLifecycle", func(t *testing.T) {
		testAuditedClassMemberLifecycle(t, ss, firstClass, secondClass, start+100)
	})
	t.Run("ConcurrentEnrollment", func(t *testing.T) {
		testConcurrentClassMemberEnrollment(t, ss, firstClass, secondClass, start+200)
	})
	t.Run("DistinctAcademicPeriods", func(t *testing.T) {
		testDistinctPeriodClassMemberEnrollment(t, ss, firstClass, nextClass, start+300)
	})
	t.Run("FiniteAffiliation", func(t *testing.T) {
		testFiniteAffiliationCannotBackOpenEnrollment(t, ss, firstClass, 1_000_000)
	})
}

func testAuditedClassMemberLifecycle(
	t *testing.T,
	ss store.Store,
	firstClass *model.Class,
	secondClass *model.Class,
	start int64,
) {
	t.Helper()
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	_, err := ss.Affiliation().Save(ctx, &model.Affiliation{
		UserId: user.Id, Kind: model.AffiliationStudent, StartAt: start - 1,
	})
	requireNoError(t, err)

	first := &model.ClassMember{ClassId: firstClass.Id, UserId: user.Id, StartAt: start}
	first.PrepareCreate(model.NewId(), model.GetMillis())
	createAttempt := saveClassMemberAuditAttempt(t, ctx, ss, firstClass.Id)
	created, err := ss.ClassMember().EnrollWithAudit(ctx, &store.ClassMemberEnrollment{
		Member: first, AuditEventID: createAttempt.Id, AuditAt: model.GetMillis(),
	})
	requireNoError(t, err)
	requireSuccessfulAudit(t, ctx, ss, createAttempt.Id)

	rolledBackTransfer := &model.ClassMember{ClassId: secondClass.Id, UserId: user.Id, StartAt: start + 5}
	rolledBackTransfer.PrepareCreate(model.NewId(), model.GetMillis())
	if _, err := ss.ClassMember().EnrollWithAudit(ctx, &store.ClassMemberEnrollment{
		Member: rolledBackTransfer, AuditEventID: model.NewId(), AuditAt: model.GetMillis(),
	}); err == nil {
		t.Fatal("transfer succeeded without its audit attempt")
	}
	unchanged, err := ss.ClassMember().Get(ctx, created.Membership.Id)
	requireNoError(t, err)
	if unchanged.EndAt != 0 || unchanged.Revision != created.Membership.Revision {
		t.Fatalf("prior enrollment close survived transfer audit rollback: %#v", unchanged)
	}

	second := &model.ClassMember{ClassId: secondClass.Id, UserId: user.Id, StartAt: start + 10}
	second.PrepareCreate(model.NewId(), model.GetMillis())
	transferAttempt := saveClassMemberAuditAttempt(t, ctx, ss, secondClass.Id)
	transferred, err := ss.ClassMember().EnrollWithAudit(ctx, &store.ClassMemberEnrollment{
		Member: second, AuditEventID: transferAttempt.Id, AuditAt: model.GetMillis(),
	})
	requireNoError(t, err)
	if transferred.Previous == nil || transferred.Previous.Id != created.Membership.Id ||
		transferred.Previous.EndAt != second.StartAt || transferred.Previous.Revision != 2 {
		t.Fatalf("audited transfer = %#v", transferred)
	}
	requireSuccessfulAudit(t, ctx, ss, transferAttempt.Id)

	history, err := ss.ClassMember().ListByUser(ctx, user.Id)
	requireNoError(t, err)
	if len(history) != 2 || history[1].Id != created.Membership.Id ||
		history[1].EndAt != second.StartAt || history[1].Revision != 2 {
		t.Fatalf("audited enrollment history = %#v", history)
	}

	endAttempt := saveClassMemberAuditAttempt(t, ctx, ss, secondClass.Id)
	ended, err := ss.ClassMember().EndWithAudit(ctx, &store.ClassMemberEnd{
		ID: transferred.Membership.Id, ExpectedRevision: transferred.Membership.Revision,
		EndAt: start + 30, AuditEventID: endAttempt.Id, AuditAt: model.GetMillis(),
	})
	requireNoError(t, err)
	if ended.Revision != transferred.Membership.Revision+1 || ended.EndAt != start+30 {
		t.Fatalf("EndWithAudit() = %#v", ended)
	}
	requireSuccessfulAudit(t, ctx, ss, endAttempt.Id)

	staleAttempt := saveClassMemberAuditAttempt(t, ctx, ss, secondClass.Id)
	if _, err := ss.ClassMember().EndWithAudit(ctx, &store.ClassMemberEnd{
		ID: ended.Id, ExpectedRevision: transferred.Membership.Revision,
		EndAt: start + 31, AuditEventID: staleAttempt.Id, AuditAt: model.GetMillis(),
	}); !store.IsConflict(err) {
		t.Fatalf("stale EndWithAudit() error = %v", err)
	}

	rollbackUser := saveUser(t, ctx, ss)
	_, err = ss.Affiliation().Save(ctx, &model.Affiliation{
		UserId: rollbackUser.Id, Kind: model.AffiliationStudent, StartAt: start - 1,
	})
	requireNoError(t, err)
	rolledBack := &model.ClassMember{ClassId: firstClass.Id, UserId: rollbackUser.Id, StartAt: start}
	rolledBack.PrepareCreate(model.NewId(), model.GetMillis())
	if _, err := ss.ClassMember().EnrollWithAudit(ctx, &store.ClassMemberEnrollment{
		Member: rolledBack, AuditEventID: model.NewId(), AuditAt: model.GetMillis(),
	}); err == nil {
		t.Fatal("EnrollWithAudit() succeeded without its audit attempt")
	}
	if _, err := ss.ClassMember().Get(ctx, rolledBack.Id); !store.IsNotFound(err) {
		t.Fatalf("enrollment survived audit rollback: %v", err)
	}
}

func testDistinctPeriodClassMemberEnrollment(t *testing.T, ss store.Store, firstClass, nextClass *model.Class, start int64) {
	t.Helper()
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	_, err := ss.Affiliation().Save(ctx, &model.Affiliation{UserId: user.Id, Kind: model.AffiliationStudent, StartAt: start - 1})
	requireNoError(t, err)
	first, err := ss.ClassMember().Enroll(ctx, &model.ClassMember{ClassId: firstClass.Id, UserId: user.Id, StartAt: start})
	requireNoError(t, err)
	next, err := ss.ClassMember().Enroll(ctx, &model.ClassMember{ClassId: nextClass.Id, UserId: user.Id, StartAt: start})
	requireNoError(t, err)
	if next.Previous != nil {
		t.Fatalf("distinct-period enrollment replaced prior membership: %#v", next)
	}
	active, err := ss.ClassMember().ListActiveByUser(ctx, user.Id, start+1)
	requireNoError(t, err)
	if len(active) != 2 || first.Membership.AcademicPeriodId == next.Membership.AcademicPeriodId {
		t.Fatalf("distinct-period active enrollments = %#v", active)
	}
}

func testFiniteAffiliationCannotBackOpenEnrollment(t *testing.T, ss store.Store, class *model.Class, start int64) {
	t.Helper()
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	_, err := ss.Affiliation().Save(ctx, &model.Affiliation{UserId: user.Id, Kind: model.AffiliationStudent, StartAt: start - 1, EndAt: start + 100})
	requireNoError(t, err)
	_, err = ss.ClassMember().Enroll(ctx, &model.ClassMember{ClassId: class.Id, UserId: user.Id, StartAt: start})
	if !store.IsConflict(err) {
		t.Fatalf("open enrollment with finite affiliation error = %v", err)
	}
}

func testConcurrentClassMemberEnrollment(
	t *testing.T,
	ss store.Store,
	firstClass *model.Class,
	secondClass *model.Class,
	start int64,
) {
	t.Helper()
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	_, err := ss.Affiliation().Save(ctx, &model.Affiliation{
		UserId: user.Id, Kind: model.AffiliationStudent, StartAt: start - 1,
	})
	requireNoError(t, err)
	members := []*model.ClassMember{
		{ClassId: firstClass.Id, UserId: user.Id, StartAt: start},
		{ClassId: secondClass.Id, UserId: user.Id, StartAt: start},
	}
	attempts := make([]*model.AuditEvent, len(members))
	for i, member := range members {
		member.PrepareCreate(model.NewId(), model.GetMillis())
		attempts[i] = saveClassMemberAuditAttempt(t, ctx, ss, member.ClassId)
	}
	errs := make([]error, len(members))
	var wg sync.WaitGroup
	for i := range members {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			_, errs[index] = ss.ClassMember().EnrollWithAudit(ctx, &store.ClassMemberEnrollment{
				Member: members[index], AuditEventID: attempts[index].Id, AuditAt: model.GetMillis(),
			})
		}(i)
	}
	wg.Wait()
	successes := 0
	for _, err := range errs {
		if err == nil {
			successes++
		} else if !store.IsConflict(err) {
			t.Fatalf("concurrent enrollment error = %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent enrollment successes = %d, errors = %v", successes, errs)
	}
	active, err := ss.ClassMember().ListActiveByUser(ctx, user.Id, start+1)
	requireNoError(t, err)
	if len(active) != 1 {
		t.Fatalf("active concurrent enrollments = %#v", active)
	}
	completedAudits := 0
	for _, attempt := range attempts {
		event, err := ss.Audit().Get(ctx, attempt.Id)
		requireNoError(t, err)
		if event.Status == model.AuditStatusSuccess {
			completedAudits++
		} else if event.Status != model.AuditStatusAttempt {
			t.Fatalf("concurrent enrollment audit = %#v", event)
		}
	}
	if completedAudits != 1 {
		t.Fatalf("completed concurrent enrollment audits = %d, want 1", completedAudits)
	}
}

func saveClassMemberAuditAttempt(
	t *testing.T,
	ctx context.Context,
	ss store.Store,
	classID string,
) *model.AuditEvent {
	t.Helper()
	attempt, err := ss.Audit().Save(ctx, &model.AuditEvent{
		Action:    string(model.ActionClassMembersManage),
		Resource:  model.Resource{Type: model.ResourceClass, Id: classID},
		ScopeType: model.RoleScopeClass, ScopeId: classID,
		Status: model.AuditStatusAttempt, NodeId: "test-node",
	})
	requireNoError(t, err)
	return attempt
}

func requireSuccessfulAudit(t *testing.T, ctx context.Context, ss store.Store, id string) {
	t.Helper()
	event, err := ss.Audit().Get(ctx, id)
	requireNoError(t, err)
	if event.Status != model.AuditStatusSuccess {
		t.Fatalf("audit %s status = %q, want success", id, event.Status)
	}
}
