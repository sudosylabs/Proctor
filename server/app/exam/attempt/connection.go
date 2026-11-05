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

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

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
	RevokedSessionIDs         []model.SessionID
	RevokedAccessTokenDigests []string
	Replayed                  bool
}

// ConnectExamAttempt idempotently admits the caller's registered, compatible
// desktop to a Sitting: it creates the Attempt and its Attempt Workspace on
// first admission, or resumes the existing Attempt, and opens a new Attempt
// Participation and Attempt Connection.
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
		AuditEventID:                       auditID, AuditAt: at,
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

// CloseExamAttemptConnection closes the caller's Attempt Connection with the
// given reason; post-commit effects are published only when the close changed
// state.
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
		AuditAt: model.TimeUTC(s.deps.Now()),
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
	revokedSessionIDs := append([]model.SessionID(nil), stored.RevokedSessionIDs...)
	return ConnectionResult{Security: stored.Security, Attempt: *stored.Attempt, Workspace: *stored.Workspace, Participation: *stored.Participation,
		Connection: *stored.Connection, ClassID: classID, FirstAdmission: stored.FirstAdmission,
		Configuration: stored.Configuration.Clone(), RuntimeCapabilities: stored.RuntimeCapabilities,
		BrowserPolicy: cloneCandidateBrowserPolicy(stored.BrowserPolicy), LiveCorrections: model.CloneCandidateLiveCorrections(stored.LiveCorrections),
		ConnectionOpened: stored.ConnectionOpened, RevokedSessionIDs: revokedSessionIDs,
		RevokedAccessTokenDigests: append([]string(nil), stored.RevokedAccessTokenDigests...), Replayed: stored.Replayed}, nil
}
