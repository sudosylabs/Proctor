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
	"github.com/sudosylabs/proctor/server/app/invocation"
	apprealtime "github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// examAttemptSittingAuthorizer is the Sitting authorization the Attempt
// manager adapter composes; its tests substitute a fake.
type examAttemptSittingAuthorizer interface {
	AuthorizeView(context.Context, invocation.Invocation, model.ExamSittingID) error
	AuthorizeManage(context.Context, invocation.Invocation, model.ExamSittingID) (override bool, err error)
	AuthorizeBrowserActivityView(context.Context, invocation.Invocation, model.ExamSittingID) (model.AcademicUnitID, error)
	AuthorizeSubmissionView(context.Context, invocation.Invocation, model.ExamID, model.SubmissionID) error
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

func (a examAttemptManagerAuthorizationAdapter) AuthorizeSittingView(ctx context.Context, invocation invocation.Invocation, sittingID model.ExamSittingID) error {
	return a.sittings.AuthorizeView(ctx, invocation, sittingID)
}

func (a examAttemptManagerAuthorizationAdapter) AuthorizeSittingManage(ctx context.Context, invocation invocation.Invocation, sittingID model.ExamSittingID) (bool, error) {
	override, err := a.sittings.AuthorizeManage(ctx, invocation, sittingID)
	if err == nil {
		return override, nil
	}
	return false, err
}

func (a examAttemptManagerAuthorizationAdapter) AuthorizeBrowserActivityView(ctx context.Context, invocation invocation.Invocation, sittingID model.ExamSittingID) (model.AcademicUnitID, error) {
	unitID, err := a.sittings.AuthorizeBrowserActivityView(ctx, invocation, sittingID)
	if err == nil {
		return unitID, nil
	}
	return "", err
}

func (a examAttemptManagerAuthorizationAdapter) AuthorizeSubmissionView(ctx context.Context, invocation invocation.Invocation,
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

// SupportsDeliveryRecovery reports whether a failure can carry delivery repair context.
func SupportsDeliveryRecovery(code string) bool { return examattempt.SupportsDeliveryRecovery(code) }
