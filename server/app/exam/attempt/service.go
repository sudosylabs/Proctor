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
	"time"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type Sittings interface {
	// Resolve returns stable Sitting ownership metadata. Store remains the
	// authority for connect-time lifecycle, deadline, and membership checks.
	Resolve(context.Context, model.ExamSittingID) (*store.ExamSittingSnapshot, error)
}

// ManagerAuthorization reports a granted current Exam Manager decision:
// Override is true when the decision rested on the explicit override Action
// rather than the ordinary Manager relationship.
type ManagerAuthorization struct {
	Override bool
}

// BrowserActivityAuthorization reports the Academic Unit of the Exam whose
// browser activity the caller may read.
type BrowserActivityAuthorization struct {
	AcademicUnitID model.AcademicUnitID
}

type ManagerAuthorizer interface {
	AuthorizeSittingView(context.Context, invocation.Invocation, model.ExamSittingID) error
	AuthorizeSittingManage(context.Context, invocation.Invocation, model.ExamSittingID) (ManagerAuthorization, error)
	AuthorizeSubmissionView(context.Context, invocation.Invocation, model.SubmissionID) error
	AuthorizeBrowserActivityView(context.Context, invocation.Invocation, model.ExamSittingID) (BrowserActivityAuthorization, error)
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

func validActiveSuspension(view *store.ExamAttemptSuspensionView, attemptID model.ExamAttemptID) bool {
	if view == nil {
		return true
	}
	return view.ID.IsValid() && view.AttemptID == attemptID && view.ParticipationID.IsValid() && validSuspensionProvenance(view) &&
		view.Generation > 0 && view.State == model.AttemptSuspensionActive && !view.StartedAt.IsZero() &&
		!view.EndedAt.Valid && view.ReallowedByUserID.IsZero()
}

type ExpiryScanResult struct {
	Due       int
	Completed int
	Replayed  int
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

type CandidateAccess struct {
	AttemptID            model.ExamAttemptID
	ConnectionID         model.AttemptConnectionID
	ContinuityCredential string
}

type OpenedContent struct {
	Body           io.ReadCloser
	MediaType      string
	SizeBytes      int64
	SHA256         string
	ContentVersion model.WorkspaceContentVersion
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
		"browser_source_conflict":                  "exam.attempt.browser_activity_conflict",
		"browser_source_current":                   "exam.attempt.browser_activity_conflict",
		"browser_source_predecessor":               "exam.attempt.browser_activity_conflict",
		"browser_source_policy":                    "exam.attempt.browser_activity_conflict",
		"browser_policy_disabled":                  "exam.attempt.browser_activity_conflict",
		"browser_source_limit":                     "exam.browser.source_budget_exhausted",
		"browser_activity_sequence":                "exam.attempt.browser_activity_conflict",
		"browser_activity_policy_semantics":        "exam.attempt.browser_activity_conflict",
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
