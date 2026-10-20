// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"

	examengine "github.com/sudosylabs/proctor/server/app/exam"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type CompleteExamSittingRecordsCommand = examengine.CompleteRecordsCommand
type WaiveSubmissionReviewCommand = examengine.WaiveReviewCommand
type CreateRetentionHoldCommand = examengine.CreateRetentionHoldCommand
type ReleaseRetentionHoldCommand = examengine.ReleaseRetentionHoldCommand
type ListRetentionHoldsQuery = store.ExamRecordsHoldListOptions
type ExamSittingRecordsSnapshot = store.ExamRecordsCompletionSnapshot
type RetentionHoldPage = store.ExamRecordsHoldPage

func (a *App) GetExamSittingRecords(ctx context.Context, invocation Invocation, examID model.ExamID, sittingID model.ExamSittingID) (*ExamSittingRecordsSnapshot, error) {
	result, err := a.examRecords.GetCompletion(ctx, invocation, examID, sittingID)
	return result, examError(err, true)
}

func (a *App) GetSubmissionReviewWaiver(ctx context.Context, invocation Invocation, scope model.RetentionHoldScope) (*model.SubmissionReviewWaiver, error) {
	result, err := a.examRecords.FindReviewWaiver(ctx, invocation, scope)
	return result, examError(err, true)
}

func (a *App) CompleteExamSittingRecords(ctx context.Context, invocation Invocation, command CompleteExamSittingRecordsCommand) (*model.ExamSittingRecordsCompletion, error) {
	result, err := a.examRecords.CompleteRecords(ctx, invocation, command)
	return result, examError(err, true)
}

func (a *App) WaiveSubmissionReview(ctx context.Context, invocation Invocation, command WaiveSubmissionReviewCommand) (*model.SubmissionReviewWaiver, error) {
	result, err := a.examRecords.WaiveReview(ctx, invocation, command)
	return result, examError(err, true)
}

func (a *App) ListRetentionHolds(ctx context.Context, invocation Invocation, query ListRetentionHoldsQuery) (*RetentionHoldPage, error) {
	result, err := a.examRecords.ListHolds(ctx, invocation, query)
	return result, examError(err, true)
}

func (a *App) CreateRetentionHold(ctx context.Context, invocation Invocation, command CreateRetentionHoldCommand) (*model.RetentionHold, error) {
	result, err := a.examRecords.CreateHold(ctx, invocation, command)
	return result, examError(err, true)
}

func (a *App) ReleaseRetentionHold(ctx context.Context, invocation Invocation, command ReleaseRetentionHoldCommand) (*model.RetentionHold, error) {
	result, err := a.examRecords.ReleaseHold(ctx, invocation, command)
	return result, examError(err, true)
}

var _ examengine.RecordsAuthorizer = (*accessControlService)(nil)
