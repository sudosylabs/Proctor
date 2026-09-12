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
	role, err := ss.Role().Save(ctx, &model.Role{Name: "export-manager-" + model.NewId(), DisplayName: "Export manager", Permissions: permissions})
	requireNoError(t, err)
	binding, err := ss.RoleBinding().Save(ctx, &model.RoleBinding{UserID: f.manager.ID, RoleID: role.ID, ScopeType: model.RoleScopeAcademicUnit, ScopeID: f.unitID.String(), StartsAt: model.NowUTC().Add(-time.Minute)})
	requireNoError(t, err)
	_, err = ss.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{UserID: f.manager.ID, AcademicUnitID: f.unitID, StartsAt: model.NowUTC().Add(-time.Minute)})
	requireNoError(t, err)
	exports := ss.ExamExport()
	selected, err := exports.ListScope(ctx, scope)
	requireNoError(t, err)
	if len(selected) != 1 || selected[0].SubmissionID != scope.SubmissionID {
		t.Fatalf("scope=%#v", selected)
	}
	makeInput := func(categories ...model.RetentionCategory) *store.ExamExportCreation {
		return &store.ExamExportCreation{ExamExportAccess: store.ExamExportAccess{ExamRecordsMutation: recordsMutation(t, ctx, ss, f.unitID, scope, principal, model.ActionExamRecordsExport), ExportID: model.NewExamExportID(), Categories: categories}, JobID: model.NewJobID(), SubmissionIDs: []model.SubmissionID{scope.SubmissionID}}
	}
	input := makeInput(model.RetentionCategoryWork)
	command := examCommand(f.manager.ID, store.ExamExportCreateOperation, "export-work", "export-work")
	if _, err = exports.Create(ctx, input, command); !store.IsConflict(err) {
		t.Fatalf("unconfigured expiry accepted: %v", err)
	}
	probe.SetPolicy(t, ctx, 1)
	broken := *input
	broken.AuditEventID = model.NewId()
	if _, err = exports.Create(ctx, &broken, command); err == nil {
		t.Fatal("export committed without its audit")
	}
	if count := probe.SourceProtectionCount(t, ctx, input.ExportID); count != 0 {
		t.Fatalf("rollback left %d protections", count)
	}
	created, err := exports.Create(ctx, input, command)
	requireNoError(t, err)
	if created.Validate() != nil || created.State != model.ExamExportQueued || created.ExpiresAt.Sub(created.CreatedAt) != 24*time.Hour || created.SourceExpiresAt != created.ExpiresAt {
		t.Fatalf("created=%#v", created)
	}
	if count := probe.SourceProtectionCount(t, ctx, created.ID); count != 1 {
		t.Fatalf("source protections=%d", count)
	}
	resolved, err := exports.ListCreationScope(ctx, scope, command)
	requireNoError(t, err)
	if !slices.Equal(resolved, selected) {
		t.Fatal("retained command did not resolve its original captured set")
	}
	changedCommand := *command
	changedCommand.Fingerprint[0] ^= 1
	if _, err := exports.ListCreationScope(ctx, scope, &changedCommand); err == nil {
		t.Fatal("changed request fingerprint reused an original capture")
	}
	wrongCapture := scope
	wrongCapture.SubmissionID = ""
	if _, err := exports.ListCreationScope(ctx, wrongCapture, command); !store.IsNotFound(err) {
		t.Fatalf("original outcome disclosed across scope: %v", err)
	}
	// A peer observes the exact same durable protections before bytes exist.
	peer := probe.Peer
	if peer == nil {
		peer = exports
	}
	access := input.ExamExportAccess
	value, err := peer.Get(ctx, &access)
	requireNoError(t, err)
	if value.Export.ID != created.ID || len(value.Submissions) != 1 {
		t.Fatalf("peer read=%#v", value)
	}
	// A live permission edit must take effect across nodes despite an unchanged
	// Session principal or a previously successful metadata read.
	withoutPermission := func(action model.Action, check func()) {
		changed := *role
		changed.Permissions = slices.DeleteFunc(slices.Clone(permissions), func(p string) bool { return p == string(action) })
		_, err := ss.Role().Update(ctx, &changed)
		requireNoError(t, err)
		check()
		changed.Permissions = slices.Clone(permissions)
		role, err = ss.Role().Update(ctx, &changed)
		requireNoError(t, err)
	}
	for _, action := range []model.Action{model.ActionExamRecordsExport, model.ActionSubmissionView} {
		withoutPermission(action, func() {
			if _, err := peer.Get(ctx, &access); !store.IsConflict(err) {
				t.Fatalf("missing %s still read export: %v", action, err)
			}
		})
	}
	bulkScope := scope
	bulkScope.SubmissionID = ""
	bulk := makeInput(model.RetentionCategoryWork)
	bulk.Scope = bulkScope
	bulk.ExamRecordsMutation = recordsMutation(t, ctx, ss, f.unitID, bulkScope, principal, model.ActionExamRecordsExport)
	withoutPermission(model.ActionExamSittingView, func() {
		if _, err := exports.Create(ctx, bulk, examCommand(f.manager.ID, store.ExamExportCreateOperation, "export-bulk-no-read", "export-bulk-no-read")); !store.IsConflict(err) {
			t.Fatalf("Sitting export skipped Sitting authority: %v", err)
		}
	})
	changedScope := makeInput(model.RetentionCategoryWork)
	changedScope.SubmissionIDs = []model.SubmissionID{model.NewSubmissionID()}
	if _, err := exports.Create(ctx, changedScope, examCommand(f.manager.ID, store.ExamExportCreateOperation, "export-changed-scope", "export-changed-scope")); !store.IsConflict(err) {
		t.Fatalf("changed capture set accepted: %v", err)
	}
	second := access
	second.Principal = saveRecordsPrincipal(t, ctx, ss, f.manager.ID, false)
	_, err = peer.Get(ctx, &second)
	requireNoError(t, err)
	_, err = ss.Session().Revoke(ctx, second.Principal.SessionID.String(), f.manager.ID.String(), model.GetMillis(), model.SessionRevocationUserSession)
	requireNoError(t, err)
	if _, err := peer.Get(ctx, &second); !store.IsConflict(err) {
		t.Fatalf("revoked Session retained export access: %v", err)
	}
	foreign := access
	foreign.Principal = saveRecordsPrincipal(t, ctx, ss, f.candidate.ID, false)
	if _, err = peer.Get(ctx, &foreign); !store.IsNotFound(err) {
		t.Fatalf("nonrequester got export: %v", err)
	}
	_, err = ss.RoleBinding().Save(ctx, &model.RoleBinding{UserID: f.candidate.ID, RoleID: role.ID, ScopeType: model.RoleScopeAcademicUnit, ScopeID: f.unitID.String(), StartsAt: model.NowUTC().Add(-time.Minute)})
	requireNoError(t, err)
	self := makeInput(model.RetentionCategoryWork)
	self.ExamRecordsMutation = recordsMutation(t, ctx, ss, f.unitID, scope, foreign.Principal, model.ActionExamRecordsExportOverride)
	if _, err := exports.Create(ctx, self, examCommand(f.candidate.ID, store.ExamExportCreateOperation, "export-self", "export-self")); !store.IsNotFound(err) {
		t.Fatalf("override allowed self export: %v", err)
	}
	wrong := access
	wrong.Scope.SubmissionID = ""
	if _, err = peer.Get(ctx, &wrong); !store.IsNotFound(err) {
		t.Fatalf("cross-scope export disclosed: %v", err)
	}
	replayInput := *input
	replayInput.ExportID = model.NewExamExportID()
	replayInput.JobID = model.NewJobID()
	replayInput.ExamRecordsMutation = recordsMutation(t, ctx, ss, f.unitID, scope, principal, model.ActionExamRecordsExport)
	replay, err := exports.Create(ctx, &replayInput, command)
	requireNoError(t, err)
	if replay.ID != created.ID || !replay.ExpiresAt.Equal(created.ExpiresAt) {
		t.Fatal("retry created a new archive or extended expiry")
	}
	token, err := model.NewJobClaimToken()
	requireNoError(t, err)
	claim, err := ss.Job().ClaimNext(ctx, &store.JobClaimRequest{Types: []model.JobType{model.JobTypeExamExportBuild}, NodeID: "export-test", ClaimToken: token, LeaseDuration: time.Minute})
	requireNoError(t, err)
	if claim == nil || claim.Job.ID != input.JobID {
		t.Fatalf("atomic export Job=%#v", claim)
	}
	build := store.ExamExportBuild{ExamExportArtifact: store.ExamExportArtifact{ExportID: created.ID, AttemptID: claim.Attempt.ID}, JobID: claim.Job.ID, ClaimToken: token}
	snapshot, err := peer.BeginBuild(ctx, &build)
	requireNoError(t, err)
	var document struct {
		Categories  []string                     `json:"categories"`
		Submissions []map[string]json.RawMessage `json:"submissions"`
	}
	requireNoError(t, json.Unmarshal(snapshot.Records, &document))
	if len(document.Submissions) != 1 || document.Submissions[0]["work"] == nil || document.Submissions[0]["integrity"] != nil {
		t.Fatalf("category selection=%s", snapshot.Records)
	}
	if _, err = exports.BeginBuild(ctx, &build); !store.IsConflict(err) {
		t.Fatalf("same writer was registered twice: %v", err)
	}
	if pending, err := exports.BeginPurgeBatch(ctx, 100); err != nil || len(pending) != 0 {
		t.Fatalf("cleanup selected a live construction writer: %#v (%v)", pending, err)
	}
	bad := build
	bad.ClaimToken, _ = model.NewJobClaimToken()
	publication := &store.ExamExportPublication{ExamExportBuild: bad, SizeBytes: 42, SHA256: strings.Repeat("a", 64)}
	if err = exports.Publish(ctx, publication); !store.IsConflict(err) {
		t.Fatalf("stale claim published: %v", err)
	}
	publication.ExamExportBuild = build
	requireNoError(t, exports.Publish(ctx, publication))
	if count := probe.SourceProtectionCount(t, ctx, created.ID); count != 0 {
		t.Fatalf("verified publication retained %d source protections", count)
	}
	value, err = exports.Get(ctx, &access)
	requireNoError(t, err)
	if value.Export.State != model.ExamExportReady || value.Artifact.AttemptID != build.AttemptID || value.Export.ArchiveSHA256 != publication.SHA256 {
		t.Fatalf("publication=%#v", value)
	}
	if pending, err := exports.BeginPurgeBatch(ctx, 100); err != nil || len(pending) != 0 {
		t.Fatalf("cleanup selected a current unexpired archive: %#v (%v)", pending, err)
	}
	// Failed/expired copies cannot be resurrected by a retained command result.
	probe.Expire(t, ctx, created.ID)
	value, err = peer.Get(ctx, &access)
	requireNoError(t, err)
	if value.Export.State != model.ExamExportExpired || value.Artifact.ExportID.IsValid() || value.Export.ArchiveSHA256 != "" {
		t.Fatalf("expired export retained access: %#v", value)
	}
	replayInput.ExamRecordsMutation = recordsMutation(t, ctx, ss, f.unitID, scope, principal, model.ActionExamRecordsExport)
	replay, err = exports.Create(ctx, &replayInput, command)
	requireNoError(t, err)
	if replay.State != model.ExamExportExpired {
		t.Fatal("retry resurrected expired archive")
	}
	count, err := exports.Reconcile(ctx, 100)
	requireNoError(t, err)
	if count != 1 {
		t.Fatalf("expiry reconciliation=%d", count)
	}
	purge, err := exports.BeginPurgeBatch(ctx, 100)
	requireNoError(t, err)
	if len(purge) != 1 || purge[0] != build.ExamExportArtifact {
		t.Fatalf("purge=%#v", purge)
	}
	requireNoError(t, exports.CompletePurge(ctx, purge[0]))
	if count := probe.ArtifactCount(t, ctx, created.ID); count != 0 {
		t.Fatalf("finished verified artifact retained=%d", count)
	}
	// The other category freezes its own structured records without work.
	decision, err := ss.ExamIntegrityReview().SaveDecision(ctx, &store.ExamIntegrityReviewDecisionMutation{SubmissionID: scope.SubmissionID, ReviewID: model.NewSubmissionReviewID(), DecisionID: model.NewIntegrityReviewDecisionID(), FlagID: flagged.Flag.ID,
		ActorUserID: f.manager.ID, Outcome: model.IntegrityReviewInconclusive, PrivateRationale: "Retained review rationale.", ChangedAt: model.NowUTC(), AuditEventID: saveIntegrityReviewAudit(t, ctx, ss, f, scope.SubmissionID, model.ActionSubmissionReview).ID.String(), AuditAt: model.GetMillis()}, examCommand(f.manager.ID, store.ExamIntegrityReviewDecisionOperation, "export-decision", "export-decision"))
	requireNoError(t, err)
	_, err = ss.ExamIntegrityReview().Finalize(ctx, &store.ExamIntegrityReviewFinalize{SubmissionID: scope.SubmissionID, ReviewID: decision.Review.ID, ActorUserID: f.manager.ID, ExpectedReviewRevision: decision.Review.Revision, ChangedAt: model.NowUTC(),
		AuditEventID: saveIntegrityReviewAudit(t, ctx, ss, f, scope.SubmissionID, model.ActionSubmissionReview).ID.String(), AuditAt: model.GetMillis()}, examCommand(f.manager.ID, store.ExamIntegrityReviewFinalizeOperation, "export-finalize", "export-finalize"))
	requireNoError(t, err)
	integrityInput := makeInput(model.RetentionCategoryIntegrity)
	integrity, err := exports.Create(ctx, integrityInput, examCommand(f.manager.ID, store.ExamExportCreateOperation, "export-integrity", "export-integrity"))
	requireNoError(t, err)
	withoutPermission(model.ActionExamAttemptBrowserActivityView, func() {
		integrityAccess := integrityInput.ExamExportAccess
		if _, err := peer.Get(ctx, &integrityAccess); err != nil {
			t.Fatalf("generic integrity export demanded private history authority: %v", err)
		}
	})
	historyInput := makeInput(model.RetentionCategoryBrowserActivity)
	_, err = exports.Create(ctx, historyInput, examCommand(f.manager.ID, store.ExamExportCreateOperation, "export-history", "export-history"))
	requireNoError(t, err)
	withoutPermission(model.ActionExamAttemptBrowserActivityView, func() {
		historyAccess := historyInput.ExamExportAccess
		if _, err := peer.Get(ctx, &historyAccess); !store.IsConflict(err) {
			t.Fatalf("history export skipped dedicated authority: %v", err)
		}
	})

	// Holding every export/read action does not turn a different User into an
	// exact Exam Manager, including through the explicit general export override.
	nonmanager := saveUser(t, ctx, ss)
	nonmanagerPrincipal := saveRecordsPrincipal(t, ctx, ss, nonmanager.ID, false)
	_, err = ss.RoleBinding().Save(ctx, &model.RoleBinding{UserID: nonmanager.ID, RoleID: role.ID,
		ScopeType: model.RoleScopeAcademicUnit, ScopeID: f.unitID.String(), StartsAt: model.NowUTC().Add(-time.Minute)})
	requireNoError(t, err)
	_, err = ss.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{UserID: nonmanager.ID, AcademicUnitID: f.unitID, StartsAt: model.NowUTC().Add(-time.Minute)})
	requireNoError(t, err)
	for _, categories := range [][]model.RetentionCategory{{model.RetentionCategoryBrowserActivity}} {
		denied := makeInput(categories...)
		denied.ExamRecordsMutation = recordsMutation(t, ctx, ss, f.unitID, scope, nonmanagerPrincipal, model.ActionExamRecordsExportOverride)
		if _, err := exports.Create(ctx, denied, examCommand(nonmanager.ID, store.ExamExportCreateOperation, model.NewId(), "browser-denied")); !store.IsConflict(err) {
			t.Fatalf("nonmanager override created Browser Activity archive: %v", err)
		}
		if count := probe.SourceProtectionCount(t, ctx, denied.ExportID); count != 0 {
			t.Fatal("denial left export protections")
		}
	}
	// A real exact Manager with valid scoped actions can use the general export
	// override without inventing an extra unit-membership history condition.
	exam, err := ss.ExamAuthoring().Resolve(ctx, f.examID)
	requireNoError(t, err)
	at := model.NowUTC()
	grant := newExamManagerMutation(t, ctx, ss, f.examID, f.manager.ID, nonmanager.ID, exam.Revision, at, false)
	grant.Notices = examManagerMailNotices(t, at, examManagerMailRecipient{nonmanager.ID, model.MailTemplateExamManagerAdded})
	added, err := ss.ExamAuthoring().AddManager(ctx, grant, examCommand(f.manager.ID, "exam.manager.add.v1", "export-manager-add", "export-manager-add"))
	requireNoError(t, err)
	memberships, err := ss.AcademicUnitMember().ListActiveByUser(ctx, nonmanager.ID.String(), model.NowUTC())
	requireNoError(t, err)
	for _, membership := range memberships {
		if membership.AcademicUnitID == f.unitID {
			_, err := ss.AcademicUnitMember().End(ctx, membership.ID.String(), membership.Revision, model.GetMillis())
			requireNoError(t, err)
		}
	}
	managedInput := makeInput(model.RetentionCategoryBrowserActivity)
	managedInput.ExamRecordsMutation = recordsMutation(t, ctx, ss, f.unitID, scope, nonmanagerPrincipal, model.ActionExamRecordsExportOverride)
	managedCommand := examCommand(nonmanager.ID, store.ExamExportCreateOperation, "export-new-manager", "export-new-manager")
	_, err = exports.Create(ctx, managedInput, managedCommand)
	requireNoError(t, err)
	managedAccess := managedInput.ExamExportAccess
	_, err = peer.Get(ctx, &managedAccess)
	requireNoError(t, err)
	// Remove that exact relationship through its real command. Every role and
	// Session remains current; peer reads and retained retries must now deny.
	at = model.NowUTC()
	removal := newExamManagerMutation(t, ctx, ss, f.examID, f.manager.ID, nonmanager.ID, added.Exam.Revision, at, false)
	removal.Notices = examManagerMailNotices(t, at, examManagerMailRecipient{nonmanager.ID, model.MailTemplateExamManagerRemoved})
	_, err = ss.ExamAuthoring().RemoveManager(ctx, removal, examCommand(f.manager.ID, "exam.manager.remove.v1", "export-manager-remove", "export-manager-remove"))
	requireNoError(t, err)
	if _, err := peer.Get(ctx, &managedAccess); !store.IsConflict(err) {
		t.Fatalf("peer override read ignored removed Manager: %v", err)
	}
	managedInput.ExamRecordsMutation = recordsMutation(t, ctx, ss, f.unitID, scope, nonmanagerPrincipal, model.ActionExamRecordsExportOverride)
	if _, err := exports.Create(ctx, managedInput, managedCommand); !store.IsConflict(err) {
		t.Fatalf("replay override ignored removed Manager: %v", err)
	}
	token2, err := model.NewJobClaimToken()
	requireNoError(t, err)
	claim2, err := ss.Job().ClaimNext(ctx, &store.JobClaimRequest{Types: []model.JobType{model.JobTypeExamExportBuild}, NodeID: "export-peer", ClaimToken: token2, LeaseDuration: time.Minute})
	requireNoError(t, err)
	if claim2 == nil || claim2.Job.ID != integrityInput.JobID {
		t.Fatalf("integrity Job=%#v", claim2)
	}
	build2 := store.ExamExportBuild{ExamExportArtifact: store.ExamExportArtifact{ExportID: integrity.ID, AttemptID: claim2.Attempt.ID}, JobID: claim2.Job.ID, ClaimToken: token2}
	snapshot, err = exports.BeginBuild(ctx, &build2)
	requireNoError(t, err)
	document.Submissions = nil
	requireNoError(t, json.Unmarshal(snapshot.Records, &document))
	if document.Submissions[0]["browser_activity"] != nil || document.Submissions[0]["integrity"] == nil || document.Submissions[0]["work"] != nil || len(snapshot.Files) != 0 {
		t.Fatal("integrity-only export included work")
	}
	var integrityRecords map[string][]json.RawMessage
	requireNoError(t, json.Unmarshal(document.Submissions[0]["integrity"], &integrityRecords))
	if integrityRecords["browser_activity"] != nil {
		t.Fatal("generic integrity export leaked ordinary browsing history")
	}
	if len(integrityRecords["review_inventory_flags"]) != 1 || len(integrityRecords["review_inventory_evidence"]) != 1 {
		t.Fatal("portable archive omitted the frozen Review inventory")
	}
	probe.Expire(t, ctx, integrity.ID)
	if err = exports.Publish(ctx, &store.ExamExportPublication{ExamExportBuild: build2, SizeBytes: 42, SHA256: strings.Repeat("a", 64)}); !store.IsConflict(err) {
		t.Fatalf("late writer published: %v", err)
	}
	_, err = exports.Reconcile(ctx, 100)
	requireNoError(t, err)
	requireNoError(t, exports.CompletePurge(ctx, build2.ExamExportArtifact))
	if count := probe.ArtifactCount(t, ctx, integrity.ID); count != 1 {
		t.Fatalf("uncertain writer lost cleanup reference=%d", count)
	}
	requireNoError(t, exports.FinishWriter(ctx, build2.ExamExportArtifact))
	requireNoError(t, exports.CompletePurge(ctx, build2.ExamExportArtifact))
	if count := probe.ArtifactCount(t, ctx, integrity.ID); count != 0 {
		t.Fatalf("finished writer cleanup reference=%d", count)
	}
	// End the already-active fixture binding in its past interval. A host clock
	// slightly ahead of PostgreSQL must not schedule this revocation in the
	// database future and make the immediate peer authorization check race it.
	_, err = ss.RoleBinding().End(ctx, binding.ID.String(), model.MillisFromTime(binding.StartsAt.Add(time.Second)))
	requireNoError(t, err)
	if _, err = peer.Get(ctx, &access); !store.IsConflict(err) {
		t.Fatalf("revoked role read retained archive: %v", err)
	}
}
