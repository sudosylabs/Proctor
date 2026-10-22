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
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestDesktopLocalAuthenticationPreservesPasswordCapacityFailure(t *testing.T) {
	for _, account := range []string{"existing", "missing"} {
		t.Run(account, func(t *testing.T) {
			persistence := newAuthenticationStoreFake()
			authentication := newTestAuthenticationService(t, persistence)
			const password = "CorrectHorseBatteryStaple1!"
			user, err := authentication.CreateLocalUser(context.Background(), CreateLocalUserCommand{
				User: &model.User{Username: "desktop-capacity", Email: "desktop-capacity@example.edu"}, Password: password,
			})
			require.NoError(t, err)
			transactions := &desktopAuthorizationStoreFake{}
			identity := testDesktopAuthorizationIdentity(t, "https://proctor.example.edu", time.Now)
			identity.authentication = authentication
			service, err := newDesktopAuthorizationService(desktopAuthorizationServiceDependencies{Transactions: transactions, Institutions: desktopAuthorizationInstitutionStoreFake{institution: &model.Institution{ID: model.NewInstitutionID()}}, AccessPolicy: desktopAuthorizationAccessPolicyFake{enabled: true}, Capabilities: &accessPolicyCapabilitiesFake{}, Audit: desktopAuthorizationAuditorFake{}, Attempts: &desktopAuthorizationAttemptLimiterFake{}, SessionPolicy: testDesktopSessionPolicy(), Policy: DesktopAuthorizationPolicy{Issuer: "https://proctor.example.edu"}, NewCredential: model.NewCredentialToken, Now: time.Now, Identity: identity})
			require.NoError(t, err)
			command := AuthenticateDesktopAuthorizationLocallyCommand{
				Binding: model.NewCredentialToken(), LoginID: user.Email, Password: password, Source: "192.0.2.37",
			}
			if account == "missing" {
				command.LoginID = "missing@example.edu"
			}
			saturatePasswordWork(t, authentication.deps.Hasher)
			result, err := service.AuthenticateDesktopAuthorizationLocally(context.Background(), invocation.Invocation{}, command)
			require.Nil(t, result, "saturated Desktop authentication = %#v/%v", result, err)
			require.True(t, apperror.Is(err, "service.busy"), "saturated Desktop authentication = %#v/%v", result, err)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			result, err = service.AuthenticateDesktopAuthorizationLocally(ctx, invocation.Invocation{}, command)
			require.Nil(t, result, "canceled Desktop authentication")
			require.True(t, apperror.Is(err, "authentication.internal"), "canceled Desktop authentication")
			require.ErrorIs(t, err, context.Canceled, "canceled Desktop authentication")
			require.Nil(t, transactions.authenticated, "rejected password work advanced Desktop authentication or issued a Session")
			require.Empty(t, persistence.sessions, "rejected password work advanced Desktop authentication or issued a Session")
		})
	}
}

func TestDesktopAuthorizationStartPinsPublicClientRequest(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	handle := model.NewCredentialToken()
	proof := model.NewCredentialToken()
	generated := []string{handle, proof}
	next := 0
	persistence := &desktopAuthorizationStoreFake{createExpiresAt: at.Add(5 * time.Minute)}
	institution := &model.Institution{ID: model.NewInstitutionID()}
	service, err := newDesktopAuthorizationService(desktopAuthorizationServiceDependencies{Transactions: persistence, Institutions: desktopAuthorizationInstitutionStoreFake{institution: institution}, AccessPolicy: desktopAuthorizationAccessPolicyFake{enabled: true}, Capabilities: &accessPolicyCapabilitiesFake{snapshot: AccessPolicyCapabilitySnapshot{}}, Audit: desktopAuthorizationAuditorFake{}, Attempts: &desktopAuthorizationAttemptLimiterFake{}, SessionPolicy: testDesktopSessionPolicy(), Policy: DesktopAuthorizationPolicy{Issuer: "https://proctor.example.edu"}, NewCredential: func() string { value := generated[next]; next++; return value }, Now: func() time.Time { return at }, Identity: testDesktopAuthorizationIdentity(t, "https://proctor.example.edu", func() time.Time { return at })})
	require.NoError(t, err)
	state := model.NewCredentialToken()
	challenge := model.NewCredentialToken()
	callback := "http://127.0.0.1:49152/" + model.NewCredentialToken()
	command := StartDesktopAuthorizationCommand{
		CallbackURL: callback, State: state, CodeChallenge: challenge,
		DeviceID: "desktop-1", DeviceName: "Exam laptop",
	}
	setTestDesktopAuthorizationBuild(t, &command)
	result, err := service.StartDesktopAuthorization(context.Background(), invocation.Invocation{}, command)
	require.NoError(t, err)
	require.NotNil(t, persistence.created)
	require.Equal(t, institution.ID, persistence.created.InstitutionID)
	require.Equal(t, "https://proctor.example.edu", persistence.created.Issuer)
	require.Equal(t, model.HashToken(handle), persistence.created.HandleHash)
	require.Equal(t, model.HashToken(proof), persistence.created.BrowserProofHash)
	require.Equal(t, model.HashToken(state), persistence.created.StateHash)
	require.Equal(t, callback, persistence.created.CallbackURL)
	require.Equal(t, challenge, persistence.created.CodeChallenge)
	require.Equal(t, 5*time.Minute, persistence.created.Lifetime)
	authorizationURL, err := url.Parse(result.AuthorizationURL)
	require.NoError(t, err)
	require.Equal(t, "https", authorizationURL.Scheme)
	require.Equal(t, "proctor.example.edu", authorizationURL.Host)
	require.Equal(t, "/authorize/desktop", authorizationURL.Path)
	require.Equal(t, handle, authorizationURL.Query().Get("request"))
	require.Equal(t, state, authorizationURL.Query().Get("state"))
	require.Equal(t, "proof="+proof, authorizationURL.Fragment)
	require.Equal(t, at.Add(5*time.Minute).UnixMilli(), result.ExpiresAt)
	for _, raw := range []string{handle, proof, state} {
		require.NotEqual(t, raw, persistence.created.HandleHash, "raw public-client secret was persisted")
		require.NotEqual(t, raw, persistence.created.BrowserProofHash, "raw public-client secret was persisted")
		require.NotEqual(t, raw, persistence.created.StateHash, "raw public-client secret was persisted")
	}
}

func TestDesktopAuthorizationStartRejectsMaintenanceBeforePersistence(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	persistence := &desktopAuthorizationStoreFake{}
	identity := testDesktopAuthorizationIdentity(t, "https://proctor.example.edu", func() time.Time { return at })
	identity.compatibility = desktopAuthorizationCompatibilityFake{result: DesktopCompatibilityResult{
		Availability:  model.DesktopAvailabilityMaintenance,
		Compatibility: DesktopCompatibilityCompatible,
	}}
	service, err := newDesktopAuthorizationService(desktopAuthorizationServiceDependencies{Transactions: persistence, Institutions: desktopAuthorizationInstitutionStoreFake{institution: &model.Institution{ID: model.NewInstitutionID()}}, AccessPolicy: desktopAuthorizationAccessPolicyFake{enabled: true}, Capabilities: &accessPolicyCapabilitiesFake{}, Audit: desktopAuthorizationAuditorFake{}, Attempts: &desktopAuthorizationAttemptLimiterFake{}, SessionPolicy: testDesktopSessionPolicy(), Policy: DesktopAuthorizationPolicy{Issuer: "https://proctor.example.edu"}, NewCredential: model.NewCredentialToken, Now: func() time.Time { return at }, Identity: identity})
	require.NoError(t, err)
	command := StartDesktopAuthorizationCommand{
		CallbackURL:   "http://127.0.0.1:49152/" + model.NewCredentialToken(),
		State:         model.NewCredentialToken(),
		CodeChallenge: model.NewCredentialToken(),
	}
	setTestDesktopAuthorizationBuild(t, &command)
	result, err := service.StartDesktopAuthorization(context.Background(), invocation.Invocation{}, command)
	require.Nil(t, result, "Start() = %#v, %v; persisted", result, err)
	require.True(t, apperror.Is(err, "authentication.desktop_authorization.unavailable"), "Start() = %#v, %v; persisted", result, err)
	require.Nil(t, persistence.created, "Start() = %#v, %v; persisted", result, err)
}

func TestDesktopAuthorizationStartRejectsEqualGeneratedProofsBeforeStore(t *testing.T) {
	t.Parallel()
	persistence := &desktopAuthorizationStoreFake{}
	credential := model.NewCredentialToken()
	service, err := newDesktopAuthorizationService(desktopAuthorizationServiceDependencies{Transactions: persistence, Institutions: desktopAuthorizationInstitutionStoreFake{institution: &model.Institution{ID: model.NewInstitutionID()}}, AccessPolicy: desktopAuthorizationAccessPolicyFake{enabled: true}, Capabilities: &accessPolicyCapabilitiesFake{}, Audit: desktopAuthorizationAuditorFake{}, Attempts: &desktopAuthorizationAttemptLimiterFake{}, SessionPolicy: testDesktopSessionPolicy(), Policy: DesktopAuthorizationPolicy{Issuer: "https://proctor.example.edu"}, NewCredential: func() string { return credential }, Now: time.Now, Identity: testDesktopAuthorizationIdentity(t, "https://proctor.example.edu", time.Now)})
	require.NoError(t, err)
	command := StartDesktopAuthorizationCommand{
		CallbackURL: "http://127.0.0.1:49152/" + model.NewCredentialToken(), State: model.NewCredentialToken(),
		CodeChallenge: model.NewCredentialToken(),
	}
	setTestDesktopAuthorizationBuild(t, &command)
	_, err = service.StartDesktopAuthorization(context.Background(), invocation.Invocation{}, command)
	require.True(t, apperror.Is(err, "authentication.desktop_authorization.unavailable"), "Start() error=%v persisted", err)
	require.Nil(t, persistence.created, "Start() error=%v persisted", err)
}

func TestDesktopAuthorizationLoopbackHTTPIssuerRequiresExplicitDevelopmentPolicy(t *testing.T) {
	t.Parallel()

	dependencies := func(policy DesktopAuthorizationPolicy) error {
		_, err := newDesktopAuthorizationService(desktopAuthorizationServiceDependencies{Transactions: &desktopAuthorizationStoreFake{}, Institutions: desktopAuthorizationInstitutionStoreFake{institution: &model.Institution{ID: model.NewInstitutionID()}}, AccessPolicy: desktopAuthorizationAccessPolicyFake{enabled: true}, Capabilities: &accessPolicyCapabilitiesFake{}, Audit: desktopAuthorizationAuditorFake{}, Attempts: &desktopAuthorizationAttemptLimiterFake{}, SessionPolicy: testDesktopSessionPolicy(), Policy: policy, NewCredential: model.NewCredentialToken, Now: time.Now, Identity: testDesktopAuthorizationIdentity(t, policy.Issuer, time.Now)})
		return err
	}
	require.Error(t, dependencies(DesktopAuthorizationPolicy{Issuer: "http://localhost:8065"}), "loopback HTTP issuer accepted without explicit development policy")
	require.NoError(t, dependencies(DesktopAuthorizationPolicy{Issuer: "http://localhost:8065", AllowLoopbackHTTPDevelopment: true}))
	require.Error(t, dependencies(DesktopAuthorizationPolicy{Issuer: "http://192.0.2.1:8065", AllowLoopbackHTTPDevelopment: true}), "non-loopback HTTP issuer accepted in development policy")
}

func TestDesktopAuthorizationApprovalAndExchangeArePurposeBound(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	code, access, refresh := model.NewCredentialToken(), model.NewCredentialToken(), model.NewCredentialToken()
	generated := []string{code, access, refresh}
	persistence := &desktopAuthorizationStoreFake{}
	institution := &model.Institution{ID: model.NewInstitutionID()}
	service, err := newDesktopAuthorizationService(desktopAuthorizationServiceDependencies{Transactions: persistence, Institutions: desktopAuthorizationInstitutionStoreFake{institution: institution}, AccessPolicy: desktopAuthorizationAccessPolicyFake{enabled: true}, Capabilities: &accessPolicyCapabilitiesFake{snapshot: AccessPolicyCapabilitySnapshot{Providers: []AccessPolicyProviderCapability{{Descriptor: model.ExternalAuthenticationProvider{ID: "campus"}}}}}, Audit: desktopAuthorizationAuditorFake{}, Attempts: &desktopAuthorizationAttemptLimiterFake{}, SessionPolicy: testDesktopSessionPolicy(), Policy: DesktopAuthorizationPolicy{Issuer: "https://proctor.example.edu"}, NewCredential: func() string { value := generated[0]; generated = generated[1:]; return value }, Now: func() time.Time { return at }, Identity: testDesktopAuthorizationIdentity(t, "https://proctor.example.edu", func() time.Time { return at })})
	require.NoError(t, err)
	binding, state := model.NewCredentialToken(), model.NewCredentialToken()
	callback := "http://[::1]:49152/" + model.NewCredentialToken()
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewID()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "oidc", AuthenticationProviderID: "campus",
		ExternalIdentityID:     model.NewExternalIdentityID(),
		AuthenticationStrength: model.AuthenticationMultiFactor, ClientType: model.SessionClientWeb,
		AuthenticatedAt: at.Add(-time.Minute), MFACompletedAt: model.OptionalTimeFrom(at)}
	persistence.issueResult = &store.DesktopAuthorizationCodeIssued{CallbackURL: callback, CodeExpiresAt: at.Add(time.Minute)}
	persistence.contextUserID = principal.UserID
	approved, err := service.ApproveDesktopAuthorization(context.Background(), invocation.New(principal, model.RequestMetadata{}),
		ApproveDesktopAuthorizationCommand{Binding: binding, State: state})
	require.NoError(t, err)
	redirect, _ := url.Parse(approved.RedirectURL)
	require.Equal(t, "http", redirect.Scheme, "approval = %#v input", approved)
	require.Equal(t, "[::1]:49152", redirect.Host, "approval = %#v input", approved)
	require.Equal(t, code, redirect.Query().Get("code"), "approval = %#v input", approved)
	require.Equal(t, state, redirect.Query().Get("state"), "approval = %#v input", approved)
	require.NotNil(t, persistence.issued, "approval = %#v input", approved)
	require.Equal(t, model.HashToken(binding), persistence.issued.BindingHash, "approval = %#v input", approved)
	require.Equal(t, time.Minute, persistence.issued.CodeLifetime, "approval = %#v input", approved)
	exchangeCommand := testDesktopAuthorizationExchangeCommand(
		t, service, at, code, state, "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk", "",
	)
	session, registration := testDesktopAuthorizationExchangeResult(t, exchangeCommand, principal.UserID, at)
	persistence.exchangeResult = &store.DesktopAuthorizationExchangeResult{
		Session: session, Registration: registration,
		AccessExpiresAt: at.Add(5 * time.Minute), RefreshExpiresAt: at.Add(time.Hour),
	}
	exchanged, err := service.ExchangeDesktopAuthorization(context.Background(), invocation.New(model.Principal{}, model.RequestMetadata{}),
		exchangeCommand)
	require.NoError(t, err)
	require.NotNil(t, persistence.exchanged, "exchange = %#v input", exchanged)
	require.Equal(t, "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM", persistence.exchanged.CodeChallenge, "exchange = %#v input", exchanged)
	require.Equal(t, 5*time.Minute, persistence.exchanged.AccessLifetime, "exchange = %#v input", exchanged)
	require.Equal(t, time.Hour, persistence.exchanged.RefreshLifetime, "exchange = %#v input", exchanged)
	require.Equal(t, 30*time.Minute, persistence.exchanged.IdleLifetime, "exchange = %#v input", exchanged)
	require.Equal(t, 2*time.Hour, persistence.exchanged.AbsoluteLifetime, "exchange = %#v input", exchanged)
	require.Equal(t, int64(1), persistence.exchanged.DesktopCompatibilityPolicyRevision, "exchange = %#v input", exchanged)
	require.Equal(t, access, exchanged.Tokens.AccessToken, "exchange = %#v input", exchanged)
	require.Equal(t, refresh, exchanged.Tokens.RefreshToken, "exchange = %#v input", exchanged)
}

func TestDesktopAuthorizationTerminalStoreRejectionCompletesFailureAudit(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	auditor := &recordingDesktopAuthorizationAuditor{}
	persistence := &desktopAuthorizationStoreFake{issueErr: store.NewErrNotFound("browser_authentication_transaction", "")}
	service, err := newDesktopAuthorizationService(desktopAuthorizationServiceDependencies{Transactions: persistence, Institutions: desktopAuthorizationInstitutionStoreFake{institution: &model.Institution{ID: model.NewInstitutionID()}}, AccessPolicy: desktopAuthorizationAccessPolicyFake{enabled: true}, Capabilities: &accessPolicyCapabilitiesFake{snapshot: AccessPolicyCapabilitySnapshot{}}, Audit: auditor, Attempts: &desktopAuthorizationAttemptLimiterFake{}, SessionPolicy: testDesktopSessionPolicy(), Policy: DesktopAuthorizationPolicy{Issuer: "https://proctor.example.edu"}, NewCredential: model.NewCredentialToken, Now: func() time.Time { return at }, Identity: testDesktopAuthorizationIdentity(t, "https://proctor.example.edu", func() time.Time { return at })})
	require.NoError(t, err)
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewID()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: model.SessionClientWeb, AuthenticatedAt: at}
	persistence.contextUserID = principal.UserID
	_, err = service.ApproveDesktopAuthorization(context.Background(), invocation.New(principal, model.RequestMetadata{}),
		ApproveDesktopAuthorizationCommand{Binding: model.NewCredentialToken(), State: model.NewCredentialToken()})
	require.True(t, apperror.Is(err, "authentication.desktop_authorization.rejected"), "Approve() error=%v failure audit=%q", err, auditor.auditID)
	require.Equal(t, "authentication.desktop_authorization.rejected", auditor.failureCode, "Approve() error=%v failure audit=%q", err, auditor.auditID)
	require.NotEmpty(t, auditor.auditID, "Approve() error=%v failure audit=%q", err, auditor.auditID)
	persistence.issueErr = nil
	persistence.exchangeErr = errors.New("database unavailable")
	exchangeCommand := testDesktopAuthorizationExchangeCommand(
		t, service, at, model.NewCredentialToken(), model.NewCredentialToken(), model.NewCredentialToken(), "",
	)
	_, err = service.ExchangeDesktopAuthorization(context.Background(), invocation.Invocation{}, exchangeCommand)
	require.True(t, apperror.Is(err, "authentication.desktop_authorization.unavailable"), "Exchange() error=%v failure audit", err)
	require.Equal(t, "authentication.desktop_authorization.unavailable", auditor.failureCode, "Exchange() error=%v failure audit", err)
}

func TestDesktopAuthorizationExchangeFailsClosedWhenSessionNonceCannotBeStored(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	persistence := &desktopAuthorizationStoreFake{}
	auditor := &recordingDesktopAuthorizationAuditor{}
	identity := testDesktopAuthorizationIdentity(t, "https://proctor.example.edu", func() time.Time { return at })
	service, err := newDesktopAuthorizationService(desktopAuthorizationServiceDependencies{Transactions: persistence, Institutions: desktopAuthorizationInstitutionStoreFake{institution: &model.Institution{ID: model.NewInstitutionID()}}, AccessPolicy: desktopAuthorizationAccessPolicyFake{enabled: true}, Capabilities: &accessPolicyCapabilitiesFake{}, Audit: auditor, Attempts: &desktopAuthorizationAttemptLimiterFake{}, SessionPolicy: testDesktopSessionPolicy(), Policy: DesktopAuthorizationPolicy{Issuer: "https://proctor.example.edu"}, NewCredential: model.NewCredentialToken, Now: func() time.Time { return at }, Identity: identity})
	require.NoError(t, err)
	command := testDesktopAuthorizationExchangeCommand(
		t, service, at, model.NewCredentialToken(), model.NewCredentialToken(), model.NewCredentialToken(), "",
	)
	session, registration := testDesktopAuthorizationExchangeResult(t, command, model.NewUserID(), at)
	persistence.exchangeResult = &store.DesktopAuthorizationExchangeResult{
		Session: session, Registration: registration,
		AccessExpiresAt: at.Add(5 * time.Minute), RefreshExpiresAt: at.Add(time.Hour),
	}
	identity.dpop.deps.Cache.(*dpopCacheFake).setAlwaysErr = errors.New("shared nonce cache unavailable")

	result, err := service.ExchangeDesktopAuthorization(context.Background(), invocation.Invocation{}, command)
	require.Nil(t, result, "Exchange() = %#v, %v; want no credential response", result, err)
	require.True(t, apperror.Is(err, "authentication.dpop.unavailable"), "Exchange() = %#v, %v; want no credential response", result, err)
	sessions := identity.authentication.deps.Sessions.(*desktopAuthorizationSessionStoreStub)
	require.Equal(t, session.ID.String(), sessions.revokedID)
	require.Equal(t, session.UserID.String(), sessions.revokedUserID)
	require.Equal(t, model.SessionRevocationDesktopAuthorizationFailed, sessions.reason)
	// The exchange's success audit is followed by a record of the withdrawal.
	require.Equal(t, session.ID, auditor.withdrawnSession)
	require.Equal(t, session.ID, auditor.completed["session_id"])
}

func TestDesktopAuthorizationCrossAccountApprovalIsAudited(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	auditor := &recordingDesktopAuthorizationAuditor{}
	persistence := &desktopAuthorizationStoreFake{}
	service, err := newDesktopAuthorizationService(desktopAuthorizationServiceDependencies{Transactions: persistence, Institutions: desktopAuthorizationInstitutionStoreFake{institution: &model.Institution{ID: model.NewInstitutionID()}}, AccessPolicy: desktopAuthorizationAccessPolicyFake{enabled: true}, Capabilities: &accessPolicyCapabilitiesFake{snapshot: AccessPolicyCapabilitySnapshot{}}, Audit: auditor, Attempts: &desktopAuthorizationAttemptLimiterFake{}, SessionPolicy: testDesktopSessionPolicy(), Policy: DesktopAuthorizationPolicy{Issuer: "https://proctor.example.edu"}, NewCredential: model.NewCredentialToken, Now: func() time.Time { return at }, Identity: testDesktopAuthorizationIdentity(t, "https://proctor.example.edu", func() time.Time { return at })})
	require.NoError(t, err)
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewID()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: model.SessionClientWeb, AuthenticatedAt: at}
	persistence.contextUserID = model.NewUserID()
	_, err = service.ApproveDesktopAuthorization(context.Background(), invocation.New(principal, model.RequestMetadata{}),
		ApproveDesktopAuthorizationCommand{Binding: model.NewCredentialToken(), State: model.NewCredentialToken()})
	require.True(t, apperror.Is(err, "authentication.desktop_authorization.rejected"), "Approve() error = %v", err)
	require.Equal(t, persistence.contextUserID, auditor.rejectedTarget, "the refused approval names the handoff's account")
	require.Equal(t, "authentication.desktop_authorization.rejected", auditor.rejectedCode)
	require.Zero(t, auditor.prepareIssue, "a refused approval issues no code")
}

func TestDesktopAuthorizationMalformedStoreFactsFailClosed(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	for name, institution := range map[string]*model.Institution{
		"nil Institution":              nil,
		"invalid Institution identity": {},
	} {
		t.Run(name, func(t *testing.T) {
			service, err := newDesktopAuthorizationService(desktopAuthorizationServiceDependencies{Transactions: &desktopAuthorizationStoreFake{}, Institutions: desktopAuthorizationInstitutionStoreFake{institution: institution}, AccessPolicy: desktopAuthorizationAccessPolicyFake{enabled: true}, Capabilities: &accessPolicyCapabilitiesFake{}, Audit: desktopAuthorizationAuditorFake{}, Attempts: &desktopAuthorizationAttemptLimiterFake{}, SessionPolicy: testDesktopSessionPolicy(), Policy: DesktopAuthorizationPolicy{Issuer: "https://proctor.example.edu"}, NewCredential: model.NewCredentialToken, Now: func() time.Time { return at }, Identity: testDesktopAuthorizationIdentity(t, "https://proctor.example.edu", func() time.Time { return at })})
			require.NoError(t, err)
			command := StartDesktopAuthorizationCommand{
				CallbackURL: "http://127.0.0.1:49152/" + model.NewCredentialToken(), State: model.NewCredentialToken(),
				CodeChallenge: model.NewCredentialToken(),
			}
			setTestDesktopAuthorizationBuild(t, &command)
			_, err = service.StartDesktopAuthorization(context.Background(), invocation.Invocation{}, command)
			require.True(t, apperror.Is(err, "authentication.desktop_authorization.unavailable"), "Start error = %v", err)
		})
	}
	newService := func(persistence *desktopAuthorizationStoreFake, audit desktopAuthorizationAuditor) *desktopAuthorizationService {
		service, err := newDesktopAuthorizationService(desktopAuthorizationServiceDependencies{Transactions: persistence, Institutions: desktopAuthorizationInstitutionStoreFake{institution: &model.Institution{ID: model.NewInstitutionID()}}, AccessPolicy: desktopAuthorizationAccessPolicyFake{enabled: true}, Capabilities: &accessPolicyCapabilitiesFake{snapshot: AccessPolicyCapabilitySnapshot{}}, Audit: audit, Attempts: &desktopAuthorizationAttemptLimiterFake{}, SessionPolicy: testDesktopSessionPolicy(), Policy: DesktopAuthorizationPolicy{Issuer: "https://proctor.example.edu"}, NewCredential: model.NewCredentialToken, Now: func() time.Time { return at }, Identity: testDesktopAuthorizationIdentity(t, "https://proctor.example.edu", func() time.Time { return at })})
		require.NoError(t, err)
		return service
	}

	t.Run("creation identity", func(t *testing.T) {
		persistence := &desktopAuthorizationStoreFake{createResult: &store.DesktopAuthorizationCreated{
			ID: model.NewBrowserAuthenticationTransactionID(), ExpiresAt: at.Add(time.Minute),
		}}
		service := newService(persistence, desktopAuthorizationAuditorFake{})
		command := StartDesktopAuthorizationCommand{
			CallbackURL: "http://127.0.0.1:49152/" + model.NewCredentialToken(), State: model.NewCredentialToken(),
			CodeChallenge: model.NewCredentialToken(),
		}
		setTestDesktopAuthorizationBuild(t, &command)
		_, err := service.StartDesktopAuthorization(context.Background(), invocation.Invocation{}, command)
		require.True(t, apperror.Is(err, "authentication.desktop_authorization.unavailable"), "Start error = %v", err)
	})

	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewID()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: model.SessionClientWeb, AuthenticatedAt: at}
	t.Run("issued callback", func(t *testing.T) {
		auditor := &recordingDesktopAuthorizationAuditor{}
		service := newService(&desktopAuthorizationStoreFake{issueResult: &store.DesktopAuthorizationCodeIssued{
			CallbackURL: "https://not-loopback.example/", CodeExpiresAt: at.Add(time.Minute),
		}, contextUserID: principal.UserID}, auditor)
		_, err := service.ApproveDesktopAuthorization(context.Background(), invocation.New(principal, model.RequestMetadata{}),
			ApproveDesktopAuthorizationCommand{Binding: model.NewCredentialToken(), State: model.NewCredentialToken()})
		require.True(t, apperror.Is(err, "authentication.desktop_authorization.unavailable"), "Approve error/audit = %v", err)
		require.Equal(t, "authentication.desktop_authorization.unavailable", auditor.failureCode, "Approve error/audit = %v", err)
	})

	t.Run("exchange expiries", func(t *testing.T) {
		auditor := &recordingDesktopAuthorizationAuditor{}
		persistence := &desktopAuthorizationStoreFake{}
		service := newService(persistence, auditor)
		exchangeCommand := testDesktopAuthorizationExchangeCommand(
			t, service, at, model.NewCredentialToken(), model.NewCredentialToken(),
			"dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk", "",
		)
		session, registration := testDesktopAuthorizationExchangeResult(t, exchangeCommand, model.NewUserID(), at)
		persistence.exchangeResult = &store.DesktopAuthorizationExchangeResult{
			Session: session, Registration: registration,
			AccessExpiresAt:  at.Add(testDesktopSessionPolicy().AccessTTL + time.Second),
			RefreshExpiresAt: at.Add(time.Hour),
		}
		_, err := service.ExchangeDesktopAuthorization(context.Background(), invocation.Invocation{}, exchangeCommand)
		require.True(t, apperror.Is(err, "authentication.desktop_authorization.unavailable"), "Exchange error/audit = %v", err)
		require.Equal(t, "authentication.desktop_authorization.unavailable", auditor.failureCode, "Exchange error/audit = %v", err)
	})
}

func TestDesktopAuthorizationPublicAttemptLimitsPrecedePersistenceAndAudit(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	persistence := &desktopAuthorizationStoreFake{}
	auditor := &recordingDesktopAuthorizationAuditor{}
	limiter := &desktopAuthorizationAttemptLimiterFake{err: apperror.New("authentication.rate_limited")}
	service, err := newDesktopAuthorizationService(desktopAuthorizationServiceDependencies{Transactions: persistence, Institutions: desktopAuthorizationInstitutionStoreFake{institution: &model.Institution{ID: model.NewInstitutionID()}}, AccessPolicy: desktopAuthorizationAccessPolicyFake{enabled: true}, Capabilities: &accessPolicyCapabilitiesFake{snapshot: AccessPolicyCapabilitySnapshot{}}, Audit: auditor, Attempts: limiter, SessionPolicy: testDesktopSessionPolicy(), Policy: DesktopAuthorizationPolicy{Issuer: "https://proctor.example.edu"}, NewCredential: model.NewCredentialToken, Now: func() time.Time { return at }, Identity: testDesktopAuthorizationIdentity(t, "https://proctor.example.edu", func() time.Time { return at })})
	require.NoError(t, err)
	state := model.NewCredentialToken()
	command := StartDesktopAuthorizationCommand{
		CallbackURL: "http://127.0.0.1:49152/" + model.NewCredentialToken(), State: state,
		CodeChallenge: model.NewCredentialToken(), Source: "192.0.2.10:5000",
	}
	setTestDesktopAuthorizationBuild(t, &command)
	_, err = service.StartDesktopAuthorization(context.Background(), invocation.Invocation{}, command)
	require.True(t, apperror.Is(err, "authentication.rate_limited"), "Start() error=%v persisted=%#v audit prepares=%d", err, persistence.created, auditor.prepareIssue)
	require.Nil(t, persistence.created, "Start() error=%v persisted=%#v audit prepares=%d", err, persistence.created, auditor.prepareIssue)
	require.Equal(t, 0, auditor.prepareExchange, "Start() error=%v persisted=%#v audit prepares=%d", err, persistence.created, auditor.prepareIssue)
	require.Equal(t, 0, auditor.prepareIssue, "Start() error=%v persisted=%#v audit prepares=%d", err, persistence.created, auditor.prepareIssue)
	require.Equal(t, desktopAuthorizationAttemptStart, limiter.operation)
	require.Equal(t, model.HashToken(state), limiter.identity)
	require.Equal(t, "192.0.2.10:5000", limiter.source)

	code := model.NewCredentialToken()
	exchangeCommand := testDesktopAuthorizationExchangeCommand(
		t, service, at, code, model.NewCredentialToken(), model.NewCredentialToken(), "192.0.2.10:5001",
	)
	_, err = service.ExchangeDesktopAuthorization(context.Background(), invocation.Invocation{}, exchangeCommand)
	require.True(t, apperror.Is(err, "authentication.rate_limited"), "Exchange() error=%v persisted=%#v exchange audits", err, persistence.exchanged)
	require.Nil(t, persistence.exchanged, "Exchange() error=%v persisted=%#v exchange audits", err, persistence.exchanged)
	require.Equal(t, 0, auditor.prepareExchange, "Exchange() error=%v persisted=%#v exchange audits", err, persistence.exchanged)
	require.Equal(t, desktopAuthorizationAttemptExchange, limiter.operation)
	require.Equal(t, model.HashToken(code), limiter.identity)
	require.Equal(t, "192.0.2.10:5001", limiter.source)
}

func TestDesktopAuthorizationAttemptAccountingSeparatesStartAndExchange(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	accounting, err := newAuthenticationAttemptAccounting(authenticationAttemptAccountingDependencies{Cache: newExpiringAuthenticationAttemptCache(func() time.Time { return now })})
	require.NoError(t, err)
	limiter := desktopAuthorizationAttemptAccounting{attempts: accounting, policy: LoginRateLimitPolicy{
		Window: time.Minute, MaximumAttempts: 1, MaximumSourceAttempts: 2,
	}}
	identity := model.HashToken(model.NewCredentialToken())
	err = limiter.Check(context.Background(), desktopAuthorizationAttemptStart, identity, "192.0.2.20:1")
	require.NoError(t, err)
	err = limiter.Check(context.Background(), desktopAuthorizationAttemptStart, identity, "192.0.2.20:1")
	require.True(t, apperror.Is(err, "authentication.rate_limited"), "repeated start error = %v", err)
	err = limiter.Check(context.Background(), desktopAuthorizationAttemptExchange, identity, "192.0.2.20:1")
	require.NoError(t, err)
}

func testDesktopSessionPolicy() SessionPolicy {
	return SessionPolicy{AccessTTL: 5 * time.Minute, RefreshTTL: time.Hour, IdleTTL: 30 * time.Minute,
		AbsoluteTTL: 2 * time.Hour, MaximumPerUser: 10}
}

type desktopAuthorizationStoreFake struct {
	authenticated   *store.DesktopAuthorizationAuthentication
	created         *store.DesktopAuthorizationCreation
	createExpiresAt time.Time
	createResult    *store.DesktopAuthorizationCreated
	createNil       bool
	issued          *store.DesktopAuthorizationCodeIssue
	issueResult     *store.DesktopAuthorizationCodeIssued
	exchanged       *store.DesktopAuthorizationExchange
	exchangeResult  *store.DesktopAuthorizationExchangeResult
	issueErr        error
	exchangeErr     error
	contextResult   *store.DesktopAuthorizationContext
	contextErr      error
	contextNil      bool
	contextBinding  string
	contextCalls    int
	authorizationID model.BrowserAuthenticationTransactionID
	contextUserID   model.UserID
	exchangeUserID  model.UserID
}

func (s *desktopAuthorizationStoreFake) CreateDesktopAuthorization(
	_ context.Context,
	transaction *store.DesktopAuthorizationCreation,
) (*store.DesktopAuthorizationCreated, error) {
	cloned := *transaction
	s.created = &cloned
	if s.createNil {
		return nil, nil
	}
	if s.createResult != nil {
		return s.createResult, nil
	}
	expiresAt := s.createExpiresAt
	if expiresAt.IsZero() {
		expiresAt = time.Now().Add(cloned.Lifetime)
	}
	return &store.DesktopAuthorizationCreated{ID: cloned.ID, ExpiresAt: expiresAt}, nil
}

func (s *desktopAuthorizationStoreFake) BindDesktopAuthorization(context.Context, *store.DesktopAuthorizationBinding) (*store.DesktopAuthorizationBound, error) {
	return &store.DesktopAuthorizationBound{ExpiresAt: time.Now().Add(time.Minute)}, nil
}

func (s *desktopAuthorizationStoreFake) GetDesktopAuthorizationContext(_ context.Context, binding string) (*store.DesktopAuthorizationContext, error) {
	s.contextBinding = binding
	s.contextCalls++
	if s.contextErr != nil || s.contextNil {
		return nil, s.contextErr
	}
	if s.contextResult != nil {
		return s.contextResult, nil
	}
	userID := s.contextUserID
	if !userID.IsValid() {
		userID = model.NewUserID()
	}
	return &store.DesktopAuthorizationContext{ID: model.NewBrowserAuthenticationTransactionID(),
		State: model.BrowserAuthenticationStateAuthenticated, UserID: userID, ExpiresAt: time.Now().Add(time.Minute)}, nil
}

func (s *desktopAuthorizationStoreFake) AuthenticateDesktopAuthorization(_ context.Context, input *store.DesktopAuthorizationAuthentication) (*store.DesktopAuthorizationAuthenticationResult, error) {
	s.authenticated = input
	return &store.DesktopAuthorizationAuthenticationResult{}, nil
}

func (s *desktopAuthorizationStoreFake) ResetDesktopAuthorizationAccount(context.Context, *store.DesktopAuthorizationAccountReset) error {
	return nil
}

func (s *desktopAuthorizationStoreFake) IssueCode(_ context.Context, input *store.DesktopAuthorizationCodeIssue) (*store.DesktopAuthorizationCodeIssued, error) {
	s.issued = input
	return s.issueResult, s.issueErr
}
func (s *desktopAuthorizationStoreFake) Cancel(context.Context, *store.DesktopAuthorizationCancellation) error {
	return nil
}
func (s *desktopAuthorizationStoreFake) ResolveDesktopAuthorizationExchange(context.Context,
	*store.DesktopAuthorizationExchangeProof,
) (*store.DesktopAuthorizationExchangeTarget, error) {
	if s.authorizationID.IsZero() {
		s.authorizationID = model.NewBrowserAuthenticationTransactionID()
	}
	if !s.exchangeUserID.IsValid() {
		s.exchangeUserID = model.NewUserID()
	}
	return &store.DesktopAuthorizationExchangeTarget{ID: s.authorizationID, UserID: s.exchangeUserID}, nil
}
func (s *desktopAuthorizationStoreFake) Exchange(_ context.Context, input *store.DesktopAuthorizationExchange) (*store.DesktopAuthorizationExchangeResult, error) {
	s.exchanged = input
	return s.exchangeResult, s.exchangeErr
}

type desktopAuthorizationInstitutionStoreFake struct {
	store.InstitutionStore
	institution *model.Institution
}

func (s desktopAuthorizationInstitutionStoreFake) GetSingleton(context.Context) (*model.Institution, error) {
	return s.institution, nil
}

type desktopAuthorizationAccessPolicyFake struct {
	enabled bool
	err     error
}

func (p desktopAuthorizationAccessPolicyFake) AllowsDesktopAuthorization(context.Context, string, string) (bool, error) {
	return p.enabled, p.err
}

func (p desktopAuthorizationAccessPolicyFake) AllowsLocalLogin(context.Context) (bool, error) {
	return p.enabled, p.err
}

type desktopAuthorizationAuditorFake struct{}

func (desktopAuthorizationAuditorFake) PrepareIssue(context.Context, invocation.Invocation, model.UserID, model.InstitutionID) (*model.AuditEvent, error) {
	return &model.AuditEvent{ID: model.NewAuditEventID()}, nil
}
func (desktopAuthorizationAuditorFake) PrepareExchange(context.Context, invocation.Invocation, model.UserID, model.InstitutionID) (*model.AuditEvent, error) {
	return &model.AuditEvent{ID: model.NewAuditEventID()}, nil
}
func (desktopAuthorizationAuditorFake) RecordRejectedIssue(context.Context, invocation.Invocation, model.UserID, model.InstitutionID, string) error {
	return nil
}
func (desktopAuthorizationAuditorFake) PrepareWithdrawal(context.Context, invocation.Invocation, model.UserID, model.InstitutionID, model.SessionID) (*model.AuditEvent, error) {
	return &model.AuditEvent{ID: model.NewAuditEventID()}, nil
}
func (desktopAuthorizationAuditorFake) Complete(context.Context, model.AuditEventID, map[string]any) error {
	return nil
}
func (desktopAuthorizationAuditorFake) Fail(context.Context, model.AuditEventID, string) error {
	return nil
}

type recordingDesktopAuthorizationAuditor struct {
	desktopAuthorizationAuditorFake
	auditID                       string
	failureCode                   string
	prepareIssue, prepareExchange int
	issueActor, exchangeActor     model.UserID
	issueRequest, exchangeRequest model.RequestMetadata
	rejectedTarget                model.UserID
	rejectedCode                  string
	withdrawnSession              model.SessionID
	completed                     map[string]any
}

func (a *recordingDesktopAuthorizationAuditor) RecordRejectedIssue(_ context.Context, _ invocation.Invocation, userID model.UserID,
	_ model.InstitutionID, errorCode string,
) error {
	a.rejectedTarget, a.rejectedCode = userID, errorCode
	return nil
}

func (a *recordingDesktopAuthorizationAuditor) PrepareWithdrawal(_ context.Context, _ invocation.Invocation, _ model.UserID,
	_ model.InstitutionID, sessionID model.SessionID,
) (*model.AuditEvent, error) {
	a.withdrawnSession = sessionID
	return &model.AuditEvent{ID: model.NewAuditEventID()}, nil
}

func (a *recordingDesktopAuthorizationAuditor) Complete(_ context.Context, _ model.AuditEventID, result map[string]any) error {
	a.completed = result
	return nil
}

func (a *recordingDesktopAuthorizationAuditor) PrepareIssue(_ context.Context, invocation invocation.Invocation, userID model.UserID, _ model.InstitutionID) (*model.AuditEvent, error) {
	a.prepareIssue++
	a.issueActor, a.issueRequest = userID, invocation.RequestMetadata()
	return &model.AuditEvent{ID: model.NewAuditEventID()}, nil
}

func (a *recordingDesktopAuthorizationAuditor) PrepareExchange(_ context.Context, invocation invocation.Invocation, userID model.UserID, _ model.InstitutionID) (*model.AuditEvent, error) {
	a.prepareExchange++
	a.exchangeActor, a.exchangeRequest = userID, invocation.RequestMetadata()
	return &model.AuditEvent{ID: model.NewAuditEventID()}, nil
}

func (a *recordingDesktopAuthorizationAuditor) Fail(_ context.Context, auditID model.AuditEventID, errorCode string) error {
	a.auditID, a.failureCode = auditID.String(), errorCode
	return nil
}

type desktopAuthorizationAttemptLimiterFake struct {
	operation desktopAuthorizationAttemptOperation
	identity  string
	source    string
	err       error
}

func (f *desktopAuthorizationAttemptLimiterFake) Check(_ context.Context, operation desktopAuthorizationAttemptOperation, identity, source string) error {
	f.operation, f.identity, f.source = operation, identity, source
	return f.err
}

type desktopAuthorizationCompatibilityFake struct {
	result DesktopCompatibilityResult
	err    error
}

func (f desktopAuthorizationCompatibilityFake) EvaluateDesktopCompatibility(context.Context, DesktopCompatibilityQuery) (DesktopCompatibilityResult, error) {
	if f.err != nil {
		return DesktopCompatibilityResult{}, f.err
	}
	if f.result.Availability == "" {
		return DesktopCompatibilityResult{Availability: model.DesktopAvailabilityReady, Compatibility: DesktopCompatibilityCompatible, PolicyRevision: 1}, nil
	}
	return f.result, nil
}

type desktopAuthorizationUserStoreStub struct{ store.UserStore }

type desktopAuthorizationSessionStoreStub struct {
	store.SessionStore
	revokedID     string
	revokedUserID string
	reason        model.SessionRevocationReason
}

func (s *desktopAuthorizationSessionStoreStub) Withdraw(_ context.Context, sessionID model.SessionID, userID model.UserID, _ int64, reason model.SessionRevocationReason) ([]string, error) {
	s.revokedID, s.revokedUserID, s.reason = sessionID.String(), userID.String(), reason
	return []string{model.HashToken("revoked-access")}, nil
}

func testDesktopAuthorizationIdentity(
	t *testing.T,
	origin string,
	now func() time.Time,
) desktopAuthorizationIdentityDependencies {
	t.Helper()
	if !strings.HasPrefix(origin, "https://") {
		origin = "https://proctor.example.edu"
	}
	security, err := newDPoPSecurity(dpopSecurityDependencies{Cache: newDPoPCacheFake(), Policy: dpopPolicy{
		Origin: origin, NonceLifetime: 5 * time.Minute, ProofLifetime: 5 * time.Minute,
		ClockSkew: time.Minute, NewNonce: model.NewCredentialToken, Now: now,
	}})
	require.NoError(t, err)
	return desktopAuthorizationIdentityDependencies{
		users: desktopAuthorizationUserStoreStub{}, authentication: &authenticationService{deps: authenticationServiceDependencies{Sessions: &desktopAuthorizationSessionStoreStub{}, SecurityEffects: discardAuthenticationSecurityEffects{}}},
		compatibility: desktopAuthorizationCompatibilityFake{}, dpop: security,
	}
}

func setTestDesktopAuthorizationBuild(t *testing.T, command *StartDesktopAuthorizationCommand) {
	t.Helper()
	_, command.PublicJWK = testDPoPKey(t)
	command.DesktopRelease = "0.1.0"
	command.DesktopBuildID = "test-build"
	command.Platform = model.DesktopPlatformDarwin
	command.Architecture = model.DesktopArchitectureARM64
	command.RealtimeProtocol = 1
}

func testDesktopAuthorizationExchangeCommand(
	t *testing.T,
	service *desktopAuthorizationService,
	at time.Time,
	code string,
	state string,
	verifier string,
	source string,
) ExchangeDesktopAuthorizationCommand {
	t.Helper()
	privateKey, publicJWK := testDPoPKey(t)
	thumbprint, err := publicJWK.Thumbprint()
	require.NoError(t, err)
	authorizationID, err := service.deps.Transactions.ResolveDesktopAuthorizationExchange(context.Background(),
		&store.DesktopAuthorizationExchangeProof{CodeHash: model.HashToken(code)})
	require.NoError(t, err)
	binding := dpopBinding{
		Kind: dpopBindingAuthorization, AuthorizationID: authorizationID.ID,
		KeyThumbprint: thumbprint, Origin: service.dpop.deps.Policy.Origin,
	}
	nonce, err := service.dpop.IssueNonce(context.Background(), binding)
	require.NoError(t, err)
	proof := signDPoPProof(t, privateKey, publicJWK, map[string]any{
		"jti": model.NewCredentialToken(), "htm": "POST",
		"htu": service.dpop.deps.Policy.Origin + "/api/v1/auth/desktop/token",
		"iat": at.Unix(), "nonce": nonce,
	})
	return ExchangeDesktopAuthorizationCommand{
		Code: code, State: state, CodeVerifier: verifier, Source: source,
		DPoPProof: proof, PublicJWK: publicJWK,
		DesktopRelease: "0.1.0", DesktopBuildID: "test-build",
		Platform: model.DesktopPlatformDarwin, Architecture: model.DesktopArchitectureARM64,
		RealtimeProtocol: 1,
	}
}

func testDesktopAuthorizationExchangeResult(
	t *testing.T,
	command ExchangeDesktopAuthorizationCommand,
	userID model.UserID,
	at time.Time,
) (*model.Session, *model.DesktopRegistration) {
	t.Helper()
	thumbprint, err := command.PublicJWK.Thumbprint()
	require.NoError(t, err)
	registrationID := model.NewDesktopRegistrationID()
	registration := &model.DesktopRegistration{
		UserID: userID, InstitutionID: model.NewInstitutionID(), PublicJWK: command.PublicJWK,
		KeyThumbprint: thumbprint, DisplayName: "Exam laptop",
		DesktopRelease: command.DesktopRelease, DesktopBuildID: command.DesktopBuildID,
		Platform: command.Platform, Architecture: command.Architecture,
		RealtimeProtocol: command.RealtimeProtocol,
	}
	registration.PrepareCreate(registrationID, at)
	session := &model.Session{
		UserID: userID, ClientType: model.SessionClientDesktop,
		DesktopRegistrationID: registrationID, DPoPKeyThumbprint: thumbprint,
		DesktopRelease: command.DesktopRelease, DesktopBuildID: command.DesktopBuildID,
		DesktopPlatform: command.Platform, DesktopArchitecture: command.Architecture,
		DesktopRealtimeProtocol: command.RealtimeProtocol,
		AuthenticationMethod:    "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		AuthenticatedAt: at, LastActivityAt: at,
		IdleExpiresAt: at.Add(30 * time.Minute), ExpiresAt: at.Add(2 * time.Hour),
	}
	session.PrepareCreate(model.NewSessionID(), at)
	return session, registration
}
