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

func (s *roleStoreFake) Get(context.Context, string) (*model.Role, error) {
	*s.events = append(*s.events, "get-role")
	return s.role, s.getErr
}

func (s *roleStoreFake) List(context.Context) ([]*model.Role, error) {
	*s.events = append(*s.events, "list-roles")
	return s.list, s.listErr
}

func (s *roleStoreFake) SaveWithAudit(_ context.Context, input *store.RoleCreation) (*model.Role, error) {
	*s.events = append(*s.events, "store-create")
	s.createInput = input
	return s.createResult, s.createErr
}

func (s *roleStoreFake) UpdateWithAudit(_ context.Context, input *store.RoleUpdate) (*model.Role, error) {
	*s.events = append(*s.events, "store-update")
	s.updateInput = input
	return s.updateResult, s.updateErr
}

func (s *roleStoreFake) ArchiveWithAudit(_ context.Context, input *store.RoleArchive) (*model.Role, error) {
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

func (a *roleAuthorizerFake) AuthorizeManage(context.Context, Invocation) (model.Resource, error) {
	*a.events = append(*a.events, "authorize-manage")
	return a.resource, a.err
}

func (a *roleAuthorizerFake) AuthorizeView(context.Context, Invocation) (model.Resource, error) {
	*a.events = append(*a.events, "authorize-view")
	return a.resource, a.err
}

func (a *roleAuthorizerFake) AuthorizeRoleBindingInstitution(context.Context, Invocation, model.Action) (model.Resource, error) {
	*a.events = append(*a.events, "authorize-binding-institution")
	return a.resource, a.err
}

func (a *roleAuthorizerFake) AuthorizeRoleBindingList(context.Context, Invocation, model.Action) (store.UserVisibilityScope, error) {
	*a.events = append(*a.events, "authorize-binding-list")
	if a.bindingScope.InstitutionWide || len(a.bindingScope.AcademicUnitRootIDs) > 0 || len(a.bindingScope.ClassIDs) > 0 {
		return a.bindingScope, a.err
	}
	return store.UserVisibilityScope{InstitutionWide: true}, a.err
}

func (a *roleAuthorizerFake) AuthorizeRoleBindingPreflight(context.Context, Invocation, model.Action) error {
	*a.events = append(*a.events, "authorize-binding-preflight")
	return a.err
}

func (a *roleAuthorizerFake) AuthorizeRoleBindingScope(context.Context, Invocation, model.Action, model.RoleScopeType, string) (model.Resource, error) {
	*a.events = append(*a.events, "authorize-binding-scope")
	return a.resource, a.err
}

func (a *roleAuthorizerFake) CanDelegateActionsAtScope(context.Context, Invocation, []string, model.RoleScopeType, string) error {
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
	resource := model.Resource{Type: model.ResourceInstitution, ID: model.NewId()}
	created := &model.Role{ID: model.NewRoleID(), Name: "teacher", DisplayName: "Teacher", Permissions: []string{string(model.ActionClassView)}}
	persistence := &roleStoreFake{events: &events, createResult: created}
	service := newRoleService(
		persistence,
		&roleAuthorizerFake{events: &events, resource: resource},
		&institutionAuditorFake{events: &events, beginID: model.NewId()},
		&roleEffectsFake{events: &events},
		func() time.Time { return time.UnixMilli(500) },
	)
	got, err := service.Create(context.Background(), Invocation{}, CreateRoleCommand{
		Name: "teacher", DisplayName: "Teacher", Permissions: []string{string(model.ActionClassView)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID.String() != created.ID.String() || persistence.createInput.AuditEventID == "" {
		t.Fatalf("result/input = %#v / %#v", got, persistence.createInput)
	}
	want := []string{"authorize-manage", "authorize-delegation", "audit-begin", "store-create"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestRoleCreateAndUpdateRejectUndelegablePermissionsBeforeAudit(t *testing.T) {
	t.Parallel()
	denied := authorizationDeniedError("test")

	t.Run("create", func(t *testing.T) {
		events := []string{}
		service := newRoleService(
			&roleStoreFake{events: &events},
			&roleAuthorizerFake{events: &events, resource: model.Resource{Type: model.ResourceInstitution, ID: model.NewId()}, delegationErr: denied},
			&institutionAuditorFake{events: &events}, &roleEffectsFake{events: &events}, time.Now,
		)
		_, err := service.Create(context.Background(), Invocation{}, CreateRoleCommand{Name: "unsafe", DisplayName: "Unsafe", Permissions: []string{string(model.ActionUserManage)}})
		if !Is(err, "authorization.denied") {
			t.Fatalf("Create() error = %v", err)
		}
		if want := []string{"authorize-manage", "authorize-delegation"}; !reflect.DeepEqual(events, want) {
			t.Fatalf("events = %v, want %v", events, want)
		}
	})

	t.Run("update", func(t *testing.T) {
		events := []string{}
		role := &model.Role{ID: model.NewRoleID(), Name: "teacher", DisplayName: "Teacher", Permissions: []string{string(model.ActionClassView)}}
		permissions := []string{string(model.ActionClassView), string(model.ActionUserManage)}
		service := newRoleService(
			&roleStoreFake{events: &events, role: role},
			&roleAuthorizerFake{events: &events, resource: model.Resource{Type: model.ResourceInstitution, ID: model.NewId()}, delegationErr: denied},
			&institutionAuditorFake{events: &events}, &roleEffectsFake{events: &events}, time.Now,
		)
		_, err := service.Update(context.Background(), Invocation{}, UpdateRoleCommand{ID: role.ID.String(), Permissions: &permissions})
		if !Is(err, "authorization.denied") {
			t.Fatalf("Update() error = %v", err)
		}
		if want := []string{"authorize-manage", "get-role", "authorize-delegation"}; !reflect.DeepEqual(events, want) {
			t.Fatalf("events = %v, want %v", events, want)
		}
	})
}

func TestRoleCreateRejectsUnknownPermissions(t *testing.T) {
	t.Parallel()
	events := []string{}
	service := newRoleService(
		&roleStoreFake{events: &events},
		&roleAuthorizerFake{events: &events, resource: model.Resource{Type: model.ResourceInstitution, ID: model.NewId()}},
		&institutionAuditorFake{events: &events},
		&roleEffectsFake{events: &events},
		time.Now,
	)
	_, err := service.Create(context.Background(), Invocation{}, CreateRoleCommand{
		Name: "teacher", DisplayName: "Teacher", Permissions: []string{"not.a.real.permission"},
	})
	if !Is(err, "role.permission.unknown") {
		t.Fatalf("error = %v", err)
	}
	want := []string{"authorize-manage"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestRoleCreateRejectsRelationshipOnlyParticipationAction(t *testing.T) {
	t.Parallel()

	err := validateKnownPermissions([]string{string(model.ActionExamSittingParticipate)})
	if err == nil || !Is(err, "role.permission.unknown") {
		t.Fatalf("validateKnownPermissions() error = %v, want role.permission.unknown", err)
	}
}

func TestRoleUpdateCommitsBeforeInvalidation(t *testing.T) {
	t.Parallel()
	events := []string{}
	role := &model.Role{ID: model.NewRoleID(), Name: "teacher", DisplayName: "Teacher", Permissions: []string{string(model.ActionClassView)}}
	updated := role.Clone()
	updated.DisplayName = "Lead Teacher"
	displayName := "Lead Teacher"
	persistence := &roleStoreFake{events: &events, role: role, updateResult: updated}
	service := newRoleService(
		persistence,
		&roleAuthorizerFake{events: &events, resource: model.Resource{Type: model.ResourceInstitution, ID: model.NewId()}},
		&institutionAuditorFake{events: &events, beginID: model.NewId()},
		&roleEffectsFake{events: &events},
		func() time.Time { return time.UnixMilli(500) },
	)
	got, err := service.Update(context.Background(), Invocation{}, UpdateRoleCommand{ID: role.ID.String(), DisplayName: &displayName})
	if err != nil {
		t.Fatal(err)
	}
	if got.DisplayName != "Lead Teacher" {
		t.Fatalf("result = %#v", got)
	}
	want := []string{"authorize-manage", "get-role", "audit-begin", "store-update", "invalidate-authorization"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestRoleUpdateFailurePublishesNoInvalidation(t *testing.T) {
	t.Parallel()
	events := []string{}
	role := &model.Role{ID: model.NewRoleID(), Name: "teacher", DisplayName: "Teacher", Permissions: []string{string(model.ActionClassView)}}
	displayName := "Lead Teacher"
	service := newRoleService(
		&roleStoreFake{events: &events, role: role, updateErr: store.NewErrConflict("role", "roles_name_key", errors.New("dup"))},
		&roleAuthorizerFake{events: &events, resource: model.Resource{Type: model.ResourceInstitution, ID: model.NewId()}},
		&institutionAuditorFake{events: &events, beginID: model.NewId()},
		&roleEffectsFake{events: &events},
		time.Now,
	)
	_, err := service.Update(context.Background(), Invocation{}, UpdateRoleCommand{ID: role.ID.String(), DisplayName: &displayName})
	if !Is(err, "role.conflict") {
		t.Fatalf("error = %v", err)
	}
	want := []string{"authorize-manage", "get-role", "audit-begin", "store-update", "audit-fail"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestRoleUpdateRejectsBuiltIn(t *testing.T) {
	t.Parallel()
	events := []string{}
	role := &model.Role{ID: model.NewRoleID(), Name: model.SystemAdministratorRoleName, DisplayName: "Admin", BuiltIn: true}
	displayName := "Nope"
	service := newRoleService(
		&roleStoreFake{events: &events, role: role},
		&roleAuthorizerFake{events: &events, resource: model.Resource{Type: model.ResourceInstitution, ID: model.NewId()}},
		&institutionAuditorFake{events: &events},
		&roleEffectsFake{events: &events},
		time.Now,
	)
	_, err := service.Update(context.Background(), Invocation{}, UpdateRoleCommand{ID: role.ID.String(), DisplayName: &displayName})
	if !Is(err, "role.built_in.protected") {
		t.Fatalf("error = %v", err)
	}
	want := []string{"authorize-manage", "get-role"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestRoleArchiveCommitsBeforeInvalidation(t *testing.T) {
	t.Parallel()
	events := []string{}
	role := &model.Role{ID: model.NewRoleID(), Name: "teacher", DisplayName: "Teacher"}
	service := newRoleService(
		&roleStoreFake{events: &events, role: role, archiveResult: role},
		&roleAuthorizerFake{events: &events, resource: model.Resource{Type: model.ResourceInstitution, ID: model.NewId()}},
		&institutionAuditorFake{events: &events, beginID: model.NewId()},
		&roleEffectsFake{events: &events},
		func() time.Time { return time.UnixMilli(500) },
	)
	if err := service.Archive(context.Background(), Invocation{}, ArchiveRoleCommand{ID: role.ID.String()}); err != nil {
		t.Fatal(err)
	}
	want := []string{"authorize-manage", "get-role", "audit-begin", "store-archive", "invalidate-authorization"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestValidatePatchedPermissionsPreservesButDoesNotIntroduceUnknownActions(t *testing.T) {
	t.Parallel()
	current := []string{string(model.ActionClassView), "future.permission"}
	if err := validatePatchedPermissions(current, nil); err != nil {
		t.Fatalf("display-only patch rejected existing unknown permission: %v", err)
	}
	preserved := []string{string(model.ActionClassMembersView), "future.permission"}
	if err := validatePatchedPermissions(current, &preserved); err != nil {
		t.Fatalf("preserved unknown permission rejected: %v", err)
	}
	introduced := []string{"another.future_permission"}
	if err := validatePatchedPermissions(current, &introduced); err == nil || !Is(err, "role.permission.unknown") {
		t.Fatalf("new unknown permission error = %v", err)
	}
}

func TestRoleMappersPreserveApplicationFailureWrappers(t *testing.T) {
	t.Parallel()

	applicationFailure := NewError("authorization.denied")
	wrapped := errors.Join(errors.New("persistence context"), applicationFailure)
	for name, mapper := range map[string]func(error) error{
		"role":         roleError,
		"role binding": roleBindingError,
	} {
		t.Run(name, func(t *testing.T) {
			if got := mapper(wrapped); got != wrapped {
				t.Fatalf("mapper returned %v, want original wrapper", got)
			}
		})
	}
}
