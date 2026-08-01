// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestAcademicUnitStore(t *testing.T, ss store.Store) {
	t.Run("CreateWithAudit", func(t *testing.T) { testAcademicUnitStoreCreateWithAudit(t, ss) })
	t.Run("Save", func(t *testing.T) { testAcademicUnitStoreSave(t, ss) })
	t.Run("Get", func(t *testing.T) { testAcademicUnitStoreGet(t, ss) })
	t.Run("ListChildren", func(t *testing.T) { testAcademicUnitStoreListChildren(t, ss) })
	t.Run("ListAncestors", func(t *testing.T) { testAcademicUnitStoreListAncestors(t, ss) })
	t.Run("Update", func(t *testing.T) { testAcademicUnitStoreUpdate(t, ss) })
	t.Run("MutationAuditAtomicity", func(t *testing.T) {
		testAcademicUnitStoreMutationAuditAtomicity(t, ss)
	})
	t.Run("RejectCycle", func(t *testing.T) { testAcademicUnitStoreRejectCycle(t, ss) })
	t.Run("RejectConcurrentCycle", func(t *testing.T) {
		testAcademicUnitStoreRejectConcurrentCycle(t, ss)
	})
	t.Run("RejectCrossInstitutionParent", func(t *testing.T) {
		testAcademicUnitStoreRejectCrossInstitutionParent(t, ss)
	})
	t.Run("EnforceInstitutionNameUniqueness", func(t *testing.T) {
		testAcademicUnitStoreEnforceInstitutionNameUniqueness(t, ss)
	})
	t.Run("SearchAndArchive", func(t *testing.T) {
		testAcademicUnitStoreSearchAndArchive(t, ss)
	})
}

func saveAcademicUnitAuditAttempt(
	t *testing.T,
	ctx context.Context,
	ss store.Store,
	unitID string,
) *model.AuditEvent {
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

func testAcademicUnitStoreMutationAuditAtomicity(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	unit := saveAcademicUnit(t, ctx, ss, institution.Id, "", "audited-update")
	attempt := saveAcademicUnitAuditAttempt(t, ctx, ss, unit.Id)
	candidate := *unit
	candidate.DisplayName = "Audited Update"
	candidate.PrepareUpdate(model.GetMillis())
	updated, err := ss.AcademicUnit().UpdateWithAudit(ctx, &store.AcademicUnitUpdate{
		Unit: &candidate, AuditEventID: attempt.Id, AuditAt: model.GetMillis(),
	})
	requireNoError(t, err)
	if updated.DisplayName != "Audited Update" {
		t.Fatalf("UpdateWithAudit() = %#v", updated)
	}
	completed, err := ss.Audit().Get(ctx, attempt.Id)
	requireNoError(t, err)
	if completed.Status != model.AuditStatusSuccess {
		t.Fatalf("update audit status = %q, want success", completed.Status)
	}

	rolledBack := *updated
	rolledBack.DisplayName = "Must Roll Back"
	rolledBack.PrepareUpdate(model.GetMillis())
	_, err = ss.AcademicUnit().UpdateWithAudit(ctx, &store.AcademicUnitUpdate{
		Unit: &rolledBack, AuditEventID: model.NewId(), AuditAt: model.GetMillis(),
	})
	if err == nil {
		t.Fatal("UpdateWithAudit() succeeded without its audit attempt")
	}
	persisted, getErr := ss.AcademicUnit().Get(ctx, unit.Id)
	requireNoError(t, getErr)
	if persisted.DisplayName != updated.DisplayName {
		t.Fatalf("update survived audit rollback: %#v", persisted)
	}

	archiveUnit := saveAcademicUnit(t, ctx, ss, institution.Id, "", "audited-archive")
	archiveAttempt := saveAcademicUnitAuditAttempt(t, ctx, ss, archiveUnit.Id)
	archived, err := ss.AcademicUnit().ArchiveWithAudit(ctx, &store.AcademicUnitArchive{
		ID: archiveUnit.Id, ArchiveAt: model.GetMillis(),
		AuditEventID: archiveAttempt.Id, AuditAt: model.GetMillis(),
	})
	requireNoError(t, err)
	if archived.DeleteAt == 0 {
		t.Fatalf("ArchiveWithAudit() = %#v", archived)
	}
	completed, err = ss.Audit().Get(ctx, archiveAttempt.Id)
	requireNoError(t, err)
	if completed.Status != model.AuditStatusSuccess {
		t.Fatalf("archive audit status = %q, want success", completed.Status)
	}

	rollbackArchive := saveAcademicUnit(t, ctx, ss, institution.Id, "", "rollback-archive")
	_, err = ss.AcademicUnit().ArchiveWithAudit(ctx, &store.AcademicUnitArchive{
		ID: rollbackArchive.Id, ArchiveAt: model.GetMillis(),
		AuditEventID: model.NewId(), AuditAt: model.GetMillis(),
	})
	if err == nil {
		t.Fatal("ArchiveWithAudit() succeeded without its audit attempt")
	}
	if _, getErr = ss.AcademicUnit().Get(ctx, rollbackArchive.Id); getErr != nil {
		t.Fatalf("archive survived audit rollback: %v", getErr)
	}
}

func testAcademicUnitStoreCreateWithAudit(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	attempt, err := ss.Audit().Save(ctx, &model.AuditEvent{
		Action:    string(model.ActionInstitutionManage),
		Resource:  model.Resource{Type: model.ResourceInstitution, Id: institution.Id},
		ScopeType: model.RoleScopeInstitution, ScopeId: institution.Id,
		Status: model.AuditStatusAttempt, NodeId: "test-node",
	})
	requireNoError(t, err)
	unit := &model.AcademicUnit{
		InstitutionId: institution.Id, Name: "audited-engineering",
		DisplayName: "Audited Engineering",
	}
	unit.PrepareCreate(model.NewId(), model.GetMillis())
	saved, err := ss.AcademicUnit().Create(ctx, &store.AcademicUnitCreation{
		Unit:         unit,
		AuditEventID: attempt.Id,
		AuditAt:      model.GetMillis(),
	})
	requireNoError(t, err)
	completed, err := ss.Audit().Get(ctx, attempt.Id)
	requireNoError(t, err)
	if completed.Status != model.AuditStatusSuccess {
		t.Fatalf("audit status = %q, want success", completed.Status)
	}
	var result map[string]any
	if err := json.Unmarshal(completed.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result["id"] != saved.Id {
		t.Fatalf("audit result = %#v, want unit ID %q", result, saved.Id)
	}

	rolledBack := &model.AcademicUnit{
		InstitutionId: institution.Id, Name: "rolled-back-unit",
		DisplayName: "Rolled Back Unit",
	}
	rolledBack.PrepareCreate(model.NewId(), model.GetMillis())
	_, err = ss.AcademicUnit().Create(ctx, &store.AcademicUnitCreation{
		Unit:         rolledBack,
		AuditEventID: model.NewId(),
		AuditAt:      model.GetMillis(),
	})
	if err == nil {
		t.Fatal("Create() succeeded without its audit attempt")
	}
	found, searchErr := ss.AcademicUnit().Search(ctx, institution.Id, "rolled-back-unit", 10)
	requireNoError(t, searchErr)
	if len(found) != 0 {
		t.Fatalf("unit survived audit rollback: %#v", found)
	}
}

func testAcademicUnitStoreSearchAndArchive(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	parent := saveAcademicUnit(t, ctx, ss, institution.Id, "", "archive-parent")
	child := saveAcademicUnit(t, ctx, ss, institution.Id, parent.Id, "distinct-computing")
	found, err := ss.AcademicUnit().Search(ctx, institution.Id, "distinct-comput", 10)
	requireNoError(t, err)
	if len(found) != 1 || found[0].Id != child.Id {
		t.Fatalf("Search() = %#v", found)
	}
	if _, err = ss.AcademicUnit().Delete(ctx, parent.Id, model.GetMillis()); !store.IsConflict(err) {
		t.Fatalf("Delete(parent with child) error = %v", err)
	}
	archived, err := ss.AcademicUnit().Delete(ctx, child.Id, model.GetMillis())
	requireNoError(t, err)
	if archived.DeleteAt == 0 {
		t.Fatalf("Delete(child) = %#v", archived)
	}
	if _, err = ss.AcademicUnit().Get(ctx, child.Id); !store.IsNotFound(err) {
		t.Fatalf("Get(archived) error = %v", err)
	}
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
	ancestors, err := ss.AcademicUnit().ListAncestors(ctx, updated.Id)
	requireNoError(t, err)
	if len(ancestors) != 2 || ancestors[0].Id != updated.Id ||
		ancestors[1].Id != secondRoot.Id {
		t.Fatalf("ancestors after reparent = %#v", ancestors)
	}
	for _, ancestor := range ancestors {
		if ancestor.Id == firstRoot.Id {
			t.Fatalf("old parent remains after reparent: %#v", ancestors)
		}
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

func testAcademicUnitStoreRejectConcurrentCycle(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	first := saveAcademicUnit(t, ctx, ss, institution.Id, "", "concurrent-first")
	second := saveAcademicUnit(t, ctx, ss, institution.Id, "", "concurrent-second")
	first.ParentId = second.Id
	second.ParentId = first.Id

	start := make(chan struct{})
	errorsByUpdate := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	update := func(unit *model.AcademicUnit) {
		defer ready.Done()
		<-start
		_, err := ss.AcademicUnit().Update(ctx, unit)
		errorsByUpdate <- err
	}
	go update(first)
	go update(second)
	close(start)
	ready.Wait()
	close(errorsByUpdate)

	successes, conflicts := 0, 0
	for err := range errorsByUpdate {
		switch {
		case err == nil:
			successes++
		case store.IsConflict(err):
			conflicts++
		default:
			t.Fatalf("concurrent Update() error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent updates: successes=%d conflicts=%d", successes, conflicts)
	}
	if _, err := ss.AcademicUnit().ListAncestors(ctx, first.Id); err != nil {
		t.Fatalf("first hierarchy after concurrent updates: %v", err)
	}
	if _, err := ss.AcademicUnit().ListAncestors(ctx, second.Id); err != nil {
		t.Fatalf("second hierarchy after concurrent updates: %v", err)
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
