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

	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type freshExternalProviderFake struct {
	providerConnectionProvider
	beginRequest    ExternalProviderBeginRequest
	completeRequest ExternalProviderCompleteRequest
}

func (p *freshExternalProviderFake) Begin(_ context.Context, request ExternalProviderBeginRequest) (*ExternalProviderBeginResponse, error) {
	p.beginRequest = request
	return &ExternalProviderBeginResponse{RedirectURL: "https://identity.example.test/login"}, nil
}

func (p *freshExternalProviderFake) Complete(_ context.Context, request ExternalProviderCompleteRequest) (*model.ExternalAuthenticationAssertion, error) {
	p.completeRequest = request
	return p.assertion, p.completeErr
}

type freshExternalSessionStoreFake struct {
	store.SessionStore
	input  *store.SessionExternalReauthentication
	result *store.SessionReauthenticationResult
	err    error
}

func (s *freshExternalSessionStoreFake) ReauthenticateExternal(_ context.Context, input *store.SessionExternalReauthentication) (*store.SessionReauthenticationResult, error) {
	s.input = input
	return s.result, s.err
}

func TestExternalReauthenticationBindsOriginalSessionAndClosedTask(t *testing.T) {
	for _, test := range []struct {
		task       string
		restricted bool
		want       string
	}{
		{task: "security", want: "/account/security"},
		{task: "connect-provider", want: "/account/connect-provider"},
		{task: "security", restricted: true, want: "/account/security"},
		{task: "connect-provider", restricted: true},
		{task: "https://elsewhere.example.test"},
	} {
		t.Run(test.task+"/"+test.want, func(t *testing.T) {
			now := model.NowUTC()
			principal := userSettingsSessionPrincipal(now)
			principal.AuthenticationMethod, principal.AuthenticationProviderID = "oidc", "campus"
			principal.ExternalIdentityID = model.NewExternalIdentityID()
			if test.restricted {
				principal.AuthenticationGeneration, principal.MFARecoveryRequired = 1, true
			}
			provider := &freshExternalProviderFake{}
			service := externalAuthenticationBeginService(t, externalProviderSourceFake{provider: provider}, newAuthenticationCacheFake(), 10)
			states := &externalLoginStateStoreFake{storeNow: now}
			events := []string{}
			audit := &mutationAttemptAuditorFake{events: &events, beginID: model.NewAuditEventID().String()}
			service.loginStates, service.mutationAudit = states, audit
			result, err := service.BeginExternalReauthentication(context.Background(), invocation.New(principal, model.RequestMetadata{}), BeginExternalReauthenticationCommand{Task: test.task, Source: "192.0.2.30"})
			if test.want == "" {
				require.Error(t, err, "unbounded continuation reached provider or Store")
				require.Nil(t, result, "unbounded continuation reached provider or Store")
				require.Nil(t, states.saved, "unbounded continuation reached provider or Store")
				require.False(t, provider.beginRequest.FreshAuthentication, "unbounded continuation reached provider or Store")
				return
			}
			require.NoError(t, err)
			bound := states.saved
			require.NotNil(t, result, "missing exact fresh-proof binding: %#v %#v", bound, provider.beginRequest)
			require.NotNil(t, bound, "missing exact fresh-proof binding: %#v %#v", bound, provider.beginRequest)
			require.True(t, provider.beginRequest.FreshAuthentication, "missing exact fresh-proof binding: %#v %#v", bound, provider.beginRequest)
			require.Equal(t, model.ExternalAuthenticationPurposeReauthenticate, bound.Purpose, "missing exact fresh-proof binding: %#v %#v", bound, provider.beginRequest)
			require.Equal(t, test.want, bound.ReturnTo, "missing exact fresh-proof binding: %#v %#v", bound, provider.beginRequest)
			require.Equal(t, principal.UserID, bound.TargetUserID, "missing exact fresh-proof binding: %#v %#v", bound, provider.beginRequest)
			require.Equal(t, principal.SessionID, bound.SessionID, "missing exact fresh-proof binding: %#v %#v", bound, provider.beginRequest)
			require.Equal(t, principal.CredentialID.String(), bound.SessionCredentialID.String(), "missing exact fresh-proof binding: %#v %#v", bound, provider.beginRequest)
			require.Equal(t, principal.ExternalIdentityID, bound.ExternalIdentityID, "missing exact fresh-proof binding: %#v %#v", bound, provider.beginRequest)
			require.Equal(t, audit.beginID, bound.AuditEventID, "missing exact fresh-proof binding: %#v %#v", bound, provider.beginRequest)
		})
	}
}

func TestExternalReauthenticationCallbackUsesBoundProofAndSafeFailureContinuation(t *testing.T) {
	for _, test := range []struct {
		name                   string
		offset                 time.Duration
		providerErr, commitErr error
		wantCommit             bool
		fail                   bool
	}{
		{name: "success", wantCommit: true},
		{name: "stale SSO", offset: -time.Minute, fail: true},
		{name: "future proof", offset: time.Minute, fail: true},
		{name: "provider rejected", providerErr: ErrExternalAuthenticationRejected, fail: true},
		{name: "target changed", commitErr: store.ErrAuthenticationGenerationChanged, wantCommit: true, fail: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := model.NowUTC()
			stateToken, binding := model.NewCredentialToken(), model.NewCredentialToken()
			state := &model.ExternalLoginState{Provider: "campus", Purpose: model.ExternalAuthenticationPurposeReauthenticate, TargetUserID: model.NewUserID(), SessionID: model.NewSessionID(), SessionCredentialID: model.NewSessionCredentialID(), ExternalIdentityID: model.NewExternalIdentityID(), AuditEventID: model.NewAuditEventID().String(), StateHash: model.HashToken(stateToken), BindingHash: model.HashToken(binding), ReturnTo: "/account/connect-provider", ClientType: model.SessionClientWeb, ExpiresAt: now.Add(time.Minute)}
			state.PrepareCreate(model.NewExternalLoginStateID(), now.Add(-time.Second))
			provider := &freshExternalProviderFake{providerConnectionProvider: providerConnectionProvider{state: stateToken, completeErr: test.providerErr, assertion: &model.ExternalAuthenticationAssertion{ProviderID: "campus", Subject: "private-subject", AuthenticatedAt: now.Add(test.offset).UnixMilli()}}}
			service := externalAuthenticationBeginService(t, externalProviderSourceFake{provider: provider}, newAuthenticationCacheFake(), 10)
			service.now = func() time.Time { return now }
			states := &externalLoginStateStoreFake{get: state, consumeResult: state, storeNow: now}
			institution, err := model.NewInstitution(model.NewInstitutionID(), "fresh-proof", "Fresh Proof", "", now)
			require.NoError(t, err)
			events := []string{}
			auditor := &mutationAttemptAuditorFake{events: &events}
			committed := &model.Session{ID: state.SessionID, UserID: state.TargetUserID}
			sessions := &freshExternalSessionStoreFake{result: &store.SessionReauthenticationResult{Session: committed}, err: test.commitErr}
			service.loginStates, service.sessions = states, sessions
			service.institutions, service.audit, service.mutationAudit, service.invalidator = providerConnectionInstitutionStoreFake{institution: institution}, externalAuditFake{}, auditor, externalInvalidatorFake{}
			result, err := service.complete(context.Background(), "campus", binding, model.ExternalAuthenticationCallback{}, model.RequestMetadata{})
			require.NoError(t, err, "completion")
			require.NotNil(t, result, "completion")
			require.Nil(t, result.Tokens, "completion")
			require.Nil(t, result.Restart, "completion")
			require.True(t, provider.completeRequest.AuthenticationStartedAt.Equal(state.CreatedAt), "provider proof was not bound to consumed flow")
			require.Equal(t, 1, states.consumeCalls, "provider proof was not bound to consumed flow")
			require.Equal(t, test.wantCommit, sessions.input != nil, "provider proof was not bound to consumed flow")
			if test.fail {
				require.Nil(t, result.Session)
				require.Equal(t, "/account/reauthenticate?task=connect-provider#external_login=failed", result.ReturnTo)
				require.Equal(t, state.AuditEventID, auditor.failID)
			} else {
				require.Same(t, committed, result.Session, "proof changed Session")
				require.Equal(t, state.ReturnTo, result.ReturnTo, "proof changed final task")
				require.Equal(t, state.ID, sessions.input.StateID)
				require.Equal(t, provider.assertion.Subject, sessions.input.Subject)
			}
		})
	}
}

type recoveryExternalIssuerFake struct {
	state *model.UserMFARecovery
	calls int
}

func (f *recoveryExternalIssuerFake) recoveryState(context.Context, model.UserID) (*model.UserMFARecovery, error) {
	return f.state, nil
}
func (f *recoveryExternalIssuerFake) createSession(context.Context, sessionIssuance) (*model.Session, *model.AuthenticationTokens, error) {
	f.calls++
	return nil, nil, errors.New("unexpected Session issuance")
}

type recoveryExternalIdentityFake struct {
	store.ExternalIdentityStore
	user     *model.User
	identity *model.ExternalIdentity
}

func (f recoveryExternalIdentityFake) ResolveOrProvision(context.Context, *store.ExternalIdentityResolutionRequest) (*store.ExternalIdentityResolution, error) {
	return &store.ExternalIdentityResolution{User: f.user, Identity: f.identity}, nil
}

func TestExternalRecoveryRequiresFreshExistingIdentityBeforeSessionIssuance(t *testing.T) {
	for _, test := range []struct {
		name                string
		purpose             model.ExternalAuthenticationPurpose
		wrongUser, oldState bool
		restart             bool
	}{
		{name: "ordinary SSO restarts fresh proof", purpose: model.ExternalAuthenticationPurposeLogin, restart: true},
		{name: "fresh recovery rejects different identity", purpose: model.ExternalAuthenticationPurposeMFARecovery, wrongUser: true},
		{name: "old unknown User flow stays invalid", purpose: model.ExternalAuthenticationPurposeLogin, oldState: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := model.NowUTC()
			user := &model.User{ID: model.NewUserID(), Username: "fresh-recovery", Email: "fresh@example.edu"}
			issuer := &recoveryExternalIssuerFake{state: &model.UserMFARecovery{UserID: user.ID, Generation: 1, ReenrollmentRequired: true, ResetAt: model.OptionalTimeFrom(now.Add(-time.Minute)), UpdatedAt: now.Add(-time.Minute)}}
			stateToken, binding := model.NewCredentialToken(), model.NewCredentialToken()
			state := &model.ExternalLoginState{Provider: "campus", Purpose: test.purpose, StateHash: model.HashToken(stateToken), BindingHash: model.HashToken(binding), ReturnTo: "/account/security", ClientType: model.SessionClientWeb, ExpiresAt: now.Add(time.Minute)}
			if test.wrongUser {
				state.TargetUserID = model.NewUserID()
			}
			createdAt := now.Add(-time.Second)
			if test.oldState {
				createdAt = now.Add(-2 * time.Minute)
			}
			state.PrepareCreate(model.NewExternalLoginStateID(), createdAt)
			provider := &freshExternalProviderFake{providerConnectionProvider: providerConnectionProvider{state: stateToken, assertion: &model.ExternalAuthenticationAssertion{ProviderID: "campus", Subject: "existing-identity", AuthenticatedAt: now.UnixMilli()}}}
			service := externalAuthenticationBeginService(t, externalProviderSourceFake{provider: provider}, newAuthenticationCacheFake(), 10)
			service.now = func() time.Time { return now }
			states := &externalLoginStateStoreFake{get: state, consumeResult: state, storeNow: now}
			institution, err := model.NewInstitution(model.NewInstitutionID(), "fresh-proof", "Fresh Proof", "", now)
			require.NoError(t, err)
			service.loginStates, service.authentication, service.audit = states, issuer, externalAuditFake{}
			service.institutions = providerConnectionInstitutionStoreFake{institution: institution}
			service.identities = recoveryExternalIdentityFake{user: user, identity: &model.ExternalIdentity{ID: model.NewExternalIdentityID(), UserID: user.ID, Provider: "campus", Subject: "existing-identity"}}
			service.capabilities = &accessPolicyCapabilitiesFake{snapshot: AccessPolicyCapabilitySnapshot{Providers: []AccessPolicyProviderCapability{{Descriptor: provider.Descriptor()}}}}
			result, err := service.complete(context.Background(), "campus", binding, model.ExternalAuthenticationCallback{}, model.RequestMetadata{})
			require.Equal(t, 0, issuer.calls, "unverified recovery flow issued a Session")
			if test.restart {
				require.NoError(t, err, "recovery restart")
				require.NotNil(t, result, "recovery restart")
				require.NotNil(t, result.Restart, "recovery restart")
				require.Nil(t, result.Session, "recovery restart")
				require.True(t, provider.beginRequest.FreshAuthentication, "recovery restart")
				require.Equal(t, model.ExternalAuthenticationPurposeMFARecovery, states.saved.Purpose, "recovery restart")
				require.Equal(t, user.ID, states.saved.TargetUserID, "recovery restart")
				require.Equal(t, "/account/security", states.saved.ReturnTo, "recovery restart")
			} else {
				require.Error(t, err, "pre-reset or wrong-User proof regained authority")
				require.Nil(t, result, "pre-reset or wrong-User proof regained authority")
			}
		})
	}
}
