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

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/app/mail"
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

// authenticationMethodServiceDependencies are the collaborators of authenticationMethodService;
// newAuthenticationMethodService validates them once and authenticationMethodService keeps them in deps.
type authenticationMethodServiceDependencies struct {
	Users        store.UserStore
	Mail         passwordChangedMailPreparer
	Passwords    store.PasswordCredentialStore
	Identities   store.ExternalIdentityStore
	Providers    externalProviderSource
	Capabilities accessPolicyCapabilitySource
	Hasher       *passwordHasher
	Audit        mutationAuditor
	Effects      authenticationSecurityEffects
	RecentTTL    time.Duration
	Now          func() time.Time
}

type authenticationMethodService struct {
	deps authenticationMethodServiceDependencies
}

// passwordChangedMailPreparer freezes the security notice a self-service
// password change sends to the account address.
type passwordChangedMailPreparer interface {
	PreparePasswordChanged(mail.NoticePreparation) (*store.PreparedMail, error)
}

// ChangePasswordCommand replaces the current User's existing local password.
type ChangePasswordCommand struct {
	Password string
}

func newAuthenticationMethodService(deps authenticationMethodServiceDependencies) (*authenticationMethodService, error) {
	if deps.Users == nil || deps.Mail == nil || deps.Passwords == nil || deps.Identities == nil || deps.Providers == nil || deps.Capabilities == nil ||
		deps.Hasher == nil || deps.Audit == nil || deps.Effects == nil || deps.RecentTTL <= 0 || deps.Now == nil {
		return nil, errors.New("authentication method service dependencies are required")
	}
	return &authenticationMethodService{deps: deps}, nil
}

func (s *authenticationMethodService) require(invocation invocation.Invocation) error {
	return requireStrongRecentSession(invocation.Principal(), s.deps.Now(), s.deps.RecentTTL)
}

func (s *authenticationMethodService) AuthenticationMethods(ctx context.Context, invocation invocation.Invocation) (AuthenticationMethodView, error) {
	if invocation.Principal().Validate() != nil {
		return AuthenticationMethodView{}, invalidTokenAppError()
	}
	userID := invocation.Principal().UserID
	view := AuthenticationMethodView{Providers: []AuthenticationProviderMethodView{}}
	if _, err := s.deps.Passwords.GetByUser(ctx, userID); err == nil {
		view.Password = true
	} else if !store.IsNotFound(err) {
		return view, authenticationUnavailable(err)
	}
	identities, err := s.deps.Identities.ListByUser(ctx, userID)
	if err != nil {
		return view, authenticationUnavailable(err)
	}
	descriptors := map[string]model.ExternalAuthenticationProvider{}
	for _, descriptor := range s.deps.Providers.Descriptors() {
		descriptors[descriptor.ID] = descriptor
	}
	for _, identity := range identities {
		descriptor, ok := descriptors[identity.Provider]
		if !ok {
			descriptor = model.ExternalAuthenticationProvider{ID: identity.Provider, DisplayName: identity.Provider}
		}
		view.Providers = append(view.Providers, AuthenticationProviderMethodView{IdentityID: identity.ID,
			ProviderID: identity.Provider, DisplayName: descriptor.DisplayName, Type: descriptor.Type})
	}
	sort.Slice(view.Providers, func(i, j int) bool {
		return view.Providers[i].IdentityID.String() < view.Providers[j].IdentityID.String()
	})
	return view, nil
}

func (s *authenticationMethodService) EnrollPassword(ctx context.Context, invocation invocation.Invocation, password string) error {
	if err := s.require(invocation); err != nil {
		return err
	}
	userID := invocation.Principal().UserID
	user, err := s.deps.Users.Get(ctx, userID)
	if err != nil {
		return authenticationMethodStoreErrors.Translate(err)
	}
	if err := checkNewPassword(password, passwordIdentityOf(user)); err != nil {
		return err
	}
	hash, err := s.deps.Hasher.Hash(ctx, password)
	if err != nil {
		return passwordHashError(err, "authentication.internal")
	}
	_, appErr := runAuditedMutation(ctx, s.deps.Audit, mutationAttempt{Invocation: invocation,
		Action: model.ActionExternalIdentityManage, Resource: model.Resource{Type: model.ResourceUser, ID: userID.String()},
		Operation: "enroll_password", Value: map[string]any{"method": "password"}}, s.deps.Now,
		func(ctx context.Context, reference mutationAttemptReference) (*store.AuthenticationMethodMutationResult, error) {
			return s.deps.Passwords.Enroll(ctx, &store.PasswordCredentialEnrollment{
				Credential: &model.PasswordCredential{UserID: userID, PasswordHash: hash}, Reuse: s.deps.Hasher.ReuseCheck(password),
				Capabilities: accessDeploymentCapabilities(s.deps.Capabilities.Snapshot()), AuditEventID: reference.ID, AuditAt: reference.MutationAt})
		}, authenticationMethodStoreErrors.Translate)
	return appErr
}

// ChangePassword replaces the current User's existing local password after
// strong recent authentication. The calling Session stays live; every other
// Session of the User is revoked, outstanding password-reset links stop
// working, and the account address receives the password-changed notice.
func (s *authenticationMethodService) ChangePassword(ctx context.Context, invocation invocation.Invocation, command ChangePasswordCommand) error {
	if err := s.require(invocation); err != nil {
		return err
	}
	principal := invocation.Principal()
	user, err := s.deps.Users.Get(ctx, principal.UserID)
	if err != nil {
		return authenticationMethodStoreErrors.Translate(err)
	}
	if err := checkNewPassword(command.Password, passwordIdentityOf(user)); err != nil {
		return err
	}
	hash, err := s.deps.Hasher.Hash(ctx, command.Password)
	if err != nil {
		return passwordHashError(err, "authentication.internal")
	}
	prepared, err := s.deps.Mail.PreparePasswordChanged(mail.NoticePreparation{Recipient: user, At: model.TimeUTC(s.deps.Now())})
	if err != nil {
		return apperror.New("authentication.method.unavailable").Wrap(err)
	}
	result, appErr := runAuditedMutation(ctx, s.deps.Audit, mutationAttempt{Invocation: invocation,
		Action: model.ActionExternalIdentityManage, Resource: model.Resource{Type: model.ResourceUser, ID: user.ID.String()},
		Operation: "change_password", Value: map[string]any{"method": "password"}}, s.deps.Now,
		func(ctx context.Context, reference mutationAttemptReference) (*store.AuthenticationMethodMutationResult, error) {
			return s.deps.Passwords.Change(ctx, &store.PasswordCredentialChange{UserID: user.ID, KeepSessionID: principal.SessionID,
				PasswordHash: hash, Reuse: s.deps.Hasher.ReuseCheck(command.Password), RevocationReason: model.SessionRevocationPasswordChanged,
				Occurrence: prepared.Occurrence, Delivery: prepared.Delivery, Job: prepared.Job,
				AuditEventID: reference.ID, AuditAt: reference.MutationAt})
		}, authenticationMethodStoreErrors.Translate)
	if appErr == nil {
		s.publishRevocations(ctx, user.ID, result)
	}
	return appErr
}

func (s *authenticationMethodService) RemovePassword(ctx context.Context, invocation invocation.Invocation) error {
	if err := s.require(invocation); err != nil {
		return err
	}
	userID := invocation.Principal().UserID
	result, appErr := runAuditedMutation(ctx, s.deps.Audit, mutationAttempt{Invocation: invocation,
		Action: model.ActionExternalIdentityManage, Resource: model.Resource{Type: model.ResourceUser, ID: userID.String()},
		Operation: "remove_password", Value: map[string]any{"method": "password"}}, s.deps.Now,
		func(ctx context.Context, reference mutationAttemptReference) (*store.AuthenticationMethodMutationResult, error) {
			return s.deps.Passwords.Remove(ctx, &store.PasswordCredentialRemoval{UserID: userID,
				Capabilities: accessDeploymentCapabilities(s.deps.Capabilities.Snapshot()), ChangedAt: reference.MutationAt,
				RevocationReason: model.SessionRevocationPasswordRemoved, AuditEventID: reference.ID, AuditAt: reference.MutationAt})
		}, authenticationMethodStoreErrors.Translate)
	if appErr == nil {
		s.publishRevocations(ctx, userID, result)
	}
	return appErr
}

func (s *authenticationMethodService) UnlinkExternalIdentity(ctx context.Context, invocation invocation.Invocation, id model.ExternalIdentityID) error {
	if err := s.require(invocation); err != nil {
		return err
	}
	if !id.IsValid() {
		return apperror.New("request.invalid")
	}
	userID := invocation.Principal().UserID
	result, appErr := runAuditedMutation(ctx, s.deps.Audit, mutationAttempt{Invocation: invocation,
		Action: model.ActionExternalIdentityManage, Resource: model.Resource{Type: model.ResourceUser, ID: userID.String()},
		Operation: "unlink_provider", Value: map[string]any{"identity_id": id.String()}}, s.deps.Now,
		func(ctx context.Context, reference mutationAttemptReference) (*store.AuthenticationMethodMutationResult, error) {
			return s.deps.Identities.Unlink(ctx, &store.ExternalIdentityUnlink{ID: id, UserID: userID,
				Capabilities: accessDeploymentCapabilities(s.deps.Capabilities.Snapshot()), ChangedAt: reference.MutationAt,
				RevocationReason: model.SessionRevocationExternalIdentityUnlinked, AuditEventID: reference.ID, AuditAt: reference.MutationAt})
		}, authenticationMethodStoreErrors.Translate)
	if appErr == nil {
		s.publishRevocations(ctx, userID, result)
	}
	return appErr
}

func (s *authenticationMethodService) publishRevocations(ctx context.Context, userID model.UserID, result *store.AuthenticationMethodMutationResult) {
	if result == nil {
		return
	}
	s.deps.Effects.SessionsRevoked(ctx, userID, sessionIDs(result.RevokedSessions), result.RevokedTokenHashes)
}
