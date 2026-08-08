// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

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
}

func (s *roleBindingStoreFake) Get(context.Context, string) (*model.RoleBinding, error) {
	*s.events = append(*s.events, "get-binding")
	return s.binding, s.getErr
}

func (s *roleBindingStoreFake) ListByUser(context.Context, string) ([]*model.RoleBinding, error) {
	*s.events = append(*s.events, "list-by-user")
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

type roleBindingEffectsFake struct{ events *[]string }

func (e *roleBindingEffectsFake) AuthorizationChangedForUser(context.Context, string) {
	*e.events = append(*e.events, "invalidate-authorization")
}

func TestRoleBindingCreateCommitsBeforeInvalidation(t *testing.T) {
	t.Parallel()
	events := []string{}
	userID := model.NewId()
	created := &model.RoleBinding{ID: model.NewRoleBindingID(), UserID: model.UserID(userID), RoleID: model.NewRoleID()}
	service := newRoleBindingService(
		&roleBindingStoreFake{events: &events, createResult: created},
		&roleBindingRoleStoreFake{events: &events, role: &model.Role{ID: created.RoleID, Name: "teacher"}},
		&roleAuthorizerFake{events: &events, resource: model.Resource{Type: model.ResourceInstitution, Id: model.NewId()}},
		&institutionAuditorFake{events: &events, beginID: model.NewId()},
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
	want := []string{"authorize-manage", "get-role", "audit-begin", "store-create", "invalidate-authorization"}
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
		&roleAuthorizerFake{events: &events, resource: model.Resource{Type: model.ResourceInstitution, Id: model.NewId()}},
		&institutionAuditorFake{events: &events},
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
	want := []string{"authorize-manage", "get-role"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestRoleBindingEndFailurePublishesNoInvalidation(t *testing.T) {
	t.Parallel()
	events := []string{}
	binding := &model.RoleBinding{ID: model.NewRoleBindingID(), UserID: model.NewUserID()}
	service := newRoleBindingService(
		&roleBindingStoreFake{
			events: &events, binding: binding,
			endErr: store.NewErrConflict("role_binding", "role_bindings_last_system_admin", errors.New("last")),
		},
		&roleBindingRoleStoreFake{events: &events},
		&roleAuthorizerFake{events: &events, resource: model.Resource{Type: model.ResourceInstitution, Id: model.NewId()}},
		&institutionAuditorFake{events: &events, beginID: model.NewId()},
		&roleBindingEffectsFake{events: &events},
		time.Now,
	)
	_, err := service.End(context.Background(), Invocation{}, EndRoleBindingCommand{ID: binding.ID.String()})
	if !Is(err, "role_binding.last_system_admin") {
		t.Fatalf("error = %v", err)
	}
	want := []string{"authorize-manage", "get-binding", "audit-begin", "store-end", "audit-fail"}
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
		&roleAuthorizerFake{events: &events, resource: model.Resource{Type: model.ResourceInstitution, Id: model.NewId()}},
		&institutionAuditorFake{events: &events},
		&roleBindingEffectsFake{events: &events},
		time.Now,
	)
	got, err := service.List(context.Background(), Invocation{}, ListRoleBindingsQuery{UserID: userID})
	if err != nil || len(got) != 1 {
		t.Fatalf("list = %#v err=%v", got, err)
	}
	want := []string{"authorize-manage", "list-by-user"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}
