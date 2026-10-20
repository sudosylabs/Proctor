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
	examsitting "github.com/sudosylabs/proctor/server/app/exam/sitting"
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

type ReleaseStudentExamResultCommand = examreview.ReleaseCommand

type ListExamIntegrityFlagsQuery = examreview.ListFlagsQuery

type ListExamIntegrityEvidenceQuery = examreview.ListEvidenceQuery

type ListExamIntegrityDiscrepanciesQuery = examreview.ListDiscrepanciesQuery

type examIntegrityReviewAuthorizationAdapter struct {
	reviews  store.ExamIntegrityReviewStore
	sittings *examsitting.Service
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

type examIntegrityReviewRealtimeEffects struct{ realtime *realtimeService }

func (effects examIntegrityReviewRealtimeEffects) publishManagerBoardInvalidation(ctx context.Context,
	result examreview.Result,
) error {
	event, err := apprealtime.NewManagerSittingBoardChangedEvent(result.Authorization.ExamID,
		result.Authorization.SittingID)
	if err != nil {
		return err
	}
	return effects.realtime.PublishRealtimeEvent(ctx, event)
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
	return errors.Join(effects.realtime.PublishRealtimeEvent(ctx, event), effects.publishManagerBoardInvalidation(ctx, result))
}

func (effects examIntegrityReviewRealtimeEffects) ReviewFinalized(ctx context.Context, result examreview.Result) error {
	event, err := apprealtime.NewExamIntegrityReviewFinalizedEvent(examIntegrityReviewEventFact(result))
	if err != nil {
		return err
	}
	return errors.Join(effects.realtime.PublishRealtimeEvent(ctx, event), effects.publishManagerBoardInvalidation(ctx, result))
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
	return errors.Join(effects.realtime.PublishRealtimeEvent(ctx, manager), effects.realtime.PublishRealtimeEvent(ctx, candidate),
		effects.realtime.PublishRealtimeEvent(ctx, activity), effects.publishManagerBoardInvalidation(ctx, result))
}

func (effects examIntegrityReviewRealtimeEffects) Report(ctx context.Context, operation string, err error) {
	effects.realtime.reportTransientFailure(ctx, operation, err)
}

type NativeConditionListQuery = examreview.NativeConditionListQuery
type NativeConditionPage = store.NativeConditionPage
