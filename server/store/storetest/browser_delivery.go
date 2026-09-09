// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"context"
	"errors"
	"fmt"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"strings"
	"testing"
	"time"
)

func browserDeliveryAuditFixture(t *testing.T, ctx context.Context, ss store.Store, access store.BrowserDeliveryAccess) string {
	t.Helper()
	target, err := ss.ExamAttempt().ResolveBrowserDeliveryTarget(ctx, access)
	requireNoError(t, err)
	audit, err := ss.Audit().Save(ctx, &model.AuditEvent{ActorID: access.Access.CandidateUserID, Action: string(model.ActionExamSittingParticipate), Resource: model.Resource{Type: model.ResourceExamSitting, ID: target.SittingID.String()}, ScopeType: model.RoleScopeClass, ScopeID: target.ClassID.String(), Status: model.AuditStatusAttempt, NodeID: "test-node"})
	requireNoError(t, err)
	return audit.ID.String()
}
func testBrowserDeliveryRecovery(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture, connected, access := newBrowserActivityFixture(t, ctx, ss, "browser-delivery-recovery")
	source := browserSourceID(10001)
	status, err := startBrowserSourceFixture(t, ctx, ss, &store.BrowserActivitySourceStart{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: source})
	requireNoError(t, err)
	selector := store.BrowserDeliveryAccess{Access: access, SourceSessionID: source, ParticipationID: connected.Participation.ID}
	event := browserActivityEvent(2, model.BrowserActivityOpened, fixture.revisionID)
	ack, err := appendBrowserActivityFixture(t, ctx, ss, &store.BrowserActivityAppend{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: source, Events: []model.BrowserActivityEvent{event}})
	requireNoError(t, err)
	if ack.HighestContiguous != 0 || ack.SettledThrough != 0 || ack.AllocatedThrough != 2 || len(ack.Receipts) != 1 || ack.Receipts[0].Validate() != nil {
		t.Fatalf("initial out-of-order receipt: %#v", ack)
	}
	original := ack.Receipts[0]
	gap := model.DeclareDeliveryGaps{DeclarationID: "browser-gap-1", AllocatedThroughSequence: 4, Ranges: []model.SequenceRange{{First: 1, Last: 1}}, Reason: "spool_lost"}
	gapInput := &store.BrowserDeliveryGapDeclaration{Access: selector, Declaration: gap, AuditEventID: browserDeliveryAuditFixture(t, ctx, ss, selector), AuditAt: model.GetMillis()}
	gapCommand := examCommand(access.CandidateUserID, store.BrowserDeliveryGapsOperation, "browser-gap-1", "browser-gap-1")
	gapped, err := ss.ExamAttempt().DeclareBrowserDeliveryGaps(ctx, gapInput, gapCommand)
	requireNoError(t, err)
	if gapped.Status.HighestContiguous != 0 || gapped.Status.SettledThrough != 2 || gapped.Status.AllocatedThrough != 4 || gapped.Receipt.DeclarationRevision != 1 {
		t.Fatalf("permanent gap manufactured receipt or lost allocation: %#v", gapped)
	}
	gapInput.AuditEventID = browserDeliveryAuditFixture(t, ctx, ss, selector)
	replay, err := ss.ExamAttempt().DeclareBrowserDeliveryGaps(ctx, gapInput, gapCommand)
	requireNoError(t, err)
	if replay.Receipt != gapped.Receipt {
		t.Fatalf("gap receipt changed: %#v", replay)
	}
	_, err = appendBrowserActivityFixture(t, ctx, ss, &store.BrowserActivityAppend{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: source, Events: []model.BrowserActivityEvent{browserActivityEvent(1, model.BrowserActivityOpened, fixture.revisionID)}})
	assertExamAttemptConflict(t, err, "declaration_conflict")
	page, err := ss.ExamAttempt().BrowserDeliveryReceipts(ctx, selector, 1, 1)
	requireNoError(t, err)
	if len(page.Receipts) != 1 || page.Receipts[0] != original || page.NextSequence != nil {
		t.Fatalf("receipt page invented missing positions: %#v", page)
	}
	_, err = startBrowserSourceFixture(t, ctx, ss, &store.BrowserActivitySourceStart{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: browserSourceID(10002), Transition: model.BrowserStartTransition{Kind: "runtime_reset", PredecessorSourceSessionID: source, Reason: model.BrowserSourceResetCoordinatorRestarted}})
	requireNoError(t, err)
	historical := access
	historical.ConnectionID = ""
	historical.ContinuityCredentialHash = ""
	selector.Access = historical
	batch := &store.BrowserActivityAppend{Access: historical, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: source, PolicyRevisionID: status.PolicyRevisionID, PolicyDigest: status.PolicyDigest, Events: []model.BrowserActivityEvent{browserActivityEvent(5, model.BrowserActivityClosed, fixture.revisionID)}, AuditEventID: browserDeliveryAuditFixture(t, ctx, ss, selector), AuditAt: model.GetMillis()}
	_, err = ss.ExamAttempt().AppendHistoricalBrowserDelivery(ctx, batch, examCommand(access.CandidateUserID, store.BrowserDeliveryAppendOperation, "browser-before-seal", "browser-before-seal"))
	assertExamAttemptConflict(t, err, "replay_window_exceeded")
	final := &store.BrowserDeliveryFinalDeclaration{Access: selector, Declaration: model.FinalDeliveryDeclaration{DeclarationID: "browser-final", ExpectedDeclarationRevision: 1, FinalSequence: 6}, AuditEventID: browserDeliveryAuditFixture(t, ctx, ss, selector), AuditAt: model.GetMillis()}
	sealed, err := ss.ExamAttempt().SealBrowserDelivery(ctx, final, examCommand(access.CandidateUserID, store.BrowserDeliveryFinalOperation, "browser-final", "browser-final"))
	requireNoError(t, err)
	if sealed.Closure.FinalSequence == nil || *sealed.Closure.FinalSequence != 6 || sealed.AllocatedThrough != 6 {
		t.Fatalf("seal: %#v", sealed)
	}
	batch.Events = []model.BrowserActivityEvent{event, browserActivityEvent(5, model.BrowserActivityOpened, fixture.revisionID), browserActivityEvent(6, model.BrowserActivityClosed, fixture.revisionID)}
	batch.AuditEventID = browserDeliveryAuditFixture(t, ctx, ss, selector)
	appendCommand := examCommand(access.CandidateUserID, store.BrowserDeliveryAppendOperation, "browser-history", "browser-history")
	ack, err = ss.ExamAttempt().AppendHistoricalBrowserDelivery(ctx, batch, appendCommand)
	requireNoError(t, err)
	if len(ack.Receipts) != 3 || ack.Receipts[0] != original || ack.HighestSeen != 6 || ack.SettledThrough != 2 {
		t.Fatalf("historical repacking lost original receipt: %#v", ack)
	}
	batch.AuditEventID = browserDeliveryAuditFixture(t, ctx, ss, selector)
	repeated, err := ss.ExamAttempt().AppendHistoricalBrowserDelivery(ctx, batch, appendCommand)
	requireNoError(t, err)
	if len(repeated.Receipts) != len(ack.Receipts) {
		t.Fatal("HTTP replay lost receipts")
	}
	for i := range ack.Receipts {
		if repeated.Receipts[i] != ack.Receipts[i] {
			t.Fatal("HTTP replay changed receipt")
		}
	}
	gapInput = &store.BrowserDeliveryGapDeclaration{Access: selector, Declaration: model.DeclareDeliveryGaps{DeclarationID: "browser-gap-2", ExpectedDeclarationRevision: 2, AllocatedThroughSequence: 6, Ranges: []model.SequenceRange{{First: 3, Last: 4}}, Reason: "spool_lost"}, AuditEventID: browserDeliveryAuditFixture(t, ctx, ss, selector), AuditAt: model.GetMillis()}
	gapped, err = ss.ExamAttempt().DeclareBrowserDeliveryGaps(ctx, gapInput, examCommand(access.CandidateUserID, store.BrowserDeliveryGapsOperation, "browser-gap-2", "browser-gap-2"))
	requireNoError(t, err)
	if gapped.Status.SettledThrough != 6 || gapped.Status.HighestContiguous != 0 || len(gapped.Status.MissingRanges) != 0 {
		t.Fatalf("gap repair settlement: %#v", gapped)
	}
	page, err = ss.ExamAttempt().BrowserDeliveryReceipts(ctx, selector, 1, 1)
	requireNoError(t, err)
	if page.NextSequence == nil || *page.NextSequence != 5 {
		t.Fatalf("bounded sparse pagination: %#v", page)
	}
}

func testBrowserDeliveryWindowAfterPermanentGaps(t *testing.T, ss store.Store) {
	ctx := context.Background()
	f, connected, access := newBrowserActivityFixture(t, ctx, ss, "browser-large-gaps")
	source := browserSourceID(11001)
	_, err := startBrowserSourceFixture(t, ctx, ss, &store.BrowserActivitySourceStart{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: source})
	requireNoError(t, err)
	selector := store.BrowserDeliveryAccess{Access: access, SourceSessionID: source, ParticipationID: connected.Participation.ID}
	for i := int64(0); i < 2; i++ {
		first, last := int64(1), int64(4096)
		if i == 1 {
			first, last = 4098, 8192
		}
		key := fmt.Sprintf("browser-large-gap-%d", i)
		input := &store.BrowserDeliveryGapDeclaration{Access: selector, Declaration: model.DeclareDeliveryGaps{DeclarationID: key, ExpectedDeclarationRevision: i, AllocatedThroughSequence: last, Ranges: []model.SequenceRange{{First: first, Last: last}}, Reason: "spool_lost"}, AuditEventID: browserDeliveryAuditFixture(t, ctx, ss, selector), AuditAt: model.GetMillis()}
		result, err := ss.ExamAttempt().DeclareBrowserDeliveryGaps(ctx, input, examCommand(f.candidate.ID, store.BrowserDeliveryGapsOperation, key, key))
		requireNoError(t, err)
		if result.Status.HighestContiguous != 0 || result.Status.SettledThrough != last {
			t.Fatal("permanent gap fabricated content or failed to advance window")
		}
		ack, err := appendBrowserActivityFixture(t, ctx, ss, &store.BrowserActivityAppend{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: source, Events: []model.BrowserActivityEvent{browserActivityEvent(last+1, model.BrowserActivityOpened, f.revisionID)}})
		requireNoError(t, err)
		if ack.SettledThrough != last+1 || ack.HighestContiguous != 0 {
			t.Fatal("settled window failed beyond permanent first/middle gaps")
		}
	}
	replay, err := appendBrowserActivityFixture(t, ctx, ss, &store.BrowserActivityAppend{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: source, Events: []model.BrowserActivityEvent{browserActivityEvent(4097, model.BrowserActivityOpened, f.revisionID)}})
	requireNoError(t, err)
	if len(replay.Receipts) != 1 || replay.Receipts[0].Sequence != 4097 || replay.SettledThrough != 8193 {
		t.Fatal("exact receipt replay was lost behind the active window")
	}
}

func testBrowserSourceReservationRace(t *testing.T, ss store.Store) {
	ctx := context.Background()
	f, connected, access := newBrowserActivityFixture(t, ctx, ss, "browser-start-race")
	presentation, err := ss.ExamAttempt().GetCandidatePresentation(ctx, access)
	requireNoError(t, err)
	results := make(chan error, 2)
	start := make(chan struct{})
	for i := range 2 {
		input := &store.BrowserActivitySourceStart{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: browserSourceID(12001 + i), PolicyRevisionID: presentation.RuntimeCapabilities.Browser.PolicyRevisionID, PolicyDigest: presentation.RuntimeCapabilities.Browser.PolicyDigest, Transition: model.BrowserStartTransition{Kind: "initial"}, AuditEventID: saveExamAttemptAudit(t, ctx, ss, f).ID.String(), AuditAt: model.GetMillis()}
		go func() { <-start; _, err := ss.ExamAttempt().StartBrowserActivity(ctx, input); results <- err }()
	}
	close(start)
	successes := 0
	for range 2 {
		if err := <-results; err == nil {
			successes++
		} else {
			assertExamAttemptConflict(t, err, "browser_source_current")
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent initial starts succeeded %d times", successes)
	}
	budget, err := ss.ExamAttempt().DeliveryBudget(ctx, store.DeliveryBudgetAccess{Access: access, ParticipationID: connected.Participation.ID})
	requireNoError(t, err)
	if budget.ControlMetadataBytes != model.NativeOwnerReservationBytes+model.BrowserOwnerReservationBytes {
		t.Fatal("refused source reserved lifetime metadata")
	}
	sources, err := ss.ExamAttempt().BrowserSourceList(ctx, store.BrowserDeliveryAccess{Access: access, ParticipationID: connected.Participation.ID})
	requireNoError(t, err)
	if len(sources) != 1 {
		t.Fatal("concurrent start retained an extra source")
	}
}

func testBrowserPendingRepairReservation(t *testing.T, ss store.Store) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	f, connected, access := newBrowserActivityFixture(t, ctx, ss, "browser-repair-reservation")
	source := browserSourceID(13001)
	_, err := startBrowserSourceFixture(t, ctx, ss, &store.BrowserActivitySourceStart{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: source})
	requireNoError(t, err)
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
		requireNoError(t, err)
		return value
	}
	next := int64(2)
	var refused []model.BrowserActivityEvent
	for round := 0; round < 16; round++ {
		events := make([]model.BrowserActivityEvent, 48)
		for i := range events {
			events[i] = event(next + int64(i))
		}
		before := budget()
		_, err := appendEvents(events)
		if err != nil {
			var conflict *store.ErrConflict
			if !errors.As(err, &conflict) || conflict.Constraint != "pending_capacity" {
				t.Fatalf("unexpected out-of-order refusal: %v", err)
			}
			after := budget()
			if after.Browser.PendingBytes != before.Browser.PendingBytes || after.Browser.Attempt.RetainedRecords != before.Browser.Attempt.RetainedRecords || after.Browser.Participation.SummaryOnly {
				t.Fatal("pending refusal retained partial bytes or latched detail stop")
			}
			if after.Browser.PendingBytes == 0 || after.Browser.PendingBytes > model.DeliveryPendingByteLimit-model.DeliveryRepairReservationBytes {
				t.Fatal("out-of-order traffic consumed repair reservation")
			}
			refused = events
			break
		}
		next += int64(len(events))
		// Refilling the shared append bucket is separate from byte admission.
		time.Sleep(150 * time.Millisecond)
	}
	if refused == nil {
		t.Fatal("bounded pending pool never refused out-of-order details")
	}
	time.Sleep(150 * time.Millisecond)
	repaired, err := appendEvents([]model.BrowserActivityEvent{event(1)})
	requireNoError(t, err)
	if repaired.HighestContiguous != next-1 || repaired.SettledThrough != next-1 || budget().Browser.PendingBytes != 0 {
		t.Fatal("head repair did not release the retained queue")
	}
	time.Sleep(150 * time.Millisecond)
	resumed, err := appendEvents(refused)
	requireNoError(t, err)
	if resumed.HighestContiguous != next+int64(len(refused))-1 || budget().Browser.Participation.SummaryOnly {
		t.Fatal("pending backpressure prevented ordinary delivery after repair")
	}
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
			requireNoError(t, err)
			selector := store.DeliveryBudgetAccess{Access: access, ParticipationID: connected.Participation.ID}
			budget, err := ss.ExamAttempt().DeliveryBudget(ctx, selector)
			requireNoError(t, err)
			var partBytes, attemptBytes int64
			if scope == "participation" {
				partBytes = budget.Browser.Participation.ByteLimit - model.DeliveryRepairReservationBytes
				attemptBytes = partBytes
			} else {
				attemptBytes = budget.Browser.Attempt.ByteLimit - model.DeliveryRepairReservationBytes
			}
			requireNoError(t, prime(ctx, connected.Attempt.ID, connected.Participation.ID, partBytes, attemptBytes))
			appendEvent := func(sequence int64) (*model.BrowserActivityAcknowledgement, error) {
				return appendBrowserActivityFixture(t, ctx, ss, &store.BrowserActivityAppend{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: source, Events: []model.BrowserActivityEvent{browserActivityEvent(sequence, model.BrowserActivityOpened, f.revisionID)}})
			}
			_, err = appendEvent(2)
			var conflict *store.ErrConflict
			if !errors.As(err, &conflict) || conflict.Constraint != "pending_capacity" {
				t.Fatalf("first gap consumed retained repair reserve: %v", err)
			}
			after, err := ss.ExamAttempt().DeliveryBudget(ctx, selector)
			requireNoError(t, err)
			if after.Browser.PendingBytes != 0 || after.Browser.Participation.RetainedBytes != partBytes || after.Browser.Attempt.RetainedBytes != attemptBytes || after.Browser.Participation.SummaryOnly || after.Browser.Attempt.SummaryOnly {
				t.Fatal("gap refusal charged bytes or permanently stopped details")
			}
			repaired, err := appendEvent(1)
			requireNoError(t, err)
			if repaired.HighestContiguous != 1 || repaired.SettledThrough != 1 {
				t.Fatal("in-order repair cannot use reserved retained bytes")
			}
		})
	}
}
