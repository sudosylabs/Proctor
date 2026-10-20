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

type examRecordsAuthorizationAdapter struct {
	authorization *accessControlService
	audit         authorizationDecisionAudit
}

func (adapter examRecordsAuthorizationAdapter) Authorize(ctx context.Context, invocation Invocation, action model.Action, resource model.Resource) error {
	return adapter.authorization.authorizeCurrentState(ctx, invocation.Principal(), action, resource, invocation.RequestMetadata())
}

func (adapter examRecordsAuthorizationAdapter) Deny(ctx context.Context, invocation Invocation, action model.Action, resource model.Resource, unitID model.AcademicUnitID) error {
	if err := adapter.audit.RecordAuthorizationDecision(ctx, invocation.Principal(), action, resource,
		model.RoleScopeAcademicUnit, unitID.String(), invocation.RequestMetadata(), false); err != nil {
		return err
	}
	return apperror.New("exam.not_found")
}

var _ examengine.RecordsAuthorizer = examRecordsAuthorizationAdapter{}
