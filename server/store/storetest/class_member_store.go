// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestClassMemberStore(t *testing.T, ss store.Store) {
	t.Run("StablePages", func(t *testing.T) { testClassMemberPaging(t, ss) })
	ctx := context.Background()
	fixture := saveClassFixture(t, ctx, ss)
	firstClass := saveClass(t, ctx, ss, fixture.level.ID.String(), fixture.period.ID.String(), "class-member-a")
	secondClass := saveClass(t, ctx, ss, fixture.level.ID.String(), fixture.period.ID.String(), "class-member-b")
	nextPeriod := saveAcademicPeriod(t, ctx, ss, fixture.institution.ID.String(), "class-member-next-period", model.MillisFromTime(fixture.period.EndsAt)+1)
	nextClass := saveClass(t, ctx, ss, fixture.level.ID.String(), nextPeriod.ID.String(), "class-member-next")
	user := saveUser(t, ctx, ss)
	start := model.GetMillis() + 1000
	_, err := ss.Affiliation().Save(ctx, &model.Affiliation{
		UserID: user.ID, Kind: model.AffiliationStudent, StartsAt: model.TimeFromMillis(start - 1),
	})
	require.NoError(t, err)

	first, err := ss.ClassMember().SeedEnroll(ctx, &model.ClassMember{
		ClassID:          firstClass.ID,
		AcademicPeriodID: model.NewAcademicPeriodID(),
		UserID:           user.ID,
		StartsAt:         model.TimeFromMillis(start),
	})
	require.NoError(t, err)
	require.Nil(t, first.Previous)
	require.Equal(t, fixture.period.ID, first.Membership.AcademicPeriodID)
	require.Equal(t, int64(1), first.Membership.Revision, "first enrollment revision")
	_, err = ss.ClassMember().SeedEnroll(ctx, &model.ClassMember{
		ClassID: firstClass.ID, UserID: user.ID, StartsAt: model.TimeFromMillis(start + 1),
	})
	var conflict *store.ErrConflict
	require.ErrorAs(t, err, &conflict)
	transfer, err := ss.ClassMember().SeedEnroll(ctx, &model.ClassMember{
		ClassID: secondClass.ID, UserID: user.ID, StartsAt: model.TimeFromMillis(start + 10),
	})
	require.NoError(t, err)
	require.NotNil(t, transfer.Previous)
	require.Equal(t, first.Membership.ID, transfer.Previous.ID)
	require.Equal(t, start+10, transfer.Previous.EndsAt.Millis())
	require.Equal(t, first.Membership.Revision+1, transfer.Previous.Revision)
	active, err := ss.ClassMember().ListActiveByUser(ctx, user.ID, start+11)
	require.NoError(t, err)
	require.Len(t, active, 1)
	require.Equal(t, transfer.Membership.ID, active[0].ID)
	history, err := ss.ClassMember().ListByUser(ctx, user.ID)
	require.NoError(t, err)
	require.Len(t, history, 2)
	ended, err := ss.ClassMember().SeedEnd(ctx, transfer.Membership.ID, transfer.Membership.Revision, start+20)
	require.NoError(t, err)
	require.Equal(t, start+20, ended.EndsAt.Millis())
	_, err = ss.ClassMember().SeedEnd(ctx, transfer.Membership.ID, transfer.Membership.Revision, start+21)
	require.True(t, store.IsConflict(err), "stale End() error")
	_, err = ss.ClassMember().SeedEnroll(ctx, &model.ClassMember{
		ClassID: firstClass.ID, UserID: user.ID, StartsAt: model.TimeFromMillis(start + 5),
	})
	require.ErrorAs(t, err, &conflict)

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
	t.Run("EndBeforeModification", func(t *testing.T) { testClassMemberEndBeforeModification(t, ss) })
}

// testClassMemberEndBeforeModification ends a backdated enrollment at an
// effective instant earlier than the row's creation. The same ordering arises
// when a millisecond mutation instant falls inside the microsecond creation
// instant or a peer node's clock trails the creating node.
func testClassMemberEndBeforeModification(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture := saveClassFixture(t, ctx, ss)
	now := model.GetMillis()
	period := saveAcademicPeriod(t, ctx, ss, fixture.institution.ID.String(), "class-member-backdated-"+model.NewID(), now-3_600_000)
	class := saveClass(t, ctx, ss, fixture.level.ID.String(), period.ID.String(), "class-member-backdated")
	user := saveUser(t, ctx, ss)
	_, err := ss.Affiliation().Save(ctx, &model.Affiliation{
		UserID: user.ID, Kind: model.AffiliationStudent, StartsAt: model.TimeFromMillis(now - 3_000_000),
	})
	require.NoError(t, err)
	enrolled, err := ss.ClassMember().SeedEnroll(ctx, &model.ClassMember{
		ClassID: class.ID, UserID: user.ID, StartsAt: model.TimeFromMillis(now - 2_000_000),
	})
	require.NoError(t, err)
	endAt := now - 1_000_000
	ended, err := ss.ClassMember().SeedEnd(ctx, enrolled.Membership.ID, enrolled.Membership.Revision, endAt)
	require.NoError(t, err)
	require.Equal(t, endAt, ended.EndsAt.Millis(), "End(before creation) = %#v, created %#v", ended, enrolled.Membership)
	require.True(t, ended.UpdatedAt.Equal(enrolled.Membership.UpdatedAt), "End(before creation) = %#v, created %#v", ended, enrolled.Membership)
	stored, err := ss.ClassMember().Get(ctx, ended.ID)
	require.NoError(t, err)
	require.Equal(t, *ended, *stored, "Get() = %#v, want %#v", stored, ended)
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
		UserID: user.ID, Kind: model.AffiliationStudent, StartsAt: model.TimeFromMillis(start - 1),
	})
	require.NoError(t, err)

	first := &model.ClassMember{ClassID: firstClass.ID, UserID: user.ID, StartsAt: model.TimeFromMillis(start)}
	first.PrepareCreate(model.NewClassMemberID(), model.NowUTC())
	// Period is authoritative from the class row during enroll.
	first.AcademicPeriodID = firstClass.AcademicPeriodID
	createAttempt := saveClassMemberAuditAttempt(t, ctx, ss, firstClass.ID.String())
	createNotice := classMemberPreparedMail(t, first, model.MailTemplateAcademicClassEnrolled, first.CreatedAt)
	created, err := ss.ClassMember().Enroll(ctx, &store.ClassMemberEnrollment{
		Member: first, ExpectedRecipientRevision: user.Revision,
		Notice: createNotice, AuditEventID: createAttempt.ID, AuditAt: model.GetMillis(),
	})
	require.NoError(t, err)
	requireSuccessfulAudit(t, ctx, ss, createAttempt.ID)
	require.NoError(t, requireClassMemberMail(t, ctx, ss, createNotice, model.MailTemplateAcademicClassEnrolled))

	rolledBackTransfer := &model.ClassMember{ClassID: secondClass.ID, UserID: user.ID, StartsAt: model.TimeFromMillis(start + 5)}
	rolledBackTransfer.PrepareCreate(model.NewClassMemberID(), model.NowUTC())
	rolledBackTransfer.AcademicPeriodID = secondClass.AcademicPeriodID
	_, err = ss.ClassMember().Enroll(ctx, &store.ClassMemberEnrollment{
		Member: rolledBackTransfer, ExpectedPreviousID: created.Membership.ID, ExpectedRecipientRevision: user.Revision,
		Notice:               classMemberPreparedMail(t, rolledBackTransfer, model.MailTemplateAcademicClassTransferred, rolledBackTransfer.CreatedAt),
		AuditEventID:         model.NewAuditEventID(),
		PreviousAuditEventID: model.NewAuditEventID(), AuditAt: model.GetMillis(),
	})
	require.Error(t, err, "transfer succeeded without its audit attempt")
	unchanged, err := ss.ClassMember().Get(ctx, created.Membership.ID)
	require.NoError(t, err)
	require.False(t, unchanged.EndsAt.Valid)
	require.Equal(t, created.Membership.Revision, unchanged.Revision)

	second := &model.ClassMember{ClassID: secondClass.ID, UserID: user.ID, StartsAt: model.TimeFromMillis(start + 10)}
	second.PrepareCreate(model.NewClassMemberID(), model.NowUTC())
	second.AcademicPeriodID = secondClass.AcademicPeriodID
	transferAttempt := saveClassMemberAuditAttempt(t, ctx, ss, secondClass.ID.String())
	previousTransferAttempt := saveClassMemberAuditAttempt(t, ctx, ss, firstClass.ID.String())
	transferNotice := classMemberPreparedMail(t, second, model.MailTemplateAcademicClassTransferred, second.CreatedAt)
	transferCommand := &store.CommandIdempotency{UserID: user.ID, Operation: "class_member.enroll.v1",
		KeyDigest:   sha256.Sum256([]byte("class-transfer-replay")),
		Fingerprint: sha256.Sum256([]byte("class-transfer-package")),
		Retention:   time.Hour, Wait: time.Second, Batch: &store.CommandBatch{GroupDigest: sha256.Sum256([]byte("class-transfer-group"))}}
	transferred, err := ss.ClassMember().Enroll(ctx, &store.ClassMemberEnrollment{
		Member: second, ExpectedPreviousID: created.Membership.ID, ExpectedRecipientRevision: user.Revision, Notice: transferNotice,
		AuditEventID: transferAttempt.ID, PreviousAuditEventID: previousTransferAttempt.ID, AuditAt: model.GetMillis(), Command: transferCommand,
	})
	require.NoError(t, err)
	require.NotNil(t, transferred.Previous)
	require.Equal(t, created.Membership.ID, transferred.Previous.ID)
	require.Equal(t, model.MillisFromTime(second.StartsAt), transferred.Previous.EndsAt.Millis())
	require.Equal(t, int64(2), transferred.Previous.Revision)
	requireSuccessfulAudit(t, ctx, ss, transferAttempt.ID)
	requireSuccessfulAudit(t, ctx, ss, previousTransferAttempt.ID)
	destinationAudit, err := ss.Audit().Get(ctx, transferAttempt.ID)
	require.NoError(t, err)
	sourceAudit, err := ss.Audit().Get(ctx, previousTransferAttempt.ID)
	require.NoError(t, err)
	require.NotContains(t, string(destinationAudit.Result), created.Membership.ID.String())
	require.Contains(t, string(destinationAudit.Result), transferred.Membership.ID.String())
	require.NotContains(t, string(sourceAudit.Result), transferred.Membership.ID.String())
	require.Contains(t, string(sourceAudit.Result), created.Membership.ID.String())
	require.NoError(t, requireClassMemberMail(t, ctx, ss, transferNotice, model.MailTemplateAcademicClassTransferred))
	replayDestinationAttempt := saveClassMemberAuditAttempt(t, ctx, ss, secondClass.ID.String())
	replaySourceAttempt := saveClassMemberAuditAttempt(t, ctx, ss, firstClass.ID.String())
	replayInput := &store.ClassMemberEnrollment{Member: second, ExpectedPreviousID: created.Membership.ID,
		ExpectedRecipientRevision: user.Revision, AuditEventID: replayDestinationAttempt.ID,
		PreviousAuditEventID: replaySourceAttempt.ID, AuditAt: model.GetMillis(), Command: transferCommand}
	_, err = ss.ClassMember().Enroll(ctx, replayInput)
	require.NoError(t, err, "retained transfer replay")
	require.True(t, replayInput.Replayed, "retained transfer replay")
	requireSuccessfulAudit(t, ctx, ss, replayDestinationAttempt.ID)
	requireSuccessfulAudit(t, ctx, ss, replaySourceAttempt.ID)
	duplicateDestinationAttempt := saveClassMemberAuditAttempt(t, ctx, ss, secondClass.ID.String())
	duplicateSourceAttempt := saveClassMemberAuditAttempt(t, ctx, ss, firstClass.ID.String())
	duplicateCommand := *transferCommand
	duplicateCommand.KeyDigest = sha256.Sum256([]byte("class-transfer-duplicate"))
	duplicateCommand.Batch = &store.CommandBatch{GroupDigest: transferCommand.Batch.GroupDigest, DuplicateOfKeyDigest: transferCommand.KeyDigest}
	duplicateInput := &store.ClassMemberEnrollment{Member: second, ExpectedPreviousID: created.Membership.ID,
		ExpectedRecipientRevision: user.Revision, AuditEventID: duplicateDestinationAttempt.ID,
		PreviousAuditEventID: duplicateSourceAttempt.ID, AuditAt: model.GetMillis(), Command: &duplicateCommand}
	_, err = ss.ClassMember().Enroll(ctx, duplicateInput)
	require.NoError(t, err, "duplicate transfer")
	require.True(t, duplicateInput.Command.Batch.Duplicate, "duplicate transfer")
	requireClassMemberDuplicateAudit(t, ctx, ss, duplicateDestinationAttempt.ID)
	requireClassMemberDuplicateAudit(t, ctx, ss, duplicateSourceAttempt.ID)
	duplicateReplayDestination := saveClassMemberAuditAttempt(t, ctx, ss, secondClass.ID.String())
	duplicateReplaySource := saveClassMemberAuditAttempt(t, ctx, ss, firstClass.ID.String())
	duplicateReplayInput := &store.ClassMemberEnrollment{Member: second, ExpectedPreviousID: created.Membership.ID,
		ExpectedRecipientRevision: user.Revision, AuditEventID: duplicateReplayDestination.ID,
		PreviousAuditEventID: duplicateReplaySource.ID, AuditAt: model.GetMillis(), Command: &duplicateCommand}
	_, err = ss.ClassMember().Enroll(ctx, duplicateReplayInput)
	require.NoError(t, err, "retained duplicate transfer")
	require.True(t, duplicateReplayInput.Replayed, "retained duplicate transfer")
	requireClassMemberDuplicateAudit(t, ctx, ss, duplicateReplayDestination.ID)
	requireClassMemberDuplicateAudit(t, ctx, ss, duplicateReplaySource.ID)

	history, err := ss.ClassMember().ListByUser(ctx, user.ID)
	require.NoError(t, err)
	require.Len(t, history, 2)
	require.Equal(t, created.Membership.ID, history[1].ID)
	require.Equal(t, model.MillisFromTime(second.StartsAt), history[1].EndsAt.Millis())
	require.Equal(t, int64(2), history[1].Revision)

	endAttempt := saveClassMemberAuditAttempt(t, ctx, ss, secondClass.ID.String())
	endNotice := classMemberPreparedMail(t, transferred.Membership, model.MailTemplateAcademicClassEnrollmentEnded, model.TimeFromMillis(start+30))
	ended, err := ss.ClassMember().End(ctx, &store.ClassMemberEnd{
		ID: transferred.Membership.ID, ExpectedRevision: transferred.Membership.Revision, ExpectedRecipientRevision: user.Revision,
		EndAt: start + 30, Notice: endNotice, AuditEventID: endAttempt.ID, AuditAt: model.GetMillis(),
	})
	require.NoError(t, err)
	require.Equal(t, transferred.Membership.Revision+1, ended.Revision)
	require.Equal(t, start+30, ended.EndsAt.Millis())
	requireSuccessfulAudit(t, ctx, ss, endAttempt.ID)
	require.NoError(t, requireClassMemberMail(t, ctx, ss, endNotice, model.MailTemplateAcademicClassEnrollmentEnded))

	staleAttempt := saveClassMemberAuditAttempt(t, ctx, ss, secondClass.ID.String())
	_, err = ss.ClassMember().End(ctx, &store.ClassMemberEnd{
		ID: ended.ID, ExpectedRevision: transferred.Membership.Revision, ExpectedRecipientRevision: user.Revision,
		EndAt:        start + 31,
		Notice:       classMemberPreparedMail(t, transferred.Membership, model.MailTemplateAcademicClassEnrollmentEnded, model.TimeFromMillis(start+31)),
		AuditEventID: staleAttempt.ID, AuditAt: model.GetMillis(),
	})
	require.True(t, store.IsConflict(err), "stale End() error")

	rollbackUser := saveUser(t, ctx, ss)
	_, err = ss.Affiliation().Save(ctx, &model.Affiliation{
		UserID: rollbackUser.ID, Kind: model.AffiliationStudent, StartsAt: model.TimeFromMillis(start - 1),
	})
	require.NoError(t, err)
	rolledBack := &model.ClassMember{ClassID: firstClass.ID, UserID: rollbackUser.ID, StartsAt: model.TimeFromMillis(start)}
	rolledBack.PrepareCreate(model.NewClassMemberID(), model.NowUTC())
	rolledBack.AcademicPeriodID = firstClass.AcademicPeriodID
	_, err = ss.ClassMember().Enroll(ctx, &store.ClassMemberEnrollment{
		Member: rolledBack, ExpectedRecipientRevision: rollbackUser.Revision,
		Notice:       classMemberPreparedMail(t, rolledBack, model.MailTemplateAcademicClassEnrolled, rolledBack.CreatedAt),
		AuditEventID: model.NewAuditEventID(), AuditAt: model.GetMillis(),
	})
	require.Error(t, err, "Enroll() succeeded without its audit attempt")
	_, err = ss.ClassMember().Get(ctx, rolledBack.ID)
	require.True(t, store.IsNotFound(err), "enrollment survived audit rollback")

	disabledMailUser := saveUser(t, ctx, ss)
	_, err = ss.Affiliation().Save(ctx, &model.Affiliation{
		UserID: disabledMailUser.ID, Kind: model.AffiliationStudent, StartsAt: model.TimeFromMillis(start - 1),
	})
	require.NoError(t, err)
	disabledMailMember := &model.ClassMember{ClassID: firstClass.ID, AcademicPeriodID: firstClass.AcademicPeriodID,
		UserID: disabledMailUser.ID, StartsAt: model.TimeFromMillis(start)}
	disabledMailMember.PrepareCreate(model.NewClassMemberID(), model.NowUTC())
	disabledNotice := classMemberPreparedMail(t, disabledMailMember, model.MailTemplateAcademicClassEnrolled, disabledMailMember.CreatedAt)
	disabledNotice = suppressClassMemberMail(t, disabledNotice, model.MailDeliveryDisabledCode)
	_, err = ss.ClassMember().Enroll(ctx, &store.ClassMemberEnrollment{
		Member: disabledMailMember, ExpectedRecipientRevision: disabledMailUser.Revision, Notice: disabledNotice,
		AuditEventID: saveClassMemberAuditAttempt(t, ctx, ss, firstClass.ID.String()).ID, AuditAt: model.GetMillis(),
	})
	require.NoError(t, err)
	disabledDelivery, err := ss.Mail().GetDelivery(ctx, disabledNotice.Delivery.ID)
	require.NoError(t, err)
	require.Equal(t, model.MailDeliverySuppressed, disabledDelivery.State)
	require.Equal(t, model.MailDeliveryDisabledCode, disabledDelivery.PublicFailureCode)
	require.Empty(t, disabledDelivery.EncryptedPayload)

	ineligibleUser := saveUser(t, ctx, ss)
	_, err = ss.Affiliation().Save(ctx, &model.Affiliation{UserID: ineligibleUser.ID, Kind: model.AffiliationStudent,
		StartsAt: model.TimeFromMillis(start - 1)})
	require.NoError(t, err)
	activeRecipientRevision := ineligibleUser.Revision
	staleQueuedMember := &model.ClassMember{ClassID: firstClass.ID, AcademicPeriodID: firstClass.AcademicPeriodID,
		UserID: ineligibleUser.ID, StartsAt: model.TimeFromMillis(start)}
	staleQueuedMember.PrepareCreate(model.NewClassMemberID(), model.NowUTC())
	staleQueuedNotice := classMemberPreparedMail(t, staleQueuedMember, model.MailTemplateAcademicClassEnrolled, staleQueuedMember.CreatedAt)
	disableAt := model.GetMillis()
	disabledResult, err := ss.User().SetDisabled(ctx, userDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
		ID: ineligibleUser.ID, ExpectedRevision: ineligibleUser.Revision, Disabled: true,
		ChangedAt: disableAt, RevocationReason: model.SessionRevocationAccountDisabled, AuditEventID: saveUserProfileAuditAttempt(t, ctx, ss, ineligibleUser.ID.String()).ID, AuditAt: disableAt,
	}))
	require.NoError(t, err)
	ineligibleUser = disabledResult.User
	_, err = ss.ClassMember().Enroll(ctx, &store.ClassMemberEnrollment{
		Member: staleQueuedMember, ExpectedRecipientRevision: activeRecipientRevision, Notice: staleQueuedNotice,
		AuditEventID: saveClassMemberAuditAttempt(t, ctx, ss, firstClass.ID.String()).ID, AuditAt: model.GetMillis(),
	})
	require.True(t, store.IsConflict(err), "queued notice raced recipient disable error")
	_, err = ss.ClassMember().Get(ctx, staleQueuedMember.ID)
	require.True(t, store.IsNotFound(err), "recipient-disable race committed membership")
	ineligibleMember := &model.ClassMember{ClassID: firstClass.ID, AcademicPeriodID: firstClass.AcademicPeriodID,
		UserID: ineligibleUser.ID, StartsAt: model.TimeFromMillis(start)}
	ineligibleMember.PrepareCreate(model.NewClassMemberID(), model.NowUTC())
	ineligibleNotice := classMemberPreparedMail(t, ineligibleMember, model.MailTemplateAcademicClassEnrolled, ineligibleMember.CreatedAt)
	ineligibleNotice = suppressClassMemberMail(t, ineligibleNotice, model.MailDeliveryRecipientIneligibleCode)
	_, err = ss.ClassMember().Enroll(ctx, &store.ClassMemberEnrollment{
		Member: ineligibleMember, ExpectedRecipientRevision: ineligibleUser.Revision, Notice: ineligibleNotice,
		AuditEventID: saveClassMemberAuditAttempt(t, ctx, ss, firstClass.ID.String()).ID, AuditAt: model.GetMillis(),
	})
	require.NoError(t, err)
	ineligibleDelivery, err := ss.Mail().GetDelivery(ctx, ineligibleNotice.Delivery.ID)
	require.NoError(t, err)
	require.Equal(t, model.MailDeliverySuppressed, ineligibleDelivery.State)
	require.Equal(t, model.MailDeliveryRecipientIneligibleCode, ineligibleDelivery.PublicFailureCode)
	require.Empty(t, ineligibleDelivery.EncryptedPayload)
	disabledRecipientRevision := ineligibleUser.Revision
	staleEndAt := start + 100
	staleEndNotice := classMemberPreparedMail(t, ineligibleMember, model.MailTemplateAcademicClassEnrollmentEnded, model.TimeFromMillis(staleEndAt))
	staleEndNotice = suppressClassMemberMail(t, staleEndNotice, model.MailDeliveryRecipientIneligibleCode)
	enabledAt := model.GetMillis() + 1
	enabledResult, err := ss.User().SetDisabled(ctx, userDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
		ID: ineligibleUser.ID, ExpectedRevision: disabledRecipientRevision,
		ChangedAt: enabledAt, AuditEventID: saveUserProfileAuditAttempt(t, ctx, ss, ineligibleUser.ID.String()).ID, AuditAt: enabledAt,
	}))
	require.NoError(t, err)
	_, err = ss.ClassMember().End(ctx, &store.ClassMemberEnd{
		ID: ineligibleMember.ID, ExpectedRevision: ineligibleMember.Revision,
		ExpectedRecipientRevision: disabledRecipientRevision, EndAt: staleEndAt, Notice: staleEndNotice,
		AuditEventID: saveClassMemberAuditAttempt(t, ctx, ss, firstClass.ID.String()).ID, AuditAt: model.GetMillis(),
	})
	require.True(t, store.IsConflict(err), "ineligible suppression raced recipient enable error")
	require.NotEqual(t, disabledRecipientRevision, enabledResult.User.Revision, "recipient enable did not advance User revision")
	unchangedAfterEnable, err := ss.ClassMember().Get(ctx, ineligibleMember.ID)
	require.NoError(t, err)
	require.False(t, unchangedAfterEnable.EndsAt.Valid)
}

func classMemberPreparedMail(t *testing.T, member *model.ClassMember, key model.MailTemplateKey, at time.Time) *store.PreparedMail {
	t.Helper()
	occurrenceID, deliveryID := model.NewMailOccurrenceID(), model.NewMailDeliveryID()
	command, err := model.EncodeMailDeliveryCommand(model.MailDeliveryCommand{DeliveryID: deliveryID})
	require.NoError(t, err)
	job, err := model.NewJob(model.NewJobID(), model.JobTypeMailDeliver, command, deliveryID.String(), at, at, model.MailMaximumAttempts)
	require.NoError(t, err)
	occurrence := &model.MailOccurrence{ID: occurrenceID, Kind: model.MailOccurrenceAcademicAdministration,
		TemplateKey: key, ActorUserID: member.UserID, CreatedAt: at}
	delivery := &model.MailDelivery{ID: deliveryID, OccurrenceID: occurrenceID, JobID: job.ID, TargetUserID: member.UserID,
		TemplateKey: key, TemplateDigest: strings.Repeat("a", 64), MaskedRecipient: "s***@example.edu",
		State: model.MailDeliveryQueued, CreatedAt: at, UpdatedAt: at, MessageDate: at, Deadline: at.Add(72 * time.Hour),
		MessageID:        "<class-transition." + deliveryID.String() + "@example.test>",
		EncryptedPayload: json.RawMessage(`{"version":1,"key_id":"11111111111111111111111111111111","ciphertext":"class-transition"}`), Revision: 1}
	require.NoError(t, occurrence.Validate())
	require.NoError(t, delivery.Validate())
	return &store.PreparedMail{Occurrence: occurrence, Delivery: delivery, Job: job}
}

func suppressClassMemberMail(t *testing.T, prepared *store.PreparedMail, publicCode string) *store.PreparedMail {
	t.Helper()
	job, err := prepared.Job.RequestCancellation(prepared.Occurrence.CreatedAt)
	require.NoError(t, err)
	delivery := prepared.Delivery.Clone()
	delivery.State = model.MailDeliverySuppressed
	delivery.PublicFailureCode = publicCode
	delivery.EncryptedPayload = nil
	require.NoError(t, delivery.Validate())
	return &store.PreparedMail{Occurrence: prepared.Occurrence, Delivery: delivery, Job: job}
}

func requireClassMemberMail(t *testing.T, ctx context.Context, ss store.Store, notice *store.PreparedMail, key model.MailTemplateKey) error {
	t.Helper()
	delivery, err := ss.Mail().GetDelivery(ctx, notice.Delivery.ID)
	if err != nil {
		return err
	}
	require.Equal(t, key, delivery.TemplateKey)
	require.Equal(t, notice.Delivery.TargetUserID, delivery.TargetUserID)
	return nil
}

func testDistinctPeriodClassMemberEnrollment(t *testing.T, ss store.Store, firstClass, nextClass *model.Class, start int64) {
	t.Helper()
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	_, err := ss.Affiliation().Save(ctx, &model.Affiliation{UserID: user.ID, Kind: model.AffiliationStudent, StartsAt: model.TimeFromMillis(start - 1)})
	require.NoError(t, err)
	first, err := ss.ClassMember().SeedEnroll(ctx, &model.ClassMember{ClassID: firstClass.ID, UserID: user.ID, StartsAt: model.TimeFromMillis(start)})
	require.NoError(t, err)
	next, err := ss.ClassMember().SeedEnroll(ctx, &model.ClassMember{ClassID: nextClass.ID, UserID: user.ID, StartsAt: model.TimeFromMillis(start)})
	require.NoError(t, err)
	require.Nil(t, next.Previous)
	active, err := ss.ClassMember().ListActiveByUser(ctx, user.ID, start+1)
	require.NoError(t, err)
	require.Len(t, active, 2)
	require.NotEqual(t, next.Membership.AcademicPeriodID, first.Membership.AcademicPeriodID)
}

func testFiniteAffiliationCannotBackOpenEnrollment(t *testing.T, ss store.Store, class *model.Class, start int64) {
	t.Helper()
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	_, err := ss.Affiliation().Save(ctx, &model.Affiliation{
		UserID: user.ID, Kind: model.AffiliationStudent,
		StartsAt: model.TimeFromMillis(start - 1), EndsAt: model.OptionalTimeFromMillis(start + 100),
	})
	require.NoError(t, err)
	_, err = ss.ClassMember().SeedEnroll(ctx, &model.ClassMember{ClassID: class.ID, UserID: user.ID, StartsAt: model.TimeFromMillis(start)})
	require.True(t, store.IsConflict(err), "open enrollment with finite affiliation error")
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
		UserID: user.ID, Kind: model.AffiliationStudent, StartsAt: model.TimeFromMillis(start - 1),
	})
	require.NoError(t, err)
	members := []*model.ClassMember{
		{ClassID: firstClass.ID, UserID: user.ID, StartsAt: model.TimeFromMillis(start)},
		{ClassID: secondClass.ID, UserID: user.ID, StartsAt: model.TimeFromMillis(start)},
	}
	attempts := make([]*model.AuditEvent, len(members))
	notices := make([]*store.PreparedMail, len(members))
	for i, member := range members {
		member.PrepareCreate(model.NewClassMemberID(), model.NowUTC())
		if i == 0 {
			member.AcademicPeriodID = firstClass.AcademicPeriodID
		} else {
			member.AcademicPeriodID = secondClass.AcademicPeriodID
		}
		attempts[i] = saveClassMemberAuditAttempt(t, ctx, ss, member.ClassID.String())
		notices[i] = classMemberPreparedMail(t, member, model.MailTemplateAcademicClassEnrolled, member.CreatedAt)
	}
	errs := make([]error, len(members))
	var wg sync.WaitGroup
	for i := range members {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			_, errs[index] = ss.ClassMember().Enroll(ctx, &store.ClassMemberEnrollment{
				Member: members[index], ExpectedRecipientRevision: user.Revision, Notice: notices[index],
				AuditEventID: attempts[index].ID, AuditAt: model.GetMillis(),
			})
		}(i)
	}
	wg.Wait()
	successes := 0
	for _, err := range errs {
		if err == nil {
			successes++
			continue
		}
		require.True(t, store.IsConflict(err), "concurrent enrollment error = %v", err)
	}
	require.Equal(t, 1, successes, "concurrent enrollment successes = %d, errors = %v", successes, errs)
	active, err := ss.ClassMember().ListActiveByUser(ctx, user.ID, start+1)
	require.NoError(t, err)
	require.Len(t, active, 1)
	completedAudits := 0
	for _, attempt := range attempts {
		event, err := ss.Audit().Get(ctx, attempt.ID)
		require.NoError(t, err)
		if event.Status == model.AuditStatusSuccess {
			completedAudits++
			continue
		}
		require.Equal(t, model.AuditStatusAttempt, event.Status, "concurrent enrollment audit")
	}
	require.Equal(t, 1, completedAudits, "completed concurrent enrollment audits")
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
		Resource:  model.Resource{Type: model.ResourceClass, ID: classID},
		ScopeType: model.RoleScopeClass, ScopeID: classID,
		Status: model.AuditStatusAttempt, NodeID: "test-node",
	})
	require.NoError(t, err)
	return attempt
}

func requireSuccessfulAudit(t *testing.T, ctx context.Context, ss store.Store, id model.AuditEventID) {
	t.Helper()
	event, err := ss.Audit().Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, model.AuditStatusSuccess, event.Status, "audit %s status", id)
}

func requireClassMemberDuplicateAudit(t *testing.T, ctx context.Context, ss store.Store, id model.AuditEventID) {
	t.Helper()
	audit, err := ss.Audit().Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, model.AuditStatusFail, audit.Status, "duplicate audit %s = status %q code", id, audit.Status)
	require.Equal(t, "onboarding_batch.duplicate", audit.ErrorCode, "duplicate audit %s = status %q code", id, audit.Status)
}
