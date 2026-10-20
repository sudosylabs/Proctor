// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"

	"github.com/sudosylabs/proctor/server/app/apperror"
	examattempt "github.com/sudosylabs/proctor/server/app/exam/attempt"
	"github.com/sudosylabs/proctor/server/store"
)

type ExamSubmissionReceipt = store.ExamSubmissionReceipt
type ExamSubmissionManagerView = examattempt.ManagedSubmission
type ExamSubmissionManifestPage = examattempt.SubmissionManifestPage
type GetExamSubmissionQuery = examattempt.GetSubmissionQuery
type ListExamSubmissionManifestQuery = examattempt.ListSubmissionManifestQuery
type OpenExamSubmissionFileQuery = examattempt.OpenSubmissionFileQuery

type SubmitExamAttemptCommand = examattempt.SubmitCommand

func (a *App) SubmitExamAttempt(ctx context.Context, invocation Invocation,
	command SubmitExamAttemptCommand,
) (response ExamSubmissionReceipt, resultErr error) {
	defer func() { a.recordOperational("exam_attempt", "submit", resultErr) }()
	result, err := a.examAttempts.Submit(ctx, invocation, command)
	if err != nil {
		return ExamSubmissionReceipt{}, examAttemptError(err, true)
	}
	return result.Receipt, nil
}

func (a *App) GetExamSubmission(ctx context.Context, invocation Invocation,
	query GetExamSubmissionQuery,
) (ExamSubmissionManagerView, error) {
	result, err := a.examAttempts.GetSubmission(ctx, invocation, query)
	if err != nil {
		return ExamSubmissionManagerView{}, examAttemptError(err, true)
	}
	if result == nil {
		return ExamSubmissionManagerView{}, apperror.New("exam.attempt.unavailable")
	}
	return *result, nil
}

func (a *App) ListExamSubmissionManifest(ctx context.Context, invocation Invocation,
	query ListExamSubmissionManifestQuery,
) (ExamSubmissionManifestPage, error) {
	result, err := a.examAttempts.ListSubmissionManifest(ctx,
		invocation, query)
	if err != nil {
		return ExamSubmissionManifestPage{}, examAttemptError(err, true)
	}
	return result, nil
}

func (a *App) OpenExamSubmissionFile(ctx context.Context, invocation Invocation,
	query OpenExamSubmissionFileQuery,
) (OpenedExamAttemptContent, error) {
	result, err := a.examAttempts.OpenSubmissionFile(ctx,
		invocation, query)
	if err != nil {
		return OpenedExamAttemptContent{}, examAttemptError(err, true)
	}
	if result == nil {
		return OpenedExamAttemptContent{}, apperror.New("exam.attempt.unavailable")
	}
	return *result, nil
}
