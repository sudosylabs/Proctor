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
	"errors"
	"slices"
	"sync"
	"testing"

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
		saved, err := ss.Role().Save(ctx, input)
		requireNoError(t, err)
		if !saved.ID.IsValid() || !input.ID.IsZero() {
			t.Fatalf("Save() = %#v, input = %#v", saved, input)
		}
		byID, err := ss.Role().Get(ctx, saved.ID.String())
		requireNoError(t, err)
		byName, err := ss.Role().GetByName(ctx, saved.Name)
		requireNoError(t, err)
		if byID.ID != saved.ID || byName.ID != saved.ID {
			t.Fatalf("role queries = %#v, %#v", byID, byName)
		}
		saved.Permissions = append(saved.Permissions, string(model.ActionClassMembersView))
		updated, err := ss.Role().Update(ctx, saved)
		requireNoError(t, err)
		if len(updated.Permissions) != 2 {
			t.Fatalf("Update() = %#v", updated)
		}
		list, err := ss.Role().List(ctx)
		requireNoError(t, err)
		batch, err := ss.Role().GetByIds(ctx, []string{saved.ID.String(), model.NewId()})
		requireNoError(t, err)
		if len(list) != 1 || len(batch) != 1 {
			t.Fatalf("List/GetByIds = %d/%d", len(list), len(batch))
		}
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
				_, updateErr := ss.Role().Update(ctx, candidate)
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
				t.Fatalf("concurrent Update() error = %v", updateErr)
			}
		}
		if succeeded != 1 || conflicted != 1 {
			t.Fatalf("concurrent Update() results = success %d conflict %d", succeeded, conflicted)
		}
		saved, err = ss.Role().Get(ctx, saved.ID.String())
		requireNoError(t, err)
		archived, err := ss.Role().Archive(ctx, saved.ID.String(), model.GetMillis())
		requireNoError(t, err)
		if !archived.IsArchived() {
			t.Fatalf("Archive() = %#v", archived)
		}
		if _, err := ss.Role().Get(ctx, saved.ID.String()); !store.IsNotFound(err) {
			t.Fatalf("Get(archived) error = %v", err)
		}
	})

	t.Run("UniquenessAndBuiltInProtection", func(t *testing.T) {
		ctx := context.Background()
		first, err := ss.Role().Save(ctx, &model.Role{
			Name: "course-owner", DisplayName: "Course Owner", BuiltIn: true,
		})
		requireNoError(t, err)
		_, err = ss.Role().Save(ctx, &model.Role{
			Name: first.Name, DisplayName: "Duplicate",
		})
		var conflict *store.ErrConflict
		if !errors.As(err, &conflict) || conflict.Constraint != "roles_name_key" {
			t.Fatalf("duplicate role error = %v", err)
		}
		if _, err := ss.Role().Archive(ctx, first.ID.String(), model.GetMillis()); !store.IsConflict(err) {
			t.Fatalf("Archive(built-in) error = %v", err)
		}
		first.DisplayName = "Modified Built-in"
		if _, err := ss.Role().Update(ctx, first); !store.IsConflict(err) {
			t.Fatalf("Update(built-in) error = %v", err)
		}
	})

	t.Run("AuditedMutationsAreAtomic", func(t *testing.T) {
		ctx := context.Background()
		at := model.GetMillis() + 100
		if _, err := ss.Role().SaveWithAudit(ctx, &store.RoleCreation{
			Role:         &model.Role{Name: "audited-teacher", DisplayName: "Audited Teacher", Permissions: []string{string(model.ActionClassView)}},
			AuditEventID: model.NewId(), AuditAt: at,
		}); err == nil {
			t.Fatal("SaveWithAudit() succeeded without its audit attempt")
		}
		list, err := ss.Role().List(ctx)
		requireNoError(t, err)
		for _, role := range list {
			if role.Name == "audited-teacher" {
				t.Fatalf("role survived audit rollback: %#v", role)
			}
		}

		createAttempt := saveRoleAuditAttempt(t, ctx, ss)
		created, err := ss.Role().SaveWithAudit(ctx, &store.RoleCreation{
			Role:         &model.Role{Name: "audited-teacher", DisplayName: "Audited Teacher", Permissions: []string{string(model.ActionClassView)}},
			AuditEventID: createAttempt.ID.String(), AuditAt: at,
		})
		requireNoError(t, err)
		createAudit, err := ss.Audit().Get(ctx, createAttempt.ID.String())
		requireNoError(t, err)
		if createAudit.Status != model.AuditStatusSuccess {
			t.Fatalf("create audit = %#v", createAudit)
		}

		created.DisplayName = "Audited Teacher Updated"
		if _, err := ss.Role().UpdateWithAudit(ctx, &store.RoleUpdate{
			Role: created, AuditEventID: model.NewId(), AuditAt: at + 1,
		}); err == nil {
			t.Fatal("UpdateWithAudit() succeeded without its audit attempt")
		}
		unchanged, err := ss.Role().Get(ctx, created.ID.String())
		requireNoError(t, err)
		if unchanged.DisplayName != "Audited Teacher" {
			t.Fatalf("role update survived audit rollback: %#v", unchanged)
		}
		updateAttempt := saveRoleAuditAttempt(t, ctx, ss)
		updated, err := ss.Role().UpdateWithAudit(ctx, &store.RoleUpdate{
			Role: created, AuditEventID: updateAttempt.ID.String(), AuditAt: at + 1,
		})
		requireNoError(t, err)
		if updated.DisplayName != "Audited Teacher Updated" {
			t.Fatalf("UpdateWithAudit() = %#v", updated)
		}

		institution := saveInstitution(t, ctx, ss)
		user := saveUser(t, ctx, ss)
		binding, err := ss.RoleBinding().Save(ctx, &model.RoleBinding{
			UserID: user.ID, RoleID: updated.ID, ScopeType: model.RoleScopeInstitution,
			ScopeID: institution.ID.String(), StartsAt: model.TimeFromMillis(at + 2),
		})
		if err != nil {
			t.Fatalf("bind role before permission fence: %v", err)
		}
		permissionCandidate := updated.Clone()
		permissionCandidate.Permissions = append(permissionCandidate.Permissions, string(model.ActionClassManage))
		permissionAttempt := saveRoleAuditAttempt(t, ctx, ss)
		if _, err := ss.Role().UpdateWithAudit(ctx, &store.RoleUpdate{
			Role: permissionCandidate, AuditEventID: permissionAttempt.ID.String(), AuditAt: at + 3,
		}); !store.IsConflict(err) {
			t.Fatalf("bound role permission UpdateWithAudit() error = %v, want conflict", err)
		}
		if _, err := ss.RoleBinding().End(ctx, binding.ID.String(), at+4); err != nil {
			t.Fatalf("end historical binding: %v", err)
		}
		historicalAttempt := saveRoleAuditAttempt(t, ctx, ss)
		permissionCandidate, err = ss.Role().UpdateWithAudit(ctx, &store.RoleUpdate{
			Role: permissionCandidate, AuditEventID: historicalAttempt.ID.String(), AuditAt: at + 5,
		})
		requireNoError(t, err)
		if !slices.Contains(permissionCandidate.Permissions, string(model.ActionClassManage)) {
			t.Fatalf("historically bound role permissions = %#v", permissionCandidate.Permissions)
		}

		if _, err := ss.Role().ArchiveWithAudit(ctx, &store.RoleArchive{
			ID: created.ID.String(), ArchiveAt: at + 2, AuditEventID: model.NewId(), AuditAt: at + 2,
		}); err == nil {
			t.Fatal("ArchiveWithAudit() succeeded without its audit attempt")
		}
		stillPresent, err := ss.Role().Get(ctx, created.ID.String())
		requireNoError(t, err)
		if stillPresent.IsArchived() {
			t.Fatalf("role archive survived audit rollback: %#v", stillPresent)
		}
		archiveAttempt := saveRoleAuditAttempt(t, ctx, ss)
		archived, err := ss.Role().ArchiveWithAudit(ctx, &store.RoleArchive{
			ID: created.ID.String(), ArchiveAt: at + 2, AuditEventID: archiveAttempt.ID.String(), AuditAt: at + 2,
		})
		requireNoError(t, err)
		if archived.ArchivedAt.Millis() != at+2 {
			t.Fatalf("ArchiveWithAudit() = %#v", archived)
		}
		if _, err := ss.Role().Get(ctx, created.ID.String()); !store.IsNotFound(err) {
			t.Fatalf("Get(archived) error = %v", err)
		}
	})
}

func saveRoleAuditAttempt(t *testing.T, ctx context.Context, ss store.Store) *model.AuditEvent {
	t.Helper()
	attempt, err := ss.Audit().Save(ctx, &model.AuditEvent{
		Action:    string(model.ActionRoleManage),
		Resource:  model.Resource{Type: model.ResourceInstitution, ID: model.NewId()},
		ScopeType: model.RoleScopeInstitution, ScopeID: model.NewId(),
		Status: model.AuditStatusAttempt, NodeID: "test-node",
	})
	requireNoError(t, err)
	return attempt
}
