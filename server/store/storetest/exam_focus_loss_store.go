// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func testExamAttemptFocusLoss(t *testing.T, ctx context.Context, ss store.Store, fixture examAttemptFixture,
	connected *store.ExamAttemptConnectResult, credentialHash string, probes ...ExamAttemptSQLProbe,
) *store.ExamAttemptFocusLossSignal {
	t.Helper()
	access := store.ExamAttemptFocusLossAccess{AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID,
		Generation: connected.Participation.Generation, CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID,
		ConnectionID: connected.Connection.ID, ContinuityCredentialHash: credentialHash}
	target, err := ss.ExamAttempt().ResolveFocusLossTarget(ctx, access)
	require.NoError(t, err)
	require.Equal(t, fixture.examID, target.ExamID)
	require.Equal(t, fixture.sitting.ID, target.SittingID)
	require.Equal(t, fixture.class.ID, target.ClassID)
	require.Equal(t, fixture.candidate.ID, target.CandidateUserID)
	require.Equal(t, connected.Attempt.ID, target.AttemptID)
	require.Equal(t, connected.Participation.ID, target.ParticipationID)
	require.Equal(t, connected.Participation.Generation, target.Generation)

	first := recordFocusLoss(t, ctx, ss, fixture, access, 1, 2000, model.FocusLossSourceWindowBlur)
	second := recordFocusLoss(t, ctx, ss, fixture, access, 2, 2000, model.FocusLossSourceDocumentHidden)
	require.True(t, first.Qualified, "Focus Loss rolling bucket = %#v, %#v", first, second)
	require.False(t, first.ThresholdCrossed, "Focus Loss rolling bucket = %#v, %#v", first, second)
	require.Equal(t, 1, first.WindowIncidentCount, "Focus Loss rolling bucket = %#v, %#v", first, second)
	require.True(t, second.Qualified, "Focus Loss rolling bucket = %#v, %#v", first, second)
	require.False(t, second.ThresholdCrossed, "Focus Loss rolling bucket = %#v, %#v", first, second)
	require.Equal(t, 2, second.WindowIncidentCount, "Focus Loss rolling bucket = %#v, %#v", first, second)
	crossingInput := newFocusLossInput(t, ctx, ss, fixture, access, 4, 2000, model.FocusLossSourceFullscreenExited)
	crossed, err := ss.ExamAttempt().RecordFocusLoss(ctx, crossingInput)
	require.NoError(t, err)
	require.False(t, crossed.Duplicate)
	require.True(t, crossed.CollectionEnabled)
	require.True(t, crossed.Qualified)
	require.Equal(t, int64(1), crossed.MissingBefore)
	require.True(t, crossed.ThresholdCrossed)
	require.Equal(t, 0, crossed.WindowIncidentCount)
	require.Equal(t, model.IntegrityOutcomeFlagAndWarn, crossed.PolicyOutcome)
	require.Equal(t, 3, crossed.RetainedEvidenceCount)
	require.NotNil(t, crossed.Flag)
	require.True(t, crossed.FlagCreated)
	require.True(t, crossed.CandidateWarningCreated)
	require.True(t, crossed.ManagerNotificationRequired)
	require.Nil(t, crossed.Attempt)
	require.Nil(t, crossed.Suspension)
	requireSuccessfulAudit(t, ctx, ss, crossingInput.AuditEventID)
	assertFocusLossAuditAllowlist(t, ctx, ss, crossingInput.AuditEventID)
	audit, err := ss.Audit().Get(ctx, crossingInput.AuditEventID)
	require.NoError(t, err)
	for _, forbidden := range []string{credentialHash, string(crossingInput.Source), "2000"} {
		require.False(t, bytes.Contains(audit.Result, []byte(forbidden)), "Focus Loss audit exposed %q", forbidden)
	}

	replay := *crossingInput
	replay.SignalID, replay.EvidenceID, replay.FlagID, replay.SuspensionID = model.NewFocusLossSignalID(), model.NewIntegrityEvidenceID(), model.NewIntegrityFlagID(), model.NewAttemptSuspensionID()
	replay.AuditEventID, replay.AuditAt = saveExamAttemptAudit(t, ctx, ss, fixture).ID, model.NowUTC()
	replayed, err := ss.ExamAttempt().RecordFocusLoss(ctx, &replay)
	require.NoError(t, err)
	require.True(t, replayed.Duplicate, "RecordFocusLoss(duplicate) = %#v, first = %#v", replayed, crossed)
	require.Equal(t, crossed.Signal.ID, replayed.Signal.ID, "RecordFocusLoss(duplicate) = %#v, first = %#v", replayed, crossed)
	require.True(t, replayed.DatabaseTime.Equal(crossed.DatabaseTime), "RecordFocusLoss(duplicate) = %#v, first = %#v", replayed, crossed)
	require.Equal(t, crossed.RetainedEvidenceCount, replayed.RetainedEvidenceCount, "RecordFocusLoss(duplicate) = %#v, first = %#v", replayed, crossed)
	require.True(t, replayed.FlagCreated, "RecordFocusLoss(duplicate) = %#v, first = %#v", replayed, crossed)
	require.True(t, replayed.CandidateWarningCreated, "RecordFocusLoss(duplicate) = %#v, first = %#v", replayed, crossed)
	require.True(t, replayed.ManagerNotificationRequired, "RecordFocusLoss(duplicate) = %#v, first = %#v", replayed, crossed)
	requireSuccessfulAudit(t, ctx, ss, replay.AuditEventID)
	assertFocusLossAuditAllowlist(t, ctx, ss, replay.AuditEventID)
	changed := replay
	changed.DurationMilliseconds++
	changed.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID
	_, err = ss.ExamAttempt().RecordFocusLoss(ctx, &changed)
	requireConflictConstraint(t, err, "focus_loss_sequence")
	stale := replay
	stale.Sequence = 3
	stale.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID
	_, err = ss.ExamAttempt().RecordFocusLoss(ctx, &stale)
	requireConflictConstraint(t, err, "focus_loss_sequence")

	recordFocusLoss(t, ctx, ss, fixture, access, 5, 2000, "")
	recordFocusLoss(t, ctx, ss, fixture, access, 6, 2000, "")
	secondCrossing := recordFocusLoss(t, ctx, ss, fixture, access, 7, 2000, "")
	require.True(t, secondCrossing.ThresholdCrossed)
	require.NotNil(t, secondCrossing.Flag)
	require.Equal(t, crossed.Flag.ID, secondCrossing.Flag.ID)
	require.False(t, secondCrossing.FlagCreated)
	require.False(t, secondCrossing.CandidateWarningCreated)
	require.False(t, secondCrossing.ManagerNotificationRequired)
	require.Equal(t, 6, secondCrossing.RetainedEvidenceCount)
	gap := recordFocusLoss(t, ctx, ss, fixture, access, 9, 1000, "")
	later := recordFocusLoss(t, ctx, ss, fixture, access, 10, 1000, "")
	require.Equal(t, int64(1), gap.MissingBefore, "Focus Loss nonqualifying gap = %#v, later = %#v", gap, later)
	require.False(t, gap.Qualified, "Focus Loss nonqualifying gap = %#v, later = %#v", gap, later)
	require.Empty(t, gap.PolicyOutcome, "Focus Loss nonqualifying gap = %#v, later = %#v", gap, later)
	require.Equal(t, int64(0), later.MissingBefore, "Focus Loss nonqualifying gap = %#v, later = %#v", gap, later)
	require.False(t, later.Qualified, "Focus Loss nonqualifying gap = %#v, later = %#v", gap, later)
	if len(probes) != 0 && probes[0].UnresolvedFocusLossMissing != nil {
		count := probes[0].UnresolvedFocusLossMissing(t, ctx, access.AttemptID, access.Generation)
		require.Equal(t, int64(2), count, "unresolved Focus Loss missing count")
	}
	return newFocusLossInput(t, ctx, ss, fixture, access, 11, 2000, model.FocusLossSourceApplicationBackgrounded)
}

func assertFocusLossAuditAllowlist(t *testing.T, ctx context.Context, ss store.Store, id model.AuditEventID) {
	t.Helper()
	audit, err := ss.Audit().Get(ctx, id)
	require.NoError(t, err)
	var data map[string]any
	err = json.Unmarshal(audit.Result, &data)
	require.NoError(t, err)
	allowed := map[string]struct{}{"exam_id": {}, "exam_sitting_id": {}, "exam_attempt_id": {}, "participation_id": {},
		"generation": {}, "signal_id": {}, "accepted_sequence": {}, "replayed": {}}
	require.Len(t, data, len(allowed))
	for key := range data {
		_, ok := allowed[key]
		require.True(t, ok, "Focus Loss audit exposed non-allowlisted key %q: %#v", key, data)
	}
}

func testExamAttemptFocusLossPolicies(t *testing.T, ctx context.Context, ss store.Store, probe ExamAttemptSQLProbe) {
	t.Helper()
	t.Run("disabled policy is diagnostic only", func(t *testing.T) {
		policy := model.FocusLossPolicy{Enabled: false, MinimumDuration: 500 * time.Millisecond, IncidentCount: 1,
			Window: 10 * time.Second, Outcome: model.IntegrityOutcomeFlagAndSuspend}
		fixture := newExamAttemptFixtureWithFocusLoss(t, ctx, ss, &policy)
		connected, access := connectFocusLossFixture(t, ctx, ss, fixture, "focus-disabled")
		presentation, err := ss.ExamAttempt().GetCandidatePresentation(ctx, store.CandidateAttemptAccess{AttemptID: connected.Attempt.ID,
			CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID,
			DesktopRegistrationID: fixture.session.DesktopRegistrationID, DPoPKeyThumbprint: fixture.session.DPoPKeyThumbprint,
			ConnectionID:             connected.Connection.ID,
			ContinuityCredentialHash: access.ContinuityCredentialHash})
		require.NoError(t, err)
		require.False(t, presentation.RuntimeCapabilities.FocusLossCollectionEnabled, "disabled Focus Loss policy told the candidate to collect signals")
		first := recordFocusLoss(t, ctx, ss, fixture, access, 1, model.FocusLossMaximumDurationMilliseconds, model.FocusLossSourceWindowBlur)
		second := recordFocusLoss(t, ctx, ss, fixture, access, 3, 500, "")
		require.False(t, first.CollectionEnabled, "disabled Focus Loss decisions = %#v, %#v", first, second)
		require.False(t, first.Qualified, "disabled Focus Loss decisions = %#v, %#v", first, second)
		require.False(t, first.ThresholdCrossed, "disabled Focus Loss decisions = %#v, %#v", first, second)
		require.Nil(t, first.Flag, "disabled Focus Loss decisions = %#v, %#v", first, second)
		require.Equal(t, int64(1), first.DiagnosticCount, "disabled Focus Loss decisions = %#v, %#v", first, second)
		require.False(t, second.CollectionEnabled, "disabled Focus Loss decisions = %#v, %#v", first, second)
		require.False(t, second.Qualified, "disabled Focus Loss decisions = %#v, %#v", first, second)
		require.False(t, second.ThresholdCrossed, "disabled Focus Loss decisions = %#v, %#v", first, second)
		require.Nil(t, second.Flag, "disabled Focus Loss decisions = %#v, %#v", first, second)
		require.Equal(t, int64(2), second.DiagnosticCount, "disabled Focus Loss decisions = %#v, %#v", first, second)
		require.Equal(t, int64(1), second.MissingBefore, "disabled Focus Loss decisions = %#v, %#v", first, second)
		require.Empty(t, second.PolicyOutcome, "disabled Focus Loss decisions = %#v, %#v", first, second)
		persisted := probe.FocusLossPersistence(t, ctx, connected.Attempt.ID, connected.Participation.Generation)
		require.Equal(t, 0, persisted.Flags)
		require.Equal(t, 0, persisted.Evidence)
		require.Equal(t, 0, persisted.Pending)
		require.Equal(t, int64(0), persisted.OverflowCount)
		require.Equal(t, int64(2), persisted.DiagnosticCount)
	})

	t.Run("receipt-time window expires old qualifiers", func(t *testing.T) {
		policy := model.FocusLossPolicy{Enabled: true, MinimumDuration: 500 * time.Millisecond, IncidentCount: 2,
			Window: 10 * time.Second, Outcome: model.IntegrityOutcomeFlag}
		fixture := newExamAttemptFixtureWithFocusLoss(t, ctx, ss, &policy)
		connected, access := connectFocusLossFixture(t, ctx, ss, fixture, "focus-window")
		first := recordFocusLoss(t, ctx, ss, fixture, access, 1, 500, "")
		probe.AgeFocusLossPending(t, ctx, connected.Attempt.ID, connected.Participation.Generation, 1, 11*time.Second)
		second := recordFocusLoss(t, ctx, ss, fixture, access, 2, 500, "")
		require.True(t, first.Qualified, "receipt-time rolling window = %#v, %#v", first, second)
		require.Equal(t, 1, first.WindowIncidentCount, "receipt-time rolling window = %#v, %#v", first, second)
		require.False(t, second.ThresholdCrossed, "receipt-time rolling window = %#v, %#v", first, second)
		require.Equal(t, 1, second.WindowIncidentCount, "receipt-time rolling window = %#v, %#v", first, second)
		require.Empty(t, second.PolicyOutcome, "receipt-time rolling window = %#v, %#v", first, second)
		require.False(t, second.DatabaseTime.Before(first.DatabaseTime), "receipt-time rolling window = %#v, %#v", first, second)
	})

	t.Run("flag evidence is capped and overflow is bounded", func(t *testing.T) {
		policy := model.FocusLossPolicy{Enabled: true, MinimumDuration: 500 * time.Millisecond, IncidentCount: 1,
			Window: 10 * time.Second, Outcome: model.IntegrityOutcomeFlag}
		fixture := newExamAttemptFixtureWithFocusLoss(t, ctx, ss, &policy)
		connected, access := connectFocusLossFixture(t, ctx, ss, fixture, "focus-overflow")
		var firstFlag model.IntegrityFlagID
		for sequence := int64(1); sequence <= model.FocusLossMaximumEvidenceEpisodes+1; sequence++ {
			result := recordFocusLoss(t, ctx, ss, fixture, access, sequence, 500+sequence, "")
			require.True(t, result.ThresholdCrossed, "Focus Loss crossing %d = %#v", sequence, result)
			require.Equal(t, 0, result.WindowIncidentCount, "Focus Loss crossing %d = %#v", sequence, result)
			require.Equal(t, model.IntegrityOutcomeFlag, result.PolicyOutcome, "Focus Loss crossing %d = %#v", sequence, result)
			require.NotNil(t, result.Flag, "Focus Loss crossing %d = %#v", sequence, result)
			require.False(t, result.CandidateWarningCreated, "Focus Loss crossing %d = %#v", sequence, result)
			require.Nil(t, result.Suspension, "Focus Loss crossing %d = %#v", sequence, result)
			if sequence == 1 {
				firstFlag = result.Flag.ID
				require.True(t, result.FlagCreated)
				require.True(t, result.ManagerNotificationRequired)
			} else {
				require.Equal(t, firstFlag, result.Flag.ID, "reused Focus Loss Flag %d", sequence)
				require.False(t, result.FlagCreated, "reused Focus Loss Flag %d", sequence)
				require.False(t, result.ManagerNotificationRequired, "reused Focus Loss Flag %d", sequence)
			}
			if sequence == model.FocusLossMaximumEvidenceEpisodes+1 {
				require.Equal(t, model.FocusLossMaximumEvidenceEpisodes, result.RetainedEvidenceCount)
				require.NotNil(t, result.Overflow)
				require.Equal(t, int64(1), result.Overflow.Count)
				require.Equal(t, 500+sequence, result.Overflow.MaximumDurationMilliseconds)
				require.True(t, result.Overflow.FirstReceivedAt.Equal(result.DatabaseTime))
				require.True(t, result.Overflow.LastReceivedAt.Equal(result.DatabaseTime))
			}
		}
		persisted := probe.FocusLossPersistence(t, ctx, connected.Attempt.ID, connected.Participation.Generation)
		require.Equal(t, 1, persisted.Flags)
		require.Equal(t, model.FocusLossMaximumEvidenceEpisodes, persisted.Evidence)
		require.Equal(t, 0, persisted.Pending)
		require.Equal(t, int64(1), persisted.OverflowCount)
	})

	t.Run("suspension is atomic replayable and causal reallow resets", func(t *testing.T) {
		policy := model.FocusLossPolicy{Enabled: true, MinimumDuration: 500 * time.Millisecond, IncidentCount: 2,
			Window: 10 * time.Second, Outcome: model.IntegrityOutcomeFlagAndSuspend}
		fixture := newExamAttemptFixtureWithFocusLoss(t, ctx, ss, &policy)
		connected, access := connectFocusLossFixture(t, ctx, ss, fixture, "focus-suspend")
		recordFocusLoss(t, ctx, ss, fixture, access, 1, 500, model.FocusLossSourceDocumentHidden)
		crossingInput := newFocusLossInput(t, ctx, ss, fixture, access, 2, 500, model.FocusLossSourceFullscreenExited)
		crossed, err := ss.ExamAttempt().RecordFocusLoss(ctx, crossingInput)
		require.NoError(t, err)
		require.True(t, crossed.ThresholdCrossed)
		require.Equal(t, model.IntegrityOutcomeFlagAndSuspend, crossed.PolicyOutcome)
		require.NotNil(t, crossed.Flag)
		require.NotNil(t, crossed.Attempt)
		require.Equal(t, model.ExamAttemptSuspended, crossed.Attempt.State)
		require.Equal(t, int64(2), crossed.Attempt.Revision)
		require.NotNil(t, crossed.Participation)
		require.Equal(t, model.AttemptParticipationEnded, crossed.Participation.State)
		require.Equal(t, model.AttemptParticipationEndPolicySuspended, crossed.Participation.EndReason)
		require.NotNil(t, crossed.Connection)
		require.Equal(t, model.AttemptConnectionClosed, crossed.Connection.State)
		require.Equal(t, model.AttemptConnectionClosePolicySuspended, crossed.Connection.CloseReason)
		require.True(t, crossed.ConnectionClosed)
		require.NotNil(t, crossed.Suspension)
		require.Equal(t, crossingInput.SuspensionID, crossed.Suspension.ID)
		require.Equal(t, model.AttemptSuspensionCandidateReasonFocusLossPolicy, crossed.Suspension.CandidateReason)
		resolved, err := ss.ExamAttempt().ResolveFocusLossTarget(ctx, access)
		require.NoError(t, err)
		require.Equal(t, connected.Attempt.ID, resolved.AttemptID)
		require.Equal(t, connected.Participation.Generation, resolved.Generation)
		replay := *crossingInput
		replay.SignalID, replay.EvidenceID, replay.FlagID, replay.SuspensionID = model.NewFocusLossSignalID(), model.NewIntegrityEvidenceID(), model.NewIntegrityFlagID(), model.NewAttemptSuspensionID()
		replay.AuditEventID, replay.AuditAt = saveExamAttemptAudit(t, ctx, ss, fixture).ID, model.NowUTC()
		replayed, err := ss.ExamAttempt().RecordFocusLoss(ctx, &replay)
		require.NoError(t, err)
		require.True(t, replayed.Duplicate, "Focus Loss self-suspended replay = %#v, first = %#v", replayed, crossed)
		require.Equal(t, crossed.Signal.ID, replayed.Signal.ID, "Focus Loss self-suspended replay = %#v, first = %#v", replayed, crossed)
		require.NotNil(t, replayed.Suspension, "Focus Loss self-suspended replay = %#v, first = %#v", replayed, crossed)
		require.Equal(t, crossed.Suspension.ID, replayed.Suspension.ID, "Focus Loss self-suspended replay = %#v, first = %#v", replayed, crossed)
		require.NotNil(t, replayed.Attempt, "Focus Loss self-suspended replay = %#v, first = %#v", replayed, crossed)
		require.Equal(t, crossed.Attempt.Revision, replayed.Attempt.Revision, "Focus Loss self-suspended replay = %#v, first = %#v", replayed, crossed)
		require.True(t, replayed.DatabaseTime.Equal(crossed.DatabaseTime), "Focus Loss self-suspended replay = %#v, first = %#v", replayed, crossed)
		changed := replay
		changed.DurationMilliseconds++
		changed.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID
		_, err = ss.ExamAttempt().RecordFocusLoss(ctx, &changed)
		requireConflictConstraint(t, err, "focus_loss_sequence")
		later := replay
		later.Sequence++
		later.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID
		_, err = ss.ExamAttempt().RecordFocusLoss(ctx, &later)
		requireConflictConstraint(t, err, "exam_attempt_state")

		reallow := &store.ExamAttemptReallow{ExamID: fixture.examID, SittingID: fixture.sitting.ID, AttemptID: connected.Attempt.ID,
			SuspensionID: crossed.Suspension.ID, ActorUserID: fixture.manager.ID, ExpectedAttemptRevision: crossed.Attempt.Revision,
			PrivateReason: "reviewed the focus policy episode",
			AuditEventID:  saveExamAttemptReallowAudit(t, ctx, ss, fixture).ID, AuditAt: model.NowUTC()}
		reallowed, err := ss.ExamAttempt().ReallowAttempt(ctx, reallow,
			examCommand(fixture.manager.ID, store.ExamAttemptReallowOperation, "focus-reallow", "focus-reallow"))
		require.NoError(t, err)
		require.True(t, reallowed.FocusLossWindowReset)
		require.Equal(t, model.ExamAttemptReady, reallowed.Attempt.State)
		require.Equal(t, int64(3), reallowed.Attempt.Revision)
		persisted := probe.FocusLossPersistence(t, ctx, connected.Attempt.ID, connected.Participation.Generation)
		require.Equal(t, 0, persisted.Pending)
		require.Equal(t, 1, persisted.Flags)
		require.Equal(t, 2, persisted.Evidence)
	})

	t.Run("established monitoring preserves access vocabulary", func(t *testing.T) {
		policy := model.FocusLossPolicy{Enabled: true, MinimumDuration: 500 * time.Millisecond, IncidentCount: 3,
			Window: 10 * time.Second, Outcome: model.IntegrityOutcomeFlag}
		fixture := newExamAttemptFixtureWithFocusLoss(t, ctx, ss, &policy)
		connected, access := connectFocusLossFixture(t, ctx, ss, fixture, "focus-access")

		wrongCredential := newFocusLossInput(t, ctx, ss, fixture, access, 1, 500, "")
		wrongCredential.Access.ContinuityCredentialHash = model.HashToken(model.NewCredentialToken())
		_, err := ss.ExamAttempt().RecordFocusLoss(ctx, wrongCredential)
		requireConflictConstraint(t, err, "attempt_participation_credential")
		wrongGeneration := newFocusLossInput(t, ctx, ss, fixture, access, 1, 500, "")
		wrongGeneration.Access.Generation++
		_, err = ss.ExamAttempt().RecordFocusLoss(ctx, wrongGeneration)
		requireConflictConstraint(t, err, "attempt_participation_generation")
		wrongSession := newFocusLossInput(t, ctx, ss, fixture, access, 1, 500, "")
		wrongSession.Access.SessionID = model.NewSessionID()
		_, err = ss.ExamAttempt().RecordFocusLoss(ctx, wrongSession)
		require.True(t, store.IsNotFound(err), "RecordFocusLoss(foreign Session) error")

		endedAt := model.NowUTC()
		_, err = ss.ClassMember().SeedEnd(ctx, fixture.membership.ID, fixture.membership.Revision, endedAt)
		require.NoError(t, err)
		time.Sleep(100 * time.Millisecond)
		accepted := recordFocusLoss(t, ctx, ss, fixture, access, 1, 500, "")
		require.True(t, accepted.Qualified)
		require.Equal(t, 1, accepted.WindowIncidentCount)
		closed, err := ss.ExamAttempt().CloseConnection(ctx, &store.ExamAttemptConnectionClose{ConnectionID: connected.Connection.ID,
			CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID, Reason: model.AttemptConnectionCloseTransport,
			AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.NowUTC()})
		require.NoError(t, err)
		require.True(t, closed.Changed)
		closedInput := newFocusLossInput(t, ctx, ss, fixture, access, 2, 500, "")
		_, err = ss.ExamAttempt().RecordFocusLoss(ctx, closedInput)
		requireConflictConstraint(t, err, "attempt_connection_closed")

		closeAt := model.NowUTC()
		_, err = ss.ExamSitting().EarlyClose(ctx, &store.ExamSittingManagerTransition{ExamID: fixture.examID,
			SittingID: fixture.sitting.ID, ActorUserID: fixture.manager.ID, ExpectedRevision: fixture.sitting.Revision,
			FinalizeJob:   newExamSittingFinalizeJob(t, fixture.sitting.ID, fixture.sitting.Revision+1, closeAt),
			PrivateReason: "verify terminal Focus Loss fence", ChangedAt: closeAt,
			AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.manager.ID, fixture.examID, fixture.unitID).ID, AuditAt: model.NowUTC()},
			examCommand(fixture.manager.ID, "exam.sitting.close", "focus-close", "focus-close"))
		require.NoError(t, err)
		terminalInput := newFocusLossInput(t, ctx, ss, fixture, access, 2, 500, "")
		_, err = ss.ExamAttempt().RecordFocusLoss(ctx, terminalInput)
		requireConflictConstraint(t, err, "exam_sitting_state")
	})

	t.Run("expiry serializes ahead of Focus Loss", func(t *testing.T) {
		policy := model.FocusLossPolicy{Enabled: true, MinimumDuration: 500 * time.Millisecond, IncidentCount: 2,
			Window: 10 * time.Second, Outcome: model.IntegrityOutcomeFlag}
		fixture := newExamAttemptFixtureWithFocusLoss(t, ctx, ss, &policy)
		connected, access := connectFocusLossFixture(t, ctx, ss, fixture, "focus-expiry-race")
		input := newFocusLossInput(t, ctx, ss, fixture, access, 1, 500, "")
		err := probe.FenceRenewalPastDeadline(t, ctx, connected.Participation.ID, func() error {
			_, recordErr := ss.ExamAttempt().RecordFocusLoss(ctx, input)
			return recordErr
		})
		requireConflictConstraint(t, err, "attempt_participation_expired")
		require.Equal(t, FocusLossPersistenceProbe{}, probe.FocusLossPersistence(t, ctx, connected.Attempt.ID, connected.Participation.Generation))
	})

	t.Run("connection-loss reallow preserves unrelated Focus bucket", func(t *testing.T) {
		policy := model.FocusLossPolicy{Enabled: true, MinimumDuration: 500 * time.Millisecond, IncidentCount: 3,
			Window: 10 * time.Second, Outcome: model.IntegrityOutcomeFlag}
		fixture := newExamAttemptFixtureWithFocusLoss(t, ctx, ss, &policy)
		connected, access := connectFocusLossFixture(t, ctx, ss, fixture, "focus-connection-loss")
		recordFocusLoss(t, ctx, ss, fixture, access, 1, 500, "")
		probe.SetParticipationLeaseExpired(t, ctx, connected.Participation.ID)
		expired, err := ss.ExamAttempt().ExpireParticipation(ctx, &store.ExamAttemptParticipationExpiry{AttemptID: connected.Attempt.ID,
			ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
			EvidenceID: model.NewIntegrityEvidenceID(), FlagID: model.NewIntegrityFlagID(), SuspensionID: model.NewAttemptSuspensionID(),
			AuditEventID: saveExamAttemptSystemAudit(t, ctx, ss, fixture).ID, AuditAt: model.NowUTC()})
		require.NoError(t, err)
		reallow := &store.ExamAttemptReallow{ExamID: fixture.examID, SittingID: fixture.sitting.ID, AttemptID: connected.Attempt.ID,
			SuspensionID: expired.Suspension.ID, ActorUserID: fixture.manager.ID, ExpectedAttemptRevision: expired.Attempt.Revision,
			PrivateReason: "connectivity was restored",
			AuditEventID:  saveExamAttemptReallowAudit(t, ctx, ss, fixture).ID, AuditAt: model.NowUTC()}
		reallowed, err := ss.ExamAttempt().ReallowAttempt(ctx, reallow,
			examCommand(fixture.manager.ID, store.ExamAttemptReallowOperation, "connection-loss-reallow-focus", "connection-loss-reallow-focus"))
		require.NoError(t, err)
		require.False(t, reallowed.FocusLossWindowReset)
		require.Equal(t, 1, probe.FocusLossPersistence(t, ctx, connected.Attempt.ID, connected.Participation.Generation).Pending)
	})

	t.Run("audit rollback leaves no accepted sequence", func(t *testing.T) {
		policy := model.FocusLossPolicy{Enabled: true, MinimumDuration: 500 * time.Millisecond, IncidentCount: 2,
			Window: 10 * time.Second, Outcome: model.IntegrityOutcomeFlag}
		fixture := newExamAttemptFixtureWithFocusLoss(t, ctx, ss, &policy)
		connected, access := connectFocusLossFixture(t, ctx, ss, fixture, "focus-audit-rollback")
		input := newFocusLossInput(t, ctx, ss, fixture, access, 1, 500, model.FocusLossSourceWindowBlur)
		input.AuditEventID = model.NewAuditEventID()
		_, err := ss.ExamAttempt().RecordFocusLoss(ctx, input)
		require.Error(t, err, "RecordFocusLoss with missing audit unexpectedly succeeded")
		require.Equal(t, FocusLossPersistenceProbe{}, probe.FocusLossPersistence(t, ctx, connected.Attempt.ID, connected.Participation.Generation))
		input.AuditEventID, input.AuditAt = saveExamAttemptAudit(t, ctx, ss, fixture).ID, model.NowUTC()
		accepted, err := ss.ExamAttempt().RecordFocusLoss(ctx, input)
		require.NoError(t, err)
		require.False(t, accepted.Duplicate)
		require.Equal(t, int64(1), accepted.AcceptedSequence)
	})

	t.Run("two stores converge the same sequence", func(t *testing.T) {
		if probe.ConcurrentExamAttempt == nil {
			t.Skip("no independent SQL Store supplied")
		}
		policy := model.FocusLossPolicy{Enabled: true, MinimumDuration: 500 * time.Millisecond, IncidentCount: 2,
			Window: 10 * time.Second, Outcome: model.IntegrityOutcomeFlag}
		fixture := newExamAttemptFixtureWithFocusLoss(t, ctx, ss, &policy)
		connected, access := connectFocusLossFixture(t, ctx, ss, fixture, "focus-concurrent")
		recordFocusLoss(t, ctx, ss, fixture, access, 1, 500, "")
		inputs := [2]*store.ExamAttemptFocusLossSignal{
			newFocusLossInput(t, ctx, ss, fixture, access, 2, 500, model.FocusLossSourceWindowBlur),
			newFocusLossInput(t, ctx, ss, fixture, access, 2, 500, model.FocusLossSourceWindowBlur),
		}
		stores := [2]store.ExamAttemptStore{ss.ExamAttempt(), probe.ConcurrentExamAttempt}
		results := make(chan *store.ExamAttemptFocusLossResult, 2)
		errorsFound := make(chan error, 2)
		start := make(chan struct{})
		var wait sync.WaitGroup
		for index := range inputs {
			wait.Add(1)
			go func(index int) {
				defer wait.Done()
				<-start
				result, err := stores[index].RecordFocusLoss(ctx, inputs[index])
				results <- result
				errorsFound <- err
			}(index)
		}
		close(start)
		wait.Wait()
		first, second := <-results, <-results
		require.NoError(t, <-errorsFound)
		require.NoError(t, <-errorsFound)
		requireSuccessfulAudit(t, ctx, ss, inputs[0].AuditEventID)
		requireSuccessfulAudit(t, ctx, ss, inputs[1].AuditEventID)
		assertFocusLossAuditAllowlist(t, ctx, ss, inputs[0].AuditEventID)
		assertFocusLossAuditAllowlist(t, ctx, ss, inputs[1].AuditEventID)
		require.NotEqual(t, second.Duplicate, first.Duplicate, "concurrent Focus Loss convergence = %#v, %#v", first, second)
		require.Equal(t, second.Signal.ID, first.Signal.ID, "concurrent Focus Loss convergence = %#v, %#v", first, second)
		require.NotNil(t, first.Flag, "concurrent Focus Loss convergence = %#v, %#v", first, second)
		require.NotNil(t, second.Flag, "concurrent Focus Loss convergence = %#v, %#v", first, second)
		require.Equal(t, second.Flag.ID, first.Flag.ID, "concurrent Focus Loss convergence = %#v, %#v", first, second)
		require.True(t, first.DatabaseTime.Equal(second.DatabaseTime), "concurrent Focus Loss convergence = %#v, %#v", first, second)
		persisted := probe.FocusLossPersistence(t, ctx, connected.Attempt.ID, connected.Participation.Generation)
		require.Equal(t, 1, persisted.Flags)
		require.Equal(t, 2, persisted.Evidence)
		require.Equal(t, 0, persisted.Pending)
	})

	if probe.AssertFocusLossSchema != nil {
		t.Run("schema invariants", func(t *testing.T) { probe.AssertFocusLossSchema(t, ctx) })
	}
}

func connectFocusLossFixture(t *testing.T, ctx context.Context, ss store.Store, fixture examAttemptFixture, key string) (*store.ExamAttemptConnectResult, store.ExamAttemptFocusLossAccess) {
	t.Helper()
	credentialHash := model.HashToken(model.NewCredentialToken())
	input := &store.ExamAttemptConnect{SittingID: fixture.sitting.ID, CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID,
		DesktopRegistrationID: fixture.session.DesktopRegistrationID, DPoPKeyThumbprint: fixture.session.DPoPKeyThumbprint,
		AttemptID: model.NewExamAttemptID(), WorkspaceID: model.NewExamAttemptWorkspaceID(), ParticipationID: model.NewAttemptParticipationID(),
		ConnectionID: model.NewAttemptConnectionID(), ContinuityCredentialHash: credentialHash,
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.NowUTC()}
	prepareExamAttemptConnect(t, ctx, ss, input)
	connected, err := ss.ExamAttempt().Connect(ctx, input, examCommand(fixture.candidate.ID, store.ExamAttemptConnectOperation, key, key))
	require.NoError(t, err)
	return connected, store.ExamAttemptFocusLossAccess{AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID,
		Generation: connected.Participation.Generation, CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID,
		ConnectionID: connected.Connection.ID, ContinuityCredentialHash: credentialHash}
}

func recordFocusLoss(t *testing.T, ctx context.Context, ss store.Store, fixture examAttemptFixture,
	access store.ExamAttemptFocusLossAccess, sequence, duration int64, source model.FocusLossSource,
) *store.ExamAttemptFocusLossResult {
	t.Helper()
	input := newFocusLossInput(t, ctx, ss, fixture, access, sequence, duration, source)
	result, err := ss.ExamAttempt().RecordFocusLoss(ctx, input)
	require.NoError(t, err)
	return result
}

func newFocusLossInput(t *testing.T, ctx context.Context, ss store.Store, fixture examAttemptFixture,
	access store.ExamAttemptFocusLossAccess, sequence, duration int64, source model.FocusLossSource,
) *store.ExamAttemptFocusLossSignal {
	t.Helper()
	return &store.ExamAttemptFocusLossSignal{Access: access, SignalID: model.NewFocusLossSignalID(), EvidenceID: model.NewIntegrityEvidenceID(), FlagID: model.NewIntegrityFlagID(),
		SuspensionID: model.NewAttemptSuspensionID(), Sequence: sequence, DurationMilliseconds: duration, Source: source,
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.NowUTC()}
}
