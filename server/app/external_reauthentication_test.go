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

func (s *freshExternalSessionStoreFake) ReauthenticateExternalWithAudit(_ context.Context, input *store.SessionExternalReauthentication) (*store.SessionReauthenticationResult, error) {
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
			result, err := service.beginReauthentication(context.Background(), NewInvocation(principal, model.RequestMetadata{}), BeginExternalReauthenticationCommand{Task: test.task, Source: "192.0.2.30"})
			if test.want == "" {
				if err == nil || result != nil || states.saved != nil || provider.beginRequest.FreshAuthentication {
					t.Fatal("unbounded continuation reached provider or Store")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			bound := states.saved
			if result == nil || bound == nil || !provider.beginRequest.FreshAuthentication || bound.Purpose != model.ExternalAuthenticationPurposeReauthenticate || bound.ReturnTo != test.want || bound.TargetUserID != principal.UserID || bound.SessionID != principal.SessionID || bound.SessionCredentialID.String() != principal.CredentialID.String() || bound.ExternalIdentityID != principal.ExternalIdentityID || bound.AuditEventID != audit.beginID {
				t.Fatalf("missing exact fresh-proof binding: %#v %#v", bound, provider.beginRequest)
			}
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
			provider := &freshExternalProviderFake{providerConnectionProvider: providerConnectionProvider{state: stateToken, completeErr: test.providerErr, assertion: &model.ExternalAuthenticationAssertion{ProviderId: "campus", Subject: "private-subject", AuthenticatedAt: now.Add(test.offset).UnixMilli()}}}
			service := externalAuthenticationBeginService(t, externalProviderSourceFake{provider: provider}, newAuthenticationCacheFake(), 10)
			service.now = func() time.Time { return now }
			states := &externalLoginStateStoreFake{get: state, consumeResult: state, storeNow: now}
			institution, err := model.NewInstitution(model.NewInstitutionID(), "fresh-proof", "Fresh Proof", "", now)
			if err != nil {
				t.Fatal(err)
			}
			events := []string{}
			auditor := &mutationAttemptAuditorFake{events: &events}
			committed := &model.Session{ID: state.SessionID, UserID: state.TargetUserID}
			sessions := &freshExternalSessionStoreFake{result: &store.SessionReauthenticationResult{Session: committed}, err: test.commitErr}
			service.loginStates, service.sessions = states, sessions
			service.institutions, service.audit, service.mutationAudit, service.invalidator = providerConnectionInstitutionStoreFake{institution: institution}, externalAuditFake{}, auditor, externalInvalidatorFake{}
			result, err := service.complete(context.Background(), "campus", binding, model.ExternalAuthenticationCallback{}, model.RequestMetadata{})
			if err != nil || result == nil || result.Tokens != nil || result.Restart != nil {
				t.Fatalf("completion=%#v err=%v", result, err)
			}
			if !provider.completeRequest.AuthenticationStartedAt.Equal(state.CreatedAt) || states.consumeCalls != 1 || (sessions.input != nil) != test.wantCommit {
				t.Fatal("provider proof was not bound to consumed flow")
			}
			if test.fail {
				if result.Session != nil || result.ReturnTo != "/account/reauthenticate?task=connect-provider#external_login=failed" || auditor.failID != state.AuditEventID {
					t.Fatalf("unsafe failure continuation: %#v", result)
				}
			} else if result.Session != committed || result.ReturnTo != state.ReturnTo || sessions.input.StateID != state.ID || sessions.input.Subject != provider.assertion.Subject {
				t.Fatalf("proof changed Session or final task: %#v", result)
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
			provider := &freshExternalProviderFake{providerConnectionProvider: providerConnectionProvider{state: stateToken, assertion: &model.ExternalAuthenticationAssertion{ProviderId: "campus", Subject: "existing-identity", AuthenticatedAt: now.UnixMilli()}}}
			service := externalAuthenticationBeginService(t, externalProviderSourceFake{provider: provider}, newAuthenticationCacheFake(), 10)
			service.now = func() time.Time { return now }
			states := &externalLoginStateStoreFake{get: state, consumeResult: state, storeNow: now}
			institution, err := model.NewInstitution(model.NewInstitutionID(), "fresh-proof", "Fresh Proof", "", now)
			if err != nil {
				t.Fatal(err)
			}
			service.loginStates, service.authentication, service.audit = states, issuer, externalAuditFake{}
			service.institutions = providerConnectionInstitutionStoreFake{institution: institution}
			service.identities = recoveryExternalIdentityFake{user: user, identity: &model.ExternalIdentity{ID: model.NewExternalIdentityID(), UserID: user.ID, Provider: "campus", Subject: "existing-identity"}}
			service.capabilities = &accessPolicyCapabilitiesFake{snapshot: AccessPolicyCapabilitySnapshot{Providers: []AccessPolicyProviderCapability{{Descriptor: provider.Descriptor()}}}}
			result, err := service.complete(context.Background(), "campus", binding, model.ExternalAuthenticationCallback{}, model.RequestMetadata{})
			if issuer.calls != 0 {
				t.Fatal("unverified recovery flow issued a Session")
			}
			if test.restart {
				if err != nil || result == nil || result.Restart == nil || result.Session != nil || !provider.beginRequest.FreshAuthentication || states.saved.Purpose != model.ExternalAuthenticationPurposeMFARecovery || states.saved.TargetUserID != user.ID || states.saved.ReturnTo != "/account/security" {
					t.Fatalf("recovery restart=%#v err=%v", result, err)
				}
			} else if err == nil || result != nil {
				t.Fatal("pre-reset or wrong-User proof regained authority")
			}
		})
	}
}
