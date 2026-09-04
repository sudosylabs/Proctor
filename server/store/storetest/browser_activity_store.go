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

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestBrowserActivityStore(t *testing.T, ss store.Store) {
	t.Helper()
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
	started, err := ss.ExamAttempt().StartBrowserActivity(ctx, &store.BrowserActivitySourceStart{
		Access: access, ParticipationID: connected.Participation.ID,
		Generation: connected.Participation.Generation, SourceSessionID: sourceID,
	})
	requireNoError(t, err)
	assertBrowserActivityAcknowledgement(t, started, sourceID, 0, 0, nil, false)
	replayedStart, err := ss.ExamAttempt().StartBrowserActivity(ctx, &store.BrowserActivitySourceStart{
		Access: access, ParticipationID: connected.Participation.ID,
		Generation: connected.Participation.Generation, SourceSessionID: sourceID,
	})
	requireNoError(t, err)
	assertBrowserActivityAcknowledgement(t, replayedStart, sourceID, 0, 0, nil, false)
	_, err = ss.ExamAttempt().StartBrowserActivity(ctx, &store.BrowserActivitySourceStart{
		Access: access, ParticipationID: connected.Participation.ID,
		Generation: connected.Participation.Generation, SourceSessionID: browserSourceID(2),
	})
	assertExamAttemptConflict(t, err, "browser_source_current")

	evenEvents := make([]model.BrowserActivityEvent, 0, 33)
	for sequence := int64(2); sequence <= 66; sequence += 2 {
		evenEvents = append(evenEvents, browserActivityEvent(sequence, model.BrowserActivityOpened, fixture.revisionID))
	}
	acknowledgement, err := ss.ExamAttempt().AppendBrowserActivity(ctx, &store.BrowserActivityAppend{
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
	duplicate, err := ss.ExamAttempt().AppendBrowserActivity(ctx, &store.BrowserActivityAppend{
		Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		SourceSessionID: sourceID, Events: evenEvents,
	})
	requireNoError(t, err)
	if duplicate.HighestContiguous != acknowledgement.HighestContiguous || duplicate.HighestSeen != acknowledgement.HighestSeen ||
		len(duplicate.MissingRanges) != len(acknowledgement.MissingRanges) || !duplicate.MissingRangesTruncated {
		t.Fatalf("duplicate Browser Activity acknowledgement = %#v, first=%#v", duplicate, acknowledgement)
	}
	changed := browserActivityEvent(2, model.BrowserActivityClosed, fixture.revisionID)
	_, err = ss.ExamAttempt().AppendBrowserActivity(ctx, &store.BrowserActivityAppend{
		Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		SourceSessionID: sourceID, Events: []model.BrowserActivityEvent{changed},
	})
	assertExamAttemptConflict(t, err, "browser_activity_sequence")
	tooFar := browserActivityEvent(model.BrowserActivityMaximumReorderWindow+1, model.BrowserActivityOpened, fixture.revisionID)
	_, err = ss.ExamAttempt().AppendBrowserActivity(ctx, &store.BrowserActivityAppend{
		Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		SourceSessionID: sourceID, Events: []model.BrowserActivityEvent{tooFar},
	})
	assertExamAttemptConflict(t, err, "browser_activity_reorder_window")

	oddEvents := make([]model.BrowserActivityEvent, 0, 33)
	for sequence := int64(1); sequence <= 65; sequence += 2 {
		oddEvents = append(oddEvents, browserActivityEvent(sequence, model.BrowserActivityOpened, fixture.revisionID))
	}
	acknowledgement, err = ss.ExamAttempt().AppendBrowserActivity(ctx, &store.BrowserActivityAppend{
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
	pausedAcknowledgement, err := ss.ExamAttempt().AppendBrowserActivity(ctx, &store.BrowserActivityAppend{
		Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		SourceSessionID: sourceID, Events: finalEvents,
	})
	requireNoError(t, err)
	assertBrowserActivityAcknowledgement(t, pausedAcknowledgement, sourceID, 70, 70, nil, false)
	replacementID := browserSourceID(2)
	_, err = ss.ExamAttempt().StartBrowserActivity(ctx, &store.BrowserActivitySourceStart{
		Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		SourceSessionID: replacementID, PredecessorSessionID: sourceID, ResetReason: model.BrowserSourceResetCoordinatorRestarted,
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
	acknowledgement, err = ss.ExamAttempt().AppendBrowserActivity(ctx, &store.BrowserActivityAppend{
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

	replaced, err := ss.ExamAttempt().StartBrowserActivity(ctx, &store.BrowserActivitySourceStart{
		Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		SourceSessionID: replacementID, PredecessorSessionID: sourceID, ResetReason: model.BrowserSourceResetCoordinatorRestarted,
	})
	requireNoError(t, err)
	assertBrowserActivityAcknowledgement(t, replaced, replacementID, 0, 0, nil, false)
	_, err = ss.ExamAttempt().AppendBrowserActivity(ctx, &store.BrowserActivityAppend{
		Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		SourceSessionID: sourceID, Events: []model.BrowserActivityEvent{browserActivityEvent(71, model.BrowserActivityOpened, fixture.revisionID)},
	})
	assertExamAttemptConflict(t, err, "browser_source_fence")
	replacementEvents := []model.BrowserActivityEvent{
		browserActivityEvent(1, model.BrowserActivityOpened, fixture.revisionID),
		browserActivityEvent(2, model.BrowserActivityClosed, fixture.revisionID),
	}
	_, err = ss.ExamAttempt().AppendBrowserActivity(ctx, &store.BrowserActivityAppend{
		Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		SourceSessionID: replacementID, Events: replacementEvents,
	})
	requireNoError(t, err)
	finalSequence := int64(2)
	sealAccess := store.ExamSubmissionSealAccess{AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID,
		Generation: connected.Participation.Generation, ConnectionID: connected.Connection.ID,
		CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID,
		ContinuityCredentialHash: access.ContinuityCredentialHash, ExpectedCurrentRevisionID: fixture.revisionID,
		ExpectedWorkspaceCursor: connected.Workspace.Cursor,
		BrowserActivity: model.BrowserActivitySubmission{State: model.BrowserActivitySubmissionComplete,
			SourceSessionID: replacementID, FinalSequence: &finalSequence}}
	seal := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: sealAccess,
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
	attachSubmissionReceipt(t, fixture.candidate, seal)
	sealed, err := ss.ExamSubmission().Seal(ctx, seal,
		examCommand(fixture.candidate.ID, store.ExamSubmissionSealOperation, "browser-activity-seal", "browser-activity-seal"))
	requireNoError(t, err)
	header, err := ss.ExamSubmission().Get(ctx, sealed.Receipt.SubmissionID)
	requireNoError(t, err)
	if header.BrowserActivity.State != model.BrowserActivitySubmissionComplete || header.IntegrityState != model.SubmissionIntegrityGapped ||
		header.UnresolvedIntegrityCount != 1 {
		t.Fatalf("Browser Activity Submission = %#v", header)
	}
	discrepancies, err := ss.ExamIntegrityReview().ListDiscrepancies(ctx, store.ExamIntegrityDiscrepancyListOptions{
		SubmissionID: header.ID, Limit: store.ExamIntegrityReviewDiscrepancyReadMaximum,
	})
	requireNoError(t, err)
	if len(discrepancies.Items) != 1 || discrepancies.Items[0].Kind != model.IntegrityDiscrepancyBrowserActivityGap ||
		discrepancies.Items[0].GapReason != string(model.IntegrityDiscrepancyBrowserActivityPriorSourceGap) ||
		discrepancies.Items[0].BrowserSourceSessionID != replacementID || discrepancies.Items[0].UnresolvedCount != 1 {
		t.Fatalf("Browser Activity discrepancies = %#v", discrepancies)
	}
}

func testCompleteBrowserActivitySubmission(t *testing.T, ss store.Store) {
	t.Helper()
	ctx := context.Background()
	fixture, connected, access := newBrowserActivityFixture(t, ctx, ss, "browser-activity-complete")
	sourceID := browserSourceID(20)
	_, err := ss.ExamAttempt().StartBrowserActivity(ctx, &store.BrowserActivitySourceStart{Access: access,
		ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: sourceID})
	requireNoError(t, err)
	_, err = ss.ExamAttempt().AppendBrowserActivity(ctx, &store.BrowserActivityAppend{Access: access,
		ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: sourceID,
		Events: []model.BrowserActivityEvent{browserActivityEvent(1, model.BrowserActivityOpened, fixture.revisionID)}})
	requireNoError(t, err)
	finalSequence := int64(1)
	sealAccess := store.ExamSubmissionSealAccess{AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID,
		Generation: connected.Participation.Generation, ConnectionID: connected.Connection.ID,
		CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID,
		ContinuityCredentialHash: access.ContinuityCredentialHash, ExpectedCurrentRevisionID: fixture.revisionID,
		ExpectedWorkspaceCursor: connected.Workspace.Cursor,
		BrowserActivity: model.BrowserActivitySubmission{State: model.BrowserActivitySubmissionComplete,
			SourceSessionID: sourceID, FinalSequence: &finalSequence}}
	notClosed := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: sealAccess,
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
	attachSubmissionReceipt(t, fixture.candidate, notClosed)
	_, err = ss.ExamSubmission().Seal(ctx, notClosed,
		examCommand(fixture.candidate.ID, store.ExamSubmissionSealOperation, "browser-activity-not-closed", "browser-activity-not-closed"))
	assertSubmissionConflict(t, err, "browser_activity_not_closed")
	_, err = ss.ExamAttempt().AppendBrowserActivity(ctx, &store.BrowserActivityAppend{Access: access,
		ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: sourceID,
		Events: []model.BrowserActivityEvent{browserActivityEvent(2, model.BrowserActivityClosed, fixture.revisionID)}})
	requireNoError(t, err)
	finalSequence = 2
	sealedInput := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: sealAccess,
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
	attachSubmissionReceipt(t, fixture.candidate, sealedInput)
	sealed, err := ss.ExamSubmission().Seal(ctx, sealedInput,
		examCommand(fixture.candidate.ID, store.ExamSubmissionSealOperation, "browser-activity-complete-seal", "browser-activity-complete-seal"))
	requireNoError(t, err)
	header, err := ss.ExamSubmission().Get(ctx, sealed.Receipt.SubmissionID)
	requireNoError(t, err)
	if header.BrowserActivity.State != model.BrowserActivitySubmissionComplete || header.BrowserActivity.FinalSequence == nil ||
		*header.BrowserActivity.FinalSequence != 2 || header.IntegrityState != model.SubmissionIntegritySettled ||
		header.UnresolvedIntegrityCount != 0 {
		t.Fatalf("complete Browser Activity Submission = %#v", header)
	}
}

func testBrowserActivitySourceLimit(t *testing.T, ss store.Store) {
	t.Helper()
	ctx := context.Background()
	_, connected, access := newBrowserActivityFixture(t, ctx, ss, "browser-activity-source-limit")
	current := browserSourceID(100)
	_, err := ss.ExamAttempt().StartBrowserActivity(ctx, &store.BrowserActivitySourceStart{Access: access,
		ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: current})
	requireNoError(t, err)
	for index := 1; index < model.BrowserSourceMaximumPerParticipation; index++ {
		next := browserSourceID(100 + index)
		_, err = ss.ExamAttempt().StartBrowserActivity(ctx, &store.BrowserActivitySourceStart{Access: access,
			ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
			SourceSessionID: next, PredecessorSessionID: current, ResetReason: model.BrowserSourceResetSpoolUnavailable})
		requireNoError(t, err)
		current = next
	}
	_, err = ss.ExamAttempt().StartBrowserActivity(ctx, &store.BrowserActivitySourceStart{Access: access,
		ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		SourceSessionID:      browserSourceID(100 + model.BrowserSourceMaximumPerParticipation),
		PredecessorSessionID: current, ResetReason: model.BrowserSourceResetSourceCorrupt})
	assertExamAttemptConflict(t, err, "browser_source_limit")
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
