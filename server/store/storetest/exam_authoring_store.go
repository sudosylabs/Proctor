// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestExamAuthoringStore(t *testing.T, ss store.Store) {
	t.Run("CreateGetAndReplay", func(t *testing.T) { testExamAuthoringCreateGetAndReplay(t, ss) })
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

	first, err := ss.ExamAuthoring().CreateIdempotently(ctx, creation, command)
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
	replayed, err := ss.ExamAuthoring().CreateIdempotently(ctx, replayCreation, command)
	requireNoError(t, err)
	if !replayed.Replayed || replayed.Value.Exam.ID != first.Value.Exam.ID {
		t.Fatalf("replay = %#v", replayed)
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
	if _, err := ss.ExamAuthoring().Create(ctx, creation); err == nil {
		t.Fatal("Create succeeded without its audit attempt")
	}
	if _, err := ss.ExamAuthoring().Get(ctx, creation.Exam.ID, creator.ID); !store.IsNotFound(err) {
		t.Fatalf("exam survived audit rollback: %v", err)
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
