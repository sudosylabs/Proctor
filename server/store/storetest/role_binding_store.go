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
	t.Run("AuditedMutations", func(t *testing.T) {
		ctx := context.Background()
		institution := saveInstitution(t, ctx, ss)
		user := saveUser(t, ctx, ss)
		role, err := ss.Role().Save(ctx, &model.Role{Name: "audited-binding-role", DisplayName: "Audited", Permissions: []string{string(model.ActionClassView)}})
		requireNoError(t, err)
		at := model.GetMillis()
		if _, err := ss.RoleBinding().SaveWithAudit(ctx, &store.RoleBindingCreation{Binding: &model.RoleBinding{UserId: user.ID.String(), RoleId: role.Id, ScopeType: model.RoleScopeInstitution, ScopeId: institution.ID.String(), StartAt: at}, AuditEventID: model.NewId(), AuditAt: at}); err == nil {
			t.Fatal("SaveWithAudit succeeded without audit attempt")
		}
		attempt, err := ss.Audit().Save(ctx, &model.AuditEvent{Action: string(model.ActionRoleManage), Resource: model.Resource{Type: model.ResourceInstitution, Id: institution.ID.String()}, ScopeType: model.RoleScopeInstitution, ScopeId: institution.ID.String(), Status: model.AuditStatusAttempt, NodeId: "test-node"})
		requireNoError(t, err)
		saved, err := ss.RoleBinding().SaveWithAudit(ctx, &store.RoleBindingCreation{Binding: &model.RoleBinding{UserId: user.ID.String(), RoleId: role.Id, ScopeType: model.RoleScopeInstitution, ScopeId: institution.ID.String(), StartAt: at}, AuditEventID: attempt.Id, AuditAt: at})
		requireNoError(t, err)
		endAttempt, err := ss.Audit().Save(ctx, &model.AuditEvent{Action: string(model.ActionRoleManage), Resource: model.Resource{Type: model.ResourceInstitution, Id: institution.ID.String()}, ScopeType: model.RoleScopeInstitution, ScopeId: institution.ID.String(), Status: model.AuditStatusAttempt, NodeId: "test-node"})
		requireNoError(t, err)
		ended, err := ss.RoleBinding().EndWithAudit(ctx, &store.RoleBindingEnd{ID: saved.Id, EndAt: at + 1, AuditEventID: endAttempt.Id, AuditAt: at + 1})
		requireNoError(t, err)
		if ended.EndAt != at+1 { t.Fatalf("EndWithAudit = %#v", ended) }
	})
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
		UserId: user.ID.String(), RoleId: role.Id, ScopeType: model.RoleScopeInstitution,
		ScopeId: institution.ID.String(), StartAt: start,
	})
	requireNoError(t, err)
	if !model.IsValidId(binding.Id) {
		t.Fatalf("Save() = %#v", binding)
	}
	got, err := ss.RoleBinding().Get(ctx, binding.Id)
	requireNoError(t, err)
	byUser, err := ss.RoleBinding().ListByUser(ctx, user.ID.String())
	requireNoError(t, err)
	byScope, err := ss.RoleBinding().ListByScope(
		ctx, model.RoleScopeInstitution, institution.ID.String(),
	)
	requireNoError(t, err)
	active, err := ss.RoleBinding().ListActiveByUser(ctx, user.ID.String(), start+1)
	requireNoError(t, err)
	if got.Id != binding.Id || len(byUser) != 1 || len(byScope) != 1 || len(active) != 1 {
		t.Fatalf("binding queries = %#v/%d/%d/%d", got, len(byUser), len(byScope), len(active))
	}
	_, err = ss.RoleBinding().Save(ctx, &model.RoleBinding{
		UserId: user.ID.String(), RoleId: role.Id, ScopeType: model.RoleScopeInstitution,
		ScopeId: institution.ID.String(), StartAt: start + 1,
	})
	if !store.IsConflict(err) {
		t.Fatalf("overlapping Save() error = %v", err)
	}
	ended, err := ss.RoleBinding().End(ctx, binding.Id, start+2)
	requireNoError(t, err)
	if ended.EndAt != start+2 {
		t.Fatalf("End() = %#v", ended)
	}
	active, err = ss.RoleBinding().ListActiveByUser(ctx, user.ID.String(), start+3)
	requireNoError(t, err)
	if len(active) != 0 {
		t.Fatalf("active ended bindings = %#v", active)
	}
	_, err = ss.RoleBinding().Save(ctx, &model.RoleBinding{
		UserId: user.ID.String(), RoleId: role.Id, ScopeType: model.RoleScopeClass,
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
		UserId: user.ID.String(), RoleId: adminRole.Id, ScopeType: model.RoleScopeInstitution,
		ScopeId: institution.ID.String(), StartAt: start,
	})
	requireNoError(t, err)
	if _, err := ss.RoleBinding().End(ctx, firstAdmin.Id, start+10); !store.IsConflict(err) {
		t.Fatalf("End(last system administrator) error = %v", err)
	}
	secondUser := saveUser(t, ctx, ss)
	_, err = ss.RoleBinding().Save(ctx, &model.RoleBinding{
		UserId: secondUser.ID.String(), RoleId: adminRole.Id, ScopeType: model.RoleScopeInstitution,
		ScopeId: institution.ID.String(), StartAt: start,
	})
	requireNoError(t, err)
	if _, err := ss.RoleBinding().End(ctx, firstAdmin.Id, start+10); err != nil {
		t.Fatalf("End(system administrator with successor) error = %v", err)
	}
}
