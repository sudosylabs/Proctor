// ---------------------------------------------------------------------------------------------
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
//
// Adapted from Mattermost server/channels/store/storetest/team_store.go. The
// suite receives the root store and verifies each ProgrammeStore operation so
// every future persistence adapter can reuse the same contract tests.

package storetest

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestProgrammeStore(t *testing.T, ss store.Store) {
	t.Run("SearchLimit", func(t *testing.T) { testProgrammeStoreSearchLimit(t, ss) })
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
	createAttempt := saveProgrammeAuditAttempt(t, ctx, ss, unit.ID.String())
	candidate := &model.Programme{AcademicUnitID: unit.ID, Name: "audited-programme", DisplayName: "Audited Programme"}
	candidate.PrepareCreate(model.ProgrammeID(model.NewID()), model.NowUTC())
	created, err := ss.Programme().Create(ctx, &store.ProgrammeCreation{Programme: candidate, AuditEventID: createAttempt.ID, AuditAt: model.GetMillis()})
	require.NoError(t, err)
	completed, err := ss.Audit().Get(ctx, createAttempt.ID)
	require.NoError(t, err)
	require.Equal(t, model.AuditStatusSuccess, completed.Status)
	rolledBackCreate := &model.Programme{AcademicUnitID: unit.ID, Name: "rolled-back-create", DisplayName: "Rolled Back Create"}
	rolledBackCreate.PrepareCreate(model.ProgrammeID(model.NewID()), model.NowUTC())
	_, err = ss.Programme().Create(ctx, &store.ProgrammeCreation{Programme: rolledBackCreate, AuditEventID: model.NewAuditEventID(), AuditAt: model.GetMillis()})
	require.Error(t, err, "Create() succeeded without its audit attempt")
	_, err = ss.Programme().Get(ctx, rolledBackCreate.ID)
	require.True(t, store.IsNotFound(err), "create survived audit rollback")

	updateAttempt := saveProgrammeAuditAttempt(t, ctx, ss, unit.ID.String())
	updatedCandidate := *created
	updatedCandidate.DisplayName = "Updated Programme"
	updatedCandidate.PrepareUpdate(model.NowUTC())
	updated, err := ss.Programme().Update(ctx, &store.ProgrammeUpdate{Programme: &updatedCandidate, AuditEventID: updateAttempt.ID, AuditAt: model.GetMillis()})
	require.NoError(t, err)
	completed, err = ss.Audit().Get(ctx, updateAttempt.ID)
	require.NoError(t, err)
	require.Equal(t, model.AuditStatusSuccess, completed.Status)
	staleAttempt := saveProgrammeAuditAttempt(t, ctx, ss, unit.ID.String())
	stale := *created
	stale.DisplayName = "Stale Programme"
	stale.PrepareUpdate(model.NowUTC())
	_, err = ss.Programme().Update(ctx, &store.ProgrammeUpdate{Programme: &stale, AuditEventID: staleAttempt.ID, AuditAt: model.GetMillis()})
	require.True(t, store.IsConflict(err), "stale Update() error")

	rolledBack := *updated
	rolledBack.DisplayName = "Must Roll Back"
	rolledBack.PrepareUpdate(model.NowUTC())
	_, err = ss.Programme().Update(ctx, &store.ProgrammeUpdate{Programme: &rolledBack, AuditEventID: model.NewAuditEventID(), AuditAt: model.GetMillis()})
	require.Error(t, err, "Update() succeeded without its audit attempt")
	persisted, err := ss.Programme().Get(ctx, updated.ID)
	require.NoError(t, err)
	require.Equal(t, updated.DisplayName, persisted.DisplayName)

	archiveAttempt := saveProgrammeAuditAttempt(t, ctx, ss, unit.ID.String())
	archived, err := ss.Programme().Archive(ctx, &store.ProgrammeArchive{ID: updated.ID, ArchiveAt: model.GetMillis(), AuditEventID: archiveAttempt.ID, AuditAt: model.GetMillis()})
	require.NoError(t, err)
	require.NotEqual(t, int64(0), archived.ArchivedAt.Millis())
	completed, err = ss.Audit().Get(ctx, archiveAttempt.ID)
	require.NoError(t, err)
	require.Equal(t, model.AuditStatusSuccess, completed.Status)

	archiveRollback := saveProgramme(t, ctx, ss, unit.ID.String(), "rolled-back-archive")
	_, err = ss.Programme().Archive(ctx, &store.ProgrammeArchive{ID: archiveRollback.ID, ArchiveAt: model.GetMillis(), AuditEventID: model.NewAuditEventID(), AuditAt: model.GetMillis()})
	require.Error(t, err, "Archive() succeeded without its audit attempt")
	_, err = ss.Programme().Get(ctx, archiveRollback.ID)
	require.NoError(t, err)

	withLevel := saveProgramme(t, ctx, ss, unit.ID.String(), "programme-with-level")
	saveProgrammeLevel(t, ctx, ss, withLevel.ID.String(), "year-1")
	blockedAttempt := saveProgrammeAuditAttempt(t, ctx, ss, unit.ID.String())
	_, err = ss.Programme().Archive(ctx, &store.ProgrammeArchive{ID: withLevel.ID, ArchiveAt: model.GetMillis(), AuditEventID: blockedAttempt.ID, AuditAt: model.GetMillis()})
	require.True(t, store.IsConflict(err), "archive with active level error")
}

func saveProgrammeAuditAttempt(t *testing.T, ctx context.Context, ss store.Store, unitID string) *model.AuditEvent {
	t.Helper()
	attempt, err := ss.Audit().Save(ctx, &model.AuditEvent{
		Action:    string(model.ActionAcademicUnitManage),
		Resource:  model.Resource{Type: model.ResourceAcademicUnit, ID: unitID},
		ScopeType: model.RoleScopeAcademicUnit, ScopeID: unitID,
		Status: model.AuditStatusAttempt, NodeID: "test-node",
	})
	require.NoError(t, err)
	return attempt
}

func testProgrammeStoreSearchAndArchive(t *testing.T, ss store.Store) {
	ctx := context.Background()
	unit, _ := saveProgrammeParents(t, ctx, ss, "archive-programme-parent")
	programme := saveProgramme(t, ctx, ss, unit.ID.String(), "distinct-robotics")
	programme.DisplayName = "Literal 50%_! Robotics"
	programme, err := UpdateProgramme(ctx, ss, programme)
	require.NoError(t, err)
	found, err := ss.Programme().SearchByAcademicUnit(ctx, unit.ID, "robot", 10)
	require.NoError(t, err)
	require.Len(t, found, 1)
	require.Equal(t, programme.ID, found[0].ID)
	for _, query := range []string{"LITERAL 50%_!", "%", "_", "!"} {
		found, err = ss.Programme().SearchByAcademicUnit(ctx, unit.ID, query, 10)
		require.NoError(t, err)
		require.Len(t, found, 1, "SearchByAcademicUnit(%q)", query)
		require.Equal(t, programme.ID, found[0].ID, "SearchByAcademicUnit(%q)", query)
	}
	archived, err := ArchiveProgramme(ctx, ss, programme.ID, model.GetMillis())
	require.NoError(t, err)
	require.NotEqual(t, int64(0), archived.ArchivedAt.Millis())
}

func testProgrammeStoreSave(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	unit := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "computing")
	programme := &model.Programme{
		AcademicUnitID: unit.ID,
		Name:           "computer-science",
		DisplayName:    "Computer Science",
		Description:    "A course of study",
	}

	saved, err := SaveProgramme(ctx, ss, programme)
	require.NoError(t, err)
	require.True(t, saved.ID.IsValid(), "Save() id = %q", saved.ID.String())
	require.True(t, programme.ID.IsZero(), "Save() mutated input id to %q", programme.ID.String())

	_, err = SaveProgramme(ctx, ss, saved)
	var invalid *store.ErrInvalidInput
	require.ErrorAs(t, err, &invalid, "second Save(saved) error")
}

func testProgrammeStoreGet(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	unit := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "computing")
	programme := saveProgramme(t, ctx, ss, unit.ID.String(), "computer-science")

	got, err := ss.Programme().Get(ctx, programme.ID)
	require.NoError(t, err)
	require.Equal(t, *programme, *got, "Get() = %#v, want %#v", got, programme)
	_, err = ss.Programme().Get(ctx, model.NewProgrammeID())
	require.True(t, store.IsNotFound(err), "Get(missing) error")
}

func testProgrammeStoreGetByName(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	unit := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "computing")
	programme := saveProgramme(t, ctx, ss, unit.ID.String(), "computer-science")

	got, err := ss.Programme().GetByName(ctx, unit.ID, programme.Name)
	require.NoError(t, err)
	require.Equal(t, programme.ID, got.ID, "GetByName() id = %q, want %q", got.ID.String(), programme.ID.String())
	_, err = ss.Programme().GetByName(ctx, unit.ID, "missing")
	require.True(t, store.IsNotFound(err), "GetByName(missing) error")
}

func testProgrammeStoreListByAcademicUnit(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	computing := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "computing")
	engineering := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "engineering")
	second := saveProgramme(t, ctx, ss, computing.ID.String(), "software-engineering")
	first := saveProgramme(t, ctx, ss, computing.ID.String(), "computer-science")
	saveProgramme(t, ctx, ss, engineering.ID.String(), "civil-engineering")

	programmes, err := ss.Programme().ListByAcademicUnit(ctx, computing.ID)
	require.NoError(t, err)
	require.Len(t, programmes, 2)
	require.Equal(t, first.ID, programmes[0].ID)
	require.Equal(t, second.ID, programmes[1].ID)
	empty, err := ss.Programme().ListByAcademicUnit(ctx, model.NewAcademicUnitID())
	require.NoError(t, err)
	require.Empty(t, empty, "ListByAcademicUnit(missing)")
}

func testProgrammeStoreUpdate(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	computing := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "computing")
	engineering := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "engineering")
	programme := saveProgramme(t, ctx, ss, computing.ID.String(), "computer-science")
	createAt := programme.CreatedAt

	programme.AcademicUnitID = engineering.ID
	programme.Name = "computing-engineering"
	programme.DisplayName = "Computing Engineering"
	updated, err := ss.Programme().SeedUpdate(ctx, programme)
	require.NoError(t, err)
	require.Equal(t, engineering.ID, updated.AcademicUnitID)
	require.Equal(t, "computing-engineering", updated.Name)
	require.True(t, updated.CreatedAt.Equal(createAt))
	require.False(t, updated.UpdatedAt.Before(programme.UpdatedAt))

	missing := *updated
	missing.ID = model.ProgrammeID(model.NewID())
	_, err = UpdateProgramme(ctx, ss, &missing)
	require.True(t, store.IsNotFound(err), "Update(missing) error")

	archivedUnit := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "archived-unit")
	_, err = ArchiveAcademicUnit(ctx, ss, archivedUnit.ID, model.GetMillis())
	require.NoError(t, err)
	invalidMove := *updated
	invalidMove.AcademicUnitID = archivedUnit.ID
	_, err = UpdateProgramme(ctx, ss, &invalidMove)
	var reference *store.ErrReference
	require.ErrorAs(t, err, &reference, "Update(archived academic unit) error")
}

func testProgrammeStoreRejectUnknownAcademicUnit(t *testing.T, ss store.Store) {
	ctx := context.Background()
	saveInstitution(t, ctx, ss)

	_, err := SaveProgramme(ctx, ss, &model.Programme{
		AcademicUnitID: model.AcademicUnitID(model.NewID()),
		Name:           "computer-science",
		DisplayName:    "Computer Science",
	})
	var reference *store.ErrReference
	require.ErrorAs(t, err, &reference, "Save(unknown academic unit) error")
	require.Equal(t, "programmes_academic_unit_id_fkey", reference.Constraint, "Save(unknown academic unit) error")
}

func testProgrammeStoreEnforceAcademicUnitNameUniqueness(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	computing := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "computing")
	engineering := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "engineering")
	saveProgramme(t, ctx, ss, computing.ID.String(), "computer-science")

	_, err := SaveProgramme(ctx, ss, &model.Programme{
		AcademicUnitID: computing.ID,
		Name:           "computer-science",
		DisplayName:    "Duplicate",
	})
	var conflict *store.ErrConflict
	require.ErrorAs(t, err, &conflict, "duplicate programme error")
	require.Equal(t, "programmes_academic_unit_id_name_key", conflict.Constraint, "duplicate programme error")

	_, err = SaveProgramme(ctx, ss, &model.Programme{
		AcademicUnitID: engineering.ID,
		Name:           "computer-science",
		DisplayName:    "Computer Science",
	})
	require.NoError(t, err)
}

func saveProgramme(
	t *testing.T,
	ctx context.Context,
	ss store.Store,
	academicUnitID string,
	name string,
) *model.Programme {
	t.Helper()
	programme, err := SaveProgramme(ctx, ss, &model.Programme{
		AcademicUnitID: model.AcademicUnitID(academicUnitID),
		Name:           name,
		DisplayName:    name,
	})
	require.NoError(t, err)
	return programme
}

func testProgrammeStoreSearchLimit(t *testing.T, ss store.Store) {
	ctx := context.Background()
	unit, _ := saveProgrammeParents(t, ctx, ss, "unrelated")
	first := saveProgramme(t, ctx, ss, unit.ID.String(), "bounded-first")
	saveProgramme(t, ctx, ss, unit.ID.String(), "bounded-second")
	for _, term := range []string{"", "  \t", "bounded"} {
		found, err := ss.Programme().SearchByAcademicUnit(ctx, unit.ID, term, 1)
		require.NoError(t, err)
		require.Len(t, found, 1, "SearchByAcademicUnit(q=%q, limit=1)", term)
		require.Equal(t, first.ID, found[0].ID, "SearchByAcademicUnit(q=%q, limit=1)", term)
	}
}
