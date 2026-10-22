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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func attachResultReleaseMail(t *testing.T, candidate *model.User, input *store.ExamIntegrityReviewRelease) {
	t.Helper()
	at := input.ChangedAt
	occurrenceID, err := model.ResultReleaseOccurrenceID(input.ReviewID, input.ExpectedReviewRevision)
	require.NoError(t, err)
	deliveryID, jobID := model.NewMailDeliveryID(), model.NewJobID()
	command, err := model.EncodeMailDeliveryCommand(model.MailDeliveryCommand{DeliveryID: deliveryID})
	require.NoError(t, err)
	job, err := model.NewJob(jobID, model.JobTypeMailDeliver, command, deliveryID.String(), at, at,
		model.MailMaximumAttempts)
	require.NoError(t, err)
	job, err = job.RequestCancellation(at)
	require.NoError(t, err)
	input.CandidateUserID = candidate.ID
	input.ExpectedRecipientRevision = candidate.Revision
	input.Notice = &store.PreparedMail{Occurrence: &model.MailOccurrence{ID: occurrenceID,
		Kind: model.MailOccurrenceResultRelease, TemplateKey: model.MailTemplateExamResultReleased,
		ActorUserID: candidate.ID, CreatedAt: at}, Delivery: &model.MailDelivery{ID: deliveryID,
		OccurrenceID: occurrenceID, JobID: jobID, TargetUserID: candidate.ID,
		TemplateKey: model.MailTemplateExamResultReleased, TemplateDigest: strings.Repeat("0", 64),
		MaskedRecipient: "c***@example.edu", State: model.MailDeliverySuppressed, CreatedAt: at, UpdatedAt: at,
		MessageDate: at, Deadline: at.Add(72 * time.Hour), MessageID: "<result." + deliveryID.String() + "@example.test>",
		PublicFailureCode: model.MailDeliveryDisabledCode, Revision: 1}, Job: job}
}

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
		DiscrepancyID: model.NewIntegrityDiscrepancyID(),
		SignalID:      model.NewFocusLossSignalID(), Sequence: 3, DurationMilliseconds: 900,
		Source:       model.FocusLossSourceFullscreenExited,
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.GetMillis()}
	lateTarget, err := ss.ExamAttempt().ResolveEndedFocusLossTarget(ctx, focusAccess)
	require.NoError(t, err)
	late, err := ss.ExamAttempt().RecordEndedFocusLoss(ctx, lateInput)
	require.NoError(t, err)
	require.NotNil(t, lateTarget, "RecordEndedFocusLoss() target")
	require.NotNil(t, late, "RecordEndedFocusLoss() target")
	require.False(t, late.Duplicate, "RecordEndedFocusLoss() target")
	require.NotNil(t, late.Discrepancy, "RecordEndedFocusLoss() target")
	require.Equal(t, *lateTarget, late.Target, "RecordEndedFocusLoss() target")
	require.Equal(t, lateInput.DiscrepancyID, late.Discrepancy.ID, "RecordEndedFocusLoss() target")
	require.Equal(t, sealed.Receipt.SubmissionID, late.Discrepancy.SubmissionID, "RecordEndedFocusLoss() target")
	require.Equal(t, int64(1), late.Discrepancy.MissingBefore, "RecordEndedFocusLoss() target")
	requireSuccessfulAudit(t, ctx, ss, lateInput.AuditEventID)
	assertReviewAuditPrivate(t, ctx, ss, lateInput.AuditEventID, string(lateInput.Source))
	replayLateInput := *lateInput
	replayLateInput.DiscrepancyID, replayLateInput.SignalID = model.NewIntegrityDiscrepancyID(), model.NewFocusLossSignalID()
	replayLateInput.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID
	replayedLate, err := ss.ExamAttempt().RecordEndedFocusLoss(ctx, &replayLateInput)
	require.NoError(t, err)
	require.NotNil(t, replayedLate)
	require.True(t, replayedLate.Duplicate)
	require.Equal(t, late.Discrepancy.ID, replayedLate.Discrepancy.ID)
	require.Equal(t, late.Discrepancy.SignalID, replayedLate.Discrepancy.SignalID)
	changedLateInput := replayLateInput
	changedLateInput.DurationMilliseconds++
	changedLateInput.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID
	_, changedErr := ss.ExamAttempt().RecordEndedFocusLoss(ctx, &changedLateInput)
	require.Error(t, changedErr, "RecordEndedFocusLoss(same sequence with changed semantics) succeeded")

	authorization, err := reviews.Resolve(ctx, sealed.Receipt.SubmissionID)
	require.NoError(t, err)
	require.NotNil(t, authorization)
	require.Equal(t, sealed.Receipt.SubmissionID, authorization.SubmissionID)
	require.Equal(t, fixture.examID, authorization.ExamID)
	require.Equal(t, fixture.sitting.ID, authorization.SittingID)
	require.Equal(t, connected.Attempt.ID, authorization.AttemptID)
	require.Equal(t, fixture.candidate.ID, authorization.CandidateUserID)
	require.Equal(t, fixture.unitID, authorization.AcademicUnitID)

	snapshot, err := reviews.Get(ctx, sealed.Receipt.SubmissionID)
	require.NoError(t, err)
	require.NotNil(t, snapshot)
	require.NotNil(t, snapshot.Submission)
	require.Equal(t, sealed.Receipt.SubmissionID, snapshot.Submission.ID)
	require.Nil(t, snapshot.Review)
	require.Empty(t, snapshot.Decisions)
	result, resultErr := reviews.GetReleasedStudentResult(ctx, connected.Attempt.ID, fixture.candidate.ID)
	require.True(t, store.IsNotFound(resultErr), "GetReleasedStudentResult(before release)")
	require.Nil(t, result, "GetReleasedStudentResult(before release)")

	flags, err := reviews.ListFlags(ctx, store.ExamIntegrityFlagListOptions{
		SubmissionID: sealed.Receipt.SubmissionID, Limit: 1,
	})
	require.NoError(t, err)
	require.NotNil(t, flags, "ListFlags() = %#v, flagged = %#v", flags, flagged)
	require.False(t, flags.HasMore, "ListFlags() = %#v, flagged = %#v", flags, flagged)
	require.Len(t, flags.Items, 1, "ListFlags() = %#v, flagged = %#v", flags, flagged)
	require.NotNil(t, flagged.Flag, "ListFlags() = %#v, flagged = %#v", flags, flagged)
	require.Equal(t, flagged.Flag.ID, flags.Items[0].Flag.ID, "ListFlags() = %#v, flagged = %#v", flags, flagged)
	require.Equal(t, 1, flags.Items[0].EvidenceCount, "ListFlags() = %#v, flagged = %#v", flags, flagged)
	require.Equal(t, int64(0), flags.Items[0].OverflowCount, "ListFlags() = %#v, flagged = %#v", flags, flagged)
	require.Equal(t, int64(0), flags.Items[0].UnresolvedMissingCount, "ListFlags() = %#v, flagged = %#v", flags, flagged)
	evidence, err := reviews.ListEvidence(ctx, store.ExamIntegrityEvidenceListOptions{
		SubmissionID: sealed.Receipt.SubmissionID, FlagID: flagged.Flag.ID, Limit: 1,
	})
	require.NoError(t, err)
	require.NotNil(t, evidence)
	require.False(t, evidence.HasMore)
	require.Len(t, evidence.Items, 1)
	require.Equal(t, flagged.Flag.ID, evidence.Items[0].FlagID)
	require.Equal(t, connected.Attempt.ID, evidence.Items[0].AttemptID)
	discrepancies, err := reviews.ListDiscrepancies(ctx, store.ExamIntegrityDiscrepancyListOptions{
		SubmissionID: sealed.Receipt.SubmissionID, Limit: 1,
	})
	require.NoError(t, err)
	require.NotNil(t, discrepancies)
	require.False(t, discrepancies.HasMore)
	require.Len(t, discrepancies.Items, 1)
	require.Equal(t, late.Discrepancy.ID, discrepancies.Items[0].ID)

	reviewID := model.NewSubmissionReviewID()
	decisionInput := &store.ExamIntegrityReviewDecisionMutation{
		SubmissionID: sealed.Receipt.SubmissionID, ReviewID: reviewID,
		DecisionID: model.NewIntegrityReviewDecisionID(), FlagID: flagged.Flag.ID,
		ActorUserID: fixture.manager.ID, ExpectedReviewRevision: 0, ExpectedDecisionRevision: 0,
		Outcome: model.IntegrityReviewConfirmed, PrivateRationale: "server evidence is internally consistent",
		ChangedAt: model.NowUTC(), AuditEventID: saveIntegrityReviewAudit(t, ctx, ss, fixture,
			sealed.Receipt.SubmissionID, model.ActionSubmissionReview).ID, AuditAt: model.GetMillis(),
	}
	decisionCommand := examCommand(fixture.manager.ID, store.ExamIntegrityReviewDecisionOperation,
		"review-decision", "review-decision")
	decided, err := reviews.SaveDecision(ctx, decisionInput, decisionCommand)
	require.NoError(t, err)
	require.NotNil(t, decided)
	require.False(t, decided.Replayed)
	require.NotNil(t, decided.Review)
	require.NotNil(t, decided.Decision)
	require.Equal(t, reviewID, decided.Review.ID)
	require.Equal(t, int64(1), decided.Review.Revision)
	require.Equal(t, decisionInput.DecisionID, decided.Decision.ID)
	require.Equal(t, int64(1), decided.Decision.Revision)
	require.Equal(t, decisionInput.PrivateRationale, decided.Decision.PrivateRationale)
	requireSuccessfulAudit(t, ctx, ss, decisionInput.AuditEventID)
	assertReviewAuditPrivate(t, ctx, ss, decisionInput.AuditEventID, decisionInput.PrivateRationale)

	replayInput := *decisionInput
	replayInput.DecisionID = model.NewIntegrityReviewDecisionID()
	replayInput.AuditEventID = saveIntegrityReviewAudit(t, ctx, ss, fixture,
		sealed.Receipt.SubmissionID, model.ActionSubmissionReview).ID
	replayed, err := reviews.SaveDecision(ctx, &replayInput, decisionCommand)
	require.NoError(t, err)
	require.NotNil(t, replayed, "SaveDecision(exact replay) = %#v, first = %#v", replayed, decided)
	require.True(t, replayed.Replayed, "SaveDecision(exact replay) = %#v, first = %#v", replayed, decided)
	require.NotNil(t, replayed.Review, "SaveDecision(exact replay) = %#v, first = %#v", replayed, decided)
	require.NotNil(t, replayed.Decision, "SaveDecision(exact replay) = %#v, first = %#v", replayed, decided)
	require.Equal(t, decided.Review.ID, replayed.Review.ID, "SaveDecision(exact replay) = %#v, first = %#v", replayed, decided)
	require.Equal(t, decided.Review.Revision, replayed.Review.Revision, "SaveDecision(exact replay) = %#v, first = %#v", replayed, decided)
	require.Equal(t, decided.Decision.ID, replayed.Decision.ID, "SaveDecision(exact replay) = %#v, first = %#v", replayed, decided)
	require.Equal(t, decided.Decision.PrivateRationale, replayed.Decision.PrivateRationale, "SaveDecision(exact replay) = %#v, first = %#v", replayed, decided)
	requireSuccessfulAudit(t, ctx, ss, replayInput.AuditEventID)

	draftInput := &store.ExamIntegrityReviewDraftMutation{
		SubmissionID: sealed.Receipt.SubmissionID, ReviewID: reviewID, ActorUserID: fixture.manager.ID,
		ExpectedReviewRevision: decided.Review.Revision, ManagerNotes: "private operational context",
		StudentRemarksMarkdown: "Your submission was reviewed. **No action is required.**",
		ChangedAt:              model.NowUTC().Add(time.Millisecond), AuditEventID: saveIntegrityReviewAudit(t, ctx, ss, fixture,
			sealed.Receipt.SubmissionID, model.ActionSubmissionReview).ID, AuditAt: model.GetMillis(),
	}
	drafted, err := reviews.UpdateDraft(ctx, draftInput, examCommand(fixture.manager.ID,
		store.ExamIntegrityReviewDraftOperation, "review-draft", "review-draft"))
	require.NoError(t, err)
	require.NotNil(t, drafted)
	require.NotNil(t, drafted.Review)
	require.Equal(t, int64(2), drafted.Review.Revision)
	require.Equal(t, draftInput.ManagerNotes, drafted.Review.ManagerNotes)
	require.Equal(t, draftInput.StudentRemarksMarkdown, drafted.Review.StudentRemarksMarkdown)
	assertReviewAuditPrivate(t, ctx, ss, draftInput.AuditEventID, draftInput.ManagerNotes, draftInput.StudentRemarksMarkdown)

	finalizeInput := &store.ExamIntegrityReviewFinalize{
		SubmissionID: sealed.Receipt.SubmissionID, ReviewID: reviewID, ActorUserID: fixture.manager.ID,
		ExpectedReviewRevision: drafted.Review.Revision, ChangedAt: model.NowUTC().Add(2 * time.Millisecond),
		AuditEventID: saveIntegrityReviewAudit(t, ctx, ss, fixture,
			sealed.Receipt.SubmissionID, model.ActionSubmissionReview).ID, AuditAt: model.GetMillis(),
	}
	finalized, err := reviews.Finalize(ctx, finalizeInput, examCommand(fixture.manager.ID,
		store.ExamIntegrityReviewFinalizeOperation, "review-finalize", "review-finalize"))
	require.NoError(t, err)
	require.NotNil(t, finalized)
	require.NotNil(t, finalized.Review)
	require.Equal(t, model.SubmissionReviewFinalized, finalized.Review.State)
	require.Equal(t, model.SubmissionReviewWithheld, finalized.Review.ReleaseState)
	require.Equal(t, int64(3), finalized.Review.Revision)
	require.Equal(t, 1, finalized.Review.FlagCount)
	require.Equal(t, 1, finalized.Review.EvidenceCount)
	require.Equal(t, 1, finalized.Review.DiscrepancyCount)
	require.True(t, validStoretestDigest(finalized.Review.EvidenceInventoryDigest))

	lateDecision := *decisionInput
	lateDecision.ExpectedReviewRevision = finalized.Review.Revision
	lateDecision.ExpectedDecisionRevision = decided.Decision.Revision
	lateDecision.Outcome = model.IntegrityReviewDismissed
	lateDecision.PrivateRationale = "must not change a frozen Review"
	lateDecision.ChangedAt = model.NowUTC().Add(3 * time.Millisecond)
	lateDecision.AuditEventID = saveIntegrityReviewAudit(t, ctx, ss, fixture,
		sealed.Receipt.SubmissionID, model.ActionSubmissionReview).ID
	_, err = reviews.SaveDecision(ctx, &lateDecision, examCommand(fixture.manager.ID,
		store.ExamIntegrityReviewDecisionOperation, "review-late-decision", "review-late-decision"))
	require.Error(t, err, "SaveDecision(after finalization) succeeded")

	releasePreparation, err := reviews.PrepareRelease(ctx, sealed.Receipt.SubmissionID, reviewID,
		finalized.Review.Revision)
	require.NoError(t, err)
	require.NotNil(t, releasePreparation)
	require.False(t, releasePreparation.Replayed)
	require.False(t, releasePreparation.ReleaseAt.IsZero())
	retriedReleasePreparation, err := reviews.PrepareRelease(ctx, sealed.Receipt.SubmissionID, reviewID,
		finalized.Review.Revision)
	require.NoError(t, err)
	require.NotNil(t, retriedReleasePreparation, "PrepareRelease(fresh retry)")
	require.False(t, retriedReleasePreparation.Replayed, "PrepareRelease(fresh retry)")
	require.True(t, retriedReleasePreparation.ReleaseAt.Equal(releasePreparation.ReleaseAt), "PrepareRelease(fresh retry)")
	releaseInput := &store.ExamIntegrityReviewRelease{
		SubmissionID: sealed.Receipt.SubmissionID, ReviewID: reviewID, ActorUserID: fixture.manager.ID,
		ExpectedReviewRevision: finalized.Review.Revision, ChangedAt: releasePreparation.ReleaseAt,
		AuditEventID: saveIntegrityReviewAudit(t, ctx, ss, fixture,
			sealed.Receipt.SubmissionID, model.ActionSubmissionRelease).ID,
		AuditAt: model.MillisFromTime(releasePreparation.ReleaseAt),
	}
	attachResultReleaseMail(t, fixture.candidate, releaseInput)
	beforeRelease, err := ss.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{
		TemplateKeys: []model.MailTemplateKey{model.MailTemplateExamResultReleased}, Limit: 200})
	require.NoError(t, err)
	require.Empty(t, beforeRelease)
	changedReleaseTime := *releaseInput
	changedReleaseTime.AuditEventID = saveIntegrityReviewAudit(t, ctx, ss, fixture,
		sealed.Receipt.SubmissionID, model.ActionSubmissionRelease).ID
	changedReleaseTime.ChangedAt = changedReleaseTime.ChangedAt.Add(24 * time.Hour)
	changedReleaseTime.AuditAt = model.MillisFromTime(changedReleaseTime.ChangedAt)
	attachResultReleaseMail(t, fixture.candidate, &changedReleaseTime)
	if _, changedTimeErr := reviews.Release(ctx, &changedReleaseTime, examCommand(fixture.manager.ID,
		store.ExamIntegrityReviewReleaseOperation, "review-release-changed-time", "review-release-changed-time")); changedTimeErr == nil {
		require.FailNow(t, "Release(with self-consistent forged future PostgreSQL time) succeeded")
	} else {
		var conflict *store.ErrConflict
		require.ErrorAs(t, changedTimeErr, &conflict)
		require.Equal(t, "result_release_time", conflict.Constraint)
	}
	staleRecipient := *releaseInput
	staleRecipient.AuditEventID = saveIntegrityReviewAudit(t, ctx, ss, fixture,
		sealed.Receipt.SubmissionID, model.ActionSubmissionRelease).ID
	staleRecipient.ExpectedRecipientRevision++
	_, staleErr := reviews.Release(ctx, &staleRecipient, examCommand(fixture.manager.ID,
		store.ExamIntegrityReviewReleaseOperation, "review-release-stale-recipient", "review-release-stale-recipient"))
	require.Error(t, staleErr, "Release(with stale candidate revision) succeeded")
	delivery, deliveryErr := ss.Mail().GetDelivery(ctx, staleRecipient.Notice.Delivery.ID)
	require.True(t, store.IsNotFound(deliveryErr), "stale-recipient result mail")
	require.Nil(t, delivery, "stale-recipient result mail")
	rollbackInput := *releaseInput
	rollbackInput.AuditEventID = model.NewAuditEventID()
	_, rollbackErr := reviews.Release(ctx, &rollbackInput, examCommand(fixture.manager.ID,
		store.ExamIntegrityReviewReleaseOperation, "review-release-rollback", "review-release-rollback"))
	require.Error(t, rollbackErr, "Release(with missing audit) succeeded")
	delivery, deliveryErr = ss.Mail().GetDelivery(ctx, rollbackInput.Notice.Delivery.ID)
	require.True(t, store.IsNotFound(deliveryErr), "rolled-back result mail")
	require.Nil(t, delivery, "rolled-back result mail")
	stillWithheld, err := reviews.Get(ctx, sealed.Receipt.SubmissionID)
	require.NoError(t, err)
	require.Equal(t, model.SubmissionReviewWithheld, stillWithheld.Review.ReleaseState)
	released, err := reviews.Release(ctx, releaseInput, examCommand(fixture.manager.ID,
		store.ExamIntegrityReviewReleaseOperation, "review-release", "review-release"))
	require.NoError(t, err)
	require.NotNil(t, released)
	require.NotNil(t, released.Review)
	require.Equal(t, model.SubmissionReviewReleased, released.Review.ReleaseState)
	require.Equal(t, int64(4), released.Review.Revision)
	require.True(t, released.Review.ReleasedAt.Valid)
	require.True(t, released.Review.ReleasedAt.Time.Equal(releaseInput.Notice.Occurrence.CreatedAt))
	releaseDelivery, err := ss.Mail().GetDelivery(ctx, releaseInput.Notice.Delivery.ID)
	require.NoError(t, err)
	require.Equal(t, releaseInput.Notice.Occurrence.ID, releaseDelivery.OccurrenceID)
	require.Equal(t, fixture.candidate.ID, releaseDelivery.TargetUserID)
	require.Equal(t, model.MailTemplateExamResultReleased, releaseDelivery.TemplateKey)
	replayPreparation, err := reviews.PrepareRelease(ctx, sealed.Receipt.SubmissionID, reviewID,
		finalized.Review.Revision)
	require.NoError(t, err)
	require.NotNil(t, replayPreparation)
	require.True(t, replayPreparation.Replayed)
	replayRelease := *releaseInput
	replayRelease.AuditEventID = saveIntegrityReviewAudit(t, ctx, ss, fixture,
		sealed.Receipt.SubmissionID, model.ActionSubmissionRelease).ID
	replayRelease.ChangedAt, replayRelease.AuditAt = replayPreparation.ReleaseAt,
		model.MillisFromTime(replayPreparation.ReleaseAt)
	replayRelease.Notice, replayRelease.ExpectedRecipientRevision = nil, 0
	replayedRelease, err := reviews.Release(ctx, &replayRelease, examCommand(fixture.manager.ID,
		store.ExamIntegrityReviewReleaseOperation, "review-release", "review-release"))
	require.NoError(t, err)
	require.NotNil(t, replayedRelease)
	require.True(t, replayedRelease.Replayed)
	require.Equal(t, released.Review.ID, replayedRelease.Review.ID)
	revokeIntegrityReviewManager(t, ctx, ss, fixture)
	revokedReplay := replayRelease
	revokedReplay.AuditEventID = saveIntegrityReviewAudit(t, ctx, ss, fixture,
		sealed.Receipt.SubmissionID, model.ActionSubmissionRelease).ID
	_, revokedErr := reviews.Release(ctx, &revokedReplay, examCommand(fixture.manager.ID,
		store.ExamIntegrityReviewReleaseOperation, "review-release", "review-release"))
	require.Error(t, revokedErr, "Release(retained replay after Manager authority revocation) succeeded")
	result, err = reviews.GetReleasedStudentResult(ctx, connected.Attempt.ID, fixture.candidate.ID)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, reviewID, result.ReviewID)
	require.Equal(t, sealed.Receipt.SubmissionID, result.SubmissionID)
	require.Equal(t, connected.Attempt.ID, result.AttemptID)
	require.Equal(t, fixture.candidate.ID, result.CandidateUserID)
	require.Equal(t, draftInput.StudentRemarksMarkdown, result.StudentRemarksMarkdown)
	require.False(t, result.ReleasedAt.IsZero())
	foreign, foreignErr := reviews.GetReleasedStudentResult(ctx, connected.Attempt.ID, model.NewUserID())
	require.True(t, store.IsNotFound(foreignErr), "GetReleasedStudentResult(foreign candidate)")
	require.Nil(t, foreign, "GetReleasedStudentResult(foreign candidate)")
	digestBeforeLate := released.Review.EvidenceInventoryDigest
	postReleaseInput := *lateInput
	postReleaseInput.DiscrepancyID, postReleaseInput.SignalID = model.NewIntegrityDiscrepancyID(), model.NewFocusLossSignalID()
	postReleaseInput.Sequence = 4
	postReleaseInput.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID
	postRelease, err := ss.ExamAttempt().RecordEndedFocusLoss(ctx, &postReleaseInput)
	require.NoError(t, err)
	require.NotNil(t, postRelease)
	require.False(t, postRelease.Duplicate)
	require.Equal(t, int64(4), postRelease.Discrepancy.Sequence)
	unchanged, err := reviews.Get(ctx, sealed.Receipt.SubmissionID)
	require.NoError(t, err)
	require.NotNil(t, unchanged.Review)
	require.Equal(t, released.Review.Revision, unchanged.Review.Revision)
	require.Equal(t, 1, unchanged.Review.DiscrepancyCount)
	require.Equal(t, digestBeforeLate, unchanged.Review.EvidenceInventoryDigest)
	allDiscrepancies, err := reviews.ListDiscrepancies(ctx, store.ExamIntegrityDiscrepancyListOptions{
		SubmissionID: sealed.Receipt.SubmissionID, Limit: 10,
	})
	require.NoError(t, err)
	seenBefore, seenAfter := false, false
	if allDiscrepancies != nil {
		for _, item := range allDiscrepancies.Items {
			seenBefore = seenBefore || item.ID == late.Discrepancy.ID
			seenAfter = seenAfter || item.ID == postRelease.Discrepancy.ID
		}
	}
	require.NotNil(t, allDiscrepancies)
	require.False(t, allDiscrepancies.HasMore)
	require.Len(t, allDiscrepancies.Items, 2)
	require.True(t, seenBefore)
	require.True(t, seenAfter)

	testIntegrityReviewIncomplete(t, ctx, ss, reviews)
	testIntegrityReviewGappedFinalization(t, ctx, ss, reviews)
	testEndedFocusLossAfterAutomaticSeal(t, ctx, ss)
	if len(probes) > 0 && probes[0].ConcurrentPeer != nil {
		testIntegrityReviewMultiNodeReplay(t, ctx, ss, reviews, probes[0].ConcurrentPeer)
	}
}

func revokeIntegrityReviewManager(t *testing.T, ctx context.Context, ss store.Store, fixture examAttemptFixture) {
	t.Helper()
	replacement := saveUser(t, ctx, ss)
	_, err := ss.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{
		AcademicUnitID: fixture.unitID, UserID: replacement.ID, StartsAt: model.NowUTC().Add(-time.Hour),
	})
	require.NoError(t, err)
	access, err := ss.ExamAuthoring().Access(ctx, fixture.examID, fixture.manager.ID)
	require.NoError(t, err)
	at := model.NowUTC()
	add := newExamManagerMutation(t, ctx, ss, fixture.examID, fixture.manager.ID, replacement.ID,
		access.Exam.Revision, at, false)
	add.Notices = examManagerMailNotices(t, at,
		examManagerMailRecipient{replacement.ID, model.MailTemplateExamManagerAdded})
	added, err := ss.ExamAuthoring().AddManager(ctx, add,
		examCommand(fixture.manager.ID, "exam.manager.add", "review-release-authority-add", "review-release-authority-add"))
	require.NoError(t, err)
	transferAt := at.Add(time.Millisecond)
	transfer := newExamManagerMutation(t, ctx, ss, fixture.examID, fixture.manager.ID, replacement.ID,
		added.Exam.Revision, transferAt, false)
	transfer.Notices = examManagerMailNotices(t, transferAt,
		examManagerMailRecipient{fixture.manager.ID, model.MailTemplateExamOwnershipTransferredFromYou},
		examManagerMailRecipient{replacement.ID, model.MailTemplateExamOwnershipTransferredToYou})
	transferred, err := ss.ExamAuthoring().TransferOwner(ctx, transfer,
		examCommand(fixture.manager.ID, "exam.owner.transfer", "review-release-authority-transfer", "review-release-authority-transfer"))
	require.NoError(t, err)
	removeAt := transferAt.Add(time.Millisecond)
	remove := newExamManagerMutation(t, ctx, ss, fixture.examID, replacement.ID, fixture.manager.ID,
		transferred.Exam.Revision, removeAt, false)
	remove.Notices = examManagerMailNotices(t, removeAt,
		examManagerMailRecipient{fixture.manager.ID, model.MailTemplateExamManagerRemoved})
	_, err = ss.ExamAuthoring().RemoveManager(ctx, remove,
		examCommand(replacement.ID, "exam.manager.remove", "review-release-authority-remove", "review-release-authority-remove"))
	require.NoError(t, err)
	revoked, err := ss.ExamAuthoring().Access(ctx, fixture.examID, fixture.manager.ID)
	require.NoError(t, err)
	require.False(t, revoked.ActorIsManager)
	require.NotEqual(t, fixture.manager.ID, revoked.Exam.OwnerUserID)
}

func testEndedFocusLossAfterAutomaticSeal(t *testing.T, ctx context.Context, ss store.Store) {
	t.Helper()
	newSuspended := func(t *testing.T, prefix string) (examAttemptFixture, *store.ExamAttemptFocusLossResult,
		store.ExamAttemptFocusLossAccess) {
		t.Helper()
		policy := model.FocusLossPolicy{Enabled: true, MinimumDuration: 500 * time.Millisecond, IncidentCount: 1,
			Window: 10 * time.Second, Outcome: model.IntegrityOutcomeFlagAndSuspend}
		fixture := newExamAttemptFixtureWithFocusLoss(t, ctx, ss, &policy)
		_, access := connectFocusLossFixture(t, ctx, ss, fixture, prefix+"-connect")
		suspended := recordFocusLoss(t, ctx, ss, fixture, access, 1, 500,
			model.FocusLossSourceApplicationBackgrounded)
		require.NotNil(t, suspended.Attempt)
		require.Equal(t, model.ExamAttemptSuspended, suspended.Attempt.State)
		require.NotNil(t, suspended.Participation)
		require.Equal(t, model.AttemptParticipationEndPolicySuspended, suspended.Participation.EndReason)
		require.NotNil(t, suspended.Connection)
		require.Equal(t, model.AttemptConnectionClosePolicySuspended, suspended.Connection.CloseReason)
		require.NotNil(t, suspended.Suspension)
		return fixture, suspended, access
	}
	automaticSeal := func(t *testing.T, prefix string, fixture examAttemptFixture, attemptID model.ExamAttemptID) *store.ExamSubmissionAutomaticSealResult {
		t.Helper()
		at := model.NowUTC()
		closing, err := ss.ExamSitting().EarlyClose(ctx, &store.ExamSittingManagerTransition{
			ExamID: fixture.examID, SittingID: fixture.sitting.ID, ActorUserID: fixture.manager.ID,
			ExpectedRevision: fixture.sitting.Revision,
			FinalizeJob:      newExamSittingFinalizeJob(t, fixture.sitting.ID, fixture.sitting.Revision+1, at),
			PrivateReason:    "seal retained late Focus Loss fixture", ChangedAt: at,
			AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.manager.ID, fixture.examID, fixture.unitID).ID,
			AuditAt:      model.GetMillis(),
		}, examCommand(fixture.manager.ID, "exam.sitting.close", prefix+"-close", prefix+"-close"))
		require.NoError(t, err)
		require.NotNil(t, closing)
		require.True(t, closing.Changed)
		require.Equal(t, model.ExamSittingClosing, closing.Value.Sitting.State)
		targets, err := ss.ExamSubmission().ListAutomaticSealTargets(ctx,
			store.ExamSubmissionAutomaticSealListOptions{SittingID: fixture.sitting.ID, Limit: 10})
		require.NoError(t, err)
		require.Len(t, targets, 1, "automatic late-discrepancy targets")
		require.Equal(t, attemptID, targets[0].AttemptID, "automatic late-discrepancy targets")
		audit := saveExamSittingSystemAudit(t, ctx, ss, fixture.sitting.ID, fixture.unitID)
		preparation, err := ss.ExamSubmission().PrepareAutomaticSeal(ctx, targets[0])
		require.NoError(t, err)
		input := &store.ExamSubmissionAutomaticSeal{Target: targets[0], SubmissionID: model.NewSubmissionID(),
			AuditEventID: audit.ID, AuditAt: model.MillisFromTime(preparation.SealAt)}
		attachAutomaticSubmissionReceipt(t, fixture.candidate, input)
		sealed, err := ss.ExamSubmission().SealForSittingClose(ctx, input)
		require.NoError(t, err)
		return sealed
	}

	t.Run("preserved suspension causes remain eligible", func(t *testing.T) {
		fixture, suspended, access := newSuspended(t, "review-automatic-suspended")
		sealed := automaticSeal(t, "review-automatic-suspended", fixture, suspended.Attempt.ID)
		require.NotNil(t, sealed)
		require.True(t, sealed.Receipt.SubmissionID.IsValid())
		require.False(t, sealed.ConnectionClosed)
		target, err := ss.ExamAttempt().ResolveEndedFocusLossTarget(ctx, access)
		require.NoError(t, err)
		input := &store.ExamAttemptFocusLossDiscrepancy{Access: access,
			DiscrepancyID: model.NewIntegrityDiscrepancyID(),
			SignalID:      model.NewFocusLossSignalID(), Sequence: 2, DurationMilliseconds: 700,
			Source:       model.FocusLossSourceFullscreenExited,
			AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.GetMillis()}
		recorded, err := ss.ExamAttempt().RecordEndedFocusLoss(ctx, input)
		require.NoError(t, err)
		require.NotNil(t, target, "automatic suspended late discrepancy target")
		require.Equal(t, sealed.Receipt.SubmissionID, target.SubmissionID, "automatic suspended late discrepancy target")
		require.NotNil(t, recorded, "automatic suspended late discrepancy target")
		require.NotNil(t, recorded.Discrepancy, "automatic suspended late discrepancy target")
		require.Equal(t, sealed.Receipt.SubmissionID, recorded.Discrepancy.SubmissionID, "automatic suspended late discrepancy target")
	})

	t.Run("selector is pinned to submission generation", func(t *testing.T) {
		fixture, suspended, oldAccess := newSuspended(t, "review-automatic-selector")
		reallow := &store.ExamAttemptReallow{ExamID: fixture.examID, SittingID: fixture.sitting.ID,
			AttemptID: suspended.Attempt.ID, SuspensionID: suspended.Suspension.ID, ActorUserID: fixture.manager.ID,
			ExpectedAttemptRevision: suspended.Attempt.Revision, PrivateReason: "resume exact retained generation",
			AuditEventID: saveExamAttemptReallowAudit(t, ctx, ss, fixture).ID,
			AuditAt:      model.GetMillis()}
		_, err := ss.ExamAttempt().ReallowAttempt(ctx, reallow, examCommand(fixture.manager.ID,
			store.ExamAttemptReallowOperation, "review-automatic-selector-reallow", "review-automatic-selector-reallow"))
		require.NoError(t, err)
		connected, currentAccess := connectFocusLossFixture(t, ctx, ss, fixture, "review-automatic-selector-reconnect")
		sealed := automaticSeal(t, "review-automatic-selector", fixture, connected.Attempt.ID)
		_, err = ss.ExamAttempt().ResolveEndedFocusLossTarget(ctx, oldAccess)
		require.Error(t, err, "ResolveEndedFocusLossTarget(previous generation) succeeded")
		current, err := ss.ExamAttempt().ResolveEndedFocusLossTarget(ctx, currentAccess)
		require.NoError(t, err)
		require.NotNil(t, current)
		require.Equal(t, sealed.Receipt.SubmissionID, current.SubmissionID)
		require.Equal(t, connected.Participation.ID, current.ParticipationID)
		require.Equal(t, connected.Participation.Generation, current.Generation)
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
	require.NotNil(t, flagged.Flag)
	require.True(t, flagged.FlagCreated)
	require.True(t, flagged.ThresholdCrossed)
	sealAccess := store.ExamSubmissionSealAccess{
		AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID,
		Generation: connected.Participation.Generation, ConnectionID: connected.Connection.ID,
		CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID,
		ContinuityCredentialHash:  access.ContinuityCredentialHash,
		ExpectedCurrentRevisionID: fixture.sitting.ExamRevisionID,
		ExpectedWorkspaceCursor:   connected.Workspace.Cursor,
		FinalFocusLossSequence:    finalSequence,
	}
	sealTarget, err := ss.ExamSubmission().ResolveSealTarget(ctx, sealAccess)
	require.NoError(t, err)
	sealInput := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: sealAccess,
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID,
		AuditAt:      model.MillisFromTime(sealTarget.SealAt)}
	attachSubmissionReceipt(t, ctx, ss, fixture.candidate, sealInput)
	sealed, err := ss.ExamSubmission().Seal(ctx, sealInput, examCommand(fixture.candidate.ID,
		store.ExamSubmissionSealOperation, prefix+"-seal", prefix+"-seal"))
	require.NoError(t, err)
	return fixture, connected, flagged, sealed, access
}

func testIntegrityReviewGappedFinalization(t *testing.T, ctx context.Context, ss store.Store,
	reviews store.ExamIntegrityReviewStore,
) {
	t.Helper()
	fixture, _, flagged, sealed, _ := newIntegrityReviewFixtureWithFinalSequence(t, ctx, ss, "review-gapped", 2)
	snapshot, err := reviews.Get(ctx, sealed.Receipt.SubmissionID)
	require.NoError(t, err)
	require.NotNil(t, snapshot)
	require.NotNil(t, snapshot.Submission)
	require.Equal(t, model.SubmissionIntegrityGapped, snapshot.Submission.IntegrityState)
	require.Equal(t, int64(1), snapshot.Submission.UnresolvedIntegrityCount)
	reviewID := model.NewSubmissionReviewID()
	decisionInput := &store.ExamIntegrityReviewDecisionMutation{SubmissionID: sealed.Receipt.SubmissionID,
		ReviewID: reviewID, DecisionID: model.NewIntegrityReviewDecisionID(), FlagID: flagged.Flag.ID,
		ActorUserID: fixture.manager.ID, Outcome: model.IntegrityReviewInconclusive,
		PrivateRationale: "The final client sequence retained one explicit gap.", ChangedAt: model.NowUTC(),
		AuditEventID: saveIntegrityReviewAudit(t, ctx, ss, fixture, sealed.Receipt.SubmissionID,
			model.ActionSubmissionReview).ID, AuditAt: model.GetMillis()}
	decided, err := reviews.SaveDecision(ctx, decisionInput, examCommand(fixture.manager.ID,
		store.ExamIntegrityReviewDecisionOperation, "review-gapped-decision", "review-gapped-decision"))
	require.NoError(t, err)
	finalize := &store.ExamIntegrityReviewFinalize{SubmissionID: sealed.Receipt.SubmissionID, ReviewID: reviewID,
		ActorUserID: fixture.manager.ID, ExpectedReviewRevision: decided.Review.Revision, ChangedAt: model.NowUTC(),
		AuditEventID: saveIntegrityReviewAudit(t, ctx, ss, fixture, sealed.Receipt.SubmissionID,
			model.ActionSubmissionReview).ID, AuditAt: model.GetMillis()}
	finalized, err := reviews.Finalize(ctx, finalize, examCommand(fixture.manager.ID,
		store.ExamIntegrityReviewFinalizeOperation, "review-gapped-finalize", "review-gapped-finalize"))
	require.NoError(t, err)
	require.NotNil(t, finalized)
	require.NotNil(t, finalized.Review)
	require.Equal(t, model.SubmissionReviewFinalized, finalized.Review.State)
}

func testIntegrityReviewIncomplete(t *testing.T, ctx context.Context, ss store.Store, reviews store.ExamIntegrityReviewStore) {
	t.Helper()
	fixture, _, _, sealed, _ := newIntegrityReviewFixture(t, ctx, ss, "review-incomplete")
	reviewID := model.NewSubmissionReviewID()
	draft := &store.ExamIntegrityReviewDraftMutation{SubmissionID: sealed.Receipt.SubmissionID, ReviewID: reviewID,
		ActorUserID: fixture.manager.ID, ExpectedReviewRevision: 0, ManagerNotes: "awaiting evidence decision",
		ChangedAt: model.NowUTC(), AuditEventID: saveIntegrityReviewAudit(t, ctx, ss, fixture,
			sealed.Receipt.SubmissionID, model.ActionSubmissionReview).ID, AuditAt: model.GetMillis()}
	created, err := reviews.UpdateDraft(ctx, draft, examCommand(fixture.manager.ID,
		store.ExamIntegrityReviewDraftOperation, "review-incomplete-draft", "review-incomplete-draft"))
	require.NoError(t, err)
	finalize := &store.ExamIntegrityReviewFinalize{SubmissionID: sealed.Receipt.SubmissionID, ReviewID: reviewID,
		ActorUserID: fixture.manager.ID, ExpectedReviewRevision: created.Review.Revision, ChangedAt: model.NowUTC(),
		AuditEventID: saveIntegrityReviewAudit(t, ctx, ss, fixture,
			sealed.Receipt.SubmissionID, model.ActionSubmissionReview).ID, AuditAt: model.GetMillis()}
	_, err = reviews.Finalize(ctx, finalize, examCommand(fixture.manager.ID,
		store.ExamIntegrityReviewFinalizeOperation, "review-incomplete-finalize", "review-incomplete-finalize"))
	require.Error(t, err, "Finalize(with undecided Flag) succeeded")
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
			sealed.Receipt.SubmissionID, model.ActionSubmissionReview).ID, AuditAt: model.GetMillis()}
	command := examCommand(fixture.manager.ID, store.ExamIntegrityReviewDecisionOperation,
		"review-peer-decision", "review-peer-decision")
	first, err := reviews.SaveDecision(ctx, input, command)
	require.NoError(t, err)
	replay := *input
	replay.ReviewID, replay.DecisionID = model.NewSubmissionReviewID(), model.NewIntegrityReviewDecisionID()
	replay.AuditEventID = saveIntegrityReviewAudit(t, ctx, ss, fixture,
		sealed.Receipt.SubmissionID, model.ActionSubmissionReview).ID
	second, err := peer.SaveDecision(ctx, &replay, command)
	require.NoError(t, err)
	require.NotNil(t, first, "multi-node Review replay = first")
	require.NotNil(t, second, "multi-node Review replay = first")
	require.False(t, first.Replayed, "multi-node Review replay = first")
	require.True(t, second.Replayed, "multi-node Review replay = first")
	require.Equal(t, second.Review.ID, first.Review.ID, "multi-node Review replay = first")
	require.Equal(t, second.Decision.ID, first.Decision.ID, "multi-node Review replay = first")
}

func saveIntegrityReviewAudit(t *testing.T, ctx context.Context, ss store.Store, fixture examAttemptFixture,
	submissionID model.SubmissionID, action model.Action,
) *model.AuditEvent {
	t.Helper()
	audit, err := ss.Audit().Save(ctx, &model.AuditEvent{ActorID: fixture.manager.ID, Action: string(action),
		Resource:  model.Resource{Type: model.ResourceSubmission, ID: submissionID.String()},
		ScopeType: model.RoleScopeAcademicUnit, ScopeID: fixture.unitID.String(), Status: model.AuditStatusAttempt,
		NodeID: "test-node"})
	require.NoError(t, err)
	return audit
}

func assertReviewAuditPrivate(t *testing.T, ctx context.Context, ss store.Store, auditID model.AuditEventID, forbidden ...string) {
	t.Helper()
	audit, err := ss.Audit().Get(ctx, auditID)
	require.NoError(t, err)
	for _, value := range forbidden {
		if value != "" {
			require.False(t, bytes.Contains(audit.Parameters, []byte(value)), "Integrity Review audit exposed private text %q: parameters=%s result=%s", value, audit.Parameters, audit.Result)
			require.False(t, bytes.Contains(audit.Result, []byte(value)), "Integrity Review audit exposed private text %q: parameters=%s result=%s", value, audit.Parameters, audit.Result)
		}
	}
}
