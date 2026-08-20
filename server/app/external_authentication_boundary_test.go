// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestExternalAuthenticationRetainsExactPersistenceContracts(t *testing.T) {
	t.Parallel()

	root := reflect.TypeOf((*store.Store)(nil)).Elem()
	typeOfService := reflect.TypeOf(externalAuthenticationService{})
	for index := 0; index < typeOfService.NumField(); index++ {
		if typeOfService.Field(index).Type == root {
			t.Fatalf("externalAuthenticationService retains root Store in %q", typeOfService.Field(index).Name)
		}
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
	attempts, err := newAuthenticationAttemptAccounting(newAuthenticationCacheFake())
	if err != nil {
		t.Fatal(err)
	}
	service := &externalAuthenticationService{
		registry:     externalProviderSourceFake{provider: externalProviderFake{}},
		loginStates:  states,
		accessPolicy: allowAllAuthenticationAccessPolicy(),
		attempts:     attempts,
		policy: ExternalAuthenticationPolicy{
			PublicURL: "https://proctor.example.test", LoginStateTTL: 10 * time.Minute,
			LoginRateLimit: LoginRateLimitPolicy{Window: time.Minute, MaximumSourceAttempts: 10},
		},
		newCredential: func() string { value := generated[next]; next++; return value },
		now:           func() time.Time { return at },
	}

	result, err := service.begin(
		context.Background(), "campus", "/after", model.SessionClientWeb,
		"browser", "Browser", "127.0.0.1",
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Binding != bindingToken || states.saved == nil ||
		states.saved.StateHash != model.HashToken(stateToken) ||
		states.saved.BindingHash != model.HashToken(bindingToken) ||
		!states.saved.CreatedAt.IsZero() || !states.saved.ExpiresAt.IsZero() ||
		states.saveLifetime != 10*time.Minute ||
		result.ExpiresAt != databaseAt.Add(10*time.Minute).UnixMilli() {
		t.Fatalf("result=%#v saved=%#v", result, states.saved)
	}
}

func TestInvitationRequiredBeginHashesClaimIntoPurposeBoundState(t *testing.T) {
	t.Parallel()
	claim := model.NewCredentialToken()
	states := &externalLoginStateStoreFake{}
	service := externalAuthenticationBeginService(t, externalProviderSourceSet{
		provider: &recordingExternalProvider{}, ids: map[string]bool{"campus": true},
	}, newAuthenticationCacheFake(), 10)
	service.loginStates = states
	service.accessPolicy = authenticationAccessPolicyFake{providers: map[string]bool{"campus": true},
		providerModes: map[string]model.ProviderAdmissionMode{"campus": model.ProviderAdmissionInvitationRequired}}

	result, err := service.beginWithInvitationClaim(context.Background(), "campus", "/join", model.SessionClientWeb,
		"", "", "127.0.0.1", claim)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || states.saved == nil || states.saved.Purpose != model.ExternalAuthenticationPurposeInvitationAdmission ||
		states.admissionClaimHash != model.HashInvitationClaim(claim) {
		t.Fatalf("result=%#v state=%#v claim_hash=%q", result, states.saved, states.admissionClaimHash)
	}
	if strings.Contains(states.saved.StateHash, claim) || strings.Contains(result.RedirectURL, claim) {
		t.Fatal("raw Invitation claim escaped into durable/provider state")
	}
}

func TestInvitationRequiredBeginRejectsWhenInvitationAdmissionIsDisabled(t *testing.T) {
	t.Parallel()

	provider := &recordingExternalProvider{}
	service := externalAuthenticationBeginService(t, externalProviderSourceSet{
		provider: provider, ids: map[string]bool{"campus": true},
	}, newAuthenticationCacheFake(), 10)
	service.accessPolicy = authenticationAccessPolicyFake{
		providers:                   map[string]bool{"campus": true},
		providerModes:               map[string]model.ProviderAdmissionMode{"campus": model.ProviderAdmissionInvitationRequired},
		invitationAdmissionDisabled: true,
	}

	result, err := service.beginWithInvitationClaim(
		context.Background(), "campus", "/join", model.SessionClientWeb,
		"", "", "192.0.2.9", model.NewCredentialToken(),
	)
	if result != nil || !Is(err, "authentication.external.account_not_linked") {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if provider.beginCalls != 0 {
		t.Fatalf("disabled invitation admission began %d provider challenges", provider.beginCalls)
	}
}

func TestExternalAuthenticationOrdinaryLoginRejectsDesktopBeforeProviderOrPersistence(t *testing.T) {
	t.Parallel()

	provider := &recordingExternalProvider{}
	states := &externalLoginStateStoreFake{}
	service := externalAuthenticationBeginService(t, externalProviderSourceSet{
		provider: provider, ids: map[string]bool{"campus": true},
	}, newAuthenticationCacheFake(), 10)
	service.loginStates = states

	result, err := service.begin(context.Background(), "campus", "/", model.SessionClientDesktop, "desktop-1", "Desktop", "127.0.0.1")
	if result != nil || !Is(err, "authentication.external.request.invalid") {
		t.Fatalf("desktop ordinary login result=%#v err=%v", result, err)
	}
	if provider.beginCalls != 0 || states.saved != nil {
		t.Fatalf("desktop ordinary login reached provider/persistence: provider=%d state=%#v", provider.beginCalls, states.saved)
	}
}

func TestProviderConnectionStartRequiresStrongRecentAndBindsExactUser(t *testing.T) {
	now := time.UnixMilli(30_000)
	states := &externalLoginStateStoreFake{}
	events := []string{}
	auditor := &mutationAttemptAuditorFake{events: &events, beginID: model.NewAuditEventID().String()}
	provider := &recordingExternalProvider{}
	service := externalAuthenticationBeginService(t, externalProviderSourceSet{provider: provider, ids: map[string]bool{"campus": true}}, newAuthenticationCacheFake(), 10)
	service.loginStates, service.mutationAudit = states, auditor
	service.capabilities = &accessPolicyCapabilitiesFake{snapshot: AccessPolicyCapabilitySnapshot{Providers: []AccessPolicyProviderCapability{{Descriptor: provider.Descriptor()}}}}
	service.recentAuthenticationTTL, service.now = 15*time.Minute, func() time.Time { return now }
	application := &App{externalAuthentication: service}
	principal := userSettingsSessionPrincipal(now)
	principal.AuthenticationStrength = model.AuthenticationMultiFactor
	principal.MFACompletedAt = model.OptionalTimeFrom(now)
	_, err := application.BeginProviderConnection(context.Background(), NewInvocation(principal, model.RequestMetadata{}), BeginProviderConnectionCommand{ProviderID: "campus", ReturnTo: "/account/security", Source: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	if states.saved == nil || states.saved.Purpose != model.ExternalAuthenticationPurposeConnect || states.saved.TargetUserID != principal.UserID || states.saved.AuditEventID != auditor.beginID {
		t.Fatalf("saved state = %#v", states.saved)
	}
	weak := principal
	weak.AuthenticationStrength = model.AuthenticationSingleFactor
	weak.MFACompletedAt = model.OptionalTime{}
	states.saved = nil
	if _, err = application.BeginProviderConnection(context.Background(), NewInvocation(weak, model.RequestMetadata{}), BeginProviderConnectionCommand{ProviderID: "campus"}); !Is(err, "authentication.strong_required") {
		t.Fatalf("weak start error = %v", err)
	}
	if states.saved != nil {
		t.Fatal("weak start persisted state")
	}
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
			if err != nil {
				t.Fatal(err)
			}
			issuer := &providerConnectionSessionIssuerFake{}
			provider := providerConnectionProvider{state: stateToken, assertion: &model.ExternalAuthenticationAssertion{ProviderId: "campus", Subject: "opaque-subject-never-exposed"}}
			service := &externalAuthenticationService{registry: externalProviderSourceFake{provider: provider}, loginStates: states,
				institutions: providerConnectionInstitutionStoreFake{institution: institution}, identities: identities,
				accessPolicy: allowAllAuthenticationAccessPolicy(), authentication: issuer,
				mutationAudit: &accessPolicyAuditFake{}, capabilities: &accessPolicyCapabilitiesFake{snapshot: AccessPolicyCapabilitySnapshot{Providers: []AccessPolicyProviderCapability{{Descriptor: provider.Descriptor()}}}},
				policy: ExternalAuthenticationPolicy{PublicURL: "https://proctor.example.test"}, now: func() time.Time { return nodeNow }}
			completion, err := service.complete(context.Background(), "campus", bindingToken, model.ExternalAuthenticationCallback{}, model.RequestMetadata{})
			if err != nil {
				t.Fatal(err)
			}
			if identities.linked == nil || identities.linked.Identity.UserID != target || identities.linked.Identity.Subject != provider.assertion.Subject ||
				identities.linked.AuditEventID != state.AuditEventID || identities.linked.AuditAt != databaseNow.UnixMilli() ||
				!identities.linked.Identity.LastSeenAt.Valid || !identities.linked.Identity.LastSeenAt.Time.Equal(databaseNow) {
				t.Fatalf("link = %#v", identities.linked)
			}
			if states.consumeCalls != 1 || issuer.calls != 0 || completion.Tokens != nil || completion.Session != nil || completion.ReturnTo != state.ReturnTo {
				t.Fatalf("completion=%#v consume calls=%d session calls=%d", completion, states.consumeCalls, issuer.calls)
			}
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
	if err != nil {
		t.Fatal(err)
	}
	provider := providerConnectionProvider{state: stateToken, completeErr: ErrExternalAuthenticationRejected}
	service := &externalAuthenticationService{registry: externalProviderSourceFake{provider: provider},
		loginStates:  &externalLoginStateStoreFake{get: state, consumeResult: state},
		institutions: providerConnectionInstitutionStoreFake{institution: institution},
		accessPolicy: allowAllAuthenticationAccessPolicy(), audit: externalAuditFake{}, mutationAudit: auditor,
		policy: ExternalAuthenticationPolicy{PublicURL: "https://proctor.example.test"}, now: func() time.Time { return now }}

	result, err := service.complete(context.Background(), "campus", bindingToken, model.ExternalAuthenticationCallback{}, model.RequestMetadata{})
	if result != nil || !Is(err, "authentication.external.rejected") {
		t.Fatalf("completion result=%#v err=%v", result, err)
	}
	if auditor.failID != auditID || auditor.failCode != "authentication.external.rejected" {
		t.Fatalf("terminal audit id=%q code=%q", auditor.failID, auditor.failCode)
	}
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
			if err != nil {
				t.Fatal(err)
			}
			provider := admissionModeProvider{providerConnectionProvider: providerConnectionProvider{
				state: stateToken, assertion: &model.ExternalAuthenticationAssertion{ProviderId: "campus", Subject: "opaque-subject",
					Username: "eligible-user", Email: "eligible@example.edu"}}, autoProvision: true}
			service := &externalAuthenticationService{registry: externalProviderSourceFake{provider: provider},
				loginStates:  &externalLoginStateStoreFake{get: state, consumeResult: state, storeNow: databaseNow},
				institutions: providerConnectionInstitutionStoreFake{institution: institution}, identities: identities,
				accessPolicy: authenticationAccessPolicyFake{providers: map[string]bool{"campus": true},
					providerModes: map[string]model.ProviderAdmissionMode{"campus": mode}},
				audit: externalAuditFake{}, capabilities: &accessPolicyCapabilitiesFake{snapshot: AccessPolicyCapabilitySnapshot{
					Providers: []AccessPolicyProviderCapability{{Descriptor: provider.Descriptor(), AutoProvision: true}}}},
				policy: ExternalAuthenticationPolicy{PublicURL: "https://proctor.example.test"}, now: func() time.Time { return databaseNow }}

			if _, err = service.complete(context.Background(), "campus", bindingToken, model.ExternalAuthenticationCallback{}, model.RequestMetadata{}); !Is(err, "authentication.external.account_not_linked") {
				t.Fatalf("completion error = %v", err)
			}
			if identities.resolution == nil || identities.resolution.User != nil || identities.resolution.Settings != nil ||
				identities.resolution.ProvisionAudit != nil || identities.resolution.DefaultProfilePictureJob != nil {
				t.Fatalf("%s prepared a provisioning package: %#v", mode, identities.resolution)
			}
			capability, configured := identities.resolution.Capabilities.Providers["campus"]
			if !configured || !capability.AutoProvision {
				t.Fatalf("%s terminal capability snapshot = %#v", mode, identities.resolution.Capabilities)
			}
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
	if err != nil {
		t.Fatal(err)
	}
	provider := admissionModeProvider{providerConnectionProvider: providerConnectionProvider{state: stateToken,
		assertion: &model.ExternalAuthenticationAssertion{ProviderId: "campus", Subject: "opaque-subject",
			Username: "invited-user", Email: " Invited@Example.EDU ", EmailVerified: true}}, autoProvision: false}
	service := &externalAuthenticationService{registry: externalProviderSourceFake{provider: provider},
		loginStates:  &externalLoginStateStoreFake{get: state, consumeResult: state, storeNow: databaseNow},
		institutions: providerConnectionInstitutionStoreFake{institution: institution}, identities: identities,
		invitationAcceptor: acceptor,
		accessPolicy: authenticationAccessPolicyFake{providers: map[string]bool{"campus": true},
			providerModes: map[string]model.ProviderAdmissionMode{"campus": model.ProviderAdmissionInvitationRequired}},
		audit: externalAuditFake{}, capabilities: &accessPolicyCapabilitiesFake{snapshot: AccessPolicyCapabilitySnapshot{
			Providers: []AccessPolicyProviderCapability{{Descriptor: provider.Descriptor()}}}},
		policy: ExternalAuthenticationPolicy{PublicURL: "https://proctor.example.test", NodeID: "node-a"}, now: func() time.Time { return databaseNow }}

	result, err := service.complete(context.Background(), "campus", bindingToken, model.ExternalAuthenticationCallback{}, model.RequestMetadata{})
	if err != nil || result == nil || result.User != acceptedUser || result.Session != nil || result.Tokens != nil || result.ReturnTo != "/join" {
		t.Fatalf("completion result=%#v error=%v", result, err)
	}
	if acceptor.state == nil || acceptor.assertion != provider.assertion || acceptor.method != "oidc" ||
		acceptor.state.InvitationID != invitationID || !acceptor.state.ConsumedAt.Valid {
		t.Fatalf("terminal acceptance state=%#v assertion=%#v method=%q", acceptor.state, acceptor.assertion, acceptor.method)
	}
	if identities.resolution != nil {
		t.Fatalf("Invitation admission used ordinary identity resolution: %#v", identities.resolution)
	}
	secondStateToken, secondBindingToken := model.NewCredentialToken(), model.NewCredentialToken()
	secondState := *state
	secondState.StateHash, secondState.BindingHash = model.HashToken(secondStateToken), model.HashToken(secondBindingToken)
	secondState.ConsumedAt = model.OptionalTime{}
	secondProvider := provider
	secondProvider.state = secondStateToken
	failureCodes := []string{}
	acceptor.result, acceptor.err, acceptor.state = nil, store.ErrAuthenticationMethodDisabled, nil
	service.registry = externalProviderSourceFake{provider: secondProvider}
	service.loginStates = &externalLoginStateStoreFake{get: &secondState, consumeResult: &secondState, storeNow: databaseNow}
	service.audit = externalAuditFake{failureCodes: &failureCodes}
	result, err = service.complete(context.Background(), "campus", secondBindingToken, model.ExternalAuthenticationCallback{}, model.RequestMetadata{})
	if result != nil || !Is(err, "authentication.external.invalid") ||
		!slices.Equal(failureCodes, []string{"authentication.external.invalid"}) {
		t.Fatalf("terminal provider removal result=%#v error=%v failure_audits=%v", result, err, failureCodes)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	provider := admissionModeProvider{providerConnectionProvider: providerConnectionProvider{
		state: stateToken,
		assertion: &model.ExternalAuthenticationAssertion{
			ProviderId: "campus", Subject: "private-subject", Username: "invited-user",
			Email: "invited@example.edu", EmailVerified: true,
		},
	}}
	service := &externalAuthenticationService{
		registry:           externalProviderSourceFake{provider: provider},
		loginStates:        &externalLoginStateStoreFake{get: state, consumeResult: state, storeNow: databaseNow},
		institutions:       providerConnectionInstitutionStoreFake{institution: institution},
		identities:         identities,
		invitationAcceptor: acceptor,
		accessPolicy: authenticationAccessPolicyFake{
			providers:                   map[string]bool{"campus": true},
			providerModes:               map[string]model.ProviderAdmissionMode{"campus": model.ProviderAdmissionInvitationRequired},
			invitationAdmissionDisabled: true,
		},
		audit: externalAuditFake{failureCodes: &failureCodes}, capabilities: &accessPolicyCapabilitiesFake{snapshot: AccessPolicyCapabilitySnapshot{
			Providers: []AccessPolicyProviderCapability{{Descriptor: provider.Descriptor()}},
		}},
		policy: ExternalAuthenticationPolicy{PublicURL: "https://proctor.example.test", NodeID: "node-b"},
		now:    func() time.Time { return databaseNow },
	}

	result, err := service.complete(context.Background(), "campus", bindingToken, model.ExternalAuthenticationCallback{}, model.RequestMetadata{})
	if result != nil || !Is(err, "authentication.external.invalid") {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if identities.resolution != nil || acceptor.state != nil {
		t.Fatalf("disabled admission reached terminal stores: resolution=%#v state=%#v", identities.resolution, acceptor.state)
	}
	if !slices.Equal(failureCodes, []string{"authentication.external.invalid"}) {
		t.Fatalf("disabled admission failure audits = %v", failureCodes)
	}
	if strings.Contains(err.Error(), invitationID.String()) || strings.Contains(err.Error(), "private-subject") ||
		strings.Contains(err.Error(), "invited@example.edu") {
		t.Fatalf("bounded admission error leaked private state: %v", err)
	}
}

func TestExternalAuthenticationCallbackRejectsLegacyDesktopStateBeforeConsumption(t *testing.T) {
	t.Parallel()

	stateToken := model.NewCredentialToken()
	bindingToken := model.NewCredentialToken()
	states := &externalLoginStateStoreFake{get: &model.ExternalLoginState{
		Provider: "campus", StateHash: model.HashToken(stateToken), BindingHash: model.HashToken(bindingToken),
		ReturnTo: "/", ClientType: model.SessionClientDesktop, ExpiresAt: time.Now().Add(time.Minute),
	}}
	service := &externalAuthenticationService{
		registry:    externalProviderSourceFake{provider: desktopCallbackExternalProvider{state: stateToken}},
		loginStates: states, accessPolicy: allowAllAuthenticationAccessPolicy(), now: time.Now,
	}
	result, err := service.complete(context.Background(), "campus", bindingToken,
		model.ExternalAuthenticationCallback{Values: map[string][]string{"code": {"accepted"}}}, model.RequestMetadata{})
	if result != nil || !Is(err, "authentication.external.invalid") {
		t.Fatalf("legacy desktop callback result=%#v err=%v", result, err)
	}
	if states.consumeCalls != 0 {
		t.Fatalf("legacy desktop state was consumed %d times", states.consumeCalls)
	}
}

func TestExternalAuthenticationBeginHidesProviderDisabledByCurrentPolicy(t *testing.T) {
	t.Parallel()

	provider := &recordingExternalProvider{}
	service := externalAuthenticationBeginService(t, externalProviderSourceSet{
		provider: provider, ids: map[string]bool{"campus": true},
	}, newAuthenticationCacheFake(), 10)
	service.accessPolicy = authenticationAccessPolicyFake{providers: map[string]bool{}}
	result, err := service.begin(context.Background(), "campus", "/", model.SessionClientWeb, "", "", "192.0.2.9")
	if result != nil || !Is(err, "authentication.external.provider_not_found") {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if provider.beginCalls != 0 {
		t.Fatalf("disabled provider began %d challenges", provider.beginCalls)
	}
}

func TestExternalAuthenticationProviderListIncludesOnlyCurrentPolicySelections(t *testing.T) {
	t.Parallel()

	configured := []model.ExternalAuthenticationProvider{
		{Id: "campus-a", DisplayName: "Campus A", Type: "oidc"},
		{Id: "campus-b", DisplayName: "Campus B", Type: "cas"},
	}
	service := &externalAuthenticationService{
		registry:     externalProviderSourceSet{descriptors: configured},
		accessPolicy: authenticationAccessPolicyFake{providers: map[string]bool{"campus-b": true}},
	}
	providers, err := service.providers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(providers, configured[1:]) {
		t.Fatalf("available providers = %#v, want %#v", providers, configured[1:])
	}
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
		now:      time.Now,
	}
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
		{"generator", func(a *externalAuthenticationConstructorArgs) { a.newCredential = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.clear(&candidate)
			if _, err := candidate.build(); err == nil {
				t.Fatalf("missing %s accepted", test.name)
			}
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
	invalidator   authenticationInvalidator
	audit         externalAuthenticationAudit
	diagnostics   authenticationDiagnostics
	acceptor      externalInvitationAcceptor
	newCredential func() string
	now           func() time.Time
}

func (a externalAuthenticationConstructorArgs) build() (*externalAuthenticationService, error) {
	events := []string{}
	return newExternalAuthenticationService(
		a.registry, a.loginStates, a.institutions, a.identities, a.sessions,
		allowAllAuthenticationAccessPolicy(), a.attempts, a.issuer, a.invalidator, a.audit,
		&mutationAttemptAuditorFake{events: &events, beginID: model.NewAuditEventID().String()},
		&accessPolicyCapabilitiesFake{}, a.acceptor, ExternalAuthenticationPolicy{}, 15*time.Minute,
		a.diagnostics, a.newCredential, a.now,
	)
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

	if _, err := service.begin(
		context.Background(), "campus-a", "/", model.SessionClientWeb,
		"", "", "192.0.2.10:4000",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := service.begin(
		context.Background(), " CAMPUS-A ", "/", model.SessionClientWeb,
		"", "", "192.0.2.10:5000",
	); !Is(err, "authentication.rate_limited") {
		t.Fatalf("normalized-source threshold error = %v", err)
	}
	if _, err := service.begin(
		context.Background(), "campus-b", "/", model.SessionClientWeb,
		"", "", "192.0.2.10:6000",
	); err != nil {
		t.Fatalf("provider-isolated attempt failed: %v", err)
	}

	cache.mu.Lock()
	defer cache.mu.Unlock()
	if len(cache.counters) != 2 {
		t.Fatalf("counter keys = %d, want 2", len(cache.counters))
	}
	for key := range cache.counters {
		if !strings.HasPrefix(key, "authentication/attempts/external-authentication/source/") {
			t.Fatalf("unexpected counter key %q", key)
		}
		if strings.Contains(key, "campus") || strings.Contains(key, "192.0.2.10") {
			t.Fatalf("counter key exposes provider or source: %q", key)
		}
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
		if _, err := service.begin(
			context.Background(), "campus", "/", model.SessionClientWeb,
			"", "", "198.51.100.7",
		); err != nil {
			t.Fatalf("attempt %d failed: %v", attempt, err)
		}
	}
	if _, err := service.begin(
		context.Background(), "campus", "/", model.SessionClientWeb,
		"", "", "198.51.100.7",
	); !Is(err, "authentication.rate_limited") {
		t.Fatalf("attempt beyond maximum error = %v", err)
	}
	if provider.beginCalls != 2 {
		t.Fatalf("provider Begin calls = %d, want 2", provider.beginCalls)
	}
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

	if _, err := service.begin(
		context.Background(), "missing", "/", model.SessionClientWeb,
		"", "", "203.0.113.8",
	); !Is(err, "authentication.external.provider_not_found") {
		t.Fatalf("missing-provider error = %v", err)
	}
	if cache.addCalls != 0 {
		t.Fatalf("provider validation occurred after %d counter calls", cache.addCalls)
	}
	if _, err := service.begin(
		context.Background(), "campus", "https://unsafe.example", model.SessionClientWeb,
		"", "", "203.0.113.8",
	); !Is(err, "authentication.external.request.invalid") {
		t.Fatalf("invalid-request error = %v", err)
	}
	if cache.addCalls != 0 {
		t.Fatalf("request validation occurred after %d counter calls", cache.addCalls)
	}
	if _, err := service.begin(
		context.Background(), "campus", "/", model.SessionClientWeb,
		"", "", "203.0.113.8",
	); !Is(err, "authentication.rate_limit_unavailable") {
		t.Fatalf("counter failure error = %v", err)
	}
	if provider.beginCalls != 0 {
		t.Fatalf("provider began before accounting succeeded")
	}
}

func externalAuthenticationBeginService(
	t *testing.T,
	registry externalProviderSource,
	cache authenticationAttemptCache,
	maximum int,
) *externalAuthenticationService {
	t.Helper()
	attempts := newExternalAuthenticationAttempts(t, cache)
	return &externalAuthenticationService{
		registry:     registry,
		loginStates:  &externalLoginStateStoreFake{},
		accessPolicy: allowAllAuthenticationAccessPolicy(),
		attempts:     attempts,
		policy: ExternalAuthenticationPolicy{
			PublicURL:     "https://proctor.example.test",
			LoginStateTTL: 10 * time.Minute,
			LoginRateLimit: LoginRateLimitPolicy{
				Window: time.Minute, MaximumSourceAttempts: maximum,
			},
		},
		newCredential: model.NewCredentialToken,
		now:           time.Now,
	}
}

func newExternalAuthenticationAttempts(
	t *testing.T,
	cache authenticationAttemptCache,
) *authenticationAttemptAccounting {
	t.Helper()
	attempts, err := newAuthenticationAttemptAccounting(cache)
	if err != nil {
		t.Fatal(err)
	}
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
	return model.ExternalAuthenticationProvider{Id: "campus", Type: "oidc"}
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
	return model.ExternalAuthenticationProvider{Id: "campus", Type: "oidc"}
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

func (s *externalInvitationAcceptorFake) AcceptExternalIdentity(_ context.Context, state *model.ExternalLoginState,
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

func (s *providerConnectionIdentityStoreFake) LinkWithAudit(_ context.Context, input *store.ExternalIdentityLink) (*store.AuthenticationMethodMutationResult, error) {
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
	return model.ExternalAuthenticationProvider{Id: "campus", Type: "oidc"}
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
func (externalAuditFake) CompleteCriticalAction(context.Context, string, model.AuditStatus, string, any) (*model.AuditEvent, error) {
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
	return model.ExternalAuthenticationProvider{Id: "campus", Type: "oidc"}
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
