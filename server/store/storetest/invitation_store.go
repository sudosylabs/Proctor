// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type InvitationSQLProbe struct {
	DisableInviterBeforeIssue      func(*testing.T, context.Context, *model.User, func() error) error
	EndBindingBeforeAccept         func(*testing.T, context.Context, *model.RoleBinding, func() error) error
	ArchiveRoleBeforeAccept        func(*testing.T, context.Context, *model.Role, func() error) error
	PayloadKeyReferences           func(*testing.T, context.Context, string) int64
	ArchiveTeacherUnitBeforeAccept func(*testing.T, context.Context, *model.AcademicUnit, func() error) error
	ArchiveTeacherUnitBeforeMail   func(*testing.T, context.Context, *model.AcademicUnit, func() error) error
	MutateTeacherRoleBeforeMail    func(*testing.T, context.Context, *model.Role, func() error) error
}

func TestInvitationStore(t *testing.T, ss store.Store, probes ...InvitationSQLProbe) {
	var probe InvitationSQLProbe
	if len(probes) > 0 {
		probe = probes[0]
	}
	TestTeacherAcademicUnitInvitationStore(t, ss, probe)
	t.Run("IssueStudentClassAtomic", func(t *testing.T) {
		testInvitationIssueStudentClassAtomic(t, ss)
	})
	t.Run("AcceptStudentClassAtomicAndReplaySafe", func(t *testing.T) {
		testInvitationAcceptStudentClassAtomicAndReplaySafe(t, ss)
	})
	t.Run("AcceptStudentClassResolvesExistingUser", func(t *testing.T) {
		testInvitationAcceptStudentClassResolvesExistingUser(t, ss)
	})
	t.Run("AcceptStudentClassRejectsConflictingMembershipAtomically", func(t *testing.T) {
		testInvitationAcceptStudentClassRejectsConflictingMembershipAtomically(t, ss)
	})
	t.Run("AcceptStudentClassCommitsSuppressedNoticeWhenMailDisabled", func(t *testing.T) {
		testInvitationAcceptStudentClassCommitsSuppressedNoticeWhenMailDisabled(t, ss)
	})
	t.Run("IssueStudentClassImmediatelyReplacesElapsedPendingInvitation", func(t *testing.T) {
		testInvitationIssueStudentClassTerminalizesElapsedPendingInvitation(t, ss, probe.PayloadKeyReferences)
	})
	if probe.DisableInviterBeforeIssue != nil {
		t.Run("IssueStudentClassSerializesWithConcurrentInviterDisable", func(t *testing.T) {
			testInvitationIssueSerializesWithInviterDisable(t, ss, probe.DisableInviterBeforeIssue)
		})
	}
	if probe.EndBindingBeforeAccept != nil {
		t.Run("AcceptStudentClassSerializesWithConcurrentBindingEnd", func(t *testing.T) {
			testInvitationAcceptSerializesWithBindingEnd(t, ss, probe.EndBindingBeforeAccept)
		})
	}
	if probe.ArchiveRoleBeforeAccept != nil {
		t.Run("AcceptStudentClassSerializesWithConcurrentRoleArchive", func(t *testing.T) {
			testInvitationAcceptSerializesWithRoleArchive(t, ss, probe.ArchiveRoleBeforeAccept)
		})
	}
}

func testInvitationIssueSerializesWithInviterDisable(
	t *testing.T,
	ss store.Store,
	disable func(*testing.T, context.Context, *model.User, func() error) error,
) {
	ctx := context.Background()
	fixture, class, inviter, _, _, issuedAt := invitationAcceptanceStoreFixture(t, ctx, ss, "disable-race")
	issue := studentClassInvitationIssueFixture(t, ss, inviter, class, fixture.period, issuedAt)
	err := disable(t, ctx, inviter, func() error {
		_, issueErr := ss.Invitation().IssueStudentClass(ctx, issue)
		return issueErr
	})
	if !store.IsConflict(err) {
		t.Fatalf("IssueStudentClass() concurrent disable error = %v", err)
	}
	if _, err = ss.Invitation().Get(ctx, issue.Invitation.ID); !store.IsNotFound(err) {
		t.Fatalf("Invitation survived concurrent inviter disable: %v", err)
	}
}

func testInvitationAcceptSerializesWithBindingEnd(
	t *testing.T,
	ss store.Store,
	end func(*testing.T, context.Context, *model.RoleBinding, func() error) error,
) {
	ctx := context.Background()
	fixture, class, inviter, _, binding, issuedAt := invitationAcceptanceStoreFixture(t, ctx, ss, "binding-race")
	issue := studentClassInvitationIssueFixture(t, ss, inviter, class, fixture.period, issuedAt)
	invitation, err := ss.Invitation().IssueStudentClass(ctx, issue)
	requireNoError(t, err)
	acceptance := studentClassInvitationAcceptanceFixture(t, invitation, model.NowUTC())
	err = end(t, ctx, binding, func() error {
		_, acceptErr := ss.Invitation().AcceptStudentClass(ctx, acceptance)
		return acceptErr
	})
	if !store.IsConflict(err) {
		t.Fatalf("AcceptStudentClass() concurrent binding end error = %v", err)
	}
	current, err := ss.Invitation().Get(ctx, invitation.ID)
	requireNoError(t, err)
	if current.State != model.InvitationPending {
		t.Fatalf("Invitation state after concurrent binding end = %q", current.State)
	}
}

func testInvitationAcceptSerializesWithRoleArchive(
	t *testing.T,
	ss store.Store,
	archive func(*testing.T, context.Context, *model.Role, func() error) error,
) {
	ctx := context.Background()
	fixture, class, inviter, role, _, issuedAt := invitationAcceptanceStoreFixture(t, ctx, ss, "role-race")
	issue := studentClassInvitationIssueFixture(t, ss, inviter, class, fixture.period, issuedAt)
	invitation, err := ss.Invitation().IssueStudentClass(ctx, issue)
	requireNoError(t, err)
	acceptance := studentClassInvitationAcceptanceFixture(t, invitation, model.NowUTC())
	err = archive(t, ctx, role, func() error {
		_, acceptErr := ss.Invitation().AcceptStudentClass(ctx, acceptance)
		return acceptErr
	})
	if !store.IsConflict(err) {
		t.Fatalf("AcceptStudentClass() concurrent Role archive error = %v", err)
	}
	current, err := ss.Invitation().Get(ctx, invitation.ID)
	requireNoError(t, err)
	if current.State != model.InvitationPending {
		t.Fatalf("Invitation state after concurrent Role archive = %q", current.State)
	}
}

func testInvitationAcceptStudentClassCommitsSuppressedNoticeWhenMailDisabled(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture, class, inviter, _, _, issuedAt := invitationAcceptanceStoreFixture(t, ctx, ss, "disabled-mail")
	issue := studentClassInvitationIssueFixture(t, ss, inviter, class, fixture.period, issuedAt)
	invitation, err := ss.Invitation().IssueStudentClass(ctx, issue)
	requireNoError(t, err)
	acceptance := studentClassInvitationAcceptanceFixture(t, invitation, model.NowUTC())
	acceptance.Delivery.State = model.MailDeliverySuppressed
	acceptance.Delivery.PublicFailureCode = model.MailDeliveryDisabledCode
	acceptance.Delivery.EncryptedPayload = nil
	acceptance.DeliveryJob, err = acceptance.DeliveryJob.RequestCancellation(acceptance.Delivery.CreatedAt)
	requireNoError(t, err)
	accepted, err := ss.Invitation().AcceptStudentClass(ctx, acceptance)
	requireNoError(t, err)
	if accepted.Invitation.State != model.InvitationAccepted {
		t.Fatalf("accepted Invitation = %#v", accepted.Invitation)
	}
	delivery, err := ss.Mail().GetDelivery(ctx, acceptance.Delivery.ID)
	requireNoError(t, err)
	job, err := ss.Job().Get(ctx, acceptance.DeliveryJob.ID)
	requireNoError(t, err)
	if delivery.State != model.MailDeliverySuppressed || delivery.PublicFailureCode != model.MailDeliveryDisabledCode || len(delivery.EncryptedPayload) != 0 ||
		job.Status != model.JobStatusCanceled || !job.CompletedAt.Valid {
		t.Fatalf("disabled acceptance mail = %#v / %#v", delivery, job)
	}
}

func testInvitationIssueStudentClassTerminalizesElapsedPendingInvitation(
	t *testing.T,
	ss store.Store,
	payloadKeyReferences func(*testing.T, context.Context, string) int64,
) {
	ctx := context.Background()
	const payloadKeyID = "11111111111111111111111111111111"
	baselineReferences := int64(0)
	if payloadKeyReferences != nil {
		baselineReferences = payloadKeyReferences(t, ctx, payloadKeyID)
	}
	unit, programme := saveProgrammeParents(t, ctx, ss, "elapsed-invitation")
	level := saveProgrammeLevel(t, ctx, ss, programme.ID.String(), "elapsed-level")
	period := saveAcademicPeriod(t, ctx, ss, unit.InstitutionID.String(), "elapsed-period", model.GetMillis()-86_400_000)
	class := saveClass(t, ctx, ss, level.ID.String(), period.ID.String(), "elapsed-class")
	inviter := saveUser(t, ctx, ss)
	role, err := ss.Role().Save(ctx, &model.Role{Name: "elapsed-inviter-" + model.NewId(), DisplayName: "Elapsed Inviter",
		Permissions: []string{string(model.ActionInvitationCreate), string(model.ActionClassMembersManage)}})
	requireNoError(t, err)
	issuedAt := model.NowUTC()
	_, err = ss.RoleBinding().Save(ctx, &model.RoleBinding{UserID: inviter.ID, RoleID: role.ID,
		ScopeType: model.RoleScopeClass, ScopeID: class.ID.String(), StartsAt: issuedAt.Add(-time.Second)})
	requireNoError(t, err)
	first := studentClassInvitationIssueFixture(t, ss, inviter, class, period, issuedAt)
	first.Invitation.TargetEmail = "elapsed-" + model.NewId() + "@example.edu"
	first.Invitation.IntendedStartsAt = issuedAt
	first.Invitation.IntendedEndsAt = model.OptionalTimeFrom(model.NowUTC().Add(2 * time.Second))
	created, err := ss.Invitation().IssueStudentClass(ctx, first)
	requireNoError(t, err)
	claimed := false
	for attempt := 0; attempt < 32 && !claimed; attempt++ {
		claim, claimErr := ss.Job().ClaimNext(ctx, &store.JobClaimRequest{
			Types: []model.JobType{model.JobTypeMailDeliverCredential}, NodeID: "invitation-terminalization",
			ClaimToken: mustClaimToken(t), LeaseDuration: time.Minute,
		})
		requireNoError(t, claimErr)
		claimed = claim.Job.ID == first.DeliveryJob.ID
	}
	if !claimed {
		t.Fatal("failed to claim elapsed Invitation delivery Job")
	}
	if _, err = ss.Mail().StartDelivery(ctx, first.Delivery.ID, first.Delivery.Revision, model.NowUTC()); err != nil {
		t.Fatalf("start elapsed Invitation delivery: %v", err)
	}
	time.Sleep(time.Until(first.Invitation.IntendedEndsAt.Time) + 20*time.Millisecond)
	second := studentClassInvitationIssueFixture(t, ss, inviter, class, period, model.NowUTC())
	second.Invitation.IntendedStartsAt = model.NowUTC()
	second.Invitation.TargetEmail = first.Invitation.TargetEmail
	replacement, err := ss.Invitation().IssueStudentClass(ctx, second)
	requireNoError(t, err)
	old, err := ss.Invitation().Get(ctx, created.ID)
	requireNoError(t, err)
	if old.State != model.InvitationExpired || replacement.State != model.InvitationPending || replacement.ID == old.ID {
		t.Fatalf("elapsed/replacement Invitations = %#v / %#v", old, replacement)
	}
	obsolete, err := ss.Mail().GetDelivery(ctx, first.Delivery.ID)
	requireNoError(t, err)
	if obsolete.State != model.MailDeliverySuppressed ||
		obsolete.PublicFailureCode != model.MailDeliveryObsoleteCode ||
		len(obsolete.EncryptedPayload) != 0 {
		t.Fatalf("elapsed Invitation delivery = %#v", obsolete)
	}
	obsoleteJob, err := ss.Job().Get(ctx, first.DeliveryJob.ID)
	requireNoError(t, err)
	if obsoleteJob.Status != model.JobStatusCancelRequested || obsoleteJob.CompletedAt.Valid {
		t.Fatalf("elapsed Invitation delivery Job = %#v", obsoleteJob)
	}
	if payloadKeyReferences != nil {
		if references := payloadKeyReferences(t, ctx, payloadKeyID); references != baselineReferences+1 {
			t.Fatalf("active payload-key references = %d, want %d", references, baselineReferences+1)
		}
	}
}

func testInvitationAcceptStudentClassResolvesExistingUser(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture, class, inviter, _, _, issuedAt := invitationAcceptanceStoreFixture(t, ctx, ss, "existing-user")
	existing := newUser()
	existing.Email = "existing-invited-" + model.NewId() + "@example.edu"
	existing.EmailVerified = false
	existing, err := createUser(t, ctx, ss, existing)
	requireNoError(t, err)
	issue := studentClassInvitationIssueFixture(t, ss, inviter, class, fixture.period, issuedAt)
	issue.Invitation.TargetEmail = existing.Email
	invitation, err := ss.Invitation().IssueStudentClass(ctx, issue)
	requireNoError(t, err)
	acceptance := studentClassInvitationAcceptanceFixture(t, invitation, model.NowUTC())
	accepted, err := ss.Invitation().AcceptStudentClass(ctx, acceptance)
	requireNoError(t, err)
	if accepted.User.ID != existing.ID || !accepted.User.EmailVerified || accepted.ClassMember.UserID != existing.ID || accepted.Affiliation.UserID != existing.ID {
		t.Fatalf("existing User acceptance = %#v", accepted)
	}
	if _, err = ss.PasswordCredential().GetByUser(ctx, existing.ID.String()); err != nil {
		t.Fatalf("existing User password credential: %v", err)
	}
	if _, err = ss.Job().Get(ctx, acceptance.DefaultProfilePictureJob.ID); !store.IsNotFound(err) {
		t.Fatalf("existing User unexpectedly enqueued a second default-picture Job: %v", err)
	}
}

func testInvitationAcceptStudentClassRejectsConflictingMembershipAtomically(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture, class, inviter, _, _, issuedAt := invitationAcceptanceStoreFixture(t, ctx, ss, "conflict-target")
	otherClass := saveClass(t, ctx, ss, fixture.level.ID.String(), fixture.period.ID.String(), "conflict-existing")
	existing := saveUser(t, ctx, ss)
	_, err := ss.Affiliation().Save(ctx, &model.Affiliation{UserID: existing.ID, Kind: model.AffiliationStudent, StartsAt: issuedAt})
	requireNoError(t, err)
	_, err = ss.ClassMember().Enroll(ctx, &model.ClassMember{ClassID: otherClass.ID, UserID: existing.ID, StartsAt: fixture.period.StartsAt})
	requireNoError(t, err)
	issue := studentClassInvitationIssueFixture(t, ss, inviter, class, fixture.period, issuedAt)
	issue.Invitation.TargetEmail = existing.Email
	invitation, err := ss.Invitation().IssueStudentClass(ctx, issue)
	requireNoError(t, err)
	acceptance := studentClassInvitationAcceptanceFixture(t, invitation, model.NowUTC())
	if _, err = ss.Invitation().AcceptStudentClass(ctx, acceptance); err == nil {
		t.Fatal("AcceptStudentClass() accepted a conflicting active Class membership")
	}
	current, getErr := ss.Invitation().Get(ctx, invitation.ID)
	requireNoError(t, getErr)
	if current.State != model.InvitationPending {
		t.Fatalf("Invitation state after conflict = %q", current.State)
	}
	if _, getErr = ss.Mail().GetDelivery(ctx, acceptance.Delivery.ID); !store.IsNotFound(getErr) {
		t.Fatalf("acceptance delivery survived conflict rollback: %v", getErr)
	}
	if _, getErr = ss.PasswordCredential().GetByUser(ctx, existing.ID.String()); !store.IsNotFound(getErr) {
		t.Fatalf("password credential survived conflict rollback: %v", getErr)
	}
}

func invitationAcceptanceStoreFixture(t *testing.T, ctx context.Context, ss store.Store, suffix string) (classFixture, *model.Class, *model.User, *model.Role, *model.RoleBinding, time.Time) {
	t.Helper()
	fixture := saveClassFixture(t, ctx, ss)
	class := saveClass(t, ctx, ss, fixture.level.ID.String(), fixture.period.ID.String(), "invitation-"+suffix)
	inviter := saveUser(t, ctx, ss)
	role, err := ss.Role().Save(ctx, &model.Role{Name: "invite-" + suffix + "-" + model.NewId(), DisplayName: "Student Inviter",
		Permissions: []string{string(model.ActionInvitationCreate), string(model.ActionClassMembersManage)}})
	requireNoError(t, err)
	issuedAt := model.NowUTC().Add(-time.Minute)
	binding, err := ss.RoleBinding().Save(ctx, &model.RoleBinding{UserID: inviter.ID, RoleID: role.ID,
		ScopeType: model.RoleScopeClass, ScopeID: class.ID.String(), StartsAt: issuedAt.Add(-time.Second)})
	requireNoError(t, err)
	return fixture, class, inviter, role, binding, issuedAt
}

func testInvitationIssueStudentClassAtomic(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture := saveClassFixture(t, ctx, ss)
	class := saveClass(t, ctx, ss, fixture.level.ID.String(), fixture.period.ID.String(), "invitation-class")
	inviter := saveUser(t, ctx, ss)
	role, err := ss.Role().Save(ctx, &model.Role{
		Name: "student-inviter-" + model.NewId(), DisplayName: "Student Inviter",
		Permissions: []string{string(model.ActionInvitationCreate), string(model.ActionClassMembersManage)},
	})
	requireNoError(t, err)
	issuedAt := model.NowUTC().Add(-time.Minute)
	_, err = ss.RoleBinding().Save(ctx, &model.RoleBinding{
		UserID: inviter.ID, RoleID: role.ID, ScopeType: model.RoleScopeClass,
		ScopeID: class.ID.String(), StartsAt: issuedAt.Add(-time.Second),
	})
	requireNoError(t, err)

	input := studentClassInvitationIssueFixture(t, ss, inviter, class, fixture.period, issuedAt)
	created, err := ss.Invitation().IssueStudentClass(ctx, input)
	requireNoError(t, err)
	if created.ID != input.Invitation.ID || created.TargetEmail != "student@example.edu" ||
		created.ClaimHash != input.Invitation.ClaimHash || created.State != model.InvitationPending {
		t.Fatalf("IssueStudentClass() = %#v", created)
	}
	byID, err := ss.Invitation().Get(ctx, created.ID)
	requireNoError(t, err)
	byClaim, err := ss.Invitation().GetByClaimHash(ctx, created.ClaimHash)
	requireNoError(t, err)
	if byID.ID != created.ID || byClaim.ID != created.ID {
		t.Fatalf("Invitation reads = %#v / %#v", byID, byClaim)
	}
	delivery, err := ss.Mail().GetDelivery(ctx, input.Delivery.ID)
	requireNoError(t, err)
	if delivery.TargetInvitationID != created.ID || delivery.TargetUserID.IsValid() ||
		delivery.TemplateKey != model.MailTemplateAccessStudentClassInvitation {
		t.Fatalf("Invitation delivery = %#v", delivery)
	}
	job, err := ss.Job().Get(ctx, input.DeliveryJob.ID)
	requireNoError(t, err)
	if job.Status != model.JobStatusQueued || job.Type != model.JobTypeMailDeliverCredential {
		t.Fatalf("Invitation delivery Job = %#v", job)
	}
	audit, err := ss.Audit().Get(ctx, model.AuditEventID(input.AuditEventID).String())
	requireNoError(t, err)
	if audit.Status != model.AuditStatusSuccess || strings.Contains(string(audit.Result), created.TargetEmail) ||
		strings.Contains(string(audit.Result), created.ClaimHash) {
		t.Fatalf("Invitation audit = %#v", audit)
	}

	rollback := studentClassInvitationIssueFixture(t, ss, inviter, class, fixture.period, issuedAt.Add(time.Second))
	rollback.Invitation.TargetEmail = "rollback@example.edu"
	rollback.AuditEventID = model.NewAuditEventID().String()
	if _, err = ss.Invitation().IssueStudentClass(ctx, rollback); err == nil {
		t.Fatal("IssueStudentClass() succeeded without its durable audit attempt")
	}
	if _, err = ss.Invitation().Get(ctx, rollback.Invitation.ID); !store.IsNotFound(err) {
		t.Fatalf("rolled-back Invitation error = %v", err)
	}
	if _, err = ss.Mail().GetDelivery(ctx, rollback.Delivery.ID); !store.IsNotFound(err) {
		t.Fatalf("rolled-back delivery error = %v", err)
	}
}

func studentClassInvitationIssueFixture(t *testing.T, ss store.Store, inviter *model.User, class *model.Class, period *model.AcademicPeriod, issuedAt time.Time) *store.StudentClassInvitationIssue {
	t.Helper()
	invitation, err := model.NewStudentClassInvitation(model.StudentClassInvitationInput{
		ID: model.NewInvitationID(), TargetEmail: "student@example.edu",
		ClassID: class.ID, AcademicPeriodID: period.ID, IntendedStartsAt: period.StartsAt,
		IntendedEndsAt: model.OptionalTimeFrom(period.EndsAt),
		Suggestions:    model.InvitationProfileSuggestions{Username: "student-one", DisplayName: "Student One", Locale: "en"},
		InviterUserID:  inviter.ID, ScopeType: model.RoleScopeClass, ScopeID: class.ID.String(),
		ClaimHash: model.HashInvitationClaim(model.NewCredentialToken()), IssuedAt: issuedAt,
	})
	requireNoError(t, err)
	occurrenceID, deliveryID, jobID := model.NewMailOccurrenceID(), model.NewMailDeliveryID(), model.NewJobID()
	command, err := model.EncodeMailDeliveryCommand(model.MailDeliveryCommandV1{DeliveryID: deliveryID})
	requireNoError(t, err)
	job, err := model.NewJob(jobID, model.JobTypeMailDeliverCredential, 1, command, deliveryID.String(), issuedAt, issuedAt, model.MailMaximumAttempts)
	requireNoError(t, err)
	delivery := &model.MailDelivery{
		ID: deliveryID, OccurrenceID: occurrenceID, JobID: jobID, TargetInvitationID: invitation.ID,
		TemplateKey: model.MailTemplateAccessStudentClassInvitation, TemplateDigest: strings.Repeat("b", 64),
		MaskedRecipient: "s***@example.edu", State: model.MailDeliveryQueued,
		CreatedAt: issuedAt, UpdatedAt: issuedAt, MessageDate: issuedAt, Deadline: invitation.ExpiresAt,
		MessageID:        "<invitation." + deliveryID.String() + "@example.test>",
		EncryptedPayload: json.RawMessage(`{"version":1,"key_id":"11111111111111111111111111111111","ciphertext":"secret"}`), Revision: 1,
	}
	attempt, err := ss.Audit().Save(context.Background(), &model.AuditEvent{
		ActorID: inviter.ID, Action: string(model.ActionInvitationCreate),
		Resource:  model.Resource{Type: model.ResourceClass, ID: class.ID.String()},
		ScopeType: model.RoleScopeClass, ScopeID: class.ID.String(), Status: model.AuditStatusAttempt,
		NodeID: "invitation-store-test",
	})
	requireNoError(t, err)
	return &store.StudentClassInvitationIssue{
		Invitation: invitation,
		Occurrence: &model.MailOccurrence{ID: occurrenceID, Kind: model.MailOccurrenceInvitation, TemplateKey: model.MailTemplateAccessStudentClassInvitation, ActorUserID: inviter.ID, CreatedAt: issuedAt},
		Delivery:   delivery, DeliveryJob: job, AuditEventID: attempt.ID.String(), AuditAt: model.MillisFromTime(issuedAt),
	}
}

func testInvitationAcceptStudentClassAtomicAndReplaySafe(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture := saveClassFixture(t, ctx, ss)
	class := saveClass(t, ctx, ss, fixture.level.ID.String(), fixture.period.ID.String(), "invitation-accept-class")
	inviter := saveUser(t, ctx, ss)
	role, err := ss.Role().Save(ctx, &model.Role{
		Name: "accept-inviter-" + model.NewId(), DisplayName: "Accept Inviter",
		Permissions: []string{string(model.ActionInvitationCreate), string(model.ActionClassMembersManage)},
	})
	requireNoError(t, err)
	issuedAt := model.NowUTC().Add(-time.Minute)
	_, err = ss.RoleBinding().Save(ctx, &model.RoleBinding{
		UserID: inviter.ID, RoleID: role.ID, ScopeType: model.RoleScopeClass,
		ScopeID: class.ID.String(), StartsAt: issuedAt.Add(-time.Second),
	})
	requireNoError(t, err)

	issue := studentClassInvitationIssueFixture(t, ss, inviter, class, fixture.period, issuedAt)
	invitation, err := ss.Invitation().IssueStudentClass(ctx, issue)
	requireNoError(t, err)
	acceptance := studentClassInvitationAcceptanceFixture(t, invitation, model.NowUTC())
	accepted, err := ss.Invitation().AcceptStudentClass(ctx, acceptance)
	requireNoError(t, err)
	if accepted.Replayed || accepted.Invitation.State != model.InvitationAccepted ||
		accepted.User.ID != acceptance.User.ID || accepted.User.Email != invitation.TargetEmail || !accepted.User.EmailVerified ||
		accepted.Affiliation.Kind != model.AffiliationStudent || accepted.ClassMember.ClassID != class.ID ||
		accepted.ClassMember.AcademicPeriodID != fixture.period.ID || accepted.ClassMember.UserID != accepted.User.ID {
		t.Fatalf("AcceptStudentClass() = %#v", accepted)
	}
	if _, err = ss.UserSettings().Get(ctx, accepted.User.ID); err != nil {
		t.Fatalf("accepted User settings: %v", err)
	}
	if _, err = ss.PasswordCredential().GetByUser(ctx, accepted.User.ID.String()); err != nil {
		t.Fatalf("accepted User password credential: %v", err)
	}
	if _, err = ss.Job().Get(ctx, acceptance.DefaultProfilePictureJob.ID); err != nil {
		t.Fatalf("accepted User default-picture Job: %v", err)
	}
	if delivery, deliveryErr := ss.Mail().GetDelivery(ctx, acceptance.Delivery.ID); deliveryErr != nil ||
		delivery.TargetUserID != accepted.User.ID || delivery.TargetInvitationID.IsValid() {
		t.Fatalf("acceptance delivery = %#v, %v", delivery, deliveryErr)
	}
	obsolete, err := ss.Mail().GetDelivery(ctx, issue.Delivery.ID)
	requireNoError(t, err)
	if obsolete.State != model.MailDeliverySuppressed || obsolete.PublicFailureCode != model.MailDeliveryObsoleteCode || len(obsolete.EncryptedPayload) != 0 {
		t.Fatalf("accepted Invitation credential delivery = %#v", obsolete)
	}
	progressedAt := accepted.ClassMember.StartsAt.Add(24 * time.Hour)
	ended, err := ss.ClassMember().End(ctx, accepted.ClassMember.ID.String(), accepted.ClassMember.Revision, model.MillisFromTime(progressedAt))
	requireNoError(t, err)
	newMembership := &model.ClassMember{ClassID: class.ID, AcademicPeriodID: fixture.period.ID, UserID: accepted.User.ID,
		StartsAt: progressedAt, EndsAt: accepted.ClassMember.EndsAt}
	enrollment, err := ss.ClassMember().Enroll(ctx, newMembership)
	requireNoError(t, err)
	newMembership = enrollment.Membership

	replayed, err := ss.Invitation().AcceptStudentClass(ctx, acceptance)
	requireNoError(t, err)
	if !replayed.Replayed || replayed.User.ID != accepted.User.ID || replayed.Invitation.Revision != accepted.Invitation.Revision ||
		replayed.Affiliation.ID != accepted.Affiliation.ID || replayed.ClassMember.ID != ended.ID || replayed.ClassMember.ID == newMembership.ID {
		t.Fatalf("replayed AcceptStudentClass() = %#v", replayed)
	}
	listed, err := ss.ClassMember().ListByClass(ctx, class.ID.String(), model.MillisFromTime(accepted.ClassMember.StartsAt))
	requireNoError(t, err)
	if len(listed) != 1 || listed[0].ID != accepted.ClassMember.ID {
		t.Fatalf("Class members after replay = %#v", listed)
	}
}

func studentClassInvitationAcceptanceFixture(t *testing.T, invitation *model.Invitation, acceptedAt time.Time) *store.StudentClassInvitationAcceptance {
	t.Helper()
	user := &model.User{
		Username: invitation.Suggestions.Username, Email: invitation.TargetEmail, EmailVerified: true,
		DisplayName: invitation.Suggestions.DisplayName, FirstName: invitation.Suggestions.FirstName,
		LastName: invitation.Suggestions.LastName, Locale: invitation.Suggestions.Locale,
	}
	user.PrepareCreate(model.NewUserID(), acceptedAt)
	settings, err := model.NewUserSettingsDocument(user.ID, model.NewUserSettingsRevision(), acceptedAt)
	requireNoError(t, err)
	credential := &model.PasswordCredential{UserID: user.ID, PasswordHash: "encoded-invitation-password"}
	credential.PrepareCreate(model.NewPasswordCredentialID(), acceptedAt)
	defaultCommand, err := model.EncodeDefaultProfilePictureCommand(model.DefaultProfilePictureCommandV1{UserID: user.ID})
	requireNoError(t, err)
	defaultJob, err := model.NewJob(model.NewJobID(), model.JobTypeProfilePictureGenerateDefault, 1, defaultCommand, user.ID.String(), acceptedAt, acceptedAt, 8)
	requireNoError(t, err)
	effectiveStart := invitation.EffectiveStartsAt(acceptedAt)
	affiliation := &model.Affiliation{UserID: user.ID, Kind: model.AffiliationStudent, StartsAt: effectiveStart}
	affiliation.PrepareCreate(model.NewAffiliationID(), acceptedAt)
	member := &model.ClassMember{
		ClassID: invitation.ClassID, AcademicPeriodID: invitation.AcademicPeriodID,
		UserID: user.ID, StartsAt: effectiveStart, EndsAt: invitation.IntendedEndsAt,
	}
	member.PrepareCreate(model.NewClassMemberID(), acceptedAt)
	occurrenceID, deliveryID, deliveryJobID := model.NewMailOccurrenceID(), model.NewMailDeliveryID(), model.NewJobID()
	deliveryCommand, err := model.EncodeMailDeliveryCommand(model.MailDeliveryCommandV1{DeliveryID: deliveryID})
	requireNoError(t, err)
	deliveryJob, err := model.NewJob(deliveryJobID, model.JobTypeMailDeliver, 1, deliveryCommand, deliveryID.String(), acceptedAt, acceptedAt, model.MailMaximumAttempts)
	requireNoError(t, err)
	delivery := &model.MailDelivery{
		ID: deliveryID, OccurrenceID: occurrenceID, JobID: deliveryJobID, TargetUserID: user.ID,
		TemplateKey: model.MailTemplateAccessInvitationAccepted, TemplateDigest: strings.Repeat("c", 64),
		MaskedRecipient: "s***@example.edu", State: model.MailDeliveryQueued,
		CreatedAt: acceptedAt, UpdatedAt: acceptedAt, MessageDate: acceptedAt,
		Deadline: acceptedAt.Add(24 * time.Hour), MessageID: "<invitation-accepted." + deliveryID.String() + "@example.test>",
		EncryptedPayload: json.RawMessage(`{"version":1,"key_id":"11111111111111111111111111111111","ciphertext":"accepted"}`), Revision: 1,
	}
	return &store.StudentClassInvitationAcceptance{
		ClaimHash: invitation.ClaimHash, AcceptedAt: model.MillisFromTime(acceptedAt),
		User: user, Settings: settings, PasswordCredential: credential,
		DefaultProfilePictureJob: defaultJob, Affiliation: affiliation, ClassMember: member,
		Occurrence: &model.MailOccurrence{ID: occurrenceID, Kind: model.MailOccurrenceInvitation, TemplateKey: model.MailTemplateAccessInvitationAccepted, ActorUserID: invitation.InviterUserID, CreatedAt: acceptedAt},
		Delivery:   delivery, DeliveryJob: deliveryJob,
		AuditEvent: &model.AuditEvent{
			ActorID: user.ID, Action: "invitation.accept", Resource: model.Resource{Type: model.ResourceClass, ID: invitation.ClassID.String()},
			ScopeType: model.RoleScopeClass, ScopeID: invitation.ClassID.String(), Status: model.AuditStatusSuccess,
			NodeID: "invitation-store-test",
		},
		RequiredActions: []model.Action{model.ActionInvitationCreate, model.ActionClassMembersManage},
	}
}
