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
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestInstitutionStore(t *testing.T, ss store.Store) {
	t.Run("Save", func(t *testing.T) { testInstitutionStoreSave(t, ss) })
	t.Run("Get", func(t *testing.T) { testInstitutionStoreGet(t, ss) })
	t.Run("GetSingleton", func(t *testing.T) { testInstitutionStoreGetSingleton(t, ss) })
	t.Run("Update", func(t *testing.T) { testInstitutionStoreUpdate(t, ss) })
	t.Run("Update", func(t *testing.T) { testInstitutionStoreUpdateWithAudit(t, ss) })
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
	require.NoError(t, err)
	candidate := *institution
	candidate.DisplayName = "Audited Northbridge"
	candidate.PrepareUpdate(model.NowUTC())
	updated, err := ss.Institution().Update(ctx, &store.InstitutionUpdate{
		Institution: &candidate, AuditEventID: attempt.ID, AuditAt: model.NowUTC(),
	})
	require.NoError(t, err)
	require.Equal(t, "Audited Northbridge", updated.DisplayName)
	completed, err := ss.Audit().Get(ctx, attempt.ID)
	require.NoError(t, err)
	require.Equal(t, model.AuditStatusSuccess, completed.Status)
	staleAttempt, err := ss.Audit().Save(ctx, &model.AuditEvent{
		Action: string(model.ActionInstitutionManage), Resource: model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()},
		ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(), Status: model.AuditStatusAttempt, NodeID: "test-node",
	})
	require.NoError(t, err)
	stale := *institution
	stale.DisplayName = "Stale Northbridge"
	stale.PrepareUpdate(model.NowUTC())
	_, err = ss.Institution().Update(ctx, &store.InstitutionUpdate{
		Institution: &stale, AuditEventID: staleAttempt.ID, AuditAt: model.NowUTC(),
	})
	require.True(t, store.IsConflict(err), "stale Update() error")

	rolledBack := *updated
	rolledBack.DisplayName = "Must Roll Back"
	rolledBack.PrepareUpdate(model.NowUTC())
	_, err = ss.Institution().Update(ctx, &store.InstitutionUpdate{
		Institution: &rolledBack, AuditEventID: model.NewAuditEventID(), AuditAt: model.NowUTC(),
	})
	require.Error(t, err, "Update() succeeded without its audit attempt")
	persisted, getErr := ss.Institution().GetSingleton(ctx)
	require.NoError(t, getErr)
	require.Equal(t, updated.DisplayName, persisted.DisplayName)
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
	require.NoError(t, err)
	require.True(t, saved.ID.IsValid(), "Save() id = %q", saved.ID.String())
	require.True(t, institution.ID.IsZero(), "Save() mutated input id to %q", institution.ID.String())

	_, err = ss.Institution().Save(ctx, saved)
	var invalid *store.ErrInvalidInput
	require.ErrorAs(t, err, &invalid, "second Save(saved) error")

	_, err = ss.Institution().Save(ctx, &model.Institution{
		Name:        "second",
		DisplayName: "Second University",
	})
	var conflict *store.ErrConflict
	require.ErrorAs(t, err, &conflict, "second active institution error")
	require.Equal(t, "institutions_singleton_key", conflict.Constraint, "second active institution error")
}

func testInstitutionStoreGet(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)

	got, err := ss.Institution().Get(ctx, institution.ID)
	require.NoError(t, err)
	require.Equal(t, *institution, *got, "Get() = %#v, want %#v", got, institution)

	_, err = ss.Institution().Get(ctx, model.NewInstitutionID())
	require.True(t, store.IsNotFound(err), "Get(missing) error")
}

func testInstitutionStoreGetSingleton(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)

	got, err := ss.Institution().GetSingleton(ctx)
	require.NoError(t, err)
	require.Equal(t, institution.ID.String(), got.ID.String(), "GetSingleton() id")
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
	updated, err := UpdateInstitution(ctx, ss, institution)
	require.NoError(t, err)
	require.Equal(t, "Northbridge", updated.DisplayName)
	require.Equal(t, capacity, updated.ExamCapacity)
	require.True(t, updated.CreatedAt.Equal(createAt))
	require.False(t, updated.UpdatedAt.Before(institution.UpdatedAt))

	missing := *updated
	missingID, parseErr := model.ParseInstitutionID(model.NewID())
	require.NoError(t, parseErr)
	missing.ID = missingID
	_, err = UpdateInstitution(ctx, ss, &missing)
	require.True(t, store.IsNotFound(err), "Update(missing) error")
}

func testInstitutionStoreArchive(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)

	require.NoError(t, ss.Institution().Archive(ctx, &store.InstitutionArchive{ID: institution.ID, ArchiveAt: model.NowUTC()}))
	_, err := ss.Institution().Get(ctx, institution.ID)
	require.True(t, store.IsNotFound(err), "Get(archived) error")
	require.True(t, store.IsNotFound(ss.Institution().Archive(ctx, &store.InstitutionArchive{ID: institution.ID, ArchiveAt: model.NowUTC()})), "second Archive() error")
	_, err = ss.Institution().Save(ctx, &model.Institution{
		Name:        "replacement",
		DisplayName: "Replacement University",
	})
	require.NoError(t, err)
}

func saveInstitution(t *testing.T, ctx context.Context, ss store.Store) *model.Institution {
	t.Helper()
	cleanupInstitution(t, ctx, ss)
	institution, err := ss.Institution().Save(ctx, &model.Institution{
		Name:        "institution-" + model.NewID(),
		DisplayName: "Northbridge University",
	})
	require.NoError(t, err)
	t.Cleanup(func() { cleanupInstitution(t, context.Background(), ss) })
	return institution
}

func cleanupInstitution(t *testing.T, ctx context.Context, ss store.Store) {
	t.Helper()
	institution, err := ss.Institution().GetSingleton(ctx)
	if store.IsNotFound(err) {
		return
	}
	require.NoError(t, err)
	require.NoError(t, ss.Institution().Archive(ctx, &store.InstitutionArchive{ID: institution.ID, ArchiveAt: model.NowUTC()}))
}
