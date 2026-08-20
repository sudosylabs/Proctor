// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestClassMemberStore(t *testing.T, ss store.Store) {
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
	requireNoError(t, err)

	first, err := ss.ClassMember().Enroll(ctx, &model.ClassMember{
		ClassID:          firstClass.ID,
		AcademicPeriodID: model.NewAcademicPeriodID(),
		UserID:           user.ID,
		StartsAt:         model.TimeFromMillis(start),
	})
	requireNoError(t, err)
	if first.Previous != nil || first.Membership.AcademicPeriodID != fixture.period.ID {
		t.Fatalf("first Enroll() = %#v", first)
	}
	if first.Membership.Revision != 1 {
		t.Fatalf("first enrollment revision = %d, want 1", first.Membership.Revision)
	}
	_, err = ss.ClassMember().Enroll(ctx, &model.ClassMember{
		ClassID: firstClass.ID, UserID: user.ID, StartsAt: model.TimeFromMillis(start + 1),
	})
	var conflict *store.ErrConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("duplicate enrollment error = %v", err)
	}
	transfer, err := ss.ClassMember().Enroll(ctx, &model.ClassMember{
		ClassID: secondClass.ID, UserID: user.ID, StartsAt: model.TimeFromMillis(start + 10),
	})
	requireNoError(t, err)
	if transfer.Previous == nil ||
		transfer.Previous.ID != first.Membership.ID ||
		transfer.Previous.EndsAt.Millis() != start+10 ||
		transfer.Previous.Revision != first.Membership.Revision+1 {
		t.Fatalf("transfer Enroll() = %#v", transfer)
	}
	active, err := ss.ClassMember().ListActiveByUser(ctx, user.ID.String(), start+11)
	requireNoError(t, err)
	if len(active) != 1 || active[0].ID != transfer.Membership.ID {
		t.Fatalf("ListActiveByUser() = %#v", active)
	}
	history, err := ss.ClassMember().ListByUser(ctx, user.ID.String())
	requireNoError(t, err)
	if len(history) != 2 {
		t.Fatalf("ListByUser() = %#v", history)
	}
	ended, err := ss.ClassMember().End(ctx, transfer.Membership.ID.String(), transfer.Membership.Revision, start+20)
	requireNoError(t, err)
	if ended.EndsAt.Millis() != start+20 {
		t.Fatalf("End() = %#v", ended)
	}
	if _, err := ss.ClassMember().End(ctx, transfer.Membership.ID.String(), transfer.Membership.Revision, start+21); !store.IsConflict(err) {
		t.Fatalf("stale End() error = %v", err)
	}
	_, err = ss.ClassMember().Enroll(ctx, &model.ClassMember{
		ClassID: firstClass.ID, UserID: user.ID, StartsAt: model.TimeFromMillis(start + 5),
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
		UserID: user.ID, Kind: model.AffiliationStudent, StartsAt: model.TimeFromMillis(start - 1),
	})
	requireNoError(t, err)

	first := &model.ClassMember{ClassID: firstClass.ID, UserID: user.ID, StartsAt: model.TimeFromMillis(start)}
	first.PrepareCreate(model.NewClassMemberID(), model.NowUTC())
	// Period is authoritative from the class row during enroll.
	first.AcademicPeriodID = firstClass.AcademicPeriodID
	createAttempt := saveClassMemberAuditAttempt(t, ctx, ss, firstClass.ID.String())
	createNotice := classMemberPreparedMail(t, first, model.MailTemplateAcademicClassEnrolled, first.CreatedAt)
	created, err := ss.ClassMember().EnrollWithAudit(ctx, &store.ClassMemberEnrollment{
		Member: first, ExpectedRecipientRevision: user.Revision,
		Notice: createNotice, AuditEventID: createAttempt.ID.String(), AuditAt: model.GetMillis(),
	})
	requireNoError(t, err)
	requireSuccessfulAudit(t, ctx, ss, createAttempt.ID.String())
	requireNoError(t, requireClassMemberMail(t, ctx, ss, createNotice, model.MailTemplateAcademicClassEnrolled))

	rolledBackTransfer := &model.ClassMember{ClassID: secondClass.ID, UserID: user.ID, StartsAt: model.TimeFromMillis(start + 5)}
	rolledBackTransfer.PrepareCreate(model.NewClassMemberID(), model.NowUTC())
	rolledBackTransfer.AcademicPeriodID = secondClass.AcademicPeriodID
	if _, err := ss.ClassMember().EnrollWithAudit(ctx, &store.ClassMemberEnrollment{
		Member: rolledBackTransfer, ExpectedPreviousID: created.Membership.ID, ExpectedRecipientRevision: user.Revision,
		Notice:               classMemberPreparedMail(t, rolledBackTransfer, model.MailTemplateAcademicClassTransferred, rolledBackTransfer.CreatedAt),
		AuditEventID:         model.NewId(),
		PreviousAuditEventID: model.NewId(), AuditAt: model.GetMillis(),
	}); err == nil {
		t.Fatal("transfer succeeded without its audit attempt")
	}
	unchanged, err := ss.ClassMember().Get(ctx, created.Membership.ID.String())
	requireNoError(t, err)
	if unchanged.EndsAt.Valid || unchanged.Revision != created.Membership.Revision {
		t.Fatalf("prior enrollment close survived transfer audit rollback: %#v", unchanged)
	}

	second := &model.ClassMember{ClassID: secondClass.ID, UserID: user.ID, StartsAt: model.TimeFromMillis(start + 10)}
	second.PrepareCreate(model.NewClassMemberID(), model.NowUTC())
	second.AcademicPeriodID = secondClass.AcademicPeriodID
	transferAttempt := saveClassMemberAuditAttempt(t, ctx, ss, secondClass.ID.String())
	previousTransferAttempt := saveClassMemberAuditAttempt(t, ctx, ss, firstClass.ID.String())
	transferNotice := classMemberPreparedMail(t, second, model.MailTemplateAcademicClassTransferred, second.CreatedAt)
	transferred, err := ss.ClassMember().EnrollWithAudit(ctx, &store.ClassMemberEnrollment{
		Member: second, ExpectedPreviousID: created.Membership.ID, ExpectedRecipientRevision: user.Revision, Notice: transferNotice,
		AuditEventID: transferAttempt.ID.String(), PreviousAuditEventID: previousTransferAttempt.ID.String(), AuditAt: model.GetMillis(),
	})
	requireNoError(t, err)
	if transferred.Previous == nil || transferred.Previous.ID != created.Membership.ID ||
		transferred.Previous.EndsAt.Millis() != model.MillisFromTime(second.StartsAt) || transferred.Previous.Revision != 2 {
		t.Fatalf("audited transfer = %#v", transferred)
	}
	requireSuccessfulAudit(t, ctx, ss, transferAttempt.ID.String())
	requireSuccessfulAudit(t, ctx, ss, previousTransferAttempt.ID.String())
	destinationAudit, err := ss.Audit().Get(ctx, transferAttempt.ID.String())
	requireNoError(t, err)
	sourceAudit, err := ss.Audit().Get(ctx, previousTransferAttempt.ID.String())
	requireNoError(t, err)
	if strings.Contains(string(destinationAudit.Result), created.Membership.ID.String()) ||
		!strings.Contains(string(destinationAudit.Result), transferred.Membership.ID.String()) {
		t.Fatalf("destination transfer audit crossed into source membership: %s", destinationAudit.Result)
	}
	if strings.Contains(string(sourceAudit.Result), transferred.Membership.ID.String()) ||
		!strings.Contains(string(sourceAudit.Result), created.Membership.ID.String()) {
		t.Fatalf("source transfer audit crossed into destination membership: %s", sourceAudit.Result)
	}
	requireNoError(t, requireClassMemberMail(t, ctx, ss, transferNotice, model.MailTemplateAcademicClassTransferred))

	history, err := ss.ClassMember().ListByUser(ctx, user.ID.String())
	requireNoError(t, err)
	if len(history) != 2 || history[1].ID != created.Membership.ID ||
		history[1].EndsAt.Millis() != model.MillisFromTime(second.StartsAt) || history[1].Revision != 2 {
		t.Fatalf("audited enrollment history = %#v", history)
	}

	endAttempt := saveClassMemberAuditAttempt(t, ctx, ss, secondClass.ID.String())
	endNotice := classMemberPreparedMail(t, transferred.Membership, model.MailTemplateAcademicClassEnrollmentEnded, model.TimeFromMillis(start+30))
	ended, err := ss.ClassMember().EndWithAudit(ctx, &store.ClassMemberEnd{
		ID: transferred.Membership.ID.String(), ExpectedRevision: transferred.Membership.Revision, ExpectedRecipientRevision: user.Revision,
		EndAt: start + 30, Notice: endNotice, AuditEventID: endAttempt.ID.String(), AuditAt: model.GetMillis(),
	})
	requireNoError(t, err)
	if ended.Revision != transferred.Membership.Revision+1 || ended.EndsAt.Millis() != start+30 {
		t.Fatalf("EndWithAudit() = %#v", ended)
	}
	requireSuccessfulAudit(t, ctx, ss, endAttempt.ID.String())
	requireNoError(t, requireClassMemberMail(t, ctx, ss, endNotice, model.MailTemplateAcademicClassEnrollmentEnded))

	staleAttempt := saveClassMemberAuditAttempt(t, ctx, ss, secondClass.ID.String())
	if _, err := ss.ClassMember().EndWithAudit(ctx, &store.ClassMemberEnd{
		ID: ended.ID.String(), ExpectedRevision: transferred.Membership.Revision, ExpectedRecipientRevision: user.Revision,
		EndAt:        start + 31,
		Notice:       classMemberPreparedMail(t, transferred.Membership, model.MailTemplateAcademicClassEnrollmentEnded, model.TimeFromMillis(start+31)),
		AuditEventID: staleAttempt.ID.String(), AuditAt: model.GetMillis(),
	}); !store.IsConflict(err) {
		t.Fatalf("stale EndWithAudit() error = %v", err)
	}

	rollbackUser := saveUser(t, ctx, ss)
	_, err = ss.Affiliation().Save(ctx, &model.Affiliation{
		UserID: rollbackUser.ID, Kind: model.AffiliationStudent, StartsAt: model.TimeFromMillis(start - 1),
	})
	requireNoError(t, err)
	rolledBack := &model.ClassMember{ClassID: firstClass.ID, UserID: rollbackUser.ID, StartsAt: model.TimeFromMillis(start)}
	rolledBack.PrepareCreate(model.NewClassMemberID(), model.NowUTC())
	rolledBack.AcademicPeriodID = firstClass.AcademicPeriodID
	if _, err := ss.ClassMember().EnrollWithAudit(ctx, &store.ClassMemberEnrollment{
		Member: rolledBack, ExpectedRecipientRevision: rollbackUser.Revision,
		Notice:       classMemberPreparedMail(t, rolledBack, model.MailTemplateAcademicClassEnrolled, rolledBack.CreatedAt),
		AuditEventID: model.NewId(), AuditAt: model.GetMillis(),
	}); err == nil {
		t.Fatal("EnrollWithAudit() succeeded without its audit attempt")
	}
	if _, err := ss.ClassMember().Get(ctx, rolledBack.ID.String()); !store.IsNotFound(err) {
		t.Fatalf("enrollment survived audit rollback: %v", err)
	}

	disabledMailUser := saveUser(t, ctx, ss)
	_, err = ss.Affiliation().Save(ctx, &model.Affiliation{
		UserID: disabledMailUser.ID, Kind: model.AffiliationStudent, StartsAt: model.TimeFromMillis(start - 1),
	})
	requireNoError(t, err)
	disabledMailMember := &model.ClassMember{ClassID: firstClass.ID, AcademicPeriodID: firstClass.AcademicPeriodID,
		UserID: disabledMailUser.ID, StartsAt: model.TimeFromMillis(start)}
	disabledMailMember.PrepareCreate(model.NewClassMemberID(), model.NowUTC())
	disabledNotice := classMemberPreparedMail(t, disabledMailMember, model.MailTemplateAcademicClassEnrolled, disabledMailMember.CreatedAt)
	disabledNotice = suppressClassMemberMail(t, disabledNotice, model.MailDeliveryDisabledCode)
	_, err = ss.ClassMember().EnrollWithAudit(ctx, &store.ClassMemberEnrollment{
		Member: disabledMailMember, ExpectedRecipientRevision: disabledMailUser.Revision, Notice: disabledNotice,
		AuditEventID: saveClassMemberAuditAttempt(t, ctx, ss, firstClass.ID.String()).ID.String(), AuditAt: model.GetMillis(),
	})
	requireNoError(t, err)
	disabledDelivery, err := ss.Mail().GetDelivery(ctx, disabledNotice.Delivery.ID)
	requireNoError(t, err)
	if disabledDelivery.State != model.MailDeliverySuppressed ||
		disabledDelivery.PublicFailureCode != model.MailDeliveryDisabledCode || len(disabledDelivery.EncryptedPayload) != 0 {
		t.Fatalf("disabled Class transition delivery = %#v", disabledDelivery)
	}

	ineligibleUser := saveUser(t, ctx, ss)
	_, err = ss.Affiliation().Save(ctx, &model.Affiliation{UserID: ineligibleUser.ID, Kind: model.AffiliationStudent,
		StartsAt: model.TimeFromMillis(start - 1)})
	requireNoError(t, err)
	activeRecipientRevision := ineligibleUser.Revision
	staleQueuedMember := &model.ClassMember{ClassID: firstClass.ID, AcademicPeriodID: firstClass.AcademicPeriodID,
		UserID: ineligibleUser.ID, StartsAt: model.TimeFromMillis(start)}
	staleQueuedMember.PrepareCreate(model.NewClassMemberID(), model.NowUTC())
	staleQueuedNotice := classMemberPreparedMail(t, staleQueuedMember, model.MailTemplateAcademicClassEnrolled, staleQueuedMember.CreatedAt)
	disableAt := model.GetMillis()
	disabledResult, err := ss.User().SetDisabledWithAudit(ctx, userDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
		ID: ineligibleUser.ID.String(), ExpectedRevision: ineligibleUser.Revision, Disabled: true,
		ChangedAt: disableAt, RevocationReason: "recipient ineligible", AuditEventID: saveUserProfileAuditAttempt(t, ctx, ss, ineligibleUser.ID.String()).ID.String(), AuditAt: disableAt,
	}))
	requireNoError(t, err)
	ineligibleUser = disabledResult.User
	if _, err = ss.ClassMember().EnrollWithAudit(ctx, &store.ClassMemberEnrollment{
		Member: staleQueuedMember, ExpectedRecipientRevision: activeRecipientRevision, Notice: staleQueuedNotice,
		AuditEventID: saveClassMemberAuditAttempt(t, ctx, ss, firstClass.ID.String()).ID.String(), AuditAt: model.GetMillis(),
	}); !store.IsConflict(err) {
		t.Fatalf("queued notice raced recipient disable error=%v", err)
	}
	if _, err = ss.ClassMember().Get(ctx, staleQueuedMember.ID.String()); !store.IsNotFound(err) {
		t.Fatalf("recipient-disable race committed membership: %v", err)
	}
	ineligibleMember := &model.ClassMember{ClassID: firstClass.ID, AcademicPeriodID: firstClass.AcademicPeriodID,
		UserID: ineligibleUser.ID, StartsAt: model.TimeFromMillis(start)}
	ineligibleMember.PrepareCreate(model.NewClassMemberID(), model.NowUTC())
	ineligibleNotice := classMemberPreparedMail(t, ineligibleMember, model.MailTemplateAcademicClassEnrolled, ineligibleMember.CreatedAt)
	ineligibleNotice = suppressClassMemberMail(t, ineligibleNotice, model.MailDeliveryRecipientIneligibleCode)
	_, err = ss.ClassMember().EnrollWithAudit(ctx, &store.ClassMemberEnrollment{
		Member: ineligibleMember, ExpectedRecipientRevision: ineligibleUser.Revision, Notice: ineligibleNotice,
		AuditEventID: saveClassMemberAuditAttempt(t, ctx, ss, firstClass.ID.String()).ID.String(), AuditAt: model.GetMillis(),
	})
	requireNoError(t, err)
	ineligibleDelivery, err := ss.Mail().GetDelivery(ctx, ineligibleNotice.Delivery.ID)
	requireNoError(t, err)
	if ineligibleDelivery.State != model.MailDeliverySuppressed ||
		ineligibleDelivery.PublicFailureCode != model.MailDeliveryRecipientIneligibleCode || len(ineligibleDelivery.EncryptedPayload) != 0 {
		t.Fatalf("ineligible-recipient Class transition delivery = %#v", ineligibleDelivery)
	}
	disabledRecipientRevision := ineligibleUser.Revision
	staleEndAt := start + 100
	staleEndNotice := classMemberPreparedMail(t, ineligibleMember, model.MailTemplateAcademicClassEnrollmentEnded, model.TimeFromMillis(staleEndAt))
	staleEndNotice = suppressClassMemberMail(t, staleEndNotice, model.MailDeliveryRecipientIneligibleCode)
	enabledAt := model.GetMillis() + 1
	enabledResult, err := ss.User().SetDisabledWithAudit(ctx, userDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
		ID: ineligibleUser.ID.String(), ExpectedRevision: disabledRecipientRevision,
		ChangedAt: enabledAt, AuditEventID: saveUserProfileAuditAttempt(t, ctx, ss, ineligibleUser.ID.String()).ID.String(), AuditAt: enabledAt,
	}))
	requireNoError(t, err)
	if _, err = ss.ClassMember().EndWithAudit(ctx, &store.ClassMemberEnd{
		ID: ineligibleMember.ID.String(), ExpectedRevision: ineligibleMember.Revision,
		ExpectedRecipientRevision: disabledRecipientRevision, EndAt: staleEndAt, Notice: staleEndNotice,
		AuditEventID: saveClassMemberAuditAttempt(t, ctx, ss, firstClass.ID.String()).ID.String(), AuditAt: model.GetMillis(),
	}); !store.IsConflict(err) {
		t.Fatalf("ineligible suppression raced recipient enable error=%v", err)
	}
	if enabledResult.User.Revision == disabledRecipientRevision {
		t.Fatal("recipient enable did not advance User revision")
	}
	unchangedAfterEnable, err := ss.ClassMember().Get(ctx, ineligibleMember.ID.String())
	requireNoError(t, err)
	if unchangedAfterEnable.EndsAt.Valid {
		t.Fatalf("recipient-enable race ended membership: %#v", unchangedAfterEnable)
	}
}

func classMemberPreparedMail(t *testing.T, member *model.ClassMember, key model.MailTemplateKey, at time.Time) *store.PreparedMail {
	t.Helper()
	occurrenceID, deliveryID := model.NewMailOccurrenceID(), model.NewMailDeliveryID()
	command, err := model.EncodeMailDeliveryCommand(model.MailDeliveryCommandV1{DeliveryID: deliveryID})
	requireNoError(t, err)
	job, err := model.NewJob(model.NewJobID(), model.JobTypeMailDeliver, 1, command, deliveryID.String(), at, at, model.MailMaximumAttempts)
	requireNoError(t, err)
	occurrence := &model.MailOccurrence{ID: occurrenceID, Kind: model.MailOccurrenceAcademicAdministration,
		TemplateKey: key, ActorUserID: member.UserID, CreatedAt: at}
	delivery := &model.MailDelivery{ID: deliveryID, OccurrenceID: occurrenceID, JobID: job.ID, TargetUserID: member.UserID,
		TemplateKey: key, TemplateDigest: strings.Repeat("a", 64), MaskedRecipient: "s***@example.edu",
		State: model.MailDeliveryQueued, CreatedAt: at, UpdatedAt: at, MessageDate: at, Deadline: at.Add(72 * time.Hour),
		MessageID:        "<class-transition." + deliveryID.String() + "@example.test>",
		EncryptedPayload: json.RawMessage(`{"version":1,"key_id":"11111111111111111111111111111111","ciphertext":"class-transition"}`), Revision: 1}
	requireNoError(t, occurrence.Validate())
	requireNoError(t, delivery.Validate())
	return &store.PreparedMail{Occurrence: occurrence, Delivery: delivery, Job: job}
}

func suppressClassMemberMail(t *testing.T, prepared *store.PreparedMail, publicCode string) *store.PreparedMail {
	t.Helper()
	job, err := prepared.Job.RequestCancellation(prepared.Occurrence.CreatedAt)
	requireNoError(t, err)
	delivery := prepared.Delivery.Clone()
	delivery.State = model.MailDeliverySuppressed
	delivery.PublicFailureCode = publicCode
	delivery.EncryptedPayload = nil
	requireNoError(t, delivery.Validate())
	return &store.PreparedMail{Occurrence: prepared.Occurrence, Delivery: delivery, Job: job}
}

func requireClassMemberMail(t *testing.T, ctx context.Context, ss store.Store, notice *store.PreparedMail, key model.MailTemplateKey) error {
	t.Helper()
	delivery, err := ss.Mail().GetDelivery(ctx, notice.Delivery.ID)
	if err != nil {
		return err
	}
	if delivery.TemplateKey != key || delivery.TargetUserID != notice.Delivery.TargetUserID {
		t.Fatalf("Class transition delivery = %#v", delivery)
	}
	return nil
}

func testDistinctPeriodClassMemberEnrollment(t *testing.T, ss store.Store, firstClass, nextClass *model.Class, start int64) {
	t.Helper()
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	_, err := ss.Affiliation().Save(ctx, &model.Affiliation{UserID: user.ID, Kind: model.AffiliationStudent, StartsAt: model.TimeFromMillis(start - 1)})
	requireNoError(t, err)
	first, err := ss.ClassMember().Enroll(ctx, &model.ClassMember{ClassID: firstClass.ID, UserID: user.ID, StartsAt: model.TimeFromMillis(start)})
	requireNoError(t, err)
	next, err := ss.ClassMember().Enroll(ctx, &model.ClassMember{ClassID: nextClass.ID, UserID: user.ID, StartsAt: model.TimeFromMillis(start)})
	requireNoError(t, err)
	if next.Previous != nil {
		t.Fatalf("distinct-period enrollment replaced prior membership: %#v", next)
	}
	active, err := ss.ClassMember().ListActiveByUser(ctx, user.ID.String(), start+1)
	requireNoError(t, err)
	if len(active) != 2 || first.Membership.AcademicPeriodID == next.Membership.AcademicPeriodID {
		t.Fatalf("distinct-period active enrollments = %#v", active)
	}
}

func testFiniteAffiliationCannotBackOpenEnrollment(t *testing.T, ss store.Store, class *model.Class, start int64) {
	t.Helper()
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	_, err := ss.Affiliation().Save(ctx, &model.Affiliation{
		UserID: user.ID, Kind: model.AffiliationStudent,
		StartsAt: model.TimeFromMillis(start - 1), EndsAt: model.OptionalTimeFromMillis(start + 100),
	})
	requireNoError(t, err)
	_, err = ss.ClassMember().Enroll(ctx, &model.ClassMember{ClassID: class.ID, UserID: user.ID, StartsAt: model.TimeFromMillis(start)})
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
		UserID: user.ID, Kind: model.AffiliationStudent, StartsAt: model.TimeFromMillis(start - 1),
	})
	requireNoError(t, err)
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
			_, errs[index] = ss.ClassMember().EnrollWithAudit(ctx, &store.ClassMemberEnrollment{
				Member: members[index], ExpectedRecipientRevision: user.Revision, Notice: notices[index],
				AuditEventID: attempts[index].ID.String(), AuditAt: model.GetMillis(),
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
	active, err := ss.ClassMember().ListActiveByUser(ctx, user.ID.String(), start+1)
	requireNoError(t, err)
	if len(active) != 1 {
		t.Fatalf("active concurrent enrollments = %#v", active)
	}
	completedAudits := 0
	for _, attempt := range attempts {
		event, err := ss.Audit().Get(ctx, attempt.ID.String())
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
		Resource:  model.Resource{Type: model.ResourceClass, ID: classID},
		ScopeType: model.RoleScopeClass, ScopeID: classID,
		Status: model.AuditStatusAttempt, NodeID: "test-node",
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
