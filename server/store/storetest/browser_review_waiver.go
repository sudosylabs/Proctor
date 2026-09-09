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

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestBrowserReviewWaiverStore(t *testing.T, ss store.Store) {
	ctx := context.Background()
	f, c, access := newBrowserActivityFixture(t, ctx, ss, "browser-waiver")
	source := browserSourceID(9101)
	status, err := startBrowserSourceFixture(t, ctx, ss, &store.BrowserActivitySourceStart{Access: access, ParticipationID: c.Participation.ID, Generation: c.Participation.Generation, SourceSessionID: source})
	requireNoError(t, err)
	_, err = appendBrowserActivityFixture(t, ctx, ss, &store.BrowserActivityAppend{Access: access, ParticipationID: c.Participation.ID, Generation: c.Participation.Generation, SourceSessionID: source, Events: []model.BrowserActivityEvent{browserActivityEvent(1, model.BrowserActivityOpened, f.revisionID)}})
	requireNoError(t, err)
	seal := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: store.ExamSubmissionSealAccess{AttemptID: c.Attempt.ID, ParticipationID: c.Participation.ID, Generation: c.Participation.Generation, ConnectionID: c.Connection.ID, CandidateUserID: f.candidate.ID, SessionID: f.session.ID, ContinuityCredentialHash: access.ContinuityCredentialHash, ExpectedCurrentRevisionID: f.revisionID, ExpectedWorkspaceCursor: c.Workspace.Cursor}, AuditEventID: saveExamAttemptAudit(t, ctx, ss, f).ID.String(), AuditAt: model.GetMillis()}
	attachSubmissionReceipt(t, f.candidate, seal)
	sealed, err := ss.ExamSubmission().Seal(ctx, seal, examCommand(f.candidate.ID, store.ExamSubmissionSealOperation, "waiver-submit", "waiver-submit"))
	requireNoError(t, err)
	access.ConnectionID = ""
	access.ContinuityCredentialHash = ""
	owner := store.BrowserDeliveryAccess{Access: access, ParticipationID: c.Participation.ID, SourceSessionID: source}
	_, err = ss.ExamAttempt().SealBrowserDelivery(ctx, &store.BrowserDeliveryFinalDeclaration{Access: owner, Declaration: model.FinalDeliveryDeclaration{DeclarationID: "waiver-final", FinalSequence: 2}, AuditEventID: browserDeliveryAuditFixture(t, ctx, ss, owner), AuditAt: model.GetMillis()}, examCommand(f.candidate.ID, store.BrowserDeliveryFinalOperation, "waiver-final", "waiver-final"))
	requireNoError(t, err)
	closeRecordsSitting(t, ctx, ss, &f)
	role, err := ss.Role().Save(ctx, &model.Role{Name: "waiver-manager-" + model.NewId(), DisplayName: "Records manager", Permissions: []string{string(model.ActionExamRecordsComplete)}})
	requireNoError(t, err)
	_, err = ss.RoleBinding().Save(ctx, &model.RoleBinding{UserID: f.manager.ID, RoleID: role.ID, ScopeType: model.RoleScopeAcademicUnit, ScopeID: f.unitID.String(), StartsAt: model.NowUTC().Add(-time.Minute)})
	requireNoError(t, err)
	_, err = ss.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{UserID: f.manager.ID, AcademicUnitID: f.unitID, StartsAt: model.NowUTC().Add(-time.Minute)})
	requireNoError(t, err)
	principal := saveRecordsPrincipal(t, ctx, ss, f.manager.ID, false)
	scope := model.RetentionHoldScope{ExamID: f.examID, SittingID: f.sitting.ID, SubmissionID: sealed.Receipt.SubmissionID}
	mutation := func() store.ExamRecordsMutation {
		return recordsMutation(t, ctx, ss, f.unitID, scope, principal, model.ActionExamRecordsComplete)
	}
	before, err := ss.ExamIntegrityReview().Get(ctx, scope.SubmissionID)
	requireNoError(t, err)
	if before.Review != nil || before.DeliveryInventoryRevision < 1 {
		t.Fatal("waiver fixture must have late delivery and no Review")
	}
	waiver := &store.ExamRecordsReviewWaiver{ExamRecordsMutation: mutation(), ReasonCode: "review_not_required", PrivateReason: "Acknowledged the current delivery uncertainty."}
	if _, err := ss.ExamRecords().WaiveReview(ctx, waiver, examCommand(f.manager.ID, store.ExamRecordsWaiveReviewOperation, "waiver-stale-zero", "waiver-stale-zero")); !store.IsConflict(err) {
		t.Fatalf("omitted inventory fence accepted late data: %v", err)
	}
	waiver.ExpectedDeliveryInventoryRevision = before.DeliveryInventoryRevision
	waiver.ExamRecordsMutation = mutation()
	first, err := ss.ExamRecords().WaiveReview(ctx, waiver, examCommand(f.manager.ID, store.ExamRecordsWaiveReviewOperation, "waiver-first", "waiver-first"))
	requireNoError(t, err)
	completion, err := ss.ExamRecords().GetCompletion(ctx, f.examID, f.sitting.ID)
	requireNoError(t, err)
	sittingScope := scope
	sittingScope.SubmissionID = ""
	_, err = ss.ExamRecords().CompleteRecords(ctx, &store.ExamRecordsCompletion{ExamRecordsMutation: recordsMutation(t, ctx, ss, f.unitID, sittingScope, principal, model.ActionExamRecordsComplete), ExpectedRevision: completion.Completion.Revision, AcknowledgedEvidenceRevision: completion.Completion.EvidenceRevision}, examCommand(f.manager.ID, store.ExamRecordsCompleteOperation, "waiver-complete", "waiver-complete"))
	requireNoError(t, err)
	_, err = ss.ExamAttempt().AppendHistoricalBrowserDelivery(ctx, &store.BrowserActivityAppend{Access: access, ParticipationID: c.Participation.ID, Generation: c.Participation.Generation, SourceSessionID: source, PolicyRevisionID: status.PolicyRevisionID, PolicyDigest: status.PolicyDigest, Events: []model.BrowserActivityEvent{browserActivityEvent(2, model.BrowserActivityClosed, f.revisionID)}, AuditEventID: browserDeliveryAuditFixture(t, ctx, ss, owner), AuditAt: model.GetMillis()}, examCommand(f.candidate.ID, store.BrowserDeliveryAppendOperation, "waiver-late", "waiver-late"))
	requireNoError(t, err)
	completion, err = ss.ExamRecords().GetCompletion(ctx, f.examID, f.sitting.ID)
	requireNoError(t, err)
	if completion.Completion.IsCurrent() || completion.PendingReviews != 1 {
		t.Fatal("late settlement retained waiver/completion authority")
	}
	current, found, err := ss.ExamRecords().FindReviewWaiver(ctx, scope)
	requireNoError(t, err)
	if !found || !current.InventoryInvalidated || current.DeliveryInventoryRevision != before.DeliveryInventoryRevision {
		t.Fatal("waiver lost its original inventory provenance")
	}
	waiver.ExpectedRevision = first.Waiver.Revision
	waiver.ExamRecordsMutation = mutation()
	if _, err := ss.ExamRecords().WaiveReview(ctx, waiver, examCommand(f.manager.ID, store.ExamRecordsWaiveReviewOperation, "waiver-stale-repeat", "waiver-stale-repeat")); !store.IsConflict(err) {
		t.Fatalf("stale no-Review snapshot silently re-waived: %v", err)
	}
	after, err := ss.ExamIntegrityReview().Get(ctx, scope.SubmissionID)
	requireNoError(t, err)
	waiver.ExpectedDeliveryInventoryRevision = after.DeliveryInventoryRevision
	waiver.ExamRecordsMutation = mutation()
	renewed, err := ss.ExamRecords().WaiveReview(ctx, waiver, examCommand(f.manager.ID, store.ExamRecordsWaiveReviewOperation, "waiver-current", "waiver-current"))
	requireNoError(t, err)
	if renewed.Waiver.InventoryInvalidated || renewed.Waiver.DeliveryInventoryRevision != after.DeliveryInventoryRevision {
		t.Fatal("current explicit waiver failed")
	}
}
