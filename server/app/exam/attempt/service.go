// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package attempt

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"
	"unicode/utf8"

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

type Sittings interface {
	// Resolve returns stable Sitting ownership metadata. Store remains the
	// authority for connect-time lifecycle, deadline, and membership checks.
	Resolve(context.Context, model.ExamSittingID) (*store.ExamSittingSnapshot, error)
}

func (fault *Fault) Error() string {
	if fault == nil {
		return "Exam Attempt fault"
	}
	return fault.Code
}

func (fault *Fault) Unwrap() error {
	if fault == nil {
		return nil
	}
	return fault.Cause
}

type ManagerAuthorizer interface {
	AuthorizeSittingView(context.Context, Call, model.ExamSittingID) error
	AuthorizeSittingManage(context.Context, Call, model.ExamSittingID) (bool, error)
	AuthorizeSubmissionView(context.Context, Call, model.SubmissionID) error
}

type Auditor interface {
	Begin(context.Context, Call, model.Action, model.Resource, model.RoleScopeType, string, string, map[string]any) (string, error)
	Fail(context.Context, string, string) error
}

// SystemAuditor creates actor-less audit attempts for trusted periodic
// continuity enforcement. A runtime scanner must never manufacture a User
// Principal merely to persist this system decision.
type SystemAuditor interface {
	Begin(context.Context, model.Action, model.Resource, model.RoleScopeType, string, string, map[string]any) (string, error)
	Fail(context.Context, string, string) error
}

type Effects interface {
	ConnectionOpened(context.Context, ConnectionResult) error
	ConnectionClosed(context.Context, ConnectionClosedResult) error
	ParticipationExpired(context.Context, ParticipationExpiry) error
	AttemptReallowed(context.Context, ReallowResult) error
	WorkspaceChanged(context.Context, WorkspaceMutationResult) error
	FocusLossEvaluated(context.Context, FocusLossEvaluation) error
	AttemptSubmitted(context.Context, SubmissionResult) error
	AttemptSealedForSittingClose(context.Context, AutomaticSubmissionResult) error
}

// SystemCall identifies the durable Job execution responsible for one
// actor-less automatic seal. IDs enter only bounded audit metadata.
type SystemCall struct {
	JobID     model.JobID
	AttemptID model.JobAttemptID
}

func (call SystemCall) valid() bool { return call.JobID.IsValid() && call.AttemptID.IsValid() }

type EffectFailures interface {
	Report(context.Context, string, error)
}

type Content interface {
	OpenExamResource(context.Context, model.FileRevisionID, model.FileRenditionID) (io.ReadCloser, error)
	OpenStarterWorkspaceObject(context.Context, model.StarterWorkspaceObjectID) (io.ReadCloser, error)
	StageAttemptWorkspaceObject(context.Context, model.AttemptWorkspaceObjectID, io.Reader, int64, string) (*model.AttemptWorkspaceContent, error)
	OpenAttemptWorkspaceObject(context.Context, model.AttemptWorkspaceObjectID) (io.ReadCloser, error)
}

type Dependencies struct {
	Persistence         store.ExamAttemptStore
	Workspace           store.ExamAttemptWorkspaceStore
	Submissions         store.ExamSubmissionStore
	Sittings            Sittings
	Managers            ManagerAuthorizer
	Auditor             Auditor
	SystemAuditor       SystemAuditor
	Effects             Effects
	EffectFailures      EffectFailures
	Content             Content
	Now                 func() time.Time
	NewAttemptID        func() model.ExamAttemptID
	NewWorkspaceID      func() model.ExamAttemptWorkspaceID
	NewParticipation    func() model.AttemptParticipationID
	NewConnection       func() model.AttemptConnectionID
	NewEvidence         func() model.IntegrityEvidenceID
	NewFlag             func() model.IntegrityFlagID
	NewSuspension       func() model.AttemptSuspensionID
	NewFocusLossSignal  func() model.FocusLossSignalID
	NewDiscrepancy      func() model.IntegrityDiscrepancyID
	NewWorkspaceEntry   func() model.AttemptWorkspaceEntryID
	NewWorkspaceObject  func() model.AttemptWorkspaceObjectID
	NewWorkspaceVersion func() model.WorkspaceContentVersion
	NewSubmission       func() model.SubmissionID
}

type Service struct{ deps Dependencies }

func New(deps Dependencies) (*Service, error) {
	if deps.Persistence == nil || deps.Workspace == nil || deps.Submissions == nil || deps.Sittings == nil || deps.Managers == nil || deps.Auditor == nil || deps.SystemAuditor == nil || deps.Effects == nil ||
		deps.EffectFailures == nil || deps.Content == nil || deps.Now == nil || deps.NewAttemptID == nil ||
		deps.NewWorkspaceID == nil || deps.NewParticipation == nil || deps.NewConnection == nil || deps.NewEvidence == nil ||
		deps.NewFlag == nil || deps.NewSuspension == nil || deps.NewFocusLossSignal == nil || deps.NewDiscrepancy == nil || deps.NewWorkspaceEntry == nil || deps.NewWorkspaceObject == nil || deps.NewWorkspaceVersion == nil || deps.NewSubmission == nil {
		return nil, errors.New("Exam Attempt dependencies are required")
	}
	return &Service{deps: deps}, nil
}

type GetManagedAttemptQuery struct {
	ExamID    model.ExamID
	SittingID model.ExamSittingID
	AttemptID model.ExamAttemptID
}

func (service *Service) GetManaged(ctx context.Context, call Call, query GetManagedAttemptQuery) (*store.ExamAttemptManagerSnapshot, error) {
	if !query.ExamID.IsValid() || !query.SittingID.IsValid() || !query.AttemptID.IsValid() {
		return nil, invalid("attempt")
	}
	if err := service.deps.Managers.AuthorizeSittingView(ctx, call, query.SittingID); err != nil {
		return nil, err
	}
	snapshot, err := service.deps.Persistence.Get(ctx, query.ExamID, query.AttemptID)
	if err != nil {
		return nil, mapStore(err)
	}
	if snapshot == nil || snapshot.Attempt == nil || snapshot.Attempt.Validate() != nil ||
		snapshot.Attempt.ExamID != query.ExamID || snapshot.Attempt.SittingID != query.SittingID ||
		!validActiveSuspension(snapshot.ActiveSuspension, snapshot.Attempt.ID) {
		return nil, unavailable(errors.New("inconsistent managed Attempt projection"))
	}
	return cloneManagerSnapshot(snapshot), nil
}

type ListManagedAttemptsQuery struct {
	ExamID          model.ExamID
	SittingID       model.ExamSittingID
	States          []model.ExamAttemptState
	BeforeCreatedAt time.Time
	BeforeAttemptID model.ExamAttemptID
	Limit           int
}

type ManagedAttemptPage struct {
	Items   []store.ExamAttemptManagerSnapshot
	HasMore bool
}

func (service *Service) ListManaged(ctx context.Context, call Call, query ListManagedAttemptsQuery) (ManagedAttemptPage, error) {
	if !query.ExamID.IsValid() || !query.SittingID.IsValid() || query.Limit < 1 || query.Limit > 200 ||
		(query.BeforeCreatedAt.IsZero() != query.BeforeAttemptID.IsZero()) {
		return ManagedAttemptPage{}, invalid("attempt_list")
	}
	if err := service.deps.Managers.AuthorizeSittingView(ctx, call, query.SittingID); err != nil {
		return ManagedAttemptPage{}, err
	}
	rows, err := service.deps.Persistence.List(ctx, store.ExamAttemptManagerListOptions{
		ExamID: query.ExamID, SittingID: query.SittingID, States: append([]model.ExamAttemptState(nil), query.States...),
		BeforeCreatedAt: model.TimeUTC(query.BeforeCreatedAt), BeforeAttemptID: query.BeforeAttemptID, Limit: query.Limit + 1,
	})
	if err != nil {
		return ManagedAttemptPage{}, mapStore(err)
	}
	page := ManagedAttemptPage{Items: make([]store.ExamAttemptManagerSnapshot, 0, min(len(rows), query.Limit)), HasMore: len(rows) > query.Limit}
	for index, row := range rows {
		if index == query.Limit {
			break
		}
		if row.Attempt == nil || row.Attempt.Validate() != nil || row.Attempt.ExamID != query.ExamID || row.Attempt.SittingID != query.SittingID ||
			!validActiveSuspension(row.ActiveSuspension, row.Attempt.ID) {
			return ManagedAttemptPage{}, unavailable(errors.New("inconsistent managed Attempt page"))
		}
		page.Items = append(page.Items, *cloneManagerSnapshot(&row))
	}
	return page, nil
}

func cloneManagerSnapshot(snapshot *store.ExamAttemptManagerSnapshot) *store.ExamAttemptManagerSnapshot {
	if snapshot == nil {
		return nil
	}
	clone := *snapshot
	if snapshot.Attempt != nil {
		value := *snapshot.Attempt
		clone.Attempt = &value
	}
	if snapshot.Workspace != nil {
		value := *snapshot.Workspace
		clone.Workspace = &value
	}
	if snapshot.LatestParticipation != nil {
		value := *snapshot.LatestParticipation
		clone.LatestParticipation = &value
	}
	if snapshot.CurrentConnection != nil {
		value := *snapshot.CurrentConnection
		clone.CurrentConnection = &value
	}
	if snapshot.ActiveSuspension != nil {
		value := *snapshot.ActiveSuspension
		clone.ActiveSuspension = &value
	}
	return &clone
}

func validActiveSuspension(view *store.ExamAttemptSuspensionView, attemptID model.ExamAttemptID) bool {
	if view == nil {
		return true
	}
	return view.ID.IsValid() && view.AttemptID == attemptID && view.ParticipationID.IsValid() && view.FlagID.IsValid() &&
		view.Generation > 0 && view.State == model.AttemptSuspensionActive && view.Source == model.AttemptSuspensionSourcePolicy &&
		(view.CandidateReason == model.AttemptSuspensionCandidateReasonSecureContinuityLost ||
			view.CandidateReason == model.AttemptSuspensionCandidateReasonFocusLossPolicy) && !view.StartedAt.IsZero() &&
		!view.EndedAt.Valid && view.ReallowedByUserID.IsZero()
}

type ConnectCommand struct {
	SittingID            model.ExamSittingID
	ContinuityCredential string
	Idempotency          *store.CommandIdempotency
}

type ConnectionResult struct {
	Attempt          model.ExamAttempt
	Workspace        model.ExamAttemptWorkspace
	Participation    store.ExamAttemptParticipationView
	Connection       model.AttemptConnection
	ClassID          model.ClassID
	FirstAdmission   bool
	ConnectionOpened bool
	Replayed         bool
}

type RenewParticipationCommand struct {
	AttemptID            model.ExamAttemptID
	ParticipationID      model.AttemptParticipationID
	ConnectionID         model.AttemptConnectionID
	Generation           int64
	Sequence             int64
	ContinuityCredential string
}

// ParticipationRenewal is the candidate-safe acknowledgement of one explicit
// application renewal. It contains authoritative database time but no raw or
// hashed credential material.
type ParticipationRenewal struct {
	AttemptID        model.ExamAttemptID
	ParticipationID  model.AttemptParticipationID
	Generation       int64
	AcceptedSequence int64
	DatabaseTime     time.Time
	LeaseExpiresAt   time.Time
	Duplicate        bool
}

type ParticipationExpiry struct {
	ExamID           model.ExamID
	SittingID        model.ExamSittingID
	ClassID          model.ClassID
	CandidateUserID  model.UserID
	Attempt          model.ExamAttempt
	Participation    store.ExamAttemptParticipationView
	Connection       store.ExamAttemptManagerConnection
	ConnectionClosed bool
	Evidence         model.IntegrityEvidence
	Flag             model.IntegrityFlag
	Suspension       store.ExamAttemptSuspensionView
	DatabaseTime     time.Time
	Replayed         bool
}

type ExpiryScanResult struct {
	Due       int
	Completed int
	Replayed  int
}

type ReallowCommand struct {
	ExamID                  model.ExamID
	SittingID               model.ExamSittingID
	AttemptID               model.ExamAttemptID
	SuspensionID            model.AttemptSuspensionID
	ExpectedAttemptRevision int64
	PrivateReason           string
	Idempotency             *store.CommandIdempotency
}

type ReallowResult struct {
	ExamID          model.ExamID
	SittingID       model.ExamSittingID
	ClassID         model.ClassID
	CandidateUserID model.UserID
	Attempt         model.ExamAttempt
	Suspension      store.ExamAttemptSuspensionView
	Replayed        bool
}

func (service *Service) Reallow(ctx context.Context, call Call, command ReallowCommand) (ReallowResult, error) {
	principal := call.Principal()
	if command.Idempotency == nil {
		return ReallowResult{}, &Fault{Code: "idempotency.key_required"}
	}
	if principal.Validate() != nil {
		return ReallowResult{}, &Fault{Code: "authentication.invalid_token"}
	}
	if !command.ExamID.IsValid() || !command.SittingID.IsValid() || !command.AttemptID.IsValid() ||
		!command.SuspensionID.IsValid() || command.ExpectedAttemptRevision < 1 || !validReallowReason(command.PrivateReason) {
		return ReallowResult{}, invalid("reallow")
	}
	override, err := service.deps.Managers.AuthorizeSittingManage(ctx, call, command.SittingID)
	if err != nil {
		return ReallowResult{}, err
	}
	snapshot, err := service.deps.Sittings.Resolve(ctx, command.SittingID)
	if err != nil {
		return ReallowResult{}, mapStore(err)
	}
	if snapshot == nil || snapshot.Sitting == nil || snapshot.Sitting.ID != command.SittingID ||
		snapshot.Sitting.ExamID != command.ExamID || !snapshot.Sitting.ClassID.IsValid() {
		return ReallowResult{}, unavailable(errors.New("incomplete Sitting ownership projection"))
	}
	auditID, err := service.deps.Auditor.Begin(ctx, call, model.ActionExamSittingManage,
		model.Resource{Type: model.ResourceExamSitting, ID: command.SittingID.String()}, model.RoleScopeClass, snapshot.Sitting.ClassID.String(),
		store.ExamAttemptReallowOperation, map[string]any{"exam_id": command.ExamID.String(), "exam_sitting_id": command.SittingID.String(),
			"exam_attempt_id": command.AttemptID.String(), "suspension_id": command.SuspensionID.String(),
			"expected_attempt_revision": command.ExpectedAttemptRevision})
	if err != nil {
		return ReallowResult{}, err
	}
	at := model.TimeUTC(service.deps.Now())
	stored, err := service.deps.Persistence.ReallowAttempt(ctx, &store.ExamAttemptReallow{
		ExamID: command.ExamID, SittingID: command.SittingID, AttemptID: command.AttemptID, SuspensionID: command.SuspensionID,
		ActorUserID: principal.UserID, ManagerOverride: override, ExpectedAttemptRevision: command.ExpectedAttemptRevision,
		PrivateReason: command.PrivateReason, ChangedAt: at, AuditEventID: auditID, AuditAt: model.MillisFromTime(at),
	}, command.Idempotency)
	if err != nil {
		return ReallowResult{}, service.failAudit(ctx, auditID, err)
	}
	if stored == nil || stored.Attempt == nil || stored.Suspension == nil || stored.ExamID != command.ExamID ||
		stored.SittingID != command.SittingID || stored.ClassID != snapshot.Sitting.ClassID || !stored.CandidateUserID.IsValid() ||
		stored.Attempt.ID != command.AttemptID || stored.Attempt.ExamID != command.ExamID || stored.Attempt.SittingID != command.SittingID ||
		stored.Attempt.CandidateUserID != stored.CandidateUserID || stored.Attempt.State != model.ExamAttemptActive || stored.Attempt.Validate() != nil ||
		stored.Attempt.Revision != command.ExpectedAttemptRevision+1 || stored.Suspension.ID != command.SuspensionID ||
		!validClosedSuspension(stored.Suspension, command.AttemptID, principal.UserID) {
		return ReallowResult{}, unavailable(errors.New("inconsistent Attempt re-allow outcome"))
	}
	result := ReallowResult{ExamID: stored.ExamID, SittingID: stored.SittingID, ClassID: stored.ClassID,
		CandidateUserID: stored.CandidateUserID, Attempt: *stored.Attempt, Suspension: *stored.Suspension, Replayed: stored.Replayed}
	if !result.Replayed {
		if effectErr := service.deps.Effects.AttemptReallowed(ctx, result); effectErr != nil {
			service.deps.EffectFailures.Report(ctx, "exam_attempt_reallowed", effectErr)
		}
	}
	return result, nil
}

func validReallowReason(reason string) bool {
	return utf8.ValidString(reason) && reason == strings.TrimSpace(reason) && utf8.RuneCountInString(reason) >= 1 &&
		utf8.RuneCountInString(reason) <= model.AttemptSuspensionPrivateReasonMaximumRunes && len(reason) <= 4000
}

func validClosedSuspension(view *store.ExamAttemptSuspensionView, attemptID model.ExamAttemptID, actorID model.UserID) bool {
	return view != nil && view.ID.IsValid() && view.AttemptID == attemptID && view.ParticipationID.IsValid() && view.FlagID.IsValid() &&
		view.Generation > 0 && view.State == model.AttemptSuspensionClosed && view.Source == model.AttemptSuspensionSourcePolicy &&
		view.CandidateReason == model.AttemptSuspensionCandidateReasonSecureContinuityLost && !view.StartedAt.IsZero() &&
		view.EndedAt.Valid && !view.EndedAt.Time.Before(view.StartedAt) && view.ReallowedByUserID == actorID
}

// ScanExpiredParticipations performs one bounded runtime maintenance pass.
// Every listed candidate is rechecked by the named conditional Store
// operation; PostgreSQL, never this loop or its clock, decides expiry.
func (service *Service) ScanExpiredParticipations(ctx context.Context, limit int) (ExpiryScanResult, error) {
	if limit < 1 || limit > 200 {
		return ExpiryScanResult{}, invalid("expiry_limit")
	}
	due, err := service.deps.Persistence.ListExpiredParticipations(ctx, limit)
	if err != nil {
		return ExpiryScanResult{}, mapStore(err)
	}
	result := ExpiryScanResult{Due: len(due)}
	for _, item := range due {
		expired, expireErr := service.expireParticipation(ctx, item)
		if expireErr != nil {
			return result, expireErr
		}
		result.Completed++
		if expired.Replayed {
			result.Replayed++
		}
	}
	return result, nil
}

func (service *Service) expireParticipation(ctx context.Context, due store.ExamAttemptParticipationExpiryDue) (ParticipationExpiry, error) {
	if !validExpiryDue(due) {
		return ParticipationExpiry{}, unavailable(errors.New("invalid Participation expiry target"))
	}
	auditID, err := service.deps.SystemAuditor.Begin(ctx, model.ActionExamSittingManage,
		model.Resource{Type: model.ResourceExamSitting, ID: due.SittingID.String()}, model.RoleScopeClass, due.ClassID.String(),
		store.ExamAttemptExpireParticipationOperation, map[string]any{
			"exam_id": due.ExamID.String(), "exam_sitting_id": due.SittingID.String(), "exam_attempt_id": due.AttemptID.String(),
			"participation_id": due.ParticipationID.String(), "generation": due.Generation,
		})
	if err != nil {
		return ParticipationExpiry{}, err
	}
	at := model.TimeUTC(service.deps.Now())
	stored, err := service.deps.Persistence.ExpireParticipation(ctx, &store.ExamAttemptParticipationExpiry{
		AttemptID: due.AttemptID, ParticipationID: due.ParticipationID, Generation: due.Generation,
		EvidenceID: service.deps.NewEvidence(), FlagID: service.deps.NewFlag(), SuspensionID: service.deps.NewSuspension(),
		AuditEventID: auditID, AuditAt: model.MillisFromTime(at),
	})
	if err != nil {
		mapped := mapStore(err)
		code := "exam.attempt.unavailable"
		var fault *Fault
		if errors.As(mapped, &fault) {
			code = fault.Code
		}
		if auditErr := service.deps.SystemAuditor.Fail(ctx, auditID, code); auditErr != nil {
			return ParticipationExpiry{}, auditErr
		}
		return ParticipationExpiry{}, mapped
	}
	result, err := projectExpiry(stored, due)
	if err != nil {
		return ParticipationExpiry{}, err
	}
	if !result.Replayed {
		if effectErr := service.deps.Effects.ParticipationExpired(ctx, result); effectErr != nil {
			service.deps.EffectFailures.Report(ctx, "exam_attempt_participation_expired", effectErr)
		}
	}
	return result, nil
}

func validExpiryDue(due store.ExamAttemptParticipationExpiryDue) bool {
	return due.ExamID.IsValid() && due.SittingID.IsValid() && due.ClassID.IsValid() && due.CandidateUserID.IsValid() &&
		due.AttemptID.IsValid() && due.ParticipationID.IsValid() && due.Generation > 0 && !due.LeaseExpiresAt.IsZero()
}

func projectExpiry(stored *store.ExamAttemptParticipationExpiryResult, due store.ExamAttemptParticipationExpiryDue) (ParticipationExpiry, error) {
	if stored == nil || stored.Attempt == nil || stored.Participation == nil || stored.Connection == nil || stored.Evidence == nil ||
		stored.Flag == nil || stored.Suspension == nil || stored.ExamID != due.ExamID || stored.SittingID != due.SittingID ||
		stored.ClassID != due.ClassID || stored.CandidateUserID != due.CandidateUserID || stored.Attempt.ID != due.AttemptID ||
		stored.Attempt.ExamID != due.ExamID || stored.Attempt.SittingID != due.SittingID || stored.Attempt.CandidateUserID != due.CandidateUserID ||
		stored.Attempt.State != model.ExamAttemptSuspended || stored.Attempt.Validate() != nil || !validParticipationView(stored.Participation) ||
		stored.Participation.ID != due.ParticipationID || stored.Participation.AttemptID != due.AttemptID ||
		stored.Participation.Generation != due.Generation || stored.Participation.State != model.AttemptParticipationEnded ||
		stored.Participation.EndReason != model.AttemptParticipationEndLeaseExpired || stored.Connection.State != model.AttemptConnectionClosed ||
		!stored.Connection.ID.IsValid() || stored.Connection.OpenedAt.IsZero() || !stored.Connection.ClosedAt.Valid ||
		stored.Connection.ClosedAt.Time.Before(stored.Connection.OpenedAt) || !stored.Connection.CloseReason.IsValid() ||
		(stored.ConnectionClosed != (stored.Connection.CloseReason == model.AttemptConnectionCloseLeaseExpired)) ||
		stored.Evidence.Validate() != nil || stored.Flag.Validate() != nil ||
		stored.Evidence.AttemptID != due.AttemptID || stored.Evidence.ParticipationID != due.ParticipationID || stored.Evidence.Generation != due.Generation ||
		stored.Evidence.FlagID != stored.Flag.ID ||
		stored.Flag.AttemptID != due.AttemptID || stored.Flag.Generation != due.Generation || !stored.Suspension.ID.IsValid() ||
		stored.Suspension.AttemptID != due.AttemptID || stored.Suspension.ParticipationID != due.ParticipationID ||
		stored.Suspension.Generation != due.Generation || stored.Suspension.FlagID != stored.Flag.ID ||
		stored.Suspension.State != model.AttemptSuspensionActive || stored.Suspension.Source != model.AttemptSuspensionSourcePolicy ||
		stored.Suspension.CandidateReason != model.AttemptSuspensionCandidateReasonSecureContinuityLost || stored.Suspension.StartedAt.IsZero() ||
		stored.Suspension.EndedAt.Valid || !stored.Suspension.ReallowedByUserID.IsZero() || stored.DatabaseTime.IsZero() ||
		stored.DatabaseTime.Before(due.LeaseExpiresAt) {
		return ParticipationExpiry{}, unavailable(errors.New("inconsistent Participation expiry outcome"))
	}
	return ParticipationExpiry{ExamID: stored.ExamID, SittingID: stored.SittingID, ClassID: stored.ClassID,
		CandidateUserID: stored.CandidateUserID, Attempt: *stored.Attempt, Participation: *stored.Participation,
		Connection: *stored.Connection, ConnectionClosed: stored.ConnectionClosed, Evidence: *stored.Evidence, Flag: *stored.Flag, Suspension: *stored.Suspension,
		DatabaseTime: model.TimeUTC(stored.DatabaseTime), Replayed: stored.Replayed}, nil
}

// RenewParticipation authenticates a continuity renewal independently of the
// WebSocket transport ping. Store owns the database clock, exclusive expiry
// boundary, duplicate outcome, and permanent generation fence.
func (service *Service) RenewParticipation(ctx context.Context, call Call, command RenewParticipationCommand) (ParticipationRenewal, error) {
	principal := call.Principal()
	if principal.Validate() != nil || principal.CredentialType != model.CredentialSessionAccess {
		return ParticipationRenewal{}, &Fault{Code: "authentication.invalid_token"}
	}
	if !command.AttemptID.IsValid() || !command.ParticipationID.IsValid() || !command.ConnectionID.IsValid() ||
		command.Generation < 1 || command.Sequence < 1 || !model.IsValidCredentialToken(command.ContinuityCredential) {
		return ParticipationRenewal{}, invalid("renewal")
	}
	stored, err := service.deps.Persistence.RenewParticipation(ctx, &store.ExamAttemptParticipationRenewal{
		AttemptID: command.AttemptID, ParticipationID: command.ParticipationID, ConnectionID: command.ConnectionID,
		CandidateUserID: principal.UserID, SessionID: principal.SessionID, Generation: command.Generation,
		Sequence: command.Sequence, ContinuityCredentialHash: model.HashToken(command.ContinuityCredential),
	})
	if err != nil {
		var conflict *store.ErrConflict
		if errors.As(err, &conflict) && conflict.Constraint == "attempt_participation_expired" {
			due, resolveErr := service.deps.Persistence.ResolveParticipationExpiry(ctx, command.AttemptID, command.ParticipationID, command.Generation)
			if resolveErr != nil {
				if store.IsNotFound(resolveErr) {
					return ParticipationRenewal{}, &Fault{Code: "exam.attempt.connection_lost", Cause: err}
				}
				return ParticipationRenewal{}, mapStore(resolveErr)
			}
			if due == nil || due.AttemptID != command.AttemptID || due.ParticipationID != command.ParticipationID || due.Generation != command.Generation {
				return ParticipationRenewal{}, unavailable(errors.New("inconsistent late-renewal expiry target"))
			}
			if _, expireErr := service.expireParticipation(ctx, *due); expireErr != nil {
				return ParticipationRenewal{}, expireErr
			}
			return ParticipationRenewal{}, &Fault{Code: "exam.attempt.connection_lost", Cause: err}
		}
		return ParticipationRenewal{}, mapStore(err)
	}
	if stored == nil || stored.AttemptID != command.AttemptID || stored.ParticipationID != command.ParticipationID ||
		stored.Generation != command.Generation || stored.AcceptedSequence != command.Sequence || stored.DatabaseTime.IsZero() ||
		stored.LeaseExpiresAt.IsZero() || !stored.LeaseExpiresAt.Equal(stored.DatabaseTime.Add(model.AttemptParticipationInitialLease)) {
		return ParticipationRenewal{}, unavailable(errors.New("inconsistent Participation renewal outcome"))
	}
	return ParticipationRenewal{
		AttemptID: stored.AttemptID, ParticipationID: stored.ParticipationID, Generation: stored.Generation,
		AcceptedSequence: stored.AcceptedSequence, DatabaseTime: model.TimeUTC(stored.DatabaseTime),
		LeaseExpiresAt: model.TimeUTC(stored.LeaseExpiresAt), Duplicate: stored.Duplicate,
	}, nil
}

func (service *Service) Connect(ctx context.Context, call Call, command ConnectCommand) (ConnectionResult, error) {
	principal := call.Principal()
	if command.Idempotency == nil {
		return ConnectionResult{}, &Fault{Code: "idempotency.key_required"}
	}
	if principal.Validate() != nil || principal.CredentialType != model.CredentialSessionAccess {
		return ConnectionResult{}, &Fault{Code: "authentication.invalid_token"}
	}
	if !command.SittingID.IsValid() {
		return ConnectionResult{}, invalid("exam_sitting_id")
	}
	if !model.IsValidCredentialToken(command.ContinuityCredential) {
		return ConnectionResult{}, invalid("continuity_credential")
	}
	snapshot, err := service.deps.Sittings.Resolve(ctx, command.SittingID)
	if err != nil {
		return ConnectionResult{}, mapStore(err)
	}
	if snapshot == nil || snapshot.Sitting == nil || snapshot.Sitting.ID != command.SittingID ||
		!snapshot.Sitting.ExamID.IsValid() || !snapshot.Sitting.ClassID.IsValid() {
		return ConnectionResult{}, unavailable(errors.New("incomplete Sitting ownership projection"))
	}
	sitting := snapshot.Sitting

	attemptID := service.deps.NewAttemptID()
	at := model.TimeUTC(service.deps.Now())
	workspaceID := service.deps.NewWorkspaceID()
	credentialHash := model.HashToken(command.ContinuityCredential)
	participationID, connectionID := service.deps.NewParticipation(), service.deps.NewConnection()

	auditID, err := service.deps.Auditor.Begin(ctx, call, model.ActionExamSittingParticipate,
		model.Resource{Type: model.ResourceExamSitting, ID: command.SittingID.String()}, model.RoleScopeClass, sitting.ClassID.String(),
		"exam_attempt_connect", map[string]any{"exam_sitting_id": command.SittingID.String()})
	if err != nil {
		return ConnectionResult{}, err
	}
	stored, err := service.deps.Persistence.Connect(ctx, &store.ExamAttemptConnect{
		SittingID: command.SittingID, CandidateUserID: principal.UserID, SessionID: principal.SessionID,
		AttemptID: attemptID, WorkspaceID: workspaceID, ParticipationID: participationID, ConnectionID: connectionID,
		ContinuityCredentialHash: credentialHash,
		AuditEventID:             auditID, AuditAt: model.MillisFromTime(at),
	}, command.Idempotency)
	if err != nil {
		return ConnectionResult{}, service.failAudit(ctx, auditID, err)
	}
	result, err := projectConnection(stored, sitting.ClassID, principal.UserID, principal.SessionID, command.SittingID)
	if err != nil {
		return ConnectionResult{}, err
	}
	if result.ConnectionOpened && !result.Replayed {
		if effectErr := service.deps.Effects.ConnectionOpened(ctx, result); effectErr != nil {
			service.deps.EffectFailures.Report(ctx, "exam_attempt_connection_opened", effectErr)
		}
	}
	return result, nil
}

type CloseConnectionCommand struct {
	AttemptID    model.ExamAttemptID
	SittingID    model.ExamSittingID
	ClassID      model.ClassID
	ConnectionID model.AttemptConnectionID
	Reason       model.AttemptConnectionCloseReason
}

type ConnectionClosedResult struct {
	AttemptID       model.ExamAttemptID
	SittingID       model.ExamSittingID
	CandidateUserID model.UserID
	Connection      model.AttemptConnection
	Changed         bool
}

func (service *Service) CloseConnection(ctx context.Context, call Call, command CloseConnectionCommand) (ConnectionClosedResult, error) {
	if call.Principal().Validate() != nil || !command.AttemptID.IsValid() || !command.SittingID.IsValid() ||
		!command.ClassID.IsValid() || !command.ConnectionID.IsValid() || !command.Reason.IsValid() {
		return ConnectionClosedResult{}, invalid("connection")
	}
	auditID, err := service.deps.Auditor.Begin(ctx, call, model.ActionExamSittingParticipate,
		model.Resource{Type: model.ResourceExamSitting, ID: command.SittingID.String()}, model.RoleScopeClass, command.ClassID.String(),
		"exam_attempt_connection_close", map[string]any{"exam_attempt_id": command.AttemptID.String(), "attempt_connection_id": command.ConnectionID.String()})
	if err != nil {
		return ConnectionClosedResult{}, err
	}
	stored, err := service.deps.Persistence.CloseConnection(ctx, &store.ExamAttemptConnectionClose{
		ConnectionID: command.ConnectionID, CandidateUserID: call.Principal().UserID, SessionID: call.Principal().SessionID,
		Reason: command.Reason, AuditEventID: auditID,
		AuditAt: model.MillisFromTime(model.TimeUTC(service.deps.Now())),
	})
	if err != nil {
		return ConnectionClosedResult{}, service.failAudit(ctx, auditID, err)
	}
	if stored == nil || stored.Connection == nil || stored.AttemptID != command.AttemptID || stored.SittingID != command.SittingID ||
		stored.Connection.ID != command.ConnectionID || stored.Connection.AttemptID != command.AttemptID || stored.Connection.Validate() != nil {
		return ConnectionClosedResult{}, unavailable(errors.New("inconsistent Connection close outcome"))
	}
	result := ConnectionClosedResult{AttemptID: stored.AttemptID, SittingID: stored.SittingID,
		CandidateUserID: stored.CandidateUserID, Connection: *stored.Connection, Changed: stored.Changed}
	if result.Changed {
		if effectErr := service.deps.Effects.ConnectionClosed(ctx, result); effectErr != nil {
			service.deps.EffectFailures.Report(ctx, "exam_attempt_connection_closed", effectErr)
		}
	}
	return result, nil
}

type CandidateAccess struct {
	AttemptID            model.ExamAttemptID
	ConnectionID         model.AttemptConnectionID
	ContinuityCredential string
}

type Presentation struct {
	AttemptID                  model.ExamAttemptID
	SittingID                  model.ExamSittingID
	AdmissionRevisionID        model.ExamRevisionID
	CurrentRevisionID          model.ExamRevisionID
	Title                      string
	InstructionsMarkdown       string
	FocusLossCollectionEnabled bool
	Resources                  []Resource
}

type Resource struct {
	ResourceID          model.ExamResourceID
	DisplayName         string
	DescriptionMarkdown string
	Position            int
	MediaType           model.ExamResourceMediaType
	SizeBytes           int64
	SHA256              string
}

func (service *Service) GetPresentation(ctx context.Context, call Call, access CandidateAccess) (Presentation, error) {
	selector, err := candidateSelector(call, access)
	if err != nil {
		return Presentation{}, err
	}
	stored, err := service.deps.Persistence.GetCandidatePresentation(ctx, selector)
	if err != nil {
		return Presentation{}, mapStore(err)
	}
	if stored == nil {
		return Presentation{}, unavailable(errors.New("missing candidate presentation"))
	}
	result := Presentation{AttemptID: stored.AttemptID, SittingID: stored.SittingID,
		AdmissionRevisionID: stored.AdmissionRevisionID, CurrentRevisionID: stored.CurrentRevisionID,
		Title: stored.Title, InstructionsMarkdown: safemarkdown.Sanitize(stored.InstructionsMarkdown),
		FocusLossCollectionEnabled: stored.FocusLossCollectionEnabled, Resources: make([]Resource, len(stored.Resources))}
	for index, item := range stored.Resources {
		result.Resources[index] = Resource{ResourceID: item.ResourceID, DisplayName: item.DisplayName,
			DescriptionMarkdown: safemarkdown.Sanitize(item.DescriptionMarkdown), Position: item.Position, MediaType: item.MediaType,
			SizeBytes: item.SizeBytes, SHA256: item.SHA256}
	}
	return result, nil
}

type WorkspaceQuery struct {
	Access         CandidateAccess
	ExpectedCursor int64
	AfterEntryID   model.AttemptWorkspaceEntryID
	Limit          int
}

type WorkspacePage struct {
	WorkspaceID     model.ExamAttemptWorkspaceID
	Cursor          int64
	Items           []store.CandidateAttemptWorkspaceItem
	HasMore         bool
	RefreshRequired bool
}

func (service *Service) ListWorkspace(ctx context.Context, call Call, query WorkspaceQuery) (WorkspacePage, error) {
	selector, err := candidateSelector(call, query.Access)
	if err != nil {
		return WorkspacePage{}, err
	}
	if query.Limit < 1 || query.Limit > model.AttemptWorkspaceJournalReadMaximum || query.ExpectedCursor < -1 ||
		(query.ExpectedCursor == -1 && !query.AfterEntryID.IsZero()) {
		return WorkspacePage{}, invalid("workspace_list")
	}
	page, err := service.deps.Workspace.List(ctx, store.CandidateWorkspaceListOptions{
		Access: selector, ExpectedCursor: query.ExpectedCursor, AfterEntryID: query.AfterEntryID, Limit: query.Limit,
	})
	if err != nil {
		return WorkspacePage{}, mapStore(err)
	}
	if page == nil || !page.WorkspaceID.IsValid() || page.Cursor < 0 ||
		(page.RefreshRequired && (len(page.Items) != 0 || page.HasMore)) {
		return WorkspacePage{}, unavailable(errors.New("missing candidate Workspace page"))
	}
	return WorkspacePage{WorkspaceID: page.WorkspaceID, Cursor: page.Cursor,
		Items:   append([]store.CandidateAttemptWorkspaceItem(nil), page.Items...),
		HasMore: page.HasMore, RefreshRequired: page.RefreshRequired}, nil
}

type OpenedContent struct {
	Body           io.ReadCloser
	MediaType      string
	SizeBytes      int64
	SHA256         string
	ContentVersion model.WorkspaceContentVersion
}

func (service *Service) OpenResource(ctx context.Context, call Call, access CandidateAccess, resourceID model.ExamResourceID) (*OpenedContent, error) {
	selector, err := candidateSelector(call, access)
	if err != nil {
		return nil, err
	}
	if !resourceID.IsValid() {
		return nil, invalid("exam_resource_id")
	}
	resolved, err := service.deps.Persistence.ResolveCandidateResource(ctx, selector, resourceID)
	if err != nil {
		return nil, mapStore(err)
	}
	if resolved == nil || resolved.Resource.ResourceID != resourceID || !resolved.FileRevisionID.IsValid() || !resolved.RenditionID.IsValid() {
		return nil, unavailable(errors.New("incomplete candidate Resource selector"))
	}
	body, err := service.deps.Content.OpenExamResource(ctx, resolved.FileRevisionID, resolved.RenditionID)
	if err != nil {
		return nil, unavailable(err)
	}
	return &OpenedContent{Body: body, MediaType: string(resolved.Resource.MediaType), SizeBytes: resolved.Resource.SizeBytes, SHA256: resolved.Resource.SHA256}, nil
}

func (service *Service) OpenWorkspaceFile(ctx context.Context, call Call, access CandidateAccess, entryID model.AttemptWorkspaceEntryID) (*OpenedContent, error) {
	selector, err := candidateSelector(call, access)
	if err != nil {
		return nil, err
	}
	if !entryID.IsValid() {
		return nil, invalid("attempt_workspace_entry_id")
	}
	resolved, err := service.deps.Workspace.ResolveFile(ctx, selector, entryID)
	if err != nil {
		return nil, mapStore(err)
	}
	if resolved == nil || resolved.Entry.EntryID != entryID || resolved.ContentVersion.IsZero() {
		return nil, unavailable(errors.New("incomplete candidate Workspace selector"))
	}
	var body io.ReadCloser
	switch resolved.StorageOrigin {
	case model.AttemptWorkspaceStorageStarter:
		if !resolved.StarterObjectID.IsValid() || !resolved.AttemptObjectID.IsValid() {
			return nil, unavailable(errors.New("inconsistent starter Workspace selector"))
		}
		body, err = service.deps.Content.OpenStarterWorkspaceObject(ctx, resolved.StarterObjectID)
	case model.AttemptWorkspaceStorageAttempt:
		if !resolved.AttemptObjectID.IsValid() || resolved.StarterObjectID.IsValid() {
			return nil, unavailable(errors.New("inconsistent attempt-origin Workspace selector"))
		}
		body, err = service.deps.Content.OpenAttemptWorkspaceObject(ctx, resolved.AttemptObjectID)
	default:
		return nil, unavailable(errors.New("invalid candidate Workspace storage origin"))
	}
	if err != nil {
		return nil, unavailable(err)
	}
	return &OpenedContent{Body: body, MediaType: resolved.Entry.MediaType, SizeBytes: resolved.Entry.SizeBytes,
		SHA256: resolved.Entry.SHA256, ContentVersion: resolved.ContentVersion}, nil
}

func candidateSelector(call Call, access CandidateAccess) (store.CandidateAttemptAccess, error) {
	principal := call.Principal()
	if principal.Validate() != nil || principal.CredentialType != model.CredentialSessionAccess {
		return store.CandidateAttemptAccess{}, &Fault{Code: "authentication.invalid_token"}
	}
	if !access.AttemptID.IsValid() || !access.ConnectionID.IsValid() || !model.IsValidCredentialToken(access.ContinuityCredential) {
		return store.CandidateAttemptAccess{}, invalid("candidate_access")
	}
	return store.CandidateAttemptAccess{AttemptID: access.AttemptID, CandidateUserID: principal.UserID,
		SessionID: principal.SessionID, ConnectionID: access.ConnectionID, ContinuityCredentialHash: model.HashToken(access.ContinuityCredential)}, nil
}

func projectConnection(stored *store.ExamAttemptConnectResult, classID model.ClassID, candidateID model.UserID,
	sessionID model.SessionID, sittingID model.ExamSittingID,
) (ConnectionResult, error) {
	if stored == nil || stored.Attempt == nil || stored.Workspace == nil || stored.Participation == nil || stored.Connection == nil ||
		stored.Attempt.Validate() != nil || stored.Workspace.Validate() != nil || !validParticipationView(stored.Participation) || stored.Connection.Validate() != nil ||
		stored.Attempt.CandidateUserID != candidateID || stored.Attempt.SittingID != sittingID || stored.Workspace.AttemptID != stored.Attempt.ID ||
		stored.ClassID != classID || stored.Participation.AttemptID != stored.Attempt.ID ||
		stored.Connection.AttemptID != stored.Attempt.ID || stored.Connection.ParticipationID != stored.Participation.ID ||
		stored.Connection.SessionID != sessionID {
		return ConnectionResult{}, unavailable(errors.New("inconsistent Exam Attempt connection outcome"))
	}
	if stored.Attempt.State != model.ExamAttemptActive || stored.Participation.State != model.AttemptParticipationActive ||
		stored.Connection.State != model.AttemptConnectionOpen {
		return ConnectionResult{}, &Fault{Code: "exam.attempt.connection_closed"}
	}
	return ConnectionResult{Attempt: *stored.Attempt, Workspace: *stored.Workspace, Participation: *stored.Participation,
		Connection: *stored.Connection, ClassID: classID, FirstAdmission: stored.FirstAdmission,
		ConnectionOpened: stored.ConnectionOpened, Replayed: stored.Replayed}, nil
}

func validParticipationView(view *store.ExamAttemptParticipationView) bool {
	if view == nil || !view.ID.IsValid() || !view.AttemptID.IsValid() || view.Generation < 1 || view.RenewalSequence < 0 ||
		view.StartedAt.IsZero() || view.UpdatedAt.IsZero() || view.UpdatedAt.Before(view.StartedAt) ||
		view.LeaseExpiresAt.IsZero() || view.LeaseExpiresAt.Before(view.StartedAt) {
		return false
	}
	switch view.State {
	case model.AttemptParticipationActive:
		return !view.EndedAt.Valid && view.EndReason == ""
	case model.AttemptParticipationEnded:
		return view.EndedAt.Valid && !view.EndedAt.Time.IsZero() && !view.EndedAt.Time.Before(view.StartedAt) &&
			!view.EndedAt.Time.After(view.UpdatedAt) && view.EndReason != ""
	default:
		return false
	}
}

func (service *Service) failAudit(ctx context.Context, auditID string, err error) error {
	mapped := mapStore(err)
	code := "exam.attempt.unavailable"
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
	return &Fault{Code: "exam.attempt.invalid", SafeFields: map[string]any{"field": field}}
}

func invalidCause(field string, cause error) error {
	return &Fault{Code: "exam.attempt.invalid", SafeFields: map[string]any{"field": field}, Cause: cause}
}

func unavailable(cause error) error { return &Fault{Code: "exam.attempt.unavailable", Cause: cause} }

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
		return &Fault{Code: "exam.attempt.not_found", Cause: err}
	case errors.As(err, &invalidInput):
		return invalidCause("value", err)
	case errors.As(err, &conflict):
		code := mapConflict(conflict.Constraint)
		return &Fault{Code: code, Cause: err}
	default:
		return unavailable(err)
	}
}

func mapConflict(constraint string) string {
	switch constraint {
	case "exam_attempt_membership":
		return "exam.attempt.not_found"
	case "exam_sitting_state", "exam_sitting_deadline_reached":
		return "exam.attempt.sitting_unavailable"
	case "exam_attempt_state":
		return "exam.attempt.state_conflict"
	case "exam_attempt_revision":
		return "exam.attempt.revision_conflict"
	case "attempt_suspension_active":
		return "exam.attempt.suspension_conflict"
	case "attempt_participation_credential":
		return "exam.attempt.continuity_invalid"
	case "attempt_participation_generation":
		return "exam.attempt.connection_closed"
	case "attempt_participation_sequence":
		return "exam.attempt.renewal_conflict"
	case "focus_loss_sequence":
		return "exam.attempt.focus_loss_conflict"
	case "attempt_participation_expired":
		return "exam.attempt.connection_lost"
	case "attempt_connection_open":
		return "exam.attempt.already_connected"
	case "attempt_connection_closed":
		return "exam.attempt.connection_closed"
	case "attempt_workspace_path":
		return "exam.attempt.workspace.path_conflict"
	case "attempt_workspace_entry":
		return "exam.attempt.workspace.entry_conflict"
	case "attempt_workspace_content_version":
		return "exam.attempt.workspace.content_conflict"
	case "attempt_workspace_not_empty":
		return "exam.attempt.workspace.directory_not_empty"
	case "attempt_workspace_entry_limit":
		return "exam.attempt.workspace.entry_limit"
	case "attempt_workspace_size_limit":
		return "exam.attempt.workspace.size_limit"
	case "attempt_workspace_object_state":
		return "exam.attempt.workspace.object_conflict"
	case "attempt_workspace_cursor":
		return "exam.attempt.workspace.cursor_conflict"
	default:
		return "exam.attempt.conflict"
	}
}
