// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func browserDeliveryAuditFixture(t *testing.T, ctx context.Context, ss store.Store, access store.BrowserDeliveryAccess) model.AuditEventID {
	t.Helper()
	target, err := ss.ExamAttempt().ResolveBrowserDeliveryTarget(ctx, access)
	require.NoError(t, err)
	audit, err := ss.Audit().Save(ctx, &model.AuditEvent{ActorID: access.Access.CandidateUserID, Action: string(model.ActionExamSittingParticipate), Resource: model.Resource{Type: model.ResourceExamSitting, ID: target.SittingID.String()}, ScopeType: model.RoleScopeClass, ScopeID: target.ClassID.String(), Status: model.AuditStatusAttempt, NodeID: "test-node"})
	require.NoError(t, err)
	return audit.ID
}
func testBrowserDeliveryRecovery(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture, connected, access := newBrowserActivityFixture(t, ctx, ss, "browser-delivery-recovery")
	source := browserSourceID(10001)
	status, err := startBrowserSourceFixture(t, ctx, ss, &store.BrowserActivitySourceStart{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: source})
	require.NoError(t, err)
	selector := store.BrowserDeliveryAccess{Access: access, SourceSessionID: source, ParticipationID: connected.Participation.ID}
	event := browserActivityEvent(2, model.BrowserActivityOpened, fixture.revisionID)
	ack, err := appendBrowserActivityFixture(t, ctx, ss, &store.BrowserActivityAppend{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: source, Events: []model.BrowserActivityEvent{event}})
	require.NoError(t, err)
	require.Equal(t, int64(0), ack.HighestContiguous)
	require.Equal(t, int64(0), ack.SettledThrough)
	require.Equal(t, int64(2), ack.AllocatedThrough)
	require.Len(t, ack.Receipts, 1)
	require.NoError(t, ack.Receipts[0].Validate())
	original := ack.Receipts[0]
	gap := model.DeclareDeliveryGaps{DeclarationID: "browser-gap-1", AllocatedThroughSequence: 4, Ranges: []model.SequenceRange{{First: 1, Last: 1}}, Reason: "spool_lost"}
	gapInput := &store.BrowserDeliveryGapDeclaration{Access: selector, Declaration: gap, AuditEventID: browserDeliveryAuditFixture(t, ctx, ss, selector), AuditAt: model.GetMillis()}
	gapCommand := examCommand(access.CandidateUserID, store.BrowserDeliveryGapsOperation, "browser-gap-1", "browser-gap-1")
	gapped, err := ss.ExamAttempt().DeclareBrowserDeliveryGaps(ctx, gapInput, gapCommand)
	require.NoError(t, err)
	require.Equal(t, int64(0), gapped.Status.HighestContiguous)
	require.Equal(t, int64(2), gapped.Status.SettledThrough)
	require.Equal(t, int64(4), gapped.Status.AllocatedThrough)
	require.Equal(t, int64(1), gapped.Receipt.DeclarationRevision)
	gapInput.AuditEventID = browserDeliveryAuditFixture(t, ctx, ss, selector)
	replay, err := ss.ExamAttempt().DeclareBrowserDeliveryGaps(ctx, gapInput, gapCommand)
	require.NoError(t, err)
	require.Equal(t, gapped.Receipt, replay.Receipt)
	_, err = appendBrowserActivityFixture(t, ctx, ss, &store.BrowserActivityAppend{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: source, Events: []model.BrowserActivityEvent{browserActivityEvent(1, model.BrowserActivityOpened, fixture.revisionID)}})
	requireConflictConstraint(t, err, "declaration_conflict")
	page, err := ss.ExamAttempt().BrowserDeliveryReceipts(ctx, selector, 1, 1)
	require.NoError(t, err)
	require.Len(t, page.Receipts, 1)
	require.Equal(t, original, page.Receipts[0])
	require.Nil(t, page.NextSequence)
	_, err = startBrowserSourceFixture(t, ctx, ss, &store.BrowserActivitySourceStart{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: browserSourceID(10002), Transition: model.BrowserStartTransition{Kind: "runtime_reset", PredecessorSourceSessionID: source, Reason: model.BrowserSourceResetCoordinatorRestarted}})
	require.NoError(t, err)
	historical := access
	historical.ConnectionID = ""
	historical.ContinuityCredentialHash = ""
	selector.Access = historical
	batch := &store.BrowserActivityAppend{Access: historical, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: source, PolicyRevisionID: status.PolicyRevisionID, PolicyDigest: status.PolicyDigest, Events: []model.BrowserActivityEvent{browserActivityEvent(5, model.BrowserActivityClosed, fixture.revisionID)}, AuditEventID: browserDeliveryAuditFixture(t, ctx, ss, selector), AuditAt: model.GetMillis()}
	_, err = ss.ExamAttempt().AppendHistoricalBrowserDelivery(ctx, batch, examCommand(access.CandidateUserID, store.BrowserDeliveryAppendOperation, "browser-before-seal", "browser-before-seal"))
	requireConflictConstraint(t, err, "replay_window_exceeded")
	final := &store.BrowserDeliveryFinalDeclaration{Access: selector, Declaration: model.FinalDeliveryDeclaration{DeclarationID: "browser-final", ExpectedDeclarationRevision: 1, FinalSequence: 6}, AuditEventID: browserDeliveryAuditFixture(t, ctx, ss, selector), AuditAt: model.GetMillis()}
	sealed, err := ss.ExamAttempt().SealBrowserDelivery(ctx, final, examCommand(access.CandidateUserID, store.BrowserDeliveryFinalOperation, "browser-final", "browser-final"))
	require.NoError(t, err)
	require.NotNil(t, sealed.Closure.FinalSequence)
	require.Equal(t, int64(6), *sealed.Closure.FinalSequence)
	require.Equal(t, int64(6), sealed.AllocatedThrough)
	batch.Events = []model.BrowserActivityEvent{event, browserActivityEvent(5, model.BrowserActivityOpened, fixture.revisionID), browserActivityEvent(6, model.BrowserActivityClosed, fixture.revisionID)}
	batch.AuditEventID = browserDeliveryAuditFixture(t, ctx, ss, selector)
	appendCommand := examCommand(access.CandidateUserID, store.BrowserDeliveryAppendOperation, "browser-history", "browser-history")
	ack, err = ss.ExamAttempt().AppendHistoricalBrowserDelivery(ctx, batch, appendCommand)
	require.NoError(t, err)
	require.Len(t, ack.Receipts, 3)
	require.Equal(t, original, ack.Receipts[0])
	require.Equal(t, int64(6), ack.HighestSeen)
	require.Equal(t, int64(2), ack.SettledThrough)
	batch.AuditEventID = browserDeliveryAuditFixture(t, ctx, ss, selector)
	repeated, err := ss.ExamAttempt().AppendHistoricalBrowserDelivery(ctx, batch, appendCommand)
	require.NoError(t, err)
	require.Len(t, repeated.Receipts, len(ack.Receipts), "HTTP replay lost receipts")
	for i := range ack.Receipts {
		require.Equal(t, ack.Receipts[i], repeated.Receipts[i], "HTTP replay changed receipt")
	}
	gapInput = &store.BrowserDeliveryGapDeclaration{Access: selector, Declaration: model.DeclareDeliveryGaps{DeclarationID: "browser-gap-2", ExpectedDeclarationRevision: 2, AllocatedThroughSequence: 6, Ranges: []model.SequenceRange{{First: 3, Last: 4}}, Reason: "spool_lost"}, AuditEventID: browserDeliveryAuditFixture(t, ctx, ss, selector), AuditAt: model.GetMillis()}
	gapped, err = ss.ExamAttempt().DeclareBrowserDeliveryGaps(ctx, gapInput, examCommand(access.CandidateUserID, store.BrowserDeliveryGapsOperation, "browser-gap-2", "browser-gap-2"))
	require.NoError(t, err)
	require.Equal(t, int64(6), gapped.Status.SettledThrough)
	require.Equal(t, int64(0), gapped.Status.HighestContiguous)
	require.Empty(t, gapped.Status.MissingRanges)
	page, err = ss.ExamAttempt().BrowserDeliveryReceipts(ctx, selector, 1, 1)
	require.NoError(t, err)
	require.NotNil(t, page.NextSequence)
	require.Equal(t, int64(5), *page.NextSequence)
}

func testBrowserDeliveryWindowAfterPermanentGaps(t *testing.T, ss store.Store) {
	ctx := context.Background()
	f, connected, access := newBrowserActivityFixture(t, ctx, ss, "browser-large-gaps")
	source := browserSourceID(11001)
	_, err := startBrowserSourceFixture(t, ctx, ss, &store.BrowserActivitySourceStart{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: source})
	require.NoError(t, err)
	selector := store.BrowserDeliveryAccess{Access: access, SourceSessionID: source, ParticipationID: connected.Participation.ID}
	for i := int64(0); i < 2; i++ {
		first, last := int64(1), int64(4096)
		if i == 1 {
			first, last = 4098, 8192
		}
		key := fmt.Sprintf("browser-large-gap-%d", i)
		input := &store.BrowserDeliveryGapDeclaration{Access: selector, Declaration: model.DeclareDeliveryGaps{DeclarationID: key, ExpectedDeclarationRevision: i, AllocatedThroughSequence: last, Ranges: []model.SequenceRange{{First: first, Last: last}}, Reason: "spool_lost"}, AuditEventID: browserDeliveryAuditFixture(t, ctx, ss, selector), AuditAt: model.GetMillis()}
		result, err := ss.ExamAttempt().DeclareBrowserDeliveryGaps(ctx, input, examCommand(f.candidate.ID, store.BrowserDeliveryGapsOperation, key, key))
		require.NoError(t, err)
		require.Equal(t, int64(0), result.Status.HighestContiguous, "permanent gap fabricated content or failed to advance window")
		require.Equal(t, last, result.Status.SettledThrough, "permanent gap fabricated content or failed to advance window")
		ack, err := appendBrowserActivityFixture(t, ctx, ss, &store.BrowserActivityAppend{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: source, Events: []model.BrowserActivityEvent{browserActivityEvent(last+1, model.BrowserActivityOpened, f.revisionID)}})
		require.NoError(t, err)
		require.Equal(t, last+1, ack.SettledThrough, "settled window failed beyond permanent first/middle gaps")
		require.Equal(t, int64(0), ack.HighestContiguous, "settled window failed beyond permanent first/middle gaps")
	}
	replay, err := appendBrowserActivityFixture(t, ctx, ss, &store.BrowserActivityAppend{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: source, Events: []model.BrowserActivityEvent{browserActivityEvent(4097, model.BrowserActivityOpened, f.revisionID)}})
	require.NoError(t, err)
	require.Len(t, replay.Receipts, 1, "exact receipt replay was lost behind the active window")
	require.Equal(t, int64(4097), replay.Receipts[0].Sequence, "exact receipt replay was lost behind the active window")
	require.Equal(t, int64(8193), replay.SettledThrough, "exact receipt replay was lost behind the active window")
}

func testBrowserSourceReservationRace(t *testing.T, ss store.Store) {
	ctx := context.Background()
	f, connected, access := newBrowserActivityFixture(t, ctx, ss, "browser-start-race")
	presentation, err := ss.ExamAttempt().GetCandidatePresentation(ctx, access)
	require.NoError(t, err)
	results := make(chan error, 2)
	start := make(chan struct{})
	for i := range 2 {
		input := &store.BrowserActivitySourceStart{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: browserSourceID(12001 + i), PolicyRevisionID: presentation.RuntimeCapabilities.Browser.PolicyRevisionID, PolicyDigest: presentation.RuntimeCapabilities.Browser.PolicyDigest, Transition: model.BrowserStartTransition{Kind: "initial"}, AuditEventID: saveExamAttemptAudit(t, ctx, ss, f).ID, AuditAt: model.GetMillis()}
		go func() { <-start; _, err := ss.ExamAttempt().StartBrowserActivity(ctx, input); results <- err }()
	}
	close(start)
	successes := 0
	for range 2 {
		if err := <-results; err == nil {
			successes++
		} else {
			requireConflictConstraint(t, err, "browser_source_current")
		}
	}
	require.Equal(t, 1, successes, "concurrent initial starts succeeded")
	budget, err := ss.ExamAttempt().DeliveryBudget(ctx, store.DeliveryBudgetAccess{Access: access, ParticipationID: connected.Participation.ID})
	require.NoError(t, err)
	require.Equal(t, model.NativeOwnerReservationBytes+model.BrowserOwnerReservationBytes, budget.ControlMetadataBytes, "refused source reserved lifetime metadata")
	sources, err := ss.ExamAttempt().BrowserSourceList(ctx, store.BrowserDeliveryAccess{Access: access, ParticipationID: connected.Participation.ID})
	require.NoError(t, err)
	require.Len(t, sources, 1, "concurrent start retained an extra source")
}

func testBrowserPendingRepairReservation(t *testing.T, ss store.Store) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	f, connected, access := newBrowserActivityFixture(t, ctx, ss, "browser-repair-reservation")
	source := browserSourceID(13001)
	_, err := startBrowserSourceFixture(t, ctx, ss, &store.BrowserActivitySourceStart{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: source})
	require.NoError(t, err)
	event := func(seq int64) model.BrowserActivityEvent {
		value := browserActivityEvent(seq, model.BrowserActivityTopNavigation, f.revisionID)
		rule := "start"
		value.MatchedRuleID = &rule
		value.Location = &model.BrowserLocation{Scheme: "https", Host: "example.edu", Path: "/" + strings.Repeat("a", 4095)}
		return value
	}
	appendEvents := func(events []model.BrowserActivityEvent) (*model.BrowserActivityAcknowledgement, error) {
		return appendBrowserActivityFixture(t, ctx, ss, &store.BrowserActivityAppend{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: source, Events: events})
	}
	budget := func() *model.DeliveryBudgetSnapshot {
		value, err := ss.ExamAttempt().DeliveryBudget(ctx, store.DeliveryBudgetAccess{Access: access, ParticipationID: connected.Participation.ID})
		require.NoError(t, err)
		return value
	}
	// The rounds outlast one initial Participation lease on a loaded
	// database, so the fixture renews it as a live client would.
	renewal := &store.ExamAttemptParticipationRenewal{DesktopCompatibilityPolicyRevision: 1, AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID, ConnectionID: connected.Connection.ID, CandidateUserID: f.candidate.ID, SessionID: f.session.ID, Generation: connected.Participation.Generation, DesktopRegistrationID: f.session.DesktopRegistrationID, DPoPKeyThumbprint: f.session.DPoPKeyThumbprint, ContinuityCredentialHash: access.ContinuityCredentialHash}
	prepareParticipationRenewalCoverage(t, ctx, ss, renewal)
	renew := func() {
		renewal.Sequence++
		_, renewErr := ss.ExamAttempt().RenewParticipation(ctx, renewal)
		require.NoError(t, renewErr)
	}
	next := int64(2)
	var refused []model.BrowserActivityEvent
	for round := 0; round < 16; round++ {
		renew()
		events := make([]model.BrowserActivityEvent, 48)
		for i := range events {
			events[i] = event(next + int64(i))
		}
		before := budget()
		_, err := appendEvents(events)
		if err != nil {
			var conflict *store.ErrConflict
			require.ErrorAs(t, err, &conflict)
			require.Equal(t, "pending_capacity", conflict.Constraint)
			after := budget()
			require.Equal(t, before.Browser.PendingBytes, after.Browser.PendingBytes, "pending refusal retained partial bytes or latched detail stop")
			require.Equal(t, before.Browser.Attempt.RetainedRecords, after.Browser.Attempt.RetainedRecords, "pending refusal retained partial bytes or latched detail stop")
			require.False(t, after.Browser.Participation.SummaryOnly, "pending refusal retained partial bytes or latched detail stop")
			require.NotEqual(t, int64(0), after.Browser.PendingBytes, "out-of-order traffic consumed repair reservation")
			require.LessOrEqual(t, after.Browser.PendingBytes, model.DeliveryPendingByteLimit-model.DeliveryRepairReservationBytes, "out-of-order traffic consumed repair reservation")
			refused = events
			break
		}
		next += int64(len(events))
		// Refilling the shared append bucket is separate from byte admission.
		time.Sleep(150 * time.Millisecond)
	}
	require.NotNil(t, refused, "bounded pending pool never refused out-of-order details")
	time.Sleep(150 * time.Millisecond)
	renew()
	repaired, err := appendEvents([]model.BrowserActivityEvent{event(1)})
	require.NoError(t, err)
	require.Equal(t, next-1, repaired.HighestContiguous, "head repair did not release the retained queue")
	require.Equal(t, next-1, repaired.SettledThrough, "head repair did not release the retained queue")
	require.Equal(t, int64(0), budget().Browser.PendingBytes, "head repair did not release the retained queue")
	time.Sleep(150 * time.Millisecond)
	resumed, err := appendEvents(refused)
	require.NoError(t, err)
	require.Equal(t, next+int64(len(refused))-1, resumed.HighestContiguous, "pending backpressure prevented ordinary delivery after repair")
	require.False(t, budget().Browser.Participation.SummaryOnly, "pending backpressure prevented ordinary delivery after repair")
}

// TestBrowserRetainedRepairReservation exercises durable byte-limit boundaries.
// prime sets only fixture counters; the actual admission and repair use the Store.
func TestBrowserRetainedRepairReservation(t *testing.T, ss store.Store, prime func(context.Context, model.ExamAttemptID, model.AttemptParticipationID, int64, int64) error) {
	for index, scope := range []string{"participation", "attempt"} {
		t.Run(scope, func(t *testing.T) {
			ctx := t.Context()
			f, connected, access := newBrowserActivityFixture(t, ctx, ss, "browser-retained-repair-"+scope)
			source := browserSourceID(14001 + index)
			_, err := startBrowserSourceFixture(t, ctx, ss, &store.BrowserActivitySourceStart{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: source})
			require.NoError(t, err)
			selector := store.DeliveryBudgetAccess{Access: access, ParticipationID: connected.Participation.ID}
			budget, err := ss.ExamAttempt().DeliveryBudget(ctx, selector)
			require.NoError(t, err)
			var partBytes, attemptBytes int64
			if scope == "participation" {
				partBytes = budget.Browser.Participation.ByteLimit - model.DeliveryRepairReservationBytes
				attemptBytes = partBytes
			} else {
				attemptBytes = budget.Browser.Attempt.ByteLimit - model.DeliveryRepairReservationBytes
			}
			require.NoError(t, prime(ctx, connected.Attempt.ID, connected.Participation.ID, partBytes, attemptBytes))
			appendEvent := func(sequence int64) (*model.BrowserActivityAcknowledgement, error) {
				return appendBrowserActivityFixture(t, ctx, ss, &store.BrowserActivityAppend{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: source, Events: []model.BrowserActivityEvent{browserActivityEvent(sequence, model.BrowserActivityOpened, f.revisionID)}})
			}
			_, err = appendEvent(2)
			var conflict *store.ErrConflict
			require.ErrorAs(t, err, &conflict)
			require.Equal(t, "pending_capacity", conflict.Constraint)
			after, err := ss.ExamAttempt().DeliveryBudget(ctx, selector)
			require.NoError(t, err)
			require.Equal(t, int64(0), after.Browser.PendingBytes, "gap refusal charged bytes or permanently stopped details")
			require.Equal(t, partBytes, after.Browser.Participation.RetainedBytes, "gap refusal charged bytes or permanently stopped details")
			require.Equal(t, attemptBytes, after.Browser.Attempt.RetainedBytes, "gap refusal charged bytes or permanently stopped details")
			require.False(t, after.Browser.Participation.SummaryOnly, "gap refusal charged bytes or permanently stopped details")
			require.False(t, after.Browser.Attempt.SummaryOnly, "gap refusal charged bytes or permanently stopped details")
			repaired, err := appendEvent(1)
			require.NoError(t, err)
			require.Equal(t, int64(1), repaired.HighestContiguous, "in-order repair cannot use reserved retained bytes")
			require.Equal(t, int64(1), repaired.SettledThrough, "in-order repair cannot use reserved retained bytes")
		})
	}
}
