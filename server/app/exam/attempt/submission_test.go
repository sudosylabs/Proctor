// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package attempt

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestSubmitSealsExactAcknowledgedStateBeforePublishingEffects(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	credential := model.NewCredentialToken()
	participationID := model.NewAttemptParticipationID()
	workspaceID := model.NewExamAttemptWorkspaceID()
	revisionID := model.NewExamRevisionID()
	submissionID := model.NewSubmissionID()
	f.submissionID = submissionID
	digest := strings.Repeat("d", 64)
	f.submissions.target = &store.ExamSubmissionSealTarget{ExamID: f.sitting.ExamID, SittingID: f.sitting.ID,
		ClassID: f.sitting.ClassID, CandidateUserID: f.userID, WorkspaceID: workspaceID, CurrentRevisionID: revisionID}
	f.submissions.target.SealAt = f.at
	f.submissions.sealResult = &store.ExamSubmissionSealResult{Receipt: store.ExamSubmissionReceipt{BrowserActivity: model.BrowserSubmissionSettlement{State: "not_applicable", InventoryRevision: 1},
		SubmissionID: submissionID, AttemptID: f.attemptID, ExamRevisionID: revisionID, State: model.ExamAttemptSubmitted,
		WorkspaceCursor: 11, ManifestDigest: digest, SubmittedAt: f.at}, ExamID: f.sitting.ExamID,
		SittingID: f.sitting.ID, ClassID: f.sitting.ClassID, CandidateUserID: f.userID,
		ParticipationID: participationID, Generation: 3, ConnectionID: f.connectionID}

	result, err := f.service.SubmitExamAttempt(context.Background(), f.invocation, SubmitCommand{
		Access: WorkspaceMutationAccess{CandidateAccess: CandidateAccess{AttemptID: f.attemptID,
			ConnectionID: f.connectionID, ContinuityCredential: credential}, ParticipationID: participationID, Generation: 3},
		ExpectedCurrentRevisionID: revisionID, ExpectedWorkspaceCursor: 11, FinalFocusLossSequence: 7,
		IdempotencyKey: "test-key",
	})
	require.NoError(t, err)
	principal := f.invocation.Principal()
	access := f.submissions.resolvedAccess
	require.Equal(t, f.attemptID, access.AttemptID, "access=%#v seal=%#v result=%#v effects=%#v", access, f.submissions.seal, result, f.effects)
	require.Equal(t, participationID, access.ParticipationID, "access=%#v seal=%#v result=%#v effects=%#v", access, f.submissions.seal, result, f.effects)
	require.Equal(t, int64(3), access.Generation, "access=%#v seal=%#v result=%#v effects=%#v", access, f.submissions.seal, result, f.effects)
	require.Equal(t, f.connectionID, access.ConnectionID, "access=%#v seal=%#v result=%#v effects=%#v", access, f.submissions.seal, result, f.effects)
	require.Equal(t, principal.UserID, access.CandidateUserID, "access=%#v seal=%#v result=%#v effects=%#v", access, f.submissions.seal, result, f.effects)
	require.Equal(t, principal.SessionID, access.SessionID, "access=%#v seal=%#v result=%#v effects=%#v", access, f.submissions.seal, result, f.effects)
	require.Equal(t, model.HashToken(credential), access.ContinuityCredentialHash, "access=%#v seal=%#v result=%#v effects=%#v", access, f.submissions.seal, result, f.effects)
	require.Equal(t, revisionID, access.ExpectedCurrentRevisionID, "access=%#v seal=%#v result=%#v effects=%#v", access, f.submissions.seal, result, f.effects)
	require.Equal(t, int64(11), access.ExpectedWorkspaceCursor, "access=%#v seal=%#v result=%#v effects=%#v", access, f.submissions.seal, result, f.effects)
	require.Equal(t, int64(7), access.FinalFocusLossSequence, "access=%#v seal=%#v result=%#v effects=%#v", access, f.submissions.seal, result, f.effects)
	require.NotNil(t, f.submissions.seal, "access=%#v seal=%#v result=%#v effects=%#v", access, f.submissions.seal, result, f.effects)
	require.True(t, f.submissions.seal.SubmissionID.IsValid(), "access=%#v seal=%#v result=%#v effects=%#v", access, f.submissions.seal, result, f.effects)
	require.True(t, f.submissions.seal.AuditEventID.IsValid(), "access=%#v seal=%#v result=%#v effects=%#v", access, f.submissions.seal, result, f.effects)
	require.Equal(t, submissionID, result.SubmissionID, "access=%#v seal=%#v result=%#v effects=%#v", access, f.submissions.seal, result, f.effects)
	require.Equal(t, 1, f.effects.submitted, "access=%#v seal=%#v result=%#v effects=%#v", access, f.submissions.seal, result, f.effects)
	audit := fmt.Sprintf("%#v", f.audit.values)
	for _, private := range []string{credential, model.HashToken(credential), participationID.String()} {
		require.NotContains(t, audit, private)
	}
	require.Len(t, f.audit.values, 1)
	require.Equal(t, f.attemptID.String(), f.audit.values["exam_attempt_id"])
	wantIdempotency, prepareErr := prepareSubmissionIdempotency(f.invocation, "test-key", f.attemptID, revisionID, 11, 7)
	require.NoError(t, prepareErr)
	require.Equal(t, wantIdempotency, f.submissions.idempotency)
	require.Equal(t, f.userID, f.mail.request.CandidateUserID, "mail request=%#v seal=%#v", f.mail.request, f.submissions.seal)
	require.Equal(t, f.sitting.ExamID, f.mail.request.ExamID, "mail request=%#v seal=%#v", f.mail.request, f.submissions.seal)
	require.Equal(t, f.sitting.ID, f.mail.request.SittingID, "mail request=%#v seal=%#v", f.mail.request, f.submissions.seal)
	require.Equal(t, submissionID, f.mail.request.SubmissionID, "mail request=%#v seal=%#v", f.mail.request, f.submissions.seal)
	require.True(t, f.mail.request.SealedAt.Equal(f.at), "mail request=%#v seal=%#v", f.mail.request, f.submissions.seal)
	require.Equal(t, model.ExamSubmissionCandidateSubmitted, f.mail.request.Provenance, "mail request=%#v seal=%#v", f.mail.request, f.submissions.seal)
	require.NotNil(t, f.submissions.seal.Notice, "mail request=%#v seal=%#v", f.mail.request, f.submissions.seal)
	require.Equal(t, int64(2), f.submissions.seal.ExpectedRecipientRevision, "mail request=%#v seal=%#v", f.mail.request, f.submissions.seal)
	require.Equal(t, model.MillisFromTime(f.at), f.submissions.seal.AuditAt, "mail request=%#v seal=%#v", f.mail.request, f.submissions.seal)
	// Contract (guardrails 8 and 14): the audit begins before the durable write,
	// and the write commits before its effect.
	require.Equal(t, []string{"submission.resolve", "audit", "submission.mail", "submission.seal", "effect.submit"}, f.order)
}

func TestSubmitReplayReturnsRetainedReceiptAndSuppressesEffects(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	proposedID, retainedID := model.NewSubmissionID(), model.NewSubmissionID()
	revisionID := model.NewExamRevisionID()
	f.submissionID = proposedID
	access := validWorkspaceMutationAccess(f)
	f.submissions.target = &store.ExamSubmissionSealTarget{ExamID: f.sitting.ExamID, SittingID: f.sitting.ID,
		ClassID: f.sitting.ClassID, CandidateUserID: f.userID, WorkspaceID: model.NewExamAttemptWorkspaceID(),
		CurrentRevisionID: revisionID, Replayed: true, SealAt: f.at}
	f.submissions.sealResult = &store.ExamSubmissionSealResult{Receipt: store.ExamSubmissionReceipt{BrowserActivity: model.BrowserSubmissionSettlement{State: "not_applicable", InventoryRevision: 1}, SubmissionID: retainedID,
		AttemptID: f.attemptID, ExamRevisionID: revisionID, State: model.ExamAttemptSubmitted, WorkspaceCursor: 0,
		ManifestDigest: strings.Repeat("e", 64), SubmittedAt: f.at}, ExamID: f.sitting.ExamID,
		SittingID: f.sitting.ID, ClassID: f.sitting.ClassID, CandidateUserID: f.userID,
		ParticipationID: access.ParticipationID, Generation: access.Generation, ConnectionID: access.ConnectionID, Replayed: true}

	result, err := f.service.SubmitExamAttempt(context.Background(), f.invocation, SubmitCommand{Access: access,
		ExpectedCurrentRevisionID: revisionID, ExpectedWorkspaceCursor: 0, FinalFocusLossSequence: 0,
		IdempotencyKey: "test-key"})
	require.NoError(t, err)
	require.Equal(t, retainedID, result.SubmissionID, "result=%#v seal=%#v effects=%#v", result, f.submissions.seal, f.effects)
	require.Equal(t, proposedID, f.submissions.seal.SubmissionID, "result=%#v seal=%#v effects=%#v", result, f.submissions.seal, f.effects)
	require.Equal(t, 0, f.effects.submitted, "result=%#v seal=%#v effects=%#v", result, f.submissions.seal, f.effects)
	require.Nil(t, f.submissions.seal.Notice, "result=%#v seal=%#v effects=%#v", result, f.submissions.seal, f.effects)
	require.Equal(t, int64(0), f.submissions.seal.ExpectedRecipientRevision, "result=%#v seal=%#v effects=%#v", result, f.submissions.seal, f.effects)
	require.False(t, f.mail.request.SubmissionID.IsValid(), "result=%#v seal=%#v effects=%#v", result, f.submissions.seal, f.effects)
}

func TestAutomaticSealUsesBoundedSystemAuditAndPublishesOnlyFreshResult(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	target := store.ExamSubmissionAutomaticSealTarget{ExamID: f.sitting.ExamID, SittingID: f.sitting.ID,
		ClassID: f.sitting.ClassID, AcademicUnitID: model.NewAcademicUnitID(), CandidateUserID: f.userID,
		AttemptID: f.attemptID, WorkspaceID: model.NewExamAttemptWorkspaceID(), CurrentRevisionID: model.NewExamRevisionID(),
		ParticipationID: model.NewAttemptParticipationID(),
		Generation:      2, ConnectionID: f.connectionID}
	retained := model.NewSubmissionID()
	f.submissionID = retained
	f.submissions.automaticTargets = []store.ExamSubmissionAutomaticSealTarget{target}
	f.submissions.automaticResult = &store.ExamSubmissionAutomaticSealResult{ExamSubmissionSealResult: store.ExamSubmissionSealResult{
		Receipt: store.ExamSubmissionReceipt{BrowserActivity: model.BrowserSubmissionSettlement{State: "not_applicable", InventoryRevision: 1}, SubmissionID: retained, AttemptID: target.AttemptID,
			ExamRevisionID: target.CurrentRevisionID, State: model.ExamAttemptSubmitted, WorkspaceCursor: 4,
			ManifestDigest: strings.Repeat("a", 64), SubmittedAt: f.at},
		ExamID: target.ExamID, SittingID: target.SittingID, ClassID: target.ClassID, CandidateUserID: target.CandidateUserID,
		ParticipationID: target.ParticipationID, Generation: target.Generation, ConnectionID: target.ConnectionID},
		ConnectionClosed: true}
	items, err := f.service.ListAutomaticSealTargets(context.Background(), target.SittingID, model.ExamAttemptID(""), 20)
	require.NoError(t, err, "targets=%#v options=%#v err", items, f.submissions.automaticOptions)
	require.Len(t, items, 1, "targets=%#v options=%#v err", items, f.submissions.automaticOptions)
	require.Equal(t, target, items[0], "targets=%#v options=%#v err", items, f.submissions.automaticOptions)
	require.Equal(t, 20, f.submissions.automaticOptions.Limit, "targets=%#v options=%#v err", items, f.submissions.automaticOptions)
	jobID, jobAttemptID := model.NewJobID(), model.NewJobAttemptID()
	result, err := f.service.SealForSittingClose(context.Background(), invocation.System{JobID: jobID, AttemptID: jobAttemptID}, target)
	require.NoError(t, err, "result=%#v input=%#v effects=%d err", result, f.submissions.automaticInput, f.effects.submitted)
	require.Equal(t, retained, result.Receipt.SubmissionID, "result=%#v input=%#v effects=%d err", result, f.submissions.automaticInput, f.effects.submitted)
	require.True(t, result.ConnectionClosed, "result=%#v input=%#v effects=%d err", result, f.submissions.automaticInput, f.effects.submitted)
	require.Equal(t, 1, f.effects.submitted, "result=%#v input=%#v effects=%d err", result, f.submissions.automaticInput, f.effects.submitted)
	require.NotNil(t, f.submissions.automaticInput, "result=%#v input=%#v effects=%d err", result, f.submissions.automaticInput, f.effects.submitted)
	require.Equal(t, target, f.submissions.automaticInput.Target, "result=%#v input=%#v effects=%d err", result, f.submissions.automaticInput, f.effects.submitted)
	require.Equal(t, model.ExamSubmissionSittingClosed, f.mail.request.Provenance, "automatic mail request=%#v input=%#v", f.mail.request, f.submissions.automaticInput)
	require.Equal(t, retained, f.mail.request.SubmissionID, "automatic mail request=%#v input=%#v", f.mail.request, f.submissions.automaticInput)
	require.True(t, f.mail.request.SealedAt.Equal(f.at), "automatic mail request=%#v input=%#v", f.mail.request, f.submissions.automaticInput)
	require.Equal(t, model.MillisFromTime(f.at), f.submissions.automaticInput.AuditAt, "automatic mail request=%#v input=%#v", f.mail.request, f.submissions.automaticInput)
	require.NotNil(t, f.submissions.automaticInput.Notice, "automatic mail request=%#v input=%#v", f.mail.request, f.submissions.automaticInput)
	require.Equal(t, int64(2), f.submissions.automaticInput.ExpectedRecipientRevision, "automatic mail request=%#v input=%#v", f.mail.request, f.submissions.automaticInput)
	require.Equal(t, jobID.String(), f.systemAudit.values["job_id"])
	require.Equal(t, jobAttemptID.String(), f.systemAudit.values["job_attempt_id"])
	require.Equal(t, target.AttemptID.String(), f.systemAudit.values["exam_attempt_id"])
	f.submissions.automaticResult.Replayed = true
	f.submissions.automaticResult.ConnectionClosed = false
	_, err = f.service.SealForSittingClose(context.Background(), invocation.System{JobID: jobID, AttemptID: jobAttemptID}, target)
	require.NoError(t, err)
	require.Equal(t, 1, f.effects.submitted, "replay duplicated effects")
}

func TestManagerEndSealsWithPrivateReasonOutsideCandidateReceipt(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	managerID := useManagerCall(f)
	f.persistence.attempt, _ = managerAttemptFixture(t, f, model.ExamAttemptActive, 4, "")
	target := store.ExamSubmissionAutomaticSealTarget{ExamID: f.sitting.ExamID, SittingID: f.sitting.ID,
		ClassID: f.sitting.ClassID, AcademicUnitID: model.NewAcademicUnitID(), CandidateUserID: f.userID,
		AttemptID: f.attemptID, WorkspaceID: model.NewExamAttemptWorkspaceID(), CurrentRevisionID: model.NewExamRevisionID(),
		ParticipationID: model.NewAttemptParticipationID(), Generation: 2, ConnectionID: f.connectionID}
	submissionID := model.NewSubmissionID()
	f.submissionID = submissionID
	f.submissions.managerPreparation = &store.ExamSubmissionManagerEndPreparation{Target: target,
		ExpectedAttemptRevision: 4, SealAt: f.at}
	f.submissions.managerResult = &store.ExamSubmissionManagerEndResult{ExamSubmissionSealResult: store.ExamSubmissionSealResult{
		Receipt: store.ExamSubmissionReceipt{BrowserActivity: model.BrowserSubmissionSettlement{State: "not_applicable", InventoryRevision: 1}, SubmissionID: submissionID, AttemptID: target.AttemptID,
			ExamRevisionID: target.CurrentRevisionID, State: model.ExamAttemptSubmitted, WorkspaceCursor: 8,
			ManifestDigest: strings.Repeat("f", 64), SubmittedAt: f.at}, ExamID: target.ExamID, SittingID: target.SittingID,
		ClassID: target.ClassID, CandidateUserID: target.CandidateUserID, ParticipationID: target.ParticipationID,
		Generation: target.Generation, ConnectionID: target.ConnectionID}, ConnectionClosed: true}
	privateReason := "candidate requested an assisted early end"
	result, err := f.service.EndExamAttemptByManager(context.Background(), f.invocation, ManagerEndCommand{ExamID: target.ExamID,
		SittingID: target.SittingID, AttemptID: target.AttemptID, ExpectedAttemptRevision: 4,
		PrivateReason: privateReason, IdempotencyKey: "manager-end-once"})
	require.NoError(t, err)
	require.Equal(t, submissionID, result.SubmissionID, "result=%#v input=%#v mail=%#v effects", result, f.submissions.managerInput, f.mail.request)
	require.Equal(t, 1, f.effects.submitted, "result=%#v input=%#v mail=%#v effects", result, f.submissions.managerInput, f.mail.request)
	require.NotNil(t, f.submissions.managerInput, "result=%#v input=%#v mail=%#v effects", result, f.submissions.managerInput, f.mail.request)
	require.Equal(t, privateReason, f.submissions.managerInput.Request.PrivateReason, "result=%#v input=%#v mail=%#v effects", result, f.submissions.managerInput, f.mail.request)
	require.Equal(t, managerID, f.submissions.managerInput.Request.ActorUserID, "result=%#v input=%#v mail=%#v effects", result, f.submissions.managerInput, f.mail.request)
	require.Equal(t, model.ExamSubmissionManagerEndedAttempt, f.mail.request.Provenance, "result=%#v input=%#v mail=%#v effects", result, f.submissions.managerInput, f.mail.request)
	require.NotContains(t, fmt.Sprintf("%#v", f.audit.values), privateReason, "private reason entered ordinary audit fields: %#v", f.audit.values)
	wantIdempotency, prepareErr := prepareManagerEndIdempotency(f.invocation, ManagerEndCommand{ExamID: target.ExamID,
		SittingID: target.SittingID, AttemptID: target.AttemptID, ExpectedAttemptRevision: 4,
		PrivateReason: privateReason, IdempotencyKey: "manager-end-once"})
	require.NoError(t, prepareErr)
	require.Equal(t, wantIdempotency, f.submissions.idempotency)
}

func TestManagerEndConcealsSelfAccessAndFailsClosedOnAuditFailure(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		auditErr error
	}{
		{name: "records concealed denial"},
		{name: "audit failure fails closed", auditErr: errors.New("audit unavailable")},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(t)
			f.persistence.attempt, _ = managerAttemptFixture(t, f, model.ExamAttemptActive, 4, "")
			f.audit.failErr = test.auditErr
			privateReason := "manager must never end their own Attempt"
			_, err := f.service.EndExamAttemptByManager(context.Background(), f.invocation, ManagerEndCommand{
				ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, AttemptID: f.attemptID,
				ExpectedAttemptRevision: 4, PrivateReason: privateReason, IdempotencyKey: "self-manager-end",
			})
			if test.auditErr != nil {
				require.ErrorIs(t, err, test.auditErr)
			} else {
				require.True(t, apperror.Is(err, "resource.not_found"))
				require.True(t, chainHasCode(err, "exam.attempt.not_found"))
			}
			require.Equal(t, model.ActionExamSittingManage, f.audit.action)
			require.Equal(t, model.Resource{Type: model.ResourceExamSitting, ID: f.sitting.ID.String()}, f.audit.resource)
			require.Equal(t, model.RoleScopeClass, f.audit.scopeType)
			require.Equal(t, f.sitting.ClassID.String(), f.audit.scopeID)
			require.Equal(t, store.ExamSubmissionManagerEndOperation, f.audit.operation)
			require.Equal(t, "exam.attempt.not_found", f.audit.failedCode)
			require.Equal(t, f.sitting.ExamID.String(), f.audit.values["exam_id"])
			require.Equal(t, f.sitting.ID.String(), f.audit.values["exam_sitting_id"])
			require.Equal(t, f.attemptID.String(), f.audit.values["exam_attempt_id"])
			require.Equal(t, int64(4), f.audit.values["expected_attempt_revision"])
			require.NotContains(t, fmt.Sprintf("%#v", f.audit.values), privateReason, "self-denial leaked or mutated: audit=%#v submissions=%#v mail=%#v effects", f.audit.values, f.submissions, f.mail.request)
			require.Nil(t, f.submissions.managerPreparation, "self-denial leaked or mutated: audit=%#v submissions=%#v mail=%#v effects", f.audit.values, f.submissions, f.mail.request)
			require.Nil(t, f.submissions.managerInput, "self-denial leaked or mutated: audit=%#v submissions=%#v mail=%#v effects", f.audit.values, f.submissions, f.mail.request)
			require.False(t, f.mail.request.SubmissionID.IsValid(), "self-denial leaked or mutated: audit=%#v submissions=%#v mail=%#v effects", f.audit.values, f.submissions, f.mail.request)
			require.Equal(t, 0, f.effects.submitted, "self-denial leaked or mutated: audit=%#v submissions=%#v mail=%#v effects", f.audit.values, f.submissions, f.mail.request)
		})
	}
}

func TestManagedSubmissionNestedOwnershipMismatchIsConcealed(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	submissionID := model.NewSubmissionID()
	f.submissions.authorization = &store.ExamSubmissionAuthorization{SubmissionID: submissionID,
		ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, AttemptID: f.attemptID, CandidateUserID: model.NewUserID(), AcademicUnitID: model.NewAcademicUnitID()}
	_, err := f.service.GetExamSubmission(context.Background(), f.invocation, GetSubmissionQuery{ExamID: model.NewExamID(),
		SittingID: f.sitting.ID, AttemptID: f.attemptID, SubmissionID: submissionID})
	require.True(t, apperror.Is(err, "resource.not_found"), "error=%v order=%v", err, f.order)
	require.True(t, chainHasCode(err, "exam.attempt.not_found"), "error=%v order=%v", err, f.order)
	// A concealed ownership mismatch never reads the Submission.
	require.Equal(t, []string{"submission.authorize", "submission.resolve_owner"}, f.order)
}

func TestOpenSubmissionFileAuthorizesThenStreamsRetainedStarterBytes(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	submission := submissionFixture(t, f, 5)
	entryID, version, starterID := model.NewAttemptWorkspaceEntryID(), model.NewWorkspaceContentVersion(), model.NewStarterWorkspaceObjectID()
	f.submissions.authorization = &store.ExamSubmissionAuthorization{SubmissionID: submission.ID,
		ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, AttemptID: f.attemptID, CandidateUserID: model.NewUserID(), AcademicUnitID: model.NewAcademicUnitID()}
	f.submissions.file = &store.ExamSubmissionFileSelector{Entry: store.ExamSubmissionManifestItem{EntryID: entryID,
		Kind: model.StarterWorkspaceEntryFile, Path: "main.go", ContentVersion: version, MediaType: "text/x-go",
		SizeBytes: 4, SHA256: strings.Repeat("a", 64)}, StorageOrigin: model.AttemptWorkspaceStorageStarter,
		StarterObjectID: starterID, ContentVersion: version}

	opened, err := f.service.OpenExamSubmissionFile(context.Background(), f.invocation, OpenSubmissionFileQuery{
		GetSubmissionQuery: GetSubmissionQuery{ExamID: f.sitting.ExamID, SittingID: f.sitting.ID,
			AttemptID: f.attemptID, SubmissionID: submission.ID}, EntryID: entryID})
	require.NoError(t, err)
	defer opened.Body.Close()
	body, _ := io.ReadAll(opened.Body)
	require.Equal(t, "workspace", string(body), "opened=%#v starter=%s attempt=%s body", opened, f.content.starterID, f.content.openAttemptID)
	require.Equal(t, version, opened.ContentVersion, "opened=%#v starter=%s attempt=%s body", opened, f.content.starterID, f.content.openAttemptID)
	require.Equal(t, strings.Repeat("a", 64), opened.SHA256, "opened=%#v starter=%s attempt=%s body", opened, f.content.starterID, f.content.openAttemptID)
	require.Equal(t, starterID, f.content.starterID, "opened=%#v starter=%s attempt=%s body", opened, f.content.starterID, f.content.openAttemptID)
	require.False(t, f.content.openAttemptID.IsValid(), "opened=%#v starter=%s attempt=%s body", opened, f.content.starterID, f.content.openAttemptID)
}

func TestOpenSubmissionFileStreamsRetainedAttemptOriginBytes(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	submission := submissionFixture(t, f, 5)
	entryID, version, objectID := model.NewAttemptWorkspaceEntryID(), model.NewWorkspaceContentVersion(), model.NewAttemptWorkspaceObjectID()
	f.submissions.authorization = &store.ExamSubmissionAuthorization{SubmissionID: submission.ID,
		ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, AttemptID: f.attemptID, CandidateUserID: model.NewUserID(), AcademicUnitID: model.NewAcademicUnitID()}
	f.submissions.file = &store.ExamSubmissionFileSelector{Entry: store.ExamSubmissionManifestItem{EntryID: entryID,
		Kind: model.StarterWorkspaceEntryFile, Path: "answer.txt", ContentVersion: version, MediaType: "text/plain",
		SizeBytes: 4, SHA256: strings.Repeat("b", 64)}, StorageOrigin: model.AttemptWorkspaceStorageAttempt,
		AttemptObjectID: objectID, ContentVersion: version}
	f.content.openAttemptBody = "work"

	opened, err := f.service.OpenExamSubmissionFile(context.Background(), f.invocation, OpenSubmissionFileQuery{
		GetSubmissionQuery: GetSubmissionQuery{ExamID: f.sitting.ExamID, SittingID: f.sitting.ID,
			AttemptID: f.attemptID, SubmissionID: submission.ID}, EntryID: entryID})
	require.NoError(t, err)
	defer opened.Body.Close()
	body, _ := io.ReadAll(opened.Body)
	require.Equal(t, "work", string(body), "opened=%#v starter=%s attempt=%s body", opened, f.content.starterID, f.content.openAttemptID)
	require.Equal(t, version, opened.ContentVersion, "opened=%#v starter=%s attempt=%s body", opened, f.content.starterID, f.content.openAttemptID)
	require.Equal(t, strings.Repeat("b", 64), opened.SHA256, "opened=%#v starter=%s attempt=%s body", opened, f.content.starterID, f.content.openAttemptID)
	require.Equal(t, objectID, f.content.openAttemptID, "opened=%#v starter=%s attempt=%s body", opened, f.content.starterID, f.content.openAttemptID)
	require.False(t, f.content.starterID.IsValid(), "opened=%#v starter=%s attempt=%s body", opened, f.content.starterID, f.content.openAttemptID)
}

func TestOpenSubmissionFileFailsClosedOnRetainedSelectorMismatch(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	submission := submissionFixture(t, f, 5)
	entryID := model.NewAttemptWorkspaceEntryID()
	f.submissions.authorization = &store.ExamSubmissionAuthorization{SubmissionID: submission.ID,
		ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, AttemptID: f.attemptID, CandidateUserID: model.NewUserID(), AcademicUnitID: model.NewAcademicUnitID()}
	f.submissions.file = &store.ExamSubmissionFileSelector{Entry: store.ExamSubmissionManifestItem{
		EntryID: model.NewAttemptWorkspaceEntryID(), Kind: model.StarterWorkspaceEntryFile, Path: "answer.txt",
		ContentVersion: model.NewWorkspaceContentVersion(), MediaType: "text/plain", SizeBytes: 4,
		SHA256: strings.Repeat("b", 64)}, StorageOrigin: model.AttemptWorkspaceStorageAttempt,
		AttemptObjectID: model.NewAttemptWorkspaceObjectID(), ContentVersion: model.NewWorkspaceContentVersion()}

	_, err := f.service.OpenExamSubmissionFile(context.Background(), f.invocation, OpenSubmissionFileQuery{
		GetSubmissionQuery: GetSubmissionQuery{ExamID: f.sitting.ExamID, SittingID: f.sitting.ID,
			AttemptID: f.attemptID, SubmissionID: submission.ID}, EntryID: entryID})
	var fault *apperror.Error
	require.ErrorAs(t, err, &fault, "error=%v starter=%s attempt=%s", err, f.content.starterID, f.content.openAttemptID)
	require.Equal(t, "exam.attempt.unavailable", fault.Code(), "error=%v starter=%s attempt=%s", err, f.content.starterID, f.content.openAttemptID)
	require.False(t, f.content.openAttemptID.IsValid(), "error=%v starter=%s attempt=%s", err, f.content.starterID, f.content.openAttemptID)
	require.False(t, f.content.starterID.IsValid(), "error=%v starter=%s attempt=%s", err, f.content.starterID, f.content.openAttemptID)
}

func TestListSubmissionManifestReturnsBoundedEntryIDKeysetAfterAuthorization(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	submission := submissionFixture(t, f, 5)
	f.submissions.authorization = &store.ExamSubmissionAuthorization{SubmissionID: submission.ID,
		ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, AttemptID: f.attemptID, CandidateUserID: model.NewUserID(), AcademicUnitID: model.NewAcademicUnitID()}
	ids := []model.AttemptWorkspaceEntryID{model.NewAttemptWorkspaceEntryID(), model.NewAttemptWorkspaceEntryID(), model.NewAttemptWorkspaceEntryID()}
	slices.SortFunc(ids, func(left, right model.AttemptWorkspaceEntryID) int {
		return strings.Compare(left.String(), right.String())
	})
	f.submissions.manifest = &store.ExamSubmissionManifestPage{SubmissionID: submission.ID, WorkspaceCursor: 5,
		ManifestDigest: submission.ManifestDigest, Items: []store.ExamSubmissionManifestItem{
			{EntryID: ids[1], Kind: model.StarterWorkspaceEntryDirectory, Path: "src"},
			{EntryID: ids[2], Kind: model.StarterWorkspaceEntryFile, Path: "src/main.go",
				ContentVersion: model.NewWorkspaceContentVersion(), MediaType: "text/x-go", SizeBytes: 4, SHA256: strings.Repeat("a", 64)},
		}}

	page, err := f.service.ListExamSubmissionManifest(context.Background(), f.invocation, ListSubmissionManifestQuery{
		GetSubmissionQuery: GetSubmissionQuery{ExamID: f.sitting.ExamID, SittingID: f.sitting.ID,
			AttemptID: f.attemptID, SubmissionID: submission.ID}, AfterEntryID: ids[0], Limit: 2,
	})
	require.NoError(t, err)
	require.Equal(t, submission.ID, page.SubmissionID, "page=%#v options=%#v", page, f.submissions.manifestOptions)
	require.Len(t, page.Items, 2, "page=%#v options=%#v", page, f.submissions.manifestOptions)
	require.Equal(t, "src/main.go", page.Items[1].Path, "page=%#v options=%#v", page, f.submissions.manifestOptions)
	require.Equal(t, ids[0], f.submissions.manifestOptions.AfterEntryID, "page=%#v options=%#v", page, f.submissions.manifestOptions)
	require.Equal(t, 2, f.submissions.manifestOptions.Limit, "page=%#v options=%#v", page, f.submissions.manifestOptions)
}

func TestGetSubmissionAuthorizesCanonicalResourceBeforeProtectedRead(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	submission := submissionFixture(t, f, 5)
	f.submissions.authorization = &store.ExamSubmissionAuthorization{SubmissionID: submission.ID,
		ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, AttemptID: f.attemptID, CandidateUserID: model.NewUserID(), AcademicUnitID: model.NewAcademicUnitID()}
	f.submissions.submission = submission

	got, err := f.service.GetExamSubmission(context.Background(), f.invocation, GetSubmissionQuery{ExamID: f.sitting.ExamID,
		SittingID: f.sitting.ID, AttemptID: f.attemptID, SubmissionID: submission.ID})
	require.NoError(t, err)
	require.Equal(t, *f.submissions.authorization, got.Authorization, "got=%#v authorized", got)
	require.Equal(t, submission.ID, got.Submission.ID, "got=%#v authorized", got)
	require.Equal(t, submission.ID, f.submissionAuthorizationID, "got=%#v authorized", got)
	// Contract: authorization precedes reading the Submission.
	require.Equal(t, []string{"submission.authorize", "submission.resolve_owner", "submission.get"}, f.order)
}

func submissionFixture(t *testing.T, f *fixture, cursor int64) *model.ExamSubmission {
	t.Helper()
	manifest, err := model.NewExamSubmissionManifest(cursor, nil)
	require.NoError(t, err)
	submission, err := model.NewExamSubmission(model.ExamSubmissionSpecification{ID: model.NewSubmissionID(),
		AttemptID: f.attemptID, ExamRevisionID: model.NewExamRevisionID(), WorkspaceID: model.NewExamAttemptWorkspaceID(), Manifest: manifest,
		BrowserActivity: model.BrowserSubmissionSettlement{State: "not_applicable", InventoryRevision: 1},
		Provenance:      model.ExamSubmissionCandidateSubmitted, SubmittedAt: f.at.Add(time.Minute)})
	require.NoError(t, err)
	return submission
}

type submissionStoreFake struct {
	f                  *fixture
	resolvedAccess     store.ExamSubmissionSealAccess
	target             *store.ExamSubmissionSealTarget
	seal               *store.ExamSubmissionSeal
	sealResult         *store.ExamSubmissionSealResult
	err                error
	authorization      *store.ExamSubmissionAuthorization
	submission         *model.ExamSubmission
	manifest           *store.ExamSubmissionManifestPage
	manifestOptions    store.ExamSubmissionManifestListOptions
	file               *store.ExamSubmissionFileSelector
	automaticTargets   []store.ExamSubmissionAutomaticSealTarget
	automaticOptions   store.ExamSubmissionAutomaticSealListOptions
	automaticInput     *store.ExamSubmissionAutomaticSeal
	automaticResult    *store.ExamSubmissionAutomaticSealResult
	managerRequest     store.ExamSubmissionManagerEndRequest
	managerPreparation *store.ExamSubmissionManagerEndPreparation
	managerInput       *store.ExamSubmissionManagerEnd
	managerResult      *store.ExamSubmissionManagerEndResult
	idempotency        *store.CommandIdempotency
}

type submissionMailFake struct {
	f       *fixture
	request SubmissionMailPreparation
	err     error
}

func (f *submissionMailFake) PrepareSubmissionReceipt(_ context.Context, request SubmissionMailPreparation) (*PreparedSubmissionMail, error) {
	f.f.order = append(f.f.order, "submission.mail")
	f.request = request
	if f.err != nil {
		return nil, f.err
	}
	return &PreparedSubmissionMail{Notice: &store.PreparedMail{Occurrence: &model.MailOccurrence{},
		Delivery: &model.MailDelivery{}, Job: &model.Job{}}, ExpectedRecipientRevision: 2}, nil
}

func (f *submissionStoreFake) ListAutomaticSealTargets(_ context.Context, options store.ExamSubmissionAutomaticSealListOptions) ([]store.ExamSubmissionAutomaticSealTarget, error) {
	f.automaticOptions = options
	return f.automaticTargets, f.err
}

func (f *submissionStoreFake) PrepareAutomaticSeal(context.Context, store.ExamSubmissionAutomaticSealTarget) (*store.ExamSubmissionAutomaticSealPreparation, error) {
	return &store.ExamSubmissionAutomaticSealPreparation{Replayed: f.automaticResult != nil && f.automaticResult.Replayed,
		SealAt: f.f.at}, f.err
}

func (f *submissionStoreFake) PrepareManagerEnd(_ context.Context, request store.ExamSubmissionManagerEndRequest) (*store.ExamSubmissionManagerEndPreparation, error) {
	f.managerRequest = request
	return f.managerPreparation, f.err
}

func (f *submissionStoreFake) EndByManager(_ context.Context, input *store.ExamSubmissionManagerEnd,
	idempotency *store.CommandIdempotency,
) (*store.ExamSubmissionManagerEndResult, error) {
	f.managerInput, f.idempotency = input, idempotency
	return f.managerResult, f.err
}

func (f *submissionStoreFake) SealForSittingClose(_ context.Context, input *store.ExamSubmissionAutomaticSeal) (*store.ExamSubmissionAutomaticSealResult, error) {
	f.automaticInput = input
	return f.automaticResult, f.err
}

func (f *submissionStoreFake) ResolveSealTarget(_ context.Context, access store.ExamSubmissionSealAccess) (*store.ExamSubmissionSealTarget, error) {
	f.f.order = append(f.f.order, "submission.resolve")
	f.resolvedAccess = access
	return f.target, f.err
}

func (f *submissionStoreFake) Seal(_ context.Context, input *store.ExamSubmissionSeal, command *store.CommandIdempotency) (*store.ExamSubmissionSealResult, error) {
	f.f.order = append(f.f.order, "submission.seal")
	f.seal, f.idempotency = input, command
	return f.sealResult, f.err
}

func (f *submissionStoreFake) Resolve(context.Context, model.SubmissionID) (*store.ExamSubmissionAuthorization, error) {
	f.f.order = append(f.f.order, "submission.resolve_owner")
	return f.authorization, f.err
}

func (f *submissionStoreFake) Get(context.Context, model.SubmissionID) (*model.ExamSubmission, error) {
	f.f.order = append(f.f.order, "submission.get")
	return f.submission, f.err
}

func (f *submissionStoreFake) ListManifest(_ context.Context, options store.ExamSubmissionManifestListOptions) (*store.ExamSubmissionManifestPage, error) {
	f.manifestOptions = options
	return f.manifest, f.err
}

func (f *submissionStoreFake) ResolveFile(context.Context, model.SubmissionID, model.AttemptWorkspaceEntryID) (*store.ExamSubmissionFileSelector, error) {
	f.f.order = append(f.f.order, "submission.resolve_file")
	return f.file, f.err
}
