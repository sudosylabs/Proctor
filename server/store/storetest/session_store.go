// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/channels/store/storetest/session_store.go.
// These tests additionally verify Proctor's split credential model, atomic
// access/refresh creation, refresh rotation, and replay revocation.

package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestSessionStores(t *testing.T, ss store.Store) {
	t.Run("SaveResolveAndList", func(t *testing.T) { testSessionSaveResolveAndList(t, ss) })
	t.Run("MaximumActive", func(t *testing.T) { testSessionMaximumActive(t, ss) })
	t.Run("UpdateActivity", func(t *testing.T) { testSessionUpdateActivity(t, ss) })
	t.Run("Revoke", func(t *testing.T) { testSessionRevoke(t, ss) })
	t.Run("RevokeWithAudit", func(t *testing.T) { testSessionRevokeWithAudit(t, ss) })
	t.Run("RevokeAllForUser", func(t *testing.T) { testSessionRevokeAllForUser(t, ss) })
	t.Run("RevokeAllForUserWithAudit", func(t *testing.T) { testSessionRevokeAllForUserWithAudit(t, ss) })
	t.Run("DisabledMailRecordsTerminalAdministrativeNotice", func(t *testing.T) {
		testSessionDisabledMailRecordsTerminalAdministrativeNotice(t, ss)
	})
	t.Run("RotateAndDetectReplay", func(t *testing.T) { testSessionRotateAndDetectReplay(t, ss) })
	t.Run("ConcurrentRefreshReplay", func(t *testing.T) { testSessionConcurrentRefreshReplay(t, ss) })
	t.Run("ConcurrentRefreshAndRevokeAll", func(t *testing.T) {
		testSessionConcurrentRefreshAndRevokeAll(t, ss)
	})
}

func testSessionSaveResolveAndList(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	session, credentials, raw := saveSession(t, ctx, ss, user.ID.String(), 10)
	if len(credentials) != 2 || session.UserID != user.ID {
		t.Fatalf("Save() session=%#v credentials=%#v", session, credentials)
	}
	credential, resolved, err := ss.SessionCredential().GetSessionByTokenHash(
		ctx,
		model.HashToken(raw.access),
		model.SessionCredentialAccess,
	)
	requireNoError(t, err)
	if credential.SessionID != session.ID || resolved.ID != session.ID {
		t.Fatalf("resolved credential=%#v session=%#v", credential, resolved)
	}
	list, err := ss.Session().ListByUser(ctx, user.ID.String())
	requireNoError(t, err)
	if len(list) != 1 || list[0].ID != session.ID {
		t.Fatalf("ListByUser() = %#v", list)
	}
	active, err := ss.Session().ListActiveByUser(ctx, user.ID.String(), model.MillisFromTime(session.CreatedAt))
	requireNoError(t, err)
	if len(active) != 1 || active[0].ID != session.ID {
		t.Fatalf("ListActiveByUser(active) = %#v", active)
	}
	active, err = ss.Session().ListActiveByUser(ctx, user.ID.String(), model.MillisFromTime(session.ExpiresAt))
	requireNoError(t, err)
	if len(active) != 0 {
		t.Fatalf("ListActiveByUser(expired) = %#v", active)
	}
}

func testSessionMaximumActive(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	saveSession(t, ctx, ss, user.ID.String(), 1)
	session, credentials, _ := newSession(user.ID.String())
	_, _, err := ss.Session().Save(ctx, session, credentials, 1)
	var conflict *store.ErrConflict
	if !errors.As(err, &conflict) || conflict.Constraint != "sessions_maximum_per_user" {
		t.Fatalf("second active session error = %v", err)
	}
}

func testSessionUpdateActivity(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	session, _, _ := saveSession(t, ctx, ss, user.ID.String(), 10)
	at := model.MillisFromTime(session.LastActivityAt) + 1_000
	idle := at + int64(time.Hour/time.Millisecond)
	requireNoError(t, ss.Session().UpdateActivity(ctx, session.ID.String(), at, idle))
	got, err := ss.Session().Get(ctx, session.ID.String())
	requireNoError(t, err)
	if model.MillisFromTime(got.LastActivityAt) != at || model.MillisFromTime(got.IdleExpiresAt) != idle {
		t.Fatalf("UpdateActivity() session = %#v", got)
	}
}

func testSessionRevoke(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	before, err := ss.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{Limit: 200})
	requireNoError(t, err)
	session, _, raw := saveSession(t, ctx, ss, user.ID.String(), 10)
	at := model.GetMillis() + 100
	other := saveUser(t, ctx, ss)
	_, err = ss.Session().Revoke(ctx, session.ID.String(), other.ID.String(), at, model.SessionRevocationUserLogout)
	if !store.IsNotFound(err) {
		t.Fatalf("cross-user Revoke() error = %v", err)
	}
	unrevoked, err := ss.Session().Get(ctx, session.ID.String())
	requireNoError(t, err)
	if unrevoked.RevokedAt.Valid {
		t.Fatalf("cross-user Revoke() changed session = %#v", unrevoked)
	}
	hashes, err := ss.Session().Revoke(ctx, session.ID.String(), user.ID.String(), at, model.SessionRevocationUserLogout)
	requireNoError(t, err)
	if len(hashes) != 2 {
		t.Fatalf("Revoke() hashes = %#v", hashes)
	}
	credential, got, err := ss.SessionCredential().GetSessionByTokenHash(
		ctx,
		model.HashToken(raw.access),
		model.SessionCredentialAccess,
	)
	requireNoError(t, err)
	if credential.RevokedAt.Millis() != at || got.RevokedAt.Millis() != at || got.RevocationReason != model.SessionRevocationUserLogout {
		t.Fatalf("revoked credential=%#v session=%#v", credential, got)
	}
	active, err := ss.Session().ListActiveByUser(ctx, user.ID.String(), at)
	requireNoError(t, err)
	if len(active) != 0 {
		t.Fatalf("revoked session remained active: %#v", active)
	}
	after, err := ss.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{Limit: 200})
	requireNoError(t, err)
	if len(after) != len(before) {
		t.Fatalf("ordinary self/logout revocation created mail: before=%d after=%d", len(before), len(after))
	}
}

func testSessionRevokeWithAudit(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	session, _, raw := saveSession(t, ctx, ss, user.ID.String(), 10)
	at := model.GetMillis() + 100

	missingAuditCommand := sessionRevocationWithNotice(t, &store.SessionRevocation{
		SessionID: session.ID.String(), UserID: user.ID.String(), RevokedAt: at,
		Reason: model.SessionRevocationAdministratorSession, AuditEventID: model.NewId(), AuditAt: at,
	})
	if _, err := ss.Session().RevokeWithAudit(ctx, missingAuditCommand); err == nil {
		t.Fatal("RevokeWithAudit() succeeded without its audit attempt")
	}
	if _, getErr := ss.Mail().GetDelivery(ctx, missingAuditCommand.Delivery.ID); !store.IsNotFound(getErr) {
		t.Fatalf("administrative Session notice survived audit rollback: %v", getErr)
	}
	unrevoked, err := ss.Session().Get(ctx, session.ID.String())
	requireNoError(t, err)
	if unrevoked.RevokedAt.Valid {
		t.Fatalf("session survived audit rollback: %#v", unrevoked)
	}
	credential, _, err := ss.SessionCredential().GetSessionByTokenHash(
		ctx, model.HashToken(raw.access), model.SessionCredentialAccess,
	)
	requireNoError(t, err)
	if credential.RevokedAt.Valid {
		t.Fatalf("credential survived audit rollback: %#v", credential)
	}

	attempt := saveSessionAuditAttempt(t, ctx, ss, user.ID.String())
	command := sessionRevocationWithNotice(t, &store.SessionRevocation{
		SessionID: session.ID.String(), UserID: user.ID.String(), RevokedAt: at,
		Reason: model.SessionRevocationAdministratorSession, AuditEventID: attempt.ID.String(), AuditAt: at,
	})
	result, err := ss.Session().RevokeWithAudit(ctx, command)
	requireNoError(t, err)
	if result.Session.RevokedAt.Millis() != at || len(result.TokenHashes) != 2 {
		t.Fatalf("RevokeWithAudit() = %#v", result)
	}
	if delivery, getErr := ss.Mail().GetDelivery(ctx, command.Delivery.ID); getErr != nil || delivery.TemplateKey != model.MailTemplateIdentitySessionsRevokedByAdmin {
		t.Fatalf("administrative revoke notice = %#v, %v", delivery, getErr)
	}
	audit, err := ss.Audit().Get(ctx, attempt.ID.String())
	requireNoError(t, err)
	if audit.Status != model.AuditStatusSuccess {
		t.Fatalf("audit status = %#v", audit)
	}
	revokedCredential, got, err := ss.SessionCredential().GetSessionByTokenHash(
		ctx, model.HashToken(raw.access), model.SessionCredentialAccess,
	)
	requireNoError(t, err)
	if revokedCredential.RevokedAt.Millis() != at || got.RevokedAt.Millis() != at {
		t.Fatalf("revoked credential=%#v session=%#v", revokedCredential, got)
	}
}

func testSessionRevokeAllForUser(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	first, _, _ := saveSession(t, ctx, ss, user.ID.String(), 10)
	second, _, _ := saveSession(t, ctx, ss, user.ID.String(), 10)
	other := saveUser(t, ctx, ss)
	otherSession, _, _ := saveSession(t, ctx, ss, other.ID.String(), 10)
	at := model.GetMillis() + 100
	revoked, hashes, err := ss.Session().RevokeAllForUser(ctx, user.ID.String(), at, model.SessionRevocationUserAllSessions)
	requireNoError(t, err)
	if len(hashes) != 4 {
		t.Fatalf("RevokeAllForUser() hashes = %#v", hashes)
	}
	if len(revoked) != 2 {
		t.Fatalf("RevokeAllForUser() sessions = %#v", revoked)
	}
	for _, id := range []string{first.ID.String(), second.ID.String()} {
		got, getErr := ss.Session().Get(ctx, id)
		requireNoError(t, getErr)
		if got.RevokedAt.Millis() != at {
			t.Fatalf("session %s not revoked: %#v", id, got)
		}
	}
	gotOther, err := ss.Session().Get(ctx, otherSession.ID.String())
	requireNoError(t, err)
	if gotOther.RevokedAt.Valid {
		t.Fatalf("other user's session was revoked: %#v", gotOther)
	}
}

func testSessionRevokeAllForUserWithAudit(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	first, _, firstRaw := saveSession(t, ctx, ss, user.ID.String(), 10)
	second, _, _ := saveSession(t, ctx, ss, user.ID.String(), 10)
	at := model.GetMillis() + 100

	if _, err := ss.Session().RevokeAllForUserWithAudit(ctx, userSessionsRevocationWithNotice(t, &store.UserSessionsRevocation{
		UserID: user.ID.String(), RevokedAt: at, Reason: model.SessionRevocationAdministratorAllSessions,
		AuditEventID: model.NewId(), AuditAt: at,
	})); err == nil {
		t.Fatal("RevokeAllForUserWithAudit() succeeded without its audit attempt")
	}
	for _, id := range []string{first.ID.String(), second.ID.String()} {
		got, getErr := ss.Session().Get(ctx, id)
		requireNoError(t, getErr)
		if got.RevokedAt.Valid {
			t.Fatalf("session %s survived audit rollback: %#v", id, got)
		}
	}
	credential, _, err := ss.SessionCredential().GetSessionByTokenHash(
		ctx, model.HashToken(firstRaw.access), model.SessionCredentialAccess,
	)
	requireNoError(t, err)
	if credential.RevokedAt.Valid {
		t.Fatalf("credential survived audit rollback: %#v", credential)
	}

	attempt := saveSessionAuditAttempt(t, ctx, ss, user.ID.String())
	command := userSessionsRevocationWithNotice(t, &store.UserSessionsRevocation{
		UserID: user.ID.String(), RevokedAt: at, Reason: model.SessionRevocationAdministratorAllSessions,
		AuditEventID: attempt.ID.String(), AuditAt: at,
	})
	result, err := ss.Session().RevokeAllForUserWithAudit(ctx, command)
	requireNoError(t, err)
	if len(result.Sessions) != 2 || len(result.TokenHashes) != 4 {
		t.Fatalf("RevokeAllForUserWithAudit() = %#v", result)
	}
	if delivery, getErr := ss.Mail().GetDelivery(ctx, command.Delivery.ID); getErr != nil || delivery.TemplateKey != model.MailTemplateIdentitySessionsRevokedByAdmin {
		t.Fatalf("administrative revoke-all notice = %#v, %v", delivery, getErr)
	}
	audit, err := ss.Audit().Get(ctx, attempt.ID.String())
	requireNoError(t, err)
	if audit.Status != model.AuditStatusSuccess {
		t.Fatalf("audit status = %#v", audit)
	}
	replayAttempt := saveSessionAuditAttempt(t, ctx, ss, user.ID.String())
	replay := userSessionsRevocationWithNotice(t, &store.UserSessionsRevocation{
		UserID: user.ID.String(), RevokedAt: at + 1, Reason: model.SessionRevocationAdministratorAllSessions,
		AuditEventID: replayAttempt.ID.String(), AuditAt: at + 1,
	})
	replayed, replayErr := ss.Session().RevokeAllForUserWithAudit(ctx, replay)
	requireNoError(t, replayErr)
	if len(replayed.Sessions) != 0 {
		t.Fatalf("replayed revoke-all result = %#v", replayed)
	}
	if _, getErr := ss.Mail().GetDelivery(ctx, replay.Delivery.ID); !store.IsNotFound(getErr) {
		t.Fatalf("replayed revoke-all persisted duplicate notice: %v", getErr)
	}
}

func testSessionDisabledMailRecordsTerminalAdministrativeNotice(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	session, _, _ := saveSession(t, ctx, ss, user.ID.String(), 10)
	at := model.GetMillis() + 100
	attempt := saveSessionAuditAttempt(t, ctx, ss, user.ID.String())
	command := sessionRevocationWithNotice(t, &store.SessionRevocation{
		SessionID: session.ID.String(), UserID: user.ID.String(), RevokedAt: at,
		Reason: model.SessionRevocationAdministratorSession, AuditEventID: attempt.ID.String(), AuditAt: at,
	})
	command.Delivery, command.DeliveryJob = suppressSecurityNoticeForDisabledMail(t, command.Delivery, command.DeliveryJob)
	if _, err := ss.Session().RevokeWithAudit(ctx, command); err != nil {
		t.Fatal(err)
	}
	delivery, err := ss.Mail().GetDelivery(ctx, command.Delivery.ID)
	requireNoError(t, err)
	if delivery.State != model.MailDeliverySuppressed || delivery.PublicFailureCode != model.MailDeliveryDisabledCode || len(delivery.EncryptedPayload) != 0 {
		t.Fatalf("disabled-mail administrative Session notice = %#v", delivery)
	}
}

func saveSessionAuditAttempt(t *testing.T, ctx context.Context, ss store.Store, userID string) *model.AuditEvent {
	t.Helper()
	attempt, err := ss.Audit().Save(ctx, &model.AuditEvent{
		Action:    string(model.ActionSessionManage),
		Resource:  model.Resource{Type: model.ResourceUser, ID: userID},
		ScopeType: model.RoleScopeInstitution, ScopeID: model.NewId(),
		Status: model.AuditStatusAttempt, NodeID: "test-node",
	})
	requireNoError(t, err)
	return attempt
}

func testSessionRotateAndDetectReplay(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	session, _, raw := saveSession(t, ctx, ss, user.ID.String(), 10)
	now := model.MillisFromTime(session.CreatedAt) + 1_000
	newAccessRaw := model.NewCredentialToken()
	newRefreshRaw := model.NewCredentialToken()
	rotation, err := ss.SessionCredential().RotateRefresh(
		ctx,
		model.HashToken(raw.refresh),
		&model.SessionCredential{
			TokenHash: model.HashToken(newAccessRaw),
			ExpiresAt: model.TimeFromMillis(now + int64((15*time.Minute)/time.Millisecond)),
		},
		&model.SessionCredential{
			TokenHash: model.HashToken(newRefreshRaw),
			ExpiresAt: session.ExpiresAt,
		},
		now,
		min(now+int64((24*time.Hour)/time.Millisecond), model.MillisFromTime(session.ExpiresAt)),
	)
	requireNoError(t, err)
	if rotation.ReplayDetected ||
		rotation.AccessCredential.Kind != model.SessionCredentialAccess ||
		rotation.RefreshCredential.ParentID.IsZero() ||
		len(rotation.RevokedAccessHashes) != 1 {
		t.Fatalf("RotateRefresh() = %#v", rotation)
	}
	oldAccess, _, err := ss.SessionCredential().GetSessionByTokenHash(
		ctx,
		model.HashToken(raw.access),
		model.SessionCredentialAccess,
	)
	requireNoError(t, err)
	if oldAccess.RevokedAt.Millis() != now {
		t.Fatalf("old access credential = %#v", oldAccess)
	}

	replayAt := now + 100
	replay, err := ss.SessionCredential().RotateRefresh(
		ctx,
		model.HashToken(raw.refresh),
		&model.SessionCredential{
			TokenHash: model.HashToken(model.NewCredentialToken()),
			ExpiresAt: model.TimeFromMillis(replayAt + int64((15*time.Minute)/time.Millisecond)),
		},
		&model.SessionCredential{
			TokenHash: model.HashToken(model.NewCredentialToken()),
			ExpiresAt: session.ExpiresAt,
		},
		replayAt,
		model.MillisFromTime(session.ExpiresAt),
	)
	requireNoError(t, err)
	if !replay.ReplayDetected || replay.Session.RevokedAt.Millis() != replayAt {
		t.Fatalf("replay rotation = %#v", replay)
	}
	newAccess, replayedSession, err := ss.SessionCredential().GetSessionByTokenHash(
		ctx,
		model.HashToken(newAccessRaw),
		model.SessionCredentialAccess,
	)
	requireNoError(t, err)
	if newAccess.RevokedAt.Millis() != replayAt || replayedSession.RevokedAt.Millis() != replayAt {
		t.Fatalf("replay did not revoke family: credential=%#v session=%#v", newAccess, replayedSession)
	}
}

func testSessionConcurrentRefreshReplay(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	session, _, raw := saveSession(t, ctx, ss, user.ID.String(), 10)
	now := model.MillisFromTime(session.CreatedAt) + 1_000

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
					ExpiresAt: model.TimeFromMillis(now + int64((15*time.Minute)/time.Millisecond)),
				},
				&model.SessionCredential{
					TokenHash: model.HashToken(model.NewCredentialToken()),
					ExpiresAt: session.ExpiresAt,
				},
				now,
				min(now+int64((24*time.Hour)/time.Millisecond), model.MillisFromTime(session.ExpiresAt)),
			)
			results <- result{rotation: rotation, err: err}
		}()
	}
	close(start)

	successes := 0
	replays := 0
	for range 2 {
		outcome := <-results
		requireNoError(t, outcome.err)
		if outcome.rotation.ReplayDetected {
			replays++
		} else {
			successes++
		}
	}
	if successes != 1 || replays != 1 {
		t.Fatalf("concurrent rotations: successes=%d replays=%d", successes, replays)
	}
	got, err := ss.Session().Get(ctx, session.ID.String())
	requireNoError(t, err)
	if !got.RevokedAt.Valid || got.RevocationReason != model.SessionRevocationRefreshReplay {
		t.Fatalf("concurrent replay did not revoke the session: %#v", got)
	}
}

func testSessionConcurrentRefreshAndRevokeAll(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	session, _, raw := saveSession(t, ctx, ss, user.ID.String(), 10)
	rotateAt := model.MillisFromTime(session.CreatedAt) + 1_000
	revokeAt := rotateAt + 100
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
				ExpiresAt: model.TimeFromMillis(rotateAt + int64((15*time.Minute)/time.Millisecond)),
			},
			&model.SessionCredential{
				TokenHash: model.HashToken(model.NewCredentialToken()),
				ExpiresAt: session.ExpiresAt,
			},
			rotateAt,
			min(rotateAt+int64((24*time.Hour)/time.Millisecond), model.MillisFromTime(session.ExpiresAt)),
		)
		rotationResult <- err
	}()
	go func() {
		<-start
		_, _, err := ss.Session().RevokeAllForUser(
			ctx,
			user.ID.String(),
			revokeAt,
			model.SessionRevocationUserAllSessions,
		)
		revocationResult <- err
	}()
	close(start)

	rotationErr := <-rotationResult
	if rotationErr != nil {
		var conflict *store.ErrConflict
		if !errors.As(rotationErr, &conflict) {
			t.Fatalf("RotateRefresh() error = %v", rotationErr)
		}
	}
	requireNoError(t, <-revocationResult)

	got, err := ss.Session().Get(ctx, session.ID.String())
	requireNoError(t, err)
	if got.RevokedAt.Millis() != revokeAt || got.RevocationReason != model.SessionRevocationUserAllSessions {
		t.Fatalf("session after concurrent revocation = %#v", got)
	}
	active, err := ss.Session().ListActiveByUser(ctx, user.ID.String(), rotateAt)
	requireNoError(t, err)
	if len(active) != 0 {
		t.Fatalf("sessions remained active after revoke-all: %#v", active)
	}
	if rotationErr == nil {
		credential, _, err := ss.SessionCredential().GetSessionByTokenHash(
			ctx,
			model.HashToken(newAccessRaw),
			model.SessionCredentialAccess,
		)
		requireNoError(t, err)
		if credential.RevokedAt.Millis() != revokeAt {
			t.Fatalf("rotated access credential was not revoked: %#v", credential)
		}
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
		ClientType:             model.SessionClientDesktop,
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
	savedSession, savedCredentials, err := ss.Session().Save(ctx, session, credentials, maximum)
	requireNoError(t, err)
	return savedSession, savedCredentials, raw
}
