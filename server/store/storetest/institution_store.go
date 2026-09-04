// ---------------------------------------------------------------------------------------------
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
//
// Adapted from Mattermost server/channels/store/storetest/team_store.go. The
// suite receives the root store contract and verifies each InstitutionStore
// operation independently so every future adapter can reuse the same tests.

package storetest

import (
	"context"
	"errors"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestInstitutionStore(t *testing.T, ss store.Store) {
	t.Run("Save", func(t *testing.T) { testInstitutionStoreSave(t, ss) })
	t.Run("Get", func(t *testing.T) { testInstitutionStoreGet(t, ss) })
	t.Run("GetSingleton", func(t *testing.T) { testInstitutionStoreGetSingleton(t, ss) })
	t.Run("Update", func(t *testing.T) { testInstitutionStoreUpdate(t, ss) })
	t.Run("UpdateWithAudit", func(t *testing.T) { testInstitutionStoreUpdateWithAudit(t, ss) })
	t.Run("Archive", func(t *testing.T) { testInstitutionStoreArchive(t, ss) })
}

func testInstitutionStoreUpdateWithAudit(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	attempt, err := ss.Audit().Save(ctx, &model.AuditEvent{
		Action:    string(model.ActionInstitutionManage),
		Resource:  model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()},
		ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(),
		Status: model.AuditStatusAttempt, NodeID: "test-node",
	})
	requireNoError(t, err)
	candidate := *institution
	candidate.DisplayName = "Audited Northbridge"
	candidate.PrepareUpdate(model.NowUTC())
	updated, err := ss.Institution().UpdateWithAudit(ctx, &store.InstitutionUpdate{
		Institution: &candidate, AuditEventID: attempt.ID.String(), AuditAt: model.GetMillis(),
	})
	requireNoError(t, err)
	if updated.DisplayName != "Audited Northbridge" {
		t.Fatalf("UpdateWithAudit() = %#v", updated)
	}
	completed, err := ss.Audit().Get(ctx, attempt.ID.String())
	requireNoError(t, err)
	if completed.Status != model.AuditStatusSuccess {
		t.Fatalf("audit status = %q", completed.Status)
	}
	staleAttempt, err := ss.Audit().Save(ctx, &model.AuditEvent{
		Action: string(model.ActionInstitutionManage), Resource: model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()},
		ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(), Status: model.AuditStatusAttempt, NodeID: "test-node",
	})
	requireNoError(t, err)
	stale := *institution
	stale.DisplayName = "Stale Northbridge"
	stale.PrepareUpdate(model.NowUTC())
	if _, err := ss.Institution().UpdateWithAudit(ctx, &store.InstitutionUpdate{
		Institution: &stale, AuditEventID: staleAttempt.ID.String(), AuditAt: model.GetMillis(),
	}); !store.IsConflict(err) {
		t.Fatalf("stale UpdateWithAudit() error = %v, want conflict", err)
	}

	rolledBack := *updated
	rolledBack.DisplayName = "Must Roll Back"
	rolledBack.PrepareUpdate(model.NowUTC())
	_, err = ss.Institution().UpdateWithAudit(ctx, &store.InstitutionUpdate{
		Institution: &rolledBack, AuditEventID: model.NewId(), AuditAt: model.GetMillis(),
	})
	if err == nil {
		t.Fatal("UpdateWithAudit() succeeded without its audit attempt")
	}
	persisted, getErr := ss.Institution().GetSingleton(ctx)
	requireNoError(t, getErr)
	if persisted.DisplayName != updated.DisplayName {
		t.Fatalf("update survived audit rollback: %#v", persisted)
	}
}

func testInstitutionStoreSave(t *testing.T, ss store.Store) {
	ctx := context.Background()
	cleanupInstitution(t, ctx, ss)

	institution := &model.Institution{
		Name:        "northbridge",
		DisplayName: "Northbridge University",
		Description: "Primary institution",
	}
	saved, err := ss.Institution().Save(ctx, institution)
	requireNoError(t, err)
	if !model.IsValidId(saved.ID.String()) {
		t.Fatalf("Save() id = %q", saved.ID.String())
	}
	if !institution.ID.IsZero() {
		t.Fatalf("Save() mutated input id to %q", institution.ID.String())
	}

	_, err = ss.Institution().Save(ctx, saved)
	var invalid *store.ErrInvalidInput
	if !errors.As(err, &invalid) {
		t.Fatalf("second Save(saved) error = %v, want invalid input", err)
	}

	_, err = ss.Institution().Save(ctx, &model.Institution{
		Name:        "second",
		DisplayName: "Second University",
	})
	var conflict *store.ErrConflict
	if !errors.As(err, &conflict) || conflict.Constraint != "institutions_singleton_key" {
		t.Fatalf("second active institution error = %v, want singleton conflict", err)
	}
}

func testInstitutionStoreGet(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)

	got, err := ss.Institution().Get(ctx, institution.ID.String())
	requireNoError(t, err)
	if *got != *institution {
		t.Fatalf("Get() = %#v, want %#v", got, institution)
	}

	_, err = ss.Institution().Get(ctx, model.NewId())
	if !store.IsNotFound(err) {
		t.Fatalf("Get(missing) error = %v, want not found", err)
	}
}

func testInstitutionStoreGetSingleton(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)

	got, err := ss.Institution().GetSingleton(ctx)
	requireNoError(t, err)
	if got.ID.String() != institution.ID.String() {
		t.Fatalf("GetSingleton() id = %q, want %q", got.ID.String(), institution.ID.String())
	}
}

func testInstitutionStoreUpdate(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	createAt := institution.CreatedAt

	institution.DisplayName = "Northbridge"
	capacity := model.DefaultExamCapacityPolicy()
	capacity.ResourceMaximumCount = 25
	capacity.WorkspaceMaximumEntries = 750
	institution.ExamCapacity = capacity
	updated, err := ss.Institution().Update(ctx, institution)
	requireNoError(t, err)
	if updated.DisplayName != "Northbridge" {
		t.Fatalf("Update() display name = %q", updated.DisplayName)
	}
	if updated.ExamCapacity != capacity {
		t.Fatalf("Update() Exam capacity = %#v", updated.ExamCapacity)
	}
	if !updated.CreatedAt.Equal(createAt) || updated.UpdatedAt.Before(institution.UpdatedAt) {
		t.Fatalf("Update() timestamps = %#v", updated)
	}

	missing := *updated
	missingID, parseErr := model.ParseInstitutionID(model.NewId())
	requireNoError(t, parseErr)
	missing.ID = missingID
	_, err = ss.Institution().Update(ctx, &missing)
	if !store.IsNotFound(err) {
		t.Fatalf("Update(missing) error = %v, want not found", err)
	}
}

func testInstitutionStoreArchive(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)

	if err := ss.Institution().Archive(ctx, institution.ID.String(), model.GetMillis()); err != nil {
		t.Fatalf("Archive() error = %v", err)
	}
	if _, err := ss.Institution().Get(ctx, institution.ID.String()); !store.IsNotFound(err) {
		t.Fatalf("Get(archived) error = %v, want not found", err)
	}
	if err := ss.Institution().Archive(ctx, institution.ID.String(), model.GetMillis()); !store.IsNotFound(err) {
		t.Fatalf("second Archive() error = %v, want not found", err)
	}
	if _, err := ss.Institution().Save(ctx, &model.Institution{
		Name:        "replacement",
		DisplayName: "Replacement University",
	}); err != nil {
		t.Fatalf("Save() after archive error = %v", err)
	}
}

func saveInstitution(t *testing.T, ctx context.Context, ss store.Store) *model.Institution {
	t.Helper()
	cleanupInstitution(t, ctx, ss)
	institution, err := ss.Institution().Save(ctx, &model.Institution{
		Name:        "institution-" + model.NewId(),
		DisplayName: "Northbridge University",
	})
	requireNoError(t, err)
	t.Cleanup(func() { cleanupInstitution(t, context.Background(), ss) })
	return institution
}

func cleanupInstitution(t *testing.T, ctx context.Context, ss store.Store) {
	t.Helper()
	institution, err := ss.Institution().GetSingleton(ctx)
	if store.IsNotFound(err) {
		return
	}
	requireNoError(t, err)
	if err := ss.Institution().Archive(ctx, institution.ID.String(), model.GetMillis()); err != nil {
		t.Fatalf("cleanup institution: %v", err)
	}
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
