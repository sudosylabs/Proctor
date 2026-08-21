// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package review

import (
	"context"
	"errors"
	"time"

	"github.com/sudosylabs/proctor/server/app/exam/safemarkdown"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type Call struct {
	principal model.Principal
	metadata  model.RequestMetadata
}

func NewCall(principal model.Principal, metadata model.RequestMetadata) Call {
	principal.CredentialScopes = append([]string(nil), principal.CredentialScopes...)
	return Call{principal: principal, metadata: metadata}
}

func (call Call) Principal() model.Principal {
	principal := call.principal
	principal.CredentialScopes = append([]string(nil), principal.CredentialScopes...)
	return principal
}

func (call Call) RequestMetadata() model.RequestMetadata { return call.metadata }

type Fault struct {
	Code       string
	SafeFields map[string]any
	Cause      error
}

func (fault *Fault) Error() string {
	if fault == nil {
		return "Exam Integrity Review fault"
	}
	return fault.Code
}

func (fault *Fault) Unwrap() error {
	if fault == nil {
		return nil
	}
	return fault.Cause
}

type Authorizer interface {
	AuthorizeView(context.Context, Call, model.SubmissionID) error
	AuthorizeReview(context.Context, Call, model.SubmissionID) (bool, error)
	AuthorizeRelease(context.Context, Call, model.SubmissionID) (bool, error)
}

type Auditor interface {
	Begin(context.Context, Call, model.Action, model.Resource, model.RoleScopeType, string, string, map[string]any) (string, error)
	Fail(context.Context, string, string) error
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
	CandidateUserID model.UserID
	ExamID          model.ExamID
	SittingID       model.ExamSittingID
	ReviewID        model.SubmissionReviewID
	ReleasedAt      time.Time
}

type PreparedResultReleaseMail struct {
	Notice                    *store.PreparedMail
	ExpectedRecipientRevision int64
}

type ResultReleaseMailPreparer interface {
	PrepareResultRelease(context.Context, ResultReleaseMailPreparation) (*PreparedResultReleaseMail, error)
}

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
	Idempotency              *store.CommandIdempotency
}

func (service *Service) SaveDecision(ctx context.Context, call Call, command SaveDecisionCommand) (Result, error) {
	if command.Idempotency == nil || !command.SubmissionID.IsValid() || !command.FlagID.IsValid() ||
		command.ExpectedReviewRevision < 0 || command.ExpectedDecisionRevision < 0 ||
		(command.ExpectedReviewRevision == 0 && !command.ReviewID.IsZero()) ||
		(command.ExpectedReviewRevision > 0 && !command.ReviewID.IsValid()) {
		return Result{}, invalid("decision")
	}
	principal := call.Principal()
	if principal.Validate() != nil {
		return Result{}, invalid("principal")
	}
	reviewID := command.ReviewID
	if reviewID.IsZero() {
		reviewID = service.deps.NewReviewID()
	}
	decisionID := service.deps.NewDecisionID()
	if _, err := model.NewIntegrityReviewDecision(decisionID, reviewID, command.FlagID, command.Outcome,
		principal.UserID, command.PrivateRationale, service.deps.Now()); err != nil {
		return Result{}, invalidCause("decision", err)
	}
	override, authorization, err := service.authorizeReviewMutation(ctx, call, command.SubmissionID)
	if err != nil {
		return Result{}, err
	}
	at := model.TimeUTC(service.deps.Now())
	auditID, err := service.deps.Auditor.Begin(ctx, call, model.ActionSubmissionReview,
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
	}, command.Idempotency)
	if err != nil {
		return Result{}, service.failAudit(ctx, auditID, err)
	}
	result, err := projectMutation(stored, command.SubmissionID)
	if err != nil {
		return Result{}, err
	}
	if !result.Replayed && (result.Review.ID != reviewID || result.Decision == nil || result.Decision.ID != decisionID) {
		return Result{}, unavailable(errors.New("unexpected fresh Review decision identities"))
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
	Idempotency            *store.CommandIdempotency
}

func (service *Service) UpdateDraft(ctx context.Context, call Call, command UpdateDraftCommand) (Result, error) {
	if command.Idempotency == nil || !command.SubmissionID.IsValid() || command.ExpectedReviewRevision < 0 ||
		(command.ExpectedReviewRevision == 0 && !command.ReviewID.IsZero()) ||
		(command.ExpectedReviewRevision > 0 && !command.ReviewID.IsValid()) {
		return Result{}, invalid("draft")
	}
	principal := call.Principal()
	if principal.Validate() != nil {
		return Result{}, invalid("principal")
	}
	reviewID := command.ReviewID
	if reviewID.IsZero() {
		reviewID = service.deps.NewReviewID()
	}
	probe, err := model.NewSubmissionReview(reviewID, command.SubmissionID, principal.UserID, service.deps.Now())
	if err != nil {
		return Result{}, invalidCause("draft", err)
	}
	if err = probe.UpdateDraft(1, command.ManagerNotes, command.StudentRemarksMarkdown, service.deps.Now()); err != nil {
		return Result{}, invalidCause("draft", err)
	}
	override, authorization, err := service.authorizeReviewMutation(ctx, call, command.SubmissionID)
	if err != nil {
		return Result{}, err
	}
	at := model.TimeUTC(service.deps.Now())
	auditID, err := service.deps.Auditor.Begin(ctx, call, model.ActionSubmissionReview,
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
	}, command.Idempotency)
	if err != nil {
		return Result{}, service.failAudit(ctx, auditID, err)
	}
	result, err := projectMutation(stored, command.SubmissionID)
	if err != nil {
		return Result{}, err
	}
	if !result.Replayed && result.Review.ID != reviewID {
		return Result{}, unavailable(errors.New("unexpected fresh Review identity"))
	}
	service.effect(ctx, "exam_integrity_review_changed", result, result.Replayed, service.deps.Effects.ReviewChanged)
	return result, nil
}

type FinalizeCommand struct {
	SubmissionID           model.SubmissionID
	ReviewID               model.SubmissionReviewID
	ExpectedReviewRevision int64
	Idempotency            *store.CommandIdempotency
}

type terminalMutationKind uint8

const (
	terminalFinalize terminalMutationKind = iota + 1
	terminalRelease
)

func (service *Service) Finalize(ctx context.Context, call Call, command FinalizeCommand) (Result, error) {
	return service.terminalMutation(ctx, call, command.SubmissionID, command.ReviewID,
		command.ExpectedReviewRevision, command.Idempotency, terminalFinalize)
}

type ReleaseCommand struct {
	SubmissionID           model.SubmissionID
	ReviewID               model.SubmissionReviewID
	ExpectedReviewRevision int64
	Idempotency            *store.CommandIdempotency
}

func (service *Service) Release(ctx context.Context, call Call, command ReleaseCommand) (Result, error) {
	return service.terminalMutation(ctx, call, command.SubmissionID, command.ReviewID,
		command.ExpectedReviewRevision, command.Idempotency, terminalRelease)
}

func (service *Service) terminalMutation(ctx context.Context, call Call, submissionID model.SubmissionID,
	reviewID model.SubmissionReviewID, expectedRevision int64, idempotency *store.CommandIdempotency,
	kind terminalMutationKind,
) (Result, error) {
	if idempotency == nil || !submissionID.IsValid() || !reviewID.IsValid() || expectedRevision < 1 ||
		(kind != terminalFinalize && kind != terminalRelease) {
		return Result{}, invalid("review")
	}
	principal := call.Principal()
	if principal.Validate() != nil {
		return Result{}, invalid("principal")
	}
	var override bool
	var authorization *store.ExamIntegrityReviewAuthorization
	var err error
	if kind == terminalRelease {
		override, authorization, err = service.authorizeReleaseMutation(ctx, call, submissionID)
	} else {
		override, authorization, err = service.authorizeReviewMutation(ctx, call, submissionID)
	}
	if err != nil {
		return Result{}, err
	}
	at := model.TimeUTC(service.deps.Now())
	var releasePreparation *store.ExamIntegrityReviewReleasePreparation
	if kind == terminalRelease {
		releasePreparation, err = service.deps.Persistence.PrepareRelease(ctx, submissionID, reviewID, expectedRevision)
		if err != nil {
			return Result{}, mapStore(err)
		}
		if releasePreparation == nil || releasePreparation.ReleaseAt.IsZero() {
			return Result{}, unavailable(errors.New("invalid result release preparation"))
		}
		at = model.TimeUTC(releasePreparation.ReleaseAt)
	}
	action, operation := model.ActionSubmissionReview, store.ExamIntegrityReviewFinalizeOperation
	if kind == terminalRelease {
		action, operation = model.ActionSubmissionRelease, store.ExamIntegrityReviewReleaseOperation
	}
	auditID, err := service.deps.Auditor.Begin(ctx, call, action,
		model.Resource{Type: model.ResourceSubmission, ID: submissionID.String()}, model.RoleScopeAcademicUnit,
		authorization.AcademicUnitID.String(), operation,
		map[string]any{"submission_id": submissionID.String(), "submission_review_id": reviewID.String(), "expected_review_revision": expectedRevision})
	if err != nil {
		return Result{}, err
	}
	var stored *store.ExamIntegrityReviewMutationResult
	if kind == terminalRelease {
		var notice *store.PreparedMail
		var expectedRecipientRevision int64
		if !releasePreparation.Replayed {
			prepared, prepareErr := service.deps.Mail.PrepareResultRelease(ctx, ResultReleaseMailPreparation{
				CandidateUserID: authorization.CandidateUserID, ExamID: authorization.ExamID,
				SittingID: authorization.SittingID, ReviewID: reviewID, ReleasedAt: at,
			})
			if prepareErr != nil || prepared == nil || prepared.Notice == nil || prepared.ExpectedRecipientRevision < 1 {
				if prepareErr == nil {
					prepareErr = errors.New("invalid result release mail preparation")
				}
				return Result{}, service.failAudit(ctx, auditID, unavailable(prepareErr))
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
		return Result{}, unavailable(errors.New("inconsistent terminal Review result"))
	}
	if kind == terminalRelease {
		service.effect(ctx, "exam_integrity_result_released", result, result.Replayed, service.deps.Effects.ResultReleased)
	} else {
		service.effect(ctx, "exam_integrity_review_finalized", result, result.Replayed, service.deps.Effects.ReviewFinalized)
	}
	return result, nil
}

type GetReviewQuery struct{ SubmissionID model.SubmissionID }

func (service *Service) Get(ctx context.Context, call Call, query GetReviewQuery) (*store.ExamSubmissionReviewSnapshot, error) {
	if !query.SubmissionID.IsValid() {
		return nil, invalid("submission_id")
	}
	if err := service.deps.Authorizer.AuthorizeView(ctx, call, query.SubmissionID); err != nil {
		return nil, err
	}
	snapshot, err := service.deps.Persistence.Get(ctx, query.SubmissionID)
	if err != nil {
		return nil, mapStore(err)
	}
	if !validSnapshot(snapshot, query.SubmissionID) {
		return nil, unavailable(errors.New("inconsistent Review snapshot"))
	}
	return cloneSnapshot(snapshot), nil
}

type ListFlagsQuery struct {
	SubmissionID model.SubmissionID
	AfterFlagID  model.IntegrityFlagID
	Limit        int
}

func (service *Service) ListFlags(ctx context.Context, call Call, query ListFlagsQuery) (*store.ExamIntegrityFlagPage, error) {
	if !query.SubmissionID.IsValid() || query.Limit < 1 || query.Limit > store.ExamIntegrityReviewFlagReadMaximum {
		return nil, invalid("flag_list")
	}
	if err := service.deps.Authorizer.AuthorizeView(ctx, call, query.SubmissionID); err != nil {
		return nil, err
	}
	page, err := service.deps.Persistence.ListFlags(ctx, store.ExamIntegrityFlagListOptions(query))
	if err != nil {
		return nil, mapStore(err)
	}
	if page == nil || len(page.Items) > query.Limit || (page.HasMore && len(page.Items) != query.Limit) {
		return nil, unavailable(errors.New("inconsistent Flag page"))
	}
	clone := &store.ExamIntegrityFlagPage{Items: append([]store.ExamIntegrityFlagSummary(nil), page.Items...), HasMore: page.HasMore}
	return clone, nil
}

type ListEvidenceQuery struct {
	SubmissionID    model.SubmissionID
	FlagID          model.IntegrityFlagID
	AfterEvidenceID model.IntegrityEvidenceID
	Limit           int
}

func (service *Service) ListEvidence(ctx context.Context, call Call, query ListEvidenceQuery) (*store.ExamIntegrityEvidencePage, error) {
	if !query.SubmissionID.IsValid() || !query.FlagID.IsValid() || query.Limit < 1 || query.Limit > store.ExamIntegrityReviewEvidenceReadMaximum {
		return nil, invalid("evidence_list")
	}
	if err := service.deps.Authorizer.AuthorizeView(ctx, call, query.SubmissionID); err != nil {
		return nil, err
	}
	page, err := service.deps.Persistence.ListEvidence(ctx, store.ExamIntegrityEvidenceListOptions(query))
	if err != nil {
		return nil, mapStore(err)
	}
	if page == nil || len(page.Items) > query.Limit || (page.HasMore && len(page.Items) != query.Limit) {
		return nil, unavailable(errors.New("inconsistent Evidence page"))
	}
	clone := &store.ExamIntegrityEvidencePage{Items: append([]model.IntegrityEvidence(nil), page.Items...), HasMore: page.HasMore}
	return clone, nil
}

type ListDiscrepanciesQuery struct {
	SubmissionID       model.SubmissionID
	AfterDiscrepancyID model.IntegrityDiscrepancyID
	Limit              int
}

func (service *Service) ListDiscrepancies(ctx context.Context, call Call,
	query ListDiscrepanciesQuery,
) (*store.ExamIntegrityDiscrepancyPage, error) {
	if !query.SubmissionID.IsValid() || query.Limit < 1 || query.Limit > store.ExamIntegrityReviewDiscrepancyReadMaximum {
		return nil, invalid("discrepancy_list")
	}
	if err := service.deps.Authorizer.AuthorizeView(ctx, call, query.SubmissionID); err != nil {
		return nil, err
	}
	page, err := service.deps.Persistence.ListDiscrepancies(ctx, store.ExamIntegrityDiscrepancyListOptions(query))
	if err != nil {
		return nil, mapStore(err)
	}
	if page == nil || len(page.Items) > query.Limit || (page.HasMore && len(page.Items) != query.Limit) {
		return nil, unavailable(errors.New("inconsistent Discrepancy page"))
	}
	clone := &store.ExamIntegrityDiscrepancyPage{Items: append([]model.IntegrityDiscrepancy(nil), page.Items...),
		HasMore: page.HasMore}
	return clone, nil
}

type GetStudentResultQuery struct{ AttemptID model.ExamAttemptID }

func (service *Service) GetStudentResult(ctx context.Context, call Call, query GetStudentResultQuery) (*model.StudentResult, error) {
	principal := call.Principal()
	if !query.AttemptID.IsValid() || principal.Validate() != nil || principal.CredentialType != model.CredentialSessionAccess {
		return nil, invalid("student_result")
	}
	result, err := service.deps.Persistence.GetReleasedStudentResult(ctx, query.AttemptID, principal.UserID)
	if err != nil {
		return nil, mapStore(err)
	}
	if result == nil || result.ReviewID.IsZero() || result.SubmissionID.IsZero() || result.AttemptID != query.AttemptID ||
		result.CandidateUserID != principal.UserID || result.ReleasedAt.IsZero() {
		return nil, unavailable(errors.New("inconsistent Student Result"))
	}
	clone := *result
	clone.StudentRemarksMarkdown = safemarkdown.Sanitize(clone.StudentRemarksMarkdown)
	return &clone, nil
}

func (service *Service) authorizeReviewMutation(ctx context.Context, call Call,
	submissionID model.SubmissionID,
) (bool, *store.ExamIntegrityReviewAuthorization, error) {
	override, err := service.deps.Authorizer.AuthorizeReview(ctx, call, submissionID)
	if err != nil {
		return false, nil, err
	}
	return service.resolveMutationAuthorization(ctx, submissionID, override)
}

func (service *Service) authorizeReleaseMutation(ctx context.Context, call Call,
	submissionID model.SubmissionID,
) (bool, *store.ExamIntegrityReviewAuthorization, error) {
	override, err := service.deps.Authorizer.AuthorizeRelease(ctx, call, submissionID)
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
		return false, nil, mapStore(err)
	}
	if !validAuthorization(authorization, submissionID) {
		return false, nil, unavailable(errors.New("inconsistent Review authorization"))
	}
	return override, authorization, nil
}

func projectMutation(stored *store.ExamIntegrityReviewMutationResult, submissionID model.SubmissionID) (Result, error) {
	if stored == nil || !validAuthorization(&stored.Authorization, submissionID) || stored.Review == nil ||
		stored.Review.Validate() != nil || stored.Review.SubmissionID != submissionID ||
		(stored.Decision != nil && (stored.Decision.Validate() != nil || stored.Decision.ReviewID != stored.Review.ID)) {
		return Result{}, unavailable(errors.New("inconsistent Review mutation result"))
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
	if snapshot == nil || !validAuthorization(&snapshot.Authorization, submissionID) || snapshot.Submission == nil ||
		snapshot.Submission.Validate() != nil || snapshot.Submission.ID != submissionID || len(snapshot.Decisions) > model.SubmissionReviewMaximumFlags {
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

func (service *Service) failAudit(ctx context.Context, auditID string, err error) error {
	mapped := mapStore(err)
	code := "exam.integrity_review.unavailable"
	var fault *Fault
	if errors.As(mapped, &fault) {
		code = fault.Code
	}
	if auditErr := service.deps.Auditor.Fail(ctx, auditID, code); auditErr != nil {
		return auditErr
	}
	return mapped
}

func invalid(field string) error {
	return &Fault{Code: "exam.integrity_review.invalid", SafeFields: map[string]any{"field": field}}
}

func invalidCause(field string, cause error) error {
	return &Fault{Code: "exam.integrity_review.invalid", SafeFields: map[string]any{"field": field}, Cause: cause}
}

func unavailable(cause error) error {
	return &Fault{Code: "exam.integrity_review.unavailable", Cause: cause}
}

func mapStore(err error) error {
	var idempotencyConflict *store.ErrIdempotencyConflict
	var idempotencyInProgress *store.ErrIdempotencyInProgress
	var invalidInput *store.ErrInvalidInput
	var conflict *store.ErrConflict
	switch {
	case errors.As(err, &idempotencyConflict):
		return &Fault{Code: "idempotency.conflict", Cause: err}
	case errors.As(err, &idempotencyInProgress):
		return &Fault{Code: "idempotency.in_progress", Cause: err}
	case store.IsNotFound(err):
		return &Fault{Code: "exam.integrity_review.not_found", Cause: err}
	case errors.As(err, &invalidInput):
		return invalidCause("value", err)
	case errors.As(err, &conflict):
		code := "exam.integrity_review.state_conflict"
		switch conflict.Constraint {
		case "integrity_review_revision", "integrity_decision_revision":
			code = "exam.integrity_review.revision_conflict"
		case "integrity_review_incomplete", "integrity_review_inventory_changed":
			code = "exam.integrity_review.incomplete"
		case "integrity_review_too_large":
			code = "exam.integrity_review.too_large"
		}
		return &Fault{Code: code, Cause: err}
	default:
		return unavailable(err)
	}
}
