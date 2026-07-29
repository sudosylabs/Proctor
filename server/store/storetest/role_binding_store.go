// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
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

func TestRoleBindingStore(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	user := saveUser(t, ctx, ss)
	role, err := ss.Role().Save(ctx, &model.Role{
		Name: "class-reader", DisplayName: "Class Reader",
		Permissions: []string{string(model.ActionClassView)},
	})
	requireNoError(t, err)
	start := model.GetMillis()
	binding, err := ss.RoleBinding().Save(ctx, &model.RoleBinding{
		UserId: user.Id, RoleId: role.Id, ScopeType: model.RoleScopeInstitution,
		ScopeId: institution.Id, StartAt: start,
	})
	requireNoError(t, err)
	if !model.IsValidId(binding.Id) {
		t.Fatalf("Save() = %#v", binding)
	}
	got, err := ss.RoleBinding().Get(ctx, binding.Id)
	requireNoError(t, err)
	byUser, err := ss.RoleBinding().ListByUser(ctx, user.Id)
	requireNoError(t, err)
	byScope, err := ss.RoleBinding().ListByScope(
		ctx, model.RoleScopeInstitution, institution.Id,
	)
	requireNoError(t, err)
	active, err := ss.RoleBinding().ListActiveByUser(ctx, user.Id, start+1)
	requireNoError(t, err)
	if got.Id != binding.Id || len(byUser) != 1 || len(byScope) != 1 || len(active) != 1 {
		t.Fatalf("binding queries = %#v/%d/%d/%d", got, len(byUser), len(byScope), len(active))
	}
	_, err = ss.RoleBinding().Save(ctx, &model.RoleBinding{
		UserId: user.Id, RoleId: role.Id, ScopeType: model.RoleScopeInstitution,
		ScopeId: institution.Id, StartAt: start + 1,
	})
	if !store.IsConflict(err) {
		t.Fatalf("overlapping Save() error = %v", err)
	}
	ended, err := ss.RoleBinding().End(ctx, binding.Id, start+2)
	requireNoError(t, err)
	if ended.EndAt != start+2 {
		t.Fatalf("End() = %#v", ended)
	}
	active, err = ss.RoleBinding().ListActiveByUser(ctx, user.Id, start+3)
	requireNoError(t, err)
	if len(active) != 0 {
		t.Fatalf("active ended bindings = %#v", active)
	}
	_, err = ss.RoleBinding().Save(ctx, &model.RoleBinding{
		UserId: user.Id, RoleId: role.Id, ScopeType: model.RoleScopeClass,
		ScopeId: model.NewId(), StartAt: start,
	})
	var reference *store.ErrReference
	if !errors.As(err, &reference) ||
		reference.Constraint != "role_bindings_class_scope_fkey" {
		t.Fatalf("invalid scope error = %v", err)
	}

	adminRole, err := ss.Role().Save(ctx, &model.Role{
		Name: model.SystemAdministratorRoleName, DisplayName: "System Administrator",
		Permissions: model.AllActions(), BuiltIn: true,
	})
	requireNoError(t, err)
	firstAdmin, err := ss.RoleBinding().Save(ctx, &model.RoleBinding{
		UserId: user.Id, RoleId: adminRole.Id, ScopeType: model.RoleScopeInstitution,
		ScopeId: institution.Id, StartAt: start,
	})
	requireNoError(t, err)
	if _, err := ss.RoleBinding().End(ctx, firstAdmin.Id, start+10); !store.IsConflict(err) {
		t.Fatalf("End(last system administrator) error = %v", err)
	}
	secondUser := saveUser(t, ctx, ss)
	_, err = ss.RoleBinding().Save(ctx, &model.RoleBinding{
		UserId: secondUser.Id, RoleId: adminRole.Id, ScopeType: model.RoleScopeInstitution,
		ScopeId: institution.Id, StartAt: start,
	})
	requireNoError(t, err)
	if _, err := ss.RoleBinding().End(ctx, firstAdmin.Id, start+10); err != nil {
		t.Fatalf("End(system administrator with successor) error = %v", err)
	}
}
