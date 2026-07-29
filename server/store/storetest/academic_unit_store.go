// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"context"
	"errors"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestAcademicUnitStore(t *testing.T, ss store.Store) {
	t.Run("Save", func(t *testing.T) { testAcademicUnitStoreSave(t, ss) })
	t.Run("Get", func(t *testing.T) { testAcademicUnitStoreGet(t, ss) })
	t.Run("ListChildren", func(t *testing.T) { testAcademicUnitStoreListChildren(t, ss) })
	t.Run("ListAncestors", func(t *testing.T) { testAcademicUnitStoreListAncestors(t, ss) })
	t.Run("Update", func(t *testing.T) { testAcademicUnitStoreUpdate(t, ss) })
	t.Run("RejectCycle", func(t *testing.T) { testAcademicUnitStoreRejectCycle(t, ss) })
	t.Run("RejectCrossInstitutionParent", func(t *testing.T) {
		testAcademicUnitStoreRejectCrossInstitutionParent(t, ss)
	})
	t.Run("EnforceInstitutionNameUniqueness", func(t *testing.T) {
		testAcademicUnitStoreEnforceInstitutionNameUniqueness(t, ss)
	})
}

func testAcademicUnitStoreListAncestors(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	root := saveAcademicUnit(t, ctx, ss, institution.Id, "", "engineering")
	child := saveAcademicUnit(t, ctx, ss, institution.Id, root.Id, "computing")
	leaf := saveAcademicUnit(t, ctx, ss, institution.Id, child.Id, "software")

	ancestors, err := ss.AcademicUnit().ListAncestors(ctx, leaf.Id)
	requireNoError(t, err)
	if len(ancestors) != 3 ||
		ancestors[0].Id != leaf.Id ||
		ancestors[1].Id != child.Id ||
		ancestors[2].Id != root.Id {
		t.Fatalf("ListAncestors() = %#v", ancestors)
	}
	if _, err := ss.AcademicUnit().ListAncestors(ctx, model.NewId()); !store.IsNotFound(err) {
		t.Fatalf("ListAncestors(missing) error = %v", err)
	}
}

func testAcademicUnitStoreSave(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	root := saveAcademicUnit(t, ctx, ss, institution.Id, "", "engineering")
	child := saveAcademicUnit(t, ctx, ss, institution.Id, root.Id, "computing")

	if !model.IsValidId(root.Id) || child.ParentId != root.Id {
		t.Fatalf("saved units = root %#v, child %#v", root, child)
	}

	_, err := ss.AcademicUnit().Save(ctx, child)
	var invalid *store.ErrInvalidInput
	if !errors.As(err, &invalid) {
		t.Fatalf("second Save(child) error = %v, want invalid input", err)
	}
}

func testAcademicUnitStoreGet(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	unit := saveAcademicUnit(t, ctx, ss, institution.Id, "", "engineering")

	got, err := ss.AcademicUnit().Get(ctx, unit.Id)
	requireNoError(t, err)
	if *got != *unit {
		t.Fatalf("Get() = %#v, want %#v", got, unit)
	}
	if _, err := ss.AcademicUnit().Get(ctx, model.NewId()); !store.IsNotFound(err) {
		t.Fatalf("Get(missing) error = %v, want not found", err)
	}
}

func testAcademicUnitStoreListChildren(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	rootB := saveAcademicUnit(t, ctx, ss, institution.Id, "", "science")
	rootA := saveAcademicUnit(t, ctx, ss, institution.Id, "", "engineering")
	childB := saveAcademicUnit(t, ctx, ss, institution.Id, rootA.Id, "software")
	childA := saveAcademicUnit(t, ctx, ss, institution.Id, rootA.Id, "computing")

	roots, err := ss.AcademicUnit().ListChildren(ctx, institution.Id, "")
	requireNoError(t, err)
	if len(roots) != 2 || roots[0].Id != rootA.Id || roots[1].Id != rootB.Id {
		t.Fatalf("ListChildren(root) = %#v", roots)
	}
	children, err := ss.AcademicUnit().ListChildren(ctx, institution.Id, rootA.Id)
	requireNoError(t, err)
	if len(children) != 2 || children[0].Id != childA.Id || children[1].Id != childB.Id {
		t.Fatalf("ListChildren(%s) = %#v", rootA.Id, children)
	}
}

func testAcademicUnitStoreUpdate(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	firstRoot := saveAcademicUnit(t, ctx, ss, institution.Id, "", "engineering")
	secondRoot := saveAcademicUnit(t, ctx, ss, institution.Id, "", "science")
	child := saveAcademicUnit(t, ctx, ss, institution.Id, firstRoot.Id, "computing")

	child.ParentId = secondRoot.Id
	child.DisplayName = "Applied Computing"
	updated, err := ss.AcademicUnit().Update(ctx, child)
	requireNoError(t, err)
	if updated.ParentId != secondRoot.Id || updated.DisplayName != "Applied Computing" {
		t.Fatalf("Update() = %#v", updated)
	}

	missing := *updated
	missing.Id = model.NewId()
	_, err = ss.AcademicUnit().Update(ctx, &missing)
	if !store.IsNotFound(err) {
		t.Fatalf("Update(missing) error = %v, want not found", err)
	}
}

func testAcademicUnitStoreRejectCycle(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	root := saveAcademicUnit(t, ctx, ss, institution.Id, "", "engineering")
	child := saveAcademicUnit(t, ctx, ss, institution.Id, root.Id, "computing")
	grandchild := saveAcademicUnit(t, ctx, ss, institution.Id, child.Id, "software")

	root.ParentId = grandchild.Id
	_, err := ss.AcademicUnit().Update(ctx, root)
	var conflict *store.ErrConflict
	if !errors.As(err, &conflict) || conflict.Constraint != "academic_units_acyclic" {
		t.Fatalf("cycle Update() error = %v, want hierarchy conflict", err)
	}
}

func testAcademicUnitStoreRejectCrossInstitutionParent(t *testing.T, ss store.Store) {
	ctx := context.Background()
	first := saveInstitution(t, ctx, ss)
	parent := saveAcademicUnit(t, ctx, ss, first.Id, "", "engineering")
	requireNoError(t, ss.Institution().Delete(ctx, first.Id, model.GetMillis()))

	second, err := ss.Institution().Save(ctx, &model.Institution{
		Name:        "second-" + model.NewId(),
		DisplayName: "Second University",
	})
	requireNoError(t, err)

	_, err = ss.AcademicUnit().Save(ctx, &model.AcademicUnit{
		InstitutionId: second.Id,
		ParentId:      parent.Id,
		Name:          "computing",
		DisplayName:   "Computing",
	})
	var reference *store.ErrReference
	if !errors.As(err, &reference) ||
		reference.Constraint != "academic_units_parent_same_institution" {
		t.Fatalf("cross-institution parent error = %v, want reference error", err)
	}
}

func testAcademicUnitStoreEnforceInstitutionNameUniqueness(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	root := saveAcademicUnit(t, ctx, ss, institution.Id, "", "engineering")
	saveAcademicUnit(t, ctx, ss, institution.Id, root.Id, "computing")

	_, err := ss.AcademicUnit().Save(ctx, &model.AcademicUnit{
		InstitutionId: institution.Id,
		ParentId:      root.Id,
		Name:          "computing",
		DisplayName:   "Duplicate Computing",
	})
	var conflict *store.ErrConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("duplicate sibling name error = %v, want conflict", err)
	}

	_, err = ss.AcademicUnit().Save(ctx, &model.AcademicUnit{
		InstitutionId: institution.Id,
		Name:          "computing",
		DisplayName:   "Root Computing",
	})
	if !errors.As(err, &conflict) {
		t.Fatalf("duplicate name under another parent error = %v, want conflict", err)
	}
}

func saveAcademicUnit(
	t *testing.T,
	ctx context.Context,
	ss store.Store,
	institutionID string,
	parentID string,
	name string,
) *model.AcademicUnit {
	t.Helper()
	unit, err := ss.AcademicUnit().Save(ctx, &model.AcademicUnit{
		InstitutionId: institutionID,
		ParentId:      parentID,
		Name:          name,
		DisplayName:   name,
	})
	requireNoError(t, err)
	return unit
}
