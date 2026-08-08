// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Adapted from Mattermost server/channels/store/storetest/
// user_access_token_store.go. Proctor additionally verifies finite expiry,
// scope and academic-unit preservation, authoritative account resolution,
// debounced usage metadata, ownership-bound revocation, and serialized limits.

package storetest

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestPersonalAccessTokenStore(t *testing.T, ss store.Store) {
	t.Run("LifecycleAndResolution", func(t *testing.T) {
		testPersonalAccessTokenLifecycle(t, ss)
	})
	t.Run("RejectsUnknownActionScope", func(t *testing.T) {
		testPersonalAccessTokenRejectsUnknownScope(t, ss)
	})
	t.Run("ReenableEnforcesMaximumActive", func(t *testing.T) {
		testPersonalAccessTokenReenableMaximum(t, ss)
	})
	t.Run("MaximumActiveIsSerialized", func(t *testing.T) {
		testPersonalAccessTokenMaximumActive(t, ss)
	})
}

func testPersonalAccessTokenLifecycle(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	unit := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "engineering")
	user, _ := saveLocalUser(t, ctx, ss)
	raw := model.NewCredentialToken()
	token := newPersonalAccessToken(user.Id, raw)
	token.AcademicUnitId = unit.ID.String()
	token, err := ss.PersonalAccessToken().Save(ctx, token, 10)
	requireNoError(t, err)

	got, err := ss.PersonalAccessToken().Get(ctx, token.Id)
	requireNoError(t, err)
	if got.TokenHash != model.HashToken(raw) ||
		got.AcademicUnitId != unit.ID.String() ||
		len(got.Scopes) != 2 {
		t.Fatalf("Get() = %#v", got)
	}
	now := token.CreateAt + 10
	resolved, err := ss.PersonalAccessToken().Resolve(
		ctx,
		model.HashToken(raw),
		now,
		1000,
	)
	requireNoError(t, err)
	if resolved.User.Id != user.Id ||
		resolved.Token.Id != token.Id ||
		resolved.Token.LastUsedAt != now {
		t.Fatalf("Resolve() = %#v", resolved)
	}
	debounced, err := ss.PersonalAccessToken().Resolve(
		ctx,
		model.HashToken(raw),
		now+100,
		1000,
	)
	requireNoError(t, err)
	if debounced.Token.LastUsedAt != now {
		t.Fatalf("debounced last_used_at = %d, want %d", debounced.Token.LastUsedAt, now)
	}
	disabled, err := ss.PersonalAccessToken().SetDisabled(
		ctx,
		token.Id,
		user.Id,
		true,
		now+150,
		10,
	)
	requireNoError(t, err)
	if disabled.DisabledAt != now+150 || disabled.IsActiveAt(now+151) {
		t.Fatalf("disabled token = %#v", disabled)
	}
	if _, err := ss.PersonalAccessToken().Resolve(
		ctx,
		model.HashToken(raw),
		now+151,
		1000,
	); !store.IsNotFound(err) {
		t.Fatalf("disabled Resolve() error = %v, want not found", err)
	}
	enabled, err := ss.PersonalAccessToken().SetDisabled(
		ctx,
		token.Id,
		user.Id,
		false,
		now+175,
		10,
	)
	requireNoError(t, err)
	if enabled.DisabledAt != 0 || !enabled.IsActiveAt(now+176) {
		t.Fatalf("enabled token = %#v", enabled)
	}
	other, _ := saveLocalUser(t, ctx, ss)
	if _, err := ss.PersonalAccessToken().Revoke(
		ctx,
		token.Id,
		other.Id,
		now+200,
	); !store.IsNotFound(err) {
		t.Fatalf("cross-user revoke error = %v, want not found", err)
	}
	revoked, err := ss.PersonalAccessToken().Revoke(
		ctx,
		token.Id,
		user.Id,
		now+200,
	)
	requireNoError(t, err)
	if revoked.RevokedAt != now+200 {
		t.Fatalf("revoked_at = %d", revoked.RevokedAt)
	}
	if _, err := ss.PersonalAccessToken().Resolve(
		ctx,
		model.HashToken(raw),
		now+201,
		1000,
	); !store.IsNotFound(err) {
		t.Fatalf("revoked Resolve() error = %v, want not found", err)
	}
	list, err := ss.PersonalAccessToken().ListByUser(ctx, user.Id)
	requireNoError(t, err)
	if len(list) != 1 || list[0].RevokedAt == 0 {
		t.Fatalf("ListByUser() = %#v", list)
	}
}

func testPersonalAccessTokenReenableMaximum(t *testing.T, ss store.Store) {
	ctx := context.Background()
	saveInstitution(t, ctx, ss)
	user, _ := saveLocalUser(t, ctx, ss)
	first, err := ss.PersonalAccessToken().Save(
		ctx,
		newPersonalAccessToken(user.Id, model.NewCredentialToken()),
		1,
	)
	requireNoError(t, err)
	now := first.CreateAt + 10
	_, err = ss.PersonalAccessToken().SetDisabled(
		ctx, first.Id, user.Id, true, now, 1,
	)
	requireNoError(t, err)
	second, err := ss.PersonalAccessToken().Save(
		ctx,
		newPersonalAccessToken(user.Id, model.NewCredentialToken()),
		1,
	)
	requireNoError(t, err)
	if _, err := ss.PersonalAccessToken().SetDisabled(
		ctx, first.Id, user.Id, false, now+1, 1,
	); !store.IsConflict(err) {
		t.Fatalf("reenable at active limit error = %v, want conflict", err)
	}
	_, err = ss.PersonalAccessToken().Revoke(
		ctx, second.Id, user.Id, now+2,
	)
	requireNoError(t, err)
	enabled, err := ss.PersonalAccessToken().SetDisabled(
		ctx, first.Id, user.Id, false, now+3, 1,
	)
	requireNoError(t, err)
	if enabled.DisabledAt != 0 {
		t.Fatalf("reenabled token = %#v", enabled)
	}
}

func testPersonalAccessTokenMaximumActive(t *testing.T, ss store.Store) {
	ctx := context.Background()
	saveInstitution(t, ctx, ss)
	user, _ := saveLocalUser(t, ctx, ss)
	const contenders = 8
	start := make(chan struct{})
	results := make(chan error, contenders)
	var wait sync.WaitGroup
	for range contenders {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			raw := model.NewCredentialToken()
			_, err := ss.PersonalAccessToken().Save(
				ctx,
				newPersonalAccessToken(user.Id, raw),
				1,
			)
			results <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case store.IsConflict(err):
			conflicts++
		default:
			t.Fatalf("concurrent Save() error = %v", err)
		}
	}
	if successes != 1 || conflicts != contenders-1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
}

func testPersonalAccessTokenRejectsUnknownScope(t *testing.T, ss store.Store) {
	ctx := context.Background()
	saveInstitution(t, ctx, ss)
	user, _ := saveLocalUser(t, ctx, ss)
	token := newPersonalAccessToken(user.Id, model.NewCredentialToken())
	token.Scopes = []string{"future.permission"}
	if _, err := ss.PersonalAccessToken().Save(
		ctx,
		token,
		10,
	); err == nil {
		t.Fatal("Save() accepted an unknown action scope")
	}
}

func newPersonalAccessToken(
	userID string,
	raw string,
) *model.PersonalAccessToken {
	return &model.PersonalAccessToken{
		UserId: userID, Description: "automation token",
		TokenHash: model.HashToken(raw),
		Scopes: []string{
			string(model.ActionAcademicUnitView),
			string(model.ActionClassView),
		},
		ExpiresAt: model.GetMillis() + (24 * time.Hour).Milliseconds(),
	}
}
