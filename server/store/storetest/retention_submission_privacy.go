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

// RetentionSubmissionPrivacyProbe observes physical redaction and advances only
// fixture lifecycle dates. Production eligibility keeps its database clock.
type RetentionSubmissionPrivacyProbe struct {
	AgeCompletion func(*testing.T, context.Context, model.ExamSittingID)
	ExpireGrace   func(*testing.T, context.Context, model.SubmissionID)
	Inspect       func(*testing.T, context.Context, model.SubmissionID, bool)
}

// TestRetentionSubmissionPrivacy verifies that integrity can expire separately
// from work without replaying private Review text or recreating attention.
func TestRetentionSubmissionPrivacy(t *testing.T, ss store.Store, probe RetentionSubmissionPrivacyProbe) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	f, connected, access := newBrowserActivityFixture(t, ctx, ss, "retirement-privacy")
	first, current := browserSourceID(900), browserSourceID(901)
	_, err := ss.ExamAttempt().StartBrowserActivity(ctx, &store.BrowserActivitySourceStart{
		Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: first})
	requireNoError(t, err)
	_, err = ss.ExamAttempt().AppendBrowserActivity(ctx, &store.BrowserActivityAppend{
		Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		SourceSessionID: first, Events: []model.BrowserActivityEvent{browserActivityEvent(2, model.BrowserActivityOpened, f.revisionID)}})
	requireNoError(t, err)
	_, err = ss.ExamAttempt().StartBrowserActivity(ctx, &store.BrowserActivitySourceStart{
		Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		SourceSessionID: current, PredecessorSessionID: first, ResetReason: model.BrowserSourceResetSourceCorrupt})
	requireNoError(t, err)
	appendInput := &store.BrowserActivityAppend{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		SourceSessionID: current, Events: []model.BrowserActivityEvent{browserActivityEvent(1, model.BrowserActivityOpened, f.revisionID), browserActivityEvent(2, model.BrowserActivityClosed, f.revisionID)}}
	_, err = ss.ExamAttempt().AppendBrowserActivity(ctx, appendInput)
	requireNoError(t, err)
	finalSequence := int64(2)
	seal := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: store.ExamSubmissionSealAccess{
		AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		ConnectionID: connected.Connection.ID, CandidateUserID: f.candidate.ID, SessionID: f.session.ID,
		ContinuityCredentialHash: access.ContinuityCredentialHash, ExpectedCurrentRevisionID: f.revisionID,
		ExpectedWorkspaceCursor: connected.Workspace.Cursor, BrowserActivity: model.BrowserActivitySubmission{
			State: model.BrowserActivitySubmissionComplete, SourceSessionID: current, FinalSequence: &finalSequence}},
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, f).ID.String(), AuditAt: model.GetMillis()}
	attachSubmissionReceipt(t, f.candidate, seal)
	sealed, err := ss.ExamSubmission().Seal(ctx, seal, examCommand(f.candidate.ID, store.ExamSubmissionSealOperation, "privacy-seal", "privacy-seal"))
	requireNoError(t, err)
	subID := sealed.Receipt.SubmissionID
	before, err := ss.ExamSubmission().Get(ctx, subID)
	requireNoError(t, err)
	if before.IntegrityState != model.SubmissionIntegrityGapped || before.UnresolvedIntegrityCount == 0 || before.BrowserActivity.SourceSessionID != current {
		t.Fatalf("fixture lacks private integrity header: %#v", before)
	}
	draft := &store.ExamIntegrityReviewDraftMutation{SubmissionID: subID, ReviewID: model.NewSubmissionReviewID(),
		ActorUserID: f.manager.ID, ManagerNotes: "private-retirement-review-note", ChangedAt: model.NowUTC(),
		AuditEventID: saveIntegrityReviewAudit(t, ctx, ss, f, subID, model.ActionSubmissionReview).ID.String(), AuditAt: model.GetMillis()}
	draftKey := examCommand(f.manager.ID, store.ExamIntegrityReviewDraftOperation, "privacy-draft", "privacy-draft")
	drafted, err := ss.ExamIntegrityReview().UpdateDraft(ctx, draft, draftKey)
	requireNoError(t, err)
	_, err = ss.ExamIntegrityReview().Finalize(ctx, &store.ExamIntegrityReviewFinalize{SubmissionID: subID,
		ReviewID: drafted.Review.ID, ActorUserID: f.manager.ID, ExpectedReviewRevision: drafted.Review.Revision,
		ChangedAt: model.NowUTC(), AuditEventID: saveIntegrityReviewAudit(t, ctx, ss, f, subID, model.ActionSubmissionReview).ID.String(), AuditAt: model.GetMillis()},
		examCommand(f.manager.ID, store.ExamIntegrityReviewFinalizeOperation, "privacy-finalize", "privacy-finalize"))
	requireNoError(t, err)
	probe.Inspect(t, ctx, subID, false)
	closeRecordsSitting(t, ctx, ss, &f)
	role, err := ss.Role().Save(ctx, &model.Role{Name: model.SystemAdministratorRoleName, DisplayName: "System Administrator", Permissions: model.AllActions(), BuiltIn: true})
	requireNoError(t, err)
	_, err = ss.RoleBinding().Save(ctx, &model.RoleBinding{UserID: f.manager.ID, RoleID: role.ID, ScopeType: model.RoleScopeInstitution, ScopeID: f.institutionID.String(), StartsAt: model.NowUTC().Add(-time.Hour)})
	requireNoError(t, err)
	_, err = ss.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{UserID: f.manager.ID, AcademicUnitID: f.unitID, StartsAt: model.NowUTC().Add(-time.Hour)})
	requireNoError(t, err)
	principal := saveRecordsPrincipal(t, ctx, ss, f.manager.ID, true)
	scope := model.RetentionHoldScope{ExamID: f.examID, SittingID: f.sitting.ID}
	_, err = ss.ExamRecords().CompleteRecords(ctx, &store.ExamRecordsCompletion{
		ExamRecordsMutation: recordsMutation(t, ctx, ss, f.unitID, scope, principal, model.ActionExamRecordsComplete), ExpectedRevision: 1},
		examCommand(f.manager.ID, store.ExamRecordsCompleteOperation, "privacy-complete", "privacy-complete"))
	requireNoError(t, err)
	probe.AgeCompletion(t, ctx, f.sitting.ID)
	policy, err := ss.RetentionPolicy().Get(ctx)
	requireNoError(t, err)
	replace := &store.RetentionPolicyReplacement{RetentionMutation: store.RetentionMutation{Principal: principal, RecentAuthenticationTTL: time.Hour}, ExpectedRevision: policy.Revision,
		Settings: model.RetentionPolicySettings{IntegrityRetentionDays: 1, ExportRetentionDays: 1, DeletionGraceDays: 1}}
	prepareRetentionPolicyAttempt(t, ctx, ss, f.institutionID, replace)
	changed, err := ss.RetentionPolicy().Replace(ctx, replace, retentionPolicyCommand(f.manager.ID, "privacy-policy"))
	requireNoError(t, err)
	mutation := func(action model.Action) store.RetentionMutation {
		audit, err := ss.Audit().Save(ctx, &model.AuditEvent{ActorID: principal.UserID, SessionID: principal.SessionID, Action: string(action),
			Resource: model.Resource{Type: model.ResourceInstitution, ID: f.institutionID.String()}, ScopeType: model.RoleScopeInstitution,
			ScopeID: f.institutionID.String(), Status: model.AuditStatusAttempt, NodeID: "retirement-privacy"})
		requireNoError(t, err)
		return store.RetentionMutation{Principal: principal, AuditEventID: audit.ID.String(), AuditAt: model.GetMillis(), RecentAuthenticationTTL: 10 * time.Minute}
	}
	preview, err := ss.Retention().CreatePreview(ctx, &store.RetentionPreviewCreation{RetentionMutation: mutation(model.ActionRetentionPolicyView),
		PreviewID: model.NewRetentionPreviewID(), ExpectedPolicyRevision: changed.Policy.Revision},
		examCommand(f.manager.ID, store.RetentionPreviewOperation, "privacy-preview", "privacy-preview"))
	requireNoError(t, err)
	if preview.Work.Eligible != 0 || preview.Integrity.Eligible != 1 {
		t.Fatalf("integrity-only preview=%#v", preview)
	}
	control, err := ss.Retention().GetControl(ctx)
	requireNoError(t, err)
	_, err = ss.Retention().ChangeControl(ctx, &store.RetentionControlChange{RetentionMutation: mutation(model.ActionRetentionCleanupManage),
		ExpectedRevision: control.Revision, ExpectedPolicyRevision: changed.Policy.Revision, PreviewID: preview.ID, State: model.RetentionControlEnabled},
		examCommand(f.manager.ID, store.RetentionControlOperation, "privacy-enable", "privacy-enable"))
	requireNoError(t, err)
	reconcile := func(want store.RetentionReconciliationResult) {
		audit, err := ss.Audit().Save(ctx, &model.AuditEvent{Action: string(model.ActionRetentionCleanupManage),
			Resource: model.Resource{Type: model.ResourceSubmission, ID: subID.String()}, ScopeType: model.RoleScopeInstitution,
			ScopeID: f.institutionID.String(), Status: model.AuditStatusAttempt, NodeID: "retirement-privacy"})
		requireNoError(t, err)
		got, err := ss.Retention().ReconcileSubmission(ctx, &store.RetentionReconciliation{SubmissionID: subID, AuditEventID: audit.ID.String(), AuditAt: model.GetMillis()})
		requireNoError(t, err)
		if got == nil || *got != want {
			t.Fatalf("retirement result=%#v, want=%#v", got, want)
		}
	}
	reconcile(store.RetentionReconciliationResult{Scheduled: 1})
	probe.ExpireGrace(t, ctx, subID)
	reconcile(store.RetentionReconciliationResult{Retired: 1})
	probe.Inspect(t, ctx, subID, true)
	after, err := ss.ExamSubmission().Get(ctx, subID)
	requireNoError(t, err)
	if after.IntegrityState != model.SubmissionIntegrityRetired || !after.IntegrityRetiredAt.Valid || after.BrowserActivity != (model.BrowserActivitySubmission{}) ||
		after.FinalFocusLossSequence != 0 || after.UnresolvedIntegrityCount != 0 || after.ManifestDigest != before.ManifestDigest ||
		after.WorkspaceCursor != before.WorkspaceCursor || after.ManifestEntryCount != before.ManifestEntryCount || after.ManifestTotalFileBytes != before.ManifestTotalFileBytes {
		t.Fatalf("retired integrity/retained work header=%#v", after)
	}
	manifest, err := ss.ExamSubmission().ListManifest(ctx, store.ExamSubmissionManifestListOptions{SubmissionID: subID, Limit: model.ExamSubmissionManifestReadMaximum})
	requireNoError(t, err)
	if len(manifest.Items) != before.ManifestEntryCount {
		t.Fatal("integrity retirement removed work entries")
	}
	if _, err := ss.ExamIntegrityReview().Get(ctx, subID); !store.IsNotFound(err) {
		t.Fatalf("retired Review remained readable: %v", err)
	}
	for _, freshAudit := range []bool{false, true} {
		replay := *draft
		if freshAudit {
			replay.AuditEventID = saveIntegrityReviewAudit(t, ctx, ss, f, subID, model.ActionSubmissionReview).ID.String()
			replay.AuditAt = model.GetMillis()
		}
		if result, err := ss.ExamIntegrityReview().UpdateDraft(ctx, &replay, draftKey); err == nil || result != nil {
			t.Fatal("retired private Review replayed")
		}
	}
	if _, err := ss.ExamAttempt().AppendBrowserActivity(ctx, appendInput); err == nil {
		t.Fatal("old Browser Activity source regained authority")
	}
	statuses, err := ss.ExamAttempt().ListSittingCandidateStatuses(ctx, store.SittingCandidateStatusListOptions{ExamID: f.examID, SittingID: f.sitting.ID, ExcludeCandidateUserID: f.manager.ID, Limit: 100})
	requireNoError(t, err)
	for _, item := range statuses.Items {
		if item.Candidate.UserID == f.candidate.ID && item.IntegrityAttentionCount != 0 {
			t.Fatal("removing finalized Review resurrected retired integrity attention")
		}
	}
}
