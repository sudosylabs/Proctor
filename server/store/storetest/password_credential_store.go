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

func TestPasswordCredentialStore(t *testing.T, ss store.Store) {
	t.Run("SaveGetAndUpdate", func(t *testing.T) {
		ctx := context.Background()
		user := saveUser(t, ctx, ss)
		input := &model.PasswordCredential{
			UserId:       user.Id,
			PasswordHash: "$argon2id$v=19$m=65536,t=3,p=2$first$hash",
		}
		saved, err := ss.PasswordCredential().Save(ctx, input)
		requireNoError(t, err)
		if !model.IsValidId(saved.Id) || input.Id != "" {
			t.Fatalf("Save() saved=%#v input=%#v", saved, input)
		}
		got, err := ss.PasswordCredential().GetByUser(ctx, user.Id)
		requireNoError(t, err)
		if *got != *saved {
			t.Fatalf("GetByUser() = %#v, want %#v", got, saved)
		}
		saved.PasswordHash = "$argon2id$v=19$m=65536,t=3,p=2$second$hash"
		saved.PasswordChangedAt = model.GetMillis() + 100
		updated, err := ss.PasswordCredential().Update(ctx, saved)
		requireNoError(t, err)
		if updated.PasswordHash != saved.PasswordHash {
			t.Fatalf("Update() = %#v", updated)
		}
	})

	t.Run("ReferencesAndUniqueness", func(t *testing.T) {
		ctx := context.Background()
		_, err := ss.PasswordCredential().Save(ctx, &model.PasswordCredential{
			UserId:       model.NewId(),
			PasswordHash: "$argon2id$missing",
		})
		var reference *store.ErrReference
		if !errors.As(err, &reference) ||
			reference.Constraint != "password_credentials_user_id_fkey" {
			t.Fatalf("unknown user error = %v", err)
		}
		user := saveUser(t, ctx, ss)
		first := &model.PasswordCredential{UserId: user.Id, PasswordHash: "$argon2id$first"}
		_, err = ss.PasswordCredential().Save(ctx, first)
		requireNoError(t, err)
		_, err = ss.PasswordCredential().Save(ctx, &model.PasswordCredential{
			UserId:       user.Id,
			PasswordHash: "$argon2id$second",
		})
		var conflict *store.ErrConflict
		if !errors.As(err, &conflict) ||
			conflict.Constraint != "password_credentials_active_user_key" {
			t.Fatalf("duplicate user credential error = %v", err)
		}
	})
}
