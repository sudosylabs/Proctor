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
	engineering := saveProgramme(t, ctx, ss, unit.Id, "software-engineering")
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
	engineering := saveProgramme(t, ctx, ss, unit.Id, "software-engineering")
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
	engineering := saveProgramme(t, ctx, ss, unit.Id, "software-engineering")
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
	unit := saveAcademicUnit(t, ctx, ss, institution.Id, "", "computing")
	return unit, saveProgramme(t, ctx, ss, unit.Id, programmeName)
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
