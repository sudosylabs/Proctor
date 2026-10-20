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
	examattempt "github.com/sudosylabs/proctor/server/app/exam/attempt"
	apprealtime "github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type ExamAttemptConnection = examattempt.ConnectionResult
type ExamAttemptConnectionClosed = examattempt.ConnectionClosedResult
type ExamAttemptParticipationRenewal = examattempt.ParticipationRenewal
type ExamAttemptFocusLossEvaluation = examattempt.FocusLossEvaluation
type ExamAttemptReallowResult = examattempt.ReallowResult
type ExamAttemptManualSuspensionResult = examattempt.ManualSuspensionResult
type SuspendExamAttemptCommand = examattempt.ManualSuspensionCommand
type CandidateExamAttemptAccess = examattempt.CandidateAccess
type CandidateExamPresentation = examattempt.Presentation
type CandidateExamWorkspaceItem = store.CandidateAttemptWorkspaceItem
type ExamAttemptManagerView = store.ExamAttemptManagerSnapshot
type OpenedExamAttemptContent = examattempt.OpenedContent
type ExamAttemptWorkspaceMutationAccess = examattempt.WorkspaceMutationAccess
type ExamAttemptWorkspaceMutationResult = examattempt.WorkspaceMutationResult
type CandidateExamWorkspaceJournalPage = examattempt.WorkspaceJournalPage
type CandidateExamActivityPage = examattempt.CandidateActivityPage
type SittingCandidateStatusesPage = examattempt.SittingCandidateStatusesPage
type SittingCandidatePresenceState = store.SittingCandidatePresenceState
type CandidateRuntimeCapabilities = store.CandidateRuntimeCapabilities
type CandidateBrowserPolicy = store.CandidateBrowserPolicy
type BrowserActivityAcknowledgement = model.BrowserActivityAcknowledgement
type BrowserSourceRefusal = store.BrowserSourceRefusal
type BrowserActivityRecord = store.BrowserActivityRecord

type StartBrowserActivityCommand = examattempt.StartBrowserActivityCommand
type AppendBrowserActivityCommand = examattempt.AppendBrowserActivityCommand
type ListBrowserActivityQuery = examattempt.BrowserActivityPageQuery
type BrowserActivityPage = examattempt.BrowserActivityPage
type AcknowledgeExamCorrectionCommand = examattempt.AcknowledgeCorrectionCommand
type ExamCorrectionAcknowledgementResult = examattempt.CorrectionAcknowledgementResult
type EndExamAttemptByManagerCommand = examattempt.ManagerEndCommand

func (a *App) StartExamAttemptBrowserActivity(ctx context.Context, invocation Invocation, command StartBrowserActivityCommand) (model.BrowserSourceStatus, error) {
	result, err := a.examAttempts.StartBrowserActivity(ctx, invocation, command)
	if err != nil {
		return model.BrowserSourceStatus{}, examAttemptError(err, true)
	}
	return result, nil
}

func (a *App) AppendExamAttemptBrowserActivity(ctx context.Context, invocation Invocation, command AppendBrowserActivityCommand) (BrowserActivityAcknowledgement, error) {
	result, err := a.examAttempts.AppendBrowserActivity(ctx, invocation, command)
	if err != nil {
		return BrowserActivityAcknowledgement{}, examAttemptError(err, true)
	}
	return result, nil
}

func (a *App) ListExamAttemptBrowserActivity(ctx context.Context, invocation Invocation, query ListBrowserActivityQuery) (BrowserActivityPage, error) {
	page, err := a.examAttempts.ListBrowserActivity(ctx, invocation, query)
	if err != nil {
		return BrowserActivityPage{}, examAttemptError(err, true)
	}
	return page, nil
}

func (a *App) AcknowledgeExamAttemptCorrection(ctx context.Context, invocation Invocation, command AcknowledgeExamCorrectionCommand) (ExamCorrectionAcknowledgementResult, error) {
	result, err := a.examAttempts.AcknowledgeCorrection(ctx, invocation, command)
	if err != nil {
		return ExamCorrectionAcknowledgementResult{}, examAttemptError(err, true)
	}
	return result, nil
}

func (a *App) EndExamAttemptByManager(ctx context.Context, invocation Invocation,
	command EndExamAttemptByManagerCommand,
) (ExamSubmissionReceipt, error) {
	result, err := a.examAttempts.EndByManager(ctx, invocation, command)
	if err != nil {
		return ExamSubmissionReceipt{}, examAttemptError(err, true)
	}
	return result.Receipt, nil
}

type ConnectExamAttemptCommand = examattempt.ConnectCommand

type CloseExamAttemptConnectionCommand = examattempt.CloseConnectionCommand

type RenewExamAttemptParticipationCommand = examattempt.RenewParticipationCommand

type EvaluateExamAttemptFocusLossCommand = examattempt.FocusLossCommand

type ReallowExamAttemptCommand = examattempt.ReallowCommand

type ListCandidateExamWorkspaceQuery = examattempt.WorkspaceQuery

type CandidateExamWorkspacePage = examattempt.WorkspacePage

type ListCandidateExamWorkspaceJournalQuery = examattempt.WorkspaceJournalQuery

type CreateCandidateExamWorkspaceDirectoryCommand = examattempt.CreateWorkspaceDirectoryCommand

type CreateCandidateExamWorkspaceFileCommand = examattempt.CreateWorkspaceFileCommand

type ReplaceCandidateExamWorkspaceFileCommand = examattempt.ReplaceWorkspaceFileCommand

type MoveCandidateExamWorkspaceEntryCommand = examattempt.MoveWorkspaceEntryCommand

type DeleteCandidateExamWorkspaceEntryCommand = examattempt.DeleteWorkspaceEntryCommand

type OpenCandidateExamResourceQuery struct {
	Access     CandidateExamAttemptAccess
	ResourceID model.ExamResourceID
}

type OpenCandidateExamWorkspaceFileQuery struct {
	Access  CandidateExamAttemptAccess
	EntryID model.AttemptWorkspaceEntryID
}

type GetExamAttemptQuery = examattempt.GetManagedAttemptQuery

type ListExamAttemptsQuery = examattempt.ListManagedAttemptsQuery

type ExamAttemptManagerPage = examattempt.ManagedAttemptPage

func (a *App) ListCandidateExamWorkspaceJournal(ctx context.Context, invocation Invocation,
	query ListCandidateExamWorkspaceJournalQuery,
) (CandidateExamWorkspaceJournalPage, error) {
	result, err := a.examAttempts.ListWorkspaceJournal(ctx, invocation, query)
	if err != nil {
		return CandidateExamWorkspaceJournalPage{}, examAttemptError(err, true)
	}
	return result, nil
}

func (a *App) CreateCandidateExamWorkspaceDirectory(ctx context.Context, invocation Invocation,
	command CreateCandidateExamWorkspaceDirectoryCommand,
) (ExamAttemptWorkspaceMutationResult, error) {
	result, err := a.examAttempts.CreateWorkspaceDirectory(ctx, invocation, command)
	if err != nil {
		return ExamAttemptWorkspaceMutationResult{}, examAttemptError(err, true)
	}
	return result, nil
}

func (a *App) CreateCandidateExamWorkspaceFile(ctx context.Context, invocation Invocation,
	command CreateCandidateExamWorkspaceFileCommand,
) (ExamAttemptWorkspaceMutationResult, error) {
	result, err := a.examAttempts.CreateWorkspaceFile(ctx, invocation, command)
	if err != nil {
		return ExamAttemptWorkspaceMutationResult{}, examAttemptError(err, true)
	}
	return result, nil
}

func (a *App) ReplaceCandidateExamWorkspaceFile(ctx context.Context, invocation Invocation,
	command ReplaceCandidateExamWorkspaceFileCommand,
) (ExamAttemptWorkspaceMutationResult, error) {
	result, err := a.examAttempts.ReplaceWorkspaceFile(ctx, invocation, command)
	if err != nil {
		return ExamAttemptWorkspaceMutationResult{}, examAttemptError(err, true)
	}
	return result, nil
}

func (a *App) MoveCandidateExamWorkspaceEntry(ctx context.Context, invocation Invocation,
	command MoveCandidateExamWorkspaceEntryCommand,
) (ExamAttemptWorkspaceMutationResult, error) {
	result, err := a.examAttempts.MoveWorkspaceEntry(ctx, invocation, command)
	if err != nil {
		return ExamAttemptWorkspaceMutationResult{}, examAttemptError(err, true)
	}
	return result, nil
}

func (a *App) DeleteCandidateExamWorkspaceEntry(ctx context.Context, invocation Invocation,
	command DeleteCandidateExamWorkspaceEntryCommand,
) (ExamAttemptWorkspaceMutationResult, error) {
	result, err := a.examAttempts.DeleteWorkspaceEntry(ctx, invocation, command)
	if err != nil {
		return ExamAttemptWorkspaceMutationResult{}, examAttemptError(err, true)
	}
	return result, nil
}

func (a *App) ReallowExamAttempt(ctx context.Context, invocation Invocation, command ReallowExamAttemptCommand) (ExamAttemptReallowResult, error) {
	result, err := a.examAttempts.Reallow(ctx, invocation, command)
	if err != nil {
		return ExamAttemptReallowResult{}, examAttemptError(err, true)
	}
	return result, nil
}

// SuspendExamAttempt is an Exam Manager's kick of one active Attempt.
func (a *App) SuspendExamAttempt(ctx context.Context, invocation Invocation, command SuspendExamAttemptCommand) (ExamAttemptManualSuspensionResult, error) {
	result, err := a.examAttempts.SuspendByManager(ctx, invocation, command)
	if err != nil {
		return ExamAttemptManualSuspensionResult{}, examAttemptError(err, true)
	}
	return result, nil
}

func (a *App) RenewExamAttemptParticipation(ctx context.Context, invocation Invocation, command RenewExamAttemptParticipationCommand) (response ExamAttemptParticipationRenewal, resultErr error) {
	defer func() { a.recordOperational("exam_attempt", "renew", resultErr) }()
	result, err := a.examAttempts.RenewParticipation(ctx, invocation, command)
	if err != nil {
		return ExamAttemptParticipationRenewal{}, examAttemptError(err, true)
	}
	return result, nil
}

func (a *App) EvaluateExamAttemptFocusLoss(ctx context.Context, invocation Invocation,
	command EvaluateExamAttemptFocusLossCommand,
) (response ExamAttemptFocusLossEvaluation, resultErr error) {
	defer func() { a.recordOperational("exam_attempt", "focus_loss", resultErr) }()
	result, err := a.examAttempts.EvaluateFocusLoss(ctx,
		invocation, command)
	if err != nil {
		return ExamAttemptFocusLossEvaluation{}, examAttemptError(err, true)
	}
	return result, nil
}

func (a *App) ConnectExamAttempt(ctx context.Context, invocation Invocation, command ConnectExamAttemptCommand) (response ExamAttemptConnection, resultErr error) {
	defer func() { a.recordOperational("exam_attempt", "connect", resultErr) }()
	result, err := a.examAttempts.Connect(ctx, invocation, command)
	if err != nil {
		return ExamAttemptConnection{}, examAttemptError(err, true)
	}
	return result, nil
}

func (a *App) CloseExamAttemptConnection(ctx context.Context, invocation Invocation, command CloseExamAttemptConnectionCommand) (response ExamAttemptConnectionClosed, resultErr error) {
	defer func() { a.recordOperational("exam_attempt", "close", resultErr) }()
	result, err := a.examAttempts.CloseConnection(ctx, invocation, command)
	if err != nil {
		return ExamAttemptConnectionClosed{}, examAttemptError(err, true)
	}
	return result, nil
}

func (a *App) GetCandidateExamPresentation(ctx context.Context, invocation Invocation, access CandidateExamAttemptAccess) (CandidateExamPresentation, error) {
	result, err := a.examAttempts.GetPresentation(ctx, invocation, access)
	if err != nil {
		return CandidateExamPresentation{}, examAttemptError(err, true)
	}
	return result, nil
}

func (a *App) ListCandidateExamWorkspace(ctx context.Context, invocation Invocation, query ListCandidateExamWorkspaceQuery) (CandidateExamWorkspacePage, error) {
	result, err := a.examAttempts.ListWorkspace(ctx, invocation, query)
	if err != nil {
		return CandidateExamWorkspacePage{}, examAttemptError(err, true)
	}
	return result, nil
}

func (a *App) OpenCandidateExamResource(ctx context.Context, invocation Invocation, query OpenCandidateExamResourceQuery) (OpenedExamAttemptContent, error) {
	result, err := a.examAttempts.OpenResource(ctx, invocation, query.Access, query.ResourceID)
	if err != nil {
		return OpenedExamAttemptContent{}, examAttemptError(err, true)
	}
	if result == nil {
		return OpenedExamAttemptContent{}, apperror.New("exam.attempt.unavailable")
	}
	return *result, nil
}

func (a *App) OpenCandidateExamWorkspaceFile(ctx context.Context, invocation Invocation, query OpenCandidateExamWorkspaceFileQuery) (OpenedExamAttemptContent, error) {
	result, err := a.examAttempts.OpenWorkspaceFile(ctx, invocation, query.Access, query.EntryID)
	if err != nil {
		return OpenedExamAttemptContent{}, examAttemptError(err, true)
	}
	if result == nil {
		return OpenedExamAttemptContent{}, apperror.New("exam.attempt.unavailable")
	}
	return *result, nil
}

func (a *App) GetExamAttempt(ctx context.Context, invocation Invocation, query GetExamAttemptQuery) (ExamAttemptManagerView, error) {
	result, err := a.examAttempts.GetManaged(ctx, invocation, query)
	if err != nil {
		return ExamAttemptManagerView{}, examAttemptError(err, true)
	}
	if result == nil {
		return ExamAttemptManagerView{}, apperror.New("exam.attempt.unavailable")
	}
	return *result, nil
}

func (a *App) ListExamAttempts(ctx context.Context, invocation Invocation, query ListExamAttemptsQuery) (ExamAttemptManagerPage, error) {
	query.States = append([]model.ExamAttemptState(nil), query.States...)
	result, err := a.examAttempts.ListManaged(ctx, invocation, query)
	if err != nil {
		return ExamAttemptManagerPage{}, examAttemptError(err, true)
	}
	return result, nil
}

type ListCandidateExamActivityQuery = examattempt.CandidateActivityQuery

func (a *App) ListCandidateExamActivity(ctx context.Context, invocation Invocation,
	query ListCandidateExamActivityQuery,
) (CandidateExamActivityPage, error) {
	result, err := a.examAttempts.ListCandidateActivity(ctx,
		invocation, query)
	if err != nil {
		return CandidateExamActivityPage{}, examAttemptError(err, true)
	}
	return result, nil
}

type ListSittingCandidateStatusesQuery = examattempt.SittingCandidateStatusesQuery

func (a *App) ListSittingCandidateStatuses(ctx context.Context, invocation Invocation,
	query ListSittingCandidateStatusesQuery,
) (SittingCandidateStatusesPage, error) {
	result, err := a.examAttempts.ListSittingCandidateStatuses(ctx,
		invocation, query)
	if err != nil {
		return SittingCandidateStatusesPage{}, examAttemptError(err, true)
	}
	return result, nil
}

func examAttemptError(err error, conceal bool) error {
	return presentExamChildError(err, conceal, "exam.attempt.unavailable", "exam.attempt.not_found", "exam.attempt.continuity_invalid")
}

type examAttemptManagerAuthorizationAdapter struct {
	sittings    examSittingUseCases
	submissions store.ExamSubmissionStore
	audit       examAttemptAuthorizationDecisionAudit
}

type examAttemptAuthorizationDecisionAudit interface {
	RecordAuthorizationDecision(context.Context, model.Principal, model.Action, model.Resource,
		model.RoleScopeType, string, model.RequestMetadata, bool) error
}

func (adapter examAttemptManagerAuthorizationAdapter) AuthorizeSittingView(ctx context.Context, invocation Invocation, sittingID model.ExamSittingID) error {
	return adapter.sittings.AuthorizeView(ctx, invocation, sittingID)
}

func (adapter examAttemptManagerAuthorizationAdapter) AuthorizeSittingManage(ctx context.Context, invocation Invocation, sittingID model.ExamSittingID) (bool, error) {
	override, err := adapter.sittings.AuthorizeManage(ctx, invocation, sittingID)
	if err == nil {
		return override, nil
	}
	return false, err
}

func (adapter examAttemptManagerAuthorizationAdapter) AuthorizeBrowserActivityView(ctx context.Context, invocation Invocation, sittingID model.ExamSittingID) (model.AcademicUnitID, error) {
	unitID, err := adapter.sittings.AuthorizeBrowserActivityView(ctx, invocation, sittingID)
	if err == nil {
		return unitID, nil
	}
	return "", err
}

func (adapter examAttemptManagerAuthorizationAdapter) AuthorizeSubmissionView(ctx context.Context, invocation Invocation,
	submissionID model.SubmissionID,
) error {
	if adapter.submissions == nil || adapter.audit == nil || !submissionID.IsValid() {
		return apperror.New("exam.attempt.unavailable").Wrap(errors.New("Submission authorization dependencies are invalid"))
	}
	authorization, err := adapter.submissions.Resolve(ctx, submissionID)
	if err != nil {
		if store.IsNotFound(err) {
			return apperror.New("exam.attempt.not_found").Wrap(err)
		}
		return apperror.New("exam.attempt.unavailable").Wrap(err)
	}
	if authorization == nil || authorization.SubmissionID != submissionID || !authorization.ExamID.IsValid() ||
		!authorization.SittingID.IsValid() || !authorization.AttemptID.IsValid() || !authorization.CandidateUserID.IsValid() ||
		!authorization.AcademicUnitID.IsValid() {
		return apperror.New("exam.attempt.unavailable").Wrap(errors.New("Submission authorization projection is incomplete"))
	}
	if authorization.CandidateUserID == invocation.Principal().UserID {
		if err = adapter.audit.RecordAuthorizationDecision(ctx, invocation.Principal(), model.ActionSubmissionView,
			model.Resource{Type: model.ResourceSubmission, ID: authorization.SubmissionID.String()},
			model.RoleScopeAcademicUnit, authorization.AcademicUnitID.String(), invocation.RequestMetadata(), false); err != nil {
			return err
		}
		return apperror.New("exam.attempt.not_found")
	}
	return adapter.sittings.AuthorizeSubmissionView(ctx, invocation,
		authorization.ExamID, submissionID)
}

type examAttemptAuditAdapter struct{ audit mutationAuditAdapter }

func (adapter examAttemptAuditAdapter) Begin(ctx context.Context, invocation Invocation, action model.Action, resource model.Resource, scopeType model.RoleScopeType, scopeID, operation string, value map[string]any) (model.AuditEventID, error) {
	return adapter.audit.BeginAtScope(ctx, invocation, action, resource,
		scopeType, scopeID, operation, value, nil)
}

func (adapter examAttemptAuditAdapter) Prepare(_ context.Context, invocation Invocation, action model.Action,
	resource model.Resource, scopeType model.RoleScopeType, scopeID, operation string, value map[string]any,
) (*model.AuditEvent, error) {
	return adapter.audit.audit.prepareCriticalActionAtScope(invocation.Principal(), action, resource, scopeType, scopeID,
		invocation.RequestMetadata(), map[string]any{"operation": operation, "value": value}, nil)
}

func (adapter examAttemptAuditAdapter) RecordAuthorizationDecision(ctx context.Context, invocation Invocation,
	action model.Action, resource model.Resource, scopeType model.RoleScopeType, scopeID string, allowed bool,
) error {
	return adapter.audit.audit.RecordAuthorizationDecision(ctx, invocation.Principal(), action, resource, scopeType, scopeID,
		invocation.RequestMetadata(), allowed)
}

func (adapter examAttemptAuditAdapter) Complete(ctx context.Context, auditID model.AuditEventID, value map[string]any) error {
	_, err := adapter.audit.audit.CompleteCriticalAction(ctx, auditID, model.AuditStatusSuccess, "", value)
	return err
}

func (adapter examAttemptAuditAdapter) Fail(ctx context.Context, id model.AuditEventID, code string) error {
	return adapter.audit.Fail(ctx, id, code)
}

type examAttemptRealtimeEffects struct {
	realtime *realtimeService
}

func (effects examAttemptRealtimeEffects) publishCandidateActivityInvalidation(ctx context.Context,
	candidateID model.UserID,
) error {
	event, err := apprealtime.NewCandidateExamActivityChangedEvent(candidateID)
	if err != nil {
		return err
	}
	return effects.realtime.Publish(ctx, event)
}

func (effects examAttemptRealtimeEffects) publishSittingBoardInvalidation(ctx context.Context,
	examID model.ExamID, sittingID model.ExamSittingID,
) error {
	event, err := apprealtime.NewManagerSittingBoardChangedEvent(examID, sittingID)
	if err != nil {
		return err
	}
	return effects.realtime.Publish(ctx, event)
}

func (effects examAttemptRealtimeEffects) publishOperationalInvalidations(ctx context.Context,
	examID model.ExamID, sittingID model.ExamSittingID, candidateID model.UserID,
) error {
	return errors.Join(
		effects.publishCandidateActivityInvalidation(ctx, candidateID),
		effects.publishSittingBoardInvalidation(ctx, examID, sittingID),
		effects.realtime.InvalidateCurrentUserContext(ctx, candidateID),
	)
}

func (effects examAttemptRealtimeEffects) ConnectionOpened(ctx context.Context, result examattempt.ConnectionResult) error {
	if len(result.RevokedSessionIDs) > 0 || len(result.RevokedAccessTokenDigests) > 0 {
		effects.realtime.SessionsRevoked(ctx, result.Attempt.CandidateUserID.String(), result.RevokedSessionIDs, result.RevokedAccessTokenDigests)
	}
	event, err := apprealtime.NewExamAttemptConnectionOpenedEvent(result.Attempt.SittingID,
		result.Attempt.ID, result.Attempt.CandidateUserID, result.Connection.ID, result.Connection.OpenedAt)
	if err != nil {
		return err
	}
	return errors.Join(effects.realtime.Publish(ctx, event), effects.publishOperationalInvalidations(ctx,
		result.Attempt.ExamID, result.Attempt.SittingID, result.Attempt.CandidateUserID))
}

func (effects examAttemptRealtimeEffects) ConnectionClosed(ctx context.Context, result examattempt.ConnectionClosedResult) error {
	event, err := apprealtime.NewExamAttemptConnectionClosedEvent(result.SittingID, result.AttemptID,
		result.CandidateUserID, result.Connection.ID, result.Connection.CloseReason, result.Connection.ClosedAt.Time)
	if err != nil {
		return err
	}
	return errors.Join(effects.realtime.Publish(ctx, event),
		effects.publishSittingBoardInvalidation(ctx, result.ExamID, result.SittingID))
}

func (effects examAttemptRealtimeEffects) ParticipationRenewed(ctx context.Context, result examattempt.ParticipationRenewal) error {
	return effects.publishSittingBoardInvalidation(ctx, result.ExamID, result.SittingID)
}

func (effects examAttemptRealtimeEffects) BrowserActivityGapChanged(ctx context.Context, examID model.ExamID,
	sittingID model.ExamSittingID,
) error {
	return effects.publishSittingBoardInvalidation(ctx, examID, sittingID)
}

func (effects examAttemptRealtimeEffects) ParticipationExpired(ctx context.Context, result examattempt.ParticipationExpiry) error {
	managerEvent, err := apprealtime.NewExamAttemptSuspendedEvent(result.SittingID, result.Attempt.ID, result.CandidateUserID,
		result.Connection.ID, result.Flag.ID, result.Suspension.ID, result.Attempt.Revision, result.DatabaseTime)
	if err != nil {
		return err
	}
	candidateEvent, err := apprealtime.NewCandidateExamAttemptSuspendedEvent(result.SittingID, result.Attempt.ID,
		result.CandidateUserID, result.Suspension.CandidateReason, result.DatabaseTime)
	if err != nil {
		return err
	}
	if !result.ConnectionClosed {
		return errors.Join(effects.realtime.Publish(ctx, managerEvent), effects.realtime.Publish(ctx, candidateEvent),
			effects.publishOperationalInvalidations(ctx, result.ExamID, result.SittingID, result.CandidateUserID))
	}
	connectionEvent, err := apprealtime.NewExamAttemptConnectionClosedEvent(result.SittingID, result.Attempt.ID,
		result.CandidateUserID, result.Connection.ID, result.Connection.CloseReason, result.Connection.ClosedAt.Time)
	if err != nil {
		return err
	}
	return errors.Join(effects.realtime.Publish(ctx, connectionEvent), effects.realtime.Publish(ctx, managerEvent),
		effects.realtime.Publish(ctx, candidateEvent), effects.publishOperationalInvalidations(ctx, result.ExamID,
			result.SittingID, result.CandidateUserID), effects.realtime.UnbindExamAttemptConnection(ctx, result.Connection.ID))
}

func (effects examAttemptRealtimeEffects) AttemptReallowed(ctx context.Context, result examattempt.ReallowResult) error {
	managerEvent, err := apprealtime.NewExamAttemptReallowedEvent(result.SittingID, result.Attempt.ID, result.CandidateUserID,
		result.Suspension.ID, result.Attempt.Revision, result.Attempt.UpdatedAt)
	if err != nil {
		return err
	}
	candidateEvent, err := apprealtime.NewCandidateExamAttemptReallowedEvent(result.SittingID, result.Attempt.ID,
		result.CandidateUserID, result.Attempt.UpdatedAt)
	if err != nil {
		return err
	}
	return errors.Join(effects.realtime.Publish(ctx, managerEvent), effects.realtime.Publish(ctx, candidateEvent),
		effects.publishOperationalInvalidations(ctx, result.ExamID, result.SittingID, result.CandidateUserID))
}

// AttemptSuspendedByManager publishes a committed kick: the closed live
// Connection, if any, the manager and candidate suspension facts, operational
// invalidations, and removal of the exact live Connection binding.
func (effects examAttemptRealtimeEffects) AttemptSuspendedByManager(ctx context.Context, result examattempt.ManualSuspensionResult) error {
	var connectionID model.AttemptConnectionID
	if result.Connection != nil {
		connectionID = result.Connection.ID
	}
	managerEvent, err := apprealtime.NewExamAttemptSuspendedEvent(result.SittingID, result.Attempt.ID, result.CandidateUserID,
		connectionID, "", result.Suspension.ID, result.Attempt.Revision, result.Suspension.StartedAt)
	if err != nil {
		return err
	}
	candidateEvent, err := apprealtime.NewCandidateExamAttemptSuspendedEvent(result.SittingID, result.Attempt.ID,
		result.CandidateUserID, result.Suspension.CandidateReason, result.Suspension.StartedAt)
	if err != nil {
		return err
	}
	if !result.ConnectionClosed || result.Connection == nil {
		return errors.Join(effects.realtime.Publish(ctx, managerEvent), effects.realtime.Publish(ctx, candidateEvent),
			effects.publishOperationalInvalidations(ctx, result.ExamID, result.SittingID, result.CandidateUserID))
	}
	connectionEvent, err := apprealtime.NewExamAttemptConnectionClosedEvent(result.SittingID, result.Attempt.ID,
		result.CandidateUserID, result.Connection.ID, result.Connection.CloseReason, result.Connection.ClosedAt.Time)
	if err != nil {
		return err
	}
	return errors.Join(effects.realtime.Publish(ctx, connectionEvent), effects.realtime.Publish(ctx, managerEvent),
		effects.realtime.Publish(ctx, candidateEvent), effects.publishOperationalInvalidations(ctx, result.ExamID,
			result.SittingID, result.CandidateUserID), effects.realtime.UnbindExamAttemptConnection(ctx, result.Connection.ID))
}

func (effects examAttemptRealtimeEffects) WorkspaceChanged(ctx context.Context, result examattempt.WorkspaceMutationResult) error {
	event, err := apprealtime.NewCandidateExamAttemptWorkspaceChangedEvent(result.SittingID, result.AttemptID,
		result.CandidateUserID, result.Change.EntryID, result.Change.Operation, result.Change.Cursor, result.Change.ChangedAt)
	if err != nil {
		return err
	}
	return errors.Join(effects.realtime.Publish(ctx, event))
}

func (effects examAttemptRealtimeEffects) FocusLossEvaluated(ctx context.Context, result examattempt.FocusLossEvaluation) error {
	if result.DiscrepancyRecorded {
		event, err := apprealtime.NewExamIntegrityDiscrepancyRecordedEvent(result.SubmissionID, result.SittingID,
			result.AttemptID, result.CandidateUserID, result.DiscrepancyID, result.ReceivedAt)
		if err != nil {
			return err
		}
		return errors.Join(effects.realtime.Publish(ctx, event),
			effects.publishSittingBoardInvalidation(ctx, result.ExamID, result.SittingID))
	}
	events := make([]apprealtime.RealtimeEvent, 0, 5)
	if result.ConnectionClosed {
		event, err := apprealtime.NewExamAttemptConnectionClosedEvent(result.SittingID, result.AttemptID,
			result.CandidateUserID, result.Connection.ID, result.Connection.CloseReason, result.Connection.ClosedAt.Time)
		if err != nil {
			return err
		}
		events = append(events, event)
	}
	if result.ManagerNotificationRequired {
		event, err := apprealtime.NewExamAttemptIntegrityFlaggedEvent(result.SittingID, result.AttemptID,
			result.CandidateUserID, result.Flag.ID, result.PolicyOutcome, result.RetainedEvidenceCount,
			result.EvidenceOverflowCount, result.ReceivedAt)
		if err != nil {
			return err
		}
		events = append(events, event)
	}
	if result.SuspensionCreated {
		managerEvent, err := apprealtime.NewExamAttemptSuspendedEvent(result.SittingID, result.AttemptID,
			result.CandidateUserID, result.Connection.ID, result.Flag.ID, result.Suspension.ID,
			result.Attempt.Revision, result.ReceivedAt)
		if err != nil {
			return err
		}
		candidateEvent, err := apprealtime.NewCandidateExamAttemptSuspendedEvent(result.SittingID, result.AttemptID,
			result.CandidateUserID, result.Suspension.CandidateReason, result.ReceivedAt)
		if err != nil {
			return err
		}
		events = append(events, managerEvent, candidateEvent)
	}
	if result.CandidateWarningCreated {
		event, err := apprealtime.NewCandidateExamAttemptFocusLossWarningEvent(result.SittingID, result.AttemptID,
			result.CandidateUserID, result.ReceivedAt)
		if err != nil {
			return err
		}
		events = append(events, event)
	}
	var joined error
	for _, event := range events {
		joined = errors.Join(joined, effects.realtime.Publish(ctx, event))
	}
	if result.GapDetected || result.FlagCreated || result.SuspensionCreated {
		joined = errors.Join(joined, effects.publishSittingBoardInvalidation(ctx, result.ExamID, result.SittingID))
	}
	if result.SuspensionCreated {
		joined = errors.Join(joined, effects.publishCandidateActivityInvalidation(ctx, result.CandidateUserID))
	}
	if result.ConnectionClosed {
		joined = errors.Join(joined, effects.realtime.UnbindExamAttemptConnection(ctx, result.Connection.ID))
	}
	return joined
}

func (effects examAttemptRealtimeEffects) AttemptSubmitted(ctx context.Context, result examattempt.SubmissionResult) error {
	publishErr, constructionErr := effects.publishExamAttemptSubmittedFacts(ctx, result)
	if constructionErr != nil {
		return constructionErr
	}
	return errors.Join(publishErr, effects.publishOperationalInvalidations(ctx, result.ExamID, result.SittingID,
		result.CandidateUserID), effects.realtime.UnbindExamAttemptConnection(ctx, result.ConnectionID))
}

func (effects examAttemptRealtimeEffects) publishExamAttemptSubmittedFacts(ctx context.Context,
	result examattempt.SubmissionResult,
) (publishErr, constructionErr error) {
	managerEvent, err := apprealtime.NewExamAttemptSubmittedEvent(result.SittingID, result.Receipt.AttemptID,
		result.CandidateUserID, result.Receipt.SubmissionID, result.Receipt.WorkspaceCursor,
		result.Receipt.ManifestDigest, result.Provenance, result.Receipt.SubmittedAt)
	if err != nil {
		return nil, err
	}
	candidateEvent, err := apprealtime.NewCandidateExamAttemptSubmittedEvent(result.SittingID, result.Receipt.AttemptID,
		result.CandidateUserID, result.Receipt.SubmissionID, result.Receipt.WorkspaceCursor,
		result.Receipt.ManifestDigest, result.Provenance, result.Receipt.SubmittedAt)
	if err != nil {
		return nil, err
	}
	return errors.Join(effects.realtime.Publish(ctx, managerEvent), effects.realtime.Publish(ctx, candidateEvent)), nil
}

func (effects examAttemptRealtimeEffects) AttemptSealedForSittingClose(ctx context.Context, result examattempt.AutomaticSubmissionResult) error {
	publishErr, constructionErr := effects.publishExamAttemptSubmittedFacts(ctx, result.SubmissionResult)
	if constructionErr != nil {
		return constructionErr
	}
	if result.ConnectionClosed {
		connectionEvent, eventErr := apprealtime.NewExamAttemptConnectionClosedEvent(result.SittingID,
			result.Receipt.AttemptID, result.CandidateUserID, result.ConnectionID,
			model.AttemptConnectionCloseSittingClosed, result.Receipt.SubmittedAt)
		if eventErr != nil {
			return eventErr
		}
		publishErr = errors.Join(publishErr, effects.realtime.Publish(ctx, connectionEvent))
	}
	return errors.Join(publishErr, effects.publishOperationalInvalidations(ctx, result.ExamID, result.SittingID,
		result.CandidateUserID), effects.realtime.UnbindExamAttemptConnection(ctx, result.ConnectionID))
}

func (effects examAttemptRealtimeEffects) Report(ctx context.Context, operation string, err error) {
	effects.realtime.reportTransientFailure(ctx, operation, err)
}

var _ examattempt.ManagerAuthorizer = examAttemptManagerAuthorizationAdapter{}
var _ examattempt.Auditor = examAttemptAuditAdapter{}
var _ examattempt.Effects = examAttemptRealtimeEffects{}
var _ examattempt.EffectFailures = examAttemptRealtimeEffects{}

type PrepareSecurityPreflightCommand = examattempt.PrepareSecurityPreflightCommand
type ReportSecurityPreflightCommand = examattempt.ReportSecurityPreflightCommand
type PreparedSecurityPreflight = store.SecurityPreflightPrepared

func (a *App) PrepareExamSecurityPreflight(ctx context.Context, invocation Invocation, command PrepareSecurityPreflightCommand) (*PreparedSecurityPreflight, error) {
	result, err := a.examAttempts.PrepareSecurityPreflight(ctx, invocation, command)
	return result, examAttemptError(err, true)
}
func (a *App) ReportExamSecurityPreflight(ctx context.Context, invocation Invocation, command ReportSecurityPreflightCommand) (*model.SecurityPreflightResult, error) {
	result, err := a.examAttempts.ReportSecurityPreflight(ctx, invocation, command)
	return result, examAttemptError(err, true)
}

type RecoveredSecurityPolicy = store.SecurityPolicyRecovery

func (a *App) RecoverExamSecurityPolicy(ctx context.Context, invocation Invocation, attemptID model.ExamAttemptID) (*store.SecurityPolicyRecovery, error) {
	result, err := a.examAttempts.RecoverSecurityPolicy(ctx, invocation, attemptID)
	if err != nil {
		return nil, examAttemptError(err, true)
	}
	return result, nil
}

type UpdateSecurityCoverageCommand = examattempt.UpdateSecurityCoverageCommand

func (a *App) UpdateExamSecurityCoverage(ctx context.Context, invocation Invocation, command UpdateSecurityCoverageCommand) (model.SecurityCoverageResult, error) {
	result, err := a.examAttempts.UpdateSecurityCoverage(ctx, invocation, command)
	if err != nil {
		return model.SecurityCoverageResult{}, examAttemptError(err, true)
	}
	return result, nil
}

type BrowserSourceQuery = examattempt.BrowserSourceQuery

type NativeDeliveryQuery = examattempt.NativeDeliveryQuery

type NativeDeliveryGapsCommand = examattempt.NativeDeliveryGapsCommand

type NativeDeliveryFinalCommand = examattempt.NativeDeliveryFinalCommand

type NativeDeliverySummaryCommand = examattempt.NativeDeliverySummaryCommand

func (a *App) NativeDeliveryStatus(ctx context.Context, invocation Invocation, query NativeDeliveryQuery) (*model.NativeSecurityStreamStatus, error) {
	result, err := a.examAttempts.NativeDeliveryStatus(ctx, invocation, query)
	return result, examAttemptError(err, true)
}

func (a *App) NativeDeliveryReceipt(ctx context.Context, invocation Invocation, query NativeDeliveryQuery, sequence int64) (*model.NativeBatchReceipt, error) {
	result, err := a.examAttempts.NativeDeliveryReceipt(ctx, invocation, query, sequence)
	return result, examAttemptError(err, true)
}

func (a *App) DeclareNativeDeliveryGaps(ctx context.Context, invocation Invocation, command NativeDeliveryGapsCommand) (*model.DeliveryGapReceipt, error) {
	result, err := a.examAttempts.DeclareNativeDeliveryGaps(ctx, invocation, command)
	return result, examAttemptError(err, true)
}

func (a *App) SealNativeDelivery(ctx context.Context, invocation Invocation, command NativeDeliveryFinalCommand) (*model.NativeSecurityStreamStatus, error) {
	result, err := a.examAttempts.SealNativeDelivery(ctx, invocation, command)
	return result, examAttemptError(err, true)
}

func (a *App) UpdateNativeDeliverySummary(ctx context.Context, invocation Invocation, command NativeDeliverySummaryCommand) (*model.NativeSecurityStreamStatus, error) {
	result, err := a.examAttempts.UpdateNativeDeliverySummary(ctx, invocation, command)
	return result, examAttemptError(err, true)
}

type NativeDeliveryAppendCommand = examattempt.NativeDeliveryAppendCommand

func (a *App) AppendNativeDelivery(ctx context.Context, invocation Invocation, command NativeDeliveryAppendCommand) (*model.NativeSecurityAcknowledgement, error) {
	result, err := a.examAttempts.AppendNativeDelivery(ctx, invocation, command)
	return result, examAttemptError(err, true)
}

func (a *App) BrowserSourceStatus(ctx context.Context, invocation Invocation, query BrowserSourceQuery) (*model.BrowserSourceStatus, error) {
	result, err := a.examAttempts.BrowserSourceStatus(ctx, invocation, query)
	return result, examAttemptError(err, true)
}
func (a *App) BrowserSourceList(ctx context.Context, invocation Invocation, query BrowserSourceQuery) ([]model.BrowserSourceStatus, error) {
	result, err := a.examAttempts.BrowserSourceList(ctx, invocation, query)
	return result, examAttemptError(err, true)
}

type BrowserDeliveryGapsCommand = examattempt.BrowserDeliveryGapsCommand
type BrowserDeliveryFinalCommand = examattempt.BrowserDeliveryFinalCommand
type BrowserDeliverySummaryCommand = examattempt.BrowserDeliverySummaryCommand
type BrowserDeliveryAppendCommand = examattempt.BrowserDeliveryAppendCommand

func (a *App) BrowserDeliveryReceipts(ctx context.Context, invocation Invocation, command BrowserSourceQuery, first int64, limit int) (*model.BrowserReceiptPage, error) {
	result, err := a.examAttempts.BrowserDeliveryReceipts(ctx, invocation, command, first, limit)
	return result, examAttemptError(err, true)
}

func (a *App) DeclareBrowserDeliveryGaps(ctx context.Context, invocation Invocation, command BrowserDeliveryGapsCommand) (*model.BrowserDeliveryGapResult, error) {
	result, err := a.examAttempts.DeclareBrowserDeliveryGaps(ctx, invocation, command)
	return result, examAttemptError(err, true)
}

func (a *App) SealBrowserDelivery(ctx context.Context, invocation Invocation, command BrowserDeliveryFinalCommand) (*model.BrowserSourceStatus, error) {
	result, err := a.examAttempts.SealBrowserDelivery(ctx, invocation, command)
	return result, examAttemptError(err, true)
}

func (a *App) UpdateBrowserDeliverySummary(ctx context.Context, invocation Invocation, command BrowserDeliverySummaryCommand) (*model.BrowserDeliverySummaryResult, error) {
	result, err := a.examAttempts.UpdateBrowserDeliverySummary(ctx, invocation, command)
	return result, examAttemptError(err, true)
}

func (a *App) AppendHistoricalBrowserDelivery(ctx context.Context, invocation Invocation, command BrowserDeliveryAppendCommand) (*model.BrowserActivityAcknowledgement, error) {
	result, err := a.examAttempts.AppendHistoricalBrowserDelivery(ctx, invocation, command)
	return result, examAttemptError(err, true)
}

type DeliveryBudgetQuery = examattempt.DeliveryBudgetQuery
type StopDeliveryDetailsCommand = examattempt.StopDeliveryDetailsCommand

func (a *App) DeliveryBudget(ctx context.Context, invocation Invocation, query DeliveryBudgetQuery) (*model.DeliveryBudgetSnapshot, error) {
	value, err := a.examAttempts.DeliveryBudget(ctx, invocation, query)
	return value, examAttemptError(err, true)
}
func (a *App) StopDeliveryDetails(ctx context.Context, invocation Invocation, command StopDeliveryDetailsCommand) (*model.StopDeliveryDetailsResult, error) {
	value, err := a.examAttempts.StopDeliveryDetails(ctx, invocation, command)
	return value, examAttemptError(err, true)
}

// SupportsDeliveryRecovery reports whether a failure can carry delivery repair context.
func SupportsDeliveryRecovery(code string) bool { return examattempt.SupportsDeliveryRecovery(code) }
