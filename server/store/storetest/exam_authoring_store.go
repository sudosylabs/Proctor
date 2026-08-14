// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestExamAuthoringStore(t *testing.T, ss store.Store) {
	t.Run("CreateGetAndReplay", func(t *testing.T) { testExamAuthoringCreateGetAndReplay(t, ss) })
	t.Run("UpdateDraftTextAndConflict", func(t *testing.T) { testExamAuthoringUpdateDraftTextAndConflict(t, ss) })
	t.Run("UpdateDraftFocusLossAndConflict", func(t *testing.T) { testExamAuthoringUpdateDraftFocusLossAndConflict(t, ss) })
	t.Run("ListCatalogAndArchive", func(t *testing.T) { testExamCatalogListAndArchive(t, ss) })
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
		UserID: creator.ID, Operation: "exam.create.v1",
		KeyDigest: sha256.Sum256([]byte("key")), FingerprintVersion: 1,
		Fingerprint: sha256.Sum256([]byte("command")), OutcomeVersion: 1,
		Retention: time.Hour, Wait: time.Second,
	}

	first, err := ss.ExamAuthoring().Create(ctx, creation, command)
	requireNoError(t, err)
	if first.Replayed || first.Value.ManagerCount != 1 || !first.Value.ActorIsManager || first.Value.OwnerUserID != creator.ID {
		t.Fatalf("first create = %#v", first)
	}
	if first.Value.ResourceCount != 0 || first.Value.HasStarterWorkspace {
		t.Fatalf("new draft has resource/starter state: %#v", first.Value)
	}
	completed, err := ss.Audit().Get(ctx, creation.AuditEventID)
	requireNoError(t, err)
	if completed.Status != model.AuditStatusSuccess {
		t.Fatalf("creation audit status = %q, want success", completed.Status)
	}

	replayCreation := newExamAuthoringCreation(t, ctx, ss, unit.ID, creator.ID, at.Add(time.Second))
	replayCreation.Exam.ID = creation.Exam.ID
	replayCreation.Draft.ExamID = creation.Exam.ID
	replayCreation.Manager.ExamID = creation.Exam.ID
	replayed, err := ss.ExamAuthoring().Create(ctx, replayCreation, command)
	requireNoError(t, err)
	if !replayed.Replayed || replayed.Value.Exam.ID != first.Value.Exam.ID {
		t.Fatalf("replay = %#v", replayed)
	}
	replayAudit, err := ss.Audit().Get(ctx, replayCreation.AuditEventID)
	requireNoError(t, err)
	if replayAudit.Status != model.AuditStatusSuccess {
		t.Fatalf("replay audit status = %q, want success", replayAudit.Status)
	}
	var replayResult map[string]any
	if err := json.Unmarshal(replayAudit.Result, &replayResult); err != nil {
		t.Fatalf("decode replay audit result: %v", err)
	}
	if replayResult["idempotency_replayed"] != true || replayResult["original_audit_event_id"] != creation.AuditEventID {
		t.Fatalf("replay audit result = %#v", replayResult)
	}

	managerView, err := ss.ExamAuthoring().Get(ctx, first.Value.Exam.ID, creator.ID)
	requireNoError(t, err)
	outsiderView, err := ss.ExamAuthoring().Get(ctx, first.Value.Exam.ID, other.ID)
	requireNoError(t, err)
	managerAccess, err := ss.ExamAuthoring().Access(ctx, first.Value.Exam.ID, creator.ID)
	requireNoError(t, err)
	outsiderAccess, err := ss.ExamAuthoring().Access(ctx, first.Value.Exam.ID, other.ID)
	requireNoError(t, err)
	if !managerView.ActorIsManager || outsiderView.ActorIsManager || managerView.ManagerCount != 1 || outsiderView.ManagerCount != 1 {
		t.Fatalf("manager/outsider views = %#v / %#v", managerView, outsiderView)
	}
	if !managerAccess.ActorIsManager || outsiderAccess.ActorIsManager || managerAccess.Exam.ID != first.Value.Exam.ID || outsiderAccess.Exam.ID != first.Value.Exam.ID {
		t.Fatalf("manager/outsider access = %#v / %#v", managerAccess, outsiderAccess)
	}
	if managerView.Draft.Policy != model.DefaultExamPolicySet() {
		t.Fatalf("persisted policy = %#v", managerView.Draft.Policy)
	}
}

func testExamAuthoringAuditAtomicity(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	unit := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "exam-rollback-unit")
	creator := saveUser(t, ctx, ss)
	creation := newExamAuthoringCreation(t, ctx, ss, unit.ID, creator.ID, model.NowUTC())
	creation.AuditEventID = model.NewId()
	command := &store.CommandIdempotency{
		UserID: creator.ID, Operation: "exam.create.v1", KeyDigest: sha256.Sum256([]byte("rollback-key")),
		FingerprintVersion: 1, Fingerprint: sha256.Sum256([]byte("rollback-command")), OutcomeVersion: 1,
		Retention: time.Hour, Wait: time.Second,
	}
	if _, err := ss.ExamAuthoring().Create(ctx, creation, command); err == nil {
		t.Fatal("Create succeeded without its audit attempt")
	}
	if _, err := ss.ExamAuthoring().Get(ctx, creation.Exam.ID, creator.ID); !store.IsNotFound(err) {
		t.Fatalf("exam survived audit rollback: %v", err)
	}
}

func testExamAuthoringUpdateDraftTextAndConflict(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	unit := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "exam-edit-unit")
	creator := saveUser(t, ctx, ss)
	createdAt := model.NowUTC()
	creation := newExamAuthoringCreation(t, ctx, ss, unit.ID, creator.ID, createdAt)
	createCommand := examCommand(creator.ID, "exam.create.v1", "edit-create", "edit-create-command")
	created, err := ss.ExamAuthoring().Create(ctx, creation, createCommand)
	requireNoError(t, err)

	title := "  Distributed Systems  "
	clearInstructions := ""
	updatedAt := createdAt.Add(time.Minute)
	update := newExamDraftTextUpdate(t, ctx, ss, created.Value.Exam.ID, creator.ID, 1, &title, &clearInstructions, updatedAt)
	updateCommand := examCommand(creator.ID, "exam.draft.text.edit.v1", "edit-key", "edit-command")
	updated, err := ss.ExamAuthoring().UpdateDraftText(ctx, update, updateCommand)
	requireNoError(t, err)
	if updated.Replayed || updated.Value.Draft.Title != "Distributed Systems" || updated.Value.Draft.InstructionsMarkdown != "" || updated.Value.Draft.Revision != 2 {
		t.Fatalf("updated Draft = %#v", updated)
	}
	if updated.Value.Exam.Revision != created.Value.Exam.Revision || updated.Value.Draft.Policy != created.Value.Draft.Policy {
		t.Fatalf("text update changed unrelated state: %#v", updated.Value)
	}

	replay := newExamDraftTextUpdate(t, ctx, ss, created.Value.Exam.ID, creator.ID, 1, &title, &clearInstructions, updatedAt.Add(time.Second))
	replayed, err := ss.ExamAuthoring().UpdateDraftText(ctx, replay, updateCommand)
	requireNoError(t, err)
	if !replayed.Replayed || replayed.Value.Draft.Revision != 2 {
		t.Fatalf("replayed update = %#v", replayed)
	}
	replayAudit, err := ss.Audit().Get(ctx, replay.AuditEventID)
	requireNoError(t, err)
	if replayAudit.Status != model.AuditStatusSuccess {
		t.Fatalf("replay audit status = %s", replayAudit.Status)
	}

	staleTitle := "Operating Systems"
	stale := newExamDraftTextUpdate(t, ctx, ss, created.Value.Exam.ID, creator.ID, 1, &staleTitle, nil, updatedAt.Add(2*time.Second))
	_, err = ss.ExamAuthoring().UpdateDraftText(ctx, stale, examCommand(creator.ID, "exam.draft.text.edit.v1", "stale-key", "stale-command"))
	var conflict *store.ErrConflict
	if !errors.As(err, &conflict) || conflict.Constraint != "exam_draft_revision" {
		t.Fatalf("stale update error = %v, want exam_draft_revision conflict", err)
	}
	current, err := ss.ExamAuthoring().Get(ctx, created.Value.Exam.ID, creator.ID)
	requireNoError(t, err)
	if current.Draft.Title != "Distributed Systems" || current.Draft.Revision != 2 {
		t.Fatalf("stale update changed Draft: %#v", current.Draft)
	}
}

func testExamAuthoringUpdateDraftFocusLossAndConflict(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	unit := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "exam-focus-policy-unit")
	creator := saveUser(t, ctx, ss)
	createdAt := model.NowUTC()
	creation := newExamAuthoringCreation(t, ctx, ss, unit.ID, creator.ID, createdAt)
	created, err := ss.ExamAuthoring().Create(ctx, creation, examCommand(creator.ID, "exam.create.v1", "focus-create", "focus-create-command"))
	requireNoError(t, err)

	focus := model.FocusLossPolicy{
		Enabled: false, MinimumDuration: 500 * time.Millisecond, IncidentCount: 100,
		Window: 4 * time.Hour, Outcome: model.IntegrityOutcomeFlagAndSuspend,
	}
	updatedAt := createdAt.Add(time.Minute)
	update := newExamDraftFocusLossUpdate(t, ctx, ss, created.Value.Exam.ID, creator.ID, 1, focus, updatedAt)
	command := examCommand(creator.ID, "exam.draft.focus_loss.configure.v1", "focus-key", "focus-command")
	updated, err := ss.ExamAuthoring().UpdateDraftFocusLoss(ctx, update, command)
	requireNoError(t, err)
	if updated.Replayed || updated.Value.Draft.Policy.FocusLoss != focus || updated.Value.Draft.Revision != 2 {
		t.Fatalf("updated Draft = %#v", updated)
	}
	if updated.Value.Draft.Policy.ConnectionLoss != created.Value.Draft.Policy.ConnectionLoss || updated.Value.Draft.Title != created.Value.Draft.Title || updated.Value.Exam.Revision != created.Value.Exam.Revision {
		t.Fatalf("Focus Loss update changed unrelated state: %#v", updated.Value)
	}

	replay := newExamDraftFocusLossUpdate(t, ctx, ss, created.Value.Exam.ID, creator.ID, 1, focus, updatedAt.Add(time.Second))
	replayed, err := ss.ExamAuthoring().UpdateDraftFocusLoss(ctx, replay, command)
	requireNoError(t, err)
	if !replayed.Replayed || replayed.Value.Draft.Revision != 2 || replayed.Value.Draft.Policy.FocusLoss != focus {
		t.Fatalf("replayed update = %#v", replayed)
	}

	staleFocus := model.DefaultExamPolicySet().FocusLoss
	stale := newExamDraftFocusLossUpdate(t, ctx, ss, created.Value.Exam.ID, creator.ID, 1, staleFocus, updatedAt.Add(2*time.Second))
	_, err = ss.ExamAuthoring().UpdateDraftFocusLoss(ctx, stale, examCommand(creator.ID, "exam.draft.focus_loss.configure.v1", "focus-stale", "focus-stale-command"))
	var conflict *store.ErrConflict
	if !errors.As(err, &conflict) || conflict.Constraint != "exam_draft_revision" {
		t.Fatalf("stale update error = %v, want exam_draft_revision conflict", err)
	}
}

func newExamDraftFocusLossUpdate(t *testing.T, ctx context.Context, ss store.Store, examID model.ExamID, actorID model.UserID, expectedRevision int64, focus model.FocusLossPolicy, at time.Time) *store.ExamDraftFocusLossUpdate {
	t.Helper()
	exam, err := ss.ExamAuthoring().Resolve(ctx, examID)
	requireNoError(t, err)
	audit, err := ss.Audit().Save(ctx, &model.AuditEvent{
		ActorID: actorID, Action: string(model.ActionExamManage), Resource: model.Resource{Type: model.ResourceExam, ID: examID.String()},
		ScopeType: model.RoleScopeAcademicUnit, ScopeID: exam.AcademicUnitID.String(), Status: model.AuditStatusAttempt, NodeID: "test-node",
	})
	requireNoError(t, err)
	return &store.ExamDraftFocusLossUpdate{
		ExamID: examID, ActorUserID: actorID, ExpectedRevision: expectedRevision, FocusLoss: focus,
		UpdatedAt: model.MillisFromTime(at), AuditEventID: audit.ID.String(), AuditAt: model.MillisFromTime(at),
	}
}

func newExamDraftTextUpdate(t *testing.T, ctx context.Context, ss store.Store, examID model.ExamID, actorID model.UserID, expectedRevision int64, title, instructions *string, at time.Time) *store.ExamDraftTextUpdate {
	t.Helper()
	exam, err := ss.ExamAuthoring().Resolve(ctx, examID)
	requireNoError(t, err)
	audit, err := ss.Audit().Save(ctx, &model.AuditEvent{
		ActorID: actorID, Action: string(model.ActionExamManage),
		Resource:  model.Resource{Type: model.ResourceExam, ID: examID.String()},
		ScopeType: model.RoleScopeAcademicUnit, ScopeID: exam.AcademicUnitID.String(),
		Status: model.AuditStatusAttempt, NodeID: "test-node",
	})
	requireNoError(t, err)
	return &store.ExamDraftTextUpdate{
		ExamID: examID, ActorUserID: actorID, ExpectedRevision: expectedRevision,
		Title: title, InstructionsMarkdown: instructions, UpdatedAt: model.MillisFromTime(at),
		AuditEventID: audit.ID.String(), AuditAt: model.MillisFromTime(at),
	}
}

func examCommand(userID model.UserID, operation, key, fingerprint string) *store.CommandIdempotency {
	return &store.CommandIdempotency{
		UserID: userID, Operation: operation, KeyDigest: sha256.Sum256([]byte(key)),
		FingerprintVersion: 1, Fingerprint: sha256.Sum256([]byte(fingerprint)), OutcomeVersion: 1,
		Retention: time.Hour, Wait: time.Second,
	}
}

func newExamAuthoringCreation(t *testing.T, ctx context.Context, ss store.Store, unitID model.AcademicUnitID, creatorID model.UserID, at time.Time) *store.ExamAuthoringCreation {
	t.Helper()
	exam, err := model.NewExam(model.NewExamID(), unitID, creatorID, at)
	requireNoError(t, err)
	draft, err := model.NewExamDraft(exam.ID, "Programming Languages", "", model.DefaultExamPolicySet(), at)
	requireNoError(t, err)
	manager, err := model.NewExamManager(exam.ID, creatorID, creatorID, at)
	requireNoError(t, err)
	audit, err := ss.Audit().Save(ctx, &model.AuditEvent{
		ActorID: creatorID, Action: string(model.ActionExamCreate),
		Resource:  model.Resource{Type: model.ResourceAcademicUnit, ID: unitID.String()},
		ScopeType: model.RoleScopeAcademicUnit, ScopeID: unitID.String(),
		Status: model.AuditStatusAttempt, NodeID: "test-node",
	})
	requireNoError(t, err)
	return &store.ExamAuthoringCreation{
		Exam: exam, Draft: draft, Manager: manager,
		AuditEventID: audit.ID.String(), AuditAt: model.MillisFromTime(at),
	}
}
