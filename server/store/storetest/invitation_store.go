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

type InvitationSQLProbe struct {
	DisableInviterBeforeIssue      func(*testing.T, context.Context, *model.User, func() error) error
	EndBindingBeforeAccept         func(*testing.T, context.Context, *model.RoleBinding, func() error) error
	ArchiveRoleBeforeAccept        func(*testing.T, context.Context, *model.Role, func() error) error
	PayloadKeyReferences           func(*testing.T, context.Context, string) int64
	SetInvitationExpiresAt         func(*testing.T, context.Context, model.InvitationID, time.Time)
	SetInvitationIntendedEndsAt    func(*testing.T, context.Context, model.InvitationID, time.Time)
	BrowserTransactionExists       func(*testing.T, context.Context, model.BrowserAuthenticationTransactionID) bool
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
	t.Run("BatchCommandIdempotencyRecoversIssueAndResend", func(t *testing.T) {
		testInvitationBatchCommandIdempotency(t, ss)
	})
	t.Run("BatchDuplicateRechecksCurrentInviterAuthority", func(t *testing.T) {
		testInvitationBatchDuplicateRechecksCurrentInviterAuthority(t, ss)
	})
	t.Run("AcceptStudentClassAtomicAndReplaySafe", func(t *testing.T) {
		testInvitationAcceptStudentClassAtomicAndReplaySafe(t, ss)
	})
	t.Run("AcceptStudentClassResolvesExistingUser", func(t *testing.T) {
		testInvitationAcceptStudentClassResolvesExistingUser(t, ss)
	})
	t.Run("BrowserHandoffCreationUsesAuthoritativeBounds", func(t *testing.T) {
		testBrowserInvitationTransactionCreation(t, ss, probe)
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
	t.Run("OnboardingImportLifecycle", func(t *testing.T) {
		testOnboardingImportLifecycle(t, ss)
	})
	t.Run("OnboardingImportTeacherAndRoleNoOps", func(t *testing.T) {
		testOnboardingImportTeacherAndRoleNoOps(t, ss)
	})
	t.Run("OnboardingImportAcademicAdministrationIsAtomicAndReauthorizes", func(t *testing.T) {
		testOnboardingImportAcademicAdministration(t, ss)
	})
	t.Run("StudentProgressionPreservesCrossPeriodHistoryAndReplays", func(t *testing.T) {
		testStudentProgressionCrossPeriod(t, ss)
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

func testStudentProgressionCrossPeriod(t *testing.T, ss store.Store) {
	t.Helper()
	ctx := context.Background()
	fixture := saveClassFixture(t, ctx, ss)
	sourceClass := saveClass(t, ctx, ss, fixture.level.ID.String(), fixture.period.ID.String(), "progression-source-"+model.NewID())
	destinationPeriod := saveAcademicPeriod(t, ctx, ss, fixture.institution.ID.String(), "progression-destination-"+model.NewID(), model.MillisFromTime(fixture.period.EndsAt)+1)
	destinationClass := saveClass(t, ctx, ss, fixture.level.ID.String(), destinationPeriod.ID.String(), "progression-destination-"+model.NewID())
	actor, student := saveUser(t, ctx, ss), saveUser(t, ctx, ss)
	effectiveAt := destinationPeriod.StartsAt.Add(time.Second)
	_, err := ss.Affiliation().Save(ctx, &model.Affiliation{UserID: student.ID, Kind: model.AffiliationStudent, StartsAt: fixture.period.StartsAt.Add(-time.Second)})
	require.NoError(t, err)
	source, err := ss.ClassMember().SeedEnroll(ctx, &model.ClassMember{ClassID: sourceClass.ID, UserID: student.ID, StartsAt: fixture.period.StartsAt})
	require.NoError(t, err)
	authorityRole, err := SaveRole(ctx, ss, &model.Role{Name: "progression-authority-" + model.NewID(), DisplayName: "Progression Authority",
		Permissions: []string{string(model.ActionAcademicProgressionManage), string(model.ActionClassMembersManage)}})
	require.NoError(t, err)
	_, err = SaveRoleBinding(ctx, ss, &model.RoleBinding{UserID: actor.ID, RoleID: authorityRole.ID, ScopeType: model.RoleScopeInstitution,
		ScopeID: fixture.institution.ID.String(), StartsAt: model.NowUTC().Add(-time.Minute)})
	require.NoError(t, err)
	session, credentials, _ := saveSession(t, ctx, ss, actor.ID.String(), 10)
	principal := model.Principal{UserID: actor.ID, SessionID: session.ID, CredentialID: model.PrincipalCredentialID(credentials[0].ID),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: session.ClientType, AuthenticatedAt: session.AuthenticatedAt}
	at := model.NowUTC()
	importID := model.NewOnboardingImportID()
	previewJob, err := model.NewJob(model.NewJobID(), model.JobTypeStudentProgressionPreview, json.RawMessage(`{"import_id":"`+importID.String()+`"}`), "progression-preview:"+importID.String(), at, at, 3)
	require.NoError(t, err)
	creationAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, destinationClass.ID.String(), "student_progression.dry_run")
	creationSourceAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, sourceClass.ID.String(), "student_progression.dry_run")
	created, err := ss.OnboardingImport().CreateOnboardingImport(ctx, &store.OnboardingImportCreation{Import: &store.OnboardingImport{ID: importID,
		Mode: model.OnboardingImportStudentProgression, State: model.OnboardingImportParsing, ScopeType: model.RoleScopeClass, ScopeID: destinationClass.ID.String(),
		SourcePeriodID: fixture.period.ID, SourceClassID: sourceClass.ID, DestinationPeriodID: destinationPeriod.ID, DestinationClassID: destinationClass.ID,
		SourcePeriodRevision: fixture.period.Revision, SourceClassRevision: sourceClass.Revision, DestinationPeriodRevision: destinationPeriod.Revision,
		DestinationClassRevision: destinationClass.Revision, EffectiveAt: effectiveAt, ActorUserID: actor.ID, Principal: principal, ParseJobID: previewJob.ID,
		CreatedAt: at, UpdatedAt: at, ExpiresAt: at.Add(7 * 24 * time.Hour), Revision: 1}, ParseJob: previewJob,
		AuditEventID: creationAudit.ID, SourceAuditEventID: creationSourceAudit.ID, AuditAt: at})
	require.NoError(t, err)
	digest := strings.Repeat("c", sha256.Size*2)
	preview, err := ss.OnboardingImport().CompleteOnboardingImportPreview(ctx, &store.OnboardingImportPreviewCompletion{ID: importID, ExpectedRevision: created.Revision,
		Digest: digest, At: at.Add(time.Second), Rows: []store.OnboardingImportRow{{ImportID: importID, RowNumber: 1, Reference: source.Membership.ID.String(),
			Operation: "class.enroll", ScopeType: model.RoleScopeClass, ScopeID: destinationClass.ID.String(), TargetRevision: destinationClass.Revision,
			UserID: student.ID, RelationshipID: source.Membership.ID.String(), RelationshipRevision: source.Membership.Revision,
			StartsAt: model.OptionalTimeFrom(effectiveAt), PreviewStatus: model.OnboardingImportRowValid, Status: model.OnboardingImportRowValid}}})
	require.NoError(t, err)
	executionJob, err := model.NewJob(model.NewJobID(), model.JobTypeOnboardingImportExecute, json.RawMessage(`{"import_id":"`+importID.String()+`"}`), "progression-execute:"+importID.String(), at, at, 3)
	require.NoError(t, err)
	commitSession, commitCredentials, _ := saveSession(t, ctx, ss, actor.ID.String(), 10)
	commitPrincipal := model.Principal{UserID: actor.ID, SessionID: commitSession.ID, CredentialID: model.PrincipalCredentialID(commitCredentials[0].ID),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: commitSession.ClientType, AuthenticatedAt: commitSession.AuthenticatedAt}
	commitAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, destinationClass.ID.String(), "student_progression.commit")
	commitSourceAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, sourceClass.ID.String(), "student_progression.commit")
	executing, err := ss.OnboardingImport().CommitOnboardingImport(ctx, &store.OnboardingImportCommit{ID: importID, ActorUserID: actor.ID, Principal: commitPrincipal, ExpectedRevision: preview.Revision,
		PreviewDigest: digest, Policy: model.OnboardingImportValidRowsOnly, IdempotencyKey: sha256.Sum256([]byte("progression-commit")), ExecutionJob: executionJob,
		At: at.Add(2 * time.Second), AuditEventID: commitAudit.ID, SourceAuditEventID: commitSourceAudit.ID, AuditAt: at.Add(2 * time.Second)})
	require.NoError(t, err)
	require.Equal(t, commitPrincipal.CredentialID, executing.Principal.CredentialID, "execution Principal = %#v; want commit Principal %#v", executing.Principal, commitPrincipal)
	candidate := &model.ClassMember{ClassID: destinationClass.ID, AcademicPeriodID: destinationPeriod.ID, UserID: student.ID, StartsAt: effectiveAt}
	candidate.PrepareCreate(model.NewClassMemberID(), at.Add(3*time.Second))
	notice := classMemberPreparedMail(t, candidate, model.MailTemplateAcademicClassEnrolled, candidate.CreatedAt)
	command := invitationTestCommand(actor.ID, "class_member.enroll", "progression-row", "progression-row")
	command.OnboardingImportID, command.OnboardingImportRowNumber = importID, 1
	audit := saveClassMemberAuditAttempt(t, ctx, ss, destinationClass.ID.String())
	progressionDestinationAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, destinationClass.ID.String(), "student_progression.execute_row")
	progressionSourceAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, sourceClass.ID.String(), "student_progression.execute_row")
	progressed, err := ss.ClassMember().Enroll(ctx, &store.ClassMemberEnrollment{Member: candidate, ExpectedRecipientRevision: student.Revision, Notice: notice,
		StudentProgression: true, ProgressionSourceAuditEventID: progressionSourceAudit.ID, ProgressionDestinationAuditEventID: progressionDestinationAudit.ID,
		AuditEventID: audit.ID, AuditAt: at.Add(3 * time.Second), Command: command})
	require.NoError(t, err)
	require.NoError(t, requireClassMemberMail(t, ctx, ss, notice, model.MailTemplateAcademicClassEnrolled))
	require.Nil(t, progressed.Previous)
	history, err := ss.ClassMember().ListByUser(ctx, student.ID)
	require.NoError(t, err)
	require.Len(t, history, 2)
	require.Equal(t, source.Membership.ID, history[1].ID)
	require.False(t, history[1].EndsAt.Valid)
	completed, err := ss.OnboardingImport().GetOnboardingImport(ctx, importID)
	require.NoError(t, err)
	require.Equal(t, model.OnboardingImportCompleted, completed.State)
	require.Equal(t, 1, completed.SucceededRows)
	replayAudit := saveClassMemberAuditAttempt(t, ctx, ss, destinationClass.ID.String())
	replayProgressionDestinationAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, destinationClass.ID.String(), "student_progression.execute_row")
	replayProgressionSourceAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, sourceClass.ID.String(), "student_progression.execute_row")
	replay := &store.ClassMemberEnrollment{Member: candidate, ExpectedRecipientRevision: student.Revision, StudentProgression: true,
		ProgressionSourceAuditEventID: replayProgressionSourceAudit.ID, ProgressionDestinationAuditEventID: replayProgressionDestinationAudit.ID,
		AuditEventID: replayAudit.ID, AuditAt: at.Add(4 * time.Second), Command: command}
	_, err = ss.ClassMember().Enroll(ctx, replay)
	require.NoError(t, err, "progression replay")
	require.True(t, replay.Replayed, "progression replay")
	afterReplay, err := ss.ClassMember().ListByUser(ctx, student.ID)
	require.NoError(t, err)
	require.Len(t, afterReplay, 2)
	noOpCandidate := &model.ClassMember{ClassID: destinationClass.ID, AcademicPeriodID: destinationPeriod.ID, UserID: student.ID,
		StartsAt: effectiveAt.Add(time.Minute)}
	noOpCandidate.PrepareCreate(model.NewClassMemberID(), at.Add(5*time.Second))
	noOpCommand := invitationTestCommand(actor.ID, "class_member.enroll", "progression-destination-no-op", "progression-destination-no-op")
	noOpAudit := saveClassMemberAuditAttempt(t, ctx, ss, destinationClass.ID.String())
	noOpDestinationAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, destinationClass.ID.String(), "student_progression.execute_row")
	noOpSourceAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, sourceClass.ID.String(), "student_progression.execute_row")
	noOpInput := &store.ClassMemberEnrollment{Member: noOpCandidate, ExpectedRecipientRevision: student.Revision, StudentProgression: true,
		ProgressionSourceAuditEventID: noOpSourceAudit.ID, ProgressionDestinationAuditEventID: noOpDestinationAudit.ID,
		AuditEventID: noOpAudit.ID, AuditAt: at.Add(5 * time.Second), Command: noOpCommand}
	noOpEnrollment, err := ss.ClassMember().Enroll(ctx, noOpInput)
	require.NoError(t, err)
	require.True(t, noOpInput.NoOp, "existing progression destination = %#v no_op", noOpEnrollment)
	require.Equal(t, progressed.Membership.ID, noOpEnrollment.Membership.ID, "existing progression destination = %#v no_op", noOpEnrollment)
}

func testOnboardingImportAcademicAdministration(t *testing.T, ss store.Store) {
	t.Helper()
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	actor, target := saveUser(t, ctx, ss), saveUser(t, ctx, ss)
	at := model.NowUTC().Add(-time.Minute)
	authorityRole, err := SaveRole(ctx, ss, &model.Role{Name: "administration-import-authority-" + model.NewID(), DisplayName: "Administration Import Authority",
		Permissions: []string{string(model.ActionOnboardingBatchManage), string(model.ActionUserManage)}})
	require.NoError(t, err)
	authorityBinding, err := SaveRoleBinding(ctx, ss, &model.RoleBinding{UserID: actor.ID, RoleID: authorityRole.ID,
		ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(), StartsAt: at.Add(-time.Second)})
	require.NoError(t, err)
	session, credentials, _ := saveSession(t, ctx, ss, actor.ID.String(), 10)
	principal := model.Principal{UserID: actor.ID, SessionID: session.ID, CredentialID: model.PrincipalCredentialID(credentials[0].ID),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: session.ClientType, AuthenticatedAt: session.AuthenticatedAt}

	row := store.OnboardingImportRow{Operation: "affiliation.add", ScopeType: model.RoleScopeInstitution,
		ScopeID: institution.ID.String(), TargetRevision: institution.Revision, UserID: target.ID,
		AffiliationKind: model.AffiliationStaff, StartsAt: model.OptionalTimeFrom(at)}
	importID := createExecutingOnboardingImport(t, ctx, ss, actor, principal, model.OnboardingImportAcademicAdministration,
		model.RoleScopeInstitution, institution.ID.String(), "", at, row)
	candidate := &model.Affiliation{UserID: target.ID, Kind: model.AffiliationStaff, StartsAt: at}
	candidate.PrepareCreate(model.NewAffiliationID(), at)
	audit := saveAffiliationAuditAttempt(t, ctx, ss, institution.ID.String(), target.ID.String())
	command := invitationTestCommand(actor.ID, "affiliation.add", "administration-import-row", "administration-import-row")
	command.OnboardingImportID, command.OnboardingImportRowNumber = importID, 1
	created, err := ss.Affiliation().Create(ctx, &store.AffiliationCreation{Affiliation: candidate, AuditEventID: audit.ID,
		AuditAt: at, Command: command})
	require.NoError(t, err)
	completed, err := ss.OnboardingImport().GetOnboardingImport(ctx, importID)
	require.NoError(t, err)
	page, err := ss.OnboardingImport().ListOnboardingImportRows(ctx, importID, 0, 10)
	require.NoError(t, err)
	require.Equal(t, model.OnboardingImportCompleted, completed.State, "academic administration import = %#v rows", completed)
	require.Equal(t, 1, completed.SucceededRows, "academic administration import = %#v rows", completed)
	require.Len(t, page.Rows, 1, "academic administration import = %#v rows", completed)
	require.Equal(t, model.OnboardingImportRowSucceeded, page.Rows[0].Status, "academic administration import = %#v rows", completed)
	require.Equal(t, created.ID.String(), page.Rows[0].ResourceID, "academic administration import = %#v rows", completed)

	duplicateTarget := saveUser(t, ctx, ss)
	duplicateCandidate := &model.Affiliation{UserID: duplicateTarget.ID, Kind: model.AffiliationStaff, StartsAt: at}
	duplicateCandidate.PrepareCreate(model.NewAffiliationID(), at)
	group := sha256.Sum256([]byte("affiliation-staff-" + duplicateTarget.ID.String()))
	authority := &store.CommandAuthorization{Principal: principal, ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(),
		Actions: []model.Action{model.ActionOnboardingBatchManage, model.ActionUserManage}}
	canonicalAudit := saveAffiliationAuditAttempt(t, ctx, ss, institution.ID.String(), duplicateTarget.ID.String())
	canonicalCommand := invitationTestCommand(actor.ID, "affiliation.add", "administration-json-a", "administration-json-duplicate")
	canonicalCommand.Authorization, canonicalCommand.Batch = authority, &store.CommandBatch{GroupDigest: group}
	canonical, err := ss.Affiliation().Create(ctx, &store.AffiliationCreation{Affiliation: duplicateCandidate,
		AuditEventID: canonicalAudit.ID, AuditAt: at, Command: canonicalCommand})
	require.NoError(t, err)

	loserCandidate := *duplicateCandidate
	loserCandidate.ID = model.NewAffiliationID()
	loserAudit := saveAffiliationAuditAttempt(t, ctx, ss, institution.ID.String(), duplicateTarget.ID.String())
	loserCommand := invitationTestCommand(actor.ID, "affiliation.add", "administration-json-b", "administration-json-duplicate")
	loserCommand.Authorization, loserCommand.Batch = authority, &store.CommandBatch{GroupDigest: group, DuplicateOfKeyDigest: canonicalCommand.KeyDigest}
	duplicate, err := ss.Affiliation().Create(ctx, &store.AffiliationCreation{Affiliation: &loserCandidate,
		AuditEventID: loserAudit.ID, AuditAt: at, Command: loserCommand})
	require.NoError(t, err)
	require.Equal(t, canonical.ID, duplicate.ID, "duplicate outcome = %#v metadata=%#v, want canonical", duplicate, loserCommand.Batch)
	require.True(t, loserCommand.Batch.Duplicate, "duplicate outcome = %#v metadata=%#v, want canonical", duplicate, loserCommand.Batch)
	_, err = ss.Affiliation().Get(ctx, loserCandidate.ID)
	require.True(t, store.IsNotFound(err), "duplicate candidate was persisted")
	loserReplayAudit := saveAffiliationAuditAttempt(t, ctx, ss, institution.ID.String(), duplicateTarget.ID.String())
	loserCommand.Batch.Duplicate = false
	_, err = ss.Affiliation().Create(ctx, &store.AffiliationCreation{Affiliation: &loserCandidate,
		AuditEventID: loserReplayAudit.ID, AuditAt: at, Command: loserCommand})
	require.NoError(t, err, "retained duplicate replay metadata=%#v error", loserCommand.Batch)
	require.True(t, loserCommand.Batch.Duplicate, "retained duplicate replay metadata=%#v error", loserCommand.Batch)

	secondTarget := saveUser(t, ctx, ss)
	secondRow := row
	secondRow.UserID = secondTarget.ID
	secondImportID := createExecutingOnboardingImport(t, ctx, ss, actor, principal, model.OnboardingImportAcademicAdministration,
		model.RoleScopeInstitution, institution.ID.String(), "", at.Add(time.Second), secondRow)
	_, err = EndRoleBinding(ctx, ss, authorityBinding.ID, at.Add(2*time.Second))
	require.NoError(t, err)
	secondCandidate := &model.Affiliation{UserID: secondTarget.ID, Kind: model.AffiliationStaff, StartsAt: at}
	secondCandidate.PrepareCreate(model.NewAffiliationID(), at)
	secondAudit := saveAffiliationAuditAttempt(t, ctx, ss, institution.ID.String(), secondTarget.ID.String())
	secondCommand := invitationTestCommand(actor.ID, "affiliation.add", "administration-import-authority-row", "administration-import-authority-row")
	secondCommand.OnboardingImportID, secondCommand.OnboardingImportRowNumber = secondImportID, 1
	_, err = ss.Affiliation().Create(ctx, &store.AffiliationCreation{Affiliation: secondCandidate, AuditEventID: secondAudit.ID,
		AuditAt: at, Command: secondCommand})
	require.True(t, store.IsConflict(err), "academic administration import after authority ended error")
	_, err = ss.Affiliation().Get(ctx, secondCandidate.ID)
	require.True(t, store.IsNotFound(err), "unauthorized import row mutated affiliation")
	jsonTarget := saveUser(t, ctx, ss)
	jsonCandidate := &model.Affiliation{UserID: jsonTarget.ID, Kind: model.AffiliationStaff, StartsAt: at}
	jsonCandidate.PrepareCreate(model.NewAffiliationID(), at)
	jsonAudit := saveAffiliationAuditAttempt(t, ctx, ss, institution.ID.String(), jsonTarget.ID.String())
	jsonCommand := invitationTestCommand(actor.ID, "affiliation.add", "administration-json-revoked", "administration-json-revoked")
	jsonCommand.Authorization = &store.CommandAuthorization{Principal: principal, ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(),
		Actions: []model.Action{model.ActionOnboardingBatchManage, model.ActionUserManage}}
	jsonCommand.Batch = &store.CommandBatch{GroupDigest: sha256.Sum256([]byte("administration-json-revoked"))}
	_, err = ss.Affiliation().Create(ctx, &store.AffiliationCreation{Affiliation: jsonCandidate, AuditEventID: jsonAudit.ID,
		AuditAt: at, Command: jsonCommand})
	require.True(t, store.IsConflict(err), "JSON row after authority ended error")
	_, err = ss.Affiliation().Get(ctx, jsonCandidate.ID)
	require.True(t, store.IsNotFound(err), "unauthorized JSON row mutated affiliation")
}

func testOnboardingImportTeacherAndRoleNoOps(t *testing.T, ss store.Store) {
	t.Helper()
	ctx := context.Background()
	unit, _ := saveProgrammeParents(t, ctx, ss, "onboarding-cross-kind-"+model.NewID())
	actor := saveUser(t, ctx, ss)
	at := model.NowUTC().Add(-time.Minute)
	targetRole, err := SaveRole(ctx, ss, &model.Role{Name: "onboarding-target-" + model.NewID(), DisplayName: "Onboarding Target",
		Permissions: []string{string(model.ActionAcademicUnitView)}})
	require.NoError(t, err)
	authorityRole, err := SaveRole(ctx, ss, &model.Role{Name: "onboarding-authority-" + model.NewID(), DisplayName: "Onboarding Authority",
		Permissions: []string{string(model.ActionOnboardingBatchManage), string(model.ActionInvitationCreate), string(model.ActionAcademicUnitMembersManage),
			string(model.ActionRoleBindingManage), string(model.ActionAcademicUnitView)}})
	require.NoError(t, err)
	_, err = SaveRoleBinding(ctx, ss, &model.RoleBinding{UserID: actor.ID, RoleID: authorityRole.ID, ScopeType: model.RoleScopeInstitution,
		ScopeID: unit.InstitutionID.String(), StartsAt: at.Add(-time.Second)})
	require.NoError(t, err)
	session, credentials, _ := saveSession(t, ctx, ss, actor.ID.String(), 10)
	principal := model.Principal{UserID: actor.ID, SessionID: session.ID, CredentialID: model.PrincipalCredentialID(credentials[0].ID),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: session.ClientType, AuthenticatedAt: session.AuthenticatedAt}

	teacherIssue := teacherAcademicUnitInvitationIssueFixture(t, ss, actor, unit, targetRole, at)
	teacherIssue.Invitation.TargetEmail = "onboarding-teacher-" + model.NewID() + "@example.edu"
	require.NoError(t, teacherIssue.Invitation.Validate())
	teacher, err := ss.Invitation().IssueTeacherAcademicUnit(ctx, teacherIssue)
	require.NoError(t, err)
	teacherImportID := createExecutingOnboardingImport(t, ctx, ss, actor, principal, model.OnboardingImportTeacherAcademicUnit,
		model.RoleScopeAcademicUnit, unit.ID.String(), targetRole.ID, at, store.OnboardingImportRow{Operation: "teacher_academic_unit.create",
			ScopeType: model.RoleScopeAcademicUnit, ScopeID: unit.ID.String(), TargetRevision: unit.Revision, RoleID: targetRole.ID,
			RoleRevision: targetRole.UpdatedAt.UnixMicro(), Email: teacher.TargetEmail, StartsAt: model.OptionalTimeFrom(teacher.IntendedStartsAt)})
	teacherNoMail := teacherAcademicUnitInvitationIssueFixture(t, ss, actor, unit, targetRole, at.Add(time.Second))
	teacherNoMail.Invitation.TargetEmail, teacherNoMail.Invitation.IntendedStartsAt = teacher.TargetEmail, teacher.IntendedStartsAt
	require.NoError(t, teacherNoMail.Invitation.Validate())
	teacherNoMail.Occurrence, teacherNoMail.Delivery, teacherNoMail.DeliveryJob = nil, nil, nil
	teacherCommand := invitationTestCommand(actor.ID, "invitation.teacher_academic_unit.issue", "onboarding-teacher-row", "onboarding-teacher-row")
	teacherCommand.OnboardingImportID, teacherCommand.OnboardingImportRowNumber = teacherImportID, 1
	teacherResult, err := ss.Invitation().IssueTeacherAcademicUnitIdempotently(ctx, teacherNoMail, teacherCommand)
	require.NoError(t, err)
	require.True(t, teacherResult.NoOp)
	require.NotNil(t, teacherResult.Invitation)
	require.Equal(t, teacher.ID, teacherResult.Invitation.ID)

	roleCandidate, err := model.NewScopedRoleInvitation(model.ScopedRoleInvitationInput{ID: model.NewInvitationID(), Purpose: model.InvitationPurposeAcademicUnitRole,
		TargetEmail: "onboarding-role-" + model.NewID() + "@example.edu", AcademicUnitID: unit.ID, RoleID: targetRole.ID, RoleActions: targetRole.Permissions,
		IntendedStartsAt: at, InviterUserID: actor.ID, ScopeType: model.RoleScopeAcademicUnit, ScopeID: unit.ID.String(),
		ClaimHash: model.HashInvitationClaim(model.NewCredentialToken()), IssuedAt: at})
	require.NoError(t, err)
	roleInvitation, err := ss.Invitation().IssueScopedRole(ctx, scopedRoleInvitationIssueFixture(t, ss, roleCandidate))
	require.NoError(t, err)
	roleImportID := createExecutingOnboardingImport(t, ctx, ss, actor, principal, model.OnboardingImportInstitution,
		model.RoleScopeInstitution, unit.InstitutionID.String(), "", at, store.OnboardingImportRow{Operation: "academic_unit_role.create",
			ScopeType: model.RoleScopeAcademicUnit, ScopeID: unit.ID.String(), TargetRevision: unit.Revision, RoleID: targetRole.ID,
			RoleRevision: targetRole.UpdatedAt.UnixMicro(), Email: roleInvitation.TargetEmail, StartsAt: model.OptionalTimeFrom(roleInvitation.IntendedStartsAt)})
	roleNoMail := scopedRoleInvitationIssueFixture(t, ss, roleCandidate)
	roleNoMail.Invitation.ID = model.NewInvitationID()
	roleNoMail.Invitation.ClaimHash = model.HashInvitationClaim(model.NewCredentialToken())
	require.NoError(t, roleNoMail.Invitation.Validate())
	roleNoMail.Occurrence, roleNoMail.Delivery, roleNoMail.DeliveryJob = nil, nil, nil
	roleCommand := invitationTestCommand(actor.ID, "invitation.academic_unit_role.issue", "onboarding-role-row", "onboarding-role-row")
	roleCommand.OnboardingImportID, roleCommand.OnboardingImportRowNumber = roleImportID, 1
	roleResult, err := ss.Invitation().IssueScopedRoleIdempotently(ctx, roleNoMail, roleCommand)
	require.NoError(t, err)
	require.True(t, roleResult.NoOp)
	require.NotNil(t, roleResult.Invitation)
	require.Equal(t, roleInvitation.ID, roleResult.Invitation.ID)
}

func testOnboardingImportLifecycle(t *testing.T, ss store.Store) {
	t.Helper()
	ctx := context.Background()
	fixture, class, actor, _ := invitationAdministrationFixture(t, ctx, ss, "onboarding-import")
	at := model.TimeUTC(time.Now())
	session, credentials, _ := saveSession(t, ctx, ss, actor.ID.String(), 10)
	principal := model.Principal{UserID: actor.ID, SessionID: session.ID, CredentialID: model.PrincipalCredentialID(credentials[0].ID),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: session.ClientType, AuthenticatedAt: session.AuthenticatedAt}
	importID := model.NewOnboardingImportID()
	scopeID := class.ID.String()
	parseJob, err := model.NewJob(model.NewJobID(), model.JobTypeOnboardingImportParse, json.RawMessage(`{"import_id":"`+importID.String()+`"}`), "parse:"+importID.String(), at, at, 3)
	require.NoError(t, err)
	creationAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, scopeID, "onboarding_import.upload")
	created, err := ss.OnboardingImport().CreateOnboardingImport(ctx, &store.OnboardingImportCreation{Import: &store.OnboardingImport{ID: importID,
		Mode: model.OnboardingImportStudentClass, State: model.OnboardingImportParsing, ScopeType: model.RoleScopeClass, ScopeID: scopeID,
		ActorUserID: actor.ID, Principal: principal, ParseJobID: parseJob.ID, CreatedAt: at, UpdatedAt: at, ExpiresAt: at.Add(7 * 24 * time.Hour), Revision: 1}, ParseJob: parseJob,
		AuditEventID: creationAudit.ID, AuditAt: at})
	require.NoError(t, err)
	digest := strings.Repeat("a", sha256.Size*2)
	preview, err := ss.OnboardingImport().CompleteOnboardingImportPreview(ctx, &store.OnboardingImportPreviewCompletion{ID: importID, ExpectedRevision: created.Revision,
		Digest: digest, IgnoredHeaders: []string{"misspelled"}, At: at.Add(time.Second), Rows: []store.OnboardingImportRow{{ImportID: importID, RowNumber: 1,
			Reference: "row-1", Operation: "student_class.create", ScopeType: model.RoleScopeClass, ScopeID: created.ScopeID, TargetRevision: 1,
			Email: "student@example.edu", PreviewStatus: model.OnboardingImportRowValid, Status: model.OnboardingImportRowValid}}})
	require.NoError(t, err)
	require.Equal(t, model.OnboardingImportPreviewReady, preview.State)
	require.Equal(t, 1, preview.ValidRows)
	require.Equal(t, 0, preview.InvalidRows)
	executionJob, err := model.NewJob(model.NewJobID(), model.JobTypeOnboardingImportExecute, json.RawMessage(`{"import_id":"`+importID.String()+`"}`), "execute:"+importID.String(), at, at, 3)
	require.NoError(t, err)
	key := sha256.Sum256([]byte("commit-key"))
	commit := &store.OnboardingImportCommit{ID: importID, ActorUserID: actor.ID, Principal: principal, ExpectedRevision: preview.Revision, PreviewDigest: digest,
		Policy: model.OnboardingImportRequireAllValid, IdempotencyKey: key, ExecutionJob: executionJob, At: at.Add(2 * time.Second)}
	commitAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, created.ScopeID, "onboarding_import.commit")
	commit.AuditEventID, commit.AuditAt = commitAudit.ID, at.Add(2*time.Second)
	executing, err := ss.OnboardingImport().CommitOnboardingImport(ctx, commit)
	require.NoError(t, err)
	require.Equal(t, model.OnboardingImportExecuting, executing.State)
	require.Equal(t, executionJob.ID, executing.ExecutionJobID)
	retryJob, _ := model.NewJob(model.NewJobID(), model.JobTypeOnboardingImportExecute, json.RawMessage(`{"import_id":"`+importID.String()+`"}`), "retry:"+importID.String(), at, at, 3)
	commit.ExecutionJob = retryJob
	replayAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, created.ScopeID, "onboarding_import.commit")
	commit.AuditEventID, commit.AuditAt = replayAudit.ID, at.Add(2*time.Second)
	replayed, err := ss.OnboardingImport().CommitOnboardingImport(ctx, commit)
	require.NoError(t, err, "replay = %#v", replayed)
	require.Equal(t, executionJob.ID, replayed.ExecutionJobID, "replay = %#v", replayed)
	changed := *commit
	changed.IdempotencyKey = sha256.Sum256([]byte("changed"))
	changedAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, created.ScopeID, "onboarding_import.commit")
	changed.AuditEventID, changed.AuditAt = changedAudit.ID, at.Add(2*time.Second)
	_, err = ss.OnboardingImport().CommitOnboardingImport(ctx, &changed)
	require.True(t, store.IsConflict(err), "changed key error")
	issue := studentClassInvitationIssueFixture(t, ss, actor, class, fixture.period, at)
	command := invitationTestCommand(actor.ID, "invitation.student_class.issue", "onboarding-row-1", "onboarding-row-1")
	command.OnboardingImportID, command.OnboardingImportRowNumber = importID, 1
	issued, err := ss.Invitation().IssueStudentClassIdempotently(ctx, issue, command)
	require.NoError(t, err)
	invitationID := issued.Invitation.ID
	completed, err := ss.OnboardingImport().GetOnboardingImport(ctx, importID)
	require.NoError(t, err)
	require.Equal(t, model.OnboardingImportCompleted, completed.State)
	require.Equal(t, 1, completed.SucceededRows)
	lateReplayAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, created.ScopeID, "onboarding_import.commit")
	commit.AuditEventID, commit.AuditAt = lateReplayAudit.ID, at.Add(3*time.Second)
	lateReplay, err := ss.OnboardingImport().CommitOnboardingImport(ctx, commit)
	require.NoError(t, err)
	require.Equal(t, executionJob.ID, lateReplay.ExecutionJobID)
	require.Equal(t, model.OnboardingImportExecuting, lateReplay.State)
	require.Equal(t, preview.Revision+1, lateReplay.Revision)
	require.Equal(t, 0, lateReplay.SucceededRows)
	require.True(t, lateReplay.UpdatedAt.Equal(commit.At))
	changedRevision := *commit
	changedRevision.ExpectedRevision++
	changedRevisionAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, created.ScopeID, "onboarding_import.commit")
	changedRevision.AuditEventID, changedRevision.AuditAt = changedRevisionAudit.ID, at.Add(3*time.Second)
	_, err = ss.OnboardingImport().CommitOnboardingImport(ctx, &changedRevision)
	require.True(t, store.IsConflict(err), "changed-revision late replay error")
	page, err := ss.OnboardingImport().ListOnboardingImportRows(ctx, importID, 0, 10)
	require.NoError(t, err, "rows = %#v", page)
	require.Len(t, page.Rows, 1, "rows = %#v", page)
	require.Equal(t, invitationID, page.Rows[0].InvitationID, "rows = %#v", page)

	noOpID := createExecutingStudentOnboardingImport(t, ctx, ss, actor, principal, class, at.Add(-time.Minute), "student@example.edu")
	noOpIssue := studentClassInvitationIssueFixture(t, ss, actor, class, fixture.period, at)
	resolved, isNoOp, err := ss.Invitation().ResolveOnboardingInvitationNoOp(ctx, noOpIssue.Invitation)
	require.NoError(t, err)
	require.True(t, isNoOp, "pending-effect preflight")
	require.NotNil(t, resolved, "pending-effect preflight")
	require.Equal(t, invitationID, resolved.ID, "pending-effect preflight")
	unusedDeliveryID := noOpIssue.Delivery.ID
	noOpIssue.Occurrence, noOpIssue.Delivery, noOpIssue.DeliveryJob = nil, nil, nil
	noOpCommand := invitationTestCommand(actor.ID, "invitation.student_class.issue", "onboarding-row-no-op", "onboarding-row-no-op")
	noOpCommand.OnboardingImportID, noOpCommand.OnboardingImportRowNumber = noOpID, 1
	noOpResult, err := ss.Invitation().IssueStudentClassIdempotently(ctx, noOpIssue, noOpCommand)
	require.NoError(t, err)
	require.True(t, noOpResult.NoOp)
	require.NotNil(t, noOpResult.Invitation)
	require.Equal(t, invitationID, noOpResult.Invitation.ID)
	noOpImport, err := ss.OnboardingImport().GetOnboardingImport(ctx, noOpID)
	require.NoError(t, err)
	require.Equal(t, model.OnboardingImportCompleted, noOpImport.State)
	require.Equal(t, 1, noOpImport.NoOpRows)
	_, err = ss.Mail().GetDelivery(ctx, unusedDeliveryID)
	require.True(t, store.IsNotFound(err), "no-op delivery error")

	cancelRaceID := createExecutingStudentOnboardingImport(t, ctx, ss, actor, principal, class, at.Add(-2*time.Minute), "canceled-row@example.edu")
	cancelRaceAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, class.ID.String(), "onboarding_import.cancel")
	_, err = ss.OnboardingImport().CancelOnboardingImport(ctx, &store.OnboardingImportCancellation{ID: cancelRaceID, ActorUserID: actor.ID, Principal: principal,
		At: at, AuditEventID: cancelRaceAudit.ID, AuditAt: at})
	require.NoError(t, err)
	canceledIssue := studentClassInvitationIssueFixture(t, ss, actor, class, fixture.period, at)
	canceledIssue.Invitation.TargetEmail = "canceled-row@example.edu"
	require.NoError(t, canceledIssue.Invitation.Validate())
	canceledCommand := invitationTestCommand(actor.ID, "invitation.student_class.issue", "onboarding-row-canceled", "onboarding-row-canceled")
	canceledCommand.OnboardingImportID, canceledCommand.OnboardingImportRowNumber = cancelRaceID, 1
	_, err = ss.Invitation().IssueStudentClassIdempotently(ctx, canceledIssue, canceledCommand)
	require.True(t, store.IsConflict(err), "canceled row issue error")
	_, err = ss.Invitation().Get(ctx, canceledIssue.Invitation.ID)
	require.True(t, store.IsNotFound(err), "canceled row Invitation error")

	emptyID := model.NewOnboardingImportID()
	emptyParseJob, err := model.NewJob(model.NewJobID(), model.JobTypeOnboardingImportParse, json.RawMessage(`{"import_id":"`+emptyID.String()+`"}`), "parse:"+emptyID.String(), at, at, 3)
	require.NoError(t, err)
	emptyCreationAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, scopeID, "onboarding_import.upload")
	emptyCreated, err := ss.OnboardingImport().CreateOnboardingImport(ctx, &store.OnboardingImportCreation{Import: &store.OnboardingImport{ID: emptyID,
		Mode: model.OnboardingImportStudentClass, State: model.OnboardingImportParsing, ScopeType: model.RoleScopeClass, ScopeID: scopeID,
		ActorUserID: actor.ID, Principal: principal, ParseJobID: emptyParseJob.ID, CreatedAt: at, UpdatedAt: at, ExpiresAt: at.Add(7 * 24 * time.Hour), Revision: 1},
		ParseJob: emptyParseJob, AuditEventID: emptyCreationAudit.ID, AuditAt: at})
	require.NoError(t, err)
	emptyPreview, err := ss.OnboardingImport().CompleteOnboardingImportPreview(ctx, &store.OnboardingImportPreviewCompletion{ID: emptyID, ExpectedRevision: emptyCreated.Revision,
		Digest: strings.Repeat("b", sha256.Size*2), At: at.Add(time.Second), Rows: []store.OnboardingImportRow{{ImportID: emptyID, RowNumber: 1,
			Reference: "invalid-row", ScopeType: model.RoleScopeClass, ScopeID: scopeID, PreviewStatus: model.OnboardingImportRowInvalid, PreviewCode: "request.invalid", Status: model.OnboardingImportRowInvalid}}})
	require.NoError(t, err)
	emptyExecutionJob, err := model.NewJob(model.NewJobID(), model.JobTypeOnboardingImportExecute, json.RawMessage(`{"import_id":"`+emptyID.String()+`"}`), "execute:"+emptyID.String(), at, at, 3)
	require.NoError(t, err)
	emptyCommitAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, scopeID, "onboarding_import.commit")
	emptyExecuting, err := ss.OnboardingImport().CommitOnboardingImport(ctx, &store.OnboardingImportCommit{ID: emptyID, ActorUserID: actor.ID, Principal: principal, ExpectedRevision: emptyPreview.Revision,
		PreviewDigest: emptyPreview.PreviewDigest, Policy: model.OnboardingImportValidRowsOnly, IdempotencyKey: sha256.Sum256([]byte("empty-commit")), ExecutionJob: emptyExecutionJob,
		At: at.Add(2 * time.Second), AuditEventID: emptyCommitAudit.ID, AuditAt: at.Add(2 * time.Second)})
	require.NoError(t, err)
	emptyFinished, err := ss.OnboardingImport().FinishOnboardingImport(ctx, &store.OnboardingImportFinish{ID: emptyID, At: at.Add(3 * time.Second)})
	require.NoError(t, err)
	require.Equal(t, model.OnboardingImportExecuting, emptyExecuting.State, "all-invalid import = %#v / %#v", emptyExecuting, emptyFinished)
	require.Equal(t, model.OnboardingImportCompleted, emptyFinished.State, "all-invalid import = %#v / %#v", emptyExecuting, emptyFinished)
	require.Equal(t, 1, emptyFinished.SkippedRows, "all-invalid import = %#v / %#v", emptyExecuting, emptyFinished)

	cancelID := model.NewOnboardingImportID()
	cancelJob, err := model.NewJob(model.NewJobID(), model.JobTypeOnboardingImportParse, json.RawMessage(`{"import_id":"`+cancelID.String()+`"}`), "parse:"+cancelID.String(), at, at, 3)
	require.NoError(t, err)
	cancelCreationAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, scopeID, "onboarding_import.upload")
	_, err = ss.OnboardingImport().CreateOnboardingImport(ctx, &store.OnboardingImportCreation{Import: &store.OnboardingImport{ID: cancelID,
		Mode: model.OnboardingImportStudentClass, State: model.OnboardingImportParsing, ScopeType: model.RoleScopeClass, ScopeID: scopeID,
		ActorUserID: actor.ID, Principal: principal, ParseJobID: cancelJob.ID, CreatedAt: at, UpdatedAt: at, ExpiresAt: at.Add(7 * 24 * time.Hour), Revision: 1},
		ParseJob: cancelJob, AuditEventID: cancelCreationAudit.ID, AuditAt: at})
	require.NoError(t, err)
	cancelAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, scopeID, "onboarding_import.cancel")
	canceled, err := ss.OnboardingImport().CancelOnboardingImport(ctx, &store.OnboardingImportCancellation{ID: cancelID, ActorUserID: actor.ID, Principal: principal, At: at.Add(time.Second),
		AuditEventID: cancelAudit.ID, AuditAt: at.Add(time.Second)})
	require.NoError(t, err)
	storedJob, err := ss.Job().Get(ctx, cancelJob.ID)
	require.NoError(t, err)
	require.Equal(t, model.OnboardingImportCanceled, canceled.State, "canceled import/job = %#v / %#v", canceled, storedJob)
	require.Equal(t, model.JobStatusCanceled, storedJob.Status, "canceled import/job = %#v / %#v", canceled, storedJob)

	oldAt := at.Add(-8 * 24 * time.Hour)
	expiredID := model.NewOnboardingImportID()
	expiredJob, err := model.NewJob(model.NewJobID(), model.JobTypeOnboardingImportParse, json.RawMessage(`{"import_id":"`+expiredID.String()+`"}`), "parse:"+expiredID.String(), oldAt, oldAt, 3)
	require.NoError(t, err)
	expiredAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, scopeID, "onboarding_import.upload")
	_, err = ss.OnboardingImport().CreateOnboardingImport(ctx, &store.OnboardingImportCreation{Import: &store.OnboardingImport{ID: expiredID,
		Mode: model.OnboardingImportStudentClass, State: model.OnboardingImportParsing, ScopeType: model.RoleScopeClass, ScopeID: scopeID,
		ActorUserID: actor.ID, Principal: principal, ParseJobID: expiredJob.ID, CreatedAt: oldAt, UpdatedAt: oldAt, ExpiresAt: oldAt.Add(7 * 24 * time.Hour), Revision: 1},
		ParseJob: expiredJob, AuditEventID: expiredAudit.ID, AuditAt: at})
	require.NoError(t, err)
	expired, err := ss.OnboardingImport().ListExpiredOnboardingImports(ctx, 100, at)
	require.NoError(t, err)
	require.Contains(t, expired, expiredID)
	purged, err := ss.OnboardingImport().PurgeOnboardingImport(ctx, &store.OnboardingImportPurge{ID: expiredID, At: at})
	require.NoError(t, err)
	require.True(t, purged, "expired onboarding import was not purged")
}

func createExecutingStudentOnboardingImport(t *testing.T, ctx context.Context, ss store.Store, actor *model.User, principal model.Principal,
	class *model.Class, at time.Time, email string,
) model.OnboardingImportID {
	t.Helper()
	id := model.NewOnboardingImportID()
	parseJob, err := model.NewJob(model.NewJobID(), model.JobTypeOnboardingImportParse, json.RawMessage(`{"import_id":"`+id.String()+`"}`), "parse:"+id.String(), at, at, 3)
	require.NoError(t, err)
	creationAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, class.ID.String(), "onboarding_import.upload")
	created, err := ss.OnboardingImport().CreateOnboardingImport(ctx, &store.OnboardingImportCreation{Import: &store.OnboardingImport{ID: id,
		Mode: model.OnboardingImportStudentClass, State: model.OnboardingImportParsing, ScopeType: model.RoleScopeClass, ScopeID: class.ID.String(), ActorUserID: actor.ID, Principal: principal, ParseJobID: parseJob.ID, CreatedAt: at, UpdatedAt: at, ExpiresAt: at.Add(7 * 24 * time.Hour), Revision: 1}, ParseJob: parseJob,
		AuditEventID: creationAudit.ID, AuditAt: at})
	require.NoError(t, err)
	preview, err := ss.OnboardingImport().CompleteOnboardingImportPreview(ctx, &store.OnboardingImportPreviewCompletion{ID: id, ExpectedRevision: created.Revision,
		Digest: strings.Repeat("c", sha256.Size*2), At: at.Add(time.Second), Rows: []store.OnboardingImportRow{{ImportID: id, RowNumber: 1, Reference: "row-1",
			Operation: "student_class.create", ScopeType: model.RoleScopeClass, ScopeID: class.ID.String(), TargetRevision: class.Revision, Email: email,
			PreviewStatus: model.OnboardingImportRowValid, Status: model.OnboardingImportRowValid}}})
	require.NoError(t, err)
	executionJob, err := model.NewJob(model.NewJobID(), model.JobTypeOnboardingImportExecute, json.RawMessage(`{"import_id":"`+id.String()+`"}`), "execute:"+id.String(), at, at, 3)
	require.NoError(t, err)
	commitAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, class.ID.String(), "onboarding_import.commit")
	_, err = ss.OnboardingImport().CommitOnboardingImport(ctx, &store.OnboardingImportCommit{ID: id, ActorUserID: actor.ID, Principal: principal, ExpectedRevision: preview.Revision,
		PreviewDigest: preview.PreviewDigest, Policy: model.OnboardingImportRequireAllValid, IdempotencyKey: sha256.Sum256([]byte("commit:" + id.String())), ExecutionJob: executionJob,
		At: at.Add(2 * time.Second), AuditEventID: commitAudit.ID, AuditAt: at.Add(2 * time.Second)})
	require.NoError(t, err)
	return id
}

func createExecutingOnboardingImport(t *testing.T, ctx context.Context, ss store.Store, actor *model.User, principal model.Principal,
	mode model.OnboardingImportMode, scopeType model.RoleScopeType, scopeID string, importRoleID model.RoleID, at time.Time, row store.OnboardingImportRow,
) model.OnboardingImportID {
	t.Helper()
	id := model.NewOnboardingImportID()
	parseJob, err := model.NewJob(model.NewJobID(), model.JobTypeOnboardingImportParse, json.RawMessage(`{"import_id":"`+id.String()+`"}`), "parse:"+id.String(), at, at, 3)
	require.NoError(t, err)
	creationAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, scopeType, scopeID, "onboarding_import.upload")
	created, err := ss.OnboardingImport().CreateOnboardingImport(ctx, &store.OnboardingImportCreation{Import: &store.OnboardingImport{ID: id,
		Mode: mode, State: model.OnboardingImportParsing, ScopeType: scopeType, ScopeID: scopeID, RoleID: importRoleID, ActorUserID: actor.ID, Principal: principal, ParseJobID: parseJob.ID, CreatedAt: at, UpdatedAt: at, ExpiresAt: at.Add(7 * 24 * time.Hour), Revision: 1}, ParseJob: parseJob,
		AuditEventID: creationAudit.ID, AuditAt: at})
	require.NoError(t, err)
	row.ImportID, row.RowNumber, row.Reference = id, 1, "row-1"
	row.PreviewStatus, row.Status, row.UpdatedAt = model.OnboardingImportRowValid, model.OnboardingImportRowValid, at.Add(time.Second)
	preview, err := ss.OnboardingImport().CompleteOnboardingImportPreview(ctx, &store.OnboardingImportPreviewCompletion{ID: id, ExpectedRevision: created.Revision,
		Digest: strings.Repeat("e", sha256.Size*2), At: at.Add(time.Second), Rows: []store.OnboardingImportRow{row}})
	require.NoError(t, err)
	executionJob, err := model.NewJob(model.NewJobID(), model.JobTypeOnboardingImportExecute, json.RawMessage(`{"import_id":"`+id.String()+`"}`), "execute:"+id.String(), at, at, 3)
	require.NoError(t, err)
	commitAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, scopeType, scopeID, "onboarding_import.commit")
	_, err = ss.OnboardingImport().CommitOnboardingImport(ctx, &store.OnboardingImportCommit{ID: id, ActorUserID: actor.ID, Principal: principal, ExpectedRevision: preview.Revision,
		PreviewDigest: preview.PreviewDigest, Policy: model.OnboardingImportRequireAllValid, IdempotencyKey: sha256.Sum256([]byte("commit:" + id.String())), ExecutionJob: executionJob,
		At: at.Add(2 * time.Second), AuditEventID: commitAudit.ID, AuditAt: at.Add(2 * time.Second)})
	require.NoError(t, err)
	return id
}

func saveOnboardingImportAuditAttempt(t *testing.T, ctx context.Context, ss store.Store, actor model.UserID, scopeType model.RoleScopeType, scopeID, action string) *model.AuditEvent {
	t.Helper()
	resourceType := model.ResourceInstitution
	if scopeType == model.RoleScopeAcademicUnit {
		resourceType = model.ResourceAcademicUnit
	} else if scopeType == model.RoleScopeClass {
		resourceType = model.ResourceClass
	}
	audit, err := ss.Audit().Save(ctx, &model.AuditEvent{ActorID: actor, Action: action, Resource: model.Resource{Type: resourceType, ID: scopeID},
		ScopeType: scopeType, ScopeID: scopeID, Status: model.AuditStatusAttempt, NodeID: "onboarding-import-store-test"})
	require.NoError(t, err)
	return audit
}

func testInvitationBatchCommandIdempotency(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture, class, inviter, issuedAt := invitationAdministrationFixture(t, ctx, ss, "batch-idempotency")
	issue := studentClassInvitationIssueFixture(t, ss, inviter, class, fixture.period, issuedAt)
	issue.Invitation.TargetEmail = "batch-" + model.NewID() + "@example.edu"
	require.NoError(t, issue.Invitation.Validate())
	issueCommand := invitationTestCommand(inviter.ID, "invitation.student_class.issue", "batch-row-0", "same-issue")
	created, err := ss.Invitation().IssueStudentClassIdempotently(ctx, issue, issueCommand)
	require.NoError(t, err)
	require.False(t, created.Replayed)
	require.Equal(t, issue.Invitation.ID, created.Invitation.ID)

	replayIssue := studentClassInvitationIssueFixture(t, ss, inviter, class, fixture.period, issuedAt.Add(time.Second))
	replayIssue.Invitation.TargetEmail = issue.Invitation.TargetEmail
	require.NoError(t, replayIssue.Invitation.Validate())
	replayed, err := ss.Invitation().IssueStudentClassIdempotently(ctx, replayIssue, issueCommand)
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
	require.Equal(t, created.Invitation.ID, replayed.Invitation.ID)
	_, err = ss.Mail().GetDelivery(ctx, replayIssue.Delivery.ID)
	require.True(t, store.IsNotFound(err), "replayed issue inserted duplicate delivery")
	replayAudit, err := ss.Audit().Get(ctx, replayIssue.AuditEventID)
	require.NoError(t, err)
	require.Equal(t, model.AuditStatusSuccess, replayAudit.Status)
	require.Contains(t, string(replayAudit.Result), "idempotency_replayed")
	require.NotContains(t, string(replayAudit.Result), issue.Invitation.TargetEmail)
	conflicting := invitationTestCommand(inviter.ID, issueCommand.Operation, "batch-row-0", "changed-issue")
	if _, err = ss.Invitation().IssueStudentClassIdempotently(ctx, replayIssue, conflicting); err == nil {
		require.FailNow(t, "conflicting Invitation issue idempotency reuse succeeded")
	} else {
		var conflict *store.ErrIdempotencyConflict
		require.ErrorAs(t, err, &conflict)
	}

	newHash := model.HashInvitationClaim(model.NewCredentialToken())
	occurrence, delivery, job := invitationLifecycleMailFixture(t, created.Invitation.ID, inviter.ID,
		model.MailTemplateAccessStudentClassInvitation, model.JobTypeMailDeliverCredential, model.NowUTC(), created.Invitation.ExpiresAt)
	resendAudit := saveInvitationLifecycleAuditAttempt(t, ctx, ss, inviter.ID, class.ID.String(), "invitation.resend")
	resend := &store.InvitationResend{ID: created.Invitation.ID, ExpectedRevision: created.Invitation.Revision,
		ClaimHash: newHash, Occurrence: occurrence, Delivery: delivery, DeliveryJob: job, ActorUserID: inviter.ID,
		AuditEventID: resendAudit.ID, AuditAt: model.NowUTC()}
	resendCommand := invitationTestCommand(inviter.ID, "invitation.resend", "batch-row-1", "same-resend")
	resent, err := ss.Invitation().ResendIdempotently(ctx, resend, resendCommand)
	require.NoError(t, err)
	require.False(t, resent.Replayed)
	require.Equal(t, created.Invitation.Revision+1, resent.Record.Invitation.Revision)

	replayOccurrence, replayDelivery, replayJob := invitationLifecycleMailFixture(t, created.Invitation.ID, inviter.ID,
		model.MailTemplateAccessStudentClassInvitation, model.JobTypeMailDeliverCredential, model.NowUTC(), created.Invitation.ExpiresAt)
	replayResendAudit := saveInvitationLifecycleAuditAttempt(t, ctx, ss, inviter.ID, class.ID.String(), "invitation.resend")
	replayResend := &store.InvitationResend{ID: created.Invitation.ID, ExpectedRevision: created.Invitation.Revision,
		ClaimHash: model.HashInvitationClaim(model.NewCredentialToken()), Occurrence: replayOccurrence, Delivery: replayDelivery,
		DeliveryJob: replayJob, ActorUserID: inviter.ID, AuditEventID: replayResendAudit.ID, AuditAt: model.NowUTC()}
	replayedResend, err := ss.Invitation().ResendIdempotently(ctx, replayResend, resendCommand)
	require.NoError(t, err)
	require.True(t, replayedResend.Replayed)
	require.Equal(t, resent.Record.Invitation.Revision, replayedResend.Record.Invitation.Revision)
	_, err = ss.Mail().GetDelivery(ctx, replayDelivery.ID)
	require.True(t, store.IsNotFound(err), "replayed resend inserted duplicate delivery")

	duplicateAudit := saveInvitationLifecycleAuditAttempt(t, ctx, ss, inviter.ID, class.ID.String(), "onboarding_batch.duplicate")
	duplicateCommand := invitationTestCommand(inviter.ID, resendCommand.Operation, "batch-row-2", "duplicate-resend")
	duplicate, err := ss.Invitation().RecordBatchDuplicate(ctx, &store.InvitationBatchDuplicate{
		LifecycleID: created.Invitation.ID, ExpectedRevision: created.Invitation.Revision, ActorUserID: inviter.ID,
		CanonicalOperation: resendCommand.Operation, CanonicalKeyDigest: resendCommand.KeyDigest, CanonicalFingerprint: resendCommand.Fingerprint,
		AuditEventID: duplicateAudit.ID, AuditAt: model.NowUTC(),
	}, duplicateCommand)
	require.NoError(t, err)
	require.True(t, duplicate.Duplicate)
	require.False(t, duplicate.Replayed)

	staleAudit := saveInvitationLifecycleAuditAttempt(t, ctx, ss, inviter.ID, class.ID.String(), "onboarding_batch.duplicate")
	staleCommand := invitationTestCommand(inviter.ID, resendCommand.Operation, "batch-row-3", "stale-resend")
	_, err = ss.Invitation().RecordBatchDuplicate(ctx, &store.InvitationBatchDuplicate{
		LifecycleID: created.Invitation.ID, ExpectedRevision: created.Invitation.Revision, ActorUserID: inviter.ID,
		CanonicalOperation: resendCommand.Operation, CanonicalKeyDigest: resendCommand.KeyDigest,
		CanonicalFingerprint: sha256.Sum256([]byte("missing-canonical-fingerprint")),
		AuditEventID:         staleAudit.ID, AuditAt: model.NowUTC(),
	}, staleCommand)
	require.True(t, store.IsConflict(err), "stale lifecycle duplicate without canonical outcome error")
}

func testInvitationBatchDuplicateRechecksCurrentInviterAuthority(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture, class, inviter, _, binding, issuedAt := invitationAcceptanceStoreFixture(t, ctx, ss, "batch-duplicate-authority")
	candidate := studentClassInvitationIssueFixture(t, ss, inviter, class, fixture.period, issuedAt).Invitation
	candidate.TargetEmail = "duplicate-authority-" + model.NewID() + "@example.edu"
	require.NoError(t, candidate.Validate())
	endedAt := model.NowUTC().Add(-1_000 * time.Millisecond)
	_, err := EndRoleBinding(ctx, ss, binding.ID, endedAt)
	require.NoError(t, err)
	attempt := saveInvitationLifecycleAuditAttempt(t, ctx, ss, inviter.ID, class.ID.String(), "onboarding_batch.duplicate")
	command := invitationTestCommand(inviter.ID, "invitation.student_class.issue", "duplicate-row", "duplicate-package")
	_, err = ss.Invitation().RecordBatchDuplicate(ctx, &store.InvitationBatchDuplicate{
		Candidate: candidate, ActorUserID: inviter.ID,
		CanonicalOperation: command.Operation, CanonicalKeyDigest: sha256.Sum256([]byte("canonical-row")),
		AuditEventID: attempt.ID, AuditAt: endedAt.Add(1 * time.Millisecond),
	}, command)
	require.True(t, store.IsConflict(err), "RecordBatchDuplicate() after inviter authority ended error")
	current, getErr := ss.Audit().Get(ctx, attempt.ID)
	require.NoError(t, getErr)
	require.Equal(t, model.AuditStatusAttempt, current.Status)
}

func invitationTestCommand(userID model.UserID, operation, key, fingerprint string) *store.CommandIdempotency {
	return &store.CommandIdempotency{UserID: userID, Operation: operation, KeyDigest: sha256.Sum256([]byte(key)), Fingerprint: sha256.Sum256([]byte(fingerprint)),
		Retention: 24 * time.Hour, Wait: 2 * time.Second}
}

func testInvitationAdministrationPaginationIsStable(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture, class, inviter, issuedAt := invitationAdministrationFixture(t, ctx, ss, "pagination")
	created := make(map[model.InvitationID]struct{}, 3)
	for index := 0; index < 3; index++ {
		issue := studentClassInvitationIssueFixture(t, ss, inviter, class, fixture.period, issuedAt)
		issue.Invitation.TargetEmail = "pagination-" + model.NewID() + "@example.edu"
		require.NoError(t, issue.Invitation.Validate())
		invitation, err := ss.Invitation().IssueStudentClass(ctx, issue)
		require.NoError(t, err)
		created[invitation.ID] = struct{}{}
	}

	visibility := store.InvitationVisibilityScope{ClassIDs: []model.ClassID{class.ID}}
	first, err := ss.Invitation().List(ctx, store.InvitationListOptions{Visibility: visibility, Limit: 2})
	require.NoError(t, err)
	require.Len(t, first.Items, 2)
	require.True(t, first.More)
	boundary := first.Items[len(first.Items)-1].Invitation
	second, err := ss.Invitation().List(ctx, store.InvitationListOptions{Visibility: visibility, Limit: 2,
		BeforeCreatedAt: boundary.CreatedAt, BeforeID: boundary.ID})
	require.NoError(t, err)
	require.Len(t, second.Items, 1)
	require.False(t, second.More)

	seen := make(map[model.InvitationID]struct{}, 3)
	for _, page := range []*store.InvitationPage{first, second} {
		for _, record := range page.Items {
			_, duplicate := seen[record.Invitation.ID]
			require.False(t, duplicate, "Invitation %s repeated across pages", record.Invitation.ID)
			seen[record.Invitation.ID] = struct{}{}
		}
	}
	require.Len(t, seen, len(created), "paginated Invitations")
	for id := range created {
		_, ok := seen[id]
		require.True(t, ok, "Invitation %s skipped by pagination", id)
	}
}

func testInvitationAdministrationRevokeAndReplaceAreRevisionFenced(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture, class, inviter, issuedAt := invitationAdministrationFixture(t, ctx, ss, "revoke-replace-race")
	issue := studentClassInvitationIssueFixture(t, ss, inviter, class, fixture.period, issuedAt)
	issue.Invitation.TargetEmail = "revoke-replace-race-" + model.NewID() + "@example.edu"
	require.NoError(t, issue.Invitation.Validate())
	current, err := ss.Invitation().IssueStudentClass(ctx, issue)
	require.NoError(t, err)

	changed, err := model.NewStudentClassInvitation(model.StudentClassInvitationInput{ID: model.NewInvitationID(),
		TargetEmail: "revoke-replace-winner-" + model.NewID() + "@example.edu", ClassID: class.ID,
		AcademicPeriodID: fixture.period.ID, IntendedStartsAt: fixture.period.StartsAt,
		IntendedEndsAt: model.OptionalTimeFrom(fixture.period.EndsAt), InviterUserID: inviter.ID,
		ScopeType: model.RoleScopeClass, ScopeID: class.ID.String(),
		ClaimHash: model.HashInvitationClaim(model.NewCredentialToken()), IssuedAt: model.NowUTC()})
	require.NoError(t, err)
	replacementOccurrence, replacementDelivery, replacementJob := invitationLifecycleMailFixture(t, changed.ID, inviter.ID,
		model.MailTemplateAccessStudentClassInvitation, model.JobTypeMailDeliverCredential, model.NowUTC(), changed.ExpiresAt)
	supersedeAudit := saveInvitationLifecycleAuditAttempt(t, ctx, ss, inviter.ID, class.ID.String(), "invitation.supersede")
	replacementAudit := saveInvitationLifecycleAuditAttempt(t, ctx, ss, inviter.ID, class.ID.String(), "invitation.replacement_issue")
	replacement := &store.InvitationReplacement{CurrentID: current.ID, ExpectedRevision: current.Revision,
		Replacement: changed, Lifetime: model.InvitationLifetime, Occurrence: replacementOccurrence,
		Delivery: replacementDelivery, DeliveryJob: replacementJob, ActorUserID: inviter.ID,
		CurrentAuditEventID: supersedeAudit.ID, ReplacementAuditEventID: replacementAudit.ID,
		AuditAt: model.NowUTC()}

	revocationOccurrence, revocationDelivery, revocationJob := invitationLifecycleMailFixture(t, current.ID, inviter.ID,
		model.MailTemplateAccessInvitationRevoked, model.JobTypeMailDeliver, model.NowUTC(), model.NowUTC().Add(24*time.Hour))
	revocationAudit := saveInvitationLifecycleAuditAttempt(t, ctx, ss, inviter.ID, class.ID.String(), "invitation.revoke")
	revocation := &store.InvitationRevocation{ID: current.ID, ExpectedRevision: current.Revision, ActorUserID: inviter.ID,
		RevocationNotice: &store.PreparedMail{Occurrence: revocationOccurrence, Delivery: revocationDelivery, Job: revocationJob},
		AuditEventID:     revocationAudit.ID, AuditAt: model.NowUTC()}

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

	require.NotEqual(t, replaceErr == nil, revokeErr == nil, "concurrent Revoke()/Replace() results=%#v/%#v errors=%v/%v", revoked, replaced, revokeErr, replaceErr)
	currentAfter, err := ss.Invitation().Get(ctx, current.ID)
	require.NoError(t, err)
	if revokeErr == nil {
		require.True(t, store.IsConflict(replaceErr), "winning Revoke() = %#v current=%#v Replace error", revoked, currentAfter)
		require.Equal(t, model.InvitationRevoked, currentAfter.State, "winning Revoke() = %#v current=%#v Replace error", revoked, currentAfter)
		require.Equal(t, current.Revision+1, currentAfter.Revision, "winning Revoke() = %#v current=%#v Replace error", revoked, currentAfter)
		_, err = ss.Invitation().Get(ctx, changed.ID)
		require.True(t, store.IsNotFound(err), "losing replacement Invitation")
		_, err = ss.Mail().GetDelivery(ctx, replacementDelivery.ID)
		require.True(t, store.IsNotFound(err), "losing replacement delivery")
	} else {
		require.True(t, store.IsConflict(revokeErr), "winning Replace() = %#v current=%#v Revoke error", replaced, currentAfter)
		require.Equal(t, model.InvitationSuperseded, currentAfter.State, "winning Replace() = %#v current=%#v Revoke error", replaced, currentAfter)
		require.Equal(t, current.Revision+1, currentAfter.Revision, "winning Replace() = %#v current=%#v Revoke error", replaced, currentAfter)
		require.NotNil(t, replaced, "winning Replace() = %#v current=%#v Revoke error", replaced, currentAfter)
		require.Equal(t, changed.ID, replaced.Invitation.ID, "winning Replace() = %#v current=%#v Revoke error", replaced, currentAfter)
		_, err = ss.Mail().GetDelivery(ctx, revocationDelivery.ID)
		require.True(t, store.IsNotFound(err), "losing revocation delivery")
	}
	obsolete, err := ss.Mail().GetDelivery(ctx, issue.Delivery.ID)
	require.NoError(t, err)
	require.Equal(t, model.MailDeliverySuppressed, obsolete.State)
	require.Empty(t, obsolete.EncryptedPayload)
}

func testInvitationAdministrationResendIsRevisionFenced(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture, class, inviter, issuedAt := invitationAdministrationFixture(t, ctx, ss, "resend-race")
	issue := studentClassInvitationIssueFixture(t, ss, inviter, class, fixture.period, issuedAt)
	issue.Invitation.TargetEmail = "resend-race-" + model.NewID() + "@example.edu"
	require.NoError(t, issue.Invitation.Validate())
	invitation, err := ss.Invitation().IssueStudentClass(ctx, issue)
	require.NoError(t, err)

	inputs := make([]*store.InvitationResend, 2)
	for index := range inputs {
		occurrence, delivery, job := invitationLifecycleMailFixture(t, invitation.ID, inviter.ID,
			model.MailTemplateAccessStudentClassInvitation, model.JobTypeMailDeliverCredential, model.NowUTC(), invitation.ExpiresAt)
		audit := saveInvitationLifecycleAuditAttempt(t, ctx, ss, inviter.ID, class.ID.String(), "invitation.resend")
		inputs[index] = &store.InvitationResend{ID: invitation.ID, ExpectedRevision: invitation.Revision,
			ClaimHash: model.HashInvitationClaim(model.NewCredentialToken()), Occurrence: occurrence, Delivery: delivery,
			DeliveryJob: job, ActorUserID: inviter.ID, AuditEventID: audit.ID, AuditAt: model.NowUTC()}
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
		require.True(t, store.IsConflict(resendErr), "concurrent Resend() error[%d]", index)
	}
	require.GreaterOrEqual(t, winner, 0, "concurrent Resend() results=%#v errors=%v", results, errors)
	require.Error(t, errors[1-winner], "concurrent Resend() results=%#v errors=%v", results, errors)
	require.NotNil(t, results[winner], "concurrent Resend() results=%#v errors=%v", results, errors)
	require.Equal(t, invitation.Revision+1, results[winner].Invitation.Revision, "concurrent Resend() results=%#v errors=%v", results, errors)
	_, err = ss.Invitation().GetByClaimHash(ctx, inputs[winner].ClaimHash)
	require.NoError(t, err)
	_, err = ss.Invitation().GetByClaimHash(ctx, inputs[1-winner].ClaimHash)
	require.True(t, store.IsNotFound(err), "losing claim lookup")
	_, err = ss.Mail().GetDelivery(ctx, inputs[1-winner].Delivery.ID)
	require.True(t, store.IsNotFound(err), "losing resend delivery")
}

func testInvitationAdministrationLifecycle(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture, class, inviter, issuedAt := invitationAdministrationFixture(t, ctx, ss, "lifecycle")
	issue := studentClassInvitationIssueFixture(t, ss, inviter, class, fixture.period, issuedAt)
	issue.Invitation.TargetEmail = "lifecycle-" + model.NewID() + "@example.edu"
	require.NoError(t, issue.Invitation.Validate())
	invitation, err := ss.Invitation().IssueStudentClass(ctx, issue)
	require.NoError(t, err)

	visibility := store.InvitationVisibilityScope{ClassIDs: []model.ClassID{class.ID}}
	page, err := ss.Invitation().List(ctx, store.InvitationListOptions{Visibility: visibility,
		Purpose: model.InvitationPurposeStudentClass, State: model.InvitationPending,
		TargetEmail: invitation.TargetEmail, TargetID: class.ID.String(), Limit: 1})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Equal(t, invitation.ID, page.Items[0].Invitation.ID)
	require.NotNil(t, page.Items[0].Delivery)
	require.Equal(t, model.MailDeliveryQueued, page.Items[0].Delivery.State)
	require.NotEmpty(t, page.Items[0].Delivery.MaskedRecipient)
	rootPage, err := ss.Invitation().List(ctx, store.InvitationListOptions{Visibility: store.InvitationVisibilityScope{
		AcademicUnitRootIDs: []model.AcademicUnitID{fixture.programme.AcademicUnitID}}, TargetEmail: invitation.TargetEmail, Limit: 10})
	require.NoError(t, err)
	require.Len(t, rootPage.Items, 1)
	require.Equal(t, invitation.ID, rootPage.Items[0].Invitation.ID)
	_, err = ss.Invitation().GetForAdministration(ctx, invitation.ID,
		store.InvitationVisibilityScope{ClassIDs: []model.ClassID{model.NewClassID()}})
	require.True(t, store.IsNotFound(err), "out-of-scope Invitation detail error")

	oldHash := invitation.ClaimHash
	newHash := model.HashInvitationClaim(model.NewCredentialToken())
	occurrence, delivery, job := invitationLifecycleMailFixture(t, invitation.ID, inviter.ID,
		model.MailTemplateAccessStudentClassInvitation, model.JobTypeMailDeliverCredential, model.NowUTC(), invitation.ExpiresAt)
	audit := saveInvitationLifecycleAuditAttempt(t, ctx, ss, inviter.ID, class.ID.String(), "invitation.resend")
	resent, err := ss.Invitation().Resend(ctx, &store.InvitationResend{ID: invitation.ID, ExpectedRevision: invitation.Revision,
		ClaimHash: newHash, Occurrence: occurrence, Delivery: delivery, DeliveryJob: job, ActorUserID: inviter.ID,
		AuditEventID: audit.ID, AuditAt: model.NowUTC()})
	require.NoError(t, err)
	require.Equal(t, newHash, resent.Invitation.ClaimHash)
	require.Equal(t, invitation.Revision+1, resent.Invitation.Revision)
	require.Equal(t, invitation.ExpiresAt, resent.Invitation.ExpiresAt)
	require.NotNil(t, resent.Delivery)
	require.Equal(t, model.MailDeliveryQueued, resent.Delivery.State)
	_, err = ss.Invitation().GetByClaimHash(ctx, oldHash)
	require.True(t, store.IsNotFound(err), "old Invitation claim error")
	obsolete, err := ss.Mail().GetDelivery(ctx, issue.Delivery.ID)
	require.NoError(t, err)
	require.Equal(t, model.MailDeliverySuppressed, obsolete.State)
	require.Empty(t, obsolete.EncryptedPayload)
	require.Equal(t, model.MailDeliveryObsoleteCode, obsolete.PublicFailureCode)

	sending, err := ss.Mail().StartDelivery(ctx, &store.MailDeliveryStart{ID: delivery.ID, ExpectedRevision: 1, At: model.NowUTC()})
	require.NoError(t, err)
	accepted, err := ss.Mail().CompleteDelivery(ctx, &store.MailDeliveryCompletion{DeliveryID: sending.ID,
		ExpectedRevision: sending.Revision, Kind: store.MailDeliveryCompletionAccepted, At: model.NowUTC()})
	require.NoError(t, err)
	require.Equal(t, model.MailDeliveryAccepted, accepted.State)
	require.Empty(t, accepted.EncryptedPayload)
	revocationOccurrence, revocationDelivery, revocationJob := invitationLifecycleMailFixture(t, invitation.ID, inviter.ID,
		model.MailTemplateAccessInvitationRevoked, model.JobTypeMailDeliver, model.NowUTC(), model.NowUTC().Add(24*time.Hour))
	revocationAudit := saveInvitationLifecycleAuditAttempt(t, ctx, ss, inviter.ID, class.ID.String(), "invitation.revoke")
	revoked, err := ss.Invitation().Revoke(ctx, &store.InvitationRevocation{ID: invitation.ID,
		ExpectedRevision: resent.Invitation.Revision, ActorUserID: inviter.ID,
		RevocationNotice: &store.PreparedMail{Occurrence: revocationOccurrence, Delivery: revocationDelivery, Job: revocationJob},
		AuditEventID:     revocationAudit.ID, AuditAt: model.NowUTC()})
	require.NoError(t, err)
	require.Equal(t, model.InvitationRevoked, revoked.Invitation.State)
	require.NotNil(t, revoked.Delivery)
	require.Equal(t, model.MailTemplateAccessInvitationRevoked, revoked.Delivery.TemplateKey)
	require.Equal(t, model.MailDeliveryQueued, revoked.Delivery.State)
	_, err = ss.Invitation().GetByClaimHash(ctx, newHash)
	require.NoError(t, err)

	unsentFixture, unsentClass, unsentInviter, unsentAt := invitationAdministrationFixture(t, ctx, ss, "unsent-revoke")
	unsentIssue := studentClassInvitationIssueFixture(t, ss, unsentInviter, unsentClass, unsentFixture.period, unsentAt)
	unsentIssue.Invitation.TargetEmail = "unsent-revoke-" + model.NewID() + "@example.edu"
	require.NoError(t, unsentIssue.Invitation.Validate())
	unsent, err := ss.Invitation().IssueStudentClass(ctx, unsentIssue)
	require.NoError(t, err)
	unusedOccurrence, unusedDelivery, unusedJob := invitationLifecycleMailFixture(t, unsent.ID, unsentInviter.ID,
		model.MailTemplateAccessInvitationRevoked, model.JobTypeMailDeliver, model.NowUTC(), model.NowUTC().Add(24*time.Hour))
	unsentAudit := saveInvitationLifecycleAuditAttempt(t, ctx, ss, unsentInviter.ID, unsentClass.ID.String(), "invitation.revoke")
	unsentRevoked, err := ss.Invitation().Revoke(ctx, &store.InvitationRevocation{ID: unsent.ID,
		ExpectedRevision: unsent.Revision, ActorUserID: unsentInviter.ID,
		RevocationNotice: &store.PreparedMail{Occurrence: unusedOccurrence, Delivery: unusedDelivery, Job: unusedJob},
		AuditEventID:     unsentAudit.ID, AuditAt: model.NowUTC()})
	require.NoError(t, err)
	require.Equal(t, model.InvitationRevoked, unsentRevoked.Invitation.State)
	require.Nil(t, unsentRevoked.Delivery)
	_, err = ss.Mail().GetDelivery(ctx, unusedDelivery.ID)
	require.True(t, store.IsNotFound(err), "unneeded revocation delivery")
	unsentObsolete, err := ss.Mail().GetDelivery(ctx, unsentIssue.Delivery.ID)
	require.NoError(t, err)
	require.Equal(t, model.MailDeliverySuppressed, unsentObsolete.State)
	require.Empty(t, unsentObsolete.EncryptedPayload)

	replacementFixture, replacementClass, replacementInviter, replacementAt := invitationAdministrationFixture(t, ctx, ss, "replacement")
	replacementIssue := studentClassInvitationIssueFixture(t, ss, replacementInviter, replacementClass, replacementFixture.period, replacementAt)
	replacementIssue.Invitation.TargetEmail = "replace-current-" + model.NewID() + "@example.edu"
	require.NoError(t, replacementIssue.Invitation.Validate())
	current, err := ss.Invitation().IssueStudentClass(ctx, replacementIssue)
	require.NoError(t, err)
	samePackage := *current
	samePackage.ID, samePackage.ClaimHash = model.NewInvitationID(), model.HashInvitationClaim(model.NewCredentialToken())
	samePackage.CreatedAt, samePackage.UpdatedAt = model.NowUTC(), model.NowUTC()
	samePackage.ExpiresAt, samePackage.Revision = samePackage.CreatedAt.Add(model.InvitationLifetime), 1
	require.NoError(t, samePackage.Validate())
	sameOccurrence, sameDelivery, sameJob := invitationLifecycleMailFixture(t, samePackage.ID, replacementInviter.ID,
		model.MailTemplateAccessStudentClassInvitation, model.JobTypeMailDeliverCredential, model.NowUTC(), samePackage.ExpiresAt)
	sameCurrentAudit := saveInvitationLifecycleAuditAttempt(t, ctx, ss, replacementInviter.ID, replacementClass.ID.String(), "invitation.supersede")
	sameReplacementAudit := saveInvitationLifecycleAuditAttempt(t, ctx, ss, replacementInviter.ID, replacementClass.ID.String(), "invitation.replacement_issue")
	_, err = ss.Invitation().Replace(ctx, &store.InvitationReplacement{CurrentID: current.ID,
		ExpectedRevision: current.Revision, Replacement: &samePackage, Lifetime: model.InvitationLifetime,
		Occurrence: sameOccurrence, Delivery: sameDelivery, DeliveryJob: sameJob, ActorUserID: replacementInviter.ID,
		CurrentAuditEventID: sameCurrentAudit.ID, ReplacementAuditEventID: sameReplacementAudit.ID,
		AuditAt: model.NowUTC()})
	require.True(t, store.IsConflict(err), "unchanged Replace() error")
	_, err = ss.Mail().GetDelivery(ctx, sameDelivery.ID)
	require.True(t, store.IsNotFound(err), "unchanged replacement delivery")
	replacementTargetClass := saveClass(t, ctx, ss, replacementFixture.level.ID.String(), replacementFixture.period.ID.String(), "invitation-admin-replacement-target")
	targetRole, err := SaveRole(ctx, ss, &model.Role{Name: "invitation-replacement-target-" + model.NewID(), DisplayName: "Invitation replacement target",
		Permissions: []string{string(model.ActionInvitationCreate), string(model.ActionClassMembersManage)}})
	require.NoError(t, err)
	_, err = SaveRoleBinding(ctx, ss, &model.RoleBinding{UserID: replacementInviter.ID, RoleID: targetRole.ID,
		ScopeType: model.RoleScopeClass, ScopeID: replacementTargetClass.ID.String(), StartsAt: replacementAt.Add(-time.Second)})
	require.NoError(t, err)
	candidate, err := model.NewStudentClassInvitation(model.StudentClassInvitationInput{ID: model.NewInvitationID(),
		TargetEmail: "replacement-" + model.NewID() + "@example.edu", ClassID: replacementTargetClass.ID,
		AcademicPeriodID: replacementFixture.period.ID, IntendedStartsAt: replacementFixture.period.StartsAt,
		IntendedEndsAt: model.OptionalTimeFrom(replacementFixture.period.EndsAt), InviterUserID: replacementInviter.ID,
		ScopeType: model.RoleScopeClass, ScopeID: replacementTargetClass.ID.String(),
		ClaimHash: model.HashInvitationClaim(model.NewCredentialToken()), IssuedAt: model.NowUTC()})
	require.NoError(t, err)
	replacementOccurrence, replacementDelivery, replacementJob := invitationLifecycleMailFixture(t, candidate.ID, replacementInviter.ID,
		model.MailTemplateAccessStudentClassInvitation, model.JobTypeMailDeliverCredential, model.NowUTC(), candidate.ExpiresAt)
	supersedeAudit := saveInvitationLifecycleAuditAttempt(t, ctx, ss, replacementInviter.ID, replacementClass.ID.String(), "invitation.supersede")
	replacementAudit := saveInvitationLifecycleAuditAttempt(t, ctx, ss, replacementInviter.ID, replacementTargetClass.ID.String(), "invitation.replacement_issue")
	replaced, err := ss.Invitation().Replace(ctx, &store.InvitationReplacement{CurrentID: current.ID,
		ExpectedRevision: current.Revision, Replacement: candidate, Lifetime: model.InvitationLifetime,
		Occurrence: replacementOccurrence, Delivery: replacementDelivery, DeliveryJob: replacementJob, ActorUserID: replacementInviter.ID,
		CurrentAuditEventID: supersedeAudit.ID, ReplacementAuditEventID: replacementAudit.ID,
		AuditAt: model.NowUTC()})
	require.NoError(t, err)
	require.Equal(t, candidate.ID, replaced.Invitation.ID)
	require.Equal(t, model.InvitationPending, replaced.Invitation.State)
	require.NotNil(t, replaced.Delivery)
	superseded, err := ss.Invitation().Get(ctx, current.ID)
	require.NoError(t, err)
	require.Equal(t, model.InvitationSuperseded, superseded.State)
	require.Equal(t, current.Revision+1, superseded.Revision)
	supersedeEvent, err := ss.Audit().Get(ctx, supersedeAudit.ID)
	require.NoError(t, err)
	replacementEvent, err := ss.Audit().Get(ctx, replacementAudit.ID)
	require.NoError(t, err)
	require.Equal(t, replacementClass.ID.String(), supersedeEvent.ScopeID, "cross-scope replacement audits = %#v / %#v", supersedeEvent, replacementEvent)
	require.NotContains(t, string(supersedeEvent.Result), replacementTargetClass.ID.String(), "cross-scope replacement audits = %#v / %#v", supersedeEvent, replacementEvent)
	require.Equal(t, replacementTargetClass.ID.String(), replacementEvent.ScopeID, "cross-scope replacement audits = %#v / %#v", supersedeEvent, replacementEvent)
	require.NotContains(t, string(replacementEvent.Result), replacementClass.ID.String(), "cross-scope replacement audits = %#v / %#v", supersedeEvent, replacementEvent)
	oldReplacementDelivery, err := ss.Mail().GetDelivery(ctx, replacementIssue.Delivery.ID)
	require.NoError(t, err)
	require.Equal(t, model.MailDeliverySuppressed, oldReplacementDelivery.State)
	require.Empty(t, oldReplacementDelivery.EncryptedPayload)
}

func invitationAdministrationFixture(t *testing.T, ctx context.Context, ss store.Store, suffix string) (classFixture, *model.Class, *model.User, time.Time) {
	t.Helper()
	fixture := saveClassFixture(t, ctx, ss)
	class := saveClass(t, ctx, ss, fixture.level.ID.String(), fixture.period.ID.String(), "invitation-admin-"+suffix)
	inviter := saveUser(t, ctx, ss)
	role, err := SaveRole(ctx, ss, &model.Role{Name: "invitation-admin-" + suffix + "-" + model.NewID(), DisplayName: "Invitation administrator",
		Permissions: []string{string(model.ActionInvitationCreate), string(model.ActionInvitationManage), string(model.ActionClassMembersManage), string(model.ActionOnboardingBatchManage)}})
	require.NoError(t, err)
	at := model.NowUTC().Add(-time.Minute)
	_, err = SaveRoleBinding(ctx, ss, &model.RoleBinding{UserID: inviter.ID, RoleID: role.ID,
		ScopeType: model.RoleScopeClass, ScopeID: class.ID.String(), StartsAt: at.Add(-time.Second)})
	require.NoError(t, err)
	return fixture, class, inviter, at
}

func invitationLifecycleMailFixture(t *testing.T, invitationID model.InvitationID, actor model.UserID,
	key model.MailTemplateKey, jobType model.JobType, at, deadline time.Time,
) (*model.MailOccurrence, *model.MailDelivery, *model.Job) {
	t.Helper()
	occurrenceID, deliveryID := model.NewMailOccurrenceID(), model.NewMailDeliveryID()
	command, err := model.EncodeMailDeliveryCommand(model.MailDeliveryCommand{DeliveryID: deliveryID})
	require.NoError(t, err)
	job, err := model.NewJob(model.NewJobID(), jobType, command, deliveryID.String(), at, at, model.MailMaximumAttempts)
	require.NoError(t, err)
	occurrence := &model.MailOccurrence{ID: occurrenceID, Kind: model.MailOccurrenceInvitation, TemplateKey: key, ActorUserID: actor, CreatedAt: at}
	delivery := &model.MailDelivery{ID: deliveryID, OccurrenceID: occurrenceID, JobID: job.ID, TargetInvitationID: invitationID,
		TemplateKey: key, TemplateDigest: strings.Repeat("d", 64), MaskedRecipient: "i***@example.edu",
		State: model.MailDeliveryQueued, CreatedAt: at, UpdatedAt: at, MessageDate: at, Deadline: deadline,
		MessageID:        "<invitation-lifecycle." + deliveryID.String() + "@example.test>",
		EncryptedPayload: json.RawMessage(`{"version":1,"key_id":"11111111111111111111111111111111","ciphertext":"lifecycle"}`), Revision: 1}
	require.NoError(t, occurrence.Validate())
	require.NoError(t, delivery.Validate())
	return occurrence, delivery, job
}

func saveInvitationLifecycleAuditAttempt(t *testing.T, ctx context.Context, ss store.Store, actor model.UserID, classID, action string) *model.AuditEvent {
	t.Helper()
	audit, err := ss.Audit().Save(ctx, &model.AuditEvent{ActorID: actor, Action: action,
		Resource: model.Resource{Type: model.ResourceClass, ID: classID}, ScopeType: model.RoleScopeClass, ScopeID: classID,
		Status: model.AuditStatusAttempt, NodeID: "invitation-lifecycle-store-test"})
	require.NoError(t, err)
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
	require.True(t, store.IsConflict(err), "AcceptScopedRole() concurrent Role archive error")
	current, getErr := ss.Invitation().Get(ctx, invitation.ID)
	require.NoError(t, getErr)
	require.Equal(t, model.InvitationPending, current.State)
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
	require.True(t, store.IsConflict(err), "AcceptScopedRole() concurrent inviter binding end error")
	current, getErr := ss.Invitation().Get(ctx, invitation.ID)
	require.NoError(t, getErr)
	require.Equal(t, model.InvitationPending, current.State)
}

func scopedRoleInvitationRaceFixture(t *testing.T, ctx context.Context, ss store.Store, suffix string) (*model.Invitation, *model.RoleBinding, *model.Role, *store.ScopedRoleInvitationAcceptance) {
	t.Helper()
	unit, _ := saveProgrammeParents(t, ctx, ss, "scoped-role-race-"+suffix+model.NewID())
	inviter, existing := saveUser(t, ctx, ss), saveUser(t, ctx, ss)
	at := model.NowUTC().Add(-time.Minute)
	issuerRole, err := SaveRole(ctx, ss, &model.Role{Name: "scoped-role-race-issuer-" + model.NewID(), DisplayName: "Scoped Role Race Issuer",
		Permissions: []string{string(model.ActionInvitationCreate), string(model.ActionRoleBindingManage), string(model.ActionAcademicAuditView)}})
	require.NoError(t, err)
	issuerBinding, err := SaveRoleBinding(ctx, ss, &model.RoleBinding{UserID: inviter.ID, RoleID: issuerRole.ID,
		ScopeType: model.RoleScopeAcademicUnit, ScopeID: unit.ID.String(), StartsAt: at.Add(-time.Second)})
	require.NoError(t, err)
	targetRole, err := SaveRole(ctx, ss, &model.Role{Name: "scoped-role-race-target-" + model.NewID(), DisplayName: "Scoped Role Race Target",
		Permissions: []string{string(model.ActionAcademicAuditView)}})
	require.NoError(t, err)
	_, err = SaveRoleBinding(ctx, ss, &model.RoleBinding{UserID: inviter.ID, RoleID: targetRole.ID,
		ScopeType: model.RoleScopeInstitution, ScopeID: unit.InstitutionID.String(), StartsAt: at.Add(-time.Second)})
	require.NoError(t, err)
	candidate, err := model.NewScopedRoleInvitation(model.ScopedRoleInvitationInput{ID: model.NewInvitationID(),
		Purpose: model.InvitationPurposeAcademicUnitRole, TargetEmail: "scoped-role-race-" + model.NewID() + "@example.edu",
		AcademicUnitID: unit.ID, RoleID: targetRole.ID, RoleActions: targetRole.Permissions, IntendedStartsAt: at,
		InviterUserID: inviter.ID, ScopeType: model.RoleScopeAcademicUnit, ScopeID: unit.ID.String(),
		ClaimHash: model.HashInvitationClaim(model.NewCredentialToken()), IssuedAt: at})
	require.NoError(t, err)
	invitation, err := ss.Invitation().IssueScopedRole(ctx, scopedRoleInvitationIssueFixture(t, ss, candidate))
	require.NoError(t, err)
	session, _, _ := saveSession(t, ctx, ss, existing.ID.String(), 10)
	acceptance := scopedRoleInvitationAcceptanceFixture(t, ctx, ss, invitation, existing.ID, session.ID, model.NowUTC())
	return invitation, issuerBinding, targetRole, acceptance
}

func testInstitutionRoleInvitationExistingUserAtomicAndReplaySafe(t *testing.T, ss store.Store) {
	ctx := context.Background()
	unit, _ := saveProgrammeParents(t, ctx, ss, "institution-role-"+model.NewID())
	inviter, existing := saveUser(t, ctx, ss), saveUser(t, ctx, ss)
	issuedAt := model.NowUTC().Add(-time.Minute)
	issuerRole, err := SaveRole(ctx, ss, &model.Role{Name: "institution-role-issuer-" + model.NewID(), DisplayName: "Institution Role Issuer",
		Permissions: []string{string(model.ActionInvitationCreate), string(model.ActionRoleBindingManage), string(model.ActionAuditView)}})
	require.NoError(t, err)
	_, err = SaveRoleBinding(ctx, ss, &model.RoleBinding{UserID: inviter.ID, RoleID: issuerRole.ID,
		ScopeType: model.RoleScopeInstitution, ScopeID: unit.InstitutionID.String(), StartsAt: issuedAt.Add(-time.Second)})
	require.NoError(t, err)
	targetRole, err := SaveRole(ctx, ss, &model.Role{Name: "institution-role-target-" + model.NewID(), DisplayName: "Institution Role Target",
		Permissions: []string{string(model.ActionAuditView)}})
	require.NoError(t, err)
	invitation, err := model.NewScopedRoleInvitation(model.ScopedRoleInvitationInput{ID: model.NewInvitationID(),
		Purpose: model.InvitationPurposeInstitutionRole, TargetEmail: "institution-invited-" + model.NewID() + "@example.edu",
		RoleID: targetRole.ID, RoleActions: targetRole.Permissions, IntendedStartsAt: issuedAt,
		InviterUserID: inviter.ID, ScopeType: model.RoleScopeInstitution, ScopeID: unit.InstitutionID.String(),
		ClaimHash: model.HashInvitationClaim(model.NewCredentialToken()), IssuedAt: issuedAt})
	require.NoError(t, err)
	issue := scopedRoleInvitationIssueFixture(t, ss, invitation)
	created, err := ss.Invitation().IssueScopedRole(ctx, issue)
	require.NoError(t, err)
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
		require.NoError(t, acceptErr)
	}
	for _, acceptance := range acceptances {
		requireScopedRoleAcceptanceAuditSuccess(t, ctx, ss, acceptance)
	}
	accepted := results[0]
	if accepted.Replayed {
		accepted = results[1]
	}
	require.NotEqual(t, results[1].Replayed, results[0].Replayed, "concurrent scoped Role replay flags")
	require.False(t, accepted.Replayed)
	require.Equal(t, existing.ID, accepted.User.ID)
	require.Equal(t, targetRole.ID, accepted.RoleBinding.RoleID)
	require.Equal(t, model.RoleScopeInstitution, accepted.RoleBinding.ScopeType)
	require.Equal(t, unit.InstitutionID.String(), accepted.RoleBinding.ScopeID)
	replayAcceptance := scopedRoleInvitationAcceptanceFixture(t, ctx, ss, created, existing.ID, session.ID, model.NowUTC())
	replayed, err := ss.Invitation().AcceptScopedRole(ctx, replayAcceptance)
	require.NoError(t, err)
	requireScopedRoleAcceptanceAuditSuccess(t, ctx, ss, replayAcceptance)
	require.True(t, replayed.Replayed)
	require.Equal(t, accepted.RoleBinding.ID, replayed.RoleBinding.ID)
}

func testScopedRoleInvitationExistingUserAtomicAndReplaySafe(t *testing.T, ss store.Store) {
	ctx := context.Background()
	unit, _ := saveProgrammeParents(t, ctx, ss, "scoped-role-"+model.NewID())
	inviter, existing, other := saveUser(t, ctx, ss), saveUser(t, ctx, ss), saveUser(t, ctx, ss)
	issuedAt := model.NowUTC().Add(-time.Minute)
	issuerRole, err := SaveRole(ctx, ss, &model.Role{Name: "scoped-role-issuer-" + model.NewID(), DisplayName: "Scoped Role Issuer",
		Permissions: []string{string(model.ActionInvitationCreate), string(model.ActionRoleBindingManage), string(model.ActionAcademicAuditView)}})
	require.NoError(t, err)
	_, err = SaveRoleBinding(ctx, ss, &model.RoleBinding{UserID: inviter.ID, RoleID: issuerRole.ID,
		ScopeType: model.RoleScopeAcademicUnit, ScopeID: unit.ID.String(), StartsAt: issuedAt.Add(-time.Second)})
	require.NoError(t, err)
	targetRole, err := SaveRole(ctx, ss, &model.Role{Name: "scoped-role-target-" + model.NewID(), DisplayName: "Scoped Role Target",
		Permissions: []string{string(model.ActionAcademicAuditView)}})
	require.NoError(t, err)
	invitation, err := model.NewScopedRoleInvitation(model.ScopedRoleInvitationInput{ID: model.NewInvitationID(),
		Purpose: model.InvitationPurposeAcademicUnitRole, TargetEmail: "invited-" + model.NewID() + "@example.edu",
		AcademicUnitID: unit.ID, RoleID: targetRole.ID, RoleActions: targetRole.Permissions, IntendedStartsAt: issuedAt,
		InviterUserID: inviter.ID, ScopeType: model.RoleScopeAcademicUnit, ScopeID: unit.ID.String(),
		ClaimHash: model.HashInvitationClaim(model.NewCredentialToken()), IssuedAt: issuedAt})
	require.NoError(t, err)
	issue := scopedRoleInvitationIssueFixture(t, ss, invitation)
	created, err := ss.Invitation().IssueScopedRole(ctx, issue)
	require.NoError(t, err)
	require.Equal(t, model.InvitationPurposeAcademicUnitRole, created.Purpose)
	require.Equal(t, targetRole.ID, created.RoleID)
	existingBinding, err := SaveRoleBinding(ctx, ss, &model.RoleBinding{UserID: existing.ID, RoleID: targetRole.ID,
		ScopeType: model.RoleScopeAcademicUnit, ScopeID: unit.ID.String(), StartsAt: issuedAt.Add(-time.Second)})
	require.NoError(t, err)
	session, _, _ := saveSession(t, ctx, ss, existing.ID.String(), 10)
	acceptance := scopedRoleInvitationAcceptanceFixture(t, ctx, ss, created, existing.ID, session.ID, model.NowUTC())
	missingAttempt := *acceptance
	missingAttempt.AuditEventID = model.NewAuditEventID()
	_, err = ss.Invitation().AcceptScopedRole(ctx, &missingAttempt)
	require.Error(t, err, "AcceptScopedRole() without persisted attempt error = nil")
	stillPending, err := ss.Invitation().Get(ctx, created.ID)
	require.NoError(t, err)
	require.Equal(t, model.InvitationPending, stillPending.State)
	accepted, err := ss.Invitation().AcceptScopedRole(ctx, acceptance)
	require.NoError(t, err)
	requireScopedRoleAcceptanceAuditSuccess(t, ctx, ss, acceptance)
	require.False(t, accepted.Replayed)
	require.Equal(t, existing.ID, accepted.User.ID)
	require.Equal(t, existing.Email, accepted.User.Email)
	require.Equal(t, existingBinding.ID, accepted.RoleBinding.ID)
	require.Equal(t, existing.ID, accepted.RoleBinding.UserID)
	require.Equal(t, targetRole.ID, accepted.RoleBinding.RoleID)
	require.Equal(t, model.RoleScopeAcademicUnit, accepted.RoleBinding.ScopeType)
	require.Equal(t, unit.ID.String(), accepted.RoleBinding.ScopeID)
	require.False(t, accepted.Invitation.AcceptedAffiliationID.IsValid())
	require.False(t, accepted.Invitation.AcceptedAcademicUnitMemberID.IsValid())
	obsolete, err := ss.Mail().GetDelivery(ctx, issue.Delivery.ID)
	require.NoError(t, err)
	require.Equal(t, model.MailDeliverySuppressed, obsolete.State)
	require.Equal(t, model.MailDeliveryObsoleteCode, obsolete.PublicFailureCode)
	require.Empty(t, obsolete.EncryptedPayload)
	replayAcceptance := scopedRoleInvitationAcceptanceFixture(t, ctx, ss, created, existing.ID, session.ID, model.NowUTC())
	replayed, err := ss.Invitation().AcceptScopedRole(ctx, replayAcceptance)
	require.NoError(t, err)
	requireScopedRoleAcceptanceAuditSuccess(t, ctx, ss, replayAcceptance)
	require.True(t, replayed.Replayed)
	require.Equal(t, existing.ID, replayed.User.ID)
	require.Equal(t, accepted.RoleBinding.ID, replayed.RoleBinding.ID)
	otherSession, _, _ := saveSession(t, ctx, ss, other.ID.String(), 10)
	differentUser := scopedRoleInvitationAcceptanceFixture(t, ctx, ss, created, other.ID, otherSession.ID, model.NowUTC())
	_, err = ss.Invitation().AcceptScopedRole(ctx, differentUser)
	require.True(t, store.IsConflict(err), "different User replay error")
}

func scopedRoleInvitationIssueFixture(t *testing.T, ss store.Store, invitation *model.Invitation) *store.ScopedRoleInvitationIssue {
	t.Helper()
	key := model.MailTemplateAccessAcademicUnitRoleInvitation
	if invitation.Purpose == model.InvitationPurposeInstitutionRole {
		key = model.MailTemplateAccessInstitutionRoleInvitation
	}
	at := invitation.CreatedAt
	occurrenceID, deliveryID, jobID := model.NewMailOccurrenceID(), model.NewMailDeliveryID(), model.NewJobID()
	command, err := model.EncodeMailDeliveryCommand(model.MailDeliveryCommand{DeliveryID: deliveryID})
	require.NoError(t, err)
	job, err := model.NewJob(jobID, model.JobTypeMailDeliverCredential, command, deliveryID.String(), at, at, model.MailMaximumAttempts)
	require.NoError(t, err)
	attempt, err := ss.Audit().Save(context.Background(), &model.AuditEvent{ActorID: invitation.InviterUserID,
		Action: string(model.ActionInvitationCreate), Resource: model.Resource{Type: resourceTypeForScopedInvitation(invitation), ID: invitation.ScopeID},
		ScopeType: invitation.ScopeType, ScopeID: invitation.ScopeID, Status: model.AuditStatusAttempt, NodeID: "scoped-role-invitation-store-test"})
	require.NoError(t, err)
	return &store.ScopedRoleInvitationIssue{Invitation: invitation, Lifetime: model.InvitationLifetime,
		Occurrence: &model.MailOccurrence{ID: occurrenceID, Kind: model.MailOccurrenceInvitation, TemplateKey: key,
			ActorUserID: invitation.InviterUserID, CreatedAt: at},
		Delivery: &model.MailDelivery{ID: deliveryID, OccurrenceID: occurrenceID, JobID: jobID, TargetInvitationID: invitation.ID,
			TemplateKey: key, TemplateDigest: strings.Repeat("d", 64), MaskedRecipient: "i***@example.edu",
			State: model.MailDeliveryQueued, CreatedAt: at, UpdatedAt: at, MessageDate: at, Deadline: invitation.ExpiresAt,
			MessageID:        "<scoped-role." + deliveryID.String() + "@example.test>",
			EncryptedPayload: json.RawMessage(`{"version":1,"key_id":"11111111111111111111111111111111","ciphertext":"secret"}`), Revision: 1},
		DeliveryJob: job, AuditEventID: attempt.ID, AuditAt: at}
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
	require.NoError(t, err)
	return &store.ScopedRoleInvitationAcceptance{ClaimHash: invitation.ClaimHash, UserID: userID, RoleBinding: binding,
		AuditEventID: attempt.ID, AuditAt: at,
		RequiredActions: []model.Action{model.ActionInvitationCreate, model.ActionRoleBindingManage}}
}

func requireScopedRoleAcceptanceAuditSuccess(t *testing.T, ctx context.Context, ss store.Store, input *store.ScopedRoleInvitationAcceptance) {
	t.Helper()
	event, err := ss.Audit().Get(ctx, input.AuditEventID)
	require.NoError(t, err)
	require.Equal(t, model.AuditStatusSuccess, event.Status)
	require.NotEmpty(t, event.Result)
	require.NotContains(t, string(event.Result), input.ClaimHash)
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
	require.True(t, store.IsConflict(err), "IssueStudentClass() concurrent disable error")
	_, err = ss.Invitation().Get(ctx, issue.Invitation.ID)
	require.True(t, store.IsNotFound(err), "Invitation survived concurrent inviter disable")
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
	require.NoError(t, err)
	acceptance := studentClassInvitationAcceptanceFixture(t, invitation, model.NowUTC())
	err = end(t, ctx, binding, func() error {
		_, acceptErr := ss.Invitation().AcceptStudentClass(ctx, acceptance)
		return acceptErr
	})
	require.True(t, store.IsConflict(err), "AcceptStudentClass() concurrent binding end error")
	current, err := ss.Invitation().Get(ctx, invitation.ID)
	require.NoError(t, err)
	require.Equal(t, model.InvitationPending, current.State)
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
	require.NoError(t, err)
	acceptance := studentClassInvitationAcceptanceFixture(t, invitation, model.NowUTC())
	err = archive(t, ctx, role, func() error {
		_, acceptErr := ss.Invitation().AcceptStudentClass(ctx, acceptance)
		return acceptErr
	})
	require.True(t, store.IsConflict(err), "AcceptStudentClass() concurrent Role archive error")
	current, err := ss.Invitation().Get(ctx, invitation.ID)
	require.NoError(t, err)
	require.Equal(t, model.InvitationPending, current.State)
}

func testInvitationAcceptStudentClassCommitsSuppressedNoticeWhenMailDisabled(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture, class, inviter, _, _, issuedAt := invitationAcceptanceStoreFixture(t, ctx, ss, "disabled-mail")
	issue := studentClassInvitationIssueFixture(t, ss, inviter, class, fixture.period, issuedAt)
	invitation, err := ss.Invitation().IssueStudentClass(ctx, issue)
	require.NoError(t, err)
	acceptance := studentClassInvitationAcceptanceFixture(t, invitation, model.NowUTC())
	acceptance.Delivery.State = model.MailDeliverySuppressed
	acceptance.Delivery.PublicFailureCode = model.MailDeliveryDisabledCode
	acceptance.Delivery.EncryptedPayload = nil
	acceptance.DeliveryJob, err = acceptance.DeliveryJob.RequestCancellation(acceptance.Delivery.CreatedAt)
	require.NoError(t, err)
	accepted, err := ss.Invitation().AcceptStudentClass(ctx, acceptance)
	require.NoError(t, err)
	require.Equal(t, model.InvitationAccepted, accepted.Invitation.State)
	delivery, err := ss.Mail().GetDelivery(ctx, acceptance.Delivery.ID)
	require.NoError(t, err)
	job, err := ss.Job().Get(ctx, acceptance.DeliveryJob.ID)
	require.NoError(t, err)
	require.Equal(t, model.MailDeliverySuppressed, delivery.State, "disabled acceptance mail = %#v / %#v", delivery, job)
	require.Equal(t, model.MailDeliveryDisabledCode, delivery.PublicFailureCode, "disabled acceptance mail = %#v / %#v", delivery, job)
	require.Empty(t, delivery.EncryptedPayload, "disabled acceptance mail = %#v / %#v", delivery, job)
	require.Equal(t, model.JobStatusCanceled, job.Status, "disabled acceptance mail = %#v / %#v", delivery, job)
	require.True(t, job.CompletedAt.Valid, "disabled acceptance mail = %#v / %#v", delivery, job)
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
	role, err := SaveRole(ctx, ss, &model.Role{Name: "elapsed-inviter-" + model.NewID(), DisplayName: "Elapsed Inviter",
		Permissions: []string{string(model.ActionInvitationCreate), string(model.ActionClassMembersManage)}})
	require.NoError(t, err)
	issuedAt := model.NowUTC()
	_, err = SaveRoleBinding(ctx, ss, &model.RoleBinding{UserID: inviter.ID, RoleID: role.ID,
		ScopeType: model.RoleScopeClass, ScopeID: class.ID.String(), StartsAt: issuedAt.Add(-time.Second)})
	require.NoError(t, err)
	first := studentClassInvitationIssueFixture(t, ss, inviter, class, period, issuedAt)
	first.Invitation.TargetEmail = "elapsed-" + model.NewID() + "@example.edu"
	first.Invitation.IntendedStartsAt = issuedAt
	// The window must outlast claiming and starting the delivery on a loaded host.
	first.Invitation.IntendedEndsAt = model.OptionalTimeFrom(model.NowUTC().Add(6 * time.Second))
	created, err := ss.Invitation().IssueStudentClass(ctx, first)
	require.NoError(t, err)
	claimed := false
	for attempt := 0; attempt < 32 && !claimed; attempt++ {
		claim, claimErr := ss.Job().ClaimNext(ctx, &store.JobClaimRequest{
			Types: []model.JobType{model.JobTypeMailDeliverCredential}, NodeID: "invitation-terminalization",
			ClaimToken: mustClaimToken(t), LeaseDuration: time.Minute,
		})
		require.NoError(t, claimErr)
		claimed = claim.Job.ID == first.DeliveryJob.ID
	}
	require.True(t, claimed, "failed to claim elapsed Invitation delivery Job")
	_, err = ss.Mail().StartDelivery(ctx, &store.MailDeliveryStart{ID: first.Delivery.ID, ExpectedRevision: first.Delivery.Revision, At: model.NowUTC()})
	require.NoError(t, err)
	time.Sleep(time.Until(first.Invitation.IntendedEndsAt.Time) + 20*time.Millisecond)
	second := studentClassInvitationIssueFixture(t, ss, inviter, class, period, model.NowUTC())
	second.Invitation.IntendedStartsAt = model.NowUTC()
	second.Invitation.TargetEmail = first.Invitation.TargetEmail
	replacement, err := ss.Invitation().IssueStudentClass(ctx, second)
	require.NoError(t, err)
	old, err := ss.Invitation().Get(ctx, created.ID)
	require.NoError(t, err)
	require.Equal(t, model.InvitationExpired, old.State, "elapsed/replacement Invitations = %#v / %#v", old, replacement)
	require.Equal(t, model.InvitationPending, replacement.State, "elapsed/replacement Invitations = %#v / %#v", old, replacement)
	require.NotEqual(t, old.ID, replacement.ID, "elapsed/replacement Invitations = %#v / %#v", old, replacement)
	obsolete, err := ss.Mail().GetDelivery(ctx, first.Delivery.ID)
	require.NoError(t, err)
	require.Equal(t, model.MailDeliverySuppressed, obsolete.State)
	require.Equal(t, model.MailDeliveryObsoleteCode, obsolete.PublicFailureCode)
	require.Empty(t, obsolete.EncryptedPayload)
	obsoleteJob, err := ss.Job().Get(ctx, first.DeliveryJob.ID)
	require.NoError(t, err)
	require.Equal(t, model.JobStatusCancelRequested, obsoleteJob.Status)
	require.False(t, obsoleteJob.CompletedAt.Valid)
	if payloadKeyReferences != nil {
		references := payloadKeyReferences(t, ctx, payloadKeyID)
		require.Equal(t, baselineReferences+1, references, "active payload-key references")
	}
}

func testInvitationAcceptStudentClassResolvesExistingUser(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture, class, inviter, _, _, issuedAt := invitationAcceptanceStoreFixture(t, ctx, ss, "existing-user")
	existing := newUser()
	existing.Email = "existing-invited-" + model.NewID() + "@example.edu"
	existing.EmailVerified = false
	existing, err := createUser(t, ctx, ss, existing)
	require.NoError(t, err)
	issue := studentClassInvitationIssueFixture(t, ss, inviter, class, fixture.period, issuedAt)
	issue.Invitation.TargetEmail = existing.Email
	invitation, err := ss.Invitation().IssueStudentClass(ctx, issue)
	require.NoError(t, err)
	acceptance := studentClassInvitationAcceptanceFixture(t, invitation, model.NowUTC())
	handle, proof := model.NewCredentialToken(), model.NewCredentialToken()
	transaction, err := ss.BrowserAuthentication().CreateInvitation(ctx, &store.BrowserInvitationTransactionCreation{
		ID: model.NewBrowserAuthenticationTransactionID(), InstitutionID: fixture.institution.ID,
		Issuer: "https://proctor.example.edu", InvitationID: invitation.ID,
		InvitationPurpose: invitation.Purpose, InvitationClaimHash: invitation.ClaimHash,
		HandleHash: model.HashToken(handle), BrowserProofHash: model.HashToken(proof),
	})
	require.NoError(t, err)
	require.False(t, transaction.ID.IsZero())
	require.False(t, transaction.ExpiresAt.After(invitation.ExpiresAt))
	acceptance.BrowserTransaction = &store.BrowserInvitationTransactionProof{
		ID: transaction.ID, HandleHash: model.HashToken(handle), BrowserProofHash: model.HashToken(proof),
	}
	otherIssue := studentClassInvitationIssueFixture(t, ss, inviter, class, fixture.period, model.NowUTC())
	otherIssue.Invitation.TargetEmail = "cross-proof-" + model.NewID() + "@example.edu"
	otherIssue.Invitation.Suggestions.Username = "cross-proof-" + model.NewID()
	otherInvitation, err := ss.Invitation().IssueStudentClass(ctx, otherIssue)
	require.NoError(t, err)
	otherAcceptance := studentClassInvitationAcceptanceFixture(t, otherInvitation, model.NowUTC())
	otherHandle, otherProof := model.NewCredentialToken(), model.NewCredentialToken()
	otherTransaction, err := ss.BrowserAuthentication().CreateInvitation(ctx, &store.BrowserInvitationTransactionCreation{
		ID: model.NewBrowserAuthenticationTransactionID(), InstitutionID: fixture.institution.ID,
		Issuer: "https://proctor.example.edu", InvitationID: otherInvitation.ID,
		InvitationPurpose: otherInvitation.Purpose, InvitationClaimHash: otherInvitation.ClaimHash,
		HandleHash: model.HashToken(otherHandle), BrowserProofHash: model.HashToken(otherProof),
	})
	require.NoError(t, err)
	otherAcceptance.BrowserTransaction = &store.BrowserInvitationTransactionProof{ID: otherTransaction.ID,
		HandleHash: model.HashToken(otherHandle), BrowserProofHash: model.HashToken(otherProof)}
	crossInvitation := *acceptance
	crossProof := *otherAcceptance.BrowserTransaction
	crossInvitation.BrowserTransaction = &crossProof
	_, err = ss.Invitation().AcceptStudentClass(ctx, &crossInvitation)
	require.Error(t, err, "AcceptStudentClass accepted another Invitation's valid browser proof")
	for _, candidate := range []struct {
		invitation *model.Invitation
		handle     string
		proof      string
	}{{invitation, handle, proof}, {otherInvitation, otherHandle, otherProof}} {
		pending, getErr := ss.Invitation().Get(ctx, candidate.invitation.ID)
		require.NoError(t, getErr)
		require.Equal(t, model.InvitationPending, pending.State, "cross-Invitation proof changed Invitation %s to", pending.ID)
		_, resolveErr := ss.BrowserAuthentication().ResolveInvitation(ctx,
			model.HashToken(candidate.handle), model.HashToken(candidate.proof))
		require.NoError(t, resolveErr, "cross-Invitation rejection consumed transaction for %s", pending.ID)
	}
	rejected := *acceptance
	rejectedProof := *acceptance.BrowserTransaction
	rejectedProof.BrowserProofHash = model.HashToken(model.NewCredentialToken())
	rejected.BrowserTransaction = &rejectedProof
	_, err = ss.Invitation().AcceptStudentClass(ctx, &rejected)
	require.Error(t, err, "AcceptStudentClass accepted a mismatched browser proof")
	stillPending, err := ss.Invitation().Get(ctx, invitation.ID)
	require.NoError(t, err)
	require.Equal(t, model.InvitationPending, stillPending.State)
	_, err = ss.BrowserAuthentication().ResolveInvitation(ctx, model.HashToken(handle), model.HashToken(proof))
	require.NoError(t, err)
	accepted, err := ss.Invitation().AcceptStudentClass(ctx, acceptance)
	require.NoError(t, err)
	require.Equal(t, existing.ID, accepted.User.ID)
	require.True(t, accepted.User.EmailVerified)
	require.Equal(t, existing.ID, accepted.ClassMember.UserID)
	require.Equal(t, existing.ID, accepted.Affiliation.UserID)
	_, err = ss.BrowserAuthentication().ResolveInvitation(ctx, model.HashToken(handle), model.HashToken(proof))
	require.True(t, store.IsNotFound(err), "resolved consumed browser Invitation transaction")
	otherAccepted, err := ss.Invitation().AcceptStudentClass(ctx, otherAcceptance)
	require.NoError(t, err)
	require.Equal(t, otherInvitation.ID, otherAccepted.Invitation.ID)
	require.Equal(t, model.InvitationAccepted, otherAccepted.Invitation.State)
	_, err = ss.BrowserAuthentication().CreateInvitation(ctx, &store.BrowserInvitationTransactionCreation{
		ID: model.NewBrowserAuthenticationTransactionID(), InstitutionID: fixture.institution.ID,
		Issuer: "https://proctor.example.edu", InvitationID: invitation.ID,
		InvitationPurpose: invitation.Purpose, InvitationClaimHash: invitation.ClaimHash,
		HandleHash: model.HashToken(model.NewCredentialToken()), BrowserProofHash: model.HashToken(model.NewCredentialToken()),
	})
	require.True(t, store.IsConflict(err), "created browser transaction for accepted Invitation")
	replayed, err := ss.Invitation().AcceptStudentClass(ctx, acceptance)
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
	require.Equal(t, existing.ID, replayed.User.ID)
	_, err = ss.PasswordCredential().GetByUser(ctx, existing.ID)
	require.NoError(t, err)
	_, err = ss.Job().Get(ctx, acceptance.DefaultProfilePictureJob.ID)
	require.True(t, store.IsNotFound(err), "existing User unexpectedly enqueued a second default-picture Job")
}

func testBrowserInvitationTransactionCreation(t *testing.T, ss store.Store, probe InvitationSQLProbe) {
	t.Helper()
	ctx := context.Background()
	fixture, class, inviter, role, _, issuedAt := invitationAcceptanceStoreFixture(t, ctx, ss, "browser-handoff-bounds")

	issueInvitation := func(t *testing.T, targetClass *model.Class, period *model.AcademicPeriod, at time.Time, intendedEnd model.OptionalTime) *model.Invitation {
		t.Helper()
		input := studentClassInvitationIssueFixture(t, ss, inviter, targetClass, period, at)
		unique := strings.ToLower(model.NewID())
		input.Invitation.TargetEmail = "browser-" + unique + "@example.edu"
		input.Invitation.Suggestions.Username = "browser-" + unique
		input.Invitation.IntendedEndsAt = intendedEnd
		created, err := ss.Invitation().IssueStudentClass(ctx, input)
		require.NoError(t, err)
		return created
	}
	newCreation := func(invitation *model.Invitation) (*store.BrowserInvitationTransactionCreation, string, string) {
		handleHash := model.HashToken(model.NewCredentialToken())
		proofHash := model.HashToken(model.NewCredentialToken())
		return &store.BrowserInvitationTransactionCreation{
			ID: model.NewBrowserAuthenticationTransactionID(), InstitutionID: fixture.institution.ID,
			Issuer: "https://proctor.example.edu", InvitationID: invitation.ID,
			InvitationPurpose: invitation.Purpose, InvitationClaimHash: invitation.ClaimHash,
			HandleHash: handleHash, BrowserProofHash: proofHash,
		}, handleHash, proofHash
	}
	assertNotInserted := func(t *testing.T, input *store.BrowserInvitationTransactionCreation, handleHash, proofHash string) {
		t.Helper()
		if probe.BrowserTransactionExists != nil {
			require.False(t, probe.BrowserTransactionExists(t, ctx, input.ID), "rejected browser Invitation transaction %s was inserted", input.ID)
		}
		_, err := ss.BrowserAuthentication().ResolveInvitation(ctx, handleHash, proofHash)
		require.True(t, store.IsNotFound(err), "ResolveInvitation() after rejected creation")
	}

	t.Run("RejectsInvalidCreations", func(t *testing.T) {
		invitation := issueInvitation(t, class, fixture.period, issuedAt, model.OptionalTimeFrom(fixture.period.EndsAt))
		valid, _, _ := newCreation(invitation)
		tests := []struct {
			name   string
			input  *store.BrowserInvitationTransactionCreation
			mutate func(*store.BrowserInvitationTransactionCreation)
		}{
			{name: "nil", input: nil},
			{name: "ID", mutate: func(value *store.BrowserInvitationTransactionCreation) { value.ID = "" }},
			{name: "institution", mutate: func(value *store.BrowserInvitationTransactionCreation) { value.InstitutionID = "" }},
			{name: "issuer", mutate: func(value *store.BrowserInvitationTransactionCreation) { value.Issuer = "http://proctor.example.edu" }},
			{name: "Invitation ID", mutate: func(value *store.BrowserInvitationTransactionCreation) { value.InvitationID = "" }},
			{name: "Invitation purpose", mutate: func(value *store.BrowserInvitationTransactionCreation) { value.InvitationPurpose = "unknown" }},
			{name: "claim hash", mutate: func(value *store.BrowserInvitationTransactionCreation) { value.InvitationClaimHash = "raw-claim" }},
			{name: "handle hash", mutate: func(value *store.BrowserInvitationTransactionCreation) { value.HandleHash = "raw-handle" }},
			{name: "proof hash", mutate: func(value *store.BrowserInvitationTransactionCreation) { value.BrowserProofHash = "raw-proof" }},
			{name: "equal handle and proof hashes", mutate: func(value *store.BrowserInvitationTransactionCreation) { value.BrowserProofHash = value.HandleHash }},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				input := test.input
				if test.mutate != nil {
					candidate := *valid
					test.mutate(&candidate)
					input = &candidate
				}
				_, createErr := ss.BrowserAuthentication().CreateInvitation(ctx, input)
				require.True(t, isInvalidInput(createErr), "CreateInvitation() error")
			})
		}
	})

	t.Run("FiveMinuteLifetime", func(t *testing.T) {
		invitation := issueInvitation(t, class, fixture.period, issuedAt, model.OptionalTimeFrom(fixture.period.EndsAt))
		input, _, _ := newCreation(invitation)
		before := model.NowUTC()
		transaction, err := ss.BrowserAuthentication().CreateInvitation(ctx, input)
		after := model.NowUTC()
		require.NoError(t, err)
		require.False(t, transaction.ExpiresAt.Before(before.Add(model.BrowserAuthenticationTransactionLifetime-time.Second)), "browser Invitation transaction expiry = %s, call window = %s..%s", transaction.ExpiresAt, before, after)
		require.False(t, transaction.ExpiresAt.After(after.Add(model.BrowserAuthenticationTransactionLifetime+time.Second)), "browser Invitation transaction expiry = %s, call window = %s..%s", transaction.ExpiresAt, before, after)
	})

	t.Run("InvitationExpiryBoundsLifetime", func(t *testing.T) {
		expiresAt := model.NowUTC().Add(2 * time.Minute)
		invitation := issueInvitation(t, class, fixture.period, expiresAt.Add(-model.InvitationLifetime), model.OptionalTimeFrom(fixture.period.EndsAt))
		input, _, _ := newCreation(invitation)
		transaction, err := ss.BrowserAuthentication().CreateInvitation(ctx, input)
		require.NoError(t, err)
		require.True(t, transaction.ExpiresAt.Equal(invitation.ExpiresAt), "browser Invitation deadline = %s, want Invitation expiry", transaction.ExpiresAt)
	})

	activePeriod := saveAcademicPeriod(t, ctx, ss, fixture.institution.ID.String(), "browser-handoff-"+model.NewID(), model.MillisFromTime(model.NowUTC().Add(-time.Hour)))
	activeClass := saveClass(t, ctx, ss, fixture.level.ID.String(), activePeriod.ID.String(), "browser-handoff-"+model.NewID())
	_, err := SaveRoleBinding(ctx, ss, &model.RoleBinding{
		UserID: inviter.ID, RoleID: role.ID, ScopeType: model.RoleScopeClass, ScopeID: activeClass.ID.String(),
		StartsAt: activePeriod.StartsAt,
	})
	require.NoError(t, err)

	t.Run("IntendedEndBoundsLifetime", func(t *testing.T) {
		intendedEnd := model.NowUTC().Add(2 * time.Minute)
		invitation := issueInvitation(t, activeClass, activePeriod, model.NowUTC().Add(-time.Minute), model.OptionalTimeFrom(intendedEnd))
		input, _, _ := newCreation(invitation)
		transaction, err := ss.BrowserAuthentication().CreateInvitation(ctx, input)
		require.NoError(t, err)
		require.True(t, transaction.ExpiresAt.Equal(invitation.IntendedEndsAt.Time), "browser Invitation deadline = %s, want intended end", transaction.ExpiresAt)
	})

	t.Run("RejectsPurposeMismatchWithoutInsert", func(t *testing.T) {
		invitation := issueInvitation(t, class, fixture.period, issuedAt, model.OptionalTimeFrom(fixture.period.EndsAt))
		input, handleHash, proofHash := newCreation(invitation)
		input.InvitationPurpose = model.InvitationPurposeTeacherAcademicUnit
		_, err := ss.BrowserAuthentication().CreateInvitation(ctx, input)
		require.True(t, store.IsNotFound(err), "CreateInvitation() purpose mismatch")
		assertNotInserted(t, input, handleHash, proofHash)
	})

	t.Run("RejectsClaimMismatchWithoutInsert", func(t *testing.T) {
		invitation := issueInvitation(t, class, fixture.period, issuedAt, model.OptionalTimeFrom(fixture.period.EndsAt))
		input, handleHash, proofHash := newCreation(invitation)
		input.InvitationClaimHash = model.HashInvitationClaim(model.NewCredentialToken())
		_, err := ss.BrowserAuthentication().CreateInvitation(ctx, input)
		require.True(t, store.IsNotFound(err), "CreateInvitation() claim mismatch")
		assertNotInserted(t, input, handleHash, proofHash)
	})

	t.Run("RejectsExpiredInvitationWithoutInsert", func(t *testing.T) {
		if probe.SetInvitationExpiresAt == nil {
			t.Skip("requires an authoritative SQL clock probe")
		}
		expiresAt := model.NowUTC().Add(2 * time.Minute)
		invitation := issueInvitation(t, class, fixture.period, expiresAt.Add(-model.InvitationLifetime), model.OptionalTimeFrom(fixture.period.EndsAt))
		probe.SetInvitationExpiresAt(t, ctx, invitation.ID, model.NowUTC().Add(-time.Minute))
		defer probe.SetInvitationExpiresAt(t, ctx, invitation.ID, invitation.ExpiresAt)
		input, handleHash, proofHash := newCreation(invitation)
		_, err := ss.BrowserAuthentication().CreateInvitation(ctx, input)
		require.True(t, store.IsConflict(err), "CreateInvitation() expired Invitation")
		assertNotInserted(t, input, handleHash, proofHash)
	})

	t.Run("RejectsEndedInvitationWithoutInsert", func(t *testing.T) {
		if probe.SetInvitationIntendedEndsAt == nil {
			t.Skip("requires an authoritative SQL clock probe")
		}
		invitation := issueInvitation(t, activeClass, activePeriod, model.NowUTC().Add(-time.Minute), model.OptionalTimeFrom(model.NowUTC().Add(2*time.Minute)))
		probe.SetInvitationIntendedEndsAt(t, ctx, invitation.ID, model.NowUTC().Add(-time.Minute))
		defer probe.SetInvitationIntendedEndsAt(t, ctx, invitation.ID, invitation.IntendedEndsAt.Time)
		input, handleHash, proofHash := newCreation(invitation)
		_, err := ss.BrowserAuthentication().CreateInvitation(ctx, input)
		require.True(t, store.IsConflict(err), "CreateInvitation() ended Invitation")
		assertNotInserted(t, input, handleHash, proofHash)
	})
}

func testInvitationAcceptStudentClassRejectsConflictingMembershipAtomically(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture, class, inviter, _, _, issuedAt := invitationAcceptanceStoreFixture(t, ctx, ss, "conflict-target")
	otherClass := saveClass(t, ctx, ss, fixture.level.ID.String(), fixture.period.ID.String(), "conflict-existing")
	existing := saveUser(t, ctx, ss)
	_, err := ss.Affiliation().Save(ctx, &model.Affiliation{UserID: existing.ID, Kind: model.AffiliationStudent, StartsAt: issuedAt})
	require.NoError(t, err)
	_, err = ss.ClassMember().SeedEnroll(ctx, &model.ClassMember{ClassID: otherClass.ID, UserID: existing.ID, StartsAt: fixture.period.StartsAt})
	require.NoError(t, err)
	issue := studentClassInvitationIssueFixture(t, ss, inviter, class, fixture.period, issuedAt)
	issue.Invitation.TargetEmail = existing.Email
	invitation, err := ss.Invitation().IssueStudentClass(ctx, issue)
	require.NoError(t, err)
	acceptance := studentClassInvitationAcceptanceFixture(t, invitation, model.NowUTC())
	_, err = ss.Invitation().AcceptStudentClass(ctx, acceptance)
	require.Error(t, err, "AcceptStudentClass() accepted a conflicting active Class membership")
	current, getErr := ss.Invitation().Get(ctx, invitation.ID)
	require.NoError(t, getErr)
	require.Equal(t, model.InvitationPending, current.State)
	_, getErr = ss.Mail().GetDelivery(ctx, acceptance.Delivery.ID)
	require.True(t, store.IsNotFound(getErr), "acceptance delivery survived conflict rollback")
	_, getErr = ss.PasswordCredential().GetByUser(ctx, existing.ID)
	require.True(t, store.IsNotFound(getErr), "password credential survived conflict rollback")
}

func invitationAcceptanceStoreFixture(t *testing.T, ctx context.Context, ss store.Store, suffix string) (classFixture, *model.Class, *model.User, *model.Role, *model.RoleBinding, time.Time) {
	t.Helper()
	fixture := saveClassFixture(t, ctx, ss)
	class := saveClass(t, ctx, ss, fixture.level.ID.String(), fixture.period.ID.String(), "invitation-"+suffix)
	inviter := saveUser(t, ctx, ss)
	role, err := SaveRole(ctx, ss, &model.Role{Name: "invite-" + suffix + "-" + model.NewID(), DisplayName: "Student Inviter",
		Permissions: []string{string(model.ActionInvitationCreate), string(model.ActionClassMembersManage)}})
	require.NoError(t, err)
	issuedAt := model.NowUTC().Add(-time.Minute)
	binding, err := SaveRoleBinding(ctx, ss, &model.RoleBinding{UserID: inviter.ID, RoleID: role.ID,
		ScopeType: model.RoleScopeClass, ScopeID: class.ID.String(), StartsAt: issuedAt.Add(-time.Second)})
	require.NoError(t, err)
	return fixture, class, inviter, role, binding, issuedAt
}

func testInvitationIssueStudentClassAtomic(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture := saveClassFixture(t, ctx, ss)
	class := saveClass(t, ctx, ss, fixture.level.ID.String(), fixture.period.ID.String(), "invitation-class")
	inviter := saveUser(t, ctx, ss)
	role, err := SaveRole(ctx, ss, &model.Role{
		Name: "student-inviter-" + model.NewID(), DisplayName: "Student Inviter",
		Permissions: []string{string(model.ActionInvitationCreate), string(model.ActionClassMembersManage)},
	})
	require.NoError(t, err)
	issuedAt := model.NowUTC().Add(-time.Minute)
	_, err = SaveRoleBinding(ctx, ss, &model.RoleBinding{
		UserID: inviter.ID, RoleID: role.ID, ScopeType: model.RoleScopeClass,
		ScopeID: class.ID.String(), StartsAt: issuedAt.Add(-time.Second),
	})
	require.NoError(t, err)

	input := studentClassInvitationIssueFixture(t, ss, inviter, class, fixture.period, issuedAt)
	created, err := ss.Invitation().IssueStudentClass(ctx, input)
	require.NoError(t, err)
	require.Equal(t, input.Invitation.ID, created.ID)
	require.Equal(t, "student@example.edu", created.TargetEmail)
	require.Equal(t, input.Invitation.ClaimHash, created.ClaimHash)
	require.Equal(t, model.InvitationPending, created.State)
	byID, err := ss.Invitation().Get(ctx, created.ID)
	require.NoError(t, err)
	byClaim, err := ss.Invitation().GetByClaimHash(ctx, created.ClaimHash)
	require.NoError(t, err)
	require.Equal(t, created.ID, byID.ID, "Invitation reads = %#v / %#v", byID, byClaim)
	require.Equal(t, created.ID, byClaim.ID, "Invitation reads = %#v / %#v", byID, byClaim)
	delivery, err := ss.Mail().GetDelivery(ctx, input.Delivery.ID)
	require.NoError(t, err)
	require.Equal(t, created.ID, delivery.TargetInvitationID)
	require.False(t, delivery.TargetUserID.IsValid())
	require.Equal(t, model.MailTemplateAccessStudentClassInvitation, delivery.TemplateKey)
	job, err := ss.Job().Get(ctx, input.DeliveryJob.ID)
	require.NoError(t, err)
	require.Equal(t, model.JobStatusQueued, job.Status)
	require.Equal(t, model.JobTypeMailDeliverCredential, job.Type)
	audit, err := ss.Audit().Get(ctx, input.AuditEventID)
	require.NoError(t, err)
	require.Equal(t, model.AuditStatusSuccess, audit.Status)
	require.NotContains(t, string(audit.Result), created.TargetEmail)
	require.NotContains(t, string(audit.Result), created.ClaimHash)

	rollback := studentClassInvitationIssueFixture(t, ss, inviter, class, fixture.period, issuedAt.Add(time.Second))
	rollback.Invitation.TargetEmail = "rollback@example.edu"
	rollback.AuditEventID = model.NewAuditEventID()
	_, err = ss.Invitation().IssueStudentClass(ctx, rollback)
	require.Error(t, err, "IssueStudentClass() succeeded without its durable audit attempt")
	_, err = ss.Invitation().Get(ctx, rollback.Invitation.ID)
	require.True(t, store.IsNotFound(err), "rolled-back Invitation error")
	_, err = ss.Mail().GetDelivery(ctx, rollback.Delivery.ID)
	require.True(t, store.IsNotFound(err), "rolled-back delivery error")
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
	require.NoError(t, err)
	occurrenceID, deliveryID, jobID := model.NewMailOccurrenceID(), model.NewMailDeliveryID(), model.NewJobID()
	command, err := model.EncodeMailDeliveryCommand(model.MailDeliveryCommand{DeliveryID: deliveryID})
	require.NoError(t, err)
	job, err := model.NewJob(jobID, model.JobTypeMailDeliverCredential, command, deliveryID.String(), issuedAt, issuedAt, model.MailMaximumAttempts)
	require.NoError(t, err)
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
	require.NoError(t, err)
	return &store.StudentClassInvitationIssue{
		Invitation: invitation,
		Occurrence: &model.MailOccurrence{ID: occurrenceID, Kind: model.MailOccurrenceInvitation, TemplateKey: model.MailTemplateAccessStudentClassInvitation, ActorUserID: inviter.ID, CreatedAt: issuedAt},
		Delivery:   delivery, DeliveryJob: job, AuditEventID: attempt.ID, AuditAt: issuedAt,
	}
}

func testInvitationAcceptStudentClassAtomicAndReplaySafe(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture := saveClassFixture(t, ctx, ss)
	class := saveClass(t, ctx, ss, fixture.level.ID.String(), fixture.period.ID.String(), "invitation-accept-class")
	inviter := saveUser(t, ctx, ss)
	role, err := SaveRole(ctx, ss, &model.Role{
		Name: "accept-inviter-" + model.NewID(), DisplayName: "Accept Inviter",
		Permissions: []string{string(model.ActionInvitationCreate), string(model.ActionClassMembersManage)},
	})
	require.NoError(t, err)
	issuedAt := model.NowUTC().Add(-time.Minute)
	_, err = SaveRoleBinding(ctx, ss, &model.RoleBinding{
		UserID: inviter.ID, RoleID: role.ID, ScopeType: model.RoleScopeClass,
		ScopeID: class.ID.String(), StartsAt: issuedAt.Add(-time.Second),
	})
	require.NoError(t, err)

	issue := studentClassInvitationIssueFixture(t, ss, inviter, class, fixture.period, issuedAt)
	invitation, err := ss.Invitation().IssueStudentClass(ctx, issue)
	require.NoError(t, err)
	acceptance := studentClassInvitationAcceptanceFixture(t, invitation, model.NowUTC())
	accepted, err := ss.Invitation().AcceptStudentClass(ctx, acceptance)
	require.NoError(t, err)
	require.False(t, accepted.Replayed)
	require.Equal(t, model.InvitationAccepted, accepted.Invitation.State)
	require.Equal(t, acceptance.User.ID, accepted.User.ID)
	require.Equal(t, invitation.TargetEmail, accepted.User.Email)
	require.True(t, accepted.User.EmailVerified)
	require.Equal(t, model.AffiliationStudent, accepted.Affiliation.Kind)
	require.Equal(t, class.ID, accepted.ClassMember.ClassID)
	require.Equal(t, fixture.period.ID, accepted.ClassMember.AcademicPeriodID)
	require.Equal(t, accepted.User.ID, accepted.ClassMember.UserID)
	_, err = ss.UserSettings().Get(ctx, accepted.User.ID)
	require.NoError(t, err)
	_, err = ss.PasswordCredential().GetByUser(ctx, accepted.User.ID)
	require.NoError(t, err)
	_, err = ss.Job().Get(ctx, acceptance.DefaultProfilePictureJob.ID)
	require.NoError(t, err)
	delivery, deliveryErr := ss.Mail().GetDelivery(ctx, acceptance.Delivery.ID)
	require.NoError(t, deliveryErr, "acceptance delivery = %#v", delivery)
	require.Equal(t, accepted.User.ID, delivery.TargetUserID, "acceptance delivery = %#v", delivery)
	require.False(t, delivery.TargetInvitationID.IsValid(), "acceptance delivery = %#v", delivery)
	obsolete, err := ss.Mail().GetDelivery(ctx, issue.Delivery.ID)
	require.NoError(t, err)
	require.Equal(t, model.MailDeliverySuppressed, obsolete.State)
	require.Equal(t, model.MailDeliveryObsoleteCode, obsolete.PublicFailureCode)
	require.Empty(t, obsolete.EncryptedPayload)
	progressedAt := accepted.ClassMember.StartsAt.Add(24 * time.Hour)
	ended, err := ss.ClassMember().SeedEnd(ctx, &store.ClassMemberSeedEnd{ID: accepted.ClassMember.ID, ExpectedRevision: accepted.ClassMember.Revision, EndAt: progressedAt})
	require.NoError(t, err)
	newMembership := &model.ClassMember{ClassID: class.ID, AcademicPeriodID: fixture.period.ID, UserID: accepted.User.ID,
		StartsAt: progressedAt, EndsAt: accepted.ClassMember.EndsAt}
	enrollment, err := ss.ClassMember().SeedEnroll(ctx, newMembership)
	require.NoError(t, err)
	newMembership = enrollment.Membership

	replayed, err := ss.Invitation().AcceptStudentClass(ctx, acceptance)
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
	require.Equal(t, accepted.User.ID, replayed.User.ID)
	require.Equal(t, accepted.Invitation.Revision, replayed.Invitation.Revision)
	require.Equal(t, accepted.Affiliation.ID, replayed.Affiliation.ID)
	require.Equal(t, ended.ID, replayed.ClassMember.ID)
	require.NotEqual(t, newMembership.ID, replayed.ClassMember.ID)
	listed, err := ss.ClassMember().ListByClass(ctx, class.ID, accepted.ClassMember.StartsAt)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, accepted.ClassMember.ID, listed[0].ID)
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
	require.NoError(t, err)
	credential := &model.PasswordCredential{UserID: user.ID, PasswordHash: "encoded-invitation-password"}
	credential.PrepareCreate(model.NewPasswordCredentialID(), acceptedAt)
	defaultCommand, err := model.EncodeDefaultProfilePictureCommand(model.DefaultProfilePictureCommand{UserID: user.ID})
	require.NoError(t, err)
	defaultJob, err := model.NewJob(model.NewJobID(), model.JobTypeProfilePictureGenerateDefault, defaultCommand, user.ID.String(), acceptedAt, acceptedAt, 8)
	require.NoError(t, err)
	effectiveStart := invitation.EffectiveStartsAt(acceptedAt)
	affiliation := &model.Affiliation{UserID: user.ID, Kind: model.AffiliationStudent, StartsAt: effectiveStart}
	affiliation.PrepareCreate(model.NewAffiliationID(), acceptedAt)
	member := &model.ClassMember{
		ClassID: invitation.ClassID, AcademicPeriodID: invitation.AcademicPeriodID,
		UserID: user.ID, StartsAt: effectiveStart, EndsAt: invitation.IntendedEndsAt,
	}
	member.PrepareCreate(model.NewClassMemberID(), acceptedAt)
	occurrenceID, deliveryID, deliveryJobID := model.NewMailOccurrenceID(), model.NewMailDeliveryID(), model.NewJobID()
	deliveryCommand, err := model.EncodeMailDeliveryCommand(model.MailDeliveryCommand{DeliveryID: deliveryID})
	require.NoError(t, err)
	deliveryJob, err := model.NewJob(deliveryJobID, model.JobTypeMailDeliver, deliveryCommand, deliveryID.String(), acceptedAt, acceptedAt, model.MailMaximumAttempts)
	require.NoError(t, err)
	delivery := &model.MailDelivery{
		ID: deliveryID, OccurrenceID: occurrenceID, JobID: deliveryJobID, TargetUserID: user.ID,
		TemplateKey: model.MailTemplateAccessInvitationAccepted, TemplateDigest: strings.Repeat("c", 64),
		MaskedRecipient: "s***@example.edu", State: model.MailDeliveryQueued,
		CreatedAt: acceptedAt, UpdatedAt: acceptedAt, MessageDate: acceptedAt,
		Deadline: acceptedAt.Add(24 * time.Hour), MessageID: "<invitation-accepted." + deliveryID.String() + "@example.test>",
		EncryptedPayload: json.RawMessage(`{"version":1,"key_id":"11111111111111111111111111111111","ciphertext":"accepted"}`), Revision: 1,
	}
	return &store.StudentClassInvitationAcceptance{
		ClaimHash: invitation.ClaimHash, AcceptedAt: acceptedAt,
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
