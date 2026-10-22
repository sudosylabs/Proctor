// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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
	packageRole, err := SaveRole(ctx, ss, &model.Role{Name: "teacher-coverage-package-" + model.NewID(), DisplayName: "Teacher Coverage Package",
		Permissions: []string{string(model.ActionAcademicUnitView)}})
	require.NoError(t, err)
	authorityRole, err := SaveRole(ctx, ss, &model.Role{Name: "teacher-coverage-authority-" + model.NewID(), DisplayName: "Teacher Coverage Authority",
		Permissions: []string{string(model.ActionInvitationCreate), string(model.ActionAcademicUnitMembersManage), string(model.ActionAcademicUnitView)}})
	require.NoError(t, err)
	issuedAt := model.NowUTC().Add(-time.Minute)
	_, err = SaveRoleBinding(ctx, ss, &model.RoleBinding{UserID: inviter.ID, RoleID: authorityRole.ID,
		ScopeType: model.RoleScopeInstitution, ScopeID: unit.InstitutionID.String(), StartsAt: issuedAt.Add(-time.Second)})
	require.NoError(t, err)

	firstIssue := teacherAcademicUnitInvitationIssueFixture(t, ss, inviter, unit, packageRole, issuedAt)
	firstIssue.Invitation.TargetEmail = "teacher-coverage@example.edu"
	firstIssue.Invitation.Suggestions.Username = "teacher-coverage-" + model.NewID()
	firstIssue.Invitation.IntendedEndsAt = model.OptionalTimeFrom(issuedAt.Add(4 * time.Hour))
	first, err := ss.Invitation().IssueTeacherAcademicUnit(ctx, firstIssue)
	require.NoError(t, err)
	firstAccepted, err := ss.Invitation().AcceptTeacherAcademicUnit(ctx, teacherAcademicUnitInvitationAcceptanceFixture(t, first, issuedAt.Add(time.Minute)))
	require.NoError(t, err)

	coveredIssue := teacherAcademicUnitInvitationIssueFixture(t, ss, inviter, unit, packageRole, issuedAt.Add(time.Second))
	coveredIssue.Invitation.TargetEmail = first.TargetEmail
	coveredIssue.Invitation.IntendedEndsAt = model.OptionalTimeFrom(issuedAt.Add(3 * time.Hour))
	covered, err := ss.Invitation().IssueTeacherAcademicUnit(ctx, coveredIssue)
	require.NoError(t, err)
	coveredAccepted, err := ss.Invitation().AcceptTeacherAcademicUnit(ctx, teacherAcademicUnitInvitationAcceptanceFixture(t, covered, issuedAt.Add(2*time.Minute)))
	require.NoError(t, err)
	require.Equal(t, firstAccepted.AcademicUnitMember.ID, coveredAccepted.AcademicUnitMember.ID, "bounded compatible package did not reuse covering relationships: %#v / %#v", firstAccepted, coveredAccepted)
	require.Equal(t, firstAccepted.RoleBinding.ID, coveredAccepted.RoleBinding.ID, "bounded compatible package did not reuse covering relationships: %#v / %#v", firstAccepted, coveredAccepted)

	unboundedRole, err := SaveRole(ctx, ss, &model.Role{Name: "teacher-unbounded-package-" + model.NewID(), DisplayName: "Unbounded Teacher Package",
		Permissions: []string{string(model.ActionAcademicUnitView)}})
	require.NoError(t, err)
	unboundedIssue := teacherAcademicUnitInvitationIssueFixture(t, ss, inviter, unit, unboundedRole, issuedAt.Add(2*time.Second))
	unboundedIssue.Invitation.TargetEmail = first.TargetEmail
	unbounded, err := ss.Invitation().IssueTeacherAcademicUnit(ctx, unboundedIssue)
	require.NoError(t, err)
	_, err = ss.Invitation().AcceptTeacherAcademicUnit(ctx, teacherAcademicUnitInvitationAcceptanceFixture(t, unbounded, issuedAt.Add(3*time.Minute)))
	require.True(t, store.IsConflict(err), "unbounded package reused finite membership")
	persisted, err := ss.Invitation().Get(ctx, unbounded.ID)
	require.NoError(t, err)
	require.Equal(t, model.InvitationPending, persisted.State)
}

func teacherInvitationMailFixture(t *testing.T, ss store.Store, suffix string) (context.Context, *model.AcademicUnit, *model.Role, *store.TeacherAcademicUnitInvitationIssue) {
	t.Helper()
	ctx := context.Background()
	parent, _ := saveProgrammeParents(t, ctx, ss, "teacher-mail-"+suffix)
	unit := saveAcademicUnit(t, ctx, ss, parent.InstitutionID.String(), parent.ID.String(), "teacher-mail-leaf-"+model.NewID())
	inviter := saveUser(t, ctx, ss)
	packageRole, err := SaveRole(ctx, ss, &model.Role{Name: "teacher-mail-package-" + model.NewID(), DisplayName: "Teacher Mail Package",
		Permissions: []string{string(model.ActionAcademicUnitView)}})
	require.NoError(t, err)
	authorityRole, err := SaveRole(ctx, ss, &model.Role{Name: "teacher-mail-authority-" + model.NewID(), DisplayName: "Teacher Mail Authority",
		Permissions: []string{string(model.ActionInvitationCreate), string(model.ActionAcademicUnitMembersManage), string(model.ActionAcademicUnitView)}})
	require.NoError(t, err)
	issuedAt := model.NowUTC().Add(-time.Minute)
	_, err = SaveRoleBinding(ctx, ss, &model.RoleBinding{UserID: inviter.ID, RoleID: authorityRole.ID,
		ScopeType: model.RoleScopeInstitution, ScopeID: unit.InstitutionID.String(), StartsAt: issuedAt.Add(-time.Second)})
	require.NoError(t, err)
	issue := teacherAcademicUnitInvitationIssueFixture(t, ss, inviter, unit, packageRole, issuedAt)
	issue.Invitation.TargetEmail = "teacher-mail-" + suffix + "@example.edu"
	_, err = ss.Invitation().IssueTeacherAcademicUnit(ctx, issue)
	require.NoError(t, err)
	return ctx, unit, packageRole, issue
}

func testTeacherInvitationMailSerializesWithUnitArchive(t *testing.T, ss store.Store, archive func(*testing.T, context.Context, *model.AcademicUnit, func() error) error) {
	t.Helper()
	ctx, unit, _, issue := teacherInvitationMailFixture(t, ss, "unit-race")
	err := archive(t, ctx, unit, func() error {
		_, startErr := ss.Mail().StartDelivery(ctx, issue.Delivery.ID, issue.Delivery.Revision, model.NowUTC())
		return startErr
	})
	require.NoError(t, err)
	assertObsoleteTeacherInvitationDelivery(t, ctx, ss, issue.Delivery.ID)
}

func testTeacherInvitationMailSerializesWithRoleMutation(t *testing.T, ss store.Store, mutate func(*testing.T, context.Context, *model.Role, func() error) error) {
	t.Helper()
	ctx, _, role, issue := teacherInvitationMailFixture(t, ss, "role-race")
	err := mutate(t, ctx, role, func() error {
		_, startErr := ss.Mail().StartDelivery(ctx, issue.Delivery.ID, issue.Delivery.Revision, model.NowUTC())
		return startErr
	})
	require.NoError(t, err)
	assertObsoleteTeacherInvitationDelivery(t, ctx, ss, issue.Delivery.ID)
}

func assertObsoleteTeacherInvitationDelivery(t *testing.T, ctx context.Context, ss store.Store, id model.MailDeliveryID) {
	t.Helper()
	delivery, err := ss.Mail().GetDelivery(ctx, id)
	require.NoError(t, err)
	require.Equal(t, model.MailDeliverySuppressed, delivery.State)
	require.Equal(t, model.MailDeliveryObsoleteCode, delivery.PublicFailureCode)
	require.Empty(t, delivery.EncryptedPayload)
}

func testTeacherInvitationSerializesWithUnitArchive(t *testing.T, ss store.Store, archive func(*testing.T, context.Context, *model.AcademicUnit, func() error) error) {
	t.Helper()
	ctx := context.Background()
	parent, _ := saveProgrammeParents(t, ctx, ss, "teacher-invitation-unit-race")
	unit := saveAcademicUnit(t, ctx, ss, parent.InstitutionID.String(), parent.ID.String(), "teacher-invitation-leaf-"+model.NewID())
	inviter := saveUser(t, ctx, ss)
	packageRole, err := SaveRole(ctx, ss, &model.Role{Name: "teacher-unit-race-package-" + model.NewID(), DisplayName: "Teacher Package",
		Permissions: []string{string(model.ActionAcademicUnitView)}})
	require.NoError(t, err)
	authorityRole, err := SaveRole(ctx, ss, &model.Role{Name: "teacher-unit-race-authority-" + model.NewID(), DisplayName: "Teacher Inviter",
		Permissions: []string{string(model.ActionInvitationCreate), string(model.ActionAcademicUnitMembersManage), string(model.ActionAcademicUnitView)}})
	require.NoError(t, err)
	issuedAt := model.NowUTC().Add(-time.Minute)
	_, err = SaveRoleBinding(ctx, ss, &model.RoleBinding{UserID: inviter.ID, RoleID: authorityRole.ID,
		ScopeType: model.RoleScopeInstitution, ScopeID: unit.InstitutionID.String(), StartsAt: issuedAt.Add(-time.Second)})
	require.NoError(t, err)
	issue := teacherAcademicUnitInvitationIssueFixture(t, ss, inviter, unit, packageRole, issuedAt)
	issue.Invitation.TargetEmail = "teacher-unit-race@example.edu"
	invitation, err := ss.Invitation().IssueTeacherAcademicUnit(ctx, issue)
	require.NoError(t, err)
	acceptance := teacherAcademicUnitInvitationAcceptanceFixture(t, invitation, issuedAt.Add(30*time.Second))
	err = archive(t, ctx, unit, func() error {
		_, acceptErr := ss.Invitation().AcceptTeacherAcademicUnit(ctx, acceptance)
		return acceptErr
	})
	require.Error(t, err, "AcceptTeacherAcademicUnit() succeeded after concurrent Academic Unit archival")
	persisted, getErr := ss.Invitation().Get(ctx, invitation.ID)
	require.NoError(t, getErr)
	require.Equal(t, model.InvitationPending, persisted.State)
}

func testTeacherAcademicUnitInvitationIssue(t *testing.T, ss store.Store) {
	t.Helper()
	ctx := context.Background()
	unit, _ := saveProgrammeParents(t, ctx, ss, "teacher-invitation")
	inviter := saveUser(t, ctx, ss)
	packageRole, err := SaveRole(ctx, ss, &model.Role{Name: "teacher-package-" + model.NewID(), DisplayName: "Teacher Package",
		Permissions: []string{string(model.ActionAcademicUnitView), string(model.ActionProgrammeManage)}})
	require.NoError(t, err)
	authorityRole, err := SaveRole(ctx, ss, &model.Role{Name: "teacher-inviter-" + model.NewID(), DisplayName: "Teacher Inviter",
		Permissions: []string{string(model.ActionInvitationCreate), string(model.ActionAcademicUnitMembersManage),
			string(model.ActionAcademicUnitView), string(model.ActionProgrammeManage)}})
	require.NoError(t, err)
	issuedAt := model.NowUTC().Add(-time.Minute)
	_, err = SaveRoleBinding(ctx, ss, &model.RoleBinding{UserID: inviter.ID, RoleID: authorityRole.ID,
		ScopeType: model.RoleScopeInstitution, ScopeID: unit.InstitutionID.String(), StartsAt: issuedAt.Add(-time.Second)})
	require.NoError(t, err)
	input := teacherAcademicUnitInvitationIssueFixture(t, ss, inviter, unit, packageRole, issuedAt)
	created, err := ss.Invitation().IssueTeacherAcademicUnit(ctx, input)
	require.NoError(t, err)
	require.Equal(t, input.Invitation.ID, created.ID)
	require.Equal(t, model.InvitationPurposeTeacherAcademicUnit, created.Purpose)
	require.Equal(t, unit.ID, created.AcademicUnitID)
	require.Equal(t, packageRole.ID, created.RoleID)
	require.True(t, slices.Equal(created.RoleActions, []string{string(model.ActionAcademicUnitView), string(model.ActionProgrammeManage)}))
	stored, err := ss.Invitation().Get(ctx, created.ID)
	require.NoError(t, err)
	require.Equal(t, unit.ID, stored.AcademicUnitID)
	require.Equal(t, packageRole.ID, stored.RoleID)
	require.True(t, slices.Equal(stored.RoleActions, created.RoleActions))
	delivery, err := ss.Mail().GetDelivery(ctx, input.Delivery.ID)
	require.NoError(t, err)
	require.Equal(t, model.MailTemplateAccessTeacherAcademicUnitInvitation, delivery.TemplateKey)
	require.Equal(t, created.ID, delivery.TargetInvitationID)

	rollback := teacherAcademicUnitInvitationIssueFixture(t, ss, inviter, unit, packageRole, issuedAt.Add(time.Second))
	rollback.Invitation.TargetEmail = "rollback-teacher@example.edu"
	rollback.AuditEventID = model.NewAuditEventID()
	_, err = ss.Invitation().IssueTeacherAcademicUnit(ctx, rollback)
	require.Error(t, err, "IssueTeacherAcademicUnit() succeeded without durable audit attempt")
	_, err = ss.Invitation().Get(ctx, rollback.Invitation.ID)
	require.True(t, store.IsNotFound(err), "rolled-back teacher Invitation")
	protectedRole, err := SaveRole(ctx, ss, &model.Role{Name: "teacher-protected-" + model.NewID(), DisplayName: "Protected Package",
		Permissions: []string{string(model.ActionRoleManage)}})
	require.NoError(t, err)
	directAuthority, err := SaveRole(ctx, ss, &model.Role{Name: "teacher-direct-protected-" + model.NewID(), DisplayName: "Direct Protected Authority",
		Permissions: []string{string(model.ActionRoleManage)}})
	require.NoError(t, err)
	_, err = SaveRoleBinding(ctx, ss, &model.RoleBinding{UserID: inviter.ID, RoleID: directAuthority.ID,
		ScopeType: model.RoleScopeAcademicUnit, ScopeID: unit.ID.String(), StartsAt: issuedAt.Add(-time.Second)})
	require.NoError(t, err)
	protected := teacherAcademicUnitInvitationIssueFixture(t, ss, inviter, unit, protectedRole, issuedAt.Add(2*time.Second))
	protected.Invitation.TargetEmail = "protected-teacher@example.edu"
	_, err = ss.Invitation().IssueTeacherAcademicUnit(ctx, protected)
	require.Error(t, err, "IssueTeacherAcademicUnit() delegated a protected action at the same Academic Unit scope")
	unboundedLifetime := teacherAcademicUnitInvitationIssueFixture(t, ss, inviter, unit, packageRole, issuedAt.Add(3*time.Second))
	unboundedLifetime.Invitation.TargetEmail = "teacher-unbounded-lifetime@example.edu"
	unboundedLifetime.Lifetime = model.InvitationLifetime + time.Hour
	var invalidInput *store.ErrInvalidInput
	_, err = ss.Invitation().IssueTeacherAcademicUnit(ctx, unboundedLifetime)
	require.ErrorAs(t, err, &invalidInput)

	for _, skew := range []time.Duration{-2 * time.Hour, 2 * time.Hour} {
		skewedAt := model.NowUTC().Add(skew)
		skewed := teacherAcademicUnitInvitationIssueFixture(t, ss, inviter, unit, packageRole, skewedAt)
		skewed.Invitation.TargetEmail = "teacher-skew-" + model.NewID() + "@example.edu"
		before := model.NowUTC()
		persisted, issueErr := ss.Invitation().IssueTeacherAcademicUnit(ctx, skewed)
		after := model.NowUTC()
		require.NoError(t, issueErr)
		require.False(t, persisted.CreatedAt.Before(before.Add(-time.Second)), "skew %v teacher Invitation lifecycle = %v..%v; local window %v..%v", skew, persisted.CreatedAt, persisted.ExpiresAt, before, after)
		require.False(t, persisted.CreatedAt.After(after.Add(time.Second)), "skew %v teacher Invitation lifecycle = %v..%v; local window %v..%v", skew, persisted.CreatedAt, persisted.ExpiresAt, before, after)
		require.Equal(t, model.InvitationLifetime, persisted.ExpiresAt.Sub(persisted.CreatedAt), "skew %v teacher Invitation lifecycle = %v..%v; local window %v..%v", skew, persisted.CreatedAt, persisted.ExpiresAt, before, after)
		delivery, deliveryErr := ss.Mail().GetDelivery(ctx, skewed.Delivery.ID)
		require.NoError(t, deliveryErr)
		require.True(t, delivery.CreatedAt.Equal(persisted.CreatedAt), "skew %v delivery lifecycle = %v..%v; Invitation", skew, delivery.CreatedAt, delivery.Deadline)
		require.True(t, delivery.Deadline.Equal(persisted.ExpiresAt), "skew %v delivery lifecycle = %v..%v; Invitation", skew, delivery.CreatedAt, delivery.Deadline)
	}
}

func testTeacherAcademicUnitInvitationAcceptance(t *testing.T, ss store.Store) {
	t.Helper()
	ctx := context.Background()
	unit, _ := saveProgrammeParents(t, ctx, ss, "teacher-invitation-accept")
	inviter := saveUser(t, ctx, ss)
	packageRole, err := SaveRole(ctx, ss, &model.Role{Name: "teacher-accept-package-" + model.NewID(), DisplayName: "Teacher Package",
		Permissions: []string{string(model.ActionAcademicUnitView), string(model.ActionProgrammeManage)}})
	require.NoError(t, err)
	authorityRole, err := SaveRole(ctx, ss, &model.Role{Name: "teacher-accept-inviter-" + model.NewID(), DisplayName: "Teacher Inviter",
		Permissions: []string{string(model.ActionInvitationCreate), string(model.ActionAcademicUnitMembersManage),
			string(model.ActionAcademicUnitView), string(model.ActionProgrammeManage)}})
	require.NoError(t, err)
	issuedAt := model.NowUTC().Add(-time.Minute)
	_, err = SaveRoleBinding(ctx, ss, &model.RoleBinding{UserID: inviter.ID, RoleID: authorityRole.ID,
		ScopeType: model.RoleScopeInstitution, ScopeID: unit.InstitutionID.String(), StartsAt: issuedAt.Add(-time.Second)})
	require.NoError(t, err)
	issue := teacherAcademicUnitInvitationIssueFixture(t, ss, inviter, unit, packageRole, issuedAt)
	invitation, err := ss.Invitation().IssueTeacherAcademicUnit(ctx, issue)
	require.NoError(t, err)
	startedCredential, err := ss.Mail().StartDelivery(ctx, issue.Delivery.ID, issue.Delivery.Revision, issuedAt.Add(20*time.Second))
	require.NoError(t, err)
	require.Equal(t, model.MailDeliverySending, startedCredential.State)
	input := teacherAcademicUnitInvitationAcceptanceFixture(t, invitation, issuedAt.Add(30*time.Second))
	accepted, err := ss.Invitation().AcceptTeacherAcademicUnit(ctx, input)
	require.NoError(t, err)
	require.Equal(t, model.InvitationAccepted, accepted.Invitation.State)
	require.Equal(t, model.AffiliationTeacher, accepted.Affiliation.Kind)
	require.Equal(t, unit.ID, accepted.AcademicUnitMember.AcademicUnitID)
	require.Equal(t, packageRole.ID, accepted.RoleBinding.RoleID)
	require.Equal(t, invitation.ID, accepted.RoleBinding.OriginInvitationID)
	suppressedCredential, err := ss.Mail().GetDelivery(ctx, issue.Delivery.ID)
	require.NoError(t, err)
	require.Equal(t, model.MailDeliverySuppressed, suppressedCredential.State)
	require.Empty(t, suppressedCredential.EncryptedPayload)
	replayed, err := ss.Invitation().AcceptTeacherAcademicUnit(ctx, input)
	require.NoError(t, err)
	require.True(t, replayed.Replayed, "teacher Invitation replay = %#v; want original %#v", replayed, accepted)
	require.Equal(t, accepted.User.ID, replayed.User.ID, "teacher Invitation replay = %#v; want original %#v", replayed, accepted)
	require.Equal(t, accepted.Affiliation.ID, replayed.Affiliation.ID, "teacher Invitation replay = %#v; want original %#v", replayed, accepted)
	require.Equal(t, accepted.AcademicUnitMember.ID, replayed.AcademicUnitMember.ID, "teacher Invitation replay = %#v; want original %#v", replayed, accepted)
	require.Equal(t, accepted.RoleBinding.ID, replayed.RoleBinding.ID, "teacher Invitation replay = %#v; want original %#v", replayed, accepted)
	secondRole, err := SaveRole(ctx, ss, &model.Role{Name: "teacher-compatible-" + model.NewID(), DisplayName: "Compatible Teacher Package",
		Permissions: []string{string(model.ActionAcademicUnitView)}})
	require.NoError(t, err)
	secondIssue := teacherAcademicUnitInvitationIssueFixture(t, ss, inviter, unit, secondRole, issuedAt.Add(time.Second))
	secondInvitation, err := ss.Invitation().IssueTeacherAcademicUnit(ctx, secondIssue)
	require.NoError(t, err)
	secondInput := teacherAcademicUnitInvitationAcceptanceFixture(t, secondInvitation, issuedAt.Add(40*time.Second))
	secondAccepted, err := ss.Invitation().AcceptTeacherAcademicUnit(ctx, secondInput)
	require.NoError(t, err)
	require.Equal(t, accepted.User.ID, secondAccepted.User.ID, "compatible teacher Invitation = %#v; first %#v", secondAccepted, accepted)
	require.Equal(t, accepted.Affiliation.ID, secondAccepted.Affiliation.ID, "compatible teacher Invitation = %#v; first %#v", secondAccepted, accepted)
	require.Equal(t, accepted.AcademicUnitMember.ID, secondAccepted.AcademicUnitMember.ID, "compatible teacher Invitation = %#v; first %#v", secondAccepted, accepted)
	require.NotEqual(t, accepted.RoleBinding.ID, secondAccepted.RoleBinding.ID, "compatible teacher Invitation = %#v; first %#v", secondAccepted, accepted)
	_, err = ss.Mail().GetDelivery(ctx, secondInput.Delivery.ID)
	require.True(t, store.IsNotFound(err), "existing teacher received redundant welcome delivery")
	independentRole, err := SaveRole(ctx, ss, &model.Role{Name: "teacher-independent-" + model.NewID(), DisplayName: "Independent",
		Permissions: []string{string(model.ActionAcademicUnitView)}})
	require.NoError(t, err)
	independent, err := SaveRoleBinding(ctx, ss, &model.RoleBinding{UserID: accepted.User.ID, RoleID: independentRole.ID,
		ScopeType: model.RoleScopeAcademicUnit, ScopeID: unit.ID.String(), StartsAt: issuedAt})
	require.NoError(t, err)
	endAt := model.TimeUTC(model.NowUTC().Add(time.Minute))
	futureRole, err := SaveRole(ctx, ss, &model.Role{Name: "teacher-future-package-" + model.NewID(), DisplayName: "Future Package",
		Permissions: []string{string(model.ActionAcademicUnitView)}})
	require.NoError(t, err)
	futurePackage, err := SaveRoleBinding(ctx, ss, &model.RoleBinding{UserID: accepted.User.ID, RoleID: futureRole.ID,
		OriginInvitationID: invitation.ID, OriginAcademicUnitMemberID: accepted.AcademicUnitMember.ID,
		ScopeType: model.RoleScopeAcademicUnit, ScopeID: unit.ID.String(), StartsAt: model.TimeUTC(endAt).Add(time.Hour)})
	require.NoError(t, err)
	_, err = EndAcademicUnitMember(ctx, ss, accepted.AcademicUnitMember.ID, accepted.AcademicUnitMember.Revision, endAt)
	require.NoError(t, err)
	endedPackage, err := ss.RoleBinding().Get(ctx, accepted.RoleBinding.ID)
	require.NoError(t, err)
	endedSecondPackage, err := ss.RoleBinding().Get(ctx, secondAccepted.RoleBinding.ID)
	require.NoError(t, err)
	retainedIndependent, err := ss.RoleBinding().Get(ctx, independent.ID)
	require.NoError(t, err)
	require.True(t, endAt.Equal(endedPackage.EndsAt.Time), "ended packages/independent bindings = %#v / %#v / %#v", endedPackage, endedSecondPackage, retainedIndependent)
	require.True(t, endAt.Equal(endedSecondPackage.EndsAt.Time), "ended packages/independent bindings = %#v / %#v / %#v", endedPackage, endedSecondPackage, retainedIndependent)
	require.False(t, retainedIndependent.EndsAt.Valid, "ended packages/independent bindings = %#v / %#v / %#v", endedPackage, endedSecondPackage, retainedIndependent)
	_, err = ss.RoleBinding().Get(ctx, futurePackage.ID)
	require.True(t, store.IsNotFound(err), "future package binding remained current after membership ended")
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
	require.NoError(t, err)
	occurrenceID, deliveryID, jobID := model.NewMailOccurrenceID(), model.NewMailDeliveryID(), model.NewJobID()
	command, err := model.EncodeMailDeliveryCommand(model.MailDeliveryCommand{DeliveryID: deliveryID})
	require.NoError(t, err)
	job, err := model.NewJob(jobID, model.JobTypeMailDeliverCredential, command, deliveryID.String(), issuedAt, issuedAt, model.MailMaximumAttempts)
	require.NoError(t, err)
	delivery := &model.MailDelivery{ID: deliveryID, OccurrenceID: occurrenceID, JobID: jobID, TargetInvitationID: invitation.ID,
		TemplateKey: model.MailTemplateAccessTeacherAcademicUnitInvitation, TemplateDigest: strings.Repeat("c", 64),
		MaskedRecipient: "t***@example.edu", State: model.MailDeliveryQueued, CreatedAt: issuedAt, UpdatedAt: issuedAt,
		MessageDate: issuedAt, Deadline: invitation.ExpiresAt, MessageID: "<teacher-invitation." + deliveryID.String() + "@example.test>",
		EncryptedPayload: json.RawMessage(`{"version":1,"key_id":"11111111111111111111111111111111","ciphertext":"secret"}`), Revision: 1}
	attempt, err := ss.Audit().Save(context.Background(), &model.AuditEvent{ActorID: inviter.ID, Action: string(model.ActionInvitationCreate),
		Resource: model.Resource{Type: model.ResourceAcademicUnit, ID: unit.ID.String()}, ScopeType: model.RoleScopeAcademicUnit,
		ScopeID: unit.ID.String(), Status: model.AuditStatusAttempt, NodeID: "teacher-invitation-store-test"})
	require.NoError(t, err)
	return &store.TeacherAcademicUnitInvitationIssue{Invitation: invitation, Lifetime: model.InvitationLifetime,
		Occurrence: &model.MailOccurrence{ID: occurrenceID, Kind: model.MailOccurrenceInvitation,
			TemplateKey: model.MailTemplateAccessTeacherAcademicUnitInvitation, ActorUserID: inviter.ID, CreatedAt: issuedAt},
		Delivery: delivery, DeliveryJob: job, AuditEventID: attempt.ID, AuditAt: issuedAt}
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
