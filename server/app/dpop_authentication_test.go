// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

type dpopRecoveryVerifier struct {
	discardAuthenticationMFAVerifier
	generation int64
}

func (v *dpopRecoveryVerifier) RecoveryState(_ context.Context, id model.UserID) (*model.UserMFARecovery, error) {
	return &model.UserMFARecovery{UserID: id, Generation: v.generation}, nil
}

type dpopRegistrationLookup struct{ registration *model.DesktopRegistration }

func (s dpopRegistrationLookup) Get(context.Context, string) (*model.DesktopRegistration, error) {
	return s.registration, nil
}

func TestDPoPAuthenticationAfterMFARecoverySupportsPrincipalRevalidation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	at := model.NowUTC()
	privateKey, jwk := testDPoPKey(t)
	user := &model.User{ID: model.NewUserID()}
	session, registration := testDesktopAuthorizationExchangeResult(t, ExchangeDesktopAuthorizationCommand{
		PublicJWK: jwk, DesktopRelease: "0.1.0", DesktopBuildID: "recovered-build",
		Platform: model.DesktopPlatformDarwin, Architecture: model.DesktopArchitectureARM64, RealtimeProtocol: 1,
	}, user.ID, at)
	session.AuthenticationGeneration = 2
	session.AuthenticationStrength = model.AuthenticationMultiFactor
	session.MFACompletedAt = model.OptionalTimeFrom(at)
	raw := model.NewCredentialToken()
	credential := &model.SessionCredential{ID: model.NewSessionCredentialID(), SessionID: session.ID,
		Kind: model.SessionCredentialAccess, TokenHash: model.HashToken(raw), ExpiresAt: at.Add(time.Hour)}
	persistence := newAuthenticationStoreFake()
	persistence.users[user.ID.String()] = user
	persistence.sessions[session.ID.String()] = session
	persistence.accessByHash[credential.TokenHash] = credential
	persistence.sessionByCredential[credential.TokenHash] = session
	recovery := &dpopRecoveryVerifier{generation: 2}
	service := newTestAuthenticationServiceWithPorts(t, persistence, newAuthenticationCacheFake(), recovery,
		discardAuthenticationPATResolver{}, model.NewCredentialToken, func() time.Time { return at })
	service.registrations = dpopRegistrationLookup{registration: registration}
	nonce, err := service.dpop.IssueNonce(ctx, dpopBinding{Kind: dpopBindingSession, SessionID: session.ID,
		DesktopRegistrationID: registration.ID, KeyThumbprint: registration.KeyThumbprint, Origin: service.dpop.policy.Origin})
	if err != nil {
		t.Fatal(err)
	}
	proof := signDPoPProof(t, privateKey, jwk, map[string]any{
		"jti": "recovered-session-proof", "htm": "GET", "htu": service.dpop.policy.Origin + "/api/v1/users/me",
		"iat": at.Unix(), "nonce": nonce, "ath": dpopAccessTokenHash(raw),
	})
	principal, err := service.authenticateDPoP(ctx, raw, proof, "GET", "/api/v1/users/me")
	if err != nil {
		t.Fatal(err)
	}
	if err = service.ValidatePrincipal(ctx, *principal); err != nil {
		t.Fatalf("fresh recovered Desktop Session rejected by realtime revalidation: %v", err)
	}
	recovery.generation++
	if err = service.ValidatePrincipal(ctx, *principal); !Is(err, "authentication.invalid_token") {
		t.Fatalf("a subsequent recovery did not invalidate the old Desktop principal: %v", err)
	}
	session.MFARecoveryRequired = true
	if principalFromDesktopAuthentication(&resolvedAuthentication{User: user, Session: session, Credential: credential}).Validate() == nil {
		t.Fatal("restricted Desktop Session acquired an ordinary principal")
	}
}
