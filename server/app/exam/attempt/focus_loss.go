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
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type FocusLossCommand struct {
	SchemaVersion        int
	AttemptID            model.ExamAttemptID
	ParticipationID      model.AttemptParticipationID
	ConnectionID         model.AttemptConnectionID
	Generation           int64
	Sequence             int64
	DurationMilliseconds int64
	Source               model.FocusLossSource
	ContinuityCredential string
}

// FocusLossEvaluation is the safe application result of one accepted claim.
// It deliberately excludes claim duration/source, credential material, Session
// identity, evidence content, raw thresholds, and disabled diagnostic counts.
// Decision booleans describe the accepted sequence and therefore survive an
// exact duplicate; Duplicate suppresses post-commit effects, not the replayed
// acknowledgement.
type FocusLossEvaluation struct {
	ExamID                      model.ExamID
	SittingID                   model.ExamSittingID
	CandidateUserID             model.UserID
	SubmissionID                model.SubmissionID
	AttemptID                   model.ExamAttemptID
	ParticipationID             model.AttemptParticipationID
	DiscrepancyID               model.IntegrityDiscrepancyID
	Generation                  int64
	AcceptedSequence            int64
	ReceivedAt                  time.Time
	Duplicate                   bool
	GapDetected                 bool
	PolicyDisabled              bool
	Qualified                   bool
	ThresholdCrossed            bool
	PolicyOutcome               model.IntegrityThresholdOutcome
	RetainedEvidenceCount       int
	EvidenceOverflowCount       int64
	Flag                        model.IntegrityFlag
	FlagCreated                 bool
	CandidateWarningCreated     bool
	ManagerNotificationRequired bool
	Attempt                     model.ExamAttempt
	Participation               store.ExamAttemptParticipationView
	Connection                  store.ExamAttemptManagerConnection
	ConnectionClosed            bool
	Suspension                  store.ExamAttemptSuspensionView
	SuspensionCreated           bool
	DiscrepancyRecorded         bool
}

func (service *Service) EvaluateFocusLoss(ctx context.Context, call Call, command FocusLossCommand) (FocusLossEvaluation, error) {
	selector, err := focusLossSelector(call, command)
	if err != nil {
		return FocusLossEvaluation{}, err
	}
	target, err := service.deps.Persistence.ResolveFocusLossTarget(ctx, selector)
	if err != nil {
		if endedFocusLossCandidate(err) {
			return service.evaluateEndedFocusLoss(ctx, call, command, selector)
		}
		return FocusLossEvaluation{}, mapStore(err)
	}
	if !validFocusLossTarget(target, selector) {
		return FocusLossEvaluation{}, unavailable(errors.New("inconsistent Focus Loss audit target"))
	}
	auditID, err := service.deps.Auditor.Begin(ctx, call, model.ActionExamSittingParticipate,
		model.Resource{Type: model.ResourceExamSitting, ID: target.SittingID.String()}, model.RoleScopeClass,
		target.ClassID.String(), store.ExamAttemptFocusLossOperation, map[string]any{
			"exam_attempt_id": command.AttemptID.String(), "generation": command.Generation, "sequence": command.Sequence,
		})
	if err != nil {
		return FocusLossEvaluation{}, err
	}
	stored, err := service.deps.Persistence.RecordFocusLoss(ctx, &store.ExamAttemptFocusLossSignal{
		Access: selector, SchemaVersion: command.SchemaVersion, SignalID: service.deps.NewFocusLossSignal(),
		EvidenceID: service.deps.NewEvidence(), FlagID: service.deps.NewFlag(), SuspensionID: service.deps.NewSuspension(),
		Sequence: command.Sequence, DurationMilliseconds: command.DurationMilliseconds, Source: command.Source,
		AuditEventID: auditID, AuditAt: model.MillisFromTime(model.TimeUTC(service.deps.Now())),
	})
	if err != nil {
		if endedFocusLossCandidate(err) {
			if auditErr := service.deps.Auditor.Fail(ctx, auditID, "exam.attempt.state_conflict"); auditErr != nil {
				return FocusLossEvaluation{}, auditErr
			}
			return service.evaluateEndedFocusLoss(ctx, call, command, selector)
		}
		return FocusLossEvaluation{}, service.failAudit(ctx, auditID, err)
	}
	result, err := projectFocusLoss(stored, target, selector, command)
	if err != nil {
		return FocusLossEvaluation{}, err
	}
	if !result.Duplicate && (result.GapDetected || result.FlagCreated || result.ManagerNotificationRequired ||
		result.CandidateWarningCreated || result.SuspensionCreated) {
		if effectErr := service.deps.Effects.FocusLossEvaluated(ctx, result); effectErr != nil {
			service.deps.EffectFailures.Report(ctx, "exam_attempt_focus_loss_evaluated", effectErr)
		}
	}
	return result, nil
}

func endedFocusLossCandidate(err error) bool {
	var conflict *store.ErrConflict
	return errors.As(err, &conflict) && (conflict.Constraint == "exam_attempt_state" ||
		conflict.Constraint == "exam_sitting_state")
}

func (service *Service) evaluateEndedFocusLoss(ctx context.Context, call Call, command FocusLossCommand,
	selector store.ExamAttemptFocusLossAccess,
) (FocusLossEvaluation, error) {
	target, err := service.deps.Persistence.ResolveEndedFocusLossTarget(ctx, selector)
	if err != nil {
		return FocusLossEvaluation{}, mapStore(err)
	}
	if target == nil || !target.SubmissionID.IsValid() || !validFocusLossTarget(&target.ExamAttemptFocusLossTarget, selector) {
		return FocusLossEvaluation{}, unavailable(errors.New("inconsistent late Focus Loss audit target"))
	}
	auditID, err := service.deps.Auditor.Begin(ctx, call, model.ActionExamSittingParticipate,
		model.Resource{Type: model.ResourceExamSitting, ID: target.SittingID.String()}, model.RoleScopeClass,
		target.ClassID.String(), store.ExamAttemptFocusLossDiscrepancyOperation, map[string]any{
			"exam_attempt_id": command.AttemptID.String(), "submission_id": target.SubmissionID.String(),
			"generation": command.Generation, "sequence": command.Sequence,
		})
	if err != nil {
		return FocusLossEvaluation{}, err
	}
	discrepancyID := service.deps.NewDiscrepancy()
	signalID := service.deps.NewFocusLossSignal()
	stored, err := service.deps.Persistence.RecordEndedFocusLoss(ctx, &store.ExamAttemptFocusLossDiscrepancy{
		Access: selector, SchemaVersion: command.SchemaVersion, DiscrepancyID: discrepancyID, SignalID: signalID,
		Sequence: command.Sequence, DurationMilliseconds: command.DurationMilliseconds, Source: command.Source,
		AuditEventID: auditID, AuditAt: model.MillisFromTime(model.TimeUTC(service.deps.Now())),
	})
	if err != nil {
		return FocusLossEvaluation{}, service.failAudit(ctx, auditID, err)
	}
	if stored == nil || stored.Discrepancy == nil || stored.Discrepancy.Validate() != nil ||
		stored.Target != *target || stored.Discrepancy.SubmissionID != target.SubmissionID ||
		stored.Discrepancy.AttemptID != selector.AttemptID || stored.Discrepancy.ParticipationID != selector.ParticipationID ||
		stored.Discrepancy.Generation != selector.Generation || stored.Discrepancy.SchemaVersion != command.SchemaVersion ||
		stored.Discrepancy.Sequence != command.Sequence || stored.Discrepancy.DurationMilliseconds != command.DurationMilliseconds ||
		stored.Discrepancy.Source != command.Source || (!stored.Duplicate &&
		(stored.Discrepancy.ID != discrepancyID || stored.Discrepancy.SignalID != signalID)) {
		return FocusLossEvaluation{}, unavailable(errors.New("inconsistent late Focus Loss outcome"))
	}
	result := FocusLossEvaluation{ExamID: target.ExamID, SittingID: target.SittingID, CandidateUserID: target.CandidateUserID,
		SubmissionID: target.SubmissionID, AttemptID: target.AttemptID, ParticipationID: target.ParticipationID,
		DiscrepancyID: stored.Discrepancy.ID, Generation: target.Generation,
		AcceptedSequence: stored.Discrepancy.Sequence, ReceivedAt: stored.Discrepancy.ReceivedAt,
		Duplicate: stored.Duplicate, GapDetected: stored.Discrepancy.MissingBefore > 0, DiscrepancyRecorded: true}
	if !result.Duplicate {
		if effectErr := service.deps.Effects.FocusLossEvaluated(ctx, result); effectErr != nil {
			service.deps.EffectFailures.Report(ctx, "exam_attempt_focus_loss_discrepancy_recorded", effectErr)
		}
	}
	return result, nil
}

func focusLossSelector(call Call, command FocusLossCommand) (store.ExamAttemptFocusLossAccess, error) {
	selector, err := candidateSelector(call, CandidateAccess{AttemptID: command.AttemptID,
		ConnectionID: command.ConnectionID, ContinuityCredential: command.ContinuityCredential})
	if err != nil {
		return store.ExamAttemptFocusLossAccess{}, err
	}
	if command.SchemaVersion != model.FocusLossSignalSchemaVersion || !command.ParticipationID.IsValid() ||
		command.Generation < 1 || command.Sequence < 1 ||
		command.DurationMilliseconds < 1 || command.DurationMilliseconds > model.FocusLossMaximumDurationMilliseconds ||
		!command.Source.IsValid() {
		return store.ExamAttemptFocusLossAccess{}, invalid("focus_loss")
	}
	return store.ExamAttemptFocusLossAccess{AttemptID: selector.AttemptID, ParticipationID: command.ParticipationID,
		Generation: command.Generation, CandidateUserID: selector.CandidateUserID, SessionID: selector.SessionID,
		ConnectionID: selector.ConnectionID, ContinuityCredentialHash: selector.ContinuityCredentialHash}, nil
}

func validFocusLossTarget(target *store.ExamAttemptFocusLossTarget, access store.ExamAttemptFocusLossAccess) bool {
	return target != nil && target.ExamID.IsValid() && target.SittingID.IsValid() && target.ClassID.IsValid() &&
		target.CandidateUserID == access.CandidateUserID && target.AttemptID == access.AttemptID &&
		target.ParticipationID == access.ParticipationID && target.Generation == access.Generation
}

func projectFocusLoss(stored *store.ExamAttemptFocusLossResult, target *store.ExamAttemptFocusLossTarget,
	access store.ExamAttemptFocusLossAccess, command FocusLossCommand,
) (FocusLossEvaluation, error) {
	if stored == nil || stored.Signal == nil || stored.Signal.Validate() != nil || stored.ExamID != target.ExamID ||
		stored.SittingID != target.SittingID || stored.ClassID != target.ClassID || stored.CandidateUserID != access.CandidateUserID ||
		stored.AttemptID != access.AttemptID || stored.ParticipationID != access.ParticipationID || stored.Generation != access.Generation ||
		stored.Signal.AttemptID != access.AttemptID || stored.Signal.ParticipationID != access.ParticipationID ||
		stored.Signal.Generation != access.Generation || stored.Signal.Sequence != command.Sequence ||
		stored.Signal.DurationMilliseconds != command.DurationMilliseconds || stored.Signal.Source != command.Source ||
		stored.AcceptedSequence != command.Sequence || stored.DatabaseTime.IsZero() ||
		!stored.DatabaseTime.Equal(stored.Signal.ReceivedAt) || stored.MissingBefore < 0 ||
		stored.WindowIncidentCount < 0 || stored.RetainedEvidenceCount < 0 ||
		stored.RetainedEvidenceCount > model.FocusLossMaximumEvidenceEpisodes || stored.DiagnosticCount < 0 {
		return FocusLossEvaluation{}, unavailable(errors.New("inconsistent Focus Loss outcome"))
	}
	if !validFocusLossEnforcement(stored, access) {
		return FocusLossEvaluation{}, unavailable(errors.New("inconsistent Focus Loss enforcement outcome"))
	}
	result := FocusLossEvaluation{ExamID: stored.ExamID, SittingID: stored.SittingID, CandidateUserID: stored.CandidateUserID,
		AttemptID: stored.AttemptID, ParticipationID: stored.ParticipationID, Generation: stored.Generation,
		AcceptedSequence: stored.AcceptedSequence, ReceivedAt: stored.DatabaseTime, Duplicate: stored.Duplicate,
		GapDetected: stored.MissingBefore > 0, PolicyDisabled: !stored.CollectionEnabled, Qualified: stored.Qualified,
		ThresholdCrossed: stored.ThresholdCrossed, PolicyOutcome: stored.PolicyOutcome,
		RetainedEvidenceCount:       stored.RetainedEvidenceCount,
		FlagCreated:                 stored.FlagCreated,
		CandidateWarningCreated:     stored.CandidateWarningCreated,
		ManagerNotificationRequired: stored.ManagerNotificationRequired,
		ConnectionClosed:            stored.ConnectionClosed}
	if stored.Flag != nil {
		result.Flag = *stored.Flag
	}
	if stored.Overflow != nil {
		result.EvidenceOverflowCount = stored.Overflow.Count
	}
	if stored.Attempt != nil {
		result.Attempt = *stored.Attempt
	}
	if stored.Participation != nil {
		result.Participation = *stored.Participation
	}
	if stored.Connection != nil {
		result.Connection = *stored.Connection
	}
	if stored.Suspension != nil {
		result.Suspension = *stored.Suspension
		result.SuspensionCreated = true
	}
	return result, nil
}

func validFocusLossEnforcement(result *store.ExamAttemptFocusLossResult, access store.ExamAttemptFocusLossAccess) bool {
	if result.Overflow != nil && (result.Overflow.Validate() != nil || result.Overflow.AttemptID != access.AttemptID ||
		result.Overflow.ParticipationID != access.ParticipationID || result.Overflow.Generation != access.Generation) {
		return false
	}
	if !result.CollectionEnabled {
		return !result.Qualified && !result.ThresholdCrossed && result.PolicyOutcome == "" && result.Flag == nil && result.Attempt == nil &&
			result.Participation == nil && result.Connection == nil && result.Suspension == nil && !result.FlagCreated &&
			!result.CandidateWarningCreated && !result.ManagerNotificationRequired && !result.ConnectionClosed
	}
	if !result.Qualified && (result.ThresholdCrossed || result.PolicyOutcome != "" || result.Flag != nil || result.FlagCreated ||
		result.CandidateWarningCreated || result.ManagerNotificationRequired || result.Attempt != nil || result.Participation != nil ||
		result.Connection != nil || result.ConnectionClosed || result.Suspension != nil) {
		return false
	}
	if (result.FlagCreated || result.ManagerNotificationRequired || result.CandidateWarningCreated || result.Suspension != nil ||
		result.ConnectionClosed) && (!result.ThresholdCrossed || !result.Qualified) {
		return false
	}
	if result.ThresholdCrossed {
		switch result.PolicyOutcome {
		case model.IntegrityOutcomeFlag, model.IntegrityOutcomeFlagAndWarn, model.IntegrityOutcomeFlagAndSuspend:
		default:
			return false
		}
		if result.Flag == nil || result.RetainedEvidenceCount == 0 && result.Overflow == nil {
			return false
		}
	} else if result.PolicyOutcome != "" {
		return false
	}
	if result.FlagCreated != result.ManagerNotificationRequired || result.CandidateWarningCreated &&
		(!result.ThresholdCrossed || result.PolicyOutcome != model.IntegrityOutcomeFlagAndWarn) {
		return false
	}
	if result.Flag != nil && (result.Flag.Validate() != nil || result.Flag.AttemptID != access.AttemptID ||
		result.Flag.Generation != access.Generation || result.Flag.Kind != model.IntegrityPolicyFocusLoss) {
		return false
	}
	if result.FlagCreated && result.Flag == nil {
		return false
	}
	if result.Attempt != nil && (result.Attempt.Validate() != nil || result.Attempt.ID != access.AttemptID ||
		result.Attempt.ExamID != result.ExamID || result.Attempt.SittingID != result.SittingID ||
		result.Attempt.CandidateUserID != result.CandidateUserID) {
		return false
	}
	if result.Participation != nil && (!validParticipationView(result.Participation) ||
		result.Participation.ID != access.ParticipationID || result.Participation.Generation != access.Generation) {
		return false
	}
	if result.Connection != nil && (result.Connection.ID != access.ConnectionID || !validFocusLossConnection(result.Connection)) {
		return false
	}
	if result.ConnectionClosed && (result.Connection == nil || result.Connection.State != model.AttemptConnectionClosed ||
		result.Connection.CloseReason != model.AttemptConnectionClosePolicySuspended) {
		return false
	}
	if result.Suspension != nil && (!validActiveSuspension(result.Suspension, access.AttemptID) || result.Flag == nil ||
		result.Suspension.ParticipationID != access.ParticipationID || result.Suspension.Generation != access.Generation ||
		result.Suspension.FlagID != result.Flag.ID ||
		result.Suspension.CandidateReason != model.AttemptSuspensionCandidateReasonFocusLossPolicy) {
		return false
	}
	if result.PolicyOutcome == model.IntegrityOutcomeFlagAndSuspend && result.ThresholdCrossed {
		return result.Attempt != nil && result.Attempt.State == model.ExamAttemptSuspended && result.Participation != nil &&
			result.Participation.State == model.AttemptParticipationEnded &&
			result.Participation.EndReason == model.AttemptParticipationEndPolicySuspended && result.Connection != nil &&
			result.Connection.State == model.AttemptConnectionClosed && result.Suspension != nil
	}
	return result.Suspension == nil && !result.ConnectionClosed
}

func validFocusLossConnection(connection *store.ExamAttemptManagerConnection) bool {
	if connection == nil || !connection.ID.IsValid() || connection.OpenedAt.IsZero() {
		return false
	}
	switch connection.State {
	case model.AttemptConnectionOpen:
		return !connection.ClosedAt.Valid && connection.CloseReason == ""
	case model.AttemptConnectionClosed:
		return connection.ClosedAt.Valid && !connection.ClosedAt.Time.Before(connection.OpenedAt) && connection.CloseReason.IsValid()
	default:
		return false
	}
}
