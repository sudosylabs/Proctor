// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"errors"

	"github.com/sudosylabs/proctor/server/app/apperror"
	examreview "github.com/sudosylabs/proctor/server/app/exam/review"
	apprealtime "github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type ExamIntegrityReviewResult = examreview.Result
type ExamSubmissionReviewSnapshot = store.ExamSubmissionReviewSnapshot
type ExamIntegrityFlagPage = store.ExamIntegrityFlagPage
type ExamIntegrityEvidencePage = store.ExamIntegrityEvidencePage
type ExamIntegrityDiscrepancyPage = store.ExamIntegrityDiscrepancyPage
type StudentExamResult = model.StudentResult

type SaveExamIntegrityDecisionCommand = examreview.SaveDecisionCommand

type UpdateExamIntegrityReviewCommand = examreview.UpdateDraftCommand

type FinalizeExamIntegrityReviewCommand = examreview.FinalizeCommand

type ReleaseStudentExamResultCommand = FinalizeExamIntegrityReviewCommand

type ListExamIntegrityFlagsQuery = examreview.ListFlagsQuery

type ListExamIntegrityEvidenceQuery = examreview.ListEvidenceQuery

type ListExamIntegrityDiscrepanciesQuery = examreview.ListDiscrepanciesQuery

func (a *App) SaveExamIntegrityDecision(ctx context.Context, invocation Invocation,
	command SaveExamIntegrityDecisionCommand,
) (ExamIntegrityReviewResult, error) {
	result, err := a.examReviews.SaveDecision(ctx, invocation, command)
	if err != nil {
		return ExamIntegrityReviewResult{}, examReviewError(err, true)
	}
	return result, nil
}

func (a *App) UpdateExamIntegrityReview(ctx context.Context, invocation Invocation,
	command UpdateExamIntegrityReviewCommand,
) (ExamIntegrityReviewResult, error) {
	result, err := a.examReviews.UpdateDraft(ctx, invocation, command)
	if err != nil {
		return ExamIntegrityReviewResult{}, examReviewError(err, true)
	}
	return result, nil
}

func (a *App) FinalizeExamIntegrityReview(ctx context.Context, invocation Invocation,
	command FinalizeExamIntegrityReviewCommand,
) (ExamIntegrityReviewResult, error) {
	result, err := a.examReviews.Finalize(ctx, invocation, command)
	if err != nil {
		return ExamIntegrityReviewResult{}, examReviewError(err, true)
	}
	return result, nil
}

func (a *App) ReleaseStudentExamResult(ctx context.Context, invocation Invocation,
	command ReleaseStudentExamResultCommand,
) (ExamIntegrityReviewResult, error) {
	result, err := a.examReviews.Release(ctx, invocation, examreview.ReleaseCommand(command))
	if err != nil {
		return ExamIntegrityReviewResult{}, examReviewError(err, true)
	}
	return result, nil
}

func (a *App) GetExamIntegrityReview(ctx context.Context, invocation Invocation,
	submissionID model.SubmissionID,
) (*ExamSubmissionReviewSnapshot, error) {
	result, err := a.examReviews.Get(ctx, invocation,
		examreview.GetReviewQuery{SubmissionID: submissionID})
	if err != nil {
		return nil, examReviewError(err, true)
	}
	return result, nil
}

func (a *App) ListExamIntegrityFlags(ctx context.Context, invocation Invocation,
	query ListExamIntegrityFlagsQuery,
) (*ExamIntegrityFlagPage, error) {
	result, err := a.examReviews.ListFlags(ctx, invocation,
		query)
	if err != nil {
		return nil, examReviewError(err, true)
	}
	return result, nil
}

func (a *App) ListExamIntegrityEvidence(ctx context.Context, invocation Invocation,
	query ListExamIntegrityEvidenceQuery,
) (*ExamIntegrityEvidencePage, error) {
	result, err := a.examReviews.ListEvidence(ctx, invocation,
		query)
	if err != nil {
		return nil, examReviewError(err, true)
	}
	return result, nil
}

func (a *App) ListExamIntegrityDiscrepancies(ctx context.Context, invocation Invocation,
	query ListExamIntegrityDiscrepanciesQuery,
) (*ExamIntegrityDiscrepancyPage, error) {
	result, err := a.examReviews.ListDiscrepancies(ctx,
		invocation, query)
	if err != nil {
		return nil, examReviewError(err, true)
	}
	return result, nil
}

func (a *App) GetStudentExamResult(ctx context.Context, invocation Invocation,
	attemptID model.ExamAttemptID,
) (*StudentExamResult, error) {
	result, err := a.examReviews.GetStudentResult(ctx,
		invocation,
		examreview.GetStudentResultQuery{AttemptID: attemptID})
	if err != nil {
		return nil, examReviewError(err, true)
	}
	return result, nil
}

func examReviewError(err error, conceal bool) error {
	return presentExamChildError(err, conceal, "exam.integrity_review.unavailable", "exam.integrity_review.not_found")
}

type examIntegrityReviewAuthorizationAdapter struct {
	reviews  store.ExamIntegrityReviewStore
	sittings examSittingUseCases
	audit    authorizationDecisionAudit
}

func (adapter examIntegrityReviewAuthorizationAdapter) AuthorizeView(ctx context.Context, invocation Invocation,
	submissionID model.SubmissionID,
) error {
	authorization, err := adapter.resolve(ctx, submissionID)
	if err != nil {
		return err
	}
	if authorization.CandidateUserID == invocation.Principal().UserID {
		return adapter.denySelf(ctx, invocation, authorization, model.ActionSubmissionView)
	}
	return adapter.sittings.AuthorizeSubmissionView(ctx,
		invocation, authorization.ExamID, submissionID)
}

func (adapter examIntegrityReviewAuthorizationAdapter) AuthorizeReview(ctx context.Context, invocation Invocation,
	submissionID model.SubmissionID,
) (bool, error) {
	authorization, err := adapter.resolve(ctx, submissionID)
	if err != nil {
		return false, err
	}
	if authorization.CandidateUserID == invocation.Principal().UserID {
		return false, adapter.denySelf(ctx, invocation, authorization, model.ActionSubmissionReview)
	}
	return adapter.sittings.AuthorizeSubmissionReview(ctx,
		invocation, authorization.ExamID, submissionID)
}

func (adapter examIntegrityReviewAuthorizationAdapter) AuthorizeRelease(ctx context.Context, invocation Invocation,
	submissionID model.SubmissionID,
) (bool, error) {
	authorization, err := adapter.resolve(ctx, submissionID)
	if err != nil {
		return false, err
	}
	if authorization.CandidateUserID == invocation.Principal().UserID {
		return false, adapter.denySelf(ctx, invocation, authorization, model.ActionSubmissionRelease)
	}
	return adapter.sittings.AuthorizeSubmissionRelease(ctx,
		invocation, authorization.ExamID, submissionID)
}

func (adapter examIntegrityReviewAuthorizationAdapter) resolve(ctx context.Context,
	submissionID model.SubmissionID,
) (*store.ExamIntegrityReviewAuthorization, error) {
	if adapter.reviews == nil || adapter.sittings == nil || adapter.audit == nil || !submissionID.IsValid() {
		return nil, apperror.New("exam.integrity_review.unavailable").Wrap(errors.New("Review authorization dependencies are invalid"))
	}
	authorization, err := adapter.reviews.Resolve(ctx, submissionID)
	if err != nil {
		if store.IsNotFound(err) {
			return nil, apperror.New("exam.integrity_review.not_found").Wrap(err)
		}
		return nil, apperror.New("exam.integrity_review.unavailable").Wrap(err)
	}
	if authorization == nil || authorization.SubmissionID != submissionID || !authorization.ExamID.IsValid() ||
		!authorization.SittingID.IsValid() || !authorization.AttemptID.IsValid() || !authorization.CandidateUserID.IsValid() ||
		!authorization.AcademicUnitID.IsValid() {
		return nil, apperror.New("exam.integrity_review.unavailable").Wrap(errors.New("Review authorization projection is incomplete"))
	}
	return authorization, nil
}

func (adapter examIntegrityReviewAuthorizationAdapter) denySelf(ctx context.Context, invocation Invocation,
	authorization *store.ExamIntegrityReviewAuthorization, action model.Action,
) error {
	err := adapter.audit.RecordAuthorizationDecision(ctx, invocation.Principal(), action,
		model.Resource{Type: model.ResourceSubmission, ID: authorization.SubmissionID.String()},
		model.RoleScopeAcademicUnit, authorization.AcademicUnitID.String(), invocation.RequestMetadata(), false)
	if err != nil {
		return err
	}
	return apperror.New("exam.integrity_review.not_found")
}

type examIntegrityReviewAuditAdapter struct{ audit mutationAuditAdapter }

func (adapter examIntegrityReviewAuditAdapter) Begin(ctx context.Context, invocation Invocation, action model.Action, resource model.Resource, scopeType model.RoleScopeType, scopeID, operation string, value map[string]any) (model.AuditEventID, error) {
	return adapter.audit.BeginAtScope(ctx, invocation, action, resource,
		scopeType, scopeID, operation, value, nil)
}

func (adapter examIntegrityReviewAuditAdapter) Fail(ctx context.Context, id model.AuditEventID, code string) error {
	return adapter.audit.Fail(ctx, id, code)
}

type examIntegrityReviewRealtimeEffects struct{ realtime *realtimeService }

func (effects examIntegrityReviewRealtimeEffects) publishManagerBoardInvalidation(ctx context.Context,
	result examreview.Result,
) error {
	event, err := apprealtime.NewManagerSittingBoardChangedEvent(result.Authorization.ExamID,
		result.Authorization.SittingID)
	if err != nil {
		return err
	}
	return effects.realtime.Publish(ctx, event)
}

func examIntegrityReviewEventFact(result examreview.Result) apprealtime.ExamIntegrityReviewEventFact {
	return apprealtime.ExamIntegrityReviewEventFact{SubmissionID: result.Authorization.SubmissionID,
		AttemptID: result.Authorization.AttemptID, CandidateID: result.Authorization.CandidateUserID,
		ReviewID: result.Review.ID, State: result.Review.State, Revision: result.Review.Revision,
		ReleaseState: result.Review.ReleaseState, ChangedAt: result.Review.UpdatedAt}
}

func (effects examIntegrityReviewRealtimeEffects) ReviewChanged(ctx context.Context, result examreview.Result) error {
	event, err := apprealtime.NewExamIntegrityReviewChangedEvent(examIntegrityReviewEventFact(result))
	if err != nil {
		return err
	}
	return errors.Join(effects.realtime.Publish(ctx, event), effects.publishManagerBoardInvalidation(ctx, result))
}

func (effects examIntegrityReviewRealtimeEffects) ReviewFinalized(ctx context.Context, result examreview.Result) error {
	event, err := apprealtime.NewExamIntegrityReviewFinalizedEvent(examIntegrityReviewEventFact(result))
	if err != nil {
		return err
	}
	return errors.Join(effects.realtime.Publish(ctx, event), effects.publishManagerBoardInvalidation(ctx, result))
}

func (effects examIntegrityReviewRealtimeEffects) ResultReleased(ctx context.Context, result examreview.Result) error {
	fact := examIntegrityReviewEventFact(result)
	manager, err := apprealtime.NewExamStudentResultReleasedEvent(fact)
	if err != nil {
		return err
	}
	candidate, err := apprealtime.NewCandidateStudentResultReleasedEvent(fact)
	if err != nil {
		return err
	}
	activity, err := apprealtime.NewCandidateExamActivityChangedEvent(result.Authorization.CandidateUserID)
	if err != nil {
		return err
	}
	return errors.Join(effects.realtime.Publish(ctx, manager), effects.realtime.Publish(ctx, candidate),
		effects.realtime.Publish(ctx, activity), effects.publishManagerBoardInvalidation(ctx, result))
}

func (effects examIntegrityReviewRealtimeEffects) Report(ctx context.Context, operation string, err error) {
	effects.realtime.reportTransientFailure(ctx, operation, err)
}

type NativeConditionListQuery = examreview.NativeConditionListQuery
type NativeConditionPage = store.NativeConditionPage

func (a *App) ListNativeConditions(ctx context.Context, invocation Invocation, query NativeConditionListQuery) (*NativeConditionPage, error) {
	result, err := a.examReviews.ListNativeConditions(ctx, invocation, query)
	if err != nil {
		return nil, examReviewError(err, true)
	}
	return result, nil
}
