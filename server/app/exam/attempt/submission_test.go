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
	browserActivity := model.BrowserActivitySubmission{State: model.BrowserActivitySubmissionNotApplicable}
	submissionID := model.NewSubmissionID()
	f.submissionID = submissionID
	digest := strings.Repeat("d", 64)
	f.submissions.target = &store.ExamSubmissionSealTarget{ExamID: f.sitting.ExamID, SittingID: f.sitting.ID,
		ClassID: f.sitting.ClassID, CandidateUserID: f.userID, WorkspaceID: workspaceID, CurrentRevisionID: revisionID}
	f.submissions.target.SealAt = f.at
	f.submissions.sealResult = &store.ExamSubmissionSealResult{Receipt: store.ExamSubmissionReceipt{
		SubmissionID: submissionID, AttemptID: f.attemptID, ExamRevisionID: revisionID, State: model.ExamAttemptSubmitted,
		WorkspaceCursor: 11, ManifestDigest: digest, SubmittedAt: f.at}, ExamID: f.sitting.ExamID,
		SittingID: f.sitting.ID, ClassID: f.sitting.ClassID, CandidateUserID: f.userID,
		ParticipationID: participationID, Generation: 3, ConnectionID: f.connectionID}

	result, err := f.service.Submit(context.Background(), f.call, SubmitCommand{
		Access: WorkspaceMutationAccess{CandidateAccess: CandidateAccess{AttemptID: f.attemptID,
			ConnectionID: f.connectionID, ContinuityCredential: credential}, ParticipationID: participationID, Generation: 3},
		ExpectedCurrentRevisionID: revisionID, ExpectedWorkspaceCursor: 11, FinalFocusLossSequence: 7,
		BrowserActivity: browserActivity, IdempotencyKey: "test-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	principal := f.call.Principal()
	access := f.submissions.resolvedAccess
	if access.AttemptID != f.attemptID || access.ParticipationID != participationID || access.Generation != 3 ||
		access.ConnectionID != f.connectionID || access.CandidateUserID != principal.UserID || access.SessionID != principal.SessionID ||
		access.ContinuityCredentialHash != model.HashToken(credential) || access.ExpectedCurrentRevisionID != revisionID ||
		access.ExpectedWorkspaceCursor != 11 || access.BrowserActivity.State != model.BrowserActivitySubmissionNotApplicable ||
		access.FinalFocusLossSequence != 7 || f.submissions.seal == nil || !f.submissions.seal.SubmissionID.IsValid() ||
		!model.IsValidId(f.submissions.seal.AuditEventID) || result.Receipt.SubmissionID != submissionID || f.effects.submitted != 1 {
		t.Fatalf("access=%#v seal=%#v result=%#v effects=%#v", access, f.submissions.seal, result, f.effects)
	}
	audit := fmt.Sprintf("%#v", f.audit.values)
	for _, private := range []string{credential, model.HashToken(credential), participationID.String()} {
		if strings.Contains(audit, private) {
			t.Fatalf("private Submission material entered audit: %s", audit)
		}
	}
	if len(f.audit.values) != 1 || f.audit.values["exam_attempt_id"] != f.attemptID.String() {
		t.Fatalf("Submission audit fields=%#v", f.audit.values)
	}
	wantIdempotency, prepareErr := prepareSubmissionIdempotency(f.call, "test-key", f.attemptID, revisionID, 11, 7, browserActivity)
	if prepareErr != nil {
		t.Fatal(prepareErr)
	}
	assertStoreBoundaryCommand(t, f.submissions.idempotency, wantIdempotency)
	if f.mail.request.CandidateUserID != f.userID || f.mail.request.ExamID != f.sitting.ExamID ||
		f.mail.request.SittingID != f.sitting.ID || f.mail.request.SubmissionID != submissionID ||
		!f.mail.request.SealedAt.Equal(f.at) || f.mail.request.Provenance != model.ExamSubmissionCandidateSubmitted || f.submissions.seal.Notice == nil ||
		f.submissions.seal.ExpectedRecipientRevision != 2 || f.submissions.seal.AuditAt != model.MillisFromTime(f.at) {
		t.Fatalf("mail request=%#v seal=%#v", f.mail.request, f.submissions.seal)
	}
	if got := strings.Join(f.order, ","); got != "submission.resolve,audit,submission.mail,submission.seal,effect.submit" {
		t.Fatalf("order=%s", got)
	}
}

func TestSubmitReplayReturnsRetainedReceiptAndSuppressesEffects(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	proposedID, retainedID := model.NewSubmissionID(), model.NewSubmissionID()
	revisionID := model.NewExamRevisionID()
	browserActivity := model.BrowserActivitySubmission{State: model.BrowserActivitySubmissionNotApplicable}
	f.submissionID = proposedID
	access := validWorkspaceMutationAccess(f)
	f.submissions.target = &store.ExamSubmissionSealTarget{ExamID: f.sitting.ExamID, SittingID: f.sitting.ID,
		ClassID: f.sitting.ClassID, CandidateUserID: f.userID, WorkspaceID: model.NewExamAttemptWorkspaceID(),
		CurrentRevisionID: revisionID, Replayed: true, SealAt: f.at}
	f.submissions.sealResult = &store.ExamSubmissionSealResult{Receipt: store.ExamSubmissionReceipt{SubmissionID: retainedID,
		AttemptID: f.attemptID, ExamRevisionID: revisionID, State: model.ExamAttemptSubmitted, WorkspaceCursor: 0,
		ManifestDigest: strings.Repeat("e", 64), SubmittedAt: f.at}, ExamID: f.sitting.ExamID,
		SittingID: f.sitting.ID, ClassID: f.sitting.ClassID, CandidateUserID: f.userID,
		ParticipationID: access.ParticipationID, Generation: access.Generation, ConnectionID: access.ConnectionID, Replayed: true}

	result, err := f.service.Submit(context.Background(), f.call, SubmitCommand{Access: access,
		ExpectedCurrentRevisionID: revisionID, ExpectedWorkspaceCursor: 0, FinalFocusLossSequence: 0,
		BrowserActivity: browserActivity, IdempotencyKey: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Receipt.SubmissionID != retainedID || f.submissions.seal.SubmissionID != proposedID ||
		!result.Replayed || f.effects.submitted != 0 || f.submissions.seal.Notice != nil ||
		f.submissions.seal.ExpectedRecipientRevision != 0 || f.mail.request.SubmissionID.IsValid() {
		t.Fatalf("result=%#v seal=%#v effects=%#v", result, f.submissions.seal, f.effects)
	}
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
		Receipt: store.ExamSubmissionReceipt{SubmissionID: retained, AttemptID: target.AttemptID,
			ExamRevisionID: target.CurrentRevisionID, State: model.ExamAttemptSubmitted, WorkspaceCursor: 4,
			ManifestDigest: strings.Repeat("a", 64), SubmittedAt: f.at},
		ExamID: target.ExamID, SittingID: target.SittingID, ClassID: target.ClassID, CandidateUserID: target.CandidateUserID,
		ParticipationID: target.ParticipationID, Generation: target.Generation, ConnectionID: target.ConnectionID},
		ConnectionClosed: true}
	items, err := f.service.ListAutomaticSealTargets(context.Background(), target.SittingID, model.ExamAttemptID(""), 20)
	if err != nil || len(items) != 1 || items[0] != target || f.submissions.automaticOptions.Limit != 20 {
		t.Fatalf("targets=%#v options=%#v err=%v", items, f.submissions.automaticOptions, err)
	}
	jobID, jobAttemptID := model.NewJobID(), model.NewJobAttemptID()
	result, err := f.service.SealForSittingClose(context.Background(), SystemCall{JobID: jobID, AttemptID: jobAttemptID}, target)
	if err != nil || result.Receipt.SubmissionID != retained || !result.ConnectionClosed || f.effects.submitted != 1 ||
		f.submissions.automaticInput == nil || f.submissions.automaticInput.Target != target {
		t.Fatalf("result=%#v input=%#v effects=%d err=%v", result, f.submissions.automaticInput, f.effects.submitted, err)
	}
	if f.mail.request.Provenance != model.ExamSubmissionSittingClosed || f.mail.request.SubmissionID != retained || !f.mail.request.SealedAt.Equal(f.at) ||
		f.submissions.automaticInput.AuditAt != model.MillisFromTime(f.at) || f.submissions.automaticInput.Notice == nil ||
		f.submissions.automaticInput.ExpectedRecipientRevision != 2 {
		t.Fatalf("automatic mail request=%#v input=%#v", f.mail.request, f.submissions.automaticInput)
	}
	if f.systemAudit.values["job_id"] != jobID.String() || f.systemAudit.values["job_attempt_id"] != jobAttemptID.String() ||
		f.systemAudit.values["exam_attempt_id"] != target.AttemptID.String() {
		t.Fatalf("system audit=%#v", f.systemAudit.values)
	}
	f.submissions.automaticResult.Replayed = true
	f.submissions.automaticResult.ConnectionClosed = false
	if _, err = f.service.SealForSittingClose(context.Background(), SystemCall{JobID: jobID, AttemptID: jobAttemptID}, target); err != nil {
		t.Fatal(err)
	}
	if f.effects.submitted != 1 {
		t.Fatalf("replay duplicated effects: %d", f.effects.submitted)
	}
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
		Receipt: store.ExamSubmissionReceipt{SubmissionID: submissionID, AttemptID: target.AttemptID,
			ExamRevisionID: target.CurrentRevisionID, State: model.ExamAttemptSubmitted, WorkspaceCursor: 8,
			ManifestDigest: strings.Repeat("f", 64), SubmittedAt: f.at}, ExamID: target.ExamID, SittingID: target.SittingID,
		ClassID: target.ClassID, CandidateUserID: target.CandidateUserID, ParticipationID: target.ParticipationID,
		Generation: target.Generation, ConnectionID: target.ConnectionID}, ConnectionClosed: true}
	privateReason := "candidate requested an assisted early end"
	result, err := f.service.EndByManager(context.Background(), f.call, ManagerEndCommand{ExamID: target.ExamID,
		SittingID: target.SittingID, AttemptID: target.AttemptID, ExpectedAttemptRevision: 4,
		PrivateReason: privateReason, IdempotencyKey: "manager-end-once"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Provenance != model.ExamSubmissionManagerEndedAttempt || result.Receipt.SubmissionID != submissionID ||
		f.effects.submitted != 1 || f.submissions.managerInput == nil ||
		f.submissions.managerInput.Request.PrivateReason != privateReason ||
		f.submissions.managerInput.Request.ActorUserID != managerID ||
		f.mail.request.Provenance != model.ExamSubmissionManagerEndedAttempt {
		t.Fatalf("result=%#v input=%#v mail=%#v effects=%d", result, f.submissions.managerInput, f.mail.request, f.effects.submitted)
	}
	if strings.Contains(fmt.Sprintf("%#v", f.audit.values), privateReason) {
		t.Fatalf("private reason entered ordinary audit fields: %#v", f.audit.values)
	}
	wantIdempotency, prepareErr := prepareManagerEndIdempotency(f.call, ManagerEndCommand{ExamID: target.ExamID,
		SittingID: target.SittingID, AttemptID: target.AttemptID, ExpectedAttemptRevision: 4,
		PrivateReason: privateReason, IdempotencyKey: "manager-end-once"})
	if prepareErr != nil {
		t.Fatal(prepareErr)
	}
	assertStoreBoundaryCommand(t, f.submissions.idempotency, wantIdempotency)
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
			_, err := f.service.EndByManager(context.Background(), f.call, ManagerEndCommand{
				ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, AttemptID: f.attemptID,
				ExpectedAttemptRevision: 4, PrivateReason: privateReason, IdempotencyKey: "self-manager-end",
			})
			if test.auditErr != nil {
				if !errors.Is(err, test.auditErr) {
					t.Fatalf("EndByManager(audit failure) error = %v", err)
				}
			} else {
				var fault *Fault
				if !errors.As(err, &fault) || fault.Code != "exam.attempt.not_found" {
					t.Fatalf("EndByManager(self) error = %v", err)
				}
			}
			if f.audit.action != model.ActionExamSittingManage ||
				f.audit.resource != (model.Resource{Type: model.ResourceExamSitting, ID: f.sitting.ID.String()}) ||
				f.audit.scopeType != model.RoleScopeClass || f.audit.scopeID != f.sitting.ClassID.String() ||
				f.audit.operation != store.ExamSubmissionManagerEndOperation ||
				f.audit.failedCode != "exam.attempt.not_found" ||
				f.audit.values["exam_id"] != f.sitting.ExamID.String() ||
				f.audit.values["exam_sitting_id"] != f.sitting.ID.String() ||
				f.audit.values["exam_attempt_id"] != f.attemptID.String() ||
				f.audit.values["expected_attempt_revision"] != int64(4) {
				t.Fatalf("self-denial audit = %#v", f.audit)
			}
			if strings.Contains(fmt.Sprintf("%#v", f.audit.values), privateReason) ||
				f.submissions.managerPreparation != nil || f.submissions.managerInput != nil ||
				f.mail.request.SubmissionID.IsValid() || f.effects.submitted != 0 {
				t.Fatalf("self-denial leaked or mutated: audit=%#v submissions=%#v mail=%#v effects=%d",
					f.audit.values, f.submissions, f.mail.request, f.effects.submitted)
			}
		})
	}
}

func TestManagedSubmissionNestedOwnershipMismatchIsConcealed(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	submissionID := model.NewSubmissionID()
	f.submissions.authorization = &store.ExamSubmissionAuthorization{SubmissionID: submissionID,
		ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, AttemptID: f.attemptID, CandidateUserID: model.NewUserID(), AcademicUnitID: model.NewAcademicUnitID()}
	_, err := f.service.GetSubmission(context.Background(), f.call, GetSubmissionQuery{ExamID: model.NewExamID(),
		SittingID: f.sitting.ID, AttemptID: f.attemptID, SubmissionID: submissionID})
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "exam.attempt.not_found" ||
		strings.Join(f.order, ",") != "submission.authorize,submission.resolve_owner" {
		t.Fatalf("error=%v order=%v", err, f.order)
	}
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

	opened, err := f.service.OpenSubmissionFile(context.Background(), f.call, OpenSubmissionFileQuery{
		GetSubmissionQuery: GetSubmissionQuery{ExamID: f.sitting.ExamID, SittingID: f.sitting.ID,
			AttemptID: f.attemptID, SubmissionID: submission.ID}, EntryID: entryID})
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Body.Close()
	body, _ := io.ReadAll(opened.Body)
	if string(body) != "workspace" || opened.ContentVersion != version || opened.SHA256 != strings.Repeat("a", 64) ||
		f.content.starterID != starterID || f.content.openAttemptID.IsValid() {
		t.Fatalf("opened=%#v starter=%s attempt=%s body=%q", opened, f.content.starterID, f.content.openAttemptID, body)
	}
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

	opened, err := f.service.OpenSubmissionFile(context.Background(), f.call, OpenSubmissionFileQuery{
		GetSubmissionQuery: GetSubmissionQuery{ExamID: f.sitting.ExamID, SittingID: f.sitting.ID,
			AttemptID: f.attemptID, SubmissionID: submission.ID}, EntryID: entryID})
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Body.Close()
	body, _ := io.ReadAll(opened.Body)
	if string(body) != "work" || opened.ContentVersion != version || opened.SHA256 != strings.Repeat("b", 64) ||
		f.content.openAttemptID != objectID || f.content.starterID.IsValid() {
		t.Fatalf("opened=%#v starter=%s attempt=%s body=%q", opened, f.content.starterID, f.content.openAttemptID, body)
	}
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

	_, err := f.service.OpenSubmissionFile(context.Background(), f.call, OpenSubmissionFileQuery{
		GetSubmissionQuery: GetSubmissionQuery{ExamID: f.sitting.ExamID, SittingID: f.sitting.ID,
			AttemptID: f.attemptID, SubmissionID: submission.ID}, EntryID: entryID})
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "exam.attempt.unavailable" ||
		f.content.openAttemptID.IsValid() || f.content.starterID.IsValid() {
		t.Fatalf("error=%v starter=%s attempt=%s", err, f.content.starterID, f.content.openAttemptID)
	}
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

	page, err := f.service.ListSubmissionManifest(context.Background(), f.call, ListSubmissionManifestQuery{
		GetSubmissionQuery: GetSubmissionQuery{ExamID: f.sitting.ExamID, SittingID: f.sitting.ID,
			AttemptID: f.attemptID, SubmissionID: submission.ID}, AfterEntryID: ids[0], Limit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.SubmissionID != submission.ID || len(page.Items) != 2 || page.Items[1].Path != "src/main.go" ||
		f.submissions.manifestOptions.AfterEntryID != ids[0] || f.submissions.manifestOptions.Limit != 2 {
		t.Fatalf("page=%#v options=%#v", page, f.submissions.manifestOptions)
	}
}

func TestGetSubmissionAuthorizesCanonicalResourceBeforeProtectedRead(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	submission := submissionFixture(t, f, 5)
	f.submissions.authorization = &store.ExamSubmissionAuthorization{SubmissionID: submission.ID,
		ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, AttemptID: f.attemptID, CandidateUserID: model.NewUserID(), AcademicUnitID: model.NewAcademicUnitID()}
	f.submissions.submission = submission

	got, err := f.service.GetSubmission(context.Background(), f.call, GetSubmissionQuery{ExamID: f.sitting.ExamID,
		SittingID: f.sitting.ID, AttemptID: f.attemptID, SubmissionID: submission.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Authorization != *f.submissions.authorization || got.Submission.ID != submission.ID ||
		f.submissionAuthorizationID != submission.ID {
		t.Fatalf("got=%#v authorized=%s", got, f.submissionAuthorizationID)
	}
	if order := strings.Join(f.order, ","); order != "submission.authorize,submission.resolve_owner,submission.get" {
		t.Fatalf("order=%s", order)
	}
}

func submissionFixture(t *testing.T, f *fixture, cursor int64) *model.ExamSubmission {
	t.Helper()
	manifest, err := model.NewExamSubmissionManifest(cursor, nil)
	if err != nil {
		t.Fatal(err)
	}
	submission, err := model.NewExamSubmission(model.ExamSubmissionSpecification{ID: model.NewSubmissionID(),
		AttemptID: f.attemptID, ExamRevisionID: model.NewExamRevisionID(), WorkspaceID: model.NewExamAttemptWorkspaceID(), Manifest: manifest,
		BrowserActivity: model.BrowserActivitySubmission{State: model.BrowserActivitySubmissionNotApplicable},
		Provenance:      model.ExamSubmissionCandidateSubmitted, SubmittedAt: f.at.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
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

func (fake *submissionMailFake) PrepareSubmissionReceipt(_ context.Context, request SubmissionMailPreparation) (*PreparedSubmissionMail, error) {
	fake.f.order = append(fake.f.order, "submission.mail")
	fake.request = request
	if fake.err != nil {
		return nil, fake.err
	}
	return &PreparedSubmissionMail{Notice: &store.PreparedMail{Occurrence: &model.MailOccurrence{},
		Delivery: &model.MailDelivery{}, Job: &model.Job{}}, ExpectedRecipientRevision: 2}, nil
}

func (fake *submissionStoreFake) ListAutomaticSealTargets(_ context.Context, options store.ExamSubmissionAutomaticSealListOptions) ([]store.ExamSubmissionAutomaticSealTarget, error) {
	fake.automaticOptions = options
	return fake.automaticTargets, fake.err
}

func (fake *submissionStoreFake) PrepareAutomaticSeal(context.Context, store.ExamSubmissionAutomaticSealTarget) (*store.ExamSubmissionAutomaticSealPreparation, error) {
	return &store.ExamSubmissionAutomaticSealPreparation{Replayed: fake.automaticResult != nil && fake.automaticResult.Replayed,
		SealAt: fake.f.at}, fake.err
}

func (fake *submissionStoreFake) PrepareManagerEnd(_ context.Context, request store.ExamSubmissionManagerEndRequest) (*store.ExamSubmissionManagerEndPreparation, error) {
	fake.managerRequest = request
	return fake.managerPreparation, fake.err
}

func (fake *submissionStoreFake) EndByManager(_ context.Context, input *store.ExamSubmissionManagerEnd,
	idempotency *store.CommandIdempotency,
) (*store.ExamSubmissionManagerEndResult, error) {
	fake.managerInput, fake.idempotency = input, idempotency
	return fake.managerResult, fake.err
}

func (fake *submissionStoreFake) SealForSittingClose(_ context.Context, input *store.ExamSubmissionAutomaticSeal) (*store.ExamSubmissionAutomaticSealResult, error) {
	fake.automaticInput = input
	return fake.automaticResult, fake.err
}

func (fake *submissionStoreFake) ResolveSealTarget(_ context.Context, access store.ExamSubmissionSealAccess) (*store.ExamSubmissionSealTarget, error) {
	fake.f.order = append(fake.f.order, "submission.resolve")
	fake.resolvedAccess = access
	return fake.target, fake.err
}

func (fake *submissionStoreFake) Seal(_ context.Context, input *store.ExamSubmissionSeal, command *store.CommandIdempotency) (*store.ExamSubmissionSealResult, error) {
	fake.f.order = append(fake.f.order, "submission.seal")
	fake.seal, fake.idempotency = input, command
	return fake.sealResult, fake.err
}

func (fake *submissionStoreFake) Resolve(context.Context, model.SubmissionID) (*store.ExamSubmissionAuthorization, error) {
	fake.f.order = append(fake.f.order, "submission.resolve_owner")
	return fake.authorization, fake.err
}

func (fake *submissionStoreFake) Get(context.Context, model.SubmissionID) (*model.ExamSubmission, error) {
	fake.f.order = append(fake.f.order, "submission.get")
	return fake.submission, fake.err
}

func (fake *submissionStoreFake) ListManifest(_ context.Context, options store.ExamSubmissionManifestListOptions) (*store.ExamSubmissionManifestPage, error) {
	fake.manifestOptions = options
	return fake.manifest, fake.err
}

func (fake *submissionStoreFake) ResolveFile(context.Context, model.SubmissionID, model.AttemptWorkspaceEntryID) (*store.ExamSubmissionFileSelector, error) {
	fake.f.order = append(fake.f.order, "submission.resolve_file")
	return fake.file, fake.err
}
