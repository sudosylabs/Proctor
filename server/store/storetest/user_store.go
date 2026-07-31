// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/channels/store/storetest/user_store.go.

package storetest

import (
	"context"
	"errors"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestUserStore(t *testing.T, ss store.Store) {
	t.Run("SaveAndGet", func(t *testing.T) { testUserStoreSaveAndGet(t, ss) })
	t.Run("NormalizedLookups", func(t *testing.T) { testUserStoreNormalizedLookups(t, ss) })
	t.Run("Update", func(t *testing.T) { testUserStoreUpdate(t, ss) })
	t.Run("UpdateLastLogin", func(t *testing.T) { testUserStoreUpdateLastLogin(t, ss) })
	t.Run("Uniqueness", func(t *testing.T) { testUserStoreUniqueness(t, ss) })
	t.Run("ListAndDisable", func(t *testing.T) { testUserStoreListAndDisable(t, ss) })
	t.Run("ProtectLastAdministrator", func(t *testing.T) {
		testUserStoreProtectLastAdministrator(t, ss)
	})
}

func testUserStoreProtectLastAdministrator(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	role, err := ss.Role().Save(ctx, &model.Role{
		Name:        model.SystemAdministratorRoleName,
		DisplayName: "System Administrator",
		BuiltIn:     true,
		Permissions: model.AllActions(),
	})
	requireNoError(t, err)
	first := saveUser(t, ctx, ss)
	firstBinding, err := ss.RoleBinding().Save(ctx, &model.RoleBinding{
		UserId: first.Id, RoleId: role.Id,
		ScopeType: model.RoleScopeInstitution, ScopeId: institution.Id,
		StartAt: model.GetMillis() - 100,
	})
	requireNoError(t, err)
	at := model.GetMillis()
	_, err = ss.User().SetDisabled(ctx, first.Id, at, at)
	var conflict *store.ErrConflict
	if !errors.As(err, &conflict) ||
		conflict.Constraint != "users_last_system_admin" {
		t.Fatalf("disable last administrator error = %v", err)
	}
	second := saveUser(t, ctx, ss)
	secondBinding, err := ss.RoleBinding().Save(ctx, &model.RoleBinding{
		UserId: second.Id, RoleId: role.Id,
		ScopeType: model.RoleScopeInstitution, ScopeId: institution.Id,
		StartAt: at - 100,
	})
	requireNoError(t, err)
	_, err = ss.User().SetDisabled(ctx, first.Id, at, at)
	requireNoError(t, err)
	if _, err = ss.RoleBinding().End(ctx, secondBinding.Id, at+1); !errors.As(err, &conflict) {
		t.Fatalf("end only enabled administrator binding error = %v", err)
	}
	if _, err = ss.RoleBinding().End(ctx, firstBinding.Id, at+1); err != nil {
		t.Fatalf("ending disabled administrator binding should be allowed: %v", err)
	}
}

func testUserStoreListAndDisable(t *testing.T, ss store.Store) {
	ctx := context.Background()
	first := newUser()
	first.Username = "aaa-" + model.NewId()
	first.DisplayName = "Searchable Alpha"
	first, err := ss.User().Save(ctx, first)
	requireNoError(t, err)
	second := newUser()
	second.Username = "bbb-" + model.NewId()
	second, err = ss.User().Save(ctx, second)
	requireNoError(t, err)

	found, err := ss.User().List(ctx, store.UserListOptions{
		Query: "Searchable Alpha", Limit: 10,
	})
	requireNoError(t, err)
	if len(found) != 1 || found[0].Id != first.Id {
		t.Fatalf("List(search) = %#v", found)
	}
	page, err := ss.User().List(ctx, store.UserListOptions{
		AfterUsername: first.Username, AfterId: first.Id, Limit: 10,
	})
	requireNoError(t, err)
	if len(page) == 0 || page[0].Id != second.Id {
		t.Fatalf("List(after) = %#v", page)
	}
	at := model.GetMillis() + 100
	disabled, err := ss.User().SetDisabled(ctx, first.Id, at, at)
	requireNoError(t, err)
	if disabled.DisabledAt != at {
		t.Fatalf("SetDisabled() = %#v", disabled)
	}
	active, err := ss.User().List(ctx, store.UserListOptions{Limit: 10})
	requireNoError(t, err)
	for _, user := range active {
		if user.Id == first.Id {
			t.Fatalf("disabled user returned in active list: %#v", active)
		}
	}
	all, err := ss.User().List(ctx, store.UserListOptions{
		Limit: 10, IncludeDisabled: true,
	})
	requireNoError(t, err)
	seen := false
	for _, user := range all {
		seen = seen || user.Id == first.Id
	}
	if !seen {
		t.Fatalf("disabled user missing from inclusive list: %#v", all)
	}
}

func testUserStoreSaveAndGet(t *testing.T, ss store.Store) {
	ctx := context.Background()
	input := newUser()
	saved, err := ss.User().Save(ctx, input)
	requireNoError(t, err)
	if !model.IsValidId(saved.Id) || input.Id != "" {
		t.Fatalf("Save() saved=%#v input=%#v", saved, input)
	}
	got, err := ss.User().Get(ctx, saved.Id)
	requireNoError(t, err)
	if *got != *saved {
		t.Fatalf("Get() = %#v, want %#v", got, saved)
	}
	if _, err := ss.User().Get(ctx, model.NewId()); !store.IsNotFound(err) {
		t.Fatalf("Get(missing) error = %v, want not found", err)
	}
	_, err = ss.User().Save(ctx, saved)
	var invalid *store.ErrInvalidInput
	if !errors.As(err, &invalid) {
		t.Fatalf("second Save(saved) error = %v, want invalid input", err)
	}
}

func testUserStoreNormalizedLookups(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)

	byUsername, err := ss.User().GetByUsername(ctx, "  "+user.Username+"  ")
	requireNoError(t, err)
	byEmail, err := ss.User().GetByEmail(ctx, "  "+user.Email+"  ")
	requireNoError(t, err)
	if byUsername.Id != user.Id || byEmail.Id != user.Id {
		t.Fatalf("lookups returned username=%#v email=%#v", byUsername, byEmail)
	}
	if _, err := ss.User().GetByEmail(ctx, "missing@example.edu"); !store.IsNotFound(err) {
		t.Fatalf("GetByEmail(missing) error = %v", err)
	}
}

func testUserStoreUpdate(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	user.DisplayName = "Updated User"
	user.EmailVerified = true
	updated, err := ss.User().Update(ctx, user)
	requireNoError(t, err)
	if updated.DisplayName != "Updated User" || !updated.EmailVerified {
		t.Fatalf("Update() = %#v", updated)
	}
	missing := *updated
	missing.Id = model.NewId()
	if _, err := ss.User().Update(ctx, &missing); !store.IsNotFound(err) {
		t.Fatalf("Update(missing) error = %v", err)
	}
}

func testUserStoreUpdateLastLogin(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	at := model.GetMillis() + 100
	requireNoError(t, ss.User().UpdateLastLogin(ctx, user.Id, at))
	got, err := ss.User().Get(ctx, user.Id)
	requireNoError(t, err)
	if got.LastLoginAt != at || got.LastActivityAt != at || got.UpdateAt < at {
		t.Fatalf("UpdateLastLogin() user = %#v", got)
	}
}

func testUserStoreUniqueness(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	duplicateUsername := newUser()
	duplicateUsername.Username = user.Username
	_, err := ss.User().Save(ctx, duplicateUsername)
	var conflict *store.ErrConflict
	if !errors.As(err, &conflict) || conflict.Constraint != "users_active_username_key" {
		t.Fatalf("duplicate username error = %v", err)
	}
	duplicateEmail := newUser()
	duplicateEmail.Email = user.Email
	_, err = ss.User().Save(ctx, duplicateEmail)
	if !errors.As(err, &conflict) || conflict.Constraint != "users_active_email_key" {
		t.Fatalf("duplicate email error = %v", err)
	}
}

func newUser() *model.User {
	id := model.NewId()
	return &model.User{
		Username:    "user-" + id,
		Email:       id + "@example.edu",
		DisplayName: "Test User",
	}
}

func saveUser(t *testing.T, ctx context.Context, ss store.Store) *model.User {
	t.Helper()
	user, err := ss.User().Save(ctx, newUser())
	requireNoError(t, err)
	return user
}
