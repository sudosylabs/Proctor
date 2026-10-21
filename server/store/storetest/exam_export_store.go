// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type ExamExportSQLProbe struct {
	Peer                  store.ExamExportStore
	SetPolicy             func(*testing.T, context.Context, int)
	SourceProtectionCount func(*testing.T, context.Context, model.ExamExportID) int
	Expire                func(*testing.T, context.Context, model.ExamExportID)
	ArtifactCount         func(*testing.T, context.Context, model.ExamExportID) int
}

func TestExamExportStore(t *testing.T, ss store.Store, probe ExamExportSQLProbe) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	f, _, flagged, sealed, _ := newIntegrityReviewFixture(t, ctx, ss, "export")
	scope := model.RetentionHoldScope{ExamID: f.examID, SittingID: f.sitting.ID, SubmissionID: sealed.Receipt.SubmissionID}
	principal := saveRecordsPrincipal(t, ctx, ss, f.manager.ID, false)
	permissions := []string{string(model.ActionExamRecordsExport), string(model.ActionExamRecordsExportOverride), string(model.ActionSubmissionView), string(model.ActionSubmissionViewOverride), string(model.ActionExamSittingView), string(model.ActionExamSittingViewOverride), string(model.ActionExamAttemptBrowserActivityView)}
	role, err := SaveRole(ctx, ss, &model.Role{Name: "export-manager-" + model.NewID(), DisplayName: "Export manager", Permissions: permissions})
	require.NoError(t, err)
	binding, err := SaveRoleBinding(ctx, ss, &model.RoleBinding{UserID: f.manager.ID, RoleID: role.ID, ScopeType: model.RoleScopeAcademicUnit, ScopeID: f.unitID.String(), StartsAt: model.NowUTC().Add(-time.Minute)})
	require.NoError(t, err)
	_, err = ss.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{UserID: f.manager.ID, AcademicUnitID: f.unitID, StartsAt: model.NowUTC().Add(-time.Minute)})
	require.NoError(t, err)
	exports := ss.ExamExport()
	selected, err := exports.ListScope(ctx, scope)
	require.NoError(t, err)
	require.Len(t, selected, 1)
	require.Equal(t, scope.SubmissionID, selected[0].SubmissionID)
	makeInput := func(categories ...model.RetentionCategory) *store.ExamExportCreation {
		return &store.ExamExportCreation{ExamExportAccess: store.ExamExportAccess{ExamRecordsMutation: recordsMutation(t, ctx, ss, f.unitID, scope, principal, model.ActionExamRecordsExport), ExportID: model.NewExamExportID(), Categories: categories}, JobID: model.NewJobID(), SubmissionIDs: []model.SubmissionID{scope.SubmissionID}}
	}
	input := makeInput(model.RetentionCategoryWork)
	command := examCommand(f.manager.ID, store.ExamExportCreateOperation, "export-work", "export-work")
	_, err = exports.Create(ctx, input, command)
	require.True(t, store.IsConflict(err), "unconfigured expiry accepted")
	probe.SetPolicy(t, ctx, 1)
	broken := *input
	broken.AuditEventID = model.NewAuditEventID()
	_, err = exports.Create(ctx, &broken, command)
	require.Error(t, err, "export committed without its audit")
	require.Equal(t, 0, probe.SourceProtectionCount(t, ctx, input.ExportID), "rollback left")
	created, err := exports.Create(ctx, input, command)
	require.NoError(t, err)
	require.NoError(t, created.Validate())
	require.Equal(t, model.ExamExportQueued, created.State)
	require.Equal(t, 24*time.Hour, created.ExpiresAt.Sub(created.CreatedAt))
	require.Equal(t, created.ExpiresAt, created.SourceExpiresAt)
	require.Equal(t, 1, probe.SourceProtectionCount(t, ctx, created.ID), "source protections")
	resolved, err := exports.ListCreationScope(ctx, scope, command)
	require.NoError(t, err)
	require.True(t, slices.Equal(resolved, selected), "retained command did not resolve its original captured set")
	changedCommand := *command
	changedCommand.Fingerprint[0] ^= 1
	_, err = exports.ListCreationScope(ctx, scope, &changedCommand)
	require.Error(t, err, "changed request fingerprint reused an original capture")
	wrongCapture := scope
	wrongCapture.SubmissionID = ""
	_, err = exports.ListCreationScope(ctx, wrongCapture, command)
	require.True(t, store.IsNotFound(err), "original outcome disclosed across scope: %v", err)
	// A peer observes the exact same durable protections before bytes exist.
	peer := probe.Peer
	if peer == nil {
		peer = exports
	}
	access := input.ExamExportAccess
	value, err := peer.Get(ctx, &access)
	require.NoError(t, err)
	require.Equal(t, created.ID, value.Export.ID)
	require.Len(t, value.Submissions, 1)
	// A live permission edit must take effect across nodes despite an unchanged
	// Session principal or a previously successful metadata read.
	withoutPermission := func(action model.Action, check func()) {
		changed := *role
		changed.Permissions = slices.DeleteFunc(slices.Clone(permissions), func(p string) bool { return p == string(action) })
		_, err = ss.Role().SeedUpdate(ctx, &changed)
		require.NoError(t, err)
		check()
		changed.Permissions = slices.Clone(permissions)
		role, err = ss.Role().SeedUpdate(ctx, &changed)
		require.NoError(t, err)
	}
	for _, action := range []model.Action{model.ActionExamRecordsExport, model.ActionSubmissionView} {
		withoutPermission(action, func() {
			_, getErr := peer.Get(ctx, &access)
			require.True(t, store.IsConflict(getErr), "missing %s still read export", action)
		})
	}
	bulkScope := scope
	bulkScope.SubmissionID = ""
	bulk := makeInput(model.RetentionCategoryWork)
	bulk.Scope = bulkScope
	bulk.ExamRecordsMutation = recordsMutation(t, ctx, ss, f.unitID, bulkScope, principal, model.ActionExamRecordsExport)
	withoutPermission(model.ActionExamSittingView, func() {
		_, createErr := exports.Create(ctx, bulk, examCommand(f.manager.ID, store.ExamExportCreateOperation, "export-bulk-no-read", "export-bulk-no-read"))
		require.True(t, store.IsConflict(createErr), "Sitting export skipped Sitting authority")
	})
	changedScope := makeInput(model.RetentionCategoryWork)
	changedScope.SubmissionIDs = []model.SubmissionID{model.NewSubmissionID()}
	_, err = exports.Create(ctx, changedScope, examCommand(f.manager.ID, store.ExamExportCreateOperation, "export-changed-scope", "export-changed-scope"))
	require.True(t, store.IsConflict(err), "changed capture set accepted: %v", err)
	second := access
	second.Principal = saveRecordsPrincipal(t, ctx, ss, f.manager.ID, false)
	_, err = peer.Get(ctx, &second)
	require.NoError(t, err)
	_, err = ss.Session().Withdraw(ctx, second.Principal.SessionID, f.manager.ID, model.GetMillis(), model.SessionRevocationInactiveUser)
	require.NoError(t, err)
	_, err = peer.Get(ctx, &second)
	require.True(t, store.IsConflict(err), "revoked Session retained export access: %v", err)
	foreign := access
	foreign.Principal = saveRecordsPrincipal(t, ctx, ss, f.candidate.ID, false)
	_, err = peer.Get(ctx, &foreign)
	require.True(t, store.IsNotFound(err), "nonrequester got export")
	_, err = SaveRoleBinding(ctx, ss, &model.RoleBinding{UserID: f.candidate.ID, RoleID: role.ID, ScopeType: model.RoleScopeAcademicUnit, ScopeID: f.unitID.String(), StartsAt: model.NowUTC().Add(-time.Minute)})
	require.NoError(t, err)
	self := makeInput(model.RetentionCategoryWork)
	self.ExamRecordsMutation = recordsMutation(t, ctx, ss, f.unitID, scope, foreign.Principal, model.ActionExamRecordsExportOverride)
	_, err = exports.Create(ctx, self, examCommand(f.candidate.ID, store.ExamExportCreateOperation, "export-self", "export-self"))
	require.True(t, store.IsNotFound(err), "override allowed self export: %v", err)
	wrong := access
	wrong.Scope.SubmissionID = ""
	_, err = peer.Get(ctx, &wrong)
	require.True(t, store.IsNotFound(err), "cross-scope export disclosed")
	replayInput := *input
	replayInput.ExportID = model.NewExamExportID()
	replayInput.JobID = model.NewJobID()
	replayInput.ExamRecordsMutation = recordsMutation(t, ctx, ss, f.unitID, scope, principal, model.ActionExamRecordsExport)
	replay, err := exports.Create(ctx, &replayInput, command)
	require.NoError(t, err)
	require.Equal(t, created.ID, replay.ID, "retry created a new archive or extended expiry")
	require.True(t, replay.ExpiresAt.Equal(created.ExpiresAt), "retry created a new archive or extended expiry")
	token, err := model.NewJobClaimToken()
	require.NoError(t, err)
	claim, err := ss.Job().ClaimNext(ctx, &store.JobClaimRequest{Types: []model.JobType{model.JobTypeExamExportBuild}, NodeID: "export-test", ClaimToken: token, LeaseDuration: time.Minute})
	require.NoError(t, err)
	require.NotNil(t, claim)
	require.Equal(t, input.JobID, claim.Job.ID)
	build := store.ExamExportBuild{ExamExportArtifact: store.ExamExportArtifact{ExportID: created.ID, AttemptID: claim.Attempt.ID}, JobID: claim.Job.ID, ClaimToken: token}
	snapshot, err := peer.BeginBuild(ctx, &build)
	require.NoError(t, err)
	var document struct {
		Categories  []string                     `json:"categories"`
		Submissions []map[string]json.RawMessage `json:"submissions"`
	}
	require.NoError(t, json.Unmarshal(snapshot.Records, &document))
	require.Len(t, document.Submissions, 1)
	require.NotNil(t, document.Submissions[0]["work"])
	require.Nil(t, document.Submissions[0]["integrity"])
	_, err = exports.BeginBuild(ctx, &build)
	require.True(t, store.IsConflict(err), "same writer was registered twice")
	pending, err := exports.BeginPurgeBatch(ctx, 100)
	require.NoError(t, err)
	require.Empty(t, pending, "cleanup selected a live construction writer")
	bad := build
	bad.ClaimToken, _ = model.NewJobClaimToken()
	publication := &store.ExamExportPublication{ExamExportBuild: bad, SizeBytes: 42, SHA256: strings.Repeat("a", 64)}
	err = exports.Publish(ctx, publication)
	require.True(t, store.IsConflict(err), "stale claim published")
	publication.ExamExportBuild = build
	require.NoError(t, exports.Publish(ctx, publication))
	require.Equal(t, 0, probe.SourceProtectionCount(t, ctx, created.ID), "verified publication retained")
	value, err = exports.Get(ctx, &access)
	require.NoError(t, err)
	require.Equal(t, model.ExamExportReady, value.Export.State)
	require.Equal(t, build.AttemptID, value.Artifact.AttemptID)
	require.Equal(t, publication.SHA256, value.Export.ArchiveSHA256)
	pending, err = exports.BeginPurgeBatch(ctx, 100)
	require.NoError(t, err)
	require.Empty(t, pending, "cleanup selected a current unexpired archive")
	// Failed/expired copies cannot be resurrected by a retained command result.
	probe.Expire(t, ctx, created.ID)
	value, err = peer.Get(ctx, &access)
	require.NoError(t, err)
	require.Equal(t, model.ExamExportExpired, value.Export.State)
	require.False(t, value.Artifact.ExportID.IsValid())
	require.Empty(t, value.Export.ArchiveSHA256)
	replayInput.ExamRecordsMutation = recordsMutation(t, ctx, ss, f.unitID, scope, principal, model.ActionExamRecordsExport)
	replay, err = exports.Create(ctx, &replayInput, command)
	require.NoError(t, err)
	require.Equal(t, model.ExamExportExpired, replay.State, "retry resurrected expired archive")
	count, err := exports.Reconcile(ctx, 100)
	require.NoError(t, err)
	require.Equal(t, 1, count, "expiry reconciliation")
	purge, err := exports.BeginPurgeBatch(ctx, 100)
	require.NoError(t, err)
	require.Len(t, purge, 1)
	require.Equal(t, build.ExamExportArtifact, purge[0])
	require.NoError(t, exports.CompletePurge(ctx, purge[0]))
	require.Equal(t, 0, probe.ArtifactCount(t, ctx, created.ID), "finished verified artifact retained")
	// The other category freezes its own structured records without work.
	decision, err := ss.ExamIntegrityReview().SaveDecision(ctx, &store.ExamIntegrityReviewDecisionMutation{SubmissionID: scope.SubmissionID, ReviewID: model.NewSubmissionReviewID(), DecisionID: model.NewIntegrityReviewDecisionID(), FlagID: flagged.Flag.ID,
		ActorUserID: f.manager.ID, Outcome: model.IntegrityReviewInconclusive, PrivateRationale: "Retained review rationale.", ChangedAt: model.NowUTC(), AuditEventID: saveIntegrityReviewAudit(t, ctx, ss, f, scope.SubmissionID, model.ActionSubmissionReview).ID, AuditAt: model.GetMillis()}, examCommand(f.manager.ID, store.ExamIntegrityReviewDecisionOperation, "export-decision", "export-decision"))
	require.NoError(t, err)
	_, err = ss.ExamIntegrityReview().Finalize(ctx, &store.ExamIntegrityReviewFinalize{SubmissionID: scope.SubmissionID, ReviewID: decision.Review.ID, ActorUserID: f.manager.ID, ExpectedReviewRevision: decision.Review.Revision, ChangedAt: model.NowUTC(),
		AuditEventID: saveIntegrityReviewAudit(t, ctx, ss, f, scope.SubmissionID, model.ActionSubmissionReview).ID, AuditAt: model.GetMillis()}, examCommand(f.manager.ID, store.ExamIntegrityReviewFinalizeOperation, "export-finalize", "export-finalize"))
	require.NoError(t, err)
	integrityInput := makeInput(model.RetentionCategoryIntegrity)
	integrity, err := exports.Create(ctx, integrityInput, examCommand(f.manager.ID, store.ExamExportCreateOperation, "export-integrity", "export-integrity"))
	require.NoError(t, err)
	withoutPermission(model.ActionExamAttemptBrowserActivityView, func() {
		integrityAccess := integrityInput.ExamExportAccess
		_, getErr := peer.Get(ctx, &integrityAccess)
		require.NoError(t, getErr)
	})
	historyInput := makeInput(model.RetentionCategoryBrowserActivity)
	_, err = exports.Create(ctx, historyInput, examCommand(f.manager.ID, store.ExamExportCreateOperation, "export-history", "export-history"))
	require.NoError(t, err)
	withoutPermission(model.ActionExamAttemptBrowserActivityView, func() {
		historyAccess := historyInput.ExamExportAccess
		_, getErr := peer.Get(ctx, &historyAccess)
		require.True(t, store.IsConflict(getErr), "history export skipped dedicated authority")
	})

	// Holding every export/read action does not turn a different User into an
	// exact Exam Manager, including through the explicit general export override.
	nonmanager := saveUser(t, ctx, ss)
	nonmanagerPrincipal := saveRecordsPrincipal(t, ctx, ss, nonmanager.ID, false)
	_, err = SaveRoleBinding(ctx, ss, &model.RoleBinding{UserID: nonmanager.ID, RoleID: role.ID,
		ScopeType: model.RoleScopeAcademicUnit, ScopeID: f.unitID.String(), StartsAt: model.NowUTC().Add(-time.Minute)})
	require.NoError(t, err)
	_, err = ss.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{UserID: nonmanager.ID, AcademicUnitID: f.unitID, StartsAt: model.NowUTC().Add(-time.Minute)})
	require.NoError(t, err)
	for _, categories := range [][]model.RetentionCategory{{model.RetentionCategoryBrowserActivity}} {
		denied := makeInput(categories...)
		denied.ExamRecordsMutation = recordsMutation(t, ctx, ss, f.unitID, scope, nonmanagerPrincipal, model.ActionExamRecordsExportOverride)
		_, err = exports.Create(ctx, denied, examCommand(nonmanager.ID, store.ExamExportCreateOperation, model.NewID(), "browser-denied"))
		require.True(t, store.IsConflict(err), "nonmanager override created Browser Activity archive")
		require.Equal(t, 0, probe.SourceProtectionCount(t, ctx, denied.ExportID), "denial left export protections")
	}
	// A real exact Manager with valid scoped actions can use the general export
	// override without inventing an extra unit-membership history condition.
	exam, err := ss.ExamAuthoring().Resolve(ctx, f.examID)
	require.NoError(t, err)
	at := model.NowUTC()
	grant := newExamManagerMutation(t, ctx, ss, f.examID, f.manager.ID, nonmanager.ID, exam.Revision, at, false)
	grant.Notices = examManagerMailNotices(t, at, examManagerMailRecipient{nonmanager.ID, model.MailTemplateExamManagerAdded})
	added, err := ss.ExamAuthoring().AddManager(ctx, grant, examCommand(f.manager.ID, "exam.manager.add.v1", "export-manager-add", "export-manager-add"))
	require.NoError(t, err)
	memberships, err := ss.AcademicUnitMember().ListActiveByUser(ctx, nonmanager.ID, model.NowUTC())
	require.NoError(t, err)
	for _, membership := range memberships {
		if membership.AcademicUnitID == f.unitID {
			_, err = EndAcademicUnitMember(ctx, ss, membership.ID, membership.Revision, model.GetMillis())
			require.NoError(t, err)
		}
	}
	managedInput := makeInput(model.RetentionCategoryBrowserActivity)
	managedInput.ExamRecordsMutation = recordsMutation(t, ctx, ss, f.unitID, scope, nonmanagerPrincipal, model.ActionExamRecordsExportOverride)
	managedCommand := examCommand(nonmanager.ID, store.ExamExportCreateOperation, "export-new-manager", "export-new-manager")
	_, err = exports.Create(ctx, managedInput, managedCommand)
	require.NoError(t, err)
	managedAccess := managedInput.ExamExportAccess
	_, err = peer.Get(ctx, &managedAccess)
	require.NoError(t, err)
	// Remove that exact relationship through its real command. Every role and
	// Session remains current; peer reads and retained retries must now deny.
	at = model.NowUTC()
	removal := newExamManagerMutation(t, ctx, ss, f.examID, f.manager.ID, nonmanager.ID, added.Exam.Revision, at, false)
	removal.Notices = examManagerMailNotices(t, at, examManagerMailRecipient{nonmanager.ID, model.MailTemplateExamManagerRemoved})
	_, err = ss.ExamAuthoring().RemoveManager(ctx, removal, examCommand(f.manager.ID, "exam.manager.remove.v1", "export-manager-remove", "export-manager-remove"))
	require.NoError(t, err)
	_, err = peer.Get(ctx, &managedAccess)
	require.True(t, store.IsConflict(err), "peer override read ignored removed Manager: %v", err)
	managedInput.ExamRecordsMutation = recordsMutation(t, ctx, ss, f.unitID, scope, nonmanagerPrincipal, model.ActionExamRecordsExportOverride)
	_, err = exports.Create(ctx, managedInput, managedCommand)
	require.True(t, store.IsConflict(err), "replay override ignored removed Manager: %v", err)
	token2, err := model.NewJobClaimToken()
	require.NoError(t, err)
	claim2, err := ss.Job().ClaimNext(ctx, &store.JobClaimRequest{Types: []model.JobType{model.JobTypeExamExportBuild}, NodeID: "export-peer", ClaimToken: token2, LeaseDuration: time.Minute})
	require.NoError(t, err)
	require.NotNil(t, claim2)
	require.Equal(t, integrityInput.JobID, claim2.Job.ID)
	build2 := store.ExamExportBuild{ExamExportArtifact: store.ExamExportArtifact{ExportID: integrity.ID, AttemptID: claim2.Attempt.ID}, JobID: claim2.Job.ID, ClaimToken: token2}
	snapshot, err = exports.BeginBuild(ctx, &build2)
	require.NoError(t, err)
	document.Submissions = nil
	require.NoError(t, json.Unmarshal(snapshot.Records, &document))
	require.Nil(t, document.Submissions[0]["browser_activity"], "integrity-only export included work")
	require.NotNil(t, document.Submissions[0]["integrity"], "integrity-only export included work")
	require.Nil(t, document.Submissions[0]["work"], "integrity-only export included work")
	require.Empty(t, snapshot.Files, "integrity-only export included work")
	var integrityRecords map[string][]json.RawMessage
	require.NoError(t, json.Unmarshal(document.Submissions[0]["integrity"], &integrityRecords))
	require.Nil(t, integrityRecords["browser_activity"], "generic integrity export leaked ordinary browsing history")
	require.Len(t, integrityRecords["review_inventory_flags"], 1, "portable archive omitted the frozen Review inventory")
	require.Len(t, integrityRecords["review_inventory_evidence"], 1, "portable archive omitted the frozen Review inventory")
	probe.Expire(t, ctx, integrity.ID)
	err = exports.Publish(ctx, &store.ExamExportPublication{ExamExportBuild: build2, SizeBytes: 42, SHA256: strings.Repeat("a", 64)})
	require.True(t, store.IsConflict(err), "late writer published")
	_, err = exports.Reconcile(ctx, 100)
	require.NoError(t, err)
	require.NoError(t, exports.CompletePurge(ctx, build2.ExamExportArtifact))
	require.Equal(t, 1, probe.ArtifactCount(t, ctx, integrity.ID), "uncertain writer lost cleanup reference")
	require.NoError(t, exports.FinishWriter(ctx, build2.ExamExportArtifact))
	require.NoError(t, exports.CompletePurge(ctx, build2.ExamExportArtifact))
	require.Equal(t, 0, probe.ArtifactCount(t, ctx, integrity.ID), "finished writer cleanup reference")
	// End the already-active fixture binding in its past interval. A host clock
	// slightly ahead of PostgreSQL must not schedule this revocation in the
	// database future and make the immediate peer authorization check race it.
	_, err = EndRoleBinding(ctx, ss, binding.ID, model.MillisFromTime(binding.StartsAt.Add(time.Second)))
	require.NoError(t, err)
	_, err = peer.Get(ctx, &access)
	require.True(t, store.IsConflict(err), "revoked role read retained archive")
}
