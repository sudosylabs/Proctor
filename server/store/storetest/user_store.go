// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/channels/store/storetest/user_store.go.

package storetest

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestUserStore(t *testing.T, ss store.Store) {
	t.Run("CreateAndGet", func(t *testing.T) { testUserStoreCreateAndGet(t, ss) })
	t.Run("CreationAndDefaultJobAreAtomic", func(t *testing.T) { testUserCreationAndDefaultJobAreAtomic(t, ss) })
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

func testUserCreationAndDefaultJobAreAtomic(t *testing.T, ss store.Store) {
	ctx := context.Background()
	mismatched := testUserCreation(newUser(), &model.PasswordCredential{PasswordHash: "encoded-password"})
	mismatched.DefaultProfilePictureJob.Command = defaultProfilePictureCommand(model.NewUserID())
	if _, err := ss.User().Create(ctx, mismatched); err == nil {
		t.Fatal("Create() accepted a default-picture Job targeting another User")
	}
	if _, err := ss.User().Get(ctx, mismatched.User.ID.String()); !store.IsNotFound(err) {
		t.Fatalf("user survived mismatched Job rollback: %v", err)
	}
	if _, err := ss.PasswordCredential().GetByUser(ctx, mismatched.User.ID.String()); !store.IsNotFound(err) {
		t.Fatalf("credential survived mismatched Job rollback: %v", err)
	}
	if _, err := ss.Job().Get(ctx, mismatched.DefaultProfilePictureJob.ID); !store.IsNotFound(err) {
		t.Fatalf("mismatched Job was persisted: %v", err)
	}
	if _, err := ss.UserSettings().Get(ctx, mismatched.User.ID); !store.IsNotFound(err) {
		t.Fatalf("settings survived mismatched Job rollback: %v", err)
	}
	mismatchedSettings := testUserCreation(newUser(), nil)
	mismatchedSettings.Settings.UserID = model.NewUserID()
	if _, err := ss.User().Create(ctx, mismatchedSettings); err == nil {
		t.Fatal("Create() accepted settings targeting another User")
	}
	if _, err := ss.User().Get(ctx, mismatchedSettings.User.ID.String()); !store.IsNotFound(err) {
		t.Fatalf("user survived mismatched settings rollback: %v", err)
	}
	permanent := testUserCreation(newUser(), nil)
	permanent.DefaultProfilePictureJob.DedupePolicy = model.JobDedupePermanent
	if _, err := ss.User().Create(ctx, permanent); err == nil {
		t.Fatal("Create() accepted a permanent-dedupe default-picture intent")
	}

	first := testUserCreation(newUser(), &model.PasswordCredential{PasswordHash: "encoded-password"})
	result, err := ss.User().Create(ctx, first)
	requireNoError(t, err)
	queued, err := ss.Job().Get(ctx, first.DefaultProfilePictureJob.ID)
	requireNoError(t, err)
	if queued.Status != model.JobStatusQueued || queued.DedupeKey != result.User.ID.String() ||
		result.PasswordCredential == nil || result.PasswordCredential.UserID != result.User.ID {
		t.Fatalf("Create() result/job = %#v / %#v", result, queued)
	}
	settings, err := ss.UserSettings().Get(ctx, result.User.ID)
	requireNoError(t, err)
	if settings.Source != model.UserSettingsInitialSource || settings.FormatVersion != model.UserSettingsFormatVersion1 || settings.UserID != result.User.ID {
		t.Fatalf("created settings = %#v", settings)
	}

	second := testUserCreation(newUser(), &model.PasswordCredential{PasswordHash: "encoded-password"})
	second.DefaultProfilePictureJob.ID = first.DefaultProfilePictureJob.ID
	if _, err = ss.User().Create(ctx, second); err == nil {
		t.Fatal("Create() succeeded when its Job insert failed")
	}
	if _, err = ss.User().Get(ctx, second.User.ID.String()); !store.IsNotFound(err) {
		t.Fatalf("user survived Job rollback: %v", err)
	}
	if _, err = ss.PasswordCredential().GetByUser(ctx, second.User.ID.String()); !store.IsNotFound(err) {
		t.Fatalf("credential survived Job rollback: %v", err)
	}
	if _, err = ss.UserSettings().Get(ctx, second.User.ID); !store.IsNotFound(err) {
		t.Fatalf("settings survived Job rollback: %v", err)
	}
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
		UserID: first.ID, RoleID: role.ID,
		ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(),
		StartsAt: model.TimeFromMillis(model.GetMillis() - 100),
	})
	requireNoError(t, err)
	at := model.GetMillis()
	attempt := saveUserProfileAuditAttempt(t, ctx, ss, first.ID.String())
	_, err = ss.User().SetDisabledWithAudit(ctx, &store.UserDisabledStateChange{
		ID: first.ID.String(), ExpectedRevision: first.Revision, Disabled: true,
		ChangedAt: at, RevocationReason: "administrator disabled account",
		AuditEventID: attempt.ID.String(), AuditAt: at,
	})
	var conflict *store.ErrConflict
	if !errors.As(err, &conflict) ||
		conflict.Constraint != "users_last_system_admin" {
		t.Fatalf("disable last administrator error = %v", err)
	}
	second := saveUser(t, ctx, ss)
	secondBinding, err := ss.RoleBinding().Save(ctx, &model.RoleBinding{
		UserID: second.ID, RoleID: role.ID,
		ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(),
		StartsAt: model.TimeFromMillis(at - 100),
	})
	requireNoError(t, err)
	_, err = ss.User().SetDisabledWithAudit(ctx, &store.UserDisabledStateChange{
		ID: first.ID.String(), ExpectedRevision: first.Revision, Disabled: true,
		ChangedAt: at, RevocationReason: "administrator disabled account",
		AuditEventID: attempt.ID.String(), AuditAt: at,
	})
	requireNoError(t, err)
	if _, err = ss.RoleBinding().End(ctx, secondBinding.ID.String(), at+1); !errors.As(err, &conflict) {
		t.Fatalf("end only enabled administrator binding error = %v", err)
	}
	if _, err = ss.RoleBinding().End(ctx, firstBinding.ID.String(), at+1); err != nil {
		t.Fatalf("ending disabled administrator binding should be allowed: %v", err)
	}
}

func testUserStoreListAndDisable(t *testing.T, ss store.Store) {
	ctx := context.Background()
	first := newUser()
	first.Username = "aaa-" + model.NewId()
	first.DisplayName = "Searchable Alpha"
	first, err := createUser(t, ctx, ss, first)
	requireNoError(t, err)
	second := newUser()
	second.Username = "bbb-" + model.NewId()
	second, err = createUser(t, ctx, ss, second)
	requireNoError(t, err)
	activeAt := model.GetMillis() + 100
	fixture := saveClassFixture(t, ctx, ss)
	visibleClass := saveClass(t, ctx, ss, fixture.level.ID.String(), fixture.period.ID.String(), "user-search-visible")
	_, err = ss.Affiliation().Save(ctx, &model.Affiliation{
		UserID: first.ID, Kind: model.AffiliationStudent,
		StartsAt: model.TimeFromMillis(activeAt - 10),
	})
	requireNoError(t, err)
	firstMembership, err := ss.ClassMember().Enroll(ctx, &model.ClassMember{
		ClassID: visibleClass.ID, UserID: first.ID, StartsAt: model.TimeFromMillis(activeAt - 10),
	})
	requireNoError(t, err)
	_, err = ss.ClassMember().End(ctx, firstMembership.Membership.ID.String(), firstMembership.Membership.Revision, activeAt+100)
	requireNoError(t, err)
	_, err = ss.Affiliation().Save(ctx, &model.Affiliation{
		UserID: second.ID, Kind: model.AffiliationStudent,
		StartsAt: model.TimeFromMillis(activeAt - 10),
	})
	requireNoError(t, err)
	_, err = ss.ClassMember().Enroll(ctx, &model.ClassMember{
		ClassID: visibleClass.ID, UserID: second.ID, StartsAt: model.TimeFromMillis(activeAt + 10),
	})
	requireNoError(t, err)
	denied, err := ss.User().List(ctx, store.UserListOptions{Limit: 10})
	requireNoError(t, err)
	if len(denied) != 0 {
		t.Fatalf("List(without visibility) = %#v, want empty", denied)
	}
	classVisible, err := ss.User().List(ctx, store.UserListOptions{
		Visibility: store.UserVisibilityScope{ClassIDs: []string{visibleClass.ID.String()}, ActiveAt: activeAt}, Limit: 10,
	})
	requireNoError(t, err)
	if len(classVisible) != 1 || classVisible[0].ID != first.ID {
		t.Fatalf("List(class visibility) = %#v, want only %s", classVisible, first.ID)
	}
	unitVisible, err := ss.User().List(ctx, store.UserListOptions{
		Visibility: store.UserVisibilityScope{AcademicUnitRootIDs: []string{fixture.programme.AcademicUnitID.String()}, ActiveAt: activeAt}, Limit: 10,
	})
	requireNoError(t, err)
	if len(unitVisible) != 1 || unitVisible[0].ID != first.ID {
		t.Fatalf("List(academic-unit visibility) = %#v, want only %s", unitVisible, first.ID)
	}
	if _, err = ss.User().List(ctx, store.UserListOptions{
		Visibility: store.UserVisibilityScope{ClassIDs: []string{"malformed"}, ActiveAt: activeAt}, Limit: 10,
	}); err == nil {
		t.Fatal("List accepted a malformed visibility ID")
	}

	found, err := ss.User().List(ctx, store.UserListOptions{
		Visibility: store.UserVisibilityScope{InstitutionWide: true},
		Query:      "Searchable Alpha", Limit: 10,
	})
	requireNoError(t, err)
	if len(found) != 1 || found[0].ID != first.ID {
		t.Fatalf("List(search) = %#v", found)
	}
	page, err := ss.User().List(ctx, store.UserListOptions{
		Visibility:    store.UserVisibilityScope{InstitutionWide: true},
		AfterUsername: first.Username, AfterId: first.ID.String(), Limit: 10,
	})
	requireNoError(t, err)
	if len(page) == 0 || page[0].ID != second.ID {
		t.Fatalf("List(after) = %#v", page)
	}
	at := model.GetMillis() + 100
	attempt := saveUserProfileAuditAttempt(t, ctx, ss, first.ID.String())
	result, err := ss.User().SetDisabledWithAudit(ctx, &store.UserDisabledStateChange{
		ID: first.ID.String(), ExpectedRevision: first.Revision, Disabled: true,
		ChangedAt: at, RevocationReason: "administrator disabled account",
		AuditEventID: attempt.ID.String(), AuditAt: at,
	})
	requireNoError(t, err)
	disabled := result.User
	if disabled.DisabledAt.Millis() != at || disabled.Revision != first.Revision+1 {
		t.Fatalf("SetDisabledWithAudit() = %#v", result)
	}
	active, err := ss.User().List(ctx, store.UserListOptions{Limit: 10, Visibility: store.UserVisibilityScope{InstitutionWide: true}})
	requireNoError(t, err)
	for _, user := range active {
		if user.ID == first.ID {
			t.Fatalf("disabled user returned in active list: %#v", active)
		}
	}
	all, err := ss.User().List(ctx, store.UserListOptions{
		Visibility: store.UserVisibilityScope{InstitutionWide: true},
		Limit:      10, IncludeDisabled: true,
	})
	requireNoError(t, err)
	seen := false
	for _, user := range all {
		seen = seen || user.ID == first.ID
	}
	if !seen {
		t.Fatalf("disabled user missing from inclusive list: %#v", all)
	}
}

func testUserStoreEnablementRevocationAndAuditAreAtomic(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	first, _, firstRaw := saveSession(t, ctx, ss, user.ID.String(), 10)
	second, _, _ := saveSession(t, ctx, ss, user.ID.String(), 10)
	at := model.GetMillis() + 100

	oversizedAttempt := saveUserProfileAuditAttempt(t, ctx, ss, user.ID.String())
	if _, err := ss.User().SetDisabledWithAudit(ctx, &store.UserDisabledStateChange{
		ID: user.ID.String(), ExpectedRevision: user.Revision, Disabled: true,
		ChangedAt:        at,
		RevocationReason: strings.Repeat("x", model.SessionRevocationMaxRunes+1),
		AuditEventID:     oversizedAttempt.ID.String(),
		AuditAt:          at,
	}); err == nil {
		t.Fatal("SetDisabledWithAudit() accepted an oversized revocation reason")
	} else {
		var invalid *store.ErrInvalidInput
		if !errors.As(err, &invalid) {
			t.Fatalf("oversized revocation reason error = %v, want invalid input", err)
		}
	}
	oversizedAudit, err := ss.Audit().Get(ctx, oversizedAttempt.ID.String())
	requireNoError(t, err)
	if oversizedAudit.Status != model.AuditStatusAttempt {
		t.Fatalf("invalid state change completed its audit: %#v", oversizedAudit)
	}

	// A missing audit attempt must roll back both the user update and every
	// session/credential revocation performed before completion was attempted.
	if _, err := ss.User().SetDisabledWithAudit(ctx, &store.UserDisabledStateChange{
		ID: user.ID.String(), ExpectedRevision: user.Revision, Disabled: true,
		ChangedAt: at, RevocationReason: "administrator disabled account",
		AuditEventID: model.NewId(), AuditAt: at,
	}); err == nil {
		t.Fatal("SetDisabledWithAudit() succeeded without its audit attempt")
	}
	unchanged, err := ss.User().Get(ctx, user.ID.String())
	requireNoError(t, err)
	if unchanged.DisabledAt.Valid || unchanged.Revision != user.Revision {
		t.Fatalf("user state survived audit rollback: %#v", unchanged)
	}
	unrevoked, err := ss.Session().Get(ctx, first.ID.String())
	requireNoError(t, err)
	credential, _, err := ss.SessionCredential().GetSessionByTokenHash(
		ctx,
		model.HashToken(firstRaw.access),
		model.SessionCredentialAccess,
	)
	requireNoError(t, err)
	if unrevoked.RevokedAt.Valid || credential.RevokedAt.Valid {
		t.Fatalf("session revocation survived audit rollback: session=%#v credential=%#v", unrevoked, credential)
	}

	disableAttempt := saveUserProfileAuditAttempt(t, ctx, ss, user.ID.String())
	disabled, err := ss.User().SetDisabledWithAudit(ctx, &store.UserDisabledStateChange{
		ID: user.ID.String(), ExpectedRevision: user.Revision, Disabled: true,
		ChangedAt: at, RevocationReason: "administrator disabled account",
		AuditEventID: disableAttempt.ID.String(), AuditAt: at,
	})
	requireNoError(t, err)
	if disabled.User.DisabledAt.Millis() != at || disabled.User.Revision != user.Revision+1 ||
		len(disabled.RevokedSessions) != 2 || len(disabled.RevokedTokenHashes) != 4 {
		t.Fatalf("SetDisabledWithAudit() = %#v", disabled)
	}
	for _, sessionID := range []string{first.ID.String(), second.ID.String()} {
		revoked, getErr := ss.Session().Get(ctx, sessionID)
		requireNoError(t, getErr)
		if revoked.RevokedAt.Millis() != at || revoked.RevocationReason != "administrator disabled account" {
			t.Fatalf("session %s was not revoked with the account: %#v", sessionID, revoked)
		}
	}
	completed, err := ss.Audit().Get(ctx, disableAttempt.ID.String())
	requireNoError(t, err)
	if completed.Status != model.AuditStatusSuccess || len(completed.Result) == 0 {
		t.Fatalf("disable audit = %#v", completed)
	}

	staleAttempt := saveUserProfileAuditAttempt(t, ctx, ss, user.ID.String())
	if _, err := ss.User().SetDisabledWithAudit(ctx, &store.UserDisabledStateChange{
		ID: user.ID.String(), ExpectedRevision: user.Revision, Disabled: false,
		ChangedAt: at + 1, AuditEventID: staleAttempt.ID.String(), AuditAt: at + 1,
	}); !store.IsConflict(err) {
		t.Fatalf("stale SetDisabledWithAudit() error = %v", err)
	}
	staleAudit, err := ss.Audit().Get(ctx, staleAttempt.ID.String())
	requireNoError(t, err)
	if staleAudit.Status != model.AuditStatusAttempt {
		t.Fatalf("stale state change completed its audit: %#v", staleAudit)
	}

	enableAttempt := saveUserProfileAuditAttempt(t, ctx, ss, user.ID.String())
	enabled, err := ss.User().SetDisabledWithAudit(ctx, &store.UserDisabledStateChange{
		ID: user.ID.String(), ExpectedRevision: disabled.User.Revision, Disabled: false,
		ChangedAt: at + 2, AuditEventID: enableAttempt.ID.String(), AuditAt: at + 2,
	})
	requireNoError(t, err)
	if enabled.User.DisabledAt.Valid || enabled.User.Revision != disabled.User.Revision+1 ||
		enabled.RevokedSessions == nil || len(enabled.RevokedSessions) != 0 ||
		enabled.RevokedTokenHashes == nil || len(enabled.RevokedTokenHashes) != 0 {
		t.Fatalf("enable result = %#v", enabled)
	}
	enableAudit, err := ss.Audit().Get(ctx, enableAttempt.ID.String())
	requireNoError(t, err)
	if enableAudit.Status != model.AuditStatusSuccess {
		t.Fatalf("enable audit = %#v", enableAudit)
	}
}

func testUserStoreCreateAndGet(t *testing.T, ss store.Store) {
	ctx := context.Background()
	input := newUser()
	saved, err := createUser(t, ctx, ss, input)
	requireNoError(t, err)
	if !saved.ID.IsValid() || saved.Revision != 1 || !input.ID.IsZero() {
		t.Fatalf("Create() saved=%#v input=%#v", saved, input)
	}
	got, err := ss.User().Get(ctx, saved.ID.String())
	requireNoError(t, err)
	if *got != *saved {
		t.Fatalf("Get() = %#v, want %#v", got, saved)
	}
	if _, err := ss.User().Get(ctx, model.NewId()); !store.IsNotFound(err) {
		t.Fatalf("Get(missing) error = %v, want not found", err)
	}
	_, err = ss.User().Create(ctx, testUserCreation(saved, nil))
	var conflict *store.ErrConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("second Create(saved) error = %v, want conflict", err)
	}
}

func testUserStoreNormalizedLookups(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)

	byUsername, err := ss.User().GetByUsername(ctx, "  "+user.Username+"  ")
	requireNoError(t, err)
	byEmail, err := ss.User().GetByEmail(ctx, "  "+user.Email+"  ")
	requireNoError(t, err)
	if byUsername.ID != user.ID || byEmail.ID != user.ID {
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
	requireNoError(t, ss.User().UpdateLastLogin(ctx, updated.ID.String(), activityAt))
	auditedCandidate := *updated
	auditedCandidate.DisplayName = "Audited User"
	auditAttempt := saveUserProfileAuditAttempt(t, ctx, ss, updated.ID.String())
	audited, err := ss.User().UpdateProfileWithAudit(ctx, &store.UserProfileUpdate{
		User: &auditedCandidate, ExpectedRevision: updated.Revision,
		AuditEventID: auditAttempt.ID.String(), AuditAt: model.GetMillis(),
	})
	requireNoError(t, err)
	if audited.DisplayName != "Audited User" || audited.Revision != updated.Revision+1 {
		t.Fatalf("UpdateProfileWithAudit() = %#v", audited)
	}
	completed, err := ss.Audit().Get(ctx, auditAttempt.ID.String())
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
	persisted, err := ss.User().Get(ctx, audited.ID.String())
	requireNoError(t, err)
	if persisted.DisplayName != audited.DisplayName || persisted.Revision != audited.Revision ||
		persisted.LastLoginAt.Millis() != activityAt || persisted.LastActivityAt.Millis() != activityAt || model.MillisFromTime(persisted.UpdatedAt) < activityAt {
		t.Fatalf("profile update survived audit rollback: %#v", persisted)
	}

	staleAttempt := saveUserProfileAuditAttempt(t, ctx, ss, updated.ID.String())
	if _, err := ss.User().UpdateProfileWithAudit(ctx, &store.UserProfileUpdate{
		User: updated, ExpectedRevision: updated.Revision,
		AuditEventID: staleAttempt.ID.String(), AuditAt: model.GetMillis(),
	}); !store.IsConflict(err) {
		t.Fatalf("stale UpdateProfileWithAudit() error = %v", err)
	}
	missing := *updated
	missing.ID = model.UserID(model.NewId())
	if _, err := ss.User().Update(ctx, &missing); !store.IsNotFound(err) {
		t.Fatalf("Update(missing) error = %v", err)
	}
}

func saveUserProfileAuditAttempt(t *testing.T, ctx context.Context, ss store.Store, userID string) *model.AuditEvent {
	t.Helper()
	attempt, err := ss.Audit().Save(ctx, &model.AuditEvent{
		Action:    string(model.ActionUserManage),
		Resource:  model.Resource{Type: model.ResourceUser, ID: userID},
		ScopeType: model.RoleScopeInstitution, ScopeID: model.NewId(),
		Status: model.AuditStatusAttempt, NodeID: "test-node",
	})
	requireNoError(t, err)
	return attempt
}

func testUserStoreUpdateLastLogin(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	revision := user.Revision
	at := model.GetMillis() + 100
	requireNoError(t, ss.User().UpdateLastLogin(ctx, user.ID.String(), at))
	got, err := ss.User().Get(ctx, user.ID.String())
	requireNoError(t, err)
	if got.LastLoginAt.Millis() != at || got.LastActivityAt.Millis() != at || model.MillisFromTime(got.UpdatedAt) < at || got.Revision != revision {
		t.Fatalf("UpdateLastLogin() user = %#v", got)
	}
}

func testUserStoreUniqueness(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	duplicateUsername := newUser()
	duplicateUsername.Username = user.Username
	_, err := createUser(t, ctx, ss, duplicateUsername)
	var conflict *store.ErrConflict
	if !errors.As(err, &conflict) || conflict.Constraint != "users_username_key" {
		t.Fatalf("duplicate username error = %v", err)
	}
	duplicateEmail := newUser()
	duplicateEmail.Email = user.Email
	_, err = createUser(t, ctx, ss, duplicateEmail)
	if !errors.As(err, &conflict) || conflict.Constraint != "users_email_key" {
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
	user, err := createUser(t, ctx, ss, newUser())
	requireNoError(t, err)
	return user
}

func createUser(t *testing.T, ctx context.Context, ss store.Store, input *model.User) (*model.User, error) {
	t.Helper()
	result, err := ss.User().Create(ctx, testUserCreation(input, nil))
	if err != nil {
		return nil, err
	}
	return result.User, nil
}

func testUserCreation(input *model.User, credential *model.PasswordCredential) *store.UserCreation {
	user := *input
	if user.ID.IsZero() {
		user.PrepareCreate(model.NewUserID(), model.NowUTC())
	}
	if credential != nil {
		copy := *credential
		if copy.ID.IsZero() {
			copy.UserID = user.ID
			copy.PrepareCreate(model.NewPasswordCredentialID(), user.CreatedAt)
		}
		credential = &copy
	}
	command := defaultProfilePictureCommand(user.ID)
	job, _ := model.NewJob(
		model.NewJobID(), model.JobTypeProfilePictureGenerateDefault, 1,
		command, user.ID.String(), user.CreatedAt, user.CreatedAt, 8,
	)
	settings, _ := model.NewUserSettingsDocument(user.ID, model.NewUserSettingsRevision(), user.CreatedAt)
	return &store.UserCreation{User: &user, Settings: settings, PasswordCredential: credential, DefaultProfilePictureJob: job}
}

func defaultProfilePictureCommand(userID model.UserID) json.RawMessage {
	document, _ := json.Marshal(map[string]string{"user_id": userID.String()})
	return document
}
