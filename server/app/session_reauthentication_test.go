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

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func (authenticationSessionStore) ReauthenticatePasswordWithAudit(context.Context, *store.SessionPasswordReauthentication) (*store.SessionReauthenticationResult, error) {
	return nil, errors.New("reauthentication is not configured by this test")
}

type reauthenticationSessionStoreFake struct {
	store.SessionStore
	input  *store.SessionPasswordReauthentication
	result *store.SessionReauthenticationResult
	err    error
	events *[]string
}

func (s *reauthenticationSessionStoreFake) ReauthenticatePasswordWithAudit(_ context.Context, input *store.SessionPasswordReauthentication) (*store.SessionReauthenticationResult, error) {
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
		{name: "wrong password", password: "IncorrectHorseBatteryStaple1!", method: "password", code: "authentication.invalid_credentials"},
		{name: "external Session", password: "CorrectHorseBatteryStaple1!", method: "oidc", code: "authentication.reauthentication_method_required"},
		{name: "password changed", password: "CorrectHorseBatteryStaple1!", method: "password", storeErr: store.ErrPasswordCredentialChanged, code: "authentication.invalid_credentials", commit: true},
		{name: "Session revoked", password: "CorrectHorseBatteryStaple1!", method: "password", storeErr: store.NewErrNotFound("session", ""), code: "authentication.invalid_credentials", commit: true},
		{name: "audit unavailable", password: "CorrectHorseBatteryStaple1!", method: "password", auditErr: NewError("audit.unavailable"), code: "audit.unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			persistence := newAuthenticationStoreFake()
			service := newTestAuthenticationService(t, persistence)
			user, err := service.createLocalUser(ctx, CreateLocalUserCommand{User: &model.User{Username: "reauthentication", Email: "reauthentication@example.edu"}, Password: "CorrectHorseBatteryStaple1!"})
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			principal := model.Principal{UserID: user.ID, SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewSessionCredentialID()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: test.method, AuthenticationStrength: model.AuthenticationMultiFactor, ClientType: model.SessionClientWeb, AuthenticatedAt: now.Add(-time.Hour), MFACompletedAt: model.OptionalTimeFrom(now.Add(-30 * time.Minute))}
			if test.method != "password" {
				principal.AuthenticationProviderID = "campus"
				principal.ExternalIdentityID = model.NewExternalIdentityID()
			}
			session := &model.Session{ID: principal.SessionID, UserID: user.ID, AuthenticatedAt: principal.AuthenticatedAt, ReauthenticatedAt: model.OptionalTimeFrom(now), MFACompletedAt: principal.MFACompletedAt}
			events := []string{}
			fake := &reauthenticationSessionStoreFake{events: &events, result: &store.SessionReauthenticationResult{Session: session}, err: test.storeErr}
			service.sessions = fake
			service.audit = &mutationAttemptAuditorFake{events: &events, beginID: model.NewId(), beginErr: test.auditErr}
			mfa := &authenticationMFAVerifierFake{}
			service.mfa = mfa
			actual, err := service.ReauthenticatePassword(ctx, NewInvocation(principal, model.RequestMetadata{}), ReauthenticatePasswordCommand{Password: test.password, Source: "192.0.2.45"})
			if test.code != "" {
				if !Is(err, test.code) || actual != nil {
					t.Fatalf("result=%v err=%v", actual, err)
				}
			} else if err != nil || actual != session {
				t.Fatalf("result=%v err=%v", actual, err)
			}
			if (fake.input != nil) != test.commit {
				t.Fatalf("unexpected commit: %v", events)
			}
			if fake.input != nil {
				credential := persistence.passwords[user.ID.String()]
				if fake.input.UserID != user.ID || fake.input.SessionID != principal.SessionID || fake.input.CredentialID.String() != principal.CredentialID.String() || fake.input.PasswordProof.ID != credential.ID || fake.input.PasswordProof.Revision != credential.Revision || !fake.input.AuditEventID.IsValid() {
					t.Fatalf("unbound proof: %#v", fake.input)
				}
			}
			if !session.AuthenticatedAt.Equal(principal.AuthenticatedAt) || session.MFACompletedAt != principal.MFACompletedAt {
				t.Fatal("primary refresh changed original authentication or MFA history")
			}
		})
	}
}

func (authenticationSessionStore) ReauthenticateExternalWithAudit(context.Context, *store.SessionExternalReauthentication) (*store.SessionReauthenticationResult, error) {
	return nil, errors.New("external reauthentication is not configured by this test")
}
