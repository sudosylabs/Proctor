// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/channels/store/storetest/team_store.go. The
// suite receives the root store and verifies each ProgrammeStore operation so
// every future persistence adapter can reuse the same contract tests.

package storetest

import (
	"context"
	"errors"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestProgrammeStore(t *testing.T, ss store.Store) {
	t.Run("MutationAuditAtomicity", func(t *testing.T) { testProgrammeStoreMutationAuditAtomicity(t, ss) })
	t.Run("Save", func(t *testing.T) { testProgrammeStoreSave(t, ss) })
	t.Run("Get", func(t *testing.T) { testProgrammeStoreGet(t, ss) })
	t.Run("GetByName", func(t *testing.T) { testProgrammeStoreGetByName(t, ss) })
	t.Run("ListByAcademicUnit", func(t *testing.T) { testProgrammeStoreListByAcademicUnit(t, ss) })
	t.Run("Update", func(t *testing.T) { testProgrammeStoreUpdate(t, ss) })
	t.Run("RejectUnknownAcademicUnit", func(t *testing.T) {
		testProgrammeStoreRejectUnknownAcademicUnit(t, ss)
	})
	t.Run("EnforceAcademicUnitNameUniqueness", func(t *testing.T) {
		testProgrammeStoreEnforceAcademicUnitNameUniqueness(t, ss)
	})
	t.Run("SearchAndArchive", func(t *testing.T) {
		testProgrammeStoreSearchAndArchive(t, ss)
	})
}

func testProgrammeStoreMutationAuditAtomicity(t *testing.T, ss store.Store) {
	ctx := context.Background()
	unit, _ := saveProgrammeParents(t, ctx, ss, "audited-programme-parent")
	createAttempt := saveProgrammeAuditAttempt(t, ctx, ss, unit.Id)
	candidate := &model.Programme{AcademicUnitId: unit.Id, Name: "audited-programme", DisplayName: "Audited Programme"}
	candidate.PrepareCreate(model.NewId(), model.GetMillis())
	created, err := ss.Programme().Create(ctx, &store.ProgrammeCreation{Programme: candidate, AuditEventID: createAttempt.Id, AuditAt: model.GetMillis()})
	requireNoError(t, err)
	completed, err := ss.Audit().Get(ctx, createAttempt.Id)
	requireNoError(t, err)
	if completed.Status != model.AuditStatusSuccess {
		t.Fatalf("create audit status = %q", completed.Status)
	}
	rolledBackCreate := &model.Programme{AcademicUnitId: unit.Id, Name: "rolled-back-create", DisplayName: "Rolled Back Create"}
	rolledBackCreate.PrepareCreate(model.NewId(), model.GetMillis())
	if _, err := ss.Programme().Create(ctx, &store.ProgrammeCreation{Programme: rolledBackCreate, AuditEventID: model.NewId(), AuditAt: model.GetMillis()}); err == nil {
		t.Fatal("Create() succeeded without its audit attempt")
	}
	if _, err := ss.Programme().Get(ctx, rolledBackCreate.Id); !store.IsNotFound(err) {
		t.Fatalf("create survived audit rollback: %v", err)
	}

	updateAttempt := saveProgrammeAuditAttempt(t, ctx, ss, unit.Id)
	updatedCandidate := *created
	updatedCandidate.DisplayName = "Updated Programme"
	updatedCandidate.PrepareUpdate(model.GetMillis())
	updated, err := ss.Programme().UpdateWithAudit(ctx, &store.ProgrammeUpdate{Programme: &updatedCandidate, AuditEventID: updateAttempt.Id, AuditAt: model.GetMillis()})
	requireNoError(t, err)
	completed, err = ss.Audit().Get(ctx, updateAttempt.Id)
	requireNoError(t, err)
	if completed.Status != model.AuditStatusSuccess {
		t.Fatalf("update audit status = %q", completed.Status)
	}

	rolledBack := *updated
	rolledBack.DisplayName = "Must Roll Back"
	rolledBack.PrepareUpdate(model.GetMillis())
	if _, err := ss.Programme().UpdateWithAudit(ctx, &store.ProgrammeUpdate{Programme: &rolledBack, AuditEventID: model.NewId(), AuditAt: model.GetMillis()}); err == nil {
		t.Fatal("UpdateWithAudit() succeeded without its audit attempt")
	}
	persisted, err := ss.Programme().Get(ctx, updated.Id)
	requireNoError(t, err)
	if persisted.DisplayName != updated.DisplayName {
		t.Fatalf("update survived audit rollback: %#v", persisted)
	}

	archiveAttempt := saveProgrammeAuditAttempt(t, ctx, ss, unit.Id)
	archived, err := ss.Programme().ArchiveWithAudit(ctx, &store.ProgrammeArchive{ID: updated.Id, ArchiveAt: model.GetMillis(), AuditEventID: archiveAttempt.Id, AuditAt: model.GetMillis()})
	requireNoError(t, err)
	if archived.DeleteAt == 0 {
		t.Fatalf("ArchiveWithAudit() = %#v", archived)
	}
	completed, err = ss.Audit().Get(ctx, archiveAttempt.Id)
	requireNoError(t, err)
	if completed.Status != model.AuditStatusSuccess {
		t.Fatalf("archive audit status = %q", completed.Status)
	}

	archiveRollback := saveProgramme(t, ctx, ss, unit.Id, "rolled-back-archive")
	if _, err := ss.Programme().ArchiveWithAudit(ctx, &store.ProgrammeArchive{ID: archiveRollback.Id, ArchiveAt: model.GetMillis(), AuditEventID: model.NewId(), AuditAt: model.GetMillis()}); err == nil {
		t.Fatal("ArchiveWithAudit() succeeded without its audit attempt")
	}
	if _, err := ss.Programme().Get(ctx, archiveRollback.Id); err != nil {
		t.Fatalf("archive survived audit rollback: %v", err)
	}

	withLevel := saveProgramme(t, ctx, ss, unit.Id, "programme-with-level")
	saveProgrammeLevel(t, ctx, ss, withLevel.Id, "year-1")
	blockedAttempt := saveProgrammeAuditAttempt(t, ctx, ss, unit.Id)
	if _, err := ss.Programme().ArchiveWithAudit(ctx, &store.ProgrammeArchive{ID: withLevel.Id, ArchiveAt: model.GetMillis(), AuditEventID: blockedAttempt.Id, AuditAt: model.GetMillis()}); !store.IsConflict(err) {
		t.Fatalf("archive with active level error = %v, want conflict", err)
	}
}

func saveProgrammeAuditAttempt(t *testing.T, ctx context.Context, ss store.Store, unitID string) *model.AuditEvent {
	t.Helper()
	attempt, err := ss.Audit().Save(ctx, &model.AuditEvent{
		Action:    string(model.ActionAcademicUnitManage),
		Resource:  model.Resource{Type: model.ResourceAcademicUnit, Id: unitID},
		ScopeType: model.RoleScopeAcademicUnit, ScopeId: unitID,
		Status: model.AuditStatusAttempt, NodeId: "test-node",
	})
	requireNoError(t, err)
	return attempt
}

func testProgrammeStoreSearchAndArchive(t *testing.T, ss store.Store) {
	ctx := context.Background()
	unit, _ := saveProgrammeParents(t, ctx, ss, "archive-programme-parent")
	programme := saveProgramme(t, ctx, ss, unit.Id, "distinct-robotics")
	found, err := ss.Programme().SearchByAcademicUnit(ctx, unit.Id, "robot", 10)
	requireNoError(t, err)
	if len(found) != 1 || found[0].Id != programme.Id {
		t.Fatalf("SearchByAcademicUnit() = %#v", found)
	}
	archived, err := ss.Programme().Delete(ctx, programme.Id, model.GetMillis())
	requireNoError(t, err)
	if archived.DeleteAt == 0 {
		t.Fatalf("Delete() = %#v", archived)
	}
}

func testProgrammeStoreSave(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	unit := saveAcademicUnit(t, ctx, ss, institution.Id, "", "computing")
	programme := &model.Programme{
		AcademicUnitId: unit.Id,
		Name:           "computer-science",
		DisplayName:    "Computer Science",
		Description:    "A course of study",
	}

	saved, err := ss.Programme().Save(ctx, programme)
	requireNoError(t, err)
	if !model.IsValidId(saved.Id) {
		t.Fatalf("Save() id = %q", saved.Id)
	}
	if programme.Id != "" {
		t.Fatalf("Save() mutated input id to %q", programme.Id)
	}

	_, err = ss.Programme().Save(ctx, saved)
	var invalid *store.ErrInvalidInput
	if !errors.As(err, &invalid) {
		t.Fatalf("second Save(saved) error = %v, want invalid input", err)
	}
}

func testProgrammeStoreGet(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	unit := saveAcademicUnit(t, ctx, ss, institution.Id, "", "computing")
	programme := saveProgramme(t, ctx, ss, unit.Id, "computer-science")

	got, err := ss.Programme().Get(ctx, programme.Id)
	requireNoError(t, err)
	if *got != *programme {
		t.Fatalf("Get() = %#v, want %#v", got, programme)
	}
	if _, err := ss.Programme().Get(ctx, model.NewId()); !store.IsNotFound(err) {
		t.Fatalf("Get(missing) error = %v, want not found", err)
	}
}

func testProgrammeStoreGetByName(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	unit := saveAcademicUnit(t, ctx, ss, institution.Id, "", "computing")
	programme := saveProgramme(t, ctx, ss, unit.Id, "computer-science")

	got, err := ss.Programme().GetByName(ctx, unit.Id, programme.Name)
	requireNoError(t, err)
	if got.Id != programme.Id {
		t.Fatalf("GetByName() id = %q, want %q", got.Id, programme.Id)
	}
	if _, err := ss.Programme().GetByName(ctx, unit.Id, "missing"); !store.IsNotFound(err) {
		t.Fatalf("GetByName(missing) error = %v, want not found", err)
	}
}

func testProgrammeStoreListByAcademicUnit(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	computing := saveAcademicUnit(t, ctx, ss, institution.Id, "", "computing")
	engineering := saveAcademicUnit(t, ctx, ss, institution.Id, "", "engineering")
	second := saveProgramme(t, ctx, ss, computing.Id, "software-engineering")
	first := saveProgramme(t, ctx, ss, computing.Id, "computer-science")
	saveProgramme(t, ctx, ss, engineering.Id, "civil-engineering")

	programmes, err := ss.Programme().ListByAcademicUnit(ctx, computing.Id)
	requireNoError(t, err)
	if len(programmes) != 2 || programmes[0].Id != first.Id || programmes[1].Id != second.Id {
		t.Fatalf("ListByAcademicUnit() = %#v", programmes)
	}
	empty, err := ss.Programme().ListByAcademicUnit(ctx, model.NewId())
	requireNoError(t, err)
	if len(empty) != 0 {
		t.Fatalf("ListByAcademicUnit(missing) = %#v, want empty", empty)
	}
}

func testProgrammeStoreUpdate(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	computing := saveAcademicUnit(t, ctx, ss, institution.Id, "", "computing")
	engineering := saveAcademicUnit(t, ctx, ss, institution.Id, "", "engineering")
	programme := saveProgramme(t, ctx, ss, computing.Id, "computer-science")
	createAt := programme.CreateAt

	programme.AcademicUnitId = engineering.Id
	programme.Name = "computing-engineering"
	programme.DisplayName = "Computing Engineering"
	updated, err := ss.Programme().Update(ctx, programme)
	requireNoError(t, err)
	if updated.AcademicUnitId != engineering.Id || updated.Name != "computing-engineering" {
		t.Fatalf("Update() = %#v", updated)
	}
	if updated.CreateAt != createAt || updated.UpdateAt < programme.UpdateAt {
		t.Fatalf("Update() timestamps = %#v", updated)
	}

	missing := *updated
	missing.Id = model.NewId()
	_, err = ss.Programme().Update(ctx, &missing)
	if !store.IsNotFound(err) {
		t.Fatalf("Update(missing) error = %v, want not found", err)
	}

	archivedUnit := saveAcademicUnit(t, ctx, ss, institution.Id, "", "archived-unit")
	if _, err := ss.AcademicUnit().Delete(ctx, archivedUnit.Id, model.GetMillis()); err != nil {
		t.Fatalf("archive destination unit: %v", err)
	}
	invalidMove := *updated
	invalidMove.AcademicUnitId = archivedUnit.Id
	_, err = ss.Programme().Update(ctx, &invalidMove)
	var reference *store.ErrReference
	if !errors.As(err, &reference) {
		t.Fatalf("Update(archived academic unit) error = %v, want reference", err)
	}
}

func testProgrammeStoreRejectUnknownAcademicUnit(t *testing.T, ss store.Store) {
	ctx := context.Background()
	saveInstitution(t, ctx, ss)

	_, err := ss.Programme().Save(ctx, &model.Programme{
		AcademicUnitId: model.NewId(),
		Name:           "computer-science",
		DisplayName:    "Computer Science",
	})
	var reference *store.ErrReference
	if !errors.As(err, &reference) ||
		reference.Constraint != "programmes_academic_unit_id_fkey" {
		t.Fatalf("Save(unknown academic unit) error = %v, want reference error", err)
	}
}

func testProgrammeStoreEnforceAcademicUnitNameUniqueness(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	computing := saveAcademicUnit(t, ctx, ss, institution.Id, "", "computing")
	engineering := saveAcademicUnit(t, ctx, ss, institution.Id, "", "engineering")
	saveProgramme(t, ctx, ss, computing.Id, "computer-science")

	_, err := ss.Programme().Save(ctx, &model.Programme{
		AcademicUnitId: computing.Id,
		Name:           "computer-science",
		DisplayName:    "Duplicate",
	})
	var conflict *store.ErrConflict
	if !errors.As(err, &conflict) || conflict.Constraint != "programmes_active_name_key" {
		t.Fatalf("duplicate programme error = %v, want scoped-name conflict", err)
	}

	if _, err := ss.Programme().Save(ctx, &model.Programme{
		AcademicUnitId: engineering.Id,
		Name:           "computer-science",
		DisplayName:    "Computer Science",
	}); err != nil {
		t.Fatalf("same name in another academic unit error = %v", err)
	}
}

func saveProgramme(
	t *testing.T,
	ctx context.Context,
	ss store.Store,
	academicUnitID string,
	name string,
) *model.Programme {
	t.Helper()
	programme, err := ss.Programme().Save(ctx, &model.Programme{
		AcademicUnitId: academicUnitID,
		Name:           name,
		DisplayName:    name,
	})
	requireNoError(t, err)
	return programme
}
