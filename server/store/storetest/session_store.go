// ---------------------------------------------------------------------------------------------
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
//
// Adapted from Mattermost server/channels/store/storetest/session_store.go.
// These tests additionally verify Proctor's split credential model, atomic
// access/refresh creation, refresh rotation, and replay revocation.

package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestSessionStores(t *testing.T, ss store.Store) {
	t.Run("SaveResolveAndList", func(t *testing.T) { testSessionSaveResolveAndList(t, ss) })
	t.Run("PasswordProofFencesCreationAndSurvivesRehash", func(t *testing.T) {
		testSessionPasswordProof(t, ss)
	})
	t.Run("MaximumActive", func(t *testing.T) { testSessionMaximumActive(t, ss) })
	t.Run("UpdateActivity", func(t *testing.T) { testSessionUpdateActivity(t, ss) })
	t.Run("EnforceExpiry", func(t *testing.T) { testSessionEnforceExpiry(t, ss) })
	t.Run("NativeTimeBoundaries", func(t *testing.T) { testSessionNativeTimeBoundaries(t, ss) })
	t.Run("Revoke", func(t *testing.T) { testSessionRevoke(t, ss) })
	t.Run("Revoke", func(t *testing.T) { testSessionRevokeWithAudit(t, ss) })
	t.Run("SelfRevocationWithAudit", func(t *testing.T) { testSelfSessionRevocationsWithAudit(t, ss) })
	t.Run("RevokeAllForUser", func(t *testing.T) { testSessionRevokeAllForUser(t, ss) })
	t.Run("RevokeAllForUser", func(t *testing.T) { testSessionRevokeAllForUserWithAudit(t, ss) })
	t.Run("DisabledMailRecordsTerminalAdministrativeNotice", func(t *testing.T) {
		testSessionDisabledMailRecordsTerminalAdministrativeNotice(t, ss)
	})
	t.Run("RotateAndDetectReplay", func(t *testing.T) { testSessionRotateAndDetectReplay(t, ss) })
	t.Run("RotateEnforcesSessionExpiry", func(t *testing.T) { testSessionRotateEnforcesSessionExpiry(t, ss) })
	t.Run("ConcurrentRefreshReplay", func(t *testing.T) { testSessionConcurrentRefreshReplay(t, ss) })
	t.Run("ConcurrentRefreshAndRevokeAll", func(t *testing.T) {
		testSessionConcurrentRefreshAndRevokeAll(t, ss)
	})
	t.Run("ListByUserPages", func(t *testing.T) { testSessionListByUserPages(t, ss) })
	t.Run("PurgeRetired", func(t *testing.T) { testSessionPurgeRetired(t, ss) })
	t.Run("PurgeRetiredCredentialLineage", func(t *testing.T) { testSessionPurgeRetiredCredentialLineage(t, ss) })
}

func testSessionListByUserPages(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	var saved []*model.Session
	for range 3 {
		session, _, _ := saveSession(t, ctx, ss, user.ID.String(), 10)
		saved = append(saved, session)
	}
	_, err := ss.Session().Withdraw(ctx, saved[1].ID, user.ID, model.GetMillis()+1, model.SessionRevocationInactiveUser)
	require.NoError(t, err)

	var paged []model.SessionID
	page := &store.SessionPageQuery{UserID: user.ID, Limit: 2}
	for {
		sessions, listErr := ss.Session().ListByUser(ctx, page)
		require.NoError(t, listErr)
		for _, session := range sessions {
			paged = append(paged, session.ID)
		}
		if len(sessions) < page.Limit {
			break
		}
		last := sessions[len(sessions)-1]
		page = &store.SessionPageQuery{UserID: user.ID, Limit: 2, BeforeCreatedAt: last.CreatedAt, BeforeID: last.ID}
	}
	require.ElementsMatch(t, []model.SessionID{saved[0].ID, saved[1].ID, saved[2].ID}, paged, "pages cover every Session once")
	require.Len(t, paged, 3)

	active, err := ss.Session().ListByUser(ctx, &store.SessionPageQuery{UserID: user.ID, Limit: 10, ActiveAt: model.NowUTC().Add(time.Second)})
	require.NoError(t, err)
	require.Len(t, active, 2, "the active page omits the revoked Session")
	for _, session := range active {
		require.NotEqual(t, saved[1].ID, session.ID)
	}

	for _, invalid := range []*store.SessionPageQuery{
		nil, {UserID: user.ID}, {UserID: user.ID, Limit: store.MaximumSessionPageSize + 1},
		{UserID: user.ID, Limit: 1, BeforeID: saved[0].ID},
	} {
		_, err = ss.Session().ListByUser(ctx, invalid)
		require.ErrorAs(t, err, new(*store.ErrInvalidInput), "page %#v", invalid)
	}
}

// purgeRetiredUntil runs Session purge passes until gone reports true. Other
// fixtures may contribute retired Sessions, so it walks every pass.
func purgeRetiredUntil(t *testing.T, ctx context.Context, ss store.Store, gone func() bool) {
	t.Helper()
	for range 20 {
		_, err := ss.SessionCredential().PurgeRetired(ctx, &store.SessionCredentialPurge{Grace: time.Millisecond, Limit: store.MaximumSessionPurgeBatch})
		require.NoError(t, err)
		var after store.SessionPurgePosition
		for {
			pass, err := ss.Session().PurgeRetired(ctx, &store.SessionPurge{Grace: time.Millisecond, Limit: store.MaximumSessionPurgeBatch, After: after})
			require.NoError(t, err)
			if pass.Examined < store.MaximumSessionPurgeBatch {
				break
			}
			after = pass.Next
		}
		if gone() {
			return
		}
	}
	require.Fail(t, "retired rows were not purged")
}

func testSessionPurgeRetired(t *testing.T, ss store.Store) {
	ctx := context.Background()
	_, err := ss.Session().PurgeRetired(ctx, &store.SessionPurge{Grace: time.Hour})
	require.ErrorAs(t, err, new(*store.ErrInvalidInput), "purge without a limit")
	_, err = ss.SessionCredential().PurgeRetired(ctx, &store.SessionCredentialPurge{Limit: 1})
	require.ErrorAs(t, err, new(*store.ErrInvalidInput), "purge without a grace")

	user := saveUser(t, ctx, ss)
	retired, _, retiredRaw := saveSession(t, ctx, ss, user.ID.String(), 10)
	live, _, liveRaw := saveSession(t, ctx, ss, user.ID.String(), 10)
	_, err = ss.Session().Withdraw(ctx, retired.ID, user.ID, model.GetMillis(), model.SessionRevocationInactiveUser)
	require.NoError(t, err)

	// Within the grace nothing is deleted.
	_, err = ss.SessionCredential().PurgeRetired(ctx, &store.SessionCredentialPurge{Grace: time.Hour, Limit: store.MaximumSessionPurgeBatch})
	require.NoError(t, err)
	_, err = ss.Session().PurgeRetired(ctx, &store.SessionPurge{Grace: time.Hour, Limit: store.MaximumSessionPurgeBatch})
	require.NoError(t, err)
	_, err = ss.Session().Get(ctx, retired.ID)
	require.NoError(t, err, "a Session inside its grace is kept")

	// A rotation revokes the live Session's first access credential and
	// marks its refresh credential used.
	now := live.CreatedAt.Add(time.Millisecond)
	_, err = ss.SessionCredential().RotateRefresh(ctx, model.HashToken(liveRaw.refresh),
		&model.SessionCredential{TokenHash: model.HashToken(model.NewCredentialToken()), ExpiresAt: now.Add(15 * time.Minute)},
		&model.SessionCredential{TokenHash: model.HashToken(model.NewCredentialToken()), ExpiresAt: live.ExpiresAt},
		now, now.Add(time.Hour))
	require.NoError(t, err)

	time.Sleep(20 * time.Millisecond)
	purgeRetiredUntil(t, ctx, ss, func() bool {
		_, getErr := ss.Session().Get(ctx, retired.ID)
		return store.IsNotFound(getErr)
	})
	_, _, err = ss.SessionCredential().GetSessionByTokenHash(ctx, model.HashToken(retiredRaw.access), model.SessionCredentialAccess)
	require.True(t, store.IsNotFound(err), "a purged Session's credentials are gone: %v", err)
	_, _, err = ss.SessionCredential().GetSessionByTokenHash(ctx, model.HashToken(liveRaw.access), model.SessionCredentialAccess)
	require.True(t, store.IsNotFound(err), "a superseded, revoked access credential is purged: %v", err)
	_, err = ss.Session().Get(ctx, live.ID)
	require.NoError(t, err, "a live Session is kept")

	// The used refresh credential is unexpired, so replay is still detected.
	replayAt := now.Add(time.Second)
	replay, err := ss.SessionCredential().RotateRefresh(ctx, model.HashToken(liveRaw.refresh),
		&model.SessionCredential{TokenHash: model.HashToken(model.NewCredentialToken()), ExpiresAt: replayAt.Add(15 * time.Minute)},
		&model.SessionCredential{TokenHash: model.HashToken(model.NewCredentialToken()), ExpiresAt: live.ExpiresAt},
		replayAt, replayAt.Add(time.Hour))
	require.NoError(t, err)
	require.True(t, replay.ReplayDetected)
}

func testSessionPurgeRetiredCredentialLineage(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	session, credentials, raw := newSession(user.ID.String())
	credentials[1].ExpiresAt = model.NowUTC().Add(300 * time.Millisecond)
	saved, _, err := SaveSession(ctx, ss, testSessionCreation(t, ctx, ss, session, credentials, 10))
	require.NoError(t, err)
	now := saved.CreatedAt.Add(time.Millisecond)
	nextRefresh := model.NewCredentialToken()
	rotation, err := ss.SessionCredential().RotateRefresh(ctx, model.HashToken(raw.refresh),
		&model.SessionCredential{TokenHash: model.HashToken(model.NewCredentialToken()), ExpiresAt: now.Add(15 * time.Minute)},
		&model.SessionCredential{TokenHash: model.HashToken(nextRefresh), ExpiresAt: saved.ExpiresAt},
		now, now.Add(time.Hour))
	require.NoError(t, err)
	require.False(t, rotation.RefreshCredential.ParentID.IsZero())

	time.Sleep(350 * time.Millisecond)
	purgeRetiredUntil(t, ctx, ss, func() bool {
		_, _, getErr := ss.SessionCredential().GetSessionByTokenHash(ctx, model.HashToken(raw.refresh), model.SessionCredentialRefresh)
		return store.IsNotFound(getErr)
	})
	current, _, err := ss.SessionCredential().GetSessionByTokenHash(ctx, model.HashToken(nextRefresh), model.SessionCredentialRefresh)
	require.NoError(t, err, "the surviving child of a purged parent stays usable")
	require.True(t, current.ParentID.IsZero(), "the purged parent is detached from its child")
}

func testSelfSessionRevocationsWithAudit(t *testing.T, ss store.Store) {
	ctx := context.Background()
	before, err := ss.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{TemplateKeys: []model.MailTemplateKey{model.MailTemplateIdentitySessionsRevokedByAdmin}, Limit: 200})
	require.NoError(t, err)
	for _, reason := range []model.SessionRevocationReason{model.SessionRevocationUserSession, model.SessionRevocationUserLogout, model.SessionRevocationUserAllSessions} {
		t.Run(string(reason), func(t *testing.T) {
			user := saveUser(t, ctx, ss)
			session, _, raw := saveSession(t, ctx, ss, user.ID.String(), 10)
			at := model.GetMillis() + 100
			revoke := func(auditID model.AuditEventID) (*store.SessionRevocationResult, bool, error) {
				if reason == model.SessionRevocationUserAllSessions {
					input := &store.UserSessionsRevocation{UserID: user.ID, RevokedAt: at, Reason: reason, AuditEventID: auditID, AuditAt: at}
					result, revokeErr := ss.Session().RevokeAllForUser(ctx, input)
					if revokeErr != nil {
						return nil, false, revokeErr
					}
					value := &store.SessionRevocationResult{TokenHashes: result.TokenHashes}
					if len(result.Sessions) > 0 {
						value.Session = result.Sessions[0]
					}
					return value, input.NoOp, nil
				}
				result, revokeErr := ss.Session().Revoke(ctx, &store.SessionRevocation{
					SessionID: session.ID, UserID: user.ID, RevokedAt: at,
					Reason: reason, AuditEventID: auditID, AuditAt: at,
				})
				return result, result != nil && result.Session == nil, revokeErr
			}
			_, _, err = revoke(model.NewAuditEventID())
			require.Error(t, err, "self revocation committed without its audit attempt")
			credential, active, err := ss.SessionCredential().GetSessionByTokenHash(ctx, model.HashToken(raw.access), model.SessionCredentialAccess)
			require.NoError(t, err)
			require.False(t, credential.RevokedAt.Valid, "audit failure left session or credential revoked")
			require.False(t, active.RevokedAt.Valid, "audit failure left session or credential revoked")
			attempt := saveSessionAuditAttempt(t, ctx, ss, user.ID.String())
			result, noop, err := revoke(attempt.ID)
			require.NoError(t, err)
			require.False(t, noop, "self revocation = %#v, no-op", result)
			require.NotNil(t, result.Session, "self revocation = %#v, no-op", result)
			require.Equal(t, at, result.Session.RevokedAt.Millis(), "self revocation = %#v, no-op", result)
			require.Len(t, result.TokenHashes, 2, "self revocation = %#v, no-op", result)
			audit, err := ss.Audit().Get(ctx, attempt.ID)
			require.NoError(t, err)
			require.Equal(t, model.AuditStatusSuccess, audit.Status)
			credential, revoked, err := ss.SessionCredential().GetSessionByTokenHash(ctx, model.HashToken(raw.access), model.SessionCredentialAccess)
			require.NoError(t, err)
			require.Equal(t, at, credential.RevokedAt.Millis(), "audit succeeded without atomic credential-family revocation")
			require.Equal(t, at, revoked.RevokedAt.Millis(), "audit succeeded without atomic credential-family revocation")

			repeatAttempt := saveSessionAuditAttempt(t, ctx, ss, user.ID.String())
			repeat, noop, err := revoke(repeatAttempt.ID)
			if reason == model.SessionRevocationUserSession {
				require.True(t, store.IsNotFound(err), "repeated single self revocation")
				return
			}
			require.NoError(t, err)
			require.True(t, noop, "repeated self revocation = %#v, no-op", repeat)
			require.Nil(t, repeat.Session, "repeated self revocation = %#v, no-op", repeat)
			require.Empty(t, repeat.TokenHashes, "repeated self revocation = %#v, no-op", repeat)
			repeatAudit, err := ss.Audit().Get(ctx, repeatAttempt.ID)
			require.NoError(t, err)
			require.Equal(t, model.AuditStatusSuccess, repeatAudit.Status)
			_, _, err = revoke(model.NewAuditEventID())
			require.Error(t, err, "self revocation no-op skipped required audit")
		})
	}
	after, err := ss.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{TemplateKeys: []model.MailTemplateKey{model.MailTemplateIdentitySessionsRevokedByAdmin}, Limit: 200})
	require.NoError(t, err)
	require.Len(t, after, len(before), "self revocation created administrator notices: before")
}

func testSessionPasswordProof(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	proof := testPasswordProof(t, ctx, ss, user.ID)
	for _, test := range []struct {
		name  string
		proof store.PasswordCredentialProof
	}{
		{name: "missing"},
		{name: "credential", proof: store.PasswordCredentialProof{ID: model.NewPasswordCredentialID(), Revision: proof.Revision}},
		{name: "revision", proof: store.PasswordCredentialProof{ID: proof.ID, Revision: proof.Revision + 1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			session, credentials, _ := newSession(user.ID.String())
			_, _, err := SaveSession(ctx, ss, &store.SessionCreation{
				Session: session, Credentials: credentials, MaximumActive: 10, PasswordProof: test.proof,
			})
			require.ErrorIs(t, err, store.ErrPasswordCredentialChanged)
		})
	}
	remaining, err := ss.Session().ListByUser(ctx, everySession(user.ID))
	require.NoError(t, err)
	require.Empty(t, remaining, "failed password proof created")
	credential, err := ss.PasswordCredential().GetByUser(ctx, user.ID)
	require.NoError(t, err)
	require.NoError(t, ss.PasswordCredential().Rehash(ctx, &store.PasswordCredentialRehash{
		ID: credential.ID, UserID: user.ID, ExpectedRevision: credential.Revision,
		ExpectedHash: credential.PasswordHash, PasswordHash: "encoded-rehashed-session-password",
	}))

	session, credentials, _ := newSession(user.ID.String())
	_, _, err = SaveSession(ctx, ss, &store.SessionCreation{
		Session: session, Credentials: credentials, MaximumActive: 10, PasswordProof: proof,
	})
	require.NoError(t, err)
}

func testSessionEnforceExpiry(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	active, _, _ := saveSession(t, ctx, ss, user.ID.String(), 10)
	for _, invalidAt := range []time.Time{{}, time.Unix(0, 0), time.Unix(-1, 0)} {
		_, err := ss.Session().EnforceExpiry(ctx, active.ID, user.ID, invalidAt)
		var invalid *store.ErrInvalidInput
		require.ErrorAs(t, err, &invalid, "EnforceExpiry(%v) error", invalidAt)
	}
	beforeExpiry, err := ss.Session().EnforceExpiry(
		ctx,
		active.ID,
		user.ID,
		active.IdleExpiresAt.Add(-time.Millisecond),
	)
	require.NoError(t, err)
	require.False(t, beforeExpiry.Expired)
	require.NotNil(t, beforeExpiry.Session)
	require.Equal(t, active.ID, beforeExpiry.Session.ID)
	require.False(t, beforeExpiry.Session.RevokedAt.Valid)
	require.Empty(t, beforeExpiry.TokenHashes)

	expired, _, raw := saveSession(t, ctx, ss, user.ID.String(), 10)
	at := expired.IdleExpiresAt.Add(time.Second)
	result, err := ss.Session().EnforceExpiry(
		ctx,
		expired.ID,
		user.ID,
		at,
	)
	require.NoError(t, err)
	require.True(t, result.Expired)
	require.NotNil(t, result.Session)
	require.True(t, result.Session.RevokedAt.Time.Equal(at))
	require.Equal(t, model.SessionRevocationExpired, result.Session.RevocationReason)
	require.Len(t, result.TokenHashes, 2)
	for _, token := range []struct {
		raw  string
		kind model.SessionCredentialKind
	}{{raw.access, model.SessionCredentialAccess}, {raw.refresh, model.SessionCredentialRefresh}} {
		credential, resolved, resolveErr := ss.SessionCredential().GetSessionByTokenHash(
			ctx, model.HashToken(token.raw), token.kind,
		)
		require.NoError(t, resolveErr)
		require.True(t, credential.RevokedAt.Time.Equal(at), "expired credential/session = %#v / %#v", credential, resolved)
		require.True(t, resolved.RevokedAt.Time.Equal(at), "expired credential/session = %#v / %#v", credential, resolved)
	}
	_, err = ss.Session().EnforceExpiry(ctx, expired.ID, user.ID, at.Add(time.Microsecond))
	require.True(t, store.IsNotFound(err), "repeated EnforceExpiry() error")
}

func testSessionSaveResolveAndList(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	session, credentials, raw := saveSession(t, ctx, ss, user.ID.String(), 10)
	require.Len(t, credentials, 2, "Save() session=%#v credentials", session)
	require.Equal(t, user.ID, session.UserID, "Save() session=%#v credentials", session)
	credential, resolved, err := ss.SessionCredential().GetSessionByTokenHash(
		ctx,
		model.HashToken(raw.access),
		model.SessionCredentialAccess,
	)
	require.NoError(t, err)
	require.Equal(t, session.ID, credential.SessionID, "resolved credential=%#v session=%#v", credential, resolved)
	require.Equal(t, session.ID, resolved.ID, "resolved credential=%#v session=%#v", credential, resolved)
	list, err := ss.Session().ListByUser(ctx, everySession(user.ID))
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, session.ID, list[0].ID)
	active, err := ss.Session().ListActiveByUser(ctx, user.ID, session.CreatedAt)
	require.NoError(t, err)
	require.Len(t, active, 1)
	require.Equal(t, session.ID, active[0].ID)
	active, err = ss.Session().ListActiveByUser(ctx, user.ID, session.ExpiresAt)
	require.NoError(t, err)
	require.Empty(t, active)
}

func testSessionMaximumActive(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	saveSession(t, ctx, ss, user.ID.String(), 1)
	session, credentials, _ := newSession(user.ID.String())
	_, _, err := SaveSession(ctx, ss, testSessionCreation(t, ctx, ss, session, credentials, 1))
	var conflict *store.ErrConflict
	require.ErrorAs(t, err, &conflict)
	require.Equal(t, "sessions_maximum_per_user", conflict.Constraint)
}

func testSessionUpdateActivity(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	session, _, _ := saveSession(t, ctx, ss, user.ID.String(), 10)
	at := session.LastActivityAt.Add(time.Second)
	idle := at.Add(time.Hour)
	require.NoError(t, ss.Session().UpdateActivity(ctx, session.ID, at, idle))
	got, err := ss.Session().Get(ctx, session.ID)
	require.NoError(t, err)
	require.True(t, got.LastActivityAt.Equal(at))
	require.True(t, got.IdleExpiresAt.Equal(idle))
}

func testSessionRevoke(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	before, err := ss.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{Limit: 200})
	require.NoError(t, err)
	session, _, raw := saveSession(t, ctx, ss, user.ID.String(), 10)
	at := model.GetMillis() + 100
	other := saveUser(t, ctx, ss)
	_, err = ss.Session().Withdraw(ctx, session.ID, user.ID, at, model.SessionRevocationUserLogout)
	require.ErrorAs(t, err, new(*store.ErrInvalidInput))
	_, err = ss.Session().Withdraw(ctx, session.ID, other.ID, at, model.SessionRevocationInactiveUser)
	require.True(t, store.IsNotFound(err), "cross-user Revoke() error")
	unrevoked, err := ss.Session().Get(ctx, session.ID)
	require.NoError(t, err)
	require.False(t, unrevoked.RevokedAt.Valid)
	hashes, err := ss.Session().Withdraw(ctx, session.ID, user.ID, at, model.SessionRevocationInactiveUser)
	require.NoError(t, err)
	require.Len(t, hashes, 2)
	credential, got, err := ss.SessionCredential().GetSessionByTokenHash(
		ctx,
		model.HashToken(raw.access),
		model.SessionCredentialAccess,
	)
	require.NoError(t, err)
	require.Equal(t, at, credential.RevokedAt.Millis(), "revoked credential=%#v session=%#v", credential, got)
	require.Equal(t, at, got.RevokedAt.Millis(), "revoked credential=%#v session=%#v", credential, got)
	require.Equal(t, model.SessionRevocationInactiveUser, got.RevocationReason, "revoked credential=%#v session=%#v", credential, got)
	active, err := ss.Session().ListActiveByUser(ctx, user.ID, model.TimeFromMillis(at))
	require.NoError(t, err)
	require.Empty(t, active)
	after, err := ss.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{Limit: 200})
	require.NoError(t, err)
	require.Len(t, after, len(before), "ordinary self/logout revocation created mail: before")
}

func testSessionRevokeWithAudit(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	session, _, raw := saveSession(t, ctx, ss, user.ID.String(), 10)
	at := model.GetMillis() + 100

	missingAuditCommand := sessionRevocationWithNotice(t, &store.SessionRevocation{
		SessionID: session.ID, UserID: user.ID, RevokedAt: at,
		Reason: model.SessionRevocationAdministratorSession, AuditEventID: model.NewAuditEventID(), AuditAt: at,
	})
	_, err := ss.Session().Revoke(ctx, missingAuditCommand)
	require.Error(t, err, "Revoke() succeeded without its audit attempt")
	_, getErr := ss.Mail().GetDelivery(ctx, missingAuditCommand.Delivery.ID)
	require.True(t, store.IsNotFound(getErr), "administrative Session notice survived audit rollback")
	unrevoked, err := ss.Session().Get(ctx, session.ID)
	require.NoError(t, err)
	require.False(t, unrevoked.RevokedAt.Valid)
	credential, _, err := ss.SessionCredential().GetSessionByTokenHash(
		ctx, model.HashToken(raw.access), model.SessionCredentialAccess,
	)
	require.NoError(t, err)
	require.False(t, credential.RevokedAt.Valid)

	attempt := saveSessionAuditAttempt(t, ctx, ss, user.ID.String())
	command := sessionRevocationWithNotice(t, &store.SessionRevocation{
		SessionID: session.ID, UserID: user.ID, RevokedAt: at,
		Reason: model.SessionRevocationAdministratorSession, AuditEventID: attempt.ID, AuditAt: at,
	})
	result, err := ss.Session().Revoke(ctx, command)
	require.NoError(t, err)
	require.Equal(t, at, result.Session.RevokedAt.Millis())
	require.Len(t, result.TokenHashes, 2)
	delivery, getErr := ss.Mail().GetDelivery(ctx, command.Delivery.ID)
	require.NoError(t, getErr, "administrative revoke notice = %#v", delivery)
	require.Equal(t, model.MailTemplateIdentitySessionsRevokedByAdmin, delivery.TemplateKey, "administrative revoke notice = %#v", delivery)
	audit, err := ss.Audit().Get(ctx, attempt.ID)
	require.NoError(t, err)
	require.Equal(t, model.AuditStatusSuccess, audit.Status)
	revokedCredential, got, err := ss.SessionCredential().GetSessionByTokenHash(
		ctx, model.HashToken(raw.access), model.SessionCredentialAccess,
	)
	require.NoError(t, err)
	require.Equal(t, at, revokedCredential.RevokedAt.Millis(), "revoked credential=%#v session=%#v", revokedCredential, got)
	require.Equal(t, at, got.RevokedAt.Millis(), "revoked credential=%#v session=%#v", revokedCredential, got)
}

func testSessionRevokeAllForUser(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	first, _, _ := saveSession(t, ctx, ss, user.ID.String(), 10)
	second, _, _ := saveSession(t, ctx, ss, user.ID.String(), 10)
	other := saveUser(t, ctx, ss)
	otherSession, _, _ := saveSession(t, ctx, ss, other.ID.String(), 10)
	at := model.GetMillis() + 100
	revocation, err := RevokeAllUserSessions(ctx, ss, user.ID, at)
	require.NoError(t, err)
	require.Len(t, revocation.TokenHashes, 4)
	require.Len(t, revocation.Sessions, 2)
	for _, id := range []string{first.ID.String(), second.ID.String()} {
		got, getErr := ss.Session().Get(ctx, model.SessionID(id))
		require.NoError(t, getErr)
		require.Equal(t, at, got.RevokedAt.Millis(), "session %s not revoked: %#v", id, got)
	}
	gotOther, err := ss.Session().Get(ctx, otherSession.ID)
	require.NoError(t, err)
	require.False(t, gotOther.RevokedAt.Valid)
}

func testSessionRevokeAllForUserWithAudit(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	first, _, firstRaw := saveSession(t, ctx, ss, user.ID.String(), 10)
	second, _, _ := saveSession(t, ctx, ss, user.ID.String(), 10)
	at := model.GetMillis() + 100

	_, err := ss.Session().RevokeAllForUser(ctx, userSessionsRevocationWithNotice(t, &store.UserSessionsRevocation{
		UserID: user.ID, RevokedAt: at, Reason: model.SessionRevocationAdministratorAllSessions,
		AuditEventID: model.NewAuditEventID(), AuditAt: at,
	}))
	require.Error(t, err, "RevokeAllForUser() succeeded without its audit attempt")
	for _, id := range []string{first.ID.String(), second.ID.String()} {
		got, getErr := ss.Session().Get(ctx, model.SessionID(id))
		require.NoError(t, getErr)
		require.False(t, got.RevokedAt.Valid, "session %s survived audit rollback: %#v", id, got)
	}
	credential, _, err := ss.SessionCredential().GetSessionByTokenHash(
		ctx, model.HashToken(firstRaw.access), model.SessionCredentialAccess,
	)
	require.NoError(t, err)
	require.False(t, credential.RevokedAt.Valid)

	attempt := saveSessionAuditAttempt(t, ctx, ss, user.ID.String())
	command := userSessionsRevocationWithNotice(t, &store.UserSessionsRevocation{
		UserID: user.ID, RevokedAt: at, Reason: model.SessionRevocationAdministratorAllSessions,
		AuditEventID: attempt.ID, AuditAt: at,
	})
	result, err := ss.Session().RevokeAllForUser(ctx, command)
	require.NoError(t, err)
	require.Len(t, result.Sessions, 2)
	require.Len(t, result.TokenHashes, 4)
	delivery, getErr := ss.Mail().GetDelivery(ctx, command.Delivery.ID)
	require.NoError(t, getErr, "administrative revoke-all notice = %#v", delivery)
	require.Equal(t, model.MailTemplateIdentitySessionsRevokedByAdmin, delivery.TemplateKey, "administrative revoke-all notice = %#v", delivery)
	audit, err := ss.Audit().Get(ctx, attempt.ID)
	require.NoError(t, err)
	require.Equal(t, model.AuditStatusSuccess, audit.Status)
	replayAttempt := saveSessionAuditAttempt(t, ctx, ss, user.ID.String())
	replay := userSessionsRevocationWithNotice(t, &store.UserSessionsRevocation{
		UserID: user.ID, RevokedAt: at + 1, Reason: model.SessionRevocationAdministratorAllSessions,
		AuditEventID: replayAttempt.ID, AuditAt: at + 1,
	})
	replayed, replayErr := ss.Session().RevokeAllForUser(ctx, replay)
	require.NoError(t, replayErr)
	require.Empty(t, replayed.Sessions)
	_, getErr = ss.Mail().GetDelivery(ctx, replay.Delivery.ID)
	require.True(t, store.IsNotFound(getErr), "replayed revoke-all persisted duplicate notice")
}

func testSessionDisabledMailRecordsTerminalAdministrativeNotice(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	session, _, _ := saveSession(t, ctx, ss, user.ID.String(), 10)
	at := model.GetMillis() + 100
	attempt := saveSessionAuditAttempt(t, ctx, ss, user.ID.String())
	command := sessionRevocationWithNotice(t, &store.SessionRevocation{
		SessionID: session.ID, UserID: user.ID, RevokedAt: at,
		Reason: model.SessionRevocationAdministratorSession, AuditEventID: attempt.ID, AuditAt: at,
	})
	command.Delivery, command.DeliveryJob = suppressSecurityNoticeForDisabledMail(t, command.Delivery, command.DeliveryJob)
	_, err := ss.Session().Revoke(ctx, command)
	require.NoError(t, err)
	delivery, err := ss.Mail().GetDelivery(ctx, command.Delivery.ID)
	require.NoError(t, err)
	require.Equal(t, model.MailDeliverySuppressed, delivery.State)
	require.Equal(t, model.MailDeliveryDisabledCode, delivery.PublicFailureCode)
	require.Empty(t, delivery.EncryptedPayload)
}

func saveSessionAuditAttempt(t *testing.T, ctx context.Context, ss store.Store, userID string) *model.AuditEvent {
	t.Helper()
	attempt, err := ss.Audit().Save(ctx, &model.AuditEvent{
		Action:    string(model.ActionSessionManage),
		Resource:  model.Resource{Type: model.ResourceUser, ID: userID},
		ScopeType: model.RoleScopeInstitution, ScopeID: model.NewID(),
		Status: model.AuditStatusAttempt, NodeID: "test-node",
	})
	require.NoError(t, err)
	return attempt
}

func testSessionRotateAndDetectReplay(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	session, _, raw := saveSession(t, ctx, ss, user.ID.String(), 10)
	now := session.CreatedAt.Add(time.Second)
	newAccessRaw := model.NewCredentialToken()
	newRefreshRaw := model.NewCredentialToken()
	rotation, err := ss.SessionCredential().RotateRefresh(
		ctx,
		model.HashToken(raw.refresh),
		&model.SessionCredential{
			TokenHash: model.HashToken(newAccessRaw),
			ExpiresAt: now.Add(15 * time.Minute),
		},
		&model.SessionCredential{
			TokenHash: model.HashToken(newRefreshRaw),
			ExpiresAt: session.ExpiresAt,
		},
		now,
		now.Add(24*time.Hour),
	)
	require.NoError(t, err)
	require.False(t, rotation.ReplayDetected)
	require.Equal(t, model.SessionCredentialAccess, rotation.AccessCredential.Kind)
	require.False(t, rotation.RefreshCredential.ParentID.IsZero())
	require.Len(t, rotation.RevokedAccessHashes, 1)
	oldAccess, _, err := ss.SessionCredential().GetSessionByTokenHash(
		ctx,
		model.HashToken(raw.access),
		model.SessionCredentialAccess,
	)
	require.NoError(t, err)
	require.True(t, oldAccess.RevokedAt.Time.Equal(now))

	replayAt := now.Add(100 * time.Millisecond)
	replay, err := ss.SessionCredential().RotateRefresh(
		ctx,
		model.HashToken(raw.refresh),
		&model.SessionCredential{
			TokenHash: model.HashToken(model.NewCredentialToken()),
			ExpiresAt: replayAt.Add(15 * time.Minute),
		},
		&model.SessionCredential{
			TokenHash: model.HashToken(model.NewCredentialToken()),
			ExpiresAt: session.ExpiresAt,
		},
		replayAt,
		session.ExpiresAt,
	)
	require.NoError(t, err)
	require.True(t, replay.ReplayDetected)
	require.True(t, replay.Session.RevokedAt.Time.Equal(replayAt))
	newAccess, replayedSession, err := ss.SessionCredential().GetSessionByTokenHash(
		ctx,
		model.HashToken(newAccessRaw),
		model.SessionCredentialAccess,
	)
	require.NoError(t, err)
	require.True(t, newAccess.RevokedAt.Time.Equal(replayAt), "replay did not revoke family: credential=%#v session=%#v", newAccess, replayedSession)
	require.True(t, replayedSession.RevokedAt.Time.Equal(replayAt), "replay did not revoke family: credential=%#v session=%#v", newAccess, replayedSession)
}

func testSessionRotateEnforcesSessionExpiry(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	session, _, raw := saveSession(t, ctx, ss, user.ID.String(), 10)
	at := session.IdleExpiresAt.Add(time.Second)
	rotation, err := ss.SessionCredential().RotateRefresh(
		ctx,
		model.HashToken(raw.refresh),
		&model.SessionCredential{
			TokenHash: model.HashToken(model.NewCredentialToken()),
			ExpiresAt: at.Add(15 * time.Minute),
		},
		&model.SessionCredential{
			TokenHash: model.HashToken(model.NewCredentialToken()),
			ExpiresAt: session.ExpiresAt,
		},
		at,
		session.ExpiresAt,
	)
	require.NoError(t, err)
	require.True(t, rotation.Expired)
	require.False(t, rotation.ReplayDetected)
	require.NotNil(t, rotation.Session)
	require.True(t, rotation.Session.RevokedAt.Time.Equal(at))
	require.Equal(t, model.SessionRevocationExpired, rotation.Session.RevocationReason)
	require.Nil(t, rotation.AccessCredential)
	require.Nil(t, rotation.RefreshCredential)
	require.Len(t, rotation.RevokedAccessHashes, 2)
}

func testSessionConcurrentRefreshReplay(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	session, _, raw := saveSession(t, ctx, ss, user.ID.String(), 10)
	now := session.CreatedAt.Add(time.Second)

	type result struct {
		rotation *store.SessionRotation
		err      error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for range 2 {
		go func() {
			<-start
			rotation, err := ss.SessionCredential().RotateRefresh(
				ctx,
				model.HashToken(raw.refresh),
				&model.SessionCredential{
					TokenHash: model.HashToken(model.NewCredentialToken()),
					ExpiresAt: now.Add(15 * time.Minute),
				},
				&model.SessionCredential{
					TokenHash: model.HashToken(model.NewCredentialToken()),
					ExpiresAt: session.ExpiresAt,
				},
				now,
				now.Add(24*time.Hour),
			)
			results <- result{rotation: rotation, err: err}
		}()
	}
	close(start)

	successes := 0
	replays := 0
	for range 2 {
		outcome := <-results
		require.NoError(t, outcome.err)
		if outcome.rotation.ReplayDetected {
			replays++
		} else {
			successes++
		}
	}
	require.Equal(t, 1, successes, "concurrent rotations: successes")
	require.Equal(t, 1, replays, "concurrent rotations: successes")
	got, err := ss.Session().Get(ctx, session.ID)
	require.NoError(t, err)
	require.True(t, got.RevokedAt.Valid)
	require.Equal(t, model.SessionRevocationRefreshReplay, got.RevocationReason)
}

func testSessionConcurrentRefreshAndRevokeAll(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	session, _, raw := saveSession(t, ctx, ss, user.ID.String(), 10)
	rotateAt := session.CreatedAt.Add(time.Second)
	revokeAt := rotateAt.Add(100 * time.Millisecond).UnixMilli()
	newAccessRaw := model.NewCredentialToken()
	start := make(chan struct{})
	rotationResult := make(chan error, 1)
	revocationResult := make(chan error, 1)

	go func() {
		<-start
		_, err := ss.SessionCredential().RotateRefresh(
			ctx,
			model.HashToken(raw.refresh),
			&model.SessionCredential{
				TokenHash: model.HashToken(newAccessRaw),
				ExpiresAt: rotateAt.Add(15 * time.Minute),
			},
			&model.SessionCredential{
				TokenHash: model.HashToken(model.NewCredentialToken()),
				ExpiresAt: session.ExpiresAt,
			},
			rotateAt,
			rotateAt.Add(24*time.Hour),
		)
		rotationResult <- err
	}()
	go func() {
		<-start
		_, err := RevokeAllUserSessions(ctx, ss, user.ID, revokeAt)
		revocationResult <- err
	}()
	close(start)

	rotationErr := <-rotationResult
	if rotationErr != nil {
		var conflict *store.ErrConflict
		require.ErrorAs(t, rotationErr, &conflict)
	}
	require.NoError(t, <-revocationResult)

	got, err := ss.Session().Get(ctx, session.ID)
	require.NoError(t, err)
	require.Equal(t, revokeAt, got.RevokedAt.Millis())
	require.Equal(t, model.SessionRevocationUserAllSessions, got.RevocationReason)
	active, err := ss.Session().ListActiveByUser(ctx, user.ID, rotateAt)
	require.NoError(t, err)
	require.Empty(t, active)
	if rotationErr == nil {
		credential, _, err := ss.SessionCredential().GetSessionByTokenHash(
			ctx,
			model.HashToken(newAccessRaw),
			model.SessionCredentialAccess,
		)
		require.NoError(t, err)
		require.Equal(t, revokeAt, credential.RevokedAt.Millis())
	}
}

type rawSessionCredentials struct {
	access  string
	refresh string
}

func newSession(userID string) (*model.Session, []*model.SessionCredential, rawSessionCredentials) {
	now := model.NowUTC()
	absolute := now.Add(30 * 24 * time.Hour)
	session := &model.Session{
		UserID:                 model.UserID(userID),
		ClientType:             model.SessionClientWeb,
		DeviceID:               "test-device",
		DeviceName:             "Test Device",
		AuthenticationMethod:   "password",
		AuthenticationStrength: model.AuthenticationSingleFactor,
		AuthenticatedAt:        now,
		LastActivityAt:         now,
		IdleExpiresAt:          now.Add(7 * 24 * time.Hour),
		ExpiresAt:              absolute,
	}
	raw := rawSessionCredentials{
		access:  model.NewCredentialToken(),
		refresh: model.NewCredentialToken(),
	}
	credentials := []*model.SessionCredential{
		{
			Kind:      model.SessionCredentialAccess,
			TokenHash: model.HashToken(raw.access),
			ExpiresAt: now.Add(15 * time.Minute),
		},
		{
			Kind:      model.SessionCredentialRefresh,
			TokenHash: model.HashToken(raw.refresh),
			ExpiresAt: absolute,
		},
	}
	return session, credentials, raw
}

func saveSession(
	t *testing.T,
	ctx context.Context,
	ss store.Store,
	userID string,
	maximum int,
) (*model.Session, []*model.SessionCredential, rawSessionCredentials) {
	t.Helper()
	session, credentials, raw := newSession(userID)
	savedSession, savedCredentials, err := SaveSession(ctx, ss, testSessionCreation(t, ctx, ss, session, credentials, maximum))
	require.NoError(t, err)
	return savedSession, savedCredentials, raw
}

func testSessionCreation(t *testing.T, ctx context.Context, ss store.Store, session *model.Session,
	credentials []*model.SessionCredential, maximum int,
) *store.SessionCreation {
	t.Helper()
	input := &store.SessionCreation{Session: session, Credentials: credentials, MaximumActive: maximum}
	if session.AuthenticationMethod == "password" {
		input.PasswordProof = testPasswordProof(t, ctx, ss, session.UserID)
	}
	return input
}

func testPasswordProof(t *testing.T, ctx context.Context, ss store.Store, userID model.UserID) store.PasswordCredentialProof {
	t.Helper()
	credential, err := ss.PasswordCredential().GetByUser(ctx, userID)
	if store.IsNotFound(err) {
		credential, err = ss.PasswordCredential().Save(ctx, &model.PasswordCredential{
			UserID: userID, PasswordHash: "encoded-session-fixture-password",
		})
	}
	require.NoError(t, err)
	return store.PasswordCredentialProof{ID: credential.ID, Revision: credential.Revision}
}

// everySession pages all of one fixture User's Sessions at once.
func everySession(userID model.UserID) *store.SessionPageQuery {
	return &store.SessionPageQuery{UserID: userID, Limit: store.MaximumSessionPageSize}
}
