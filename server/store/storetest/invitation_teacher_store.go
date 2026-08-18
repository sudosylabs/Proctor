// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestTeacherAcademicUnitInvitationStore(t *testing.T, ss store.Store, probe InvitationSQLProbe) {
	t.Run("IssueFreezesPackageAndMailAtomically", func(t *testing.T) {
		testTeacherAcademicUnitInvitationIssue(t, ss)
	})
	t.Run("AcceptCreatesExactPackageAndReplaysOriginalOutcome", func(t *testing.T) {
		testTeacherAcademicUnitInvitationAcceptance(t, ss)
	})
	t.Run("CompatibleRelationshipsMustCoverFrozenInterval", func(t *testing.T) {
		testTeacherAcademicUnitInvitationIntervalCoverage(t, ss)
	})
	if probe.ArchiveTeacherUnitBeforeAccept != nil {
		t.Run("AcceptSerializesWithAcademicUnitArchive", func(t *testing.T) {
			testTeacherInvitationSerializesWithUnitArchive(t, ss, probe.ArchiveTeacherUnitBeforeAccept)
		})
	}
	if probe.ArchiveTeacherUnitBeforeMail != nil {
		t.Run("CredentialStartSerializesWithAcademicUnitArchive", func(t *testing.T) {
			testTeacherInvitationMailSerializesWithUnitArchive(t, ss, probe.ArchiveTeacherUnitBeforeMail)
		})
	}
	if probe.MutateTeacherRoleBeforeMail != nil {
		t.Run("CredentialStartSerializesWithRoleMutation", func(t *testing.T) {
			testTeacherInvitationMailSerializesWithRoleMutation(t, ss, probe.MutateTeacherRoleBeforeMail)
		})
	}
}

func testTeacherAcademicUnitInvitationIntervalCoverage(t *testing.T, ss store.Store) {
	t.Helper()
	ctx := context.Background()
	unit, _ := saveProgrammeParents(t, ctx, ss, "teacher-invitation-coverage")
	inviter := saveUser(t, ctx, ss)
	packageRole, err := ss.Role().Save(ctx, &model.Role{Name: "teacher-coverage-package-" + model.NewId(), DisplayName: "Teacher Coverage Package",
		Permissions: []string{string(model.ActionAcademicUnitView)}})
	requireNoError(t, err)
	authorityRole, err := ss.Role().Save(ctx, &model.Role{Name: "teacher-coverage-authority-" + model.NewId(), DisplayName: "Teacher Coverage Authority",
		Permissions: []string{string(model.ActionInvitationCreate), string(model.ActionAcademicUnitMembersManage), string(model.ActionAcademicUnitView)}})
	requireNoError(t, err)
	issuedAt := model.NowUTC().Add(-time.Minute)
	_, err = ss.RoleBinding().Save(ctx, &model.RoleBinding{UserID: inviter.ID, RoleID: authorityRole.ID,
		ScopeType: model.RoleScopeInstitution, ScopeID: unit.InstitutionID.String(), StartsAt: issuedAt.Add(-time.Second)})
	requireNoError(t, err)

	firstIssue := teacherAcademicUnitInvitationIssueFixture(t, ss, inviter, unit, packageRole, issuedAt)
	firstIssue.Invitation.TargetEmail = "teacher-coverage@example.edu"
	firstIssue.Invitation.Suggestions.Username = "teacher-coverage-" + model.NewId()
	firstIssue.Invitation.IntendedEndsAt = model.OptionalTimeFrom(issuedAt.Add(4 * time.Hour))
	first, err := ss.Invitation().IssueTeacherAcademicUnit(ctx, firstIssue)
	requireNoError(t, err)
	firstAccepted, err := ss.Invitation().AcceptTeacherAcademicUnit(ctx, teacherAcademicUnitInvitationAcceptanceFixture(t, first, issuedAt.Add(time.Minute)))
	requireNoError(t, err)

	coveredIssue := teacherAcademicUnitInvitationIssueFixture(t, ss, inviter, unit, packageRole, issuedAt.Add(time.Second))
	coveredIssue.Invitation.TargetEmail = first.TargetEmail
	coveredIssue.Invitation.IntendedEndsAt = model.OptionalTimeFrom(issuedAt.Add(3 * time.Hour))
	covered, err := ss.Invitation().IssueTeacherAcademicUnit(ctx, coveredIssue)
	requireNoError(t, err)
	coveredAccepted, err := ss.Invitation().AcceptTeacherAcademicUnit(ctx, teacherAcademicUnitInvitationAcceptanceFixture(t, covered, issuedAt.Add(2*time.Minute)))
	requireNoError(t, err)
	if coveredAccepted.AcademicUnitMember.ID != firstAccepted.AcademicUnitMember.ID || coveredAccepted.RoleBinding.ID != firstAccepted.RoleBinding.ID {
		t.Fatalf("bounded compatible package did not reuse covering relationships: %#v / %#v", firstAccepted, coveredAccepted)
	}

	unboundedRole, err := ss.Role().Save(ctx, &model.Role{Name: "teacher-unbounded-package-" + model.NewId(), DisplayName: "Unbounded Teacher Package",
		Permissions: []string{string(model.ActionAcademicUnitView)}})
	requireNoError(t, err)
	unboundedIssue := teacherAcademicUnitInvitationIssueFixture(t, ss, inviter, unit, unboundedRole, issuedAt.Add(2*time.Second))
	unboundedIssue.Invitation.TargetEmail = first.TargetEmail
	unbounded, err := ss.Invitation().IssueTeacherAcademicUnit(ctx, unboundedIssue)
	requireNoError(t, err)
	if _, err = ss.Invitation().AcceptTeacherAcademicUnit(ctx, teacherAcademicUnitInvitationAcceptanceFixture(t, unbounded, issuedAt.Add(3*time.Minute))); !store.IsConflict(err) {
		t.Fatalf("unbounded package reused finite membership: %v", err)
	}
	persisted, err := ss.Invitation().Get(ctx, unbounded.ID)
	requireNoError(t, err)
	if persisted.State != model.InvitationPending {
		t.Fatalf("under-covered package was consumed: %#v", persisted)
	}
}

func teacherInvitationMailFixture(t *testing.T, ss store.Store, suffix string) (context.Context, *model.AcademicUnit, *model.Role, *store.TeacherAcademicUnitInvitationIssue) {
	t.Helper()
	ctx := context.Background()
	parent, _ := saveProgrammeParents(t, ctx, ss, "teacher-mail-"+suffix)
	unit := saveAcademicUnit(t, ctx, ss, parent.InstitutionID.String(), parent.ID.String(), "teacher-mail-leaf-"+model.NewId())
	inviter := saveUser(t, ctx, ss)
	packageRole, err := ss.Role().Save(ctx, &model.Role{Name: "teacher-mail-package-" + model.NewId(), DisplayName: "Teacher Mail Package",
		Permissions: []string{string(model.ActionAcademicUnitView)}})
	requireNoError(t, err)
	authorityRole, err := ss.Role().Save(ctx, &model.Role{Name: "teacher-mail-authority-" + model.NewId(), DisplayName: "Teacher Mail Authority",
		Permissions: []string{string(model.ActionInvitationCreate), string(model.ActionAcademicUnitMembersManage), string(model.ActionAcademicUnitView)}})
	requireNoError(t, err)
	issuedAt := model.NowUTC().Add(-time.Minute)
	_, err = ss.RoleBinding().Save(ctx, &model.RoleBinding{UserID: inviter.ID, RoleID: authorityRole.ID,
		ScopeType: model.RoleScopeInstitution, ScopeID: unit.InstitutionID.String(), StartsAt: issuedAt.Add(-time.Second)})
	requireNoError(t, err)
	issue := teacherAcademicUnitInvitationIssueFixture(t, ss, inviter, unit, packageRole, issuedAt)
	issue.Invitation.TargetEmail = "teacher-mail-" + suffix + "@example.edu"
	_, err = ss.Invitation().IssueTeacherAcademicUnit(ctx, issue)
	requireNoError(t, err)
	return ctx, unit, packageRole, issue
}

func testTeacherInvitationMailSerializesWithUnitArchive(t *testing.T, ss store.Store, archive func(*testing.T, context.Context, *model.AcademicUnit, func() error) error) {
	t.Helper()
	ctx, unit, _, issue := teacherInvitationMailFixture(t, ss, "unit-race")
	err := archive(t, ctx, unit, func() error {
		_, startErr := ss.Mail().StartDelivery(ctx, issue.Delivery.ID, issue.Delivery.Revision, model.NowUTC())
		return startErr
	})
	requireNoError(t, err)
	assertObsoleteTeacherInvitationDelivery(t, ctx, ss, issue.Delivery.ID)
}

func testTeacherInvitationMailSerializesWithRoleMutation(t *testing.T, ss store.Store, mutate func(*testing.T, context.Context, *model.Role, func() error) error) {
	t.Helper()
	ctx, _, role, issue := teacherInvitationMailFixture(t, ss, "role-race")
	err := mutate(t, ctx, role, func() error {
		_, startErr := ss.Mail().StartDelivery(ctx, issue.Delivery.ID, issue.Delivery.Revision, model.NowUTC())
		return startErr
	})
	requireNoError(t, err)
	assertObsoleteTeacherInvitationDelivery(t, ctx, ss, issue.Delivery.ID)
}

func assertObsoleteTeacherInvitationDelivery(t *testing.T, ctx context.Context, ss store.Store, id model.MailDeliveryID) {
	t.Helper()
	delivery, err := ss.Mail().GetDelivery(ctx, id)
	requireNoError(t, err)
	if delivery.State != model.MailDeliverySuppressed || delivery.PublicFailureCode != model.MailDeliveryObsoleteCode || len(delivery.EncryptedPayload) != 0 {
		t.Fatalf("stale teacher Invitation credential delivery = %#v", delivery)
	}
}

func testTeacherInvitationSerializesWithUnitArchive(t *testing.T, ss store.Store, archive func(*testing.T, context.Context, *model.AcademicUnit, func() error) error) {
	t.Helper()
	ctx := context.Background()
	parent, _ := saveProgrammeParents(t, ctx, ss, "teacher-invitation-unit-race")
	unit := saveAcademicUnit(t, ctx, ss, parent.InstitutionID.String(), parent.ID.String(), "teacher-invitation-leaf-"+model.NewId())
	inviter := saveUser(t, ctx, ss)
	packageRole, err := ss.Role().Save(ctx, &model.Role{Name: "teacher-unit-race-package-" + model.NewId(), DisplayName: "Teacher Package",
		Permissions: []string{string(model.ActionAcademicUnitView)}})
	requireNoError(t, err)
	authorityRole, err := ss.Role().Save(ctx, &model.Role{Name: "teacher-unit-race-authority-" + model.NewId(), DisplayName: "Teacher Inviter",
		Permissions: []string{string(model.ActionInvitationCreate), string(model.ActionAcademicUnitMembersManage), string(model.ActionAcademicUnitView)}})
	requireNoError(t, err)
	issuedAt := model.NowUTC().Add(-time.Minute)
	_, err = ss.RoleBinding().Save(ctx, &model.RoleBinding{UserID: inviter.ID, RoleID: authorityRole.ID,
		ScopeType: model.RoleScopeInstitution, ScopeID: unit.InstitutionID.String(), StartsAt: issuedAt.Add(-time.Second)})
	requireNoError(t, err)
	issue := teacherAcademicUnitInvitationIssueFixture(t, ss, inviter, unit, packageRole, issuedAt)
	issue.Invitation.TargetEmail = "teacher-unit-race@example.edu"
	invitation, err := ss.Invitation().IssueTeacherAcademicUnit(ctx, issue)
	requireNoError(t, err)
	acceptance := teacherAcademicUnitInvitationAcceptanceFixture(t, invitation, issuedAt.Add(30*time.Second))
	err = archive(t, ctx, unit, func() error {
		_, acceptErr := ss.Invitation().AcceptTeacherAcademicUnit(ctx, acceptance)
		return acceptErr
	})
	if err == nil {
		t.Fatal("AcceptTeacherAcademicUnit() succeeded after concurrent Academic Unit archival")
	}
	persisted, getErr := ss.Invitation().Get(ctx, invitation.ID)
	requireNoError(t, getErr)
	if persisted.State != model.InvitationPending {
		t.Fatalf("failed teacher acceptance consumed Invitation: %#v", persisted)
	}
}

func testTeacherAcademicUnitInvitationIssue(t *testing.T, ss store.Store) {
	t.Helper()
	ctx := context.Background()
	unit, _ := saveProgrammeParents(t, ctx, ss, "teacher-invitation")
	inviter := saveUser(t, ctx, ss)
	packageRole, err := ss.Role().Save(ctx, &model.Role{Name: "teacher-package-" + model.NewId(), DisplayName: "Teacher Package",
		Permissions: []string{string(model.ActionAcademicUnitView), string(model.ActionProgrammeManage)}})
	requireNoError(t, err)
	authorityRole, err := ss.Role().Save(ctx, &model.Role{Name: "teacher-inviter-" + model.NewId(), DisplayName: "Teacher Inviter",
		Permissions: []string{string(model.ActionInvitationCreate), string(model.ActionAcademicUnitMembersManage),
			string(model.ActionAcademicUnitView), string(model.ActionProgrammeManage)}})
	requireNoError(t, err)
	issuedAt := model.NowUTC().Add(-time.Minute)
	_, err = ss.RoleBinding().Save(ctx, &model.RoleBinding{UserID: inviter.ID, RoleID: authorityRole.ID,
		ScopeType: model.RoleScopeInstitution, ScopeID: unit.InstitutionID.String(), StartsAt: issuedAt.Add(-time.Second)})
	requireNoError(t, err)
	input := teacherAcademicUnitInvitationIssueFixture(t, ss, inviter, unit, packageRole, issuedAt)
	created, err := ss.Invitation().IssueTeacherAcademicUnit(ctx, input)
	requireNoError(t, err)
	if created.ID != input.Invitation.ID || created.Purpose != model.InvitationPurposeTeacherAcademicUnit ||
		created.AcademicUnitID != unit.ID || created.RoleID != packageRole.ID ||
		!slices.Equal(created.RoleActions, []string{string(model.ActionAcademicUnitView), string(model.ActionProgrammeManage)}) {
		t.Fatalf("IssueTeacherAcademicUnit() = %#v", created)
	}
	stored, err := ss.Invitation().Get(ctx, created.ID)
	requireNoError(t, err)
	if stored.AcademicUnitID != unit.ID || stored.RoleID != packageRole.ID || !slices.Equal(stored.RoleActions, created.RoleActions) {
		t.Fatalf("stored teacher Invitation = %#v", stored)
	}
	delivery, err := ss.Mail().GetDelivery(ctx, input.Delivery.ID)
	requireNoError(t, err)
	if delivery.TemplateKey != model.MailTemplateAccessTeacherAcademicUnitInvitation || delivery.TargetInvitationID != created.ID {
		t.Fatalf("teacher Invitation delivery = %#v", delivery)
	}

	rollback := teacherAcademicUnitInvitationIssueFixture(t, ss, inviter, unit, packageRole, issuedAt.Add(time.Second))
	rollback.Invitation.TargetEmail = "rollback-teacher@example.edu"
	rollback.AuditEventID = model.NewAuditEventID().String()
	if _, err = ss.Invitation().IssueTeacherAcademicUnit(ctx, rollback); err == nil {
		t.Fatal("IssueTeacherAcademicUnit() succeeded without durable audit attempt")
	}
	if _, err = ss.Invitation().Get(ctx, rollback.Invitation.ID); !store.IsNotFound(err) {
		t.Fatalf("rolled-back teacher Invitation = %v", err)
	}
	protectedRole, err := ss.Role().Save(ctx, &model.Role{Name: "teacher-protected-" + model.NewId(), DisplayName: "Protected Package",
		Permissions: []string{string(model.ActionRoleManage)}})
	requireNoError(t, err)
	directAuthority, err := ss.Role().Save(ctx, &model.Role{Name: "teacher-direct-protected-" + model.NewId(), DisplayName: "Direct Protected Authority",
		Permissions: []string{string(model.ActionRoleManage)}})
	requireNoError(t, err)
	_, err = ss.RoleBinding().Save(ctx, &model.RoleBinding{UserID: inviter.ID, RoleID: directAuthority.ID,
		ScopeType: model.RoleScopeAcademicUnit, ScopeID: unit.ID.String(), StartsAt: issuedAt.Add(-time.Second)})
	requireNoError(t, err)
	protected := teacherAcademicUnitInvitationIssueFixture(t, ss, inviter, unit, protectedRole, issuedAt.Add(2*time.Second))
	protected.Invitation.TargetEmail = "protected-teacher@example.edu"
	if _, err = ss.Invitation().IssueTeacherAcademicUnit(ctx, protected); err == nil {
		t.Fatal("IssueTeacherAcademicUnit() delegated a protected action at the same Academic Unit scope")
	}
	unboundedLifetime := teacherAcademicUnitInvitationIssueFixture(t, ss, inviter, unit, packageRole, issuedAt.Add(3*time.Second))
	unboundedLifetime.Invitation.TargetEmail = "teacher-unbounded-lifetime@example.edu"
	unboundedLifetime.Lifetime = model.StudentClassInvitationLifetime + time.Hour
	var invalidInput *store.ErrInvalidInput
	if _, err = ss.Invitation().IssueTeacherAcademicUnit(ctx, unboundedLifetime); !errors.As(err, &invalidInput) {
		t.Fatalf("IssueTeacherAcademicUnit() unbounded lifetime error = %v", err)
	}

	for _, skew := range []time.Duration{-2 * time.Hour, 2 * time.Hour} {
		skewedAt := model.NowUTC().Add(skew)
		skewed := teacherAcademicUnitInvitationIssueFixture(t, ss, inviter, unit, packageRole, skewedAt)
		skewed.Invitation.TargetEmail = "teacher-skew-" + model.NewId() + "@example.edu"
		before := model.NowUTC()
		persisted, issueErr := ss.Invitation().IssueTeacherAcademicUnit(ctx, skewed)
		after := model.NowUTC()
		requireNoError(t, issueErr)
		if persisted.CreatedAt.Before(before.Add(-time.Second)) || persisted.CreatedAt.After(after.Add(time.Second)) ||
			persisted.ExpiresAt.Sub(persisted.CreatedAt) != model.StudentClassInvitationLifetime {
			t.Fatalf("skew %v teacher Invitation lifecycle = %v..%v; local window %v..%v", skew, persisted.CreatedAt, persisted.ExpiresAt, before, after)
		}
		delivery, deliveryErr := ss.Mail().GetDelivery(ctx, skewed.Delivery.ID)
		requireNoError(t, deliveryErr)
		if !delivery.CreatedAt.Equal(persisted.CreatedAt) || !delivery.Deadline.Equal(persisted.ExpiresAt) {
			t.Fatalf("skew %v delivery lifecycle = %v..%v; Invitation %v..%v", skew, delivery.CreatedAt, delivery.Deadline, persisted.CreatedAt, persisted.ExpiresAt)
		}
	}
}

func testTeacherAcademicUnitInvitationAcceptance(t *testing.T, ss store.Store) {
	t.Helper()
	ctx := context.Background()
	unit, _ := saveProgrammeParents(t, ctx, ss, "teacher-invitation-accept")
	inviter := saveUser(t, ctx, ss)
	packageRole, err := ss.Role().Save(ctx, &model.Role{Name: "teacher-accept-package-" + model.NewId(), DisplayName: "Teacher Package",
		Permissions: []string{string(model.ActionAcademicUnitView), string(model.ActionProgrammeManage)}})
	requireNoError(t, err)
	authorityRole, err := ss.Role().Save(ctx, &model.Role{Name: "teacher-accept-inviter-" + model.NewId(), DisplayName: "Teacher Inviter",
		Permissions: []string{string(model.ActionInvitationCreate), string(model.ActionAcademicUnitMembersManage),
			string(model.ActionAcademicUnitView), string(model.ActionProgrammeManage)}})
	requireNoError(t, err)
	issuedAt := model.NowUTC().Add(-time.Minute)
	_, err = ss.RoleBinding().Save(ctx, &model.RoleBinding{UserID: inviter.ID, RoleID: authorityRole.ID,
		ScopeType: model.RoleScopeInstitution, ScopeID: unit.InstitutionID.String(), StartsAt: issuedAt.Add(-time.Second)})
	requireNoError(t, err)
	issue := teacherAcademicUnitInvitationIssueFixture(t, ss, inviter, unit, packageRole, issuedAt)
	invitation, err := ss.Invitation().IssueTeacherAcademicUnit(ctx, issue)
	requireNoError(t, err)
	startedCredential, err := ss.Mail().StartDelivery(ctx, issue.Delivery.ID, issue.Delivery.Revision, issuedAt.Add(20*time.Second))
	requireNoError(t, err)
	if startedCredential.State != model.MailDeliverySending {
		t.Fatalf("active teacher Invitation credential = %#v", startedCredential)
	}
	input := teacherAcademicUnitInvitationAcceptanceFixture(t, invitation, issuedAt.Add(30*time.Second))
	accepted, err := ss.Invitation().AcceptTeacherAcademicUnit(ctx, input)
	requireNoError(t, err)
	if accepted.Invitation.State != model.InvitationAccepted || accepted.Affiliation.Kind != model.AffiliationTeacher ||
		accepted.AcademicUnitMember.AcademicUnitID != unit.ID || accepted.RoleBinding.RoleID != packageRole.ID ||
		accepted.RoleBinding.OriginInvitationID != invitation.ID {
		t.Fatalf("AcceptTeacherAcademicUnit() = %#v", accepted)
	}
	suppressedCredential, err := ss.Mail().GetDelivery(ctx, issue.Delivery.ID)
	requireNoError(t, err)
	if suppressedCredential.State != model.MailDeliverySuppressed || len(suppressedCredential.EncryptedPayload) != 0 {
		t.Fatalf("accepted teacher credential = %#v", suppressedCredential)
	}
	replayed, err := ss.Invitation().AcceptTeacherAcademicUnit(ctx, input)
	requireNoError(t, err)
	if !replayed.Replayed || replayed.User.ID != accepted.User.ID || replayed.Affiliation.ID != accepted.Affiliation.ID ||
		replayed.AcademicUnitMember.ID != accepted.AcademicUnitMember.ID || replayed.RoleBinding.ID != accepted.RoleBinding.ID {
		t.Fatalf("teacher Invitation replay = %#v; want original %#v", replayed, accepted)
	}
	secondRole, err := ss.Role().Save(ctx, &model.Role{Name: "teacher-compatible-" + model.NewId(), DisplayName: "Compatible Teacher Package",
		Permissions: []string{string(model.ActionAcademicUnitView)}})
	requireNoError(t, err)
	secondIssue := teacherAcademicUnitInvitationIssueFixture(t, ss, inviter, unit, secondRole, issuedAt.Add(time.Second))
	secondInvitation, err := ss.Invitation().IssueTeacherAcademicUnit(ctx, secondIssue)
	requireNoError(t, err)
	secondInput := teacherAcademicUnitInvitationAcceptanceFixture(t, secondInvitation, issuedAt.Add(40*time.Second))
	secondAccepted, err := ss.Invitation().AcceptTeacherAcademicUnit(ctx, secondInput)
	requireNoError(t, err)
	if secondAccepted.User.ID != accepted.User.ID || secondAccepted.Affiliation.ID != accepted.Affiliation.ID ||
		secondAccepted.AcademicUnitMember.ID != accepted.AcademicUnitMember.ID || secondAccepted.RoleBinding.ID == accepted.RoleBinding.ID {
		t.Fatalf("compatible teacher Invitation = %#v; first %#v", secondAccepted, accepted)
	}
	if _, err = ss.Mail().GetDelivery(ctx, secondInput.Delivery.ID); !store.IsNotFound(err) {
		t.Fatalf("existing teacher received redundant welcome delivery: %v", err)
	}
	independentRole, err := ss.Role().Save(ctx, &model.Role{Name: "teacher-independent-" + model.NewId(), DisplayName: "Independent",
		Permissions: []string{string(model.ActionAcademicUnitView)}})
	requireNoError(t, err)
	independent, err := ss.RoleBinding().Save(ctx, &model.RoleBinding{UserID: accepted.User.ID, RoleID: independentRole.ID,
		ScopeType: model.RoleScopeAcademicUnit, ScopeID: unit.ID.String(), StartsAt: issuedAt})
	requireNoError(t, err)
	endAt := model.MillisFromTime(model.NowUTC().Add(time.Minute))
	futureRole, err := ss.Role().Save(ctx, &model.Role{Name: "teacher-future-package-" + model.NewId(), DisplayName: "Future Package",
		Permissions: []string{string(model.ActionAcademicUnitView)}})
	requireNoError(t, err)
	futurePackage, err := ss.RoleBinding().Save(ctx, &model.RoleBinding{UserID: accepted.User.ID, RoleID: futureRole.ID,
		OriginInvitationID: invitation.ID, OriginAcademicUnitMemberID: accepted.AcademicUnitMember.ID,
		ScopeType: model.RoleScopeAcademicUnit, ScopeID: unit.ID.String(), StartsAt: model.TimeFromMillis(endAt).Add(time.Hour)})
	requireNoError(t, err)
	_, err = ss.AcademicUnitMember().End(ctx, accepted.AcademicUnitMember.ID.String(), accepted.AcademicUnitMember.Revision, endAt)
	requireNoError(t, err)
	endedPackage, err := ss.RoleBinding().Get(ctx, accepted.RoleBinding.ID.String())
	requireNoError(t, err)
	endedSecondPackage, err := ss.RoleBinding().Get(ctx, secondAccepted.RoleBinding.ID.String())
	requireNoError(t, err)
	retainedIndependent, err := ss.RoleBinding().Get(ctx, independent.ID.String())
	requireNoError(t, err)
	if endedPackage.EndsAt.Millis() != endAt || endedSecondPackage.EndsAt.Millis() != endAt || retainedIndependent.EndsAt.Valid {
		t.Fatalf("ended packages/independent bindings = %#v / %#v / %#v", endedPackage, endedSecondPackage, retainedIndependent)
	}
	if _, err = ss.RoleBinding().Get(ctx, futurePackage.ID.String()); !store.IsNotFound(err) {
		t.Fatalf("future package binding remained current after membership ended: %v", err)
	}
}

func teacherAcademicUnitInvitationIssueFixture(t *testing.T, ss store.Store, inviter *model.User, unit *model.AcademicUnit, role *model.Role, issuedAt time.Time) *store.TeacherAcademicUnitInvitationIssue {
	t.Helper()
	invitation, err := model.NewTeacherAcademicUnitInvitation(model.TeacherAcademicUnitInvitationInput{
		ID: model.NewInvitationID(), TargetEmail: "teacher@example.edu", AcademicUnitID: unit.ID, RoleID: role.ID,
		RoleActions: role.Permissions, IntendedStartsAt: issuedAt,
		Suggestions:   model.InvitationProfileSuggestions{Username: "teacher-one", DisplayName: "Teacher One", Locale: "en"},
		InviterUserID: inviter.ID, ScopeType: model.RoleScopeAcademicUnit, ScopeID: unit.ID.String(),
		ClaimHash: model.HashInvitationClaim(model.NewCredentialToken()), IssuedAt: issuedAt,
	})
	requireNoError(t, err)
	occurrenceID, deliveryID, jobID := model.NewMailOccurrenceID(), model.NewMailDeliveryID(), model.NewJobID()
	command, err := model.EncodeMailDeliveryCommand(model.MailDeliveryCommandV1{DeliveryID: deliveryID})
	requireNoError(t, err)
	job, err := model.NewJob(jobID, model.JobTypeMailDeliverCredential, 1, command, deliveryID.String(), issuedAt, issuedAt, model.MailMaximumAttempts)
	requireNoError(t, err)
	delivery := &model.MailDelivery{ID: deliveryID, OccurrenceID: occurrenceID, JobID: jobID, TargetInvitationID: invitation.ID,
		TemplateKey: model.MailTemplateAccessTeacherAcademicUnitInvitation, TemplateDigest: strings.Repeat("c", 64),
		MaskedRecipient: "t***@example.edu", State: model.MailDeliveryQueued, CreatedAt: issuedAt, UpdatedAt: issuedAt,
		MessageDate: issuedAt, Deadline: invitation.ExpiresAt, MessageID: "<teacher-invitation." + deliveryID.String() + "@example.test>",
		EncryptedPayload: json.RawMessage(`{"version":1,"key_id":"11111111111111111111111111111111","ciphertext":"secret"}`), Revision: 1}
	attempt, err := ss.Audit().Save(context.Background(), &model.AuditEvent{ActorID: inviter.ID, Action: string(model.ActionInvitationCreate),
		Resource: model.Resource{Type: model.ResourceAcademicUnit, ID: unit.ID.String()}, ScopeType: model.RoleScopeAcademicUnit,
		ScopeID: unit.ID.String(), Status: model.AuditStatusAttempt, NodeID: "teacher-invitation-store-test"})
	requireNoError(t, err)
	return &store.TeacherAcademicUnitInvitationIssue{Invitation: invitation, Lifetime: model.StudentClassInvitationLifetime,
		Occurrence: &model.MailOccurrence{ID: occurrenceID, Kind: model.MailOccurrenceInvitation,
			TemplateKey: model.MailTemplateAccessTeacherAcademicUnitInvitation, ActorUserID: inviter.ID, CreatedAt: issuedAt},
		Delivery: delivery, DeliveryJob: job, AuditEventID: attempt.ID.String(), AuditAt: model.MillisFromTime(issuedAt)}
}

func teacherAcademicUnitInvitationAcceptanceFixture(t *testing.T, invitation *model.Invitation, acceptedAt time.Time) *store.TeacherAcademicUnitInvitationAcceptance {
	t.Helper()
	student := studentClassInvitationAcceptanceFixture(t, &model.Invitation{
		ID: invitation.ID, TargetEmail: invitation.TargetEmail, Suggestions: invitation.Suggestions,
		ClassID: model.NewClassID(), AcademicPeriodID: model.NewAcademicPeriodID(), IntendedStartsAt: invitation.IntendedStartsAt,
		IntendedEndsAt: invitation.IntendedEndsAt, InviterUserID: invitation.InviterUserID, ClaimHash: invitation.ClaimHash,
	}, acceptedAt)
	affiliation := *student.Affiliation
	affiliation.Kind = model.AffiliationTeacher
	member := &model.AcademicUnitMember{AcademicUnitID: invitation.AcademicUnitID, UserID: student.User.ID,
		StartsAt: invitation.EffectiveStartsAt(acceptedAt), EndsAt: invitation.IntendedEndsAt}
	member.PrepareCreate(model.NewAcademicUnitMemberID(), acceptedAt)
	binding := &model.RoleBinding{UserID: student.User.ID, RoleID: invitation.RoleID, OriginInvitationID: invitation.ID,
		OriginAcademicUnitMemberID: member.ID,
		ScopeType:                  model.RoleScopeAcademicUnit, ScopeID: invitation.AcademicUnitID.String(),
		StartsAt: invitation.EffectiveStartsAt(acceptedAt), EndsAt: invitation.IntendedEndsAt}
	binding.PrepareCreate(model.NewRoleBindingID(), acceptedAt)
	student.AuditEvent.Resource = model.Resource{Type: model.ResourceAcademicUnit, ID: invitation.AcademicUnitID.String()}
	student.AuditEvent.ScopeType, student.AuditEvent.ScopeID = model.RoleScopeAcademicUnit, invitation.AcademicUnitID.String()
	return &store.TeacherAcademicUnitInvitationAcceptance{ClaimHash: invitation.ClaimHash, AcceptedAt: student.AcceptedAt,
		User: student.User, Settings: student.Settings, PasswordCredential: student.PasswordCredential,
		DefaultProfilePictureJob: student.DefaultProfilePictureJob, Affiliation: &affiliation,
		AcademicUnitMember: member, RoleBinding: binding, Occurrence: student.Occurrence, Delivery: student.Delivery,
		DeliveryJob: student.DeliveryJob, AuditEvent: student.AuditEvent,
		RequiredActions: []model.Action{model.ActionInvitationCreate, model.ActionAcademicUnitMembersManage}}
}
