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
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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
		require.NoError(t, err)
	}
	created := createCatalogExam(t, ctx, ss, unit.ID, creator.ID, at, "manager-create")
	examID := created.Value.Exam.ID

	add := newExamManagerMutation(t, ctx, ss, examID, creator.ID, target.ID, 1, at.Add(time.Minute), false)
	add.Notices = examManagerMailNotices(t, at.Add(time.Minute), examManagerMailRecipient{target.ID, model.MailTemplateExamManagerAdded})
	added, err := ss.ExamAuthoring().AddManager(ctx, add, examCommand(creator.ID, "exam.manager.add", "manager-add", "manager-add-command"))
	require.NoError(t, err)
	require.False(t, added.Replayed)
	require.Equal(t, int64(2), added.Exam.Revision)
	require.Equal(t, target.ID, added.Manager.UserID)
	require.Equal(t, creator.ID, added.Exam.OwnerUserID)
	replay := newExamManagerMutation(t, ctx, ss, examID, creator.ID, target.ID, 1, at.Add(2*time.Minute), false)
	replayed, err := ss.ExamAuthoring().AddManager(ctx, replay, examCommand(creator.ID, "exam.manager.add", "manager-add", "manager-add-command"))
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
	require.Equal(t, int64(2), replayed.Exam.Revision)
	deliveries, err := ss.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{TemplateKeys: []model.MailTemplateKey{model.MailTemplateExamManagerAdded}, Limit: 10})
	require.NoError(t, err)
	require.Len(t, deliveries, 1)
	require.Equal(t, target.ID, deliveries[0].TargetUserID)
	_, err = ss.ExamAuthoring().AddManager(ctx, newExamManagerMutation(t, ctx, ss, examID, creator.ID, target.ID, 2, at.Add(3*time.Minute), false), examCommand(creator.ID, "exam.manager.add", "manager-duplicate", "manager-duplicate-command"))
	requireConflictConstraint(t, err, "exam_manager_exists")

	page, err := ss.ExamAuthoring().ListManagers(ctx, store.ExamManagerListOptions{ExamID: examID, Limit: 1})
	require.NoError(t, err)
	require.Len(t, page, 1)
	require.Equal(t, target.ID, page[0].Manager.UserID)
	require.False(t, page[0].IsCreator)
	require.False(t, page[0].IsOwner)
	next, err := ss.ExamAuthoring().ListManagers(ctx, store.ExamManagerListOptions{ExamID: examID, Limit: 1,
		BeforeGrantedAt: page[0].Manager.GrantedAt, BeforeUserID: page[0].Manager.UserID})
	require.NoError(t, err)
	require.Len(t, next, 1)
	require.Equal(t, creator.ID, next[0].Manager.UserID)
	require.True(t, next[0].IsCreator)
	require.True(t, next[0].IsOwner)

	transfer := newExamManagerMutation(t, ctx, ss, examID, creator.ID, target.ID, 2, at.Add(4*time.Minute), false)
	transfer.Notices = examManagerMailNotices(t, at.Add(4*time.Minute),
		examManagerMailRecipient{creator.ID, model.MailTemplateExamOwnershipTransferredFromYou},
		examManagerMailRecipient{target.ID, model.MailTemplateExamOwnershipTransferredToYou})
	transferred, err := ss.ExamAuthoring().TransferOwner(ctx, transfer, examCommand(creator.ID, "exam.owner.transfer", "owner-transfer", "owner-transfer-command"))
	require.NoError(t, err)
	require.Equal(t, target.ID, transferred.Exam.OwnerUserID)
	require.Equal(t, int64(3), transferred.Exam.Revision)
	_, err = ss.ExamAuthoring().TransferOwner(ctx, newExamManagerMutation(t, ctx, ss, examID, target.ID, target.ID, 3, at.Add(5*time.Minute), false), examCommand(target.ID, "exam.owner.transfer", "owner-transfer-noop", "owner-transfer-noop-command"))
	requireConflictConstraint(t, err, "exam_owner_no_changes")
	_, err = ss.ExamAuthoring().RemoveManager(ctx, newExamManagerMutation(t, ctx, ss, examID, target.ID, target.ID, 3, at.Add(5*time.Minute), false), examCommand(target.ID, "exam.manager.remove", "remove-owner", "remove-owner-command"))
	requireConflictConstraint(t, err, "exam_owner_manager")

	removeCreator := newExamManagerMutation(t, ctx, ss, examID, target.ID, creator.ID, 3, at.Add(6*time.Minute), false)
	removeCreator.Notices = examManagerMailNotices(t, at.Add(6*time.Minute), examManagerMailRecipient{creator.ID, model.MailTemplateExamManagerRemoved})
	removedCreator, err := ss.ExamAuthoring().RemoveManager(ctx, removeCreator, examCommand(target.ID, "exam.manager.remove", "remove-creator", "remove-creator-command"))
	require.NoError(t, err)
	require.Equal(t, int64(4), removedCreator.Exam.Revision)
	require.Equal(t, creator.ID, removedCreator.Manager.UserID)
	require.Equal(t, creator.ID, removedCreator.Exam.CreatorUserID)
	_, err = ss.ExamAuthoring().RemoveManager(ctx, newExamManagerMutation(t, ctx, ss, examID, target.ID, creator.ID, 4, at.Add(7*time.Minute), false), examCommand(target.ID, "exam.manager.remove", "remove-missing", "remove-missing-command"))
	requireConflictConstraint(t, err, "exam_manager_missing")
	_, err = ss.ExamAuthoring().AddManager(ctx, newExamManagerMutation(t, ctx, ss, examID, target.ID, later.ID, 3, at.Add(7*time.Minute), false), examCommand(target.ID, "exam.manager.add", "manager-add-stale", "manager-add-stale-command"))
	requireConflictConstraint(t, err, "exam_revision")

	addLater := newExamManagerMutation(t, ctx, ss, examID, target.ID, later.ID, 4, at.Add(7*time.Minute), false)
	addLater.Notices = examManagerMailNotices(t, at.Add(7*time.Minute), examManagerMailRecipient{later.ID, model.MailTemplateExamManagerAdded})
	addedLater, err := ss.ExamAuthoring().AddManager(ctx, addLater, examCommand(target.ID, "exam.manager.add", "manager-add-later", "manager-add-later-command"))
	require.NoError(t, err)
	require.Equal(t, int64(5), addedLater.Exam.Revision)
	memberships, err := ss.AcademicUnitMember().ListActiveByUser(ctx, later.ID, at.Add(8*time.Minute))
	require.NoError(t, err)
	require.Len(t, memberships, 1)
	_, err = EndAcademicUnitMember(ctx, ss, memberships[0].ID, memberships[0].Revision, model.MillisFromTime(memberships[0].CreatedAt.Add(time.Millisecond)))
	require.NoError(t, err)
	_, err = ss.ExamAuthoring().TransferOwner(ctx, newExamManagerMutation(t, ctx, ss, examID, target.ID, later.ID, 5, at.Add(9*time.Minute), false), examCommand(target.ID, "exam.owner.transfer", "ineligible-transfer", "ineligible-transfer-command"))
	requireConflictConstraint(t, err, "exam_manager_ineligible")

	current, err := ss.ExamAuthoring().Access(ctx, examID, later.ID)
	require.NoError(t, err)
	require.True(t, current.ActorIsManager)
	require.Equal(t, target.ID, current.Exam.OwnerUserID)
	require.Equal(t, int64(5), current.Exam.Revision)

	rollbackTarget := saveUser(t, ctx, ss)
	_, err = ss.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{AcademicUnitID: unit.ID, UserID: rollbackTarget.ID, StartsAt: at.Add(-time.Hour)})
	require.NoError(t, err)
	rollbackExam := createCatalogExam(t, ctx, ss, unit.ID, creator.ID, at.Add(10*time.Minute), "manager-mail-rollback")
	_, err = ss.ExamAuthoring().AddManager(ctx,
		newExamManagerMutation(t, ctx, ss, rollbackExam.Value.Exam.ID, creator.ID, rollbackTarget.ID, 1, at.Add(11*time.Minute), false),
		examCommand(creator.ID, "exam.manager.add", "manager-mail-rollback", "manager-mail-rollback-command"))
	var invalid *store.ErrInvalidInput
	require.ErrorAs(t, err, &invalid)
	rollbackState, err := ss.ExamAuthoring().Access(ctx, rollbackExam.Value.Exam.ID, rollbackTarget.ID)
	require.NoError(t, err)
	require.False(t, rollbackState.ActorIsManager)
	require.Equal(t, int64(1), rollbackState.Exam.Revision)

	testConcurrentExamManagerAdditions(t, ctx, ss, unit.ID, creator.ID, at.Add(12*time.Minute))
}

func testConcurrentExamManagerAdditions(t *testing.T, ctx context.Context, ss store.Store, unitID model.AcademicUnitID, creatorID model.UserID, at time.Time) {
	t.Helper()
	firstTarget, secondTarget := saveUser(t, ctx, ss), saveUser(t, ctx, ss)
	for _, target := range []*model.User{firstTarget, secondTarget} {
		_, err := ss.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{AcademicUnitID: unitID, UserID: target.ID, StartsAt: model.NowUTC().Add(-time.Hour)})
		require.NoError(t, err)
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
		examCommand(creatorID, "exam.manager.add", "manager-race-first", "manager-race-first-command"),
		examCommand(creatorID, "exam.manager.add", "manager-race-second", "manager-race-second-command"),
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
			require.NotNil(t, results[index], "concurrent addition result[%d]", index)
			require.Equal(t, int64(2), results[index].Exam.Revision, "concurrent addition result[%d]", index)
			continue
		}
		var conflict *store.ErrConflict
		if errors.As(err, &conflict) && conflict.Constraint == "exam_revision" {
			revisionConflicts++
			continue
		}
		require.FailNow(t, fmt.Sprintf("concurrent addition error[%d] = %v", index, err))
	}
	require.Equal(t, 1, successes, "concurrent additions: successes=%d revision conflicts=%d errors=%v", successes, revisionConflicts, errs)
	require.Equal(t, 1, revisionConflicts, "concurrent additions: successes=%d revision conflicts=%d errors=%v", successes, revisionConflicts, errs)
	managers, err := ss.ExamAuthoring().ListManagers(ctx, store.ExamManagerListOptions{ExamID: examID, Limit: 10})
	require.NoError(t, err)
	require.Len(t, managers, 2)
}

func newExamManagerMutation(t *testing.T, ctx context.Context, ss store.Store, examID model.ExamID, actorID, targetID model.UserID, revision int64, at time.Time, override bool) *store.ExamManagerMutation {
	t.Helper()
	exam, err := ss.ExamAuthoring().Resolve(ctx, examID)
	require.NoError(t, err)
	audit, err := ss.Audit().Save(ctx, &model.AuditEvent{ActorID: actorID, Action: string(model.ActionExamManage),
		Resource: model.Resource{Type: model.ResourceExam, ID: examID.String()}, ScopeType: model.RoleScopeAcademicUnit,
		ScopeID: exam.AcademicUnitID.String(), Status: model.AuditStatusAttempt, NodeID: "test-node"})
	require.NoError(t, err)
	return &store.ExamManagerMutation{ExamID: examID, ActorUserID: actorID, TargetUserID: targetID, ManagerOverride: override,
		ExpectedRevision: revision, ChangedAt: model.MillisFromTime(at),
		AuditEventID: audit.ID, AuditAt: model.MillisFromTime(at)}
}

type examManagerMailRecipient struct {
	userID model.UserID
	key    model.MailTemplateKey
}

func examManagerMailNotices(t *testing.T, at time.Time, recipients ...examManagerMailRecipient) []store.ExamManagerMail {
	t.Helper()
	require.NotEmpty(t, recipients, "Exam Manager mail fixture requires recipients")
	at = model.TimeFromMillis(model.MillisFromTime(at))
	notices := make([]store.ExamManagerMail, 0, len(recipients))
	for _, recipient := range recipients {
		occurrence, delivery, job := userTokenMailFixture(t, recipient.userID, model.NewMailOccurrenceID(),
			model.MailOccurrenceExamManagement, recipient.key, model.JobTypeMailDeliver, at, at.Add(72*time.Hour))
		notices = append(notices, store.ExamManagerMail{Occurrence: occurrence, Delivery: delivery, Job: job})
	}
	return notices
}
