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
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestBrowserActivityStore(t *testing.T, ss store.Store) {
	t.Helper()
	t.Run("pending queue reserves head repair", func(t *testing.T) { testBrowserPendingRepairReservation(t, ss) })
	t.Run("permanent gaps advance the replay window", func(t *testing.T) { testBrowserDeliveryWindowAfterPermanentGaps(t, ss) })
	t.Run("initial source reservations serialize", func(t *testing.T) { testBrowserSourceReservationRace(t, ss) })
	t.Run("event receipts gaps and historical recovery", func(t *testing.T) { testBrowserDeliveryRecovery(t, ss) })
	t.Run("independent correction capability gates", func(t *testing.T) { testCorrectionCapabilityGates(t, ss) })
	t.Run("correction publication serializes Workspace mutations", func(t *testing.T) { testCorrectionPublicationWorkspaceRace(t, ss) })
	t.Run("bounded append replay reset pagination and Submission accounting", func(t *testing.T) {
		testBrowserActivityLifecycle(t, ss)
	})
	t.Run("complete terminal source settles", func(t *testing.T) {
		testCompleteBrowserActivitySubmission(t, ss)
	})
	t.Run("source count is bounded per Participation", func(t *testing.T) {
		testBrowserActivitySourceLimit(t, ss)
	})
}

func testBrowserActivityLifecycle(t *testing.T, ss store.Store) {
	t.Helper()
	ctx := context.Background()
	fixture, connected, access := newBrowserActivityFixture(t, ctx, ss, "browser-activity-lifecycle")
	sourceID := browserSourceID(1)
	started, err := startBrowserSourceFixture(t, ctx, ss, &store.BrowserActivitySourceStart{
		Access: access, ParticipationID: connected.Participation.ID,
		Generation: connected.Participation.Generation, SourceSessionID: sourceID,
	})
	requireNoError(t, err)
	assertBrowserSourceStart(t, started)
	replayedStart, err := startBrowserSourceFixture(t, ctx, ss, &store.BrowserActivitySourceStart{
		Access: access, ParticipationID: connected.Participation.ID,
		Generation: connected.Participation.Generation, SourceSessionID: sourceID,
	})
	requireNoError(t, err)
	assertBrowserSourceStart(t, replayedStart)
	_, err = startBrowserSourceFixture(t, ctx, ss, &store.BrowserActivitySourceStart{
		Access: access, ParticipationID: connected.Participation.ID,
		Generation: connected.Participation.Generation, SourceSessionID: browserSourceID(2),
	})
	assertExamAttemptConflict(t, err, "browser_source_current")

	evenEvents := make([]model.BrowserActivityEvent, 0, 33)
	for sequence := int64(2); sequence <= 66; sequence += 2 {
		evenEvents = append(evenEvents, browserActivityEvent(sequence, model.BrowserActivityOpened, fixture.revisionID))
	}
	acknowledgement, err := appendBrowserActivityFixture(t, ctx, ss, &store.BrowserActivityAppend{
		Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		SourceSessionID: sourceID, Events: evenEvents,
	})
	requireNoError(t, err)
	if acknowledgement.HighestContiguous != 0 || acknowledgement.HighestSeen != 66 ||
		len(acknowledgement.MissingRanges) != model.BrowserActivityMaximumMissingRanges ||
		!acknowledgement.MissingRangesTruncated || acknowledgement.MissingRanges[0] != (model.BrowserActivityMissingRange{First: 1, Last: 1}) ||
		acknowledgement.MissingRanges[31] != (model.BrowserActivityMissingRange{First: 63, Last: 63}) {
		t.Fatalf("gapped Browser Activity acknowledgement = %#v", acknowledgement)
	}
	duplicate, err := appendBrowserActivityFixture(t, ctx, ss, &store.BrowserActivityAppend{
		Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		SourceSessionID: sourceID, Events: evenEvents,
	})
	requireNoError(t, err)
	if duplicate.HighestContiguous != acknowledgement.HighestContiguous || duplicate.HighestSeen != acknowledgement.HighestSeen ||
		len(duplicate.MissingRanges) != len(acknowledgement.MissingRanges) || !duplicate.MissingRangesTruncated {
		t.Fatalf("duplicate Browser Activity acknowledgement = %#v, first=%#v", duplicate, acknowledgement)
	}
	changed := browserActivityEvent(2, model.BrowserActivityClosed, fixture.revisionID)
	_, err = appendBrowserActivityFixture(t, ctx, ss, &store.BrowserActivityAppend{
		Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		SourceSessionID: sourceID, Events: []model.BrowserActivityEvent{changed},
	})
	assertExamAttemptConflict(t, err, "browser_activity_sequence")
	tooFar := browserActivityEvent(model.BrowserActivityMaximumReorderWindow+1, model.BrowserActivityOpened, fixture.revisionID)
	_, err = appendBrowserActivityFixture(t, ctx, ss, &store.BrowserActivityAppend{
		Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		SourceSessionID: sourceID, Events: []model.BrowserActivityEvent{tooFar},
	})
	assertExamAttemptConflict(t, err, "replay_window_exceeded")

	oddEvents := make([]model.BrowserActivityEvent, 0, 33)
	for sequence := int64(1); sequence <= 65; sequence += 2 {
		oddEvents = append(oddEvents, browserActivityEvent(sequence, model.BrowserActivityOpened, fixture.revisionID))
	}
	acknowledgement, err = appendBrowserActivityFixture(t, ctx, ss, &store.BrowserActivityAppend{
		Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		SourceSessionID: sourceID, Events: oddEvents,
	})
	requireNoError(t, err)
	assertBrowserActivityAcknowledgement(t, acknowledgement, sourceID, 66, 66, nil, false)

	paused, err := ss.ExamSitting().Pause(ctx, &store.ExamSittingManagerTransition{ExamID: fixture.examID,
		SittingID: fixture.sitting.ID, ActorUserID: fixture.manager.ID, ExpectedRevision: fixture.sitting.Revision,
		PrivateReason: "prove Browser Activity is fenced while paused", ChangedAt: model.NowUTC(),
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.manager.ID, fixture.examID, fixture.unitID).ID.String(), AuditAt: model.GetMillis()},
		examCommand(fixture.manager.ID, "exam.sitting.pause.v1", "browser-activity-pause", "browser-activity-pause"))
	requireNoError(t, err)
	schemeBlockedReason := model.BrowserBlockSchemeNotAllowed
	invalidURLReason := model.BrowserBlockInvalidURL
	matchedRuleID := "start"
	finalEvents := []model.BrowserActivityEvent{
		{Sequence: 67, Kind: model.BrowserActivityBlockedNavigation, PolicyRevisionID: fixture.revisionID,
			ClientOccurredAt: browserActivityClientTime(67), Location: &model.BrowserLocation{Scheme: "http", Host: "example.edu", Path: "/blocked"},
			BlockReason: &schemeBlockedReason},
		{Sequence: 68, Kind: model.BrowserActivityBlockedNavigation, PolicyRevisionID: fixture.revisionID,
			ClientOccurredAt: browserActivityClientTime(68), Location: &model.BrowserLocation{}, BlockReason: &invalidURLReason},
		{Sequence: 69, Kind: model.BrowserActivityTopNavigation, PolicyRevisionID: fixture.revisionID,
			ClientOccurredAt: browserActivityClientTime(69), Location: &model.BrowserLocation{Scheme: "https", Host: "example.edu", Path: "/exam"},
			MatchedRuleID: &matchedRuleID},
		browserActivityEvent(70, model.BrowserActivityClosed, fixture.revisionID),
	}
	pausedAcknowledgement, err := appendBrowserActivityFixture(t, ctx, ss, &store.BrowserActivityAppend{
		Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		SourceSessionID: sourceID, Events: finalEvents,
	})
	requireNoError(t, err)
	assertBrowserActivityAcknowledgement(t, pausedAcknowledgement, sourceID, 70, 70, nil, false)
	replacementID := browserSourceID(2)
	_, err = startBrowserSourceFixture(t, ctx, ss, &store.BrowserActivitySourceStart{
		Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		SourceSessionID: replacementID, Transition: model.BrowserStartTransition{Kind: "runtime_reset", PredecessorSourceSessionID: sourceID, Reason: model.BrowserSourceResetCoordinatorRestarted},
	})
	assertExamAttemptConflict(t, err, "exam_sitting_state")
	resumed, err := ss.ExamSitting().Resume(ctx, &store.ExamSittingManagerTransition{ExamID: fixture.examID,
		SittingID: fixture.sitting.ID, ActorUserID: fixture.manager.ID, ExpectedRevision: paused.Value.Sitting.Revision,
		PrivateReason: "finish Browser Activity conformance", ChangedAt: model.NowUTC(),
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.manager.ID, fixture.examID, fixture.unitID).ID.String(), AuditAt: model.GetMillis()},
		examCommand(fixture.manager.ID, "exam.sitting.resume.v1", "browser-activity-resume", "browser-activity-resume"))
	requireNoError(t, err)
	if resumed.Value.Sitting.State != model.ExamSittingOpen {
		t.Fatalf("Resume() = %#v", resumed)
	}
	acknowledgement, err = appendBrowserActivityFixture(t, ctx, ss, &store.BrowserActivityAppend{
		Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		SourceSessionID: sourceID, Events: finalEvents,
	})
	requireNoError(t, err)
	assertBrowserActivityAcknowledgement(t, acknowledgement, sourceID, 70, 70, nil, false)

	listed, err := ss.ExamAttempt().ListBrowserActivity(ctx, store.BrowserActivityListOptions{
		ExamID: fixture.examID, SittingID: fixture.sitting.ID, AttemptID: connected.Attempt.ID, Limit: 201,
	})
	requireNoError(t, err)
	if len(listed) != 70 || listed[66].Event.Kind != model.BrowserActivityBlockedNavigation ||
		listed[66].Event.BlockReason == nil || *listed[66].Event.BlockReason != schemeBlockedReason ||
		listed[66].Event.Location == nil || listed[66].Event.Location.Scheme != "http" || listed[66].Event.MatchedRuleID != nil ||
		listed[67].Event.Kind != model.BrowserActivityBlockedNavigation || listed[67].Event.BlockReason == nil ||
		*listed[67].Event.BlockReason != invalidURLReason || listed[67].Event.Location == nil ||
		*listed[67].Event.Location != (model.BrowserLocation{}) || listed[67].Event.MatchedRuleID != nil ||
		listed[68].Event.Kind != model.BrowserActivityTopNavigation || listed[68].Event.MatchedRuleID == nil ||
		listed[69].Event.Kind != model.BrowserActivityClosed || listed[69].Event.Location != nil ||
		listed[69].Event.MatchedRuleID != nil || listed[69].Event.BlockReason != nil {
		t.Fatalf("listed Browser Activity = %#v", listed)
	}
	page, err := ss.ExamAttempt().ListBrowserActivity(ctx, store.BrowserActivityListOptions{
		ExamID: fixture.examID, SittingID: fixture.sitting.ID, AttemptID: connected.Attempt.ID,
		AfterReceivedAt: listed[0].Event.ReceivedAt, AfterSourceID: listed[0].SourceSessionID,
		AfterSequence: listed[0].Event.Sequence, Limit: 1,
	})
	requireNoError(t, err)
	if len(page) != 1 || page[0].SourceSessionID != listed[1].SourceSessionID || page[0].Event.Sequence != listed[1].Event.Sequence {
		t.Fatalf("Browser Activity keyset page = %#v, all=%#v", page, listed[:2])
	}

	replaced, err := startBrowserSourceFixture(t, ctx, ss, &store.BrowserActivitySourceStart{
		Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		SourceSessionID: replacementID, Transition: model.BrowserStartTransition{Kind: "runtime_reset", PredecessorSourceSessionID: sourceID, Reason: model.BrowserSourceResetCoordinatorRestarted},
	})
	requireNoError(t, err)
	assertBrowserSourceStart(t, replaced)
	_, err = appendBrowserActivityFixture(t, ctx, ss, &store.BrowserActivityAppend{
		Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		SourceSessionID: sourceID, Events: []model.BrowserActivityEvent{browserActivityEvent(71, model.BrowserActivityOpened, fixture.revisionID)},
	})
	assertExamAttemptConflict(t, err, "browser_source_fence")
	replacementEvents := []model.BrowserActivityEvent{
		browserActivityEvent(1, model.BrowserActivityOpened, fixture.revisionID),
		browserActivityEvent(2, model.BrowserActivityClosed, fixture.revisionID),
	}
	_, err = appendBrowserActivityFixture(t, ctx, ss, &store.BrowserActivityAppend{
		Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		SourceSessionID: replacementID, Events: replacementEvents,
	})
	requireNoError(t, err)
	sealAccess := store.ExamSubmissionSealAccess{AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID,
		Generation: connected.Participation.Generation, ConnectionID: connected.Connection.ID,
		CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID,
		ContinuityCredentialHash: access.ContinuityCredentialHash, ExpectedCurrentRevisionID: fixture.revisionID,
		ExpectedWorkspaceCursor: connected.Workspace.Cursor,
	}
	seal := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: sealAccess,
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
	attachSubmissionReceipt(t, fixture.candidate, seal)
	sealed, err := ss.ExamSubmission().Seal(ctx, seal,
		examCommand(fixture.candidate.ID, store.ExamSubmissionSealOperation, "browser-activity-seal", "browser-activity-seal"))
	requireNoError(t, err)
	header, err := ss.ExamSubmission().Get(ctx, sealed.Receipt.SubmissionID)
	requireNoError(t, err)
	if header.BrowserActivity.State != "pending" || header.BrowserActivity.SourceCount != 2 || header.BrowserActivity.PendingSourceCount != 2 || header.BrowserActivity.IncompleteSourceCount != 2 || header.IntegrityState != model.SubmissionIntegritySettled ||
		header.UnresolvedIntegrityCount != 0 {
		t.Fatalf("Browser Activity Submission = %#v", header)
	}
	discrepancies, err := ss.ExamIntegrityReview().ListDiscrepancies(ctx, store.ExamIntegrityDiscrepancyListOptions{
		SubmissionID: header.ID, Limit: store.ExamIntegrityReviewDiscrepancyReadMaximum,
	})
	requireNoError(t, err)
	if len(discrepancies.Items) != 0 {
		t.Fatalf("browser delivery uncertainty became immutable evidence: %#v", discrepancies)
	}
}

func testCompleteBrowserActivitySubmission(t *testing.T, ss store.Store) {
	t.Helper()
	ctx := context.Background()
	fixture, connected, access := newBrowserActivityFixture(t, ctx, ss, "browser-activity-complete")
	sourceID := browserSourceID(20)
	_, err := startBrowserSourceFixture(t, ctx, ss, &store.BrowserActivitySourceStart{Access: access,
		ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: sourceID})
	requireNoError(t, err)
	_, err = appendBrowserActivityFixture(t, ctx, ss, &store.BrowserActivityAppend{Access: access,
		ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: sourceID,
		Events: []model.BrowserActivityEvent{browserActivityEvent(1, model.BrowserActivityOpened, fixture.revisionID)}})
	requireNoError(t, err)

	sealAccess := store.ExamSubmissionSealAccess{AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, ConnectionID: connected.Connection.ID, CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID, ContinuityCredentialHash: access.ContinuityCredentialHash, ExpectedCurrentRevisionID: fixture.revisionID, ExpectedWorkspaceCursor: connected.Workspace.Cursor}
	input := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: sealAccess, AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
	attachSubmissionReceipt(t, fixture.candidate, input)
	sealed, err := ss.ExamSubmission().Seal(ctx, input, examCommand(fixture.candidate.ID, store.ExamSubmissionSealOperation, "browser-complete-submit", "browser-complete-submit"))
	requireNoError(t, err)
	header, err := ss.ExamSubmission().Get(ctx, sealed.Receipt.SubmissionID)
	requireNoError(t, err)
	if header.BrowserActivity.State != "pending" || header.BrowserActivity.SourceCount != 1 {
		t.Fatalf("submit must close without waiting for browser_closed: %#v", header)
	}
	initialRevision := header.BrowserActivity.InventoryRevision
	historical := access
	historical.ConnectionID = ""
	historical.ContinuityCredentialHash = ""
	selector := store.BrowserDeliveryAccess{Access: historical, SourceSessionID: sourceID, ParticipationID: connected.Participation.ID}
	final, err := ss.ExamAttempt().SealBrowserDelivery(ctx, &store.BrowserDeliveryFinalDeclaration{Access: selector, Declaration: model.FinalDeliveryDeclaration{DeclarationID: "browser-complete-final", FinalSequence: 2}, AuditEventID: browserDeliveryAuditFixture(t, ctx, ss, selector), AuditAt: model.GetMillis()}, examCommand(fixture.candidate.ID, store.BrowserDeliveryFinalOperation, "browser-complete-final", "browser-complete-final"))
	requireNoError(t, err)
	_, err = ss.ExamAttempt().AppendHistoricalBrowserDelivery(ctx, &store.BrowserActivityAppend{Access: historical, SourceSessionID: sourceID, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, PolicyRevisionID: final.PolicyRevisionID, PolicyDigest: final.PolicyDigest, Events: []model.BrowserActivityEvent{browserActivityEvent(2, model.BrowserActivityClosed, fixture.revisionID)}, AuditEventID: browserDeliveryAuditFixture(t, ctx, ss, selector), AuditAt: model.GetMillis()}, examCommand(fixture.candidate.ID, store.BrowserDeliveryAppendOperation, "browser-complete-tail", "browser-complete-tail"))
	requireNoError(t, err)
	after, err := ss.ExamSubmission().Get(ctx, sealed.Receipt.SubmissionID)
	requireNoError(t, err)
	if after.BrowserActivity.State != "settled" || after.BrowserActivity.PendingSourceCount != 0 || after.BrowserActivity.IncompleteSourceCount != 0 || after.BrowserActivity.InventoryRevision <= initialRevision || after.ManifestDigest != header.ManifestDigest || !after.SubmittedAt.Equal(header.SubmittedAt) {
		t.Fatalf("late collection changed immutable submission or lost settlement: before=%#v after=%#v", header, after)
	}
}

func testBrowserActivitySourceLimit(t *testing.T, ss store.Store) {
	t.Helper()
	ctx := context.Background()
	f, connected, access := newBrowserActivityFixture(t, ctx, ss, "browser-activity-source-limit")
	current := browserSourceID(100)
	_, err := startBrowserSourceFixture(t, ctx, ss, &store.BrowserActivitySourceStart{Access: access,
		ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: current})
	requireNoError(t, err)
	for index := 1; index <= model.BrowserRuntimeResetStartLimit; index++ {
		next := browserSourceID(100 + index)
		_, err = startBrowserSourceFixture(t, ctx, ss, &store.BrowserActivitySourceStart{Access: access,
			ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
			SourceSessionID: next, Transition: model.BrowserStartTransition{Kind: "runtime_reset", PredecessorSourceSessionID: current, Reason: model.BrowserSourceResetSpoolUnavailable}})
		requireNoError(t, err)
		current = next
	}
	refusedInput := &store.BrowserActivitySourceStart{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: browserSourceID(100 + model.BrowserSourceMaximumPerParticipation), Transition: model.BrowserStartTransition{Kind: "runtime_reset", PredecessorSourceSessionID: current, Reason: model.BrowserSourceResetSourceCorrupt}}
	_, err = startBrowserSourceFixture(t, ctx, ss, refusedInput)
	var refusal *store.BrowserSourceRefusal
	if !errors.As(err, &refusal) || refusal.Code != "exam.browser.source_budget_exhausted" || refusal.Status.Closure.CloseReason == nil || *refusal.Status.Closure.CloseReason != model.DeliveryClosedRuntimeReset || refusal.Status.RemainingRuntimeResetStarts != 0 {
		t.Fatalf("reset refusal did not retain closed predecessor: %#v, %v", refusal, err)
	}
	closedAt := *refusal.Status.Closure.ClosedAt
	_, err = startBrowserSourceFixture(t, ctx, ss, refusedInput)
	var replay *store.BrowserSourceRefusal
	if !errors.As(err, &replay) || !replay.Status.Closure.ClosedAt.Equal(closedAt) || replay.Status.RemainingRuntimeResetStarts != 0 {
		t.Fatalf("refusal replay changed closure: %#v, %v", replay, err)
	}
	altered := *refusedInput
	altered.Transition.Reason = model.BrowserSourceResetCoordinatorRestarted
	_, err = startBrowserSourceFixture(t, ctx, ss, &altered)
	assertExamAttemptConflict(t, err, "browser_source_conflict")
	altered = *refusedInput
	altered.SourceSessionID = browserSourceID(999)
	_, err = startBrowserSourceFixture(t, ctx, ss, &altered)
	assertExamAttemptConflict(t, err, "browser_source_predecessor")
	status, err := ss.ExamAttempt().BrowserSourceStatus(ctx, store.BrowserDeliveryAccess{Access: access, SourceSessionID: current})
	requireNoError(t, err)
	if !status.Closure.ClosedAt.Equal(closedAt) {
		t.Fatal("lost response recovery moved deadline")
	}
	statuses, err := ss.ExamAttempt().BrowserSourceList(ctx, store.BrowserDeliveryAccess{Access: access, ParticipationID: connected.Participation.ID})
	requireNoError(t, err)
	if len(statuses) != 17 || statuses[0].SourceSessionID != browserSourceID(100) || statuses[16].SourceSessionID != current {
		t.Fatalf("source ledger = %#v", statuses)
	}
	presentation, err := ss.ExamAttempt().GetCandidatePresentation(ctx, access)
	requireNoError(t, err)
	if presentation.RuntimeCapabilities.Browser.State != store.CandidateBrowserTemporarilyUnavailable || !presentation.RuntimeCapabilities.WorkspaceMutationAllowed || !presentation.RuntimeCapabilities.SubmissionAllowed {
		t.Fatalf("reset refusal affected independent capabilities: %#v", presentation.RuntimeCapabilities)
	}
	// Exhausting runtime resets does not spend correction starts. A genuine
	// later policy revision recovers the Browser and may use all 32 successors.
	renewal := &store.ExamAttemptParticipationRenewal{DesktopCompatibilityPolicyRevision: 1, AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID, ConnectionID: connected.Connection.ID, CandidateUserID: f.candidate.ID, SessionID: f.session.ID, Generation: connected.Participation.Generation, DesktopRegistrationID: f.session.DesktopRegistrationID, DPoPKeyThumbprint: f.session.DPoPKeyThumbprint, ContinuityCredentialHash: access.ContinuityCredentialHash}
	prepareParticipationRenewalCoverage(t, ctx, ss, renewal)
	for index := 1; index <= model.BrowserCorrectionStartLimit; index++ {
		renewal.Sequence++
		_, err = ss.ExamAttempt().RenewParticipation(ctx, renewal)
		requireNoError(t, err)
		policy, err := model.NewBrowserPolicy(true, "start", []model.BrowserPolicyRule{{RuleID: "start", Origin: "https://example.edu", PathPrefix: fmt.Sprintf("/revision-%d", index), HostMatch: model.BrowserPolicyHostExact, AllowRedirects: true, BlockedNavigationOutcome: model.BrowserPolicyBlockedNavigationRecord}})
		requireNoError(t, err)
		key := fmt.Sprintf("source-capacity-correction-%d", index)
		corrected, err := ss.ExamCorrection().Apply(ctx, &store.ExamCorrectionApplication{RevisionID: model.NewExamRevisionID(), ExamID: f.examID, SittingID: f.sitting.ID, CurrentRevisionID: f.revisionID, ExpectedSittingRevision: f.sitting.Revision, ActorUserID: f.manager.ID, Resources: []store.ExamCorrectionResourceManifestItem{}, BrowserPolicy: &policy, CandidateSummary: "Updated browser references", AffectedCapabilities: []model.CandidateCapability{model.CandidateCapabilityBrowser}, PrivateReason: "Verify independent Browser source budgets", AppliedAt: model.NowUTC(), AuditEventID: saveExamSittingAudit(t, ctx, ss, f.manager.ID, f.examID, f.unitID).ID.String(), AuditAt: model.GetMillis()}, examCommand(f.manager.ID, "exam.sitting.correction.apply.v1", key, key))
		requireNoError(t, err)
		f.revisionID, f.sitting = corrected.Revision.ID, corrected.Sitting.Sitting
		next := browserSourceID(200 + index)
		successor, err := startBrowserSourceFixture(t, ctx, ss, &store.BrowserActivitySourceStart{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: next, Transition: model.BrowserStartTransition{Kind: "policy_correction", PredecessorSourceSessionID: current}})
		requireNoError(t, err)
		if successor.RemainingRuntimeResetStarts != 0 || successor.RemainingCorrectionStarts != int64(model.BrowserCorrectionStartLimit-index) || successor.Closure.ClosedAt != nil {
			t.Fatal("correction successor lost independent lifetime counters")
		}
		current = next
	}
	statuses, err = ss.ExamAttempt().BrowserSourceList(ctx, store.BrowserDeliveryAccess{Access: access, ParticipationID: connected.Participation.ID})
	requireNoError(t, err)
	if len(statuses) != model.BrowserSourceMaximumPerParticipation {
		t.Fatalf("mixed source budget retained %d sources; want 49", len(statuses))
	}
	budget, err := ss.ExamAttempt().DeliveryBudget(ctx, store.DeliveryBudgetAccess{Access: access, ParticipationID: connected.Participation.ID})
	requireNoError(t, err)
	if budget.ControlMetadataBytes != model.NativeOwnerReservationBytes+49*model.BrowserOwnerReservationBytes {
		t.Fatal("empty source reservation was omitted or refused starts were charged")
	}
	foreign := access
	foreign.CandidateUserID = model.NewUserID()
	_, err = ss.ExamAttempt().BrowserSourceStatus(ctx, store.BrowserDeliveryAccess{Access: foreign, SourceSessionID: current})
	var missing *store.ErrNotFound
	if !errors.As(err, &missing) {
		t.Fatalf("foreign source visible: %v", err)
	}
}

func newBrowserActivityFixture(t *testing.T, ctx context.Context, ss store.Store,
	key string,
) (examAttemptFixture, *store.ExamAttemptConnectResult, store.CandidateAttemptAccess) {
	t.Helper()
	policy, err := model.NewBrowserPolicy(true, "start", []model.BrowserPolicyRule{{RuleID: "start",
		Origin: "https://example.edu", PathPrefix: "/", HostMatch: model.BrowserPolicyHostExact,
		AllowRedirects: true, BlockedNavigationOutcome: model.BrowserPolicyBlockedNavigationRecord}})
	requireNoError(t, err)
	fixture := newExamAttemptFixtureWithBrowserPolicy(t, ctx, ss, &policy)
	connected, focusAccess := connectFocusLossFixture(t, ctx, ss, fixture, key)
	access := store.CandidateAttemptAccess{AttemptID: connected.Attempt.ID, CandidateUserID: fixture.candidate.ID,
		SessionID: fixture.session.ID, DesktopRegistrationID: fixture.session.DesktopRegistrationID,
		DPoPKeyThumbprint: fixture.session.DPoPKeyThumbprint, ConnectionID: connected.Connection.ID,
		ContinuityCredentialHash: focusAccess.ContinuityCredentialHash}
	return fixture, connected, access
}

func browserSourceID(value int) model.BrowserSourceSessionID {
	return model.BrowserSourceSessionID(fmt.Sprintf("00000000-0000-4000-8000-%012x", value))
}

func browserActivityClientTime(sequence int64) time.Time {
	return time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC).Add(time.Duration(sequence) * time.Millisecond)
}

func browserActivityEvent(sequence int64, kind model.BrowserActivityKind,
	revisionID model.ExamRevisionID,
) model.BrowserActivityEvent {
	return model.BrowserActivityEvent{Sequence: sequence, Kind: kind, PolicyRevisionID: revisionID,
		ClientOccurredAt: browserActivityClientTime(sequence)}
}

func assertBrowserActivityAcknowledgement(t *testing.T, value *model.BrowserActivityAcknowledgement,
	sourceID model.BrowserSourceSessionID, contiguous, seen int64, missing []model.BrowserActivityMissingRange, truncated bool,
) {
	t.Helper()
	if value == nil || value.SourceSessionID != sourceID || value.HighestContiguous != contiguous || value.HighestSeen != seen ||
		value.MissingRangesTruncated != truncated || value.ServerTime.IsZero() || len(value.MissingRanges) != len(missing) {
		t.Fatalf("Browser Activity acknowledgement = %#v", value)
	}
	for index := range missing {
		if value.MissingRanges[index] != missing[index] {
			t.Fatalf("Browser Activity acknowledgement = %#v, missing=%#v", value, missing)
		}
	}
}

func startBrowserSourceFixture(t *testing.T, ctx context.Context, ss store.Store, input *store.BrowserActivitySourceStart) (*model.BrowserSourceStatus, error) {
	t.Helper()
	presentation, err := ss.ExamAttempt().GetCandidatePresentation(ctx, input.Access)
	if err != nil {
		return nil, err
	}
	input.PolicyRevisionID = presentation.RuntimeCapabilities.Browser.PolicyRevisionID
	input.PolicyDigest = presentation.RuntimeCapabilities.Browser.PolicyDigest
	if input.Transition.Kind == "" {
		input.Transition.Kind = "initial"
	}
	target, err := ss.ExamAttempt().ResolveLiveDeliveryTarget(ctx, input.Access)
	if err != nil {
		return nil, err
	}
	audit, err := ss.Audit().Save(ctx, &model.AuditEvent{ActorID: input.Access.CandidateUserID, Action: string(model.ActionExamSittingParticipate), Resource: model.Resource{Type: model.ResourceExamSitting, ID: target.SittingID.String()}, ScopeType: model.RoleScopeClass, ScopeID: target.ClassID.String(), Status: model.AuditStatusAttempt, NodeID: "test-node"})
	requireNoError(t, err)
	input.AuditEventID = audit.ID.String()
	input.AuditAt = model.GetMillis()
	return ss.ExamAttempt().StartBrowserActivity(ctx, input)
}
func assertBrowserSourceStart(t *testing.T, status *model.BrowserSourceStatus) {
	t.Helper()
	if status == nil || status.Validate() != nil || status.HighestSeen != 0 || status.HighestContiguous != 0 || status.Closure.ClosedAt != nil {
		t.Fatalf("new Browser source status: %#v", status)
	}
}

func appendBrowserActivityFixture(t *testing.T, ctx context.Context, ss store.Store, input *store.BrowserActivityAppend) (*model.BrowserActivityAcknowledgement, error) {
	t.Helper()
	status, err := ss.ExamAttempt().BrowserSourceStatus(ctx, store.BrowserDeliveryAccess{Access: input.Access, SourceSessionID: input.SourceSessionID})
	if err != nil {
		return nil, err
	}
	input.PolicyRevisionID = status.PolicyRevisionID
	input.PolicyDigest = status.PolicyDigest
	target, err := ss.ExamAttempt().ResolveLiveDeliveryTarget(ctx, input.Access)
	if err != nil {
		return nil, err
	}
	audit, err := ss.Audit().Save(ctx, &model.AuditEvent{ActorID: input.Access.CandidateUserID, Action: string(model.ActionExamSittingParticipate), Resource: model.Resource{Type: model.ResourceExamSitting, ID: target.SittingID.String()}, ScopeType: model.RoleScopeClass, ScopeID: target.ClassID.String(), Status: model.AuditStatusAttempt, NodeID: "test-node"})
	requireNoError(t, err)
	input.AuditEventID = audit.ID.String()
	input.AuditAt = model.GetMillis()
	return ss.ExamAttempt().AppendBrowserActivity(ctx, input)
}
