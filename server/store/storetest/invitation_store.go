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
	return &store.ScopedRoleInvitationIssue{Invitation: invitation, Lifetime: model.StudentClassInvitationLifetime,
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
