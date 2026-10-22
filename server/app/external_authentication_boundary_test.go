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
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestExternalAuthenticationRetainsExactPersistenceContracts(t *testing.T) {
	t.Parallel()

	root := reflect.TypeOf((*store.Store)(nil)).Elem()
	typeOfService := reflect.TypeOf(externalAuthenticationService{})
	for index := 0; index < typeOfService.NumField(); index++ {
		require.NotEqual(t, root, typeOfService.Field(index).Type, "externalAuthenticationService retains root Store in")
	}
}

func TestExternalAuthenticationBeginUsesControlledCredentials(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	stateToken := "MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE"
	bindingToken := "YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXpBQkNERUY"
	generated := []string{stateToken, bindingToken}
	next := 0
	databaseAt := at.Add(-2 * time.Hour)
	states := &externalLoginStateStoreFake{storeNow: databaseAt}
	attempts, err := newAuthenticationAttemptAccounting(authenticationAttemptAccountingDependencies{Cache: newAuthenticationCacheFake()})
	require.NoError(t, err)
	service := &externalAuthenticationService{deps: externalAuthenticationServiceDependencies{Registry: externalProviderSourceFake{provider: externalProviderFake{}}, LoginStates: states, AccessPolicy: allowAllAuthenticationAccessPolicy(), Attempts: attempts, Policy: ExternalAuthenticationPolicy{
		PublicURL: "https://proctor.example.test", LoginStateTTL: 10 * time.Minute,
		LoginRateLimit: LoginRateLimitPolicy{Window: time.Minute, MaximumSourceAttempts: 10},
	}, NewCredential: func() string { value := generated[next]; next++; return value }, Now: func() time.Time { return at }}}

	result, err := service.begin(
		context.Background(), "campus", "/after", model.SessionClientWeb,
		"browser", "Browser", "127.0.0.1",
	)
	require.NoError(t, err)
	require.Equal(t, bindingToken, result.Binding, "result=%#v saved", result)
	require.NotNil(t, states.saved, "result=%#v saved", result)
	require.Equal(t, model.HashToken(stateToken), states.saved.StateHash, "result=%#v saved", result)
	require.Equal(t, model.HashToken(bindingToken), states.saved.BindingHash, "result=%#v saved", result)
	require.True(t, states.saved.CreatedAt.IsZero(), "result=%#v saved", result)
	require.True(t, states.saved.ExpiresAt.IsZero(), "result=%#v saved", result)
	require.Equal(t, 10*time.Minute, states.saveLifetime, "result=%#v saved", result)
	require.Equal(t, databaseAt.Add(10*time.Minute).UnixMilli(), result.ExpiresAt, "result=%#v saved", result)
}

func TestInvitationRequiredBeginHashesClaimIntoPurposeBoundState(t *testing.T) {
	t.Parallel()
	claim := model.NewCredentialToken()
	states := &externalLoginStateStoreFake{}
	service := externalAuthenticationBeginService(t, externalProviderSourceSet{
		provider: &recordingExternalProvider{}, ids: map[string]bool{"campus": true},
	}, newAuthenticationCacheFake(), 10)
	service.deps.LoginStates = states
	service.deps.AccessPolicy = authenticationAccessPolicyFake{providers: map[string]bool{"campus": true},
		providerModes: map[string]model.ProviderAdmissionMode{"campus": model.ProviderAdmissionInvitationRequired}}

	result, err := service.beginWithInvitationClaim(context.Background(), "campus", "/join", model.SessionClientWeb,
		"", "", "127.0.0.1", claim)
	require.NoError(t, err)
	require.NotNil(t, result, "result")
	require.NotNil(t, states.saved, "result")
	require.Equal(t, model.ExternalAuthenticationPurposeInvitationAdmission, states.saved.Purpose, "result")
	require.Equal(t, model.HashInvitationClaim(claim), states.admissionClaimHash, "result")
	require.NotContains(t, states.saved.StateHash, claim, "raw Invitation claim escaped into durable/provider state")
	require.NotContains(t, result.RedirectURL, claim, "raw Invitation claim escaped into durable/provider state")
}

func TestInvitationRequiredBeginRejectsWhenInvitationAdmissionIsDisabled(t *testing.T) {
	t.Parallel()

	provider := &recordingExternalProvider{}
	service := externalAuthenticationBeginService(t, externalProviderSourceSet{
		provider: provider, ids: map[string]bool{"campus": true},
	}, newAuthenticationCacheFake(), 10)
	service.deps.AccessPolicy = authenticationAccessPolicyFake{
		providers:                   map[string]bool{"campus": true},
		providerModes:               map[string]model.ProviderAdmissionMode{"campus": model.ProviderAdmissionInvitationRequired},
		invitationAdmissionDisabled: true,
	}

	result, err := service.beginWithInvitationClaim(
		context.Background(), "campus", "/join", model.SessionClientWeb,
		"", "", "192.0.2.9", model.NewCredentialToken(),
	)
	require.Nil(t, result, "result=%#v err=%v", result, err)
	require.True(t, apperror.Is(err, "authentication.external.account_not_linked"), "result=%#v err=%v", result, err)
	require.Equal(t, 0, provider.beginCalls, "disabled invitation admission began")
}

func TestExternalAuthenticationOrdinaryLoginRejectsDesktopBeforeProviderOrPersistence(t *testing.T) {
	t.Parallel()

	provider := &recordingExternalProvider{}
	states := &externalLoginStateStoreFake{}
	service := externalAuthenticationBeginService(t, externalProviderSourceSet{
		provider: provider, ids: map[string]bool{"campus": true},
	}, newAuthenticationCacheFake(), 10)
	service.deps.LoginStates = states

	result, err := service.begin(context.Background(), "campus", "/", model.SessionClientDesktop, "desktop-1", "Desktop", "127.0.0.1")
	require.Nil(t, result, "desktop ordinary login result=%#v err=%v", result, err)
	require.True(t, apperror.Is(err, "authentication.external.request.invalid"), "desktop ordinary login result=%#v err=%v", result, err)
	require.Equal(t, 0, provider.beginCalls, "desktop ordinary login reached provider/persistence: provider")
	require.Nil(t, states.saved, "desktop ordinary login reached provider/persistence: provider")
}

func TestProviderConnectionStartRequiresStrongRecentAndBindsExactUser(t *testing.T) {
	now := time.UnixMilli(30_000)
	states := &externalLoginStateStoreFake{}
	events := []string{}
	auditor := &mutationAttemptAuditorFake{events: &events, beginID: model.NewAuditEventID().String()}
	provider := &recordingExternalProvider{}
	service := externalAuthenticationBeginService(t, externalProviderSourceSet{provider: provider, ids: map[string]bool{"campus": true}}, newAuthenticationCacheFake(), 10)
	service.deps.LoginStates, service.deps.MutationAudit = states, auditor
	service.deps.Capabilities = &accessPolicyCapabilitiesFake{snapshot: AccessPolicyCapabilitySnapshot{Providers: []AccessPolicyProviderCapability{{Descriptor: provider.Descriptor()}}}}
	service.deps.RecentAuthenticationTTL, service.deps.Now = 15*time.Minute, func() time.Time { return now }
	application := &App{externalAuthenticationService: service}
	principal := userSettingsSessionPrincipal(now)
	principal.AuthenticationStrength = model.AuthenticationMultiFactor
	principal.MFACompletedAt = model.OptionalTimeFrom(now)
	_, err := application.BeginProviderConnection(context.Background(), invocation.New(principal, model.RequestMetadata{}), BeginProviderConnectionCommand{ProviderID: "campus", ReturnTo: "/account/security", Source: "127.0.0.1"})
	require.NoError(t, err)
	require.NotNil(t, states.saved)
	require.Equal(t, model.ExternalAuthenticationPurposeConnect, states.saved.Purpose)
	require.Equal(t, principal.UserID, states.saved.TargetUserID)
	require.Equal(t, auditor.beginID, states.saved.AuditEventID)
	weak := principal
	weak.AuthenticationStrength = model.AuthenticationSingleFactor
	weak.MFACompletedAt = model.OptionalTime{}
	states.saved = nil
	_, err = application.BeginProviderConnection(context.Background(), invocation.New(weak, model.RequestMetadata{}), BeginProviderConnectionCommand{ProviderID: "campus"})
	require.True(t, apperror.Is(err, "authentication.strong_required"), "weak start error = %v", err)
	require.Nil(t, states.saved, "weak start persisted state")
}

func TestProviderConnectionCallbackLinksOnlyTransactionUserWithoutSession(t *testing.T) {
	for _, nodeOffset := range []time.Duration{-2 * time.Hour, 2 * time.Hour} {
		t.Run(nodeOffset.String(), func(t *testing.T) {
			databaseNow := time.UnixMilli(40_000)
			nodeNow := databaseNow.Add(nodeOffset)
			stateToken, bindingToken := model.NewCredentialToken(), model.NewCredentialToken()
			target := model.NewUserID()
			state := &model.ExternalLoginState{Provider: "campus", Purpose: model.ExternalAuthenticationPurposeConnect,
				TargetUserID: target, AuditEventID: model.NewAuditEventID().String(), StateHash: model.HashToken(stateToken),
				BindingHash: model.HashToken(bindingToken), ReturnTo: "/account/security", ClientType: model.SessionClientWeb,
				ExpiresAt: databaseNow.Add(time.Minute)}
			states := &externalLoginStateStoreFake{get: state, consumeResult: state, storeNow: databaseNow}
			identities := &providerConnectionIdentityStoreFake{}
			institution, err := model.NewInstitution(model.NewInstitutionID(), "northbridge", "Northbridge", "", databaseNow)
			require.NoError(t, err)
			issuer := &providerConnectionSessionIssuerFake{}
			provider := providerConnectionProvider{state: stateToken, assertion: &model.ExternalAuthenticationAssertion{ProviderID: "campus", Subject: "opaque-subject-never-exposed"}}
			service := &externalAuthenticationService{deps: externalAuthenticationServiceDependencies{Registry: externalProviderSourceFake{provider: provider}, LoginStates: states, Institutions: providerConnectionInstitutionStoreFake{institution: institution}, Identities: identities, AccessPolicy: allowAllAuthenticationAccessPolicy(), Authentication: issuer, MutationAudit: &accessPolicyAuditFake{}, Capabilities: &accessPolicyCapabilitiesFake{snapshot: AccessPolicyCapabilitySnapshot{Providers: []AccessPolicyProviderCapability{{Descriptor: provider.Descriptor()}}}}, Policy: ExternalAuthenticationPolicy{PublicURL: "https://proctor.example.test"}, Now: func() time.Time { return nodeNow }}}
			completion, err := service.complete(context.Background(), "campus", bindingToken, model.ExternalAuthenticationCallback{}, model.RequestMetadata{})
			require.NoError(t, err)
			require.NotNil(t, identities.linked)
			require.Equal(t, target, identities.linked.Identity.UserID)
			require.Equal(t, provider.assertion.Subject, identities.linked.Identity.Subject)
			require.Equal(t, model.AuditEventID(state.AuditEventID), identities.linked.AuditEventID)
			require.Equal(t, databaseNow.UnixMilli(), identities.linked.AuditAt)
			require.True(t, identities.linked.Identity.LastSeenAt.Valid)
			require.True(t, identities.linked.Identity.LastSeenAt.Time.Equal(databaseNow))
			require.Equal(t, 1, states.consumeCalls, "completion=%#v consume calls=%d session calls", completion, states.consumeCalls)
			require.Equal(t, 0, issuer.calls, "completion=%#v consume calls=%d session calls", completion, states.consumeCalls)
			require.Nil(t, completion.Tokens, "completion=%#v consume calls=%d session calls", completion, states.consumeCalls)
			require.Nil(t, completion.Session, "completion=%#v consume calls=%d session calls", completion, states.consumeCalls)
			require.Equal(t, state.ReturnTo, completion.ReturnTo, "completion=%#v consume calls=%d session calls", completion, states.consumeCalls)
		})
	}
}

func TestProviderConnectionCallbackTerminalizesAttemptWhenProviderRejectsAfterConsumption(t *testing.T) {
	now := time.UnixMilli(45_000)
	stateToken, bindingToken := model.NewCredentialToken(), model.NewCredentialToken()
	auditID := model.NewAuditEventID().String()
	state := &model.ExternalLoginState{Provider: "campus", Purpose: model.ExternalAuthenticationPurposeConnect,
		TargetUserID: model.NewUserID(), AuditEventID: auditID, StateHash: model.HashToken(stateToken),
		BindingHash: model.HashToken(bindingToken), ReturnTo: "/account/security", ClientType: model.SessionClientWeb,
		ExpiresAt: now.Add(time.Minute)}
	events := []string{}
	auditor := &mutationAttemptAuditorFake{events: &events}
	institution, err := model.NewInstitution(model.NewInstitutionID(), "northbridge", "Northbridge", "", now)
	require.NoError(t, err)
	provider := providerConnectionProvider{state: stateToken, completeErr: ErrExternalAuthenticationRejected}
	service := &externalAuthenticationService{deps: externalAuthenticationServiceDependencies{Registry: externalProviderSourceFake{provider: provider}, LoginStates: &externalLoginStateStoreFake{get: state, consumeResult: state}, Institutions: providerConnectionInstitutionStoreFake{institution: institution}, AccessPolicy: allowAllAuthenticationAccessPolicy(), Audit: externalAuditFake{}, MutationAudit: auditor, Policy: ExternalAuthenticationPolicy{PublicURL: "https://proctor.example.test"}, Now: func() time.Time { return now }}}

	result, err := service.complete(context.Background(), "campus", bindingToken, model.ExternalAuthenticationCallback{}, model.RequestMetadata{})
	require.Nil(t, result, "completion result=%#v err=%v", result, err)
	require.True(t, apperror.Is(err, "authentication.external.rejected"), "completion result=%#v err=%v", result, err)
	require.Equal(t, auditID, auditor.failID, "terminal audit id")
	require.Equal(t, "authentication.external.rejected", auditor.failCode, "terminal audit id")
}

func TestOrdinaryExternalAuthenticationDoesNotPrepareProvisioningOutsideAutoProvisionMode(t *testing.T) {
	for _, mode := range []model.ProviderAdmissionMode{
		model.ProviderAdmissionLinkedOnly,
		model.ProviderAdmissionInvitationRequired,
	} {
		t.Run(string(mode), func(t *testing.T) {
			databaseNow := time.UnixMilli(50_000)
			stateToken, bindingToken := model.NewCredentialToken(), model.NewCredentialToken()
			state := &model.ExternalLoginState{Provider: "campus", Purpose: model.ExternalAuthenticationPurposeLogin,
				StateHash: model.HashToken(stateToken), BindingHash: model.HashToken(bindingToken), ReturnTo: "/",
				ClientType: model.SessionClientWeb, ExpiresAt: databaseNow.Add(time.Minute)}
			identities := &externalIdentityResolutionStoreFake{}
			institution, err := model.NewInstitution(model.NewInstitutionID(), "northbridge", "Northbridge", "", databaseNow)
			require.NoError(t, err)
			provider := admissionModeProvider{providerConnectionProvider: providerConnectionProvider{
				state: stateToken, assertion: &model.ExternalAuthenticationAssertion{ProviderID: "campus", Subject: "opaque-subject",
					Username: "eligible-user", Email: "eligible@example.edu"}}, autoProvision: true}
			service := &externalAuthenticationService{deps: externalAuthenticationServiceDependencies{Registry: externalProviderSourceFake{provider: provider}, LoginStates: &externalLoginStateStoreFake{get: state, consumeResult: state, storeNow: databaseNow}, Institutions: providerConnectionInstitutionStoreFake{institution: institution}, Identities: identities, AccessPolicy: authenticationAccessPolicyFake{providers: map[string]bool{"campus": true},
				providerModes: map[string]model.ProviderAdmissionMode{"campus": mode}}, Audit: externalAuditFake{}, Capabilities: &accessPolicyCapabilitiesFake{snapshot: AccessPolicyCapabilitySnapshot{
				Providers: []AccessPolicyProviderCapability{{Descriptor: provider.Descriptor(), AutoProvision: true}}}}, Policy: ExternalAuthenticationPolicy{PublicURL: "https://proctor.example.test"}, Now: func() time.Time { return databaseNow }}}

			_, err = service.complete(context.Background(), "campus", bindingToken, model.ExternalAuthenticationCallback{}, model.RequestMetadata{})
			require.True(t, apperror.Is(err, "authentication.external.account_not_linked"), "completion error = %v", err)
			require.NotNil(t, identities.resolution, "%s prepared a provisioning package", mode)
			require.Nil(t, identities.resolution.User, "%s prepared a provisioning package", mode)
			require.Nil(t, identities.resolution.Settings, "%s prepared a provisioning package", mode)
			require.Nil(t, identities.resolution.AuditEvent, "%s prepared a provisioning package", mode)
			require.Nil(t, identities.resolution.DefaultProfilePictureJob, "%s prepared a provisioning package", mode)
			capability, configured := identities.resolution.Capabilities.Providers["campus"]
			require.True(t, configured, "%s terminal capability snapshot = %#v", mode, identities.resolution.Capabilities)
			require.True(t, capability.AutoProvision, "%s terminal capability snapshot = %#v", mode, identities.resolution.Capabilities)
		})
	}
}

func TestInvitationAdmissionDelegatesExactProofWithoutCreatingOrdinarySession(t *testing.T) {
	t.Parallel()
	databaseNow := time.UnixMilli(55_000)
	stateToken, bindingToken := model.NewCredentialToken(), model.NewCredentialToken()
	invitationID := model.NewInvitationID()
	state := &model.ExternalLoginState{Provider: "campus", Purpose: model.ExternalAuthenticationPurposeInvitationAdmission,
		InvitationID: invitationID, StateHash: model.HashToken(stateToken), BindingHash: model.HashToken(bindingToken),
		ReturnTo: "/join", ClientType: model.SessionClientWeb, ExpiresAt: databaseNow.Add(time.Minute)}
	identities := &externalIdentityResolutionStoreFake{}
	acceptedUser := &model.User{ID: model.NewUserID(), Email: "invited@example.edu"}
	acceptor := &externalInvitationAcceptorFake{result: &store.ExternalIdentityInvitationAcceptanceResult{
		User: acceptedUser,
		Identity: &model.ExternalIdentity{ID: model.NewExternalIdentityID(), UserID: acceptedUser.ID,
			Provider: "campus", Subject: "opaque-subject"},
	}}
	institution, err := model.NewInstitution(model.NewInstitutionID(), "northbridge", "Northbridge", "", databaseNow)
	require.NoError(t, err)
	provider := admissionModeProvider{providerConnectionProvider: providerConnectionProvider{state: stateToken,
		assertion: &model.ExternalAuthenticationAssertion{ProviderID: "campus", Subject: "opaque-subject",
			Username: "invited-user", Email: " Invited@Example.EDU ", EmailVerified: true}}, autoProvision: false}
	service := &externalAuthenticationService{deps: externalAuthenticationServiceDependencies{Registry: externalProviderSourceFake{provider: provider}, LoginStates: &externalLoginStateStoreFake{get: state, consumeResult: state, storeNow: databaseNow}, Institutions: providerConnectionInstitutionStoreFake{institution: institution}, Identities: identities, InvitationAcceptor: acceptor, AccessPolicy: authenticationAccessPolicyFake{providers: map[string]bool{"campus": true},
		providerModes: map[string]model.ProviderAdmissionMode{"campus": model.ProviderAdmissionInvitationRequired}}, Audit: externalAuditFake{}, Capabilities: &accessPolicyCapabilitiesFake{snapshot: AccessPolicyCapabilitySnapshot{
		Providers: []AccessPolicyProviderCapability{{Descriptor: provider.Descriptor()}}}}, Policy: ExternalAuthenticationPolicy{PublicURL: "https://proctor.example.test", NodeID: "node-a"}, Now: func() time.Time { return databaseNow }}}

	result, err := service.complete(context.Background(), "campus", bindingToken, model.ExternalAuthenticationCallback{}, model.RequestMetadata{})
	require.NoError(t, err, "completion result")
	require.NotNil(t, result, "completion result")
	require.Same(t, acceptedUser, result.User, "completion result")
	require.Nil(t, result.Session, "completion result")
	require.Nil(t, result.Tokens, "completion result")
	require.Equal(t, "/join", result.ReturnTo, "completion result")
	require.NotNil(t, acceptor.state, "terminal acceptance state")
	require.Same(t, provider.assertion, acceptor.assertion, "terminal acceptance state")
	require.Equal(t, "oidc", acceptor.method, "terminal acceptance state")
	require.Equal(t, invitationID, acceptor.state.InvitationID, "terminal acceptance state")
	require.True(t, acceptor.state.ConsumedAt.Valid, "terminal acceptance state")
	require.Nil(t, identities.resolution)
	secondStateToken, secondBindingToken := model.NewCredentialToken(), model.NewCredentialToken()
	secondState := *state
	secondState.StateHash, secondState.BindingHash = model.HashToken(secondStateToken), model.HashToken(secondBindingToken)
	secondState.ConsumedAt = model.OptionalTime{}
	secondProvider := provider
	secondProvider.state = secondStateToken
	failureCodes := []string{}
	acceptor.result, acceptor.err, acceptor.state = nil, store.ErrAuthenticationMethodDisabled, nil
	service.deps.Registry = externalProviderSourceFake{provider: secondProvider}
	service.deps.LoginStates = &externalLoginStateStoreFake{get: &secondState, consumeResult: &secondState, storeNow: databaseNow}
	service.deps.Audit = externalAuditFake{failureCodes: &failureCodes}
	result, err = service.complete(context.Background(), "campus", secondBindingToken, model.ExternalAuthenticationCallback{}, model.RequestMetadata{})
	require.Nil(t, result, "terminal provider removal result=%#v error=%v failure_audits=%v", result, err, failureCodes)
	require.True(t, apperror.Is(err, "authentication.external.invalid"), "terminal provider removal result=%#v error=%v failure_audits=%v", result, err, failureCodes)
	require.True(t, slices.Equal(failureCodes, []string{"authentication.external.invalid"}), "terminal provider removal result=%#v error=%v failure_audits=%v", result, err, failureCodes)
}

func TestInvitationAdmissionCallbackRejectsWhenGloballyDisabledAfterStart(t *testing.T) {
	t.Parallel()

	databaseNow := time.UnixMilli(56_000)
	stateToken, bindingToken := model.NewCredentialToken(), model.NewCredentialToken()
	invitationID := model.NewInvitationID()
	state := &model.ExternalLoginState{
		Provider:     "campus",
		Purpose:      model.ExternalAuthenticationPurposeInvitationAdmission,
		InvitationID: invitationID,
		StateHash:    model.HashToken(stateToken),
		BindingHash:  model.HashToken(bindingToken),
		ReturnTo:     "/join",
		ClientType:   model.SessionClientWeb,
		ExpiresAt:    databaseNow.Add(time.Minute),
	}
	identities := &externalIdentityResolutionStoreFake{}
	acceptor := &externalInvitationAcceptorFake{}
	failureCodes := []string{}
	institution, err := model.NewInstitution(model.NewInstitutionID(), "northbridge", "Northbridge", "", databaseNow)
	require.NoError(t, err)
	provider := admissionModeProvider{providerConnectionProvider: providerConnectionProvider{
		state: stateToken,
		assertion: &model.ExternalAuthenticationAssertion{
			ProviderID: "campus", Subject: "private-subject", Username: "invited-user",
			Email: "invited@example.edu", EmailVerified: true,
		},
	}}
	service := &externalAuthenticationService{deps: externalAuthenticationServiceDependencies{Registry: externalProviderSourceFake{provider: provider}, LoginStates: &externalLoginStateStoreFake{get: state, consumeResult: state, storeNow: databaseNow}, Institutions: providerConnectionInstitutionStoreFake{institution: institution}, Identities: identities, InvitationAcceptor: acceptor, AccessPolicy: authenticationAccessPolicyFake{
		providers:                   map[string]bool{"campus": true},
		providerModes:               map[string]model.ProviderAdmissionMode{"campus": model.ProviderAdmissionInvitationRequired},
		invitationAdmissionDisabled: true,
	}, Audit: externalAuditFake{failureCodes: &failureCodes}, Capabilities: &accessPolicyCapabilitiesFake{snapshot: AccessPolicyCapabilitySnapshot{
		Providers: []AccessPolicyProviderCapability{{Descriptor: provider.Descriptor()}},
	}}, Policy: ExternalAuthenticationPolicy{PublicURL: "https://proctor.example.test", NodeID: "node-b"}, Now: func() time.Time { return databaseNow }}}

	result, err := service.complete(context.Background(), "campus", bindingToken, model.ExternalAuthenticationCallback{}, model.RequestMetadata{})
	require.Nil(t, result, "result=%#v err=%v", result, err)
	require.True(t, apperror.Is(err, "authentication.external.invalid"), "result=%#v err=%v", result, err)
	require.Nil(t, identities.resolution, "disabled admission reached terminal stores: resolution")
	require.Nil(t, acceptor.state, "disabled admission reached terminal stores: resolution")
	require.True(t, slices.Equal(failureCodes, []string{"authentication.external.invalid"}), "disabled admission failure audits = %v", failureCodes)
	require.NotContains(t, err.Error(), invitationID.String())
	require.NotContains(t, err.Error(), "private-subject")
	require.NotContains(t, err.Error(), "invited@example.edu")
}

func TestExternalAuthenticationCallbackRejectsLegacyDesktopStateBeforeConsumption(t *testing.T) {
	t.Parallel()

	stateToken := model.NewCredentialToken()
	bindingToken := model.NewCredentialToken()
	states := &externalLoginStateStoreFake{get: &model.ExternalLoginState{
		Provider: "campus", StateHash: model.HashToken(stateToken), BindingHash: model.HashToken(bindingToken),
		ReturnTo: "/", ClientType: model.SessionClientDesktop, ExpiresAt: time.Now().Add(time.Minute),
	}}
	service := &externalAuthenticationService{deps: externalAuthenticationServiceDependencies{Registry: externalProviderSourceFake{provider: desktopCallbackExternalProvider{state: stateToken}}, LoginStates: states, AccessPolicy: allowAllAuthenticationAccessPolicy(), Now: time.Now}}
	result, err := service.complete(context.Background(), "campus", bindingToken,
		model.ExternalAuthenticationCallback{Values: map[string][]string{"code": {"accepted"}}}, model.RequestMetadata{})
	require.Nil(t, result, "legacy desktop callback result=%#v err=%v", result, err)
	require.True(t, apperror.Is(err, "authentication.external.invalid"), "legacy desktop callback result=%#v err=%v", result, err)
	require.Equal(t, 0, states.consumeCalls, "legacy desktop state was consumed")
}

func TestExternalAuthenticationBeginHidesProviderDisabledByCurrentPolicy(t *testing.T) {
	t.Parallel()

	provider := &recordingExternalProvider{}
	service := externalAuthenticationBeginService(t, externalProviderSourceSet{
		provider: provider, ids: map[string]bool{"campus": true},
	}, newAuthenticationCacheFake(), 10)
	service.deps.AccessPolicy = authenticationAccessPolicyFake{providers: map[string]bool{}}
	result, err := service.begin(context.Background(), "campus", "/", model.SessionClientWeb, "", "", "192.0.2.9")
	require.Nil(t, result, "result=%#v err=%v", result, err)
	require.True(t, apperror.Is(err, "authentication.external.provider_not_found"), "result=%#v err=%v", result, err)
	require.Equal(t, 0, provider.beginCalls, "disabled provider began")
}

func TestExternalAuthenticationProviderListIncludesOnlyCurrentPolicySelections(t *testing.T) {
	t.Parallel()

	configured := []model.ExternalAuthenticationProvider{
		{ID: "campus-a", DisplayName: "Campus A", Type: "oidc"},
		{ID: "campus-b", DisplayName: "Campus B", Type: "cas"},
	}
	service := &externalAuthenticationService{deps: externalAuthenticationServiceDependencies{Registry: externalProviderSourceSet{descriptors: configured}, AccessPolicy: authenticationAccessPolicyFake{providers: map[string]bool{"campus-b": true}}}}
	providers, err := service.ExternalAuthenticationProviders(context.Background())
	require.NoError(t, err)
	require.Equal(t, configured[1:], providers, "available providers")
}

func TestExternalAuthenticationRequiresFocusedDependencies(t *testing.T) {
	t.Parallel()

	valid := externalAuthenticationConstructorArgs{
		registry:    externalProviderSourceFake{provider: externalProviderFake{}},
		loginStates: &externalLoginStateStoreFake{}, institutions: externalInstitutionStoreFake{},
		identities: externalIdentityStoreFake{}, sessions: externalSessionStoreFake{},
		attempts:    newExternalAuthenticationAttempts(t, newAuthenticationCacheFake()),
		issuer:      externalSessionIssuerFake{},
		invalidator: externalInvalidatorFake{}, audit: externalAuditFake{},
		diagnostics: &securityEffectsDiagnosticsFake{}, newCredential: model.NewCredentialToken,
		acceptor: &externalInvitationAcceptorFake{},
		desktop:  &externalDesktopAuthorizationFake{},
		now:      time.Now,
	}
	_, err := valid.build()
	require.NoError(t, err)
	tests := []struct {
		name  string
		clear func(*externalAuthenticationConstructorArgs)
	}{
		{"login states", func(a *externalAuthenticationConstructorArgs) { a.loginStates = nil }},
		{"institutions", func(a *externalAuthenticationConstructorArgs) { a.institutions = nil }},
		{"identities", func(a *externalAuthenticationConstructorArgs) { a.identities = nil }},
		{"sessions", func(a *externalAuthenticationConstructorArgs) { a.sessions = nil }},
		{"attempt accounting", func(a *externalAuthenticationConstructorArgs) { a.attempts = nil }},
		{"Invitation acceptor", func(a *externalAuthenticationConstructorArgs) { a.acceptor = nil }},
		{"Desktop authorization", func(a *externalAuthenticationConstructorArgs) { a.desktop = nil }},
		{"generator", func(a *externalAuthenticationConstructorArgs) { a.newCredential = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.clear(&candidate)
			_, err := candidate.build()
			require.Error(t, err, "missing %s accepted", test.name)
		})
	}
}

type externalAuthenticationConstructorArgs struct {
	registry      externalProviderSource
	loginStates   store.ExternalLoginStateStore
	institutions  store.InstitutionStore
	identities    store.ExternalIdentityStore
	sessions      store.SessionStore
	attempts      *authenticationAttemptAccounting
	issuer        authenticationSessionIssuer
	invalidator   realtime.AuthenticationInvalidator
	audit         externalAuthenticationAudit
	diagnostics   authenticationDiagnostics
	acceptor      externalInvitationAcceptor
	desktop       externalAuthenticationDesktopAuthorization
	newCredential func() string
	now           func() time.Time
}

func (a externalAuthenticationConstructorArgs) build() (*externalAuthenticationService, error) {
	events := []string{}
	return newExternalAuthenticationService(externalAuthenticationServiceDependencies{Registry: a.registry, LoginStates: a.loginStates, Institutions: a.institutions, Identities: a.identities, Sessions: a.sessions, AccessPolicy: allowAllAuthenticationAccessPolicy(), Attempts: a.attempts, Authentication: a.issuer, Invalidator: a.invalidator, Audit: a.audit, MutationAudit: &mutationAttemptAuditorFake{events: &events, beginID: model.NewAuditEventID().String()}, Capabilities: &accessPolicyCapabilitiesFake{}, InvitationAcceptor: a.acceptor, DesktopAuthorization: a.desktop, Policy: ExternalAuthenticationPolicy{}, RecentAuthenticationTTL: 15 * time.Minute, Diagnostics: a.diagnostics, NewCredential: a.newCredential, Now: a.now})
}

func TestExternalAuthenticationInitiationUsesProviderScopedSourceAccounting(t *testing.T) {
	t.Parallel()

	cache := newAuthenticationCacheFake()
	provider := &recordingExternalProvider{}
	service := externalAuthenticationBeginService(
		t,
		externalProviderSourceSet{provider: provider, ids: map[string]bool{
			"campus-a": true,
			"campus-b": true,
		}},
		cache,
		1,
	)

	_, err := service.begin(
		context.Background(), "campus-a", "/", model.SessionClientWeb,
		"", "", "192.0.2.10:4000",
	)
	require.NoError(t, err)
	_, err = service.begin(
		context.Background(), " CAMPUS-A ", "/", model.SessionClientWeb,
		"", "", "192.0.2.10:5000",
	)
	require.True(t, apperror.Is(err, "authentication.rate_limited"), "normalized-source threshold error = %v", err)
	_, err = service.begin(
		context.Background(), "campus-b", "/", model.SessionClientWeb,
		"", "", "192.0.2.10:6000",
	)
	require.NoError(t, err)

	cache.mu.Lock()
	defer cache.mu.Unlock()
	require.Len(t, cache.counters, 2, "counter keys")
	for key := range cache.counters {
		require.True(t, strings.HasPrefix(key, "authentication/attempts/external-authentication/source/"), "unexpected counter key %q", key)
		require.NotContains(t, key, "campus")
		require.NotContains(t, key, "192.0.2.10")
	}
}

func TestExternalAuthenticationInitiationAllowsMaximumThenLimitsNext(t *testing.T) {
	t.Parallel()

	provider := &recordingExternalProvider{}
	service := externalAuthenticationBeginService(
		t,
		externalProviderSourceSet{provider: provider, ids: map[string]bool{"campus": true}},
		newAuthenticationCacheFake(),
		2,
	)
	for attempt := 1; attempt <= 2; attempt++ {
		_, err := service.begin(
			context.Background(), "campus", "/", model.SessionClientWeb,
			"", "", "198.51.100.7",
		)
		require.NoError(t, err, "attempt %d failed", attempt)
	}
	_, err := service.begin(
		context.Background(), "campus", "/", model.SessionClientWeb,
		"", "", "198.51.100.7",
	)
	require.True(t, apperror.Is(err, "authentication.rate_limited"), "attempt beyond maximum error = %v", err)
	require.Equal(t, 2, provider.beginCalls, "provider Begin calls")
}

func TestExternalAuthenticationInitiationPreservesValidationAndFailureOrdering(t *testing.T) {
	t.Parallel()

	cache := &externalAuthenticationAttemptFaultCache{
		authenticationCacheFake: newAuthenticationCacheFake(),
		err:                     errors.New("counter unavailable"),
	}
	provider := &recordingExternalProvider{}
	service := externalAuthenticationBeginService(
		t,
		externalProviderSourceSet{provider: provider, ids: map[string]bool{"campus": true}},
		cache,
		2,
	)

	_, err := service.begin(
		context.Background(), "missing", "/", model.SessionClientWeb,
		"", "", "203.0.113.8",
	)
	require.True(t, apperror.Is(err, "authentication.external.provider_not_found"), "missing-provider error = %v", err)
	require.Equal(t, 0, cache.addCalls, "provider validation occurred after")
	_, err = service.begin(
		context.Background(), "campus", "https://unsafe.example", model.SessionClientWeb,
		"", "", "203.0.113.8",
	)
	require.True(t, apperror.Is(err, "authentication.external.request.invalid"), "invalid-request error = %v", err)
	require.Equal(t, 0, cache.addCalls, "request validation occurred after")
	_, err = service.begin(
		context.Background(), "campus", "/", model.SessionClientWeb,
		"", "", "203.0.113.8",
	)
	require.True(t, apperror.Is(err, "authentication.rate_limit_unavailable"), "counter failure error = %v", err)
	require.Equal(t, 0, provider.beginCalls, "provider began before accounting succeeded")
}

func externalAuthenticationBeginService(
	t *testing.T,
	registry externalProviderSource,
	cache authenticationAttemptCache,
	maximum int,
) *externalAuthenticationService {
	t.Helper()
	attempts := newExternalAuthenticationAttempts(t, cache)
	return &externalAuthenticationService{deps: externalAuthenticationServiceDependencies{Registry: registry, LoginStates: &externalLoginStateStoreFake{}, AccessPolicy: allowAllAuthenticationAccessPolicy(), Attempts: attempts, Policy: ExternalAuthenticationPolicy{
		PublicURL:     "https://proctor.example.test",
		LoginStateTTL: 10 * time.Minute,
		LoginRateLimit: LoginRateLimitPolicy{
			Window: time.Minute, MaximumSourceAttempts: maximum,
		},
	}, NewCredential: model.NewCredentialToken, Now: time.Now}}
}

func newExternalAuthenticationAttempts(
	t *testing.T,
	cache authenticationAttemptCache,
) *authenticationAttemptAccounting {
	t.Helper()
	attempts, err := newAuthenticationAttemptAccounting(authenticationAttemptAccountingDependencies{Cache: cache})
	require.NoError(t, err)
	return attempts
}

type externalProviderSourceSet struct {
	provider    ExternalIdentityProvider
	ids         map[string]bool
	descriptors []model.ExternalAuthenticationProvider
}

func (s externalProviderSourceSet) Descriptors() []model.ExternalAuthenticationProvider {
	return append([]model.ExternalAuthenticationProvider(nil), s.descriptors...)
}
func (s externalProviderSourceSet) Provider(id string) (ExternalIdentityProvider, bool) {
	return s.provider, s.ids[id]
}

type recordingExternalProvider struct{ beginCalls int }

func (*recordingExternalProvider) Descriptor() model.ExternalAuthenticationProvider {
	return model.ExternalAuthenticationProvider{ID: "campus", Type: "oidc"}
}
func (*recordingExternalProvider) AutoProvision() bool { return false }
func (p *recordingExternalProvider) Begin(
	context.Context,
	ExternalProviderBeginRequest,
) (*ExternalProviderBeginResponse, error) {
	p.beginCalls++
	return &ExternalProviderBeginResponse{RedirectURL: "https://identity.example.test/login"}, nil
}
func (*recordingExternalProvider) State(model.ExternalAuthenticationCallback) (string, error) {
	return "", nil
}
func (*recordingExternalProvider) Complete(
	context.Context,
	ExternalProviderCompleteRequest,
) (*model.ExternalAuthenticationAssertion, error) {
	return nil, nil
}

type externalAuthenticationAttemptFaultCache struct {
	*authenticationCacheFake
	err      error
	addCalls int
}

func (c *externalAuthenticationAttemptFaultCache) Add(
	context.Context,
	string,
	int64,
	time.Duration,
) (int64, error) {
	c.addCalls++
	return 0, c.err
}

type externalProviderSourceFake struct{ provider ExternalIdentityProvider }

func (s externalProviderSourceFake) Descriptors() []model.ExternalAuthenticationProvider { return nil }
func (s externalProviderSourceFake) Provider(id string) (ExternalIdentityProvider, bool) {
	return s.provider, id == "campus"
}

type externalProviderFake struct{}

func (externalProviderFake) Descriptor() model.ExternalAuthenticationProvider {
	return model.ExternalAuthenticationProvider{ID: "campus", Type: "oidc"}
}
func (externalProviderFake) AutoProvision() bool { return false }
func (externalProviderFake) Begin(context.Context, ExternalProviderBeginRequest) (*ExternalProviderBeginResponse, error) {
	return &ExternalProviderBeginResponse{RedirectURL: "https://identity.example.test/login"}, nil
}
func (externalProviderFake) State(model.ExternalAuthenticationCallback) (string, error) {
	return "", nil
}
func (externalProviderFake) Complete(context.Context, ExternalProviderCompleteRequest) (*model.ExternalAuthenticationAssertion, error) {
	return nil, nil
}

type externalLoginStateStoreFake struct {
	store.ExternalLoginStateStore
	saved              *model.ExternalLoginState
	saveLifetime       time.Duration
	storeNow           time.Time
	get                *model.ExternalLoginState
	consumeCalls       int
	consumeResult      *model.ExternalLoginState
	admissionClaimHash string
	admissionErr       error
}

type externalInstitutionStoreFake struct{ store.InstitutionStore }
type externalIdentityStoreFake struct{ store.ExternalIdentityStore }
type externalSessionStoreFake struct{ store.SessionStore }

type externalSessionIssuerFake struct{}

func (externalSessionIssuerFake) createSession(context.Context, sessionIssuance) (*model.Session, *model.AuthenticationTokens, error) {
	return nil, nil, nil
}

type providerConnectionSessionIssuerFake struct{ calls int }

func (s *providerConnectionSessionIssuerFake) createSession(context.Context, sessionIssuance) (*model.Session, *model.AuthenticationTokens, error) {
	s.calls++
	return nil, nil, errors.New("provider connection must not create a session")
}

type providerConnectionIdentityStoreFake struct {
	store.ExternalIdentityStore
	linked *store.ExternalIdentityLink
}

type externalIdentityResolutionStoreFake struct {
	store.ExternalIdentityStore
	resolution *store.ExternalIdentityResolutionRequest
}

type externalInvitationAcceptorFake struct {
	state      *model.ExternalLoginState
	assertion  *model.ExternalAuthenticationAssertion
	capability store.AccessDeploymentCapabilities
	metadata   model.RequestMetadata
	method     string
	result     *store.ExternalIdentityInvitationAcceptanceResult
	err        error
}

func (s *externalInvitationAcceptorFake) acceptExternalIdentity(_ context.Context, state *model.ExternalLoginState,
	assertion *model.ExternalAuthenticationAssertion, capability store.AccessDeploymentCapabilities,
	metadata model.RequestMetadata, method string,
) (*store.ExternalIdentityInvitationAcceptanceResult, error) {
	s.state, s.assertion, s.capability, s.metadata, s.method = state, assertion, capability, metadata, method
	return s.result, s.err
}

func (s *externalIdentityResolutionStoreFake) ResolveOrProvision(_ context.Context, input *store.ExternalIdentityResolutionRequest) (*store.ExternalIdentityResolution, error) {
	s.resolution = input
	return nil, store.NewErrNotFound("external_identity", input.Identity.Provider)
}

func (s *providerConnectionIdentityStoreFake) Link(_ context.Context, input *store.ExternalIdentityLink) (*store.AuthenticationMethodMutationResult, error) {
	s.linked = input
	return &store.AuthenticationMethodMutationResult{Identity: input.Identity}, nil
}

type providerConnectionInstitutionStoreFake struct {
	store.InstitutionStore
	institution *model.Institution
}

type admissionModeProvider struct {
	providerConnectionProvider
	autoProvision bool
}

func (p admissionModeProvider) AutoProvision() bool { return p.autoProvision }

func (s providerConnectionInstitutionStoreFake) GetSingleton(context.Context) (*model.Institution, error) {
	return s.institution, nil
}

type providerConnectionProvider struct {
	state       string
	assertion   *model.ExternalAuthenticationAssertion
	completeErr error
}

func (p providerConnectionProvider) Descriptor() model.ExternalAuthenticationProvider {
	return model.ExternalAuthenticationProvider{ID: "campus", Type: "oidc"}
}
func (providerConnectionProvider) AutoProvision() bool { return false }
func (providerConnectionProvider) Begin(context.Context, ExternalProviderBeginRequest) (*ExternalProviderBeginResponse, error) {
	return nil, errors.New("unused")
}
func (p providerConnectionProvider) State(model.ExternalAuthenticationCallback) (string, error) {
	return p.state, nil
}
func (p providerConnectionProvider) Complete(context.Context, ExternalProviderCompleteRequest) (*model.ExternalAuthenticationAssertion, error) {
	return p.assertion, p.completeErr
}

type externalInvalidatorFake struct{}

func (externalInvalidatorFake) InvalidateAccessCredentials(context.Context, []string) {}
func (externalInvalidatorFake) InvalidateSessionActivity(context.Context, []string)   {}

type externalAuditFake struct{ failureCodes *[]string }

func (externalAuditFake) BeginAuthentication(context.Context, string, string, string, model.SessionClientType, model.RequestMetadata, string) (*model.AuditEvent, error) {
	return nil, nil
}
func (f externalAuditFake) RecordExternalAuthenticationFailure(_ context.Context, _, _ string, _ model.RequestMetadata, _, code string) error {
	if f.failureCodes != nil {
		*f.failureCodes = append(*f.failureCodes, code)
	}
	return nil
}
func (externalAuditFake) CompleteCriticalAction(context.Context, model.AuditEventID, model.AuditStatus, string, any) (*model.AuditEvent, error) {
	return nil, nil
}

func (s *externalLoginStateStoreFake) Save(_ context.Context, state *model.ExternalLoginState, lifetime time.Duration) (*model.ExternalLoginState, error) {
	s.saved = state
	s.saveLifetime = lifetime
	result := *state
	at := s.storeNow
	if at.IsZero() {
		at = model.NowUTC()
	}
	result.ExpiresAt = model.TimeUTC(at).Add(lifetime)
	result.PrepareCreate(model.NewExternalLoginStateID(), at)
	return &result, nil
}

func (s *externalLoginStateStoreFake) SaveInvitationAdmission(_ context.Context, state *model.ExternalLoginState, lifetime time.Duration, claimHash string) (*model.ExternalLoginState, error) {
	s.admissionClaimHash = claimHash
	if s.admissionErr != nil {
		return nil, s.admissionErr
	}
	state = cloneExternalLoginStateForTest(state)
	state.InvitationID = model.NewInvitationID()
	return s.Save(context.Background(), state, lifetime)
}

func cloneExternalLoginStateForTest(state *model.ExternalLoginState) *model.ExternalLoginState {
	result := *state
	return &result
}

func (s *externalLoginStateStoreFake) GetByStateHash(context.Context, string) (*model.ExternalLoginState, error) {
	if s.get == nil {
		return nil, store.NewErrNotFound("external_login_state", "")
	}
	return s.get, nil
}

func (s *externalLoginStateStoreFake) Consume(context.Context, string, string, string) (*model.ExternalLoginState, error) {
	s.consumeCalls++
	if s.consumeResult != nil {
		result := *s.consumeResult
		if !result.ConsumedAt.Valid {
			at := s.storeNow
			if at.IsZero() {
				at = model.NowUTC()
			}
			result.ConsumedAt = model.OptionalTimeFrom(at)
			result.UpdatedAt = model.TimeUTC(at)
		}
		return &result, nil
	}
	return nil, errors.New("desktop state must not be consumed")
}

type desktopCallbackExternalProvider struct{ state string }

func (desktopCallbackExternalProvider) Descriptor() model.ExternalAuthenticationProvider {
	return model.ExternalAuthenticationProvider{ID: "campus", Type: "oidc"}
}
func (desktopCallbackExternalProvider) AutoProvision() bool { return false }
func (desktopCallbackExternalProvider) Begin(context.Context, ExternalProviderBeginRequest) (*ExternalProviderBeginResponse, error) {
	return nil, errors.New("not used")
}
func (p desktopCallbackExternalProvider) State(model.ExternalAuthenticationCallback) (string, error) {
	return p.state, nil
}
func (desktopCallbackExternalProvider) Complete(context.Context, ExternalProviderCompleteRequest) (*model.ExternalAuthenticationAssertion, error) {
	return nil, errors.New("not used")
}

func (externalSessionIssuerFake) recoveryState(_ context.Context, userID model.UserID) (*model.UserMFARecovery, error) {
	return &model.UserMFARecovery{UserID: userID}, nil
}

func (providerConnectionSessionIssuerFake) recoveryState(_ context.Context, userID model.UserID) (*model.UserMFARecovery, error) {
	return &model.UserMFARecovery{UserID: userID}, nil
}
