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

type roleBindingStoreFake struct {
	events       *[]string
	binding      *model.RoleBinding
	list         []*model.RoleBinding
	createInput  *store.RoleBindingCreation
	endInput     *store.RoleBindingEnd
	createResult *model.RoleBinding
	endResult    *model.RoleBinding
	getErr       error
	listErr      error
	createErr    error
	endErr       error
	visibility   store.UserVisibilityScope
}

func (s *roleBindingStoreFake) Get(context.Context, model.RoleBindingID) (*model.RoleBinding, error) {
	*s.events = append(*s.events, "get-binding")
	return s.binding, s.getErr
}

func (s *roleBindingStoreFake) ListByUser(context.Context, model.UserID) ([]*model.RoleBinding, error) {
	*s.events = append(*s.events, "list-by-user")
	return s.list, s.listErr
}

func (s *roleBindingStoreFake) ListVisibleByUser(_ context.Context, _ model.UserID, visibility store.UserVisibilityScope) ([]*model.RoleBinding, error) {
	*s.events = append(*s.events, "list-visible-by-user")
	s.visibility = visibility
	return s.list, s.listErr
}

func (s *roleBindingStoreFake) ListByScope(context.Context, model.RoleScopeType, string) ([]*model.RoleBinding, error) {
	*s.events = append(*s.events, "list-by-scope")
	return s.list, s.listErr
}

func (s *roleBindingStoreFake) Save(_ context.Context, input *store.RoleBindingCreation) (*model.RoleBinding, error) {
	*s.events = append(*s.events, "store-create")
	s.createInput = input
	return s.createResult, s.createErr
}

func (s *roleBindingStoreFake) End(_ context.Context, input *store.RoleBindingEnd) (*model.RoleBinding, error) {
	*s.events = append(*s.events, "store-end")
	s.endInput = input
	return s.endResult, s.endErr
}

type roleBindingRoleStoreFake struct {
	events *[]string
	role   *model.Role
	err    error
}

func (s *roleBindingRoleStoreFake) Get(context.Context, model.RoleID) (*model.Role, error) {
	*s.events = append(*s.events, "get-role")
	return s.role, s.err
}

func (s *roleBindingRoleStoreFake) GetIncludingArchived(context.Context, model.RoleID) (*model.Role, error) {
	*s.events = append(*s.events, "get-role-including-archived")
	return s.role, s.err
}

type roleBindingEffectsFake struct{ events *[]string }

func (e *roleBindingEffectsFake) AuthorizationChangedForUser(context.Context, model.UserID) {
	*e.events = append(*e.events, "invalidate-authorization")
}

func TestRoleBindingCreateCommitsBeforeInvalidation(t *testing.T) {
	t.Parallel()
	events := []string{}
	userID := model.NewID()
	created := &model.RoleBinding{ID: model.NewRoleBindingID(), UserID: model.UserID(userID), RoleID: model.NewRoleID()}
	mail := &relationshipMailPreparerTestFake{}
	service := newRoleBindingService(roleBindingServiceDependencies{Bindings: &roleBindingStoreFake{events: &events, createResult: created}, Roles: &roleBindingRoleStoreFake{events: &events, role: &model.Role{ID: created.RoleID, Name: "teacher"}}, Users: relationshipUserStoreTestFake{}, Authorization: &roleAuthorizerFake{events: &events, resource: model.Resource{Type: model.ResourceInstitution, ID: model.NewID()}}, Capabilities: &accessPolicyCapabilitiesFake{}, Audit: &institutionAuditorFake{events: &events, beginID: model.NewID()}, Mail: mail, Effects: &roleBindingEffectsFake{events: &events}, Now: func() time.Time { return time.UnixMilli(500) }})
	got, err := service.CreateRoleBinding(context.Background(), invocation.Invocation{}, CreateRoleBindingCommand{
		UserID: model.UserID(userID), RoleID: created.RoleID, ScopeType: model.RoleScopeInstitution,
		ScopeID: model.NewID(), StartAt: 100,
	})
	require.NoError(t, err)
	require.Equal(t, created.ID, got.ID)
	require.Len(t, mail.requests, 1)
	require.Equal(t, model.MailTemplateAuthorizationInstitutionRoleAssigned, mail.requests[0].TemplateKey)
	require.NotNil(t, service.deps.Bindings.(*roleBindingStoreFake).createInput.Notice)
	// Contract (guardrails 8 and 14): authorization precedes the audit, the audit
	// begins before the durable write, and the write commits before its effect.
	require.Equal(t, []string{"authorize-binding-scope", "get-role", "authorize-delegation", "audit-begin", "store-create", "invalidate-authorization"}, events)
}

func TestRoleBindingCreateRejectsSystemAdminOutsideInstitution(t *testing.T) {
	t.Parallel()
	events := []string{}
	roleID := model.NewID()
	service := newRoleBindingService(roleBindingServiceDependencies{Bindings: &roleBindingStoreFake{events: &events}, Roles: &roleBindingRoleStoreFake{events: &events, role: &model.Role{ID: model.RoleID(roleID), Name: model.SystemAdministratorRoleName}}, Users: relationshipUserStoreTestFake{}, Authorization: &roleAuthorizerFake{events: &events, resource: model.Resource{Type: model.ResourceInstitution, ID: model.NewID()}}, Capabilities: &accessPolicyCapabilitiesFake{}, Audit: &institutionAuditorFake{events: &events}, Mail: &relationshipMailPreparerTestFake{}, Effects: &roleBindingEffectsFake{events: &events}, Now: time.Now})
	_, err := service.CreateRoleBinding(context.Background(), invocation.Invocation{}, CreateRoleBindingCommand{
		UserID: model.UserID(model.NewID()), RoleID: model.RoleID(roleID), ScopeType: model.RoleScopeClass,
		ScopeID: model.NewID(), StartAt: 100,
	})
	require.True(t, apperror.Is(err, "role_binding.system_admin_scope_invalid"), "error = %v", err)
	require.Equal(t, []string{"authorize-binding-scope", "get-role"}, events)
}

func TestRoleBindingRetainedCreateUsesArchivedRoleForCurrentDelegation(t *testing.T) {
	t.Parallel()
	events := []string{}
	userID, roleID, scopeID := model.NewUserID(), model.NewRoleID(), model.NewID()
	created := &model.RoleBinding{ID: model.NewRoleBindingID(), UserID: userID, RoleID: roleID, ScopeType: model.RoleScopeInstitution, ScopeID: scopeID}
	persistence := &roleBindingStoreFake{events: &events, createResult: created}
	archivedRole := &model.Role{ID: roleID, Name: "teacher", Permissions: []string{string(model.ActionClassMembersManage)},
		UpdatedAt: model.TimeFromMillis(300), ArchivedAt: model.OptionalTimeFromMillis(300)}
	service := newRoleBindingService(roleBindingServiceDependencies{Bindings: persistence, Roles: &roleBindingRoleStoreFake{events: &events, role: archivedRole}, Users: relationshipUserStoreTestFake{}, Authorization: &roleAuthorizerFake{events: &events, resource: model.Resource{Type: model.ResourceInstitution, ID: scopeID}}, Capabilities: &accessPolicyCapabilitiesFake{}, Audit: &institutionAuditorFake{events: &events, beginID: model.NewID()}, Mail: &relationshipMailPreparerTestFake{}, Effects: &roleBindingEffectsFake{events: &events}, Now: func() time.Time { return time.UnixMilli(500) }})
	result, err := service.CreateRoleBinding(context.Background(), invocation.New(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{}),
		CreateRoleBindingCommand{UserID: userID, RoleID: roleID, ScopeType: model.RoleScopeInstitution,
			ScopeID: scopeID, StartAt: 100, IdempotencyKey: "row", batchRetainedOutcome: true})
	require.NoError(t, err, "retained create")
	require.NotNil(t, result, "retained create")
	require.NotNil(t, persistence.createInput, "retained create")
	// Contract (guardrails 8 and 14): authorization precedes the audit, the audit
	// begins before the durable write, and the write commits before its effect.
	require.Equal(t, []string{"authorize-binding-scope", "get-role-including-archived", "authorize-delegation", "audit-begin", "store-create", "invalidate-authorization"}, events)
}

func TestRoleBindingEndFailurePublishesNoInvalidation(t *testing.T) {
	t.Parallel()
	events := []string{}
	binding := &model.RoleBinding{ID: model.NewRoleBindingID(), UserID: model.NewUserID(), ScopeType: model.RoleScopeClass, ScopeID: model.NewID()}
	mail := &relationshipMailPreparerTestFake{}
	persistence := &roleBindingStoreFake{
		events: &events, binding: binding,
		endErr: store.NewErrConflict("role_binding", "role_bindings_last_system_admin", errors.New("last")),
	}
	service := newRoleBindingService(roleBindingServiceDependencies{Bindings: persistence, Roles: &roleBindingRoleStoreFake{events: &events}, Users: relationshipUserStoreTestFake{}, Authorization: &roleAuthorizerFake{events: &events, resource: model.Resource{Type: model.ResourceInstitution, ID: model.NewID()}}, Capabilities: &accessPolicyCapabilitiesFake{snapshot: AccessPolicyCapabilitySnapshot{Providers: []AccessPolicyProviderCapability{{
		Descriptor: model.ExternalAuthenticationProvider{ID: "campus", Type: "oidc"},
	}}}}, Audit: &institutionAuditorFake{events: &events, beginID: model.NewID()}, Mail: mail, Effects: &roleBindingEffectsFake{events: &events}, Now: time.Now})
	_, err := service.EndRoleBinding(context.Background(), invocation.Invocation{}, EndRoleBindingCommand{ID: binding.ID})
	require.True(t, apperror.Is(err, "role_binding.last_system_admin"), "error = %v", err)
	_, available := persistence.endInput.Capabilities.Providers["campus"]
	require.True(t, available, "Store capability snapshot = %#v, want configured campus provider", persistence.endInput.Capabilities)
	require.NotNil(t, persistence.endInput.Notice, "role ending mail = %#v", persistence.endInput)
	require.Len(t, mail.requests, 1, "role ending mail = %#v", persistence.endInput)
	require.Equal(t, model.MailTemplateAuthorizationScopedRoleEnded, mail.requests[0].TemplateKey, "role ending mail = %#v", persistence.endInput)
	// Contract (guardrail 8): authorization precedes the audit, the audit begins
	// before the durable write, and the audit outcome follows the write.
	require.Equal(t, []string{"authorize-binding-preflight", "get-binding", "authorize-binding-scope", "audit-begin", "store-end", "audit-fail"}, events)
}

func TestRoleBindingListByUserAuthorizesThenReads(t *testing.T) {
	t.Parallel()
	events := []string{}
	userID := model.NewID()
	service := newRoleBindingService(roleBindingServiceDependencies{Bindings: &roleBindingStoreFake{events: &events, list: []*model.RoleBinding{{ID: model.NewRoleBindingID(), UserID: model.UserID(userID)}}}, Roles: &roleBindingRoleStoreFake{events: &events}, Users: relationshipUserStoreTestFake{}, Authorization: &roleAuthorizerFake{events: &events, resource: model.Resource{Type: model.ResourceInstitution, ID: model.NewID()}}, Capabilities: &accessPolicyCapabilitiesFake{}, Audit: &institutionAuditorFake{events: &events}, Mail: &relationshipMailPreparerTestFake{}, Effects: &roleBindingEffectsFake{events: &events}, Now: time.Now})
	got, err := service.ListRoleBindings(context.Background(), invocation.Invocation{}, ListRoleBindingsQuery{UserID: model.UserID(userID)})
	require.NoError(t, err, "list")
	require.Len(t, got, 1, "list")
	// Contract: authorization precedes the read.
	require.Equal(t, []string{"authorize-binding-list", "list-visible-by-user"}, events)
}

func TestRoleBindingListByUserPassesAcademicScopeToPersistence(t *testing.T) {
	t.Parallel()
	events := []string{}
	rootID := model.NewAcademicUnitID().String()
	persistence := &roleBindingStoreFake{events: &events}
	service := newRoleBindingService(roleBindingServiceDependencies{Bindings: persistence, Roles: &roleBindingRoleStoreFake{events: &events}, Users: relationshipUserStoreTestFake{}, Authorization: &roleAuthorizerFake{events: &events, bindingScope: store.UserVisibilityScope{AcademicUnitRootIDs: []model.AcademicUnitID{model.AcademicUnitID(rootID)}}}, Capabilities: &accessPolicyCapabilitiesFake{}, Audit: &institutionAuditorFake{events: &events}, Mail: &relationshipMailPreparerTestFake{}, Effects: &roleBindingEffectsFake{events: &events}, Now: time.Now})
	_, err := service.ListRoleBindings(context.Background(), invocation.Invocation{}, ListRoleBindingsQuery{UserID: model.NewUserID()})
	require.NoError(t, err)
	require.Equal(t, []model.AcademicUnitID{model.AcademicUnitID(rootID)}, persistence.visibility.AcademicUnitRootIDs)
	require.False(t, persistence.visibility.InstitutionWide)
}
