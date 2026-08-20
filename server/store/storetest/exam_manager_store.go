// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func testExamManagersAndOwnership(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	unit := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "exam-manager-unit")
	creator, target, later := saveUser(t, ctx, ss), saveUser(t, ctx, ss), saveUser(t, ctx, ss)
	at := model.NowUTC()
	for _, user := range []*model.User{creator, target, later} {
		_, err := ss.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{AcademicUnitID: unit.ID, UserID: user.ID, StartsAt: at.Add(-time.Hour)})
		requireNoError(t, err)
	}
	created := createCatalogExam(t, ctx, ss, unit.ID, creator.ID, at, "manager-create")
	examID := created.Value.Exam.ID

	add := newExamManagerMutation(t, ctx, ss, examID, creator.ID, target.ID, 1, at.Add(time.Minute), false)
	add.Notices = examManagerMailNotices(t, at.Add(time.Minute), examManagerMailRecipient{target.ID, model.MailTemplateExamManagerAdded})
	added, err := ss.ExamAuthoring().AddManager(ctx, add, examCommand(creator.ID, "exam.manager.add.v1", "manager-add", "manager-add-command"))
	requireNoError(t, err)
	if added.Replayed || added.Exam.Revision != 2 || added.Manager.UserID != target.ID || added.Exam.OwnerUserID != creator.ID {
		t.Fatalf("added Manager = %#v", added)
	}
	replay := newExamManagerMutation(t, ctx, ss, examID, creator.ID, target.ID, 1, at.Add(2*time.Minute), false)
	replayed, err := ss.ExamAuthoring().AddManager(ctx, replay, examCommand(creator.ID, "exam.manager.add.v1", "manager-add", "manager-add-command"))
	requireNoError(t, err)
	if !replayed.Replayed || replayed.Exam.Revision != 2 {
		t.Fatalf("replayed Manager addition = %#v", replayed)
	}
	deliveries, err := ss.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{TemplateKeys: []model.MailTemplateKey{model.MailTemplateExamManagerAdded}, Limit: 10})
	requireNoError(t, err)
	if len(deliveries) != 1 || deliveries[0].TargetUserID != target.ID {
		t.Fatalf("Manager addition deliveries = %#v", deliveries)
	}
	_, err = ss.ExamAuthoring().AddManager(ctx, newExamManagerMutation(t, ctx, ss, examID, creator.ID, target.ID, 2, at.Add(3*time.Minute), false), examCommand(creator.ID, "exam.manager.add.v1", "manager-duplicate", "manager-duplicate-command"))
	assertExamManagerConflict(t, err, "exam_manager_exists")

	page, err := ss.ExamAuthoring().ListManagers(ctx, store.ExamManagerListOptions{ExamID: examID, Limit: 1})
	requireNoError(t, err)
	if len(page) != 1 || page[0].Manager.UserID != target.ID || page[0].IsCreator || page[0].IsOwner {
		t.Fatalf("first Manager page = %#v", page)
	}
	next, err := ss.ExamAuthoring().ListManagers(ctx, store.ExamManagerListOptions{ExamID: examID, Limit: 1,
		BeforeGrantedAt: page[0].Manager.GrantedAt, BeforeUserID: page[0].Manager.UserID})
	requireNoError(t, err)
	if len(next) != 1 || next[0].Manager.UserID != creator.ID || !next[0].IsCreator || !next[0].IsOwner {
		t.Fatalf("next Manager page = %#v", next)
	}

	transfer := newExamManagerMutation(t, ctx, ss, examID, creator.ID, target.ID, 2, at.Add(4*time.Minute), false)
	transfer.Notices = examManagerMailNotices(t, at.Add(4*time.Minute),
		examManagerMailRecipient{creator.ID, model.MailTemplateExamOwnershipTransferredFromYou},
		examManagerMailRecipient{target.ID, model.MailTemplateExamOwnershipTransferredToYou})
	transferred, err := ss.ExamAuthoring().TransferOwner(ctx, transfer, examCommand(creator.ID, "exam.owner.transfer.v1", "owner-transfer", "owner-transfer-command"))
	requireNoError(t, err)
	if transferred.Exam.OwnerUserID != target.ID || transferred.Exam.Revision != 3 {
		t.Fatalf("transferred owner = %#v", transferred)
	}
	_, err = ss.ExamAuthoring().TransferOwner(ctx, newExamManagerMutation(t, ctx, ss, examID, target.ID, target.ID, 3, at.Add(5*time.Minute), false), examCommand(target.ID, "exam.owner.transfer.v1", "owner-transfer-noop", "owner-transfer-noop-command"))
	assertExamManagerConflict(t, err, "exam_owner_no_changes")
	_, err = ss.ExamAuthoring().RemoveManager(ctx, newExamManagerMutation(t, ctx, ss, examID, target.ID, target.ID, 3, at.Add(5*time.Minute), false), examCommand(target.ID, "exam.manager.remove.v1", "remove-owner", "remove-owner-command"))
	assertExamManagerConflict(t, err, "exam_owner_manager")

	removeCreator := newExamManagerMutation(t, ctx, ss, examID, target.ID, creator.ID, 3, at.Add(6*time.Minute), false)
	removeCreator.Notices = examManagerMailNotices(t, at.Add(6*time.Minute), examManagerMailRecipient{creator.ID, model.MailTemplateExamManagerRemoved})
	removedCreator, err := ss.ExamAuthoring().RemoveManager(ctx, removeCreator, examCommand(target.ID, "exam.manager.remove.v1", "remove-creator", "remove-creator-command"))
	requireNoError(t, err)
	if removedCreator.Exam.Revision != 4 || removedCreator.Manager.UserID != creator.ID || removedCreator.Exam.CreatorUserID != creator.ID {
		t.Fatalf("removed creator relationship = %#v", removedCreator)
	}
	_, err = ss.ExamAuthoring().RemoveManager(ctx, newExamManagerMutation(t, ctx, ss, examID, target.ID, creator.ID, 4, at.Add(7*time.Minute), false), examCommand(target.ID, "exam.manager.remove.v1", "remove-missing", "remove-missing-command"))
	assertExamManagerConflict(t, err, "exam_manager_missing")
	_, err = ss.ExamAuthoring().AddManager(ctx, newExamManagerMutation(t, ctx, ss, examID, target.ID, later.ID, 3, at.Add(7*time.Minute), false), examCommand(target.ID, "exam.manager.add.v1", "manager-add-stale", "manager-add-stale-command"))
	assertExamManagerConflict(t, err, "exam_revision")

	addLater := newExamManagerMutation(t, ctx, ss, examID, target.ID, later.ID, 4, at.Add(7*time.Minute), false)
	addLater.Notices = examManagerMailNotices(t, at.Add(7*time.Minute), examManagerMailRecipient{later.ID, model.MailTemplateExamManagerAdded})
	addedLater, err := ss.ExamAuthoring().AddManager(ctx, addLater, examCommand(target.ID, "exam.manager.add.v1", "manager-add-later", "manager-add-later-command"))
	requireNoError(t, err)
	if addedLater.Exam.Revision != 5 {
		t.Fatalf("later Manager = %#v", addedLater)
	}
	memberships, err := ss.AcademicUnitMember().ListActiveByUser(ctx, later.ID.String(), model.MillisFromTime(at.Add(8*time.Minute)))
	requireNoError(t, err)
	if len(memberships) != 1 {
		t.Fatalf("later memberships = %#v", memberships)
	}
	_, err = ss.AcademicUnitMember().End(ctx, memberships[0].ID.String(), memberships[0].Revision, model.MillisFromTime(memberships[0].CreatedAt.Add(time.Millisecond)))
	requireNoError(t, err)
	_, err = ss.ExamAuthoring().TransferOwner(ctx, newExamManagerMutation(t, ctx, ss, examID, target.ID, later.ID, 5, at.Add(9*time.Minute), false), examCommand(target.ID, "exam.owner.transfer.v1", "ineligible-transfer", "ineligible-transfer-command"))
	assertExamManagerConflict(t, err, "exam_manager_ineligible")

	current, err := ss.ExamAuthoring().Access(ctx, examID, later.ID)
	requireNoError(t, err)
	if !current.ActorIsManager || current.Exam.OwnerUserID != target.ID || current.Exam.Revision != 5 {
		t.Fatalf("lost eligibility erased provenance or changed owner: %#v", current)
	}

	rollbackTarget := saveUser(t, ctx, ss)
	_, err = ss.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{AcademicUnitID: unit.ID, UserID: rollbackTarget.ID, StartsAt: at.Add(-time.Hour)})
	requireNoError(t, err)
	rollbackExam := createCatalogExam(t, ctx, ss, unit.ID, creator.ID, at.Add(10*time.Minute), "manager-mail-rollback")
	_, err = ss.ExamAuthoring().AddManager(ctx,
		newExamManagerMutation(t, ctx, ss, rollbackExam.Value.Exam.ID, creator.ID, rollbackTarget.ID, 1, at.Add(11*time.Minute), false),
		examCommand(creator.ID, "exam.manager.add.v1", "manager-mail-rollback", "manager-mail-rollback-command"))
	var invalid *store.ErrInvalidInput
	if !errors.As(err, &invalid) {
		t.Fatalf("missing atomic Manager mail error = %v", err)
	}
	rollbackState, err := ss.ExamAuthoring().Access(ctx, rollbackExam.Value.Exam.ID, rollbackTarget.ID)
	requireNoError(t, err)
	if rollbackState.ActorIsManager || rollbackState.Exam.Revision != 1 {
		t.Fatalf("failed Manager mail changed relationship = %#v", rollbackState)
	}

	testConcurrentExamManagerAdditions(t, ctx, ss, unit.ID, creator.ID, at.Add(12*time.Minute))
}

func testConcurrentExamManagerAdditions(t *testing.T, ctx context.Context, ss store.Store, unitID model.AcademicUnitID, creatorID model.UserID, at time.Time) {
	t.Helper()
	firstTarget, secondTarget := saveUser(t, ctx, ss), saveUser(t, ctx, ss)
	for _, target := range []*model.User{firstTarget, secondTarget} {
		_, err := ss.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{AcademicUnitID: unitID, UserID: target.ID, StartsAt: model.NowUTC().Add(-time.Hour)})
		requireNoError(t, err)
	}
	created := createCatalogExam(t, ctx, ss, unitID, creatorID, at, "manager-race-create")
	examID := created.Value.Exam.ID
	mutations := []*store.ExamManagerMutation{
		newExamManagerMutation(t, ctx, ss, examID, creatorID, firstTarget.ID, 1, at.Add(time.Minute), false),
		newExamManagerMutation(t, ctx, ss, examID, creatorID, secondTarget.ID, 1, at.Add(time.Minute), false),
	}
	mutations[0].Notices = examManagerMailNotices(t, at.Add(time.Minute), examManagerMailRecipient{firstTarget.ID, model.MailTemplateExamManagerAdded})
	mutations[1].Notices = examManagerMailNotices(t, at.Add(time.Minute), examManagerMailRecipient{secondTarget.ID, model.MailTemplateExamManagerAdded})
	commands := []*store.CommandIdempotency{
		examCommand(creatorID, "exam.manager.add.v1", "manager-race-first", "manager-race-first-command"),
		examCommand(creatorID, "exam.manager.add.v1", "manager-race-second", "manager-race-second-command"),
	}

	errs := make([]error, len(mutations))
	results := make([]*store.ExamManagerCommandResult, len(mutations))
	var wg sync.WaitGroup
	for index := range mutations {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[index], errs[index] = ss.ExamAuthoring().AddManager(ctx, mutations[index], commands[index])
		}()
	}
	wg.Wait()

	successes, revisionConflicts := 0, 0
	for index, err := range errs {
		if err == nil {
			successes++
			if results[index] == nil || results[index].Exam.Revision != 2 {
				t.Fatalf("concurrent addition result[%d] = %#v", index, results[index])
			}
			continue
		}
		var conflict *store.ErrConflict
		if errors.As(err, &conflict) && conflict.Constraint == "exam_revision" {
			revisionConflicts++
			continue
		}
		t.Fatalf("concurrent addition error[%d] = %v", index, err)
	}
	if successes != 1 || revisionConflicts != 1 {
		t.Fatalf("concurrent additions: successes=%d revision conflicts=%d errors=%v", successes, revisionConflicts, errs)
	}
	managers, err := ss.ExamAuthoring().ListManagers(ctx, store.ExamManagerListOptions{ExamID: examID, Limit: 10})
	requireNoError(t, err)
	if len(managers) != 2 {
		t.Fatalf("Managers after concurrent additions = %#v", managers)
	}
}

func newExamManagerMutation(t *testing.T, ctx context.Context, ss store.Store, examID model.ExamID, actorID, targetID model.UserID, revision int64, at time.Time, override bool) *store.ExamManagerMutation {
	t.Helper()
	exam, err := ss.ExamAuthoring().Resolve(ctx, examID)
	requireNoError(t, err)
	audit, err := ss.Audit().Save(ctx, &model.AuditEvent{ActorID: actorID, Action: string(model.ActionExamManage),
		Resource: model.Resource{Type: model.ResourceExam, ID: examID.String()}, ScopeType: model.RoleScopeAcademicUnit,
		ScopeID: exam.AcademicUnitID.String(), Status: model.AuditStatusAttempt, NodeID: "test-node"})
	requireNoError(t, err)
	return &store.ExamManagerMutation{ExamID: examID, ActorUserID: actorID, TargetUserID: targetID, ManagerOverride: override,
		ExpectedRevision: revision, ChangedAt: model.MillisFromTime(at),
		AuditEventID: audit.ID.String(), AuditAt: model.MillisFromTime(at)}
}

type examManagerMailRecipient struct {
	userID model.UserID
	key    model.MailTemplateKey
}

func examManagerMailNotices(t *testing.T, at time.Time, recipients ...examManagerMailRecipient) []store.ExamManagerMail {
	t.Helper()
	if len(recipients) == 0 {
		t.Fatal("Exam Manager mail fixture requires recipients")
	}
	at = model.TimeFromMillis(model.MillisFromTime(at))
	notices := make([]store.ExamManagerMail, 0, len(recipients))
	for _, recipient := range recipients {
		occurrence, delivery, job := userTokenMailFixture(t, recipient.userID, model.NewMailOccurrenceID(),
			model.MailOccurrenceExamManagement, recipient.key, model.JobTypeMailDeliver, at, at.Add(72*time.Hour))
		notices = append(notices, store.ExamManagerMail{Occurrence: occurrence, Delivery: delivery, Job: job})
	}
	return notices
}

func assertExamManagerConflict(t *testing.T, err error, constraint string) {
	t.Helper()
	var conflict *store.ErrConflict
	if !errors.As(err, &conflict) || conflict.Constraint != constraint {
		t.Fatalf("error = %v, want %s conflict", err, constraint)
	}
}
