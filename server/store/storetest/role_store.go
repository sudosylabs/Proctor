// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Adapted from Mattermost server/channels/store/storetest/role_store.go.

package storetest

import (
	"context"
	"errors"
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
		if !model.IsValidId(saved.Id) || input.Id != "" {
			t.Fatalf("Save() = %#v, input = %#v", saved, input)
		}
		byID, err := ss.Role().Get(ctx, saved.Id)
		requireNoError(t, err)
		byName, err := ss.Role().GetByName(ctx, saved.Name)
		requireNoError(t, err)
		if byID.Id != saved.Id || byName.Id != saved.Id {
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
		batch, err := ss.Role().GetByIds(ctx, []string{saved.Id, model.NewId()})
		requireNoError(t, err)
		if len(list) != 1 || len(batch) != 1 {
			t.Fatalf("List/GetByIds = %d/%d", len(list), len(batch))
		}
		deleted, err := ss.Role().Delete(ctx, saved.Id, model.GetMillis())
		requireNoError(t, err)
		if deleted.DeleteAt == 0 {
			t.Fatalf("Delete() = %#v", deleted)
		}
		if _, err := ss.Role().Get(ctx, saved.Id); !store.IsNotFound(err) {
			t.Fatalf("Get(deleted) error = %v", err)
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
		if !errors.As(err, &conflict) || conflict.Constraint != "roles_active_name_key" {
			t.Fatalf("duplicate role error = %v", err)
		}
		if _, err := ss.Role().Delete(ctx, first.Id, model.GetMillis()); !store.IsConflict(err) {
			t.Fatalf("Delete(built-in) error = %v", err)
		}
		first.DisplayName = "Modified Built-in"
		if _, err := ss.Role().Update(ctx, first); !store.IsConflict(err) {
			t.Fatalf("Update(built-in) error = %v", err)
		}
	})
}
