// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

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
	requireNoError(t, err)
	if target.ExamID != fixture.examID || target.SittingID != fixture.sitting.ID || target.ClassID != fixture.class.ID ||
		target.CandidateUserID != fixture.candidate.ID || target.AttemptID != connected.Attempt.ID ||
		target.ParticipationID != connected.Participation.ID || target.Generation != connected.Participation.Generation {
		t.Fatalf("ResolveFocusLossTarget() = %#v", target)
	}

	first := recordFocusLoss(t, ctx, ss, fixture, access, 1, 2000, model.FocusLossSourceWindowBlur)
	second := recordFocusLoss(t, ctx, ss, fixture, access, 2, 2000, model.FocusLossSourceDocumentHidden)
	if !first.Qualified || first.ThresholdCrossed || first.WindowIncidentCount != 1 ||
		!second.Qualified || second.ThresholdCrossed || second.WindowIncidentCount != 2 {
		t.Fatalf("Focus Loss rolling bucket = %#v, %#v", first, second)
	}
	crossingInput := newFocusLossInput(t, ctx, ss, fixture, access, 4, 2000, model.FocusLossSourceFullscreenExited)
	crossed, err := ss.ExamAttempt().RecordFocusLoss(ctx, crossingInput)
	requireNoError(t, err)
	if crossed.Duplicate || !crossed.CollectionEnabled || !crossed.Qualified || crossed.MissingBefore != 1 ||
		!crossed.ThresholdCrossed || crossed.WindowIncidentCount != 0 || crossed.PolicyOutcome != model.IntegrityOutcomeFlagAndWarn ||
		crossed.RetainedEvidenceCount != 3 || crossed.Flag == nil || !crossed.FlagCreated || !crossed.CandidateWarningCreated ||
		!crossed.ManagerNotificationRequired || crossed.Attempt != nil || crossed.Suspension != nil {
		t.Fatalf("RecordFocusLoss(threshold) = %#v", crossed)
	}
	requireSuccessfulAudit(t, ctx, ss, crossingInput.AuditEventID)
	assertFocusLossAuditAllowlist(t, ctx, ss, crossingInput.AuditEventID)
	audit, err := ss.Audit().Get(ctx, crossingInput.AuditEventID)
	requireNoError(t, err)
	for _, forbidden := range []string{credentialHash, string(crossingInput.Source), "2000"} {
		if bytes.Contains(audit.Result, []byte(forbidden)) {
			t.Fatalf("Focus Loss audit exposed %q", forbidden)
		}
	}

	replay := *crossingInput
	replay.SignalID, replay.EvidenceID, replay.FlagID, replay.SuspensionID = model.NewFocusLossSignalID(), model.NewIntegrityEvidenceID(), model.NewIntegrityFlagID(), model.NewAttemptSuspensionID()
	replay.AuditEventID, replay.AuditAt = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), model.GetMillis()
	replayed, err := ss.ExamAttempt().RecordFocusLoss(ctx, &replay)
	requireNoError(t, err)
	if !replayed.Duplicate || replayed.Signal.ID != crossed.Signal.ID || !replayed.DatabaseTime.Equal(crossed.DatabaseTime) ||
		replayed.RetainedEvidenceCount != crossed.RetainedEvidenceCount || !replayed.FlagCreated || !replayed.CandidateWarningCreated ||
		!replayed.ManagerNotificationRequired {
		t.Fatalf("RecordFocusLoss(duplicate) = %#v, first = %#v", replayed, crossed)
	}
	requireSuccessfulAudit(t, ctx, ss, replay.AuditEventID)
	assertFocusLossAuditAllowlist(t, ctx, ss, replay.AuditEventID)
	changed := replay
	changed.DurationMilliseconds++
	changed.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String()
	_, err = ss.ExamAttempt().RecordFocusLoss(ctx, &changed)
	assertExamAttemptConflict(t, err, "focus_loss_sequence")
	stale := replay
	stale.Sequence = 3
	stale.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String()
	_, err = ss.ExamAttempt().RecordFocusLoss(ctx, &stale)
	assertExamAttemptConflict(t, err, "focus_loss_sequence")

	recordFocusLoss(t, ctx, ss, fixture, access, 5, 2000, "")
	recordFocusLoss(t, ctx, ss, fixture, access, 6, 2000, "")
	secondCrossing := recordFocusLoss(t, ctx, ss, fixture, access, 7, 2000, "")
	if !secondCrossing.ThresholdCrossed || secondCrossing.Flag == nil || secondCrossing.Flag.ID != crossed.Flag.ID ||
		secondCrossing.FlagCreated || secondCrossing.CandidateWarningCreated || secondCrossing.ManagerNotificationRequired ||
		secondCrossing.RetainedEvidenceCount != 6 {
		t.Fatalf("RecordFocusLoss(second warning crossing) = %#v", secondCrossing)
	}
	gap := recordFocusLoss(t, ctx, ss, fixture, access, 9, 1000, "")
	later := recordFocusLoss(t, ctx, ss, fixture, access, 10, 1000, "")
	if gap.MissingBefore != 1 || gap.Qualified || gap.PolicyOutcome != "" || later.MissingBefore != 0 || later.Qualified {
		t.Fatalf("Focus Loss nonqualifying gap = %#v, later = %#v", gap, later)
	}
	if len(probes) != 0 && probes[0].UnresolvedFocusLossMissing != nil {
		if count := probes[0].UnresolvedFocusLossMissing(t, ctx, access.AttemptID, access.Generation); count != 2 {
			t.Fatalf("unresolved Focus Loss missing count = %d, want 2", count)
		}
	}
	return newFocusLossInput(t, ctx, ss, fixture, access, 11, 2000, model.FocusLossSourceApplicationBackgrounded)
}

func assertFocusLossAuditAllowlist(t *testing.T, ctx context.Context, ss store.Store, id string) {
	t.Helper()
	audit, err := ss.Audit().Get(ctx, id)
	requireNoError(t, err)
	var data map[string]any
	if err = json.Unmarshal(audit.Result, &data); err != nil {
		t.Fatalf("decode Focus Loss audit: %v", err)
	}
	allowed := map[string]struct{}{"exam_id": {}, "exam_sitting_id": {}, "exam_attempt_id": {}, "participation_id": {},
		"generation": {}, "signal_id": {}, "accepted_sequence": {}, "replayed": {}}
	if len(data) != len(allowed) {
		t.Fatalf("Focus Loss audit keys = %#v", data)
	}
	for key := range data {
		if _, ok := allowed[key]; !ok {
			t.Fatalf("Focus Loss audit exposed non-allowlisted key %q: %#v", key, data)
		}
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
		requireNoError(t, err)
		if presentation.RuntimeCapabilities.FocusLossCollectionEnabled {
			t.Fatal("disabled Focus Loss policy told the candidate to collect signals")
		}
		first := recordFocusLoss(t, ctx, ss, fixture, access, 1, model.FocusLossMaximumDurationMilliseconds, model.FocusLossSourceWindowBlur)
		second := recordFocusLoss(t, ctx, ss, fixture, access, 3, 500, "")
		if first.CollectionEnabled || first.Qualified || first.ThresholdCrossed || first.Flag != nil || first.DiagnosticCount != 1 ||
			second.CollectionEnabled || second.Qualified || second.ThresholdCrossed || second.Flag != nil || second.DiagnosticCount != 2 ||
			second.MissingBefore != 1 || second.PolicyOutcome != "" {
			t.Fatalf("disabled Focus Loss decisions = %#v, %#v", first, second)
		}
		persisted := probe.FocusLossPersistence(t, ctx, connected.Attempt.ID, connected.Participation.Generation)
		if persisted.Flags != 0 || persisted.Evidence != 0 || persisted.Pending != 0 || persisted.OverflowCount != 0 || persisted.DiagnosticCount != 2 {
			t.Fatalf("disabled Focus Loss persistence = %#v", persisted)
		}
	})

	t.Run("receipt-time window expires old qualifiers", func(t *testing.T) {
		policy := model.FocusLossPolicy{Enabled: true, MinimumDuration: 500 * time.Millisecond, IncidentCount: 2,
			Window: 10 * time.Second, Outcome: model.IntegrityOutcomeFlag}
		fixture := newExamAttemptFixtureWithFocusLoss(t, ctx, ss, &policy)
		connected, access := connectFocusLossFixture(t, ctx, ss, fixture, "focus-window")
		first := recordFocusLoss(t, ctx, ss, fixture, access, 1, 500, "")
		probe.AgeFocusLossPending(t, ctx, connected.Attempt.ID, connected.Participation.Generation, 1, 11*time.Second)
		second := recordFocusLoss(t, ctx, ss, fixture, access, 2, 500, "")
		if !first.Qualified || first.WindowIncidentCount != 1 || second.ThresholdCrossed || second.WindowIncidentCount != 1 ||
			second.PolicyOutcome != "" || second.DatabaseTime.Before(first.DatabaseTime) {
			t.Fatalf("receipt-time rolling window = %#v, %#v", first, second)
		}
	})

	t.Run("flag evidence is capped and overflow is bounded", func(t *testing.T) {
		policy := model.FocusLossPolicy{Enabled: true, MinimumDuration: 500 * time.Millisecond, IncidentCount: 1,
			Window: 10 * time.Second, Outcome: model.IntegrityOutcomeFlag}
		fixture := newExamAttemptFixtureWithFocusLoss(t, ctx, ss, &policy)
		connected, access := connectFocusLossFixture(t, ctx, ss, fixture, "focus-overflow")
		var firstFlag model.IntegrityFlagID
		for sequence := int64(1); sequence <= model.FocusLossMaximumEvidenceEpisodes+1; sequence++ {
			result := recordFocusLoss(t, ctx, ss, fixture, access, sequence, 500+sequence, "")
			if !result.ThresholdCrossed || result.WindowIncidentCount != 0 || result.PolicyOutcome != model.IntegrityOutcomeFlag ||
				result.Flag == nil || result.CandidateWarningCreated || result.Suspension != nil {
				t.Fatalf("Focus Loss crossing %d = %#v", sequence, result)
			}
			if sequence == 1 {
				firstFlag = result.Flag.ID
				if !result.FlagCreated || !result.ManagerNotificationRequired {
					t.Fatalf("first Focus Loss Flag = %#v", result)
				}
			} else if result.Flag.ID != firstFlag || result.FlagCreated || result.ManagerNotificationRequired {
				t.Fatalf("reused Focus Loss Flag %d = %#v", sequence, result)
			}
			if sequence == model.FocusLossMaximumEvidenceEpisodes+1 {
				if result.RetainedEvidenceCount != model.FocusLossMaximumEvidenceEpisodes || result.Overflow == nil ||
					result.Overflow.Count != 1 || result.Overflow.MaximumDurationMilliseconds != 500+sequence ||
					!result.Overflow.FirstReceivedAt.Equal(result.DatabaseTime) || !result.Overflow.LastReceivedAt.Equal(result.DatabaseTime) {
					t.Fatalf("Focus Loss overflow result = %#v", result)
				}
			}
		}
		persisted := probe.FocusLossPersistence(t, ctx, connected.Attempt.ID, connected.Participation.Generation)
		if persisted.Flags != 1 || persisted.Evidence != model.FocusLossMaximumEvidenceEpisodes || persisted.Pending != 0 || persisted.OverflowCount != 1 {
			t.Fatalf("Focus Loss evidence persistence = %#v", persisted)
		}
	})

	t.Run("suspension is atomic replayable and causal reallow resets", func(t *testing.T) {
		policy := model.FocusLossPolicy{Enabled: true, MinimumDuration: 500 * time.Millisecond, IncidentCount: 2,
			Window: 10 * time.Second, Outcome: model.IntegrityOutcomeFlagAndSuspend}
		fixture := newExamAttemptFixtureWithFocusLoss(t, ctx, ss, &policy)
		connected, access := connectFocusLossFixture(t, ctx, ss, fixture, "focus-suspend")
		recordFocusLoss(t, ctx, ss, fixture, access, 1, 500, model.FocusLossSourceDocumentHidden)
		crossingInput := newFocusLossInput(t, ctx, ss, fixture, access, 2, 500, model.FocusLossSourceFullscreenExited)
		crossed, err := ss.ExamAttempt().RecordFocusLoss(ctx, crossingInput)
		requireNoError(t, err)
		if !crossed.ThresholdCrossed || crossed.PolicyOutcome != model.IntegrityOutcomeFlagAndSuspend || crossed.Flag == nil ||
			crossed.Attempt == nil || crossed.Attempt.State != model.ExamAttemptSuspended || crossed.Attempt.Revision != 2 ||
			crossed.Participation == nil || crossed.Participation.State != model.AttemptParticipationEnded ||
			crossed.Participation.EndReason != model.AttemptParticipationEndPolicySuspended || crossed.Connection == nil ||
			crossed.Connection.State != model.AttemptConnectionClosed || crossed.Connection.CloseReason != model.AttemptConnectionClosePolicySuspended ||
			!crossed.ConnectionClosed || crossed.Suspension == nil || crossed.Suspension.ID != crossingInput.SuspensionID ||
			crossed.Suspension.CandidateReason != model.AttemptSuspensionCandidateReasonFocusLossPolicy {
			t.Fatalf("Focus Loss suspension = %#v", crossed)
		}
		resolved, err := ss.ExamAttempt().ResolveFocusLossTarget(ctx, access)
		requireNoError(t, err)
		if resolved.AttemptID != connected.Attempt.ID || resolved.Generation != connected.Participation.Generation {
			t.Fatalf("ResolveFocusLossTarget(self-suspended) = %#v", resolved)
		}
		replay := *crossingInput
		replay.SignalID, replay.EvidenceID, replay.FlagID, replay.SuspensionID = model.NewFocusLossSignalID(), model.NewIntegrityEvidenceID(), model.NewIntegrityFlagID(), model.NewAttemptSuspensionID()
		replay.AuditEventID, replay.AuditAt = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), model.GetMillis()
		replayed, err := ss.ExamAttempt().RecordFocusLoss(ctx, &replay)
		requireNoError(t, err)
		if !replayed.Duplicate || replayed.Signal.ID != crossed.Signal.ID || replayed.Suspension == nil ||
			replayed.Suspension.ID != crossed.Suspension.ID || replayed.Attempt == nil || replayed.Attempt.Revision != crossed.Attempt.Revision ||
			!replayed.DatabaseTime.Equal(crossed.DatabaseTime) {
			t.Fatalf("Focus Loss self-suspended replay = %#v, first = %#v", replayed, crossed)
		}
		changed := replay
		changed.DurationMilliseconds++
		changed.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String()
		_, err = ss.ExamAttempt().RecordFocusLoss(ctx, &changed)
		assertExamAttemptConflict(t, err, "focus_loss_sequence")
		later := replay
		later.Sequence++
		later.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String()
		_, err = ss.ExamAttempt().RecordFocusLoss(ctx, &later)
		assertExamAttemptConflict(t, err, "exam_attempt_state")

		reallow := &store.ExamAttemptReallow{ExamID: fixture.examID, SittingID: fixture.sitting.ID, AttemptID: connected.Attempt.ID,
			SuspensionID: crossed.Suspension.ID, ActorUserID: fixture.manager.ID, ExpectedAttemptRevision: crossed.Attempt.Revision,
			PrivateReason: "reviewed the focus policy episode", ChangedAt: model.NowUTC(),
			AuditEventID: saveExamAttemptReallowAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
		reallowed, err := ss.ExamAttempt().ReallowAttempt(ctx, reallow,
			examCommand(fixture.manager.ID, store.ExamAttemptReallowOperation, "focus-reallow", "focus-reallow"))
		requireNoError(t, err)
		if !reallowed.FocusLossWindowReset || reallowed.Attempt.State != model.ExamAttemptReady || reallowed.Attempt.Revision != 3 {
			t.Fatalf("ReallowAttempt(Focus Loss) = %#v", reallowed)
		}
		persisted := probe.FocusLossPersistence(t, ctx, connected.Attempt.ID, connected.Participation.Generation)
		if persisted.Pending != 0 || persisted.Flags != 1 || persisted.Evidence != 2 {
			t.Fatalf("Focus Loss causal reset persistence = %#v", persisted)
		}
	})

	t.Run("established monitoring preserves access vocabulary", func(t *testing.T) {
		policy := model.FocusLossPolicy{Enabled: true, MinimumDuration: 500 * time.Millisecond, IncidentCount: 3,
			Window: 10 * time.Second, Outcome: model.IntegrityOutcomeFlag}
		fixture := newExamAttemptFixtureWithFocusLoss(t, ctx, ss, &policy)
		connected, access := connectFocusLossFixture(t, ctx, ss, fixture, "focus-access")

		wrongCredential := newFocusLossInput(t, ctx, ss, fixture, access, 1, 500, "")
		wrongCredential.Access.ContinuityCredentialHash = model.HashToken(model.NewCredentialToken())
		_, err := ss.ExamAttempt().RecordFocusLoss(ctx, wrongCredential)
		assertExamAttemptConflict(t, err, "attempt_participation_credential")
		wrongGeneration := newFocusLossInput(t, ctx, ss, fixture, access, 1, 500, "")
		wrongGeneration.Access.Generation++
		_, err = ss.ExamAttempt().RecordFocusLoss(ctx, wrongGeneration)
		assertExamAttemptConflict(t, err, "attempt_participation_generation")
		wrongSession := newFocusLossInput(t, ctx, ss, fixture, access, 1, 500, "")
		wrongSession.Access.SessionID = model.NewSessionID()
		if _, err = ss.ExamAttempt().RecordFocusLoss(ctx, wrongSession); !store.IsNotFound(err) {
			t.Fatalf("RecordFocusLoss(foreign Session) error = %v", err)
		}

		endedAt := model.GetMillis()
		_, err = ss.ClassMember().End(ctx, fixture.membership.ID.String(), fixture.membership.Revision, endedAt)
		requireNoError(t, err)
		time.Sleep(100 * time.Millisecond)
		accepted := recordFocusLoss(t, ctx, ss, fixture, access, 1, 500, "")
		if !accepted.Qualified || accepted.WindowIncidentCount != 1 {
			t.Fatalf("Focus Loss after membership ended = %#v", accepted)
		}
		closed, err := ss.ExamAttempt().CloseConnection(ctx, &store.ExamAttemptConnectionClose{ConnectionID: connected.Connection.ID,
			CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID, Reason: model.AttemptConnectionCloseTransport,
			AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()})
		requireNoError(t, err)
		if !closed.Changed {
			t.Fatalf("CloseConnection(Focus Loss access) = %#v", closed)
		}
		closedInput := newFocusLossInput(t, ctx, ss, fixture, access, 2, 500, "")
		_, err = ss.ExamAttempt().RecordFocusLoss(ctx, closedInput)
		assertExamAttemptConflict(t, err, "attempt_connection_closed")

		closeAt := model.NowUTC()
		_, err = ss.ExamSitting().EarlyClose(ctx, &store.ExamSittingManagerTransition{ExamID: fixture.examID,
			SittingID: fixture.sitting.ID, ActorUserID: fixture.manager.ID, ExpectedRevision: fixture.sitting.Revision,
			FinalizeJob:   newExamSittingFinalizeJob(t, fixture.sitting.ID, fixture.sitting.Revision+1, closeAt),
			PrivateReason: "verify terminal Focus Loss fence", ChangedAt: closeAt,
			AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.manager.ID, fixture.examID, fixture.unitID).ID.String(), AuditAt: model.GetMillis()},
			examCommand(fixture.manager.ID, "exam.sitting.close.v1", "focus-close", "focus-close"))
		requireNoError(t, err)
		terminalInput := newFocusLossInput(t, ctx, ss, fixture, access, 2, 500, "")
		_, err = ss.ExamAttempt().RecordFocusLoss(ctx, terminalInput)
		assertExamAttemptConflict(t, err, "exam_sitting_state")
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
		assertExamAttemptConflict(t, err, "attempt_participation_expired")
		if persisted := probe.FocusLossPersistence(t, ctx, connected.Attempt.ID, connected.Participation.Generation); persisted != (FocusLossPersistenceProbe{}) {
			t.Fatalf("expired Focus Loss race left persistence = %#v", persisted)
		}
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
			AuditEventID: saveExamAttemptSystemAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()})
		requireNoError(t, err)
		reallow := &store.ExamAttemptReallow{ExamID: fixture.examID, SittingID: fixture.sitting.ID, AttemptID: connected.Attempt.ID,
			SuspensionID: expired.Suspension.ID, ActorUserID: fixture.manager.ID, ExpectedAttemptRevision: expired.Attempt.Revision,
			PrivateReason: "connectivity was restored", ChangedAt: model.NowUTC(),
			AuditEventID: saveExamAttemptReallowAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
		reallowed, err := ss.ExamAttempt().ReallowAttempt(ctx, reallow,
			examCommand(fixture.manager.ID, store.ExamAttemptReallowOperation, "connection-loss-reallow-focus", "connection-loss-reallow-focus"))
		requireNoError(t, err)
		if reallowed.FocusLossWindowReset {
			t.Fatalf("Connection Loss reallow reset Focus Loss window: %#v", reallowed)
		}
		if persisted := probe.FocusLossPersistence(t, ctx, connected.Attempt.ID, connected.Participation.Generation); persisted.Pending != 1 {
			t.Fatalf("Connection Loss reallow changed Focus Loss bucket = %#v", persisted)
		}
	})

	t.Run("audit rollback leaves no accepted sequence", func(t *testing.T) {
		policy := model.FocusLossPolicy{Enabled: true, MinimumDuration: 500 * time.Millisecond, IncidentCount: 2,
			Window: 10 * time.Second, Outcome: model.IntegrityOutcomeFlag}
		fixture := newExamAttemptFixtureWithFocusLoss(t, ctx, ss, &policy)
		connected, access := connectFocusLossFixture(t, ctx, ss, fixture, "focus-audit-rollback")
		input := newFocusLossInput(t, ctx, ss, fixture, access, 1, 500, model.FocusLossSourceWindowBlur)
		input.AuditEventID = model.NewId()
		if _, err := ss.ExamAttempt().RecordFocusLoss(ctx, input); err == nil {
			t.Fatal("RecordFocusLoss with missing audit unexpectedly succeeded")
		}
		if persisted := probe.FocusLossPersistence(t, ctx, connected.Attempt.ID, connected.Participation.Generation); persisted != (FocusLossPersistenceProbe{}) {
			t.Fatalf("failed Focus Loss audit left partial persistence = %#v", persisted)
		}
		input.AuditEventID, input.AuditAt = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), model.GetMillis()
		accepted, err := ss.ExamAttempt().RecordFocusLoss(ctx, input)
		requireNoError(t, err)
		if accepted.Duplicate || accepted.AcceptedSequence != 1 {
			t.Fatalf("RecordFocusLoss after audit rollback = %#v", accepted)
		}
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
		requireNoError(t, <-errorsFound)
		requireNoError(t, <-errorsFound)
		requireSuccessfulAudit(t, ctx, ss, inputs[0].AuditEventID)
		requireSuccessfulAudit(t, ctx, ss, inputs[1].AuditEventID)
		assertFocusLossAuditAllowlist(t, ctx, ss, inputs[0].AuditEventID)
		assertFocusLossAuditAllowlist(t, ctx, ss, inputs[1].AuditEventID)
		if first.Duplicate == second.Duplicate || first.Signal.ID != second.Signal.ID || first.Flag == nil || second.Flag == nil ||
			first.Flag.ID != second.Flag.ID || !first.DatabaseTime.Equal(second.DatabaseTime) {
			t.Fatalf("concurrent Focus Loss convergence = %#v, %#v", first, second)
		}
		if persisted := probe.FocusLossPersistence(t, ctx, connected.Attempt.ID, connected.Participation.Generation); persisted.Flags != 1 || persisted.Evidence != 2 || persisted.Pending != 0 {
			t.Fatalf("concurrent Focus Loss persistence = %#v", persisted)
		}
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
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
	prepareExamAttemptConnect(t, ctx, ss, input)
	connected, err := ss.ExamAttempt().Connect(ctx, input, examCommand(fixture.candidate.ID, store.ExamAttemptConnectOperation, key, key))
	requireNoError(t, err)
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
	requireNoError(t, err)
	return result
}

func newFocusLossInput(t *testing.T, ctx context.Context, ss store.Store, fixture examAttemptFixture,
	access store.ExamAttemptFocusLossAccess, sequence, duration int64, source model.FocusLossSource,
) *store.ExamAttemptFocusLossSignal {
	t.Helper()
	return &store.ExamAttemptFocusLossSignal{Access: access, SchemaVersion: model.FocusLossSignalSchemaVersion,
		SignalID: model.NewFocusLossSignalID(), EvidenceID: model.NewIntegrityEvidenceID(), FlagID: model.NewIntegrityFlagID(),
		SuspensionID: model.NewAttemptSuspensionID(), Sequence: sequence, DurationMilliseconds: duration, Source: source,
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
}
