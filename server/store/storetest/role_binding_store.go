// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"context"
	"errors"
	"sync"
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
		if _, err := ss.RoleBinding().SaveWithAudit(ctx, &store.RoleBindingCreation{
			Binding: &model.RoleBinding{
				UserID: user.ID, RoleID: role.ID, ScopeType: model.RoleScopeInstitution,
				ScopeID: institution.ID.String(), StartsAt: model.TimeFromMillis(at),
			},
			AuditEventID: model.NewId(), AuditAt: at,
		}); err == nil {
			t.Fatal("SaveWithAudit succeeded without audit attempt")
		}
		attempt, err := ss.Audit().Save(ctx, &model.AuditEvent{
			Action:    string(model.ActionRoleManage),
			Resource:  model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()},
			ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(),
			Status: model.AuditStatusAttempt, NodeID: "test-node",
		})
		requireNoError(t, err)
		staleUpdatedAt := role.UpdatedAt
		stalePermissions := append([]string(nil), role.Permissions...)
		role.DisplayName = "Audited Updated"
		role, err = ss.Role().Update(ctx, role)
		requireNoError(t, err)
		if _, err := ss.RoleBinding().SaveWithAudit(ctx, &store.RoleBindingCreation{
			Binding: &model.RoleBinding{
				UserID: user.ID, RoleID: role.ID, ScopeType: model.RoleScopeInstitution,
				ScopeID: institution.ID.String(), StartsAt: model.TimeFromMillis(at),
			},
			ExpectedRoleUpdatedAt: staleUpdatedAt, ExpectedRolePermissions: stalePermissions,
			AuditEventID: attempt.ID.String(), AuditAt: at,
		}); !store.IsConflict(err) {
			t.Fatalf("SaveWithAudit(stale role snapshot) error = %v, want conflict", err)
		}
		attempt, err = ss.Audit().Save(ctx, &model.AuditEvent{
			Action: string(model.ActionRoleManage), Resource: model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()},
			ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(), Status: model.AuditStatusAttempt, NodeID: "test-node",
		})
		requireNoError(t, err)
		saved, err := ss.RoleBinding().SaveWithAudit(ctx, &store.RoleBindingCreation{
			Binding: &model.RoleBinding{
				UserID: user.ID, RoleID: role.ID, ScopeType: model.RoleScopeInstitution,
				ScopeID: institution.ID.String(), StartsAt: model.TimeFromMillis(at),
			},
			ExpectedRoleUpdatedAt: role.UpdatedAt, ExpectedRolePermissions: append([]string(nil), role.Permissions...),
			AuditEventID: attempt.ID.String(), AuditAt: at,
		})
		requireNoError(t, err)
		endAttempt, err := ss.Audit().Save(ctx, &model.AuditEvent{
			Action:    string(model.ActionRoleManage),
			Resource:  model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()},
			ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(),
			Status: model.AuditStatusAttempt, NodeID: "test-node",
		})
		requireNoError(t, err)
		ended, err := ss.RoleBinding().EndWithAudit(ctx, &store.RoleBindingEnd{
			ID: saved.ID.String(), EndAt: at + 1, AuditEventID: endAttempt.ID.String(), AuditAt: at + 1,
		})
		requireNoError(t, err)
		if ended.EndsAt.Millis() != at+1 {
			t.Fatalf("EndWithAudit = %#v", ended)
		}
	})
	t.Run("ScopedUserProjection", func(t *testing.T) {
		ctx := context.Background()
		institution := saveInstitution(t, ctx, ss)
		root := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "binding-root")
		child := saveAcademicUnit(t, ctx, ss, institution.ID.String(), root.ID.String(), "binding-child")
		sibling := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "binding-sibling")
		programme := saveProgramme(t, ctx, ss, child.ID.String(), "binding-programme")
		level := saveProgrammeLevel(t, ctx, ss, programme.ID.String(), "binding-level")
		period := saveAcademicPeriod(t, ctx, ss, institution.ID.String(), "binding-period", 1_800_000_000_000)
		class := saveClass(t, ctx, ss, level.ID.String(), period.ID.String(), "binding-class")
		user := saveUser(t, ctx, ss)
		role, err := ss.Role().Save(ctx, &model.Role{Name: "binding-scope-reader", DisplayName: "Reader", Permissions: []string{string(model.ActionClassView)}})
		requireNoError(t, err)
		start := model.GetMillis()
		bindings := make([]*model.RoleBinding, 0, 4)
		for _, scope := range []struct {
			type_ model.RoleScopeType
			id    string
		}{
			{model.RoleScopeInstitution, institution.ID.String()},
			{model.RoleScopeAcademicUnit, child.ID.String()},
			{model.RoleScopeAcademicUnit, sibling.ID.String()},
			{model.RoleScopeClass, class.ID.String()},
		} {
			saved, saveErr := ss.RoleBinding().Save(ctx, &model.RoleBinding{UserID: user.ID, RoleID: role.ID, ScopeType: scope.type_, ScopeID: scope.id, StartsAt: model.TimeFromMillis(start)})
			requireNoError(t, saveErr)
			bindings = append(bindings, saved)
		}
		visible, err := ss.RoleBinding().ListVisibleByUser(ctx, user.ID.String(), store.UserVisibilityScope{AcademicUnitRootIDs: []string{root.ID.String()}})
		requireNoError(t, err)
		seen := map[model.RoleBindingID]bool{}
		for _, binding := range visible {
			seen[binding.ID] = true
		}
		if len(visible) != 2 || !seen[bindings[1].ID] || !seen[bindings[3].ID] || seen[bindings[0].ID] || seen[bindings[2].ID] {
			t.Fatalf("scoped bindings = %#v", visible)
		}
	})
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	user := saveUserWithPassword(t, ctx, ss)
	role, err := ss.Role().Save(ctx, &model.Role{
		Name: "class-reader", DisplayName: "Class Reader",
		Permissions: []string{string(model.ActionClassView)},
	})
	requireNoError(t, err)
	start := model.GetMillis()
	binding, err := ss.RoleBinding().Save(ctx, &model.RoleBinding{
		UserID: user.ID, RoleID: role.ID, ScopeType: model.RoleScopeInstitution,
		ScopeID: institution.ID.String(), StartsAt: model.TimeFromMillis(start),
	})
	requireNoError(t, err)
	if !binding.ID.IsValid() {
		t.Fatalf("Save() = %#v", binding)
	}
	got, err := ss.RoleBinding().Get(ctx, binding.ID.String())
	requireNoError(t, err)
	byUser, err := ss.RoleBinding().ListByUser(ctx, user.ID.String())
	requireNoError(t, err)
	byScope, err := ss.RoleBinding().ListByScope(
		ctx, model.RoleScopeInstitution, institution.ID.String(),
	)
	requireNoError(t, err)
	active, err := ss.RoleBinding().ListActiveByUser(ctx, user.ID.String(), start+1)
	requireNoError(t, err)
	if got.ID != binding.ID || len(byUser) != 1 || len(byScope) != 1 || len(active) != 1 {
		t.Fatalf("binding queries = %#v/%d/%d/%d", got, len(byUser), len(byScope), len(active))
	}
	_, err = ss.RoleBinding().Save(ctx, &model.RoleBinding{
		UserID: user.ID, RoleID: role.ID, ScopeType: model.RoleScopeInstitution,
		ScopeID: institution.ID.String(), StartsAt: model.TimeFromMillis(start + 1),
	})
	if !store.IsConflict(err) {
		t.Fatalf("overlapping Save() error = %v", err)
	}
	ended, err := ss.RoleBinding().End(ctx, binding.ID.String(), start+2)
	requireNoError(t, err)
	if ended.EndsAt.Millis() != start+2 {
		t.Fatalf("End() = %#v", ended)
	}
	active, err = ss.RoleBinding().ListActiveByUser(ctx, user.ID.String(), start+3)
	requireNoError(t, err)
	if len(active) != 0 {
		t.Fatalf("active ended bindings = %#v", active)
	}
	_, err = ss.RoleBinding().Save(ctx, &model.RoleBinding{
		UserID: user.ID, RoleID: role.ID, ScopeType: model.RoleScopeClass,
		ScopeID: model.NewId(), StartsAt: model.TimeFromMillis(start),
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
		UserID: user.ID, RoleID: adminRole.ID, ScopeType: model.RoleScopeInstitution,
		ScopeID: institution.ID.String(), StartsAt: model.TimeFromMillis(start),
	})
	requireNoError(t, err)
	if _, err := ss.RoleBinding().End(ctx, firstAdmin.ID.String(), start+10); !store.IsConflict(err) {
		t.Fatalf("End(last system administrator) error = %v", err)
	}
	secondUser := saveUserWithPassword(t, ctx, ss)
	secondAdmin, err := ss.RoleBinding().Save(ctx, &model.RoleBinding{
		UserID: secondUser.ID, RoleID: adminRole.ID, ScopeType: model.RoleScopeInstitution,
		ScopeID: institution.ID.String(), StartsAt: model.TimeFromMillis(start),
	})
	requireNoError(t, err)
	if _, err := ss.RoleBinding().End(ctx, firstAdmin.ID.String(), start+10); err != nil {
		t.Fatalf("End(system administrator with successor) error = %v", err)
	}
	thirdUser := saveUserWithPassword(t, ctx, ss)
	thirdAdmin, err := ss.RoleBinding().Save(ctx, &model.RoleBinding{
		UserID: thirdUser.ID, RoleID: adminRole.ID, ScopeType: model.RoleScopeInstitution,
		ScopeID: institution.ID.String(), StartsAt: model.TimeFromMillis(start),
	})
	requireNoError(t, err)
	startRace := make(chan struct{})
	endErrors := make(chan error, 2)
	var workers sync.WaitGroup
	for _, id := range []string{secondAdmin.ID.String(), thirdAdmin.ID.String()} {
		id := id
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-startRace
			_, endErr := ss.RoleBinding().End(ctx, id, start+20)
			endErrors <- endErr
		}()
	}
	close(startRace)
	workers.Wait()
	close(endErrors)
	succeeded, conflicted := 0, 0
	for endErr := range endErrors {
		switch {
		case endErr == nil:
			succeeded++
		case store.IsConflict(endErr):
			conflicted++
		default:
			t.Fatalf("concurrent administrator End() error = %v", endErr)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("concurrent administrator End() results = success %d conflict %d", succeeded, conflicted)
	}
}
