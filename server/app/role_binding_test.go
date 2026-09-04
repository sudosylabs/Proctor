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
	"reflect"
	"testing"
	"time"

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

func (s *roleBindingStoreFake) Get(context.Context, string) (*model.RoleBinding, error) {
	*s.events = append(*s.events, "get-binding")
	return s.binding, s.getErr
}

func (s *roleBindingStoreFake) ListByUser(context.Context, string) ([]*model.RoleBinding, error) {
	*s.events = append(*s.events, "list-by-user")
	return s.list, s.listErr
}

func (s *roleBindingStoreFake) ListVisibleByUser(_ context.Context, _ string, visibility store.UserVisibilityScope) ([]*model.RoleBinding, error) {
	*s.events = append(*s.events, "list-visible-by-user")
	s.visibility = visibility
	return s.list, s.listErr
}

func (s *roleBindingStoreFake) ListByScope(context.Context, model.RoleScopeType, string) ([]*model.RoleBinding, error) {
	*s.events = append(*s.events, "list-by-scope")
	return s.list, s.listErr
}

func (s *roleBindingStoreFake) SaveWithAudit(_ context.Context, input *store.RoleBindingCreation) (*model.RoleBinding, error) {
	*s.events = append(*s.events, "store-create")
	s.createInput = input
	return s.createResult, s.createErr
}

func (s *roleBindingStoreFake) EndWithAudit(_ context.Context, input *store.RoleBindingEnd) (*model.RoleBinding, error) {
	*s.events = append(*s.events, "store-end")
	s.endInput = input
	return s.endResult, s.endErr
}

type roleBindingRoleStoreFake struct {
	events *[]string
	role   *model.Role
	err    error
}

func (s *roleBindingRoleStoreFake) Get(context.Context, string) (*model.Role, error) {
	*s.events = append(*s.events, "get-role")
	return s.role, s.err
}

func (s *roleBindingRoleStoreFake) GetIncludingArchived(context.Context, string) (*model.Role, error) {
	*s.events = append(*s.events, "get-role-including-archived")
	return s.role, s.err
}

type roleBindingEffectsFake struct{ events *[]string }

func (e *roleBindingEffectsFake) AuthorizationChangedForUser(context.Context, string) {
	*e.events = append(*e.events, "invalidate-authorization")
}

func TestRoleBindingCreateCommitsBeforeInvalidation(t *testing.T) {
	t.Parallel()
	events := []string{}
	userID := model.NewId()
	created := &model.RoleBinding{ID: model.NewRoleBindingID(), UserID: model.UserID(userID), RoleID: model.NewRoleID()}
	mail := &relationshipMailPreparerTestFake{}
	service := newRoleBindingService(
		&roleBindingStoreFake{events: &events, createResult: created},
		&roleBindingRoleStoreFake{events: &events, role: &model.Role{ID: created.RoleID, Name: "teacher"}},
		relationshipUserStoreTestFake{},
		&roleAuthorizerFake{events: &events, resource: model.Resource{Type: model.ResourceInstitution, ID: model.NewId()}},
		&accessPolicyCapabilitiesFake{},
		&institutionAuditorFake{events: &events, beginID: model.NewId()},
		mail,
		&roleBindingEffectsFake{events: &events},
		func() time.Time { return time.UnixMilli(500) },
	)
	got, err := service.Create(context.Background(), Invocation{}, CreateRoleBindingCommand{
		UserID: userID, RoleID: created.RoleID.String(), ScopeType: model.RoleScopeInstitution,
		ScopeID: model.NewId(), StartAt: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != created.ID {
		t.Fatalf("result = %#v", got)
	}
	if len(mail.requests) != 1 || mail.requests[0].TemplateKey != model.MailTemplateAuthorizationInstitutionRoleAssigned ||
		service.bindings.(*roleBindingStoreFake).createInput.Notice == nil {
		t.Fatalf("role assignment mail = %#v", mail.requests)
	}
	want := []string{"authorize-binding-scope", "get-role", "authorize-delegation", "audit-begin", "store-create", "invalidate-authorization"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestRoleBindingCreateRejectsSystemAdminOutsideInstitution(t *testing.T) {
	t.Parallel()
	events := []string{}
	roleID := model.NewId()
	service := newRoleBindingService(
		&roleBindingStoreFake{events: &events},
		&roleBindingRoleStoreFake{events: &events, role: &model.Role{ID: model.RoleID(roleID), Name: model.SystemAdministratorRoleName}},
		relationshipUserStoreTestFake{},
		&roleAuthorizerFake{events: &events, resource: model.Resource{Type: model.ResourceInstitution, ID: model.NewId()}},
		&accessPolicyCapabilitiesFake{},
		&institutionAuditorFake{events: &events},
		&relationshipMailPreparerTestFake{},
		&roleBindingEffectsFake{events: &events},
		time.Now,
	)
	_, err := service.Create(context.Background(), Invocation{}, CreateRoleBindingCommand{
		UserID: model.NewId(), RoleID: roleID, ScopeType: model.RoleScopeClass,
		ScopeID: model.NewId(), StartAt: 100,
	})
	if !Is(err, "role_binding.system_admin_requires_institution_scope") {
		t.Fatalf("error = %v", err)
	}
	want := []string{"authorize-binding-scope", "get-role"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestRoleBindingRetainedCreateUsesArchivedRoleForCurrentDelegation(t *testing.T) {
	t.Parallel()
	events := []string{}
	userID, roleID, scopeID := model.NewUserID(), model.NewRoleID(), model.NewId()
	created := &model.RoleBinding{ID: model.NewRoleBindingID(), UserID: userID, RoleID: roleID, ScopeType: model.RoleScopeInstitution, ScopeID: scopeID}
	persistence := &roleBindingStoreFake{events: &events, createResult: created}
	archivedRole := &model.Role{ID: roleID, Name: "teacher", Permissions: []string{string(model.ActionClassMembersManage)},
		UpdatedAt: model.TimeFromMillis(300), ArchivedAt: model.OptionalTimeFromMillis(300)}
	service := newRoleBindingService(persistence,
		&roleBindingRoleStoreFake{events: &events, role: archivedRole},
		relationshipUserStoreTestFake{},
		&roleAuthorizerFake{events: &events, resource: model.Resource{Type: model.ResourceInstitution, ID: scopeID}},
		&accessPolicyCapabilitiesFake{}, &institutionAuditorFake{events: &events, beginID: model.NewId()}, &relationshipMailPreparerTestFake{},
		&roleBindingEffectsFake{events: &events}, func() time.Time { return time.UnixMilli(500) })
	result, err := service.Create(context.Background(), NewInvocation(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{}),
		CreateRoleBindingCommand{UserID: userID.String(), RoleID: roleID.String(), ScopeType: model.RoleScopeInstitution,
			ScopeID: scopeID, StartAt: 100, IdempotencyKey: "row", batchRetainedOutcome: true})
	if err != nil || result == nil || persistence.createInput == nil {
		t.Fatalf("retained create = %#v, %v, input %#v", result, err, persistence.createInput)
	}
	if want := []string{"authorize-binding-scope", "get-role-including-archived", "authorize-delegation", "audit-begin", "store-create", "invalidate-authorization"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestRoleBindingEndFailurePublishesNoInvalidation(t *testing.T) {
	t.Parallel()
	events := []string{}
	binding := &model.RoleBinding{ID: model.NewRoleBindingID(), UserID: model.NewUserID(), ScopeType: model.RoleScopeClass, ScopeID: model.NewId()}
	mail := &relationshipMailPreparerTestFake{}
	persistence := &roleBindingStoreFake{
		events: &events, binding: binding,
		endErr: store.NewErrConflict("role_binding", "role_bindings_last_system_admin", errors.New("last")),
	}
	service := newRoleBindingService(
		persistence,
		&roleBindingRoleStoreFake{events: &events},
		relationshipUserStoreTestFake{},
		&roleAuthorizerFake{events: &events, resource: model.Resource{Type: model.ResourceInstitution, ID: model.NewId()}},
		&accessPolicyCapabilitiesFake{snapshot: AccessPolicyCapabilitySnapshot{Providers: []AccessPolicyProviderCapability{{
			Descriptor: model.ExternalAuthenticationProvider{Id: "campus", Type: "oidc"},
		}}}},
		&institutionAuditorFake{events: &events, beginID: model.NewId()},
		mail,
		&roleBindingEffectsFake{events: &events},
		time.Now,
	)
	_, err := service.End(context.Background(), Invocation{}, EndRoleBindingCommand{ID: binding.ID.String()})
	if !Is(err, "role_binding.last_system_admin") {
		t.Fatalf("error = %v", err)
	}
	if _, available := persistence.endInput.Capabilities.Providers["campus"]; !available {
		t.Fatalf("Store capability snapshot = %#v, want configured campus provider", persistence.endInput.Capabilities)
	}
	if persistence.endInput.Notice == nil || len(mail.requests) != 1 ||
		mail.requests[0].TemplateKey != model.MailTemplateAuthorizationScopedRoleEnded {
		t.Fatalf("role ending mail = %#v / %#v", persistence.endInput, mail.requests)
	}
	want := []string{"authorize-binding-preflight", "get-binding", "authorize-binding-scope", "audit-begin", "store-end", "audit-fail"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestRoleBindingListByUserAuthorizesThenReads(t *testing.T) {
	t.Parallel()
	events := []string{}
	userID := model.NewId()
	service := newRoleBindingService(
		&roleBindingStoreFake{events: &events, list: []*model.RoleBinding{{ID: model.NewRoleBindingID(), UserID: model.UserID(userID)}}},
		&roleBindingRoleStoreFake{events: &events},
		relationshipUserStoreTestFake{},
		&roleAuthorizerFake{events: &events, resource: model.Resource{Type: model.ResourceInstitution, ID: model.NewId()}},
		&accessPolicyCapabilitiesFake{},
		&institutionAuditorFake{events: &events},
		&relationshipMailPreparerTestFake{},
		&roleBindingEffectsFake{events: &events},
		time.Now,
	)
	got, err := service.List(context.Background(), Invocation{}, ListRoleBindingsQuery{UserID: userID})
	if err != nil || len(got) != 1 {
		t.Fatalf("list = %#v err=%v", got, err)
	}
	want := []string{"authorize-binding-list", "list-visible-by-user"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestRoleBindingListByUserPassesAcademicScopeToPersistence(t *testing.T) {
	t.Parallel()
	events := []string{}
	rootID := model.NewAcademicUnitID().String()
	persistence := &roleBindingStoreFake{events: &events}
	service := newRoleBindingService(
		persistence, &roleBindingRoleStoreFake{events: &events},
		relationshipUserStoreTestFake{},
		&roleAuthorizerFake{events: &events, bindingScope: store.UserVisibilityScope{AcademicUnitRootIDs: []string{rootID}}},
		&accessPolicyCapabilitiesFake{},
		&institutionAuditorFake{events: &events}, &relationshipMailPreparerTestFake{}, &roleBindingEffectsFake{events: &events}, time.Now,
	)
	if _, err := service.List(context.Background(), Invocation{}, ListRoleBindingsQuery{UserID: model.NewUserID().String()}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(persistence.visibility.AcademicUnitRootIDs, []string{rootID}) || persistence.visibility.InstitutionWide {
		t.Fatalf("visibility = %#v", persistence.visibility)
	}
}
