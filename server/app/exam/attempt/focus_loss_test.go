// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package attempt

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestEvaluateFocusLossHashesCredentialAuditsSafeScopeAndPublishesCommittedWarning(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	credential := model.NewCredentialToken()
	participationID := model.NewAttemptParticipationID()
	signalID, flagID := model.NewFocusLossSignalID(), model.NewIntegrityFlagID()
	receivedAt := f.at.Add(time.Second)
	signal, err := model.NewFocusLossSignal(signalID, f.attemptID, participationID, 3, 11, 2500,
		model.FocusLossSourceDocumentHidden, receivedAt)
	if err != nil {
		t.Fatal(err)
	}
	flag, err := model.NewIntegrityFlag(flagID, f.attemptID, 3, model.IntegrityPolicyFocusLoss, receivedAt)
	if err != nil {
		t.Fatal(err)
	}
	f.focusSignalID = signalID
	f.focusFlagID = flagID
	f.persistence.focusTarget = &store.ExamAttemptFocusLossTarget{ExamID: f.sitting.ExamID, SittingID: f.sitting.ID,
		ClassID: f.sitting.ClassID, CandidateUserID: f.userID, AttemptID: f.attemptID,
		ParticipationID: participationID, Generation: 3}
	f.persistence.focusResult = &store.ExamAttemptFocusLossResult{ExamID: f.sitting.ExamID, SittingID: f.sitting.ID,
		ClassID: f.sitting.ClassID, CandidateUserID: f.userID, AttemptID: f.attemptID,
		ParticipationID: participationID, Generation: 3, Signal: signal, AcceptedSequence: 11, DatabaseTime: receivedAt,
		CollectionEnabled: true, Qualified: true, ThresholdCrossed: true, PolicyOutcome: model.IntegrityOutcomeFlagAndWarn,
		RetainedEvidenceCount: 1, Flag: flag, FlagCreated: true, CandidateWarningCreated: true,
		ManagerNotificationRequired: true}

	result, err := f.service.EvaluateFocusLoss(context.Background(), f.call, FocusLossCommand{
		SchemaVersion: model.FocusLossSignalSchemaVersion,
		AttemptID:     f.attemptID, ParticipationID: participationID, ConnectionID: f.connectionID,
		Generation: 3, Sequence: 11, DurationMilliseconds: 2500, Source: model.FocusLossSourceDocumentHidden,
		ContinuityCredential: credential,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.AttemptID != f.attemptID || result.Generation != 3 || result.AcceptedSequence != 11 ||
		result.ReceivedAt != receivedAt || result.Duplicate || result.GapDetected || result.PolicyDisabled || !result.Qualified ||
		!result.FlagCreated || !result.CandidateWarningCreated || result.SuspensionCreated || f.effects.focusLoss != 1 {
		t.Fatalf("result=%#v effects=%#v", result, f.effects)
	}
	principal := f.call.Principal()
	access := f.persistence.focusAccess
	if access.AttemptID != f.attemptID || access.ParticipationID != participationID || access.ConnectionID != f.connectionID ||
		access.CandidateUserID != principal.UserID || access.SessionID != principal.SessionID || access.Generation != 3 ||
		access.ContinuityCredentialHash != model.HashToken(credential) || f.persistence.focusSignal == nil ||
		f.persistence.focusSignal.Access != access ||
		f.persistence.focusSignal.SchemaVersion != model.FocusLossSignalSchemaVersion ||
		f.persistence.focusSignal.SignalID != signalID ||
		!f.persistence.focusSignal.EvidenceID.IsValid() || f.persistence.focusSignal.FlagID != flagID ||
		!f.persistence.focusSignal.FlagID.IsValid() || !f.persistence.focusSignal.SuspensionID.IsValid() {
		t.Fatalf("access=%#v signal=%#v", access, f.persistence.focusSignal)
	}
	capture := fmt.Sprintf("%#v", f.audit.values)
	for _, forbidden := range []string{credential, model.HashToken(credential), principal.SessionID.String(),
		string(model.FocusLossSourceDocumentHidden), "2500"} {
		if strings.Contains(capture, forbidden) {
			t.Fatalf("Focus Loss audit exposed %q: %s", forbidden, capture)
		}
	}
	if got := strings.Join(f.order, ","); got != "focus.resolve,audit,focus.record,effect.focus_loss" {
		t.Fatalf("order=%s", got)
	}
}

func TestEvaluateFocusLossRetainsExplicitLateDiscrepancyAfterSubmission(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	participationID := model.NewAttemptParticipationID()
	credential := model.NewCredentialToken()
	submissionID, discrepancyID, signalID := model.NewSubmissionID(), model.NewIntegrityDiscrepancyID(), model.NewFocusLossSignalID()
	receivedAt := f.at.Add(time.Second)
	f.focusSignalID, f.discrepancyID = signalID, discrepancyID
	f.persistence.focusErr = store.NewErrConflict("exam_attempt", "exam_attempt_state", errors.New("submitted"))
	target := &store.ExamAttemptFocusLossDiscrepancyTarget{ExamAttemptFocusLossTarget: store.ExamAttemptFocusLossTarget{
		ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, ClassID: f.sitting.ClassID, CandidateUserID: f.userID,
		AttemptID: f.attemptID, ParticipationID: participationID, Generation: 3}, SubmissionID: submissionID}
	discrepancy, err := model.NewIntegrityDiscrepancy(model.IntegrityDiscrepancySpecification{
		ID: discrepancyID, SubmissionID: submissionID, AttemptID: f.attemptID, ParticipationID: participationID,
		Generation: 3, Kind: model.IntegrityDiscrepancyLateFocusLoss, SchemaVersion: model.FocusLossSignalSchemaVersion,
		SignalID: signalID, Sequence: 12, DurationMilliseconds: 2100, Source: model.FocusLossSourceDocumentHidden,
		MissingBefore: 1, ReceivedAt: receivedAt})
	if err != nil {
		t.Fatal(err)
	}
	f.persistence.lateFocusTarget = target
	f.persistence.lateFocusResult = &store.ExamAttemptFocusLossDiscrepancyResult{Target: *target,
		Discrepancy: discrepancy}
	result, err := f.service.EvaluateFocusLoss(context.Background(), f.call, FocusLossCommand{
		SchemaVersion: model.FocusLossSignalSchemaVersion, AttemptID: f.attemptID, ParticipationID: participationID,
		ConnectionID: f.connectionID, Generation: 3, Sequence: 12, DurationMilliseconds: 2100,
		Source: model.FocusLossSourceDocumentHidden, ContinuityCredential: credential})
	if err != nil {
		t.Fatal(err)
	}
	if !result.DiscrepancyRecorded || result.SubmissionID != submissionID || result.DiscrepancyID != discrepancyID ||
		result.AcceptedSequence != 12 || !result.GapDetected || result.ReceivedAt != receivedAt || f.effects.focusLoss != 1 {
		t.Fatalf("result=%#v effects=%#v", result, f.effects)
	}
	if f.persistence.lateFocusInput == nil || f.persistence.lateFocusInput.Access.ContinuityCredentialHash != model.HashToken(credential) ||
		f.persistence.lateFocusInput.DiscrepancyID.IsZero() || f.persistence.lateFocusInput.SignalID != signalID ||
		f.persistence.lateFocusInput.Sequence != 12 || f.persistence.lateFocusInput.DurationMilliseconds != 2100 {
		t.Fatalf("late input=%#v", f.persistence.lateFocusInput)
	}
	if got := strings.Join(f.order, ","); got != "focus.resolve,focus.late.resolve,audit,focus.late.record,effect.focus_loss" {
		t.Fatalf("order=%s", got)
	}
}

func TestEvaluateFocusLossRejectsMissingOrUnknownClaimSchemaBeforePersistence(t *testing.T) {
	t.Parallel()
	for _, schemaVersion := range []int{0, model.FocusLossSignalSchemaVersion + 1} {
		schemaVersion := schemaVersion
		t.Run(fmt.Sprintf("schema_%d", schemaVersion), func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			_, err := f.service.EvaluateFocusLoss(context.Background(), f.call, FocusLossCommand{
				SchemaVersion: schemaVersion, AttemptID: f.attemptID,
				ParticipationID: model.NewAttemptParticipationID(), ConnectionID: f.connectionID,
				Generation: 1, Sequence: 1, DurationMilliseconds: 500,
				ContinuityCredential: model.NewCredentialToken(),
			})
			var fault *Fault
			if !errors.As(err, &fault) || fault.Code != "exam.attempt.invalid" ||
				f.persistence.focusTarget != nil || f.persistence.focusSignal != nil || f.effects.focusLoss != 0 {
				t.Fatalf("error=%v target=%#v signal=%#v effects=%#v", err,
					f.persistence.focusTarget, f.persistence.focusSignal, f.effects)
			}
		})
	}
}

func TestEvaluateFocusLossDisabledPolicyReturnsDiagnosticAcknowledgementWithoutEffect(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	participationID := model.NewAttemptParticipationID()
	credential := model.NewCredentialToken()
	signalID := model.NewFocusLossSignalID()
	signal, err := model.NewFocusLossSignal(signalID, f.attemptID, participationID, 1, 1, 700,
		model.FocusLossSourceWindowBlur, f.at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	f.focusSignalID = signalID
	f.persistence.focusTarget = focusLossTargetFixture(f, participationID, 1)
	f.persistence.focusResult = &store.ExamAttemptFocusLossResult{ExamID: f.sitting.ExamID, SittingID: f.sitting.ID,
		ClassID: f.sitting.ClassID, CandidateUserID: f.userID, AttemptID: f.attemptID,
		ParticipationID: participationID, Generation: 1, Signal: signal, AcceptedSequence: 1,
		DatabaseTime: signal.ReceivedAt, CollectionEnabled: false, DiagnosticCount: 1}
	result, err := f.service.EvaluateFocusLoss(context.Background(), f.call, FocusLossCommand{SchemaVersion: model.FocusLossSignalSchemaVersion, AttemptID: f.attemptID,
		ParticipationID: participationID, ConnectionID: f.connectionID, Generation: 1, Sequence: 1,
		DurationMilliseconds: 700, Source: model.FocusLossSourceWindowBlur, ContinuityCredential: credential})
	if err != nil || !result.PolicyDisabled || result.Qualified || result.FlagCreated || result.CandidateWarningCreated ||
		result.SuspensionCreated || f.effects.focusLoss != 0 {
		t.Fatalf("result=%#v error=%v effects=%#v", result, err, f.effects)
	}
	if capture := fmt.Sprintf("%#v", result); strings.Contains(capture, "window_blur") || strings.Contains(capture, "700") ||
		strings.Contains(capture, credential) || strings.Contains(capture, model.HashToken(credential)) {
		t.Fatalf("disabled acknowledgement exposed claim/private data: %s", capture)
	}
}

func TestEvaluateFocusLossDuplicateAcknowledgesPriorDecisionWithoutRepeatingEffects(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	participationID := model.NewAttemptParticipationID()
	signal, err := model.NewFocusLossSignal(model.NewFocusLossSignalID(), f.attemptID, participationID, 2, 8, 2500,
		model.FocusLossSourceDocumentHidden, f.at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	flag, err := model.NewIntegrityFlag(model.NewIntegrityFlagID(), f.attemptID, 2, model.IntegrityPolicyFocusLoss, signal.ReceivedAt)
	if err != nil {
		t.Fatal(err)
	}
	f.persistence.focusTarget = focusLossTargetFixture(f, participationID, 2)
	f.persistence.focusResult = &store.ExamAttemptFocusLossResult{ExamID: f.sitting.ExamID, SittingID: f.sitting.ID,
		ClassID: f.sitting.ClassID, CandidateUserID: f.userID, AttemptID: f.attemptID,
		ParticipationID: participationID, Generation: 2, Signal: signal, AcceptedSequence: 8,
		DatabaseTime: signal.ReceivedAt, CollectionEnabled: true, Qualified: true, ThresholdCrossed: true,
		PolicyOutcome: model.IntegrityOutcomeFlagAndWarn, RetainedEvidenceCount: 1, Flag: flag,
		FlagCreated: true, CandidateWarningCreated: true, ManagerNotificationRequired: true, Duplicate: true}
	result, err := f.service.EvaluateFocusLoss(context.Background(), f.call, FocusLossCommand{SchemaVersion: model.FocusLossSignalSchemaVersion, AttemptID: f.attemptID,
		ParticipationID: participationID, ConnectionID: f.connectionID, Generation: 2, Sequence: 8,
		DurationMilliseconds: 2500, Source: model.FocusLossSourceDocumentHidden,
		ContinuityCredential: model.NewCredentialToken()})
	if err != nil || !result.Duplicate || !result.FlagCreated || !result.CandidateWarningCreated ||
		!result.ManagerNotificationRequired || result.SuspensionCreated || f.effects.focusLoss != 0 {
		t.Fatalf("result=%#v error=%v effects=%#v", result, err, f.effects)
	}
}

func TestEvaluateFocusLossDuplicateReplaysPriorSuspensionWithoutRepeatingEffects(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	participationID := model.NewAttemptParticipationID()
	command, stored := focusLossSuspensionFixture(t, f, participationID)
	stored.Duplicate = true
	f.persistence.focusTarget = focusLossTargetFixture(f, participationID, command.Generation)
	f.persistence.focusResult = stored

	result, err := f.service.EvaluateFocusLoss(context.Background(), f.call, command)
	if err != nil || !result.Duplicate || !result.FlagCreated || !result.ManagerNotificationRequired ||
		!result.ConnectionClosed || !result.SuspensionCreated || f.effects.focusLoss != 0 {
		t.Fatalf("result=%#v error=%v effects=%#v", result, err, f.effects)
	}
}

func TestEvaluateFocusLossProjectsCommittedPolicySuspensionAndPublishesOnce(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	participationID := model.NewAttemptParticipationID()
	command, stored := focusLossSuspensionFixture(t, f, participationID)
	f.persistence.focusTarget = focusLossTargetFixture(f, participationID, command.Generation)
	f.persistence.focusResult = stored

	result, err := f.service.EvaluateFocusLoss(context.Background(), f.call, command)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Qualified || !result.ThresholdCrossed || result.PolicyOutcome != model.IntegrityOutcomeFlagAndSuspend ||
		!result.FlagCreated || !result.ManagerNotificationRequired || !result.ConnectionClosed || !result.SuspensionCreated ||
		result.CandidateWarningCreated || result.Connection.ID != f.connectionID ||
		result.Suspension.CandidateReason != model.AttemptSuspensionCandidateReasonFocusLossPolicy || f.effects.focusLoss != 1 {
		t.Fatalf("result=%#v effects=%#v", result, f.effects)
	}
}

func TestEvaluateFocusLossRejectsEnforcementForAnotherConnection(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	participationID := model.NewAttemptParticipationID()
	command, stored := focusLossSuspensionFixture(t, f, participationID)
	stored.Connection.ID = model.NewAttemptConnectionID()
	f.persistence.focusTarget = focusLossTargetFixture(f, participationID, command.Generation)
	f.persistence.focusResult = stored

	result, err := f.service.EvaluateFocusLoss(context.Background(), f.call, command)
	if err == nil || result != (FocusLossEvaluation{}) || f.effects.focusLoss != 0 {
		t.Fatalf("result=%#v error=%v effects=%#v", result, err, f.effects)
	}
}

func TestEvaluateFocusLossRejectsInconsistentSuspensionAggregate(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		mutate func(*store.ExamAttemptFocusLossResult)
	}{
		{name: "attempt remains active", mutate: func(result *store.ExamAttemptFocusLossResult) {
			result.Attempt.State = model.ExamAttemptActive
		}},
		{name: "suspension names another flag", mutate: func(result *store.ExamAttemptFocusLossResult) {
			result.Suspension.FlagID = model.NewIntegrityFlagID()
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			participationID := model.NewAttemptParticipationID()
			command, stored := focusLossSuspensionFixture(t, f, participationID)
			test.mutate(stored)
			f.persistence.focusTarget = focusLossTargetFixture(f, participationID, command.Generation)
			f.persistence.focusResult = stored

			result, err := f.service.EvaluateFocusLoss(context.Background(), f.call, command)
			if err == nil || result != (FocusLossEvaluation{}) || f.effects.focusLoss != 0 {
				t.Fatalf("result=%#v error=%v effects=%#v", result, err, f.effects)
			}
		})
	}
}

func TestEvaluateFocusLossRejectsThresholdWithoutItsOpenFlag(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	participationID := model.NewAttemptParticipationID()
	receivedAt := f.at.Add(time.Second)
	signal, err := model.NewFocusLossSignal(model.NewFocusLossSignalID(), f.attemptID, participationID,
		2, 4, 2500, model.FocusLossSourceDocumentHidden, receivedAt)
	if err != nil {
		t.Fatal(err)
	}
	f.persistence.focusTarget = focusLossTargetFixture(f, participationID, 2)
	f.persistence.focusResult = &store.ExamAttemptFocusLossResult{ExamID: f.sitting.ExamID, SittingID: f.sitting.ID,
		ClassID: f.sitting.ClassID, CandidateUserID: f.userID, AttemptID: f.attemptID,
		ParticipationID: participationID, Generation: 2, Signal: signal, AcceptedSequence: 4,
		DatabaseTime: receivedAt, CollectionEnabled: true, Qualified: true, ThresholdCrossed: true,
		PolicyOutcome: model.IntegrityOutcomeFlag, RetainedEvidenceCount: 1}

	result, err := f.service.EvaluateFocusLoss(context.Background(), f.call, FocusLossCommand{SchemaVersion: model.FocusLossSignalSchemaVersion, AttemptID: f.attemptID,
		ParticipationID: participationID, ConnectionID: f.connectionID, Generation: 2, Sequence: 4,
		DurationMilliseconds: 2500, Source: model.FocusLossSourceDocumentHidden,
		ContinuityCredential: model.NewCredentialToken()})
	if err == nil || result != (FocusLossEvaluation{}) || f.effects.focusLoss != 0 {
		t.Fatalf("result=%#v error=%v effects=%#v", result, err, f.effects)
	}
}

func TestEvaluateFocusLossMapsChangedSameSequenceToStableConflict(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	participationID := model.NewAttemptParticipationID()
	f.persistence.focusTarget = focusLossTargetFixture(f, participationID, 2)
	f.persistence.focusErr = store.NewErrConflict("exam_attempt_focus_loss", "focus_loss_sequence", errors.New("changed claim"))

	_, err := f.service.EvaluateFocusLoss(context.Background(), f.call, FocusLossCommand{SchemaVersion: model.FocusLossSignalSchemaVersion, AttemptID: f.attemptID,
		ParticipationID: participationID, ConnectionID: f.connectionID, Generation: 2, Sequence: 8,
		DurationMilliseconds: 2500, Source: model.FocusLossSourceDocumentHidden,
		ContinuityCredential: model.NewCredentialToken()})
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "exam.attempt.focus_loss_conflict" || f.effects.focusLoss != 0 {
		t.Fatalf("error=%v effects=%#v", err, f.effects)
	}
}

func focusLossSuspensionFixture(t *testing.T, f *fixture,
	participationID model.AttemptParticipationID,
) (FocusLossCommand, *store.ExamAttemptFocusLossResult) {
	t.Helper()
	generation, sequence := int64(4), int64(9)
	receivedAt := f.at.Add(time.Second)
	signal, err := model.NewFocusLossSignal(model.NewFocusLossSignalID(), f.attemptID, participationID,
		generation, sequence, 2500, model.FocusLossSourceApplicationBackgrounded, receivedAt)
	if err != nil {
		t.Fatal(err)
	}
	flag, err := model.NewIntegrityFlag(model.NewIntegrityFlagID(), f.attemptID, generation,
		model.IntegrityPolicyFocusLoss, receivedAt)
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := model.NewExamAttempt(f.attemptID, f.sitting.ExamID, f.sitting.ID, f.userID,
		f.revision.ID, f.at.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err = attempt.Suspend(receivedAt); err != nil {
		t.Fatal(err)
	}
	participation := &store.ExamAttemptParticipationView{ID: participationID, AttemptID: f.attemptID,
		State: model.AttemptParticipationEnded, Generation: generation, RenewalSequence: sequence,
		StartedAt: f.at.Add(-time.Minute), UpdatedAt: receivedAt,
		LeaseExpiresAt: f.at.Add(model.AttemptParticipationInitialLease), EndedAt: model.OptionalTimeFrom(receivedAt),
		EndReason: model.AttemptParticipationEndPolicySuspended}
	connection := &store.ExamAttemptManagerConnection{ID: f.connectionID, State: model.AttemptConnectionClosed,
		OpenedAt: f.at.Add(-time.Minute), ClosedAt: model.OptionalTimeFrom(receivedAt),
		CloseReason: model.AttemptConnectionClosePolicySuspended}
	suspension := &store.ExamAttemptSuspensionView{ID: model.NewAttemptSuspensionID(), AttemptID: f.attemptID,
		ParticipationID: participationID, FlagID: flag.ID, Generation: generation, State: model.AttemptSuspensionActive,
		Source: model.AttemptSuspensionSourcePolicy, CandidateReason: model.AttemptSuspensionCandidateReasonFocusLossPolicy,
		StartedAt: receivedAt}
	command := FocusLossCommand{SchemaVersion: model.FocusLossSignalSchemaVersion,
		AttemptID: f.attemptID, ParticipationID: participationID, ConnectionID: f.connectionID,
		Generation: generation, Sequence: sequence, DurationMilliseconds: 2500,
		Source: model.FocusLossSourceApplicationBackgrounded, ContinuityCredential: model.NewCredentialToken()}
	return command, &store.ExamAttemptFocusLossResult{ExamID: f.sitting.ExamID, SittingID: f.sitting.ID,
		ClassID: f.sitting.ClassID, CandidateUserID: f.userID, AttemptID: f.attemptID,
		ParticipationID: participationID, Generation: generation, Signal: signal, AcceptedSequence: sequence,
		DatabaseTime: receivedAt, CollectionEnabled: true, Qualified: true, ThresholdCrossed: true,
		PolicyOutcome: model.IntegrityOutcomeFlagAndSuspend, RetainedEvidenceCount: 1, Flag: flag,
		FlagCreated: true, ManagerNotificationRequired: true, Attempt: attempt, Participation: participation,
		Connection: connection, ConnectionClosed: true, Suspension: suspension}
}

func focusLossTargetFixture(f *fixture, participationID model.AttemptParticipationID,
	generation int64,
) *store.ExamAttemptFocusLossTarget {
	return &store.ExamAttemptFocusLossTarget{ExamID: f.sitting.ExamID, SittingID: f.sitting.ID,
		ClassID: f.sitting.ClassID, CandidateUserID: f.userID, AttemptID: f.attemptID,
		ParticipationID: participationID, Generation: generation}
}
