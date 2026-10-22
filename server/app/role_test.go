// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type roleStoreFake struct {
	events        *[]string
	role          *model.Role
	list          []*model.Role
	createInput   *store.RoleCreation
	updateInput   *store.RoleUpdate
	archiveInput  *store.RoleArchive
	createResult  *model.Role
	updateResult  *model.Role
	archiveResult *model.Role
	getErr        error
	listErr       error
	createErr     error
	updateErr     error
	archiveErr    error
}

func (s *roleStoreFake) Get(context.Context, model.RoleID) (*model.Role, error) {
	*s.events = append(*s.events, "get-role")
	return s.role, s.getErr
}

func (s *roleStoreFake) List(context.Context) ([]*model.Role, error) {
	*s.events = append(*s.events, "list-roles")
	return s.list, s.listErr
}

func (s *roleStoreFake) Save(_ context.Context, input *store.RoleCreation) (*model.Role, error) {
	*s.events = append(*s.events, "store-create")
	s.createInput = input
	return s.createResult, s.createErr
}

func (s *roleStoreFake) Update(_ context.Context, input *store.RoleUpdate) (*model.Role, error) {
	*s.events = append(*s.events, "store-update")
	s.updateInput = input
	return s.updateResult, s.updateErr
}

func (s *roleStoreFake) Archive(_ context.Context, input *store.RoleArchive) (*model.Role, error) {
	*s.events = append(*s.events, "store-archive")
	s.archiveInput = input
	return s.archiveResult, s.archiveErr
}

type roleAuthorizerFake struct {
	events        *[]string
	resource      model.Resource
	err           error
	delegationErr error
	bindingScope  store.UserVisibilityScope
}

func (a *roleAuthorizerFake) AuthorizeInstitution(_ context.Context, _ invocation.Invocation, action model.Action) (model.Resource, error) {
	event := "authorize-view"
	if action == model.ActionRoleManage {
		event = "authorize-manage"
	}
	*a.events = append(*a.events, event)
	return a.resource, a.err
}

func (a *roleAuthorizerFake) AuthorizeRoleBindingList(context.Context, invocation.Invocation, model.Action) (store.UserVisibilityScope, error) {
	*a.events = append(*a.events, "authorize-binding-list")
	if a.bindingScope.InstitutionWide || len(a.bindingScope.AcademicUnitRootIDs) > 0 || len(a.bindingScope.ClassIDs) > 0 {
		return a.bindingScope, a.err
	}
	return store.UserVisibilityScope{InstitutionWide: true}, a.err
}

func (a *roleAuthorizerFake) AuthorizeRoleBindingPreflight(context.Context, invocation.Invocation, model.Action) error {
	*a.events = append(*a.events, "authorize-binding-preflight")
	return a.err
}

func (a *roleAuthorizerFake) AuthorizeRoleBindingScope(context.Context, invocation.Invocation, model.Action, model.RoleScopeType, string) (model.Resource, error) {
	*a.events = append(*a.events, "authorize-binding-scope")
	return a.resource, a.err
}

func (a *roleAuthorizerFake) CanDelegateActionsAtScope(context.Context, invocation.Invocation, []string, model.RoleScopeType, string) error {
	*a.events = append(*a.events, "authorize-delegation")
	return a.delegationErr
}

type roleEffectsFake struct{ events *[]string }

func (e *roleEffectsFake) AuthorizationChanged(context.Context) {
	*e.events = append(*e.events, "invalidate-authorization")
}

func TestRoleCreateCommitsWithoutSideEffects(t *testing.T) {
	t.Parallel()
	events := []string{}
	resource := model.Resource{Type: model.ResourceInstitution, ID: model.NewID()}
	created := &model.Role{ID: model.NewRoleID(), Name: "teacher", DisplayName: "Teacher", Permissions: []string{string(model.ActionClassView)}}
	persistence := &roleStoreFake{events: &events, createResult: created}
	service := newRoleService(roleServiceDependencies{Roles: persistence, Authorization: &roleAuthorizerFake{events: &events, resource: resource}, Audit: &institutionAuditorFake{events: &events, beginID: model.NewID()}, Effects: &roleEffectsFake{events: &events}, Now: func() time.Time { return time.UnixMilli(500) }})
	got, err := service.CreateRole(context.Background(), invocation.Invocation{}, CreateRoleCommand{
		Name: "teacher", DisplayName: "Teacher", Permissions: []string{string(model.ActionClassView)},
	})
	require.NoError(t, err)
	require.Equal(t, created.ID.String(), got.ID.String(), "result/input = %#v / %#v", got, persistence.createInput)
	require.NotEmpty(t, persistence.createInput.AuditEventID, "result/input = %#v / %#v", got, persistence.createInput)
	// Contract (guardrail 8): authorization precedes the audit, and the audit
	// begins before the durable write.
	require.Equal(t, []string{"authorize-manage", "authorize-delegation", "audit-begin", "store-create"}, events)
}

func TestRoleCreateAndUpdateRejectUndelegablePermissionsBeforeAudit(t *testing.T) {
	t.Parallel()
	denied := authorizationDeniedError()

	t.Run("create", func(t *testing.T) {
		events := []string{}
		service := newRoleService(roleServiceDependencies{Roles: &roleStoreFake{events: &events}, Authorization: &roleAuthorizerFake{events: &events, resource: model.Resource{Type: model.ResourceInstitution, ID: model.NewID()}, delegationErr: denied}, Audit: &institutionAuditorFake{events: &events}, Effects: &roleEffectsFake{events: &events}, Now: time.Now})
		_, err := service.CreateRole(context.Background(), invocation.Invocation{}, CreateRoleCommand{Name: "unsafe", DisplayName: "Unsafe", Permissions: []string{string(model.ActionUserManage)}})
		require.True(t, apperror.Is(err, "authorization.denied"), "Create() error = %v", err)
		require.Equal(t, []string{"authorize-manage", "authorize-delegation"}, events)
	})

	t.Run("update", func(t *testing.T) {
		events := []string{}
		role := &model.Role{ID: model.NewRoleID(), Name: "teacher", DisplayName: "Teacher", Permissions: []string{string(model.ActionClassView)}}
		permissions := []string{string(model.ActionClassView), string(model.ActionUserManage)}
		service := newRoleService(roleServiceDependencies{Roles: &roleStoreFake{events: &events, role: role}, Authorization: &roleAuthorizerFake{events: &events, resource: model.Resource{Type: model.ResourceInstitution, ID: model.NewID()}, delegationErr: denied}, Audit: &institutionAuditorFake{events: &events}, Effects: &roleEffectsFake{events: &events}, Now: time.Now})
		_, err := service.UpdateRole(context.Background(), invocation.Invocation{}, UpdateRoleCommand{ID: role.ID, Permissions: &permissions})
		require.True(t, apperror.Is(err, "authorization.denied"), "Update() error = %v", err)
		require.Equal(t, []string{"authorize-manage", "get-role", "authorize-delegation"}, events)
	})
}

func TestRoleCreateRejectsUnknownPermissions(t *testing.T) {
	t.Parallel()
	events := []string{}
	service := newRoleService(roleServiceDependencies{Roles: &roleStoreFake{events: &events}, Authorization: &roleAuthorizerFake{events: &events, resource: model.Resource{Type: model.ResourceInstitution, ID: model.NewID()}}, Audit: &institutionAuditorFake{events: &events}, Effects: &roleEffectsFake{events: &events}, Now: time.Now})
	_, err := service.CreateRole(context.Background(), invocation.Invocation{}, CreateRoleCommand{
		Name: "teacher", DisplayName: "Teacher", Permissions: []string{"not.a.real.permission"},
	})
	require.True(t, apperror.Is(err, "role.permission.invalid"), "error = %v", err)
	want := []string{"authorize-manage"}
	require.Equal(t, want, events, "events")
}

func TestRoleCreateRejectsRelationshipOnlyParticipationAction(t *testing.T) {
	t.Parallel()

	err := validateKnownPermissions([]string{string(model.ActionExamSittingParticipate)})
	require.Error(t, err, "validateKnownPermissions() error")
	require.True(t, apperror.Is(err, "role.permission.invalid"), "validateKnownPermissions() error")
}

func TestRoleUpdateCommitsBeforeInvalidation(t *testing.T) {
	t.Parallel()
	events := []string{}
	role := &model.Role{ID: model.NewRoleID(), Name: "teacher", DisplayName: "Teacher", Permissions: []string{string(model.ActionClassView)}}
	updated := role.Clone()
	updated.DisplayName = "Lead Teacher"
	displayName := "Lead Teacher"
	persistence := &roleStoreFake{events: &events, role: role, updateResult: updated}
	service := newRoleService(roleServiceDependencies{Roles: persistence, Authorization: &roleAuthorizerFake{events: &events, resource: model.Resource{Type: model.ResourceInstitution, ID: model.NewID()}}, Audit: &institutionAuditorFake{events: &events, beginID: model.NewID()}, Effects: &roleEffectsFake{events: &events}, Now: func() time.Time { return time.UnixMilli(500) }})
	got, err := service.UpdateRole(context.Background(), invocation.Invocation{}, UpdateRoleCommand{ID: role.ID, DisplayName: &displayName})
	require.NoError(t, err)
	require.Equal(t, "Lead Teacher", got.DisplayName)
	// Contract (guardrails 8 and 14): authorization precedes the audit, the audit
	// begins before the durable write, and the write commits before its effect.
	require.Equal(t, []string{"authorize-manage", "get-role", "audit-begin", "store-update", "invalidate-authorization"}, events)
}

func TestRoleUpdateFailurePublishesNoInvalidation(t *testing.T) {
	t.Parallel()
	events := []string{}
	role := &model.Role{ID: model.NewRoleID(), Name: "teacher", DisplayName: "Teacher", Permissions: []string{string(model.ActionClassView)}}
	displayName := "Lead Teacher"
	service := newRoleService(roleServiceDependencies{Roles: &roleStoreFake{events: &events, role: role, updateErr: store.NewErrConflict("role", "roles_name_key", errors.New("dup"))}, Authorization: &roleAuthorizerFake{events: &events, resource: model.Resource{Type: model.ResourceInstitution, ID: model.NewID()}}, Audit: &institutionAuditorFake{events: &events, beginID: model.NewID()}, Effects: &roleEffectsFake{events: &events}, Now: time.Now})
	_, err := service.UpdateRole(context.Background(), invocation.Invocation{}, UpdateRoleCommand{ID: role.ID, DisplayName: &displayName})
	require.True(t, apperror.Is(err, "role.conflict"), "error = %v", err)
	// Contract (guardrail 8): authorization precedes the audit, the audit begins
	// before the durable write, and the audit outcome follows the write.
	require.Equal(t, []string{"authorize-manage", "get-role", "audit-begin", "store-update", "audit-fail"}, events)
}

func TestRoleUpdateRejectsBuiltIn(t *testing.T) {
	t.Parallel()
	events := []string{}
	role := &model.Role{ID: model.NewRoleID(), Name: model.SystemAdministratorRoleName, DisplayName: "Admin", BuiltIn: true}
	displayName := "Nope"
	service := newRoleService(roleServiceDependencies{Roles: &roleStoreFake{events: &events, role: role}, Authorization: &roleAuthorizerFake{events: &events, resource: model.Resource{Type: model.ResourceInstitution, ID: model.NewID()}}, Audit: &institutionAuditorFake{events: &events}, Effects: &roleEffectsFake{events: &events}, Now: time.Now})
	_, err := service.UpdateRole(context.Background(), invocation.Invocation{}, UpdateRoleCommand{ID: role.ID, DisplayName: &displayName})
	require.True(t, apperror.Is(err, "role.built_in.protected"), "error = %v", err)
	require.Equal(t, []string{"authorize-manage", "get-role"}, events)
}

func TestRoleArchiveCommitsBeforeInvalidation(t *testing.T) {
	t.Parallel()
	events := []string{}
	role := &model.Role{ID: model.NewRoleID(), Name: "teacher", DisplayName: "Teacher"}
	service := newRoleService(roleServiceDependencies{Roles: &roleStoreFake{events: &events, role: role, archiveResult: role}, Authorization: &roleAuthorizerFake{events: &events, resource: model.Resource{Type: model.ResourceInstitution, ID: model.NewID()}}, Audit: &institutionAuditorFake{events: &events, beginID: model.NewID()}, Effects: &roleEffectsFake{events: &events}, Now: func() time.Time { return time.UnixMilli(500) }})
	require.NoError(t, service.ArchiveRole(context.Background(), invocation.Invocation{}, ArchiveRoleCommand{ID: role.ID}))
	// Contract (guardrails 8 and 14): authorization precedes the audit, the audit
	// begins before the durable write, and the write commits before its effect.
	require.Equal(t, []string{"authorize-manage", "get-role", "audit-begin", "store-archive", "invalidate-authorization"}, events)
}

func TestValidatePatchedPermissionsPreservesButDoesNotIntroduceUnknownActions(t *testing.T) {
	t.Parallel()
	current := []string{string(model.ActionClassView), "future.permission"}
	require.NoError(t, validatePatchedPermissions(current, nil))
	preserved := []string{string(model.ActionClassMembersView), "future.permission"}
	require.NoError(t, validatePatchedPermissions(current, &preserved))
	introduced := []string{"another.future_permission"}
	err := validatePatchedPermissions(current, &introduced)
	require.Error(t, err)
	require.True(t, apperror.Is(err, "role.permission.invalid"))
}

func TestRoleMappersPreserveApplicationFailureWrappers(t *testing.T) {
	t.Parallel()

	applicationFailure := apperror.New("authorization.denied")
	wrapped := errors.Join(errors.New("persistence context"), applicationFailure)
	for name, mapper := range map[string]func(error) error{
		"role":         roleStoreErrors.Translate,
		"role binding": roleBindingStoreErrors.Translate,
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, wrapped, mapper(wrapped), "mapper returned")
		})
	}
}
