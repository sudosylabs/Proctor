// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/channels/store/storetest/team_store.go. The
// suite receives the root store and verifies each ProgrammeLevelStore
// operation so every future persistence adapter can reuse the same tests.

package storetest

import (
	"context"
	"errors"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestProgrammeLevelStore(t *testing.T, ss store.Store) {
	t.Run("MutationAuditAtomicity", func(t *testing.T) { testProgrammeLevelStoreMutationAuditAtomicity(t, ss) })
	t.Run("Save", func(t *testing.T) { testProgrammeLevelStoreSave(t, ss) })
	t.Run("Get", func(t *testing.T) { testProgrammeLevelStoreGet(t, ss) })
	t.Run("GetByName", func(t *testing.T) { testProgrammeLevelStoreGetByName(t, ss) })
	t.Run("ListByProgramme", func(t *testing.T) { testProgrammeLevelStoreListByProgramme(t, ss) })
	t.Run("Update", func(t *testing.T) { testProgrammeLevelStoreUpdate(t, ss) })
	t.Run("RejectUnknownProgramme", func(t *testing.T) {
		testProgrammeLevelStoreRejectUnknownProgramme(t, ss)
	})
	t.Run("EnforceProgrammeNameUniqueness", func(t *testing.T) {
		testProgrammeLevelStoreEnforceProgrammeNameUniqueness(t, ss)
	})
	t.Run("SearchAndArchive", func(t *testing.T) {
		testProgrammeLevelStoreSearchAndArchive(t, ss)
	})
}

func testProgrammeLevelStoreMutationAuditAtomicity(t *testing.T, ss store.Store) {
	ctx := context.Background()
	unit, programme := saveProgrammeParents(t, ctx, ss, "audited-level-programme")
	createAttempt := saveProgrammeLevelAuditAttempt(t, ctx, ss, unit.ID.String())
	candidate := &model.ProgrammeLevel{ProgrammeId: programme.Id, Name: "audited-level", DisplayName: "Audited Level"}
	candidate.PrepareCreate(model.NewId(), model.GetMillis())
	created, err := ss.ProgrammeLevel().Create(ctx, &store.ProgrammeLevelCreation{Level: candidate, AuditEventID: createAttempt.Id, AuditAt: model.GetMillis()})
	requireNoError(t, err)
	completed, err := ss.Audit().Get(ctx, createAttempt.Id)
	requireNoError(t, err)
	if completed.Status != model.AuditStatusSuccess {
		t.Fatalf("create audit status = %q", completed.Status)
	}

	rolledBackCreate := &model.ProgrammeLevel{ProgrammeId: programme.Id, Name: "rolled-back-create", DisplayName: "Rolled Back Create"}
	rolledBackCreate.PrepareCreate(model.NewId(), model.GetMillis())
	if _, err := ss.ProgrammeLevel().Create(ctx, &store.ProgrammeLevelCreation{Level: rolledBackCreate, AuditEventID: model.NewId(), AuditAt: model.GetMillis()}); err == nil {
		t.Fatal("Create() succeeded without its audit attempt")
	}
	if _, err := ss.ProgrammeLevel().Get(ctx, rolledBackCreate.Id); !store.IsNotFound(err) {
		t.Fatalf("create survived audit rollback: %v", err)
	}

	updateAttempt := saveProgrammeLevelAuditAttempt(t, ctx, ss, unit.ID.String())
	updatedCandidate := *created
	updatedCandidate.DisplayName = "Updated Level"
	updatedCandidate.PrepareUpdate(model.GetMillis())
	updated, err := ss.ProgrammeLevel().UpdateWithAudit(ctx, &store.ProgrammeLevelUpdate{Level: &updatedCandidate, AuditEventID: updateAttempt.Id, AuditAt: model.GetMillis()})
	requireNoError(t, err)
	completed, err = ss.Audit().Get(ctx, updateAttempt.Id)
	requireNoError(t, err)
	if completed.Status != model.AuditStatusSuccess {
		t.Fatalf("update audit status = %q", completed.Status)
	}

	rolledBack := *updated
	rolledBack.DisplayName = "Must Roll Back"
	rolledBack.PrepareUpdate(model.GetMillis())
	if _, err := ss.ProgrammeLevel().UpdateWithAudit(ctx, &store.ProgrammeLevelUpdate{Level: &rolledBack, AuditEventID: model.NewId(), AuditAt: model.GetMillis()}); err == nil {
		t.Fatal("UpdateWithAudit() succeeded without its audit attempt")
	}
	persisted, err := ss.ProgrammeLevel().Get(ctx, updated.Id)
	requireNoError(t, err)
	if persisted.DisplayName != updated.DisplayName {
		t.Fatalf("update survived audit rollback: %#v", persisted)
	}

	archiveAttempt := saveProgrammeLevelAuditAttempt(t, ctx, ss, unit.ID.String())
	archived, err := ss.ProgrammeLevel().ArchiveWithAudit(ctx, &store.ProgrammeLevelArchive{ID: updated.Id, ArchiveAt: model.GetMillis(), AuditEventID: archiveAttempt.Id, AuditAt: model.GetMillis()})
	requireNoError(t, err)
	if archived.DeleteAt == 0 {
		t.Fatalf("ArchiveWithAudit() = %#v", archived)
	}

	archiveRollback := saveProgrammeLevel(t, ctx, ss, programme.Id, "rolled-back-archive")
	if _, err := ss.ProgrammeLevel().ArchiveWithAudit(ctx, &store.ProgrammeLevelArchive{ID: archiveRollback.Id, ArchiveAt: model.GetMillis(), AuditEventID: model.NewId(), AuditAt: model.GetMillis()}); err == nil {
		t.Fatal("ArchiveWithAudit() succeeded without its audit attempt")
	}
	if _, err := ss.ProgrammeLevel().Get(ctx, archiveRollback.Id); err != nil {
		t.Fatalf("archive survived audit rollback: %v", err)
	}

	withClass := saveProgrammeLevel(t, ctx, ss, programme.Id, "level-with-class")
	period := saveAcademicPeriod(t, ctx, ss, unit.InstitutionID.String(), "2026-2027", 1_800_000_000_000)
	saveClass(t, ctx, ss, withClass.Id, period.Id, "class-a")
	blockedAttempt := saveProgrammeLevelAuditAttempt(t, ctx, ss, unit.ID.String())
	if _, err := ss.ProgrammeLevel().ArchiveWithAudit(ctx, &store.ProgrammeLevelArchive{ID: withClass.Id, ArchiveAt: model.GetMillis(), AuditEventID: blockedAttempt.Id, AuditAt: model.GetMillis()}); !store.IsConflict(err) {
		t.Fatalf("archive with active class error = %v, want conflict", err)
	}
}

func saveProgrammeLevelAuditAttempt(t *testing.T, ctx context.Context, ss store.Store, unitID string) *model.AuditEvent {
	t.Helper()
	attempt, err := ss.Audit().Save(ctx, &model.AuditEvent{Action: string(model.ActionAcademicUnitManage), Resource: model.Resource{Type: model.ResourceAcademicUnit, Id: unitID}, ScopeType: model.RoleScopeAcademicUnit, ScopeId: unitID, Status: model.AuditStatusAttempt, NodeId: "test-node"})
	requireNoError(t, err)
	return attempt
}

func testProgrammeLevelStoreSearchAndArchive(t *testing.T, ss store.Store) {
	ctx := context.Background()
	_, programme := saveProgrammeParents(t, ctx, ss, "archive-level-programme")
	level := saveProgrammeLevel(t, ctx, ss, programme.Id, "distinct-foundation")
	found, err := ss.ProgrammeLevel().SearchByProgramme(ctx, programme.Id, "foundation", 10)
	requireNoError(t, err)
	if len(found) != 1 || found[0].Id != level.Id {
		t.Fatalf("SearchByProgramme() = %#v", found)
	}
	archived, err := ss.ProgrammeLevel().Delete(ctx, level.Id, model.GetMillis())
	requireNoError(t, err)
	if archived.DeleteAt == 0 {
		t.Fatalf("Delete() = %#v", archived)
	}
}

func testProgrammeLevelStoreSave(t *testing.T, ss store.Store) {
	ctx := context.Background()
	_, programme := saveProgrammeParents(t, ctx, ss, "computer-science")
	level := &model.ProgrammeLevel{
		ProgrammeId: programme.Id,
		Name:        "year-1",
		DisplayName: "Year 1",
		Description: "First curriculum stage",
	}

	saved, err := ss.ProgrammeLevel().Save(ctx, level)
	requireNoError(t, err)
	if !model.IsValidId(saved.Id) {
		t.Fatalf("Save() id = %q", saved.Id)
	}
	if level.Id != "" {
		t.Fatalf("Save() mutated input id to %q", level.Id)
	}

	_, err = ss.ProgrammeLevel().Save(ctx, saved)
	var invalid *store.ErrInvalidInput
	if !errors.As(err, &invalid) {
		t.Fatalf("second Save(saved) error = %v, want invalid input", err)
	}
}

func testProgrammeLevelStoreGet(t *testing.T, ss store.Store) {
	ctx := context.Background()
	_, programme := saveProgrammeParents(t, ctx, ss, "computer-science")
	level := saveProgrammeLevel(t, ctx, ss, programme.Id, "year-1")

	got, err := ss.ProgrammeLevel().Get(ctx, level.Id)
	requireNoError(t, err)
	if *got != *level {
		t.Fatalf("Get() = %#v, want %#v", got, level)
	}
	if _, err := ss.ProgrammeLevel().Get(ctx, model.NewId()); !store.IsNotFound(err) {
		t.Fatalf("Get(missing) error = %v, want not found", err)
	}
}

func testProgrammeLevelStoreGetByName(t *testing.T, ss store.Store) {
	ctx := context.Background()
	_, programme := saveProgrammeParents(t, ctx, ss, "computer-science")
	level := saveProgrammeLevel(t, ctx, ss, programme.Id, "year-1")

	got, err := ss.ProgrammeLevel().GetByName(ctx, programme.Id, level.Name)
	requireNoError(t, err)
	if got.Id != level.Id {
		t.Fatalf("GetByName() id = %q, want %q", got.Id, level.Id)
	}
	if _, err := ss.ProgrammeLevel().GetByName(ctx, programme.Id, "missing"); !store.IsNotFound(err) {
		t.Fatalf("GetByName(missing) error = %v, want not found", err)
	}
}

func testProgrammeLevelStoreListByProgramme(t *testing.T, ss store.Store) {
	ctx := context.Background()
	unit, computing := saveProgrammeParents(t, ctx, ss, "computer-science")
	engineering := saveProgramme(t, ctx, ss, unit.ID.String(), "software-engineering")
	second := saveProgrammeLevel(t, ctx, ss, computing.Id, "year-2")
	first := saveProgrammeLevel(t, ctx, ss, computing.Id, "year-1")
	saveProgrammeLevel(t, ctx, ss, engineering.Id, "year-1")

	levels, err := ss.ProgrammeLevel().ListByProgramme(ctx, computing.Id)
	requireNoError(t, err)
	if len(levels) != 2 || levels[0].Id != first.Id || levels[1].Id != second.Id {
		t.Fatalf("ListByProgramme() = %#v", levels)
	}
	empty, err := ss.ProgrammeLevel().ListByProgramme(ctx, model.NewId())
	requireNoError(t, err)
	if len(empty) != 0 {
		t.Fatalf("ListByProgramme(missing) = %#v, want empty", empty)
	}
}

func testProgrammeLevelStoreUpdate(t *testing.T, ss store.Store) {
	ctx := context.Background()
	unit, computing := saveProgrammeParents(t, ctx, ss, "computer-science")
	engineering := saveProgramme(t, ctx, ss, unit.ID.String(), "software-engineering")
	level := saveProgrammeLevel(t, ctx, ss, computing.Id, "year-1")
	createAt := level.CreateAt

	level.ProgrammeId = engineering.Id
	level.Name = "foundation"
	level.DisplayName = "Foundation"
	updated, err := ss.ProgrammeLevel().Update(ctx, level)
	requireNoError(t, err)
	if updated.ProgrammeId != engineering.Id || updated.Name != "foundation" {
		t.Fatalf("Update() = %#v", updated)
	}
	if updated.CreateAt != createAt || updated.UpdateAt < level.UpdateAt {
		t.Fatalf("Update() timestamps = %#v", updated)
	}

	missing := *updated
	missing.Id = model.NewId()
	_, err = ss.ProgrammeLevel().Update(ctx, &missing)
	if !store.IsNotFound(err) {
		t.Fatalf("Update(missing) error = %v, want not found", err)
	}
	archivedProgramme := saveProgramme(t, ctx, ss, unit.ID.String(), "archived-programme")
	if _, err := ss.Programme().Delete(ctx, archivedProgramme.Id, model.GetMillis()); err != nil {
		t.Fatalf("archive destination programme: %v", err)
	}
	invalidMove := *updated
	invalidMove.ProgrammeId = archivedProgramme.Id
	_, err = ss.ProgrammeLevel().Update(ctx, &invalidMove)
	var reference *store.ErrReference
	if !errors.As(err, &reference) {
		t.Fatalf("Update(archived programme) error = %v, want reference", err)
	}
}

func testProgrammeLevelStoreRejectUnknownProgramme(t *testing.T, ss store.Store) {
	ctx := context.Background()
	saveInstitution(t, ctx, ss)

	_, err := ss.ProgrammeLevel().Save(ctx, &model.ProgrammeLevel{
		ProgrammeId: model.NewId(),
		Name:        "year-1",
		DisplayName: "Year 1",
	})
	var reference *store.ErrReference
	if !errors.As(err, &reference) ||
		reference.Constraint != "programme_levels_programme_id_fkey" {
		t.Fatalf("Save(unknown programme) error = %v, want reference error", err)
	}
}

func testProgrammeLevelStoreEnforceProgrammeNameUniqueness(t *testing.T, ss store.Store) {
	ctx := context.Background()
	unit, computing := saveProgrammeParents(t, ctx, ss, "computer-science")
	engineering := saveProgramme(t, ctx, ss, unit.ID.String(), "software-engineering")
	saveProgrammeLevel(t, ctx, ss, computing.Id, "year-1")

	_, err := ss.ProgrammeLevel().Save(ctx, &model.ProgrammeLevel{
		ProgrammeId: computing.Id,
		Name:        "year-1",
		DisplayName: "Duplicate",
	})
	var conflict *store.ErrConflict
	if !errors.As(err, &conflict) ||
		conflict.Constraint != "programme_levels_active_name_key" {
		t.Fatalf("duplicate programme level error = %v, want scoped-name conflict", err)
	}

	if _, err := ss.ProgrammeLevel().Save(ctx, &model.ProgrammeLevel{
		ProgrammeId: engineering.Id,
		Name:        "year-1",
		DisplayName: "Year 1",
	}); err != nil {
		t.Fatalf("same name in another programme error = %v", err)
	}
}

func saveProgrammeParents(
	t *testing.T,
	ctx context.Context,
	ss store.Store,
	programmeName string,
) (*model.AcademicUnit, *model.Programme) {
	t.Helper()
	institution := saveInstitution(t, ctx, ss)
	unit := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "computing")
	return unit, saveProgramme(t, ctx, ss, unit.ID.String(), programmeName)
}

func saveProgrammeLevel(
	t *testing.T,
	ctx context.Context,
	ss store.Store,
	programmeID string,
	name string,
) *model.ProgrammeLevel {
	t.Helper()
	level, err := ss.ProgrammeLevel().Save(ctx, &model.ProgrammeLevel{
		ProgrammeId: programmeID,
		Name:        name,
		DisplayName: name,
	})
	requireNoError(t, err)
	return level
}
