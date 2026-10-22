// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestSelfSessionServiceUsesCallerOwnershipAndPostCommitEffects(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 12, 9, 30, 0, 0, time.UTC)
	principal := selfSessionPrincipal(now)
	session := &model.Session{ID: principal.SessionID, UserID: principal.UserID}
	events := []string{}
	persistence := &selfSessionStoreFake{
		events:  &events,
		session: session,
		hashes:  []string{"access-hash"},
	}
	effects := &selfSessionEffectsFake{events: &events}
	service, err := newSelfSessionService(selfSessionServiceDependencies{Sessions: persistence, Audit: &mutationAttemptAuditorFake{events: &events, beginID: model.NewID()}, Effects: effects, Now: func() time.Time { return now }})
	require.NoError(t, err)

	invocation := invocation.New(principal, model.RequestMetadata{})
	err = service.RevokeSession(
		context.Background(),
		invocation,
		RevokeSessionCommand{SessionID: session.ID},
	)
	require.NoError(t, err)
	// Contract (guardrails 8 and 14): the audit begins before the durable write,
	// and the write commits before its effect.
	require.Equal(t, []string{"get", "begin", "revoke", "effects"}, events)
	require.Equal(t, principal.UserID.String(), persistence.revokedUserID, "revocation = user")
	require.True(t, now.Equal(persistence.revokedAt), "revocation = user")
	require.Equal(t, principal.UserID, effects.userID)
	require.Equal(t, []model.SessionID{session.ID}, effects.sessionIDs)
	require.Equal(t, []string{"access-hash"}, effects.hashes)
}

func TestSelfSessionServiceHidesAnotherUsersSession(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 12, 9, 30, 0, 0, time.UTC)
	principal := selfSessionPrincipal(now)
	persistence := &selfSessionStoreFake{
		events:  &[]string{},
		session: &model.Session{ID: model.NewSessionID(), UserID: model.NewUserID()},
	}
	service, err := newSelfSessionService(selfSessionServiceDependencies{Sessions: persistence, Audit: &mutationAttemptAuditorFake{events: &[]string{}, beginID: model.NewID()}, Effects: &selfSessionEffectsFake{}, Now: func() time.Time { return now }})
	require.NoError(t, err)

	err = service.RevokeSession(
		context.Background(),
		invocation.New(principal, model.RequestMetadata{}),
		RevokeSessionCommand{SessionID: persistence.session.ID},
	)
	require.True(t, apperror.Is(err, "session.not_found"), "error = %v, want session.not_found", err)
	require.Equal(t, 0, persistence.revokeCalls, "revoke calls")
}

func TestSelfSessionListUsesNativeDecisionTime(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 12, 9, 30, 0, 123_456_789, time.FixedZone("offset", 7200))
	principal := selfSessionPrincipal(model.TimeUTC(at))
	persistence := &selfSessionStoreFake{session: &model.Session{ID: principal.SessionID, UserID: principal.UserID}}
	service, err := newSelfSessionService(selfSessionServiceDependencies{Sessions: persistence, Audit: &mutationAttemptAuditorFake{events: &[]string{}, beginID: model.NewID()}, Effects: &selfSessionEffectsFake{}, Now: func() time.Time { return at }})
	require.NoError(t, err)
	_, err = service.ListSessions(context.Background(), invocation.New(principal, model.RequestMetadata{}), ListSessionsQuery{})
	require.NoError(t, err)
	require.True(t, persistence.listedAt.Equal(model.TimeUTC(at)), "active Session listing time = %v", persistence.listedAt)
	require.Same(t, time.UTC, persistence.listedAt.Location(), "active Session listing time = %v", persistence.listedAt)
}

func TestSelfSessionServiceRequiresFocusedDependencies(t *testing.T) {
	t.Parallel()

	_, err := newSelfSessionService(selfSessionServiceDependencies{Sessions: &selfSessionStoreFake{}, Audit: nil, Effects: &selfSessionEffectsFake{}, Now: time.Now})
	require.Error(t, err, "nil self-session audit was accepted")
	_, err = newSelfSessionService(selfSessionServiceDependencies{Sessions: nil, Audit: &mutationAttemptAuditorFake{}, Effects: &selfSessionEffectsFake{}, Now: time.Now})
	require.Error(t, err, "nil self-session store was accepted")
	_, err = newSelfSessionService(selfSessionServiceDependencies{Sessions: &selfSessionStoreFake{}, Audit: &mutationAttemptAuditorFake{}, Effects: nil, Now: time.Now})
	require.Error(t, err, "nil self-session effects were accepted")
	_, err = newSelfSessionService(selfSessionServiceDependencies{Sessions: &selfSessionStoreFake{}, Audit: &mutationAttemptAuditorFake{}, Effects: &selfSessionEffectsFake{}, Now: nil})
	require.Error(t, err, "nil self-session clock was accepted")
}

func TestSelfSessionRevocationsRequireAuditBeforeMutationAndEffects(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"revoke_own_session", "revoke_own_sessions", "logout"} {
		for _, outcome := range []string{"success", "attempt unavailable", "completion unavailable"} {
			t.Run(operation+"/"+outcome, func(t *testing.T) {
				now := time.Date(2026, 8, 12, 9, 30, 0, 0, time.UTC)
				principal := selfSessionPrincipal(now)
				events := []string{}
				persistence := &selfSessionStoreFake{events: &events,
					session: &model.Session{ID: principal.SessionID, UserID: principal.UserID}, hashes: []string{"access-hash"}}
				audit := &mutationAttemptAuditorFake{events: &events, beginID: model.NewID()}
				effects := &selfSessionEffectsFake{events: &events}
				if outcome == "attempt unavailable" {
					audit.beginErr = apperror.New("audit.unavailable")
				}
				if outcome == "completion unavailable" {
					persistence.revokeErr = errors.New("audit completion unavailable")
				}
				self, err := newSelfSessionService(selfSessionServiceDependencies{Sessions: persistence, Audit: audit, Effects: effects, Now: func() time.Time { return now }})
				require.NoError(t, err)
				invocation := invocation.New(principal, model.RequestMetadata{})
				switch operation {
				case "revoke_own_session":
					err = self.RevokeSession(context.Background(), invocation, RevokeSessionCommand{SessionID: principal.SessionID})
				case "revoke_own_sessions":
					err = self.RevokeAllSessions(context.Background(), invocation, RevokeAllSessionsCommand{})
				case "logout":
					authentication := &authenticationService{deps: authenticationServiceDependencies{Sessions: persistence, Audit: audit, SecurityEffects: effects, Now: self.deps.Now}}
					err = authentication.Logout(context.Background(), invocation, LogoutCommand{})
				}
				if outcome != "success" {
					require.Error(t, err, "failed audit allowed revocation/effect: error=%v session=%#v effects=%#v", err, persistence.session, effects)
					require.False(t, persistence.session.RevokedAt.Valid, "failed audit allowed revocation/effect: error=%v session=%#v effects=%#v", err, persistence.session, effects)
					require.Empty(t, effects.userID, "failed audit allowed revocation/effect: error=%v session=%#v effects=%#v", err, persistence.session, effects)
					if outcome == "attempt unavailable" {
						require.Equal(t, 0, persistence.revokeCalls, "Store mutation ran before durable attempt")
					}
					if outcome == "completion unavailable" {
						require.Equal(t, audit.beginID, audit.failID, "mutation rollback was not recorded against its audit attempt")
					}
					return
				}
				require.NoError(t, err)
				require.True(t, persistence.session.RevokedAt.Valid, "revocation/effects = %#v / %#v", persistence.session, effects)
				require.Equal(t, principal.UserID, effects.userID, "revocation/effects = %#v / %#v", persistence.session, effects)
				// Contract (guardrails 8 and 14): the audit begins before the durable
				// revocation, which commits before its effects. Revoking one Session
				// first reads it to confirm the caller owns it.
				want := []string{"begin", "revoke", "effects"}
				if operation == "revoke_own_session" {
					want = append([]string{"get"}, want...)
				}
				require.Equal(t, want, events)
				require.Equal(t, operation, audit.attempt.Operation)
				require.Equal(t, principal.UserID.String(), audit.attempt.Resource.ID)
				require.Equal(t, model.ActionSessionManage, audit.attempt.Action)
				input := persistence.revocation
				if input != nil {
					require.Equal(t, model.AuditEventID(audit.beginID), input.AuditEventID)
					require.True(t, model.TimeUTC(now).Equal(input.AuditAt))
					require.Nil(t, input.Occurrence)
					require.Nil(t, input.Delivery)
					require.Nil(t, input.DeliveryJob)
				}
				if input := persistence.allRevocation; input != nil {
					require.Equal(t, model.AuditEventID(audit.beginID), input.AuditEventID)
					require.True(t, model.TimeUTC(now).Equal(input.AuditAt))
					require.Nil(t, input.Occurrence)
					require.Nil(t, input.Delivery)
					require.Nil(t, input.DeliveryJob)
				}
			})
		}
	}
}

func TestSelfSessionRevocationRepeatOutcomes(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"revoke_own_session", "revoke_own_sessions", "logout"} {
		t.Run(operation, func(t *testing.T) {
			now := time.Date(2026, 8, 12, 9, 30, 0, 0, time.UTC)
			principal := selfSessionPrincipal(now)
			events := []string{}
			persistence := &selfSessionStoreFake{events: &events, session: &model.Session{
				ID: principal.SessionID, UserID: principal.UserID, RevokedAt: model.OptionalTimeFrom(now.Add(-time.Minute)),
			}}
			audit := &mutationAttemptAuditorFake{events: &events, beginID: model.NewID()}
			effects := &selfSessionEffectsFake{events: &events}
			self, err := newSelfSessionService(selfSessionServiceDependencies{Sessions: persistence, Audit: audit, Effects: effects, Now: func() time.Time { return now }})
			require.NoError(t, err)
			invocation := invocation.New(principal, model.RequestMetadata{})
			switch operation {
			case "revoke_own_session":
				err = self.RevokeSession(context.Background(), invocation, RevokeSessionCommand{SessionID: principal.SessionID})
				require.True(t, apperror.Is(err, "session.not_found"), "repeat = %v audit=%#v", err, audit)
				require.Equal(t, "session.not_found", audit.failCode, "repeat = %v audit=%#v", err, audit)
			case "revoke_own_sessions":
				err = self.RevokeAllSessions(context.Background(), invocation, RevokeAllSessionsCommand{})
				require.NoError(t, err)
				require.True(t, persistence.allRevocation.NoOp)
			case "logout":
				authentication := &authenticationService{deps: authenticationServiceDependencies{Sessions: persistence, Audit: audit, SecurityEffects: effects, Now: self.deps.Now}}
				err = authentication.Logout(context.Background(), invocation, LogoutCommand{})
				require.NoError(t, err)
			}
			require.Equal(t, 1, persistence.revokeCalls)
			require.Empty(t, effects.userID)
		})
	}
}

func selfSessionPrincipal(at time.Time) model.Principal {
	return model.Principal{
		UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID:         model.PrincipalCredentialID(model.NewSessionCredentialID()),
		CredentialType:       model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: model.SessionClientWeb, AuthenticatedAt: at,
	}
}

type selfSessionStoreFake struct {
	store.SessionStore
	events        *[]string
	session       *model.Session
	hashes        []string
	revokedUserID string
	revokedAt     time.Time
	revokeCalls   int
	listedAt      time.Time
	revokeErr     error
	revocation    *store.SessionRevocation
	allRevocation *store.UserSessionsRevocation
}

func (s *selfSessionStoreFake) Get(context.Context, model.SessionID) (*model.Session, error) {
	*s.events = append(*s.events, "get")
	if s.session == nil {
		return nil, store.NewErrNotFound("session", "")
	}
	return s.session, nil
}

func (s *selfSessionStoreFake) ListActiveByUser(_ context.Context, _ model.UserID, at time.Time) ([]*model.Session, error) {
	s.listedAt = at
	return []*model.Session{s.session}, nil
}

func (s *selfSessionStoreFake) Revoke(_ context.Context, input *store.SessionRevocation) (*store.SessionRevocationResult, error) {
	*s.events = append(*s.events, "revoke")
	s.revokeCalls++
	s.revocation = input
	s.revokedUserID, s.revokedAt = input.UserID.String(), input.RevokedAt
	if s.revokeErr != nil {
		return nil, s.revokeErr
	}
	if s.session == nil || s.session.RevokedAt.Valid || s.session.UserID != input.UserID {
		if input.Reason == model.SessionRevocationUserLogout {
			return &store.SessionRevocationResult{}, nil
		}
		return nil, store.NewErrNotFound("session", input.SessionID.String())
	}
	s.session.RevokedAt = model.OptionalTimeFrom(input.RevokedAt)
	return &store.SessionRevocationResult{Session: s.session, TokenHashes: append([]string(nil), s.hashes...)}, nil
}

func (s *selfSessionStoreFake) RevokeAllForUser(_ context.Context, input *store.UserSessionsRevocation) (*store.UserSessionsRevocationResult, error) {
	*s.events = append(*s.events, "revoke")
	s.revokeCalls++
	s.allRevocation = input
	if s.revokeErr != nil {
		return nil, s.revokeErr
	}
	if s.session == nil || s.session.RevokedAt.Valid {
		input.NoOp = true
		return &store.UserSessionsRevocationResult{}, nil
	}
	s.session.RevokedAt = model.OptionalTimeFrom(input.RevokedAt)
	return &store.UserSessionsRevocationResult{Sessions: []*model.Session{s.session}, TokenHashes: append([]string(nil), s.hashes...)}, nil
}

type selfSessionEffectsFake struct {
	events     *[]string
	userID     model.UserID
	sessionIDs []model.SessionID
	hashes     []string
}

func (e *selfSessionEffectsFake) AuthenticationCacheInvalidated(context.Context, model.UserID, []string) {
}

func (e *selfSessionEffectsFake) SessionsRevoked(_ context.Context, userID model.UserID, sessionIDs []model.SessionID, hashes []string) {
	if e.events != nil {
		*e.events = append(*e.events, "effects")
	}
	e.userID = userID
	e.sessionIDs = append([]model.SessionID(nil), sessionIDs...)
	e.hashes = append([]string(nil), hashes...)
}
