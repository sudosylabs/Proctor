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
	role, err := ss.Role().Save(ctx, &model.Role{Name: model.SystemAdministratorRoleName, DisplayName: "System Administrator", Permissions: model.AllActions(), BuiltIn: true})
	requireNoError(t, err)
	_, err = ss.RoleBinding().Save(ctx, &model.RoleBinding{UserID: f.manager.ID, RoleID: role.ID, ScopeType: model.RoleScopeInstitution, ScopeID: f.institutionID.String(), StartsAt: model.NowUTC().Add(-time.Hour)})
	requireNoError(t, err)
	_, err = ss.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{UserID: f.manager.ID, AcademicUnitID: f.unitID, StartsAt: model.NowUTC().Add(-time.Hour)})
	requireNoError(t, err)
	principal := saveRecordsPrincipal(t, ctx, ss, f.manager.ID, true)
	scope := model.RetentionHoldScope{ExamID: f.examID, SittingID: f.sitting.ID, SubmissionID: id}
	closeRecordsSitting(t, ctx, ss, &f)
	_, err = ss.ExamRecords().WaiveReview(ctx, &store.ExamRecordsReviewWaiver{ExamRecordsMutation: recordsMutation(t, ctx, ss, f.unitID, scope, principal, model.ActionExamRecordsComplete), ReasonCode: "review_not_required", PrivateReason: "No review required for this fixture."}, examCommand(f.manager.ID, store.ExamRecordsWaiveReviewOperation, "export-retention-waive", "export-retention-waive"))
	requireNoError(t, err)
	wholeSitting := scope
	wholeSitting.SubmissionID = ""
	_, err = ss.ExamRecords().CompleteRecords(ctx, &store.ExamRecordsCompletion{ExamRecordsMutation: recordsMutation(t, ctx, ss, f.unitID, wholeSitting, principal, model.ActionExamRecordsComplete), ExpectedRevision: 1}, examCommand(f.manager.ID, store.ExamRecordsCompleteOperation, "export-retention-complete", "export-retention-complete"))
	requireNoError(t, err)
	policy, err := ss.RetentionPolicy().Get(ctx)
	requireNoError(t, err)
	replacement := &store.RetentionPolicyReplacement{RetentionMutation: store.RetentionMutation{Principal: principal, RecentAuthenticationTTL: time.Hour}, ExpectedRevision: policy.Revision, Settings: model.RetentionPolicySettings{SubmissionRetentionDays: 1, IntegrityRetentionDays: 1, AuditRetentionDays: 1, ExportRetentionDays: 7, DeletionGraceDays: 1}}
	prepareRetentionPolicyAttempt(t, ctx, ss, f.institutionID, replacement)
	replaced, err := ss.RetentionPolicy().Replace(ctx, replacement, retentionPolicyCommand(f.manager.ID, "export-retention-policy"))
	requireNoError(t, err)
	policy = replaced.Policy
	dates.AgeCompletion(t, ctx, f.sitting.ID)
	mutation := func(action model.Action) store.RetentionMutation {
		a, err := ss.Audit().Save(ctx, &model.AuditEvent{ActorID: principal.UserID, SessionID: principal.SessionID, Action: string(action), Resource: model.Resource{Type: model.ResourceInstitution, ID: f.institutionID.String()}, ScopeType: model.RoleScopeInstitution, ScopeID: f.institutionID.String(), Status: model.AuditStatusAttempt, NodeID: "export-retention"})
		requireNoError(t, err)
		return store.RetentionMutation{Principal: principal, AuditEventID: a.ID.String(), AuditAt: model.GetMillis(), RecentAuthenticationTTL: 10 * time.Minute}
	}
	preview, err := ss.Retention().CreatePreview(ctx, &store.RetentionPreviewCreation{RetentionMutation: mutation(model.ActionRetentionPolicyView), PreviewID: model.NewRetentionPreviewID(), ExpectedPolicyRevision: policy.Revision}, examCommand(f.manager.ID, store.RetentionPreviewOperation, "export-retention-preview", "export-retention-preview"))
	requireNoError(t, err)
	control, err := ss.Retention().GetControl(ctx)
	requireNoError(t, err)
	_, err = ss.Retention().ChangeControl(ctx, &store.RetentionControlChange{RetentionMutation: mutation(model.ActionRetentionCleanupManage), ExpectedRevision: control.Revision, ExpectedPolicyRevision: policy.Revision, PreviewID: preview.ID, State: model.RetentionControlEnabled}, examCommand(f.manager.ID, store.RetentionControlOperation, "export-retention-enable", "export-retention-enable"))
	requireNoError(t, err)
	peer := probe.Peer
	if peer == nil {
		peer = ss.Retention()
	}
	reconcile := func(expected store.RetentionReconciliationResult) {
		a, err := ss.Audit().Save(ctx, &model.AuditEvent{Action: string(model.ActionRetentionCleanupManage), Resource: model.Resource{Type: model.ResourceSubmission, ID: id.String()}, ScopeType: model.RoleScopeInstitution, ScopeID: f.institutionID.String(), Status: model.AuditStatusAttempt, NodeID: "export-retention-peer"})
		requireNoError(t, err)
		result, err := peer.ReconcileSubmission(ctx, &store.RetentionReconciliation{SubmissionID: id, AuditEventID: a.ID.String(), AuditAt: model.GetMillis()})
		requireNoError(t, err)
		if result == nil || *result != expected {
			t.Fatalf("reconcile=%#v want=%#v", result, expected)
		}
	}
	reconcile(store.RetentionReconciliationResult{Scheduled: 2})
	oldNotices, err := ss.Retention().ListNotices(ctx, principal.UserID, "", 100)
	requireNoError(t, err)
	if len(oldNotices) != 2 {
		t.Fatalf("scheduled notices=%d", len(oldNotices))
	}
	expiryID := probe.SeedExpiryGrace(t, ctx, scope)
	input := &store.ExamExportCreation{ExamExportAccess: store.ExamExportAccess{ExamRecordsMutation: recordsMutation(t, ctx, ss, f.unitID, scope, principal, model.ActionExamRecordsExport), ExportID: model.NewExamExportID(), Categories: []model.RetentionCategory{model.RetentionCategoryWork}}, JobID: model.NewJobID(), SubmissionIDs: []model.SubmissionID{id}}
	key := examCommand(f.manager.ID, store.ExamExportCreateOperation, "protected-export", "protected-export")
	created, err := ss.ExamExport().Create(ctx, input, key)
	requireNoError(t, err)
	if created.ExpiresAt.Sub(created.CreatedAt) != 7*24*time.Hour || created.SourceExpiresAt.Sub(created.CreatedAt) != 24*time.Hour {
		t.Fatal("archive/source lifetimes are not independent")
	}
	if probe.HasExpiryGrace(t, ctx, expiryID) {
		t.Fatal("fresh export left stale audit grace")
	}
	currentNotices, err := ss.Retention().ListNotices(ctx, principal.UserID, "", 100)
	requireNoError(t, err)
	if len(currentNotices) != 2 {
		t.Fatalf("source protection notice count=%d", len(currentNotices))
	}
	for _, notice := range currentNotices {
		if notice.CancelledAt.Valid != (notice.Category == model.RetentionCategoryWork) {
			t.Fatalf("source protection cancelled wrong notice: %#v", notice)
		}
	}
	// Integrity may retire independently. The peer cannot retire protected work.
	dates.ExpireGrace(t, ctx, id)
	reconcile(store.RetentionReconciliationResult{Retired: 1})
	reconcile(store.RetentionReconciliationResult{})
	// Freeze work after integrity redaction: no nullable integrity field may be
	// hydrated or copied into the work-only portable document.
	token, err := model.NewJobClaimToken()
	requireNoError(t, err)
	claim, err := ss.Job().ClaimNext(ctx, &store.JobClaimRequest{Types: []model.JobType{model.JobTypeExamExportBuild}, NodeID: "export-retention-builder", ClaimToken: token, LeaseDuration: time.Minute})
	requireNoError(t, err)
	if claim == nil || claim.Job.ID != input.JobID {
		t.Fatal("missing protected export Job")
	}
	build := store.ExamExportBuild{ExamExportArtifact: store.ExamExportArtifact{ExportID: created.ID, AttemptID: claim.Attempt.ID}, JobID: claim.Job.ID, ClaimToken: token}
	snapshot, err := ss.ExamExport().BeginBuild(ctx, &build)
	requireNoError(t, err)
	if len(snapshot.Files) != 2 || strings.Contains(string(snapshot.Records), "private_reason") || strings.Contains(string(snapshot.Records), "unresolved_integrity_count") {
		t.Fatal("work-only archive changed after integrity retirement")
	}
	requireNoError(t, ss.ExamExport().Publish(ctx, &store.ExamExportPublication{ExamExportBuild: build, SizeBytes: 42, SHA256: strings.Repeat("a", 64)}))
	reconcile(store.RetentionReconciliationResult{Scheduled: 1})
	// An exact replay cannot cancel the new grace or renew source protection.
	input.ExamRecordsMutation = recordsMutation(t, ctx, ss, f.unitID, scope, principal, model.ActionExamRecordsExport)
	_, err = ss.ExamExport().Create(ctx, input, key)
	requireNoError(t, err)
	reconcile(store.RetentionReconciliationResult{})
	dates.ExpireGrace(t, ctx, id)
	reconcile(store.RetentionReconciliationResult{Retired: 1})
	access := input.ExamExportAccess
	ready, err := ss.ExamExport().Get(ctx, &access)
	requireNoError(t, err)
	if ready.Export.State != model.ExamExportReady {
		t.Fatal("independent archive disappeared with its source")
	}
	input.ExportID, input.JobID = model.NewExamExportID(), model.NewJobID()
	input.ExamRecordsMutation = recordsMutation(t, ctx, ss, f.unitID, scope, principal, model.ActionExamRecordsExport)
	if _, err := ss.ExamExport().Create(ctx, input, examCommand(f.manager.ID, store.ExamExportCreateOperation, "retired-export", "retired-export")); !store.IsConflict(err) {
		t.Fatalf("retired work was exported: %v", err)
	}
}
