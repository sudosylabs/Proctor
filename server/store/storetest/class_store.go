// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/channels/store/storetest/team_store.go. The
// suite receives the root store and verifies each ClassStore operation so every
// future persistence adapter can reuse the same tests.

package storetest

import (
	"context"
	"errors"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestClassStore(t *testing.T, ss store.Store) {
	t.Run("MutationAuditAtomicity", func(t *testing.T) { testClassStoreMutationAuditAtomicity(t, ss) })
	t.Run("Save", func(t *testing.T) { testClassStoreSave(t, ss) })
	t.Run("Get", func(t *testing.T) { testClassStoreGet(t, ss) })
	t.Run("GetByName", func(t *testing.T) { testClassStoreGetByName(t, ss) })
	t.Run("ListByProgrammeLevel", func(t *testing.T) { testClassStoreListByProgrammeLevel(t, ss) })
	t.Run("ListByAcademicPeriod", func(t *testing.T) { testClassStoreListByAcademicPeriod(t, ss) })
	t.Run("GetAcademicUnitId", func(t *testing.T) { testClassStoreGetAcademicUnitId(t, ss) })
	t.Run("Update", func(t *testing.T) { testClassStoreUpdate(t, ss) })
	t.Run("RejectUnknownProgrammeLevel", func(t *testing.T) {
		testClassStoreRejectUnknownProgrammeLevel(t, ss)
	})
	t.Run("RejectUnknownAcademicPeriod", func(t *testing.T) {
		testClassStoreRejectUnknownAcademicPeriod(t, ss)
	})
	t.Run("EnforceScopedNameUniqueness", func(t *testing.T) {
		testClassStoreEnforceScopedNameUniqueness(t, ss)
	})
	t.Run("SearchAndArchive", func(t *testing.T) {
		testClassStoreSearchAndArchive(t, ss)
	})
}

func testClassStoreMutationAuditAtomicity(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture := saveClassFixture(t, ctx, ss)
	createAttempt := saveClassAuditAttempt(t, ctx, ss, fixture.programme.AcademicUnitId)
	candidate := &model.Class{ProgrammeLevelId: fixture.level.Id, AcademicPeriodId: fixture.period.Id, Name: "audited-class", DisplayName: "Audited Class"}
	candidate.PrepareCreate(model.NewId(), model.GetMillis())
	created, err := ss.Class().Create(ctx, &store.ClassCreation{Class: candidate, AuditEventID: createAttempt.Id, AuditAt: model.GetMillis()})
	requireNoError(t, err)
	completed, err := ss.Audit().Get(ctx, createAttempt.Id)
	requireNoError(t, err)
	if completed.Status != model.AuditStatusSuccess {
		t.Fatalf("create audit status = %q", completed.Status)
	}

	rolledBackCreate := &model.Class{ProgrammeLevelId: fixture.level.Id, AcademicPeriodId: fixture.period.Id, Name: "rolled-back-class", DisplayName: "Rolled Back"}
	rolledBackCreate.PrepareCreate(model.NewId(), model.GetMillis())
	if _, err := ss.Class().Create(ctx, &store.ClassCreation{Class: rolledBackCreate, AuditEventID: model.NewId(), AuditAt: model.GetMillis()}); err == nil {
		t.Fatal("Create() succeeded without audit attempt")
	}
	if _, err := ss.Class().Get(ctx, rolledBackCreate.Id); !store.IsNotFound(err) {
		t.Fatalf("create survived rollback: %v", err)
	}

	updateAttempt := saveClassAuditAttempt(t, ctx, ss, fixture.programme.AcademicUnitId)
	updatedCandidate := *created
	updatedCandidate.DisplayName = "Updated Class"
	updatedCandidate.PrepareUpdate(created.UpdateAt + 1)
	updated, err := ss.Class().UpdateWithAudit(ctx, &store.ClassUpdate{Class: &updatedCandidate, ExpectedAcademicUnitID: fixture.programme.AcademicUnitId, ExpectedRevision: created.Revision, AuditEventID: updateAttempt.Id, AuditAt: model.GetMillis()})
	requireNoError(t, err)
	if updated.Revision != created.Revision+1 {
		t.Fatalf("update revision = %d, want %d", updated.Revision, created.Revision+1)
	}
	staleLegacy := *created
	staleLegacy.DisplayName = "Stale Legacy Update"
	if _, err := ss.Class().Update(ctx, &staleLegacy); !store.IsConflict(err) {
		t.Fatalf("stale Update() error = %v", err)
	}
	staleAttempt := saveClassAuditAttempt(t, ctx, ss, fixture.programme.AcademicUnitId)
	staleCandidate := *updated
	staleCandidate.DisplayName = "Stale Update"
	staleCandidate.PrepareUpdate(updated.UpdateAt + 1)
	if _, err := ss.Class().UpdateWithAudit(ctx, &store.ClassUpdate{Class: &staleCandidate, ExpectedAcademicUnitID: fixture.programme.AcademicUnitId, ExpectedRevision: created.Revision, AuditEventID: staleAttempt.Id, AuditAt: model.GetMillis()}); !store.IsConflict(err) {
		t.Fatalf("stale UpdateWithAudit() error = %v", err)
	}
	wrongOwnerAttempt := saveClassAuditAttempt(t, ctx, ss, fixture.programme.AcademicUnitId)
	if _, err := ss.Class().ArchiveWithAudit(ctx, &store.ClassArchive{ID: updated.Id, ExpectedAcademicUnitID: model.NewId(), ExpectedRevision: updated.Revision, ArchiveAt: updated.UpdateAt + 1, AuditEventID: wrongOwnerAttempt.Id, AuditAt: model.GetMillis()}); !store.IsConflict(err) {
		t.Fatalf("wrong-owner ArchiveWithAudit() error = %v", err)
	}
	rolledBack := *updated
	rolledBack.DisplayName = "Must Roll Back"
	rolledBack.PrepareUpdate(updated.UpdateAt + 1)
	if _, err := ss.Class().UpdateWithAudit(ctx, &store.ClassUpdate{Class: &rolledBack, ExpectedAcademicUnitID: fixture.programme.AcademicUnitId, ExpectedRevision: updated.Revision, AuditEventID: model.NewId(), AuditAt: model.GetMillis()}); err == nil {
		t.Fatal("UpdateWithAudit() succeeded without audit attempt")
	}
	persisted, err := ss.Class().Get(ctx, updated.Id)
	requireNoError(t, err)
	if persisted.DisplayName != updated.DisplayName {
		t.Fatalf("update survived rollback: %#v", persisted)
	}

	archiveAttempt := saveClassAuditAttempt(t, ctx, ss, fixture.programme.AcademicUnitId)
	archived, err := ss.Class().ArchiveWithAudit(ctx, &store.ClassArchive{ID: updated.Id, ExpectedAcademicUnitID: fixture.programme.AcademicUnitId, ExpectedRevision: updated.Revision, ArchiveAt: updated.UpdateAt + 1, AuditEventID: archiveAttempt.Id, AuditAt: model.GetMillis()})
	requireNoError(t, err)
	if archived.Revision != updated.Revision+1 {
		t.Fatalf("archive revision = %d, want %d", archived.Revision, updated.Revision+1)
	}
	if archived.DeleteAt == 0 {
		t.Fatalf("ArchiveWithAudit() = %#v", archived)
	}
	archiveRollback := saveClass(t, ctx, ss, fixture.level.Id, fixture.period.Id, "rolled-back-archive")
	if _, err := ss.Class().ArchiveWithAudit(ctx, &store.ClassArchive{ID: archiveRollback.Id, ExpectedAcademicUnitID: fixture.programme.AcademicUnitId, ExpectedRevision: archiveRollback.Revision, ArchiveAt: model.GetMillis(), AuditEventID: model.NewId(), AuditAt: model.GetMillis()}); err == nil {
		t.Fatal("ArchiveWithAudit() succeeded without audit attempt")
	}
	if _, err := ss.Class().Get(ctx, archiveRollback.Id); err != nil {
		t.Fatalf("archive survived rollback: %v", err)
	}
}

func saveClassAuditAttempt(t *testing.T, ctx context.Context, ss store.Store, unitID string) *model.AuditEvent {
	t.Helper()
	attempt, err := ss.Audit().Save(ctx, &model.AuditEvent{Action: string(model.ActionAcademicUnitManage), Resource: model.Resource{Type: model.ResourceAcademicUnit, Id: unitID}, ScopeType: model.RoleScopeAcademicUnit, ScopeId: unitID, Status: model.AuditStatusAttempt, NodeId: "test-node"})
	requireNoError(t, err)
	return attempt
}

func testClassStoreSearchAndArchive(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture := saveClassFixture(t, ctx, ss)
	class := saveClass(
		t, ctx, ss, fixture.level.Id, fixture.period.Id, "distinct-class-zeta",
	)
	unitID, err := ss.Class().GetAcademicUnitId(ctx, class.Id)
	requireNoError(t, err)
	found, err := ss.Class().SearchByAcademicUnit(ctx, unitID, "zeta", 10)
	requireNoError(t, err)
	if len(found) != 1 || found[0].Id != class.Id {
		t.Fatalf("SearchByAcademicUnit() = %#v", found)
	}
	archived, err := ss.Class().Delete(ctx, class.Id, model.GetMillis())
	requireNoError(t, err)
	if archived.DeleteAt == 0 {
		t.Fatalf("Delete() = %#v", archived)
	}
}

func testClassStoreGetAcademicUnitId(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture := saveClassFixture(t, ctx, ss)
	class := saveClass(t, ctx, ss, fixture.level.Id, fixture.period.Id, "class-a")
	academicUnitID, err := ss.Class().GetAcademicUnitId(ctx, class.Id)
	requireNoError(t, err)
	programme, err := ss.Programme().Get(ctx, fixture.programme.Id)
	requireNoError(t, err)
	if academicUnitID != programme.AcademicUnitId {
		t.Fatalf("GetAcademicUnitId() = %q, want %q", academicUnitID, programme.AcademicUnitId)
	}
	if _, err := ss.Class().GetAcademicUnitId(ctx, model.NewId()); !store.IsNotFound(err) {
		t.Fatalf("GetAcademicUnitId(missing) error = %v", err)
	}
}

func testClassStoreSave(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture := saveClassFixture(t, ctx, ss)
	class := &model.Class{
		ProgrammeLevelId: fixture.level.Id,
		AcademicPeriodId: fixture.period.Id,
		Name:             "class-a",
		DisplayName:      "Class A",
		Description:      "Primary student roster",
	}

	saved, err := ss.Class().Save(ctx, class)
	requireNoError(t, err)
	if !model.IsValidId(saved.Id) {
		t.Fatalf("Save() id = %q", saved.Id)
	}
	if class.Id != "" {
		t.Fatalf("Save() mutated input id to %q", class.Id)
	}

	_, err = ss.Class().Save(ctx, saved)
	var invalid *store.ErrInvalidInput
	if !errors.As(err, &invalid) {
		t.Fatalf("second Save(saved) error = %v, want invalid input", err)
	}
}

func testClassStoreGet(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture := saveClassFixture(t, ctx, ss)
	class := saveClass(t, ctx, ss, fixture.level.Id, fixture.period.Id, "class-a")

	got, err := ss.Class().Get(ctx, class.Id)
	requireNoError(t, err)
	if *got != *class {
		t.Fatalf("Get() = %#v, want %#v", got, class)
	}
	if _, err := ss.Class().Get(ctx, model.NewId()); !store.IsNotFound(err) {
		t.Fatalf("Get(missing) error = %v, want not found", err)
	}
}

func testClassStoreGetByName(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture := saveClassFixture(t, ctx, ss)
	class := saveClass(t, ctx, ss, fixture.level.Id, fixture.period.Id, "class-a")

	got, err := ss.Class().GetByName(ctx, fixture.level.Id, fixture.period.Id, class.Name)
	requireNoError(t, err)
	if got.Id != class.Id {
		t.Fatalf("GetByName() id = %q, want %q", got.Id, class.Id)
	}
	if _, err := ss.Class().GetByName(
		ctx,
		fixture.level.Id,
		fixture.period.Id,
		"missing",
	); !store.IsNotFound(err) {
		t.Fatalf("GetByName(missing) error = %v, want not found", err)
	}
}

func testClassStoreListByProgrammeLevel(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture := saveClassFixture(t, ctx, ss)
	nextPeriod := saveAcademicPeriod(
		t,
		ctx,
		ss,
		fixture.institution.Id,
		"2027-2028",
		fixture.period.StartAt+40_000_000_000,
	)
	otherLevel := saveProgrammeLevel(t, ctx, ss, fixture.programme.Id, "year-2")
	first := saveClass(t, ctx, ss, fixture.level.Id, fixture.period.Id, "class-a")
	second := saveClass(t, ctx, ss, fixture.level.Id, nextPeriod.Id, "class-a")
	saveClass(t, ctx, ss, otherLevel.Id, fixture.period.Id, "class-a")

	classes, err := ss.Class().ListByProgrammeLevel(ctx, fixture.level.Id)
	requireNoError(t, err)
	if len(classes) != 2 || classes[0].Id != first.Id || classes[1].Id != second.Id {
		t.Fatalf("ListByProgrammeLevel() = %#v", classes)
	}
	empty, err := ss.Class().ListByProgrammeLevel(ctx, model.NewId())
	requireNoError(t, err)
	if len(empty) != 0 {
		t.Fatalf("ListByProgrammeLevel(missing) = %#v, want empty", empty)
	}
}

func testClassStoreListByAcademicPeriod(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture := saveClassFixture(t, ctx, ss)
	otherLevel := saveProgrammeLevel(t, ctx, ss, fixture.programme.Id, "year-2")
	nextPeriod := saveAcademicPeriod(
		t,
		ctx,
		ss,
		fixture.institution.Id,
		"2027-2028",
		fixture.period.StartAt+40_000_000_000,
	)
	first := saveClass(t, ctx, ss, fixture.level.Id, fixture.period.Id, "class-a")
	second := saveClass(t, ctx, ss, otherLevel.Id, fixture.period.Id, "class-b")
	saveClass(t, ctx, ss, fixture.level.Id, nextPeriod.Id, "class-a")

	classes, err := ss.Class().ListByAcademicPeriod(ctx, fixture.period.Id)
	requireNoError(t, err)
	if len(classes) != 2 {
		t.Fatalf("ListByAcademicPeriod() = %#v", classes)
	}
	gotIDs := map[string]bool{classes[0].Id: true, classes[1].Id: true}
	if !gotIDs[first.Id] || !gotIDs[second.Id] {
		t.Fatalf("ListByAcademicPeriod() ids = %#v", gotIDs)
	}
	empty, err := ss.Class().ListByAcademicPeriod(ctx, model.NewId())
	requireNoError(t, err)
	if len(empty) != 0 {
		t.Fatalf("ListByAcademicPeriod(missing) = %#v, want empty", empty)
	}
}

func testClassStoreUpdate(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture := saveClassFixture(t, ctx, ss)
	otherLevel := saveProgrammeLevel(t, ctx, ss, fixture.programme.Id, "year-2")
	nextPeriod := saveAcademicPeriod(
		t,
		ctx,
		ss,
		fixture.institution.Id,
		"2027-2028",
		fixture.period.StartAt+40_000_000_000,
	)
	class := saveClass(t, ctx, ss, fixture.level.Id, fixture.period.Id, "class-a")
	createAt := class.CreateAt

	class.ProgrammeLevelId = otherLevel.Id
	class.AcademicPeriodId = nextPeriod.Id
	class.Name = "class-b"
	class.DisplayName = "Class B"
	updated, err := ss.Class().Update(ctx, class)
	requireNoError(t, err)
	if updated.ProgrammeLevelId != otherLevel.Id ||
		updated.AcademicPeriodId != nextPeriod.Id ||
		updated.Name != "class-b" {
		t.Fatalf("Update() = %#v", updated)
	}
	if updated.CreateAt != createAt || updated.UpdateAt < class.UpdateAt {
		t.Fatalf("Update() timestamps = %#v", updated)
	}

	missing := *updated
	missing.Id = model.NewId()
	_, err = ss.Class().Update(ctx, &missing)
	if !store.IsNotFound(err) {
		t.Fatalf("Update(missing) error = %v, want not found", err)
	}
}

func testClassStoreRejectUnknownProgrammeLevel(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	period := saveAcademicPeriod(t, ctx, ss, institution.Id, "2026-2027", 1_800_000_000_000)

	_, err := ss.Class().Save(ctx, &model.Class{
		ProgrammeLevelId: model.NewId(),
		AcademicPeriodId: period.Id,
		Name:             "class-a",
		DisplayName:      "Class A",
	})
	var reference *store.ErrReference
	if !errors.As(err, &reference) ||
		reference.Constraint != "classes_programme_level_id_fkey" {
		t.Fatalf("Save(unknown programme level) error = %v, want reference error", err)
	}
}

func testClassStoreRejectUnknownAcademicPeriod(t *testing.T, ss store.Store) {
	ctx := context.Background()
	_, programme := saveProgrammeParents(t, ctx, ss, "computer-science")
	level := saveProgrammeLevel(t, ctx, ss, programme.Id, "year-1")

	_, err := ss.Class().Save(ctx, &model.Class{
		ProgrammeLevelId: level.Id,
		AcademicPeriodId: model.NewId(),
		Name:             "class-a",
		DisplayName:      "Class A",
	})
	var reference *store.ErrReference
	if !errors.As(err, &reference) ||
		reference.Constraint != "classes_academic_period_id_fkey" {
		t.Fatalf("Save(unknown academic period) error = %v, want reference error", err)
	}
}

func testClassStoreEnforceScopedNameUniqueness(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture := saveClassFixture(t, ctx, ss)
	otherLevel := saveProgrammeLevel(t, ctx, ss, fixture.programme.Id, "year-2")
	nextPeriod := saveAcademicPeriod(
		t,
		ctx,
		ss,
		fixture.institution.Id,
		"2027-2028",
		fixture.period.StartAt+40_000_000_000,
	)
	saveClass(t, ctx, ss, fixture.level.Id, fixture.period.Id, "class-a")

	_, err := ss.Class().Save(ctx, &model.Class{
		ProgrammeLevelId: fixture.level.Id,
		AcademicPeriodId: fixture.period.Id,
		Name:             "class-a",
		DisplayName:      "Duplicate",
	})
	var conflict *store.ErrConflict
	if !errors.As(err, &conflict) || conflict.Constraint != "classes_active_name_key" {
		t.Fatalf("duplicate class error = %v, want scoped-name conflict", err)
	}

	if _, err := ss.Class().Save(ctx, &model.Class{
		ProgrammeLevelId: otherLevel.Id,
		AcademicPeriodId: fixture.period.Id,
		Name:             "class-a",
		DisplayName:      "Class A",
	}); err != nil {
		t.Fatalf("same name for another programme level error = %v", err)
	}
	if _, err := ss.Class().Save(ctx, &model.Class{
		ProgrammeLevelId: fixture.level.Id,
		AcademicPeriodId: nextPeriod.Id,
		Name:             "class-a",
		DisplayName:      "Class A",
	}); err != nil {
		t.Fatalf("same name for another academic period error = %v", err)
	}
}

type classFixture struct {
	institution *model.Institution
	programme   *model.Programme
	level       *model.ProgrammeLevel
	period      *model.AcademicPeriod
}

func saveClassFixture(t *testing.T, ctx context.Context, ss store.Store) classFixture {
	t.Helper()
	unit, programme := saveProgrammeParents(t, ctx, ss, "computer-science")
	level := saveProgrammeLevel(t, ctx, ss, programme.Id, "year-1")
	period := saveAcademicPeriod(t, ctx, ss, unit.InstitutionId, "2026-2027", 1_800_000_000_000)
	return classFixture{
		institution: &model.Institution{Id: unit.InstitutionId},
		programme:   programme,
		level:       level,
		period:      period,
	}
}

func saveClass(
	t *testing.T,
	ctx context.Context,
	ss store.Store,
	programmeLevelID string,
	academicPeriodID string,
	name string,
) *model.Class {
	t.Helper()
	class, err := ss.Class().Save(ctx, &model.Class{
		ProgrammeLevelId: programmeLevelID,
		AcademicPeriodId: academicPeriodID,
		Name:             name,
		DisplayName:      name,
	})
	requireNoError(t, err)
	return class
}
