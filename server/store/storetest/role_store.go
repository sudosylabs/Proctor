// ---------------------------------------------------------------------------------------------
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
//
// Adapted from Mattermost server/channels/store/storetest/role_store.go.

package storetest

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestRoleStore(t *testing.T, ss store.Store) {
	t.Run("LifecycleAndQueries", func(t *testing.T) {
		ctx := context.Background()
		input := &model.Role{
			Name: "department-teacher", DisplayName: "Department Teacher",
			Permissions: []string{string(model.ActionClassView)},
		}
		saved, err := SaveRole(ctx, ss, input)
		require.NoError(t, err)
		require.True(t, saved.ID.IsValid(), "Save() = %#v, input = %#v", saved, input)
		require.True(t, input.ID.IsZero(), "Save() = %#v, input = %#v", saved, input)
		byID, err := ss.Role().Get(ctx, saved.ID)
		require.NoError(t, err)
		byName, err := ss.Role().GetByName(ctx, saved.Name)
		require.NoError(t, err)
		require.Equal(t, saved.ID, byID.ID, "role queries = %#v, %#v", byID, byName)
		require.Equal(t, saved.ID, byName.ID, "role queries = %#v, %#v", byID, byName)
		saved.Permissions = append(saved.Permissions, string(model.ActionClassMembersView))
		updated, err := UpdateRole(ctx, ss, saved)
		require.NoError(t, err)
		require.Len(t, updated.Permissions, 2)
		list, err := ss.Role().List(ctx)
		require.NoError(t, err)
		batch, err := ss.Role().GetByIDs(ctx, []model.RoleID{saved.ID, model.NewRoleID()})
		require.NoError(t, err)
		require.Len(t, list, 1, "List/GetByIDs")
		require.Len(t, batch, 1, "List/GetByIDs")
		firstUpdate := saved.Clone()
		firstUpdate.DisplayName = "Concurrent First"
		secondUpdate := saved.Clone()
		secondUpdate.DisplayName = "Concurrent Second"
		start := make(chan struct{})
		errorsByUpdate := make(chan error, 2)
		var workers sync.WaitGroup
		for _, candidate := range []*model.Role{firstUpdate, secondUpdate} {
			candidate := candidate
			workers.Add(1)
			go func() {
				defer workers.Done()
				<-start
				_, updateErr := UpdateRole(ctx, ss, candidate)
				errorsByUpdate <- updateErr
			}()
		}
		close(start)
		workers.Wait()
		close(errorsByUpdate)
		succeeded, conflicted := 0, 0
		for updateErr := range errorsByUpdate {
			switch {
			case updateErr == nil:
				succeeded++
			case store.IsConflict(updateErr):
				conflicted++
			default:
				require.FailNow(t, fmt.Sprintf("concurrent Update() error = %v", updateErr))
			}
		}
		require.Equal(t, 1, succeeded, "concurrent Update() results = success")
		require.Equal(t, 1, conflicted, "concurrent Update() results = success")
		saved, err = ss.Role().Get(ctx, saved.ID)
		require.NoError(t, err)
		archived, err := ArchiveRole(ctx, ss, saved.ID, model.GetMillis())
		require.NoError(t, err)
		require.True(t, archived.IsArchived())
		_, err = ss.Role().Get(ctx, saved.ID)
		require.True(t, store.IsNotFound(err), "Get(archived) error")
	})

	t.Run("UniquenessAndBuiltInProtection", func(t *testing.T) {
		ctx := context.Background()
		first, err := SaveRole(ctx, ss, &model.Role{
			Name: "course-owner", DisplayName: "Course Owner", BuiltIn: true,
		})
		require.NoError(t, err)
		_, err = SaveRole(ctx, ss, &model.Role{
			Name: first.Name, DisplayName: "Duplicate",
		})
		var conflict *store.ErrConflict
		require.ErrorAs(t, err, &conflict)
		require.Equal(t, "roles_name_key", conflict.Constraint)
		_, err = ArchiveRole(ctx, ss, first.ID, model.GetMillis())
		require.True(t, store.IsConflict(err), "Archive(built-in) error")
		first.DisplayName = "Modified Built-in"
		_, err = UpdateRole(ctx, ss, first)
		require.True(t, store.IsConflict(err), "Update(built-in) error")
	})

	t.Run("AuditedMutationsAreAtomic", func(t *testing.T) {
		ctx := context.Background()
		at := model.GetMillis() + 100
		_, err := ss.Role().Save(ctx, &store.RoleCreation{
			Role:         &model.Role{Name: "audited-teacher", DisplayName: "Audited Teacher", Permissions: []string{string(model.ActionClassView)}},
			AuditEventID: model.NewAuditEventID(), AuditAt: at,
		})
		require.Error(t, err, "Save() succeeded without its audit attempt")
		list, err := ss.Role().List(ctx)
		require.NoError(t, err)
		for _, role := range list {
			require.NotEqual(t, "audited-teacher", role.Name)
		}

		createAttempt := saveRoleAuditAttempt(t, ctx, ss)
		created, err := ss.Role().Save(ctx, &store.RoleCreation{
			Role:         &model.Role{Name: "audited-teacher", DisplayName: "Audited Teacher", Permissions: []string{string(model.ActionClassView)}},
			AuditEventID: createAttempt.ID, AuditAt: at,
		})
		require.NoError(t, err)
		createAudit, err := ss.Audit().Get(ctx, createAttempt.ID)
		require.NoError(t, err)
		require.Equal(t, model.AuditStatusSuccess, createAudit.Status)

		created.DisplayName = "Audited Teacher Updated"
		_, err = ss.Role().Update(ctx, &store.RoleUpdate{
			Role: created, AuditEventID: model.NewAuditEventID(), AuditAt: at + 1,
		})
		require.Error(t, err, "Update() succeeded without its audit attempt")
		unchanged, err := ss.Role().Get(ctx, created.ID)
		require.NoError(t, err)
		require.Equal(t, "Audited Teacher", unchanged.DisplayName)
		updateAttempt := saveRoleAuditAttempt(t, ctx, ss)
		updated, err := ss.Role().Update(ctx, &store.RoleUpdate{
			Role: created, AuditEventID: updateAttempt.ID, AuditAt: at + 1,
		})
		require.NoError(t, err)
		require.Equal(t, "Audited Teacher Updated", updated.DisplayName)

		institution := saveInstitution(t, ctx, ss)
		user := saveUser(t, ctx, ss)
		binding, err := SaveRoleBinding(ctx, ss, &model.RoleBinding{
			UserID: user.ID, RoleID: updated.ID, ScopeType: model.RoleScopeInstitution,
			ScopeID: institution.ID.String(), StartsAt: model.TimeFromMillis(at + 2),
		})
		require.NoError(t, err)
		permissionCandidate := updated.Clone()
		permissionCandidate.Permissions = append(permissionCandidate.Permissions, string(model.ActionClassManage))
		permissionAttempt := saveRoleAuditAttempt(t, ctx, ss)
		_, err = ss.Role().Update(ctx, &store.RoleUpdate{
			Role: permissionCandidate, AuditEventID: permissionAttempt.ID, AuditAt: at + 3,
		})
		require.True(t, store.IsConflict(err), "bound role permission Update() error")
		_, err = EndRoleBinding(ctx, ss, binding.ID, at+4)
		require.NoError(t, err)
		historicalAttempt := saveRoleAuditAttempt(t, ctx, ss)
		permissionCandidate, err = ss.Role().Update(ctx, &store.RoleUpdate{
			Role: permissionCandidate, AuditEventID: historicalAttempt.ID, AuditAt: at + 5,
		})
		require.NoError(t, err)
		require.Contains(t, permissionCandidate.Permissions, string(model.ActionClassManage))

		_, err = ss.Role().Archive(ctx, &store.RoleArchive{
			ID: created.ID, ArchiveAt: at + 2, AuditEventID: model.NewAuditEventID(), AuditAt: at + 2,
		})
		require.Error(t, err, "Archive() succeeded without its audit attempt")
		stillPresent, err := ss.Role().Get(ctx, created.ID)
		require.NoError(t, err)
		require.False(t, stillPresent.IsArchived())
		archiveAttempt := saveRoleAuditAttempt(t, ctx, ss)
		archived, err := ss.Role().Archive(ctx, &store.RoleArchive{
			ID: created.ID, ArchiveAt: at + 2, AuditEventID: archiveAttempt.ID, AuditAt: at + 2,
		})
		require.NoError(t, err)
		require.Equal(t, at+2, archived.ArchivedAt.Millis())
		_, err = ss.Role().Get(ctx, created.ID)
		require.True(t, store.IsNotFound(err), "Get(archived) error")
	})
}

func saveRoleAuditAttempt(t *testing.T, ctx context.Context, ss store.Store) *model.AuditEvent {
	t.Helper()
	attempt, err := ss.Audit().Save(ctx, &model.AuditEvent{
		Action:    string(model.ActionRoleManage),
		Resource:  model.Resource{Type: model.ResourceInstitution, ID: model.NewID()},
		ScopeType: model.RoleScopeInstitution, ScopeID: model.NewID(),
		Status: model.AuditStatusAttempt, NodeID: "test-node",
	})
	require.NoError(t, err)
	return attempt
}
