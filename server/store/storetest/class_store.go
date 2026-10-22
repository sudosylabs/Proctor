// ---------------------------------------------------------------------------------------------
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
//
// Adapted from Mattermost server/channels/store/storetest/team_store.go. The
// suite receives the root store and verifies each ClassStore operation so every
// future persistence adapter can reuse the same tests.

package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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
	t.Run("GetAcademicUnitID", func(t *testing.T) { testClassStoreGetAcademicUnitID(t, ss) })
	t.Run("Update", func(t *testing.T) { testClassStoreUpdate(t, ss) })
	t.Run("RejectUnknownProgrammeLevel", func(t *testing.T) {
		testClassStoreRejectUnknownProgrammeLevel(t, ss)
	})
	t.Run("RejectUnknownAcademicPeriod", func(t *testing.T) {
		testClassStoreRejectUnknownAcademicPeriod(t, ss)
	})
	t.Run("EnforceAcademicPeriodApplicability", func(t *testing.T) {
		testClassStoreEnforceAcademicPeriodApplicability(t, ss)
	})
	t.Run("EnforceScopedNameUniqueness", func(t *testing.T) {
		testClassStoreEnforceScopedNameUniqueness(t, ss)
	})
	t.Run("SearchAndArchive", func(t *testing.T) {
		testClassStoreSearchAndArchive(t, ss)
	})
}

func testClassStoreEnforceAcademicPeriodApplicability(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	root := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "period-root")
	child := saveAcademicUnit(t, ctx, ss, institution.ID.String(), root.ID.String(), "period-child")
	sibling := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "period-sibling")
	programme := saveProgramme(t, ctx, ss, child.ID.String(), "period-programme")
	level := saveProgrammeLevel(t, ctx, ss, programme.ID.String(), "period-level")
	applicable := saveAcademicUnitPeriod(t, ctx, ss, root.ID, "root-period", 1_800_000_000_000)
	inapplicable := saveAcademicUnitPeriod(t, ctx, ss, sibling.ID, "sibling-period", 1_800_000_000_000)

	_, err := SaveClass(ctx, ss, &model.Class{
		ProgrammeLevelID: level.ID, AcademicPeriodID: applicable.ID,
		Name: "applicable", DisplayName: "Applicable",
	})
	require.NoError(t, err)
	_, err = SaveClass(ctx, ss, &model.Class{
		ProgrammeLevelID: level.ID, AcademicPeriodID: inapplicable.ID,
		Name: "cross-subtree", DisplayName: "Cross Subtree",
	})
	var reference *store.ErrReference
	require.ErrorAs(t, err, &reference, "cross-subtree Class error")
	require.Equal(t, "classes_academic_period_not_applicable", reference.Constraint, "cross-subtree Class error")
}

func testClassStoreMutationAuditAtomicity(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture := saveClassFixture(t, ctx, ss)
	createAttempt := saveClassAuditAttempt(t, ctx, ss, fixture.programme.AcademicUnitID.String())
	candidate := &model.Class{
		ProgrammeLevelID: fixture.level.ID,
		AcademicPeriodID: fixture.period.ID,
		Name:             "audited-class",
		DisplayName:      "Audited Class",
	}
	candidate.PrepareCreate(model.ClassID(model.NewID()), model.NowUTC())
	created, err := ss.Class().Create(ctx, &store.ClassCreation{Class: candidate, AuditEventID: createAttempt.ID, AuditAt: model.NowUTC()})
	require.NoError(t, err)
	completed, err := ss.Audit().Get(ctx, createAttempt.ID)
	require.NoError(t, err)
	require.Equal(t, model.AuditStatusSuccess, completed.Status)

	rolledBackCreate := &model.Class{
		ProgrammeLevelID: fixture.level.ID,
		AcademicPeriodID: fixture.period.ID,
		Name:             "rolled-back-class",
		DisplayName:      "Rolled Back",
	}
	rolledBackCreate.PrepareCreate(model.ClassID(model.NewID()), model.NowUTC())
	_, err = ss.Class().Create(ctx, &store.ClassCreation{Class: rolledBackCreate, AuditEventID: model.NewAuditEventID(), AuditAt: model.NowUTC()})
	require.Error(t, err, "Create() succeeded without audit attempt")
	_, err = ss.Class().Get(ctx, rolledBackCreate.ID)
	require.True(t, store.IsNotFound(err), "create survived rollback")

	updateAttempt := saveClassAuditAttempt(t, ctx, ss, fixture.programme.AcademicUnitID.String())
	updatedCandidate := *created
	updatedCandidate.DisplayName = "Updated Class"
	updatedCandidate.PrepareUpdate(created.UpdatedAt.Add(1_000_000)) // 1ms
	updated, err := ss.Class().Update(ctx, &store.ClassUpdate{Class: &updatedCandidate, ExpectedAcademicUnitID: fixture.programme.AcademicUnitID, ExpectedRevision: created.Revision, AuditEventID: updateAttempt.ID, AuditAt: model.NowUTC()})
	require.NoError(t, err)
	require.Equal(t, created.Revision+1, updated.Revision, "update revision")
	staleLegacy := *created
	staleLegacy.DisplayName = "Stale Legacy Update"
	_, err = UpdateClass(ctx, ss, &staleLegacy)
	require.True(t, store.IsConflict(err), "stale Update() error")
	staleAttempt := saveClassAuditAttempt(t, ctx, ss, fixture.programme.AcademicUnitID.String())
	staleCandidate := *updated
	staleCandidate.DisplayName = "Stale Update"
	staleCandidate.PrepareUpdate(updated.UpdatedAt.Add(1_000_000))
	_, err = ss.Class().Update(ctx, &store.ClassUpdate{Class: &staleCandidate, ExpectedAcademicUnitID: fixture.programme.AcademicUnitID, ExpectedRevision: created.Revision, AuditEventID: staleAttempt.ID, AuditAt: model.NowUTC()})
	require.True(t, store.IsConflict(err), "stale Update() error")
	wrongOwnerAttempt := saveClassAuditAttempt(t, ctx, ss, fixture.programme.AcademicUnitID.String())
	_, err = ss.Class().Archive(ctx, &store.ClassArchive{ID: updated.ID, ExpectedAcademicUnitID: model.NewAcademicUnitID(), ExpectedRevision: updated.Revision, ArchiveAt: updated.UpdatedAt.Add(1 * time.Millisecond), AuditEventID: wrongOwnerAttempt.ID, AuditAt: model.NowUTC()})
	require.True(t, store.IsConflict(err), "wrong-owner Archive() error")
	rolledBack := *updated
	rolledBack.DisplayName = "Must Roll Back"
	rolledBack.PrepareUpdate(updated.UpdatedAt.Add(1_000_000))
	_, err = ss.Class().Update(ctx, &store.ClassUpdate{Class: &rolledBack, ExpectedAcademicUnitID: fixture.programme.AcademicUnitID, ExpectedRevision: updated.Revision, AuditEventID: model.NewAuditEventID(), AuditAt: model.NowUTC()})
	require.Error(t, err, "Update() succeeded without audit attempt")
	persisted, err := ss.Class().Get(ctx, updated.ID)
	require.NoError(t, err)
	require.Equal(t, updated.DisplayName, persisted.DisplayName)

	archiveAttempt := saveClassAuditAttempt(t, ctx, ss, fixture.programme.AcademicUnitID.String())
	archived, err := ss.Class().Archive(ctx, &store.ClassArchive{ID: updated.ID, ExpectedAcademicUnitID: fixture.programme.AcademicUnitID, ExpectedRevision: updated.Revision, ArchiveAt: updated.UpdatedAt.Add(1 * time.Millisecond), AuditEventID: archiveAttempt.ID, AuditAt: model.NowUTC()})
	require.NoError(t, err)
	require.Equal(t, updated.Revision+1, archived.Revision, "archive revision")
	require.True(t, archived.IsArchived())
	archiveRollback := saveClass(t, ctx, ss, fixture.level.ID.String(), fixture.period.ID.String(), "rolled-back-archive")
	_, err = ss.Class().Archive(ctx, &store.ClassArchive{ID: archiveRollback.ID, ExpectedAcademicUnitID: fixture.programme.AcademicUnitID, ExpectedRevision: archiveRollback.Revision, ArchiveAt: model.NowUTC(), AuditEventID: model.NewAuditEventID(), AuditAt: model.NowUTC()})
	require.Error(t, err, "Archive() succeeded without audit attempt")
	_, err = ss.Class().Get(ctx, archiveRollback.ID)
	require.NoError(t, err)
}

func saveClassAuditAttempt(t *testing.T, ctx context.Context, ss store.Store, unitID string) *model.AuditEvent {
	t.Helper()
	attempt, err := ss.Audit().Save(ctx, &model.AuditEvent{Action: string(model.ActionAcademicUnitManage), Resource: model.Resource{Type: model.ResourceAcademicUnit, ID: unitID}, ScopeType: model.RoleScopeAcademicUnit, ScopeID: unitID, Status: model.AuditStatusAttempt, NodeID: "test-node"})
	require.NoError(t, err)
	return attempt
}

func testClassStoreSearchAndArchive(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture := saveClassFixture(t, ctx, ss)
	class := saveClass(
		t, ctx, ss, fixture.level.ID.String(), fixture.period.ID.String(), "distinct-class-zeta",
	)
	class.DisplayName = "Literal 50%_! Zeta"
	class, err := UpdateClass(ctx, ss, class)
	require.NoError(t, err)
	unitID, err := ss.Class().GetAcademicUnitID(ctx, class.ID)
	require.NoError(t, err)
	found, err := ss.Class().SearchByAcademicUnit(ctx, unitID, store.AcademicNameListOptions{Term: "zeta", Limit: 10})
	require.NoError(t, err)
	require.Len(t, found, 1)
	require.Equal(t, class.ID, found[0].ID)
	for _, query := range []string{"LITERAL 50%_!", "%", "_", "!"} {
		found, err = ss.Class().SearchByAcademicUnit(ctx, unitID, store.AcademicNameListOptions{Term: query, Limit: 10})
		require.NoError(t, err)
		require.Len(t, found, 1, "SearchByAcademicUnit(%q)", query)
		require.Equal(t, class.ID, found[0].ID, "SearchByAcademicUnit(%q)", query)
	}
	archived, err := ArchiveClass(ctx, ss, class.ID, model.NowUTC())
	require.NoError(t, err)
	require.True(t, archived.IsArchived())
}

func testClassStoreGetAcademicUnitID(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture := saveClassFixture(t, ctx, ss)
	class := saveClass(t, ctx, ss, fixture.level.ID.String(), fixture.period.ID.String(), "class-a")
	academicUnitID, err := ss.Class().GetAcademicUnitID(ctx, class.ID)
	require.NoError(t, err)
	programme, err := ss.Programme().Get(ctx, fixture.programme.ID)
	require.NoError(t, err)
	require.Equal(t, programme.AcademicUnitID, academicUnitID, "GetAcademicUnitID()")
	_, err = ss.Class().GetAcademicUnitID(ctx, model.NewClassID())
	require.True(t, store.IsNotFound(err), "GetAcademicUnitID(missing) error")
}

func testClassStoreSave(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture := saveClassFixture(t, ctx, ss)
	class := &model.Class{
		ProgrammeLevelID: fixture.level.ID,
		AcademicPeriodID: fixture.period.ID,
		Name:             "class-a",
		DisplayName:      "Class A",
		Description:      "Primary student roster",
	}

	saved, err := SaveClass(ctx, ss, class)
	require.NoError(t, err)
	require.True(t, saved.ID.IsValid())
	require.True(t, class.ID.IsZero())

	_, err = SaveClass(ctx, ss, saved)
	var invalid *store.ErrInvalidInput
	require.ErrorAs(t, err, &invalid, "second Save(saved) error")
}

func testClassStoreGet(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture := saveClassFixture(t, ctx, ss)
	class := saveClass(t, ctx, ss, fixture.level.ID.String(), fixture.period.ID.String(), "class-a")

	got, err := ss.Class().Get(ctx, class.ID)
	require.NoError(t, err)
	require.Equal(t, *class, *got, "Get() = %#v, want %#v", got, class)
	_, err = ss.Class().Get(ctx, model.NewClassID())
	require.True(t, store.IsNotFound(err), "Get(missing) error")
}

func testClassStoreGetByName(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture := saveClassFixture(t, ctx, ss)
	class := saveClass(t, ctx, ss, fixture.level.ID.String(), fixture.period.ID.String(), "class-a")

	got, err := ss.Class().GetByName(ctx, fixture.level.ID, fixture.period.ID, class.Name)
	require.NoError(t, err)
	require.Equal(t, class.ID, got.ID, "GetByName() id")
	_, err = ss.Class().GetByName(
		ctx,
		fixture.level.ID,
		fixture.period.ID,
		"missing",
	)
	require.True(t, store.IsNotFound(err), "GetByName(missing) error")
}

func testClassStoreListByProgrammeLevel(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture := saveClassFixture(t, ctx, ss)
	nextPeriod := saveAcademicPeriod(
		t,
		ctx,
		ss,
		fixture.institution.ID.String(),
		"2027-2028",
		model.MillisFromTime(fixture.period.StartsAt)+40_000_000_000,
	)
	otherLevel := saveProgrammeLevel(t, ctx, ss, fixture.programme.ID.String(), "year-2")
	first := saveClass(t, ctx, ss, fixture.level.ID.String(), fixture.period.ID.String(), "class-a")
	second := saveClass(t, ctx, ss, fixture.level.ID.String(), nextPeriod.ID.String(), "class-b")
	saveClass(t, ctx, ss, otherLevel.ID.String(), fixture.period.ID.String(), "class-a")

	classes, err := ss.Class().ListByProgrammeLevel(ctx, fixture.level.ID, store.AcademicNameListOptions{Limit: 200})
	require.NoError(t, err)
	require.Len(t, classes, 2)
	require.Equal(t, first.ID, classes[0].ID)
	require.Equal(t, second.ID, classes[1].ID, "ordered by name then identity")
	page, err := ss.Class().ListByProgrammeLevel(ctx, fixture.level.ID, store.AcademicNameListOptions{AfterName: first.Name, AfterID: first.ID.String(), Limit: 200})
	require.NoError(t, err)
	require.Len(t, page, 1, "continuation after (name, id)")
	require.Equal(t, second.ID, page[0].ID, "continuation after (name, id)")
	empty, err := ss.Class().ListByProgrammeLevel(ctx, model.NewProgrammeLevelID(), store.AcademicNameListOptions{Limit: 200})
	require.NoError(t, err)
	require.Empty(t, empty, "ListByProgrammeLevel(missing)")
}

func testClassStoreListByAcademicPeriod(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture := saveClassFixture(t, ctx, ss)
	otherLevel := saveProgrammeLevel(t, ctx, ss, fixture.programme.ID.String(), "year-2")
	nextPeriod := saveAcademicPeriod(
		t,
		ctx,
		ss,
		fixture.institution.ID.String(),
		"2027-2028",
		model.MillisFromTime(fixture.period.StartsAt)+40_000_000_000,
	)
	first := saveClass(t, ctx, ss, fixture.level.ID.String(), fixture.period.ID.String(), "class-a")
	second := saveClass(t, ctx, ss, otherLevel.ID.String(), fixture.period.ID.String(), "class-b")
	saveClass(t, ctx, ss, fixture.level.ID.String(), nextPeriod.ID.String(), "class-a")

	classes, err := ss.Class().ListByAcademicPeriod(ctx, fixture.period.ID)
	require.NoError(t, err)
	require.Len(t, classes, 2)
	gotIDs := map[model.ClassID]bool{classes[0].ID: true, classes[1].ID: true}
	require.True(t, gotIDs[first.ID])
	require.True(t, gotIDs[second.ID])
	empty, err := ss.Class().ListByAcademicPeriod(ctx, model.NewAcademicPeriodID())
	require.NoError(t, err)
	require.Empty(t, empty, "ListByAcademicPeriod(missing)")
}

func testClassStoreUpdate(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture := saveClassFixture(t, ctx, ss)
	otherLevel := saveProgrammeLevel(t, ctx, ss, fixture.programme.ID.String(), "year-2")
	nextPeriod := saveAcademicPeriod(
		t,
		ctx,
		ss,
		fixture.institution.ID.String(),
		"2027-2028",
		model.MillisFromTime(fixture.period.StartsAt)+40_000_000_000,
	)
	class := saveClass(t, ctx, ss, fixture.level.ID.String(), fixture.period.ID.String(), "class-a")
	createAt := class.CreatedAt
	previousUpdateAt := class.UpdatedAt

	class.ProgrammeLevelID = otherLevel.ID
	class.AcademicPeriodID = nextPeriod.ID
	class.Name = "class-b"
	class.DisplayName = "Class B"
	updated, err := UpdateClass(ctx, ss, class)
	require.NoError(t, err)
	require.Equal(t, otherLevel.ID, updated.ProgrammeLevelID)
	require.Equal(t, nextPeriod.ID, updated.AcademicPeriodID)
	require.Equal(t, "class-b", updated.Name)
	require.True(t, updated.CreatedAt.Equal(createAt))
	require.False(t, updated.UpdatedAt.Before(previousUpdateAt))

	missing := *updated
	missing.ID = model.ClassID(model.NewID())
	_, err = UpdateClass(ctx, ss, &missing)
	require.True(t, store.IsNotFound(err), "Update(missing) error")
}

func testClassStoreRejectUnknownProgrammeLevel(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	period := saveAcademicPeriod(t, ctx, ss, institution.ID.String(), "2026-2027", 1_800_000_000_000)

	_, err := SaveClass(ctx, ss, &model.Class{
		ProgrammeLevelID: model.ProgrammeLevelID(model.NewID()),
		AcademicPeriodID: period.ID,
		Name:             "class-a",
		DisplayName:      "Class A",
	})
	var reference *store.ErrReference
	require.ErrorAs(t, err, &reference, "Save(unknown programme level) error")
	require.Equal(t, "classes_programme_level_id_fkey", reference.Constraint, "Save(unknown programme level) error")
}

func testClassStoreRejectUnknownAcademicPeriod(t *testing.T, ss store.Store) {
	ctx := context.Background()
	_, programme := saveProgrammeParents(t, ctx, ss, "computer-science")
	level := saveProgrammeLevel(t, ctx, ss, programme.ID.String(), "year-1")

	_, err := SaveClass(ctx, ss, &model.Class{
		ProgrammeLevelID: level.ID,
		AcademicPeriodID: model.AcademicPeriodID(model.NewID()),
		Name:             "class-a",
		DisplayName:      "Class A",
	})
	var reference *store.ErrReference
	require.ErrorAs(t, err, &reference, "Save(unknown academic period) error")
	require.Equal(t, "classes_academic_period_id_fkey", reference.Constraint, "Save(unknown academic period) error")
}

func testClassStoreEnforceScopedNameUniqueness(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture := saveClassFixture(t, ctx, ss)
	otherLevel := saveProgrammeLevel(t, ctx, ss, fixture.programme.ID.String(), "year-2")
	nextPeriod := saveAcademicPeriod(
		t,
		ctx,
		ss,
		fixture.institution.ID.String(),
		"2027-2028",
		model.MillisFromTime(fixture.period.StartsAt)+40_000_000_000,
	)
	saveClass(t, ctx, ss, fixture.level.ID.String(), fixture.period.ID.String(), "class-a")

	_, err := SaveClass(ctx, ss, &model.Class{
		ProgrammeLevelID: fixture.level.ID,
		AcademicPeriodID: fixture.period.ID,
		Name:             "class-a",
		DisplayName:      "Duplicate",
	})
	var conflict *store.ErrConflict
	require.ErrorAs(t, err, &conflict, "duplicate class error")
	require.Equal(t, "classes_programme_level_id_academic_period_id_name_key", conflict.Constraint, "duplicate class error")

	_, err = SaveClass(ctx, ss, &model.Class{
		ProgrammeLevelID: otherLevel.ID,
		AcademicPeriodID: fixture.period.ID,
		Name:             "class-a",
		DisplayName:      "Class A",
	})
	require.NoError(t, err)
	_, err = SaveClass(ctx, ss, &model.Class{
		ProgrammeLevelID: fixture.level.ID,
		AcademicPeriodID: nextPeriod.ID,
		Name:             "class-a",
		DisplayName:      "Class A",
	})
	require.NoError(t, err)
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
	level := saveProgrammeLevel(t, ctx, ss, programme.ID.String(), "year-1")
	period := saveAcademicPeriod(t, ctx, ss, unit.InstitutionID.String(), "2026-2027", 1_800_000_000_000)
	return classFixture{
		institution: &model.Institution{ID: unit.InstitutionID},
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
	class, err := SaveClass(ctx, ss, &model.Class{
		ProgrammeLevelID: model.ProgrammeLevelID(programmeLevelID),
		AcademicPeriodID: model.AcademicPeriodID(academicPeriodID),
		Name:             name,
		DisplayName:      name,
	})
	require.NoError(t, err)
	return class
}
