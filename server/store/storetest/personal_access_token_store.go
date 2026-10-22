// ---------------------------------------------------------------------------------------------
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
//
// Adapted from Mattermost server/channels/store/storetest/
// user_access_token_store.go. Proctor additionally verifies finite expiry,
// scope and academic-unit preservation, authoritative account resolution,
// debounced usage metadata, ownership-bound revocation, and serialized limits.

package storetest

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestPersonalAccessTokenStore(t *testing.T, ss store.Store) {
	t.Run("NativeExpiryAndUsagePrecision", func(t *testing.T) {
		testPersonalAccessTokenNativeExpiry(t, ss)
	})
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
	require.NoError(t, err)

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
				ID: token.ID, UserID: user.ID, Disabled: true, MaximumActive: 10,
				PreparationID: preparation.ID, Notice: notice,
			})
			results <- transitionResult{result: result, err: transitionErr}
		}()
	}
	fresh := 0
	for range 2 {
		result := <-results
		require.NoError(t, result.err)
		require.NotNil(t, result.result)
		require.NotNil(t, result.result.Token)
		require.True(t, result.result.Token.DisabledAt.Valid)
		if result.result.Fresh {
			fresh++
		}
	}
	require.Equal(t, 1, fresh, "fresh concurrent disables")
	require.Equal(t, 1, countPersonalAccessTokenTerminalAudits(t, ctx, ss, user.ID, "personal_access_token.disable"), "disable audits")
	requirePersonalAccessTokenDelivery(t, ctx, ss, user.ID, model.MailTemplateIdentityPersonalAccessTokenDisabled, model.MailDeliveryQueued)

	revokeResults := make(chan transitionResult, 2)
	for range 2 {
		preparation := preparePersonalAccessTokenMutation(t, ctx, ss, user.ID, token.ID.String(), store.PersonalAccessTokenMutationRevoke, token.Auditable())
		notice := personalAccessTokenNoticeFixture(t, user.ID, model.MailTemplateIdentityPersonalAccessTokenRevoked, preparation.ActionAt, token.ExpiresAt)
		go func() {
			result, transitionErr := ss.PersonalAccessToken().Revoke(ctx, &store.PersonalAccessTokenRevocation{
				ID: token.ID, UserID: user.ID, PreparationID: preparation.ID, Notice: notice,
			})
			revokeResults <- transitionResult{result: result, err: transitionErr}
		}()
	}
	fresh = 0
	for range 2 {
		result := <-revokeResults
		require.NoError(t, result.err)
		require.NotNil(t, result.result)
		require.NotNil(t, result.result.Token)
		require.True(t, result.result.Token.RevokedAt.Valid)
		if result.result.Fresh {
			fresh++
		}
	}
	require.Equal(t, 1, fresh, "fresh concurrent revokes")
	require.Equal(t, 1, countPersonalAccessTokenTerminalAudits(t, ctx, ss, user.ID, "personal_access_token.revoke"), "fresh concurrent revokes")
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
		require.NoError(t, err)
		created := createdResult.Token
		wallAfter := model.NowUTC().Add(time.Minute)
		require.False(t, created.CreatedAt.Before(wallBefore), "Create(skew=%v) created_at = %s, want PostgreSQL wall time", skew, created.CreatedAt)
		require.False(t, created.CreatedAt.After(wallAfter), "Create(skew=%v) created_at = %s, want PostgreSQL wall time", skew, created.CreatedAt)
		require.True(t, notice.Occurrence.CreatedAt.Equal(originalOccurrenceAt), "Create(skew=%v) mutated caller occurrence", skew)
		completed := requirePersonalAccessTokenTerminalAudit(t, ctx, ss, user.ID, "personal_access_token.create")
		require.False(t, completed.UpdatedAt.Before(wallBefore), "Create(skew=%v) audit=%s token", skew, completed.UpdatedAt)
		require.False(t, completed.UpdatedAt.After(wallAfter), "Create(skew=%v) audit=%s token", skew, completed.UpdatedAt)
		require.False(t, completed.UpdatedAt.Before(created.CreatedAt), "Create(skew=%v) audit=%s token", skew, completed.UpdatedAt)
		deliveries, listErr := ss.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{TemplateKeys: []model.MailTemplateKey{model.MailTemplateIdentityPersonalAccessTokenCreated}, Limit: 20})
		require.NoError(t, listErr)
		var delivery *model.MailDelivery
		for _, candidateDelivery := range deliveries {
			if candidateDelivery.TargetUserID == user.ID {
				delivery = candidateDelivery
				break
			}
		}
		require.NotNil(t, delivery, "Create(skew=%v) delivery=%#v token_at", skew, delivery)
		require.True(t, delivery.CreatedAt.Equal(created.CreatedAt), "Create(skew=%v) delivery=%#v token_at", skew, delivery)
		require.Equal(t, 24*time.Hour, delivery.Deadline.Sub(delivery.CreatedAt), "Create(skew=%v) delivery=%#v token_at", skew, delivery)
		job, getErr := ss.Job().Get(ctx, delivery.JobID)
		require.NoError(t, getErr)
		require.True(t, job.CreatedAt.Equal(created.CreatedAt), "Create(skew=%v) job=%#v token_at", skew, job)
		require.True(t, job.AvailableAt.Equal(created.CreatedAt), "Create(skew=%v) job=%#v token_at", skew, job)
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
	require.NoError(t, err)
	created := createdResult.Token
	require.Equal(t, candidate.TokenHash, created.TokenHash)
	require.NotEmpty(t, created.TokenHash)
	requirePersonalAccessTokenTerminalAudit(t, ctx, ss, user.ID, "personal_access_token.create")
	requirePersonalAccessTokenDelivery(t, ctx, ss, user.ID, model.MailTemplateIdentityPersonalAccessTokenCreated, model.MailDeliveryQueued)

	disableAt := at.Add(time.Second)
	disablePreparation := preparePersonalAccessTokenMutation(t, ctx, ss, user.ID, created.ID.String(), store.PersonalAccessTokenMutationDisable, created.Auditable())
	disableNotice := personalAccessTokenNoticeFixture(t, user.ID, model.MailTemplateIdentityPersonalAccessTokenDisabled, disablePreparation.ActionAt, created.ExpiresAt)
	disableNotice.Delivery, disableNotice.Job = suppressSecurityNoticeForDisabledMail(t, disableNotice.Delivery, disableNotice.Job)
	disabledResult, err := ss.PersonalAccessToken().ChangeState(ctx, &store.PersonalAccessTokenStateMutation{
		ID: created.ID, UserID: user.ID, Disabled: true, MaximumActive: 10,
		PreparationID: disablePreparation.ID, Notice: disableNotice,
	})
	require.NoError(t, err)
	disabled := disabledResult.Token
	require.True(t, disabled.DisabledAt.Valid)
	requirePersonalAccessTokenTerminalAudit(t, ctx, ss, user.ID, "personal_access_token.disable")
	requirePersonalAccessTokenDelivery(t, ctx, ss, user.ID, model.MailTemplateIdentityPersonalAccessTokenDisabled, model.MailDeliverySuppressed)

	failedAt := disableAt.Add(time.Second)
	failedNotice := personalAccessTokenNoticeFixture(t, user.ID, model.MailTemplateIdentityPersonalAccessTokenEnabled, failedAt, created.ExpiresAt)
	_, err = ss.PersonalAccessToken().ChangeState(ctx, &store.PersonalAccessTokenStateMutation{
		ID: created.ID, UserID: user.ID, Disabled: false, MaximumActive: 10,
		PreparationID: model.NewPersonalAccessTokenPreparationID(), Notice: failedNotice,
	})
	require.Error(t, err, "ChangeState() committed without its persisted audit attempt")
	unchanged, getErr := ss.PersonalAccessToken().Get(ctx, created.ID)
	require.NoError(t, getErr)
	require.True(t, unchanged.DisabledAt.Valid)
	enablePreparation := preparePersonalAccessTokenMutation(t, ctx, ss, user.ID, created.ID.String(), store.PersonalAccessTokenMutationEnable, disabled.Auditable())
	enabledResult, err := ss.PersonalAccessToken().ChangeState(ctx, &store.PersonalAccessTokenStateMutation{
		ID: created.ID, UserID: user.ID, Disabled: false, MaximumActive: 10,
		PreparationID: enablePreparation.ID,
		Notice:        personalAccessTokenNoticeFixture(t, user.ID, model.MailTemplateIdentityPersonalAccessTokenEnabled, enablePreparation.ActionAt, created.ExpiresAt),
	})
	require.NoError(t, err)
	enabled := enabledResult.Token
	require.False(t, enabled.DisabledAt.Valid)

	revokePreparation := preparePersonalAccessTokenMutation(t, ctx, ss, user.ID, created.ID.String(), store.PersonalAccessTokenMutationRevoke, enabled.Auditable())
	revokedResult, err := ss.PersonalAccessToken().Revoke(ctx, &store.PersonalAccessTokenRevocation{
		ID: created.ID, UserID: user.ID, PreparationID: revokePreparation.ID,
		Notice: personalAccessTokenNoticeFixture(t, user.ID, model.MailTemplateIdentityPersonalAccessTokenRevoked, revokePreparation.ActionAt, created.ExpiresAt),
	})
	require.NoError(t, err)
	revoked := revokedResult.Token
	require.True(t, revoked.RevokedAt.Valid)
}

func personalAccessTokenNoticeFixture(t *testing.T, userID model.UserID, key model.MailTemplateKey, at, expiresAt time.Time) store.PersonalAccessTokenSecurityNotice {
	t.Helper()
	occurrence, delivery, job := securityNoticeMailFixture(t, userID, key, at)
	return store.PersonalAccessTokenSecurityNotice{Occurrence: occurrence, Delivery: delivery, Job: job, ExpiresAt: expiresAt}
}

func requirePersonalAccessTokenDelivery(t *testing.T, ctx context.Context, ss store.Store, userID model.UserID, key model.MailTemplateKey, state model.MailDeliveryState) {
	t.Helper()
	deliveries, err := ss.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{TemplateKeys: []model.MailTemplateKey{key}, Limit: 10})
	require.NoError(t, err)
	matches := 0
	for _, delivery := range deliveries {
		if delivery.TargetUserID == userID && delivery.State == state {
			matches++
		}
	}
	require.Equal(t, 1, matches, "deliveries for user=%s key=%q = %#v, want one %q", userID, key, deliveries, state)
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
		ID: token.ID, UserID: token.UserID, Disabled: disabled, MaximumActive: maximum,
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
	result, err := ss.PersonalAccessToken().Revoke(ctx, &store.PersonalAccessTokenRevocation{
		ID: token.ID, UserID: userID, PreparationID: preparation.ID,
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
	require.NoError(t, err)
	session, _, _ := saveSession(t, ctx, ss, userID.String(), 100)
	prepared, err := ss.PersonalAccessToken().PrepareMutation(ctx, &store.PersonalAccessTokenMutationPreparation{
		UserID: userID, TokenID: model.PersonalAccessTokenID(tokenID), Kind: kind, Lifetime: 5 * time.Minute,
		AuditEvent: personalAccessTokenAuditDraft(userID, session.ID, institution.ID, kind, prior),
	})
	require.NoError(t, err)
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
	events, err := ss.Audit().List(ctx, store.AuditListOptions{ActorID: userID, Action: action, Limit: 20, Visibility: store.AuditVisibilityScope{InstitutionWide: true}})
	require.NoError(t, err)
	for _, event := range events {
		if event.Status == model.AuditStatusSuccess {
			return event
		}
	}
	require.FailNow(t, fmt.Sprintf("missing successful %s audit for %s: %#v", action, userID, events))
	return nil
}

func countPersonalAccessTokenTerminalAudits(t *testing.T, ctx context.Context, ss store.Store, userID model.UserID, action string) int {
	t.Helper()
	events, err := ss.Audit().List(ctx, store.AuditListOptions{ActorID: userID, Action: action, Limit: 20, Visibility: store.AuditVisibilityScope{InstitutionWide: true}})
	require.NoError(t, err)
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
	require.NoError(t, err)
	at := model.TimeUTC(token.CreatedAt).Add(10 * time.Millisecond)
	audit := saveUserProfileAuditAttempt(t, ctx, ss, user.ID.String())
	_, err = ss.User().SetDisabled(ctx, userDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
		ID: user.ID, ExpectedRevision: user.Revision, Disabled: true,
		ChangedAt: at, RevocationReason: model.SessionRevocationAccountDisabled,
		AuditEventID: audit.ID, AuditAt: at,
	}))
	require.NoError(t, err)

	_, err = ss.PersonalAccessToken().Resolve(
		ctx,
		model.HashToken(raw),
		model.TimeUTC(at.Add(1*time.Millisecond)),
		time.Second,
	)
	require.True(t, store.IsNotFound(err), "disabled-account Resolve() error")
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
	require.NoError(t, err)

	got, err := ss.PersonalAccessToken().Get(ctx, token.ID)
	require.NoError(t, err)
	require.Equal(t, model.HashToken(raw), got.TokenHash)
	require.Equal(t, unit.ID, got.AcademicUnitID)
	require.Len(t, got.Scopes, 2)
	now := model.MillisFromTime(token.CreatedAt) + 10
	resolved, err := ss.PersonalAccessToken().Resolve(
		ctx,
		model.HashToken(raw),
		model.TimeFromMillis(now),
		time.Second,
	)
	require.NoError(t, err)
	require.Equal(t, user.ID.String(), resolved.User.ID.String())
	require.Equal(t, token.ID, resolved.Token.ID)
	require.Equal(t, now, resolved.Token.LastUsedAt.Millis())
	debounced, err := ss.PersonalAccessToken().Resolve(
		ctx,
		model.HashToken(raw),
		model.TimeFromMillis(now+100),
		time.Second,
	)
	require.NoError(t, err)
	require.Equal(t, now, debounced.Token.LastUsedAt.Millis(), "debounced last_used_at")
	disabled, err := changePersonalAccessTokenStateWithNotice(t, ctx, ss, token, true, 10)
	require.NoError(t, err)
	require.True(t, disabled.DisabledAt.Valid)
	require.False(t, disabled.IsActiveAt(model.NowUTC()))
	_, err = ss.PersonalAccessToken().Resolve(
		ctx,
		model.HashToken(raw),
		model.TimeFromMillis(now+151),
		time.Second,
	)
	require.True(t, store.IsNotFound(err), "disabled Resolve() error")
	enabled, err := changePersonalAccessTokenStateWithNotice(t, ctx, ss, disabled, false, 10)
	require.NoError(t, err)
	require.False(t, enabled.DisabledAt.Valid)
	require.True(t, enabled.IsActiveAt(model.NowUTC()))
	other, _ := saveLocalUser(t, ctx, ss)
	otherSession, _, _ := saveSession(t, ctx, ss, other.ID.String(), 10)
	_, err = ss.PersonalAccessToken().PrepareMutation(ctx, &store.PersonalAccessTokenMutationPreparation{
		UserID: other.ID, TokenID: token.ID, Kind: store.PersonalAccessTokenMutationRevoke, Lifetime: 5 * time.Minute,
		AuditEvent: personalAccessTokenAuditDraft(other.ID, otherSession.ID, institution.ID, store.PersonalAccessTokenMutationRevoke, token.Auditable()),
	})
	require.True(t, store.IsNotFound(err), "cross-user revoke error")
	revoked, err := revokePersonalAccessTokenWithNotice(t, ctx, ss, token, user.ID)
	require.NoError(t, err)
	require.True(t, revoked.RevokedAt.Valid, "revoked_at")
	_, err = ss.PersonalAccessToken().Resolve(
		ctx,
		model.HashToken(raw),
		model.TimeFromMillis(now+201),
		time.Second,
	)
	require.True(t, store.IsNotFound(err), "revoked Resolve() error")
	list, err := ss.PersonalAccessToken().ListByUser(ctx, user.ID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.True(t, list[0].RevokedAt.Valid)

	unscopedRaw := model.NewCredentialToken()
	unscoped, err := createPersonalAccessTokenWithNotice(t, ctx, ss, newPersonalAccessToken(user.ID.String(), unscopedRaw), 10)
	require.NoError(t, err)
	require.True(t, unscoped.AcademicUnitID.IsZero())
	unscopedResolved, err := ss.PersonalAccessToken().Resolve(
		ctx,
		model.HashToken(unscopedRaw),
		unscoped.CreatedAt.Add(time.Millisecond),
		time.Second,
	)
	require.NoError(t, err)
	require.True(t, unscopedResolved.Token.AcademicUnitID.IsZero())
}

func testPersonalAccessTokenReenableMaximum(t *testing.T, ss store.Store) {
	ctx := context.Background()
	saveInstitution(t, ctx, ss)
	user, _ := saveLocalUser(t, ctx, ss)
	first, err := createPersonalAccessTokenWithNotice(t, ctx, ss, newPersonalAccessToken(user.ID.String(), model.NewCredentialToken()), 1)
	require.NoError(t, err)
	_, err = changePersonalAccessTokenStateWithNotice(t, ctx, ss, first, true, 1)
	require.NoError(t, err)
	second, err := createPersonalAccessTokenWithNotice(t, ctx, ss, newPersonalAccessToken(user.ID.String(), model.NewCredentialToken()), 1)
	require.NoError(t, err)
	_, err = changePersonalAccessTokenStateWithNotice(t, ctx, ss, first, false, 1)
	require.True(t, store.IsConflict(err), "reenable at active limit error")
	_, err = revokePersonalAccessTokenWithNotice(t, ctx, ss, second, user.ID)
	require.NoError(t, err)
	enabled, err := changePersonalAccessTokenStateWithNotice(t, ctx, ss, first, false, 1)
	require.NoError(t, err)
	require.False(t, enabled.DisabledAt.Valid)
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
			require.FailNow(t, fmt.Sprintf("concurrent Create() error = %v", err))
		}
	}
	require.Equal(t, 1, successes, "successes")
	require.Equal(t, contenders-1, conflicts, "successes")
}

func testPersonalAccessTokenRejectsUnknownScope(t *testing.T, ss store.Store) {
	ctx := context.Background()
	saveInstitution(t, ctx, ss)
	user, _ := saveLocalUser(t, ctx, ss)
	token := newPersonalAccessToken(user.ID.String(), model.NewCredentialToken())
	token.Scopes = []string{"future.permission"}
	_, err := createPersonalAccessTokenWithNotice(t, ctx, ss, token, 10)
	require.Error(t, err, "Create() accepted an unknown action scope")
	token = newPersonalAccessToken(user.ID.String(), model.NewCredentialToken())
	token.Scopes = []string{string(model.ActionRoleBindingManage)}
	_, err = createPersonalAccessTokenWithNotice(t, ctx, ss, token, 10)
	require.Error(t, err, "Create() accepted an interactive-only action scope")
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
