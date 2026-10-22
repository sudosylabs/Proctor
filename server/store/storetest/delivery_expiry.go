// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// TestDeliveryExpiryStore verifies abandoned delivery converges without a client
// status request, a fabricated receipt, quota refund or sealed-content mutation.
func TestDeliveryExpiryStore(t *testing.T, ss store.Store, age func(*testing.T, context.Context, model.ExamAttemptID), corrupt ...func(string, string) func()) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	f, connected, access := newBrowserActivityFixture(t, ctx, ss, "delivery-expiry")
	source := browserSourceID(7001)
	_, err := startBrowserSourceFixture(t, ctx, ss, &store.BrowserActivitySourceStart{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: source})
	require.NoError(t, err)
	_, err = appendBrowserActivityFixture(t, ctx, ss, &store.BrowserActivityAppend{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: source, Events: []model.BrowserActivityEvent{browserActivityEvent(2, model.BrowserActivityOpened, f.revisionID)}})
	require.NoError(t, err)
	native := store.NativeDeliveryAccess{Access: access, StreamID: connected.Security.DeliveryStreamID, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation}
	_, err = ss.ExamAttempt().DeclareNativeDeliveryGaps(ctx, &store.NativeDeliveryGapDeclaration{Access: native, Declaration: model.DeclareDeliveryGaps{DeclarationID: model.NewID(), AllocatedThroughSequence: 4, Ranges: []model.SequenceRange{{First: 1, Last: 1}}, Reason: "spool_lost"}, AuditEventID: saveExamAttemptAudit(t, ctx, ss, f).ID, AuditAt: model.NowUTC()}, examCommand(f.candidate.ID, store.NativeDeliveryGapsOperation, "expiry-gap", "expiry-gap"))
	require.NoError(t, err)
	seal := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: store.ExamSubmissionSealAccess{AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, ConnectionID: connected.Connection.ID, CandidateUserID: f.candidate.ID, SessionID: f.session.ID, ContinuityCredentialHash: access.ContinuityCredentialHash, ExpectedCurrentRevisionID: f.revisionID, ExpectedWorkspaceCursor: connected.Workspace.Cursor}, AuditEventID: saveExamAttemptAudit(t, ctx, ss, f).ID, AuditAt: model.NowUTC()}
	attachSubmissionReceipt(t, ctx, ss, f.candidate, seal)
	sealed, err := ss.ExamSubmission().Seal(ctx, seal, examCommand(f.candidate.ID, store.ExamSubmissionSealOperation, "expiry-seal", "expiry-seal"))
	require.NoError(t, err)
	due, err := ss.ExamAttempt().ListExpiredDeliveries(ctx, 200)
	require.NoError(t, err)
	require.Empty(t, due, "unexpired upload selected")
	subID := sealed.Receipt.SubmissionID
	draft, err := ss.ExamIntegrityReview().UpdateDraft(ctx, &store.ExamIntegrityReviewDraftMutation{SubmissionID: subID, ReviewID: model.NewSubmissionReviewID(), ActorUserID: f.manager.ID, ChangedAt: model.NowUTC(), AuditEventID: saveIntegrityReviewAudit(t, ctx, ss, f, subID, model.ActionSubmissionReview).ID, AuditAt: model.NowUTC()}, examCommand(f.manager.ID, store.ExamIntegrityReviewDraftOperation, "expiry-review", "expiry-review"))
	require.NoError(t, err)
	finalized, err := ss.ExamIntegrityReview().Finalize(ctx, &store.ExamIntegrityReviewFinalize{SubmissionID: subID, ReviewID: draft.Review.ID, ExpectedRevision: draft.Review.Revision, ActorUserID: f.manager.ID, ChangedAt: model.NowUTC(), AuditEventID: saveIntegrityReviewAudit(t, ctx, ss, f, subID, model.ActionSubmissionReview).ID, AuditAt: model.NowUTC()}, examCommand(f.manager.ID, store.ExamIntegrityReviewFinalizeOperation, "expiry-review-final", "expiry-review-final"))
	require.NoError(t, err)
	age(t, ctx, connected.Attempt.ID)
	readAccess := access
	readAccess.ConnectionID = ""
	readAccess.ContinuityCredentialHash = ""
	preview, err := ss.ExamAttempt().BrowserSourceStatus(ctx, store.BrowserDeliveryAccess{Access: readAccess, SourceSessionID: source, ParticipationID: connected.Participation.ID})
	require.NoError(t, err)
	require.True(t, preview.Closure.UnknownTail, "status did not project elapsed deadline")
	nativePreviewAccess := native
	nativePreviewAccess.Access = readAccess
	nativePreview, err := ss.ExamAttempt().NativeDeliveryStatus(ctx, nativePreviewAccess)
	require.NoError(t, err)
	require.True(t, nativePreview.Closure.UnknownTail, "native status did not project expiry")
	require.Equal(t, int64(4), nativePreview.TerminalMissingThroughSequence, "native status did not project expiry")
	_, err = ss.ExamAttempt().ResolveNativeDeliveryTarget(ctx, nativePreviewAccess)
	require.NoError(t, err)
	untouched, err := ss.ExamIntegrityReview().Get(ctx, subID)
	require.NoError(t, err)
	require.Equal(t, model.SubmissionReviewFinalized, untouched.Review.State, "read-only status changed an approved Review")
	require.Equal(t, finalized.Review.Revision, untouched.Review.Revision, "read-only status changed an approved Review")

	due, err = ss.ExamAttempt().ListExpiredDeliveries(ctx, 200)
	require.NoError(t, err)
	require.Len(t, due, 2)
	for _, d := range due {
		input := &store.DeliveryExpiry{Due: d, AuditEventID: model.NewAuditEventID(), AuditAt: model.NowUTC()}
		_, err = ss.ExamAttempt().ExpireDelivery(ctx, input)
		require.Error(t, err, "expiry committed without audit")
		input.AuditEventID = saveExamAttemptAudit(t, ctx, ss, f).ID
		if d.Family == "native" && len(corrupt) > 0 {
			for _, raw := range []string{`{`, `{}`, `{"closed_at":"2026-09-09T12:00:00Z"}`} {
				restore := corrupt[0](d.SourceID, raw)
				changed, err := ss.ExamAttempt().ExpireDelivery(ctx, input)
				restore()
				require.False(t, changed, "corrupt expiry closure: changed")
				require.ErrorIs(t, err, store.ErrInvalidState, "corrupt expiry closure: changed")
			}
		}
		changed, err := ss.ExamAttempt().ExpireDelivery(ctx, input)
		require.NoError(t, err)
		require.True(t, changed, "first expiry was not committed")
		input.AuditEventID = saveExamAttemptAudit(t, ctx, ss, f).ID
		changed, err = ss.ExamAttempt().ExpireDelivery(ctx, input)
		require.NoError(t, err)
		require.False(t, changed, "expiry replay repeated transition")
	}
	due, err = ss.ExamAttempt().ListExpiredDeliveries(ctx, 200)
	require.NoError(t, err)
	require.Empty(t, due, "settled owners remain in expiry scan")
	access.ConnectionID = ""
	access.ContinuityCredentialHash = ""
	status, err := ss.ExamAttempt().BrowserSourceStatus(ctx, store.BrowserDeliveryAccess{Access: access, SourceSessionID: source, ParticipationID: connected.Participation.ID})
	require.NoError(t, err)
	require.Equal(t, int64(0), status.HighestContiguous)
	require.Equal(t, int64(2), status.HighestSeen)
	require.Equal(t, int64(2), status.SettledThrough)
	require.Equal(t, int64(2), status.TerminalMissingThrough)
	require.True(t, status.Closure.UnknownTail)
	native.Access = access
	nativeStatus, err := ss.ExamAttempt().NativeDeliveryStatus(ctx, native)
	require.NoError(t, err)
	require.Equal(t, int64(0), nativeStatus.HighestContiguousBatchSequence)
	require.Equal(t, int64(4), nativeStatus.SettledThroughBatchSequence)
	require.Equal(t, int64(4), nativeStatus.TerminalMissingThroughSequence)
	reopened, err := ss.ExamIntegrityReview().Get(ctx, subID)
	require.NoError(t, err)
	require.Equal(t, model.SubmissionReviewDraft, reopened.Review.State, "audited expiry failed to invalidate changed settlement")
	require.Greater(t, reopened.Review.Revision, finalized.Review.Revision, "audited expiry failed to invalidate changed settlement")
	submission, err := ss.ExamSubmission().Get(ctx, sealed.Receipt.SubmissionID)
	require.NoError(t, err)
	require.Equal(t, sealed.Receipt.ManifestDigest, submission.ManifestDigest)
	require.Equal(t, "incomplete", submission.BrowserActivity.State)
	require.Equal(t, int64(0), submission.BrowserActivity.PendingSourceCount)
	require.Equal(t, int64(1), submission.BrowserActivity.SourceCount)
}
