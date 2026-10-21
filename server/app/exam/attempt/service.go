// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package attempt

import (
	"context"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/exam/safemarkdown"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type Sittings interface {
	// Resolve returns stable Sitting ownership metadata. Store remains the
	// authority for connect-time lifecycle, deadline, and membership checks.
	Resolve(context.Context, model.ExamSittingID) (*store.ExamSittingSnapshot, error)
}

type ManagerAuthorizer interface {
	AuthorizeSittingView(context.Context, invocation.Invocation, model.ExamSittingID) error
	AuthorizeSittingManage(context.Context, invocation.Invocation, model.ExamSittingID) (override bool, err error)
	AuthorizeSubmissionView(context.Context, invocation.Invocation, model.SubmissionID) error
	AuthorizeBrowserActivityView(context.Context, invocation.Invocation, model.ExamSittingID) (model.AcademicUnitID, error)
}

type Auditor interface {
	BeginAtScope(context.Context, invocation.Invocation, model.Action, model.Resource, model.RoleScopeType, string, string, map[string]any, map[string]any) (model.AuditEventID, error)
	PrepareAtScope(context.Context, invocation.Invocation, model.Action, model.Resource, model.RoleScopeType, string, string, map[string]any) (*model.AuditEvent, error)
	RecordAuthorizationDecision(context.Context, invocation.Invocation, model.Action, model.Resource, model.RoleScopeType, string, bool) error
	Fail(context.Context, model.AuditEventID, string) error
	Complete(context.Context, model.AuditEventID, map[string]any) error
}

// SystemAuditor creates actor-less audit attempts for trusted periodic
// continuity enforcement. A runtime scanner must never manufacture a User
// Principal merely to persist this system decision.
type SystemAuditor interface {
	Begin(context.Context, model.Action, model.Resource, model.RoleScopeType, string, string, map[string]any) (model.AuditEventID, error)
	Fail(context.Context, model.AuditEventID, string) error
}

type Effects interface {
	ConnectionOpened(context.Context, ConnectionResult) error
	ConnectionClosed(context.Context, ConnectionClosedResult) error
	ParticipationRenewed(context.Context, ParticipationRenewal) error
	ParticipationExpired(context.Context, ParticipationExpiry) error
	AttemptReallowed(context.Context, ReallowResult) error
	AttemptSuspendedByManager(context.Context, ManualSuspensionResult) error
	WorkspaceChanged(context.Context, WorkspaceMutationResult) error
	FocusLossEvaluated(context.Context, FocusLossEvaluation) error
	BrowserActivityGapChanged(context.Context, model.ExamID, model.ExamSittingID) error
	AttemptSubmitted(context.Context, SubmissionResult) error
	AttemptSealedForSittingClose(context.Context, AutomaticSubmissionResult) error
}

type EffectFailures interface {
	Report(context.Context, string, error)
}

type Content interface {
	OpenExamResource(context.Context, model.FileRevisionID, model.FileRenditionID) (io.ReadCloser, error)
	OpenStarterWorkspaceObject(context.Context, model.StarterWorkspaceObjectID) (io.ReadCloser, error)
	StageAttemptWorkspaceObject(context.Context, model.AttemptWorkspaceObjectID, io.Reader, int64, string) (*model.AttemptWorkspaceContent, error)
	OpenAttemptWorkspaceObject(context.Context, model.AttemptWorkspaceObjectID) (io.ReadCloser, error)
}

// SubmissionMailPreparation contains only the safe identity needed to freeze
// a candidate receipt. The implementation resolves recipient and published
// Exam title without exposing them to the Attempt package.
type SubmissionMailPreparation struct {
	CandidateUserID model.UserID
	ExamID          model.ExamID
	SittingID       model.ExamSittingID
	SubmissionID    model.SubmissionID
	SealedAt        time.Time
	Provenance      model.ExamSubmissionProvenance
}

type PreparedSubmissionMail struct {
	Notice                    *store.PreparedMail
	ExpectedRecipientRevision int64
}

type SubmissionMailPreparer interface {
	PrepareSubmissionReceipt(context.Context, SubmissionMailPreparation) (*PreparedSubmissionMail, error)
}

type DesktopBuildResolution struct {
	Build                       model.DesktopBuildTuple
	CompatibilityPolicyRevision int64
}

type NativeDeliveryBuildIdentity struct {
	ReleaseID    string
	MatrixID     string
	MatrixDigest string
	TargetTuple  string
}

type DesktopBuildResolver interface {
	ResolveNativeDeliveryBuild(context.Context, NativeDeliveryBuildIdentity) (model.DesktopBuildTuple, error)
	ResolveAttemptDesktopBuild(context.Context, model.Principal) (DesktopBuildResolution, error)
}

// Dependencies are the collaborators of Service; New validates them once and the
// service retains them unchanged.
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
	Mail                SubmissionMailPreparer
	DesktopBuilds       DesktopBuildResolver
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
	Operational         OperationalRecorder
}

// OperationalRecorder receives the outcome of each use case the module
// observes. It is optional; a module built without one records nothing.
type OperationalRecorder interface {
	RecordOperational(subsystem, event string, err error)
}

type Service struct {
	deps             Dependencies
	liveControls     controlAdmission
	deliveryControls controlAdmission
}

func (s *Service) recordOperational(event string, err error) {
	if s.deps.Operational != nil {
		s.deps.Operational.RecordOperational("exam_attempt", event, err)
	}
}

func New(deps Dependencies) (*Service, error) {
	if deps.Persistence == nil || deps.Workspace == nil || deps.Submissions == nil || deps.Sittings == nil || deps.Managers == nil || deps.Auditor == nil || deps.SystemAuditor == nil || deps.Effects == nil ||
		deps.EffectFailures == nil || deps.Content == nil || deps.Mail == nil || deps.DesktopBuilds == nil || deps.Now == nil || deps.NewAttemptID == nil ||
		deps.NewWorkspaceID == nil || deps.NewParticipation == nil || deps.NewConnection == nil || deps.NewEvidence == nil ||
		deps.NewFlag == nil || deps.NewSuspension == nil || deps.NewFocusLossSignal == nil || deps.NewDiscrepancy == nil || deps.NewWorkspaceEntry == nil || deps.NewWorkspaceObject == nil || deps.NewWorkspaceVersion == nil || deps.NewSubmission == nil {
		return nil, errors.New("Exam Attempt dependencies are required")
	}
	return &Service{deps: deps, liveControls: controlAdmission{lane: liveControlLane}}, nil
}

type GetManagedAttemptQuery struct {
	ExamID    model.ExamID
	SittingID model.ExamSittingID
	AttemptID model.ExamAttemptID
}

func (s *Service) GetExamAttempt(ctx context.Context, invocation invocation.Invocation, query GetManagedAttemptQuery) (_ store.ExamAttemptManagerSnapshot, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	if !query.ExamID.IsValid() || !query.SittingID.IsValid() || !query.AttemptID.IsValid() {
		return store.ExamAttemptManagerSnapshot{}, apperror.Invalid("exam.attempt.invalid", "attempt")
	}
	// Authorization precedes every Attempt read so a caller without Sitting
	// view authority cannot distinguish existing, mismatched, or missing
	// Attempts; the authorizer records that decision.
	if err := s.deps.Managers.AuthorizeSittingView(ctx, invocation, query.SittingID); err != nil {
		return store.ExamAttemptManagerSnapshot{}, err
	}
	sitting, err := s.deps.Sittings.Resolve(ctx, query.SittingID)
	if err != nil {
		return store.ExamAttemptManagerSnapshot{}, storeError(storeErrors, err)
	}
	if sitting == nil || sitting.Sitting == nil || sitting.Sitting.ID != query.SittingID || !sitting.AcademicUnitID.IsValid() {
		return store.ExamAttemptManagerSnapshot{}, apperror.New("exam.attempt.unavailable").Wrap(errors.New("inconsistent managed Attempt Sitting projection"))
	}
	if sitting.Sitting.ExamID != query.ExamID {
		return store.ExamAttemptManagerSnapshot{}, apperror.New("exam.attempt.not_found")
	}
	snapshot, err := s.deps.Persistence.Get(ctx, query.ExamID, query.AttemptID)
	if err != nil {
		return store.ExamAttemptManagerSnapshot{}, storeError(storeErrors, err)
	}
	if snapshot == nil || snapshot.Attempt == nil || snapshot.Attempt.Validate() != nil ||
		snapshot.Attempt.ExamID != query.ExamID || !validActiveSuspension(snapshot.ActiveSuspension, snapshot.Attempt.ID) {
		return store.ExamAttemptManagerSnapshot{}, apperror.New("exam.attempt.unavailable").Wrap(errors.New("inconsistent managed Attempt projection"))
	}
	if snapshot.Attempt.SittingID != query.SittingID {
		return store.ExamAttemptManagerSnapshot{}, apperror.New("exam.attempt.not_found")
	}
	if snapshot.Attempt.CandidateUserID == invocation.Principal().UserID {
		resource := model.Resource{Type: model.ResourceExamSitting, ID: snapshot.Attempt.SittingID.String()}
		auditID, auditErr := s.deps.Auditor.BeginAtScope(ctx, invocation, model.ActionExamSittingView, resource,
			model.RoleScopeAcademicUnit, sitting.AcademicUnitID.String(), "view_managed_attempt", map[string]any{
				"exam_id": snapshot.Attempt.ExamID.String(), "exam_sitting_id": snapshot.Attempt.SittingID.String(),
				"exam_attempt_id": snapshot.Attempt.ID.String(),
			}, nil)
		if auditErr != nil {
			return store.ExamAttemptManagerSnapshot{}, auditErr
		}
		if auditErr = s.deps.Auditor.Fail(ctx, auditID, "exam.attempt.not_found"); auditErr != nil {
			return store.ExamAttemptManagerSnapshot{}, auditErr
		}
		return store.ExamAttemptManagerSnapshot{}, apperror.New("exam.attempt.not_found")
	}
	clone := cloneManagerSnapshot(snapshot)
	if clone == nil {
		return store.ExamAttemptManagerSnapshot{}, apperror.New("exam.attempt.unavailable")
	}
	return *clone, nil
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

type CandidateActivityQuery struct {
	BeforeScheduledStart time.Time
	BeforeSittingID      model.ExamSittingID
	Limit                int
}

type CandidateActivityPage struct {
	ServerTime time.Time
	Items      []store.CandidateExamActivityItem
	HasMore    bool
}

func (s *Service) ListCandidateExamActivity(ctx context.Context, invocation invocation.Invocation, query CandidateActivityQuery) (_ CandidateActivityPage, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	principal := invocation.Principal()
	if principal.Validate() != nil || principal.CredentialType != model.CredentialSessionAccess || query.Limit < 1 || query.Limit > 200 ||
		(query.BeforeScheduledStart.IsZero() != query.BeforeSittingID.IsZero()) {
		return CandidateActivityPage{}, apperror.Invalid("exam.attempt.invalid", "candidate_activity")
	}
	options := store.CandidateExamActivityListOptions{CandidateUserID: principal.UserID,
		BeforeScheduledStart: model.TimeUTC(query.BeforeScheduledStart), BeforeSittingID: query.BeforeSittingID, Limit: query.Limit + 1}
	if principal.HasRegisteredDesktopKey() {
		options.DesktopSessionID = principal.SessionID
		options.DesktopRegistrationID = principal.DesktopRegistrationID
		options.DPoPKeyThumbprint = principal.DPoPKeyThumbprint
	}
	stored, err := s.deps.Persistence.ListCandidateActivity(ctx, options)
	if err != nil {
		return CandidateActivityPage{}, storeError(storeErrors, err)
	}
	if stored == nil || stored.ServerTime.IsZero() || stored.Items == nil {
		return CandidateActivityPage{}, apperror.New("exam.attempt.unavailable").Wrap(errors.New("incomplete candidate activity projection"))
	}
	page := CandidateActivityPage{ServerTime: model.TimeUTC(stored.ServerTime),
		Items: make([]store.CandidateExamActivityItem, 0, min(len(stored.Items), query.Limit)), HasMore: len(stored.Items) > query.Limit}
	for index, item := range stored.Items {
		if index == query.Limit {
			break
		}
		item.AllowedActions = append([]store.CandidateExamAllowedAction(nil), item.AllowedActions...)
		page.Items = append(page.Items, item)
	}
	return page, nil
}

type SittingCandidateStatusesQuery struct {
	ExamID               model.ExamID
	SittingID            model.ExamSittingID
	AfterCandidateUserID model.UserID
	Limit                int
}

type SittingCandidateStatusesPage struct {
	store.SittingCandidateStatusPage
	HasMore bool
}

func (s *Service) ListSittingCandidateStatuses(ctx context.Context, invocation invocation.Invocation,
	query SittingCandidateStatusesQuery,
) (_ SittingCandidateStatusesPage, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	principal := invocation.Principal()
	if principal.Validate() != nil || !query.ExamID.IsValid() || !query.SittingID.IsValid() || query.Limit < 1 || query.Limit > 200 ||
		!query.AfterCandidateUserID.IsZero() && !query.AfterCandidateUserID.IsValid() {
		return SittingCandidateStatusesPage{}, apperror.Invalid("exam.attempt.invalid", "candidate_statuses")
	}
	if err := s.deps.Managers.AuthorizeSittingView(ctx, invocation, query.SittingID); err != nil {
		return SittingCandidateStatusesPage{}, err
	}
	reallowAuthorized := false
	if _, err := s.deps.Managers.AuthorizeSittingManage(ctx, invocation, query.SittingID); err == nil {
		reallowAuthorized = true
	} else {
		var fault *apperror.Error
		if !errors.As(err, &fault) || fault.Code() != "authorization.denied" {
			return SittingCandidateStatusesPage{}, err
		}
	}
	stored, err := s.deps.Persistence.ListSittingCandidateStatuses(ctx, store.SittingCandidateStatusListOptions{
		ExamID: query.ExamID, SittingID: query.SittingID, ExcludeCandidateUserID: principal.UserID,
		AfterCandidateUserID: query.AfterCandidateUserID, ReallowAuthorized: reallowAuthorized, Limit: query.Limit + 1,
	})
	if err != nil {
		return SittingCandidateStatusesPage{}, storeError(storeErrors, err)
	}
	if stored == nil || stored.ExamID != query.ExamID || stored.SittingID != query.SittingID || stored.ServerTime.IsZero() ||
		!stored.SittingState.IsValid() || stored.SittingRevision < 1 || stored.Items == nil {
		return SittingCandidateStatusesPage{}, apperror.New("exam.attempt.unavailable").Wrap(errors.New("incomplete Sitting candidate-status projection"))
	}
	page := SittingCandidateStatusesPage{SittingCandidateStatusPage: *stored, HasMore: len(stored.Items) > query.Limit}
	page.Items = append([]store.SittingCandidateStatusItem(nil), stored.Items[:min(len(stored.Items), query.Limit)]...)
	for i := range page.Items {
		if page.Items[i].NativeSecurity != nil {
			if page.Items[i].NativeSecurity.Validate() != nil {
				return SittingCandidateStatusesPage{}, apperror.New("exam.attempt.unavailable").Wrap(model.ErrNativeDeliveryInvalid)
			}
			value := page.Items[i].NativeSecurity.Clone()
			page.Items[i].NativeSecurity = &value
		}
	}

	return page, nil
}

func (s *Service) ListExamAttempts(ctx context.Context, invocation invocation.Invocation, query ListManagedAttemptsQuery) (_ ManagedAttemptPage, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	if !query.ExamID.IsValid() || !query.SittingID.IsValid() || query.Limit < 1 || query.Limit > 200 ||
		(query.BeforeCreatedAt.IsZero() != query.BeforeAttemptID.IsZero()) {
		return ManagedAttemptPage{}, apperror.Invalid("exam.attempt.invalid", "attempt_list")
	}
	if err := s.deps.Managers.AuthorizeSittingView(ctx, invocation, query.SittingID); err != nil {
		return ManagedAttemptPage{}, err
	}
	rows, err := s.deps.Persistence.List(ctx, store.ExamAttemptManagerListOptions{
		ExamID: query.ExamID, SittingID: query.SittingID, ExcludeCandidateUserID: invocation.Principal().UserID,
		States:          append([]model.ExamAttemptState(nil), query.States...),
		BeforeCreatedAt: model.TimeUTC(query.BeforeCreatedAt), BeforeAttemptID: query.BeforeAttemptID, Limit: query.Limit + 1,
	})
	if err != nil {
		return ManagedAttemptPage{}, storeError(storeErrors, err)
	}
	page := ManagedAttemptPage{Items: make([]store.ExamAttemptManagerSnapshot, 0, min(len(rows), query.Limit)), HasMore: len(rows) > query.Limit}
	for index, row := range rows {
		if index == query.Limit {
			break
		}
		if row.Attempt == nil || row.Attempt.Validate() != nil || row.Attempt.ExamID != query.ExamID || row.Attempt.SittingID != query.SittingID ||
			!validActiveSuspension(row.ActiveSuspension, row.Attempt.ID) {
			return ManagedAttemptPage{}, apperror.New("exam.attempt.unavailable").Wrap(errors.New("inconsistent managed Attempt page"))
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
	return view.ID.IsValid() && view.AttemptID == attemptID && view.ParticipationID.IsValid() && validSuspensionProvenance(view) &&
		view.Generation > 0 && view.State == model.AttemptSuspensionActive && !view.StartedAt.IsZero() &&
		!view.EndedAt.Valid && view.ReallowedByUserID.IsZero()
}

type ConnectCommand struct {
	Security                         model.ConnectSecurity
	SittingID                        model.ExamSittingID
	ContinuityCredential             string
	ConfigurationManifestFingerprint string
	InitialConfiguration             *model.AttemptConfigurationCandidate
	IdempotencyKey                   string
}

type ConnectionResult struct {
	Security                  model.AdmittedSecurity
	Attempt                   model.ExamAttempt
	Workspace                 model.ExamAttemptWorkspace
	Participation             store.ExamAttemptParticipationView
	Connection                model.AttemptConnection
	ClassID                   model.ClassID
	Configuration             model.AttemptConfiguration
	RuntimeCapabilities       store.CandidateRuntimeCapabilities
	BrowserPolicy             *store.CandidateBrowserPolicy
	LiveCorrections           []model.CandidateLiveCorrection
	FirstAdmission            bool
	ConnectionOpened          bool
	RevokedSessionIDs         []string
	RevokedAccessTokenDigests []string
	Replayed                  bool
}

type RenewParticipationCommand struct {
	SecurityCoverage     model.SecurityCoverageRenewal
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
	SecurityCoverage model.SecurityCoverageResult
	AttemptID        model.ExamAttemptID
	ExamID           model.ExamID
	SittingID        model.ExamSittingID
	CandidateUserID  model.UserID
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
	IdempotencyKey          string
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

func (s *Service) ReallowExamAttempt(ctx context.Context, invocation invocation.Invocation, command ReallowCommand) (_ ReallowResult, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	principal := invocation.Principal()
	if principal.Validate() != nil {
		return ReallowResult{}, apperror.New("authentication.token_invalid")
	}
	if !command.ExamID.IsValid() || !command.SittingID.IsValid() || !command.AttemptID.IsValid() ||
		!command.SuspensionID.IsValid() || command.ExpectedAttemptRevision < 1 || !validReallowReason(command.PrivateReason) {
		return ReallowResult{}, apperror.Invalid("exam.attempt.invalid", "reallow")
	}
	idempotency, err := prepareReallowIdempotency(invocation, command)
	if err != nil {
		return ReallowResult{}, err
	}
	// Authorize the exact Sitting before reading the Attempt so existence is
	// not probeable; later exits complete the audit attempt as failed.
	override, err := s.deps.Managers.AuthorizeSittingManage(ctx, invocation, command.SittingID)
	if err != nil {
		return ReallowResult{}, err
	}
	snapshot, err := s.deps.Sittings.Resolve(ctx, command.SittingID)
	if err != nil {
		return ReallowResult{}, storeError(storeErrors, err)
	}
	if snapshot == nil || snapshot.Sitting == nil || snapshot.Sitting.ID != command.SittingID || !snapshot.Sitting.ClassID.IsValid() {
		return ReallowResult{}, apperror.New("exam.attempt.unavailable").Wrap(errors.New("incomplete Sitting ownership projection"))
	}
	action := model.ActionExamSittingManage
	if override {
		action = model.ActionExamSittingManageOverride
	}
	auditID, err := s.deps.Auditor.BeginAtScope(ctx, invocation, action,
		model.Resource{Type: model.ResourceExamSitting, ID: command.SittingID.String()}, model.RoleScopeClass, snapshot.Sitting.ClassID.String(),
		store.ExamAttemptReallowOperation, map[string]any{"exam_id": command.ExamID.String(), "exam_sitting_id": command.SittingID.String(),
			"exam_attempt_id": command.AttemptID.String(), "suspension_id": command.SuspensionID.String(),
			"expected_attempt_revision": command.ExpectedAttemptRevision}, nil)
	if err != nil {
		return ReallowResult{}, err
	}
	if snapshot.Sitting.ExamID != command.ExamID {
		return ReallowResult{}, s.failAudit(ctx, auditID, apperror.New("exam.attempt.not_found"))
	}
	managerSnapshot, err := s.deps.Persistence.Get(ctx, command.ExamID, command.AttemptID)
	if err != nil {
		return ReallowResult{}, s.failAudit(ctx, auditID, err)
	}
	if managerSnapshot == nil || managerSnapshot.Attempt == nil || managerSnapshot.Attempt.Validate() != nil ||
		managerSnapshot.Attempt.ExamID != command.ExamID ||
		!validActiveSuspension(managerSnapshot.ActiveSuspension, managerSnapshot.Attempt.ID) {
		return ReallowResult{}, s.failAudit(ctx, auditID, apperror.New("exam.attempt.unavailable").Wrap(errors.New("inconsistent re-allow Attempt projection")))
	}
	if managerSnapshot.Attempt.SittingID != command.SittingID || managerSnapshot.Attempt.CandidateUserID == principal.UserID {
		return ReallowResult{}, s.failAudit(ctx, auditID, apperror.New("exam.attempt.not_found"))
	}
	at := model.TimeUTC(s.deps.Now())
	stored, err := s.deps.Persistence.ReallowAttempt(ctx, &store.ExamAttemptReallow{
		ExamID: command.ExamID, SittingID: command.SittingID, AttemptID: command.AttemptID, SuspensionID: command.SuspensionID,
		ActorUserID: principal.UserID, ManagerOverride: override, ExpectedAttemptRevision: command.ExpectedAttemptRevision,
		PrivateReason: command.PrivateReason, AuditEventID: auditID, AuditAt: model.MillisFromTime(at),
	}, idempotency)
	if err != nil {
		return ReallowResult{}, s.failAudit(ctx, auditID, err)
	}
	if stored == nil || stored.Attempt == nil || stored.Suspension == nil || stored.ExamID != command.ExamID ||
		stored.SittingID != command.SittingID || stored.ClassID != snapshot.Sitting.ClassID || !stored.CandidateUserID.IsValid() ||
		stored.Attempt.ID != command.AttemptID || stored.Attempt.ExamID != command.ExamID || stored.Attempt.SittingID != command.SittingID ||
		stored.Attempt.CandidateUserID != stored.CandidateUserID || stored.Attempt.State != model.ExamAttemptReady || stored.Attempt.Validate() != nil ||
		stored.Attempt.Revision != command.ExpectedAttemptRevision+1 || stored.Suspension.ID != command.SuspensionID ||
		!validClosedSuspension(stored.Suspension, command.AttemptID, principal.UserID) {
		return ReallowResult{}, apperror.New("exam.attempt.unavailable").Wrap(errors.New("inconsistent Attempt re-allow outcome"))
	}
	result := ReallowResult{ExamID: stored.ExamID, SittingID: stored.SittingID, ClassID: stored.ClassID,
		CandidateUserID: stored.CandidateUserID, Attempt: *stored.Attempt, Suspension: *stored.Suspension, Replayed: stored.Replayed}
	if !result.Replayed {
		if effectErr := s.deps.Effects.AttemptReallowed(ctx, result); effectErr != nil {
			s.deps.EffectFailures.Report(ctx, "exam_attempt_reallowed", effectErr)
		}
	}
	return result, nil
}

func validReallowReason(reason string) bool {
	return utf8.ValidString(reason) && reason == strings.TrimSpace(reason) && utf8.RuneCountInString(reason) >= 1 &&
		utf8.RuneCountInString(reason) <= model.AttemptSuspensionPrivateReasonMaximumRunes && len(reason) <= 4000
}

// reallowableSuspensionReason names the policy suspension reasons a manager
// may re-allow. Active and closed validation share it so the post-commit
// check cannot reject an outcome the Store deliberately committed.
func reallowableSuspensionReason(reason model.AttemptSuspensionCandidateReason) bool {
	return reason == model.AttemptSuspensionCandidateReasonSecureContinuityLost ||
		reason == model.AttemptSuspensionCandidateReasonFocusLossPolicy
}

// validSuspensionProvenance accepts a policy episode that links its Flag or a
// manual episode (an Exam Manager's kick) that links its suspending manager.
// Every re-allowable episode passes it in both its active and closed state.
func validSuspensionProvenance(view *store.ExamAttemptSuspensionView) bool {
	switch view.Source {
	case model.AttemptSuspensionSourcePolicy:
		return view.FlagID.IsValid() && reallowableSuspensionReason(view.CandidateReason) && view.SuspendedByUserID.IsZero()
	case model.AttemptSuspensionSourceManual:
		return view.FlagID.IsZero() && view.CandidateReason == model.AttemptSuspensionCandidateReasonManagerSuspended &&
			view.SuspendedByUserID.IsValid()
	default:
		return false
	}
}

func validClosedSuspension(view *store.ExamAttemptSuspensionView, attemptID model.ExamAttemptID, actorID model.UserID) bool {
	return view != nil && view.ID.IsValid() && view.AttemptID == attemptID && view.ParticipationID.IsValid() && validSuspensionProvenance(view) &&
		view.Generation > 0 && view.State == model.AttemptSuspensionClosed && !view.StartedAt.IsZero() &&
		view.EndedAt.Valid && !view.EndedAt.Time.Before(view.StartedAt) && view.ReallowedByUserID == actorID
}

// ScanExpiredParticipations performs one bounded runtime maintenance pass.
// Every listed candidate is rechecked by the named conditional Store
// operation; PostgreSQL, never this loop or its clock, decides expiry.
func (s *Service) ScanExpiredParticipations(ctx context.Context, limit int) (ExpiryScanResult, error) {
	if limit < 1 || limit > 200 {
		return ExpiryScanResult{}, apperror.Invalid("exam.attempt.invalid", "expiry_limit")
	}
	due, err := s.deps.Persistence.ListExpiredParticipations(ctx, limit)
	if err != nil {
		return ExpiryScanResult{}, storeError(storeErrors, err)
	}
	result := ExpiryScanResult{Due: len(due)}
	for _, item := range due {
		expired, expireErr := s.expireParticipation(ctx, item)
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

func (s *Service) expireParticipation(ctx context.Context, due store.ExamAttemptParticipationExpiryDue) (ParticipationExpiry, error) {
	if !validExpiryDue(due) {
		return ParticipationExpiry{}, apperror.New("exam.attempt.unavailable").Wrap(errors.New("invalid Participation expiry target"))
	}
	auditID, err := s.deps.SystemAuditor.Begin(ctx, model.ActionExamSittingManage,
		model.Resource{Type: model.ResourceExamSitting, ID: due.SittingID.String()}, model.RoleScopeClass, due.ClassID.String(),
		store.ExamAttemptExpireParticipationOperation, map[string]any{
			"exam_id": due.ExamID.String(), "exam_sitting_id": due.SittingID.String(), "exam_attempt_id": due.AttemptID.String(),
			"participation_id": due.ParticipationID.String(), "generation": due.Generation,
		})
	if err != nil {
		return ParticipationExpiry{}, err
	}
	at := model.TimeUTC(s.deps.Now())
	stored, err := s.deps.Persistence.ExpireParticipation(ctx, &store.ExamAttemptParticipationExpiry{
		AttemptID: due.AttemptID, ParticipationID: due.ParticipationID, Generation: due.Generation,
		EvidenceID: s.deps.NewEvidence(), FlagID: s.deps.NewFlag(), SuspensionID: s.deps.NewSuspension(),
		AuditEventID: auditID, AuditAt: model.MillisFromTime(at),
	})
	if err != nil {
		mapped := storeError(storeErrors, err)
		code := apperror.CodeOf(mapped, "exam.attempt.unavailable")
		if auditErr := s.deps.SystemAuditor.Fail(ctx, auditID, code); auditErr != nil {
			return ParticipationExpiry{}, errors.Join(mapped, auditErr)
		}
		return ParticipationExpiry{}, mapped
	}
	result, err := projectExpiry(stored, due)
	if err != nil {
		return ParticipationExpiry{}, err
	}
	if !result.Replayed {
		if effectErr := s.deps.Effects.ParticipationExpired(ctx, result); effectErr != nil {
			s.deps.EffectFailures.Report(ctx, "exam_attempt_participation_expired", effectErr)
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
		return ParticipationExpiry{}, apperror.New("exam.attempt.unavailable").Wrap(errors.New("inconsistent Participation expiry outcome"))
	}
	return ParticipationExpiry{ExamID: stored.ExamID, SittingID: stored.SittingID, ClassID: stored.ClassID,
		CandidateUserID: stored.CandidateUserID, Attempt: *stored.Attempt, Participation: *stored.Participation,
		Connection: *stored.Connection, ConnectionClosed: stored.ConnectionClosed, Evidence: *stored.Evidence, Flag: *stored.Flag, Suspension: *stored.Suspension,
		DatabaseTime: model.TimeUTC(stored.DatabaseTime), Replayed: stored.Replayed}, nil
}

// RenewParticipation authenticates a continuity renewal independently of the
// WebSocket transport ping. Store owns the database clock, exclusive expiry
// boundary, duplicate outcome, and permanent generation fence.
func (s *Service) RenewExamAttemptParticipation(ctx context.Context, invocation invocation.Invocation, command RenewParticipationCommand) (_ ParticipationRenewal, resultErr error) {
	defer func() { s.recordOperational("renew", resultErr) }()
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	release, admissionErr := s.enterControl(ctx, invocation, true)
	if admissionErr != nil {
		return ParticipationRenewal{}, admissionErr
	}
	defer release()
	principal := invocation.Principal()
	if principal.Validate() != nil || principal.CredentialType != model.CredentialSessionAccess {
		return ParticipationRenewal{}, apperror.New("authentication.token_invalid")
	}
	if !principal.HasRegisteredDesktopKey() {
		return ParticipationRenewal{}, apperror.New("exam.attempt.registered_desktop_required")
	}
	if !command.AttemptID.IsValid() || !command.ParticipationID.IsValid() || !command.ConnectionID.IsValid() ||
		command.Generation < 1 || command.Sequence < 1 || command.SecurityCoverage.Validate() != nil || !model.IsValidCredentialToken(command.ContinuityCredential) {
		return ParticipationRenewal{}, apperror.Invalid("exam.attempt.invalid", "renewal")
	}
	build, err := s.deps.DesktopBuilds.ResolveAttemptDesktopBuild(ctx, principal)
	if err != nil || build.Build.Validate() != nil || build.CompatibilityPolicyRevision < 1 {
		return ParticipationRenewal{}, apperror.New("exam.attempt.desktop_incompatible").Wrap(err)
	}
	stored, err := s.deps.Persistence.RenewParticipation(ctx, &store.ExamAttemptParticipationRenewal{
		SecurityCoverage: command.SecurityCoverage, DesktopBuild: build.Build,
		AttemptID: command.AttemptID, ParticipationID: command.ParticipationID, ConnectionID: command.ConnectionID,
		CandidateUserID: principal.UserID, SessionID: principal.SessionID, Generation: command.Generation,
		DesktopRegistrationID: principal.DesktopRegistrationID, DPoPKeyThumbprint: principal.DPoPKeyThumbprint,
		DesktopCompatibilityPolicyRevision: build.CompatibilityPolicyRevision,
		Sequence:                           command.Sequence, ContinuityCredentialHash: model.HashToken(command.ContinuityCredential),
	})
	if err != nil {
		var conflict *store.ErrConflict
		if errors.As(err, &conflict) && conflict.Constraint == "attempt_participation_expired" {
			due, resolveErr := s.deps.Persistence.ResolveParticipationExpiry(ctx, command.AttemptID, command.ParticipationID, command.Generation)
			if resolveErr != nil {
				if store.IsNotFound(resolveErr) {
					return ParticipationRenewal{}, apperror.New("exam.attempt.connection_lost").Wrap(err)
				}
				return ParticipationRenewal{}, storeError(storeErrors, resolveErr)
			}
			if due == nil || due.AttemptID != command.AttemptID || due.ParticipationID != command.ParticipationID || due.Generation != command.Generation {
				return ParticipationRenewal{}, apperror.New("exam.attempt.unavailable").Wrap(errors.New("inconsistent late-renewal expiry target"))
			}
			if _, expireErr := s.expireParticipation(ctx, *due); expireErr != nil {
				return ParticipationRenewal{}, expireErr
			}
			return ParticipationRenewal{}, apperror.New("exam.attempt.connection_lost").Wrap(err)
		}
		return ParticipationRenewal{}, storeError(storeErrors, err)
	}
	if stored == nil || stored.SecurityCoverage.Validate() != nil || stored.AttemptID != command.AttemptID || stored.ParticipationID != command.ParticipationID ||
		!stored.ExamID.IsValid() || !stored.SittingID.IsValid() || stored.CandidateUserID != principal.UserID ||
		stored.Generation != command.Generation || stored.AcceptedSequence != command.Sequence || stored.DatabaseTime.IsZero() ||
		stored.LeaseExpiresAt.IsZero() || !stored.LeaseExpiresAt.After(stored.DatabaseTime) || stored.LeaseExpiresAt.After(stored.DatabaseTime.Add(model.AttemptParticipationInitialLease)) {
		return ParticipationRenewal{}, apperror.New("exam.attempt.unavailable").Wrap(errors.New("inconsistent Participation renewal outcome"))
	}
	result := ParticipationRenewal{SecurityCoverage: stored.SecurityCoverage,
		AttemptID: stored.AttemptID, ExamID: stored.ExamID, SittingID: stored.SittingID,
		CandidateUserID: stored.CandidateUserID, ParticipationID: stored.ParticipationID, Generation: stored.Generation,
		AcceptedSequence: stored.AcceptedSequence, DatabaseTime: model.TimeUTC(stored.DatabaseTime),
		LeaseExpiresAt: model.TimeUTC(stored.LeaseExpiresAt), Duplicate: stored.Duplicate,
	}
	if !result.Duplicate {
		if effectErr := s.deps.Effects.ParticipationRenewed(ctx, result); effectErr != nil {
			s.deps.EffectFailures.Report(ctx, "exam_attempt_participation_renewed", effectErr)
		}
	}
	return result, nil
}

func (s *Service) ConnectExamAttempt(ctx context.Context, invocation invocation.Invocation, command ConnectCommand) (_ ConnectionResult, resultErr error) {
	defer func() { s.recordOperational("connect", resultErr) }()
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	if command.Security.Validate() != nil {
		return ConnectionResult{}, apperror.Invalid("exam.attempt.invalid", "security")
	}
	principal := invocation.Principal()
	if principal.Validate() != nil || principal.CredentialType != model.CredentialSessionAccess {
		return ConnectionResult{}, apperror.New("authentication.token_invalid")
	}
	if !principal.HasRegisteredDesktopKey() {
		return ConnectionResult{}, apperror.New("exam.attempt.registered_desktop_required")
	}
	if !command.SittingID.IsValid() {
		return ConnectionResult{}, apperror.Invalid("exam.attempt.invalid", "exam_sitting_id")
	}
	if !model.IsValidCredentialToken(command.ContinuityCredential) {
		return ConnectionResult{}, apperror.Invalid("exam.attempt.invalid", "continuity_credential")
	}
	build, err := s.deps.DesktopBuilds.ResolveAttemptDesktopBuild(ctx, principal)
	if err != nil || build.Build.Validate() != nil || build.CompatibilityPolicyRevision < 1 {
		return ConnectionResult{}, apperror.New("exam.attempt.desktop_incompatible").Wrap(err)
	}
	if err = validateConnectConfiguration(command, build.Build); err != nil {
		return ConnectionResult{}, err
	}
	idempotency, err := prepareConnectIdempotency(invocation, command)
	if err != nil {
		return ConnectionResult{}, err
	}
	snapshot, err := s.deps.Sittings.Resolve(ctx, command.SittingID)
	if err != nil {
		return ConnectionResult{}, storeError(storeErrors, err)
	}
	if snapshot == nil || snapshot.Sitting == nil || snapshot.Sitting.ID != command.SittingID ||
		!snapshot.Sitting.ExamID.IsValid() || !snapshot.Sitting.ClassID.IsValid() {
		return ConnectionResult{}, apperror.New("exam.attempt.unavailable").Wrap(errors.New("incomplete Sitting ownership projection"))
	}
	sitting := snapshot.Sitting

	attemptID := s.deps.NewAttemptID()
	at := model.TimeUTC(s.deps.Now())
	workspaceID := s.deps.NewWorkspaceID()
	credentialHash := model.HashToken(command.ContinuityCredential)
	participationID, connectionID := s.deps.NewParticipation(), s.deps.NewConnection()

	auditID, err := s.deps.Auditor.BeginAtScope(ctx, invocation, model.ActionExamSittingParticipate,
		model.Resource{Type: model.ResourceExamSitting, ID: command.SittingID.String()}, model.RoleScopeClass, sitting.ClassID.String(),
		"exam_attempt_connect", map[string]any{"exam_sitting_id": command.SittingID.String()}, nil)
	if err != nil {
		return ConnectionResult{}, err
	}
	stored, err := s.deps.Persistence.Connect(ctx, &store.ExamAttemptConnect{
		Security:  command.Security,
		SittingID: command.SittingID, CandidateUserID: principal.UserID, SessionID: principal.SessionID,
		DesktopRegistrationID: principal.DesktopRegistrationID, DPoPKeyThumbprint: principal.DPoPKeyThumbprint,
		AttemptID: attemptID, WorkspaceID: workspaceID, ParticipationID: participationID, ConnectionID: connectionID,
		ContinuityCredentialHash:           credentialHash,
		ConfigurationManifestFingerprint:   command.ConfigurationManifestFingerprint,
		InitialConfiguration:               cloneAttemptConfigurationPointer(command.InitialConfiguration),
		DesktopBuild:                       build.Build,
		DesktopCompatibilityPolicyRevision: build.CompatibilityPolicyRevision,
		AuditEventID:                       auditID, AuditAt: model.MillisFromTime(at),
	}, idempotency)
	if err != nil {
		return ConnectionResult{}, s.failAudit(ctx, auditID, err)
	}
	result, err := projectConnection(stored, sitting.ClassID, principal.UserID, principal.SessionID, command.SittingID)
	if err != nil {
		return ConnectionResult{}, err
	}
	if result.ConnectionOpened && !result.Replayed {
		if effectErr := s.deps.Effects.ConnectionOpened(ctx, result); effectErr != nil {
			s.deps.EffectFailures.Report(ctx, "exam_attempt_connection_opened", effectErr)
		}
	}
	return result, nil
}

func validateConnectConfiguration(command ConnectCommand, build model.DesktopBuildTuple) error {
	if !model.IsValidSHA256Fingerprint(command.ConfigurationManifestFingerprint) ||
		command.ConfigurationManifestFingerprint != build.AttemptConfigurationManifestFingerprint {
		return apperror.New("exam.attempt.configuration_unsupported")
	}
	if command.InitialConfiguration != nil && (command.InitialConfiguration.Validate() != nil ||
		command.InitialConfiguration.ManifestFingerprint != build.AttemptConfigurationManifestFingerprint ||
		build.ConfigurationManifest.ValidateSelection(command.InitialConfiguration.ApprovedCommands, command.InitialConfiguration.ApprovedKeybindings) != nil) {
		return apperror.Invalid("exam.attempt.invalid", "initial_configuration")
	}
	return nil
}

func cloneAttemptConfigurationPointer(value *model.AttemptConfigurationCandidate) *model.AttemptConfigurationCandidate {
	if value == nil {
		return nil
	}
	clone := value.Clone()
	return &clone
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
	ExamID          model.ExamID
	SittingID       model.ExamSittingID
	CandidateUserID model.UserID
	Connection      model.AttemptConnection
	Changed         bool
}

func (s *Service) CloseExamAttemptConnection(ctx context.Context, invocation invocation.Invocation, command CloseConnectionCommand) (_ ConnectionClosedResult, resultErr error) {
	defer func() { s.recordOperational("close", resultErr) }()
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	if invocation.Principal().Validate() != nil || !command.AttemptID.IsValid() || !command.SittingID.IsValid() ||
		!command.ClassID.IsValid() || !command.ConnectionID.IsValid() || !command.Reason.IsValid() {
		return ConnectionClosedResult{}, apperror.Invalid("exam.attempt.invalid", "connection")
	}
	auditID, err := s.deps.Auditor.BeginAtScope(ctx, invocation, model.ActionExamSittingParticipate,
		model.Resource{Type: model.ResourceExamSitting, ID: command.SittingID.String()}, model.RoleScopeClass, command.ClassID.String(),
		"exam_attempt_connection_close", map[string]any{"exam_attempt_id": command.AttemptID.String(), "attempt_connection_id": command.ConnectionID.String()}, nil)
	if err != nil {
		return ConnectionClosedResult{}, err
	}
	stored, err := s.deps.Persistence.CloseConnection(ctx, &store.ExamAttemptConnectionClose{
		ConnectionID: command.ConnectionID, CandidateUserID: invocation.Principal().UserID, SessionID: invocation.Principal().SessionID,
		Reason: command.Reason, AuditEventID: auditID,
		AuditAt: model.MillisFromTime(model.TimeUTC(s.deps.Now())),
	})
	if err != nil {
		return ConnectionClosedResult{}, s.failAudit(ctx, auditID, err)
	}
	if stored == nil || stored.Connection == nil || !stored.ExamID.IsValid() || stored.AttemptID != command.AttemptID || stored.SittingID != command.SittingID ||
		stored.Connection.ID != command.ConnectionID || stored.Connection.AttemptID != command.AttemptID || stored.Connection.Validate() != nil {
		return ConnectionClosedResult{}, apperror.New("exam.attempt.unavailable").Wrap(errors.New("inconsistent Connection close outcome"))
	}
	result := ConnectionClosedResult{AttemptID: stored.AttemptID, ExamID: stored.ExamID, SittingID: stored.SittingID,
		CandidateUserID: stored.CandidateUserID, Connection: *stored.Connection, Changed: stored.Changed}
	if result.Changed {
		if effectErr := s.deps.Effects.ConnectionClosed(ctx, result); effectErr != nil {
			s.deps.EffectFailures.Report(ctx, "exam_attempt_connection_closed", effectErr)
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
	AttemptID            model.ExamAttemptID
	SittingID            model.ExamSittingID
	ClassID              model.ClassID
	Title                string
	InstructionsMarkdown string
	Capacity             model.ExamCapacityPolicy
	RuntimeCapabilities  store.CandidateRuntimeCapabilities
	BrowserPolicy        *store.CandidateBrowserPolicy
	LiveCorrections      []model.CandidateLiveCorrection
	Resources            []Resource
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

func (s *Service) GetCandidateExamPresentation(ctx context.Context, invocation invocation.Invocation, access CandidateAccess) (_ Presentation, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	selector, err := candidateSelector(invocation, access)
	if err != nil {
		return Presentation{}, err
	}
	stored, err := s.deps.Persistence.GetCandidatePresentation(ctx, selector)
	if err != nil {
		return Presentation{}, storeError(storeErrors, err)
	}
	if stored == nil || stored.Capacity.Validate() != nil || stored.RuntimeCapabilities.Validate() != nil || !validCandidateBrowserPolicy(stored.RuntimeCapabilities, stored.BrowserPolicy) ||
		!validCandidateLiveCorrections(stored.RuntimeCapabilities, stored.LiveCorrections) ||
		stored.RuntimeCapabilities.ExamRevision.AdmissionRevisionID.IsZero() ||
		stored.RuntimeCapabilities.ExamRevision.CurrentRevisionID.IsZero() {
		return Presentation{}, apperror.New("exam.attempt.unavailable").Wrap(errors.New("missing candidate presentation"))
	}
	result := Presentation{AttemptID: stored.AttemptID, SittingID: stored.SittingID, ClassID: stored.ClassID,
		Title: stored.Title, InstructionsMarkdown: safemarkdown.Sanitize(stored.InstructionsMarkdown), Capacity: stored.Capacity,
		RuntimeCapabilities: stored.RuntimeCapabilities, BrowserPolicy: cloneCandidateBrowserPolicy(stored.BrowserPolicy),
		LiveCorrections: model.CloneCandidateLiveCorrections(stored.LiveCorrections),
		Resources:       make([]Resource, len(stored.Resources))}
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

func (s *Service) ListCandidateExamWorkspace(ctx context.Context, invocation invocation.Invocation, query WorkspaceQuery) (_ WorkspacePage, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	selector, err := candidateSelector(invocation, query.Access)
	if err != nil {
		return WorkspacePage{}, err
	}
	if query.Limit < 1 || query.Limit > model.AttemptWorkspaceJournalReadMaximum || query.ExpectedCursor < -1 ||
		(query.ExpectedCursor == -1 && !query.AfterEntryID.IsZero()) {
		return WorkspacePage{}, apperror.Invalid("exam.attempt.invalid", "workspace_list")
	}
	page, err := s.deps.Workspace.List(ctx, store.CandidateWorkspaceListOptions{
		Access: selector, ExpectedCursor: query.ExpectedCursor, AfterEntryID: query.AfterEntryID, Limit: query.Limit,
	})
	if err != nil {
		return WorkspacePage{}, storeError(storeErrors, err)
	}
	if page == nil || !page.WorkspaceID.IsValid() || page.Cursor < 0 ||
		(page.RefreshRequired && (len(page.Items) != 0 || page.HasMore)) {
		return WorkspacePage{}, apperror.New("exam.attempt.unavailable").Wrap(errors.New("missing candidate Workspace page"))
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

// OpenResourceQuery names one published Resource through the candidate's
// Attempt access.
type OpenResourceQuery struct {
	Access     CandidateAccess
	ResourceID model.ExamResourceID
}

func (s *Service) OpenCandidateExamResource(ctx context.Context, invocation invocation.Invocation, query OpenResourceQuery) (_ OpenedContent, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	selector, err := candidateSelector(invocation, query.Access)
	if err != nil {
		return OpenedContent{}, err
	}
	if !query.ResourceID.IsValid() {
		return OpenedContent{}, apperror.Invalid("exam.attempt.invalid", "exam_resource_id")
	}
	resolved, err := s.deps.Persistence.ResolveCandidateResource(ctx, selector, query.ResourceID)
	if err != nil {
		return OpenedContent{}, storeError(storeErrors, err)
	}
	if resolved == nil || resolved.Resource.ResourceID != query.ResourceID || !resolved.FileRevisionID.IsValid() || !resolved.RenditionID.IsValid() {
		return OpenedContent{}, apperror.New("exam.attempt.unavailable").Wrap(errors.New("incomplete candidate Resource selector"))
	}
	body, err := s.deps.Content.OpenExamResource(ctx, resolved.FileRevisionID, resolved.RenditionID)
	if err != nil {
		return OpenedContent{}, apperror.New("exam.attempt.unavailable").Wrap(err)
	}
	return OpenedContent{Body: body, MediaType: string(resolved.Resource.MediaType), SizeBytes: resolved.Resource.SizeBytes, SHA256: resolved.Resource.SHA256}, nil
}

// OpenWorkspaceFileQuery names one Workspace file through the candidate's
// Attempt access.
type OpenWorkspaceFileQuery struct {
	Access  CandidateAccess
	EntryID model.AttemptWorkspaceEntryID
}

func (s *Service) OpenCandidateExamWorkspaceFile(ctx context.Context, invocation invocation.Invocation, query OpenWorkspaceFileQuery) (_ OpenedContent, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	selector, err := candidateSelector(invocation, query.Access)
	if err != nil {
		return OpenedContent{}, err
	}
	if !query.EntryID.IsValid() {
		return OpenedContent{}, apperror.Invalid("exam.attempt.invalid", "attempt_workspace_entry_id")
	}
	resolved, err := s.deps.Workspace.ResolveFile(ctx, selector, query.EntryID)
	if err != nil {
		return OpenedContent{}, storeError(storeErrors, err)
	}
	if resolved == nil || resolved.Entry.EntryID != query.EntryID || resolved.ContentVersion.IsZero() {
		return OpenedContent{}, apperror.New("exam.attempt.unavailable").Wrap(errors.New("incomplete candidate Workspace selector"))
	}
	var body io.ReadCloser
	switch resolved.StorageOrigin {
	case model.AttemptWorkspaceStorageStarter:
		if !resolved.StarterObjectID.IsValid() || !resolved.AttemptObjectID.IsValid() {
			return OpenedContent{}, apperror.New("exam.attempt.unavailable").Wrap(errors.New("inconsistent starter Workspace selector"))
		}
		body, err = s.deps.Content.OpenStarterWorkspaceObject(ctx, resolved.StarterObjectID)
	case model.AttemptWorkspaceStorageAttempt:
		if !resolved.AttemptObjectID.IsValid() || resolved.StarterObjectID.IsValid() {
			return OpenedContent{}, apperror.New("exam.attempt.unavailable").Wrap(errors.New("inconsistent attempt-origin Workspace selector"))
		}
		body, err = s.deps.Content.OpenAttemptWorkspaceObject(ctx, resolved.AttemptObjectID)
	default:
		return OpenedContent{}, apperror.New("exam.attempt.unavailable").Wrap(errors.New("invalid candidate Workspace storage origin"))
	}
	if err != nil {
		return OpenedContent{}, apperror.New("exam.attempt.unavailable").Wrap(err)
	}
	return OpenedContent{Body: body, MediaType: resolved.Entry.MediaType, SizeBytes: resolved.Entry.SizeBytes,
		SHA256: resolved.Entry.SHA256, ContentVersion: resolved.ContentVersion}, nil
}

func candidateSelector(invocation invocation.Invocation, access CandidateAccess) (store.CandidateAttemptAccess, error) {
	principal := invocation.Principal()
	if principal.Validate() != nil || principal.CredentialType != model.CredentialSessionAccess {
		return store.CandidateAttemptAccess{}, apperror.New("authentication.token_invalid")
	}
	if !principal.HasRegisteredDesktopKey() {
		return store.CandidateAttemptAccess{}, apperror.New("exam.attempt.registered_desktop_required")
	}
	if !access.AttemptID.IsValid() || !access.ConnectionID.IsValid() || !model.IsValidCredentialToken(access.ContinuityCredential) {
		return store.CandidateAttemptAccess{}, apperror.Invalid("exam.attempt.invalid", "candidate_access")
	}
	return store.CandidateAttemptAccess{AttemptID: access.AttemptID, CandidateUserID: principal.UserID,
		SessionID: principal.SessionID, DesktopRegistrationID: principal.DesktopRegistrationID,
		DPoPKeyThumbprint: principal.DPoPKeyThumbprint, ConnectionID: access.ConnectionID,
		ContinuityCredentialHash: model.HashToken(access.ContinuityCredential)}, nil
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
		return ConnectionResult{}, apperror.New("exam.attempt.unavailable").Wrap(errors.New("inconsistent Exam Attempt connection outcome"))
	}
	if stored.Attempt.State != model.ExamAttemptActive || stored.Participation.State != model.AttemptParticipationActive ||
		stored.Connection.State != model.AttemptConnectionOpen {
		return ConnectionResult{}, apperror.New("exam.attempt.connection_closed")
	}
	if stored.Security.Validate() != nil || stored.Security.ParticipationID != stored.Participation.ID || stored.Security.Policy.Scope.AttemptID != stored.Attempt.ID || stored.Security.Generation != stored.Participation.Generation || stored.Configuration.Validate() != nil || stored.RuntimeCapabilities.Validate() != nil || !validCandidateBrowserPolicy(stored.RuntimeCapabilities, stored.BrowserPolicy) ||
		!validCandidateLiveCorrections(stored.RuntimeCapabilities, stored.LiveCorrections) {
		return ConnectionResult{}, apperror.New("exam.attempt.unavailable").Wrap(errors.New("inconsistent Exam Attempt configuration outcome"))
	}
	var revokedSessionIDs []string
	for _, id := range stored.RevokedSessionIDs {
		revokedSessionIDs = append(revokedSessionIDs, id.String())
	}
	return ConnectionResult{Security: stored.Security, Attempt: *stored.Attempt, Workspace: *stored.Workspace, Participation: *stored.Participation,
		Connection: *stored.Connection, ClassID: classID, FirstAdmission: stored.FirstAdmission,
		Configuration: stored.Configuration.Clone(), RuntimeCapabilities: stored.RuntimeCapabilities,
		BrowserPolicy: cloneCandidateBrowserPolicy(stored.BrowserPolicy), LiveCorrections: model.CloneCandidateLiveCorrections(stored.LiveCorrections),
		ConnectionOpened: stored.ConnectionOpened, RevokedSessionIDs: revokedSessionIDs,
		RevokedAccessTokenDigests: append([]string(nil), stored.RevokedAccessTokenDigests...), Replayed: stored.Replayed}, nil
}

func validCandidateBrowserPolicy(capabilities store.CandidateRuntimeCapabilities, policy *store.CandidateBrowserPolicy) bool {
	if slices.Contains(capabilities.PendingCorrectionCapabilities, model.CandidateCapabilityBrowser) {
		return policy == nil
	}
	if policy == nil || !policy.PolicyRevisionID.IsValid() || policy.PolicyRevisionID != capabilities.ExamRevision.CurrentRevisionID || policy.PolicyRevisionNumber < 1 || policy.Policy.Validate() != nil || policy.BrowserActivityDisclosure.Validate() != nil || policy.BrowserActivityDisclosure.MayCreateIntegrityEvidence != policy.Policy.MayCreateIntegrityEvidence() {
		return false
	}
	if policy.Policy.Enabled != (capabilities.Browser.State != store.CandidateBrowserDisabled) {
		return false
	}
	if policy.Policy.Enabled && (policy.PolicyRevisionID != capabilities.Browser.PolicyRevisionID || policy.PolicyDigest != capabilities.Browser.PolicyDigest) {
		return false
	}
	digest, err := model.BrowserPolicyDigest(policy.Policy)
	return err == nil && policy.PolicyDigest == digest
}

func validCandidateLiveCorrections(capabilities store.CandidateRuntimeCapabilities, corrections []model.CandidateLiveCorrection) bool {
	var previous int64
	for _, correction := range corrections {
		if correction.Validate() != nil || correction.RevisionNumber <= previous ||
			correction.RevisionID == capabilities.ExamRevision.AdmissionRevisionID {
			return false
		}
		previous = correction.RevisionNumber
	}
	pending := model.PendingCorrectionCapabilities(corrections)
	return slices.Equal(pending, capabilities.PendingCorrectionCapabilities) && (len(pending) != 0) == capabilities.ExamRevision.AcknowledgementRequired
}

func cloneCandidateBrowserPolicy(value *store.CandidateBrowserPolicy) *store.CandidateBrowserPolicy {
	if value == nil {
		return nil
	}
	clone := *value
	clone.Policy = value.Policy.Clone()
	return &clone
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

func (s *Service) failAudit(ctx context.Context, auditID model.AuditEventID, err error) error {
	mapped := err
	code := "exam.attempt.unavailable"
	var fault *apperror.Error
	if !errors.As(mapped, &fault) {
		mapped = storeError(storeErrors, err)
	}
	if errors.As(mapped, &fault) {
		code = fault.Code()
	}
	if auditErr := s.deps.Auditor.Fail(ctx, auditID, code); auditErr != nil {
		return errors.Join(mapped, auditErr)
	}
	return mapped
}

// storeError presents an Attempt Store failure. Typed refusals that carry
// safe detail are presented before the declarative table.
func storeError(table apperror.StoreTable, err error) error {
	var nativeRefusal *store.NativeDeliveryRefusal
	var browserRefusal *store.BrowserDeliveryRefusal
	var capacity *model.DeliveryMetadataCapacity
	switch {
	case errors.Is(err, store.ErrInvalidState):
		return apperror.New("exam.attempt.unavailable").Wrap(err)
	case errors.As(err, &nativeRefusal):
		return apperror.New(deliveryRefusalCode(nativeRefusal.Reason)).Wrap(err)
	case errors.As(err, &browserRefusal):
		return apperror.New(deliveryRefusalCode(browserRefusal.Reason)).Wrap(err)
	case errors.As(err, &capacity):
		return apperror.New("exam.delivery.metadata_capacity").WithFields(map[string]string{
			"control_metadata_bytes":       strconv.FormatInt(capacity.ControlMetadataBytes, 10),
			"control_metadata_limit_bytes": strconv.FormatInt(capacity.ControlMetadataLimitBytes, 10),
			"required_reservation_bytes":   strconv.FormatInt(capacity.RequiredReservationBytes, 10),
			"remaining_reservable_bytes":   strconv.FormatInt(capacity.RemainingReservableBytes, 10),
		}).Wrap(err)
	default:
		return table.Translate(err)
	}
}

// deliveryRefusalCode presents the bounded reason of a committed delivery
// refusal.
func deliveryRefusalCode(reason string) apperror.Code {
	switch reason {
	case "sequence_limit":
		return "exam.delivery.sequence_limit"
	case "detail_budget_exhausted":
		return "exam.delivery.detail_budget_exhausted"
	default:
		return "exam.attempt.unavailable"
	}
}

// deliveryConflictCodes presents delivery conflicts, whose constraint names
// are meaningful only on the native_delivery and browser_delivery entities.
var deliveryConflictCodes = map[string]apperror.Code{
	"append_rate_limited":     "exam.delivery.append_rate_limited",
	"batch_conflict":          "exam.delivery.batch_conflict",
	"declaration_conflict":    "exam.delivery.declaration_conflict",
	"detail_budget_exhausted": "exam.delivery.detail_budget_exhausted",
	"pending_capacity":        "exam.delivery.pending_capacity",
	"replay_window_exceeded":  "exam.delivery.replay_window_exceeded",
	"summary_rate_limited":    "exam.delivery.summary_rate_limited",
}

// publicFailures presents every Attempt use-case failure: unexpected errors
// become exam.attempt.unavailable, and protected targets conceal denial and absence.
var publicFailures = apperror.Concealment{
	Unavailable: "exam.attempt.unavailable",
	Concealed:   []apperror.Code{"exam.attempt.not_found", "exam.attempt.continuity_invalid"},
}

var storeErrors = apperror.StoreTable{
	Rules: slices.Concat(
		apperror.Conflicts("browser_delivery", deliveryConflictCodes),
		apperror.Conflicts("security_preflight", map[string]apperror.Code{
			"attempt_not_ready":         "exam.security.attempt_not_ready",
			"configuration_unsupported": "exam.security.configuration_unsupported",
			"policy_changed":            "exam.security.policy_changed",
			"posture_blocked":           "exam.security.posture_blocked",
			"preflight_consumed":        "exam.security.preflight_consumed",
			"preflight_expired":         "exam.security.preflight_expired",
			"preflight_rate_limited":    "exam.security.preflight_rate_limited",
			"preflight_report_changed":  "exam.security.preflight_report_changed",
			"preflight_required":        "exam.security.preflight_required",
			"preflight_superseded":      "exam.security.preflight_superseded",
			"resume_required":           "exam.security.resume_required",
			"session_changed":           "exam.security.session_changed",
		}),
	),
	Constraints: map[string]apperror.Code{
		"control_conflict":                         "exam.security.control_conflict",
		"exam_attempt_membership":                  "exam.attempt.not_found",
		"exam_sitting_state":                       "exam.attempt.sitting_not_open",
		"exam_sitting_deadline_reached":            "exam.attempt.sitting_not_open",
		"exam_attempt_state":                       "exam.attempt.state_conflict",
		"exam_attempt_management_authority":        "exam.attempt.management_authority_conflict",
		"desktop_compatibility_policy_revision":    "exam.attempt.desktop_incompatible",
		"exam_attempt_revision":                    "exam.attempt.revision_conflict",
		"exam_sitting_revision_selection":          "exam.attempt.revision_conflict",
		"exam_correction_acknowledgement_target":   "exam.attempt.correction_conflict",
		"exam_correction_acknowledgement_order":    "exam.attempt.correction_conflict",
		"exam_correction_acknowledgement_required": "exam.attempt.correction_conflict",
		"browser_source_fence":                     "exam.attempt.browser_activity_conflict",
		"browser_activity_sequence":                "exam.attempt.browser_activity_conflict",
		"browser_activity_policy_semantics":        "exam.attempt.browser_activity_conflict",
		"browser_activity_not_applicable":          "exam.attempt.browser_activity_conflict",
		"browser_activity_source":                  "exam.attempt.browser_activity_conflict",
		"browser_activity_source_fence":            "exam.attempt.browser_activity_conflict",
		"browser_activity_incomplete":              "exam.attempt.browser_activity_conflict",
		"browser_activity_not_closed":              "exam.attempt.browser_activity_conflict",
		"browser_activity_final_sequence":          "exam.attempt.browser_activity_conflict",
		"browser_activity_accounting":              "exam.attempt.browser_activity_conflict",
		"exam_submission_causal_selector":          "idempotency.conflict",
		"attempt_configuration_required":           "exam.attempt.configuration_required",
		"attempt_configuration_stale":              "exam.attempt.configuration_stale",
		"attempt_configuration_frozen":             "exam.attempt.configuration_frozen",
		"attempt_configuration_unsupported":        "exam.attempt.configuration_unsupported",
		"attempt_suspension_active":                "exam.attempt.suspension_conflict",
		"attempt_participation_credential":         "exam.attempt.continuity_invalid",
		"attempt_participation_generation":         "exam.attempt.connection_closed",
		"attempt_participation_sequence":           "exam.attempt.renewal_conflict",
		"focus_loss_sequence":                      "exam.attempt.focus_loss_conflict",
		"attempt_participation_expired":            "exam.attempt.connection_lost",
		"attempt_connection_open":                  "exam.attempt.already_connected",
		"attempt_connection_closed":                "exam.attempt.connection_closed",
		"attempt_workspace_path":                   "exam.attempt.workspace.path_conflict",
		"attempt_workspace_entry":                  "exam.attempt.workspace.entry_conflict",
		"attempt_workspace_content_version":        "exam.attempt.workspace.content_conflict",
		"attempt_workspace_not_empty":              "exam.attempt.workspace.directory_not_empty",
		"attempt_workspace_entry_limit":            "exam.attempt.workspace.entry_limit",
		"attempt_workspace_size_limit":             "exam.attempt.workspace.size_limit",
		"attempt_workspace_object_state":           "exam.attempt.workspace.object_conflict",
		"attempt_workspace_cursor":                 "exam.attempt.workspace.cursor_conflict",
	},
	NotFound:     "exam.attempt.not_found",
	Conflict:     "exam.attempt.conflict",
	Invalid:      "exam.attempt.invalid",
	InvalidField: "value",
	Unavailable:  "exam.attempt.unavailable",
}
