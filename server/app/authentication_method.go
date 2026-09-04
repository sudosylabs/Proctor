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
	"sort"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type AuthenticationMethodView struct {
	Password  bool
	Providers []AuthenticationProviderMethodView
}

type AuthenticationProviderMethodView struct {
	IdentityID  model.ExternalIdentityID
	ProviderID  string
	DisplayName string
	Type        string
}

type authenticationMethodService struct {
	passwords    store.PasswordCredentialStore
	identities   store.ExternalIdentityStore
	providers    externalProviderSource
	capabilities accessPolicyCapabilitySource
	hasher       *passwordHasher
	audit        mutationAuditor
	effects      authenticationSecurityEffects
	recentTTL    time.Duration
	now          func() time.Time
}

func newAuthenticationMethodService(passwords store.PasswordCredentialStore,
	identities store.ExternalIdentityStore, providers externalProviderSource, capabilities accessPolicyCapabilitySource,
	hasher *passwordHasher, audit mutationAuditor, effects authenticationSecurityEffects,
	recentTTL time.Duration, now func() time.Time) (*authenticationMethodService, error) {
	if passwords == nil || identities == nil || providers == nil || capabilities == nil ||
		hasher == nil || audit == nil || effects == nil || recentTTL <= 0 || now == nil {
		return nil, errors.New("authentication method service dependencies are required")
	}
	return &authenticationMethodService{passwords: passwords, identities: identities,
		providers: providers, capabilities: capabilities, hasher: hasher, audit: audit, effects: effects,
		recentTTL: recentTTL, now: now}, nil
}

func (a *App) AuthenticationMethods(ctx context.Context, invocation Invocation) (AuthenticationMethodView, error) {
	return a.authenticationMethods.list(ctx, invocation)
}

func (a *App) EnrollPassword(ctx context.Context, invocation Invocation, password string) error {
	return a.authenticationMethods.enrollPassword(ctx, invocation, password)
}

func (a *App) RemovePassword(ctx context.Context, invocation Invocation) error {
	return a.authenticationMethods.removePassword(ctx, invocation)
}

func (a *App) UnlinkExternalIdentity(ctx context.Context, invocation Invocation, id model.ExternalIdentityID) error {
	return a.authenticationMethods.unlink(ctx, invocation, id)
}

func (s *authenticationMethodService) require(invocation Invocation) error {
	return requireStrongRecentSession(invocation.Principal(), s.now(), s.recentTTL)
}

func (s *authenticationMethodService) list(ctx context.Context, invocation Invocation) (AuthenticationMethodView, error) {
	if invocation.Principal().Validate() != nil {
		return AuthenticationMethodView{}, invalidTokenAppError()
	}
	userID := invocation.Principal().UserID.String()
	view := AuthenticationMethodView{Providers: []AuthenticationProviderMethodView{}}
	if _, err := s.passwords.GetByUser(ctx, userID); err == nil {
		view.Password = true
	} else if !store.IsNotFound(err) {
		return view, authenticationUnavailable(err)
	}
	identities, err := s.identities.ListByUser(ctx, userID)
	if err != nil {
		return view, authenticationUnavailable(err)
	}
	descriptors := map[string]model.ExternalAuthenticationProvider{}
	for _, descriptor := range s.providers.Descriptors() {
		descriptors[descriptor.Id] = descriptor
	}
	for _, identity := range identities {
		descriptor, ok := descriptors[identity.Provider]
		if !ok {
			descriptor = model.ExternalAuthenticationProvider{Id: identity.Provider, DisplayName: identity.Provider}
		}
		view.Providers = append(view.Providers, AuthenticationProviderMethodView{IdentityID: identity.ID,
			ProviderID: identity.Provider, DisplayName: descriptor.DisplayName, Type: descriptor.Type})
	}
	sort.Slice(view.Providers, func(i, j int) bool {
		return view.Providers[i].IdentityID.String() < view.Providers[j].IdentityID.String()
	})
	return view, nil
}

func (s *authenticationMethodService) enrollPassword(ctx context.Context, invocation Invocation, password string) error {
	if err := s.require(invocation); err != nil {
		return err
	}
	hash, err := s.hasher.Hash(password)
	if err != nil {
		return NewError("authentication.password.invalid")
	}
	userID := invocation.Principal().UserID
	_, appErr := runAuditedMutation(ctx, s.audit, mutationAttempt{Invocation: invocation,
		Action: model.ActionExternalIdentityManage, Resource: model.Resource{Type: model.ResourceUser, ID: userID.String()},
		Operation: "enroll_password", Value: map[string]any{"method": "password"}}, s.now,
		func(ctx context.Context, reference mutationAttemptReference) (*store.AuthenticationMethodMutationResult, error) {
			return s.passwords.EnrollWithAudit(ctx, &store.PasswordCredentialEnrollment{
				Credential:   &model.PasswordCredential{UserID: userID, PasswordHash: hash},
				Capabilities: accessDeploymentCapabilities(s.capabilities.Snapshot()), AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis})
		}, authenticationMethodMutationError)
	return appErr
}

func (s *authenticationMethodService) removePassword(ctx context.Context, invocation Invocation) error {
	if err := s.require(invocation); err != nil {
		return err
	}
	userID := invocation.Principal().UserID
	result, appErr := runAuditedMutation(ctx, s.audit, mutationAttempt{Invocation: invocation,
		Action: model.ActionExternalIdentityManage, Resource: model.Resource{Type: model.ResourceUser, ID: userID.String()},
		Operation: "remove_password", Value: map[string]any{"method": "password"}}, s.now,
		func(ctx context.Context, reference mutationAttemptReference) (*store.AuthenticationMethodMutationResult, error) {
			return s.passwords.RemoveWithAudit(ctx, &store.PasswordCredentialRemoval{UserID: userID,
				Capabilities: accessDeploymentCapabilities(s.capabilities.Snapshot()), ChangedAt: reference.MutationAtMillis,
				RevocationReason: model.SessionRevocationPasswordRemoved, AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis})
		}, authenticationMethodMutationError)
	if appErr == nil {
		s.publishRevocations(ctx, userID, result)
	}
	return appErr
}

func (s *authenticationMethodService) unlink(ctx context.Context, invocation Invocation, id model.ExternalIdentityID) error {
	if err := s.require(invocation); err != nil {
		return err
	}
	if !id.IsValid() {
		return NewError("request.invalid")
	}
	userID := invocation.Principal().UserID
	result, appErr := runAuditedMutation(ctx, s.audit, mutationAttempt{Invocation: invocation,
		Action: model.ActionExternalIdentityManage, Resource: model.Resource{Type: model.ResourceUser, ID: userID.String()},
		Operation: "unlink_provider", Value: map[string]any{"identity_id": id.String()}}, s.now,
		func(ctx context.Context, reference mutationAttemptReference) (*store.AuthenticationMethodMutationResult, error) {
			return s.identities.UnlinkWithAudit(ctx, &store.ExternalIdentityUnlink{ID: id, UserID: userID,
				Capabilities: accessDeploymentCapabilities(s.capabilities.Snapshot()), ChangedAt: reference.MutationAtMillis,
				RevocationReason: model.SessionRevocationExternalIdentityUnlinked, AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis})
		}, authenticationMethodMutationError)
	if appErr == nil {
		s.publishRevocations(ctx, userID, result)
	}
	return appErr
}

func (s *authenticationMethodService) publishRevocations(ctx context.Context, userID model.UserID, result *store.AuthenticationMethodMutationResult) {
	if result == nil {
		return
	}
	ids := make([]string, 0, len(result.RevokedSessions))
	for _, session := range result.RevokedSessions {
		ids = append(ids, session.ID.String())
	}
	s.effects.SessionsRevoked(ctx, userID.String(), ids, result.RevokedTokenHashes)
}
