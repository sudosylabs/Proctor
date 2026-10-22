// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package store

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestFocusLossSignalContractIsHashOnlySequencedAndEffectSafe(t *testing.T) {
	t.Parallel()
	access := ExamAttemptFocusLossAccess{AttemptID: model.NewExamAttemptID(), ParticipationID: model.NewAttemptParticipationID(),
		Generation: 3, CandidateUserID: model.NewUserID(), SessionID: model.NewSessionID(), ConnectionID: model.NewAttemptConnectionID(),
		ContinuityCredentialHash: model.HashToken(model.NewCredentialToken())}
	target := ExamAttemptFocusLossTarget{ExamID: model.NewExamID(), SittingID: model.NewExamSittingID(), ClassID: model.NewClassID(),
		CandidateUserID: access.CandidateUserID, AttemptID: access.AttemptID, ParticipationID: access.ParticipationID, Generation: access.Generation}
	input := ExamAttemptFocusLossSignal{Access: access,
		SignalID: model.NewFocusLossSignalID(), EvidenceID: model.NewIntegrityEvidenceID(), FlagID: model.NewIntegrityFlagID(),
		SuspensionID: model.NewAttemptSuspensionID(), Sequence: 7, DurationMilliseconds: 500,
		Source: model.FocusLossSourceDocumentHidden, AuditEventID: model.NewAuditEventID(), AuditAt: model.NowUTC()}
	result := ExamAttemptFocusLossResult{ExamID: target.ExamID, SittingID: target.SittingID, ClassID: target.ClassID,
		CandidateUserID: target.CandidateUserID, AttemptID: access.AttemptID, ParticipationID: access.ParticipationID,
		Generation: access.Generation, AcceptedSequence: input.Sequence, DatabaseTime: model.NowUTC(), CollectionEnabled: true,
		Qualified: true, MissingBefore: 2, WindowIncidentCount: 1}
	require.Equal(t, int64(7), result.AcceptedSequence, "Focus Loss contract = %#v / %#v / %#v", access, target, result)
	require.Equal(t, int64(2), result.MissingBefore, "Focus Loss contract = %#v / %#v / %#v", access, target, result)
	require.Equal(t, int64(500), input.DurationMilliseconds, "Focus Loss contract = %#v / %#v / %#v", access, target, result)
	require.Equal(t, access.ParticipationID, target.ParticipationID, "Focus Loss contract = %#v / %#v / %#v", access, target, result)
	inputType := reflect.TypeOf(input)
	for _, forbidden := range []string{"ClientTimestamp", "ReceivedAt", "Outcome", "Severity", "Guilt", "Payload"} {
		_, exists := inputType.FieldByName(forbidden)
		require.False(t, exists, "Focus Loss input lets the client supply %s", forbidden)
	}
	assertFocusLossResultSafe(t, reflect.TypeOf(result))
	methods := reflect.TypeOf((*ExamAttemptStore)(nil)).Elem()
	for _, name := range []string{"ResolveFocusLossTarget", "RecordFocusLoss"} {
		_, exists := methods.MethodByName(name)
		require.True(t, exists, "ExamAttemptStore is missing %s", name)
	}
}

func TestFocusLossResultExpressesDisabledDuplicateThresholdAndOverflowWithoutPayloads(t *testing.T) {
	t.Parallel()
	receivedAt := time.Date(2026, time.August, 21, 9, 0, 0, 0, time.UTC)
	result := ExamAttemptFocusLossResult{CollectionEnabled: true, Qualified: true, ThresholdCrossed: true,
		FlagCreated: true, CandidateWarningCreated: true, ManagerNotificationRequired: true, DatabaseTime: receivedAt,
		Overflow: &model.FocusLossEvidenceOverflow{AttemptID: model.NewExamAttemptID(), ParticipationID: model.NewAttemptParticipationID(),
			Generation: 2, Count: 1, FirstReceivedAt: receivedAt, LastReceivedAt: receivedAt, MaximumDurationMilliseconds: 500}}
	require.True(t, result.ThresholdCrossed)
	require.Equal(t, 0, result.WindowIncidentCount)
	require.True(t, result.FlagCreated)
	require.True(t, result.CandidateWarningCreated)
	require.True(t, result.ManagerNotificationRequired)
	disabled := ExamAttemptFocusLossResult{AcceptedSequence: 1, DatabaseTime: receivedAt, DiagnosticCount: 1}
	require.False(t, disabled.CollectionEnabled)
	require.False(t, disabled.Qualified)
	require.Nil(t, disabled.Flag)
	require.Nil(t, disabled.Suspension)
	replay := result
	replay.Duplicate = true
	require.True(t, replay.Duplicate)
	require.True(t, replay.CandidateWarningCreated)
	assertFocusLossResultSafe(t, reflect.TypeOf(result))
}

func TestExamAttemptReallowResultMakesCausalFocusWindowResetExplicit(t *testing.T) {
	t.Parallel()
	result := ExamAttemptReallowResult{FocusLossWindowReset: true}
	require.True(t, result.FocusLossWindowReset, "Focus Loss causal evaluation reset is not observable")
}

func TestCandidateExamPresentationExposesOnlyTheSafeCollectionDecision(t *testing.T) {
	t.Parallel()
	presentation := CandidateExamPresentation{RuntimeCapabilities: CandidateRuntimeCapabilities{FocusLossCollectionEnabled: true}}
	require.True(t, presentation.RuntimeCapabilities.FocusLossCollectionEnabled, "candidate presentation omitted the current Revision's Focus Loss collection decision")
	typ := reflect.TypeOf(presentation)
	for _, forbidden := range []string{"FocusLossPolicy", "MinimumDuration", "Threshold", "Window", "Outcome"} {
		_, exists := typ.FieldByName(forbidden)
		require.False(t, exists, "candidate presentation exposes private policy field %s", forbidden)
	}
}

func assertFocusLossResultSafe(t *testing.T, typ reflect.Type) {
	t.Helper()
	for index := 0; index < typ.NumField(); index++ {
		name := strings.ToLower(typ.Field(index).Name)
		for _, forbidden := range []string{"credential", "session", "clipboard", "screenshot", "terminal", "sourcecode", "payload"} {
			require.NotContains(t, name, forbidden, "outbound")
		}
	}
}
