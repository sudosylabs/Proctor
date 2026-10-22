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

func (authenticationSessionStore) ReauthenticatePassword(context.Context, *store.SessionPasswordReauthentication) (*store.SessionReauthenticationResult, error) {
	return nil, errors.New("reauthentication is not configured by this test")
}

type reauthenticationSessionStoreFake struct {
	store.SessionStore
	input  *store.SessionPasswordReauthentication
	result *store.SessionReauthenticationResult
	err    error
	events *[]string
}

func (s *reauthenticationSessionStoreFake) ReauthenticatePassword(_ context.Context, input *store.SessionPasswordReauthentication) (*store.SessionReauthenticationResult, error) {
	s.input = input
	*s.events = append(*s.events, "commit")
	return s.result, s.err
}

func TestPasswordReauthenticationBindsCurrentIdentityAndRequiresCommit(t *testing.T) {
	for _, test := range []struct {
		name     string
		password string
		method   string
		storeErr error
		auditErr error
		code     string
		commit   bool
	}{
		{name: "success", password: "CorrectHorseBatteryStaple1!", method: "password", commit: true},
		{name: "wrong password", password: "IncorrectHorseBatteryStaple1!", method: "password", code: "authentication.credentials_invalid"},
		{name: "external Session", password: "CorrectHorseBatteryStaple1!", method: "oidc", code: "authentication.reauthentication_method_required"},
		{name: "password changed", password: "CorrectHorseBatteryStaple1!", method: "password", storeErr: store.ErrPasswordCredentialChanged, code: "authentication.credentials_invalid", commit: true},
		{name: "Session revoked", password: "CorrectHorseBatteryStaple1!", method: "password", storeErr: store.NewErrNotFound("session", ""), code: "authentication.credentials_invalid", commit: true},
		{name: "audit unavailable", password: "CorrectHorseBatteryStaple1!", method: "password", auditErr: apperror.New("audit.unavailable"), code: "audit.unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			persistence := newAuthenticationStoreFake()
			service := newTestAuthenticationService(t, persistence)
			user, err := service.CreateLocalUser(ctx, CreateLocalUserCommand{User: &model.User{Username: "reauthentication", Email: "reauthentication@example.edu"}, Password: "CorrectHorseBatteryStaple1!"})
			require.NoError(t, err)
			now := time.Now().UTC()
			principal := model.Principal{UserID: user.ID, SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewSessionCredentialID()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: test.method, AuthenticationStrength: model.AuthenticationMultiFactor, ClientType: model.SessionClientWeb, AuthenticatedAt: now.Add(-time.Hour), MFACompletedAt: model.OptionalTimeFrom(now.Add(-30 * time.Minute))}
			if test.method != "password" {
				principal.AuthenticationProviderID = "campus"
				principal.ExternalIdentityID = model.NewExternalIdentityID()
			}
			session := &model.Session{ID: principal.SessionID, UserID: user.ID, AuthenticatedAt: principal.AuthenticatedAt, ReauthenticatedAt: model.OptionalTimeFrom(now), MFACompletedAt: principal.MFACompletedAt}
			events := []string{}
			fake := &reauthenticationSessionStoreFake{events: &events, result: &store.SessionReauthenticationResult{Session: session}, err: test.storeErr}
			service.deps.Sessions = fake
			service.deps.Audit = &mutationAttemptAuditorFake{events: &events, beginID: model.NewID(), beginErr: test.auditErr}
			mfa := &authenticationMFAVerifierFake{}
			service.deps.MFA = mfa
			actual, err := service.ReauthenticatePassword(ctx, invocation.New(principal, model.RequestMetadata{}), ReauthenticatePasswordCommand{Password: test.password, Source: "192.0.2.45"})
			if test.code != "" {
				require.True(t, apperror.Is(err, test.code), "result=%v err=%v", actual, err)
				require.Nil(t, actual, "result=%v err=%v", actual, err)
			} else {
				require.NoError(t, err)
				require.Same(t, session, actual)
			}
			require.Equal(t, test.commit, fake.input != nil, "unexpected commit: %v", events)
			if fake.input != nil {
				credential := persistence.passwords[user.ID.String()]
				require.Equal(t, user.ID, fake.input.UserID)
				require.Equal(t, principal.SessionID, fake.input.SessionID)
				require.Equal(t, principal.CredentialID.String(), fake.input.CredentialID.String())
				require.Equal(t, credential.ID, fake.input.PasswordProof.ID)
				require.Equal(t, credential.Revision, fake.input.PasswordProof.Revision)
				require.True(t, fake.input.AuditEventID.IsValid())
			}
			require.True(t, session.AuthenticatedAt.Equal(principal.AuthenticatedAt), "primary refresh changed original authentication or MFA history")
			require.Equal(t, principal.MFACompletedAt, session.MFACompletedAt, "primary refresh changed original authentication or MFA history")
		})
	}
}

func (authenticationSessionStore) ReauthenticateExternal(context.Context, *store.SessionExternalReauthentication) (*store.SessionReauthenticationResult, error) {
	return nil, errors.New("external reauthentication is not configured by this test")
}
