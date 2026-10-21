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
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// TestBrowserExhaustionSettlement covers the transaction that refuses new detail,
// including buffered records on a different source and an already finalized Review.
func TestBrowserExhaustionSettlement(t *testing.T, ss store.Store, mode string, exhaust func(model.ExamAttemptID, model.AttemptParticipationID), pendingBytes func(model.ExamAttemptID) int64) {
	ctx := context.Background()
	f, connected, access := newBrowserActivityFixture(t, ctx, ss, "exhaustion-settlement")
	sources := []model.BrowserSourceSessionID{browserSourceID(19701), browserSourceID(19702)}
	for i, source := range sources {
		start := &store.BrowserActivitySourceStart{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: source}
		if i > 0 {
			start.Transition = model.BrowserStartTransition{Kind: "runtime_reset", PredecessorSourceSessionID: sources[i-1], Reason: model.BrowserSourceResetCoordinatorRestarted}
		}
		_, err := startBrowserSourceFixture(t, ctx, ss, start)
		require.NoError(t, err)
		_, err = appendBrowserActivityFixture(t, ctx, ss, &store.BrowserActivityAppend{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: source, Events: []model.BrowserActivityEvent{browserActivityEvent(2, model.BrowserActivityOpened, f.revisionID)}})
		require.NoError(t, err)
	}
	seal := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: store.ExamSubmissionSealAccess{AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, ConnectionID: connected.Connection.ID, CandidateUserID: f.candidate.ID, SessionID: f.session.ID, ContinuityCredentialHash: access.ContinuityCredentialHash, ExpectedCurrentRevisionID: f.revisionID, ExpectedWorkspaceCursor: connected.Workspace.Cursor}, AuditEventID: saveExamAttemptAudit(t, ctx, ss, f).ID, AuditAt: model.GetMillis()}
	attachSubmissionReceipt(t, ctx, ss, f.candidate, seal)
	sealed, err := ss.ExamSubmission().Seal(ctx, seal, examCommand(f.candidate.ID, store.ExamSubmissionSealOperation, "exhaustion-seal", "exhaustion-seal"))
	require.NoError(t, err)
	access.ConnectionID = ""
	access.ContinuityCredentialHash = ""
	for _, source := range sources {
		owner := store.BrowserDeliveryAccess{Access: access, ParticipationID: connected.Participation.ID, SourceSessionID: source}
		_, err = ss.ExamAttempt().SealBrowserDelivery(ctx, &store.BrowserDeliveryFinalDeclaration{Access: owner, Declaration: model.FinalDeliveryDeclaration{DeclarationID: model.NewID(), FinalSequence: 2}, AuditEventID: browserDeliveryAuditFixture(t, ctx, ss, owner), AuditAt: model.GetMillis()}, examCommand(f.candidate.ID, store.BrowserDeliveryFinalOperation, string(source), string(source)))
		require.NoError(t, err)
	}
	sub := sealed.Receipt.SubmissionID
	draft, err := ss.ExamIntegrityReview().UpdateDraft(ctx, &store.ExamIntegrityReviewDraftMutation{SubmissionID: sub, ReviewID: model.NewSubmissionReviewID(), ActorUserID: f.manager.ID, ChangedAt: model.NowUTC(), AuditEventID: saveIntegrityReviewAudit(t, ctx, ss, f, sub, model.ActionSubmissionReview).ID, AuditAt: model.GetMillis()}, examCommand(f.manager.ID, store.ExamIntegrityReviewDraftOperation, "exhaustion-review", "exhaustion-review"))
	require.NoError(t, err)
	final, err := ss.ExamIntegrityReview().Finalize(ctx, &store.ExamIntegrityReviewFinalize{SubmissionID: sub, ReviewID: draft.Review.ID, ExpectedReviewRevision: draft.Review.Revision, ActorUserID: f.manager.ID, ChangedAt: model.NowUTC(), AuditEventID: saveIntegrityReviewAudit(t, ctx, ss, f, sub, model.ActionSubmissionReview).ID, AuditAt: model.GetMillis()}, examCommand(f.manager.ID, store.ExamIntegrityReviewFinalizeOperation, "exhaustion-finalize", "exhaustion-finalize"))
	require.NoError(t, err)
	before, err := ss.ExamSubmission().Get(ctx, sub)
	require.NoError(t, err)
	require.Equal(t, "pending", before.BrowserActivity.State, "fixture has no pending delivery")
	require.NotEqual(t, int64(0), pendingBytes(connected.Attempt.ID), "fixture has no pending delivery")
	exhaust(connected.Attempt.ID, connected.Participation.ID)
	owner := store.BrowserDeliveryAccess{Access: access, ParticipationID: connected.Participation.ID, SourceSessionID: sources[1]}
	status, err := ss.ExamAttempt().BrowserSourceStatus(ctx, owner)
	require.NoError(t, err)
	gap := model.DeclareDeliveryGaps{DeclarationID: model.NewID(), ExpectedDeclarationRevision: 1, AllocatedThroughSequence: 2, Ranges: []model.SequenceRange{{First: 1, Last: 1}}, Reason: "spool_lost"}
	trigger := func(audit model.AuditEventID) error {
		if mode == "metadata" || mode == "intervals" {
			_, err := ss.ExamAttempt().DeclareBrowserDeliveryGaps(ctx, &store.BrowserDeliveryGapDeclaration{Access: owner, Declaration: gap, AuditEventID: audit, AuditAt: model.GetMillis()}, examCommand(f.candidate.ID, store.BrowserDeliveryGapsOperation, "exhaustion-gap", "exhaustion-gap"))
			return err
		}
		_, err := ss.ExamAttempt().AppendHistoricalBrowserDelivery(ctx, &store.BrowserActivityAppend{Access: access, ParticipationID: owner.ParticipationID, Generation: connected.Participation.Generation, SourceSessionID: owner.SourceSessionID, PolicyRevisionID: status.PolicyRevisionID, PolicyDigest: status.PolicyDigest, Events: []model.BrowserActivityEvent{browserActivityEvent(1, model.BrowserActivityOpened, f.revisionID)}, AuditEventID: audit, AuditAt: model.GetMillis()}, examCommand(f.candidate.ID, store.BrowserDeliveryAppendOperation, "exhaustion-append", "exhaustion-append"))
		return err
	}
	// Failing audit must roll back latching, interpretation and settlement together.
	err = trigger(model.NewAuditEventID())
	require.Error(t, err, "exhaustion committed without audit")
	unchanged, err := ss.ExamSubmission().Get(ctx, sub)
	require.NoError(t, err)
	require.Equal(t, before.BrowserActivity, unchanged.BrowserActivity, "failed exhaustion leaked settlement")
	require.NotEqual(t, int64(0), pendingBytes(connected.Attempt.ID), "failed exhaustion leaked settlement")
	err = trigger(browserDeliveryAuditFixture(t, ctx, ss, owner))
	var refusal *store.BrowserDeliveryRefusal
	var capacity *model.DeliveryMetadataCapacity
	if !errors.As(err, &refusal) {
		require.ErrorAs(t, err, &capacity)
	}
	// Read the aggregate before any source status operation could mask stale state.
	after, err := ss.ExamSubmission().Get(ctx, sub)
	require.NoError(t, err)
	require.Equal(t, "incomplete", after.BrowserActivity.State, "exhaustion left stale settlement: %#v pending", after.BrowserActivity)
	require.Equal(t, int64(0), after.BrowserActivity.PendingSourceCount, "exhaustion left stale settlement: %#v pending", after.BrowserActivity)
	require.Equal(t, int64(2), after.BrowserActivity.IncompleteSourceCount, "exhaustion left stale settlement: %#v pending", after.BrowserActivity)
	require.Equal(t, int64(0), pendingBytes(connected.Attempt.ID), "exhaustion left stale settlement: %#v pending", after.BrowserActivity)
	review, err := ss.ExamIntegrityReview().Get(ctx, sub)
	require.NoError(t, err)
	require.Equal(t, model.SubmissionReviewDraft, review.Review.State, "exhaustion preserved finalized review")
	require.Greater(t, review.Review.Revision, final.Review.Revision, "exhaustion preserved finalized review")
	require.Equal(t, before.ManifestDigest, after.ManifestDigest, "exhaustion changed sealed work")
	for _, source := range sources {
		owner.SourceSessionID = source
		status, err := ss.ExamAttempt().BrowserSourceStatus(ctx, owner)
		require.NoError(t, err)
		require.Equal(t, int64(2), status.SettledThrough, "exhaustion lost settlement or fabricated receipt")
		require.Equal(t, int64(0), status.HighestContiguous, "exhaustion lost settlement or fabricated receipt")
		require.Equal(t, int64(2), status.TerminalMissingThrough, "exhaustion lost settlement or fabricated receipt")
	}
	owner.SourceSessionID = sources[1]
	_ = trigger(browserDeliveryAuditFixture(t, ctx, ss, owner))
	replay, err := ss.ExamIntegrityReview().Get(ctx, sub)
	require.NoError(t, err)
	require.Equal(t, review.Review.Revision, replay.Review.Revision, "exact refusal replay repeated settlement")
	require.Equal(t, int64(0), pendingBytes(connected.Attempt.ID), "exact refusal replay repeated settlement")
}
