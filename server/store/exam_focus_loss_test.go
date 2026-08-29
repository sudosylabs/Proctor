// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

func TestFocusLossSignalContractIsHashOnlySequencedAndEffectSafe(t *testing.T) {
	t.Parallel()
	access := ExamAttemptFocusLossAccess{AttemptID: model.NewExamAttemptID(), ParticipationID: model.NewAttemptParticipationID(),
		Generation: 3, CandidateUserID: model.NewUserID(), SessionID: model.NewSessionID(), ConnectionID: model.NewAttemptConnectionID(),
		ContinuityCredentialHash: model.HashToken(model.NewCredentialToken())}
	target := ExamAttemptFocusLossTarget{ExamID: model.NewExamID(), SittingID: model.NewExamSittingID(), ClassID: model.NewClassID(),
		CandidateUserID: access.CandidateUserID, AttemptID: access.AttemptID, ParticipationID: access.ParticipationID, Generation: access.Generation}
	input := ExamAttemptFocusLossSignal{Access: access, SchemaVersion: model.FocusLossSignalSchemaVersion,
		SignalID: model.NewFocusLossSignalID(), EvidenceID: model.NewIntegrityEvidenceID(), FlagID: model.NewIntegrityFlagID(),
		SuspensionID: model.NewAttemptSuspensionID(), Sequence: 7, DurationMilliseconds: 500,
		Source: model.FocusLossSourceDocumentHidden, AuditEventID: model.NewId(), AuditAt: model.GetMillis()}
	result := ExamAttemptFocusLossResult{ExamID: target.ExamID, SittingID: target.SittingID, ClassID: target.ClassID,
		CandidateUserID: target.CandidateUserID, AttemptID: access.AttemptID, ParticipationID: access.ParticipationID,
		Generation: access.Generation, AcceptedSequence: input.Sequence, DatabaseTime: model.NowUTC(), CollectionEnabled: true,
		Qualified: true, MissingBefore: 2, WindowIncidentCount: 1}
	if result.AcceptedSequence != 7 || result.MissingBefore != 2 || input.DurationMilliseconds != 500 ||
		target.ParticipationID != access.ParticipationID {
		t.Fatalf("Focus Loss contract = %#v / %#v / %#v", access, target, result)
	}
	inputType := reflect.TypeOf(input)
	for _, forbidden := range []string{"ClientTimestamp", "ReceivedAt", "Outcome", "Severity", "Guilt", "Payload"} {
		if _, exists := inputType.FieldByName(forbidden); exists {
			t.Fatalf("Focus Loss input lets the client supply %s", forbidden)
		}
	}
	assertFocusLossResultSafe(t, reflect.TypeOf(result))
	methods := reflect.TypeOf((*ExamAttemptStore)(nil)).Elem()
	for _, name := range []string{"ResolveFocusLossTarget", "RecordFocusLoss"} {
		if _, exists := methods.MethodByName(name); !exists {
			t.Fatalf("ExamAttemptStore is missing %s", name)
		}
	}
}

func TestFocusLossResultExpressesDisabledDuplicateThresholdAndOverflowWithoutPayloads(t *testing.T) {
	t.Parallel()
	receivedAt := time.Date(2026, time.August, 21, 9, 0, 0, 0, time.UTC)
	result := ExamAttemptFocusLossResult{CollectionEnabled: true, Qualified: true, ThresholdCrossed: true,
		FlagCreated: true, CandidateWarningCreated: true, ManagerNotificationRequired: true, DatabaseTime: receivedAt,
		Overflow: &model.FocusLossEvidenceOverflow{AttemptID: model.NewExamAttemptID(), ParticipationID: model.NewAttemptParticipationID(),
			Generation: 2, Count: 1, FirstReceivedAt: receivedAt, LastReceivedAt: receivedAt, MaximumDurationMilliseconds: 500}}
	if !result.ThresholdCrossed || result.WindowIncidentCount != 0 || !result.FlagCreated ||
		!result.CandidateWarningCreated || !result.ManagerNotificationRequired {
		t.Fatalf("threshold result = %#v", result)
	}
	disabled := ExamAttemptFocusLossResult{AcceptedSequence: 1, DatabaseTime: receivedAt, DiagnosticCount: 1}
	if disabled.CollectionEnabled || disabled.Qualified || disabled.Flag != nil || disabled.Suspension != nil {
		t.Fatalf("disabled result invented enforcement: %#v", disabled)
	}
	replay := result
	replay.Duplicate = true
	if !replay.Duplicate || !replay.CandidateWarningCreated {
		t.Fatalf("duplicate did not retain its prior accepted result: %#v", replay)
	}
	assertFocusLossResultSafe(t, reflect.TypeOf(result))
}

func TestExamAttemptReallowResultMakesCausalFocusWindowResetExplicit(t *testing.T) {
	t.Parallel()
	result := ExamAttemptReallowResult{FocusLossWindowReset: true}
	if !result.FocusLossWindowReset {
		t.Fatal("Focus Loss causal evaluation reset is not observable")
	}
}

func TestCandidateExamPresentationExposesOnlyTheSafeCollectionDecision(t *testing.T) {
	t.Parallel()
	presentation := CandidateExamPresentation{RuntimeCapabilities: CandidateRuntimeCapabilities{FocusLossCollectionEnabled: true}}
	if !presentation.RuntimeCapabilities.FocusLossCollectionEnabled {
		t.Fatal("candidate presentation omitted the current Revision's Focus Loss collection decision")
	}
	typ := reflect.TypeOf(presentation)
	for _, forbidden := range []string{"FocusLossPolicy", "MinimumDuration", "Threshold", "Window", "Outcome"} {
		if _, exists := typ.FieldByName(forbidden); exists {
			t.Fatalf("candidate presentation exposes private policy field %s", forbidden)
		}
	}
}

func assertFocusLossResultSafe(t *testing.T, typ reflect.Type) {
	t.Helper()
	for index := 0; index < typ.NumField(); index++ {
		name := strings.ToLower(typ.Field(index).Name)
		for _, forbidden := range []string{"credential", "session", "clipboard", "screenshot", "terminal", "sourcecode", "payload"} {
			if strings.Contains(name, forbidden) {
				t.Fatalf("outbound %s exposes unsafe field %s", typ, typ.Field(index).Name)
			}
		}
	}
}
