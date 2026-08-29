// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"testing"

	examattempt "github.com/sudosylabs/proctor/server/app/exam/attempt"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestSubmitExamAttemptBuildsSemanticFingerprintAndReturnsOnlySafeReceipt(t *testing.T) {
	t.Parallel()

	access := examattempt.WorkspaceMutationAccess{CandidateAccess: examattempt.CandidateAccess{
		AttemptID: model.NewExamAttemptID(), ConnectionID: model.NewAttemptConnectionID(),
		ContinuityCredential: model.NewCredentialToken()}, ParticipationID: model.NewAttemptParticipationID(), Generation: 2}
	revisionID := model.NewExamRevisionID()
	receipt := store.ExamSubmissionReceipt{SubmissionID: model.NewSubmissionID(), AttemptID: access.AttemptID, ExamRevisionID: revisionID,
		State: model.ExamAttemptSubmitted, WorkspaceCursor: 8, ManifestDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	fake := &examSubmissionFacadeFake{submitResult: examattempt.SubmissionResult{Receipt: receipt}}
	application := &App{examAttempts: fake}
	command := SubmitExamAttemptCommand{Access: access, ExpectedCurrentRevisionID: revisionID, ExpectedWorkspaceCursor: 8,
		FinalFocusLossSequence: 5, BrowserActivity: model.BrowserActivitySubmission{State: model.BrowserActivitySubmissionNotApplicable},
		IdempotencyKey: "submit-once"}
	got, err := application.SubmitExamAttempt(context.Background(), NewInvocation(examAttemptPrincipal(), model.RequestMetadata{}), command)
	if err != nil {
		t.Fatal(err)
	}
	if got != receipt || len(fake.submits) != 1 || fake.submits[0].ExpectedCurrentRevisionID != revisionID ||
		fake.submits[0].ExpectedWorkspaceCursor != 8 || fake.submits[0].BrowserActivity.State != model.BrowserActivitySubmissionNotApplicable ||
		fake.submits[0].FinalFocusLossSequence != 5 || fake.submits[0].IdempotencyKey != "submit-once" {
		t.Fatalf("receipt=%#v submits=%#v", got, fake.submits)
	}
}

func TestSubmitExamAttemptRequiresIdempotencyBeforeChildCall(t *testing.T) {
	t.Parallel()
	fake := &examSubmissionFacadeFake{err: &examattempt.Fault{Code: "idempotency.key_required"}}
	application := &App{examAttempts: fake}
	_, err := application.SubmitExamAttempt(context.Background(), NewInvocation(examAttemptPrincipal(), model.RequestMetadata{}),
		SubmitExamAttemptCommand{})
	if !Is(err, "idempotency.key_required") || len(fake.submits) != 1 {
		t.Fatalf("error=%v submits=%d", err, len(fake.submits))
	}
}

type examSubmissionFacadeFake struct {
	examAttemptUseCases
	submits      []examattempt.SubmitCommand
	submitResult examattempt.SubmissionResult
	err          error
}

func (fake *examSubmissionFacadeFake) Submit(_ context.Context, _ examattempt.Call,
	command examattempt.SubmitCommand,
) (examattempt.SubmissionResult, error) {
	fake.submits = append(fake.submits, command)
	return fake.submitResult, fake.err
}

func (*examSubmissionFacadeFake) GetSubmission(context.Context, examattempt.Call,
	examattempt.GetSubmissionQuery,
) (*examattempt.ManagedSubmission, error) {
	return nil, nil
}

func (*examSubmissionFacadeFake) ListSubmissionManifest(context.Context, examattempt.Call,
	examattempt.ListSubmissionManifestQuery,
) (examattempt.SubmissionManifestPage, error) {
	return examattempt.SubmissionManifestPage{}, nil
}

func (*examSubmissionFacadeFake) OpenSubmissionFile(context.Context, examattempt.Call,
	examattempt.OpenSubmissionFileQuery,
) (*examattempt.OpenedContent, error) {
	return nil, nil
}
