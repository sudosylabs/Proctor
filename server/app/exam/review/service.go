// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package review

import (
	"context"
	"errors"
	"time"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/exam/safemarkdown"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type Authorizer interface {
	AuthorizeView(context.Context, invocation.Invocation, model.SubmissionID) error
	AuthorizeReview(context.Context, invocation.Invocation, model.SubmissionID) (bool, error)
	AuthorizeRelease(context.Context, invocation.Invocation, model.SubmissionID) (bool, error)
}

type Auditor interface {
	Begin(context.Context, invocation.Invocation, model.Action, model.Resource, model.RoleScopeType, string, string, map[string]any) (model.AuditEventID, error)
	Fail(context.Context, model.AuditEventID, string) error
}

type Effects interface {
	ReviewChanged(context.Context, Result) error
	ReviewFinalized(context.Context, Result) error
	ResultReleased(context.Context, Result) error
}

type EffectFailures interface {
	Report(context.Context, string, error)
}

type ResultReleaseMailPreparation struct {
	ExpectedReviewRevision int64
	CandidateUserID        model.UserID
	ExamID                 model.ExamID
	SittingID              model.ExamSittingID
	ReviewID               model.SubmissionReviewID
	ReleasedAt             time.Time
}

type PreparedResultReleaseMail struct {
	Notice                    *store.PreparedMail
	ExpectedRecipientRevision int64
}

type ResultReleaseMailPreparer interface {
	PrepareResultRelease(context.Context, ResultReleaseMailPreparation) (*PreparedResultReleaseMail, error)
}

// Dependencies are the collaborators of Service; New validates them once and the
// service retains them unchanged.
type Dependencies struct {
	Persistence    store.ExamIntegrityReviewStore
	Authorizer     Authorizer
	Auditor        Auditor
	Effects        Effects
	EffectFailures EffectFailures
	Mail           ResultReleaseMailPreparer
	Now            func() time.Time
	NewReviewID    func() model.SubmissionReviewID
	NewDecisionID  func() model.IntegrityReviewDecisionID
}

type Service struct{ deps Dependencies }

func New(deps Dependencies) (*Service, error) {
	if deps.Persistence == nil || deps.Authorizer == nil || deps.Auditor == nil || deps.Effects == nil ||
		deps.EffectFailures == nil || deps.Mail == nil || deps.Now == nil || deps.NewReviewID == nil || deps.NewDecisionID == nil {
		return nil, errors.New("Exam Integrity Review dependencies are required")
	}
	return &Service{deps: deps}, nil
}

type Result struct {
	Authorization store.ExamIntegrityReviewAuthorization
	Review        *model.SubmissionReview
	Decision      *model.IntegrityReviewDecision
	Replayed      bool
}

type SaveDecisionCommand struct {
	SubmissionID             model.SubmissionID
	ReviewID                 model.SubmissionReviewID
	FlagID                   model.IntegrityFlagID
	ExpectedReviewRevision   int64
	ExpectedDecisionRevision int64
	Outcome                  model.IntegrityReviewOutcome
	PrivateRationale         string
	IdempotencyKey           string
}

func (service *Service) SaveDecision(ctx context.Context, invocation invocation.Invocation, command SaveDecisionCommand) (Result, error) {
	if !command.SubmissionID.IsValid() || !command.FlagID.IsValid() ||
		command.ExpectedReviewRevision < 0 || command.ExpectedDecisionRevision < 0 ||
		(command.ExpectedReviewRevision == 0 && !command.ReviewID.IsZero()) ||
		(command.ExpectedReviewRevision > 0 && !command.ReviewID.IsValid()) {
		return Result{}, apperror.Invalid("exam.integrity_review.invalid", "decision")
	}
	principal := invocation.Principal()
	if principal.Validate() != nil {
		return Result{}, apperror.Invalid("exam.integrity_review.invalid", "principal")
	}
	idempotency, err := prepareDecisionIdempotency(invocation, command)
	if err != nil {
		return Result{}, err
	}
	reviewID := command.ReviewID
	if reviewID.IsZero() {
		reviewID = service.deps.NewReviewID()
	}
	decisionID := service.deps.NewDecisionID()
	if _, err := model.NewIntegrityReviewDecision(decisionID, reviewID, command.FlagID, command.Outcome,
		principal.UserID, command.PrivateRationale, service.deps.Now()); err != nil {
		return Result{}, apperror.Invalid("exam.integrity_review.invalid", "decision").Wrap(err)
	}
	override, authorization, err := service.authorizeReviewMutation(ctx, invocation, command.SubmissionID)
	if err != nil {
		return Result{}, err
	}
	at := model.TimeUTC(service.deps.Now())
	auditID, err := service.deps.Auditor.Begin(ctx, invocation, model.ActionSubmissionReview,
		model.Resource{Type: model.ResourceSubmission, ID: command.SubmissionID.String()}, model.RoleScopeAcademicUnit,
		authorization.AcademicUnitID.String(), store.ExamIntegrityReviewDecisionOperation, map[string]any{
			"submission_id": command.SubmissionID.String(), "integrity_flag_id": command.FlagID.String(),
			"expected_review_revision":   command.ExpectedReviewRevision,
			"expected_decision_revision": command.ExpectedDecisionRevision, "outcome": string(command.Outcome),
		})
	if err != nil {
		return Result{}, err
	}
	stored, err := service.deps.Persistence.SaveDecision(ctx, &store.ExamIntegrityReviewDecisionMutation{
		SubmissionID: command.SubmissionID, ReviewID: reviewID, DecisionID: decisionID, FlagID: command.FlagID,
		ActorUserID: principal.UserID, ManagerOverride: override, ExpectedReviewRevision: command.ExpectedReviewRevision,
		ExpectedDecisionRevision: command.ExpectedDecisionRevision, Outcome: command.Outcome,
		PrivateRationale: command.PrivateRationale, ChangedAt: at, AuditEventID: auditID, AuditAt: model.MillisFromTime(at),
	}, idempotency)
	if err != nil {
		return Result{}, service.failAudit(ctx, auditID, err)
	}
	result, err := projectMutation(stored, command.SubmissionID)
	if err != nil {
		return Result{}, err
	}
	if !result.Replayed && (result.Review.ID != reviewID || result.Decision == nil || result.Decision.ID != decisionID) {
		return Result{}, apperror.New("exam.integrity_review.unavailable").Wrap(errors.New("unexpected fresh Review decision identities"))
	}
	service.effect(ctx, "exam_integrity_review_changed", result, result.Replayed, service.deps.Effects.ReviewChanged)
	return result, nil
}

type UpdateDraftCommand struct {
	SubmissionID           model.SubmissionID
	ReviewID               model.SubmissionReviewID
	ExpectedReviewRevision int64
	ManagerNotes           string
	StudentRemarksMarkdown string
	IdempotencyKey         string
}

func (service *Service) UpdateDraft(ctx context.Context, invocation invocation.Invocation, command UpdateDraftCommand) (Result, error) {
	if !command.SubmissionID.IsValid() || command.ExpectedReviewRevision < 0 ||
		(command.ExpectedReviewRevision == 0 && !command.ReviewID.IsZero()) ||
		(command.ExpectedReviewRevision > 0 && !command.ReviewID.IsValid()) {
		return Result{}, apperror.Invalid("exam.integrity_review.invalid", "draft")
	}
	principal := invocation.Principal()
	if principal.Validate() != nil {
		return Result{}, apperror.Invalid("exam.integrity_review.invalid", "principal")
	}
	idempotency, err := prepareDraftIdempotency(invocation, command)
	if err != nil {
		return Result{}, err
	}
	reviewID := command.ReviewID
	if reviewID.IsZero() {
		reviewID = service.deps.NewReviewID()
	}
	probe, err := model.NewSubmissionReview(reviewID, command.SubmissionID, principal.UserID, service.deps.Now())
	if err != nil {
		return Result{}, apperror.Invalid("exam.integrity_review.invalid", "draft").Wrap(err)
	}
	if err = probe.UpdateDraft(1, command.ManagerNotes, command.StudentRemarksMarkdown, service.deps.Now()); err != nil {
		return Result{}, apperror.Invalid("exam.integrity_review.invalid", "draft").Wrap(err)
	}
	override, authorization, err := service.authorizeReviewMutation(ctx, invocation, command.SubmissionID)
	if err != nil {
		return Result{}, err
	}
	at := model.TimeUTC(service.deps.Now())
	auditID, err := service.deps.Auditor.Begin(ctx, invocation, model.ActionSubmissionReview,
		model.Resource{Type: model.ResourceSubmission, ID: command.SubmissionID.String()}, model.RoleScopeAcademicUnit,
		authorization.AcademicUnitID.String(), store.ExamIntegrityReviewDraftOperation,
		map[string]any{"submission_id": command.SubmissionID.String(), "expected_review_revision": command.ExpectedReviewRevision})
	if err != nil {
		return Result{}, err
	}
	stored, err := service.deps.Persistence.UpdateDraft(ctx, &store.ExamIntegrityReviewDraftMutation{
		SubmissionID: command.SubmissionID, ReviewID: reviewID, ActorUserID: principal.UserID, ManagerOverride: override,
		ExpectedReviewRevision: command.ExpectedReviewRevision, ManagerNotes: command.ManagerNotes,
		StudentRemarksMarkdown: command.StudentRemarksMarkdown, ChangedAt: at, AuditEventID: auditID,
		AuditAt: model.MillisFromTime(at),
	}, idempotency)
	if err != nil {
		return Result{}, service.failAudit(ctx, auditID, err)
	}
	result, err := projectMutation(stored, command.SubmissionID)
	if err != nil {
		return Result{}, err
	}
	if !result.Replayed && result.Review.ID != reviewID {
		return Result{}, apperror.New("exam.integrity_review.unavailable").Wrap(errors.New("unexpected fresh Review identity"))
	}
	service.effect(ctx, "exam_integrity_review_changed", result, result.Replayed, service.deps.Effects.ReviewChanged)
	return result, nil
}

type FinalizeCommand struct {
	SubmissionID           model.SubmissionID
	ReviewID               model.SubmissionReviewID
	ExpectedReviewRevision int64
	IdempotencyKey         string
}

type terminalMutationKind uint8

const (
	terminalFinalize terminalMutationKind = iota + 1
	terminalRelease
)

func (service *Service) Finalize(ctx context.Context, invocation invocation.Invocation, command FinalizeCommand) (Result, error) {
	return service.terminalMutation(ctx, invocation, command.SubmissionID, command.ReviewID,
		command.ExpectedReviewRevision, command.IdempotencyKey, terminalFinalize)
}

type ReleaseCommand struct {
	SubmissionID           model.SubmissionID
	ReviewID               model.SubmissionReviewID
	ExpectedReviewRevision int64
	IdempotencyKey         string
}

func (service *Service) Release(ctx context.Context, invocation invocation.Invocation, command ReleaseCommand) (Result, error) {
	return service.terminalMutation(ctx, invocation, command.SubmissionID, command.ReviewID,
		command.ExpectedReviewRevision, command.IdempotencyKey, terminalRelease)
}

func (service *Service) terminalMutation(ctx context.Context, invocation invocation.Invocation, submissionID model.SubmissionID,
	reviewID model.SubmissionReviewID, expectedRevision int64, idempotencyKey string,
	kind terminalMutationKind,
) (Result, error) {
	if !submissionID.IsValid() || !reviewID.IsValid() || expectedRevision < 1 ||
		(kind != terminalFinalize && kind != terminalRelease) {
		return Result{}, apperror.Invalid("exam.integrity_review.invalid", "review")
	}
	principal := invocation.Principal()
	if principal.Validate() != nil {
		return Result{}, apperror.Invalid("exam.integrity_review.invalid", "principal")
	}
	operation := store.ExamIntegrityReviewFinalizeOperation
	if kind == terminalRelease {
		operation = store.ExamIntegrityReviewReleaseOperation
	}
	idempotency, err := prepareTerminalIdempotency(invocation, operation, idempotencyKey, submissionID, reviewID, expectedRevision)
	if err != nil {
		return Result{}, err
	}
	var override bool
	var authorization *store.ExamIntegrityReviewAuthorization
	if kind == terminalRelease {
		override, authorization, err = service.authorizeReleaseMutation(ctx, invocation, submissionID)
	} else {
		override, authorization, err = service.authorizeReviewMutation(ctx, invocation, submissionID)
	}
	if err != nil {
		return Result{}, err
	}
	action := model.ActionSubmissionReview
	if kind == terminalRelease {
		action = model.ActionSubmissionRelease
	}
	auditID, err := service.deps.Auditor.Begin(ctx, invocation, action,
		model.Resource{Type: model.ResourceSubmission, ID: submissionID.String()}, model.RoleScopeAcademicUnit,
		authorization.AcademicUnitID.String(), operation,
		map[string]any{"submission_id": submissionID.String(), "submission_review_id": reviewID.String(), "expected_review_revision": expectedRevision})
	if err != nil {
		return Result{}, err
	}
	at := model.TimeUTC(service.deps.Now())
	var releasePreparation *store.ExamIntegrityReviewReleasePreparation
	if kind == terminalRelease {
		// The release-time reservation is durable, so it follows the audit
		// attempt like every other examination write.
		releasePreparation, err = service.deps.Persistence.PrepareRelease(ctx, submissionID, reviewID, expectedRevision)
		if err != nil {
			return Result{}, service.failAudit(ctx, auditID, err)
		}
		if releasePreparation == nil || releasePreparation.ReleaseAt.IsZero() {
			return Result{}, service.failAudit(ctx, auditID, apperror.New("exam.integrity_review.unavailable").Wrap(errors.New("invalid result release preparation")))
		}
		at = model.TimeUTC(releasePreparation.ReleaseAt)
	}
	var stored *store.ExamIntegrityReviewMutationResult
	if kind == terminalRelease {
		var notice *store.PreparedMail
		var expectedRecipientRevision int64
		if !releasePreparation.Replayed {
			prepared, prepareErr := service.deps.Mail.PrepareResultRelease(ctx, ResultReleaseMailPreparation{
				CandidateUserID: authorization.CandidateUserID, ExamID: authorization.ExamID,
				SittingID: authorization.SittingID, ReviewID: reviewID, ExpectedReviewRevision: expectedRevision, ReleasedAt: at,
			})
			if prepareErr != nil || prepared == nil || prepared.Notice == nil || prepared.ExpectedRecipientRevision < 1 {
				if prepareErr == nil {
					prepareErr = errors.New("invalid result release mail preparation")
				}
				return Result{}, service.failAudit(ctx, auditID, apperror.New("exam.integrity_review.unavailable").Wrap(prepareErr))
			}
			notice, expectedRecipientRevision = prepared.Notice, prepared.ExpectedRecipientRevision
		}
		stored, err = service.deps.Persistence.Release(ctx, &store.ExamIntegrityReviewRelease{SubmissionID: submissionID,
			ReviewID: reviewID, ActorUserID: principal.UserID, ManagerOverride: override,
			ExpectedReviewRevision: expectedRevision, ChangedAt: at, AuditEventID: auditID, AuditAt: model.MillisFromTime(at),
			CandidateUserID: authorization.CandidateUserID, Notice: notice,
			ExpectedRecipientRevision: expectedRecipientRevision}, idempotency)
	} else {
		stored, err = service.deps.Persistence.Finalize(ctx, &store.ExamIntegrityReviewFinalize{SubmissionID: submissionID,
			ReviewID: reviewID, ActorUserID: principal.UserID, ManagerOverride: override,
			ExpectedReviewRevision: expectedRevision, ChangedAt: at, AuditEventID: auditID, AuditAt: model.MillisFromTime(at)}, idempotency)
	}
	if err != nil {
		return Result{}, service.failAudit(ctx, auditID, err)
	}
	result, err := projectMutation(stored, submissionID)
	if err != nil {
		return Result{}, err
	}
	if result.Review.ID != reviewID || (kind == terminalRelease && result.Review.ReleaseState != model.SubmissionReviewReleased) ||
		(kind == terminalFinalize && result.Review.State != model.SubmissionReviewFinalized) {
		return Result{}, apperror.New("exam.integrity_review.unavailable").Wrap(errors.New("inconsistent terminal Review result"))
	}
	if kind == terminalRelease {
		service.effect(ctx, "exam_integrity_result_released", result, result.Replayed, service.deps.Effects.ResultReleased)
	} else {
		service.effect(ctx, "exam_integrity_review_finalized", result, result.Replayed, service.deps.Effects.ReviewFinalized)
	}
	return result, nil
}

type GetReviewQuery struct{ SubmissionID model.SubmissionID }

func (service *Service) Get(ctx context.Context, invocation invocation.Invocation, query GetReviewQuery) (*store.ExamSubmissionReviewSnapshot, error) {
	if !query.SubmissionID.IsValid() {
		return nil, apperror.Invalid("exam.integrity_review.invalid", "submission_id")
	}
	if err := service.deps.Authorizer.AuthorizeView(ctx, invocation, query.SubmissionID); err != nil {
		return nil, err
	}
	snapshot, err := service.deps.Persistence.Get(ctx, query.SubmissionID)
	if err != nil {
		return nil, storeErrors.Translate(err)
	}
	if !validSnapshot(snapshot, query.SubmissionID) {
		return nil, apperror.New("exam.integrity_review.unavailable").Wrap(errors.New("inconsistent Review snapshot"))
	}
	return cloneSnapshot(snapshot), nil
}

type ListFlagsQuery struct {
	SubmissionID model.SubmissionID
	AfterFlagID  model.IntegrityFlagID
	Limit        int
}

func (service *Service) ListFlags(ctx context.Context, invocation invocation.Invocation, query ListFlagsQuery) (*store.ExamIntegrityFlagPage, error) {
	if !query.SubmissionID.IsValid() || query.Limit < 1 || query.Limit > store.ExamIntegrityReviewFlagReadMaximum {
		return nil, apperror.Invalid("exam.integrity_review.invalid", "flag_list")
	}
	if err := service.deps.Authorizer.AuthorizeView(ctx, invocation, query.SubmissionID); err != nil {
		return nil, err
	}
	page, err := service.deps.Persistence.ListFlags(ctx, store.ExamIntegrityFlagListOptions(query))
	if err != nil {
		return nil, storeErrors.Translate(err)
	}
	if page == nil || len(page.Items) > query.Limit || (page.HasMore && len(page.Items) != query.Limit) {
		return nil, apperror.New("exam.integrity_review.unavailable").Wrap(errors.New("inconsistent Flag page"))
	}
	clone := &store.ExamIntegrityFlagPage{Items: append([]store.ExamIntegrityFlagSummary(nil), page.Items...), HasMore: page.HasMore}
	for i := range clone.Items {
		if clone.Items[i].Browser != nil {
			if clone.Items[i].Browser.Validate() != nil {
				return nil, apperror.New("exam.integrity_review.unavailable").Wrap(errors.New("invalid Browser evidence group"))
			}
			v := clone.Items[i].Browser.Clone()
			clone.Items[i].Browser = &v
		}
	}
	return clone, nil
}

type ListEvidenceQuery struct {
	SubmissionID    model.SubmissionID
	FlagID          model.IntegrityFlagID
	AfterEvidenceID model.IntegrityEvidenceID
	Limit           int
}

func (service *Service) ListEvidence(ctx context.Context, invocation invocation.Invocation, query ListEvidenceQuery) (*store.ExamIntegrityEvidencePage, error) {
	if !query.SubmissionID.IsValid() || !query.FlagID.IsValid() || query.Limit < 1 || query.Limit > store.ExamIntegrityReviewEvidenceReadMaximum {
		return nil, apperror.Invalid("exam.integrity_review.invalid", "evidence_list")
	}
	if err := service.deps.Authorizer.AuthorizeView(ctx, invocation, query.SubmissionID); err != nil {
		return nil, err
	}
	page, err := service.deps.Persistence.ListEvidence(ctx, store.ExamIntegrityEvidenceListOptions(query))
	if err != nil {
		return nil, storeErrors.Translate(err)
	}
	if page == nil || len(page.Items) > query.Limit || (page.HasMore && len(page.Items) != query.Limit) {
		return nil, apperror.New("exam.integrity_review.unavailable").Wrap(errors.New("inconsistent Evidence page"))
	}
	clone := &store.ExamIntegrityEvidencePage{Items: append([]model.IntegrityEvidence(nil), page.Items...), HasMore: page.HasMore}
	for i := range clone.Items {
		if clone.Items[i].Browser != nil {
			if clone.Items[i].Browser.Validate() != nil {
				return nil, apperror.New("exam.integrity_review.unavailable").Wrap(errors.New("invalid Browser evidence copy"))
			}
			v := clone.Items[i].Browser.Clone()
			clone.Items[i].Browser = &v
		}
	}
	return clone, nil
}

type ListDiscrepanciesQuery struct {
	SubmissionID       model.SubmissionID
	AfterDiscrepancyID model.IntegrityDiscrepancyID
	Limit              int
}

func (service *Service) ListDiscrepancies(ctx context.Context, invocation invocation.Invocation,
	query ListDiscrepanciesQuery,
) (*store.ExamIntegrityDiscrepancyPage, error) {
	if !query.SubmissionID.IsValid() || query.Limit < 1 || query.Limit > store.ExamIntegrityReviewDiscrepancyReadMaximum {
		return nil, apperror.Invalid("exam.integrity_review.invalid", "discrepancy_list")
	}
	if err := service.deps.Authorizer.AuthorizeView(ctx, invocation, query.SubmissionID); err != nil {
		return nil, err
	}
	page, err := service.deps.Persistence.ListDiscrepancies(ctx, store.ExamIntegrityDiscrepancyListOptions(query))
	if err != nil {
		return nil, storeErrors.Translate(err)
	}
	if page == nil || len(page.Items) > query.Limit || (page.HasMore && len(page.Items) != query.Limit) {
		return nil, apperror.New("exam.integrity_review.unavailable").Wrap(errors.New("inconsistent Discrepancy page"))
	}
	clone := &store.ExamIntegrityDiscrepancyPage{Items: append([]model.IntegrityDiscrepancy(nil), page.Items...),
		HasMore: page.HasMore}
	return clone, nil
}

type GetStudentResultQuery struct{ AttemptID model.ExamAttemptID }

func (service *Service) GetStudentResult(ctx context.Context, invocation invocation.Invocation, query GetStudentResultQuery) (*model.StudentResult, error) {
	principal := invocation.Principal()
	if !query.AttemptID.IsValid() || principal.Validate() != nil || principal.CredentialType != model.CredentialSessionAccess {
		return nil, apperror.Invalid("exam.integrity_review.invalid", "student_result")
	}
	result, err := service.deps.Persistence.GetReleasedStudentResult(ctx, query.AttemptID, principal.UserID)
	if err != nil {
		return nil, storeErrors.Translate(err)
	}
	if result == nil || result.ReviewID.IsZero() || result.SubmissionID.IsZero() || result.AttemptID != query.AttemptID ||
		result.CandidateUserID != principal.UserID || result.ReleasedAt.IsZero() {
		return nil, apperror.New("exam.integrity_review.unavailable").Wrap(errors.New("inconsistent Student Result"))
	}
	clone := *result
	clone.StudentRemarksMarkdown = safemarkdown.Sanitize(clone.StudentRemarksMarkdown)
	return &clone, nil
}

func (service *Service) authorizeReviewMutation(ctx context.Context, invocation invocation.Invocation,
	submissionID model.SubmissionID,
) (bool, *store.ExamIntegrityReviewAuthorization, error) {
	override, err := service.deps.Authorizer.AuthorizeReview(ctx, invocation, submissionID)
	if err != nil {
		return false, nil, err
	}
	return service.resolveMutationAuthorization(ctx, submissionID, override)
}

func (service *Service) authorizeReleaseMutation(ctx context.Context, invocation invocation.Invocation,
	submissionID model.SubmissionID,
) (bool, *store.ExamIntegrityReviewAuthorization, error) {
	override, err := service.deps.Authorizer.AuthorizeRelease(ctx, invocation, submissionID)
	if err != nil {
		return false, nil, err
	}
	return service.resolveMutationAuthorization(ctx, submissionID, override)
}

func (service *Service) resolveMutationAuthorization(ctx context.Context, submissionID model.SubmissionID,
	override bool,
) (bool, *store.ExamIntegrityReviewAuthorization, error) {
	authorization, err := service.deps.Persistence.Resolve(ctx, submissionID)
	if err != nil {
		return false, nil, storeErrors.Translate(err)
	}
	if !validAuthorization(authorization, submissionID) {
		return false, nil, apperror.New("exam.integrity_review.unavailable").Wrap(errors.New("inconsistent Review authorization"))
	}
	return override, authorization, nil
}

func projectMutation(stored *store.ExamIntegrityReviewMutationResult, submissionID model.SubmissionID) (Result, error) {
	if stored == nil || !validAuthorization(&stored.Authorization, submissionID) || stored.Review == nil ||
		stored.Review.Validate() != nil || stored.Review.SubmissionID != submissionID ||
		(stored.Decision != nil && (stored.Decision.Validate() != nil || stored.Decision.ReviewID != stored.Review.ID)) {
		return Result{}, apperror.New("exam.integrity_review.unavailable").Wrap(errors.New("inconsistent Review mutation result"))
	}
	review := *stored.Review
	result := Result{Authorization: stored.Authorization, Review: &review, Replayed: stored.Replayed}
	if stored.Decision != nil {
		decision := *stored.Decision
		result.Decision = &decision
	}
	return result, nil
}

func validAuthorization(value *store.ExamIntegrityReviewAuthorization, submissionID model.SubmissionID) bool {
	return value != nil && value.SubmissionID == submissionID && value.ExamID.IsValid() && value.SittingID.IsValid() &&
		value.AttemptID.IsValid() && value.CandidateUserID.IsValid() && value.AcademicUnitID.IsValid()
}

func validSnapshot(snapshot *store.ExamSubmissionReviewSnapshot, submissionID model.SubmissionID) bool {
	if snapshot == nil || snapshot.NativeConditions.Validate() != nil || snapshot.DeliveryInventoryRevision < 0 || !validAuthorization(&snapshot.Authorization, submissionID) || snapshot.Submission == nil ||
		snapshot.Submission.Validate() != nil || snapshot.Submission.ID != submissionID || len(snapshot.Decisions) > model.SubmissionReviewMaximumFlags {
		return false
	}
	if snapshot.BrowserEvidenceOverflow != nil && snapshot.BrowserEvidenceOverflow.Validate() != nil {
		return false
	}
	if snapshot.Review == nil {
		return len(snapshot.Decisions) == 0
	}
	if snapshot.Review.Validate() != nil || snapshot.Review.SubmissionID != submissionID {
		return false
	}
	for _, decision := range snapshot.Decisions {
		if decision.Validate() != nil || decision.ReviewID != snapshot.Review.ID {
			return false
		}
	}
	return true
}

func cloneSnapshot(snapshot *store.ExamSubmissionReviewSnapshot) *store.ExamSubmissionReviewSnapshot {
	clone := *snapshot
	if snapshot.BrowserEvidenceOverflow != nil {
		v := *snapshot.BrowserEvidenceOverflow
		clone.BrowserEvidenceOverflow = &v
	}
	submission := *snapshot.Submission
	clone.Submission = &submission
	if snapshot.Review != nil {
		review := *snapshot.Review
		clone.Review = &review
	}
	clone.Decisions = append([]model.IntegrityReviewDecision(nil), snapshot.Decisions...)
	return &clone
}

func (service *Service) effect(ctx context.Context, operation string, result Result, replayed bool,
	deliver func(context.Context, Result) error,
) {
	if replayed {
		return
	}
	if err := deliver(ctx, result); err != nil {
		service.deps.EffectFailures.Report(ctx, operation, err)
	}
}

func (service *Service) failAudit(ctx context.Context, auditID model.AuditEventID, err error) error {
	mapped := storeErrors.Translate(err)
	code := apperror.CodeOf(mapped, "exam.integrity_review.unavailable")
	if auditErr := service.deps.Auditor.Fail(ctx, auditID, code); auditErr != nil {
		return errors.Join(mapped, auditErr)
	}
	return mapped
}

var storeErrors = apperror.StoreTable{
	Constraints: map[string]apperror.Code{
		"integrity_review_revision":          "exam.integrity_review.revision_conflict",
		"integrity_decision_revision":        "exam.integrity_review.revision_conflict",
		"integrity_review_incomplete":        "exam.integrity_review.incomplete",
		"integrity_review_inventory_changed": "exam.integrity_review.incomplete",
		"integrity_review_too_large":         "exam.integrity_review.too_large",
	},
	NotFound:     "exam.integrity_review.not_found",
	Conflict:     "exam.integrity_review.state_conflict",
	Invalid:      "exam.integrity_review.invalid",
	InvalidField: "value",
	Unavailable:  "exam.integrity_review.unavailable",
}
