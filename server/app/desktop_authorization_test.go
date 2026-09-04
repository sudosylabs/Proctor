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

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestDesktopAuthorizationStartPinsPublicClientRequest(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	handle := model.NewCredentialToken()
	proof := model.NewCredentialToken()
	generated := []string{handle, proof}
	next := 0
	persistence := &desktopAuthorizationStoreFake{createExpiresAt: at.Add(5 * time.Minute)}
	institution := &model.Institution{ID: model.NewInstitutionID()}
	service, err := newDesktopAuthorizationService(
		persistence,
		desktopAuthorizationInstitutionStoreFake{institution: institution},
		desktopAuthorizationAccessPolicyFake{enabled: true},
		&accessPolicyCapabilitiesFake{snapshot: AccessPolicyCapabilitySnapshot{}},
		desktopAuthorizationAuditorFake{},
		&desktopAuthorizationAttemptLimiterFake{},
		testDesktopSessionPolicy(),
		DesktopAuthorizationPolicy{Issuer: "https://proctor.example.edu"},
		func() string { value := generated[next]; next++; return value },
		func() time.Time { return at },
		testDesktopAuthorizationIdentity(t, "https://proctor.example.edu", func() time.Time { return at }),
	)
	if err != nil {
		t.Fatal(err)
	}
	state := model.NewCredentialToken()
	challenge := model.NewCredentialToken()
	callback := "http://127.0.0.1:49152/" + model.NewCredentialToken()
	command := StartDesktopAuthorizationCommand{
		CallbackURL: callback, State: state, CodeChallenge: challenge,
		DeviceID: "desktop-1", DeviceName: "Exam laptop",
	}
	setTestDesktopAuthorizationBuild(t, &command)
	result, err := service.Start(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if persistence.created == nil ||
		persistence.created.InstitutionID != institution.ID ||
		persistence.created.Issuer != "https://proctor.example.edu" ||
		persistence.created.HandleHash != model.HashToken(handle) ||
		persistence.created.BrowserProofHash != model.HashToken(proof) ||
		persistence.created.StateHash != model.HashToken(state) ||
		persistence.created.CallbackURL != callback ||
		persistence.created.CodeChallenge != challenge ||
		persistence.created.Lifetime != 5*time.Minute {
		t.Fatalf("created transaction = %#v", persistence.created)
	}
	authorizationURL, err := url.Parse(result.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	if authorizationURL.Scheme != "https" || authorizationURL.Host != "proctor.example.edu" ||
		authorizationURL.Path != "/authorize/desktop" ||
		authorizationURL.Query().Get("request") != handle ||
		authorizationURL.Query().Get("state") != state ||
		authorizationURL.Fragment != "proof="+proof ||
		result.ExpiresAt != at.Add(5*time.Minute).UnixMilli() {
		t.Fatalf("start result = %#v", result)
	}
	for _, raw := range []string{handle, proof, state} {
		if persistence.created.HandleHash == raw || persistence.created.BrowserProofHash == raw ||
			persistence.created.StateHash == raw {
			t.Fatal("raw public-client secret was persisted")
		}
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
	service, err := newDesktopAuthorizationService(
		persistence,
		desktopAuthorizationInstitutionStoreFake{institution: &model.Institution{ID: model.NewInstitutionID()}},
		desktopAuthorizationAccessPolicyFake{enabled: true},
		&accessPolicyCapabilitiesFake{},
		desktopAuthorizationAuditorFake{},
		&desktopAuthorizationAttemptLimiterFake{},
		testDesktopSessionPolicy(),
		DesktopAuthorizationPolicy{Issuer: "https://proctor.example.edu"},
		model.NewCredentialToken,
		func() time.Time { return at },
		identity,
	)
	if err != nil {
		t.Fatal(err)
	}
	command := StartDesktopAuthorizationCommand{
		CallbackURL:   "http://127.0.0.1:49152/" + model.NewCredentialToken(),
		State:         model.NewCredentialToken(),
		CodeChallenge: model.NewCredentialToken(),
	}
	setTestDesktopAuthorizationBuild(t, &command)
	result, err := service.Start(context.Background(), command)
	if result != nil || !Is(err, "authentication.desktop_authorization.unavailable") || persistence.created != nil {
		t.Fatalf("Start() = %#v, %v; persisted=%#v", result, err, persistence.created)
	}
}

func TestDesktopAuthorizationStartRejectsEqualGeneratedProofsBeforeStore(t *testing.T) {
	t.Parallel()
	persistence := &desktopAuthorizationStoreFake{}
	credential := model.NewCredentialToken()
	service, err := newDesktopAuthorizationService(
		persistence,
		desktopAuthorizationInstitutionStoreFake{institution: &model.Institution{ID: model.NewInstitutionID()}},
		desktopAuthorizationAccessPolicyFake{enabled: true}, &accessPolicyCapabilitiesFake{},
		desktopAuthorizationAuditorFake{}, &desktopAuthorizationAttemptLimiterFake{}, testDesktopSessionPolicy(),
		DesktopAuthorizationPolicy{Issuer: "https://proctor.example.edu"}, func() string { return credential }, time.Now,
		testDesktopAuthorizationIdentity(t, "https://proctor.example.edu", time.Now),
	)
	if err != nil {
		t.Fatal(err)
	}
	command := StartDesktopAuthorizationCommand{
		CallbackURL: "http://127.0.0.1:49152/" + model.NewCredentialToken(), State: model.NewCredentialToken(),
		CodeChallenge: model.NewCredentialToken(),
	}
	setTestDesktopAuthorizationBuild(t, &command)
	_, err = service.Start(context.Background(), command)
	if !Is(err, "authentication.desktop_authorization.unavailable") || persistence.created != nil {
		t.Fatalf("Start() error=%v persisted=%#v", err, persistence.created)
	}
}

func TestDesktopAuthorizationLoopbackHTTPIssuerRequiresExplicitDevelopmentPolicy(t *testing.T) {
	t.Parallel()

	dependencies := func(policy DesktopAuthorizationPolicy) error {
		_, err := newDesktopAuthorizationService(
			&desktopAuthorizationStoreFake{},
			desktopAuthorizationInstitutionStoreFake{institution: &model.Institution{ID: model.NewInstitutionID()}},
			desktopAuthorizationAccessPolicyFake{enabled: true}, &accessPolicyCapabilitiesFake{},
			desktopAuthorizationAuditorFake{}, &desktopAuthorizationAttemptLimiterFake{}, testDesktopSessionPolicy(),
			policy, model.NewCredentialToken, time.Now,
			testDesktopAuthorizationIdentity(t, policy.Issuer, time.Now),
		)
		return err
	}
	if err := dependencies(DesktopAuthorizationPolicy{Issuer: "http://localhost:8065"}); err == nil {
		t.Fatal("loopback HTTP issuer accepted without explicit development policy")
	}
	if err := dependencies(DesktopAuthorizationPolicy{Issuer: "http://localhost:8065", AllowLoopbackHTTPDevelopment: true}); err != nil {
		t.Fatalf("explicit loopback HTTP development issuer rejected: %v", err)
	}
	if err := dependencies(DesktopAuthorizationPolicy{Issuer: "http://192.0.2.1:8065", AllowLoopbackHTTPDevelopment: true}); err == nil {
		t.Fatal("non-loopback HTTP issuer accepted in development policy")
	}
}

func TestDesktopAuthorizationApprovalAndExchangeArePurposeBound(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	code, access, refresh := model.NewCredentialToken(), model.NewCredentialToken(), model.NewCredentialToken()
	generated := []string{code, access, refresh}
	persistence := &desktopAuthorizationStoreFake{}
	institution := &model.Institution{ID: model.NewInstitutionID()}
	service, err := newDesktopAuthorizationService(
		persistence, desktopAuthorizationInstitutionStoreFake{institution: institution},
		desktopAuthorizationAccessPolicyFake{enabled: true},
		&accessPolicyCapabilitiesFake{snapshot: AccessPolicyCapabilitySnapshot{Providers: []AccessPolicyProviderCapability{{Descriptor: model.ExternalAuthenticationProvider{Id: "campus"}}}}},
		desktopAuthorizationAuditorFake{}, &desktopAuthorizationAttemptLimiterFake{}, testDesktopSessionPolicy(),
		DesktopAuthorizationPolicy{Issuer: "https://proctor.example.edu"},
		func() string { value := generated[0]; generated = generated[1:]; return value }, func() time.Time { return at },
		testDesktopAuthorizationIdentity(t, "https://proctor.example.edu", func() time.Time { return at }),
	)
	if err != nil {
		t.Fatal(err)
	}
	binding, state := model.NewCredentialToken(), model.NewCredentialToken()
	callback := "http://[::1]:49152/" + model.NewCredentialToken()
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "oidc", AuthenticationProviderID: "campus",
		ExternalIdentityID:     model.NewExternalIdentityID(),
		AuthenticationStrength: model.AuthenticationMultiFactor, ClientType: model.SessionClientWeb,
		AuthenticatedAt: at.Add(-time.Minute), MFACompletedAt: model.OptionalTimeFrom(at)}
	persistence.issueResult = &store.DesktopAuthorizationCodeIssued{CallbackURL: callback, CodeExpiresAt: at.Add(time.Minute)}
	approved, err := service.Approve(context.Background(), NewInvocation(principal, model.RequestMetadata{}),
		ApproveDesktopAuthorizationCommand{Binding: binding, State: state})
	if err != nil {
		t.Fatal(err)
	}
	redirect, _ := url.Parse(approved.RedirectURL)
	if redirect.Scheme != "http" || redirect.Host != "[::1]:49152" || redirect.Query().Get("code") != code ||
		redirect.Query().Get("state") != state || persistence.issued == nil ||
		persistence.issued.BindingHash != model.HashToken(binding) ||
		persistence.issued.CodeLifetime != time.Minute {
		t.Fatalf("approval = %#v input=%#v", approved, persistence.issued)
	}
	exchangeCommand := testDesktopAuthorizationExchangeCommand(
		t, service, at, code, state, "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk", "",
	)
	session, registration := testDesktopAuthorizationExchangeResult(t, exchangeCommand, principal.UserID, at)
	persistence.exchangeResult = &store.DesktopAuthorizationExchangeResult{
		Session: session, Registration: registration,
		AccessExpiresAt: at.Add(5 * time.Minute), RefreshExpiresAt: at.Add(time.Hour),
	}
	exchanged, err := service.Exchange(context.Background(), NewInvocation(model.Principal{}, model.RequestMetadata{}),
		exchangeCommand)
	if err != nil {
		t.Fatal(err)
	}
	if persistence.exchanged == nil || persistence.exchanged.CodeChallenge != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" ||
		persistence.exchanged.AccessLifetime != 5*time.Minute || persistence.exchanged.RefreshLifetime != time.Hour ||
		persistence.exchanged.IdleLifetime != 30*time.Minute || persistence.exchanged.AbsoluteLifetime != 2*time.Hour ||
		persistence.exchanged.DesktopCompatibilityPolicyRevision != 1 ||
		exchanged.Tokens.AccessToken != access || exchanged.Tokens.RefreshToken != refresh {
		t.Fatalf("exchange = %#v input=%#v", exchanged, persistence.exchanged)
	}
}

func TestDesktopAuthorizationTerminalStoreRejectionCompletesFailureAudit(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	auditor := &recordingDesktopAuthorizationAuditor{}
	persistence := &desktopAuthorizationStoreFake{issueErr: store.NewErrNotFound("browser_authentication_transaction", "")}
	service, err := newDesktopAuthorizationService(
		persistence,
		desktopAuthorizationInstitutionStoreFake{institution: &model.Institution{ID: model.NewInstitutionID()}},
		desktopAuthorizationAccessPolicyFake{enabled: true},
		&accessPolicyCapabilitiesFake{snapshot: AccessPolicyCapabilitySnapshot{}}, auditor,
		&desktopAuthorizationAttemptLimiterFake{},
		testDesktopSessionPolicy(), DesktopAuthorizationPolicy{Issuer: "https://proctor.example.edu"},
		model.NewCredentialToken, func() time.Time { return at },
		testDesktopAuthorizationIdentity(t, "https://proctor.example.edu", func() time.Time { return at }),
	)
	if err != nil {
		t.Fatal(err)
	}
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: model.SessionClientWeb, AuthenticatedAt: at}
	_, err = service.Approve(context.Background(), NewInvocation(principal, model.RequestMetadata{}),
		ApproveDesktopAuthorizationCommand{Binding: model.NewCredentialToken(), State: model.NewCredentialToken()})
	if !Is(err, "authentication.desktop_authorization.rejected") || auditor.failureCode != "authentication.desktop_authorization.rejected" || auditor.auditID == "" {
		t.Fatalf("Approve() error=%v failure audit=%q/%q", err, auditor.auditID, auditor.failureCode)
	}
	persistence.issueErr = nil
	persistence.exchangeErr = errors.New("database unavailable")
	exchangeCommand := testDesktopAuthorizationExchangeCommand(
		t, service, at, model.NewCredentialToken(), model.NewCredentialToken(), model.NewCredentialToken(), "",
	)
	_, err = service.Exchange(context.Background(), Invocation{}, exchangeCommand)
	if !Is(err, "authentication.desktop_authorization.unavailable") || auditor.failureCode != "authentication.desktop_authorization.unavailable" {
		t.Fatalf("Exchange() error=%v failure audit=%q", err, auditor.failureCode)
	}
}

func TestDesktopAuthorizationExchangeFailsClosedWhenSessionNonceCannotBeStored(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	persistence := &desktopAuthorizationStoreFake{}
	identity := testDesktopAuthorizationIdentity(t, "https://proctor.example.edu", func() time.Time { return at })
	service, err := newDesktopAuthorizationService(
		persistence,
		desktopAuthorizationInstitutionStoreFake{institution: &model.Institution{ID: model.NewInstitutionID()}},
		desktopAuthorizationAccessPolicyFake{enabled: true}, &accessPolicyCapabilitiesFake{},
		desktopAuthorizationAuditorFake{}, &desktopAuthorizationAttemptLimiterFake{}, testDesktopSessionPolicy(),
		DesktopAuthorizationPolicy{Issuer: "https://proctor.example.edu"}, model.NewCredentialToken,
		func() time.Time { return at }, identity,
	)
	if err != nil {
		t.Fatal(err)
	}
	command := testDesktopAuthorizationExchangeCommand(
		t, service, at, model.NewCredentialToken(), model.NewCredentialToken(), model.NewCredentialToken(), "",
	)
	session, registration := testDesktopAuthorizationExchangeResult(t, command, model.NewUserID(), at)
	persistence.exchangeResult = &store.DesktopAuthorizationExchangeResult{
		Session: session, Registration: registration,
		AccessExpiresAt: at.Add(5 * time.Minute), RefreshExpiresAt: at.Add(time.Hour),
	}
	identity.dpop.cache.(*dpopCacheFake).setAlwaysErr = errors.New("shared nonce cache unavailable")

	result, err := service.Exchange(context.Background(), Invocation{}, command)
	if result != nil || !Is(err, "authentication.dpop.unavailable") {
		t.Fatalf("Exchange() = %#v, %v; want no credential response", result, err)
	}
	sessions := identity.authentication.sessions.(*desktopAuthorizationSessionStoreStub)
	if sessions.revokedID != session.ID.String() || sessions.revokedUserID != session.UserID.String() ||
		sessions.reason != model.SessionRevocationDesktopAuthorizationFailed {
		t.Fatalf("compensating revocation = %#v", sessions)
	}
}

func TestDesktopAuthorizationMalformedStoreFactsFailClosed(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	for name, institution := range map[string]*model.Institution{
		"nil Institution":              nil,
		"invalid Institution identity": {},
	} {
		t.Run(name, func(t *testing.T) {
			service, err := newDesktopAuthorizationService(
				&desktopAuthorizationStoreFake{}, desktopAuthorizationInstitutionStoreFake{institution: institution},
				desktopAuthorizationAccessPolicyFake{enabled: true}, &accessPolicyCapabilitiesFake{},
				desktopAuthorizationAuditorFake{}, &desktopAuthorizationAttemptLimiterFake{}, testDesktopSessionPolicy(),
				DesktopAuthorizationPolicy{Issuer: "https://proctor.example.edu"}, model.NewCredentialToken, func() time.Time { return at },
				testDesktopAuthorizationIdentity(t, "https://proctor.example.edu", func() time.Time { return at }),
			)
			if err != nil {
				t.Fatal(err)
			}
			command := StartDesktopAuthorizationCommand{
				CallbackURL: "http://127.0.0.1:49152/" + model.NewCredentialToken(), State: model.NewCredentialToken(),
				CodeChallenge: model.NewCredentialToken(),
			}
			setTestDesktopAuthorizationBuild(t, &command)
			_, err = service.Start(context.Background(), command)
			if !Is(err, "authentication.desktop_authorization.unavailable") {
				t.Fatalf("Start error = %v", err)
			}
		})
	}
	newService := func(persistence *desktopAuthorizationStoreFake, audit desktopAuthorizationAuditor) *desktopAuthorizationService {
		service, err := newDesktopAuthorizationService(
			persistence,
			desktopAuthorizationInstitutionStoreFake{institution: &model.Institution{ID: model.NewInstitutionID()}},
			desktopAuthorizationAccessPolicyFake{enabled: true}, &accessPolicyCapabilitiesFake{snapshot: AccessPolicyCapabilitySnapshot{}},
			audit, &desktopAuthorizationAttemptLimiterFake{}, testDesktopSessionPolicy(),
			DesktopAuthorizationPolicy{Issuer: "https://proctor.example.edu"}, model.NewCredentialToken,
			func() time.Time { return at },
			testDesktopAuthorizationIdentity(t, "https://proctor.example.edu", func() time.Time { return at }),
		)
		if err != nil {
			t.Fatal(err)
		}
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
		_, err := service.Start(context.Background(), command)
		if !Is(err, "authentication.desktop_authorization.unavailable") {
			t.Fatalf("Start error = %v", err)
		}
	})

	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: model.SessionClientWeb, AuthenticatedAt: at}
	t.Run("issued callback", func(t *testing.T) {
		auditor := &recordingDesktopAuthorizationAuditor{}
		service := newService(&desktopAuthorizationStoreFake{issueResult: &store.DesktopAuthorizationCodeIssued{
			CallbackURL: "https://not-loopback.example/", CodeExpiresAt: at.Add(time.Minute),
		}}, auditor)
		_, err := service.Approve(context.Background(), NewInvocation(principal, model.RequestMetadata{}),
			ApproveDesktopAuthorizationCommand{Binding: model.NewCredentialToken(), State: model.NewCredentialToken()})
		if !Is(err, "authentication.desktop_authorization.unavailable") || auditor.failureCode != "authentication.desktop_authorization.unavailable" {
			t.Fatalf("Approve error/audit = %v/%q", err, auditor.failureCode)
		}
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
		_, err := service.Exchange(context.Background(), Invocation{}, exchangeCommand)
		if !Is(err, "authentication.desktop_authorization.unavailable") || auditor.failureCode != "authentication.desktop_authorization.unavailable" {
			t.Fatalf("Exchange error/audit = %v/%q", err, auditor.failureCode)
		}
	})
}

func TestDesktopAuthorizationPublicAttemptLimitsPrecedePersistenceAndAudit(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	persistence := &desktopAuthorizationStoreFake{}
	auditor := &recordingDesktopAuthorizationAuditor{}
	limiter := &desktopAuthorizationAttemptLimiterFake{err: NewError("authentication.rate_limited")}
	service, err := newDesktopAuthorizationService(
		persistence,
		desktopAuthorizationInstitutionStoreFake{institution: &model.Institution{ID: model.NewInstitutionID()}},
		desktopAuthorizationAccessPolicyFake{enabled: true},
		&accessPolicyCapabilitiesFake{snapshot: AccessPolicyCapabilitySnapshot{}},
		auditor, limiter, testDesktopSessionPolicy(),
		DesktopAuthorizationPolicy{Issuer: "https://proctor.example.edu"},
		model.NewCredentialToken, func() time.Time { return at },
		testDesktopAuthorizationIdentity(t, "https://proctor.example.edu", func() time.Time { return at }),
	)
	if err != nil {
		t.Fatal(err)
	}
	state := model.NewCredentialToken()
	command := StartDesktopAuthorizationCommand{
		CallbackURL: "http://127.0.0.1:49152/" + model.NewCredentialToken(), State: state,
		CodeChallenge: model.NewCredentialToken(), Source: "192.0.2.10:5000",
	}
	setTestDesktopAuthorizationBuild(t, &command)
	_, err = service.Start(context.Background(), command)
	if !Is(err, "authentication.rate_limited") || persistence.created != nil || auditor.prepareExchange != 0 || auditor.prepareIssue != 0 {
		t.Fatalf("Start() error=%v persisted=%#v audit prepares=%d/%d", err, persistence.created, auditor.prepareIssue, auditor.prepareExchange)
	}
	if limiter.operation != desktopAuthorizationAttemptStart || limiter.identity != model.HashToken(state) || limiter.source != "192.0.2.10:5000" {
		t.Fatalf("start attempt = %#v", limiter)
	}

	code := model.NewCredentialToken()
	exchangeCommand := testDesktopAuthorizationExchangeCommand(
		t, service, at, code, model.NewCredentialToken(), model.NewCredentialToken(), "192.0.2.10:5001",
	)
	_, err = service.Exchange(context.Background(), Invocation{}, exchangeCommand)
	if !Is(err, "authentication.rate_limited") || persistence.exchanged != nil || auditor.prepareExchange != 0 {
		t.Fatalf("Exchange() error=%v persisted=%#v exchange audits=%d", err, persistence.exchanged, auditor.prepareExchange)
	}
	if limiter.operation != desktopAuthorizationAttemptExchange || limiter.identity != model.HashToken(code) || limiter.source != "192.0.2.10:5001" {
		t.Fatalf("exchange attempt = %#v", limiter)
	}
}

func TestDesktopAuthorizationAttemptAccountingSeparatesStartAndExchange(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	accounting, err := newAuthenticationAttemptAccounting(newExpiringAuthenticationAttemptCache(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	limiter := desktopAuthorizationAttemptAccounting{attempts: accounting, policy: LoginRateLimitPolicy{
		Window: time.Minute, MaximumAttempts: 1, MaximumSourceAttempts: 2,
	}}
	identity := model.HashToken(model.NewCredentialToken())
	if err = limiter.Check(context.Background(), desktopAuthorizationAttemptStart, identity, "192.0.2.20:1"); err != nil {
		t.Fatalf("first start: %v", err)
	}
	if err = limiter.Check(context.Background(), desktopAuthorizationAttemptStart, identity, "192.0.2.20:1"); !Is(err, "authentication.rate_limited") {
		t.Fatalf("repeated start error = %v", err)
	}
	if err = limiter.Check(context.Background(), desktopAuthorizationAttemptExchange, identity, "192.0.2.20:1"); err != nil {
		t.Fatalf("exchange must use a distinct domain: %v", err)
	}
}

func testDesktopSessionPolicy() SessionPolicy {
	return SessionPolicy{AccessTTL: 5 * time.Minute, RefreshTTL: time.Hour, IdleTTL: 30 * time.Minute,
		AbsoluteTTL: 2 * time.Hour, MaximumPerUser: 10}
}

type desktopAuthorizationStoreFake struct {
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
	authorizationID model.BrowserAuthenticationTransactionID
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

func (s *desktopAuthorizationStoreFake) GetDesktopAuthorizationContext(context.Context, string) (*store.DesktopAuthorizationContext, error) {
	if s.contextResult != nil {
		return s.contextResult, nil
	}
	return &store.DesktopAuthorizationContext{ID: model.NewBrowserAuthenticationTransactionID(),
		State: model.BrowserAuthenticationStateAuthenticated, UserID: model.NewUserID(), ExpiresAt: time.Now().Add(time.Minute)}, nil
}

func (s *desktopAuthorizationStoreFake) AuthenticateDesktopAuthorization(context.Context, *store.DesktopAuthorizationAuthentication) (*store.DesktopAuthorizationAuthenticationResult, error) {
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
) (model.BrowserAuthenticationTransactionID, error) {
	if s.authorizationID.IsZero() {
		s.authorizationID = model.NewBrowserAuthenticationTransactionID()
	}
	return s.authorizationID, nil
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

func (desktopAuthorizationAuditorFake) PrepareIssue(context.Context, model.UserID, model.InstitutionID) (*model.AuditEvent, error) {
	return &model.AuditEvent{ID: model.NewAuditEventID()}, nil
}
func (desktopAuthorizationAuditorFake) PrepareExchange(context.Context, Invocation, model.InstitutionID) (*model.AuditEvent, error) {
	return &model.AuditEvent{ID: model.NewAuditEventID()}, nil
}
func (desktopAuthorizationAuditorFake) Fail(context.Context, string, string) error { return nil }

type recordingDesktopAuthorizationAuditor struct {
	desktopAuthorizationAuditorFake
	auditID                       string
	failureCode                   string
	prepareIssue, prepareExchange int
}

func (a *recordingDesktopAuthorizationAuditor) PrepareIssue(context.Context, model.UserID, model.InstitutionID) (*model.AuditEvent, error) {
	a.prepareIssue++
	return &model.AuditEvent{ID: model.NewAuditEventID()}, nil
}

func (a *recordingDesktopAuthorizationAuditor) PrepareExchange(context.Context, Invocation, model.InstitutionID) (*model.AuditEvent, error) {
	a.prepareExchange++
	return &model.AuditEvent{ID: model.NewAuditEventID()}, nil
}

func (a *recordingDesktopAuthorizationAuditor) Fail(_ context.Context, auditID, errorCode string) error {
	a.auditID, a.failureCode = auditID, errorCode
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

func (f desktopAuthorizationCompatibilityFake) Evaluate(context.Context, DesktopCompatibilityQuery) (DesktopCompatibilityResult, error) {
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

func (stub *desktopAuthorizationSessionStoreStub) Revoke(_ context.Context, sessionID, userID string, _ int64,
	reason model.SessionRevocationReason,
) ([]string, error) {
	stub.revokedID, stub.revokedUserID, stub.reason = sessionID, userID, reason
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
	security, err := newDPoPSecurity(newDPoPCacheFake(), dpopPolicy{
		Origin: origin, NonceLifetime: 5 * time.Minute, ProofLifetime: 5 * time.Minute,
		ClockSkew: time.Minute, NewNonce: model.NewCredentialToken, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return desktopAuthorizationIdentityDependencies{
		users: desktopAuthorizationUserStoreStub{}, authentication: &authenticationService{
			sessions: &desktopAuthorizationSessionStoreStub{}, securityEffects: discardAuthenticationSecurityEffects{},
		},
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
	if err != nil {
		t.Fatal(err)
	}
	authorizationID, err := service.transactions.ResolveDesktopAuthorizationExchange(context.Background(),
		&store.DesktopAuthorizationExchangeProof{CodeHash: model.HashToken(code)})
	if err != nil {
		t.Fatal(err)
	}
	binding := dpopBinding{
		Kind: dpopBindingAuthorization, AuthorizationID: authorizationID,
		KeyThumbprint: thumbprint, Origin: service.dpop.policy.Origin,
	}
	nonce, err := service.dpop.IssueNonce(context.Background(), binding)
	if err != nil {
		t.Fatal(err)
	}
	proof := signDPoPProof(t, privateKey, publicJWK, map[string]any{
		"jti": model.NewCredentialToken(), "htm": "POST",
		"htu": service.dpop.policy.Origin + "/api/v1/auth/desktop/token",
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
	if err != nil {
		t.Fatal(err)
	}
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
