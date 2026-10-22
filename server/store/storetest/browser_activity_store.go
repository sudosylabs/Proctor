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
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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
	require.NoError(t, err)
	assertBrowserSourceStart(t, started)
	replayedStart, err := startBrowserSourceFixture(t, ctx, ss, &store.BrowserActivitySourceStart{
		Access: access, ParticipationID: connected.Participation.ID,
		Generation: connected.Participation.Generation, SourceSessionID: sourceID,
	})
	require.NoError(t, err)
	assertBrowserSourceStart(t, replayedStart)
	_, err = startBrowserSourceFixture(t, ctx, ss, &store.BrowserActivitySourceStart{
		Access: access, ParticipationID: connected.Participation.ID,
		Generation: connected.Participation.Generation, SourceSessionID: browserSourceID(2),
	})
	requireConflictConstraint(t, err, "browser_source_current")

	evenEvents := make([]model.BrowserActivityEvent, 0, 33)
	for sequence := int64(2); sequence <= 66; sequence += 2 {
		evenEvents = append(evenEvents, browserActivityEvent(sequence, model.BrowserActivityOpened, fixture.revisionID))
	}
	acknowledgement, err := appendBrowserActivityFixture(t, ctx, ss, &store.BrowserActivityAppend{
		Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		SourceSessionID: sourceID, Events: evenEvents,
	})
	require.NoError(t, err)
	require.Equal(t, int64(0), acknowledgement.HighestContiguous)
	require.Equal(t, int64(66), acknowledgement.HighestSeen)
	require.Len(t, acknowledgement.MissingRanges, model.BrowserActivityMaximumMissingRanges)
	require.True(t, acknowledgement.MissingRangesTruncated)
	require.Equal(t, model.BrowserActivityMissingRange{First: 1, Last: 1}, acknowledgement.MissingRanges[0])
	require.Equal(t, model.BrowserActivityMissingRange{First: 63, Last: 63}, acknowledgement.MissingRanges[31])
	duplicate, err := appendBrowserActivityFixture(t, ctx, ss, &store.BrowserActivityAppend{
		Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		SourceSessionID: sourceID, Events: evenEvents,
	})
	require.NoError(t, err)
	require.Equal(t, acknowledgement.HighestContiguous, duplicate.HighestContiguous, "duplicate Browser Activity acknowledgement = %#v, first=%#v", duplicate, acknowledgement)
	require.Equal(t, acknowledgement.HighestSeen, duplicate.HighestSeen, "duplicate Browser Activity acknowledgement = %#v, first=%#v", duplicate, acknowledgement)
	require.Len(t, duplicate.MissingRanges, len(acknowledgement.MissingRanges), "duplicate Browser Activity acknowledgement = %#v, first=%#v", duplicate, acknowledgement)
	require.True(t, duplicate.MissingRangesTruncated, "duplicate Browser Activity acknowledgement = %#v, first=%#v", duplicate, acknowledgement)
	changed := browserActivityEvent(2, model.BrowserActivityClosed, fixture.revisionID)
	_, err = appendBrowserActivityFixture(t, ctx, ss, &store.BrowserActivityAppend{
		Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		SourceSessionID: sourceID, Events: []model.BrowserActivityEvent{changed},
	})
	requireConflictConstraint(t, err, "browser_activity_sequence")
	tooFar := browserActivityEvent(model.BrowserActivityMaximumReorderWindow+1, model.BrowserActivityOpened, fixture.revisionID)
	_, err = appendBrowserActivityFixture(t, ctx, ss, &store.BrowserActivityAppend{
		Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		SourceSessionID: sourceID, Events: []model.BrowserActivityEvent{tooFar},
	})
	requireConflictConstraint(t, err, "replay_window_exceeded")

	oddEvents := make([]model.BrowserActivityEvent, 0, 33)
	for sequence := int64(1); sequence <= 65; sequence += 2 {
		oddEvents = append(oddEvents, browserActivityEvent(sequence, model.BrowserActivityOpened, fixture.revisionID))
	}
	acknowledgement, err = appendBrowserActivityFixture(t, ctx, ss, &store.BrowserActivityAppend{
		Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		SourceSessionID: sourceID, Events: oddEvents,
	})
	require.NoError(t, err)
	assertBrowserActivityAcknowledgement(t, acknowledgement, sourceID, 66, 66, nil, false)

	paused, err := ss.ExamSitting().Pause(ctx, &store.ExamSittingManagerTransition{ExamID: fixture.examID,
		SittingID: fixture.sitting.ID, ActorUserID: fixture.manager.ID, ExpectedRevision: fixture.sitting.Revision,
		PrivateReason: "prove Browser Activity is fenced while paused", ChangedAt: model.NowUTC(),
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.manager.ID, fixture.examID, fixture.unitID).ID, AuditAt: model.NowUTC()},
		examCommand(fixture.manager.ID, "exam.sitting.pause", "browser-activity-pause", "browser-activity-pause"))
	require.NoError(t, err)
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
	require.NoError(t, err)
	assertBrowserActivityAcknowledgement(t, pausedAcknowledgement, sourceID, 70, 70, nil, false)
	replacementID := browserSourceID(2)
	_, err = startBrowserSourceFixture(t, ctx, ss, &store.BrowserActivitySourceStart{
		Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		SourceSessionID: replacementID, Transition: model.BrowserStartTransition{Kind: "runtime_reset", PredecessorSourceSessionID: sourceID, Reason: model.BrowserSourceResetCoordinatorRestarted},
	})
	requireConflictConstraint(t, err, "exam_sitting_state")
	resumed, err := ss.ExamSitting().Resume(ctx, &store.ExamSittingManagerTransition{ExamID: fixture.examID,
		SittingID: fixture.sitting.ID, ActorUserID: fixture.manager.ID, ExpectedRevision: paused.Value.Sitting.Revision,
		PrivateReason: "finish Browser Activity conformance", ChangedAt: model.NowUTC(),
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.manager.ID, fixture.examID, fixture.unitID).ID, AuditAt: model.NowUTC()},
		examCommand(fixture.manager.ID, "exam.sitting.resume", "browser-activity-resume", "browser-activity-resume"))
	require.NoError(t, err)
	require.Equal(t, model.ExamSittingOpen, resumed.Value.Sitting.State)
	acknowledgement, err = appendBrowserActivityFixture(t, ctx, ss, &store.BrowserActivityAppend{
		Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		SourceSessionID: sourceID, Events: finalEvents,
	})
	require.NoError(t, err)
	assertBrowserActivityAcknowledgement(t, acknowledgement, sourceID, 70, 70, nil, false)

	listed, err := ss.ExamAttempt().ListBrowserActivity(ctx, store.BrowserActivityListOptions{
		ExamID: fixture.examID, SittingID: fixture.sitting.ID, AttemptID: connected.Attempt.ID, Limit: 201,
	})
	require.NoError(t, err)
	require.Len(t, listed, 70)
	require.Equal(t, model.BrowserActivityBlockedNavigation, listed[66].Event.Kind)
	require.NotNil(t, listed[66].Event.BlockReason)
	require.Equal(t, schemeBlockedReason, *listed[66].Event.BlockReason)
	require.NotNil(t, listed[66].Event.Location)
	require.Equal(t, "http", listed[66].Event.Location.Scheme)
	require.Nil(t, listed[66].Event.MatchedRuleID)
	require.Equal(t, model.BrowserActivityBlockedNavigation, listed[67].Event.Kind)
	require.NotNil(t, listed[67].Event.BlockReason)
	require.Equal(t, invalidURLReason, *listed[67].Event.BlockReason)
	require.NotNil(t, listed[67].Event.Location)
	require.Equal(t, model.BrowserLocation{}, *listed[67].Event.Location)
	require.Nil(t, listed[67].Event.MatchedRuleID)
	require.Equal(t, model.BrowserActivityTopNavigation, listed[68].Event.Kind)
	require.NotNil(t, listed[68].Event.MatchedRuleID)
	require.Equal(t, model.BrowserActivityClosed, listed[69].Event.Kind)
	require.Nil(t, listed[69].Event.Location)
	require.Nil(t, listed[69].Event.MatchedRuleID)
	require.Nil(t, listed[69].Event.BlockReason)
	page, err := ss.ExamAttempt().ListBrowserActivity(ctx, store.BrowserActivityListOptions{
		ExamID: fixture.examID, SittingID: fixture.sitting.ID, AttemptID: connected.Attempt.ID,
		AfterReceivedAt: listed[0].Event.ReceivedAt, AfterSourceID: listed[0].SourceSessionID,
		AfterSequence: listed[0].Event.Sequence, Limit: 1,
	})
	require.NoError(t, err)
	require.Len(t, page, 1, "Browser Activity keyset page")
	require.Equal(t, listed[1].SourceSessionID, page[0].SourceSessionID, "Browser Activity keyset page")
	require.Equal(t, listed[1].Event.Sequence, page[0].Event.Sequence, "Browser Activity keyset page")

	replaced, err := startBrowserSourceFixture(t, ctx, ss, &store.BrowserActivitySourceStart{
		Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		SourceSessionID: replacementID, Transition: model.BrowserStartTransition{Kind: "runtime_reset", PredecessorSourceSessionID: sourceID, Reason: model.BrowserSourceResetCoordinatorRestarted},
	})
	require.NoError(t, err)
	assertBrowserSourceStart(t, replaced)
	_, err = appendBrowserActivityFixture(t, ctx, ss, &store.BrowserActivityAppend{
		Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		SourceSessionID: sourceID, Events: []model.BrowserActivityEvent{browserActivityEvent(71, model.BrowserActivityOpened, fixture.revisionID)},
	})
	requireConflictConstraint(t, err, "browser_source_fence")
	replacementEvents := []model.BrowserActivityEvent{
		browserActivityEvent(1, model.BrowserActivityOpened, fixture.revisionID),
		browserActivityEvent(2, model.BrowserActivityClosed, fixture.revisionID),
	}
	_, err = appendBrowserActivityFixture(t, ctx, ss, &store.BrowserActivityAppend{
		Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		SourceSessionID: replacementID, Events: replacementEvents,
	})
	require.NoError(t, err)
	sealAccess := store.ExamSubmissionSealAccess{AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID,
		Generation: connected.Participation.Generation, ConnectionID: connected.Connection.ID,
		CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID,
		ContinuityCredentialHash: access.ContinuityCredentialHash, ExpectedCurrentRevisionID: fixture.revisionID,
		ExpectedWorkspaceCursor: connected.Workspace.Cursor,
	}
	seal := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: sealAccess,
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.NowUTC()}
	attachSubmissionReceipt(t, ctx, ss, fixture.candidate, seal)
	sealed, err := ss.ExamSubmission().Seal(ctx, seal,
		examCommand(fixture.candidate.ID, store.ExamSubmissionSealOperation, "browser-activity-seal", "browser-activity-seal"))
	require.NoError(t, err)
	header, err := ss.ExamSubmission().Get(ctx, sealed.Receipt.SubmissionID)
	require.NoError(t, err)
	require.Equal(t, "pending", header.BrowserActivity.State)
	require.Equal(t, int64(2), header.BrowserActivity.SourceCount)
	require.Equal(t, int64(2), header.BrowserActivity.PendingSourceCount)
	require.Equal(t, int64(2), header.BrowserActivity.IncompleteSourceCount)
	require.Equal(t, model.SubmissionIntegritySettled, header.IntegrityState)
	require.Equal(t, int64(0), header.UnresolvedIntegrityCount)
	discrepancies, err := ss.ExamIntegrityReview().ListDiscrepancies(ctx, store.ExamIntegrityDiscrepancyListOptions{
		SubmissionID: header.ID, Limit: store.ExamIntegrityReviewDiscrepancyReadMaximum,
	})
	require.NoError(t, err)
	require.Empty(t, discrepancies.Items)
}

func testCompleteBrowserActivitySubmission(t *testing.T, ss store.Store) {
	t.Helper()
	ctx := context.Background()
	fixture, connected, access := newBrowserActivityFixture(t, ctx, ss, "browser-activity-complete")
	sourceID := browserSourceID(20)
	_, err := startBrowserSourceFixture(t, ctx, ss, &store.BrowserActivitySourceStart{Access: access,
		ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: sourceID})
	require.NoError(t, err)
	_, err = appendBrowserActivityFixture(t, ctx, ss, &store.BrowserActivityAppend{Access: access,
		ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: sourceID,
		Events: []model.BrowserActivityEvent{browserActivityEvent(1, model.BrowserActivityOpened, fixture.revisionID)}})
	require.NoError(t, err)

	sealAccess := store.ExamSubmissionSealAccess{AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, ConnectionID: connected.Connection.ID, CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID, ContinuityCredentialHash: access.ContinuityCredentialHash, ExpectedCurrentRevisionID: fixture.revisionID, ExpectedWorkspaceCursor: connected.Workspace.Cursor}
	input := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: sealAccess, AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.NowUTC()}
	attachSubmissionReceipt(t, ctx, ss, fixture.candidate, input)
	sealed, err := ss.ExamSubmission().Seal(ctx, input, examCommand(fixture.candidate.ID, store.ExamSubmissionSealOperation, "browser-complete-submit", "browser-complete-submit"))
	require.NoError(t, err)
	header, err := ss.ExamSubmission().Get(ctx, sealed.Receipt.SubmissionID)
	require.NoError(t, err)
	require.Equal(t, "pending", header.BrowserActivity.State)
	require.Equal(t, int64(1), header.BrowserActivity.SourceCount)
	initialRevision := header.BrowserActivity.InventoryRevision
	historical := access
	historical.ConnectionID = ""
	historical.ContinuityCredentialHash = ""
	selector := store.BrowserDeliveryAccess{Access: historical, SourceSessionID: sourceID, ParticipationID: connected.Participation.ID}
	final, err := ss.ExamAttempt().SealBrowserDelivery(ctx, &store.BrowserDeliveryFinalDeclaration{Access: selector, Declaration: model.FinalDeliveryDeclaration{DeclarationID: "browser-complete-final", FinalSequence: 2}, AuditEventID: browserDeliveryAuditFixture(t, ctx, ss, selector), AuditAt: model.NowUTC()}, examCommand(fixture.candidate.ID, store.BrowserDeliveryFinalOperation, "browser-complete-final", "browser-complete-final"))
	require.NoError(t, err)
	_, err = ss.ExamAttempt().AppendHistoricalBrowserDelivery(ctx, &store.BrowserActivityAppend{Access: historical, SourceSessionID: sourceID, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, PolicyRevisionID: final.PolicyRevisionID, PolicyDigest: final.PolicyDigest, Events: []model.BrowserActivityEvent{browserActivityEvent(2, model.BrowserActivityClosed, fixture.revisionID)}, AuditEventID: browserDeliveryAuditFixture(t, ctx, ss, selector), AuditAt: model.NowUTC()}, examCommand(fixture.candidate.ID, store.BrowserDeliveryAppendOperation, "browser-complete-tail", "browser-complete-tail"))
	require.NoError(t, err)
	after, err := ss.ExamSubmission().Get(ctx, sealed.Receipt.SubmissionID)
	require.NoError(t, err)
	require.Equal(t, "settled", after.BrowserActivity.State, "late collection changed immutable submission or lost settlement: before=%#v after=%#v", header, after)
	require.Equal(t, int64(0), after.BrowserActivity.PendingSourceCount, "late collection changed immutable submission or lost settlement: before=%#v after=%#v", header, after)
	require.Equal(t, int64(0), after.BrowserActivity.IncompleteSourceCount, "late collection changed immutable submission or lost settlement: before=%#v after=%#v", header, after)
	require.Greater(t, after.BrowserActivity.InventoryRevision, initialRevision, "late collection changed immutable submission or lost settlement: before=%#v after=%#v", header, after)
	require.Equal(t, header.ManifestDigest, after.ManifestDigest, "late collection changed immutable submission or lost settlement: before=%#v after=%#v", header, after)
	require.True(t, after.SubmittedAt.Equal(header.SubmittedAt), "late collection changed immutable submission or lost settlement: before=%#v after=%#v", header, after)
}

func testBrowserActivitySourceLimit(t *testing.T, ss store.Store) {
	t.Helper()
	ctx := context.Background()
	f, connected, access := newBrowserActivityFixture(t, ctx, ss, "browser-activity-source-limit")
	current := browserSourceID(100)
	_, err := startBrowserSourceFixture(t, ctx, ss, &store.BrowserActivitySourceStart{Access: access,
		ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: current})
	require.NoError(t, err)
	for index := 1; index <= model.BrowserRuntimeResetStartLimit; index++ {
		next := browserSourceID(100 + index)
		_, err = startBrowserSourceFixture(t, ctx, ss, &store.BrowserActivitySourceStart{Access: access,
			ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
			SourceSessionID: next, Transition: model.BrowserStartTransition{Kind: "runtime_reset", PredecessorSourceSessionID: current, Reason: model.BrowserSourceResetSpoolUnavailable}})
		require.NoError(t, err)
		current = next
	}
	refusedInput := &store.BrowserActivitySourceStart{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: browserSourceID(100 + model.BrowserSourceMaximumPerParticipation), Transition: model.BrowserStartTransition{Kind: "runtime_reset", PredecessorSourceSessionID: current, Reason: model.BrowserSourceResetSourceCorrupt}}
	_, err = startBrowserSourceFixture(t, ctx, ss, refusedInput)
	var refusal *store.BrowserSourceRefusal
	require.ErrorAs(t, err, &refusal, "reset refusal did not retain closed predecessor: %#v", refusal)
	require.Equal(t, "exam.browser.source_budget_exhausted", refusal.Code, "reset refusal did not retain closed predecessor: %#v", refusal)
	require.NotNil(t, refusal.Status.Closure.CloseReason, "reset refusal did not retain closed predecessor: %#v", refusal)
	require.Equal(t, model.DeliveryClosedRuntimeReset, *refusal.Status.Closure.CloseReason, "reset refusal did not retain closed predecessor: %#v", refusal)
	require.Equal(t, int64(0), refusal.Status.RemainingRuntimeResetStarts, "reset refusal did not retain closed predecessor: %#v", refusal)
	closedAt := *refusal.Status.Closure.ClosedAt
	_, err = startBrowserSourceFixture(t, ctx, ss, refusedInput)
	var replay *store.BrowserSourceRefusal
	require.ErrorAs(t, err, &replay, "refusal replay changed closure: %#v", replay)
	require.True(t, replay.Status.Closure.ClosedAt.Equal(closedAt), "refusal replay changed closure: %#v", replay)
	require.Equal(t, int64(0), replay.Status.RemainingRuntimeResetStarts, "refusal replay changed closure: %#v", replay)
	altered := *refusedInput
	altered.Transition.Reason = model.BrowserSourceResetCoordinatorRestarted
	_, err = startBrowserSourceFixture(t, ctx, ss, &altered)
	requireConflictConstraint(t, err, "browser_source_conflict")
	altered = *refusedInput
	altered.SourceSessionID = browserSourceID(999)
	_, err = startBrowserSourceFixture(t, ctx, ss, &altered)
	requireConflictConstraint(t, err, "browser_source_predecessor")
	status, err := ss.ExamAttempt().BrowserSourceStatus(ctx, store.BrowserDeliveryAccess{Access: access, SourceSessionID: current})
	require.NoError(t, err)
	require.True(t, status.Closure.ClosedAt.Equal(closedAt), "lost response recovery moved deadline")
	statuses, err := ss.ExamAttempt().BrowserSourceList(ctx, store.BrowserDeliveryAccess{Access: access, ParticipationID: connected.Participation.ID})
	require.NoError(t, err)
	require.Len(t, statuses, 17)
	require.Equal(t, browserSourceID(100), statuses[0].SourceSessionID)
	require.Equal(t, current, statuses[16].SourceSessionID)
	presentation, err := ss.ExamAttempt().GetCandidatePresentation(ctx, access)
	require.NoError(t, err)
	require.Equal(t, store.CandidateBrowserTemporarilyUnavailable, presentation.RuntimeCapabilities.Browser.State)
	require.True(t, presentation.RuntimeCapabilities.WorkspaceMutationAllowed)
	require.True(t, presentation.RuntimeCapabilities.SubmissionAllowed)
	// Exhausting runtime resets does not spend correction starts. A genuine
	// later policy revision recovers the Browser and may use all 32 successors.
	renewal := &store.ExamAttemptParticipationRenewal{DesktopCompatibilityPolicyRevision: 1, AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID, ConnectionID: connected.Connection.ID, CandidateUserID: f.candidate.ID, SessionID: f.session.ID, Generation: connected.Participation.Generation, DesktopRegistrationID: f.session.DesktopRegistrationID, DPoPKeyThumbprint: f.session.DPoPKeyThumbprint, ContinuityCredentialHash: access.ContinuityCredentialHash}
	prepareParticipationRenewalCoverage(t, ctx, ss, renewal)
	for index := 1; index <= model.BrowserCorrectionStartLimit; index++ {
		renewal.Sequence++
		_, err = ss.ExamAttempt().RenewParticipation(ctx, renewal)
		require.NoError(t, err)
		policy, err := model.NewBrowserPolicy(true, "start", []model.BrowserPolicyRule{{RuleID: "start", Origin: "https://example.edu", PathPrefix: fmt.Sprintf("/revision-%d", index), HostMatch: model.BrowserPolicyHostExact, AllowRedirects: true, BlockedNavigationOutcome: model.BrowserPolicyBlockedNavigationRecord}})
		require.NoError(t, err)
		key := fmt.Sprintf("source-capacity-correction-%d", index)
		corrected, err := ss.ExamCorrection().Apply(ctx, &store.ExamCorrectionApplication{RevisionID: model.NewExamRevisionID(), ExamID: f.examID, SittingID: f.sitting.ID, CurrentRevisionID: f.revisionID, ExpectedSittingRevision: f.sitting.Revision, ActorUserID: f.manager.ID, Resources: []store.ExamCorrectionResourceManifestItem{}, BrowserPolicy: &policy, CandidateSummary: "Updated browser references", AffectedCapabilities: []model.CandidateCapability{model.CandidateCapabilityBrowser}, PrivateReason: "Verify independent Browser source budgets", AppliedAt: model.NowUTC(), AuditEventID: saveExamSittingAudit(t, ctx, ss, f.manager.ID, f.examID, f.unitID).ID, AuditAt: model.NowUTC()}, examCommand(f.manager.ID, "exam.sitting.correction.apply", key, key))
		require.NoError(t, err)
		f.revisionID, f.sitting = corrected.Revision.ID, corrected.Sitting.Sitting
		next := browserSourceID(200 + index)
		successor, err := startBrowserSourceFixture(t, ctx, ss, &store.BrowserActivitySourceStart{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: next, Transition: model.BrowserStartTransition{Kind: "policy_correction", PredecessorSourceSessionID: current}})
		require.NoError(t, err)
		require.Equal(t, int64(0), successor.RemainingRuntimeResetStarts, "correction successor lost independent lifetime counters")
		require.Equal(t, int64(model.BrowserCorrectionStartLimit-index), successor.RemainingCorrectionStarts, "correction successor lost independent lifetime counters")
		require.Nil(t, successor.Closure.ClosedAt, "correction successor lost independent lifetime counters")
		current = next
	}
	// Replay the original canonical command, not a helper that substitutes the
	// new policy revision. Its refusal lives on the original closed predecessor.
	refusedInput.AuditEventID = browserDeliveryAuditFixture(t, ctx, ss, store.BrowserDeliveryAccess{Access: access, SourceSessionID: refusal.Status.SourceSessionID, ParticipationID: connected.Participation.ID})
	refusedInput.AuditAt = model.NowUTC()
	_, err = ss.ExamAttempt().StartBrowserActivity(ctx, refusedInput)
	require.ErrorAs(t, err, &replay, "historical refusal replay after correction: %#v", replay)
	require.Equal(t, refusal.Status.SourceSessionID, replay.Status.SourceSessionID, "historical refusal replay after correction: %#v", replay)
	require.True(t, replay.Status.Closure.ClosedAt.Equal(closedAt), "historical refusal replay after correction: %#v", replay)
	require.Equal(t, int64(0), replay.Status.RemainingCorrectionStarts, "historical refusal replay after correction: %#v", replay)
	altered = *refusedInput
	altered.Transition.Reason = model.BrowserSourceResetCoordinatorRestarted
	_, err = ss.ExamAttempt().StartBrowserActivity(ctx, &altered)
	requireConflictConstraint(t, err, "browser_source_conflict")
	statuses, err = ss.ExamAttempt().BrowserSourceList(ctx, store.BrowserDeliveryAccess{Access: access, ParticipationID: connected.Participation.ID})
	require.NoError(t, err)
	require.Len(t, statuses, model.BrowserSourceMaximumPerParticipation, "mixed source budget retained")
	budget, err := ss.ExamAttempt().DeliveryBudget(ctx, store.DeliveryBudgetAccess{Access: access, ParticipationID: connected.Participation.ID})
	require.NoError(t, err)
	require.Equal(t, model.NativeOwnerReservationBytes+49*model.BrowserOwnerReservationBytes, budget.ControlMetadataBytes, "empty source reservation was omitted or refused starts were charged")
	foreign := access
	foreign.CandidateUserID = model.NewUserID()
	_, err = ss.ExamAttempt().BrowserSourceStatus(ctx, store.BrowserDeliveryAccess{Access: foreign, SourceSessionID: current})
	var missing *store.ErrNotFound
	require.ErrorAs(t, err, &missing)
}

func newBrowserActivityFixture(t *testing.T, ctx context.Context, ss store.Store,
	key string,
) (examAttemptFixture, *store.ExamAttemptConnectResult, store.CandidateAttemptAccess) {
	t.Helper()
	policy, err := model.NewBrowserPolicy(true, "start", []model.BrowserPolicyRule{{RuleID: "start",
		Origin: "https://example.edu", PathPrefix: "/", HostMatch: model.BrowserPolicyHostExact,
		AllowRedirects: true, BlockedNavigationOutcome: model.BrowserPolicyBlockedNavigationRecord}})
	require.NoError(t, err)
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
	require.NotNil(t, value)
	require.Equal(t, sourceID, value.SourceSessionID)
	require.Equal(t, contiguous, value.HighestContiguous)
	require.Equal(t, seen, value.HighestSeen)
	require.Equal(t, truncated, value.MissingRangesTruncated)
	require.False(t, value.ServerTime.IsZero())
	require.Len(t, value.MissingRanges, len(missing))
	for index := range missing {
		require.Equal(t, missing[index], value.MissingRanges[index], "Browser Activity acknowledgement = %#v, missing=%#v", value, missing)
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
	require.NoError(t, err)
	input.AuditEventID = audit.ID
	input.AuditAt = model.NowUTC()
	return ss.ExamAttempt().StartBrowserActivity(ctx, input)
}
func assertBrowserSourceStart(t *testing.T, status *model.BrowserSourceStatus) {
	t.Helper()
	require.NotNil(t, status)
	require.NoError(t, status.Validate())
	require.Equal(t, int64(0), status.HighestSeen)
	require.Equal(t, int64(0), status.HighestContiguous)
	require.Nil(t, status.Closure.ClosedAt)
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
	require.NoError(t, err)
	input.AuditEventID = audit.ID
	input.AuditAt = model.NowUTC()
	return ss.ExamAttempt().AppendBrowserActivity(ctx, input)
}
