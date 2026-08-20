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
	t.Run("RejectsDisabledAccount", func(t *testing.T) {
		testPersonalAccessTokenRejectsDisabledAccount(t, ss)
	})
	t.Run("ReenableEnforcesMaximumActive", func(t *testing.T) {
		testPersonalAccessTokenReenableMaximum(t, ss)
	})
	t.Run("MaximumActiveIsSerialized", func(t *testing.T) {
		testPersonalAccessTokenMaximumActive(t, ss)
	})
	t.Run("AtomicSecurityNoticesAndAudit", func(t *testing.T) {
		testPersonalAccessTokenAtomicSecurityNotices(t, ss)
	})
	t.Run("AtomicSecurityNoticeUsesDatabaseClock", func(t *testing.T) {
		testPersonalAccessTokenDatabaseClock(t, ss)
	})
	t.Run("ConcurrentReplayEmitsOneAuditAndNotice", func(t *testing.T) {
		testPersonalAccessTokenConcurrentReplay(t, ss)
	})
}

func testPersonalAccessTokenConcurrentReplay(t *testing.T, ss store.Store) {
	ctx := context.Background()
	saveInstitution(t, ctx, ss)
	user, _ := saveLocalUser(t, ctx, ss)
	token, err := createPersonalAccessTokenWithNotice(t, ctx, ss, newPersonalAccessToken(user.ID.String(), model.NewCredentialToken()), 10)
	requireNoError(t, err)

	type transitionResult struct {
		result *store.PersonalAccessTokenMutationResult
		err    error
	}
	results := make(chan transitionResult, 2)
	for range 2 {
		preparation := preparePersonalAccessTokenMutation(t, ctx, ss, user.ID, token.ID.String(), store.PersonalAccessTokenMutationDisable, token.Auditable())
		notice := personalAccessTokenNoticeFixture(t, user.ID, model.MailTemplateIdentityPersonalAccessTokenDisabled, preparation.ActionAt, token.ExpiresAt)
		go func() {
			result, transitionErr := ss.PersonalAccessToken().ChangeState(ctx, &store.PersonalAccessTokenStateMutation{
				ID: token.ID.String(), UserID: user.ID.String(), Disabled: true, MaximumActive: 10,
				PreparationID: preparation.ID, Notice: notice,
			})
			results <- transitionResult{result: result, err: transitionErr}
		}()
	}
	fresh := 0
	for range 2 {
		result := <-results
		requireNoError(t, result.err)
		if result.result == nil || result.result.Token == nil || !result.result.Token.DisabledAt.Valid {
			t.Fatalf("concurrent disable result=%#v", result.result)
		}
		if result.result.Fresh {
			fresh++
		}
	}
	if fresh != 1 {
		t.Fatalf("fresh concurrent disables=%d, want 1", fresh)
	}
	if got := countPersonalAccessTokenTerminalAudits(t, ctx, ss, user.ID, "personal_access_token.disable"); got != 1 {
		t.Fatalf("disable audits=%d, want 1", got)
	}
	requirePersonalAccessTokenDelivery(t, ctx, ss, user.ID, model.MailTemplateIdentityPersonalAccessTokenDisabled, model.MailDeliveryQueued)

	revokeResults := make(chan transitionResult, 2)
	for range 2 {
		preparation := preparePersonalAccessTokenMutation(t, ctx, ss, user.ID, token.ID.String(), store.PersonalAccessTokenMutationRevoke, token.Auditable())
		notice := personalAccessTokenNoticeFixture(t, user.ID, model.MailTemplateIdentityPersonalAccessTokenRevoked, preparation.ActionAt, token.ExpiresAt)
		go func() {
			result, transitionErr := ss.PersonalAccessToken().RevokeWithAudit(ctx, &store.PersonalAccessTokenRevocation{
				ID: token.ID.String(), UserID: user.ID.String(), PreparationID: preparation.ID, Notice: notice,
			})
			revokeResults <- transitionResult{result: result, err: transitionErr}
		}()
	}
	fresh = 0
	for range 2 {
		result := <-revokeResults
		requireNoError(t, result.err)
		if result.result == nil || result.result.Token == nil || !result.result.Token.RevokedAt.Valid {
			t.Fatalf("concurrent revoke result=%#v", result.result)
		}
		if result.result.Fresh {
			fresh++
		}
	}
	if fresh != 1 || countPersonalAccessTokenTerminalAudits(t, ctx, ss, user.ID, "personal_access_token.revoke") != 1 {
		t.Fatalf("fresh concurrent revokes=%d", fresh)
	}
	requirePersonalAccessTokenDelivery(t, ctx, ss, user.ID, model.MailTemplateIdentityPersonalAccessTokenRevoked, model.MailDeliveryQueued)
}

func testPersonalAccessTokenDatabaseClock(t *testing.T, ss store.Store) {
	ctx := context.Background()
	saveInstitution(t, ctx, ss)
	for _, skew := range []time.Duration{-2 * time.Hour, 2 * time.Hour} {
		user, _ := saveLocalUser(t, ctx, ss)
		wallBefore := model.NowUTC().Add(-time.Minute)
		candidate := newPersonalAccessToken(user.ID.String(), model.NewCredentialToken())
		candidate.ExpiresAt = model.NowUTC().Add(24 * time.Hour)
		preparation := preparePersonalAccessTokenMutation(t, ctx, ss, user.ID, "", store.PersonalAccessTokenMutationCreate, nil)
		preparedAt := preparation.ActionAt
		notice := personalAccessTokenNoticeFixture(t, user.ID, model.MailTemplateIdentityPersonalAccessTokenCreated, preparedAt, candidate.ExpiresAt)
		originalOccurrenceAt := notice.Occurrence.CreatedAt
		createdResult, err := ss.PersonalAccessToken().Create(ctx, &store.PersonalAccessTokenCreationMutation{
			Token: candidate, MaximumActive: 10, MinimumLifetime: time.Minute, MaximumLifetime: 48 * time.Hour,
			PreparationID: preparation.ID, Notice: notice,
		})
		requireNoError(t, err)
		created := createdResult.Token
		wallAfter := model.NowUTC().Add(time.Minute)
		if created.CreatedAt.Before(wallBefore) || created.CreatedAt.After(wallAfter) {
			t.Fatalf("Create(skew=%v) created_at = %s, want PostgreSQL wall time", skew, created.CreatedAt)
		}
		if !notice.Occurrence.CreatedAt.Equal(originalOccurrenceAt) {
			t.Fatalf("Create(skew=%v) mutated caller occurrence", skew)
		}
		completed := requirePersonalAccessTokenTerminalAudit(t, ctx, ss, user.ID, "personal_access_token.create")
		if completed.UpdatedAt.Before(wallBefore) || completed.UpdatedAt.After(wallAfter) || completed.UpdatedAt.Before(created.CreatedAt) {
			t.Fatalf("Create(skew=%v) audit=%s token=%s, want one authoritative wall-time window", skew, completed.UpdatedAt, created.CreatedAt)
		}
		deliveries, listErr := ss.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{TemplateKeys: []model.MailTemplateKey{model.MailTemplateIdentityPersonalAccessTokenCreated}, Limit: 20})
		requireNoError(t, listErr)
		var delivery *model.MailDelivery
		for _, candidateDelivery := range deliveries {
			if candidateDelivery.TargetUserID == user.ID {
				delivery = candidateDelivery
				break
			}
		}
		if delivery == nil || !delivery.CreatedAt.Equal(created.CreatedAt) || delivery.Deadline.Sub(delivery.CreatedAt) != 24*time.Hour {
			t.Fatalf("Create(skew=%v) delivery=%#v token_at=%s", skew, delivery, created.CreatedAt)
		}
		job, getErr := ss.Job().Get(ctx, delivery.JobID)
		requireNoError(t, getErr)
		if !job.CreatedAt.Equal(created.CreatedAt) || !job.AvailableAt.Equal(created.CreatedAt) {
			t.Fatalf("Create(skew=%v) job=%#v token_at=%s", skew, job, created.CreatedAt)
		}
	}
}

func testPersonalAccessTokenAtomicSecurityNotices(t *testing.T, ss store.Store) {
	ctx := context.Background()
	saveInstitution(t, ctx, ss)
	user, _ := saveLocalUser(t, ctx, ss)
	at := model.NowUTC().Add(-time.Minute)
	candidate := newPersonalAccessToken(user.ID.String(), model.NewCredentialToken())
	candidate.ExpiresAt = at.Add(time.Hour)
	createPreparation := preparePersonalAccessTokenMutation(t, ctx, ss, user.ID, "", store.PersonalAccessTokenMutationCreate, nil)
	createdNotice := personalAccessTokenNoticeFixture(t, user.ID, model.MailTemplateIdentityPersonalAccessTokenCreated, createPreparation.ActionAt, candidate.ExpiresAt)
	createdResult, err := ss.PersonalAccessToken().Create(ctx, &store.PersonalAccessTokenCreationMutation{
		Token: candidate, MaximumActive: 10, MinimumLifetime: time.Minute, MaximumLifetime: 24 * time.Hour,
		PreparationID: createPreparation.ID, Notice: createdNotice,
	})
	requireNoError(t, err)
	created := createdResult.Token
	if created.TokenHash != candidate.TokenHash || created.TokenHash == "" {
		t.Fatalf("Create() = %#v", created)
	}
	requirePersonalAccessTokenTerminalAudit(t, ctx, ss, user.ID, "personal_access_token.create")
	requirePersonalAccessTokenDelivery(t, ctx, ss, user.ID, model.MailTemplateIdentityPersonalAccessTokenCreated, model.MailDeliveryQueued)

	disableAt := at.Add(time.Second)
	disablePreparation := preparePersonalAccessTokenMutation(t, ctx, ss, user.ID, created.ID.String(), store.PersonalAccessTokenMutationDisable, created.Auditable())
	disableNotice := personalAccessTokenNoticeFixture(t, user.ID, model.MailTemplateIdentityPersonalAccessTokenDisabled, disablePreparation.ActionAt, created.ExpiresAt)
	disableNotice.Delivery, disableNotice.Job = suppressSecurityNoticeForDisabledMail(t, disableNotice.Delivery, disableNotice.Job)
	disabledResult, err := ss.PersonalAccessToken().ChangeState(ctx, &store.PersonalAccessTokenStateMutation{
		ID: created.ID.String(), UserID: user.ID.String(), Disabled: true, MaximumActive: 10,
		PreparationID: disablePreparation.ID, Notice: disableNotice,
	})
	requireNoError(t, err)
	disabled := disabledResult.Token
	if !disabled.DisabledAt.Valid {
		t.Fatalf("ChangeState(disable) = %#v", disabled)
	}
	requirePersonalAccessTokenTerminalAudit(t, ctx, ss, user.ID, "personal_access_token.disable")
	requirePersonalAccessTokenDelivery(t, ctx, ss, user.ID, model.MailTemplateIdentityPersonalAccessTokenDisabled, model.MailDeliverySuppressed)

	failedAt := disableAt.Add(time.Second)
	failedNotice := personalAccessTokenNoticeFixture(t, user.ID, model.MailTemplateIdentityPersonalAccessTokenEnabled, failedAt, created.ExpiresAt)
	if _, err = ss.PersonalAccessToken().ChangeState(ctx, &store.PersonalAccessTokenStateMutation{
		ID: created.ID.String(), UserID: user.ID.String(), Disabled: false, MaximumActive: 10,
		PreparationID: model.NewId(), Notice: failedNotice,
	}); err == nil {
		t.Fatal("ChangeState() committed without its persisted audit attempt")
	}
	unchanged, getErr := ss.PersonalAccessToken().Get(ctx, created.ID.String())
	requireNoError(t, getErr)
	if !unchanged.DisabledAt.Valid {
		t.Fatalf("failed ChangeState() changed token = %#v", unchanged)
	}
	enablePreparation := preparePersonalAccessTokenMutation(t, ctx, ss, user.ID, created.ID.String(), store.PersonalAccessTokenMutationEnable, disabled.Auditable())
	enabledResult, err := ss.PersonalAccessToken().ChangeState(ctx, &store.PersonalAccessTokenStateMutation{
		ID: created.ID.String(), UserID: user.ID.String(), Disabled: false, MaximumActive: 10,
		PreparationID: enablePreparation.ID,
		Notice:        personalAccessTokenNoticeFixture(t, user.ID, model.MailTemplateIdentityPersonalAccessTokenEnabled, enablePreparation.ActionAt, created.ExpiresAt),
	})
	requireNoError(t, err)
	enabled := enabledResult.Token
	if enabled.DisabledAt.Valid {
		t.Fatalf("ChangeState(enable) = %#v", enabled)
	}

	revokePreparation := preparePersonalAccessTokenMutation(t, ctx, ss, user.ID, created.ID.String(), store.PersonalAccessTokenMutationRevoke, enabled.Auditable())
	revokedResult, err := ss.PersonalAccessToken().RevokeWithAudit(ctx, &store.PersonalAccessTokenRevocation{
		ID: created.ID.String(), UserID: user.ID.String(), PreparationID: revokePreparation.ID,
		Notice: personalAccessTokenNoticeFixture(t, user.ID, model.MailTemplateIdentityPersonalAccessTokenRevoked, revokePreparation.ActionAt, created.ExpiresAt),
	})
	requireNoError(t, err)
	revoked := revokedResult.Token
	if !revoked.RevokedAt.Valid {
		t.Fatalf("RevokeWithAudit() = %#v", revoked)
	}
}

func personalAccessTokenNoticeFixture(t *testing.T, userID model.UserID, key model.MailTemplateKey, at, expiresAt time.Time) store.PersonalAccessTokenSecurityNotice {
	t.Helper()
	occurrence, delivery, job := securityNoticeMailFixture(t, userID, key, at.UnixMilli())
	return store.PersonalAccessTokenSecurityNotice{Occurrence: occurrence, Delivery: delivery, Job: job, ExpiresAt: expiresAt}
}

func requirePersonalAccessTokenDelivery(t *testing.T, ctx context.Context, ss store.Store, userID model.UserID, key model.MailTemplateKey, state model.MailDeliveryState) {
	t.Helper()
	deliveries, err := ss.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{TemplateKeys: []model.MailTemplateKey{key}, Limit: 10})
	requireNoError(t, err)
	matches := 0
	for _, delivery := range deliveries {
		if delivery.TargetUserID == userID && delivery.State == state {
			matches++
		}
	}
	if matches != 1 {
		t.Fatalf("deliveries for user=%s key=%q = %#v, want one %q", userID, key, deliveries, state)
	}
}

func createPersonalAccessTokenWithNotice(t *testing.T, ctx context.Context, ss store.Store, token *model.PersonalAccessToken, maximum int) (*model.PersonalAccessToken, error) {
	t.Helper()
	input := personalAccessTokenCreationInput(t, ctx, ss, token, maximum)
	result, err := ss.PersonalAccessToken().Create(ctx, input)
	if err != nil {
		return nil, err
	}
	return result.Token, nil
}

func personalAccessTokenCreationInput(t *testing.T, ctx context.Context, ss store.Store, token *model.PersonalAccessToken, maximum int) *store.PersonalAccessTokenCreationMutation {
	t.Helper()
	preparation := preparePersonalAccessTokenMutation(t, ctx, ss, token.UserID, "", store.PersonalAccessTokenMutationCreate, nil)
	return &store.PersonalAccessTokenCreationMutation{
		Token: token, MaximumActive: maximum, MinimumLifetime: time.Millisecond,
		MaximumLifetime: 48 * time.Hour, PreparationID: preparation.ID,
		Notice: personalAccessTokenNoticeFixture(t, token.UserID, model.MailTemplateIdentityPersonalAccessTokenCreated, preparation.ActionAt, token.ExpiresAt),
	}
}

func changePersonalAccessTokenStateWithNotice(t *testing.T, ctx context.Context, ss store.Store, token *model.PersonalAccessToken, disabled bool, maximum int) (*model.PersonalAccessToken, error) {
	t.Helper()
	key := model.MailTemplateIdentityPersonalAccessTokenEnabled
	kind := store.PersonalAccessTokenMutationEnable
	if disabled {
		key = model.MailTemplateIdentityPersonalAccessTokenDisabled
		kind = store.PersonalAccessTokenMutationDisable
	}
	preparation := preparePersonalAccessTokenMutation(t, ctx, ss, token.UserID, token.ID.String(), kind, token.Auditable())
	result, err := ss.PersonalAccessToken().ChangeState(ctx, &store.PersonalAccessTokenStateMutation{
		ID: token.ID.String(), UserID: token.UserID.String(), Disabled: disabled, MaximumActive: maximum,
		PreparationID: preparation.ID,
		Notice:        personalAccessTokenNoticeFixture(t, token.UserID, key, preparation.ActionAt, token.ExpiresAt),
	})
	if err != nil {
		return nil, err
	}
	return result.Token, nil
}

func revokePersonalAccessTokenWithNotice(t *testing.T, ctx context.Context, ss store.Store, token *model.PersonalAccessToken, userID model.UserID) (*model.PersonalAccessToken, error) {
	t.Helper()
	preparation := preparePersonalAccessTokenMutation(t, ctx, ss, userID, token.ID.String(), store.PersonalAccessTokenMutationRevoke, token.Auditable())
	result, err := ss.PersonalAccessToken().RevokeWithAudit(ctx, &store.PersonalAccessTokenRevocation{
		ID: token.ID.String(), UserID: userID.String(), PreparationID: preparation.ID,
		Notice: personalAccessTokenNoticeFixture(t, userID, model.MailTemplateIdentityPersonalAccessTokenRevoked, preparation.ActionAt, token.ExpiresAt),
	})
	if err != nil {
		return nil, err
	}
	return result.Token, nil
}

func preparePersonalAccessTokenMutation(t *testing.T, ctx context.Context, ss store.Store, userID model.UserID, tokenID string, kind store.PersonalAccessTokenMutationKind, prior map[string]any) *store.PreparedPersonalAccessTokenMutation {
	t.Helper()
	institution, err := ss.Institution().GetSingleton(ctx)
	requireNoError(t, err)
	session, _, _ := saveSession(t, ctx, ss, userID.String(), 100)
	prepared, err := ss.PersonalAccessToken().PrepareMutation(ctx, &store.PersonalAccessTokenMutationPreparation{
		UserID: userID.String(), TokenID: tokenID, Kind: kind, Lifetime: 5 * time.Minute,
		Audit: personalAccessTokenAuditDraft(userID, session.ID, institution.ID, kind, prior),
	})
	requireNoError(t, err)
	return prepared
}

func personalAccessTokenAuditDraft(userID model.UserID, sessionID model.SessionID, institutionID model.InstitutionID, kind store.PersonalAccessTokenMutationKind, prior map[string]any) *model.AuditEvent {
	action := map[store.PersonalAccessTokenMutationKind]string{
		store.PersonalAccessTokenMutationCreate:  "personal_access_token.create",
		store.PersonalAccessTokenMutationEnable:  "personal_access_token.enable",
		store.PersonalAccessTokenMutationDisable: "personal_access_token.disable",
		store.PersonalAccessTokenMutationRevoke:  "personal_access_token.revoke",
	}[kind]
	priorJSON, _ := model.EncodeAuditData(prior)
	return &model.AuditEvent{ActorID: userID, SessionID: sessionID, Action: action,
		Resource:  model.Resource{Type: model.ResourceInstitution, ID: institutionID.String()},
		ScopeType: model.RoleScopeInstitution, ScopeID: institutionID.String(), Status: model.AuditStatusAttempt,
		NodeID: "storetest", ClientType: string(model.SessionClientWeb), AuthMethod: "password", PriorState: priorJSON}
}

func requirePersonalAccessTokenTerminalAudit(t *testing.T, ctx context.Context, ss store.Store, userID model.UserID, action string) *model.AuditEvent {
	t.Helper()
	events, err := ss.Audit().List(ctx, store.AuditListOptions{ActorId: userID.String(), Action: action, Limit: 20, Visibility: store.AuditVisibilityScope{InstitutionWide: true}})
	requireNoError(t, err)
	for _, event := range events {
		if event.Status == model.AuditStatusSuccess {
			return event
		}
	}
	t.Fatalf("missing successful %s audit for %s: %#v", action, userID, events)
	return nil
}

func countPersonalAccessTokenTerminalAudits(t *testing.T, ctx context.Context, ss store.Store, userID model.UserID, action string) int {
	t.Helper()
	events, err := ss.Audit().List(ctx, store.AuditListOptions{ActorId: userID.String(), Action: action, Limit: 20, Visibility: store.AuditVisibilityScope{InstitutionWide: true}})
	requireNoError(t, err)
	count := 0
	for _, event := range events {
		if event.Status == model.AuditStatusSuccess || event.Status == model.AuditStatusFail {
			count++
		}
	}
	return count
}

func testPersonalAccessTokenRejectsDisabledAccount(t *testing.T, ss store.Store) {
	ctx := context.Background()
	saveInstitution(t, ctx, ss)
	user, _ := saveLocalUser(t, ctx, ss)
	raw := model.NewCredentialToken()
	token, err := createPersonalAccessTokenWithNotice(t, ctx, ss, newPersonalAccessToken(user.ID.String(), raw), 10)
	requireNoError(t, err)
	at := model.MillisFromTime(token.CreatedAt) + 10
	audit := saveUserProfileAuditAttempt(t, ctx, ss, user.ID.String())
	_, err = ss.User().SetDisabledWithAudit(ctx, userDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
		ID: user.ID.String(), ExpectedRevision: user.Revision, Disabled: true,
		ChangedAt: at, RevocationReason: "account disabled",
		AuditEventID: audit.ID.String(), AuditAt: at,
	}))
	requireNoError(t, err)

	if _, err := ss.PersonalAccessToken().Resolve(
		ctx,
		model.HashToken(raw),
		at+1,
		1000,
	); !store.IsNotFound(err) {
		t.Fatalf("disabled-account Resolve() error = %v, want not found", err)
	}
}

func testPersonalAccessTokenLifecycle(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	unit := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "engineering")
	user, _ := saveLocalUser(t, ctx, ss)
	raw := model.NewCredentialToken()
	token := newPersonalAccessToken(user.ID.String(), raw)
	token.AcademicUnitID = unit.ID
	token, err := createPersonalAccessTokenWithNotice(t, ctx, ss, token, 10)
	requireNoError(t, err)

	got, err := ss.PersonalAccessToken().Get(ctx, token.ID.String())
	requireNoError(t, err)
	if got.TokenHash != model.HashToken(raw) ||
		got.AcademicUnitID != unit.ID ||
		len(got.Scopes) != 2 {
		t.Fatalf("Get() = %#v", got)
	}
	now := model.MillisFromTime(token.CreatedAt) + 10
	resolved, err := ss.PersonalAccessToken().Resolve(
		ctx,
		model.HashToken(raw),
		now,
		1000,
	)
	requireNoError(t, err)
	if resolved.User.ID.String() != user.ID.String() ||
		resolved.Token.ID != token.ID ||
		resolved.Token.LastUsedAt.Millis() != now {
		t.Fatalf("Resolve() = %#v", resolved)
	}
	debounced, err := ss.PersonalAccessToken().Resolve(
		ctx,
		model.HashToken(raw),
		now+100,
		1000,
	)
	requireNoError(t, err)
	if debounced.Token.LastUsedAt.Millis() != now {
		t.Fatalf("debounced last_used_at = %d, want %d", debounced.Token.LastUsedAt.Millis(), now)
	}
	disabled, err := changePersonalAccessTokenStateWithNotice(t, ctx, ss, token, true, 10)
	requireNoError(t, err)
	if !disabled.DisabledAt.Valid || disabled.IsActiveAt(model.NowUTC()) {
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
	enabled, err := changePersonalAccessTokenStateWithNotice(t, ctx, ss, disabled, false, 10)
	requireNoError(t, err)
	if enabled.DisabledAt.Valid || !enabled.IsActiveAt(model.NowUTC()) {
		t.Fatalf("enabled token = %#v", enabled)
	}
	other, _ := saveLocalUser(t, ctx, ss)
	otherSession, _, _ := saveSession(t, ctx, ss, other.ID.String(), 10)
	if _, err := ss.PersonalAccessToken().PrepareMutation(ctx, &store.PersonalAccessTokenMutationPreparation{
		UserID: other.ID.String(), TokenID: token.ID.String(), Kind: store.PersonalAccessTokenMutationRevoke, Lifetime: 5 * time.Minute,
		Audit: personalAccessTokenAuditDraft(other.ID, otherSession.ID, institution.ID, store.PersonalAccessTokenMutationRevoke, token.Auditable()),
	}); !store.IsNotFound(err) {
		t.Fatalf("cross-user revoke error = %v, want not found", err)
	}
	revoked, err := revokePersonalAccessTokenWithNotice(t, ctx, ss, token, user.ID)
	requireNoError(t, err)
	if !revoked.RevokedAt.Valid {
		t.Fatalf("revoked_at = %d", revoked.RevokedAt.Millis())
	}
	if _, err := ss.PersonalAccessToken().Resolve(
		ctx,
		model.HashToken(raw),
		now+201,
		1000,
	); !store.IsNotFound(err) {
		t.Fatalf("revoked Resolve() error = %v, want not found", err)
	}
	list, err := ss.PersonalAccessToken().ListByUser(ctx, user.ID.String())
	requireNoError(t, err)
	if len(list) != 1 || !list[0].RevokedAt.Valid {
		t.Fatalf("ListByUser() = %#v", list)
	}

	unscopedRaw := model.NewCredentialToken()
	unscoped, err := createPersonalAccessTokenWithNotice(t, ctx, ss, newPersonalAccessToken(user.ID.String(), unscopedRaw), 10)
	requireNoError(t, err)
	if !unscoped.AcademicUnitID.IsZero() {
		t.Fatalf("unscoped token acquired academic unit %q", unscoped.AcademicUnitID)
	}
	unscopedResolved, err := ss.PersonalAccessToken().Resolve(
		ctx,
		model.HashToken(unscopedRaw),
		model.MillisFromTime(unscoped.CreatedAt)+1,
		1000,
	)
	requireNoError(t, err)
	if !unscopedResolved.Token.AcademicUnitID.IsZero() {
		t.Fatalf("unscoped resolution acquired academic unit %q", unscopedResolved.Token.AcademicUnitID)
	}
}

func testPersonalAccessTokenReenableMaximum(t *testing.T, ss store.Store) {
	ctx := context.Background()
	saveInstitution(t, ctx, ss)
	user, _ := saveLocalUser(t, ctx, ss)
	first, err := createPersonalAccessTokenWithNotice(t, ctx, ss, newPersonalAccessToken(user.ID.String(), model.NewCredentialToken()), 1)
	requireNoError(t, err)
	_, err = changePersonalAccessTokenStateWithNotice(t, ctx, ss, first, true, 1)
	requireNoError(t, err)
	second, err := createPersonalAccessTokenWithNotice(t, ctx, ss, newPersonalAccessToken(user.ID.String(), model.NewCredentialToken()), 1)
	requireNoError(t, err)
	if _, err := changePersonalAccessTokenStateWithNotice(t, ctx, ss, first, false, 1); !store.IsConflict(err) {
		t.Fatalf("reenable at active limit error = %v, want conflict", err)
	}
	_, err = revokePersonalAccessTokenWithNotice(t, ctx, ss, second, user.ID)
	requireNoError(t, err)
	enabled, err := changePersonalAccessTokenStateWithNotice(t, ctx, ss, first, false, 1)
	requireNoError(t, err)
	if enabled.DisabledAt.Valid {
		t.Fatalf("reenabled token = %#v", enabled)
	}
}

func testPersonalAccessTokenMaximumActive(t *testing.T, ss store.Store) {
	ctx := context.Background()
	saveInstitution(t, ctx, ss)
	user, _ := saveLocalUser(t, ctx, ss)
	const contenders = 8
	inputs := make([]*store.PersonalAccessTokenCreationMutation, 0, contenders)
	for range contenders {
		inputs = append(inputs, personalAccessTokenCreationInput(t, ctx, ss, newPersonalAccessToken(user.ID.String(), model.NewCredentialToken()), 1))
	}
	start := make(chan struct{})
	results := make(chan error, contenders)
	var wait sync.WaitGroup
	for _, input := range inputs {
		input := input
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := ss.PersonalAccessToken().Create(ctx, input)
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
			t.Fatalf("concurrent Create() error = %v", err)
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
	token := newPersonalAccessToken(user.ID.String(), model.NewCredentialToken())
	token.Scopes = []string{"future.permission"}
	if _, err := createPersonalAccessTokenWithNotice(t, ctx, ss, token, 10); err == nil {
		t.Fatal("Create() accepted an unknown action scope")
	}
	token = newPersonalAccessToken(user.ID.String(), model.NewCredentialToken())
	token.Scopes = []string{string(model.ActionRoleBindingManage)}
	if _, err := createPersonalAccessTokenWithNotice(t, ctx, ss, token, 10); err == nil {
		t.Fatal("Create() accepted an interactive-only action scope")
	}
}

func newPersonalAccessToken(
	userID string,
	raw string,
) *model.PersonalAccessToken {
	return &model.PersonalAccessToken{
		UserID:      model.UserID(userID),
		Description: "automation token",
		TokenHash:   model.HashToken(raw),
		Scopes: []string{
			string(model.ActionAcademicUnitView),
			string(model.ActionClassView),
		},
		ExpiresAt: model.NowUTC().Add(24 * time.Hour),
	}
}
