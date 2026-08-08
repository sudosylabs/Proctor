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
		Resource:  model.Resource{Type: model.ResourceAcademicUnit, ID: unitID},
		ScopeType: model.RoleScopeAcademicUnit, ScopeID: unitID,
		Status: model.AuditStatusAttempt, NodeID: "test-node",
	})
	requireNoError(t, err)
	return attempt
}

func testAcademicUnitStoreMutationAuditAtomicity(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	unit := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "audited-update")
	attempt := saveAcademicUnitAuditAttempt(t, ctx, ss, unit.ID.String())
	candidate := *unit
	candidate.DisplayName = "Audited Update"
	candidate.PrepareUpdate(model.NowUTC())
	updated, err := ss.AcademicUnit().UpdateWithAudit(ctx, &store.AcademicUnitUpdate{
		Unit: &candidate, AuditEventID: attempt.ID.String(), AuditAt: model.GetMillis(),
	})
	requireNoError(t, err)
	if updated.DisplayName != "Audited Update" {
		t.Fatalf("UpdateWithAudit() = %#v", updated)
	}
	completed, err := ss.Audit().Get(ctx, attempt.ID.String())
	requireNoError(t, err)
	if completed.Status != model.AuditStatusSuccess {
		t.Fatalf("update audit status = %q, want success", completed.Status)
	}
	staleAttempt := saveAcademicUnitAuditAttempt(t, ctx, ss, unit.ID.String())
	stale := *unit
	stale.DisplayName = "Stale Update"
	stale.PrepareUpdate(model.NowUTC())
	if _, err := ss.AcademicUnit().UpdateWithAudit(ctx, &store.AcademicUnitUpdate{
		Unit: &stale, AuditEventID: staleAttempt.ID.String(), AuditAt: model.GetMillis(),
	}); !store.IsConflict(err) {
		t.Fatalf("stale UpdateWithAudit() error = %v, want conflict", err)
	}

	rolledBack := *updated
	rolledBack.DisplayName = "Must Roll Back"
	rolledBack.PrepareUpdate(model.NowUTC())
	_, err = ss.AcademicUnit().UpdateWithAudit(ctx, &store.AcademicUnitUpdate{
		Unit: &rolledBack, AuditEventID: model.NewId(), AuditAt: model.GetMillis(),
	})
	if err == nil {
		t.Fatal("UpdateWithAudit() succeeded without its audit attempt")
	}
	persisted, getErr := ss.AcademicUnit().Get(ctx, unit.ID.String())
	requireNoError(t, getErr)
	if persisted.DisplayName != updated.DisplayName {
		t.Fatalf("update survived audit rollback: %#v", persisted)
	}

	archiveUnit := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "audited-archive")
	archiveAttempt := saveAcademicUnitAuditAttempt(t, ctx, ss, archiveUnit.ID.String())
	archived, err := ss.AcademicUnit().ArchiveWithAudit(ctx, &store.AcademicUnitArchive{
		ID: archiveUnit.ID.String(), ArchiveAt: model.GetMillis(),
		AuditEventID: archiveAttempt.ID.String(), AuditAt: model.GetMillis(),
	})
	requireNoError(t, err)
	if archived.ArchivedAt.Millis() == 0 {
		t.Fatalf("ArchiveWithAudit() = %#v", archived)
	}
	completed, err = ss.Audit().Get(ctx, archiveAttempt.ID.String())
	requireNoError(t, err)
	if completed.Status != model.AuditStatusSuccess {
		t.Fatalf("archive audit status = %q, want success", completed.Status)
	}

	rollbackArchive := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "rollback-archive")
	_, err = ss.AcademicUnit().ArchiveWithAudit(ctx, &store.AcademicUnitArchive{
		ID: rollbackArchive.ID.String(), ArchiveAt: model.GetMillis(),
		AuditEventID: model.NewId(), AuditAt: model.GetMillis(),
	})
	if err == nil {
		t.Fatal("ArchiveWithAudit() succeeded without its audit attempt")
	}
	if _, getErr = ss.AcademicUnit().Get(ctx, rollbackArchive.ID.String()); getErr != nil {
		t.Fatalf("archive survived audit rollback: %v", getErr)
	}
}

func testAcademicUnitStoreCreateWithAudit(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	attempt, err := ss.Audit().Save(ctx, &model.AuditEvent{
		Action:    string(model.ActionInstitutionManage),
		Resource:  model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()},
		ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(),
		Status: model.AuditStatusAttempt, NodeID: "test-node",
	})
	requireNoError(t, err)
	unit := &model.AcademicUnit{
		InstitutionID: institution.ID, Name: "audited-engineering",
		DisplayName: "Audited Engineering",
	}
	unit.PrepareCreate(model.AcademicUnitID(model.NewId()), model.NowUTC())
	saved, err := ss.AcademicUnit().Create(ctx, &store.AcademicUnitCreation{
		Unit:         unit,
		AuditEventID: attempt.ID.String(),
		AuditAt:      model.GetMillis(),
	})
	requireNoError(t, err)
	completed, err := ss.Audit().Get(ctx, attempt.ID.String())
	requireNoError(t, err)
	if completed.Status != model.AuditStatusSuccess {
		t.Fatalf("audit status = %q, want success", completed.Status)
	}
	var result map[string]any
	if err := json.Unmarshal(completed.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result["id"] != saved.ID.String() {
		t.Fatalf("audit result = %#v, want unit ID %q", result, saved.ID.String())
	}

	rolledBack := &model.AcademicUnit{
		InstitutionID: institution.ID, Name: "rolled-back-unit",
		DisplayName: "Rolled Back Unit",
	}
	rolledBack.PrepareCreate(model.AcademicUnitID(model.NewId()), model.NowUTC())
	_, err = ss.AcademicUnit().Create(ctx, &store.AcademicUnitCreation{
		Unit:         rolledBack,
		AuditEventID: model.NewId(),
		AuditAt:      model.GetMillis(),
	})
	if err == nil {
		t.Fatal("Create() succeeded without its audit attempt")
	}
	found, searchErr := ss.AcademicUnit().Search(ctx, institution.ID.String(), "rolled-back-unit", 10)
	requireNoError(t, searchErr)
	if len(found) != 0 {
		t.Fatalf("unit survived audit rollback: %#v", found)
	}
}

func testAcademicUnitStoreSearchAndArchive(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	parent := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "archive-parent")
	child := saveAcademicUnit(t, ctx, ss, institution.ID.String(), parent.ID.String(), "distinct-computing")
	found, err := ss.AcademicUnit().Search(ctx, institution.ID.String(), "distinct-comput", 10)
	requireNoError(t, err)
	if len(found) != 1 || found[0].ID != child.ID {
		t.Fatalf("Search() = %#v", found)
	}
	if _, err = ss.AcademicUnit().Delete(ctx, parent.ID.String(), model.GetMillis()); !store.IsConflict(err) {
		t.Fatalf("Delete(parent with child) error = %v", err)
	}
	archived, err := ss.AcademicUnit().Delete(ctx, child.ID.String(), model.GetMillis())
	requireNoError(t, err)
	if archived.ArchivedAt.Millis() == 0 {
		t.Fatalf("Delete(child) = %#v", archived)
	}
	if _, err = ss.AcademicUnit().Get(ctx, child.ID.String()); !store.IsNotFound(err) {
		t.Fatalf("Get(archived) error = %v", err)
	}
}

func testAcademicUnitStoreListAncestors(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	root := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "engineering")
	child := saveAcademicUnit(t, ctx, ss, institution.ID.String(), root.ID.String(), "computing")
	leaf := saveAcademicUnit(t, ctx, ss, institution.ID.String(), child.ID.String(), "software")

	ancestors, err := ss.AcademicUnit().ListAncestors(ctx, leaf.ID.String())
	requireNoError(t, err)
	if len(ancestors) != 3 ||
		ancestors[0].ID != leaf.ID ||
		ancestors[1].ID != child.ID ||
		ancestors[2].ID != root.ID {
		t.Fatalf("ListAncestors() = %#v", ancestors)
	}
	if _, err := ss.AcademicUnit().ListAncestors(ctx, model.NewId()); !store.IsNotFound(err) {
		t.Fatalf("ListAncestors(missing) error = %v", err)
	}
}

func testAcademicUnitStoreSave(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	root := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "engineering")
	child := saveAcademicUnit(t, ctx, ss, institution.ID.String(), root.ID.String(), "computing")

	if !model.IsValidId(root.ID.String()) || child.ParentID != root.ID {
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
	unit := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "engineering")

	got, err := ss.AcademicUnit().Get(ctx, unit.ID.String())
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
	rootB := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "science")
	rootA := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "engineering")
	childB := saveAcademicUnit(t, ctx, ss, institution.ID.String(), rootA.ID.String(), "software")
	childA := saveAcademicUnit(t, ctx, ss, institution.ID.String(), rootA.ID.String(), "computing")

	roots, err := ss.AcademicUnit().ListChildren(ctx, institution.ID.String(), "")
	requireNoError(t, err)
	if len(roots) != 2 || roots[0].ID != rootA.ID || roots[1].ID != rootB.ID {
		t.Fatalf("ListChildren(root) = %#v", roots)
	}
	children, err := ss.AcademicUnit().ListChildren(ctx, institution.ID.String(), rootA.ID.String())
	requireNoError(t, err)
	if len(children) != 2 || children[0].ID != childA.ID || children[1].ID != childB.ID {
		t.Fatalf("ListChildren(%s) = %#v", rootA.ID.String(), children)
	}
}

func testAcademicUnitStoreUpdate(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	firstRoot := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "engineering")
	secondRoot := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "science")
	child := saveAcademicUnit(t, ctx, ss, institution.ID.String(), firstRoot.ID.String(), "computing")

	child.ParentID = secondRoot.ID
	child.DisplayName = "Applied Computing"
	updated, err := ss.AcademicUnit().Update(ctx, child)
	requireNoError(t, err)
	if updated.ParentID != secondRoot.ID || updated.DisplayName != "Applied Computing" {
		t.Fatalf("Update() = %#v", updated)
	}
	ancestors, err := ss.AcademicUnit().ListAncestors(ctx, updated.ID.String())
	requireNoError(t, err)
	if len(ancestors) != 2 || ancestors[0].ID != updated.ID ||
		ancestors[1].ID != secondRoot.ID {
		t.Fatalf("ancestors after reparent = %#v", ancestors)
	}
	for _, ancestor := range ancestors {
		if ancestor.ID == firstRoot.ID {
			t.Fatalf("old parent remains after reparent: %#v", ancestors)
		}
	}

	missing := *updated
	missing.ID = model.AcademicUnitID(model.NewId())
	_, err = ss.AcademicUnit().Update(ctx, &missing)
	if !store.IsNotFound(err) {
		t.Fatalf("Update(missing) error = %v, want not found", err)
	}
}

func testAcademicUnitStoreRejectCycle(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	root := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "engineering")
	child := saveAcademicUnit(t, ctx, ss, institution.ID.String(), root.ID.String(), "computing")
	grandchild := saveAcademicUnit(t, ctx, ss, institution.ID.String(), child.ID.String(), "software")

	root.ParentID = grandchild.ID
	_, err := ss.AcademicUnit().Update(ctx, root)
	var conflict *store.ErrConflict
	if !errors.As(err, &conflict) || conflict.Constraint != "academic_units_acyclic" {
		t.Fatalf("cycle Update() error = %v, want hierarchy conflict", err)
	}
}

func testAcademicUnitStoreRejectConcurrentCycle(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	first := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "concurrent-first")
	second := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "concurrent-second")
	first.ParentID = second.ID
	second.ParentID = first.ID

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
	if _, err := ss.AcademicUnit().ListAncestors(ctx, first.ID.String()); err != nil {
		t.Fatalf("first hierarchy after concurrent updates: %v", err)
	}
	if _, err := ss.AcademicUnit().ListAncestors(ctx, second.ID.String()); err != nil {
		t.Fatalf("second hierarchy after concurrent updates: %v", err)
	}
}

func testAcademicUnitStoreRejectCrossInstitutionParent(t *testing.T, ss store.Store) {
	ctx := context.Background()
	first := saveInstitution(t, ctx, ss)
	parent := saveAcademicUnit(t, ctx, ss, first.ID.String(), "", "engineering")
	requireNoError(t, ss.Institution().Delete(ctx, first.ID.String(), model.GetMillis()))

	second, err := ss.Institution().Save(ctx, &model.Institution{
		Name:        "second-" + model.NewId(),
		DisplayName: "Second University",
	})
	requireNoError(t, err)

	_, err = ss.AcademicUnit().Save(ctx, &model.AcademicUnit{
		InstitutionID: second.ID,
		ParentID:      parent.ID,
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
	root := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "engineering")
	saveAcademicUnit(t, ctx, ss, institution.ID.String(), root.ID.String(), "computing")

	_, err := ss.AcademicUnit().Save(ctx, &model.AcademicUnit{
		InstitutionID: institution.ID,
		ParentID:      root.ID,
		Name:          "computing",
		DisplayName:   "Duplicate Computing",
	})
	var conflict *store.ErrConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("duplicate sibling name error = %v, want conflict", err)
	}

	_, err = ss.AcademicUnit().Save(ctx, &model.AcademicUnit{
		InstitutionID: institution.ID,
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
		InstitutionID: model.InstitutionID(institutionID),
		ParentID:      model.AcademicUnitID(parentID),
		Name:          name,
		DisplayName:   name,
	})
	requireNoError(t, err)
	return unit
}
