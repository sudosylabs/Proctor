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
	"github.com/sudosylabs/proctor/server/app/invocation"
	apprealtime "github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type examIntegrityReviewAuthorizationAdapter struct {
	reviews  store.ExamIntegrityReviewStore
	sittings *examsitting.Service
	audit    authorizationDecisionAudit
}

func (a examIntegrityReviewAuthorizationAdapter) AuthorizeView(ctx context.Context, invocation invocation.Invocation,
	submissionID model.SubmissionID,
) error {
	authorization, err := a.resolve(ctx, submissionID)
	if err != nil {
		return err
	}
	if authorization.CandidateUserID == invocation.Principal().UserID {
		return a.denySelf(ctx, invocation, authorization, model.ActionSubmissionView)
	}
	return a.sittings.AuthorizeSubmissionView(ctx,
		invocation, authorization.ExamID, submissionID)
}

func (a examIntegrityReviewAuthorizationAdapter) AuthorizeReview(ctx context.Context, invocation invocation.Invocation,
	submissionID model.SubmissionID,
) (bool, error) {
	authorization, err := a.resolve(ctx, submissionID)
	if err != nil {
		return false, err
	}
	if authorization.CandidateUserID == invocation.Principal().UserID {
		return false, a.denySelf(ctx, invocation, authorization, model.ActionSubmissionReview)
	}
	return a.sittings.AuthorizeSubmissionReview(ctx,
		invocation, authorization.ExamID, submissionID)
}

func (a examIntegrityReviewAuthorizationAdapter) AuthorizeRelease(ctx context.Context, invocation invocation.Invocation,
	submissionID model.SubmissionID,
) (bool, error) {
	authorization, err := a.resolve(ctx, submissionID)
	if err != nil {
		return false, err
	}
	if authorization.CandidateUserID == invocation.Principal().UserID {
		return false, a.denySelf(ctx, invocation, authorization, model.ActionSubmissionRelease)
	}
	return a.sittings.AuthorizeSubmissionRelease(ctx,
		invocation, authorization.ExamID, submissionID)
}

func (a examIntegrityReviewAuthorizationAdapter) resolve(ctx context.Context,
	submissionID model.SubmissionID,
) (*store.ExamIntegrityReviewAuthorization, error) {
	if a.reviews == nil || a.sittings == nil || a.audit == nil || !submissionID.IsValid() {
		return nil, apperror.New("exam.integrity_review.unavailable").Wrap(errors.New("Review authorization dependencies are invalid"))
	}
	authorization, err := a.reviews.Resolve(ctx, submissionID)
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

func (a examIntegrityReviewAuthorizationAdapter) denySelf(ctx context.Context, invocation invocation.Invocation,
	authorization *store.ExamIntegrityReviewAuthorization, action model.Action,
) error {
	err := a.audit.RecordAuthorizationDecision(ctx, invocation.Principal(), action,
		model.Resource{Type: model.ResourceSubmission, ID: authorization.SubmissionID.String()},
		model.RoleScopeAcademicUnit, authorization.AcademicUnitID.String(), invocation.RequestMetadata(), false)
	if err != nil {
		return err
	}
	return apperror.New("exam.integrity_review.not_found")
}

type examIntegrityReviewRealtimeEffects struct{ realtime *realtimeService }

func (e examIntegrityReviewRealtimeEffects) publishManagerBoardInvalidation(ctx context.Context,
	result examreview.Result,
) error {
	event, err := apprealtime.NewManagerSittingBoardChangedEvent(result.Authorization.ExamID,
		result.Authorization.SittingID)
	if err != nil {
		return err
	}
	return e.realtime.PublishRealtimeEvent(ctx, event)
}

func examIntegrityReviewEventFact(result examreview.Result) apprealtime.ExamIntegrityReviewEventFact {
	return apprealtime.ExamIntegrityReviewEventFact{SubmissionID: result.Authorization.SubmissionID,
		AttemptID: result.Authorization.AttemptID, CandidateID: result.Authorization.CandidateUserID,
		ReviewID: result.Review.ID, State: result.Review.State, Revision: result.Review.Revision,
		ReleaseState: result.Review.ReleaseState, ChangedAt: result.Review.UpdatedAt}
}

func (e examIntegrityReviewRealtimeEffects) ReviewChanged(ctx context.Context, result examreview.Result) error {
	event, err := apprealtime.NewExamIntegrityReviewChangedEvent(examIntegrityReviewEventFact(result))
	if err != nil {
		return err
	}
	return errors.Join(e.realtime.PublishRealtimeEvent(ctx, event), e.publishManagerBoardInvalidation(ctx, result))
}

func (e examIntegrityReviewRealtimeEffects) ReviewFinalized(ctx context.Context, result examreview.Result) error {
	event, err := apprealtime.NewExamIntegrityReviewFinalizedEvent(examIntegrityReviewEventFact(result))
	if err != nil {
		return err
	}
	return errors.Join(e.realtime.PublishRealtimeEvent(ctx, event), e.publishManagerBoardInvalidation(ctx, result))
}

func (e examIntegrityReviewRealtimeEffects) ResultReleased(ctx context.Context, result examreview.Result) error {
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
	return errors.Join(e.realtime.PublishRealtimeEvent(ctx, manager), e.realtime.PublishRealtimeEvent(ctx, candidate),
		e.realtime.PublishRealtimeEvent(ctx, activity), e.publishManagerBoardInvalidation(ctx, result))
}

func (e examIntegrityReviewRealtimeEffects) Report(ctx context.Context, operation string, err error) {
	e.realtime.reportTransientFailure(ctx, operation, err)
}
