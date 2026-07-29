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
	t.Run("Save", func(t *testing.T) { testClassStoreSave(t, ss) })
	t.Run("Get", func(t *testing.T) { testClassStoreGet(t, ss) })
	t.Run("GetByName", func(t *testing.T) { testClassStoreGetByName(t, ss) })
	t.Run("ListByProgrammeLevel", func(t *testing.T) { testClassStoreListByProgrammeLevel(t, ss) })
	t.Run("ListByAcademicPeriod", func(t *testing.T) { testClassStoreListByAcademicPeriod(t, ss) })
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
