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

	if _, err := ss.Class().Save(ctx, &model.Class{
		ProgrammeLevelID: level.ID, AcademicPeriodID: applicable.ID,
		Name: "applicable", DisplayName: "Applicable",
	}); err != nil {
		t.Fatalf("descendant Class rejected owner-ancestor period: %v", err)
	}
	_, err := ss.Class().Save(ctx, &model.Class{
		ProgrammeLevelID: level.ID, AcademicPeriodID: inapplicable.ID,
		Name: "cross-subtree", DisplayName: "Cross Subtree",
	})
	var reference *store.ErrReference
	if !errors.As(err, &reference) || reference.Constraint != "classes_academic_period_not_applicable" {
		t.Fatalf("cross-subtree Class error = %v, want applicability reference", err)
	}
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
	candidate.PrepareCreate(model.ClassID(model.NewId()), model.NowUTC())
	created, err := ss.Class().Create(ctx, &store.ClassCreation{Class: candidate, AuditEventID: createAttempt.ID.String(), AuditAt: model.GetMillis()})
	requireNoError(t, err)
	completed, err := ss.Audit().Get(ctx, createAttempt.ID.String())
	requireNoError(t, err)
	if completed.Status != model.AuditStatusSuccess {
		t.Fatalf("create audit status = %q", completed.Status)
	}

	rolledBackCreate := &model.Class{
		ProgrammeLevelID: fixture.level.ID,
		AcademicPeriodID: fixture.period.ID,
		Name:             "rolled-back-class",
		DisplayName:      "Rolled Back",
	}
	rolledBackCreate.PrepareCreate(model.ClassID(model.NewId()), model.NowUTC())
	if _, err := ss.Class().Create(ctx, &store.ClassCreation{Class: rolledBackCreate, AuditEventID: model.NewId(), AuditAt: model.GetMillis()}); err == nil {
		t.Fatal("Create() succeeded without audit attempt")
	}
	if _, err := ss.Class().Get(ctx, rolledBackCreate.ID.String()); !store.IsNotFound(err) {
		t.Fatalf("create survived rollback: %v", err)
	}

	updateAttempt := saveClassAuditAttempt(t, ctx, ss, fixture.programme.AcademicUnitID.String())
	updatedCandidate := *created
	updatedCandidate.DisplayName = "Updated Class"
	updatedCandidate.PrepareUpdate(created.UpdatedAt.Add(1_000_000)) // 1ms
	updated, err := ss.Class().UpdateWithAudit(ctx, &store.ClassUpdate{Class: &updatedCandidate, ExpectedAcademicUnitID: fixture.programme.AcademicUnitID.String(), ExpectedRevision: created.Revision, AuditEventID: updateAttempt.ID.String(), AuditAt: model.GetMillis()})
	requireNoError(t, err)
	if updated.Revision != created.Revision+1 {
		t.Fatalf("update revision = %d, want %d", updated.Revision, created.Revision+1)
	}
	staleLegacy := *created
	staleLegacy.DisplayName = "Stale Legacy Update"
	if _, err := ss.Class().Update(ctx, &staleLegacy); !store.IsConflict(err) {
		t.Fatalf("stale Update() error = %v", err)
	}
	staleAttempt := saveClassAuditAttempt(t, ctx, ss, fixture.programme.AcademicUnitID.String())
	staleCandidate := *updated
	staleCandidate.DisplayName = "Stale Update"
	staleCandidate.PrepareUpdate(updated.UpdatedAt.Add(1_000_000))
	if _, err := ss.Class().UpdateWithAudit(ctx, &store.ClassUpdate{Class: &staleCandidate, ExpectedAcademicUnitID: fixture.programme.AcademicUnitID.String(), ExpectedRevision: created.Revision, AuditEventID: staleAttempt.ID.String(), AuditAt: model.GetMillis()}); !store.IsConflict(err) {
		t.Fatalf("stale UpdateWithAudit() error = %v", err)
	}
	wrongOwnerAttempt := saveClassAuditAttempt(t, ctx, ss, fixture.programme.AcademicUnitID.String())
	if _, err := ss.Class().ArchiveWithAudit(ctx, &store.ClassArchive{ID: updated.ID.String(), ExpectedAcademicUnitID: model.NewId(), ExpectedRevision: updated.Revision, ArchiveAt: model.MillisFromTime(updated.UpdatedAt) + 1, AuditEventID: wrongOwnerAttempt.ID.String(), AuditAt: model.GetMillis()}); !store.IsConflict(err) {
		t.Fatalf("wrong-owner ArchiveWithAudit() error = %v", err)
	}
	rolledBack := *updated
	rolledBack.DisplayName = "Must Roll Back"
	rolledBack.PrepareUpdate(updated.UpdatedAt.Add(1_000_000))
	if _, err := ss.Class().UpdateWithAudit(ctx, &store.ClassUpdate{Class: &rolledBack, ExpectedAcademicUnitID: fixture.programme.AcademicUnitID.String(), ExpectedRevision: updated.Revision, AuditEventID: model.NewId(), AuditAt: model.GetMillis()}); err == nil {
		t.Fatal("UpdateWithAudit() succeeded without audit attempt")
	}
	persisted, err := ss.Class().Get(ctx, updated.ID.String())
	requireNoError(t, err)
	if persisted.DisplayName != updated.DisplayName {
		t.Fatalf("update survived rollback: %#v", persisted)
	}

	archiveAttempt := saveClassAuditAttempt(t, ctx, ss, fixture.programme.AcademicUnitID.String())
	archived, err := ss.Class().ArchiveWithAudit(ctx, &store.ClassArchive{ID: updated.ID.String(), ExpectedAcademicUnitID: fixture.programme.AcademicUnitID.String(), ExpectedRevision: updated.Revision, ArchiveAt: model.MillisFromTime(updated.UpdatedAt) + 1, AuditEventID: archiveAttempt.ID.String(), AuditAt: model.GetMillis()})
	requireNoError(t, err)
	if archived.Revision != updated.Revision+1 {
		t.Fatalf("archive revision = %d, want %d", archived.Revision, updated.Revision+1)
	}
	if !archived.IsArchived() {
		t.Fatalf("ArchiveWithAudit() = %#v", archived)
	}
	archiveRollback := saveClass(t, ctx, ss, fixture.level.ID.String(), fixture.period.ID.String(), "rolled-back-archive")
	if _, err := ss.Class().ArchiveWithAudit(ctx, &store.ClassArchive{ID: archiveRollback.ID.String(), ExpectedAcademicUnitID: fixture.programme.AcademicUnitID.String(), ExpectedRevision: archiveRollback.Revision, ArchiveAt: model.GetMillis(), AuditEventID: model.NewId(), AuditAt: model.GetMillis()}); err == nil {
		t.Fatal("ArchiveWithAudit() succeeded without audit attempt")
	}
	if _, err := ss.Class().Get(ctx, archiveRollback.ID.String()); err != nil {
		t.Fatalf("archive survived rollback: %v", err)
	}
}

func saveClassAuditAttempt(t *testing.T, ctx context.Context, ss store.Store, unitID string) *model.AuditEvent {
	t.Helper()
	attempt, err := ss.Audit().Save(ctx, &model.AuditEvent{Action: string(model.ActionAcademicUnitManage), Resource: model.Resource{Type: model.ResourceAcademicUnit, ID: unitID}, ScopeType: model.RoleScopeAcademicUnit, ScopeID: unitID, Status: model.AuditStatusAttempt, NodeID: "test-node"})
	requireNoError(t, err)
	return attempt
}

func testClassStoreSearchAndArchive(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture := saveClassFixture(t, ctx, ss)
	class := saveClass(
		t, ctx, ss, fixture.level.ID.String(), fixture.period.ID.String(), "distinct-class-zeta",
	)
	class.DisplayName = "Literal 50%_! Zeta"
	class, err := ss.Class().Update(ctx, class)
	requireNoError(t, err)
	unitID, err := ss.Class().GetAcademicUnitId(ctx, class.ID.String())
	requireNoError(t, err)
	found, err := ss.Class().SearchByAcademicUnit(ctx, unitID, "zeta", 10)
	requireNoError(t, err)
	if len(found) != 1 || found[0].ID != class.ID {
		t.Fatalf("SearchByAcademicUnit() = %#v", found)
	}
	for _, query := range []string{"LITERAL 50%_!", "%", "_", "!"} {
		found, err = ss.Class().SearchByAcademicUnit(ctx, unitID, query, 10)
		requireNoError(t, err)
		if len(found) != 1 || found[0].ID != class.ID {
			t.Fatalf("SearchByAcademicUnit(%q) = %#v, want only %s", query, found, class.ID)
		}
	}
	archived, err := ss.Class().Archive(ctx, class.ID.String(), model.GetMillis())
	requireNoError(t, err)
	if !archived.IsArchived() {
		t.Fatalf("Archive() = %#v", archived)
	}
}

func testClassStoreGetAcademicUnitId(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture := saveClassFixture(t, ctx, ss)
	class := saveClass(t, ctx, ss, fixture.level.ID.String(), fixture.period.ID.String(), "class-a")
	academicUnitID, err := ss.Class().GetAcademicUnitId(ctx, class.ID.String())
	requireNoError(t, err)
	programme, err := ss.Programme().Get(ctx, fixture.programme.ID.String())
	requireNoError(t, err)
	if academicUnitID != programme.AcademicUnitID.String() {
		t.Fatalf("GetAcademicUnitId() = %q, want %q", academicUnitID, programme.AcademicUnitID.String())
	}
	if _, err := ss.Class().GetAcademicUnitId(ctx, model.NewId()); !store.IsNotFound(err) {
		t.Fatalf("GetAcademicUnitId(missing) error = %v", err)
	}
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

	saved, err := ss.Class().Save(ctx, class)
	requireNoError(t, err)
	if !saved.ID.IsValid() {
		t.Fatalf("Save() id = %q", saved.ID)
	}
	if !class.ID.IsZero() {
		t.Fatalf("Save() mutated input id to %q", class.ID)
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
	class := saveClass(t, ctx, ss, fixture.level.ID.String(), fixture.period.ID.String(), "class-a")

	got, err := ss.Class().Get(ctx, class.ID.String())
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
	class := saveClass(t, ctx, ss, fixture.level.ID.String(), fixture.period.ID.String(), "class-a")

	got, err := ss.Class().GetByName(ctx, fixture.level.ID.String(), fixture.period.ID.String(), class.Name)
	requireNoError(t, err)
	if got.ID != class.ID {
		t.Fatalf("GetByName() id = %q, want %q", got.ID, class.ID)
	}
	if _, err := ss.Class().GetByName(
		ctx,
		fixture.level.ID.String(),
		fixture.period.ID.String(),
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
		fixture.institution.ID.String(),
		"2027-2028",
		model.MillisFromTime(fixture.period.StartsAt)+40_000_000_000,
	)
	otherLevel := saveProgrammeLevel(t, ctx, ss, fixture.programme.ID.String(), "year-2")
	first := saveClass(t, ctx, ss, fixture.level.ID.String(), fixture.period.ID.String(), "class-a")
	second := saveClass(t, ctx, ss, fixture.level.ID.String(), nextPeriod.ID.String(), "class-a")
	saveClass(t, ctx, ss, otherLevel.ID.String(), fixture.period.ID.String(), "class-a")

	classes, err := ss.Class().ListByProgrammeLevel(ctx, fixture.level.ID.String())
	requireNoError(t, err)
	if len(classes) != 2 || classes[0].ID != first.ID || classes[1].ID != second.ID {
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

	classes, err := ss.Class().ListByAcademicPeriod(ctx, fixture.period.ID.String())
	requireNoError(t, err)
	if len(classes) != 2 {
		t.Fatalf("ListByAcademicPeriod() = %#v", classes)
	}
	gotIDs := map[model.ClassID]bool{classes[0].ID: true, classes[1].ID: true}
	if !gotIDs[first.ID] || !gotIDs[second.ID] {
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
	updated, err := ss.Class().Update(ctx, class)
	requireNoError(t, err)
	if updated.ProgrammeLevelID != otherLevel.ID ||
		updated.AcademicPeriodID != nextPeriod.ID ||
		updated.Name != "class-b" {
		t.Fatalf("Update() = %#v", updated)
	}
	if !updated.CreatedAt.Equal(createAt) || updated.UpdatedAt.Before(previousUpdateAt) {
		t.Fatalf("Update() timestamps = %#v", updated)
	}

	missing := *updated
	missing.ID = model.ClassID(model.NewId())
	_, err = ss.Class().Update(ctx, &missing)
	if !store.IsNotFound(err) {
		t.Fatalf("Update(missing) error = %v, want not found", err)
	}
}

func testClassStoreRejectUnknownProgrammeLevel(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	period := saveAcademicPeriod(t, ctx, ss, institution.ID.String(), "2026-2027", 1_800_000_000_000)

	_, err := ss.Class().Save(ctx, &model.Class{
		ProgrammeLevelID: model.ProgrammeLevelID(model.NewId()),
		AcademicPeriodID: period.ID,
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
	level := saveProgrammeLevel(t, ctx, ss, programme.ID.String(), "year-1")

	_, err := ss.Class().Save(ctx, &model.Class{
		ProgrammeLevelID: level.ID,
		AcademicPeriodID: model.AcademicPeriodID(model.NewId()),
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

	_, err := ss.Class().Save(ctx, &model.Class{
		ProgrammeLevelID: fixture.level.ID,
		AcademicPeriodID: fixture.period.ID,
		Name:             "class-a",
		DisplayName:      "Duplicate",
	})
	var conflict *store.ErrConflict
	if !errors.As(err, &conflict) || conflict.Constraint != "classes_programme_level_id_academic_period_id_name_key" {
		t.Fatalf("duplicate class error = %v, want scoped-name conflict", err)
	}

	if _, err := ss.Class().Save(ctx, &model.Class{
		ProgrammeLevelID: otherLevel.ID,
		AcademicPeriodID: fixture.period.ID,
		Name:             "class-a",
		DisplayName:      "Class A",
	}); err != nil {
		t.Fatalf("same name for another programme level error = %v", err)
	}
	if _, err := ss.Class().Save(ctx, &model.Class{
		ProgrammeLevelID: fixture.level.ID,
		AcademicPeriodID: nextPeriod.ID,
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
	class, err := ss.Class().Save(ctx, &model.Class{
		ProgrammeLevelID: model.ProgrammeLevelID(programmeLevelID),
		AcademicPeriodID: model.AcademicPeriodID(academicPeriodID),
		Name:             name,
		DisplayName:      name,
	})
	requireNoError(t, err)
	return class
}
