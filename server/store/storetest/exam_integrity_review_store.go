// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// ExamIntegrityReviewSQLProbe exposes only the independent adapter needed to
// characterize multi-node command convergence.
type ExamIntegrityReviewSQLProbe struct {
	ConcurrentPeer store.ExamIntegrityReviewStore
}

// TestExamIntegrityReviewStore verifies post-seal evidence inspection, the
// revision-fenced Review lifecycle, and the deliberately narrow released
// student projection through the public Store contract.
func TestExamIntegrityReviewStore(t *testing.T, ss store.Store, reviews store.ExamIntegrityReviewStore,
	probes ...ExamIntegrityReviewSQLProbe,
) {
	t.Helper()
	ctx := context.Background()
	fixture, connected, flagged, sealed, focusAccess := newIntegrityReviewFixture(t, ctx, ss, "review-main")

	lateInput := &store.ExamAttemptFocusLossDiscrepancy{Access: focusAccess,
		SchemaVersion: model.FocusLossSignalSchemaVersion, DiscrepancyID: model.NewIntegrityDiscrepancyID(),
		SignalID: model.NewFocusLossSignalID(), Sequence: 3, DurationMilliseconds: 900,
		Source:       model.FocusLossSourceFullscreenExited,
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
	lateTarget, err := ss.ExamAttempt().ResolveEndedFocusLossTarget(ctx, focusAccess)
	requireNoError(t, err)
	late, err := ss.ExamAttempt().RecordEndedFocusLoss(ctx, lateInput)
	requireNoError(t, err)
	if lateTarget == nil || late == nil || late.Duplicate || late.Discrepancy == nil ||
		late.Target != *lateTarget || late.Discrepancy.ID != lateInput.DiscrepancyID ||
		late.Discrepancy.SubmissionID != sealed.Receipt.SubmissionID || late.Discrepancy.MissingBefore != 1 {
		t.Fatalf("RecordEndedFocusLoss() target=%#v result=%#v", lateTarget, late)
	}
	requireSuccessfulAudit(t, ctx, ss, lateInput.AuditEventID)
	assertReviewAuditPrivate(t, ctx, ss, lateInput.AuditEventID, string(lateInput.Source))
	replayLateInput := *lateInput
	replayLateInput.DiscrepancyID, replayLateInput.SignalID = model.NewIntegrityDiscrepancyID(), model.NewFocusLossSignalID()
	replayLateInput.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String()
	replayedLate, err := ss.ExamAttempt().RecordEndedFocusLoss(ctx, &replayLateInput)
	requireNoError(t, err)
	if replayedLate == nil || !replayedLate.Duplicate || replayedLate.Discrepancy.ID != late.Discrepancy.ID ||
		replayedLate.Discrepancy.SignalID != late.Discrepancy.SignalID {
		t.Fatalf("RecordEndedFocusLoss(replay) = %#v", replayedLate)
	}
	changedLateInput := replayLateInput
	changedLateInput.DurationMilliseconds++
	changedLateInput.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String()
	if _, changedErr := ss.ExamAttempt().RecordEndedFocusLoss(ctx, &changedLateInput); changedErr == nil {
		t.Fatal("RecordEndedFocusLoss(same sequence with changed semantics) succeeded")
	}

	authorization, err := reviews.Resolve(ctx, sealed.Receipt.SubmissionID)
	requireNoError(t, err)
	if authorization == nil || authorization.SubmissionID != sealed.Receipt.SubmissionID ||
		authorization.ExamID != fixture.examID || authorization.SittingID != fixture.sitting.ID ||
		authorization.AttemptID != connected.Attempt.ID || authorization.CandidateUserID != fixture.candidate.ID ||
		authorization.AcademicUnitID != fixture.unitID {
		t.Fatalf("Resolve() = %#v", authorization)
	}

	snapshot, err := reviews.Get(ctx, sealed.Receipt.SubmissionID)
	requireNoError(t, err)
	if snapshot == nil || snapshot.Submission == nil || snapshot.Submission.ID != sealed.Receipt.SubmissionID ||
		snapshot.Review != nil || len(snapshot.Decisions) != 0 {
		t.Fatalf("Get(before Review) = %#v", snapshot)
	}
	if result, resultErr := reviews.GetReleasedStudentResult(ctx, connected.Attempt.ID, fixture.candidate.ID); !store.IsNotFound(resultErr) || result != nil {
		t.Fatalf("GetReleasedStudentResult(before release) = %#v, %v", result, resultErr)
	}

	flags, err := reviews.ListFlags(ctx, store.ExamIntegrityFlagListOptions{
		SubmissionID: sealed.Receipt.SubmissionID, Limit: 1,
	})
	requireNoError(t, err)
	if flags == nil || flags.HasMore || len(flags.Items) != 1 || flagged.Flag == nil ||
		flags.Items[0].Flag.ID != flagged.Flag.ID || flags.Items[0].EvidenceCount != 1 ||
		flags.Items[0].OverflowCount != 0 || flags.Items[0].UnresolvedMissingCount != 0 {
		t.Fatalf("ListFlags() = %#v, flagged = %#v", flags, flagged)
	}
	evidence, err := reviews.ListEvidence(ctx, store.ExamIntegrityEvidenceListOptions{
		SubmissionID: sealed.Receipt.SubmissionID, FlagID: flagged.Flag.ID, Limit: 1,
	})
	requireNoError(t, err)
	if evidence == nil || evidence.HasMore || len(evidence.Items) != 1 ||
		evidence.Items[0].FlagID != flagged.Flag.ID || evidence.Items[0].AttemptID != connected.Attempt.ID {
		t.Fatalf("ListEvidence() = %#v", evidence)
	}
	discrepancies, err := reviews.ListDiscrepancies(ctx, store.ExamIntegrityDiscrepancyListOptions{
		SubmissionID: sealed.Receipt.SubmissionID, Limit: 1,
	})
	requireNoError(t, err)
	if discrepancies == nil || discrepancies.HasMore || len(discrepancies.Items) != 1 ||
		discrepancies.Items[0].ID != late.Discrepancy.ID {
		t.Fatalf("ListDiscrepancies() = %#v", discrepancies)
	}

	reviewID := model.NewSubmissionReviewID()
	decisionInput := &store.ExamIntegrityReviewDecisionMutation{
		SubmissionID: sealed.Receipt.SubmissionID, ReviewID: reviewID,
		DecisionID: model.NewIntegrityReviewDecisionID(), FlagID: flagged.Flag.ID,
		ActorUserID: fixture.manager.ID, ExpectedReviewRevision: 0, ExpectedDecisionRevision: 0,
		Outcome: model.IntegrityReviewConfirmed, PrivateRationale: "server evidence is internally consistent",
		ChangedAt: model.NowUTC(), AuditEventID: saveIntegrityReviewAudit(t, ctx, ss, fixture,
			sealed.Receipt.SubmissionID, model.ActionSubmissionReview).ID.String(), AuditAt: model.GetMillis(),
	}
	decisionCommand := examCommand(fixture.manager.ID, store.ExamIntegrityReviewDecisionOperation,
		"review-decision", "review-decision")
	decided, err := reviews.SaveDecision(ctx, decisionInput, decisionCommand)
	requireNoError(t, err)
	if decided == nil || decided.Replayed || decided.Review == nil || decided.Decision == nil ||
		decided.Review.ID != reviewID || decided.Review.Revision != 1 || decided.Decision.ID != decisionInput.DecisionID ||
		decided.Decision.Revision != 1 || decided.Decision.PrivateRationale != decisionInput.PrivateRationale {
		t.Fatalf("SaveDecision() = %#v", decided)
	}
	requireSuccessfulAudit(t, ctx, ss, decisionInput.AuditEventID)
	assertReviewAuditPrivate(t, ctx, ss, decisionInput.AuditEventID, decisionInput.PrivateRationale)

	replayInput := *decisionInput
	replayInput.DecisionID = model.NewIntegrityReviewDecisionID()
	replayInput.AuditEventID = saveIntegrityReviewAudit(t, ctx, ss, fixture,
		sealed.Receipt.SubmissionID, model.ActionSubmissionReview).ID.String()
	replayed, err := reviews.SaveDecision(ctx, &replayInput, decisionCommand)
	requireNoError(t, err)
	if replayed == nil || !replayed.Replayed || replayed.Review == nil || replayed.Decision == nil ||
		replayed.Review.ID != decided.Review.ID || replayed.Review.Revision != decided.Review.Revision ||
		replayed.Decision.ID != decided.Decision.ID || replayed.Decision.PrivateRationale != decided.Decision.PrivateRationale {
		t.Fatalf("SaveDecision(exact replay) = %#v, first = %#v", replayed, decided)
	}
	requireSuccessfulAudit(t, ctx, ss, replayInput.AuditEventID)

	draftInput := &store.ExamIntegrityReviewDraftMutation{
		SubmissionID: sealed.Receipt.SubmissionID, ReviewID: reviewID, ActorUserID: fixture.manager.ID,
		ExpectedReviewRevision: decided.Review.Revision, ManagerNotes: "private operational context",
		StudentRemarksMarkdown: "Your submission was reviewed. **No action is required.**",
		ChangedAt:              model.NowUTC().Add(time.Millisecond), AuditEventID: saveIntegrityReviewAudit(t, ctx, ss, fixture,
			sealed.Receipt.SubmissionID, model.ActionSubmissionReview).ID.String(), AuditAt: model.GetMillis(),
	}
	drafted, err := reviews.UpdateDraft(ctx, draftInput, examCommand(fixture.manager.ID,
		store.ExamIntegrityReviewDraftOperation, "review-draft", "review-draft"))
	requireNoError(t, err)
	if drafted == nil || drafted.Review == nil || drafted.Review.Revision != 2 ||
		drafted.Review.ManagerNotes != draftInput.ManagerNotes ||
		drafted.Review.StudentRemarksMarkdown != draftInput.StudentRemarksMarkdown {
		t.Fatalf("UpdateDraft() = %#v", drafted)
	}
	assertReviewAuditPrivate(t, ctx, ss, draftInput.AuditEventID, draftInput.ManagerNotes, draftInput.StudentRemarksMarkdown)

	finalizeInput := &store.ExamIntegrityReviewFinalize{
		SubmissionID: sealed.Receipt.SubmissionID, ReviewID: reviewID, ActorUserID: fixture.manager.ID,
		ExpectedReviewRevision: drafted.Review.Revision, ChangedAt: model.NowUTC().Add(2 * time.Millisecond),
		AuditEventID: saveIntegrityReviewAudit(t, ctx, ss, fixture,
			sealed.Receipt.SubmissionID, model.ActionSubmissionReview).ID.String(), AuditAt: model.GetMillis(),
	}
	finalized, err := reviews.Finalize(ctx, finalizeInput, examCommand(fixture.manager.ID,
		store.ExamIntegrityReviewFinalizeOperation, "review-finalize", "review-finalize"))
	requireNoError(t, err)
	if finalized == nil || finalized.Review == nil || finalized.Review.State != model.SubmissionReviewFinalized ||
		finalized.Review.ReleaseState != model.SubmissionReviewWithheld || finalized.Review.Revision != 3 ||
		finalized.Review.FlagCount != 1 || finalized.Review.EvidenceCount != 1 || finalized.Review.DiscrepancyCount != 1 ||
		!validStoretestDigest(finalized.Review.EvidenceInventoryDigest) {
		t.Fatalf("Finalize() = %#v", finalized)
	}

	lateDecision := *decisionInput
	lateDecision.ExpectedReviewRevision = finalized.Review.Revision
	lateDecision.ExpectedDecisionRevision = decided.Decision.Revision
	lateDecision.Outcome = model.IntegrityReviewDismissed
	lateDecision.PrivateRationale = "must not change a frozen Review"
	lateDecision.ChangedAt = model.NowUTC().Add(3 * time.Millisecond)
	lateDecision.AuditEventID = saveIntegrityReviewAudit(t, ctx, ss, fixture,
		sealed.Receipt.SubmissionID, model.ActionSubmissionReview).ID.String()
	if _, err = reviews.SaveDecision(ctx, &lateDecision, examCommand(fixture.manager.ID,
		store.ExamIntegrityReviewDecisionOperation, "review-late-decision", "review-late-decision")); err == nil {
		t.Fatal("SaveDecision(after finalization) succeeded")
	}

	releaseInput := &store.ExamIntegrityReviewRelease{
		SubmissionID: sealed.Receipt.SubmissionID, ReviewID: reviewID, ActorUserID: fixture.manager.ID,
		ExpectedReviewRevision: finalized.Review.Revision, ChangedAt: model.NowUTC().Add(4 * time.Millisecond),
		AuditEventID: saveIntegrityReviewAudit(t, ctx, ss, fixture,
			sealed.Receipt.SubmissionID, model.ActionSubmissionRelease).ID.String(), AuditAt: model.GetMillis(),
	}
	released, err := reviews.Release(ctx, releaseInput, examCommand(fixture.manager.ID,
		store.ExamIntegrityReviewReleaseOperation, "review-release", "review-release"))
	requireNoError(t, err)
	if released == nil || released.Review == nil || released.Review.ReleaseState != model.SubmissionReviewReleased ||
		released.Review.Revision != 4 || !released.Review.ReleasedAt.Valid {
		t.Fatalf("Release() = %#v", released)
	}
	result, err := reviews.GetReleasedStudentResult(ctx, connected.Attempt.ID, fixture.candidate.ID)
	requireNoError(t, err)
	if result == nil || result.ReviewID != reviewID || result.SubmissionID != sealed.Receipt.SubmissionID ||
		result.AttemptID != connected.Attempt.ID || result.CandidateUserID != fixture.candidate.ID ||
		result.StudentRemarksMarkdown != draftInput.StudentRemarksMarkdown || result.ReleasedAt.IsZero() {
		t.Fatalf("GetReleasedStudentResult() = %#v", result)
	}
	if foreign, foreignErr := reviews.GetReleasedStudentResult(ctx, connected.Attempt.ID, model.NewUserID()); !store.IsNotFound(foreignErr) || foreign != nil {
		t.Fatalf("GetReleasedStudentResult(foreign candidate) = %#v, %v", foreign, foreignErr)
	}
	digestBeforeLate := released.Review.EvidenceInventoryDigest
	postReleaseInput := *lateInput
	postReleaseInput.DiscrepancyID, postReleaseInput.SignalID = model.NewIntegrityDiscrepancyID(), model.NewFocusLossSignalID()
	postReleaseInput.Sequence = 4
	postReleaseInput.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String()
	postRelease, err := ss.ExamAttempt().RecordEndedFocusLoss(ctx, &postReleaseInput)
	requireNoError(t, err)
	if postRelease == nil || postRelease.Duplicate || postRelease.Discrepancy.Sequence != 4 {
		t.Fatalf("RecordEndedFocusLoss(after release) = %#v", postRelease)
	}
	unchanged, err := reviews.Get(ctx, sealed.Receipt.SubmissionID)
	requireNoError(t, err)
	if unchanged.Review == nil || unchanged.Review.Revision != released.Review.Revision ||
		unchanged.Review.DiscrepancyCount != 1 || unchanged.Review.EvidenceInventoryDigest != digestBeforeLate {
		t.Fatalf("late discrepancy mutated released Review: %#v", unchanged.Review)
	}
	allDiscrepancies, err := reviews.ListDiscrepancies(ctx, store.ExamIntegrityDiscrepancyListOptions{
		SubmissionID: sealed.Receipt.SubmissionID, Limit: 10,
	})
	requireNoError(t, err)
	seenBefore, seenAfter := false, false
	if allDiscrepancies != nil {
		for _, item := range allDiscrepancies.Items {
			seenBefore = seenBefore || item.ID == late.Discrepancy.ID
			seenAfter = seenAfter || item.ID == postRelease.Discrepancy.ID
		}
	}
	if allDiscrepancies == nil || allDiscrepancies.HasMore || len(allDiscrepancies.Items) != 2 ||
		!seenBefore || !seenAfter {
		t.Fatalf("ListDiscrepancies(after release) = %#v", allDiscrepancies)
	}

	testIntegrityReviewIncomplete(t, ctx, ss, reviews)
	testIntegrityReviewGappedFinalization(t, ctx, ss, reviews)
	testEndedFocusLossAfterAutomaticSeal(t, ctx, ss)
	if len(probes) > 0 && probes[0].ConcurrentPeer != nil {
		testIntegrityReviewMultiNodeReplay(t, ctx, ss, reviews, probes[0].ConcurrentPeer)
	}
}

func testEndedFocusLossAfterAutomaticSeal(t *testing.T, ctx context.Context, ss store.Store) {
	t.Helper()
	newSuspended := func(prefix string) (examAttemptFixture, *store.ExamAttemptFocusLossResult,
		store.ExamAttemptFocusLossAccess) {
		policy := model.FocusLossPolicy{Enabled: true, MinimumDuration: 500 * time.Millisecond, IncidentCount: 1,
			Window: 10 * time.Second, Outcome: model.IntegrityOutcomeFlagAndSuspend}
		fixture := newExamAttemptFixtureWithFocusLoss(t, ctx, ss, &policy)
		_, access := connectFocusLossFixture(t, ctx, ss, fixture, prefix+"-connect")
		suspended := recordFocusLoss(t, ctx, ss, fixture, access, 1, 500,
			model.FocusLossSourceApplicationBackgrounded)
		if suspended.Attempt == nil || suspended.Attempt.State != model.ExamAttemptSuspended ||
			suspended.Participation == nil || suspended.Participation.EndReason != model.AttemptParticipationEndPolicySuspended ||
			suspended.Connection == nil || suspended.Connection.CloseReason != model.AttemptConnectionClosePolicySuspended ||
			suspended.Suspension == nil {
			t.Fatalf("automatic late-discrepancy suspension fixture = %#v", suspended)
		}
		return fixture, suspended, access
	}
	automaticSeal := func(prefix string, fixture examAttemptFixture, attemptID model.ExamAttemptID) *store.ExamSubmissionAutomaticSealResult {
		at := model.NowUTC()
		closing, err := ss.ExamSitting().EarlyClose(ctx, &store.ExamSittingManagerTransition{
			ExamID: fixture.examID, SittingID: fixture.sitting.ID, ActorUserID: fixture.manager.ID,
			ExpectedRevision: fixture.sitting.Revision,
			FinalizeJob:      newExamSittingFinalizeJob(t, fixture.sitting.ID, fixture.sitting.Revision+1, at),
			PrivateReason:    "seal retained late Focus Loss fixture", ChangedAt: at,
			AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.manager.ID, fixture.examID, fixture.unitID).ID.String(),
			AuditAt:      model.GetMillis(),
		}, examCommand(fixture.manager.ID, "exam.sitting.close.v1", prefix+"-close", prefix+"-close"))
		requireNoError(t, err)
		if closing == nil || !closing.Changed || closing.Value.Sitting.State != model.ExamSittingClosing {
			t.Fatalf("EarlyClose(automatic late-discrepancy fixture) = %#v", closing)
		}
		targets, err := ss.ExamSubmission().ListAutomaticSealTargets(ctx,
			store.ExamSubmissionAutomaticSealListOptions{SittingID: fixture.sitting.ID, Limit: 10})
		requireNoError(t, err)
		if len(targets) != 1 || targets[0].AttemptID != attemptID {
			t.Fatalf("automatic late-discrepancy targets = %#v; want attempt %s", targets, attemptID)
		}
		audit := saveExamSittingSystemAudit(t, ctx, ss, fixture.sitting.ID, fixture.unitID)
		sealed, err := ss.ExamSubmission().SealForSittingClose(ctx, &store.ExamSubmissionAutomaticSeal{
			Target: targets[0], SubmissionID: model.NewSubmissionID(), AuditEventID: audit.ID.String(), AuditAt: model.GetMillis(),
		})
		requireNoError(t, err)
		return sealed
	}

	t.Run("preserved suspension causes remain eligible", func(t *testing.T) {
		fixture, suspended, access := newSuspended("review-automatic-suspended")
		sealed := automaticSeal("review-automatic-suspended", fixture, suspended.Attempt.ID)
		if sealed == nil || sealed.Receipt.SubmissionID.IsValid() == false || sealed.ConnectionClosed {
			t.Fatalf("SealForSittingClose(suspended) = %#v", sealed)
		}
		target, err := ss.ExamAttempt().ResolveEndedFocusLossTarget(ctx, access)
		requireNoError(t, err)
		input := &store.ExamAttemptFocusLossDiscrepancy{Access: access,
			SchemaVersion: model.FocusLossSignalSchemaVersion, DiscrepancyID: model.NewIntegrityDiscrepancyID(),
			SignalID: model.NewFocusLossSignalID(), Sequence: 2, DurationMilliseconds: 700,
			Source:       model.FocusLossSourceFullscreenExited,
			AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
		recorded, err := ss.ExamAttempt().RecordEndedFocusLoss(ctx, input)
		requireNoError(t, err)
		if target == nil || target.SubmissionID != sealed.Receipt.SubmissionID || recorded == nil ||
			recorded.Discrepancy == nil || recorded.Discrepancy.SubmissionID != sealed.Receipt.SubmissionID {
			t.Fatalf("automatic suspended late discrepancy target=%#v result=%#v", target, recorded)
		}
	})

	t.Run("selector is pinned to submission generation", func(t *testing.T) {
		fixture, suspended, oldAccess := newSuspended("review-automatic-selector")
		reallow := &store.ExamAttemptReallow{ExamID: fixture.examID, SittingID: fixture.sitting.ID,
			AttemptID: suspended.Attempt.ID, SuspensionID: suspended.Suspension.ID, ActorUserID: fixture.manager.ID,
			ExpectedAttemptRevision: suspended.Attempt.Revision, PrivateReason: "resume exact retained generation",
			ChangedAt: model.NowUTC(), AuditEventID: saveExamAttemptReallowAudit(t, ctx, ss, fixture).ID.String(),
			AuditAt: model.GetMillis()}
		_, err := ss.ExamAttempt().ReallowAttempt(ctx, reallow, examCommand(fixture.manager.ID,
			store.ExamAttemptReallowOperation, "review-automatic-selector-reallow", "review-automatic-selector-reallow"))
		requireNoError(t, err)
		connected, currentAccess := connectFocusLossFixture(t, ctx, ss, fixture, "review-automatic-selector-reconnect")
		sealed := automaticSeal("review-automatic-selector", fixture, connected.Attempt.ID)
		if _, err = ss.ExamAttempt().ResolveEndedFocusLossTarget(ctx, oldAccess); err == nil {
			t.Fatal("ResolveEndedFocusLossTarget(previous generation) succeeded")
		}
		current, err := ss.ExamAttempt().ResolveEndedFocusLossTarget(ctx, currentAccess)
		requireNoError(t, err)
		if current == nil || current.SubmissionID != sealed.Receipt.SubmissionID ||
			current.ParticipationID != connected.Participation.ID || current.Generation != connected.Participation.Generation {
			t.Fatalf("ResolveEndedFocusLossTarget(current generation) = %#v", current)
		}
	})
}

func newIntegrityReviewFixture(t *testing.T, ctx context.Context, ss store.Store, prefix string) (
	examAttemptFixture, *store.ExamAttemptConnectResult, *store.ExamAttemptFocusLossResult, *store.ExamSubmissionSealResult,
	store.ExamAttemptFocusLossAccess,
) {
	return newIntegrityReviewFixtureWithFinalSequence(t, ctx, ss, prefix, 1)
}

func newIntegrityReviewFixtureWithFinalSequence(t *testing.T, ctx context.Context, ss store.Store, prefix string,
	finalSequence int64,
) (
	examAttemptFixture, *store.ExamAttemptConnectResult, *store.ExamAttemptFocusLossResult, *store.ExamSubmissionSealResult,
	store.ExamAttemptFocusLossAccess,
) {
	t.Helper()
	policy := model.FocusLossPolicy{Enabled: true, MinimumDuration: 500 * time.Millisecond, IncidentCount: 1,
		Window: 10 * time.Second, Outcome: model.IntegrityOutcomeFlag}
	fixture := newExamAttemptFixtureWithFocusLoss(t, ctx, ss, &policy)
	connected, access := connectFocusLossFixture(t, ctx, ss, fixture, prefix+"-connect")
	flagged := recordFocusLoss(t, ctx, ss, fixture, access, 1, 500, model.FocusLossSourceApplicationBackgrounded)
	if flagged.Flag == nil || !flagged.FlagCreated || !flagged.ThresholdCrossed {
		t.Fatalf("Focus Loss review fixture = %#v", flagged)
	}
	sealInput := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: store.ExamSubmissionSealAccess{
		AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID,
		Generation: connected.Participation.Generation, ConnectionID: connected.Connection.ID,
		CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID,
		ContinuityCredentialHash: access.ContinuityCredentialHash, ExpectedWorkspaceCursor: connected.Workspace.Cursor,
		FinalFocusLossSequence: finalSequence,
	}, AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
	sealed, err := ss.ExamSubmission().Seal(ctx, sealInput, examCommand(fixture.candidate.ID,
		store.ExamSubmissionSealOperation, prefix+"-seal", prefix+"-seal"))
	requireNoError(t, err)
	return fixture, connected, flagged, sealed, access
}

func testIntegrityReviewGappedFinalization(t *testing.T, ctx context.Context, ss store.Store,
	reviews store.ExamIntegrityReviewStore,
) {
	t.Helper()
	fixture, _, flagged, sealed, _ := newIntegrityReviewFixtureWithFinalSequence(t, ctx, ss, "review-gapped", 2)
	snapshot, err := reviews.Get(ctx, sealed.Receipt.SubmissionID)
	requireNoError(t, err)
	if snapshot == nil || snapshot.Submission == nil || snapshot.Submission.IntegrityState != model.SubmissionIntegrityGapped ||
		snapshot.Submission.UnresolvedIntegrityCount != 1 {
		t.Fatalf("gapped Submission snapshot = %#v", snapshot)
	}
	reviewID := model.NewSubmissionReviewID()
	decisionInput := &store.ExamIntegrityReviewDecisionMutation{SubmissionID: sealed.Receipt.SubmissionID,
		ReviewID: reviewID, DecisionID: model.NewIntegrityReviewDecisionID(), FlagID: flagged.Flag.ID,
		ActorUserID: fixture.manager.ID, Outcome: model.IntegrityReviewInconclusive,
		PrivateRationale: "The final client sequence retained one explicit gap.", ChangedAt: model.NowUTC(),
		AuditEventID: saveIntegrityReviewAudit(t, ctx, ss, fixture, sealed.Receipt.SubmissionID,
			model.ActionSubmissionReview).ID.String(), AuditAt: model.GetMillis()}
	decided, err := reviews.SaveDecision(ctx, decisionInput, examCommand(fixture.manager.ID,
		store.ExamIntegrityReviewDecisionOperation, "review-gapped-decision", "review-gapped-decision"))
	requireNoError(t, err)
	finalize := &store.ExamIntegrityReviewFinalize{SubmissionID: sealed.Receipt.SubmissionID, ReviewID: reviewID,
		ActorUserID: fixture.manager.ID, ExpectedReviewRevision: decided.Review.Revision, ChangedAt: model.NowUTC(),
		AuditEventID: saveIntegrityReviewAudit(t, ctx, ss, fixture, sealed.Receipt.SubmissionID,
			model.ActionSubmissionReview).ID.String(), AuditAt: model.GetMillis()}
	finalized, err := reviews.Finalize(ctx, finalize, examCommand(fixture.manager.ID,
		store.ExamIntegrityReviewFinalizeOperation, "review-gapped-finalize", "review-gapped-finalize"))
	requireNoError(t, err)
	if finalized == nil || finalized.Review == nil || finalized.Review.State != model.SubmissionReviewFinalized {
		t.Fatalf("Finalize(gapped Submission) = %#v", finalized)
	}
}

func testIntegrityReviewIncomplete(t *testing.T, ctx context.Context, ss store.Store, reviews store.ExamIntegrityReviewStore) {
	t.Helper()
	fixture, _, _, sealed, _ := newIntegrityReviewFixture(t, ctx, ss, "review-incomplete")
	reviewID := model.NewSubmissionReviewID()
	draft := &store.ExamIntegrityReviewDraftMutation{SubmissionID: sealed.Receipt.SubmissionID, ReviewID: reviewID,
		ActorUserID: fixture.manager.ID, ExpectedReviewRevision: 0, ManagerNotes: "awaiting evidence decision",
		ChangedAt: model.NowUTC(), AuditEventID: saveIntegrityReviewAudit(t, ctx, ss, fixture,
			sealed.Receipt.SubmissionID, model.ActionSubmissionReview).ID.String(), AuditAt: model.GetMillis()}
	created, err := reviews.UpdateDraft(ctx, draft, examCommand(fixture.manager.ID,
		store.ExamIntegrityReviewDraftOperation, "review-incomplete-draft", "review-incomplete-draft"))
	requireNoError(t, err)
	finalize := &store.ExamIntegrityReviewFinalize{SubmissionID: sealed.Receipt.SubmissionID, ReviewID: reviewID,
		ActorUserID: fixture.manager.ID, ExpectedReviewRevision: created.Review.Revision, ChangedAt: model.NowUTC(),
		AuditEventID: saveIntegrityReviewAudit(t, ctx, ss, fixture,
			sealed.Receipt.SubmissionID, model.ActionSubmissionReview).ID.String(), AuditAt: model.GetMillis()}
	if _, err = reviews.Finalize(ctx, finalize, examCommand(fixture.manager.ID,
		store.ExamIntegrityReviewFinalizeOperation, "review-incomplete-finalize", "review-incomplete-finalize")); err == nil {
		t.Fatal("Finalize(with undecided Flag) succeeded")
	}
}

func testIntegrityReviewMultiNodeReplay(t *testing.T, ctx context.Context, ss store.Store,
	reviews, peer store.ExamIntegrityReviewStore,
) {
	t.Helper()
	fixture, _, flagged, sealed, _ := newIntegrityReviewFixture(t, ctx, ss, "review-peer")
	input := &store.ExamIntegrityReviewDecisionMutation{SubmissionID: sealed.Receipt.SubmissionID,
		ReviewID: model.NewSubmissionReviewID(), DecisionID: model.NewIntegrityReviewDecisionID(), FlagID: flagged.Flag.ID,
		ActorUserID: fixture.manager.ID, Outcome: model.IntegrityReviewInconclusive, PrivateRationale: "bounded uncertainty remains",
		ChangedAt: model.NowUTC(), AuditEventID: saveIntegrityReviewAudit(t, ctx, ss, fixture,
			sealed.Receipt.SubmissionID, model.ActionSubmissionReview).ID.String(), AuditAt: model.GetMillis()}
	command := examCommand(fixture.manager.ID, store.ExamIntegrityReviewDecisionOperation,
		"review-peer-decision", "review-peer-decision")
	first, err := reviews.SaveDecision(ctx, input, command)
	requireNoError(t, err)
	replay := *input
	replay.ReviewID, replay.DecisionID = model.NewSubmissionReviewID(), model.NewIntegrityReviewDecisionID()
	replay.AuditEventID = saveIntegrityReviewAudit(t, ctx, ss, fixture,
		sealed.Receipt.SubmissionID, model.ActionSubmissionReview).ID.String()
	second, err := peer.SaveDecision(ctx, &replay, command)
	requireNoError(t, err)
	if first == nil || second == nil || first.Replayed || !second.Replayed || first.Review.ID != second.Review.ID ||
		first.Decision.ID != second.Decision.ID {
		t.Fatalf("multi-node Review replay = first %#v, second %#v", first, second)
	}
}

func saveIntegrityReviewAudit(t *testing.T, ctx context.Context, ss store.Store, fixture examAttemptFixture,
	submissionID model.SubmissionID, action model.Action,
) *model.AuditEvent {
	t.Helper()
	audit, err := ss.Audit().Save(ctx, &model.AuditEvent{ActorID: fixture.manager.ID, Action: string(action),
		Resource:  model.Resource{Type: model.ResourceSubmission, ID: submissionID.String()},
		ScopeType: model.RoleScopeAcademicUnit, ScopeID: fixture.unitID.String(), Status: model.AuditStatusAttempt,
		NodeID: "test-node"})
	requireNoError(t, err)
	return audit
}

func assertReviewAuditPrivate(t *testing.T, ctx context.Context, ss store.Store, auditID string, forbidden ...string) {
	t.Helper()
	audit, err := ss.Audit().Get(ctx, auditID)
	requireNoError(t, err)
	for _, value := range forbidden {
		if value != "" && (bytes.Contains(audit.Parameters, []byte(value)) || bytes.Contains(audit.Result, []byte(value))) {
			t.Fatalf("Integrity Review audit exposed private text %q: parameters=%s result=%s", value, audit.Parameters, audit.Result)
		}
	}
}
