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

// RetentionSubmissionPrivacyProbe observes physical redaction and advances only
// fixture lifecycle dates. Production eligibility keeps its database clock.
type RetentionSubmissionPrivacyProbe struct {
	AgeCompletion      func(*testing.T, context.Context, model.ExamSittingID)
	ExpireGrace        func(*testing.T, context.Context, model.SubmissionID)
	Inspect            func(*testing.T, context.Context, model.SubmissionID, bool)
	InspectDelivery    func(*testing.T, context.Context, model.SubmissionID, bool)
	InspectOperational func(*testing.T, context.Context, model.SubmissionID)
	InspectBrowser     func(*testing.T, context.Context, model.SubmissionID)
}

// TestRetentionSubmissionPrivacy verifies that integrity can expire separately
// from work without replaying private Review text or recreating attention.
func TestRetentionSubmissionPrivacy(t *testing.T, ss store.Store, probe RetentionSubmissionPrivacyProbe) {
	testRetentionSubmissionPrivacy(t, ss, probe, false, false)
}

func TestRetentionNativeOperationalIndependence(t *testing.T, ss store.Store, probe RetentionSubmissionPrivacyProbe) {
	testRetentionSubmissionPrivacy(t, ss, probe, true, false)
}

func TestRetentionBrowserEvidenceIndependence(t *testing.T, ss store.Store, probe RetentionSubmissionPrivacyProbe) {
	testRetentionSubmissionPrivacy(t, ss, probe, false, true)
}

func testRetentionSubmissionPrivacy(t *testing.T, ss store.Store, probe RetentionSubmissionPrivacyProbe, operationalFirst, browserFirst bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	var f examAttemptFixture
	var connected *store.ExamAttemptConnectResult
	var access store.CandidateAttemptAccess
	if browserFirst {
		policy, err := model.NewBrowserPolicy(true, "start", []model.BrowserPolicyRule{{RuleID: "start", Origin: "https://example.edu", PathPrefix: "/", HostMatch: model.BrowserPolicyHostExact, AllowRedirects: false, BlockedNavigationOutcome: model.BrowserPolicyBlockedNavigationIntegrityEvidence}})
		requireNoError(t, err)
		f = newExamAttemptFixtureWithBrowserPolicy(t, ctx, ss, &policy)
		admitted, focus := connectFocusLossFixture(t, ctx, ss, f, "browser-retirement")
		connected = admitted
		access = store.CandidateAttemptAccess{AttemptID: connected.Attempt.ID, CandidateUserID: f.candidate.ID, SessionID: f.session.ID, DesktopRegistrationID: f.session.DesktopRegistrationID, DPoPKeyThumbprint: f.session.DPoPKeyThumbprint, ConnectionID: connected.Connection.ID, ContinuityCredentialHash: focus.ContinuityCredentialHash}
	} else {
		f, connected, access = newBrowserActivityFixture(t, ctx, ss, "retirement-privacy")
	}
	first, current := browserSourceID(900), browserSourceID(901)
	_, err := startBrowserSourceFixture(t, ctx, ss, &store.BrowserActivitySourceStart{
		Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: first})
	requireNoError(t, err)
	_, err = appendBrowserActivityFixture(t, ctx, ss, &store.BrowserActivityAppend{
		Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		SourceSessionID: first, Events: []model.BrowserActivityEvent{browserActivityEvent(2, model.BrowserActivityOpened, f.revisionID)}})
	requireNoError(t, err)
	_, err = startBrowserSourceFixture(t, ctx, ss, &store.BrowserActivitySourceStart{
		Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		SourceSessionID: current, Transition: model.BrowserStartTransition{Kind: "runtime_reset", PredecessorSourceSessionID: first, Reason: model.BrowserSourceResetSourceCorrupt}})
	requireNoError(t, err)
	appendInput := &store.BrowserActivityAppend{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		SourceSessionID: current, Events: []model.BrowserActivityEvent{browserActivityEvent(1, model.BrowserActivityOpened, f.revisionID), browserActivityEvent(2, model.BrowserActivityClosed, f.revisionID)}}
	if browserFirst {
		rule, reason, prior := "start", model.BrowserBlockRedirectNotAllowed, int64(1)
		nav := browserActivityEvent(1, model.BrowserActivityTopNavigation, f.revisionID)
		nav.MatchedRuleID = &rule
		nav.Location = &model.BrowserLocation{Scheme: "https", Host: "example.edu", Path: "/"}
		blocked := browserActivityEvent(2, model.BrowserActivityBlockedNavigation, f.revisionID)
		blocked.MatchedRuleID = &rule
		blocked.BlockReason = &reason
		blocked.RedirectFromSequence = &prior
		blocked.Location = &model.BrowserLocation{Scheme: "https", Host: "outside.example", Path: "/"}
		appendInput.Events = []model.BrowserActivityEvent{nav, blocked}
	}
	_, err = appendBrowserActivityFixture(t, ctx, ss, appendInput)
	requireNoError(t, err)
	nativeInput := appendRetirementNativeCondition(t, ctx, ss, f, connected, access)
	seal := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: store.ExamSubmissionSealAccess{
		AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		ConnectionID: connected.Connection.ID, CandidateUserID: f.candidate.ID, SessionID: f.session.ID,
		ContinuityCredentialHash: access.ContinuityCredentialHash, ExpectedCurrentRevisionID: f.revisionID,
		ExpectedWorkspaceCursor: connected.Workspace.Cursor, FinalFocusLossSequence: 2},
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, f).ID.String(), AuditAt: model.GetMillis()}
	attachSubmissionReceipt(t, f.candidate, seal)
	sealed, err := ss.ExamSubmission().Seal(ctx, seal, examCommand(f.candidate.ID, store.ExamSubmissionSealOperation, "privacy-seal", "privacy-seal"))
	requireNoError(t, err)
	subID := sealed.Receipt.SubmissionID
	before, err := ss.ExamSubmission().Get(ctx, subID)
	requireNoError(t, err)
	if before.IntegrityState != model.SubmissionIntegrityGapped || before.UnresolvedIntegrityCount == 0 || before.BrowserActivity.SourceCount != 2 {
		t.Fatalf("fixture lacks private integrity header: %#v", before)
	}
	historical := access
	historical.ConnectionID = ""
	historical.ContinuityCredentialHash = ""
	for _, source := range []model.BrowserSourceSessionID{first, current} {
		owner := store.BrowserDeliveryAccess{Access: historical, ParticipationID: connected.Participation.ID, SourceSessionID: source}
		revision := int64(0)
		if source == first {
			_, err = ss.ExamAttempt().DeclareBrowserDeliveryGaps(ctx, &store.BrowserDeliveryGapDeclaration{Access: owner, Declaration: model.DeclareDeliveryGaps{DeclarationID: "privacy-loss", AllocatedThroughSequence: 2, Ranges: []model.SequenceRange{{First: 1, Last: 1}}, Reason: "spool_lost"}, AuditEventID: browserDeliveryAuditFixture(t, ctx, ss, owner), AuditAt: model.GetMillis()}, examCommand(f.candidate.ID, store.BrowserDeliveryGapsOperation, "privacy-loss", "privacy-loss"))
			requireNoError(t, err)
			revision = 1
		}
		key := "privacy-final-" + string(source)
		_, err = ss.ExamAttempt().SealBrowserDelivery(ctx, &store.BrowserDeliveryFinalDeclaration{Access: owner, Declaration: model.FinalDeliveryDeclaration{DeclarationID: key, ExpectedDeclarationRevision: revision, FinalSequence: 2}, AuditEventID: browserDeliveryAuditFixture(t, ctx, ss, owner), AuditAt: model.GetMillis()}, examCommand(f.candidate.ID, store.BrowserDeliveryFinalOperation, key, key))
		requireNoError(t, err)
	}
	draft := &store.ExamIntegrityReviewDraftMutation{SubmissionID: subID, ReviewID: model.NewSubmissionReviewID(),
		ActorUserID: f.manager.ID, ManagerNotes: "private-retirement-review-note", ChangedAt: model.NowUTC(),
		AuditEventID: saveIntegrityReviewAudit(t, ctx, ss, f, subID, model.ActionSubmissionReview).ID.String(), AuditAt: model.GetMillis()}
	draftKey := examCommand(f.manager.ID, store.ExamIntegrityReviewDraftOperation, "privacy-draft", "privacy-draft")
	drafted, err := ss.ExamIntegrityReview().UpdateDraft(ctx, draft, draftKey)
	requireNoError(t, err)
	var browserFlag model.IntegrityFlagID
	if browserFirst {
		flags, err := ss.ExamIntegrityReview().ListFlags(ctx, store.ExamIntegrityFlagListOptions{SubmissionID: subID, Limit: 100})
		requireNoError(t, err)
		if len(flags.Items) != 1 || flags.Items[0].EvidenceCount != 1 {
			t.Fatal("fixture lacks independent Browser evidence")
		}
		browserFlag = flags.Items[0].Flag.ID
		decided, err := ss.ExamIntegrityReview().SaveDecision(ctx, &store.ExamIntegrityReviewDecisionMutation{SubmissionID: subID, ReviewID: drafted.Review.ID, DecisionID: model.NewIntegrityReviewDecisionID(), FlagID: browserFlag, ActorUserID: f.manager.ID, ExpectedReviewRevision: drafted.Review.Revision, Outcome: model.IntegrityReviewInconclusive, PrivateRationale: "Retain the minimized Review copy independently", ChangedAt: model.NowUTC(), AuditEventID: saveIntegrityReviewAudit(t, ctx, ss, f, subID, model.ActionSubmissionReview).ID.String(), AuditAt: model.GetMillis()}, examCommand(f.manager.ID, store.ExamIntegrityReviewDecisionOperation, "retirement-browser-decision", "retirement-browser-decision"))
		requireNoError(t, err)
		drafted.Review = decided.Review
	}
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
	completion, err := ss.ExamRecords().GetCompletion(ctx, f.examID, f.sitting.ID)
	requireNoError(t, err)
	_, err = ss.ExamRecords().CompleteRecords(ctx, &store.ExamRecordsCompletion{
		ExamRecordsMutation: recordsMutation(t, ctx, ss, f.unitID, scope, principal, model.ActionExamRecordsComplete), ExpectedRevision: completion.Completion.Revision, AcknowledgedEvidenceRevision: completion.Completion.EvidenceRevision},
		examCommand(f.manager.ID, store.ExamRecordsCompleteOperation, "privacy-complete", "privacy-complete"))
	requireNoError(t, err)
	probe.AgeCompletion(t, ctx, f.sitting.ID)
	policy, err := ss.RetentionPolicy().Get(ctx)
	requireNoError(t, err)
	replace := &store.RetentionPolicyReplacement{RetentionMutation: store.RetentionMutation{Principal: principal, RecentAuthenticationTTL: time.Hour}, ExpectedRevision: policy.Revision,
		Settings: model.RetentionPolicySettings{IntegrityRetentionDays: 1, ExportRetentionDays: 1, DeletionGraceDays: 1}}
	if browserFirst {
		replace.Settings.IntegrityRetentionDays = 0
		replace.Settings.BrowserActivityRetentionDays = 1
	}
	if operationalFirst {
		replace.Settings.IntegrityRetentionDays = 0
		replace.Settings.SecurityOperationalRetentionDays = 1
	}
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
	expectedIntegrity, expectedOperational, expectedBrowser := int64(1), int64(0), int64(0)
	if browserFirst {
		expectedIntegrity, expectedBrowser = 0, 1
	}
	if operationalFirst {
		expectedIntegrity, expectedOperational = 0, 1
	}
	if preview.Work.Eligible != 0 || int64(preview.Integrity.Eligible) != expectedIntegrity || int64(preview.SecurityOperational.Eligible) != expectedOperational || int64(preview.BrowserActivity.Eligible) != expectedBrowser {
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
	if operationalFirst || browserFirst {
		if operationalFirst {
			probe.InspectOperational(t, ctx, subID)
		}
		if browserFirst {
			probe.InspectBrowser(t, ctx, subID)
			evidence, err := ss.ExamIntegrityReview().ListEvidence(ctx, store.ExamIntegrityEvidenceListOptions{SubmissionID: subID, FlagID: browserFlag, Limit: 100})
			requireNoError(t, err)
			if len(evidence.Items) != 1 || evidence.Items[0].Browser == nil || evidence.Items[0].Validate() != nil {
				t.Fatal("Browser retirement erased the Review copy")
			}
		}
		page, err := ss.ExamIntegrityReview().ListNativeConditions(ctx, store.NativeConditionListOptions{SubmissionID: subID, Limit: 100})
		requireNoError(t, err)
		if len(page.Items) != 1 || page.Items[0].Validate() != nil {
			t.Fatal("operational retirement erased independent native review evidence")
		}
		snapshot, err := ss.ExamIntegrityReview().Get(ctx, subID)
		requireNoError(t, err)
		if snapshot.Review == nil || snapshot.Review.State != model.SubmissionReviewFinalized || snapshot.NativeConditions.Records != 1 {
			t.Fatal("operational retirement invalidated retained integrity inventory")
		}
		return
	}
	probe.Inspect(t, ctx, subID, true)
	nativePage, err := ss.ExamIntegrityReview().ListNativeConditions(ctx, store.NativeConditionListOptions{SubmissionID: subID, Limit: 100})
	requireNoError(t, err)
	if len(nativePage.Items) != 0 {
		t.Fatal("retired native conditions remained readable")
	}
	nativeInput.Access.Access.ConnectionID = ""
	nativeInput.Access.Access.ContinuityCredentialHash = ""
	nativeInput.Batch.BatchSequence = 2
	nativeInput.Batch.Records[0].Occurrence.Status = "repeated"
	nativeInput.Batch.Records[0].Occurrence.RepeatCount = 2
	nativeInput.AuditEventID = saveExamAttemptAudit(t, ctx, ss, f).ID.String()
	nativeInput.AuditAt = model.GetMillis()
	if _, err := ss.ExamAttempt().AppendNativeDelivery(ctx, nativeInput, examCommand(f.candidate.ID, store.NativeDeliveryAppendOperation, "retired-native-new", "retired-native-new")); !errors.Is(err, model.ErrDeliveryExpired) {
		t.Fatalf("new native condition after integrity retirement: %v", err)
	}

	after, err := ss.ExamSubmission().Get(ctx, subID)
	requireNoError(t, err)
	if after.IntegrityState != model.SubmissionIntegrityRetired || !after.IntegrityRetiredAt.Valid || after.BrowserActivity.Validate() != nil || after.BrowserActivity.SourceCount != 2 ||
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
	if _, err := appendBrowserActivityFixture(t, ctx, ss, appendInput); err == nil {
		t.Fatal("old Browser Activity source regained authority")
	}
	statuses, err := ss.ExamAttempt().ListSittingCandidateStatuses(ctx, store.SittingCandidateStatusListOptions{ExamID: f.examID, SittingID: f.sitting.ID, ExcludeCandidateUserID: f.manager.ID, Limit: 100})
	requireNoError(t, err)
	for _, item := range statuses.Items {
		if item.Candidate.UserID == f.candidate.ID && item.IntegrityAttentionCount != 0 {
			t.Fatal("removing finalized Review resurrected retired integrity attention")
		}
	}
	// Independent history remains recoverable after integrity retirement. Then
	// explicitly approve its own period; retirement must preserve lifetime quotas.
	historicalAccess := access
	historicalAccess.ConnectionID = ""
	historicalAccess.ContinuityCredentialHash = ""
	browserAccess := store.BrowserDeliveryAccess{Access: historicalAccess, SourceSessionID: current, ParticipationID: connected.Participation.ID}
	if _, err := ss.ExamAttempt().BrowserSourceStatus(ctx, browserAccess); err != nil {
		t.Fatalf("integrity retirement removed independently retained history: %v", err)
	}
	probe.InspectDelivery(t, ctx, subID, false)
	policy, err = ss.RetentionPolicy().Get(ctx)
	requireNoError(t, err)
	replace.ExpectedRevision = policy.Revision
	replace.Settings.BrowserActivityRetentionDays = 1
	replace.Settings.SecurityOperationalRetentionDays = 1
	prepareRetentionPolicyAttempt(t, ctx, ss, f.institutionID, replace)
	changed, err = ss.RetentionPolicy().Replace(ctx, replace, retentionPolicyCommand(f.manager.ID, "privacy-delivery-policy"))
	requireNoError(t, err)
	preview, err = ss.Retention().CreatePreview(ctx, &store.RetentionPreviewCreation{RetentionMutation: mutation(model.ActionRetentionPolicyView), PreviewID: model.NewRetentionPreviewID(), ExpectedPolicyRevision: changed.Policy.Revision}, examCommand(f.manager.ID, store.RetentionPreviewOperation, "privacy-delivery-preview", "privacy-delivery-preview"))
	requireNoError(t, err)
	if preview.BrowserActivity.Eligible != 1 || preview.SecurityOperational.Eligible != 1 || preview.Work.Eligible != 0 {
		t.Fatalf("independent delivery preview=%#v", preview)
	}
	control, err = ss.Retention().GetControl(ctx)
	requireNoError(t, err)
	_, err = ss.Retention().ChangeControl(ctx, &store.RetentionControlChange{RetentionMutation: mutation(model.ActionRetentionCleanupManage), ExpectedRevision: control.Revision, ExpectedPolicyRevision: changed.Policy.Revision, PreviewID: preview.ID, State: model.RetentionControlEnabled}, examCommand(f.manager.ID, store.RetentionControlOperation, "privacy-delivery-enable", "privacy-delivery-enable"))
	requireNoError(t, err)
	reconcile(store.RetentionReconciliationResult{Scheduled: 2})
	probe.ExpireGrace(t, ctx, subID)
	reconcile(store.RetentionReconciliationResult{Retired: 2})
	probe.InspectDelivery(t, ctx, subID, true)
	if _, err := ss.ExamAttempt().BrowserSourceStatus(ctx, browserAccess); !errors.Is(err, model.ErrDeliveryExpired) {
		t.Fatalf("retired browser owner recovered delivery: %v", err)
	}
	nativeAccess := store.NativeDeliveryAccess{Access: historicalAccess, StreamID: connected.Security.DeliveryStreamID, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation}
	if _, err := ss.ExamAttempt().NativeDeliveryStatus(ctx, nativeAccess); !errors.Is(err, model.ErrDeliveryExpired) {
		t.Fatalf("retired native owner recovered delivery: %v", err)
	}
	after, err = ss.ExamSubmission().Get(ctx, subID)
	requireNoError(t, err)
	if after.ManifestDigest != before.ManifestDigest || after.BrowserActivity.SourceCount != 2 || after.BrowserActivity.PendingSourceCount != 0 || after.BrowserActivity.State != "incomplete" {
		t.Fatalf("retirement changed work or invented Browser completeness: %#v", after)
	}
	reconcile(store.RetentionReconciliationResult{})

}

func appendRetirementNativeCondition(t *testing.T, ctx context.Context, ss store.Store, f examAttemptFixture, connected *store.ExamAttemptConnectResult, access store.CandidateAttemptAccess) *store.NativeDeliveryAppend {
	t.Helper()
	renewal := &store.ExamAttemptParticipationRenewal{AttemptID: access.AttemptID, ParticipationID: connected.Participation.ID, ConnectionID: access.ConnectionID, CandidateUserID: access.CandidateUserID, SessionID: access.SessionID, DesktopRegistrationID: access.DesktopRegistrationID, DPoPKeyThumbprint: access.DPoPKeyThumbprint, ContinuityCredentialHash: access.ContinuityCredentialHash, Generation: connected.Participation.Generation, DesktopCompatibilityPolicyRevision: 1}
	prepareParticipationRenewalCoverage(t, ctx, ss, renewal)
	var source model.NativeSourceCoverage
	for _, candidate := range renewal.SecurityCoverage.Sources {
		if candidate.SourceID == model.NativeSourceCapture {
			source = candidate
		}
	}
	at := model.NowUTC().Truncate(time.Millisecond)
	occurrence := model.NativeOccurrence{Kind: "occurrence", OccurrenceID: model.NewId(), ConditionID: "baseline.capture", DetectorID: "synthetic-baseline", DetectorVersion: 1, CapabilityID: "baseline", Mode: model.NativeClaimEnforce, Status: "opened", FirstObservedAt: at, LastConfirmedAt: at, RepeatCount: 1, Certainty: "complete", SourceRanges: []model.NativeSourceRange{{SourceID: source.SourceID, SourceInstanceID: source.SourceInstanceID, FirstSequence: 0, LastSequence: 1}}}
	b := model.NativeSecurityBatch{StreamID: connected.Security.DeliveryStreamID, BatchSequence: 1, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SecuritySessionID: connected.Security.SecuritySessionID, PolicyDigest: connected.Security.Policy.Digest, ApplicationReleaseID: connected.Security.Policy.ApplicationReleaseID, MatrixID: connected.Security.Policy.MatrixID, Records: []model.NativeRecord{{Occurrence: &occurrence}}}
	input := &store.NativeDeliveryAppend{Access: store.NativeDeliveryAccess{Access: access, StreamID: b.StreamID, ParticipationID: b.ParticipationID, Generation: b.Generation}, Batch: b, DesktopBuild: renewal.DesktopBuild, AuditEventID: saveExamAttemptAudit(t, ctx, ss, f).ID.String(), AuditAt: model.GetMillis()}
	_, err := ss.ExamAttempt().AppendNativeDelivery(ctx, input, examCommand(f.candidate.ID, store.NativeDeliveryAppendOperation, "retirement-native", "retirement-native"))
	requireNoError(t, err)
	return input
}
