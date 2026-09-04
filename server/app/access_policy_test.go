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

type accessPolicyStoreFake struct {
	snapshot       *store.AccessPolicySnapshot
	blockers       []store.AccessPolicyBlocker
	preflightInput *store.AccessPolicyPreflight
	replaceInput   *store.AccessPolicyReplacement
	command        *store.CommandIdempotency
	result         *store.AccessPolicyReplacementResult
	preflightErr   error
	replaceErr     error
}

func (s *accessPolicyStoreFake) Get(context.Context, int) (*store.AccessPolicySnapshot, error) {
	return s.snapshot, nil
}
func (s *accessPolicyStoreFake) Preflight(_ context.Context, input *store.AccessPolicyPreflight) ([]store.AccessPolicyBlocker, error) {
	s.preflightInput = input
	return append([]store.AccessPolicyBlocker(nil), s.blockers...), s.preflightErr
}
func (s *accessPolicyStoreFake) Replace(_ context.Context, input *store.AccessPolicyReplacement, command *store.CommandIdempotency) (*store.AccessPolicyReplacementResult, error) {
	s.replaceInput, s.command = input, command
	return s.result, s.replaceErr
}

type accessPolicyAuthorizationFake struct{ action model.Action }

func (a *accessPolicyAuthorizationFake) Authorize(_ context.Context, _ Invocation, action model.Action, _ model.Resource) error {
	a.action = action
	return nil
}

type accessPolicyCapabilitiesFake struct {
	snapshot AccessPolicyCapabilitySnapshot
}

func (c *accessPolicyCapabilitiesFake) Snapshot() AccessPolicyCapabilitySnapshot { return c.snapshot }

type accessPolicyAuditFake struct {
	beginID  string
	beginErr error
	failErr  error
	attempt  mutationAttempt
	failID   string
	failCode string
}

func (a *accessPolicyAuditFake) Begin(_ context.Context, invocation Invocation, action model.Action, resource model.Resource,
	operation string, value, prior map[string]any,
) (string, error) {
	a.attempt = mutationAttempt{Invocation: invocation, Action: action, Resource: resource,
		Operation: operation, Value: value, Prior: prior}
	return a.beginID, a.beginErr
}

func (a *accessPolicyAuditFake) BeginAtScope(_ context.Context, invocation Invocation, action model.Action, resource model.Resource,
	scopeType model.RoleScopeType, scopeID, operation string, value, prior map[string]any,
) (string, error) {
	a.attempt = mutationAttempt{Invocation: invocation, Action: action, Resource: resource,
		ScopeType: scopeType, ScopeID: scopeID, Operation: operation, Value: value, Prior: prior}
	return a.beginID, a.beginErr
}

func (a *accessPolicyAuditFake) Fail(_ context.Context, id, code string) error {
	a.failID, a.failCode = id, code
	return a.failErr
}

type accessPolicyEffectsFake struct{ changes []accessPolicyChanged }

func (e *accessPolicyEffectsFake) Changed(_ context.Context, change accessPolicyChanged) error {
	e.changes = append(e.changes, change)
	return nil
}

func accessPolicyFixture(t *testing.T) (*accessPolicyService, *accessPolicyStoreFake, *accessPolicyCapabilitiesFake, *accessPolicyEffectsFake, *accessPolicyAuditFake, Invocation) {
	t.Helper()
	at := time.UnixMilli(1_000)
	institution, err := model.NewInstitution(model.NewInstitutionID(), "northbridge", "Northbridge", "", at)
	if err != nil {
		t.Fatal(err)
	}
	policy := model.NewInitialAccessPolicy(model.NewAccessPolicyID(), at)
	persistence := &accessPolicyStoreFake{snapshot: &store.AccessPolicySnapshot{Policy: policy}}
	capabilities := &accessPolicyCapabilitiesFake{snapshot: AccessPolicyCapabilitySnapshot{
		Providers:   []AccessPolicyProviderCapability{{Descriptor: model.ExternalAuthenticationProvider{Id: "campus-oidc", DisplayName: "Campus", Type: "oidc"}, AutoProvision: true}},
		DurableMail: true,
	}}
	effects := &accessPolicyEffectsFake{}
	audit := &accessPolicyAuditFake{beginID: model.NewId()}
	service, err := newAccessPolicyService(persistence, &accessPolicyInstitutionFake{institution}, &accessPolicyAuthorizationFake{}, capabilities,
		audit, effects, accessPolicyEffectFailuresFake{}, "https://proctor.example.edu", time.Minute,
		func() time.Time { return at.Add(time.Minute) })
	if err != nil {
		t.Fatal(err)
	}
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationMultiFactor,
		AuthenticatedAt: at, MFACompletedAt: model.OptionalTimeFrom(at.Add(30 * time.Second)), ClientType: model.SessionClientWeb}
	return service, persistence, capabilities, effects, audit, NewInvocation(principal, model.RequestMetadata{RequestID: "request-1"})
}

type accessPolicyInstitutionFake struct{ institution *model.Institution }

func (s *accessPolicyInstitutionFake) GetSingleton(context.Context) (*model.Institution, error) {
	return s.institution, nil
}

type accessPolicyEffectFailuresFake struct{}

func (accessPolicyEffectFailuresFake) Report(context.Context, string, error) {}

type accessPolicyProviderSourceFake struct {
	descriptors []model.ExternalAuthenticationProvider
	providers   map[string]ExternalIdentityProvider
}

func (s accessPolicyProviderSourceFake) Descriptors() []model.ExternalAuthenticationProvider {
	return append([]model.ExternalAuthenticationProvider(nil), s.descriptors...)
}
func (s accessPolicyProviderSourceFake) Provider(id string) (ExternalIdentityProvider, bool) {
	provider, ok := s.providers[id]
	return provider, ok
}

type accessPolicyMailSenderFake struct{ enabled bool }

func (s accessPolicyMailSenderFake) Enabled() bool   { return s.enabled }
func (accessPolicyMailSenderFake) From() MailAddress { return MailAddress{} }
func (accessPolicyMailSenderFake) Send(context.Context, OutboundMail) (MailTransportOutcome, error) {
	return MailTransportUnknown, nil
}

type accessPolicyMailHealthFake struct{ code string }

func (h accessPolicyMailHealthFake) Code() string { return h.code }

func TestDeploymentAccessPolicyCapabilitiesUseOnlyLiveProvidersAndMailAvailability(t *testing.T) {
	t.Parallel()
	provider := &accessPolicyExternalProviderFake{autoProvision: true}
	snapshot := (deploymentAccessPolicyCapabilities{
		providers: accessPolicyProviderSourceFake{
			descriptors: []model.ExternalAuthenticationProvider{
				{Id: "removed", DisplayName: "Removed", Type: "oidc"},
				{Id: "campus", DisplayName: "Campus", Type: "oidc"},
			},
			providers: map[string]ExternalIdentityProvider{"campus": provider},
		},
		mail: accessPolicyMailSenderFake{enabled: true}, health: accessPolicyMailHealthFake{code: MailHealthHealthy},
	}).Snapshot()
	if !snapshot.DurableMail || len(snapshot.Providers) != 1 || snapshot.Providers[0].Descriptor.Id != "campus" ||
		!snapshot.Providers[0].AutoProvision {
		t.Fatalf("capability snapshot = %#v", snapshot)
	}
}

func TestDeploymentAccessPolicyCapabilitiesRejectDisabledAndUnhealthyDurableMail(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		enabled bool
		health  string
	}{
		{name: "disabled", health: MailHealthHealthy},
		{name: "smtp outage", enabled: true, health: MailHealthSMTPOutage},
		{name: "delayed queue", enabled: true, health: MailHealthQueueDelayed},
		{name: "health unknown", enabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			snapshot := (deploymentAccessPolicyCapabilities{mail: accessPolicyMailSenderFake{enabled: tc.enabled},
				health: accessPolicyMailHealthFake{code: tc.health}}).Snapshot()
			if snapshot.DurableMail {
				t.Fatalf("durable mail available for enabled=%v health=%q", tc.enabled, tc.health)
			}
		})
	}
}

func TestDeploymentAccessPolicyCapabilitiesTrackAuthoritativeMailHealth(t *testing.T) {
	t.Parallel()
	health := newMailHealth(true)
	capabilities := deploymentAccessPolicyCapabilities{mail: accessPolicyMailSenderFake{enabled: true}, health: health}
	if !capabilities.Snapshot().DurableMail {
		t.Fatal("healthy configured durable mail was unavailable")
	}
	health.set(MailHealthSMTPOutage)
	if capabilities.Snapshot().DurableMail {
		t.Fatal("SMTP outage remained available for invitation-required activation")
	}
}

type accessPolicyExternalProviderFake struct{ autoProvision bool }

func (p *accessPolicyExternalProviderFake) Descriptor() model.ExternalAuthenticationProvider {
	return model.ExternalAuthenticationProvider{Id: "campus", DisplayName: "Campus", Type: "oidc"}
}
func (p *accessPolicyExternalProviderFake) AutoProvision() bool { return p.autoProvision }
func (*accessPolicyExternalProviderFake) Begin(context.Context, ExternalProviderBeginRequest) (*ExternalProviderBeginResponse, error) {
	return nil, nil
}
func (*accessPolicyExternalProviderFake) State(model.ExternalAuthenticationCallback) (string, error) {
	return "", nil
}
func (*accessPolicyExternalProviderFake) Complete(context.Context, ExternalProviderCompleteRequest) (*model.ExternalAuthenticationAssertion, error) {
	return nil, nil
}

func TestAccessPolicyPreflightUsesCurrentDeploymentCapabilities(t *testing.T) {
	t.Parallel()
	service, persistence, _, _, _, invocation := accessPolicyFixture(t)
	settings := persistence.snapshot.Policy.Settings()
	settings.ProviderAdmissions["campus-oidc"] = model.ProviderAdmissionAutoProvision
	result, err := service.Preflight(context.Background(), invocation, PreflightAccessPolicyCommand{ExpectedRevision: 1, Settings: settings})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Blockers) != 0 || persistence.preflightInput == nil ||
		!persistence.preflightInput.Capabilities.DurableMail || !persistence.preflightInput.Capabilities.Providers["campus-oidc"].AutoProvision {
		t.Fatalf("preflight = %#v input %#v", result, persistence.preflightInput)
	}
}

func TestAccessPolicyMutationRequiresInteractiveStrongRecentAuthentication(t *testing.T) {
	t.Parallel()
	service, persistence, _, _, _, invocation := accessPolicyFixture(t)
	settings := persistence.snapshot.Policy.Settings()
	cases := []struct {
		name      string
		principal model.Principal
		wantCode  string
	}{
		{name: "personal access token", principal: func() model.Principal {
			p := invocation.Principal()
			p.CredentialType = model.CredentialPersonalAccessToken
			p.SessionID = ""
			return p
		}(), wantCode: "authentication.invalid_token"},
		{name: "single factor", principal: func() model.Principal {
			p := invocation.Principal()
			p.AuthenticationStrength = model.AuthenticationSingleFactor
			p.MFACompletedAt = model.OptionalTime{}
			return p
		}(), wantCode: "authentication.strong_required"},
		{name: "stale", principal: func() model.Principal {
			p := invocation.Principal()
			p.AuthenticatedAt = time.UnixMilli(1)
			p.MFACompletedAt = model.OptionalTimeFrom(time.UnixMilli(1))
			return p
		}(), wantCode: "authentication.reauthentication_required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			invocation := NewInvocation(tc.principal, model.RequestMetadata{})
			operations := []struct {
				name string
				run  func() error
			}{
				{name: "preflight", run: func() error {
					_, err := service.Preflight(context.Background(), invocation,
						PreflightAccessPolicyCommand{ExpectedRevision: 1, Settings: settings})
					return err
				}},
				{name: "replace", run: func() error {
					_, err := service.Replace(context.Background(), invocation,
						ReplaceAccessPolicyCommand{ExpectedRevision: 1, Settings: settings, IdempotencyKey: "assurance"})
					return err
				}},
			}
			for _, operation := range operations {
				t.Run(operation.name, func(t *testing.T) {
					err := operation.run()
					var appErr *Error
					if !errors.As(err, &appErr) || appErr.Code() != tc.wantCode {
						t.Fatalf("error = %v, want %s", err, tc.wantCode)
					}
				})
			}
		})
	}
}

func TestAccessPolicyReplacementIsIdempotentAndPublishesOnlyCommittedRevision(t *testing.T) {
	t.Parallel()
	service, persistence, _, effects, audit, invocation := accessPolicyFixture(t)
	next := persistence.snapshot.Policy.Clone()
	transition, err := next.Replace(1, func() model.AccessPolicySettings {
		value := next.Settings()
		value.DesktopAuthorizationEnabled = false
		return value
	}(), invocation.Principal().UserID, time.UnixMilli(2_000))
	if err != nil {
		t.Fatal(err)
	}
	persistence.result = &store.AccessPolicyReplacementResult{Snapshot: &store.AccessPolicySnapshot{Policy: next, History: []*model.AccessPolicyTransition{transition}}, Changed: true}
	settings := persistence.snapshot.Policy.Settings()
	settings.DesktopAuthorizationEnabled = false
	result, err := service.Replace(context.Background(), invocation, ReplaceAccessPolicyCommand{ExpectedRevision: 1, Settings: settings, IdempotencyKey: "policy-1"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Policy.Revision != 2 || persistence.command == nil || persistence.command.Operation != "access_policy.replace.v1" ||
		persistence.replaceInput == nil || persistence.replaceInput.AuditEventID != audit.beginID ||
		audit.attempt.Operation != "replace" || audit.attempt.ScopeType != model.RoleScopeInstitution ||
		len(effects.changes) != 1 || effects.changes[0].Revision != 2 {
		t.Fatalf("result=%#v command=%#v replacement=%#v effects=%v", result, persistence.command, persistence.replaceInput, effects.changes)
	}
}

func TestAccessPolicyReplacementResolvesExactReplayBeforeRevisionPreflight(t *testing.T) {
	t.Parallel()
	service, persistence, _, effects, audit, invocation := accessPolicyFixture(t)
	replayed := persistence.snapshot.Policy.Clone()
	replayed.Revision = 2
	replayed.UpdatedAt = replayed.UpdatedAt.Add(time.Second)
	persistence.preflightErr = &store.ErrAccessPolicyRevisionConflict{CurrentRevision: 2}
	persistence.result = &store.AccessPolicyReplacementResult{
		Snapshot: &store.AccessPolicySnapshot{Policy: replayed}, Changed: true, Replayed: true,
	}
	settings := persistence.snapshot.Policy.Settings()
	settings.DesktopAuthorizationEnabled = false
	result, err := service.Replace(context.Background(), invocation, ReplaceAccessPolicyCommand{
		ExpectedRevision: 1, Settings: settings, IdempotencyKey: "lost-response",
	})
	if err != nil || result.Policy == nil || result.Policy.Revision != 2 {
		t.Fatalf("replay result = %#v, %v", result, err)
	}
	if persistence.preflightInput != nil {
		t.Fatal("replacement performed a revision-sensitive preflight before replay resolution")
	}
	if len(effects.changes) != 0 {
		t.Fatalf("replay effects = %#v", effects.changes)
	}
	if audit.attempt.Operation != "replace" || persistence.replaceInput.AuditEventID != audit.beginID {
		t.Fatalf("replay audit = %#v replacement = %#v", audit.attempt, persistence.replaceInput)
	}
}

func TestAccessPolicyReplacementFingerprintIncludesSessionRevocationChoice(t *testing.T) {
	t.Parallel()
	service, persistence, _, _, _, invocation := accessPolicyFixture(t)
	persistence.result = &store.AccessPolicyReplacementResult{Snapshot: persistence.snapshot}
	settings := persistence.snapshot.Policy.Settings()
	settings.DesktopAuthorizationEnabled = false
	_, err := service.Replace(context.Background(), invocation, ReplaceAccessPolicyCommand{
		ExpectedRevision: 1, Settings: settings, RevokeExistingSessions: true, IdempotencyKey: "policy-revoke",
	})
	if err != nil {
		t.Fatal(err)
	}
	if persistence.replaceInput == nil || !persistence.replaceInput.Preflight.RevokeExistingSessions {
		t.Fatalf("replacement = %#v", persistence.replaceInput)
	}
	withRevocation := persistence.command.Fingerprint
	persistence.result = &store.AccessPolicyReplacementResult{Snapshot: persistence.snapshot}
	_, err = service.Replace(context.Background(), invocation, ReplaceAccessPolicyCommand{
		ExpectedRevision: 1, Settings: settings, RevokeExistingSessions: false, IdempotencyKey: "policy-revoke",
	})
	if err != nil {
		t.Fatal(err)
	}
	if persistence.command.Fingerprint == withRevocation {
		t.Fatal("session revocation choice was omitted from the idempotency fingerprint")
	}
}

func TestAccessPolicyReplacementMapsConflictingIdempotencyReuse(t *testing.T) {
	t.Parallel()
	service, persistence, _, _, _, invocation := accessPolicyFixture(t)
	persistence.replaceErr = &store.ErrIdempotencyConflict{}
	settings := persistence.snapshot.Policy.Settings()
	settings.DesktopAuthorizationEnabled = false
	_, err := service.Replace(context.Background(), invocation, ReplaceAccessPolicyCommand{
		ExpectedRevision: 1, Settings: settings, IdempotencyKey: "reused-key",
	})
	if !Is(err, "idempotency.conflict") {
		t.Fatalf("error = %v", err)
	}
	if persistence.preflightInput != nil {
		t.Fatal("conflicting reuse was preceded by a revision-sensitive preflight")
	}
}

func TestAccessPolicyReplacementCompletesRevisionBlockerAndStoreFailures(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		storeErr error
		wantCode string
	}{
		{name: "revision", storeErr: &store.ErrAccessPolicyRevisionConflict{CurrentRevision: 2}, wantCode: "access_policy.revision_conflict"},
		{name: "blocker", storeErr: &store.ErrAccessPolicyBlocked{Blockers: []store.AccessPolicyBlocker{{Code: store.AccessPolicyBlockerLastAdministratorPath}}}, wantCode: "access_policy.blocked"},
		{name: "store", storeErr: errors.New("database unavailable"), wantCode: "access_policy.unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			service, persistence, _, effects, audit, invocation := accessPolicyFixture(t)
			persistence.replaceErr = tc.storeErr
			settings := persistence.snapshot.Policy.Settings()
			settings.DesktopAuthorizationEnabled = false
			_, err := service.Replace(context.Background(), invocation, ReplaceAccessPolicyCommand{
				ExpectedRevision: 1, Settings: settings, IdempotencyKey: "failed-" + tc.name,
			})
			if !Is(err, tc.wantCode) {
				t.Fatalf("error = %v, want %s", err, tc.wantCode)
			}
			if audit.attempt.Operation != "replace" || audit.failID != audit.beginID || audit.failCode != tc.wantCode {
				t.Fatalf("audit = %#v fail=%q/%q", audit.attempt, audit.failID, audit.failCode)
			}
			if len(effects.changes) != 0 {
				t.Fatalf("failure effects = %#v", effects.changes)
			}
		})
	}
}

func TestAccessPolicyReadFailsSafelyForMissingSnapshot(t *testing.T) {
	t.Parallel()
	service, persistence, _, _, _, invocation := accessPolicyFixture(t)
	for _, snapshot := range []*store.AccessPolicySnapshot{nil, {}} {
		persistence.snapshot = snapshot
		_, err := service.Read(context.Background(), invocation)
		if !Is(err, "access_policy.unavailable") {
			t.Fatalf("snapshot %#v error = %v", snapshot, err)
		}
	}
}

func TestPublicAccessDiscoveryFiltersUnavailableProvidersAndRestoresByStableID(t *testing.T) {
	t.Parallel()
	service, persistence, capabilities, _, _, _ := accessPolicyFixture(t)
	policy := persistence.snapshot.Policy.Clone()
	policy.ProviderAdmissions["campus-oidc"] = model.ProviderAdmissionLinkedOnly
	persistence.snapshot.Policy = policy

	capabilities.snapshot.Providers = nil
	discovery, err := service.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if discovery.CanonicalOrigin != "https://proctor.example.edu" || len(discovery.Providers) != 0 || !discovery.Initialized {
		t.Fatalf("removed-provider discovery = %#v", discovery)
	}
	capabilities.snapshot.Providers = []AccessPolicyProviderCapability{{Descriptor: model.ExternalAuthenticationProvider{Id: "campus-oidc", DisplayName: "Campus", Type: "oidc"}}}
	discovery, err = service.Discover(context.Background())
	if err != nil || len(discovery.Providers) != 1 || discovery.Providers[0].Id != "campus-oidc" {
		t.Fatalf("restored-provider discovery = %#v, %v", discovery, err)
	}
}
