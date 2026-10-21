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

// ExamAttemptConnection is the outcome of admitting a candidate's registered
// desktop to an Exam Attempt: the admitted Attempt, Attempt Workspace, Attempt
// Participation, Attempt Connection, frozen configuration and current runtime
// capabilities. Replayed reports an idempotent replay.
type ExamAttemptConnection = examattempt.ConnectionResult

// ExamAttemptConnectionClosed is the outcome of closing one Attempt
// Connection; Changed is false when the Connection was already closed.
type ExamAttemptConnectionClosed = examattempt.ConnectionClosedResult

// ExamAttemptParticipationRenewal is the candidate-safe acknowledgement of one
// Attempt Participation renewal, carrying authoritative database time and the
// new lease expiry but no credential material.
type ExamAttemptParticipationRenewal = examattempt.ParticipationRenewal

// ExamAttemptFocusLossEvaluation is the safe result of evaluating one accepted
// Focus Loss claim against the frozen policy, including any Integrity Flag or
// Attempt Suspension it produced. Duplicate suppresses post-commit effects,
// not the replayed acknowledgement.
type ExamAttemptFocusLossEvaluation = examattempt.FocusLossEvaluation

// ExamAttemptReallowResult is the committed outcome of an Exam Manager
// re-allowing a suspended Exam Attempt; Replayed reports an idempotent replay.
type ExamAttemptReallowResult = examattempt.ReallowResult

// ExamAttemptManualSuspensionResult is the committed, private-reason-free
// outcome of an Exam Manager kick. Connection is the kicked generation's
// latest Attempt Connection, if any.
type ExamAttemptManualSuspensionResult = examattempt.ManualSuspensionResult

// SuspendExamAttemptCommand is an Exam Manager's kick: it ends the live
// Participation Generation and opens a manual Attempt Suspension, fenced by
// ExpectedAttemptRevision and replayed by IdempotencyKey.
type SuspendExamAttemptCommand = examattempt.ManualSuspensionCommand

// CandidateExamAttemptAccess identifies the candidate's own Exam Attempt
// through its live Attempt Connection and continuity credential; candidate
// reads and mutations authenticate with it.
type CandidateExamAttemptAccess = examattempt.CandidateAccess

// CandidateExamPresentation is the correction-aware protected content a
// candidate sees for an admitted Exam Attempt: Exam Instructions, published
// Exam Resources, Browser Policy, live corrections and runtime capabilities.
type CandidateExamPresentation = examattempt.Presentation

// CandidateExamWorkspaceItem is the candidate-safe metadata of one Workspace
// Entry; it exposes neither starter object identity nor a VFS key.
type CandidateExamWorkspaceItem = store.CandidateAttemptWorkspaceItem

// ExamAttemptManagerView is the bounded projection of one Exam Attempt shown
// to Exam Managers, including its latest Attempt Participation, current
// Attempt Connection and active Attempt Suspension. It carries no credential
// hash or authored content.
type ExamAttemptManagerView = store.ExamAttemptManagerSnapshot

// OpenedExamAttemptContent is an opened file stream with its media type, size,
// SHA-256 and Workspace Content Version; the caller must close Body.
type OpenedExamAttemptContent = examattempt.OpenedContent

// ExamAttemptWorkspaceMutationAccess is candidate access fenced to one live
// Attempt Participation and Participation Generation, required by every
// Attempt Workspace mutation and Submission.
type ExamAttemptWorkspaceMutationAccess = examattempt.WorkspaceMutationAccess

// ExamAttemptWorkspaceMutationResult is the outcome of one Attempt Workspace
// mutation: the affected Workspace Entry (nil after deletion) and its journal
// change. Replayed reports an idempotent replay.
type ExamAttemptWorkspaceMutationResult = examattempt.WorkspaceMutationResult

// CandidateExamWorkspaceJournalPage is one page of Attempt Workspace journal
// entries after a cursor. RefreshRequired means the cursor predates the
// retained journal and the client must reload the manifest.
type CandidateExamWorkspaceJournalPage = examattempt.WorkspaceJournalPage

// CandidateExamActivityPage is one page of the candidate's own Exam Sittings
// and Attempts with the server time it was read at.
type CandidateExamActivityPage = examattempt.CandidateActivityPage

// SittingCandidateStatusesPage is one page of per-candidate Attempt status for
// an Exam Sitting's monitoring board.
type SittingCandidateStatusesPage = examattempt.SittingCandidateStatusesPage

// SittingCandidatePresenceState classifies a candidate's presence in an Exam
// Sitting, such as not yet started.
type SittingCandidatePresenceState = store.SittingCandidatePresenceState

// CandidateRuntimeCapabilities is the authoritative snapshot of what a
// candidate's active Exam Attempt may currently do. It carries no credential,
// placement, capacity or authored Browser Policy data.
type CandidateRuntimeCapabilities = store.CandidateRuntimeCapabilities

// CandidateBrowserPolicy is the Browser Policy currently disclosed to a
// candidate with its separately revisioned disclosure; a pending browser
// correction withholds the policy until acknowledged.
type CandidateBrowserPolicy = store.CandidateBrowserPolicy

// BrowserActivityAcknowledgement acknowledges a delivered Browser Activity
// batch with per-event receipts, the settled and contiguous sequence
// positions, and any missing ranges still expected.
type BrowserActivityAcknowledgement = model.BrowserActivityAcknowledgement

// BrowserSourceRefusal describes an already committed closure of a predecessor
// Browser Activity source session.
type BrowserSourceRefusal = store.BrowserSourceRefusal

// BrowserActivityRecord is one stored Browser Activity event with the Attempt
// Participation, Participation Generation and source session that delivered
// it.
type BrowserActivityRecord = store.BrowserActivityRecord

// StartBrowserActivityCommand registers or transitions a Browser Activity
// source session for the candidate's live Attempt Participation, fenced by
// Participation Generation and the frozen policy revision and digest.
type StartBrowserActivityCommand = examattempt.StartBrowserActivityCommand

// AppendBrowserActivityCommand appends a bounded batch of Browser Activity
// events from a started source session of the candidate's live Attempt
// Participation.
type AppendBrowserActivityCommand = examattempt.AppendBrowserActivityCommand

// ListBrowserActivityQuery pages an Exam Attempt's Browser Activity for an
// Exam Manager, after a (received-at, source session, sequence) cursor.
type ListBrowserActivityQuery = examattempt.BrowserActivityPageQuery

// BrowserActivityPage is one page of Browser Activity records; HasMore reports
// whether another page follows.
type BrowserActivityPage = examattempt.BrowserActivityPage

// AcknowledgeExamCorrectionCommand records that the candidate's live Attempt
// Participation has received a Sitting correction. ExpectedCurrentRevisionID
// must match the Attempt's current Exam Revision; IdempotencyKey makes it
// replayable.
type AcknowledgeExamCorrectionCommand = examattempt.AcknowledgeCorrectionCommand

// ExamCorrectionAcknowledgementResult is the outcome of acknowledging a
// Sitting correction, with the runtime capabilities that now apply.
type ExamCorrectionAcknowledgementResult = examattempt.CorrectionAcknowledgementResult

// EndExamAttemptByManagerCommand asks an Exam Manager to end a suspended Exam
// Attempt and seal its Submission, fenced by ExpectedAttemptRevision and
// replayed by IdempotencyKey. PrivateReason is never shown to the candidate.
type EndExamAttemptByManagerCommand = examattempt.ManagerEndCommand

// ConnectExamAttemptCommand asks to admit the caller's registered desktop to
// an Exam Sitting, starting the Exam Attempt on first admission or opening a
// new Attempt Connection on an existing one. IdempotencyKey makes it
// replayable.
type ConnectExamAttemptCommand = examattempt.ConnectCommand

// CloseExamAttemptConnectionCommand closes one Attempt Connection for the
// stated reason.
type CloseExamAttemptConnectionCommand = examattempt.CloseConnectionCommand

// RenewExamAttemptParticipationCommand renews the lease of a live Attempt
// Participation, fenced by Participation Generation and a monotonic Sequence
// and authenticated by the continuity credential.
type RenewExamAttemptParticipationCommand = examattempt.RenewParticipationCommand

// EvaluateExamAttemptFocusLossCommand reports one Focus Loss claim, identified
// by Participation Generation and Sequence, from the candidate's live Attempt
// Participation.
type EvaluateExamAttemptFocusLossCommand = examattempt.FocusLossCommand

// ReallowExamAttemptCommand asks an Exam Manager to end one Attempt Suspension
// so the Exam Attempt may continue, fenced by ExpectedAttemptRevision and
// replayed by IdempotencyKey.
type ReallowExamAttemptCommand = examattempt.ReallowCommand

// ListCandidateExamWorkspaceQuery pages the candidate's Attempt Workspace
// manifest at ExpectedCursor (-1 starts at the current cursor), after a
// Workspace Entry.
type ListCandidateExamWorkspaceQuery = examattempt.WorkspaceQuery

// CandidateExamWorkspacePage is one page of the Attempt Workspace manifest at
// Cursor. RefreshRequired means the Workspace changed since the expected
// cursor and listing must restart.
type CandidateExamWorkspacePage = examattempt.WorkspacePage

// ListCandidateExamWorkspaceJournalQuery pages the candidate's Attempt
// Workspace journal after AfterCursor.
type ListCandidateExamWorkspaceJournalQuery = examattempt.WorkspaceJournalQuery

// CreateCandidateExamWorkspaceDirectoryCommand creates a directory at a
// Workspace Path in the candidate's Attempt Workspace; IdempotencyKey makes it
// replayable.
type CreateCandidateExamWorkspaceDirectoryCommand = examattempt.CreateWorkspaceDirectoryCommand

// CreateCandidateExamWorkspaceFileCommand creates a file at a Workspace Path
// in the candidate's Attempt Workspace from Body, verified against Size and
// ExpectedSHA256; IdempotencyKey makes it replayable.
type CreateCandidateExamWorkspaceFileCommand = examattempt.CreateWorkspaceFileCommand

// ReplaceCandidateExamWorkspaceFileCommand replaces a file's content in the
// candidate's Attempt Workspace, fenced by ExpectedPath and
// ExpectedContentVersion; IdempotencyKey makes it replayable.
type ReplaceCandidateExamWorkspaceFileCommand = examattempt.ReplaceWorkspaceFileCommand

// MoveCandidateExamWorkspaceEntryCommand moves a Workspace Entry from
// ExpectedPath to DestinationPath in the candidate's Attempt Workspace;
// IdempotencyKey makes it replayable.
type MoveCandidateExamWorkspaceEntryCommand = examattempt.MoveWorkspaceEntryCommand

// DeleteCandidateExamWorkspaceEntryCommand deletes a Workspace Entry from the
// candidate's Attempt Workspace. A recursive delete is fenced by
// ExpectedWorkspaceCursor and a file delete by ExpectedContentVersion;
// IdempotencyKey makes it replayable.
type DeleteCandidateExamWorkspaceEntryCommand = examattempt.DeleteWorkspaceEntryCommand

// OpenCandidateExamResourceQuery names one published Exam Resource through the
// candidate's Attempt access.
type OpenCandidateExamResourceQuery = examattempt.OpenResourceQuery

// OpenCandidateExamWorkspaceFileQuery names one Attempt Workspace file through
// the candidate's Attempt access.
type OpenCandidateExamWorkspaceFileQuery = examattempt.OpenWorkspaceFileQuery

// GetExamAttemptQuery names one Exam Attempt within an Exam Sitting for an
// Exam Manager.
type GetExamAttemptQuery = examattempt.GetManagedAttemptQuery

// ListExamAttemptsQuery pages an Exam Sitting's Attempts for an Exam Manager,
// optionally filtered by state, newest first.
type ListExamAttemptsQuery = examattempt.ListManagedAttemptsQuery

// ExamAttemptManagerPage is one page of Exam Attempt manager views; HasMore
// reports whether another page follows.
type ExamAttemptManagerPage = examattempt.ManagedAttemptPage

// ListCandidateExamActivityQuery pages the caller's own Exam Sittings and
// Attempts before a scheduled-start cursor.
type ListCandidateExamActivityQuery = examattempt.CandidateActivityQuery

// ListSittingCandidateStatusesQuery pages the candidates of one Exam Sitting
// after a User cursor for the monitoring board.
type ListSittingCandidateStatusesQuery = examattempt.SittingCandidateStatusesQuery

// examAttemptSittingAuthorizer is the Sitting authorization the Attempt
// manager adapter composes; its tests substitute a fake.
type examAttemptSittingAuthorizer interface {
	AuthorizeView(context.Context, Invocation, model.ExamSittingID) error
	AuthorizeManage(context.Context, Invocation, model.ExamSittingID) (override bool, err error)
	AuthorizeBrowserActivityView(context.Context, Invocation, model.ExamSittingID) (model.AcademicUnitID, error)
	AuthorizeSubmissionView(context.Context, Invocation, model.ExamID, model.SubmissionID) error
}

type examAttemptManagerAuthorizationAdapter struct {
	sittings    examAttemptSittingAuthorizer
	submissions store.ExamSubmissionStore
	audit       examAttemptAuthorizationDecisionAudit
}

type examAttemptAuthorizationDecisionAudit interface {
	RecordAuthorizationDecision(context.Context, model.Principal, model.Action, model.Resource,
		model.RoleScopeType, string, model.RequestMetadata, bool) error
}

func (a examAttemptManagerAuthorizationAdapter) AuthorizeSittingView(ctx context.Context, invocation Invocation, sittingID model.ExamSittingID) error {
	return a.sittings.AuthorizeView(ctx, invocation, sittingID)
}

func (a examAttemptManagerAuthorizationAdapter) AuthorizeSittingManage(ctx context.Context, invocation Invocation, sittingID model.ExamSittingID) (bool, error) {
	override, err := a.sittings.AuthorizeManage(ctx, invocation, sittingID)
	if err == nil {
		return override, nil
	}
	return false, err
}

func (a examAttemptManagerAuthorizationAdapter) AuthorizeBrowserActivityView(ctx context.Context, invocation Invocation, sittingID model.ExamSittingID) (model.AcademicUnitID, error) {
	unitID, err := a.sittings.AuthorizeBrowserActivityView(ctx, invocation, sittingID)
	if err == nil {
		return unitID, nil
	}
	return "", err
}

func (a examAttemptManagerAuthorizationAdapter) AuthorizeSubmissionView(ctx context.Context, invocation Invocation,
	submissionID model.SubmissionID,
) error {
	if a.submissions == nil || a.audit == nil || !submissionID.IsValid() {
		return apperror.New("exam.attempt.unavailable").Wrap(errors.New("Submission authorization dependencies are invalid"))
	}
	authorization, err := a.submissions.Resolve(ctx, submissionID)
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
		if err = a.audit.RecordAuthorizationDecision(ctx, invocation.Principal(), model.ActionSubmissionView,
			model.Resource{Type: model.ResourceSubmission, ID: authorization.SubmissionID.String()},
			model.RoleScopeAcademicUnit, authorization.AcademicUnitID.String(), invocation.RequestMetadata(), false); err != nil {
			return err
		}
		return apperror.New("exam.attempt.not_found")
	}
	return a.sittings.AuthorizeSubmissionView(ctx, invocation,
		authorization.ExamID, submissionID)
}

type examAttemptRealtimeEffects struct {
	realtime *realtimeService
}

func (e examAttemptRealtimeEffects) publishCandidateActivityInvalidation(ctx context.Context,
	candidateID model.UserID,
) error {
	event, err := apprealtime.NewCandidateExamActivityChangedEvent(candidateID)
	if err != nil {
		return err
	}
	return e.realtime.PublishRealtimeEvent(ctx, event)
}

func (e examAttemptRealtimeEffects) publishSittingBoardInvalidation(ctx context.Context,
	examID model.ExamID, sittingID model.ExamSittingID,
) error {
	event, err := apprealtime.NewManagerSittingBoardChangedEvent(examID, sittingID)
	if err != nil {
		return err
	}
	return e.realtime.PublishRealtimeEvent(ctx, event)
}

func (e examAttemptRealtimeEffects) publishOperationalInvalidations(ctx context.Context,
	examID model.ExamID, sittingID model.ExamSittingID, candidateID model.UserID,
) error {
	return errors.Join(
		e.publishCandidateActivityInvalidation(ctx, candidateID),
		e.publishSittingBoardInvalidation(ctx, examID, sittingID),
		e.realtime.InvalidateCurrentUserContext(ctx, candidateID),
	)
}

func (e examAttemptRealtimeEffects) ConnectionOpened(ctx context.Context, result examattempt.ConnectionResult) error {
	if len(result.RevokedSessionIDs) > 0 || len(result.RevokedAccessTokenDigests) > 0 {
		e.realtime.SessionsRevoked(ctx, result.Attempt.CandidateUserID.String(), result.RevokedSessionIDs, result.RevokedAccessTokenDigests)
	}
	event, err := apprealtime.NewExamAttemptConnectionOpenedEvent(result.Attempt.SittingID,
		result.Attempt.ID, result.Attempt.CandidateUserID, result.Connection.ID, result.Connection.OpenedAt)
	if err != nil {
		return err
	}
	return errors.Join(e.realtime.PublishRealtimeEvent(ctx, event), e.publishOperationalInvalidations(ctx,
		result.Attempt.ExamID, result.Attempt.SittingID, result.Attempt.CandidateUserID))
}

func (e examAttemptRealtimeEffects) ConnectionClosed(ctx context.Context, result examattempt.ConnectionClosedResult) error {
	event, err := apprealtime.NewExamAttemptConnectionClosedEvent(result.SittingID, result.AttemptID,
		result.CandidateUserID, result.Connection.ID, result.Connection.CloseReason, result.Connection.ClosedAt.Time)
	if err != nil {
		return err
	}
	return errors.Join(e.realtime.PublishRealtimeEvent(ctx, event),
		e.publishSittingBoardInvalidation(ctx, result.ExamID, result.SittingID))
}

func (e examAttemptRealtimeEffects) ParticipationRenewed(ctx context.Context, result examattempt.ParticipationRenewal) error {
	return e.publishSittingBoardInvalidation(ctx, result.ExamID, result.SittingID)
}

func (e examAttemptRealtimeEffects) BrowserActivityGapChanged(ctx context.Context, examID model.ExamID,
	sittingID model.ExamSittingID,
) error {
	return e.publishSittingBoardInvalidation(ctx, examID, sittingID)
}

func (e examAttemptRealtimeEffects) ParticipationExpired(ctx context.Context, result examattempt.ParticipationExpiry) error {
	managerEvent, err := apprealtime.NewExamAttemptPolicySuspendedEvent(result.SittingID, result.Attempt.ID, result.CandidateUserID,
		result.Connection.ID, result.Flag.ID, result.Suspension.ID, result.Attempt.Revision, result.DatabaseTime)
	if err != nil {
		return err
	}
	candidateEvent, err := apprealtime.NewCandidateExamAttemptSuspendedEvent(result.SittingID, result.Attempt.ID,
		result.CandidateUserID, result.Suspension.Source, result.Suspension.CandidateReason, result.DatabaseTime)
	if err != nil {
		return err
	}
	if !result.ConnectionClosed {
		return errors.Join(e.realtime.PublishRealtimeEvent(ctx, managerEvent), e.realtime.PublishRealtimeEvent(ctx, candidateEvent),
			e.publishOperationalInvalidations(ctx, result.ExamID, result.SittingID, result.CandidateUserID))
	}
	connectionEvent, err := apprealtime.NewExamAttemptConnectionClosedEvent(result.SittingID, result.Attempt.ID,
		result.CandidateUserID, result.Connection.ID, result.Connection.CloseReason, result.Connection.ClosedAt.Time)
	if err != nil {
		return err
	}
	return errors.Join(e.realtime.PublishRealtimeEvent(ctx, connectionEvent), e.realtime.PublishRealtimeEvent(ctx, managerEvent),
		e.realtime.PublishRealtimeEvent(ctx, candidateEvent), e.publishOperationalInvalidations(ctx, result.ExamID,
			result.SittingID, result.CandidateUserID), e.realtime.UnbindExamAttemptConnection(ctx, result.Connection.ID))
}

func (e examAttemptRealtimeEffects) AttemptReallowed(ctx context.Context, result examattempt.ReallowResult) error {
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
	return errors.Join(e.realtime.PublishRealtimeEvent(ctx, managerEvent), e.realtime.PublishRealtimeEvent(ctx, candidateEvent),
		e.publishOperationalInvalidations(ctx, result.ExamID, result.SittingID, result.CandidateUserID))
}

// AttemptSuspendedByManager publishes a committed kick: the closed live
// Connection, if any, the manager and candidate suspension facts, operational
// invalidations, and removal of the exact live Connection binding.
func (e examAttemptRealtimeEffects) AttemptSuspendedByManager(ctx context.Context, result examattempt.ManualSuspensionResult) error {
	var connectionID model.AttemptConnectionID
	if result.Connection != nil {
		connectionID = result.Connection.ID
	}
	managerEvent, err := apprealtime.NewExamAttemptKickedEvent(result.SittingID, result.Attempt.ID, result.CandidateUserID,
		connectionID, result.Suspension.ID, result.Attempt.Revision, result.Suspension.StartedAt)
	if err != nil {
		return err
	}
	candidateEvent, err := apprealtime.NewCandidateExamAttemptSuspendedEvent(result.SittingID, result.Attempt.ID,
		result.CandidateUserID, result.Suspension.Source, result.Suspension.CandidateReason, result.Suspension.StartedAt)
	if err != nil {
		return err
	}
	if !result.ConnectionClosed || result.Connection == nil {
		return errors.Join(e.realtime.PublishRealtimeEvent(ctx, managerEvent), e.realtime.PublishRealtimeEvent(ctx, candidateEvent),
			e.publishOperationalInvalidations(ctx, result.ExamID, result.SittingID, result.CandidateUserID))
	}
	connectionEvent, err := apprealtime.NewExamAttemptConnectionClosedEvent(result.SittingID, result.Attempt.ID,
		result.CandidateUserID, result.Connection.ID, result.Connection.CloseReason, result.Connection.ClosedAt.Time)
	if err != nil {
		return err
	}
	return errors.Join(e.realtime.PublishRealtimeEvent(ctx, connectionEvent), e.realtime.PublishRealtimeEvent(ctx, managerEvent),
		e.realtime.PublishRealtimeEvent(ctx, candidateEvent), e.publishOperationalInvalidations(ctx, result.ExamID,
			result.SittingID, result.CandidateUserID), e.realtime.UnbindExamAttemptConnection(ctx, result.Connection.ID))
}

func (e examAttemptRealtimeEffects) WorkspaceChanged(ctx context.Context, result examattempt.WorkspaceMutationResult) error {
	event, err := apprealtime.NewCandidateExamAttemptWorkspaceChangedEvent(result.SittingID, result.AttemptID,
		result.CandidateUserID, result.Change.EntryID, result.Change.Operation, result.Change.Cursor, result.Change.ChangedAt)
	if err != nil {
		return err
	}
	return errors.Join(e.realtime.PublishRealtimeEvent(ctx, event))
}

func (e examAttemptRealtimeEffects) FocusLossEvaluated(ctx context.Context, result examattempt.FocusLossEvaluation) error {
	if result.DiscrepancyRecorded {
		event, err := apprealtime.NewExamIntegrityDiscrepancyRecordedEvent(result.SubmissionID, result.SittingID,
			result.AttemptID, result.CandidateUserID, result.DiscrepancyID, result.ReceivedAt)
		if err != nil {
			return err
		}
		return errors.Join(e.realtime.PublishRealtimeEvent(ctx, event),
			e.publishSittingBoardInvalidation(ctx, result.ExamID, result.SittingID))
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
		managerEvent, err := apprealtime.NewExamAttemptPolicySuspendedEvent(result.SittingID, result.AttemptID,
			result.CandidateUserID, result.Connection.ID, result.Flag.ID, result.Suspension.ID,
			result.Attempt.Revision, result.ReceivedAt)
		if err != nil {
			return err
		}
		candidateEvent, err := apprealtime.NewCandidateExamAttemptSuspendedEvent(result.SittingID, result.AttemptID,
			result.CandidateUserID, result.Suspension.Source, result.Suspension.CandidateReason, result.ReceivedAt)
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
		joined = errors.Join(joined, e.realtime.PublishRealtimeEvent(ctx, event))
	}
	if result.GapDetected || result.FlagCreated || result.SuspensionCreated {
		joined = errors.Join(joined, e.publishSittingBoardInvalidation(ctx, result.ExamID, result.SittingID))
	}
	if result.SuspensionCreated {
		joined = errors.Join(joined, e.publishCandidateActivityInvalidation(ctx, result.CandidateUserID))
	}
	if result.ConnectionClosed {
		joined = errors.Join(joined, e.realtime.UnbindExamAttemptConnection(ctx, result.Connection.ID))
	}
	return joined
}

func (e examAttemptRealtimeEffects) AttemptSubmitted(ctx context.Context, result examattempt.SubmissionResult) error {
	publishErr, constructionErr := e.publishExamAttemptSubmittedFacts(ctx, result)
	if constructionErr != nil {
		return constructionErr
	}
	return errors.Join(publishErr, e.publishOperationalInvalidations(ctx, result.ExamID, result.SittingID,
		result.CandidateUserID), e.realtime.UnbindExamAttemptConnection(ctx, result.ConnectionID))
}

func (e examAttemptRealtimeEffects) publishExamAttemptSubmittedFacts(ctx context.Context,
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
	return errors.Join(e.realtime.PublishRealtimeEvent(ctx, managerEvent), e.realtime.PublishRealtimeEvent(ctx, candidateEvent)), nil
}

func (e examAttemptRealtimeEffects) AttemptSealedForSittingClose(ctx context.Context, result examattempt.AutomaticSubmissionResult) error {
	publishErr, constructionErr := e.publishExamAttemptSubmittedFacts(ctx, result.SubmissionResult)
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
		publishErr = errors.Join(publishErr, e.realtime.PublishRealtimeEvent(ctx, connectionEvent))
	}
	return errors.Join(publishErr, e.publishOperationalInvalidations(ctx, result.ExamID, result.SittingID,
		result.CandidateUserID), e.realtime.UnbindExamAttemptConnection(ctx, result.ConnectionID))
}

func (e examAttemptRealtimeEffects) Report(ctx context.Context, operation string, err error) {
	e.realtime.reportTransientFailure(ctx, operation, err)
}

var _ examattempt.ManagerAuthorizer = examAttemptManagerAuthorizationAdapter{}
var _ examattempt.Auditor = mutationAuditAdapter{}
var _ examattempt.Effects = examAttemptRealtimeEffects{}
var _ examattempt.EffectFailures = examAttemptRealtimeEffects{}

// PrepareSecurityPreflightCommand starts a security preflight for an Exam
// Sitting from a registered desktop, binding the native registry, source
// manifest and configuration manifest digests; IdempotencyKey makes it
// replayable.
type PrepareSecurityPreflightCommand = examattempt.PrepareSecurityPreflightCommand

// ReportSecurityPreflightCommand submits the desktop's report for a prepared
// security preflight; IdempotencyKey makes it replayable.
type ReportSecurityPreflightCommand = examattempt.ReportSecurityPreflightCommand

// PreparedSecurityPreflight is a prepared security preflight: its challenge,
// the resolved native policy, the Browser Activity disclosure and any frozen
// Attempt configuration.
type PreparedSecurityPreflight = store.SecurityPreflightPrepared

// RecoveredSecurityPolicy returns privileged admission provenance only to the
// active owner's registered Session. It neither grants nor renews a
// Participation Lease.
type RecoveredSecurityPolicy = store.SecurityPolicyRecovery

// UpdateSecurityCoverageCommand reports the current security coverage of the
// candidate's live Attempt Participation, fenced by Participation Generation.
type UpdateSecurityCoverageCommand = examattempt.UpdateSecurityCoverageCommand

// BrowserSourceQuery selects Browser Activity delivery state for the
// candidate's Exam Attempt, by source session or by Attempt Participation.
type BrowserSourceQuery = examattempt.BrowserSourceQuery

// NativeDeliveryQuery selects one native security stream of the candidate's
// Exam Attempt.
type NativeDeliveryQuery = examattempt.NativeDeliveryQuery

// NativeDeliveryGapsCommand declares native security batches the client can no
// longer deliver for one stream; IdempotencyKey makes it replayable.
type NativeDeliveryGapsCommand = examattempt.NativeDeliveryGapsCommand

// NativeDeliveryFinalCommand records the final delivered batch sequence for
// one native security stream; IdempotencyKey makes it replayable.
type NativeDeliveryFinalCommand = examattempt.NativeDeliveryFinalCommand

// NativeDeliverySummaryCommand records the client's summary of native security
// records it did not retain for one stream; IdempotencyKey makes it
// replayable.
type NativeDeliverySummaryCommand = examattempt.NativeDeliverySummaryCommand

// NativeDeliveryAppendCommand appends one native security batch to a stream of
// the candidate's Exam Attempt; IdempotencyKey makes it replayable.
type NativeDeliveryAppendCommand = examattempt.NativeDeliveryAppendCommand

// BrowserDeliveryGapsCommand declares Browser Activity the client can no
// longer deliver for one source session; IdempotencyKey makes it replayable.
type BrowserDeliveryGapsCommand = examattempt.BrowserDeliveryGapsCommand

// BrowserDeliveryFinalCommand records the final delivered sequence for one
// Browser Activity source session; IdempotencyKey makes it replayable.
type BrowserDeliveryFinalCommand = examattempt.BrowserDeliveryFinalCommand

// BrowserDeliverySummaryCommand records the client's summary of Browser
// Activity it did not retain for one source session; IdempotencyKey makes it
// replayable.
type BrowserDeliverySummaryCommand = examattempt.BrowserDeliverySummaryCommand

// BrowserDeliveryAppendCommand delivers a Browser Activity batch outside a
// live Attempt Connection; its access must carry no connection or continuity
// credential. IdempotencyKey makes it replayable.
type BrowserDeliveryAppendCommand = examattempt.BrowserDeliveryAppendCommand

// DeliveryBudgetQuery selects the delivery quota usage of one Attempt
// Participation of the candidate's Exam Attempt.
type DeliveryBudgetQuery = examattempt.DeliveryBudgetQuery

// StopDeliveryDetailsCommand stops detailed delivery for one delivery family
// of the candidate's live Exam Attempt after a local loss inventory;
// IdempotencyKey makes it replayable.
type StopDeliveryDetailsCommand = examattempt.StopDeliveryDetailsCommand

// SupportsDeliveryRecovery reports whether a failure can carry delivery repair context.
func SupportsDeliveryRecovery(code string) bool { return examattempt.SupportsDeliveryRecovery(code) }
