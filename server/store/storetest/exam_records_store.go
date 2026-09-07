// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type ExamRecordsSQLProbe struct{ ConcurrentPeer store.ExamRecordsStore }

// TestExamRecordsStore uses the real delivery and Review aggregates to verify
// post-closure completion, private waivers, late-data staleness and manual holds.
func TestExamRecordsStore(t *testing.T, ss store.Store, probes ...ExamRecordsSQLProbe) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	f, _, flagged, sealed, focusAccess := newIntegrityReviewFixture(t, ctx, ss, "records")
	records := ss.ExamRecords()
	scope := model.RetentionHoldScope{ExamID: f.examID, SittingID: f.sitting.ID}
	submissionScope := scope
	submissionScope.SubmissionID = sealed.Receipt.SubmissionID
	role, err := ss.Role().Save(ctx, &model.Role{Name: "records-manager-" + model.NewId(), DisplayName: "Records manager", Permissions: []string{
		string(model.ActionExamRecordsComplete), string(model.ActionExamRecordsCompleteOverride), string(model.ActionExamRecordsHold), string(model.ActionExamRecordsHoldOverride), string(model.ActionRetentionHoldRelease)}})
	requireNoError(t, err)
	binding, err := ss.RoleBinding().Save(ctx, &model.RoleBinding{UserID: f.manager.ID, RoleID: role.ID, ScopeType: model.RoleScopeAcademicUnit, ScopeID: f.unitID.String(), StartsAt: model.NowUTC().Add(-time.Minute)})
	requireNoError(t, err)
	membership, err := ss.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{UserID: f.manager.ID, AcademicUnitID: f.unitID, StartsAt: model.NowUTC().Add(-time.Minute)})
	requireNoError(t, err)
	principal := saveRecordsPrincipal(t, ctx, ss, f.manager.ID, false)
	mutation := func(scope model.RetentionHoldScope, principal model.Principal, action model.Action) store.ExamRecordsMutation {
		return recordsMutation(t, ctx, ss, f.unitID, scope, principal, action)
	}
	complete := &store.ExamRecordsCompletion{ExamRecordsMutation: mutation(scope, principal, model.ActionExamRecordsComplete), ExpectedRevision: 1}
	if _, err = records.CompleteRecords(ctx, complete, examCommand(f.manager.ID, store.ExamRecordsCompleteOperation, "records-open", "records-open")); !store.IsConflict(err) {
		t.Fatalf("open Sitting completed: %v", err)
	}
	closeRecordsSitting(t, ctx, ss, &f)
	complete.ExamRecordsMutation = mutation(scope, principal, model.ActionExamRecordsComplete)
	if _, err = records.CompleteRecords(ctx, complete, examCommand(f.manager.ID, store.ExamRecordsCompleteOperation, "records-pending", "records-pending")); !store.IsConflict(err) {
		t.Fatalf("undecided review completed: %v", err)
	}
	waiver := &store.ExamRecordsReviewWaiver{ExamRecordsMutation: mutation(submissionScope, principal, model.ActionExamRecordsComplete), ReasonCode: "review_not_required", PrivateReason: "Private record review rationale."}
	if _, err = records.WaiveReview(ctx, waiver, examCommand(f.manager.ID, store.ExamRecordsWaiveReviewOperation, "records-undecided", "records-undecided")); !store.IsConflict(err) {
		t.Fatalf("undecided Flag waived: %v", err)
	}
	_, err = ss.RoleBinding().Save(ctx, &model.RoleBinding{UserID: f.candidate.ID, RoleID: role.ID, ScopeType: model.RoleScopeAcademicUnit, ScopeID: f.unitID.String(), StartsAt: model.NowUTC().Add(-time.Minute)})
	requireNoError(t, err)
	self := *waiver
	self.ExamRecordsMutation = mutation(submissionScope, saveRecordsPrincipal(t, ctx, ss, f.candidate.ID, false), model.ActionExamRecordsCompleteOverride)
	if _, err = records.WaiveReview(ctx, &self, examCommand(f.candidate.ID, store.ExamRecordsWaiveReviewOperation, "records-self", "records-self")); !store.IsConflict(err) {
		t.Fatalf("self waiver accepted: %v", err)
	}
	decision := &store.ExamIntegrityReviewDecisionMutation{SubmissionID: submissionScope.SubmissionID, ReviewID: model.NewSubmissionReviewID(), DecisionID: model.NewIntegrityReviewDecisionID(), FlagID: flagged.Flag.ID,
		ActorUserID: f.manager.ID, Outcome: model.IntegrityReviewInconclusive, PrivateRationale: "Evidence reviewed; no final disposition is needed.", ChangedAt: model.NowUTC(), AuditEventID: saveIntegrityReviewAudit(t, ctx, ss, f, submissionScope.SubmissionID, model.ActionSubmissionReview).ID.String(), AuditAt: model.GetMillis()}
	decided, err := ss.ExamIntegrityReview().SaveDecision(ctx, decision, examCommand(f.manager.ID, store.ExamIntegrityReviewDecisionOperation, "records-decision", "records-decision"))
	requireNoError(t, err)
	waiver.ExpectedReviewRevision = decided.Review.Revision
	waiver.ExamRecordsMutation = mutation(submissionScope, principal, model.ActionExamRecordsComplete)
	waiver.AuditEventID = model.NewId()
	waiverKey := examCommand(f.manager.ID, store.ExamRecordsWaiveReviewOperation, "records-waiver", "records-waiver")
	if _, err = records.WaiveReview(ctx, waiver, waiverKey); err == nil {
		t.Fatal("waiver committed without audit attempt")
	}
	if found, err := ss.CommandOutcome().Has(ctx, waiverKey); err != nil || found {
		t.Fatalf("failed waiver left replay outcome: %v %v", found, err)
	}
	if value, found, err := records.FindReviewWaiver(ctx, submissionScope); err != nil || found || value != nil {
		t.Fatalf("failed waiver persisted: %#v %v %v", value, found, err)
	}
	waiver.ExamRecordsMutation = mutation(submissionScope, principal, model.ActionExamRecordsComplete)
	waived, err := records.WaiveReview(ctx, waiver, waiverKey)
	requireNoError(t, err)
	assertReviewAuditPrivate(t, ctx, ss, waiver.AuditEventID, waiver.PrivateReason)
	if waived.Waiver.Revision != 1 || waived.Replayed {
		t.Fatalf("waiver result = %#v", waived)
	}
	complete.ExamRecordsMutation = mutation(scope, principal, model.ActionExamRecordsComplete)
	completeKey := examCommand(f.manager.ID, store.ExamRecordsCompleteOperation, "records-complete", "records-complete")
	completed, err := records.CompleteRecords(ctx, complete, completeKey)
	requireNoError(t, err)
	if !completed.Completion.IsCurrent() {
		t.Fatalf("completion = %#v", completed)
	}
	first := *completed.Completion
	replayInput := *complete
	replayInput.ExamRecordsMutation = mutation(scope, principal, model.ActionExamRecordsComplete)
	replay, err := records.CompleteRecords(ctx, &replayInput, completeKey)
	requireNoError(t, err)
	if !replay.Replayed || *replay.Completion != first {
		t.Fatal("exact completion replay restarted clock")
	}
	noOp := replayInput
	noOp.ExpectedRevision = first.Revision
	noOp.ExamRecordsMutation = mutation(scope, principal, model.ActionExamRecordsComplete)
	unchanged, err := records.CompleteRecords(ctx, &noOp, examCommand(f.manager.ID, store.ExamRecordsCompleteOperation, "records-noop", "records-noop"))
	requireNoError(t, err)
	if *unchanged.Completion != first {
		t.Fatal("current completion restarted clock")
	}
	draft := &store.ExamIntegrityReviewDraftMutation{SubmissionID: submissionScope.SubmissionID, ReviewID: decided.Review.ID, ActorUserID: f.manager.ID, ExpectedReviewRevision: decided.Review.Revision,
		ManagerNotes: "New private draft material.", ChangedAt: model.NowUTC(), AuditEventID: saveIntegrityReviewAudit(t, ctx, ss, f, submissionScope.SubmissionID, model.ActionSubmissionReview).ID.String(), AuditAt: model.GetMillis()}
	draftResult, err := ss.ExamIntegrityReview().UpdateDraft(ctx, draft, examCommand(f.manager.ID, store.ExamIntegrityReviewDraftOperation, "records-draft", "records-draft"))
	requireNoError(t, err)
	stale, err := records.GetCompletion(ctx, f.examID, f.sitting.ID)
	requireNoError(t, err)
	if stale.Completion.IsCurrent() || !stale.Completion.StaleAt.Valid || stale.PendingReviews != 1 || stale.Completion.EvidenceRevision != 1 {
		t.Fatalf("waived Review edit failed to stale: %#v", stale)
	}
	complete.ExamRecordsMutation = mutation(scope, principal, model.ActionExamRecordsComplete)
	complete.ExpectedRevision = stale.Completion.Revision
	complete.AcknowledgedEvidenceRevision = stale.Completion.EvidenceRevision
	if _, err = records.CompleteRecords(ctx, complete, examCommand(f.manager.ID, store.ExamRecordsCompleteOperation, "records-stale-waiver", "records-stale-waiver")); !store.IsConflict(err) {
		t.Fatalf("stale waiver accepted: %v", err)
	}
	waiver.ExpectedRevision = waived.Waiver.Revision
	waiver.ExpectedReviewRevision = draftResult.Review.Revision
	waiver.ExamRecordsMutation = mutation(submissionScope, principal, model.ActionExamRecordsComplete)
	waived, err = records.WaiveReview(ctx, waiver, examCommand(f.manager.ID, store.ExamRecordsWaiveReviewOperation, "records-renew-waiver", "records-renew-waiver"))
	requireNoError(t, err)
	complete.ExamRecordsMutation = mutation(scope, principal, model.ActionExamRecordsComplete)
	renewed, err := records.CompleteRecords(ctx, complete, examCommand(f.manager.ID, store.ExamRecordsCompleteOperation, "records-renew", "records-renew"))
	requireNoError(t, err)
	if !renewed.Completion.IsCurrent() || !renewed.Completion.CompletedAt.Time.After(first.CompletedAt.Time) {
		t.Fatalf("renewed completion = %#v", renewed)
	}
	late := &store.ExamAttemptFocusLossDiscrepancy{Access: focusAccess, SchemaVersion: model.FocusLossSignalSchemaVersion, DiscrepancyID: model.NewIntegrityDiscrepancyID(), SignalID: model.NewFocusLossSignalID(), Sequence: 2, DurationMilliseconds: 900, Source: model.FocusLossSourceFullscreenExited, AuditEventID: saveExamAttemptAudit(t, ctx, ss, f).ID.String(), AuditAt: model.GetMillis()}
	accepted, err := ss.ExamAttempt().RecordEndedFocusLoss(ctx, late)
	requireNoError(t, err)
	if accepted.Duplicate {
		t.Fatal("fresh late data treated as duplicate")
	}
	stale, err = records.GetCompletion(ctx, f.examID, f.sitting.ID)
	requireNoError(t, err)
	if stale.Completion.IsCurrent() || stale.Completion.EvidenceRevision != 2 || stale.PendingReviews != 1 {
		t.Fatalf("late data failed to stale: %#v", stale)
	}
	duplicate := *late
	duplicate.DiscrepancyID = model.NewIntegrityDiscrepancyID()
	duplicate.SignalID = model.NewFocusLossSignalID()
	duplicate.AuditEventID = saveExamAttemptAudit(t, ctx, ss, f).ID.String()
	accepted, err = ss.ExamAttempt().RecordEndedFocusLoss(ctx, &duplicate)
	requireNoError(t, err)
	afterReplay, err := records.GetCompletion(ctx, f.examID, f.sitting.ID)
	requireNoError(t, err)
	if !accepted.Duplicate || !reflect.DeepEqual(afterReplay, stale) {
		t.Fatalf("duplicate changed records state: %#v vs %#v", afterReplay, stale)
	}
	complete.ExamRecordsMutation = mutation(scope, principal, model.ActionExamRecordsComplete)
	complete.ExpectedRevision = stale.Completion.Revision
	if _, err = records.CompleteRecords(ctx, complete, examCommand(f.manager.ID, store.ExamRecordsCompleteOperation, "records-unacknowledged", "records-unacknowledged")); !store.IsConflict(err) {
		t.Fatalf("unacknowledged late data accepted: %v", err)
	}
	waiver.ExpectedRevision = waived.Waiver.Revision
	waiver.ExpectedDiscrepancyCount = 1
	waiver.ExamRecordsMutation = mutation(submissionScope, principal, model.ActionExamRecordsComplete)
	_, err = records.WaiveReview(ctx, waiver, examCommand(f.manager.ID, store.ExamRecordsWaiveReviewOperation, "records-late-waiver", "records-late-waiver"))
	requireNoError(t, err)
	complete.AcknowledgedEvidenceRevision = stale.Completion.EvidenceRevision
	complete.ExamRecordsMutation = mutation(scope, principal, model.ActionExamRecordsComplete)
	_, err = records.CompleteRecords(ctx, complete, examCommand(f.manager.ID, store.ExamRecordsCompleteOperation, "records-late-complete", "records-late-complete"))
	requireNoError(t, err)
	testRecordsHolds(t, ctx, ss, f, principal, scope, submissionScope, probes)
	_, err = ss.AcademicUnitMember().End(ctx, membership.ID.String(), membership.Revision, model.GetMillis())
	requireNoError(t, err)
	hold := &store.ExamRecordsHoldCreation{ExamRecordsMutation: mutation(scope, principal, model.ActionExamRecordsHold), HoldID: model.NewRetentionHoldID(), ReasonCode: "other", PrivateReason: "Preserve."}
	if _, err = records.CreateHold(ctx, hold, examCommand(f.manager.ID, store.ExamRecordsCreateHoldOperation, "records-member-ended", "records-member-ended")); !store.IsConflict(err) {
		t.Fatalf("ended exact membership granted ordinary authority: %v", err)
	}
	hold.ExamRecordsMutation = mutation(scope, principal, model.ActionExamRecordsHoldOverride)
	overrideKey := examCommand(f.manager.ID, store.ExamRecordsCreateHoldOperation, "records-scoped-override", "records-scoped-override")
	if _, err = records.CreateHold(ctx, hold, overrideKey); err != nil {
		t.Fatalf("nonadministrator scoped override denied: %v", err)
	}
	_, err = ss.RoleBinding().End(ctx, binding.ID.String(), model.GetMillis())
	requireNoError(t, err)
	if _, err = records.CreateHold(ctx, hold, overrideKey); !store.IsConflict(err) {
		t.Fatalf("same-audit replay ignored revoked role: %v", err)
	}
}

func recordsMutation(t *testing.T, ctx context.Context, ss store.Store, unitID model.AcademicUnitID, scope model.RetentionHoldScope, principal model.Principal, action model.Action) store.ExamRecordsMutation {
	t.Helper()
	audit, err := ss.Audit().Save(ctx, &model.AuditEvent{ActorID: principal.UserID, Action: string(action), Resource: scope.Resource(), ScopeType: model.RoleScopeAcademicUnit, ScopeID: unitID.String(), Status: model.AuditStatusAttempt, NodeID: "records-test"})
	requireNoError(t, err)
	return store.ExamRecordsMutation{Scope: scope, Principal: principal, Action: action, AuditEventID: audit.ID.String(), AuditAt: model.GetMillis()}
}
func saveRecordsPrincipal(t *testing.T, ctx context.Context, ss store.Store, userID model.UserID, strong bool) model.Principal {
	t.Helper()
	session, credentials, _ := newSession(userID.String())
	if strong {
		session.AuthenticationStrength = model.AuthenticationMultiFactor
		session.MFACompletedAt = model.OptionalTimeFrom(session.AuthenticatedAt)
	}
	saved, credentials, err := ss.Session().Save(ctx, testSessionCreation(t, ctx, ss, session, credentials, 10))
	requireNoError(t, err)
	return model.Principal{UserID: userID, SessionID: saved.ID, CredentialID: model.PrincipalCredentialID(credentials[0].ID), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: saved.AuthenticationStrength, AuthenticatedAt: saved.AuthenticatedAt, MFACompletedAt: saved.MFACompletedAt, ClientType: saved.ClientType}
}
func closeRecordsSitting(t *testing.T, ctx context.Context, ss store.Store, f *examAttemptFixture) {
	t.Helper()
	at := model.NowUTC()
	_, err := ss.ExamSitting().EarlyClose(ctx, &store.ExamSittingManagerTransition{ExamID: f.examID, SittingID: f.sitting.ID, ActorUserID: f.manager.ID, ExpectedRevision: f.sitting.Revision,
		FinalizeJob: newExamSittingFinalizeJob(t, f.sitting.ID, f.sitting.Revision+1, at), PrivateReason: "Delivery is finished.", ChangedAt: at, AuditEventID: saveExamSittingAudit(t, ctx, ss, f.manager.ID, f.examID, f.unitID).ID.String(), AuditAt: model.GetMillis()}, examCommand(f.manager.ID, "exam.sitting.close.v1", "records-close", "records-close"))
	requireNoError(t, err)
	closed, err := ss.ExamSitting().FinishSealing(ctx, &store.ExamSittingFinishSealing{SittingID: f.sitting.ID, AuditEventID: saveExamSittingSystemAudit(t, ctx, ss, f.sitting.ID, f.unitID).ID.String(), AuditAt: model.GetMillis()})
	requireNoError(t, err)
	f.sitting = closed.Value.Sitting
}

func testRecordsHolds(t *testing.T, ctx context.Context, ss store.Store, f examAttemptFixture, principal model.Principal, scope, submissionScope model.RetentionHoldScope, probes []ExamRecordsSQLProbe) {
	t.Helper()
	records := ss.ExamRecords()
	examScope := model.RetentionHoldScope{ExamID: scope.ExamID}
	var submissionHold *model.RetentionHold
	for i, target := range []model.RetentionHoldScope{examScope, scope, submissionScope} {
		input := &store.ExamRecordsHoldCreation{ExamRecordsMutation: recordsMutation(t, ctx, ss, f.unitID, target, principal, model.ActionExamRecordsHold), HoldID: model.NewRetentionHoldID(), ReasonCode: "integrity_review", PrivateReason: "Private preservation rationale."}
		key := examCommand(f.manager.ID, store.ExamRecordsCreateHoldOperation, input.HoldID.String(), input.HoldID.String())
		result, err := records.CreateHold(ctx, input, key)
		requireNoError(t, err)
		if result.Replayed || result.Hold.Scope != target {
			t.Fatalf("hold scope = %#v", result)
		}
		assertReviewAuditPrivate(t, ctx, ss, input.AuditEventID, input.PrivateReason)
		if i == 2 {
			submissionHold = result.Hold
		}
		input.HoldID = model.NewRetentionHoldID()
		input.AuditEventID = recordsMutation(t, ctx, ss, f.unitID, target, principal, model.ActionExamRecordsHold).AuditEventID
		replayed, err := records.CreateHold(ctx, input, key)
		requireNoError(t, err)
		if !replayed.Replayed || !reflect.DeepEqual(replayed.Hold, result.Hold) {
			t.Fatal("hold replay created or changed hold")
		}
	}
	page, err := records.ListHolds(ctx, store.ExamRecordsHoldListOptions{Scope: submissionScope, Limit: 2})
	requireNoError(t, err)
	if !page.HasMore || len(page.Holds) != 2 {
		t.Fatalf("hold first page = %#v", page)
	}
	next, err := records.ListHolds(ctx, store.ExamRecordsHoldListOptions{Scope: submissionScope, After: page.Holds[1].ID, Limit: 2})
	requireNoError(t, err)
	if next.HasMore || len(next.Holds) != 1 || next.Holds[0].ID.String() <= page.Holds[1].ID.String() {
		t.Fatalf("hold continuation = %#v", next)
	}
	missingAudit := &store.ExamRecordsHoldCreation{ExamRecordsMutation: recordsMutation(t, ctx, ss, f.unitID, examScope, principal, model.ActionExamRecordsHold), HoldID: model.NewRetentionHoldID(), ReasonCode: "other", PrivateReason: "Preserve."}
	missingAudit.AuditEventID = model.NewId()
	if _, err = records.CreateHold(ctx, missingAudit, examCommand(f.manager.ID, store.ExamRecordsCreateHoldOperation, "hold-missing-audit", "hold-missing-audit")); err == nil {
		t.Fatal("hold committed without audit")
	}
	all, err := records.ListHolds(ctx, store.ExamRecordsHoldListOptions{Scope: examScope, Limit: 200})
	requireNoError(t, err)
	if len(all.Holds) != 3 {
		t.Fatalf("failed hold persisted: %#v", all)
	}
	admin := saveUser(t, ctx, ss)
	adminRole, err := ss.Role().Save(ctx, &model.Role{Name: model.SystemAdministratorRoleName, DisplayName: "System administrator", BuiltIn: true, Permissions: []string{string(model.ActionRetentionHoldRelease)}})
	requireNoError(t, err)
	_, err = ss.RoleBinding().Save(ctx, &model.RoleBinding{UserID: admin.ID, RoleID: adminRole.ID, ScopeType: model.RoleScopeInstitution, ScopeID: f.institutionID.String(), StartsAt: model.NowUTC().Add(-time.Minute)})
	requireNoError(t, err)
	weakAdmin := saveRecordsPrincipal(t, ctx, ss, admin.ID, false)
	strongAdmin := saveRecordsPrincipal(t, ctx, ss, admin.ID, true)
	release := &store.ExamRecordsHoldRelease{ExamRecordsMutation: recordsMutation(t, ctx, ss, f.unitID, submissionScope, weakAdmin, model.ActionRetentionHoldRelease), HoldID: submissionHold.ID, ExpectedRevision: 1, ReasonCode: "case_closed", PrivateReason: "Private release rationale.", RecentAuthenticationTTL: 5 * time.Minute}
	if _, err = records.ReleaseHold(ctx, release, examCommand(admin.ID, store.ExamRecordsReleaseHoldOperation, "hold-release-weak", "hold-release-weak")); !store.IsConflict(err) {
		t.Fatalf("weak admin release accepted: %v", err)
	}
	// An in-flight request can retain its original strong Principal after an
	// MFA disablement has downgraded the authoritative Session. The aggregate
	// must use the Session's current assurance even for an otherwise valid key.
	staleStrong := weakAdmin
	staleStrong.AuthenticationStrength = model.AuthenticationMultiFactor
	staleStrong.MFACompletedAt = model.OptionalTimeFrom(staleStrong.AuthenticatedAt)
	release.ExamRecordsMutation = recordsMutation(t, ctx, ss, f.unitID, submissionScope, staleStrong, model.ActionRetentionHoldRelease)
	staleKey := examCommand(admin.ID, store.ExamRecordsReleaseHoldOperation, "hold-release-stale-strength", "hold-release-stale-strength")
	if _, err = records.ReleaseHold(ctx, release, staleKey); !store.IsConflict(err) {
		t.Fatalf("stale MFA assurance released hold: %v", err)
	}
	if found, err := ss.CommandOutcome().Has(ctx, staleKey); err != nil || found {
		t.Fatalf("rejected stale assurance reserved an outcome: %v %v", found, err)
	}
	release.ExamRecordsMutation = recordsMutation(t, ctx, ss, f.unitID, submissionScope, principal, model.ActionRetentionHoldRelease)
	if _, err = records.ReleaseHold(ctx, release, examCommand(f.manager.ID, store.ExamRecordsReleaseHoldOperation, "hold-release-manager", "hold-release-manager")); !store.IsConflict(err) {
		t.Fatalf("ordinary role released hold: %v", err)
	}
	release.ExamRecordsMutation = recordsMutation(t, ctx, ss, f.unitID, submissionScope, strongAdmin, model.ActionRetentionHoldRelease)
	key := examCommand(admin.ID, store.ExamRecordsReleaseHoldOperation, "hold-release", "hold-release")
	released, err := records.ReleaseHold(ctx, release, key)
	requireNoError(t, err)
	if !released.Hold.ReleasedAt.Valid || released.Hold.Revision != 2 {
		t.Fatalf("released hold = %#v", released)
	}
	assertReviewAuditPrivate(t, ctx, ss, release.AuditEventID, release.PrivateReason)
	replay, err := records.ReleaseHold(ctx, release, key)
	requireNoError(t, err)
	if !replay.Replayed || !reflect.DeepEqual(replay.Hold, released.Hold) {
		t.Fatal("hold release retry changed state")
	}
	all, err = records.ListHolds(ctx, store.ExamRecordsHoldListOptions{Scope: submissionScope, Limit: 200})
	requireNoError(t, err)
	if len(all.Holds) != 2 {
		t.Fatalf("released hold listed active: %#v", all)
	}
	all, err = records.ListHolds(ctx, store.ExamRecordsHoldListOptions{Scope: submissionScope, Limit: 200, IncludeReleased: true})
	requireNoError(t, err)
	if len(all.Holds) != 3 {
		t.Fatalf("released hold not inspectable: %#v", all)
	}
	for _, probe := range probes {
		if probe.ConcurrentPeer == nil {
			continue
		}
		testConcurrentRecordsHold(t, ctx, ss, records, probe.ConcurrentPeer, f, principal, scope)
	}
}

func testConcurrentRecordsHold(t *testing.T, ctx context.Context, ss store.Store, first, peer store.ExamRecordsStore, f examAttemptFixture, principal model.Principal, scope model.RetentionHoldScope) {
	t.Helper()
	a := &store.ExamRecordsHoldCreation{ExamRecordsMutation: recordsMutation(t, ctx, ss, f.unitID, scope, principal, model.ActionExamRecordsHold), HoldID: model.NewRetentionHoldID(), ReasonCode: "other", PrivateReason: "Preserve."}
	b := *a
	b.HoldID = model.NewRetentionHoldID()
	b.AuditEventID = recordsMutation(t, ctx, ss, f.unitID, scope, principal, model.ActionExamRecordsHold).AuditEventID
	key := examCommand(f.manager.ID, store.ExamRecordsCreateHoldOperation, "hold-concurrent", "hold-concurrent")
	type outcome struct {
		value *store.ExamRecordsHoldResult
		err   error
	}
	results := make(chan outcome, 2)
	go func() { value, err := first.CreateHold(ctx, a, key); results <- outcome{value, err} }()
	go func() { value, err := peer.CreateHold(ctx, &b, key); results <- outcome{value, err} }()
	x, y := <-results, <-results
	requireNoError(t, x.err)
	requireNoError(t, y.err)
	if x.value.Replayed == y.value.Replayed || !reflect.DeepEqual(x.value.Hold, y.value.Hold) {
		t.Fatalf("concurrent holds diverged: %#v %#v", x, y)
	}
}
