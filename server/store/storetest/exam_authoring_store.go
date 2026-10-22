// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestExamAuthoringStore(t *testing.T, ss store.Store) {
	t.Run("CreateGetAndReplay", func(t *testing.T) { testExamAuthoringCreateGetAndReplay(t, ss) })
	t.Run("UpdateDraftTextAndConflict", func(t *testing.T) { testExamAuthoringUpdateDraftTextAndConflict(t, ss) })
	t.Run("BrowserPolicyInstitutionPin", func(t *testing.T) { testBrowserPolicyInstitutionPin(t, ss) })
	t.Run("UpdateDraftNativePolicyAndConflict", func(t *testing.T) { testExamAuthoringUpdateDraftNativePolicyAndConflict(t, ss) })
	t.Run("UpdateDraftFocusLossAndConflict", func(t *testing.T) { testExamAuthoringUpdateDraftFocusLossAndConflict(t, ss) })
	t.Run("ListCatalogAndArchive", func(t *testing.T) { testExamCatalogListAndArchive(t, ss) })
	t.Run("ManageManagersAndOwnership", func(t *testing.T) { testExamManagersAndOwnership(t, ss) })
	t.Run("AuditAtomicity", func(t *testing.T) { testExamAuthoringAuditAtomicity(t, ss) })
}

func testExamAuthoringCreateGetAndReplay(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	unit := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "exam-unit")
	creator := saveUser(t, ctx, ss)
	other := saveUser(t, ctx, ss)
	at := model.NowUTC()
	creation := newExamAuthoringCreation(t, ctx, ss, unit.ID, creator.ID, at)
	command := &store.CommandIdempotency{
		UserID: creator.ID, Operation: "exam.create",
		KeyDigest:   sha256.Sum256([]byte("key")),
		Fingerprint: sha256.Sum256([]byte("command")),
		Retention:   time.Hour, Wait: time.Second,
	}

	first, err := ss.ExamAuthoring().Create(ctx, creation, command)
	require.NoError(t, err)
	require.False(t, first.Replayed)
	require.Equal(t, 1, first.Value.ManagerCount)
	require.True(t, first.Value.ActorIsManager)
	require.Equal(t, creator.ID, first.Value.OwnerUserID)
	require.Equal(t, model.DefaultExamCapacityPolicy(), first.Value.Capacity)
	require.Equal(t, 0, first.Value.ResourceCount)
	require.False(t, first.Value.HasStarterWorkspace)
	completed, err := ss.Audit().Get(ctx, creation.AuditEventID)
	require.NoError(t, err)
	require.Equal(t, model.AuditStatusSuccess, completed.Status, "creation audit status")

	replayCreation := newExamAuthoringCreation(t, ctx, ss, unit.ID, creator.ID, at.Add(time.Second))
	replayCreation.Exam.ID = creation.Exam.ID
	replayCreation.Draft.ExamID = creation.Exam.ID
	replayCreation.Manager.ExamID = creation.Exam.ID
	replayed, err := ss.ExamAuthoring().Create(ctx, replayCreation, command)
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
	require.Equal(t, first.Value.Exam.ID, replayed.Value.Exam.ID)
	require.Equal(t, first.Value.Capacity, replayed.Value.Capacity)
	replayAudit, err := ss.Audit().Get(ctx, replayCreation.AuditEventID)
	require.NoError(t, err)
	require.Equal(t, model.AuditStatusSuccess, replayAudit.Status, "replay audit status")
	var replayResult map[string]any
	require.NoError(t, json.Unmarshal(replayAudit.Result, &replayResult))
	require.Equal(t, true, replayResult["idempotency_replayed"])
	require.Equal(t, creation.AuditEventID.String(), replayResult["original_audit_event_id"])

	managerView, err := ss.ExamAuthoring().Get(ctx, first.Value.Exam.ID, creator.ID)
	require.NoError(t, err)
	outsiderView, err := ss.ExamAuthoring().Get(ctx, first.Value.Exam.ID, other.ID)
	require.NoError(t, err)
	managerAccess, err := ss.ExamAuthoring().Access(ctx, first.Value.Exam.ID, creator.ID)
	require.NoError(t, err)
	outsiderAccess, err := ss.ExamAuthoring().Access(ctx, first.Value.Exam.ID, other.ID)
	require.NoError(t, err)
	require.True(t, managerView.ActorIsManager, "manager/outsider views = %#v / %#v", managerView, outsiderView)
	require.False(t, outsiderView.ActorIsManager, "manager/outsider views = %#v / %#v", managerView, outsiderView)
	require.Equal(t, 1, managerView.ManagerCount, "manager/outsider views = %#v / %#v", managerView, outsiderView)
	require.Equal(t, 1, outsiderView.ManagerCount, "manager/outsider views = %#v / %#v", managerView, outsiderView)
	require.True(t, managerAccess.ActorIsManager, "manager/outsider access = %#v / %#v", managerAccess, outsiderAccess)
	require.False(t, outsiderAccess.ActorIsManager, "manager/outsider access = %#v / %#v", managerAccess, outsiderAccess)
	require.Equal(t, first.Value.Exam.ID, managerAccess.Exam.ID, "manager/outsider access = %#v / %#v", managerAccess, outsiderAccess)
	require.Equal(t, first.Value.Exam.ID, outsiderAccess.Exam.ID, "manager/outsider access = %#v / %#v", managerAccess, outsiderAccess)
	require.True(t, managerView.Draft.Policy.Equal(model.DefaultExamPolicySet()))
	require.Equal(t, model.DefaultExamCapacityPolicy(), managerView.Capacity, "persisted capacity")
	require.Equal(t, managerView.Capacity, outsiderView.Capacity, "persisted capacity")
}

func testExamAuthoringAuditAtomicity(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	unit := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "exam-rollback-unit")
	creator := saveUser(t, ctx, ss)
	creation := newExamAuthoringCreation(t, ctx, ss, unit.ID, creator.ID, model.NowUTC())
	creation.AuditEventID = model.NewAuditEventID()
	command := &store.CommandIdempotency{
		UserID: creator.ID, Operation: "exam.create", KeyDigest: sha256.Sum256([]byte("rollback-key")), Fingerprint: sha256.Sum256([]byte("rollback-command")),
		Retention: time.Hour, Wait: time.Second,
	}
	_, err := ss.ExamAuthoring().Create(ctx, creation, command)
	require.Error(t, err, "Create succeeded without its audit attempt")
	_, err = ss.ExamAuthoring().Get(ctx, creation.Exam.ID, creator.ID)
	require.True(t, store.IsNotFound(err), "exam survived audit rollback")
}

func testExamAuthoringUpdateDraftTextAndConflict(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	unit := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "exam-edit-unit")
	creator := saveUser(t, ctx, ss)
	createdAt := model.NowUTC()
	creation := newExamAuthoringCreation(t, ctx, ss, unit.ID, creator.ID, createdAt)
	createCommand := examCommand(creator.ID, "exam.create", "edit-create", "edit-create-command")
	created, err := ss.ExamAuthoring().Create(ctx, creation, createCommand)
	require.NoError(t, err)

	title := "  Distributed Systems  "
	clearInstructions := ""
	updatedAt := createdAt.Add(time.Minute)
	update := newExamDraftTextUpdate(t, ctx, ss, created.Value.Exam.ID, creator.ID, 1, &title, &clearInstructions, updatedAt)
	updateCommand := examCommand(creator.ID, "exam.draft.text.edit", "edit-key", "edit-command")
	updated, err := ss.ExamAuthoring().UpdateDraftText(ctx, update, updateCommand)
	require.NoError(t, err)
	require.False(t, updated.Replayed)
	require.Equal(t, "Distributed Systems", updated.Value.Draft.Title)
	require.Empty(t, updated.Value.Draft.InstructionsMarkdown)
	require.Equal(t, int64(2), updated.Value.Draft.Revision)
	require.Equal(t, created.Value.Exam.Revision, updated.Value.Exam.Revision)
	require.True(t, updated.Value.Draft.Policy.Equal(created.Value.Draft.Policy))

	replay := newExamDraftTextUpdate(t, ctx, ss, created.Value.Exam.ID, creator.ID, 1, &title, &clearInstructions, updatedAt.Add(time.Second))
	replayed, err := ss.ExamAuthoring().UpdateDraftText(ctx, replay, updateCommand)
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
	require.Equal(t, int64(2), replayed.Value.Draft.Revision)
	replayAudit, err := ss.Audit().Get(ctx, replay.AuditEventID)
	require.NoError(t, err)
	require.Equal(t, model.AuditStatusSuccess, replayAudit.Status)

	staleTitle := "Operating Systems"
	stale := newExamDraftTextUpdate(t, ctx, ss, created.Value.Exam.ID, creator.ID, 1, &staleTitle, nil, updatedAt.Add(2*time.Second))
	_, err = ss.ExamAuthoring().UpdateDraftText(ctx, stale, examCommand(creator.ID, "exam.draft.text.edit", "stale-key", "stale-command"))
	var conflict *store.ErrConflict
	require.ErrorAs(t, err, &conflict, "stale update error")
	require.Equal(t, "exam_draft_revision", conflict.Constraint, "stale update error")
	current, err := ss.ExamAuthoring().Get(ctx, created.Value.Exam.ID, creator.ID)
	require.NoError(t, err)
	require.Equal(t, "Distributed Systems", current.Draft.Title)
	require.Equal(t, int64(2), current.Draft.Revision)
}

func testExamAuthoringUpdateDraftFocusLossAndConflict(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	unit := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "exam-focus-policy-unit")
	creator := saveUser(t, ctx, ss)
	createdAt := model.NowUTC()
	creation := newExamAuthoringCreation(t, ctx, ss, unit.ID, creator.ID, createdAt)
	created, err := ss.ExamAuthoring().Create(ctx, creation, examCommand(creator.ID, "exam.create", "focus-create", "focus-create-command"))
	require.NoError(t, err)

	focus := model.FocusLossPolicy{
		Enabled: false, MinimumDuration: 500 * time.Millisecond, IncidentCount: 100,
		Window: 4 * time.Hour, Outcome: model.IntegrityOutcomeFlagAndSuspend,
	}
	updatedAt := createdAt.Add(time.Minute)
	update := newExamDraftFocusLossUpdate(t, ctx, ss, created.Value.Exam.ID, creator.ID, 1, focus, updatedAt)
	command := examCommand(creator.ID, "exam.draft.focus_loss.configure", "focus-key", "focus-command")
	updated, err := ss.ExamAuthoring().UpdateDraftFocusLoss(ctx, update, command)
	require.NoError(t, err)
	require.False(t, updated.Replayed)
	require.Equal(t, focus, updated.Value.Draft.Policy.FocusLoss)
	require.Equal(t, int64(2), updated.Value.Draft.Revision)
	require.Equal(t, created.Value.Draft.Policy.ConnectionLoss, updated.Value.Draft.Policy.ConnectionLoss)
	require.Equal(t, created.Value.Draft.Title, updated.Value.Draft.Title)
	require.Equal(t, created.Value.Exam.Revision, updated.Value.Exam.Revision)

	replay := newExamDraftFocusLossUpdate(t, ctx, ss, created.Value.Exam.ID, creator.ID, 1, focus, updatedAt.Add(time.Second))
	replayed, err := ss.ExamAuthoring().UpdateDraftFocusLoss(ctx, replay, command)
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
	require.Equal(t, int64(2), replayed.Value.Draft.Revision)
	require.Equal(t, focus, replayed.Value.Draft.Policy.FocusLoss)

	staleFocus := model.DefaultExamPolicySet().FocusLoss
	stale := newExamDraftFocusLossUpdate(t, ctx, ss, created.Value.Exam.ID, creator.ID, 1, staleFocus, updatedAt.Add(2*time.Second))
	_, err = ss.ExamAuthoring().UpdateDraftFocusLoss(ctx, stale, examCommand(creator.ID, "exam.draft.focus_loss.configure", "focus-stale", "focus-stale-command"))
	var conflict *store.ErrConflict
	require.ErrorAs(t, err, &conflict, "stale update error")
	require.Equal(t, "exam_draft_revision", conflict.Constraint, "stale update error")
}

func newExamDraftFocusLossUpdate(t *testing.T, ctx context.Context, ss store.Store, examID model.ExamID, actorID model.UserID, expectedRevision int64, focus model.FocusLossPolicy, at time.Time) *store.ExamDraftFocusLossUpdate {
	t.Helper()
	exam, err := ss.ExamAuthoring().Resolve(ctx, examID)
	require.NoError(t, err)
	audit, err := ss.Audit().Save(ctx, &model.AuditEvent{
		ActorID: actorID, Action: string(model.ActionExamManage), Resource: model.Resource{Type: model.ResourceExam, ID: examID.String()},
		ScopeType: model.RoleScopeAcademicUnit, ScopeID: exam.AcademicUnitID.String(), Status: model.AuditStatusAttempt, NodeID: "test-node",
	})
	require.NoError(t, err)
	return &store.ExamDraftFocusLossUpdate{
		ExamID: examID, ActorUserID: actorID, ExpectedRevision: expectedRevision, FocusLoss: focus,
		UpdatedAt: at, AuditEventID: audit.ID, AuditAt: at,
	}
}

func newExamDraftTextUpdate(t *testing.T, ctx context.Context, ss store.Store, examID model.ExamID, actorID model.UserID, expectedRevision int64, title, instructions *string, at time.Time) *store.ExamDraftTextUpdate {
	t.Helper()
	exam, err := ss.ExamAuthoring().Resolve(ctx, examID)
	require.NoError(t, err)
	audit, err := ss.Audit().Save(ctx, &model.AuditEvent{
		ActorID: actorID, Action: string(model.ActionExamManage),
		Resource:  model.Resource{Type: model.ResourceExam, ID: examID.String()},
		ScopeType: model.RoleScopeAcademicUnit, ScopeID: exam.AcademicUnitID.String(),
		Status: model.AuditStatusAttempt, NodeID: "test-node",
	})
	require.NoError(t, err)
	return &store.ExamDraftTextUpdate{
		ExamID: examID, ActorUserID: actorID, ExpectedRevision: expectedRevision,
		Title: title, InstructionsMarkdown: instructions, UpdatedAt: at,
		AuditEventID: audit.ID, AuditAt: at,
	}
}

func examCommand(userID model.UserID, operation, key, fingerprint string) *store.CommandIdempotency {
	return &store.CommandIdempotency{
		UserID: userID, Operation: operation, KeyDigest: sha256.Sum256([]byte(key)), Fingerprint: sha256.Sum256([]byte(fingerprint)),
		Retention: time.Hour, Wait: time.Second,
	}
}

func newExamAuthoringCreation(t *testing.T, ctx context.Context, ss store.Store, unitID model.AcademicUnitID, creatorID model.UserID, at time.Time) *store.ExamAuthoringCreation {
	t.Helper()
	exam, err := model.NewExam(model.NewExamID(), unitID, creatorID, at)
	require.NoError(t, err)
	draft, err := model.NewExamDraft(exam.ID, "Programming Languages", "", model.DefaultExamPolicySet(), at)
	require.NoError(t, err)
	manager, err := model.NewExamManager(exam.ID, creatorID, creatorID, at)
	require.NoError(t, err)
	audit, err := ss.Audit().Save(ctx, &model.AuditEvent{
		ActorID: creatorID, Action: string(model.ActionExamCreate),
		Resource:  model.Resource{Type: model.ResourceAcademicUnit, ID: unitID.String()},
		ScopeType: model.RoleScopeAcademicUnit, ScopeID: unitID.String(),
		Status: model.AuditStatusAttempt, NodeID: "test-node",
	})
	require.NoError(t, err)
	return &store.ExamAuthoringCreation{
		Exam: exam, Draft: draft, Manager: manager,
		AuditEventID: audit.ID, AuditAt: at,
	}
}

func testExamAuthoringUpdateDraftNativePolicyAndConflict(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	unit := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "exam-native-policy-unit")
	creator := saveUser(t, ctx, ss)
	createdAt := model.NowUTC()
	creation := newExamAuthoringCreation(t, ctx, ss, unit.ID, creator.ID, createdAt)
	created, err := ss.ExamAuthoring().Create(ctx, creation, examCommand(creator.ID, "exam.create", "native-create", "native-create-command"))
	require.NoError(t, err)

	native := model.DefaultNativeSecurityPolicy()
	require.NoError(t, json.Unmarshal([]byte(`{"id":"interactive_session","mode":"observe"}`), &native.Families[1]))
	updatedAt := createdAt.Add(time.Minute)
	update := newExamDraftNativePolicyUpdate(t, ctx, ss, created.Value.Exam.ID, creator.ID, 1, native, updatedAt)
	command := examCommand(creator.ID, "exam.draft.native_policy.configure", "native-key", "native-command")
	updated, err := ss.ExamAuthoring().UpdateDraftNativePolicy(ctx, update, command)
	require.NoError(t, err)
	require.False(t, updated.Replayed)
	require.Equal(t, model.NativeModeObserve, updated.Value.Draft.Policy.Native.Families[1].Mode())
	require.Equal(t, int64(2), updated.Value.Draft.Revision)
	require.Equal(t, created.Value.Draft.Policy.ConnectionLoss, updated.Value.Draft.Policy.ConnectionLoss)
	require.Equal(t, created.Value.Draft.Title, updated.Value.Draft.Title)
	require.Equal(t, created.Value.Exam.Revision, updated.Value.Exam.Revision)

	replay := newExamDraftNativePolicyUpdate(t, ctx, ss, created.Value.Exam.ID, creator.ID, 1, native, updatedAt.Add(time.Second))
	replayed, err := ss.ExamAuthoring().UpdateDraftNativePolicy(ctx, replay, command)
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
	require.Equal(t, int64(2), replayed.Value.Draft.Revision)
	require.Equal(t, model.NativeModeObserve, replayed.Value.Draft.Policy.Native.Families[1].Mode())

	staleFocus := model.DefaultNativeSecurityPolicy()
	stale := newExamDraftNativePolicyUpdate(t, ctx, ss, created.Value.Exam.ID, creator.ID, 1, staleFocus, updatedAt.Add(2*time.Second))
	_, err = ss.ExamAuthoring().UpdateDraftNativePolicy(ctx, stale, examCommand(creator.ID, "exam.draft.native_policy.configure", "native-stale", "native-stale-command"))
	var conflict *store.ErrConflict
	require.ErrorAs(t, err, &conflict, "stale update error")
	require.Equal(t, "exam_draft_revision", conflict.Constraint, "stale update error")
}

func newExamDraftNativePolicyUpdate(t *testing.T, ctx context.Context, ss store.Store, examID model.ExamID, actorID model.UserID, expectedRevision int64, native model.NativeSecurityPolicy, at time.Time) *store.ExamDraftNativePolicyUpdate {
	t.Helper()
	exam, err := ss.ExamAuthoring().Resolve(ctx, examID)
	require.NoError(t, err)
	audit, err := ss.Audit().Save(ctx, &model.AuditEvent{
		ActorID: actorID, Action: string(model.ActionExamManage), Resource: model.Resource{Type: model.ResourceExam, ID: examID.String()},
		ScopeType: model.RoleScopeAcademicUnit, ScopeID: exam.AcademicUnitID.String(), Status: model.AuditStatusAttempt, NodeID: "test-node",
	})
	require.NoError(t, err)
	return &store.ExamDraftNativePolicyUpdate{
		ExamID: examID, ActorUserID: actorID, ExpectedRevision: expectedRevision, NativePolicy: native,
		UpdatedAt: at, AuditEventID: audit.ID, AuditAt: at,
	}
}

func testBrowserPolicyInstitutionPin(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	unit := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "browser-pin")
	creator := saveUser(t, ctx, ss)
	at := model.NowUTC()
	creation := newExamAuthoringCreation(t, ctx, ss, unit.ID, creator.ID, at)
	created, err := ss.ExamAuthoring().Create(ctx, creation, examCommand(creator.ID, "exam.create", "pin-create", "pin-create"))
	require.NoError(t, err)
	policy, err := model.NewBrowserPolicy(true, "start", []model.BrowserPolicyRule{{RuleID: "start", Origin: "http://institution.example:8080", PathPrefix: "/exam", HostMatch: model.BrowserPolicyHostExact, BlockedNavigationOutcome: model.BrowserPolicyBlockedNavigationIntegrityEvidence, InstitutionHTTPException: true}})
	require.NoError(t, err)
	base := newExamDraftTextUpdate(t, ctx, ss, created.Value.Exam.ID, creator.ID, 1, nil, nil, at.Add(time.Second))
	input := &store.ExamDraftBrowserPolicyUpdate{ExamID: base.ExamID, ActorUserID: creator.ID, ExpectedRevision: 1, Policy: policy, InstitutionOrigin: "https://unrelated.example:8080", UpdatedAt: base.UpdatedAt, AuditEventID: base.AuditEventID, AuditAt: base.AuditAt}
	command := examCommand(creator.ID, "exam.draft.browser_policy.configure", "pin-update", "pin-update")
	_, err = ss.ExamAuthoring().UpdateDraftBrowserPolicy(ctx, input, command)
	var invalid *store.ErrInvalidInput
	require.ErrorAs(t, err, &invalid)
	unchanged, err := ss.ExamAuthoring().Get(ctx, input.ExamID, creator.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1), unchanged.Draft.Revision, "refused exception changed draft")
	require.False(t, unchanged.Draft.BrowserPolicy.Enabled, "refused exception changed draft")
	input.InstitutionOrigin = "https://institution.example:8080"
	updated, err := ss.ExamAuthoring().UpdateDraftBrowserPolicy(ctx, input, command)
	require.NoError(t, err)
	require.Equal(t, int64(2), updated.Value.Draft.Revision)
	require.True(t, updated.Value.Draft.BrowserPolicy.MayCreateIntegrityEvidence())
	reloaded, err := ss.ExamAuthoring().Get(ctx, input.ExamID, creator.ID)
	require.NoError(t, err)
	require.NoError(t, reloaded.Draft.BrowserPolicy.ValidateInstitutionOrigin(input.InstitutionOrigin))
	digest, err := model.BrowserPolicyDigest(reloaded.Draft.BrowserPolicy)
	require.NoError(t, err)
	require.True(t, model.IsValidSHA256Fingerprint(digest), "invalid persisted digest")
}
