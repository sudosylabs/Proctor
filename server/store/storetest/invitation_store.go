// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"slices"
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
	sourceClass := saveClass(t, ctx, ss, fixture.level.ID.String(), fixture.period.ID.String(), "progression-source-"+model.NewId())
	destinationPeriod := saveAcademicPeriod(t, ctx, ss, fixture.institution.ID.String(), "progression-destination-"+model.NewId(), model.MillisFromTime(fixture.period.EndsAt)+1)
	destinationClass := saveClass(t, ctx, ss, fixture.level.ID.String(), destinationPeriod.ID.String(), "progression-destination-"+model.NewId())
	actor, student := saveUser(t, ctx, ss), saveUser(t, ctx, ss)
	effectiveAt := destinationPeriod.StartsAt.Add(time.Second)
	_, err := ss.Affiliation().Save(ctx, &model.Affiliation{UserID: student.ID, Kind: model.AffiliationStudent, StartsAt: fixture.period.StartsAt.Add(-time.Second)})
	requireNoError(t, err)
	source, err := ss.ClassMember().Enroll(ctx, &model.ClassMember{ClassID: sourceClass.ID, UserID: student.ID, StartsAt: fixture.period.StartsAt})
	requireNoError(t, err)
	authorityRole, err := ss.Role().Save(ctx, &model.Role{Name: "progression-authority-" + model.NewId(), DisplayName: "Progression Authority",
		Permissions: []string{string(model.ActionAcademicProgressionManage), string(model.ActionClassMembersManage)}})
	requireNoError(t, err)
	_, err = ss.RoleBinding().Save(ctx, &model.RoleBinding{UserID: actor.ID, RoleID: authorityRole.ID, ScopeType: model.RoleScopeInstitution,
		ScopeID: fixture.institution.ID.String(), StartsAt: model.NowUTC().Add(-time.Minute)})
	requireNoError(t, err)
	session, credentials, _ := saveSession(t, ctx, ss, actor.ID.String(), 10)
	principal := model.Principal{UserID: actor.ID, SessionID: session.ID, CredentialID: model.PrincipalCredentialID(credentials[0].ID),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: session.ClientType, AuthenticatedAt: session.AuthenticatedAt}
	at := model.NowUTC()
	importID := model.NewOnboardingImportID()
	previewJob, err := model.NewJob(model.NewJobID(), model.JobTypeStudentProgressionPreview, 1, json.RawMessage(`{"import_id":"`+importID.String()+`"}`), "progression-preview:"+importID.String(), at, at, 3)
	requireNoError(t, err)
	creationAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, destinationClass.ID.String(), "student_progression.dry_run")
	creationSourceAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, sourceClass.ID.String(), "student_progression.dry_run")
	created, err := ss.OnboardingImport().CreateOnboardingImport(ctx, &store.OnboardingImportCreation{Import: &store.OnboardingImport{ID: importID,
		Mode: model.OnboardingImportStudentProgression, State: model.OnboardingImportParsing, ScopeType: model.RoleScopeClass, ScopeID: destinationClass.ID.String(),
		SourcePeriodID: fixture.period.ID, SourceClassID: sourceClass.ID, DestinationPeriodID: destinationPeriod.ID, DestinationClassID: destinationClass.ID,
		SourcePeriodRevision: fixture.period.Revision, SourceClassRevision: sourceClass.Revision, DestinationPeriodRevision: destinationPeriod.Revision,
		DestinationClassRevision: destinationClass.Revision, EffectiveAt: effectiveAt, ActorUserID: actor.ID, Principal: principal, ParseJobID: previewJob.ID,
		CreatedAt: at, UpdatedAt: at, ExpiresAt: at.Add(7 * 24 * time.Hour), Revision: 1}, ParseJob: previewJob,
		AuditEventID: creationAudit.ID.String(), SourceAuditEventID: creationSourceAudit.ID.String(), AuditAt: model.MillisFromTime(at)})
	requireNoError(t, err)
	digest := strings.Repeat("c", sha256.Size*2)
	preview, err := ss.OnboardingImport().CompleteOnboardingImportPreview(ctx, &store.OnboardingImportPreviewCompletion{ID: importID, ExpectedRevision: created.Revision,
		Digest: digest, At: at.Add(time.Second), Rows: []store.OnboardingImportRow{{ImportID: importID, RowNumber: 1, Reference: source.Membership.ID.String(),
			Operation: "class.enroll", ScopeType: model.RoleScopeClass, ScopeID: destinationClass.ID.String(), TargetRevision: destinationClass.Revision,
			UserID: student.ID, RelationshipID: source.Membership.ID.String(), RelationshipRevision: source.Membership.Revision,
			StartsAt: model.MillisFromTime(effectiveAt), PreviewStatus: model.OnboardingImportRowValid, Status: model.OnboardingImportRowValid}}})
	requireNoError(t, err)
	executionJob, err := model.NewJob(model.NewJobID(), model.JobTypeOnboardingImportExecute, 1, json.RawMessage(`{"import_id":"`+importID.String()+`"}`), "progression-execute:"+importID.String(), at, at, 3)
	requireNoError(t, err)
	commitSession, commitCredentials, _ := saveSession(t, ctx, ss, actor.ID.String(), 10)
	commitPrincipal := model.Principal{UserID: actor.ID, SessionID: commitSession.ID, CredentialID: model.PrincipalCredentialID(commitCredentials[0].ID),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: commitSession.ClientType, AuthenticatedAt: commitSession.AuthenticatedAt}
	commitAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, destinationClass.ID.String(), "student_progression.commit")
	commitSourceAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, sourceClass.ID.String(), "student_progression.commit")
	executing, err := ss.OnboardingImport().CommitOnboardingImport(ctx, &store.OnboardingImportCommit{ID: importID, ActorUserID: actor.ID, Principal: commitPrincipal, ExpectedRevision: preview.Revision,
		PreviewDigest: digest, Policy: model.OnboardingImportValidRowsOnly, IdempotencyKey: sha256.Sum256([]byte("progression-commit")), ExecutionJob: executionJob,
		At: at.Add(2 * time.Second), AuditEventID: commitAudit.ID.String(), SourceAuditEventID: commitSourceAudit.ID.String(), AuditAt: model.MillisFromTime(at.Add(2 * time.Second))})
	requireNoError(t, err)
	if executing.Principal.CredentialID != commitPrincipal.CredentialID {
		t.Fatalf("execution Principal = %#v; want commit Principal %#v", executing.Principal, commitPrincipal)
	}
	candidate := &model.ClassMember{ClassID: destinationClass.ID, AcademicPeriodID: destinationPeriod.ID, UserID: student.ID, StartsAt: effectiveAt}
	candidate.PrepareCreate(model.NewClassMemberID(), at.Add(3*time.Second))
	notice := classMemberPreparedMail(t, candidate, model.MailTemplateAcademicClassEnrolled, candidate.CreatedAt)
	command := invitationTestCommand(actor.ID, "class_member.enroll.v1", "progression-row", "progression-row")
	command.OnboardingImportID, command.OnboardingImportRowNumber = importID, 1
	audit := saveClassMemberAuditAttempt(t, ctx, ss, destinationClass.ID.String())
	progressionDestinationAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, destinationClass.ID.String(), "student_progression.execute_row")
	progressionSourceAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, sourceClass.ID.String(), "student_progression.execute_row")
	progressed, err := ss.ClassMember().EnrollWithAudit(ctx, &store.ClassMemberEnrollment{Member: candidate, ExpectedRecipientRevision: student.Revision, Notice: notice,
		StudentProgression: true, ProgressionSourceAuditEventID: progressionSourceAudit.ID.String(), ProgressionDestinationAuditEventID: progressionDestinationAudit.ID.String(),
		AuditEventID: audit.ID.String(), AuditAt: model.MillisFromTime(at.Add(3 * time.Second)), Command: command})
	requireNoError(t, err)
	requireNoError(t, requireClassMemberMail(t, ctx, ss, notice, model.MailTemplateAcademicClassEnrolled))
	if progressed.Previous != nil {
		t.Fatalf("cross-period progression rewrote source: %#v", progressed)
	}
	history, err := ss.ClassMember().ListByUser(ctx, student.ID.String())
	requireNoError(t, err)
	if len(history) != 2 || history[1].ID != source.Membership.ID || history[1].EndsAt.Valid {
		t.Fatalf("progression history = %#v", history)
	}
	completed, err := ss.OnboardingImport().GetOnboardingImport(ctx, importID)
	requireNoError(t, err)
	if completed.State != model.OnboardingImportCompleted || completed.SucceededRows != 1 {
		t.Fatalf("completed progression = %#v", completed)
	}
	replayAudit := saveClassMemberAuditAttempt(t, ctx, ss, destinationClass.ID.String())
	replayProgressionDestinationAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, destinationClass.ID.String(), "student_progression.execute_row")
	replayProgressionSourceAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, sourceClass.ID.String(), "student_progression.execute_row")
	replay := &store.ClassMemberEnrollment{Member: candidate, ExpectedRecipientRevision: student.Revision, StudentProgression: true,
		ProgressionSourceAuditEventID: replayProgressionSourceAudit.ID.String(), ProgressionDestinationAuditEventID: replayProgressionDestinationAudit.ID.String(),
		AuditEventID: replayAudit.ID.String(), AuditAt: model.MillisFromTime(at.Add(4 * time.Second)), Command: command}
	if _, err = ss.ClassMember().EnrollWithAudit(ctx, replay); err != nil || !replay.Replayed {
		t.Fatalf("progression replay = %v replayed=%v", err, replay.Replayed)
	}
	afterReplay, err := ss.ClassMember().ListByUser(ctx, student.ID.String())
	requireNoError(t, err)
	if len(afterReplay) != 2 {
		t.Fatalf("progression replay duplicated history: %#v", afterReplay)
	}
	noOpCandidate := &model.ClassMember{ClassID: destinationClass.ID, AcademicPeriodID: destinationPeriod.ID, UserID: student.ID,
		StartsAt: effectiveAt.Add(time.Minute)}
	noOpCandidate.PrepareCreate(model.NewClassMemberID(), at.Add(5*time.Second))
	noOpCommand := invitationTestCommand(actor.ID, "class_member.enroll.v1", "progression-destination-no-op", "progression-destination-no-op")
	noOpAudit := saveClassMemberAuditAttempt(t, ctx, ss, destinationClass.ID.String())
	noOpDestinationAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, destinationClass.ID.String(), "student_progression.execute_row")
	noOpSourceAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, sourceClass.ID.String(), "student_progression.execute_row")
	noOpInput := &store.ClassMemberEnrollment{Member: noOpCandidate, ExpectedRecipientRevision: student.Revision, StudentProgression: true,
		ProgressionSourceAuditEventID: noOpSourceAudit.ID.String(), ProgressionDestinationAuditEventID: noOpDestinationAudit.ID.String(),
		AuditEventID: noOpAudit.ID.String(), AuditAt: model.MillisFromTime(at.Add(5 * time.Second)), Command: noOpCommand}
	noOpEnrollment, err := ss.ClassMember().EnrollWithAudit(ctx, noOpInput)
	requireNoError(t, err)
	if !noOpInput.NoOp || noOpEnrollment.Membership.ID != progressed.Membership.ID {
		t.Fatalf("existing progression destination = %#v no_op=%v", noOpEnrollment, noOpInput.NoOp)
	}
}

func testOnboardingImportAcademicAdministration(t *testing.T, ss store.Store) {
	t.Helper()
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	actor, target := saveUser(t, ctx, ss), saveUser(t, ctx, ss)
	at := model.NowUTC().Add(-time.Minute)
	authorityRole, err := ss.Role().Save(ctx, &model.Role{Name: "administration-import-authority-" + model.NewId(), DisplayName: "Administration Import Authority",
		Permissions: []string{string(model.ActionOnboardingBatchManage), string(model.ActionUserManage)}})
	requireNoError(t, err)
	authorityBinding, err := ss.RoleBinding().Save(ctx, &model.RoleBinding{UserID: actor.ID, RoleID: authorityRole.ID,
		ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(), StartsAt: at.Add(-time.Second)})
	requireNoError(t, err)
	session, credentials, _ := saveSession(t, ctx, ss, actor.ID.String(), 10)
	principal := model.Principal{UserID: actor.ID, SessionID: session.ID, CredentialID: model.PrincipalCredentialID(credentials[0].ID),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: session.ClientType, AuthenticatedAt: session.AuthenticatedAt}

	row := store.OnboardingImportRow{Operation: "affiliation.add", ScopeType: model.RoleScopeInstitution,
		ScopeID: institution.ID.String(), TargetRevision: institution.Revision, UserID: target.ID,
		AffiliationKind: model.AffiliationStaff, StartsAt: model.MillisFromTime(at)}
	importID := createExecutingOnboardingImport(t, ctx, ss, actor, principal, model.OnboardingImportAcademicAdministration,
		model.RoleScopeInstitution, institution.ID.String(), "", at, row)
	candidate := &model.Affiliation{UserID: target.ID, Kind: model.AffiliationStaff, StartsAt: at}
	candidate.PrepareCreate(model.NewAffiliationID(), at)
	audit := saveAffiliationAuditAttempt(t, ctx, ss, institution.ID.String(), target.ID.String())
	command := invitationTestCommand(actor.ID, "affiliation.add.v1", "administration-import-row", "administration-import-row")
	command.OnboardingImportID, command.OnboardingImportRowNumber = importID, 1
	created, err := ss.Affiliation().Create(ctx, &store.AffiliationCreation{Affiliation: candidate, AuditEventID: audit.ID.String(),
		AuditAt: model.MillisFromTime(at), Command: command})
	requireNoError(t, err)
	completed, err := ss.OnboardingImport().GetOnboardingImport(ctx, importID)
	requireNoError(t, err)
	page, err := ss.OnboardingImport().ListOnboardingImportRows(ctx, importID, 0, 10)
	requireNoError(t, err)
	if completed.State != model.OnboardingImportCompleted || completed.SucceededRows != 1 || len(page.Rows) != 1 ||
		page.Rows[0].Status != model.OnboardingImportRowSucceeded || page.Rows[0].ResourceID != created.ID.String() {
		t.Fatalf("academic administration import = %#v rows=%#v", completed, page.Rows)
	}

	duplicateTarget := saveUser(t, ctx, ss)
	duplicateCandidate := &model.Affiliation{UserID: duplicateTarget.ID, Kind: model.AffiliationStaff, StartsAt: at}
	duplicateCandidate.PrepareCreate(model.NewAffiliationID(), at)
	group := sha256.Sum256([]byte("affiliation-staff-" + duplicateTarget.ID.String()))
	authority := &store.CommandAuthorization{Principal: principal, ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(),
		Actions: []model.Action{model.ActionOnboardingBatchManage, model.ActionUserManage}}
	canonicalAudit := saveAffiliationAuditAttempt(t, ctx, ss, institution.ID.String(), duplicateTarget.ID.String())
	canonicalCommand := invitationTestCommand(actor.ID, "affiliation.add.v1", "administration-json-a", "administration-json-duplicate")
	canonicalCommand.Authorization, canonicalCommand.Batch = authority, &store.CommandBatch{GroupDigest: group}
	canonical, err := ss.Affiliation().Create(ctx, &store.AffiliationCreation{Affiliation: duplicateCandidate,
		AuditEventID: canonicalAudit.ID.String(), AuditAt: model.MillisFromTime(at), Command: canonicalCommand})
	requireNoError(t, err)

	loserCandidate := *duplicateCandidate
	loserCandidate.ID = model.NewAffiliationID()
	loserAudit := saveAffiliationAuditAttempt(t, ctx, ss, institution.ID.String(), duplicateTarget.ID.String())
	loserCommand := invitationTestCommand(actor.ID, "affiliation.add.v1", "administration-json-b", "administration-json-duplicate")
	loserCommand.Authorization, loserCommand.Batch = authority, &store.CommandBatch{GroupDigest: group, DuplicateOfKeyDigest: canonicalCommand.KeyDigest}
	duplicate, err := ss.Affiliation().Create(ctx, &store.AffiliationCreation{Affiliation: &loserCandidate,
		AuditEventID: loserAudit.ID.String(), AuditAt: model.MillisFromTime(at), Command: loserCommand})
	requireNoError(t, err)
	if duplicate.ID != canonical.ID || !loserCommand.Batch.Duplicate {
		t.Fatalf("duplicate outcome = %#v metadata=%#v, want canonical %s", duplicate, loserCommand.Batch, canonical.ID)
	}
	if _, err = ss.Affiliation().Get(ctx, loserCandidate.ID.String()); !store.IsNotFound(err) {
		t.Fatalf("duplicate candidate was persisted: %v", err)
	}
	loserReplayAudit := saveAffiliationAuditAttempt(t, ctx, ss, institution.ID.String(), duplicateTarget.ID.String())
	loserCommand.Batch.Duplicate = false
	if _, err = ss.Affiliation().Create(ctx, &store.AffiliationCreation{Affiliation: &loserCandidate,
		AuditEventID: loserReplayAudit.ID.String(), AuditAt: model.MillisFromTime(at), Command: loserCommand}); err != nil || !loserCommand.Batch.Duplicate {
		t.Fatalf("retained duplicate replay metadata=%#v error=%v", loserCommand.Batch, err)
	}

	secondTarget := saveUser(t, ctx, ss)
	secondRow := row
	secondRow.UserID = secondTarget.ID
	secondImportID := createExecutingOnboardingImport(t, ctx, ss, actor, principal, model.OnboardingImportAcademicAdministration,
		model.RoleScopeInstitution, institution.ID.String(), "", at.Add(time.Second), secondRow)
	_, err = ss.RoleBinding().End(ctx, authorityBinding.ID.String(), model.MillisFromTime(at.Add(2*time.Second)))
	requireNoError(t, err)
	secondCandidate := &model.Affiliation{UserID: secondTarget.ID, Kind: model.AffiliationStaff, StartsAt: at}
	secondCandidate.PrepareCreate(model.NewAffiliationID(), at)
	secondAudit := saveAffiliationAuditAttempt(t, ctx, ss, institution.ID.String(), secondTarget.ID.String())
	secondCommand := invitationTestCommand(actor.ID, "affiliation.add.v1", "administration-import-authority-row", "administration-import-authority-row")
	secondCommand.OnboardingImportID, secondCommand.OnboardingImportRowNumber = secondImportID, 1
	if _, err = ss.Affiliation().Create(ctx, &store.AffiliationCreation{Affiliation: secondCandidate, AuditEventID: secondAudit.ID.String(),
		AuditAt: model.MillisFromTime(at), Command: secondCommand}); !store.IsConflict(err) {
		t.Fatalf("academic administration import after authority ended error = %v", err)
	}
	if _, err = ss.Affiliation().Get(ctx, secondCandidate.ID.String()); !store.IsNotFound(err) {
		t.Fatalf("unauthorized import row mutated affiliation: %v", err)
	}
	jsonTarget := saveUser(t, ctx, ss)
	jsonCandidate := &model.Affiliation{UserID: jsonTarget.ID, Kind: model.AffiliationStaff, StartsAt: at}
	jsonCandidate.PrepareCreate(model.NewAffiliationID(), at)
	jsonAudit := saveAffiliationAuditAttempt(t, ctx, ss, institution.ID.String(), jsonTarget.ID.String())
	jsonCommand := invitationTestCommand(actor.ID, "affiliation.add.v1", "administration-json-revoked", "administration-json-revoked")
	jsonCommand.Authorization = &store.CommandAuthorization{Principal: principal, ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(),
		Actions: []model.Action{model.ActionOnboardingBatchManage, model.ActionUserManage}}
	jsonCommand.Batch = &store.CommandBatch{GroupDigest: sha256.Sum256([]byte("administration-json-revoked"))}
	if _, err = ss.Affiliation().Create(ctx, &store.AffiliationCreation{Affiliation: jsonCandidate, AuditEventID: jsonAudit.ID.String(),
		AuditAt: model.MillisFromTime(at), Command: jsonCommand}); !store.IsConflict(err) {
		t.Fatalf("JSON row after authority ended error = %v", err)
	}
	if _, err = ss.Affiliation().Get(ctx, jsonCandidate.ID.String()); !store.IsNotFound(err) {
		t.Fatalf("unauthorized JSON row mutated affiliation: %v", err)
	}
}

func testOnboardingImportTeacherAndRoleNoOps(t *testing.T, ss store.Store) {
	t.Helper()
	ctx := context.Background()
	unit, _ := saveProgrammeParents(t, ctx, ss, "onboarding-cross-kind-"+model.NewId())
	actor := saveUser(t, ctx, ss)
	at := model.NowUTC().Add(-time.Minute)
	targetRole, err := ss.Role().Save(ctx, &model.Role{Name: "onboarding-target-" + model.NewId(), DisplayName: "Onboarding Target",
		Permissions: []string{string(model.ActionAcademicUnitView)}})
	requireNoError(t, err)
	authorityRole, err := ss.Role().Save(ctx, &model.Role{Name: "onboarding-authority-" + model.NewId(), DisplayName: "Onboarding Authority",
		Permissions: []string{string(model.ActionOnboardingBatchManage), string(model.ActionInvitationCreate), string(model.ActionAcademicUnitMembersManage),
			string(model.ActionRoleBindingManage), string(model.ActionAcademicUnitView)}})
	requireNoError(t, err)
	_, err = ss.RoleBinding().Save(ctx, &model.RoleBinding{UserID: actor.ID, RoleID: authorityRole.ID, ScopeType: model.RoleScopeInstitution,
		ScopeID: unit.InstitutionID.String(), StartsAt: at.Add(-time.Second)})
	requireNoError(t, err)
	session, credentials, _ := saveSession(t, ctx, ss, actor.ID.String(), 10)
	principal := model.Principal{UserID: actor.ID, SessionID: session.ID, CredentialID: model.PrincipalCredentialID(credentials[0].ID),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: session.ClientType, AuthenticatedAt: session.AuthenticatedAt}

	teacherIssue := teacherAcademicUnitInvitationIssueFixture(t, ss, actor, unit, targetRole, at)
	teacherIssue.Invitation.TargetEmail = "onboarding-teacher-" + model.NewId() + "@example.edu"
	requireNoError(t, teacherIssue.Invitation.Validate())
	teacher, err := ss.Invitation().IssueTeacherAcademicUnit(ctx, teacherIssue)
	requireNoError(t, err)
	teacherImportID := createExecutingOnboardingImport(t, ctx, ss, actor, principal, model.OnboardingImportTeacherAcademicUnit,
		model.RoleScopeAcademicUnit, unit.ID.String(), targetRole.ID, at, store.OnboardingImportRow{Operation: "teacher_academic_unit.create",
			ScopeType: model.RoleScopeAcademicUnit, ScopeID: unit.ID.String(), TargetRevision: unit.Revision, RoleID: targetRole.ID,
			RoleRevision: targetRole.UpdatedAt.UnixMicro(), Email: teacher.TargetEmail, StartsAt: model.MillisFromTime(teacher.IntendedStartsAt)})
	teacherNoMail := teacherAcademicUnitInvitationIssueFixture(t, ss, actor, unit, targetRole, at.Add(time.Second))
	teacherNoMail.Invitation.TargetEmail, teacherNoMail.Invitation.IntendedStartsAt = teacher.TargetEmail, teacher.IntendedStartsAt
	requireNoError(t, teacherNoMail.Invitation.Validate())
	teacherNoMail.Occurrence, teacherNoMail.Delivery, teacherNoMail.DeliveryJob = nil, nil, nil
	teacherCommand := invitationTestCommand(actor.ID, "invitation.teacher_academic_unit.issue.v1", "onboarding-teacher-row", "onboarding-teacher-row")
	teacherCommand.OnboardingImportID, teacherCommand.OnboardingImportRowNumber = teacherImportID, 1
	teacherResult, err := ss.Invitation().IssueTeacherAcademicUnitIdempotently(ctx, teacherNoMail, teacherCommand)
	requireNoError(t, err)
	if !teacherResult.NoOp || teacherResult.Invitation == nil || teacherResult.Invitation.ID != teacher.ID {
		t.Fatalf("teacher import no-op = %#v", teacherResult)
	}

	roleCandidate, err := model.NewScopedRoleInvitation(model.ScopedRoleInvitationInput{ID: model.NewInvitationID(), Purpose: model.InvitationPurposeAcademicUnitRole,
		TargetEmail: "onboarding-role-" + model.NewId() + "@example.edu", AcademicUnitID: unit.ID, RoleID: targetRole.ID, RoleActions: targetRole.Permissions,
		IntendedStartsAt: at, InviterUserID: actor.ID, ScopeType: model.RoleScopeAcademicUnit, ScopeID: unit.ID.String(),
		ClaimHash: model.HashInvitationClaim(model.NewCredentialToken()), IssuedAt: at})
	requireNoError(t, err)
	roleInvitation, err := ss.Invitation().IssueScopedRole(ctx, scopedRoleInvitationIssueFixture(t, ss, roleCandidate))
	requireNoError(t, err)
	roleImportID := createExecutingOnboardingImport(t, ctx, ss, actor, principal, model.OnboardingImportInstitution,
		model.RoleScopeInstitution, unit.InstitutionID.String(), "", at, store.OnboardingImportRow{Operation: "academic_unit_role.create",
			ScopeType: model.RoleScopeAcademicUnit, ScopeID: unit.ID.String(), TargetRevision: unit.Revision, RoleID: targetRole.ID,
			RoleRevision: targetRole.UpdatedAt.UnixMicro(), Email: roleInvitation.TargetEmail, StartsAt: model.MillisFromTime(roleInvitation.IntendedStartsAt)})
	roleNoMail := scopedRoleInvitationIssueFixture(t, ss, roleCandidate)
	roleNoMail.Invitation.ID = model.NewInvitationID()
	roleNoMail.Invitation.ClaimHash = model.HashInvitationClaim(model.NewCredentialToken())
	requireNoError(t, roleNoMail.Invitation.Validate())
	roleNoMail.Occurrence, roleNoMail.Delivery, roleNoMail.DeliveryJob = nil, nil, nil
	roleCommand := invitationTestCommand(actor.ID, "invitation.academic_unit_role.issue.v1", "onboarding-role-row", "onboarding-role-row")
	roleCommand.OnboardingImportID, roleCommand.OnboardingImportRowNumber = roleImportID, 1
	roleResult, err := ss.Invitation().IssueScopedRoleIdempotently(ctx, roleNoMail, roleCommand)
	requireNoError(t, err)
	if !roleResult.NoOp || roleResult.Invitation == nil || roleResult.Invitation.ID != roleInvitation.ID {
		t.Fatalf("Role import no-op = %#v", roleResult)
	}
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
	parseJob, err := model.NewJob(model.NewJobID(), model.JobTypeOnboardingImportParse, 1, json.RawMessage(`{"import_id":"`+importID.String()+`"}`), "parse:"+importID.String(), at, at, 3)
	if err != nil {
		t.Fatal(err)
	}
	creationAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, scopeID, "onboarding_import.upload")
	created, err := ss.OnboardingImport().CreateOnboardingImport(ctx, &store.OnboardingImportCreation{Import: &store.OnboardingImport{ID: importID,
		Mode: model.OnboardingImportStudentClass, State: model.OnboardingImportParsing, ScopeType: model.RoleScopeClass, ScopeID: scopeID,
		ActorUserID: actor.ID, Principal: principal, ParseJobID: parseJob.ID, CreatedAt: at, UpdatedAt: at, ExpiresAt: at.Add(7 * 24 * time.Hour), Revision: 1}, ParseJob: parseJob,
		AuditEventID: creationAudit.ID.String(), AuditAt: model.MillisFromTime(at)})
	if err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("a", sha256.Size*2)
	preview, err := ss.OnboardingImport().CompleteOnboardingImportPreview(ctx, &store.OnboardingImportPreviewCompletion{ID: importID, ExpectedRevision: created.Revision,
		Digest: digest, IgnoredHeaders: []string{"misspelled"}, At: at.Add(time.Second), Rows: []store.OnboardingImportRow{{ImportID: importID, RowNumber: 1,
			Reference: "row-1", Operation: "student_class.create", ScopeType: model.RoleScopeClass, ScopeID: created.ScopeID, TargetRevision: 1,
			Email: "student@example.edu", PreviewStatus: model.OnboardingImportRowValid, Status: model.OnboardingImportRowValid}}})
	if err != nil {
		t.Fatal(err)
	}
	if preview.State != model.OnboardingImportPreviewReady || preview.ValidRows != 1 || preview.InvalidRows != 0 {
		t.Fatalf("preview = %#v", preview)
	}
	executionJob, err := model.NewJob(model.NewJobID(), model.JobTypeOnboardingImportExecute, 1, json.RawMessage(`{"import_id":"`+importID.String()+`"}`), "execute:"+importID.String(), at, at, 3)
	if err != nil {
		t.Fatal(err)
	}
	key := sha256.Sum256([]byte("commit-key"))
	commit := &store.OnboardingImportCommit{ID: importID, ActorUserID: actor.ID, Principal: principal, ExpectedRevision: preview.Revision, PreviewDigest: digest,
		Policy: model.OnboardingImportRequireAllValid, IdempotencyKey: key, ExecutionJob: executionJob, At: at.Add(2 * time.Second)}
	commitAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, created.ScopeID, "onboarding_import.commit")
	commit.AuditEventID, commit.AuditAt = commitAudit.ID.String(), model.MillisFromTime(at.Add(2*time.Second))
	executing, err := ss.OnboardingImport().CommitOnboardingImport(ctx, commit)
	if err != nil {
		t.Fatal(err)
	}
	if executing.State != model.OnboardingImportExecuting || executing.ExecutionJobID != executionJob.ID {
		t.Fatalf("executing = %#v", executing)
	}
	retryJob, _ := model.NewJob(model.NewJobID(), model.JobTypeOnboardingImportExecute, 1, json.RawMessage(`{"import_id":"`+importID.String()+`"}`), "retry:"+importID.String(), at, at, 3)
	commit.ExecutionJob = retryJob
	replayAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, created.ScopeID, "onboarding_import.commit")
	commit.AuditEventID, commit.AuditAt = replayAudit.ID.String(), model.MillisFromTime(at.Add(2*time.Second))
	replayed, err := ss.OnboardingImport().CommitOnboardingImport(ctx, commit)
	if err != nil || replayed.ExecutionJobID != executionJob.ID {
		t.Fatalf("replay = %#v, %v", replayed, err)
	}
	changed := *commit
	changed.IdempotencyKey = sha256.Sum256([]byte("changed"))
	changedAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, created.ScopeID, "onboarding_import.commit")
	changed.AuditEventID, changed.AuditAt = changedAudit.ID.String(), model.MillisFromTime(at.Add(2*time.Second))
	if _, err = ss.OnboardingImport().CommitOnboardingImport(ctx, &changed); !store.IsConflict(err) {
		t.Fatalf("changed key error = %v", err)
	}
	issue := studentClassInvitationIssueFixture(t, ss, actor, class, fixture.period, at)
	command := invitationTestCommand(actor.ID, "invitation.student_class.issue.v1", "onboarding-row-1", "onboarding-row-1")
	command.OnboardingImportID, command.OnboardingImportRowNumber = importID, 1
	issued, err := ss.Invitation().IssueStudentClassIdempotently(ctx, issue, command)
	if err != nil {
		t.Fatal(err)
	}
	invitationID := issued.Invitation.ID
	completed, err := ss.OnboardingImport().GetOnboardingImport(ctx, importID)
	requireNoError(t, err)
	if completed.State != model.OnboardingImportCompleted || completed.SucceededRows != 1 {
		t.Fatalf("completed = %#v", completed)
	}
	lateReplayAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, created.ScopeID, "onboarding_import.commit")
	commit.AuditEventID, commit.AuditAt = lateReplayAudit.ID.String(), model.MillisFromTime(at.Add(3*time.Second))
	lateReplay, err := ss.OnboardingImport().CommitOnboardingImport(ctx, commit)
	requireNoError(t, err)
	if lateReplay.ExecutionJobID != executionJob.ID || lateReplay.State != model.OnboardingImportExecuting ||
		lateReplay.Revision != preview.Revision+1 || lateReplay.SucceededRows != 0 || !lateReplay.UpdatedAt.Equal(commit.At) {
		t.Fatalf("late commit replay = %#v", lateReplay)
	}
	changedRevision := *commit
	changedRevision.ExpectedRevision++
	changedRevisionAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, created.ScopeID, "onboarding_import.commit")
	changedRevision.AuditEventID, changedRevision.AuditAt = changedRevisionAudit.ID.String(), model.MillisFromTime(at.Add(3*time.Second))
	if _, err = ss.OnboardingImport().CommitOnboardingImport(ctx, &changedRevision); !store.IsConflict(err) {
		t.Fatalf("changed-revision late replay error = %v", err)
	}
	page, err := ss.OnboardingImport().ListOnboardingImportRows(ctx, importID, 0, 10)
	if err != nil || len(page.Rows) != 1 || page.Rows[0].InvitationID != invitationID {
		t.Fatalf("rows = %#v, %v", page, err)
	}

	noOpID := createExecutingStudentOnboardingImport(t, ctx, ss, actor, principal, class, at.Add(-time.Minute), "student@example.edu")
	noOpIssue := studentClassInvitationIssueFixture(t, ss, actor, class, fixture.period, at)
	resolved, isNoOp, err := ss.Invitation().ResolveOnboardingInvitationNoOp(ctx, noOpIssue.Invitation)
	requireNoError(t, err)
	if !isNoOp || resolved == nil || resolved.ID != invitationID {
		t.Fatalf("pending-effect preflight = %#v, %t", resolved, isNoOp)
	}
	unusedDeliveryID := noOpIssue.Delivery.ID
	noOpIssue.Occurrence, noOpIssue.Delivery, noOpIssue.DeliveryJob = nil, nil, nil
	noOpCommand := invitationTestCommand(actor.ID, "invitation.student_class.issue.v1", "onboarding-row-no-op", "onboarding-row-no-op")
	noOpCommand.OnboardingImportID, noOpCommand.OnboardingImportRowNumber = noOpID, 1
	noOpResult, err := ss.Invitation().IssueStudentClassIdempotently(ctx, noOpIssue, noOpCommand)
	requireNoError(t, err)
	if !noOpResult.NoOp || noOpResult.Invitation == nil || noOpResult.Invitation.ID != invitationID {
		t.Fatalf("pending-effect no-op = %#v", noOpResult)
	}
	noOpImport, err := ss.OnboardingImport().GetOnboardingImport(ctx, noOpID)
	requireNoError(t, err)
	if noOpImport.State != model.OnboardingImportCompleted || noOpImport.NoOpRows != 1 {
		t.Fatalf("no-op import = %#v", noOpImport)
	}
	if _, err = ss.Mail().GetDelivery(ctx, unusedDeliveryID); !store.IsNotFound(err) {
		t.Fatalf("no-op delivery error = %v", err)
	}

	cancelRaceID := createExecutingStudentOnboardingImport(t, ctx, ss, actor, principal, class, at.Add(-2*time.Minute), "canceled-row@example.edu")
	cancelRaceAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, class.ID.String(), "onboarding_import.cancel")
	_, err = ss.OnboardingImport().CancelOnboardingImport(ctx, &store.OnboardingImportCancellation{ID: cancelRaceID, ActorUserID: actor.ID, Principal: principal,
		At: at, AuditEventID: cancelRaceAudit.ID.String(), AuditAt: model.MillisFromTime(at)})
	requireNoError(t, err)
	canceledIssue := studentClassInvitationIssueFixture(t, ss, actor, class, fixture.period, at)
	canceledIssue.Invitation.TargetEmail = "canceled-row@example.edu"
	requireNoError(t, canceledIssue.Invitation.Validate())
	canceledCommand := invitationTestCommand(actor.ID, "invitation.student_class.issue.v1", "onboarding-row-canceled", "onboarding-row-canceled")
	canceledCommand.OnboardingImportID, canceledCommand.OnboardingImportRowNumber = cancelRaceID, 1
	if _, err = ss.Invitation().IssueStudentClassIdempotently(ctx, canceledIssue, canceledCommand); !store.IsConflict(err) {
		t.Fatalf("canceled row issue error = %v", err)
	}
	if _, err = ss.Invitation().Get(ctx, canceledIssue.Invitation.ID); !store.IsNotFound(err) {
		t.Fatalf("canceled row Invitation error = %v", err)
	}

	emptyID := model.NewOnboardingImportID()
	emptyParseJob, err := model.NewJob(model.NewJobID(), model.JobTypeOnboardingImportParse, 1, json.RawMessage(`{"import_id":"`+emptyID.String()+`"}`), "parse:"+emptyID.String(), at, at, 3)
	requireNoError(t, err)
	emptyCreationAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, scopeID, "onboarding_import.upload")
	emptyCreated, err := ss.OnboardingImport().CreateOnboardingImport(ctx, &store.OnboardingImportCreation{Import: &store.OnboardingImport{ID: emptyID,
		Mode: model.OnboardingImportStudentClass, State: model.OnboardingImportParsing, ScopeType: model.RoleScopeClass, ScopeID: scopeID,
		ActorUserID: actor.ID, Principal: principal, ParseJobID: emptyParseJob.ID, CreatedAt: at, UpdatedAt: at, ExpiresAt: at.Add(7 * 24 * time.Hour), Revision: 1},
		ParseJob: emptyParseJob, AuditEventID: emptyCreationAudit.ID.String(), AuditAt: model.MillisFromTime(at)})
	requireNoError(t, err)
	emptyPreview, err := ss.OnboardingImport().CompleteOnboardingImportPreview(ctx, &store.OnboardingImportPreviewCompletion{ID: emptyID, ExpectedRevision: emptyCreated.Revision,
		Digest: strings.Repeat("b", sha256.Size*2), At: at.Add(time.Second), Rows: []store.OnboardingImportRow{{ImportID: emptyID, RowNumber: 1,
			Reference: "invalid-row", ScopeType: model.RoleScopeClass, ScopeID: scopeID, PreviewStatus: model.OnboardingImportRowInvalid, PreviewCode: "request.invalid", Status: model.OnboardingImportRowInvalid}}})
	requireNoError(t, err)
	emptyExecutionJob, err := model.NewJob(model.NewJobID(), model.JobTypeOnboardingImportExecute, 1, json.RawMessage(`{"import_id":"`+emptyID.String()+`"}`), "execute:"+emptyID.String(), at, at, 3)
	requireNoError(t, err)
	emptyCommitAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, scopeID, "onboarding_import.commit")
	emptyExecuting, err := ss.OnboardingImport().CommitOnboardingImport(ctx, &store.OnboardingImportCommit{ID: emptyID, ActorUserID: actor.ID, Principal: principal, ExpectedRevision: emptyPreview.Revision,
		PreviewDigest: emptyPreview.PreviewDigest, Policy: model.OnboardingImportValidRowsOnly, IdempotencyKey: sha256.Sum256([]byte("empty-commit")), ExecutionJob: emptyExecutionJob,
		At: at.Add(2 * time.Second), AuditEventID: emptyCommitAudit.ID.String(), AuditAt: model.MillisFromTime(at.Add(2 * time.Second))})
	requireNoError(t, err)
	emptyFinished, err := ss.OnboardingImport().FinishOnboardingImport(ctx, emptyID, at.Add(3*time.Second))
	requireNoError(t, err)
	if emptyExecuting.State != model.OnboardingImportExecuting || emptyFinished.State != model.OnboardingImportCompleted || emptyFinished.SkippedRows != 1 {
		t.Fatalf("all-invalid import = %#v / %#v", emptyExecuting, emptyFinished)
	}

	cancelID := model.NewOnboardingImportID()
	cancelJob, err := model.NewJob(model.NewJobID(), model.JobTypeOnboardingImportParse, 1, json.RawMessage(`{"import_id":"`+cancelID.String()+`"}`), "parse:"+cancelID.String(), at, at, 3)
	requireNoError(t, err)
	cancelCreationAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, scopeID, "onboarding_import.upload")
	_, err = ss.OnboardingImport().CreateOnboardingImport(ctx, &store.OnboardingImportCreation{Import: &store.OnboardingImport{ID: cancelID,
		Mode: model.OnboardingImportStudentClass, State: model.OnboardingImportParsing, ScopeType: model.RoleScopeClass, ScopeID: scopeID,
		ActorUserID: actor.ID, Principal: principal, ParseJobID: cancelJob.ID, CreatedAt: at, UpdatedAt: at, ExpiresAt: at.Add(7 * 24 * time.Hour), Revision: 1},
		ParseJob: cancelJob, AuditEventID: cancelCreationAudit.ID.String(), AuditAt: model.MillisFromTime(at)})
	requireNoError(t, err)
	cancelAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, scopeID, "onboarding_import.cancel")
	canceled, err := ss.OnboardingImport().CancelOnboardingImport(ctx, &store.OnboardingImportCancellation{ID: cancelID, ActorUserID: actor.ID, Principal: principal, At: at.Add(time.Second),
		AuditEventID: cancelAudit.ID.String(), AuditAt: model.MillisFromTime(at.Add(time.Second))})
	requireNoError(t, err)
	storedJob, err := ss.Job().Get(ctx, cancelJob.ID)
	requireNoError(t, err)
	if canceled.State != model.OnboardingImportCanceled || storedJob.Status != model.JobStatusCanceled {
		t.Fatalf("canceled import/job = %#v / %#v", canceled, storedJob)
	}

	oldAt := at.Add(-8 * 24 * time.Hour)
	expiredID := model.NewOnboardingImportID()
	expiredJob, err := model.NewJob(model.NewJobID(), model.JobTypeOnboardingImportParse, 1, json.RawMessage(`{"import_id":"`+expiredID.String()+`"}`), "parse:"+expiredID.String(), oldAt, oldAt, 3)
	requireNoError(t, err)
	expiredAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, scopeID, "onboarding_import.upload")
	_, err = ss.OnboardingImport().CreateOnboardingImport(ctx, &store.OnboardingImportCreation{Import: &store.OnboardingImport{ID: expiredID,
		Mode: model.OnboardingImportStudentClass, State: model.OnboardingImportParsing, ScopeType: model.RoleScopeClass, ScopeID: scopeID,
		ActorUserID: actor.ID, Principal: principal, ParseJobID: expiredJob.ID, CreatedAt: oldAt, UpdatedAt: oldAt, ExpiresAt: oldAt.Add(7 * 24 * time.Hour), Revision: 1},
		ParseJob: expiredJob, AuditEventID: expiredAudit.ID.String(), AuditAt: model.MillisFromTime(at)})
	requireNoError(t, err)
	expired, err := ss.OnboardingImport().ListExpiredOnboardingImports(ctx, 100, at)
	requireNoError(t, err)
	if !slices.Contains(expired, expiredID) {
		t.Fatalf("expired onboarding imports = %#v", expired)
	}
	purged, err := ss.OnboardingImport().PurgeOnboardingImport(ctx, expiredID, at)
	requireNoError(t, err)
	if !purged {
		t.Fatal("expired onboarding import was not purged")
	}
}

func createExecutingStudentOnboardingImport(t *testing.T, ctx context.Context, ss store.Store, actor *model.User, principal model.Principal,
	class *model.Class, at time.Time, email string,
) model.OnboardingImportID {
	t.Helper()
	id := model.NewOnboardingImportID()
	parseJob, err := model.NewJob(model.NewJobID(), model.JobTypeOnboardingImportParse, 1, json.RawMessage(`{"import_id":"`+id.String()+`"}`), "parse:"+id.String(), at, at, 3)
	requireNoError(t, err)
	creationAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, class.ID.String(), "onboarding_import.upload")
	created, err := ss.OnboardingImport().CreateOnboardingImport(ctx, &store.OnboardingImportCreation{Import: &store.OnboardingImport{ID: id,
		Mode: model.OnboardingImportStudentClass, State: model.OnboardingImportParsing, ScopeType: model.RoleScopeClass, ScopeID: class.ID.String(), ActorUserID: actor.ID, Principal: principal, ParseJobID: parseJob.ID, CreatedAt: at, UpdatedAt: at, ExpiresAt: at.Add(7 * 24 * time.Hour), Revision: 1}, ParseJob: parseJob,
		AuditEventID: creationAudit.ID.String(), AuditAt: model.MillisFromTime(at)})
	requireNoError(t, err)
	preview, err := ss.OnboardingImport().CompleteOnboardingImportPreview(ctx, &store.OnboardingImportPreviewCompletion{ID: id, ExpectedRevision: created.Revision,
		Digest: strings.Repeat("c", sha256.Size*2), At: at.Add(time.Second), Rows: []store.OnboardingImportRow{{ImportID: id, RowNumber: 1, Reference: "row-1",
			Operation: "student_class.create", ScopeType: model.RoleScopeClass, ScopeID: class.ID.String(), TargetRevision: class.Revision, Email: email,
			PreviewStatus: model.OnboardingImportRowValid, Status: model.OnboardingImportRowValid}}})
	requireNoError(t, err)
	executionJob, err := model.NewJob(model.NewJobID(), model.JobTypeOnboardingImportExecute, 1, json.RawMessage(`{"import_id":"`+id.String()+`"}`), "execute:"+id.String(), at, at, 3)
	requireNoError(t, err)
	commitAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, model.RoleScopeClass, class.ID.String(), "onboarding_import.commit")
	_, err = ss.OnboardingImport().CommitOnboardingImport(ctx, &store.OnboardingImportCommit{ID: id, ActorUserID: actor.ID, Principal: principal, ExpectedRevision: preview.Revision,
		PreviewDigest: preview.PreviewDigest, Policy: model.OnboardingImportRequireAllValid, IdempotencyKey: sha256.Sum256([]byte("commit:" + id.String())), ExecutionJob: executionJob,
		At: at.Add(2 * time.Second), AuditEventID: commitAudit.ID.String(), AuditAt: model.MillisFromTime(at.Add(2 * time.Second))})
	requireNoError(t, err)
	return id
}

func createExecutingOnboardingImport(t *testing.T, ctx context.Context, ss store.Store, actor *model.User, principal model.Principal,
	mode model.OnboardingImportMode, scopeType model.RoleScopeType, scopeID string, importRoleID model.RoleID, at time.Time, row store.OnboardingImportRow,
) model.OnboardingImportID {
	t.Helper()
	id := model.NewOnboardingImportID()
	parseJob, err := model.NewJob(model.NewJobID(), model.JobTypeOnboardingImportParse, 1, json.RawMessage(`{"import_id":"`+id.String()+`"}`), "parse:"+id.String(), at, at, 3)
	requireNoError(t, err)
	creationAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, scopeType, scopeID, "onboarding_import.upload")
	created, err := ss.OnboardingImport().CreateOnboardingImport(ctx, &store.OnboardingImportCreation{Import: &store.OnboardingImport{ID: id,
		Mode: mode, State: model.OnboardingImportParsing, ScopeType: scopeType, ScopeID: scopeID, RoleID: importRoleID, ActorUserID: actor.ID, Principal: principal, ParseJobID: parseJob.ID, CreatedAt: at, UpdatedAt: at, ExpiresAt: at.Add(7 * 24 * time.Hour), Revision: 1}, ParseJob: parseJob,
		AuditEventID: creationAudit.ID.String(), AuditAt: model.MillisFromTime(at)})
	requireNoError(t, err)
	row.ImportID, row.RowNumber, row.Reference = id, 1, "row-1"
	row.PreviewStatus, row.Status, row.UpdatedAt = model.OnboardingImportRowValid, model.OnboardingImportRowValid, at.Add(time.Second)
	preview, err := ss.OnboardingImport().CompleteOnboardingImportPreview(ctx, &store.OnboardingImportPreviewCompletion{ID: id, ExpectedRevision: created.Revision,
		Digest: strings.Repeat("e", sha256.Size*2), At: at.Add(time.Second), Rows: []store.OnboardingImportRow{row}})
	requireNoError(t, err)
	executionJob, err := model.NewJob(model.NewJobID(), model.JobTypeOnboardingImportExecute, 1, json.RawMessage(`{"import_id":"`+id.String()+`"}`), "execute:"+id.String(), at, at, 3)
	requireNoError(t, err)
	commitAudit := saveOnboardingImportAuditAttempt(t, ctx, ss, actor.ID, scopeType, scopeID, "onboarding_import.commit")
	_, err = ss.OnboardingImport().CommitOnboardingImport(ctx, &store.OnboardingImportCommit{ID: id, ActorUserID: actor.ID, Principal: principal, ExpectedRevision: preview.Revision,
		PreviewDigest: preview.PreviewDigest, Policy: model.OnboardingImportRequireAllValid, IdempotencyKey: sha256.Sum256([]byte("commit:" + id.String())), ExecutionJob: executionJob,
		At: at.Add(2 * time.Second), AuditEventID: commitAudit.ID.String(), AuditAt: model.MillisFromTime(at.Add(2 * time.Second))})
	requireNoError(t, err)
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
	requireNoError(t, err)
	return audit
}

func testInvitationBatchCommandIdempotency(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture, class, inviter, issuedAt := invitationAdministrationFixture(t, ctx, ss, "batch-idempotency")
	issue := studentClassInvitationIssueFixture(t, ss, inviter, class, fixture.period, issuedAt)
	issue.Invitation.TargetEmail = "batch-" + model.NewId() + "@example.edu"
	requireNoError(t, issue.Invitation.Validate())
	issueCommand := invitationTestCommand(inviter.ID, "invitation.student_class.issue.v1", "batch-row-0", "same-issue")
	created, err := ss.Invitation().IssueStudentClassIdempotently(ctx, issue, issueCommand)
	requireNoError(t, err)
	if created.Replayed || created.Invitation.ID != issue.Invitation.ID {
		t.Fatalf("fresh idempotent Invitation issue = %#v", created)
	}

	replayIssue := studentClassInvitationIssueFixture(t, ss, inviter, class, fixture.period, issuedAt.Add(time.Second))
	replayIssue.Invitation.TargetEmail = issue.Invitation.TargetEmail
	requireNoError(t, replayIssue.Invitation.Validate())
	replayed, err := ss.Invitation().IssueStudentClassIdempotently(ctx, replayIssue, issueCommand)
	requireNoError(t, err)
	if !replayed.Replayed || replayed.Invitation.ID != created.Invitation.ID {
		t.Fatalf("replayed idempotent Invitation issue = %#v", replayed)
	}
	if _, err = ss.Mail().GetDelivery(ctx, replayIssue.Delivery.ID); !store.IsNotFound(err) {
		t.Fatalf("replayed issue inserted duplicate delivery: %v", err)
	}
	replayAudit, err := ss.Audit().Get(ctx, replayIssue.AuditEventID)
	requireNoError(t, err)
	if replayAudit.Status != model.AuditStatusSuccess || !strings.Contains(string(replayAudit.Result), "idempotency_replayed") ||
		strings.Contains(string(replayAudit.Result), issue.Invitation.TargetEmail) {
		t.Fatalf("replayed issue audit = %#v", replayAudit)
	}
	conflicting := invitationTestCommand(inviter.ID, issueCommand.Operation, "batch-row-0", "changed-issue")
	if _, err = ss.Invitation().IssueStudentClassIdempotently(ctx, replayIssue, conflicting); err == nil {
		t.Fatal("conflicting Invitation issue idempotency reuse succeeded")
	} else {
		var conflict *store.ErrIdempotencyConflict
		if !errors.As(err, &conflict) {
			t.Fatalf("conflicting Invitation issue error = %v", err)
		}
	}

	newHash := model.HashInvitationClaim(model.NewCredentialToken())
	occurrence, delivery, job := invitationLifecycleMailFixture(t, created.Invitation.ID, inviter.ID,
		model.MailTemplateAccessStudentClassInvitation, model.JobTypeMailDeliverCredential, model.NowUTC(), created.Invitation.ExpiresAt)
	resendAudit := saveInvitationLifecycleAuditAttempt(t, ctx, ss, inviter.ID, class.ID.String(), "invitation.resend")
	resend := &store.InvitationResend{ID: created.Invitation.ID, ExpectedRevision: created.Invitation.Revision,
		ClaimHash: newHash, Occurrence: occurrence, Delivery: delivery, DeliveryJob: job, ActorUserID: inviter.ID,
		AuditEventID: resendAudit.ID.String(), AuditAt: model.MillisFromTime(model.NowUTC())}
	resendCommand := invitationTestCommand(inviter.ID, "invitation.resend.v1", "batch-row-1", "same-resend")
	resent, err := ss.Invitation().ResendIdempotently(ctx, resend, resendCommand)
	requireNoError(t, err)
	if resent.Replayed || resent.Record.Invitation.Revision != created.Invitation.Revision+1 {
		t.Fatalf("fresh idempotent Invitation resend = %#v", resent)
	}

	replayOccurrence, replayDelivery, replayJob := invitationLifecycleMailFixture(t, created.Invitation.ID, inviter.ID,
		model.MailTemplateAccessStudentClassInvitation, model.JobTypeMailDeliverCredential, model.NowUTC(), created.Invitation.ExpiresAt)
	replayResendAudit := saveInvitationLifecycleAuditAttempt(t, ctx, ss, inviter.ID, class.ID.String(), "invitation.resend")
	replayResend := &store.InvitationResend{ID: created.Invitation.ID, ExpectedRevision: created.Invitation.Revision,
		ClaimHash: model.HashInvitationClaim(model.NewCredentialToken()), Occurrence: replayOccurrence, Delivery: replayDelivery,
		DeliveryJob: replayJob, ActorUserID: inviter.ID, AuditEventID: replayResendAudit.ID.String(), AuditAt: model.MillisFromTime(model.NowUTC())}
	replayedResend, err := ss.Invitation().ResendIdempotently(ctx, replayResend, resendCommand)
	requireNoError(t, err)
	if !replayedResend.Replayed || replayedResend.Record.Invitation.Revision != resent.Record.Invitation.Revision {
		t.Fatalf("replayed idempotent Invitation resend = %#v", replayedResend)
	}
	if _, err = ss.Mail().GetDelivery(ctx, replayDelivery.ID); !store.IsNotFound(err) {
		t.Fatalf("replayed resend inserted duplicate delivery: %v", err)
	}

	duplicateAudit := saveInvitationLifecycleAuditAttempt(t, ctx, ss, inviter.ID, class.ID.String(), "onboarding_batch.duplicate")
	duplicateCommand := invitationTestCommand(inviter.ID, resendCommand.Operation, "batch-row-2", "duplicate-resend")
	duplicate, err := ss.Invitation().RecordBatchDuplicate(ctx, &store.InvitationBatchDuplicate{
		LifecycleID: created.Invitation.ID, ExpectedRevision: created.Invitation.Revision, ActorUserID: inviter.ID,
		CanonicalOperation: resendCommand.Operation, CanonicalKeyDigest: resendCommand.KeyDigest, CanonicalFingerprint: resendCommand.Fingerprint,
		AuditEventID: duplicateAudit.ID.String(), AuditAt: model.GetMillis(),
	}, duplicateCommand)
	requireNoError(t, err)
	if !duplicate.Duplicate || duplicate.Replayed {
		t.Fatalf("fresh lifecycle duplicate after canonical resend = %#v", duplicate)
	}

	staleAudit := saveInvitationLifecycleAuditAttempt(t, ctx, ss, inviter.ID, class.ID.String(), "onboarding_batch.duplicate")
	staleCommand := invitationTestCommand(inviter.ID, resendCommand.Operation, "batch-row-3", "stale-resend")
	_, err = ss.Invitation().RecordBatchDuplicate(ctx, &store.InvitationBatchDuplicate{
		LifecycleID: created.Invitation.ID, ExpectedRevision: created.Invitation.Revision, ActorUserID: inviter.ID,
		CanonicalOperation: resendCommand.Operation, CanonicalKeyDigest: resendCommand.KeyDigest,
		CanonicalFingerprint: sha256.Sum256([]byte("missing-canonical-fingerprint")),
		AuditEventID:         staleAudit.ID.String(), AuditAt: model.GetMillis(),
	}, staleCommand)
	if !store.IsConflict(err) {
		t.Fatalf("stale lifecycle duplicate without canonical outcome error = %v", err)
	}
}

func testInvitationBatchDuplicateRechecksCurrentInviterAuthority(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture, class, inviter, _, binding, issuedAt := invitationAcceptanceStoreFixture(t, ctx, ss, "batch-duplicate-authority")
	candidate := studentClassInvitationIssueFixture(t, ss, inviter, class, fixture.period, issuedAt).Invitation
	candidate.TargetEmail = "duplicate-authority-" + model.NewId() + "@example.edu"
	requireNoError(t, candidate.Validate())
	endedAt := model.GetMillis() - 1_000
	if _, err := ss.RoleBinding().End(ctx, binding.ID.String(), endedAt); err != nil {
		t.Fatalf("end inviter authority: %v", err)
	}
	attempt := saveInvitationLifecycleAuditAttempt(t, ctx, ss, inviter.ID, class.ID.String(), "onboarding_batch.duplicate")
	command := invitationTestCommand(inviter.ID, "invitation.student_class.issue.v1", "duplicate-row", "duplicate-package")
	_, err := ss.Invitation().RecordBatchDuplicate(ctx, &store.InvitationBatchDuplicate{
		Candidate: candidate, ActorUserID: inviter.ID,
		CanonicalOperation: command.Operation, CanonicalKeyDigest: sha256.Sum256([]byte("canonical-row")),
		AuditEventID: attempt.ID.String(), AuditAt: endedAt + 1,
	}, command)
	if !store.IsConflict(err) {
		t.Fatalf("RecordBatchDuplicate() after inviter authority ended error = %v", err)
	}
	current, getErr := ss.Audit().Get(ctx, attempt.ID.String())
	requireNoError(t, getErr)
	if current.Status != model.AuditStatusAttempt {
		t.Fatalf("failed duplicate authority audit status = %q", current.Status)
	}
}

func invitationTestCommand(userID model.UserID, operation, key, fingerprint string) *store.CommandIdempotency {
	return &store.CommandIdempotency{UserID: userID, Operation: operation, KeyDigest: sha256.Sum256([]byte(key)),
		FingerprintVersion: 1, Fingerprint: sha256.Sum256([]byte(fingerprint)), OutcomeVersion: 1,
		Retention: 24 * time.Hour, Wait: 2 * time.Second}
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
		Permissions: []string{string(model.ActionInvitationCreate), string(model.ActionInvitationManage), string(model.ActionClassMembersManage), string(model.ActionOnboardingBatchManage)}})
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
	handle, proof := model.NewCredentialToken(), model.NewCredentialToken()
	transaction, err := ss.BrowserAuthentication().CreateInvitation(ctx, &store.BrowserInvitationTransactionCreation{
		ID: model.NewBrowserAuthenticationTransactionID(), InstitutionID: fixture.institution.ID,
		Issuer: "https://proctor.example.edu", InvitationID: invitation.ID,
		InvitationPurpose: invitation.Purpose, InvitationClaimHash: invitation.ClaimHash,
		HandleHash: model.HashToken(handle), BrowserProofHash: model.HashToken(proof),
	})
	if err != nil {
		t.Fatalf("create browser Invitation transaction: %v", err)
	}
	if transaction.ID.IsZero() || transaction.ExpiresAt.After(invitation.ExpiresAt) {
		t.Fatalf("browser Invitation transaction = %#v", transaction)
	}
	acceptance.BrowserTransaction = &store.BrowserInvitationTransactionProof{
		ID: transaction.ID, HandleHash: model.HashToken(handle), BrowserProofHash: model.HashToken(proof),
	}
	otherIssue := studentClassInvitationIssueFixture(t, ss, inviter, class, fixture.period, model.NowUTC())
	otherIssue.Invitation.TargetEmail = "cross-proof-" + model.NewId() + "@example.edu"
	otherIssue.Invitation.Suggestions.Username = "cross-proof-" + model.NewId()
	otherInvitation, err := ss.Invitation().IssueStudentClass(ctx, otherIssue)
	requireNoError(t, err)
	otherAcceptance := studentClassInvitationAcceptanceFixture(t, otherInvitation, model.NowUTC())
	otherHandle, otherProof := model.NewCredentialToken(), model.NewCredentialToken()
	otherTransaction, err := ss.BrowserAuthentication().CreateInvitation(ctx, &store.BrowserInvitationTransactionCreation{
		ID: model.NewBrowserAuthenticationTransactionID(), InstitutionID: fixture.institution.ID,
		Issuer: "https://proctor.example.edu", InvitationID: otherInvitation.ID,
		InvitationPurpose: otherInvitation.Purpose, InvitationClaimHash: otherInvitation.ClaimHash,
		HandleHash: model.HashToken(otherHandle), BrowserProofHash: model.HashToken(otherProof),
	})
	requireNoError(t, err)
	otherAcceptance.BrowserTransaction = &store.BrowserInvitationTransactionProof{ID: otherTransaction.ID,
		HandleHash: model.HashToken(otherHandle), BrowserProofHash: model.HashToken(otherProof)}
	crossInvitation := *acceptance
	crossProof := *otherAcceptance.BrowserTransaction
	crossInvitation.BrowserTransaction = &crossProof
	if _, err = ss.Invitation().AcceptStudentClass(ctx, &crossInvitation); err == nil {
		t.Fatal("AcceptStudentClass accepted another Invitation's valid browser proof")
	}
	for _, candidate := range []struct {
		invitation *model.Invitation
		handle     string
		proof      string
	}{{invitation, handle, proof}, {otherInvitation, otherHandle, otherProof}} {
		pending, getErr := ss.Invitation().Get(ctx, candidate.invitation.ID)
		requireNoError(t, getErr)
		if pending.State != model.InvitationPending {
			t.Fatalf("cross-Invitation proof changed Invitation %s to %q", pending.ID, pending.State)
		}
		if _, resolveErr := ss.BrowserAuthentication().ResolveInvitation(ctx,
			model.HashToken(candidate.handle), model.HashToken(candidate.proof)); resolveErr != nil {
			t.Fatalf("cross-Invitation rejection consumed transaction for %s: %v", pending.ID, resolveErr)
		}
	}
	rejected := *acceptance
	rejectedProof := *acceptance.BrowserTransaction
	rejectedProof.BrowserProofHash = model.HashToken(model.NewCredentialToken())
	rejected.BrowserTransaction = &rejectedProof
	if _, err = ss.Invitation().AcceptStudentClass(ctx, &rejected); err == nil {
		t.Fatal("AcceptStudentClass accepted a mismatched browser proof")
	}
	stillPending, err := ss.Invitation().Get(ctx, invitation.ID)
	requireNoError(t, err)
	if stillPending.State != model.InvitationPending {
		t.Fatalf("mismatched browser proof consumed Invitation: %#v", stillPending)
	}
	if _, err = ss.BrowserAuthentication().ResolveInvitation(ctx, model.HashToken(handle), model.HashToken(proof)); err != nil {
		t.Fatalf("failed acceptance transaction consumed the valid browser Invitation proof: %v", err)
	}
	accepted, err := ss.Invitation().AcceptStudentClass(ctx, acceptance)
	requireNoError(t, err)
	if accepted.User.ID != existing.ID || !accepted.User.EmailVerified || accepted.ClassMember.UserID != existing.ID || accepted.Affiliation.UserID != existing.ID {
		t.Fatalf("existing User acceptance = %#v", accepted)
	}
	if _, err = ss.BrowserAuthentication().ResolveInvitation(ctx, model.HashToken(handle), model.HashToken(proof)); !store.IsNotFound(err) {
		t.Fatalf("resolved consumed browser Invitation transaction: %v", err)
	}
	otherAccepted, err := ss.Invitation().AcceptStudentClass(ctx, otherAcceptance)
	requireNoError(t, err)
	if otherAccepted.Invitation.ID != otherInvitation.ID || otherAccepted.Invitation.State != model.InvitationAccepted {
		t.Fatalf("other Invitation was not usable after cross-proof rejection: %#v", otherAccepted)
	}
	if _, err = ss.BrowserAuthentication().CreateInvitation(ctx, &store.BrowserInvitationTransactionCreation{
		ID: model.NewBrowserAuthenticationTransactionID(), InstitutionID: fixture.institution.ID,
		Issuer: "https://proctor.example.edu", InvitationID: invitation.ID,
		InvitationPurpose: invitation.Purpose, InvitationClaimHash: invitation.ClaimHash,
		HandleHash: model.HashToken(model.NewCredentialToken()), BrowserProofHash: model.HashToken(model.NewCredentialToken()),
	}); !store.IsConflict(err) {
		t.Fatalf("created browser transaction for accepted Invitation: %v", err)
	}
	replayed, err := ss.Invitation().AcceptStudentClass(ctx, acceptance)
	requireNoError(t, err)
	if !replayed.Replayed || replayed.User.ID != existing.ID {
		t.Fatalf("browser Invitation acceptance replay = %#v", replayed)
	}
	if _, err = ss.PasswordCredential().GetByUser(ctx, existing.ID.String()); err != nil {
		t.Fatalf("existing User password credential: %v", err)
	}
	if _, err = ss.Job().Get(ctx, acceptance.DefaultProfilePictureJob.ID); !store.IsNotFound(err) {
		t.Fatalf("existing User unexpectedly enqueued a second default-picture Job: %v", err)
	}
}

func testBrowserInvitationTransactionCreation(t *testing.T, ss store.Store, probe InvitationSQLProbe) {
	t.Helper()
	ctx := context.Background()
	fixture, class, inviter, role, _, issuedAt := invitationAcceptanceStoreFixture(t, ctx, ss, "browser-handoff-bounds")

	issueInvitation := func(t *testing.T, targetClass *model.Class, period *model.AcademicPeriod, at time.Time, intendedEnd model.OptionalTime) *model.Invitation {
		t.Helper()
		input := studentClassInvitationIssueFixture(t, ss, inviter, targetClass, period, at)
		unique := strings.ToLower(model.NewId())
		input.Invitation.TargetEmail = "browser-" + unique + "@example.edu"
		input.Invitation.Suggestions.Username = "browser-" + unique
		input.Invitation.IntendedEndsAt = intendedEnd
		created, err := ss.Invitation().IssueStudentClass(ctx, input)
		requireNoError(t, err)
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
		if probe.BrowserTransactionExists != nil && probe.BrowserTransactionExists(t, ctx, input.ID) {
			t.Fatalf("rejected browser Invitation transaction %s was inserted", input.ID)
		}
		if _, err := ss.BrowserAuthentication().ResolveInvitation(ctx, handleHash, proofHash); !store.IsNotFound(err) {
			t.Fatalf("ResolveInvitation() after rejected creation = %v, want not found", err)
		}
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
				if _, createErr := ss.BrowserAuthentication().CreateInvitation(ctx, input); !isInvalidInput(createErr) {
					t.Fatalf("CreateInvitation() error = %v, want invalid input", createErr)
				}
			})
		}
	})

	t.Run("FiveMinuteLifetime", func(t *testing.T) {
		invitation := issueInvitation(t, class, fixture.period, issuedAt, model.OptionalTimeFrom(fixture.period.EndsAt))
		input, _, _ := newCreation(invitation)
		before := model.NowUTC()
		transaction, err := ss.BrowserAuthentication().CreateInvitation(ctx, input)
		after := model.NowUTC()
		requireNoError(t, err)
		if transaction.ExpiresAt.Before(before.Add(model.BrowserAuthenticationTransactionLifetime-time.Second)) ||
			transaction.ExpiresAt.After(after.Add(model.BrowserAuthenticationTransactionLifetime+time.Second)) {
			t.Fatalf("browser Invitation transaction expiry = %s, call window = %s..%s", transaction.ExpiresAt, before, after)
		}
	})

	t.Run("InvitationExpiryBoundsLifetime", func(t *testing.T) {
		expiresAt := model.NowUTC().Add(2 * time.Minute)
		invitation := issueInvitation(t, class, fixture.period, expiresAt.Add(-model.InvitationLifetime), model.OptionalTimeFrom(fixture.period.EndsAt))
		input, _, _ := newCreation(invitation)
		transaction, err := ss.BrowserAuthentication().CreateInvitation(ctx, input)
		requireNoError(t, err)
		if !transaction.ExpiresAt.Equal(invitation.ExpiresAt) {
			t.Fatalf("browser Invitation deadline = %s, want Invitation expiry %s", transaction.ExpiresAt, invitation.ExpiresAt)
		}
	})

	activePeriod := saveAcademicPeriod(t, ctx, ss, fixture.institution.ID.String(), "browser-handoff-"+model.NewId(), model.MillisFromTime(model.NowUTC().Add(-time.Hour)))
	activeClass := saveClass(t, ctx, ss, fixture.level.ID.String(), activePeriod.ID.String(), "browser-handoff-"+model.NewId())
	_, err := ss.RoleBinding().Save(ctx, &model.RoleBinding{
		UserID: inviter.ID, RoleID: role.ID, ScopeType: model.RoleScopeClass, ScopeID: activeClass.ID.String(),
		StartsAt: activePeriod.StartsAt,
	})
	requireNoError(t, err)

	t.Run("IntendedEndBoundsLifetime", func(t *testing.T) {
		intendedEnd := model.NowUTC().Add(2 * time.Minute)
		invitation := issueInvitation(t, activeClass, activePeriod, model.NowUTC().Add(-time.Minute), model.OptionalTimeFrom(intendedEnd))
		input, _, _ := newCreation(invitation)
		transaction, err := ss.BrowserAuthentication().CreateInvitation(ctx, input)
		requireNoError(t, err)
		if !transaction.ExpiresAt.Equal(invitation.IntendedEndsAt.Time) {
			t.Fatalf("browser Invitation deadline = %s, want intended end %s", transaction.ExpiresAt, invitation.IntendedEndsAt.Time)
		}
	})

	t.Run("RejectsPurposeMismatchWithoutInsert", func(t *testing.T) {
		invitation := issueInvitation(t, class, fixture.period, issuedAt, model.OptionalTimeFrom(fixture.period.EndsAt))
		input, handleHash, proofHash := newCreation(invitation)
		input.InvitationPurpose = model.InvitationPurposeTeacherAcademicUnit
		if _, err := ss.BrowserAuthentication().CreateInvitation(ctx, input); !store.IsNotFound(err) {
			t.Fatalf("CreateInvitation() purpose mismatch = %v, want not found", err)
		}
		assertNotInserted(t, input, handleHash, proofHash)
	})

	t.Run("RejectsClaimMismatchWithoutInsert", func(t *testing.T) {
		invitation := issueInvitation(t, class, fixture.period, issuedAt, model.OptionalTimeFrom(fixture.period.EndsAt))
		input, handleHash, proofHash := newCreation(invitation)
		input.InvitationClaimHash = model.HashInvitationClaim(model.NewCredentialToken())
		if _, err := ss.BrowserAuthentication().CreateInvitation(ctx, input); !store.IsNotFound(err) {
			t.Fatalf("CreateInvitation() claim mismatch = %v, want not found", err)
		}
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
		if _, err := ss.BrowserAuthentication().CreateInvitation(ctx, input); !store.IsConflict(err) {
			t.Fatalf("CreateInvitation() expired Invitation = %v, want conflict", err)
		}
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
		if _, err := ss.BrowserAuthentication().CreateInvitation(ctx, input); !store.IsConflict(err) {
			t.Fatalf("CreateInvitation() ended Invitation = %v, want conflict", err)
		}
		assertNotInserted(t, input, handleHash, proofHash)
	})
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
