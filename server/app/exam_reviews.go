// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"fmt"

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

type SaveExamIntegrityDecisionCommand struct {
	SubmissionID             model.SubmissionID
	ReviewID                 model.SubmissionReviewID
	FlagID                   model.IntegrityFlagID
	ExpectedReviewRevision   int64
	ExpectedDecisionRevision int64
	Outcome                  model.IntegrityReviewOutcome
	PrivateRationale         string
	IdempotencyKey           string
}

type UpdateExamIntegrityReviewCommand struct {
	SubmissionID           model.SubmissionID
	ReviewID               model.SubmissionReviewID
	ExpectedReviewRevision int64
	ManagerNotes           string
	StudentRemarksMarkdown string
	IdempotencyKey         string
}

type FinalizeExamIntegrityReviewCommand struct {
	SubmissionID           model.SubmissionID
	ReviewID               model.SubmissionReviewID
	ExpectedReviewRevision int64
	IdempotencyKey         string
}

type ReleaseStudentExamResultCommand = FinalizeExamIntegrityReviewCommand

type ListExamIntegrityFlagsQuery struct {
	SubmissionID model.SubmissionID
	AfterFlagID  model.IntegrityFlagID
	Limit        int
}

type ListExamIntegrityEvidenceQuery struct {
	SubmissionID    model.SubmissionID
	FlagID          model.IntegrityFlagID
	AfterEvidenceID model.IntegrityEvidenceID
	Limit           int
}

type ListExamIntegrityDiscrepanciesQuery struct {
	SubmissionID       model.SubmissionID
	AfterDiscrepancyID model.IntegrityDiscrepancyID
	Limit              int
}

type examReviewUseCases interface {
	SaveDecision(context.Context, examreview.Call, examreview.SaveDecisionCommand) (examreview.Result, error)
	UpdateDraft(context.Context, examreview.Call, examreview.UpdateDraftCommand) (examreview.Result, error)
	Finalize(context.Context, examreview.Call, examreview.FinalizeCommand) (examreview.Result, error)
	Release(context.Context, examreview.Call, examreview.ReleaseCommand) (examreview.Result, error)
	Get(context.Context, examreview.Call, examreview.GetReviewQuery) (*store.ExamSubmissionReviewSnapshot, error)
	ListFlags(context.Context, examreview.Call, examreview.ListFlagsQuery) (*store.ExamIntegrityFlagPage, error)
	ListEvidence(context.Context, examreview.Call, examreview.ListEvidenceQuery) (*store.ExamIntegrityEvidencePage, error)
	ListDiscrepancies(context.Context, examreview.Call, examreview.ListDiscrepanciesQuery) (*store.ExamIntegrityDiscrepancyPage, error)
	GetStudentResult(context.Context, examreview.Call, examreview.GetStudentResultQuery) (*model.StudentResult, error)
}

func (a *App) SaveExamIntegrityDecision(ctx context.Context, invocation Invocation,
	command SaveExamIntegrityDecisionCommand,
) (ExamIntegrityReviewResult, error) {
	idempotency, err := newCommandIdempotency(invocation, store.ExamIntegrityReviewDecisionOperation,
		command.IdempotencyKey, struct {
			SubmissionID             string `json:"submission_id"`
			ReviewID                 string `json:"submission_review_id,omitempty"`
			FlagID                   string `json:"integrity_flag_id"`
			ExpectedReviewRevision   int64  `json:"expected_review_revision"`
			ExpectedDecisionRevision int64  `json:"expected_decision_revision"`
			Outcome                  string `json:"outcome"`
			PrivateRationale         string `json:"private_rationale"`
		}{command.SubmissionID.String(), command.ReviewID.String(), command.FlagID.String(),
			command.ExpectedReviewRevision, command.ExpectedDecisionRevision, string(command.Outcome), command.PrivateRationale})
	if err != nil {
		return ExamIntegrityReviewResult{}, err
	}
	result, err := a.examReviews.SaveDecision(ctx, examreview.NewCall(invocation.Principal(), invocation.RequestMetadata()),
		examreview.SaveDecisionCommand{SubmissionID: command.SubmissionID, ReviewID: command.ReviewID,
			FlagID: command.FlagID, ExpectedReviewRevision: command.ExpectedReviewRevision,
			ExpectedDecisionRevision: command.ExpectedDecisionRevision, Outcome: command.Outcome,
			PrivateRationale: command.PrivateRationale, Idempotency: idempotency})
	if err != nil {
		return ExamIntegrityReviewResult{}, examReviewError(err, true)
	}
	return result, nil
}

func (a *App) UpdateExamIntegrityReview(ctx context.Context, invocation Invocation,
	command UpdateExamIntegrityReviewCommand,
) (ExamIntegrityReviewResult, error) {
	idempotency, err := newCommandIdempotency(invocation, store.ExamIntegrityReviewDraftOperation,
		command.IdempotencyKey, struct {
			SubmissionID           string `json:"submission_id"`
			ReviewID               string `json:"submission_review_id,omitempty"`
			ExpectedReviewRevision int64  `json:"expected_review_revision"`
			ManagerNotes           string `json:"manager_notes"`
			StudentRemarksMarkdown string `json:"student_remarks_markdown"`
		}{command.SubmissionID.String(), command.ReviewID.String(), command.ExpectedReviewRevision,
			command.ManagerNotes, command.StudentRemarksMarkdown})
	if err != nil {
		return ExamIntegrityReviewResult{}, err
	}
	result, err := a.examReviews.UpdateDraft(ctx, examreview.NewCall(invocation.Principal(), invocation.RequestMetadata()),
		examreview.UpdateDraftCommand{SubmissionID: command.SubmissionID, ReviewID: command.ReviewID,
			ExpectedReviewRevision: command.ExpectedReviewRevision, ManagerNotes: command.ManagerNotes,
			StudentRemarksMarkdown: command.StudentRemarksMarkdown, Idempotency: idempotency})
	if err != nil {
		return ExamIntegrityReviewResult{}, examReviewError(err, true)
	}
	return result, nil
}

func (a *App) FinalizeExamIntegrityReview(ctx context.Context, invocation Invocation,
	command FinalizeExamIntegrityReviewCommand,
) (ExamIntegrityReviewResult, error) {
	idempotency, err := newExamReviewTerminalIdempotency(invocation, store.ExamIntegrityReviewFinalizeOperation,
		command.IdempotencyKey, command.SubmissionID, command.ReviewID, command.ExpectedReviewRevision)
	if err != nil {
		return ExamIntegrityReviewResult{}, err
	}
	result, err := a.examReviews.Finalize(ctx, examreview.NewCall(invocation.Principal(), invocation.RequestMetadata()),
		examreview.FinalizeCommand{SubmissionID: command.SubmissionID, ReviewID: command.ReviewID,
			ExpectedReviewRevision: command.ExpectedReviewRevision, Idempotency: idempotency})
	if err != nil {
		return ExamIntegrityReviewResult{}, examReviewError(err, true)
	}
	return result, nil
}

func (a *App) ReleaseStudentExamResult(ctx context.Context, invocation Invocation,
	command ReleaseStudentExamResultCommand,
) (ExamIntegrityReviewResult, error) {
	idempotency, err := newExamReviewTerminalIdempotency(invocation, store.ExamIntegrityReviewReleaseOperation,
		command.IdempotencyKey, command.SubmissionID, command.ReviewID, command.ExpectedReviewRevision)
	if err != nil {
		return ExamIntegrityReviewResult{}, err
	}
	result, err := a.examReviews.Release(ctx, examreview.NewCall(invocation.Principal(), invocation.RequestMetadata()),
		examreview.ReleaseCommand{SubmissionID: command.SubmissionID, ReviewID: command.ReviewID,
			ExpectedReviewRevision: command.ExpectedReviewRevision, Idempotency: idempotency})
	if err != nil {
		return ExamIntegrityReviewResult{}, examReviewError(err, true)
	}
	return result, nil
}

func newExamReviewTerminalIdempotency(invocation Invocation, operation, key string, submissionID model.SubmissionID,
	reviewID model.SubmissionReviewID, expectedRevision int64,
) (*store.CommandIdempotency, error) {
	return newCommandIdempotency(invocation, operation, key, struct {
		SubmissionID     string `json:"submission_id"`
		ReviewID         string `json:"submission_review_id"`
		ExpectedRevision int64  `json:"expected_review_revision"`
	}{submissionID.String(), reviewID.String(), expectedRevision})
}

func (a *App) GetExamIntegrityReview(ctx context.Context, invocation Invocation,
	submissionID model.SubmissionID,
) (*ExamSubmissionReviewSnapshot, error) {
	result, err := a.examReviews.Get(ctx, examreview.NewCall(invocation.Principal(), invocation.RequestMetadata()),
		examreview.GetReviewQuery{SubmissionID: submissionID})
	if err != nil {
		return nil, examReviewError(err, true)
	}
	return result, nil
}

func (a *App) ListExamIntegrityFlags(ctx context.Context, invocation Invocation,
	query ListExamIntegrityFlagsQuery,
) (*ExamIntegrityFlagPage, error) {
	result, err := a.examReviews.ListFlags(ctx, examreview.NewCall(invocation.Principal(), invocation.RequestMetadata()),
		examreview.ListFlagsQuery(query))
	if err != nil {
		return nil, examReviewError(err, true)
	}
	return result, nil
}

func (a *App) ListExamIntegrityEvidence(ctx context.Context, invocation Invocation,
	query ListExamIntegrityEvidenceQuery,
) (*ExamIntegrityEvidencePage, error) {
	result, err := a.examReviews.ListEvidence(ctx, examreview.NewCall(invocation.Principal(), invocation.RequestMetadata()),
		examreview.ListEvidenceQuery(query))
	if err != nil {
		return nil, examReviewError(err, true)
	}
	return result, nil
}

func (a *App) ListExamIntegrityDiscrepancies(ctx context.Context, invocation Invocation,
	query ListExamIntegrityDiscrepanciesQuery,
) (*ExamIntegrityDiscrepancyPage, error) {
	result, err := a.examReviews.ListDiscrepancies(ctx,
		examreview.NewCall(invocation.Principal(), invocation.RequestMetadata()), examreview.ListDiscrepanciesQuery(query))
	if err != nil {
		return nil, examReviewError(err, true)
	}
	return result, nil
}

func (a *App) GetStudentExamResult(ctx context.Context, invocation Invocation,
	attemptID model.ExamAttemptID,
) (*StudentExamResult, error) {
	result, err := a.examReviews.GetStudentResult(ctx,
		examreview.NewCall(invocation.Principal(), invocation.RequestMetadata()),
		examreview.GetStudentResultQuery{AttemptID: attemptID})
	if err != nil {
		return nil, examReviewError(err, true)
	}
	return result, nil
}

func examReviewError(err error, conceal bool) error {
	if err == nil {
		return nil
	}
	if existing, ok := As(err); ok {
		if conceal && existing.Code() == "authorization.denied" {
			return NewError("resource.not_found").Wrap(err)
		}
		return err
	}
	var fault *examreview.Fault
	if !errors.As(err, &fault) {
		return NewError("exam.integrity_review.unavailable").Wrap(err)
	}
	if conceal && fault.Code == "exam.integrity_review.not_found" {
		return NewError("resource.not_found").Wrap(err)
	}
	mapped := NewError(fault.Code)
	for key, value := range fault.SafeFields {
		mapped.WithField(key, fmt.Sprint(value))
	}
	return mapped.Wrap(err)
}

type examIntegrityReviewAuthorizationAdapter struct {
	reviews  store.ExamIntegrityReviewStore
	sittings examSittingUseCases
}

func (adapter examIntegrityReviewAuthorizationAdapter) AuthorizeView(ctx context.Context, call examreview.Call,
	submissionID model.SubmissionID,
) error {
	authorization, err := adapter.resolve(ctx, submissionID)
	if err != nil {
		return err
	}
	return adapter.sittings.AuthorizeSubmissionView(ctx,
		examsitting.NewCall(call.Principal(), call.RequestMetadata()), authorization.ExamID, submissionID)
}

func (adapter examIntegrityReviewAuthorizationAdapter) AuthorizeReview(ctx context.Context, call examreview.Call,
	submissionID model.SubmissionID,
) (bool, error) {
	authorization, err := adapter.resolve(ctx, submissionID)
	if err != nil {
		return false, err
	}
	return adapter.sittings.AuthorizeSubmissionReview(ctx,
		examsitting.NewCall(call.Principal(), call.RequestMetadata()), authorization.ExamID, submissionID)
}

func (adapter examIntegrityReviewAuthorizationAdapter) AuthorizeRelease(ctx context.Context, call examreview.Call,
	submissionID model.SubmissionID,
) (bool, error) {
	authorization, err := adapter.resolve(ctx, submissionID)
	if err != nil {
		return false, err
	}
	return adapter.sittings.AuthorizeSubmissionRelease(ctx,
		examsitting.NewCall(call.Principal(), call.RequestMetadata()), authorization.ExamID, submissionID)
}

func (adapter examIntegrityReviewAuthorizationAdapter) resolve(ctx context.Context,
	submissionID model.SubmissionID,
) (*store.ExamIntegrityReviewAuthorization, error) {
	if adapter.reviews == nil || adapter.sittings == nil || !submissionID.IsValid() {
		return nil, &examreview.Fault{Code: "exam.integrity_review.unavailable", Cause: errors.New("Review authorization dependencies are invalid")}
	}
	authorization, err := adapter.reviews.Resolve(ctx, submissionID)
	if err != nil {
		if store.IsNotFound(err) {
			return nil, &examreview.Fault{Code: "exam.integrity_review.not_found", Cause: err}
		}
		return nil, &examreview.Fault{Code: "exam.integrity_review.unavailable", Cause: err}
	}
	if authorization == nil || authorization.SubmissionID != submissionID || !authorization.ExamID.IsValid() ||
		!authorization.SittingID.IsValid() || !authorization.AttemptID.IsValid() || !authorization.CandidateUserID.IsValid() ||
		!authorization.AcademicUnitID.IsValid() {
		return nil, &examreview.Fault{Code: "exam.integrity_review.unavailable", Cause: errors.New("Review authorization projection is incomplete")}
	}
	return authorization, nil
}

type examIntegrityReviewAuditAdapter struct{ audit mutationAuditAdapter }

func (adapter examIntegrityReviewAuditAdapter) Begin(ctx context.Context, call examreview.Call, action model.Action,
	resource model.Resource, scopeType model.RoleScopeType, scopeID, operation string, value map[string]any,
) (string, error) {
	return adapter.audit.BeginAtScope(ctx, NewInvocation(call.Principal(), call.RequestMetadata()), action, resource,
		scopeType, scopeID, operation, value, nil)
}

func (adapter examIntegrityReviewAuditAdapter) Fail(ctx context.Context, id, code string) error {
	return adapter.audit.Fail(ctx, id, code)
}

type examIntegrityReviewRealtimeEffects struct{ realtime *realtimeService }

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
	return effects.realtime.Publish(ctx, event)
}

func (effects examIntegrityReviewRealtimeEffects) ReviewFinalized(ctx context.Context, result examreview.Result) error {
	event, err := apprealtime.NewExamIntegrityReviewFinalizedEvent(examIntegrityReviewEventFact(result))
	if err != nil {
		return err
	}
	return effects.realtime.Publish(ctx, event)
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
	return errors.Join(effects.realtime.Publish(ctx, manager), effects.realtime.Publish(ctx, candidate))
}

func (effects examIntegrityReviewRealtimeEffects) Report(ctx context.Context, operation string, err error) {
	effects.realtime.reportTransientFailure(ctx, operation, err)
}
