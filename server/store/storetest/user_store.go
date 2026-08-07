// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/channels/store/storetest/user_store.go.

package storetest

import (
	"context"
	"errors"
	"strings"
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
	t.Run("EnablementRevocationAndAuditAreAtomic", func(t *testing.T) {
		testUserStoreEnablementRevocationAndAuditAreAtomic(t, ss)
	})
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
	attempt := saveUserProfileAuditAttempt(t, ctx, ss, first.Id)
	_, err = ss.User().SetDisabledWithAudit(ctx, &store.UserDisabledStateChange{
		ID: first.Id, ExpectedRevision: first.Revision, Disabled: true,
		ChangedAt: at, RevocationReason: "administrator disabled account",
		AuditEventID: attempt.Id, AuditAt: at,
	})
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
	_, err = ss.User().SetDisabledWithAudit(ctx, &store.UserDisabledStateChange{
		ID: first.Id, ExpectedRevision: first.Revision, Disabled: true,
		ChangedAt: at, RevocationReason: "administrator disabled account",
		AuditEventID: attempt.Id, AuditAt: at,
	})
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
	attempt := saveUserProfileAuditAttempt(t, ctx, ss, first.Id)
	result, err := ss.User().SetDisabledWithAudit(ctx, &store.UserDisabledStateChange{
		ID: first.Id, ExpectedRevision: first.Revision, Disabled: true,
		ChangedAt: at, RevocationReason: "administrator disabled account",
		AuditEventID: attempt.Id, AuditAt: at,
	})
	requireNoError(t, err)
	disabled := result.User
	if disabled.DisabledAt != at || disabled.Revision != first.Revision+1 {
		t.Fatalf("SetDisabledWithAudit() = %#v", result)
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

func testUserStoreEnablementRevocationAndAuditAreAtomic(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	first, _, firstRaw := saveSession(t, ctx, ss, user.Id, 10)
	second, _, _ := saveSession(t, ctx, ss, user.Id, 10)
	at := model.GetMillis() + 100

	oversizedAttempt := saveUserProfileAuditAttempt(t, ctx, ss, user.Id)
	if _, err := ss.User().SetDisabledWithAudit(ctx, &store.UserDisabledStateChange{
		ID: user.Id, ExpectedRevision: user.Revision, Disabled: true,
		ChangedAt:        at,
		RevocationReason: strings.Repeat("x", model.SessionRevocationMaxRunes+1),
		AuditEventID:     oversizedAttempt.Id,
		AuditAt:          at,
	}); err == nil {
		t.Fatal("SetDisabledWithAudit() accepted an oversized revocation reason")
	} else {
		var invalid *store.ErrInvalidInput
		if !errors.As(err, &invalid) {
			t.Fatalf("oversized revocation reason error = %v, want invalid input", err)
		}
	}
	oversizedAudit, err := ss.Audit().Get(ctx, oversizedAttempt.Id)
	requireNoError(t, err)
	if oversizedAudit.Status != model.AuditStatusAttempt {
		t.Fatalf("invalid state change completed its audit: %#v", oversizedAudit)
	}

	// A missing audit attempt must roll back both the user update and every
	// session/credential revocation performed before completion was attempted.
	if _, err := ss.User().SetDisabledWithAudit(ctx, &store.UserDisabledStateChange{
		ID: user.Id, ExpectedRevision: user.Revision, Disabled: true,
		ChangedAt: at, RevocationReason: "administrator disabled account",
		AuditEventID: model.NewId(), AuditAt: at,
	}); err == nil {
		t.Fatal("SetDisabledWithAudit() succeeded without its audit attempt")
	}
	unchanged, err := ss.User().Get(ctx, user.Id)
	requireNoError(t, err)
	if unchanged.DisabledAt != 0 || unchanged.Revision != user.Revision {
		t.Fatalf("user state survived audit rollback: %#v", unchanged)
	}
	unrevoked, err := ss.Session().Get(ctx, first.Id)
	requireNoError(t, err)
	credential, _, err := ss.SessionCredential().GetSessionByTokenHash(
		ctx,
		model.HashToken(firstRaw.access),
		model.SessionCredentialAccess,
	)
	requireNoError(t, err)
	if unrevoked.RevokedAt != 0 || credential.RevokedAt != 0 {
		t.Fatalf("session revocation survived audit rollback: session=%#v credential=%#v", unrevoked, credential)
	}

	disableAttempt := saveUserProfileAuditAttempt(t, ctx, ss, user.Id)
	disabled, err := ss.User().SetDisabledWithAudit(ctx, &store.UserDisabledStateChange{
		ID: user.Id, ExpectedRevision: user.Revision, Disabled: true,
		ChangedAt: at, RevocationReason: "administrator disabled account",
		AuditEventID: disableAttempt.Id, AuditAt: at,
	})
	requireNoError(t, err)
	if disabled.User.DisabledAt != at || disabled.User.Revision != user.Revision+1 ||
		len(disabled.RevokedSessions) != 2 || len(disabled.RevokedTokenHashes) != 4 {
		t.Fatalf("SetDisabledWithAudit() = %#v", disabled)
	}
	for _, sessionID := range []string{first.Id, second.Id} {
		revoked, getErr := ss.Session().Get(ctx, sessionID)
		requireNoError(t, getErr)
		if revoked.RevokedAt != at || revoked.RevocationReason != "administrator disabled account" {
			t.Fatalf("session %s was not revoked with the account: %#v", sessionID, revoked)
		}
	}
	completed, err := ss.Audit().Get(ctx, disableAttempt.Id)
	requireNoError(t, err)
	if completed.Status != model.AuditStatusSuccess || len(completed.Result) == 0 {
		t.Fatalf("disable audit = %#v", completed)
	}

	staleAttempt := saveUserProfileAuditAttempt(t, ctx, ss, user.Id)
	if _, err := ss.User().SetDisabledWithAudit(ctx, &store.UserDisabledStateChange{
		ID: user.Id, ExpectedRevision: user.Revision, Disabled: false,
		ChangedAt: at + 1, AuditEventID: staleAttempt.Id, AuditAt: at + 1,
	}); !store.IsConflict(err) {
		t.Fatalf("stale SetDisabledWithAudit() error = %v", err)
	}
	staleAudit, err := ss.Audit().Get(ctx, staleAttempt.Id)
	requireNoError(t, err)
	if staleAudit.Status != model.AuditStatusAttempt {
		t.Fatalf("stale state change completed its audit: %#v", staleAudit)
	}

	enableAttempt := saveUserProfileAuditAttempt(t, ctx, ss, user.Id)
	enabled, err := ss.User().SetDisabledWithAudit(ctx, &store.UserDisabledStateChange{
		ID: user.Id, ExpectedRevision: disabled.User.Revision, Disabled: false,
		ChangedAt: at + 2, AuditEventID: enableAttempt.Id, AuditAt: at + 2,
	})
	requireNoError(t, err)
	if enabled.User.DisabledAt != 0 || enabled.User.Revision != disabled.User.Revision+1 ||
		enabled.RevokedSessions == nil || len(enabled.RevokedSessions) != 0 ||
		enabled.RevokedTokenHashes == nil || len(enabled.RevokedTokenHashes) != 0 {
		t.Fatalf("enable result = %#v", enabled)
	}
	enableAudit, err := ss.Audit().Get(ctx, enableAttempt.Id)
	requireNoError(t, err)
	if enableAudit.Status != model.AuditStatusSuccess {
		t.Fatalf("enable audit = %#v", enableAudit)
	}
}

func testUserStoreSaveAndGet(t *testing.T, ss store.Store) {
	ctx := context.Background()
	input := newUser()
	saved, err := ss.User().Save(ctx, input)
	requireNoError(t, err)
	if !model.IsValidId(saved.Id) || saved.Revision != 1 || input.Id != "" {
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
	stale := *user
	user.DisplayName = "Updated User"
	user.EmailVerified = true
	updated, err := ss.User().Update(ctx, user)
	requireNoError(t, err)
	if updated.DisplayName != "Updated User" || !updated.EmailVerified || updated.Revision != user.Revision+1 {
		t.Fatalf("Update() = %#v", updated)
	}
	stale.DisplayName = "Stale User"
	if _, err := ss.User().Update(ctx, &stale); !store.IsConflict(err) {
		t.Fatalf("stale Update() error = %v", err)
	}

	activityAt := model.GetMillis() + 100
	requireNoError(t, ss.User().UpdateLastLogin(ctx, updated.Id, activityAt))
	auditedCandidate := *updated
	auditedCandidate.DisplayName = "Audited User"
	auditAttempt := saveUserProfileAuditAttempt(t, ctx, ss, updated.Id)
	audited, err := ss.User().UpdateProfileWithAudit(ctx, &store.UserProfileUpdate{
		User: &auditedCandidate, ExpectedRevision: updated.Revision,
		AuditEventID: auditAttempt.Id, AuditAt: model.GetMillis(),
	})
	requireNoError(t, err)
	if audited.DisplayName != "Audited User" || audited.Revision != updated.Revision+1 {
		t.Fatalf("UpdateProfileWithAudit() = %#v", audited)
	}
	completed, err := ss.Audit().Get(ctx, auditAttempt.Id)
	requireNoError(t, err)
	if completed.Status != model.AuditStatusSuccess {
		t.Fatalf("profile update audit = %#v", completed)
	}

	rolledBack := *audited
	rolledBack.DisplayName = "Must Roll Back"
	if _, err := ss.User().UpdateProfileWithAudit(ctx, &store.UserProfileUpdate{
		User: &rolledBack, ExpectedRevision: audited.Revision,
		AuditEventID: model.NewId(), AuditAt: model.GetMillis(),
	}); err == nil {
		t.Fatal("UpdateProfileWithAudit() succeeded without its audit attempt")
	}
	persisted, err := ss.User().Get(ctx, audited.Id)
	requireNoError(t, err)
	if persisted.DisplayName != audited.DisplayName || persisted.Revision != audited.Revision ||
		persisted.LastLoginAt != activityAt || persisted.LastActivityAt != activityAt || persisted.UpdateAt < activityAt {
		t.Fatalf("profile update survived audit rollback: %#v", persisted)
	}

	staleAttempt := saveUserProfileAuditAttempt(t, ctx, ss, updated.Id)
	if _, err := ss.User().UpdateProfileWithAudit(ctx, &store.UserProfileUpdate{
		User: updated, ExpectedRevision: updated.Revision,
		AuditEventID: staleAttempt.Id, AuditAt: model.GetMillis(),
	}); !store.IsConflict(err) {
		t.Fatalf("stale UpdateProfileWithAudit() error = %v", err)
	}
	missing := *updated
	missing.Id = model.NewId()
	if _, err := ss.User().Update(ctx, &missing); !store.IsNotFound(err) {
		t.Fatalf("Update(missing) error = %v", err)
	}
}

func saveUserProfileAuditAttempt(t *testing.T, ctx context.Context, ss store.Store, userID string) *model.AuditEvent {
	t.Helper()
	attempt, err := ss.Audit().Save(ctx, &model.AuditEvent{
		Action:    string(model.ActionUserManage),
		Resource:  model.Resource{Type: model.ResourceUser, Id: userID},
		ScopeType: model.RoleScopeInstitution, ScopeId: model.NewId(),
		Status: model.AuditStatusAttempt, NodeId: "test-node",
	})
	requireNoError(t, err)
	return attempt
}

func testUserStoreUpdateLastLogin(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	revision := user.Revision
	at := model.GetMillis() + 100
	requireNoError(t, ss.User().UpdateLastLogin(ctx, user.Id, at))
	got, err := ss.User().Get(ctx, user.Id)
	requireNoError(t, err)
	if got.LastLoginAt != at || got.LastActivityAt != at || got.UpdateAt < at || got.Revision != revision {
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
