// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"time"

	apprealtime "github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const (
	accessDiscoveryVersion          = 1
	desktopAuthorizationProtocolMin = 1
	desktopAuthorizationProtocolMax = 1
)

type AccessPolicyProviderCapability struct {
	Descriptor    model.ExternalAuthenticationProvider
	AutoProvision bool
}

type AccessPolicyCapabilitySnapshot struct {
	Providers   []AccessPolicyProviderCapability
	DurableMail bool
}

type accessPolicyCapabilitySource interface {
	Snapshot() AccessPolicyCapabilitySnapshot
}

// deploymentAccessPolicyCapabilities projects only safe process capabilities
// from the live registry and mail transport. Secrets never enter policy code.
type deploymentAccessPolicyCapabilities struct {
	providers externalProviderSource
	mail      MailDeliverySender
	health    interface{ Code() string }
}

func (c deploymentAccessPolicyCapabilities) Snapshot() AccessPolicyCapabilitySnapshot {
	durableMail := c.mail != nil && c.mail.Enabled() && c.health != nil && c.health.Code() == MailHealthHealthy
	result := AccessPolicyCapabilitySnapshot{DurableMail: durableMail}
	if c.providers == nil {
		return result
	}
	for _, descriptor := range c.providers.Descriptors() {
		provider, available := c.providers.Provider(descriptor.Id)
		if !available || provider == nil {
			continue
		}
		result.Providers = append(result.Providers, AccessPolicyProviderCapability{
			Descriptor: descriptor, AutoProvision: provider.AutoProvision(),
		})
	}
	sort.Slice(result.Providers, func(i, j int) bool {
		return result.Providers[i].Descriptor.Id < result.Providers[j].Descriptor.Id
	})
	return result
}

type accessPolicyInstitutionStore interface {
	GetSingleton(context.Context) (*model.Institution, error)
}

type accessPolicyAuthorizer interface {
	Authorize(context.Context, Invocation, model.Action, model.Resource) error
}

type accessPolicyChanged struct {
	InstitutionID      model.InstitutionID
	Revision           int64
	SessionRevocations []store.AccessPolicySessionRevocation
}

type accessPolicyEffects interface {
	Changed(context.Context, accessPolicyChanged) error
}

type accessPolicyEffectFailures interface {
	Report(context.Context, string, error)
}

type accessPolicyService struct {
	policies                store.AccessPolicyStore
	institutions            accessPolicyInstitutionStore
	authorization           accessPolicyAuthorizer
	capabilities            accessPolicyCapabilitySource
	audit                   mutationAuditor
	effects                 accessPolicyEffects
	effectFailures          accessPolicyEffectFailures
	canonicalOrigin         string
	recentAuthenticationTTL time.Duration
	now                     func() time.Time
}

func newAccessPolicyService(policies store.AccessPolicyStore, institutions accessPolicyInstitutionStore,
	authorization accessPolicyAuthorizer, capabilities accessPolicyCapabilitySource,
	audit mutationAuditor, effects accessPolicyEffects, failures accessPolicyEffectFailures,
	canonicalOrigin string, recentTTL time.Duration, now func() time.Time,
) (*accessPolicyService, error) {
	parsed, err := url.Parse(canonicalOrigin)
	if policies == nil || institutions == nil || authorization == nil || capabilities == nil || audit == nil ||
		effects == nil || failures == nil || now == nil || recentTTL <= 0 || err != nil || parsed.Scheme == "" ||
		parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("access policy service dependencies are invalid")
	}
	return &accessPolicyService{policies: policies, institutions: institutions, authorization: authorization,
		capabilities: capabilities, audit: audit, effects: effects, effectFailures: failures,
		canonicalOrigin: canonicalOrigin, recentAuthenticationTTL: recentTTL, now: now}, nil
}

type AccessPolicyView struct {
	Policy             *model.AccessPolicy
	History            []*model.AccessPolicyTransition
	AvailableProviders []model.ExternalAuthenticationProvider
	DurableMail        bool
}

type PreflightAccessPolicyCommand struct {
	ExpectedRevision       int64
	Settings               model.AccessPolicySettings
	RevokeExistingSessions bool
}

type AccessPolicyPreflightResult struct{ Blockers []store.AccessPolicyBlocker }

type ReplaceAccessPolicyCommand struct {
	ExpectedRevision       int64
	Settings               model.AccessPolicySettings
	RevokeExistingSessions bool
	IdempotencyKey         string
}

func (a *App) GetAccessPolicy(ctx context.Context, invocation Invocation) (AccessPolicyView, error) {
	if a == nil || a.accessPolicies == nil {
		return AccessPolicyView{}, NewError("access_policy.unavailable")
	}
	return a.accessPolicies.Read(ctx, invocation)
}

func (a *App) PreflightAccessPolicy(ctx context.Context, invocation Invocation, command PreflightAccessPolicyCommand) (AccessPolicyPreflightResult, error) {
	if a == nil || a.accessPolicies == nil {
		return AccessPolicyPreflightResult{}, NewError("access_policy.unavailable")
	}
	return a.accessPolicies.Preflight(ctx, invocation, command)
}

func (a *App) ReplaceAccessPolicy(ctx context.Context, invocation Invocation, command ReplaceAccessPolicyCommand) (AccessPolicyView, error) {
	if a == nil || a.accessPolicies == nil {
		return AccessPolicyView{}, NewError("access_policy.unavailable")
	}
	return a.accessPolicies.Replace(ctx, invocation, command)
}

func (a *App) DiscoverAccess(ctx context.Context) (PublicAccessDiscovery, error) {
	if a == nil || a.accessPolicies == nil {
		return PublicAccessDiscovery{}, NewError("access_policy.unavailable")
	}
	return a.accessPolicies.Discover(ctx)
}

func (s *accessPolicyService) Read(ctx context.Context, invocation Invocation) (AccessPolicyView, error) {
	institution, resource, err := s.authorize(ctx, invocation, model.ActionAccessPolicyView)
	_ = institution
	_ = resource
	if err != nil {
		return AccessPolicyView{}, err
	}
	snapshot, err := s.policies.Get(ctx, model.AccessPolicyTransitionHistoryLimit)
	if err != nil || snapshot == nil || snapshot.Policy == nil {
		return AccessPolicyView{}, accessPolicyError(err)
	}
	return s.view(snapshot), nil
}

func (s *accessPolicyService) Preflight(ctx context.Context, invocation Invocation, command PreflightAccessPolicyCommand) (AccessPolicyPreflightResult, error) {
	if err := s.requireStrongRecent(invocation); err != nil {
		return AccessPolicyPreflightResult{}, err
	}
	if _, _, err := s.authorize(ctx, invocation, model.ActionAccessPolicyManage); err != nil {
		return AccessPolicyPreflightResult{}, err
	}
	input, err := s.preflightInput(command.ExpectedRevision, command.Settings, command.RevokeExistingSessions)
	if err != nil {
		return AccessPolicyPreflightResult{}, err
	}
	blockers, err := s.policies.Preflight(ctx, input)
	if err != nil {
		return AccessPolicyPreflightResult{}, accessPolicyError(err)
	}
	return AccessPolicyPreflightResult{Blockers: append([]store.AccessPolicyBlocker(nil), blockers...)}, nil
}

func (s *accessPolicyService) Replace(ctx context.Context, invocation Invocation, command ReplaceAccessPolicyCommand) (AccessPolicyView, error) {
	if err := s.requireStrongRecent(invocation); err != nil {
		return AccessPolicyView{}, err
	}
	institution, _, err := s.authorize(ctx, invocation, model.ActionAccessPolicyManage)
	if err != nil {
		return AccessPolicyView{}, err
	}
	if command.IdempotencyKey == "" {
		return AccessPolicyView{}, NewError("idempotency.key_required")
	}
	input, err := s.preflightInput(command.ExpectedRevision, command.Settings, command.RevokeExistingSessions)
	if err != nil {
		return AccessPolicyView{}, err
	}
	idempotency, err := newCommandIdempotency(invocation, "access_policy.replace.v1", command.IdempotencyKey,
		struct {
			ExpectedRevision       int64                      `json:"expected_revision"`
			Settings               model.AccessPolicySettings `json:"settings"`
			RevokeExistingSessions bool                       `json:"revoke_existing_sessions"`
		}{command.ExpectedRevision, command.Settings, command.RevokeExistingSessions})
	if err != nil {
		return AccessPolicyView{}, err
	}
	stored, err := runAuditedMutation(ctx, s.audit, mutationAttempt{
		Invocation: invocation, Action: model.ActionAccessPolicyManage,
		Resource:  model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()},
		ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(), Operation: "replace",
		Value: accessPolicyReplacementAuditValue(command),
	}, s.now, func(ctx context.Context, reference mutationAttemptReference) (*store.AccessPolicyReplacementResult, error) {
		return s.policies.Replace(ctx, &store.AccessPolicyReplacement{Preflight: *input,
			ActorID: invocation.Principal().UserID, AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis}, idempotency)
	}, accessPolicyError)
	if err != nil {
		return AccessPolicyView{}, err
	}
	if stored == nil || stored.Snapshot == nil || stored.Snapshot.Policy == nil {
		return AccessPolicyView{}, NewError("access_policy.unavailable")
	}
	if stored.Changed && !stored.Replayed {
		if effectErr := s.effects.Changed(ctx, accessPolicyChanged{InstitutionID: institution.ID,
			Revision: stored.Snapshot.Policy.Revision, SessionRevocations: stored.SessionRevocations}); effectErr != nil {
			s.effectFailures.Report(ctx, "access_policy_changed", effectErr)
		}
	}
	return s.view(stored.Snapshot), nil
}

func accessPolicyReplacementAuditValue(command ReplaceAccessPolicyCommand) map[string]any {
	settings := command.Settings
	return map[string]any{
		"expected_revision":                   command.ExpectedRevision,
		"revoke_existing_sessions":            command.RevokeExistingSessions,
		"local_login_enabled":                 settings.LocalLoginEnabled,
		"public_registration_enabled":         settings.PublicRegistrationEnabled,
		"invitation_admission_enabled":        settings.InvitationAdmissionEnabled,
		"invitation_local_credential_enabled": settings.InvitationLocalCredentialEnabled,
		"desktop_authorization_enabled":       settings.DesktopAuthorizationEnabled,
		"provider_admissions":                 settings.ProviderAdmissions,
	}
}

type accessPolicyRealtimeEffects struct{ realtime *realtimeService }

func (e accessPolicyRealtimeEffects) Changed(ctx context.Context, change accessPolicyChanged) error {
	for _, revocation := range change.SessionRevocations {
		e.realtime.SessionsRevoked(ctx, revocation.UserID.String(), sessionIDStrings(revocation.SessionIDs), revocation.AccessTokenHashes)
	}
	event, err := apprealtime.NewAccessPolicyChangedEvent(change.InstitutionID, change.Revision)
	if err != nil {
		return err
	}
	return e.realtime.Publish(ctx, event)
}

func sessionIDStrings(ids []model.SessionID) []string {
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		result = append(result, id.String())
	}
	return result
}

type accessPolicyEffectReporter struct{ realtime *realtimeService }

func (r accessPolicyEffectReporter) Report(ctx context.Context, operation string, err error) {
	r.realtime.reportTransientFailure(ctx, operation, err)
}

func (s *accessPolicyService) authorize(ctx context.Context, invocation Invocation, action model.Action) (*model.Institution, model.Resource, error) {
	institution, err := s.institutions.GetSingleton(ctx)
	if err != nil || institution == nil || !institution.ID.IsValid() {
		return nil, model.Resource{}, accessPolicyError(err)
	}
	resource := model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}
	if err = s.authorization.Authorize(ctx, invocation, action, resource); err != nil {
		return nil, model.Resource{}, err
	}
	return institution, resource, nil
}

func (s *accessPolicyService) requireStrongRecent(invocation Invocation) error {
	return requireStrongRecentSession(invocation.Principal(), s.now(), s.recentAuthenticationTTL)
}

func (s *accessPolicyService) preflightInput(expected int64, settings model.AccessPolicySettings, revokeExistingSessions bool) (*store.AccessPolicyPreflight, error) {
	if expected < 1 || settings.Validate() != nil {
		return nil, NewError("access_policy.invalid")
	}
	capabilities := accessDeploymentCapabilities(s.capabilities.Snapshot())
	return &store.AccessPolicyPreflight{ExpectedRevision: expected, Settings: settings,
		RevokeExistingSessions: revokeExistingSessions,
		Capabilities:           capabilities,
		CheckedAt:              model.TimeUTC(s.now())}, nil
}

func accessDeploymentCapabilities(snapshot AccessPolicyCapabilitySnapshot) store.AccessDeploymentCapabilities {
	providers := make(map[string]store.AccessProviderCapability, len(snapshot.Providers))
	for _, provider := range snapshot.Providers {
		if provider.Descriptor.Id != "" {
			providers[provider.Descriptor.Id] = store.AccessProviderCapability{AutoProvision: provider.AutoProvision}
		}
	}
	return store.AccessDeploymentCapabilities{Providers: providers, DurableMail: snapshot.DurableMail}
}

func (s *accessPolicyService) view(snapshot *store.AccessPolicySnapshot) AccessPolicyView {
	if snapshot == nil || snapshot.Policy == nil {
		return AccessPolicyView{}
	}
	capabilities := s.capabilities.Snapshot()
	providers := make([]model.ExternalAuthenticationProvider, 0, len(capabilities.Providers))
	for _, provider := range capabilities.Providers {
		providers = append(providers, provider.Descriptor)
	}
	return AccessPolicyView{Policy: snapshot.Policy.Clone(), History: cloneAccessPolicyHistory(snapshot.History),
		AvailableProviders: providers, DurableMail: capabilities.DurableMail}
}

type PublicInstitutionPresentation struct {
	ID                model.InstitutionID
	Name, DisplayName string
}
type PublicAccessCapabilities struct {
	LocalLogin, PublicRegistration, InvitationAdmission, DesktopAuthorization bool
}
type DesktopAuthorizationCompatibility struct {
	Protocol                       string
	MinimumVersion, MaximumVersion int
}
type PublicAccessDiscovery struct {
	DiscoveryVersion     int
	CanonicalOrigin      string
	InstallationID       string
	Initialized          bool
	Institution          *PublicInstitutionPresentation
	PolicyRevision       int64
	Capabilities         PublicAccessCapabilities
	Providers            []model.ExternalAuthenticationProvider
	DesktopAuthorization DesktopAuthorizationCompatibility
}

func (s *accessPolicyService) Discover(ctx context.Context) (PublicAccessDiscovery, error) {
	result := PublicAccessDiscovery{DiscoveryVersion: accessDiscoveryVersion, CanonicalOrigin: s.canonicalOrigin,
		DesktopAuthorization: DesktopAuthorizationCompatibility{Protocol: "proctor-desktop-authorization", MinimumVersion: desktopAuthorizationProtocolMin, MaximumVersion: desktopAuthorizationProtocolMax}}
	institution, err := s.institutions.GetSingleton(ctx)
	if store.IsNotFound(err) {
		return result, nil
	}
	if err != nil || institution == nil || !institution.ID.IsValid() {
		return PublicAccessDiscovery{}, accessPolicyError(err)
	}
	snapshot, err := s.policies.Get(ctx, 0)
	if err != nil || snapshot == nil || snapshot.Policy == nil {
		return PublicAccessDiscovery{}, accessPolicyError(err)
	}
	result.Initialized, result.InstallationID = true, institution.ID.String()
	result.Institution = &PublicInstitutionPresentation{ID: institution.ID, Name: institution.Name, DisplayName: institution.DisplayName}
	policy := snapshot.Policy
	result.PolicyRevision = policy.Revision
	result.Capabilities = PublicAccessCapabilities{LocalLogin: policy.LocalLoginEnabled, PublicRegistration: policy.PublicRegistrationEnabled,
		InvitationAdmission: policy.InvitationAdmissionEnabled, DesktopAuthorization: policy.DesktopAuthorizationEnabled}
	for _, provider := range s.capabilities.Snapshot().Providers {
		if _, enabled := policy.ProviderAdmissions[provider.Descriptor.Id]; enabled {
			result.Providers = append(result.Providers, provider.Descriptor)
		}
	}
	sort.Slice(result.Providers, func(i, j int) bool { return result.Providers[i].Id < result.Providers[j].Id })
	return result, nil
}

func cloneAccessPolicyHistory(history []*model.AccessPolicyTransition) []*model.AccessPolicyTransition {
	result := make([]*model.AccessPolicyTransition, 0, len(history))
	for _, item := range history {
		if item != nil {
			clone := *item
			clone.ChangedFields = append([]string(nil), item.ChangedFields...)
			result = append(result, &clone)
		}
	}
	return result
}

func blockedAccessPolicyError(blockers []store.AccessPolicyBlocker) error {
	if len(blockers) == 0 {
		return nil
	}
	return NewError("access_policy.blocked").WithField("blocker", string(blockers[0].Code))
}

func accessPolicyModelError(err error) error {
	if errors.Is(err, model.ErrAccessPolicyRevisionConflict) {
		return NewError("access_policy.revision_conflict").Wrap(err)
	}
	return NewError("access_policy.invalid").Wrap(err)
}

func accessPolicyError(err error) error {
	var revision *store.ErrAccessPolicyRevisionConflict
	var blocked *store.ErrAccessPolicyBlocked
	switch {
	case errors.As(err, &revision):
		return NewError("access_policy.revision_conflict").WithField("current_revision", strconv.FormatInt(revision.CurrentRevision, 10)).Wrap(err)
	case errors.As(err, &blocked):
		return blockedAccessPolicyError(blocked.Blockers).(*Error).Wrap(err)
	case idempotencyError(err) != nil:
		return idempotencyError(err)
	default:
		return NewError("access_policy.unavailable").Wrap(err)
	}
}
