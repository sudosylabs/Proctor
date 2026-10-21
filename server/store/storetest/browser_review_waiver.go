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

func TestBrowserReviewWaiverStore(t *testing.T, ss store.Store) {
	ctx := context.Background()
	f, c, access := newBrowserActivityFixture(t, ctx, ss, "browser-waiver")
	source := browserSourceID(9101)
	status, err := startBrowserSourceFixture(t, ctx, ss, &store.BrowserActivitySourceStart{Access: access, ParticipationID: c.Participation.ID, Generation: c.Participation.Generation, SourceSessionID: source})
	require.NoError(t, err)
	_, err = appendBrowserActivityFixture(t, ctx, ss, &store.BrowserActivityAppend{Access: access, ParticipationID: c.Participation.ID, Generation: c.Participation.Generation, SourceSessionID: source, Events: []model.BrowserActivityEvent{browserActivityEvent(1, model.BrowserActivityOpened, f.revisionID)}})
	require.NoError(t, err)
	seal := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: store.ExamSubmissionSealAccess{AttemptID: c.Attempt.ID, ParticipationID: c.Participation.ID, Generation: c.Participation.Generation, ConnectionID: c.Connection.ID, CandidateUserID: f.candidate.ID, SessionID: f.session.ID, ContinuityCredentialHash: access.ContinuityCredentialHash, ExpectedCurrentRevisionID: f.revisionID, ExpectedWorkspaceCursor: c.Workspace.Cursor}, AuditEventID: saveExamAttemptAudit(t, ctx, ss, f).ID, AuditAt: model.GetMillis()}
	attachSubmissionReceipt(t, ctx, ss, f.candidate, seal)
	sealed, err := ss.ExamSubmission().Seal(ctx, seal, examCommand(f.candidate.ID, store.ExamSubmissionSealOperation, "waiver-submit", "waiver-submit"))
	require.NoError(t, err)
	access.ConnectionID = ""
	access.ContinuityCredentialHash = ""
	owner := store.BrowserDeliveryAccess{Access: access, ParticipationID: c.Participation.ID, SourceSessionID: source}
	_, err = ss.ExamAttempt().SealBrowserDelivery(ctx, &store.BrowserDeliveryFinalDeclaration{Access: owner, Declaration: model.FinalDeliveryDeclaration{DeclarationID: "waiver-final", FinalSequence: 2}, AuditEventID: browserDeliveryAuditFixture(t, ctx, ss, owner), AuditAt: model.GetMillis()}, examCommand(f.candidate.ID, store.BrowserDeliveryFinalOperation, "waiver-final", "waiver-final"))
	require.NoError(t, err)
	closeRecordsSitting(t, ctx, ss, &f)
	role, err := SaveRole(ctx, ss, &model.Role{Name: "waiver-manager-" + model.NewID(), DisplayName: "Records manager", Permissions: []string{string(model.ActionExamRecordsComplete)}})
	require.NoError(t, err)
	_, err = SaveRoleBinding(ctx, ss, &model.RoleBinding{UserID: f.manager.ID, RoleID: role.ID, ScopeType: model.RoleScopeAcademicUnit, ScopeID: f.unitID.String(), StartsAt: model.NowUTC().Add(-time.Minute)})
	require.NoError(t, err)
	_, err = ss.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{UserID: f.manager.ID, AcademicUnitID: f.unitID, StartsAt: model.NowUTC().Add(-time.Minute)})
	require.NoError(t, err)
	principal := saveRecordsPrincipal(t, ctx, ss, f.manager.ID, false)
	scope := model.RetentionHoldScope{ExamID: f.examID, SittingID: f.sitting.ID, SubmissionID: sealed.Receipt.SubmissionID}
	mutation := func() store.ExamRecordsMutation {
		return recordsMutation(t, ctx, ss, f.unitID, scope, principal, model.ActionExamRecordsComplete)
	}
	before, err := ss.ExamIntegrityReview().Get(ctx, scope.SubmissionID)
	require.NoError(t, err)
	require.Nil(t, before.Review, "waiver fixture must have late delivery and no Review")
	require.GreaterOrEqual(t, before.DeliveryInventoryRevision, int64(1), "waiver fixture must have late delivery and no Review")
	waiver := &store.ExamRecordsReviewWaiver{ExamRecordsMutation: mutation(), ReasonCode: "review_not_required", PrivateReason: "Acknowledged the current delivery uncertainty."}
	_, err = ss.ExamRecords().WaiveReview(ctx, waiver, examCommand(f.manager.ID, store.ExamRecordsWaiveReviewOperation, "waiver-stale-zero", "waiver-stale-zero"))
	require.True(t, store.IsConflict(err), "omitted inventory fence accepted late data")
	waiver.ExpectedDeliveryInventoryRevision = before.DeliveryInventoryRevision
	waiver.ExamRecordsMutation = mutation()
	first, err := ss.ExamRecords().WaiveReview(ctx, waiver, examCommand(f.manager.ID, store.ExamRecordsWaiveReviewOperation, "waiver-first", "waiver-first"))
	require.NoError(t, err)
	completion, err := ss.ExamRecords().GetCompletion(ctx, f.examID, f.sitting.ID)
	require.NoError(t, err)
	sittingScope := scope
	sittingScope.SubmissionID = ""
	_, err = ss.ExamRecords().CompleteRecords(ctx, &store.ExamRecordsCompletion{ExamRecordsMutation: recordsMutation(t, ctx, ss, f.unitID, sittingScope, principal, model.ActionExamRecordsComplete), ExpectedRevision: completion.Completion.Revision, AcknowledgedEvidenceRevision: completion.Completion.EvidenceRevision}, examCommand(f.manager.ID, store.ExamRecordsCompleteOperation, "waiver-complete", "waiver-complete"))
	require.NoError(t, err)
	_, err = ss.ExamAttempt().AppendHistoricalBrowserDelivery(ctx, &store.BrowserActivityAppend{Access: access, ParticipationID: c.Participation.ID, Generation: c.Participation.Generation, SourceSessionID: source, PolicyRevisionID: status.PolicyRevisionID, PolicyDigest: status.PolicyDigest, Events: []model.BrowserActivityEvent{browserActivityEvent(2, model.BrowserActivityClosed, f.revisionID)}, AuditEventID: browserDeliveryAuditFixture(t, ctx, ss, owner), AuditAt: model.GetMillis()}, examCommand(f.candidate.ID, store.BrowserDeliveryAppendOperation, "waiver-late", "waiver-late"))
	require.NoError(t, err)
	completion, err = ss.ExamRecords().GetCompletion(ctx, f.examID, f.sitting.ID)
	require.NoError(t, err)
	require.False(t, completion.Completion.IsCurrent(), "late settlement retained waiver/completion authority")
	require.Equal(t, int64(1), completion.PendingReviews, "late settlement retained waiver/completion authority")
	current, found, err := ss.ExamRecords().FindReviewWaiver(ctx, scope)
	require.NoError(t, err)
	require.True(t, found, "waiver lost its original inventory provenance")
	require.True(t, current.InventoryInvalidated, "waiver lost its original inventory provenance")
	require.Equal(t, before.DeliveryInventoryRevision, current.DeliveryInventoryRevision, "waiver lost its original inventory provenance")
	waiver.ExpectedRevision = first.Waiver.Revision
	waiver.ExamRecordsMutation = mutation()
	_, err = ss.ExamRecords().WaiveReview(ctx, waiver, examCommand(f.manager.ID, store.ExamRecordsWaiveReviewOperation, "waiver-stale-repeat", "waiver-stale-repeat"))
	require.True(t, store.IsConflict(err), "stale no-Review snapshot silently re-waived")
	after, err := ss.ExamIntegrityReview().Get(ctx, scope.SubmissionID)
	require.NoError(t, err)
	waiver.ExpectedDeliveryInventoryRevision = after.DeliveryInventoryRevision
	waiver.ExamRecordsMutation = mutation()
	renewed, err := ss.ExamRecords().WaiveReview(ctx, waiver, examCommand(f.manager.ID, store.ExamRecordsWaiveReviewOperation, "waiver-current", "waiver-current"))
	require.NoError(t, err)
	require.False(t, renewed.Waiver.InventoryInvalidated, "current explicit waiver failed")
	require.Equal(t, after.DeliveryInventoryRevision, renewed.Waiver.DeliveryInventoryRevision, "current explicit waiver failed")
}
