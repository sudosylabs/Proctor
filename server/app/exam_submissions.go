// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"

	examattempt "github.com/sudosylabs/proctor/server/app/exam/attempt"
	"github.com/sudosylabs/proctor/server/store"
)

type ExamSubmissionReceipt = store.ExamSubmissionReceipt
type ExamSubmissionManagerView = examattempt.ManagedSubmission
type ExamSubmissionManifestPage = examattempt.SubmissionManifestPage
type GetExamSubmissionQuery = examattempt.GetSubmissionQuery
type ListExamSubmissionManifestQuery = examattempt.ListSubmissionManifestQuery
type OpenExamSubmissionFileQuery = examattempt.OpenSubmissionFileQuery

type SubmitExamAttemptCommand struct {
	Access                  ExamAttemptWorkspaceMutationAccess
	ExpectedWorkspaceCursor int64
	FinalFocusLossSequence  int64
	IdempotencyKey          string
}

func (a *App) SubmitExamAttempt(ctx context.Context, invocation Invocation,
	command SubmitExamAttemptCommand,
) (response ExamSubmissionReceipt, resultErr error) {
	defer func() { a.recordOperational("exam_attempt", "submit", resultErr) }()
	result, err := a.examAttempts.Submit(ctx, examattempt.NewCall(invocation.Principal(), invocation.RequestMetadata()),
		examattempt.SubmitCommand{Access: command.Access, ExpectedWorkspaceCursor: command.ExpectedWorkspaceCursor,
			FinalFocusLossSequence: command.FinalFocusLossSequence, IdempotencyKey: command.IdempotencyKey})
	if err != nil {
		return ExamSubmissionReceipt{}, examAttemptError(err, true)
	}
	return result.Receipt, nil
}

func (a *App) GetExamSubmission(ctx context.Context, invocation Invocation,
	query GetExamSubmissionQuery,
) (ExamSubmissionManagerView, error) {
	result, err := a.examAttempts.GetSubmission(ctx, examattempt.NewCall(invocation.Principal(), invocation.RequestMetadata()), query)
	if err != nil {
		return ExamSubmissionManagerView{}, examAttemptError(err, true)
	}
	if result == nil {
		return ExamSubmissionManagerView{}, NewError("exam.attempt.unavailable")
	}
	return *result, nil
}

func (a *App) ListExamSubmissionManifest(ctx context.Context, invocation Invocation,
	query ListExamSubmissionManifestQuery,
) (ExamSubmissionManifestPage, error) {
	result, err := a.examAttempts.ListSubmissionManifest(ctx,
		examattempt.NewCall(invocation.Principal(), invocation.RequestMetadata()), query)
	if err != nil {
		return ExamSubmissionManifestPage{}, examAttemptError(err, true)
	}
	return result, nil
}

func (a *App) OpenExamSubmissionFile(ctx context.Context, invocation Invocation,
	query OpenExamSubmissionFileQuery,
) (OpenedExamAttemptContent, error) {
	result, err := a.examAttempts.OpenSubmissionFile(ctx,
		examattempt.NewCall(invocation.Principal(), invocation.RequestMetadata()), query)
	if err != nil {
		return OpenedExamAttemptContent{}, examAttemptError(err, true)
	}
	if result == nil {
		return OpenedExamAttemptContent{}, NewError("exam.attempt.unavailable")
	}
	return *result, nil
}
