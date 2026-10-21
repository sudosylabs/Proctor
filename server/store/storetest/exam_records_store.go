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
	role, err := SaveRole(ctx, ss, &model.Role{Name: "records-manager-" + model.NewID(), DisplayName: "Records manager", Permissions: []string{
		string(model.ActionExamRecordsComplete), string(model.ActionExamRecordsCompleteOverride), string(model.ActionExamRecordsHold), string(model.ActionExamRecordsHoldOverride), string(model.ActionRetentionHoldRelease)}})
	require.NoError(t, err)
	binding, err := SaveRoleBinding(ctx, ss, &model.RoleBinding{UserID: f.manager.ID, RoleID: role.ID, ScopeType: model.RoleScopeAcademicUnit, ScopeID: f.unitID.String(), StartsAt: model.NowUTC().Add(-time.Minute)})
	require.NoError(t, err)
	membership, err := ss.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{UserID: f.manager.ID, AcademicUnitID: f.unitID, StartsAt: model.NowUTC().Add(-time.Minute)})
	require.NoError(t, err)
	principal := saveRecordsPrincipal(t, ctx, ss, f.manager.ID, false)
	mutation := func(scope model.RetentionHoldScope, principal model.Principal, action model.Action) store.ExamRecordsMutation {
		return recordsMutation(t, ctx, ss, f.unitID, scope, principal, action)
	}
	complete := &store.ExamRecordsCompletion{ExamRecordsMutation: mutation(scope, principal, model.ActionExamRecordsComplete), ExpectedRevision: 1}
	_, err = records.CompleteRecords(ctx, complete, examCommand(f.manager.ID, store.ExamRecordsCompleteOperation, "records-open", "records-open"))
	require.True(t, store.IsConflict(err), "open Sitting completed")
	closeRecordsSitting(t, ctx, ss, &f)
	complete.ExamRecordsMutation = mutation(scope, principal, model.ActionExamRecordsComplete)
	_, err = records.CompleteRecords(ctx, complete, examCommand(f.manager.ID, store.ExamRecordsCompleteOperation, "records-pending", "records-pending"))
	require.True(t, store.IsConflict(err), "undecided review completed")
	waiver := &store.ExamRecordsReviewWaiver{ExamRecordsMutation: mutation(submissionScope, principal, model.ActionExamRecordsComplete), ReasonCode: "review_not_required", PrivateReason: "Private record review rationale."}
	_, err = records.WaiveReview(ctx, waiver, examCommand(f.manager.ID, store.ExamRecordsWaiveReviewOperation, "records-undecided", "records-undecided"))
	require.True(t, store.IsConflict(err), "undecided Flag waived")
	_, err = SaveRoleBinding(ctx, ss, &model.RoleBinding{UserID: f.candidate.ID, RoleID: role.ID, ScopeType: model.RoleScopeAcademicUnit, ScopeID: f.unitID.String(), StartsAt: model.NowUTC().Add(-time.Minute)})
	require.NoError(t, err)
	self := *waiver
	self.ExamRecordsMutation = mutation(submissionScope, saveRecordsPrincipal(t, ctx, ss, f.candidate.ID, false), model.ActionExamRecordsCompleteOverride)
	_, err = records.WaiveReview(ctx, &self, examCommand(f.candidate.ID, store.ExamRecordsWaiveReviewOperation, "records-self", "records-self"))
	require.True(t, store.IsConflict(err), "self waiver accepted")
	decision := &store.ExamIntegrityReviewDecisionMutation{SubmissionID: submissionScope.SubmissionID, ReviewID: model.NewSubmissionReviewID(), DecisionID: model.NewIntegrityReviewDecisionID(), FlagID: flagged.Flag.ID,
		ActorUserID: f.manager.ID, Outcome: model.IntegrityReviewInconclusive, PrivateRationale: "Evidence reviewed; no final disposition is needed.", ChangedAt: model.NowUTC(), AuditEventID: saveIntegrityReviewAudit(t, ctx, ss, f, submissionScope.SubmissionID, model.ActionSubmissionReview).ID, AuditAt: model.GetMillis()}
	decided, err := ss.ExamIntegrityReview().SaveDecision(ctx, decision, examCommand(f.manager.ID, store.ExamIntegrityReviewDecisionOperation, "records-decision", "records-decision"))
	require.NoError(t, err)
	waiver.ExpectedReviewRevision = decided.Review.Revision
	waiver.ExamRecordsMutation = mutation(submissionScope, principal, model.ActionExamRecordsComplete)
	waiver.AuditEventID = model.NewAuditEventID()
	waiverKey := examCommand(f.manager.ID, store.ExamRecordsWaiveReviewOperation, "records-waiver", "records-waiver")
	_, err = records.WaiveReview(ctx, waiver, waiverKey)
	require.Error(t, err, "waiver committed without audit attempt")
	found, err := ss.CommandOutcome().Has(ctx, waiverKey)
	require.NoError(t, err, "failed waiver left replay outcome")
	require.False(t, found, "failed waiver left replay outcome")
	value, found, err := records.FindReviewWaiver(ctx, submissionScope)
	require.NoError(t, err, "failed waiver persisted")
	require.False(t, found, "failed waiver persisted")
	require.Nil(t, value, "failed waiver persisted")
	waiver.ExamRecordsMutation = mutation(submissionScope, principal, model.ActionExamRecordsComplete)
	waived, err := records.WaiveReview(ctx, waiver, waiverKey)
	require.NoError(t, err)
	assertReviewAuditPrivate(t, ctx, ss, waiver.AuditEventID, waiver.PrivateReason)
	require.Equal(t, int64(1), waived.Waiver.Revision)
	require.False(t, waived.Replayed)
	complete.ExamRecordsMutation = mutation(scope, principal, model.ActionExamRecordsComplete)
	completeKey := examCommand(f.manager.ID, store.ExamRecordsCompleteOperation, "records-complete", "records-complete")
	completed, err := records.CompleteRecords(ctx, complete, completeKey)
	require.NoError(t, err)
	require.True(t, completed.Completion.IsCurrent())
	first := *completed.Completion
	replayInput := *complete
	replayInput.ExamRecordsMutation = mutation(scope, principal, model.ActionExamRecordsComplete)
	replay, err := records.CompleteRecords(ctx, &replayInput, completeKey)
	require.NoError(t, err)
	require.True(t, replay.Replayed, "exact completion replay restarted clock")
	require.Equal(t, first, *replay.Completion, "exact completion replay restarted clock")
	noOp := replayInput
	noOp.ExpectedRevision = first.Revision
	noOp.ExamRecordsMutation = mutation(scope, principal, model.ActionExamRecordsComplete)
	unchanged, err := records.CompleteRecords(ctx, &noOp, examCommand(f.manager.ID, store.ExamRecordsCompleteOperation, "records-noop", "records-noop"))
	require.NoError(t, err)
	require.Equal(t, first, *unchanged.Completion, "current completion restarted clock")
	draft := &store.ExamIntegrityReviewDraftMutation{SubmissionID: submissionScope.SubmissionID, ReviewID: decided.Review.ID, ActorUserID: f.manager.ID, ExpectedReviewRevision: decided.Review.Revision,
		ManagerNotes: "New private draft material.", ChangedAt: model.NowUTC(), AuditEventID: saveIntegrityReviewAudit(t, ctx, ss, f, submissionScope.SubmissionID, model.ActionSubmissionReview).ID, AuditAt: model.GetMillis()}
	draftResult, err := ss.ExamIntegrityReview().UpdateDraft(ctx, draft, examCommand(f.manager.ID, store.ExamIntegrityReviewDraftOperation, "records-draft", "records-draft"))
	require.NoError(t, err)
	stale, err := records.GetCompletion(ctx, f.examID, f.sitting.ID)
	require.NoError(t, err)
	require.False(t, stale.Completion.IsCurrent())
	require.True(t, stale.Completion.StaleAt.Valid)
	require.Equal(t, int64(1), stale.PendingReviews)
	require.Equal(t, int64(1), stale.Completion.EvidenceRevision)
	complete.ExamRecordsMutation = mutation(scope, principal, model.ActionExamRecordsComplete)
	complete.ExpectedRevision = stale.Completion.Revision
	complete.AcknowledgedEvidenceRevision = stale.Completion.EvidenceRevision
	_, err = records.CompleteRecords(ctx, complete, examCommand(f.manager.ID, store.ExamRecordsCompleteOperation, "records-stale-waiver", "records-stale-waiver"))
	require.True(t, store.IsConflict(err), "stale waiver accepted")
	waiver.ExpectedRevision = waived.Waiver.Revision
	waiver.ExpectedReviewRevision = draftResult.Review.Revision
	waiver.ExamRecordsMutation = mutation(submissionScope, principal, model.ActionExamRecordsComplete)
	waived, err = records.WaiveReview(ctx, waiver, examCommand(f.manager.ID, store.ExamRecordsWaiveReviewOperation, "records-renew-waiver", "records-renew-waiver"))
	require.NoError(t, err)
	complete.ExamRecordsMutation = mutation(scope, principal, model.ActionExamRecordsComplete)
	renewed, err := records.CompleteRecords(ctx, complete, examCommand(f.manager.ID, store.ExamRecordsCompleteOperation, "records-renew", "records-renew"))
	require.NoError(t, err)
	require.True(t, renewed.Completion.IsCurrent())
	require.True(t, renewed.Completion.CompletedAt.Time.After(first.CompletedAt.Time))
	late := &store.ExamAttemptFocusLossDiscrepancy{Access: focusAccess, SchemaVersion: model.FocusLossSignalSchemaVersion, DiscrepancyID: model.NewIntegrityDiscrepancyID(), SignalID: model.NewFocusLossSignalID(), Sequence: 2, DurationMilliseconds: 900, Source: model.FocusLossSourceFullscreenExited, AuditEventID: saveExamAttemptAudit(t, ctx, ss, f).ID, AuditAt: model.GetMillis()}
	accepted, err := ss.ExamAttempt().RecordEndedFocusLoss(ctx, late)
	require.NoError(t, err)
	require.False(t, accepted.Duplicate, "fresh late data treated as duplicate")
	stale, err = records.GetCompletion(ctx, f.examID, f.sitting.ID)
	require.NoError(t, err)
	require.False(t, stale.Completion.IsCurrent())
	require.Equal(t, int64(2), stale.Completion.EvidenceRevision)
	require.Equal(t, int64(1), stale.PendingReviews)
	current, found, findErr := records.FindReviewWaiver(ctx, submissionScope)
	require.NoError(t, findErr, "late Focus Loss discrepancy left the waiver presented as current: %#v", current)
	require.True(t, found, "late Focus Loss discrepancy left the waiver presented as current: %#v", current)
	require.True(t, current.InventoryInvalidated, "late Focus Loss discrepancy left the waiver presented as current: %#v", current)
	duplicate := *late
	duplicate.DiscrepancyID = model.NewIntegrityDiscrepancyID()
	duplicate.SignalID = model.NewFocusLossSignalID()
	duplicate.AuditEventID = saveExamAttemptAudit(t, ctx, ss, f).ID
	accepted, err = ss.ExamAttempt().RecordEndedFocusLoss(ctx, &duplicate)
	require.NoError(t, err)
	afterReplay, err := records.GetCompletion(ctx, f.examID, f.sitting.ID)
	require.NoError(t, err)
	require.True(t, accepted.Duplicate, "duplicate changed records state")
	require.Equal(t, stale, afterReplay, "duplicate changed records state")
	complete.ExamRecordsMutation = mutation(scope, principal, model.ActionExamRecordsComplete)
	complete.ExpectedRevision = stale.Completion.Revision
	_, err = records.CompleteRecords(ctx, complete, examCommand(f.manager.ID, store.ExamRecordsCompleteOperation, "records-unacknowledged", "records-unacknowledged"))
	require.True(t, store.IsConflict(err), "unacknowledged late data accepted")
	waiver.ExpectedRevision = waived.Waiver.Revision
	waiver.ExpectedDiscrepancyCount = 1
	waiver.ExamRecordsMutation = mutation(submissionScope, principal, model.ActionExamRecordsComplete)
	lateWaiver, err := records.WaiveReview(ctx, waiver, examCommand(f.manager.ID, store.ExamRecordsWaiveReviewOperation, "records-late-waiver", "records-late-waiver"))
	require.NoError(t, err)
	require.False(t, lateWaiver.Waiver.InventoryInvalidated)
	require.Equal(t, int64(1), lateWaiver.Waiver.DiscrepancyCount)
	complete.AcknowledgedEvidenceRevision = stale.Completion.EvidenceRevision
	complete.ExamRecordsMutation = mutation(scope, principal, model.ActionExamRecordsComplete)
	_, err = records.CompleteRecords(ctx, complete, examCommand(f.manager.ID, store.ExamRecordsCompleteOperation, "records-late-complete", "records-late-complete"))
	require.NoError(t, err)
	testRecordsHolds(t, ctx, ss, f, principal, scope, submissionScope, probes)
	// PostgreSQL time can trail the host clock, and authority is judged at
	// PostgreSQL time, so end authority safely in the past.
	_, err = EndAcademicUnitMember(ctx, ss, membership.ID, membership.Revision, model.GetMillis()-1000)
	require.NoError(t, err)
	hold := &store.ExamRecordsHoldCreation{ExamRecordsMutation: mutation(scope, principal, model.ActionExamRecordsHold), HoldID: model.NewRetentionHoldID(), ReasonCode: "other", PrivateReason: "Preserve."}
	_, err = records.CreateHold(ctx, hold, examCommand(f.manager.ID, store.ExamRecordsCreateHoldOperation, "records-member-ended", "records-member-ended"))
	require.True(t, store.IsConflict(err), "ended exact membership granted ordinary authority")
	hold.ExamRecordsMutation = mutation(scope, principal, model.ActionExamRecordsHoldOverride)
	overrideKey := examCommand(f.manager.ID, store.ExamRecordsCreateHoldOperation, "records-scoped-override", "records-scoped-override")
	_, err = records.CreateHold(ctx, hold, overrideKey)
	require.NoError(t, err)
	_, err = EndRoleBinding(ctx, ss, binding.ID, model.GetMillis()-1000)
	require.NoError(t, err)
	_, err = records.CreateHold(ctx, hold, overrideKey)
	require.True(t, store.IsConflict(err), "same-audit replay ignored revoked role")
}

func recordsMutation(t *testing.T, ctx context.Context, ss store.Store, unitID model.AcademicUnitID, scope model.RetentionHoldScope, principal model.Principal, action model.Action) store.ExamRecordsMutation {
	t.Helper()
	audit, err := ss.Audit().Save(ctx, &model.AuditEvent{ActorID: principal.UserID, Action: string(action), Resource: scope.Resource(), ScopeType: model.RoleScopeAcademicUnit, ScopeID: unitID.String(), Status: model.AuditStatusAttempt, NodeID: "records-test"})
	require.NoError(t, err)
	return store.ExamRecordsMutation{Scope: scope, Principal: principal, Action: action, AuditEventID: audit.ID, AuditAt: model.GetMillis()}
}
func saveRecordsPrincipal(t *testing.T, ctx context.Context, ss store.Store, userID model.UserID, strong bool) model.Principal {
	t.Helper()
	session, credentials, _ := newSession(userID.String())
	if strong {
		session.AuthenticationStrength = model.AuthenticationMultiFactor
		session.MFACompletedAt = model.OptionalTimeFrom(session.AuthenticatedAt)
	}
	saved, credentials, err := SaveSession(ctx, ss, testSessionCreation(t, ctx, ss, session, credentials, 10))
	require.NoError(t, err)
	return model.Principal{UserID: userID, SessionID: saved.ID, CredentialID: model.PrincipalCredentialID(credentials[0].ID), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: saved.AuthenticationStrength, AuthenticatedAt: saved.AuthenticatedAt, MFACompletedAt: saved.MFACompletedAt, ClientType: saved.ClientType}
}
func closeRecordsSitting(t *testing.T, ctx context.Context, ss store.Store, f *examAttemptFixture) {
	t.Helper()
	at := model.NowUTC()
	_, err := ss.ExamSitting().EarlyClose(ctx, &store.ExamSittingManagerTransition{ExamID: f.examID, SittingID: f.sitting.ID, ActorUserID: f.manager.ID, ExpectedRevision: f.sitting.Revision,
		FinalizeJob: newExamSittingFinalizeJob(t, f.sitting.ID, f.sitting.Revision+1, at), PrivateReason: "Delivery is finished.", ChangedAt: at, AuditEventID: saveExamSittingAudit(t, ctx, ss, f.manager.ID, f.examID, f.unitID).ID, AuditAt: model.GetMillis()}, examCommand(f.manager.ID, "exam.sitting.close.v1", "records-close", "records-close"))
	require.NoError(t, err)
	closed, err := ss.ExamSitting().FinishSealing(ctx, &store.ExamSittingFinishSealing{SittingID: f.sitting.ID, AuditEventID: saveExamSittingSystemAudit(t, ctx, ss, f.sitting.ID, f.unitID).ID, AuditAt: model.GetMillis()})
	require.NoError(t, err)
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
		require.NoError(t, err)
		require.False(t, result.Replayed)
		require.Equal(t, target, result.Hold.Scope)
		assertReviewAuditPrivate(t, ctx, ss, input.AuditEventID, input.PrivateReason)
		if i == 2 {
			submissionHold = result.Hold
		}
		input.HoldID = model.NewRetentionHoldID()
		input.AuditEventID = recordsMutation(t, ctx, ss, f.unitID, target, principal, model.ActionExamRecordsHold).AuditEventID
		replayed, err := records.CreateHold(ctx, input, key)
		require.NoError(t, err)
		require.True(t, replayed.Replayed, "hold replay created or changed hold")
		require.Equal(t, result.Hold, replayed.Hold, "hold replay created or changed hold")
	}
	page, err := records.ListHolds(ctx, store.ExamRecordsHoldListOptions{Scope: submissionScope, Limit: 2})
	require.NoError(t, err)
	require.True(t, page.HasMore)
	require.Len(t, page.Holds, 2)
	next, err := records.ListHolds(ctx, store.ExamRecordsHoldListOptions{Scope: submissionScope, After: page.Holds[1].ID, Limit: 2})
	require.NoError(t, err)
	require.False(t, next.HasMore)
	require.Len(t, next.Holds, 1)
	require.Greater(t, next.Holds[0].ID.String(), page.Holds[1].ID.String())
	missingAudit := &store.ExamRecordsHoldCreation{ExamRecordsMutation: recordsMutation(t, ctx, ss, f.unitID, examScope, principal, model.ActionExamRecordsHold), HoldID: model.NewRetentionHoldID(), ReasonCode: "other", PrivateReason: "Preserve."}
	missingAudit.AuditEventID = model.NewAuditEventID()
	_, err = records.CreateHold(ctx, missingAudit, examCommand(f.manager.ID, store.ExamRecordsCreateHoldOperation, "hold-missing-audit", "hold-missing-audit"))
	require.Error(t, err, "hold committed without audit")
	all, err := records.ListHolds(ctx, store.ExamRecordsHoldListOptions{Scope: examScope, Limit: 200})
	require.NoError(t, err)
	require.Len(t, all.Holds, 3)
	admin := saveUser(t, ctx, ss)
	adminRole, err := SaveRole(ctx, ss, &model.Role{Name: model.SystemAdministratorRoleName, DisplayName: "System administrator", BuiltIn: true, Permissions: []string{string(model.ActionRetentionHoldRelease)}})
	require.NoError(t, err)
	_, err = SaveRoleBinding(ctx, ss, &model.RoleBinding{UserID: admin.ID, RoleID: adminRole.ID, ScopeType: model.RoleScopeInstitution, ScopeID: f.institutionID.String(), StartsAt: model.NowUTC().Add(-time.Minute)})
	require.NoError(t, err)
	weakAdmin := saveRecordsPrincipal(t, ctx, ss, admin.ID, false)
	strongAdmin := saveRecordsPrincipal(t, ctx, ss, admin.ID, true)
	release := &store.ExamRecordsHoldRelease{ExamRecordsMutation: recordsMutation(t, ctx, ss, f.unitID, submissionScope, weakAdmin, model.ActionRetentionHoldRelease), HoldID: submissionHold.ID, ExpectedRevision: 1, ReasonCode: "case_closed", PrivateReason: "Private release rationale.", RecentAuthenticationTTL: 5 * time.Minute}
	_, err = records.ReleaseHold(ctx, release, examCommand(admin.ID, store.ExamRecordsReleaseHoldOperation, "hold-release-weak", "hold-release-weak"))
	require.True(t, store.IsConflict(err), "weak admin release accepted")
	// An in-flight request can retain its original strong Principal after an
	// MFA disablement has downgraded the authoritative Session. The aggregate
	// must use the Session's current assurance even for an otherwise valid key.
	staleStrong := weakAdmin
	staleStrong.AuthenticationStrength = model.AuthenticationMultiFactor
	staleStrong.MFACompletedAt = model.OptionalTimeFrom(staleStrong.AuthenticatedAt)
	release.ExamRecordsMutation = recordsMutation(t, ctx, ss, f.unitID, submissionScope, staleStrong, model.ActionRetentionHoldRelease)
	staleKey := examCommand(admin.ID, store.ExamRecordsReleaseHoldOperation, "hold-release-stale-strength", "hold-release-stale-strength")
	_, err = records.ReleaseHold(ctx, release, staleKey)
	require.True(t, store.IsConflict(err), "stale MFA assurance released hold")
	found, err := ss.CommandOutcome().Has(ctx, staleKey)
	require.NoError(t, err, "rejected stale assurance reserved an outcome")
	require.False(t, found, "rejected stale assurance reserved an outcome")
	release.ExamRecordsMutation = recordsMutation(t, ctx, ss, f.unitID, submissionScope, principal, model.ActionRetentionHoldRelease)
	_, err = records.ReleaseHold(ctx, release, examCommand(f.manager.ID, store.ExamRecordsReleaseHoldOperation, "hold-release-manager", "hold-release-manager"))
	require.True(t, store.IsConflict(err), "ordinary role released hold")
	release.ExamRecordsMutation = recordsMutation(t, ctx, ss, f.unitID, submissionScope, strongAdmin, model.ActionRetentionHoldRelease)
	key := examCommand(admin.ID, store.ExamRecordsReleaseHoldOperation, "hold-release", "hold-release")
	released, err := records.ReleaseHold(ctx, release, key)
	require.NoError(t, err)
	require.True(t, released.Hold.ReleasedAt.Valid)
	require.Equal(t, int64(2), released.Hold.Revision)
	assertReviewAuditPrivate(t, ctx, ss, release.AuditEventID, release.PrivateReason)
	replay, err := records.ReleaseHold(ctx, release, key)
	require.NoError(t, err)
	require.True(t, replay.Replayed, "hold release retry changed state")
	require.Equal(t, released.Hold, replay.Hold, "hold release retry changed state")
	all, err = records.ListHolds(ctx, store.ExamRecordsHoldListOptions{Scope: submissionScope, Limit: 200})
	require.NoError(t, err)
	require.Len(t, all.Holds, 2)
	all, err = records.ListHolds(ctx, store.ExamRecordsHoldListOptions{Scope: submissionScope, Limit: 200, IncludeReleased: true})
	require.NoError(t, err)
	require.Len(t, all.Holds, 3)
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
	require.NoError(t, x.err)
	require.NoError(t, y.err)
	require.NotEqual(t, y.value.Replayed, x.value.Replayed, "concurrent holds diverged: %#v %#v", x, y)
	require.Equal(t, y.value.Hold, x.value.Hold, "concurrent holds diverged: %#v %#v", x, y)
}
