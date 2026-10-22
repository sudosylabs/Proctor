// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type ExamExportRetentionSQLProbe struct {
	Peer            store.RetentionStore
	SeedExpiryGrace func(*testing.T, context.Context, model.RetentionHoldScope) string
	HasExpiryGrace  func(*testing.T, context.Context, string) bool
}

// A construction reference must be authoritative even when reservation and
// publication happen between two scheduler scans on another node.
func TestExamExportRetention(t *testing.T, ss store.Store, dates RetentionSQLProbe, probe ExamExportRetentionSQLProbe) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	f, id, _, _, _ := newRetentionFixture(t, ctx, ss)
	role, err := SaveRole(ctx, ss, &model.Role{Name: model.SystemAdministratorRoleName, DisplayName: "System Administrator", Permissions: model.AllActions(), BuiltIn: true})
	require.NoError(t, err)
	_, err = SaveRoleBinding(ctx, ss, &model.RoleBinding{UserID: f.manager.ID, RoleID: role.ID, ScopeType: model.RoleScopeInstitution, ScopeID: f.institutionID.String(), StartsAt: model.NowUTC().Add(-time.Hour)})
	require.NoError(t, err)
	_, err = ss.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{UserID: f.manager.ID, AcademicUnitID: f.unitID, StartsAt: model.NowUTC().Add(-time.Hour)})
	require.NoError(t, err)
	principal := saveRecordsPrincipal(t, ctx, ss, f.manager.ID, true)
	scope := model.RetentionHoldScope{ExamID: f.examID, SittingID: f.sitting.ID, SubmissionID: id}
	closeRecordsSitting(t, ctx, ss, &f)
	_, err = ss.ExamRecords().WaiveReview(ctx, &store.ExamRecordsReviewWaiver{ExamRecordsMutation: recordsMutation(t, ctx, ss, f.unitID, scope, principal, model.ActionExamRecordsComplete), ReasonCode: "review_not_required", PrivateReason: "No review required for this fixture."}, examCommand(f.manager.ID, store.ExamRecordsWaiveReviewOperation, "export-retention-waive", "export-retention-waive"))
	require.NoError(t, err)
	wholeSitting := scope
	wholeSitting.SubmissionID = ""
	_, err = ss.ExamRecords().CompleteRecords(ctx, &store.ExamRecordsCompletion{ExamRecordsMutation: recordsMutation(t, ctx, ss, f.unitID, wholeSitting, principal, model.ActionExamRecordsComplete), ExpectedRevision: 1}, examCommand(f.manager.ID, store.ExamRecordsCompleteOperation, "export-retention-complete", "export-retention-complete"))
	require.NoError(t, err)
	policy, err := ss.RetentionPolicy().Get(ctx)
	require.NoError(t, err)
	replacement := &store.RetentionPolicyReplacement{RetentionMutation: store.RetentionMutation{Principal: principal, RecentAuthenticationTTL: time.Hour}, ExpectedRevision: policy.Revision, Settings: model.RetentionPolicySettings{SubmissionRetentionDays: 1, IntegrityRetentionDays: 1, AuditRetentionDays: 1, ExportRetentionDays: 7, DeletionGraceDays: 1}}
	prepareRetentionPolicyAttempt(t, ctx, ss, f.institutionID, replacement)
	replaced, err := ss.RetentionPolicy().Replace(ctx, replacement, retentionPolicyCommand(f.manager.ID, "export-retention-policy"))
	require.NoError(t, err)
	policy = replaced.Policy
	dates.AgeCompletion(t, ctx, f.sitting.ID)
	mutation := func(action model.Action) store.RetentionMutation {
		a, err := ss.Audit().Save(ctx, &model.AuditEvent{ActorID: principal.UserID, SessionID: principal.SessionID, Action: string(action), Resource: model.Resource{Type: model.ResourceInstitution, ID: f.institutionID.String()}, ScopeType: model.RoleScopeInstitution, ScopeID: f.institutionID.String(), Status: model.AuditStatusAttempt, NodeID: "export-retention"})
		require.NoError(t, err)
		return store.RetentionMutation{Principal: principal, AuditEventID: a.ID, AuditAt: model.NowUTC(), RecentAuthenticationTTL: 10 * time.Minute}
	}
	preview, err := ss.Retention().CreatePreview(ctx, &store.RetentionPreviewCreation{RetentionMutation: mutation(model.ActionRetentionPolicyView), PreviewID: model.NewRetentionPreviewID(), ExpectedPolicyRevision: policy.Revision}, examCommand(f.manager.ID, store.RetentionPreviewOperation, "export-retention-preview", "export-retention-preview"))
	require.NoError(t, err)
	control, err := ss.Retention().GetControl(ctx)
	require.NoError(t, err)
	_, err = ss.Retention().ChangeControl(ctx, &store.RetentionControlChange{RetentionMutation: mutation(model.ActionRetentionCleanupManage), ExpectedRevision: control.Revision, ExpectedPolicyRevision: policy.Revision, PreviewID: preview.ID, State: model.RetentionControlEnabled}, examCommand(f.manager.ID, store.RetentionControlOperation, "export-retention-enable", "export-retention-enable"))
	require.NoError(t, err)
	peer := probe.Peer
	if peer == nil {
		peer = ss.Retention()
	}
	reconcile := func(expected store.RetentionReconciliationResult) {
		a, err := ss.Audit().Save(ctx, &model.AuditEvent{Action: string(model.ActionRetentionCleanupManage), Resource: model.Resource{Type: model.ResourceSubmission, ID: id.String()}, ScopeType: model.RoleScopeInstitution, ScopeID: f.institutionID.String(), Status: model.AuditStatusAttempt, NodeID: "export-retention-peer"})
		require.NoError(t, err)
		result, err := peer.ReconcileSubmission(ctx, &store.RetentionReconciliation{SubmissionID: id, AuditEventID: a.ID, AuditAt: model.NowUTC()})
		require.NoError(t, err)
		require.NotNil(t, result, "reconcile")
		require.Equal(t, expected, *result, "reconcile")
	}
	reconcile(store.RetentionReconciliationResult{Scheduled: 2})
	oldNotices, err := ss.Retention().ListNotices(ctx, principal.UserID, "", 100)
	require.NoError(t, err)
	require.Len(t, oldNotices, 2, "scheduled notices")
	expiryID := probe.SeedExpiryGrace(t, ctx, scope)
	input := &store.ExamExportCreation{ExamExportAccess: store.ExamExportAccess{ExamRecordsMutation: recordsMutation(t, ctx, ss, f.unitID, scope, principal, model.ActionExamRecordsExport), ExportID: model.NewExamExportID(), Categories: []model.RetentionCategory{model.RetentionCategoryWork}}, JobID: model.NewJobID(), SubmissionIDs: []model.SubmissionID{id}}
	key := examCommand(f.manager.ID, store.ExamExportCreateOperation, "protected-export", "protected-export")
	created, err := ss.ExamExport().Create(ctx, input, key)
	require.NoError(t, err)
	require.Equal(t, 7*24*time.Hour, created.ExpiresAt.Sub(created.CreatedAt), "archive/source lifetimes are not independent")
	require.Equal(t, 24*time.Hour, created.SourceExpiresAt.Sub(created.CreatedAt), "archive/source lifetimes are not independent")
	require.False(t, probe.HasExpiryGrace(t, ctx, expiryID), "fresh export left stale audit grace")
	currentNotices, err := ss.Retention().ListNotices(ctx, principal.UserID, "", 100)
	require.NoError(t, err)
	require.Len(t, currentNotices, 2, "source protection notice count")
	for _, notice := range currentNotices {
		require.Equal(t, notice.Category == model.RetentionCategoryWork, notice.CanceledAt.Valid)
	}
	// Integrity may retire independently. The peer cannot retire protected work.
	dates.ExpireGrace(t, ctx, id)
	reconcile(store.RetentionReconciliationResult{Retired: 1})
	reconcile(store.RetentionReconciliationResult{})
	// Freeze work after integrity redaction: no nullable integrity field may be
	// hydrated or copied into the work-only portable document.
	token, err := model.NewJobClaimToken()
	require.NoError(t, err)
	claim, err := ss.Job().ClaimNext(ctx, &store.JobClaimRequest{Types: []model.JobType{model.JobTypeExamExportBuild}, NodeID: "export-retention-builder", ClaimToken: token, LeaseDuration: time.Minute})
	require.NoError(t, err)
	require.NotNil(t, claim, "missing protected export Job")
	require.Equal(t, input.JobID, claim.Job.ID, "missing protected export Job")
	build := store.ExamExportBuild{ExamExportArtifact: store.ExamExportArtifact{ExportID: created.ID, AttemptID: claim.Attempt.ID}, JobID: claim.Job.ID, ClaimToken: token}
	snapshot, err := ss.ExamExport().BeginBuild(ctx, &build)
	require.NoError(t, err)
	require.Len(t, snapshot.Files, 2, "work-only archive changed after integrity retirement")
	require.NotContains(t, string(snapshot.Records), "private_reason", "work-only archive changed after integrity retirement")
	require.NotContains(t, string(snapshot.Records), "unresolved_integrity_count", "work-only archive changed after integrity retirement")
	require.NoError(t, ss.ExamExport().Publish(ctx, &store.ExamExportPublication{ExamExportBuild: build, SizeBytes: 42, SHA256: strings.Repeat("a", 64)}))
	reconcile(store.RetentionReconciliationResult{Scheduled: 1})
	// An exact replay cannot cancel the new grace or renew source protection.
	input.ExamRecordsMutation = recordsMutation(t, ctx, ss, f.unitID, scope, principal, model.ActionExamRecordsExport)
	_, err = ss.ExamExport().Create(ctx, input, key)
	require.NoError(t, err)
	reconcile(store.RetentionReconciliationResult{})
	dates.ExpireGrace(t, ctx, id)
	reconcile(store.RetentionReconciliationResult{Retired: 1})
	access := input.ExamExportAccess
	ready, err := ss.ExamExport().Get(ctx, &access)
	require.NoError(t, err)
	require.Equal(t, model.ExamExportReady, ready.Export.State, "independent archive disappeared with its source")
	input.ExportID, input.JobID = model.NewExamExportID(), model.NewJobID()
	input.ExamRecordsMutation = recordsMutation(t, ctx, ss, f.unitID, scope, principal, model.ActionExamRecordsExport)
	_, err = ss.ExamExport().Create(ctx, input, examCommand(f.manager.ID, store.ExamExportCreateOperation, "retired-export", "retired-export"))
	require.True(t, store.IsConflict(err), "retired work was exported")
}
