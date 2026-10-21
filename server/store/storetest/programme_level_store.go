// ---------------------------------------------------------------------------------------------
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
//
// Adapted from Mattermost server/channels/store/storetest/team_store.go. The
// suite receives the root store and verifies each ProgrammeLevelStore
// operation so every future persistence adapter can reuse the same tests.

package storetest

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestProgrammeLevelStore(t *testing.T, ss store.Store) {
	t.Run("SearchLimit", func(t *testing.T) { testProgrammeLevelStoreSearchLimit(t, ss) })
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
	candidate := &model.ProgrammeLevel{ProgrammeID: programme.ID, Name: "audited-level", DisplayName: "Audited Level"}
	candidate.PrepareCreate(model.ProgrammeLevelID(model.NewID()), model.NowUTC())
	created, err := ss.ProgrammeLevel().Create(ctx, &store.ProgrammeLevelCreation{Level: candidate, AuditEventID: createAttempt.ID, AuditAt: model.GetMillis()})
	require.NoError(t, err)
	completed, err := ss.Audit().Get(ctx, createAttempt.ID)
	require.NoError(t, err)
	require.Equal(t, model.AuditStatusSuccess, completed.Status)

	rolledBackCreate := &model.ProgrammeLevel{ProgrammeID: programme.ID, Name: "rolled-back-create", DisplayName: "Rolled Back Create"}
	rolledBackCreate.PrepareCreate(model.ProgrammeLevelID(model.NewID()), model.NowUTC())
	_, err = ss.ProgrammeLevel().Create(ctx, &store.ProgrammeLevelCreation{Level: rolledBackCreate, AuditEventID: model.NewAuditEventID(), AuditAt: model.GetMillis()})
	require.Error(t, err, "Create() succeeded without its audit attempt")
	_, err = ss.ProgrammeLevel().Get(ctx, rolledBackCreate.ID)
	require.True(t, store.IsNotFound(err), "create survived audit rollback")

	updateAttempt := saveProgrammeLevelAuditAttempt(t, ctx, ss, unit.ID.String())
	updatedCandidate := *created
	updatedCandidate.DisplayName = "Updated Level"
	updatedCandidate.PrepareUpdate(model.NowUTC())
	updated, err := ss.ProgrammeLevel().Update(ctx, &store.ProgrammeLevelUpdate{Level: &updatedCandidate, AuditEventID: updateAttempt.ID, AuditAt: model.GetMillis()})
	require.NoError(t, err)
	completed, err = ss.Audit().Get(ctx, updateAttempt.ID)
	require.NoError(t, err)
	require.Equal(t, model.AuditStatusSuccess, completed.Status)
	staleAttempt := saveProgrammeLevelAuditAttempt(t, ctx, ss, unit.ID.String())
	stale := *created
	stale.DisplayName = "Stale Level"
	stale.PrepareUpdate(model.NowUTC())
	_, err = ss.ProgrammeLevel().Update(ctx, &store.ProgrammeLevelUpdate{Level: &stale, AuditEventID: staleAttempt.ID, AuditAt: model.GetMillis()})
	require.True(t, store.IsConflict(err), "stale Update() error")

	rolledBack := *updated
	rolledBack.DisplayName = "Must Roll Back"
	rolledBack.PrepareUpdate(model.NowUTC())
	_, err = ss.ProgrammeLevel().Update(ctx, &store.ProgrammeLevelUpdate{Level: &rolledBack, AuditEventID: model.NewAuditEventID(), AuditAt: model.GetMillis()})
	require.Error(t, err, "Update() succeeded without its audit attempt")
	persisted, err := ss.ProgrammeLevel().Get(ctx, updated.ID)
	require.NoError(t, err)
	require.Equal(t, updated.DisplayName, persisted.DisplayName)

	archiveAttempt := saveProgrammeLevelAuditAttempt(t, ctx, ss, unit.ID.String())
	archived, err := ss.ProgrammeLevel().Archive(ctx, &store.ProgrammeLevelArchive{ID: updated.ID, ArchiveAt: model.GetMillis(), AuditEventID: archiveAttempt.ID, AuditAt: model.GetMillis()})
	require.NoError(t, err)
	require.NotEqual(t, int64(0), archived.ArchivedAt.Millis())

	archiveRollback := saveProgrammeLevel(t, ctx, ss, programme.ID.String(), "rolled-back-archive")
	_, err = ss.ProgrammeLevel().Archive(ctx, &store.ProgrammeLevelArchive{ID: archiveRollback.ID, ArchiveAt: model.GetMillis(), AuditEventID: model.NewAuditEventID(), AuditAt: model.GetMillis()})
	require.Error(t, err, "Archive() succeeded without its audit attempt")
	_, err = ss.ProgrammeLevel().Get(ctx, archiveRollback.ID)
	require.NoError(t, err)

	withClass := saveProgrammeLevel(t, ctx, ss, programme.ID.String(), "level-with-class")
	period := saveAcademicPeriod(t, ctx, ss, unit.InstitutionID.String(), "2026-2027", 1_800_000_000_000)
	saveClass(t, ctx, ss, withClass.ID.String(), period.ID.String(), "class-a")
	blockedAttempt := saveProgrammeLevelAuditAttempt(t, ctx, ss, unit.ID.String())
	_, err = ss.ProgrammeLevel().Archive(ctx, &store.ProgrammeLevelArchive{ID: withClass.ID, ArchiveAt: model.GetMillis(), AuditEventID: blockedAttempt.ID, AuditAt: model.GetMillis()})
	require.True(t, store.IsConflict(err), "archive with active class error")
}

func saveProgrammeLevelAuditAttempt(t *testing.T, ctx context.Context, ss store.Store, unitID string) *model.AuditEvent {
	t.Helper()
	attempt, err := ss.Audit().Save(ctx, &model.AuditEvent{Action: string(model.ActionAcademicUnitManage), Resource: model.Resource{Type: model.ResourceAcademicUnit, ID: unitID}, ScopeType: model.RoleScopeAcademicUnit, ScopeID: unitID, Status: model.AuditStatusAttempt, NodeID: "test-node"})
	require.NoError(t, err)
	return attempt
}

func testProgrammeLevelStoreSearchAndArchive(t *testing.T, ss store.Store) {
	ctx := context.Background()
	_, programme := saveProgrammeParents(t, ctx, ss, "archive-level-programme")
	level := saveProgrammeLevel(t, ctx, ss, programme.ID.String(), "distinct-foundation")
	level.DisplayName = "Literal 50%_! Foundation"
	level, err := UpdateProgrammeLevel(ctx, ss, level)
	require.NoError(t, err)
	found, err := ss.ProgrammeLevel().SearchByProgramme(ctx, programme.ID, "foundation", 10)
	require.NoError(t, err)
	require.Len(t, found, 1)
	require.Equal(t, level.ID, found[0].ID)
	for _, query := range []string{"LITERAL 50%_!", "%", "_", "!"} {
		found, err = ss.ProgrammeLevel().SearchByProgramme(ctx, programme.ID, query, 10)
		require.NoError(t, err)
		require.Len(t, found, 1, "SearchByProgramme(%q)", query)
		require.Equal(t, level.ID, found[0].ID, "SearchByProgramme(%q)", query)
	}
	archived, err := ArchiveProgrammeLevel(ctx, ss, level.ID, model.GetMillis())
	require.NoError(t, err)
	require.NotEqual(t, int64(0), archived.ArchivedAt.Millis())
}

func testProgrammeLevelStoreSave(t *testing.T, ss store.Store) {
	ctx := context.Background()
	_, programme := saveProgrammeParents(t, ctx, ss, "computer-science")
	level := &model.ProgrammeLevel{
		ProgrammeID: programme.ID,
		Name:        "year-1",
		DisplayName: "Year 1",
		Description: "First curriculum stage",
	}

	saved, err := SaveProgrammeLevel(ctx, ss, level)
	require.NoError(t, err)
	require.True(t, saved.ID.IsValid(), "Save() id = %q", saved.ID.String())
	require.True(t, level.ID.IsZero(), "Save() mutated input id to %q", level.ID.String())

	_, err = SaveProgrammeLevel(ctx, ss, saved)
	var invalid *store.ErrInvalidInput
	require.ErrorAs(t, err, &invalid, "second Save(saved) error")
}

func testProgrammeLevelStoreGet(t *testing.T, ss store.Store) {
	ctx := context.Background()
	_, programme := saveProgrammeParents(t, ctx, ss, "computer-science")
	level := saveProgrammeLevel(t, ctx, ss, programme.ID.String(), "year-1")

	got, err := ss.ProgrammeLevel().Get(ctx, level.ID)
	require.NoError(t, err)
	require.Equal(t, *level, *got, "Get() = %#v, want %#v", got, level)
	_, err = ss.ProgrammeLevel().Get(ctx, model.NewProgrammeLevelID())
	require.True(t, store.IsNotFound(err), "Get(missing) error")
}

func testProgrammeLevelStoreGetByName(t *testing.T, ss store.Store) {
	ctx := context.Background()
	_, programme := saveProgrammeParents(t, ctx, ss, "computer-science")
	level := saveProgrammeLevel(t, ctx, ss, programme.ID.String(), "year-1")

	got, err := ss.ProgrammeLevel().GetByName(ctx, programme.ID, level.Name)
	require.NoError(t, err)
	require.Equal(t, level.ID, got.ID, "GetByName() id = %q, want %q", got.ID.String(), level.ID.String())
	_, err = ss.ProgrammeLevel().GetByName(ctx, programme.ID, "missing")
	require.True(t, store.IsNotFound(err), "GetByName(missing) error")
}

func testProgrammeLevelStoreListByProgramme(t *testing.T, ss store.Store) {
	ctx := context.Background()
	unit, computing := saveProgrammeParents(t, ctx, ss, "computer-science")
	engineering := saveProgramme(t, ctx, ss, unit.ID.String(), "software-engineering")
	second := saveProgrammeLevel(t, ctx, ss, computing.ID.String(), "year-2")
	first := saveProgrammeLevel(t, ctx, ss, computing.ID.String(), "year-1")
	saveProgrammeLevel(t, ctx, ss, engineering.ID.String(), "year-1")

	levels, err := ss.ProgrammeLevel().ListByProgramme(ctx, computing.ID)
	require.NoError(t, err)
	require.Len(t, levels, 2)
	require.Equal(t, first.ID, levels[0].ID)
	require.Equal(t, second.ID, levels[1].ID)
	empty, err := ss.ProgrammeLevel().ListByProgramme(ctx, model.NewProgrammeID())
	require.NoError(t, err)
	require.Empty(t, empty, "ListByProgramme(missing)")
}

func testProgrammeLevelStoreUpdate(t *testing.T, ss store.Store) {
	ctx := context.Background()
	unit, computing := saveProgrammeParents(t, ctx, ss, "computer-science")
	engineering := saveProgramme(t, ctx, ss, unit.ID.String(), "software-engineering")
	level := saveProgrammeLevel(t, ctx, ss, computing.ID.String(), "year-1")
	createAt := level.CreatedAt

	level.ProgrammeID = engineering.ID
	level.Name = "foundation"
	level.DisplayName = "Foundation"
	updated, err := ss.ProgrammeLevel().SeedUpdate(ctx, level)
	require.NoError(t, err)
	require.Equal(t, engineering.ID, updated.ProgrammeID)
	require.Equal(t, "foundation", updated.Name)
	require.True(t, updated.CreatedAt.Equal(createAt))
	require.False(t, updated.UpdatedAt.Before(level.UpdatedAt))

	missing := *updated
	missing.ID = model.ProgrammeLevelID(model.NewID())
	_, err = UpdateProgrammeLevel(ctx, ss, &missing)
	require.True(t, store.IsNotFound(err), "Update(missing) error")
	archivedProgramme := saveProgramme(t, ctx, ss, unit.ID.String(), "archived-programme")
	_, err = ArchiveProgramme(ctx, ss, archivedProgramme.ID, model.GetMillis())
	require.NoError(t, err)
	invalidMove := *updated
	invalidMove.ProgrammeID = archivedProgramme.ID
	_, err = UpdateProgrammeLevel(ctx, ss, &invalidMove)
	var reference *store.ErrReference
	require.ErrorAs(t, err, &reference, "Update(archived programme) error")
}

func testProgrammeLevelStoreRejectUnknownProgramme(t *testing.T, ss store.Store) {
	ctx := context.Background()
	saveInstitution(t, ctx, ss)

	_, err := SaveProgrammeLevel(ctx, ss, &model.ProgrammeLevel{
		ProgrammeID: model.ProgrammeID(model.NewID()),
		Name:        "year-1",
		DisplayName: "Year 1",
	})
	var reference *store.ErrReference
	require.ErrorAs(t, err, &reference, "Save(unknown programme) error")
	require.Equal(t, "programme_levels_programme_id_fkey", reference.Constraint, "Save(unknown programme) error")
}

func testProgrammeLevelStoreEnforceProgrammeNameUniqueness(t *testing.T, ss store.Store) {
	ctx := context.Background()
	unit, computing := saveProgrammeParents(t, ctx, ss, "computer-science")
	engineering := saveProgramme(t, ctx, ss, unit.ID.String(), "software-engineering")
	saveProgrammeLevel(t, ctx, ss, computing.ID.String(), "year-1")

	_, err := SaveProgrammeLevel(ctx, ss, &model.ProgrammeLevel{
		ProgrammeID: computing.ID,
		Name:        "year-1",
		DisplayName: "Duplicate",
	})
	var conflict *store.ErrConflict
	require.ErrorAs(t, err, &conflict, "duplicate programme level error")
	require.Equal(t, "programme_levels_programme_id_name_key", conflict.Constraint, "duplicate programme level error")

	_, err = SaveProgrammeLevel(ctx, ss, &model.ProgrammeLevel{
		ProgrammeID: engineering.ID,
		Name:        "year-1",
		DisplayName: "Year 1",
	})
	require.NoError(t, err)
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
	level, err := SaveProgrammeLevel(ctx, ss, &model.ProgrammeLevel{
		ProgrammeID: model.ProgrammeID(programmeID),
		Name:        name,
		DisplayName: name,
	})
	require.NoError(t, err)
	return level
}

func testProgrammeLevelStoreSearchLimit(t *testing.T, ss store.Store) {
	ctx := context.Background()
	_, programme := saveProgrammeParents(t, ctx, ss, "bounded-programme")
	first := saveProgrammeLevel(t, ctx, ss, programme.ID.String(), "bounded-first")
	saveProgrammeLevel(t, ctx, ss, programme.ID.String(), "bounded-second")
	for _, term := range []string{"", "  \t", "bounded"} {
		found, err := ss.ProgrammeLevel().SearchByProgramme(ctx, programme.ID, term, 1)
		require.NoError(t, err)
		require.Len(t, found, 1, "SearchByProgramme(q=%q, limit=1)", term)
		require.Equal(t, first.ID, found[0].ID, "SearchByProgramme(q=%q, limit=1)", term)
	}
}
