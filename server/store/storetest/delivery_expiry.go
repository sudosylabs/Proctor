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
	"time"

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
	requireNoError(t, err)
	_, err = appendBrowserActivityFixture(t, ctx, ss, &store.BrowserActivityAppend{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: source, Events: []model.BrowserActivityEvent{browserActivityEvent(2, model.BrowserActivityOpened, f.revisionID)}})
	requireNoError(t, err)
	native := store.NativeDeliveryAccess{Access: access, StreamID: connected.Security.DeliveryStreamID, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation}
	_, err = ss.ExamAttempt().DeclareNativeDeliveryGaps(ctx, &store.NativeDeliveryGapDeclaration{Access: native, Declaration: model.DeclareDeliveryGaps{DeclarationID: model.NewId(), AllocatedThroughSequence: 4, Ranges: []model.SequenceRange{{First: 1, Last: 1}}, Reason: "spool_lost"}, AuditEventID: saveExamAttemptAudit(t, ctx, ss, f).ID.String(), AuditAt: model.GetMillis()}, examCommand(f.candidate.ID, store.NativeDeliveryGapsOperation, "expiry-gap", "expiry-gap"))
	requireNoError(t, err)
	seal := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: store.ExamSubmissionSealAccess{AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, ConnectionID: connected.Connection.ID, CandidateUserID: f.candidate.ID, SessionID: f.session.ID, ContinuityCredentialHash: access.ContinuityCredentialHash, ExpectedCurrentRevisionID: f.revisionID, ExpectedWorkspaceCursor: connected.Workspace.Cursor}, AuditEventID: saveExamAttemptAudit(t, ctx, ss, f).ID.String(), AuditAt: model.GetMillis()}
	attachSubmissionReceipt(t, f.candidate, seal)
	sealed, err := ss.ExamSubmission().Seal(ctx, seal, examCommand(f.candidate.ID, store.ExamSubmissionSealOperation, "expiry-seal", "expiry-seal"))
	requireNoError(t, err)
	due, err := ss.ExamAttempt().ListExpiredDeliveries(ctx, 200)
	requireNoError(t, err)
	if len(due) != 0 {
		t.Fatal("unexpired upload selected")
	}
	subID := sealed.Receipt.SubmissionID
	draft, err := ss.ExamIntegrityReview().UpdateDraft(ctx, &store.ExamIntegrityReviewDraftMutation{SubmissionID: subID, ReviewID: model.NewSubmissionReviewID(), ActorUserID: f.manager.ID, ChangedAt: model.NowUTC(), AuditEventID: saveIntegrityReviewAudit(t, ctx, ss, f, subID, model.ActionSubmissionReview).ID.String(), AuditAt: model.GetMillis()}, examCommand(f.manager.ID, store.ExamIntegrityReviewDraftOperation, "expiry-review", "expiry-review"))
	requireNoError(t, err)
	finalized, err := ss.ExamIntegrityReview().Finalize(ctx, &store.ExamIntegrityReviewFinalize{SubmissionID: subID, ReviewID: draft.Review.ID, ExpectedReviewRevision: draft.Review.Revision, ActorUserID: f.manager.ID, ChangedAt: model.NowUTC(), AuditEventID: saveIntegrityReviewAudit(t, ctx, ss, f, subID, model.ActionSubmissionReview).ID.String(), AuditAt: model.GetMillis()}, examCommand(f.manager.ID, store.ExamIntegrityReviewFinalizeOperation, "expiry-review-final", "expiry-review-final"))
	requireNoError(t, err)
	age(t, ctx, connected.Attempt.ID)
	readAccess := access
	readAccess.ConnectionID = ""
	readAccess.ContinuityCredentialHash = ""
	preview, err := ss.ExamAttempt().BrowserSourceStatus(ctx, store.BrowserDeliveryAccess{Access: readAccess, SourceSessionID: source, ParticipationID: connected.Participation.ID})
	requireNoError(t, err)
	if !preview.Closure.UnknownTail {
		t.Fatal("status did not project elapsed deadline")
	}
	nativePreviewAccess := native
	nativePreviewAccess.Access = readAccess
	nativePreview, err := ss.ExamAttempt().NativeDeliveryStatus(ctx, nativePreviewAccess)
	requireNoError(t, err)
	if !nativePreview.Closure.UnknownTail || nativePreview.TerminalMissingThroughSequence != 4 {
		t.Fatal("native status did not project expiry")
	}
	_, err = ss.ExamAttempt().ResolveNativeDeliveryTarget(ctx, nativePreviewAccess)
	requireNoError(t, err)
	untouched, err := ss.ExamIntegrityReview().Get(ctx, subID)
	requireNoError(t, err)
	if untouched.Review.State != model.SubmissionReviewFinalized || untouched.Review.Revision != finalized.Review.Revision {
		t.Fatal("read-only status changed an approved Review")
	}

	due, err = ss.ExamAttempt().ListExpiredDeliveries(ctx, 200)
	requireNoError(t, err)
	if len(due) != 2 {
		t.Fatalf("abandoned sources=%#v", due)
	}
	for _, d := range due {
		input := &store.DeliveryExpiry{Due: d, AuditEventID: model.NewId(), AuditAt: model.GetMillis()}
		if _, err := ss.ExamAttempt().ExpireDelivery(ctx, input); err == nil {
			t.Fatal("expiry committed without audit")
		}
		input.AuditEventID = saveExamAttemptAudit(t, ctx, ss, f).ID.String()
		if d.Family == "native" && len(corrupt) > 0 {
			for _, raw := range []string{`{`, `{}`, `{"closed_at":"2026-09-09T12:00:00Z"}`} {
				restore := corrupt[0](d.SourceID, raw)
				changed, err := ss.ExamAttempt().ExpireDelivery(ctx, input)
				restore()
				if changed || !errors.Is(err, store.ErrInvalidState) {
					t.Fatalf("corrupt expiry closure: changed=%v err=%v", changed, err)
				}
			}
		}
		changed, err := ss.ExamAttempt().ExpireDelivery(ctx, input)
		requireNoError(t, err)
		if !changed {
			t.Fatal("first expiry was not committed")
		}
		input.AuditEventID = saveExamAttemptAudit(t, ctx, ss, f).ID.String()
		changed, err = ss.ExamAttempt().ExpireDelivery(ctx, input)
		requireNoError(t, err)
		if changed {
			t.Fatal("expiry replay repeated transition")
		}
	}
	due, err = ss.ExamAttempt().ListExpiredDeliveries(ctx, 200)
	requireNoError(t, err)
	if len(due) != 0 {
		t.Fatal("settled owners remain in expiry scan")
	}
	access.ConnectionID = ""
	access.ContinuityCredentialHash = ""
	status, err := ss.ExamAttempt().BrowserSourceStatus(ctx, store.BrowserDeliveryAccess{Access: access, SourceSessionID: source, ParticipationID: connected.Participation.ID})
	requireNoError(t, err)
	if status.HighestContiguous != 0 || status.HighestSeen != 2 || status.SettledThrough != 2 || status.TerminalMissingThrough != 2 || !status.Closure.UnknownTail {
		t.Fatalf("expired browser invented completeness: %#v", status)
	}
	native.Access = access
	nativeStatus, err := ss.ExamAttempt().NativeDeliveryStatus(ctx, native)
	requireNoError(t, err)
	if nativeStatus.HighestContiguousBatchSequence != 0 || nativeStatus.SettledThroughBatchSequence != 4 || nativeStatus.TerminalMissingThroughSequence != 4 {
		t.Fatalf("expired native invented receipts: %#v", nativeStatus)
	}
	reopened, err := ss.ExamIntegrityReview().Get(ctx, subID)
	requireNoError(t, err)
	if reopened.Review.State != model.SubmissionReviewDraft || reopened.Review.Revision <= finalized.Review.Revision {
		t.Fatal("audited expiry failed to invalidate changed settlement")
	}
	submission, err := ss.ExamSubmission().Get(ctx, sealed.Receipt.SubmissionID)
	requireNoError(t, err)
	if submission.ManifestDigest != sealed.Receipt.ManifestDigest || submission.BrowserActivity.State != "incomplete" || submission.BrowserActivity.PendingSourceCount != 0 || submission.BrowserActivity.SourceCount != 1 {
		t.Fatalf("expiry changed sealed work or failed settlement: %#v", submission)
	}
}
