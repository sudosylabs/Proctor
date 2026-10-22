// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

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
	require.NoError(t, err)
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
	updated, err := ss.AcademicUnit().Update(ctx, &store.AcademicUnitUpdate{
		Unit: &candidate, AuditEventID: attempt.ID, AuditAt: model.GetMillis(),
	})
	require.NoError(t, err)
	require.Equal(t, "Audited Update", updated.DisplayName)
	completed, err := ss.Audit().Get(ctx, attempt.ID)
	require.NoError(t, err)
	require.Equal(t, model.AuditStatusSuccess, completed.Status, "update audit status")
	staleAttempt := saveAcademicUnitAuditAttempt(t, ctx, ss, unit.ID.String())
	stale := *unit
	stale.DisplayName = "Stale Update"
	stale.PrepareUpdate(model.NowUTC())
	_, err = ss.AcademicUnit().Update(ctx, &store.AcademicUnitUpdate{
		Unit: &stale, AuditEventID: staleAttempt.ID, AuditAt: model.GetMillis(),
	})
	require.True(t, store.IsConflict(err), "stale Update() error")

	rolledBack := *updated
	rolledBack.DisplayName = "Must Roll Back"
	rolledBack.PrepareUpdate(model.NowUTC())
	_, err = ss.AcademicUnit().Update(ctx, &store.AcademicUnitUpdate{
		Unit: &rolledBack, AuditEventID: model.NewAuditEventID(), AuditAt: model.GetMillis(),
	})
	require.Error(t, err, "Update() succeeded without its audit attempt")
	persisted, getErr := ss.AcademicUnit().Get(ctx, unit.ID)
	require.NoError(t, getErr)
	require.Equal(t, updated.DisplayName, persisted.DisplayName)

	archiveUnit := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "audited-archive")
	archiveAttempt := saveAcademicUnitAuditAttempt(t, ctx, ss, archiveUnit.ID.String())
	archived, err := ss.AcademicUnit().Archive(ctx, &store.AcademicUnitArchive{
		ID: archiveUnit.ID, ArchiveAt: model.GetMillis(),
		AuditEventID: archiveAttempt.ID, AuditAt: model.GetMillis(),
	})
	require.NoError(t, err)
	require.NotEqual(t, int64(0), archived.ArchivedAt.Millis())
	completed, err = ss.Audit().Get(ctx, archiveAttempt.ID)
	require.NoError(t, err)
	require.Equal(t, model.AuditStatusSuccess, completed.Status, "archive audit status")

	rollbackArchive := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "rollback-archive")
	_, err = ss.AcademicUnit().Archive(ctx, &store.AcademicUnitArchive{
		ID: rollbackArchive.ID, ArchiveAt: model.GetMillis(),
		AuditEventID: model.NewAuditEventID(), AuditAt: model.GetMillis(),
	})
	require.Error(t, err, "Archive() succeeded without its audit attempt")
	_, getErr = ss.AcademicUnit().Get(ctx, rollbackArchive.ID)
	require.NoError(t, getErr)
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
	require.NoError(t, err)
	unit := &model.AcademicUnit{
		InstitutionID: institution.ID, Name: "audited-engineering",
		DisplayName: "Audited Engineering",
	}
	unit.PrepareCreate(model.AcademicUnitID(model.NewID()), model.NowUTC())
	saved, err := ss.AcademicUnit().Create(ctx, &store.AcademicUnitCreation{
		Unit:         unit,
		AuditEventID: attempt.ID,
		AuditAt:      model.GetMillis(),
	})
	require.NoError(t, err)
	completed, err := ss.Audit().Get(ctx, attempt.ID)
	require.NoError(t, err)
	require.Equal(t, model.AuditStatusSuccess, completed.Status, "audit status")
	var result map[string]any
	require.NoError(t, json.Unmarshal(completed.Result, &result))
	require.Equal(t, saved.ID.String(), result["id"], "audit result = %#v, want unit ID", result)

	rolledBack := &model.AcademicUnit{
		InstitutionID: institution.ID, Name: "rolled-back-unit",
		DisplayName: "Rolled Back Unit",
	}
	rolledBack.PrepareCreate(model.AcademicUnitID(model.NewID()), model.NowUTC())
	_, err = ss.AcademicUnit().Create(ctx, &store.AcademicUnitCreation{
		Unit:         rolledBack,
		AuditEventID: model.NewAuditEventID(),
		AuditAt:      model.GetMillis(),
	})
	require.Error(t, err, "Create() succeeded without its audit attempt")
	found, searchErr := ss.AcademicUnit().Search(ctx, institution.ID, store.AcademicNameListOptions{Term: "rolled-back-unit", Limit: 10})
	require.NoError(t, searchErr)
	require.Empty(t, found)
}

func testAcademicUnitStoreSearchAndArchive(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	parent := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "archive-parent")
	child := saveAcademicUnit(t, ctx, ss, institution.ID.String(), parent.ID.String(), "distinct-computing")
	child.DisplayName = "Literal 50%_! Computing"
	child, err := UpdateAcademicUnit(ctx, ss, child)
	require.NoError(t, err)
	found, err := ss.AcademicUnit().Search(ctx, institution.ID, store.AcademicNameListOptions{Term: "distinct-comput", Limit: 10})
	require.NoError(t, err)
	require.Len(t, found, 1)
	require.Equal(t, child.ID, found[0].ID)
	for _, query := range []string{"LITERAL 50%_!", "%", "_", "!"} {
		found, err = ss.AcademicUnit().Search(ctx, institution.ID, store.AcademicNameListOptions{Term: query, Limit: 10})
		require.NoError(t, err)
		require.Len(t, found, 1, "Search(%q)", query)
		require.Equal(t, child.ID, found[0].ID, "Search(%q)", query)
	}
	exact, err := ss.AcademicUnit().GetByName(ctx, institution.ID, child.Name)
	require.NoError(t, err)
	require.Equal(t, child.ID, exact.ID)
	otherInstitution := saveInstitution(t, ctx, ss)
	_, err = ss.AcademicUnit().GetByName(ctx, otherInstitution.ID, child.Name)
	require.True(t, store.IsNotFound(err), "cross-Institution GetByName() error")
	_, err = ArchiveAcademicUnit(ctx, ss, parent.ID, model.GetMillis())
	require.True(t, store.IsConflict(err), "Archive(parent with child) error")
	archived, err := ArchiveAcademicUnit(ctx, ss, child.ID, model.GetMillis())
	require.NoError(t, err)
	require.NotEqual(t, int64(0), archived.ArchivedAt.Millis())
	_, err = ss.AcademicUnit().Get(ctx, child.ID)
	require.True(t, store.IsNotFound(err), "Get(archived) error")
}

func testAcademicUnitStoreListAncestors(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	root := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "engineering")
	child := saveAcademicUnit(t, ctx, ss, institution.ID.String(), root.ID.String(), "computing")
	leaf := saveAcademicUnit(t, ctx, ss, institution.ID.String(), child.ID.String(), "software")

	ancestors, err := ss.AcademicUnit().ListAncestors(ctx, leaf.ID)
	require.NoError(t, err)
	require.Len(t, ancestors, 3)
	require.Equal(t, leaf.ID, ancestors[0].ID)
	require.Equal(t, child.ID, ancestors[1].ID)
	require.Equal(t, root.ID, ancestors[2].ID)
	_, err = ss.AcademicUnit().ListAncestors(ctx, model.NewAcademicUnitID())
	require.True(t, store.IsNotFound(err), "ListAncestors(missing) error")
}

func testAcademicUnitStoreSave(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	root := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "engineering")
	child := saveAcademicUnit(t, ctx, ss, institution.ID.String(), root.ID.String(), "computing")

	require.True(t, root.ID.IsValid(), "saved units = root %#v, child %#v", root, child)
	require.Equal(t, root.ID, child.ParentID, "saved units = root %#v, child %#v", root, child)

	_, err := SaveAcademicUnit(ctx, ss, child)
	var invalid *store.ErrInvalidInput
	require.ErrorAs(t, err, &invalid, "second Save(child) error")
}

func testAcademicUnitStoreGet(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	unit := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "engineering")

	got, err := ss.AcademicUnit().Get(ctx, unit.ID)
	require.NoError(t, err)
	require.Equal(t, *unit, *got, "Get() = %#v, want %#v", got, unit)
	_, err = ss.AcademicUnit().Get(ctx, model.NewAcademicUnitID())
	require.True(t, store.IsNotFound(err), "Get(missing) error")
}

func testAcademicUnitStoreListChildren(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	rootB := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "science")
	rootA := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "engineering")
	childB := saveAcademicUnit(t, ctx, ss, institution.ID.String(), rootA.ID.String(), "software")
	childA := saveAcademicUnit(t, ctx, ss, institution.ID.String(), rootA.ID.String(), "computing")

	roots, err := ss.AcademicUnit().ListChildren(ctx, institution.ID, "", store.AcademicNameListOptions{Limit: 200})
	require.NoError(t, err)
	require.Len(t, roots, 2)
	require.Equal(t, rootA.ID, roots[0].ID)
	require.Equal(t, rootB.ID, roots[1].ID)
	children, err := ss.AcademicUnit().ListChildren(ctx, institution.ID, rootA.ID, store.AcademicNameListOptions{Limit: 200})
	require.NoError(t, err)
	require.Len(t, children, 2, "ListChildren(%s)", rootA.ID.String())
	require.Equal(t, childA.ID, children[0].ID, "ListChildren(%s)", rootA.ID.String())
	require.Equal(t, childB.ID, children[1].ID, "ListChildren(%s)", rootA.ID.String())

	first, err := ss.AcademicUnit().ListChildren(ctx, institution.ID, "", store.AcademicNameListOptions{Limit: 1})
	require.NoError(t, err)
	require.Len(t, first, 1)
	require.Equal(t, rootA.ID, first[0].ID, "first page")
	next, err := ss.AcademicUnit().ListChildren(ctx, institution.ID, "", store.AcademicNameListOptions{
		AfterName: first[0].Name, AfterID: first[0].ID.String(), Limit: 201,
	})
	require.NoError(t, err)
	require.Len(t, next, 1, "continuation after (name, id)")
	require.Equal(t, rootB.ID, next[0].ID, "continuation after (name, id)")
	searched, err := ss.AcademicUnit().Search(ctx, institution.ID, store.AcademicNameListOptions{
		AfterName: rootA.Name, AfterID: rootA.ID.String(), Limit: 10,
	})
	require.NoError(t, err)
	for _, unit := range searched {
		require.Greater(t, unit.Name, rootA.Name, "search continuation stays after its keyset")
	}
	for _, invalid := range []store.AcademicNameListOptions{
		{Limit: 0}, {Limit: 202}, {AfterName: "x", Limit: 1}, {AfterID: rootA.ID.String(), Limit: 1}, {AfterName: "x", AfterID: "bad", Limit: 1},
	} {
		_, err = ss.AcademicUnit().ListChildren(ctx, institution.ID, "", invalid)
		var invalidInput *store.ErrInvalidInput
		require.ErrorAs(t, err, &invalidInput, "invalid options %#v", invalid)
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
	updated, err := UpdateAcademicUnit(ctx, ss, child)
	require.NoError(t, err)
	require.Equal(t, secondRoot.ID, updated.ParentID)
	require.Equal(t, "Applied Computing", updated.DisplayName)
	ancestors, err := ss.AcademicUnit().ListAncestors(ctx, updated.ID)
	require.NoError(t, err)
	require.Len(t, ancestors, 2)
	require.Equal(t, updated.ID, ancestors[0].ID)
	require.Equal(t, secondRoot.ID, ancestors[1].ID)
	for _, ancestor := range ancestors {
		require.NotEqual(t, firstRoot.ID, ancestor.ID, "old parent remains after reparent: %#v", ancestors)
	}

	missing := *updated
	missing.ID = model.AcademicUnitID(model.NewID())
	_, err = UpdateAcademicUnit(ctx, ss, &missing)
	require.True(t, store.IsNotFound(err), "Update(missing) error")
}

func testAcademicUnitStoreRejectCycle(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	root := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "engineering")
	child := saveAcademicUnit(t, ctx, ss, institution.ID.String(), root.ID.String(), "computing")
	grandchild := saveAcademicUnit(t, ctx, ss, institution.ID.String(), child.ID.String(), "software")

	root.ParentID = grandchild.ID
	_, err := UpdateAcademicUnit(ctx, ss, root)
	var conflict *store.ErrConflict
	require.ErrorAs(t, err, &conflict, "cycle Update() error")
	require.Equal(t, "academic_units_acyclic", conflict.Constraint, "cycle Update() error")
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
		_, err := UpdateAcademicUnit(ctx, ss, unit)
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
			require.FailNow(t, fmt.Sprintf("concurrent Update() error = %v", err))
		}
	}
	require.Equal(t, 1, successes, "concurrent updates: successes")
	require.Equal(t, 1, conflicts, "concurrent updates: successes")
	_, err := ss.AcademicUnit().ListAncestors(ctx, first.ID)
	require.NoError(t, err)
	_, err = ss.AcademicUnit().ListAncestors(ctx, second.ID)
	require.NoError(t, err)
}

func testAcademicUnitStoreRejectCrossInstitutionParent(t *testing.T, ss store.Store) {
	ctx := context.Background()
	first := saveInstitution(t, ctx, ss)
	parent := saveAcademicUnit(t, ctx, ss, first.ID.String(), "", "engineering")
	require.NoError(t, ss.Institution().Archive(ctx, first.ID, model.GetMillis()))

	second, err := ss.Institution().Save(ctx, &model.Institution{
		Name:        "second-" + model.NewID(),
		DisplayName: "Second University",
	})
	require.NoError(t, err)

	_, err = SaveAcademicUnit(ctx, ss, &model.AcademicUnit{
		InstitutionID: second.ID,
		ParentID:      parent.ID,
		Name:          "computing",
		DisplayName:   "Computing",
	})
	var reference *store.ErrReference
	require.ErrorAs(t, err, &reference, "cross-institution parent error")
	require.Equal(t, "academic_units_parent_same_institution", reference.Constraint, "cross-institution parent error")
}

func testAcademicUnitStoreEnforceInstitutionNameUniqueness(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	root := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "engineering")
	saveAcademicUnit(t, ctx, ss, institution.ID.String(), root.ID.String(), "computing")

	_, err := SaveAcademicUnit(ctx, ss, &model.AcademicUnit{
		InstitutionID: institution.ID,
		ParentID:      root.ID,
		Name:          "computing",
		DisplayName:   "Duplicate Computing",
	})
	var conflict *store.ErrConflict
	require.ErrorAs(t, err, &conflict, "duplicate sibling name error")

	_, err = SaveAcademicUnit(ctx, ss, &model.AcademicUnit{
		InstitutionID: institution.ID,
		Name:          "computing",
		DisplayName:   "Root Computing",
	})
	require.ErrorAs(t, err, &conflict, "duplicate name under another parent error")
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
	unit, err := SaveAcademicUnit(ctx, ss, &model.AcademicUnit{
		InstitutionID: model.InstitutionID(institutionID),
		ParentID:      model.AcademicUnitID(parentID),
		Name:          name,
		DisplayName:   name,
	})
	require.NoError(t, err)
	return unit
}
