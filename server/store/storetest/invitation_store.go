// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
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
	t.Run("ScopedRoleExistingUserAtomicAndReplaySafe", func(t *testing.T) {
		testScopedRoleInvitationExistingUserAtomicAndReplaySafe(t, ss)
	})
	t.Run("InstitutionRoleExistingUserAtomicAndReplaySafe", func(t *testing.T) {
		testInstitutionRoleInvitationExistingUserAtomicAndReplaySafe(t, ss)
	})
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
	t.Run("AdministrationLifecycle", func(t *testing.T) {
		testInvitationAdministrationLifecycle(t, ss)
	})
	t.Run("AdministrationResendIsRevisionFenced", func(t *testing.T) {
		testInvitationAdministrationResendIsRevisionFenced(t, ss)
	})
	t.Run("AdministrationRevokeAndReplaceAreRevisionFenced", func(t *testing.T) {
		testInvitationAdministrationRevokeAndReplaceAreRevisionFenced(t, ss)
	})
	t.Run("AdministrationPaginationIsStable", func(t *testing.T) {
		testInvitationAdministrationPaginationIsStable(t, ss)
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
		t.Run("AcceptScopedRoleSerializesWithConcurrentRoleArchive", func(t *testing.T) {
			testScopedRoleAcceptSerializesWithRoleArchive(t, ss, probe.ArchiveRoleBeforeAccept)
		})
	}
	if probe.EndBindingBeforeAccept != nil {
		t.Run("AcceptScopedRoleSerializesWithConcurrentInviterBindingEnd", func(t *testing.T) {
			testScopedRoleAcceptSerializesWithInviterBindingEnd(t, ss, probe.EndBindingBeforeAccept)
		})
	}
}

func testInvitationAdministrationPaginationIsStable(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture, class, inviter, issuedAt := invitationAdministrationFixture(t, ctx, ss, "pagination")
	created := make(map[model.InvitationID]struct{}, 3)
	for index := 0; index < 3; index++ {
		issue := studentClassInvitationIssueFixture(t, ss, inviter, class, fixture.period, issuedAt)
		issue.Invitation.TargetEmail = "pagination-" + model.NewId() + "@example.edu"
		requireNoError(t, issue.Invitation.Validate())
		invitation, err := ss.Invitation().IssueStudentClass(ctx, issue)
		requireNoError(t, err)
		created[invitation.ID] = struct{}{}
	}

	visibility := store.InvitationVisibilityScope{ClassIDs: []string{class.ID.String()}}
	first, err := ss.Invitation().List(ctx, store.InvitationListOptions{Visibility: visibility, Limit: 2})
	requireNoError(t, err)
	if len(first.Items) != 2 || !first.More {
		t.Fatalf("first Invitation page = %#v", first)
	}
	boundary := first.Items[len(first.Items)-1].Invitation
	second, err := ss.Invitation().List(ctx, store.InvitationListOptions{Visibility: visibility, Limit: 2,
		BeforeCreatedAt: boundary.CreatedAt, BeforeID: boundary.ID})
	requireNoError(t, err)
	if len(second.Items) != 1 || second.More {
		t.Fatalf("second Invitation page = %#v", second)
	}

	seen := make(map[model.InvitationID]struct{}, 3)
	for _, page := range []*store.InvitationPage{first, second} {
		for _, record := range page.Items {
			if _, duplicate := seen[record.Invitation.ID]; duplicate {
				t.Fatalf("Invitation %s repeated across pages", record.Invitation.ID)
			}
			seen[record.Invitation.ID] = struct{}{}
		}
	}
	if len(seen) != len(created) {
		t.Fatalf("paginated Invitations = %v, want %v", seen, created)
	}
	for id := range created {
		if _, ok := seen[id]; !ok {
			t.Fatalf("Invitation %s skipped by pagination", id)
		}
	}
}

func testInvitationAdministrationRevokeAndReplaceAreRevisionFenced(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture, class, inviter, issuedAt := invitationAdministrationFixture(t, ctx, ss, "revoke-replace-race")
	issue := studentClassInvitationIssueFixture(t, ss, inviter, class, fixture.period, issuedAt)
	issue.Invitation.TargetEmail = "revoke-replace-race-" + model.NewId() + "@example.edu"
	requireNoError(t, issue.Invitation.Validate())
	current, err := ss.Invitation().IssueStudentClass(ctx, issue)
	requireNoError(t, err)

	changed, err := model.NewStudentClassInvitation(model.StudentClassInvitationInput{ID: model.NewInvitationID(),
		TargetEmail: "revoke-replace-winner-" + model.NewId() + "@example.edu", ClassID: class.ID,
		AcademicPeriodID: fixture.period.ID, IntendedStartsAt: fixture.period.StartsAt,
		IntendedEndsAt: model.OptionalTimeFrom(fixture.period.EndsAt), InviterUserID: inviter.ID,
		ScopeType: model.RoleScopeClass, ScopeID: class.ID.String(),
		ClaimHash: model.HashInvitationClaim(model.NewCredentialToken()), IssuedAt: model.NowUTC()})
	requireNoError(t, err)
	replacementOccurrence, replacementDelivery, replacementJob := invitationLifecycleMailFixture(t, changed.ID, inviter.ID,
		model.MailTemplateAccessStudentClassInvitation, model.JobTypeMailDeliverCredential, model.NowUTC(), changed.ExpiresAt)
	supersedeAudit := saveInvitationLifecycleAuditAttempt(t, ctx, ss, inviter.ID, class.ID.String(), "invitation.supersede")
	replacementAudit := saveInvitationLifecycleAuditAttempt(t, ctx, ss, inviter.ID, class.ID.String(), "invitation.replacement_issue")
	replacement := &store.InvitationReplacement{CurrentID: current.ID, ExpectedCurrentRevision: current.Revision,
		Replacement: changed, Lifetime: model.InvitationLifetime, Occurrence: replacementOccurrence,
		Delivery: replacementDelivery, DeliveryJob: replacementJob, ActorUserID: inviter.ID,
		CurrentAuditEventID: supersedeAudit.ID.String(), ReplacementAuditEventID: replacementAudit.ID.String(),
		AuditAt: model.MillisFromTime(model.NowUTC())}

	revocationOccurrence, revocationDelivery, revocationJob := invitationLifecycleMailFixture(t, current.ID, inviter.ID,
		model.MailTemplateAccessInvitationRevoked, model.JobTypeMailDeliver, model.NowUTC(), model.NowUTC().Add(24*time.Hour))
	revocationAudit := saveInvitationLifecycleAuditAttempt(t, ctx, ss, inviter.ID, class.ID.String(), "invitation.revoke")
	revocation := &store.InvitationRevocation{ID: current.ID, ExpectedRevision: current.Revision, ActorUserID: inviter.ID,
		RevocationNotice: &store.PreparedMail{Occurrence: revocationOccurrence, Delivery: revocationDelivery, Job: revocationJob},
		AuditEventID:     revocationAudit.ID.String(), AuditAt: model.MillisFromTime(model.NowUTC())}

	var revoked, replaced *store.InvitationAdministrationRecord
	var revokeErr, replaceErr error
	start := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		<-start
		revoked, revokeErr = ss.Invitation().Revoke(ctx, revocation)
	}()
	go func() {
		defer wait.Done()
		<-start
		replaced, replaceErr = ss.Invitation().Replace(ctx, replacement)
	}()
	close(start)
	wait.Wait()

	if (revokeErr == nil) == (replaceErr == nil) {
		t.Fatalf("concurrent Revoke()/Replace() results=%#v/%#v errors=%v/%v", revoked, replaced, revokeErr, replaceErr)
	}
	currentAfter, err := ss.Invitation().Get(ctx, current.ID)
	requireNoError(t, err)
	if revokeErr == nil {
		if !store.IsConflict(replaceErr) || currentAfter.State != model.InvitationRevoked || currentAfter.Revision != current.Revision+1 {
			t.Fatalf("winning Revoke() = %#v current=%#v Replace error=%v", revoked, currentAfter, replaceErr)
		}
		if _, err = ss.Invitation().Get(ctx, changed.ID); !store.IsNotFound(err) {
			t.Fatalf("losing replacement Invitation = %v", err)
		}
		if _, err = ss.Mail().GetDelivery(ctx, replacementDelivery.ID); !store.IsNotFound(err) {
			t.Fatalf("losing replacement delivery = %v", err)
		}
	} else {
		if !store.IsConflict(revokeErr) || currentAfter.State != model.InvitationSuperseded || currentAfter.Revision != current.Revision+1 ||
			replaced == nil || replaced.Invitation.ID != changed.ID {
			t.Fatalf("winning Replace() = %#v current=%#v Revoke error=%v", replaced, currentAfter, revokeErr)
		}
		if _, err = ss.Mail().GetDelivery(ctx, revocationDelivery.ID); !store.IsNotFound(err) {
			t.Fatalf("losing revocation delivery = %v", err)
		}
	}
	obsolete, err := ss.Mail().GetDelivery(ctx, issue.Delivery.ID)
	requireNoError(t, err)
	if obsolete.State != model.MailDeliverySuppressed || len(obsolete.EncryptedPayload) != 0 {
		t.Fatalf("terminal race credential delivery = %#v", obsolete)
	}
}

func testInvitationAdministrationResendIsRevisionFenced(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture, class, inviter, issuedAt := invitationAdministrationFixture(t, ctx, ss, "resend-race")
	issue := studentClassInvitationIssueFixture(t, ss, inviter, class, fixture.period, issuedAt)
	issue.Invitation.TargetEmail = "resend-race-" + model.NewId() + "@example.edu"
	requireNoError(t, issue.Invitation.Validate())
	invitation, err := ss.Invitation().IssueStudentClass(ctx, issue)
	requireNoError(t, err)

	inputs := make([]*store.InvitationResend, 2)
	for index := range inputs {
		occurrence, delivery, job := invitationLifecycleMailFixture(t, invitation.ID, inviter.ID,
			model.MailTemplateAccessStudentClassInvitation, model.JobTypeMailDeliverCredential, model.NowUTC(), invitation.ExpiresAt)
		audit := saveInvitationLifecycleAuditAttempt(t, ctx, ss, inviter.ID, class.ID.String(), "invitation.resend")
		inputs[index] = &store.InvitationResend{ID: invitation.ID, ExpectedRevision: invitation.Revision,
			ClaimHash: model.HashInvitationClaim(model.NewCredentialToken()), Occurrence: occurrence, Delivery: delivery,
			DeliveryJob: job, ActorUserID: inviter.ID, AuditEventID: audit.ID.String(), AuditAt: model.MillisFromTime(model.NowUTC())}
	}
	results := make([]*store.InvitationAdministrationRecord, 2)
	errors := make([]error, 2)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for index := range inputs {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			results[index], errors[index] = ss.Invitation().Resend(ctx, inputs[index])
		}(index)
	}
	close(start)
	wait.Wait()
	winner := -1
	for index, resendErr := range errors {
		if resendErr == nil {
			winner = index
			continue
		}
		if !store.IsConflict(resendErr) {
			t.Fatalf("concurrent Resend() error[%d] = %v", index, resendErr)
		}
	}
	if winner < 0 || errors[1-winner] == nil || results[winner] == nil || results[winner].Invitation.Revision != invitation.Revision+1 {
		t.Fatalf("concurrent Resend() results=%#v errors=%v", results, errors)
	}
	if _, err = ss.Invitation().GetByClaimHash(ctx, inputs[winner].ClaimHash); err != nil {
		t.Fatalf("winning claim lookup = %v", err)
	}
	if _, err = ss.Invitation().GetByClaimHash(ctx, inputs[1-winner].ClaimHash); !store.IsNotFound(err) {
		t.Fatalf("losing claim lookup = %v", err)
	}
	if _, err = ss.Mail().GetDelivery(ctx, inputs[1-winner].Delivery.ID); !store.IsNotFound(err) {
		t.Fatalf("losing resend delivery = %v", err)
	}
}

func testInvitationAdministrationLifecycle(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture, class, inviter, issuedAt := invitationAdministrationFixture(t, ctx, ss, "lifecycle")
	issue := studentClassInvitationIssueFixture(t, ss, inviter, class, fixture.period, issuedAt)
	issue.Invitation.TargetEmail = "lifecycle-" + model.NewId() + "@example.edu"
	requireNoError(t, issue.Invitation.Validate())
	invitation, err := ss.Invitation().IssueStudentClass(ctx, issue)
	requireNoError(t, err)

	visibility := store.InvitationVisibilityScope{ClassIDs: []string{class.ID.String()}}
	page, err := ss.Invitation().List(ctx, store.InvitationListOptions{Visibility: visibility,
		Purpose: model.InvitationPurposeStudentClass, State: model.InvitationPending,
		TargetEmail: invitation.TargetEmail, TargetID: class.ID.String(), Limit: 1})
	requireNoError(t, err)
	if len(page.Items) != 1 || page.Items[0].Invitation.ID != invitation.ID || page.Items[0].Delivery == nil ||
		page.Items[0].Delivery.State != model.MailDeliveryQueued || page.Items[0].Delivery.MaskedRecipient == "" {
		t.Fatalf("Invitation administration list = %#v", page)
	}
	rootPage, err := ss.Invitation().List(ctx, store.InvitationListOptions{Visibility: store.InvitationVisibilityScope{
		AcademicUnitRootIDs: []string{fixture.programme.AcademicUnitID.String()}}, TargetEmail: invitation.TargetEmail, Limit: 10})
	requireNoError(t, err)
	if len(rootPage.Items) != 1 || rootPage.Items[0].Invitation.ID != invitation.ID {
		t.Fatalf("Academic Unit subtree Invitation list = %#v", rootPage)
	}
	if _, err = ss.Invitation().GetForAdministration(ctx, invitation.ID,
		store.InvitationVisibilityScope{ClassIDs: []string{model.NewClassID().String()}}); !store.IsNotFound(err) {
		t.Fatalf("out-of-scope Invitation detail error = %v", err)
	}

	oldHash := invitation.ClaimHash
	newHash := model.HashInvitationClaim(model.NewCredentialToken())
	occurrence, delivery, job := invitationLifecycleMailFixture(t, invitation.ID, inviter.ID,
		model.MailTemplateAccessStudentClassInvitation, model.JobTypeMailDeliverCredential, model.NowUTC(), invitation.ExpiresAt)
	audit := saveInvitationLifecycleAuditAttempt(t, ctx, ss, inviter.ID, class.ID.String(), "invitation.resend")
	resent, err := ss.Invitation().Resend(ctx, &store.InvitationResend{ID: invitation.ID, ExpectedRevision: invitation.Revision,
		ClaimHash: newHash, Occurrence: occurrence, Delivery: delivery, DeliveryJob: job, ActorUserID: inviter.ID,
		AuditEventID: audit.ID.String(), AuditAt: model.MillisFromTime(model.NowUTC())})
	requireNoError(t, err)
	if resent.Invitation.ClaimHash != newHash || resent.Invitation.Revision != invitation.Revision+1 ||
		resent.Invitation.ExpiresAt != invitation.ExpiresAt || resent.Delivery == nil || resent.Delivery.State != model.MailDeliveryQueued {
		t.Fatalf("Resend() = %#v", resent)
	}
	if _, err = ss.Invitation().GetByClaimHash(ctx, oldHash); !store.IsNotFound(err) {
		t.Fatalf("old Invitation claim error = %v", err)
	}
	obsolete, err := ss.Mail().GetDelivery(ctx, issue.Delivery.ID)
	requireNoError(t, err)
	if obsolete.State != model.MailDeliverySuppressed || len(obsolete.EncryptedPayload) != 0 || obsolete.PublicFailureCode != model.MailDeliveryObsoleteCode {
		t.Fatalf("obsolete Invitation delivery = %#v", obsolete)
	}

	sending, err := ss.Mail().StartDelivery(ctx, delivery.ID, 1, model.NowUTC())
	requireNoError(t, err)
	accepted, err := ss.Mail().CompleteDelivery(ctx, &store.MailDeliveryCompletion{DeliveryID: sending.ID,
		ExpectedRevision: sending.Revision, Kind: store.MailDeliveryCompletionAccepted, At: model.NowUTC()})
	requireNoError(t, err)
	if accepted.State != model.MailDeliveryAccepted || len(accepted.EncryptedPayload) != 0 {
		t.Fatalf("accepted Invitation delivery = %#v", accepted)
	}
	revocationOccurrence, revocationDelivery, revocationJob := invitationLifecycleMailFixture(t, invitation.ID, inviter.ID,
		model.MailTemplateAccessInvitationRevoked, model.JobTypeMailDeliver, model.NowUTC(), model.NowUTC().Add(24*time.Hour))
	revocationAudit := saveInvitationLifecycleAuditAttempt(t, ctx, ss, inviter.ID, class.ID.String(), "invitation.revoke")
	revoked, err := ss.Invitation().Revoke(ctx, &store.InvitationRevocation{ID: invitation.ID,
		ExpectedRevision: resent.Invitation.Revision, ActorUserID: inviter.ID,
		RevocationNotice: &store.PreparedMail{Occurrence: revocationOccurrence, Delivery: revocationDelivery, Job: revocationJob},
		AuditEventID:     revocationAudit.ID.String(), AuditAt: model.MillisFromTime(model.NowUTC())})
	requireNoError(t, err)
	if revoked.Invitation.State != model.InvitationRevoked || revoked.Delivery == nil ||
		revoked.Delivery.TemplateKey != model.MailTemplateAccessInvitationRevoked || revoked.Delivery.State != model.MailDeliveryQueued {
		t.Fatalf("Revoke() after accepted SMTP delivery = %#v", revoked)
	}
	if _, err = ss.Invitation().GetByClaimHash(ctx, newHash); err != nil {
		t.Fatalf("terminal Invitation lookup by claim hash = %v", err)
	}

	unsentFixture, unsentClass, unsentInviter, unsentAt := invitationAdministrationFixture(t, ctx, ss, "unsent-revoke")
	unsentIssue := studentClassInvitationIssueFixture(t, ss, unsentInviter, unsentClass, unsentFixture.period, unsentAt)
	unsentIssue.Invitation.TargetEmail = "unsent-revoke-" + model.NewId() + "@example.edu"
	requireNoError(t, unsentIssue.Invitation.Validate())
	unsent, err := ss.Invitation().IssueStudentClass(ctx, unsentIssue)
	requireNoError(t, err)
	unusedOccurrence, unusedDelivery, unusedJob := invitationLifecycleMailFixture(t, unsent.ID, unsentInviter.ID,
		model.MailTemplateAccessInvitationRevoked, model.JobTypeMailDeliver, model.NowUTC(), model.NowUTC().Add(24*time.Hour))
	unsentAudit := saveInvitationLifecycleAuditAttempt(t, ctx, ss, unsentInviter.ID, unsentClass.ID.String(), "invitation.revoke")
	unsentRevoked, err := ss.Invitation().Revoke(ctx, &store.InvitationRevocation{ID: unsent.ID,
		ExpectedRevision: unsent.Revision, ActorUserID: unsentInviter.ID,
		RevocationNotice: &store.PreparedMail{Occurrence: unusedOccurrence, Delivery: unusedDelivery, Job: unusedJob},
		AuditEventID:     unsentAudit.ID.String(), AuditAt: model.MillisFromTime(model.NowUTC())})
	requireNoError(t, err)
	if unsentRevoked.Invitation.State != model.InvitationRevoked || unsentRevoked.Delivery != nil {
		t.Fatalf("Revoke() before SMTP acceptance = %#v", unsentRevoked)
	}
	if _, err = ss.Mail().GetDelivery(ctx, unusedDelivery.ID); !store.IsNotFound(err) {
		t.Fatalf("unneeded revocation delivery = %v", err)
	}
	unsentObsolete, err := ss.Mail().GetDelivery(ctx, unsentIssue.Delivery.ID)
	requireNoError(t, err)
	if unsentObsolete.State != model.MailDeliverySuppressed || len(unsentObsolete.EncryptedPayload) != 0 {
		t.Fatalf("revoked unsent credential delivery = %#v", unsentObsolete)
	}

	replacementFixture, replacementClass, replacementInviter, replacementAt := invitationAdministrationFixture(t, ctx, ss, "replacement")
	replacementIssue := studentClassInvitationIssueFixture(t, ss, replacementInviter, replacementClass, replacementFixture.period, replacementAt)
	replacementIssue.Invitation.TargetEmail = "replace-current-" + model.NewId() + "@example.edu"
	requireNoError(t, replacementIssue.Invitation.Validate())
	current, err := ss.Invitation().IssueStudentClass(ctx, replacementIssue)
	requireNoError(t, err)
	samePackage := *current
	samePackage.ID, samePackage.ClaimHash = model.NewInvitationID(), model.HashInvitationClaim(model.NewCredentialToken())
	samePackage.CreatedAt, samePackage.UpdatedAt = model.NowUTC(), model.NowUTC()
	samePackage.ExpiresAt, samePackage.Revision = samePackage.CreatedAt.Add(model.InvitationLifetime), 1
	requireNoError(t, samePackage.Validate())
	sameOccurrence, sameDelivery, sameJob := invitationLifecycleMailFixture(t, samePackage.ID, replacementInviter.ID,
		model.MailTemplateAccessStudentClassInvitation, model.JobTypeMailDeliverCredential, model.NowUTC(), samePackage.ExpiresAt)
	sameCurrentAudit := saveInvitationLifecycleAuditAttempt(t, ctx, ss, replacementInviter.ID, replacementClass.ID.String(), "invitation.supersede")
	sameReplacementAudit := saveInvitationLifecycleAuditAttempt(t, ctx, ss, replacementInviter.ID, replacementClass.ID.String(), "invitation.replacement_issue")
	_, err = ss.Invitation().Replace(ctx, &store.InvitationReplacement{CurrentID: current.ID,
		ExpectedCurrentRevision: current.Revision, Replacement: &samePackage, Lifetime: model.InvitationLifetime,
		Occurrence: sameOccurrence, Delivery: sameDelivery, DeliveryJob: sameJob, ActorUserID: replacementInviter.ID,
		CurrentAuditEventID: sameCurrentAudit.ID.String(), ReplacementAuditEventID: sameReplacementAudit.ID.String(),
		AuditAt: model.MillisFromTime(model.NowUTC())})
	if !store.IsConflict(err) {
		t.Fatalf("unchanged Replace() error = %v", err)
	}
	if _, err = ss.Mail().GetDelivery(ctx, sameDelivery.ID); !store.IsNotFound(err) {
		t.Fatalf("unchanged replacement delivery = %v", err)
	}
	replacementTargetClass := saveClass(t, ctx, ss, replacementFixture.level.ID.String(), replacementFixture.period.ID.String(), "invitation-admin-replacement-target")
	targetRole, err := ss.Role().Save(ctx, &model.Role{Name: "invitation-replacement-target-" + model.NewId(), DisplayName: "Invitation replacement target",
		Permissions: []string{string(model.ActionInvitationCreate), string(model.ActionClassMembersManage)}})
	requireNoError(t, err)
	_, err = ss.RoleBinding().Save(ctx, &model.RoleBinding{UserID: replacementInviter.ID, RoleID: targetRole.ID,
		ScopeType: model.RoleScopeClass, ScopeID: replacementTargetClass.ID.String(), StartsAt: replacementAt.Add(-time.Second)})
	requireNoError(t, err)
	candidate, err := model.NewStudentClassInvitation(model.StudentClassInvitationInput{ID: model.NewInvitationID(),
		TargetEmail: "replacement-" + model.NewId() + "@example.edu", ClassID: replacementTargetClass.ID,
		AcademicPeriodID: replacementFixture.period.ID, IntendedStartsAt: replacementFixture.period.StartsAt,
		IntendedEndsAt: model.OptionalTimeFrom(replacementFixture.period.EndsAt), InviterUserID: replacementInviter.ID,
		ScopeType: model.RoleScopeClass, ScopeID: replacementTargetClass.ID.String(),
		ClaimHash: model.HashInvitationClaim(model.NewCredentialToken()), IssuedAt: model.NowUTC()})
	requireNoError(t, err)
	replacementOccurrence, replacementDelivery, replacementJob := invitationLifecycleMailFixture(t, candidate.ID, replacementInviter.ID,
		model.MailTemplateAccessStudentClassInvitation, model.JobTypeMailDeliverCredential, model.NowUTC(), candidate.ExpiresAt)
	supersedeAudit := saveInvitationLifecycleAuditAttempt(t, ctx, ss, replacementInviter.ID, replacementClass.ID.String(), "invitation.supersede")
	replacementAudit := saveInvitationLifecycleAuditAttempt(t, ctx, ss, replacementInviter.ID, replacementTargetClass.ID.String(), "invitation.replacement_issue")
	replaced, err := ss.Invitation().Replace(ctx, &store.InvitationReplacement{CurrentID: current.ID,
		ExpectedCurrentRevision: current.Revision, Replacement: candidate, Lifetime: model.InvitationLifetime,
		Occurrence: replacementOccurrence, Delivery: replacementDelivery, DeliveryJob: replacementJob, ActorUserID: replacementInviter.ID,
		CurrentAuditEventID: supersedeAudit.ID.String(), ReplacementAuditEventID: replacementAudit.ID.String(),
		AuditAt: model.MillisFromTime(model.NowUTC())})
	requireNoError(t, err)
	if replaced.Invitation.ID != candidate.ID || replaced.Invitation.State != model.InvitationPending || replaced.Delivery == nil {
		t.Fatalf("Replace() = %#v", replaced)
	}
	superseded, err := ss.Invitation().Get(ctx, current.ID)
	requireNoError(t, err)
	if superseded.State != model.InvitationSuperseded || superseded.Revision != current.Revision+1 {
		t.Fatalf("superseded Invitation = %#v", superseded)
	}
	supersedeEvent, err := ss.Audit().Get(ctx, supersedeAudit.ID.String())
	requireNoError(t, err)
	replacementEvent, err := ss.Audit().Get(ctx, replacementAudit.ID.String())
	requireNoError(t, err)
	if supersedeEvent.ScopeID != replacementClass.ID.String() || strings.Contains(string(supersedeEvent.Result), replacementTargetClass.ID.String()) ||
		replacementEvent.ScopeID != replacementTargetClass.ID.String() || strings.Contains(string(replacementEvent.Result), replacementClass.ID.String()) {
		t.Fatalf("cross-scope replacement audits = %#v / %#v", supersedeEvent, replacementEvent)
	}
	oldReplacementDelivery, err := ss.Mail().GetDelivery(ctx, replacementIssue.Delivery.ID)
	requireNoError(t, err)
	if oldReplacementDelivery.State != model.MailDeliverySuppressed || len(oldReplacementDelivery.EncryptedPayload) != 0 {
		t.Fatalf("superseded Invitation delivery = %#v", oldReplacementDelivery)
	}
}

func invitationAdministrationFixture(t *testing.T, ctx context.Context, ss store.Store, suffix string) (classFixture, *model.Class, *model.User, time.Time) {
	t.Helper()
	fixture := saveClassFixture(t, ctx, ss)
	class := saveClass(t, ctx, ss, fixture.level.ID.String(), fixture.period.ID.String(), "invitation-admin-"+suffix)
	inviter := saveUser(t, ctx, ss)
	role, err := ss.Role().Save(ctx, &model.Role{Name: "invitation-admin-" + suffix + "-" + model.NewId(), DisplayName: "Invitation administrator",
		Permissions: []string{string(model.ActionInvitationCreate), string(model.ActionInvitationManage), string(model.ActionClassMembersManage)}})
	requireNoError(t, err)
	at := model.NowUTC().Add(-time.Minute)
	_, err = ss.RoleBinding().Save(ctx, &model.RoleBinding{UserID: inviter.ID, RoleID: role.ID,
		ScopeType: model.RoleScopeClass, ScopeID: class.ID.String(), StartsAt: at.Add(-time.Second)})
	requireNoError(t, err)
	return fixture, class, inviter, at
}

func invitationLifecycleMailFixture(t *testing.T, invitationID model.InvitationID, actor model.UserID,
	key model.MailTemplateKey, jobType model.JobType, at, deadline time.Time,
) (*model.MailOccurrence, *model.MailDelivery, *model.Job) {
	t.Helper()
	occurrenceID, deliveryID := model.NewMailOccurrenceID(), model.NewMailDeliveryID()
	command, err := model.EncodeMailDeliveryCommand(model.MailDeliveryCommandV1{DeliveryID: deliveryID})
	requireNoError(t, err)
	job, err := model.NewJob(model.NewJobID(), jobType, 1, command, deliveryID.String(), at, at, model.MailMaximumAttempts)
	requireNoError(t, err)
	occurrence := &model.MailOccurrence{ID: occurrenceID, Kind: model.MailOccurrenceInvitation, TemplateKey: key, ActorUserID: actor, CreatedAt: at}
	delivery := &model.MailDelivery{ID: deliveryID, OccurrenceID: occurrenceID, JobID: job.ID, TargetInvitationID: invitationID,
		TemplateKey: key, TemplateDigest: strings.Repeat("d", 64), MaskedRecipient: "i***@example.edu",
		State: model.MailDeliveryQueued, CreatedAt: at, UpdatedAt: at, MessageDate: at, Deadline: deadline,
		MessageID:        "<invitation-lifecycle." + deliveryID.String() + "@example.test>",
		EncryptedPayload: json.RawMessage(`{"version":1,"key_id":"11111111111111111111111111111111","ciphertext":"lifecycle"}`), Revision: 1}
	requireNoError(t, occurrence.Validate())
	requireNoError(t, delivery.Validate())
	return occurrence, delivery, job
}

func saveInvitationLifecycleAuditAttempt(t *testing.T, ctx context.Context, ss store.Store, actor model.UserID, classID, action string) *model.AuditEvent {
	t.Helper()
	audit, err := ss.Audit().Save(ctx, &model.AuditEvent{ActorID: actor, Action: action,
		Resource: model.Resource{Type: model.ResourceClass, ID: classID}, ScopeType: model.RoleScopeClass, ScopeID: classID,
		Status: model.AuditStatusAttempt, NodeID: "invitation-lifecycle-store-test"})
	requireNoError(t, err)
	return audit
}

func testScopedRoleAcceptSerializesWithRoleArchive(t *testing.T, ss store.Store,
	archive func(*testing.T, context.Context, *model.Role, func() error) error,
) {
	ctx := context.Background()
	invitation, _, targetRole, acceptance := scopedRoleInvitationRaceFixture(t, ctx, ss, "role-archive")
	err := archive(t, ctx, targetRole, func() error {
		_, acceptErr := ss.Invitation().AcceptScopedRole(ctx, acceptance)
		return acceptErr
	})
	if !store.IsConflict(err) {
		t.Fatalf("AcceptScopedRole() concurrent Role archive error = %v", err)
	}
	current, getErr := ss.Invitation().Get(ctx, invitation.ID)
	requireNoError(t, getErr)
	if current.State != model.InvitationPending {
		t.Fatalf("scoped Role Invitation state after Role archive = %q", current.State)
	}
}

func testScopedRoleAcceptSerializesWithInviterBindingEnd(t *testing.T, ss store.Store,
	end func(*testing.T, context.Context, *model.RoleBinding, func() error) error,
) {
	ctx := context.Background()
	invitation, issuerBinding, _, acceptance := scopedRoleInvitationRaceFixture(t, ctx, ss, "binding-end")
	err := end(t, ctx, issuerBinding, func() error {
		_, acceptErr := ss.Invitation().AcceptScopedRole(ctx, acceptance)
		return acceptErr
	})
	if !store.IsConflict(err) {
		t.Fatalf("AcceptScopedRole() concurrent inviter binding end error = %v", err)
	}
	current, getErr := ss.Invitation().Get(ctx, invitation.ID)
	requireNoError(t, getErr)
	if current.State != model.InvitationPending {
		t.Fatalf("scoped Role Invitation state after binding end = %q", current.State)
	}
}

func scopedRoleInvitationRaceFixture(t *testing.T, ctx context.Context, ss store.Store, suffix string) (*model.Invitation, *model.RoleBinding, *model.Role, *store.ScopedRoleInvitationAcceptance) {
	t.Helper()
	unit, _ := saveProgrammeParents(t, ctx, ss, "scoped-role-race-"+suffix+model.NewId())
	inviter, existing := saveUser(t, ctx, ss), saveUser(t, ctx, ss)
	at := model.NowUTC().Add(-time.Minute)
	issuerRole, err := ss.Role().Save(ctx, &model.Role{Name: "scoped-role-race-issuer-" + model.NewId(), DisplayName: "Scoped Role Race Issuer",
		Permissions: []string{string(model.ActionInvitationCreate), string(model.ActionRoleBindingManage), string(model.ActionAcademicAuditView)}})
	requireNoError(t, err)
	issuerBinding, err := ss.RoleBinding().Save(ctx, &model.RoleBinding{UserID: inviter.ID, RoleID: issuerRole.ID,
		ScopeType: model.RoleScopeAcademicUnit, ScopeID: unit.ID.String(), StartsAt: at.Add(-time.Second)})
	requireNoError(t, err)
	targetRole, err := ss.Role().Save(ctx, &model.Role{Name: "scoped-role-race-target-" + model.NewId(), DisplayName: "Scoped Role Race Target",
		Permissions: []string{string(model.ActionAcademicAuditView)}})
	requireNoError(t, err)
	_, err = ss.RoleBinding().Save(ctx, &model.RoleBinding{UserID: inviter.ID, RoleID: targetRole.ID,
		ScopeType: model.RoleScopeInstitution, ScopeID: unit.InstitutionID.String(), StartsAt: at.Add(-time.Second)})
	requireNoError(t, err)
	candidate, err := model.NewScopedRoleInvitation(model.ScopedRoleInvitationInput{ID: model.NewInvitationID(),
		Purpose: model.InvitationPurposeAcademicUnitRole, TargetEmail: "scoped-role-race-" + model.NewId() + "@example.edu",
		AcademicUnitID: unit.ID, RoleID: targetRole.ID, RoleActions: targetRole.Permissions, IntendedStartsAt: at,
		InviterUserID: inviter.ID, ScopeType: model.RoleScopeAcademicUnit, ScopeID: unit.ID.String(),
		ClaimHash: model.HashInvitationClaim(model.NewCredentialToken()), IssuedAt: at})
	requireNoError(t, err)
	invitation, err := ss.Invitation().IssueScopedRole(ctx, scopedRoleInvitationIssueFixture(t, ss, candidate))
	requireNoError(t, err)
	session, _, _ := saveSession(t, ctx, ss, existing.ID.String(), 10)
	acceptance := scopedRoleInvitationAcceptanceFixture(t, ctx, ss, invitation, existing.ID, session.ID, model.NowUTC())
	return invitation, issuerBinding, targetRole, acceptance
}

func testInstitutionRoleInvitationExistingUserAtomicAndReplaySafe(t *testing.T, ss store.Store) {
	ctx := context.Background()
	unit, _ := saveProgrammeParents(t, ctx, ss, "institution-role-"+model.NewId())
	inviter, existing := saveUser(t, ctx, ss), saveUser(t, ctx, ss)
	issuedAt := model.NowUTC().Add(-time.Minute)
	issuerRole, err := ss.Role().Save(ctx, &model.Role{Name: "institution-role-issuer-" + model.NewId(), DisplayName: "Institution Role Issuer",
		Permissions: []string{string(model.ActionInvitationCreate), string(model.ActionRoleBindingManage), string(model.ActionAuditView)}})
	requireNoError(t, err)
	_, err = ss.RoleBinding().Save(ctx, &model.RoleBinding{UserID: inviter.ID, RoleID: issuerRole.ID,
		ScopeType: model.RoleScopeInstitution, ScopeID: unit.InstitutionID.String(), StartsAt: issuedAt.Add(-time.Second)})
	requireNoError(t, err)
	targetRole, err := ss.Role().Save(ctx, &model.Role{Name: "institution-role-target-" + model.NewId(), DisplayName: "Institution Role Target",
		Permissions: []string{string(model.ActionAuditView)}})
	requireNoError(t, err)
	invitation, err := model.NewScopedRoleInvitation(model.ScopedRoleInvitationInput{ID: model.NewInvitationID(),
		Purpose: model.InvitationPurposeInstitutionRole, TargetEmail: "institution-invited-" + model.NewId() + "@example.edu",
		RoleID: targetRole.ID, RoleActions: targetRole.Permissions, IntendedStartsAt: issuedAt,
		InviterUserID: inviter.ID, ScopeType: model.RoleScopeInstitution, ScopeID: unit.InstitutionID.String(),
		ClaimHash: model.HashInvitationClaim(model.NewCredentialToken()), IssuedAt: issuedAt})
	requireNoError(t, err)
	issue := scopedRoleInvitationIssueFixture(t, ss, invitation)
	created, err := ss.Invitation().IssueScopedRole(ctx, issue)
	requireNoError(t, err)
	session, _, _ := saveSession(t, ctx, ss, existing.ID.String(), 10)
	acceptances := []*store.ScopedRoleInvitationAcceptance{
		scopedRoleInvitationAcceptanceFixture(t, ctx, ss, created, existing.ID, session.ID, model.NowUTC()),
		scopedRoleInvitationAcceptanceFixture(t, ctx, ss, created, existing.ID, session.ID, model.NowUTC()),
	}
	results := make([]*store.ScopedRoleInvitationAcceptanceResult, 2)
	errors := make([]error, 2)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for index := range results {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			results[index], errors[index] = ss.Invitation().AcceptScopedRole(ctx, acceptances[index])
		}(index)
	}
	close(start)
	wait.Wait()
	for _, acceptErr := range errors {
		requireNoError(t, acceptErr)
	}
	for _, acceptance := range acceptances {
		requireScopedRoleAcceptanceAuditSuccess(t, ctx, ss, acceptance)
	}
	accepted := results[0]
	if accepted.Replayed {
		accepted = results[1]
	}
	if results[0].Replayed == results[1].Replayed {
		t.Fatalf("concurrent scoped Role replay flags = %v / %v", results[0].Replayed, results[1].Replayed)
	}
	if accepted.Replayed || accepted.User.ID != existing.ID || accepted.RoleBinding.RoleID != targetRole.ID ||
		accepted.RoleBinding.ScopeType != model.RoleScopeInstitution || accepted.RoleBinding.ScopeID != unit.InstitutionID.String() {
		t.Fatalf("institution AcceptScopedRole() = %#v", accepted)
	}
	replayAcceptance := scopedRoleInvitationAcceptanceFixture(t, ctx, ss, created, existing.ID, session.ID, model.NowUTC())
	replayed, err := ss.Invitation().AcceptScopedRole(ctx, replayAcceptance)
	requireNoError(t, err)
	requireScopedRoleAcceptanceAuditSuccess(t, ctx, ss, replayAcceptance)
	if !replayed.Replayed || replayed.RoleBinding.ID != accepted.RoleBinding.ID {
		t.Fatalf("institution replay = %#v", replayed)
	}
}

func testScopedRoleInvitationExistingUserAtomicAndReplaySafe(t *testing.T, ss store.Store) {
	ctx := context.Background()
	unit, _ := saveProgrammeParents(t, ctx, ss, "scoped-role-"+model.NewId())
	inviter, existing, other := saveUser(t, ctx, ss), saveUser(t, ctx, ss), saveUser(t, ctx, ss)
	issuedAt := model.NowUTC().Add(-time.Minute)
	issuerRole, err := ss.Role().Save(ctx, &model.Role{Name: "scoped-role-issuer-" + model.NewId(), DisplayName: "Scoped Role Issuer",
		Permissions: []string{string(model.ActionInvitationCreate), string(model.ActionRoleBindingManage), string(model.ActionAcademicAuditView)}})
	requireNoError(t, err)
	_, err = ss.RoleBinding().Save(ctx, &model.RoleBinding{UserID: inviter.ID, RoleID: issuerRole.ID,
		ScopeType: model.RoleScopeAcademicUnit, ScopeID: unit.ID.String(), StartsAt: issuedAt.Add(-time.Second)})
	requireNoError(t, err)
	targetRole, err := ss.Role().Save(ctx, &model.Role{Name: "scoped-role-target-" + model.NewId(), DisplayName: "Scoped Role Target",
		Permissions: []string{string(model.ActionAcademicAuditView)}})
	requireNoError(t, err)
	invitation, err := model.NewScopedRoleInvitation(model.ScopedRoleInvitationInput{ID: model.NewInvitationID(),
		Purpose: model.InvitationPurposeAcademicUnitRole, TargetEmail: "invited-" + model.NewId() + "@example.edu",
		AcademicUnitID: unit.ID, RoleID: targetRole.ID, RoleActions: targetRole.Permissions, IntendedStartsAt: issuedAt,
		InviterUserID: inviter.ID, ScopeType: model.RoleScopeAcademicUnit, ScopeID: unit.ID.String(),
		ClaimHash: model.HashInvitationClaim(model.NewCredentialToken()), IssuedAt: issuedAt})
	requireNoError(t, err)
	issue := scopedRoleInvitationIssueFixture(t, ss, invitation)
	created, err := ss.Invitation().IssueScopedRole(ctx, issue)
	requireNoError(t, err)
	if created.Purpose != model.InvitationPurposeAcademicUnitRole || created.RoleID != targetRole.ID {
		t.Fatalf("IssueScopedRole() = %#v", created)
	}
	existingBinding, err := ss.RoleBinding().Save(ctx, &model.RoleBinding{UserID: existing.ID, RoleID: targetRole.ID,
		ScopeType: model.RoleScopeAcademicUnit, ScopeID: unit.ID.String(), StartsAt: issuedAt.Add(-time.Second)})
	requireNoError(t, err)
	session, _, _ := saveSession(t, ctx, ss, existing.ID.String(), 10)
	acceptance := scopedRoleInvitationAcceptanceFixture(t, ctx, ss, created, existing.ID, session.ID, model.NowUTC())
	missingAttempt := *acceptance
	missingAttempt.AuditEventID = model.NewAuditEventID().String()
	if _, err = ss.Invitation().AcceptScopedRole(ctx, &missingAttempt); err == nil {
		t.Fatal("AcceptScopedRole() without persisted attempt error = nil")
	}
	stillPending, err := ss.Invitation().Get(ctx, created.ID)
	requireNoError(t, err)
	if stillPending.State != model.InvitationPending {
		t.Fatalf("Invitation state after missing audit attempt = %q", stillPending.State)
	}
	accepted, err := ss.Invitation().AcceptScopedRole(ctx, acceptance)
	requireNoError(t, err)
	requireScopedRoleAcceptanceAuditSuccess(t, ctx, ss, acceptance)
	if accepted.Replayed || accepted.User.ID != existing.ID || accepted.User.Email != existing.Email ||
		accepted.RoleBinding.ID != existingBinding.ID || accepted.RoleBinding.UserID != existing.ID || accepted.RoleBinding.RoleID != targetRole.ID ||
		accepted.RoleBinding.ScopeType != model.RoleScopeAcademicUnit || accepted.RoleBinding.ScopeID != unit.ID.String() ||
		accepted.Invitation.AcceptedAffiliationID.IsValid() || accepted.Invitation.AcceptedAcademicUnitMemberID.IsValid() {
		t.Fatalf("AcceptScopedRole() = %#v", accepted)
	}
	obsolete, err := ss.Mail().GetDelivery(ctx, issue.Delivery.ID)
	requireNoError(t, err)
	if obsolete.State != model.MailDeliverySuppressed || obsolete.PublicFailureCode != model.MailDeliveryObsoleteCode || len(obsolete.EncryptedPayload) != 0 {
		t.Fatalf("accepted scoped Role credential delivery = %#v", obsolete)
	}
	replayAcceptance := scopedRoleInvitationAcceptanceFixture(t, ctx, ss, created, existing.ID, session.ID, model.NowUTC())
	replayed, err := ss.Invitation().AcceptScopedRole(ctx, replayAcceptance)
	requireNoError(t, err)
	requireScopedRoleAcceptanceAuditSuccess(t, ctx, ss, replayAcceptance)
	if !replayed.Replayed || replayed.User.ID != existing.ID || replayed.RoleBinding.ID != accepted.RoleBinding.ID {
		t.Fatalf("replayed AcceptScopedRole() = %#v", replayed)
	}
	otherSession, _, _ := saveSession(t, ctx, ss, other.ID.String(), 10)
	differentUser := scopedRoleInvitationAcceptanceFixture(t, ctx, ss, created, other.ID, otherSession.ID, model.NowUTC())
	if _, err = ss.Invitation().AcceptScopedRole(ctx, differentUser); !store.IsConflict(err) {
		t.Fatalf("different User replay error = %v", err)
	}
}

func scopedRoleInvitationIssueFixture(t *testing.T, ss store.Store, invitation *model.Invitation) *store.ScopedRoleInvitationIssue {
	t.Helper()
	key := model.MailTemplateAccessAcademicUnitRoleInvitation
	if invitation.Purpose == model.InvitationPurposeInstitutionRole {
		key = model.MailTemplateAccessInstitutionRoleInvitation
	}
	at := invitation.CreatedAt
	occurrenceID, deliveryID, jobID := model.NewMailOccurrenceID(), model.NewMailDeliveryID(), model.NewJobID()
	command, err := model.EncodeMailDeliveryCommand(model.MailDeliveryCommandV1{DeliveryID: deliveryID})
	requireNoError(t, err)
	job, err := model.NewJob(jobID, model.JobTypeMailDeliverCredential, 1, command, deliveryID.String(), at, at, model.MailMaximumAttempts)
	requireNoError(t, err)
	attempt, err := ss.Audit().Save(context.Background(), &model.AuditEvent{ActorID: invitation.InviterUserID,
		Action: string(model.ActionInvitationCreate), Resource: model.Resource{Type: resourceTypeForScopedInvitation(invitation), ID: invitation.ScopeID},
		ScopeType: invitation.ScopeType, ScopeID: invitation.ScopeID, Status: model.AuditStatusAttempt, NodeID: "scoped-role-invitation-store-test"})
	requireNoError(t, err)
	return &store.ScopedRoleInvitationIssue{Invitation: invitation, Lifetime: model.InvitationLifetime,
		Occurrence: &model.MailOccurrence{ID: occurrenceID, Kind: model.MailOccurrenceInvitation, TemplateKey: key,
			ActorUserID: invitation.InviterUserID, CreatedAt: at},
		Delivery: &model.MailDelivery{ID: deliveryID, OccurrenceID: occurrenceID, JobID: jobID, TargetInvitationID: invitation.ID,
			TemplateKey: key, TemplateDigest: strings.Repeat("d", 64), MaskedRecipient: "i***@example.edu",
			State: model.MailDeliveryQueued, CreatedAt: at, UpdatedAt: at, MessageDate: at, Deadline: invitation.ExpiresAt,
			MessageID:        "<scoped-role." + deliveryID.String() + "@example.test>",
			EncryptedPayload: json.RawMessage(`{"version":1,"key_id":"11111111111111111111111111111111","ciphertext":"secret"}`), Revision: 1},
		DeliveryJob: job, AuditEventID: attempt.ID.String(), AuditAt: model.MillisFromTime(at)}
}

func scopedRoleInvitationAcceptanceFixture(t *testing.T, ctx context.Context, ss store.Store, invitation *model.Invitation,
	userID model.UserID, sessionID model.SessionID, at time.Time,
) *store.ScopedRoleInvitationAcceptance {
	t.Helper()
	binding := &model.RoleBinding{UserID: userID, RoleID: invitation.RoleID, OriginInvitationID: invitation.ID,
		ScopeType: invitation.ScopeType, ScopeID: invitation.ScopeID, StartsAt: invitation.EffectiveStartsAt(at), EndsAt: invitation.IntendedEndsAt}
	binding.PrepareCreate(model.NewRoleBindingID(), at)
	attempt, err := ss.Audit().Save(ctx, &model.AuditEvent{ActorID: userID, SessionID: sessionID,
		Action: "invitation.accept", Resource: model.Resource{Type: resourceTypeForScopedInvitation(invitation), ID: invitation.ScopeID},
		ScopeType: invitation.ScopeType, ScopeID: invitation.ScopeID, Status: model.AuditStatusAttempt,
		NodeID: "scoped-role-invitation-store-test"})
	requireNoError(t, err)
	return &store.ScopedRoleInvitationAcceptance{ClaimHash: invitation.ClaimHash, UserID: userID, RoleBinding: binding,
		AuditEventID: attempt.ID.String(), AuditAt: model.MillisFromTime(at),
		RequiredActions: []model.Action{model.ActionInvitationCreate, model.ActionRoleBindingManage}}
}

func requireScopedRoleAcceptanceAuditSuccess(t *testing.T, ctx context.Context, ss store.Store, input *store.ScopedRoleInvitationAcceptance) {
	t.Helper()
	event, err := ss.Audit().Get(ctx, input.AuditEventID)
	requireNoError(t, err)
	if event.Status != model.AuditStatusSuccess || len(event.Result) == 0 ||
		strings.Contains(string(event.Result), input.ClaimHash) {
		t.Fatalf("scoped Role acceptance audit = %#v", event)
	}
}

func resourceTypeForScopedInvitation(invitation *model.Invitation) model.ResourceType {
	if invitation.Purpose == model.InvitationPurposeInstitutionRole {
		return model.ResourceInstitution
	}
	return model.ResourceAcademicUnit
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
