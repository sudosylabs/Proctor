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
	"strings"
	"time"

	appmail "github.com/sudosylabs/proctor/server/app/mail"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// mfaAudit preserves the exact critical-audit ordering of MFA mutations while
// keeping the focused application service independent of the audit service.
type mfaAudit interface {
	Begin(context.Context, Invocation, model.Action, model.Resource, any) (string, error)
	Complete(context.Context, string, model.AuditStatus, string, any) error
}

type mfaResetAuthorizer interface {
	Authorize(context.Context, model.Principal, model.Action, model.Resource, model.RequestMetadata) error
}

type mfaSecurityDependencies struct {
	authorization mfaResetAuthorizer
	capabilities  accessPolicyCapabilitySource
	attempts      *authenticationAttemptAccounting
	rateLimit     LoginRateLimitPolicy
}

type mfaEffects interface {
	SessionsRevoked(context.Context, string, []string, []string)
}

// mfaApplicationService owns enrollment, verification, recovery-code, and
// assurance-transition policy. mfaMechanics remains the cryptographic owner.
type mfaApplicationService struct {
	users                   store.UserStore
	credentials             store.MFAStore
	sessions                store.SessionStore
	institutions            store.InstitutionStore
	audit                   mfaAudit
	effects                 mfaEffects
	mail                    mfaNoticeMailPreparer
	security                mfaSecurityDependencies
	mechanics               *mfaMechanics
	recentAuthenticationTTL time.Duration
	now                     func() time.Time
}

func newMFAApplicationService(
	users store.UserStore,
	credentials store.MFAStore,
	sessions store.SessionStore,
	institutions store.InstitutionStore,
	audit mfaAudit,
	effects mfaEffects,
	mail mfaNoticeMailPreparer,
	mechanics *mfaMechanics,
	security mfaSecurityDependencies,
	recentAuthenticationTTL time.Duration,
	now func() time.Time,
) (*mfaApplicationService, error) {
	switch {
	case users == nil:
		return nil, errors.New("MFA user store is required")
	case credentials == nil:
		return nil, errors.New("MFA store is required")
	case sessions == nil:
		return nil, errors.New("MFA session store is required")
	case institutions == nil:
		return nil, errors.New("MFA institution store is required")
	case audit == nil:
		return nil, errors.New("MFA audit is required")
	case effects == nil:
		return nil, errors.New("MFA effects are required")
	case mail == nil:
		return nil, errors.New("MFA mail preparer is required")
	case mechanics == nil:
		return nil, errors.New("MFA mechanics are required")
	case security.authorization == nil || security.capabilities == nil || security.attempts == nil:
		return nil, errors.New("MFA security dependencies are required")
	case security.rateLimit.MaximumAttempts <= 0 || security.rateLimit.MaximumSourceAttempts <= 0 || security.rateLimit.Window <= 0:
		return nil, errors.New("MFA attempt policy is required")
	case recentAuthenticationTTL <= 0:
		return nil, errors.New("MFA recent authentication TTL must be positive")
	case now == nil:
		return nil, errors.New("MFA clock is required")
	}
	return &mfaApplicationService{
		users: users, credentials: credentials, sessions: sessions,
		institutions: institutions, audit: audit, effects: effects,
		mail: mail, mechanics: mechanics, security: security, recentAuthenticationTTL: recentAuthenticationTTL,
		now: now,
	}, nil
}

func (s *mfaApplicationService) GetStatus(ctx context.Context, invocation Invocation) (*MFAStatus, error) {
	principal := invocation.Principal()
	if err := s.requireRecoverySession(principal, false); err != nil {
		return nil, err
	}
	status := &MFAStatus{ServiceEnabled: s.mechanics.settings.Enabled, MFARecoveryRequired: principal.MFARecoveryRequired,
		AuthenticationMethod: principal.AuthenticationMethod, AuthenticationProviderID: principal.AuthenticationProviderID,
		AuthenticationStrength: principal.AuthenticationStrength, RecentlyAuthenticated: principal.IsRecentlyAuthenticated(s.now(), s.recentAuthenticationTTL)}
	credential, err := s.credentials.GetByUser(ctx, principal.UserID.String())
	if store.IsNotFound(err) {
		return status, nil
	}
	if err != nil {
		return nil, mfaStoreFailure(err)
	}
	status.Enabled = credential.IsActive()
	status.Pending = credential.IsPendingAt(model.TimeUTC(s.now()))
	status.PendingExpiresAt = credential.PendingExpiresAt
	if status.Enabled {
		status.RecoveryCodesRemaining, err = s.credentials.CountRecoveryCodes(ctx, principal.UserID.String())
		if err != nil {
			return nil, mfaStoreFailure(err)
		}
	}
	return status, nil
}

func (s *mfaApplicationService) Setup(
	ctx context.Context,
	invocation Invocation,
	command SetupMFACommand,
) (*MFASetup, error) {
	principal := invocation.Principal()
	if err := s.requireRecoverySession(principal, true); err != nil {
		return nil, err
	}
	if err := s.requireEnabled(); err != nil {
		return nil, err
	}
	user, err := s.users.Get(ctx, principal.UserID.String())
	if err != nil {
		return nil, mfaStoreFailure(err)
	}
	accountName := command.AccountName
	if strings.TrimSpace(accountName) == "" {
		accountName = user.Email
	}
	secret, err := s.mechanics.newSecret()
	if err != nil {
		return nil, authenticationUnavailable(err)
	}
	sealed, err := s.mechanics.sealTOTPSecret(principal.UserID.String(), secret)
	if err != nil {
		return nil, authenticationUnavailable(err)
	}
	now := model.TimeUTC(s.now())
	candidate := &model.MFACredential{
		UserID: principal.UserID, State: model.MFAStatePending,
		EncryptedSecret: sealed.encoded, EncryptionKeyID: sealed.keyID,
		PendingExpiresAt: model.OptionalTimeFrom(now.Add(s.mechanics.settings.SetupTTL)),
		CreatedAt:        now,
	}
	resource, appErr := s.auditResource(ctx)
	if appErr != nil {
		return nil, appErr
	}
	auditID, appErr := s.audit.Begin(
		ctx, invocation, actionMFASetup, resource, nil,
	)
	if appErr != nil {
		return nil, appErr
	}
	saved, err := s.credentials.SavePendingWithAudit(ctx, &store.MFAPendingEnrollment{Principal: principal, Credential: candidate, Lifetime: s.mechanics.settings.SetupTTL, RecentAuthenticationTTL: s.recentAuthenticationTTL, AuditEventID: model.AuditEventID(auditID)})
	if err != nil {
		return nil, s.failMutation(ctx, auditID, "MFA", err)
	}
	return &MFASetup{
		Secret: secret,
		ProvisioningURI: mfaProvisioningURI(
			s.mechanics.settings.Issuer, accountName, secret,
		),
		ExpiresAt: saved.PendingExpiresAt.Time,
	}, nil
}

func (s *mfaApplicationService) Activate(
	ctx context.Context,
	invocation Invocation,
	command ActivateMFACommand,
) (*MFAActivation, error) {
	principal := invocation.Principal()
	if err := s.requireRecoverySession(principal, true); err != nil {
		return nil, err
	}
	if err := s.requireEnabled(); err != nil {
		return nil, err
	}
	if err := s.limitFactorAttempt(ctx, invocation); err != nil {
		return nil, err
	}
	credential, err := s.credentials.GetByUser(ctx, principal.UserID.String())
	if err != nil {
		return nil, mfaStoreFailure(err)
	}
	now := model.TimeUTC(s.now())
	if !credential.IsPendingAt(now) {
		return nil, mfaInvalidCodeError("ActivateMFA")
	}
	secret, err := s.mechanics.decrypt(principal.UserID.String(), credential)
	if err != nil {
		return nil, authenticationUnavailable(err)
	}
	timeStep, valid := verifyTOTP(secret, command.Code, 0, now)
	if !valid {
		return nil, mfaInvalidCodeError("ActivateMFA")
	}
	rawCodes, recoveryCodes, err := s.mechanics.newRecoveryCodes(
		principal.UserID.String(), s.mechanics.settings.RecoveryCodeCount,
	)
	if err != nil {
		return nil, authenticationUnavailable(err)
	}
	// The frozen notice retains its existing millisecond timestamp contract;
	// the credential and Session expiry decision uses the native instant.
	prepared, appErr := s.prepareSecurityNotice(
		ctx, principal.UserID, appmail.MFANoticeEnabled, model.TimeFromMillis(now.UnixMilli()),
	)
	if appErr != nil {
		return nil, appErr
	}
	resource, appErr := s.auditResource(ctx)
	if appErr != nil {
		return nil, appErr
	}
	auditID, appErr := s.audit.Begin(
		ctx, invocation, actionMFAActivate, resource, credential.Auditable(),
	)
	if appErr != nil {
		return nil, appErr
	}
	activated, err := s.credentials.Activate(ctx, &store.MFAActivationMutation{
		Principal: principal, RecentAuthenticationTTL: s.recentAuthenticationTTL,
		CredentialID: credential.ID.String(), UserID: principal.UserID.String(), TimeStep: timeStep,
		RecoveryCodes: recoveryCodes, SessionID: principal.SessionID.String(), At: now,
		AuditEventID: auditID, AuditAt: now.UnixMilli(), Notice: mfaSecurityNotice(prepared),
	})
	if err != nil {
		return nil, s.failMutation(ctx, auditID, "MFA", err)
	}
	s.effects.SessionsRevoked(
		ctx, principal.UserID.String(), []string{principal.SessionID.String()},
		activated.AccessTokenHashes,
	)
	return &MFAActivation{RecoveryCodes: rawCodes}, nil
}

func (s *mfaApplicationService) Challenge(
	ctx context.Context,
	invocation Invocation,
	command ChallengeMFACommand,
) (*model.Session, error) {
	principal := invocation.Principal()
	if err := s.requireInteractiveSession(principal, false); err != nil {
		return nil, err
	}
	if err := s.requireEnabled(); err != nil {
		return nil, err
	}
	if err := s.limitFactorAttempt(ctx, invocation); err != nil {
		return nil, err
	}
	resource, appErr := s.auditResource(ctx)
	if appErr != nil {
		return nil, appErr
	}
	auditID, appErr := s.audit.Begin(ctx, invocation, actionMFAChallenge, resource, nil)
	if appErr != nil {
		return nil, appErr
	}
	now := model.TimeUTC(s.now())
	proof, err := s.mechanics.verifySecondFactor(ctx, s.credentials, principal.UserID.String(), command.Code, now)
	if err != nil {
		code := "authentication.mfa.invalid_code"
		if failure, ok := As(err); ok {
			code = failure.Code()
		}
		if auditErr := s.audit.Complete(ctx, auditID, model.AuditStatusFail, code, nil); auditErr != nil {
			return nil, auditErr
		}
		return nil, err
	}
	result, err := s.credentials.ChallengeWithAudit(ctx, &store.MFAChallenge{Principal: principal, CredentialID: proof.credentialID, TimeStep: proof.step, RecoveryCodeHash: proof.recoveryHash, VerifiedAt: now, AuditEventID: model.AuditEventID(auditID)})
	if err != nil {
		if store.IsNotFound(err) {
			if auditErr := s.audit.Complete(ctx, auditID, model.AuditStatusFail, "authentication.mfa.invalid_code", nil); auditErr != nil {
				return nil, auditErr
			}
			return nil, mfaInvalidCodeError("ChallengeMFA")
		}
		return nil, s.failMutation(ctx, auditID, "ChallengeMFA", err)
	}
	if result == nil || result.Session == nil {
		return nil, authenticationUnavailable(errors.New("MFA challenge returned no Session"))
	}
	s.effects.SessionsRevoked(ctx, principal.UserID.String(), []string{principal.SessionID.String()}, result.AccessTokenHashes)
	return result.Session, nil
}

func (s *mfaApplicationService) RegenerateRecoveryCodes(
	ctx context.Context,
	invocation Invocation,
) ([]string, error) {
	principal := invocation.Principal()
	if err := s.requireStrongRecentSession(principal); err != nil {
		return nil, err
	}
	if err := s.requireEnabled(); err != nil {
		return nil, err
	}
	rawCodes, codes, err := s.mechanics.newRecoveryCodes(
		principal.UserID.String(), s.mechanics.settings.RecoveryCodeCount,
	)
	if err != nil {
		return nil, authenticationUnavailable(err)
	}
	now := model.TimeFromMillis(s.now().UnixMilli())
	prepared, appErr := s.prepareSecurityNotice(
		ctx, principal.UserID, appmail.MFANoticeRecoveryCodesRegenerated, now,
	)
	if appErr != nil {
		return nil, appErr
	}
	resource, appErr := s.auditResource(ctx)
	if appErr != nil {
		return nil, appErr
	}
	auditID, appErr := s.audit.Begin(
		ctx, invocation, actionMFARecoveryCodesRegenerate, resource, nil,
	)
	if appErr != nil {
		return nil, appErr
	}
	if err := s.credentials.ReplaceRecoveryCodes(ctx, &store.MFARecoveryCodesRegeneration{
		Principal: principal, RecentAuthenticationTTL: s.recentAuthenticationTTL,
		UserID: principal.UserID.String(), RecoveryCodes: codes, At: now.UnixMilli(),
		AuditEventID: auditID, AuditAt: now.UnixMilli(), Notice: mfaSecurityNotice(prepared),
	}); err != nil {
		return nil, s.failMutation(
			ctx, auditID, "RegenerateMFARecoveryCodes.replace", err,
		)
	}
	return rawCodes, nil
}

func (s *mfaApplicationService) Disable(
	ctx context.Context,
	invocation Invocation,
) error {
	principal := invocation.Principal()
	if err := s.requireStrongRecentSession(principal); err != nil {
		return err
	}
	if err := s.requireEnabled(); err != nil {
		return err
	}
	now := model.TimeFromMillis(s.now().UnixMilli())
	prepared, appErr := s.prepareSecurityNotice(
		ctx, principal.UserID, appmail.MFANoticeDisabled, now,
	)
	if appErr != nil {
		return appErr
	}
	resource, appErr := s.auditResource(ctx)
	if appErr != nil {
		return appErr
	}
	auditID, appErr := s.audit.Begin(ctx, invocation, actionMFADisable, resource, nil)
	if appErr != nil {
		return appErr
	}
	result, err := s.credentials.Disable(ctx, &store.MFADisablement{
		Principal: principal, RecentAuthenticationTTL: s.recentAuthenticationTTL,
		UserID: principal.UserID.String(), At: now.UnixMilli(), AuditEventID: auditID,
		AuditAt: now.UnixMilli(), Notice: mfaSecurityNotice(prepared),
	})
	if err != nil {
		return s.failMutation(ctx, auditID, "DisableMFA.disable", err)
	}
	s.effects.SessionsRevoked(
		ctx, principal.UserID.String(), nil, result.AccessTokenHashes,
	)
	return nil
}

func (s *mfaApplicationService) prepareSecurityNotice(
	ctx context.Context,
	userID model.UserID,
	kind appmail.MFANoticeKind,
	at time.Time,
) (*preparedDirectMail, error) {
	user, err := s.users.Get(ctx, userID.String())
	if err != nil {
		return nil, mfaStoreFailure(err)
	}
	prepared, err := s.mail.PrepareMFANotice(appmail.NoticePreparation{Recipient: user, At: at}, kind)
	if err != nil {
		return nil, authenticationUnavailable(err)
	}
	return prepared, nil
}

func mfaSecurityNotice(prepared *preparedDirectMail) store.MFASecurityNotice {
	if prepared == nil {
		return store.MFASecurityNotice{}
	}
	return store.MFASecurityNotice{
		Occurrence: prepared.Occurrence, Delivery: prepared.Delivery, Job: prepared.Job,
	}
}

// VerifyLogin is the narrow behavior Authentication consumes. Authentication
// does not retain or query the MFA application implementation.
func (s *mfaApplicationService) VerifyLogin(
	ctx context.Context,
	userID string,
	code string,
	at time.Time,
) (model.AuthenticationStrength, int64, error) {
	credential, err := s.credentials.GetByUser(ctx, userID)
	if store.IsNotFound(err) {
		return model.AuthenticationSingleFactor, 0, nil
	}
	if err != nil {
		return "", 0, authenticationUnavailable(err)
	}
	if !credential.IsActive() {
		return model.AuthenticationSingleFactor, 0, nil
	}
	if !s.mechanics.settings.Enabled {
		return "", 0, NewError("authentication.mfa.unavailable")
	}
	if strings.TrimSpace(code) == "" {
		return "", 0, NewError("authentication.mfa.required")
	}
	if appErr := s.consumeSecondFactor(ctx, userID, code, at); appErr != nil {
		return "", 0, appErr
	}
	return model.AuthenticationMultiFactor, at.UnixMilli(), nil
}

func (s *mfaApplicationService) consumeSecondFactor(
	ctx context.Context,
	userID string,
	code string,
	now time.Time,
) error {
	return s.mechanics.consumeSecondFactor(ctx, s.credentials, userID, code, now)
}

func (s *mfaApplicationService) requireEnabled() error {
	if s.mechanics.settings.Enabled {
		return nil
	}
	return NewError("authentication.mfa.disabled")
}

func (s *mfaApplicationService) requireInteractiveSession(
	principal model.Principal,
	recent bool,
) error {
	if principal.Validate() != nil || principal.CredentialType != model.CredentialSessionAccess {
		return NewError("authentication.session_required")
	}
	if recent && !principal.IsRecentlyAuthenticated(s.now(), s.recentAuthenticationTTL) {
		return NewError("authentication.reauthentication_required")
	}
	return nil
}

func (s *mfaApplicationService) requireStrongRecentSession(principal model.Principal) error {
	if err := s.requireInteractiveSession(principal, true); err != nil {
		return err
	}
	if !principal.HasStrongAuthentication() {
		return NewError("authentication.strong_authentication_required")
	}
	return nil
}

func (s *mfaApplicationService) auditResource(ctx context.Context) (model.Resource, error) {
	institution, err := s.institutions.GetSingleton(ctx)
	if err != nil {
		return model.Resource{}, mfaStoreError("MFA.audit_resource", err)
	}
	return model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}, nil
}

func (s *mfaApplicationService) failMutation(
	ctx context.Context,
	auditID string,
	where string,
	err error,
) error {
	mapped := mfaStoreError(where, err)
	code := "authentication.mfa.unavailable"
	if failure, ok := As(mapped); ok {
		code = failure.Code()
	}
	if auditErr := s.audit.Complete(
		ctx, auditID, model.AuditStatusFail, code, nil,
	); auditErr != nil {
		return auditErr
	}
	return mapped
}

type mfaAuditAdapter struct{ audit *auditService }

func (a mfaAuditAdapter) Begin(
	ctx context.Context,
	invocation Invocation,
	action model.Action,
	resource model.Resource,
	prior any,
) (string, error) {
	event, err := a.audit.BeginCriticalAction(
		ctx, invocation.Principal(), action, resource,
		invocation.RequestMetadata(), nil, prior,
	)
	if err != nil {
		return "", err
	}
	return event.ID.String(), nil
}

func (a mfaAuditAdapter) Complete(
	ctx context.Context,
	auditID string,
	status model.AuditStatus,
	errorCode string,
	result any,
) error {
	_, err := a.audit.CompleteCriticalAction(ctx, auditID, status, errorCode, result)
	return err
}

var _ authenticationMFAVerifier = (*mfaApplicationService)(nil)

func (s *mfaApplicationService) RecoveryState(ctx context.Context, userID model.UserID) (*model.UserMFARecovery, error) {
	state, err := s.credentials.GetRecoveryState(ctx, userID)
	if err != nil {
		return nil, authenticationUnavailable(err)
	}
	if state.ReenrollmentRequired && !s.mechanics.settings.Enabled {
		return nil, NewError("authentication.mfa.unavailable")
	}
	return state, nil
}

func (s *mfaApplicationService) requireRecoverySession(principal model.Principal, recent bool) error {
	if principal.ValidateMFARecovery() != nil || principal.CredentialType != model.CredentialSessionAccess {
		return NewError("authentication.session_required")
	}
	if recent && !principal.IsRecentlyAuthenticated(s.now(), s.recentAuthenticationTTL) {
		return NewError("authentication.reauthentication_required")
	}
	return nil
}

func (s *mfaApplicationService) limitFactorAttempt(ctx context.Context, invocation Invocation) error {
	_, limited, err := s.security.attempts.account(ctx, authenticationAttemptIntent{
		purpose: authenticationAttemptPurposeMFA, window: s.security.rateLimit.Window,
		limits: []authenticationAttemptLimit{
			{dimension: authenticationAttemptDimensionIdentity, maximum: s.security.rateLimit.MaximumAttempts, identity: invocation.Principal().UserID.String()},
			{dimension: authenticationAttemptDimensionSource, maximum: s.security.rateLimit.MaximumSourceAttempts, source: invocation.RequestMetadata().IPAddress},
		},
	})
	if err != nil {
		return rateLimitUnavailableAppError(err)
	}
	if limited {
		return NewError("authentication.rate_limited")
	}
	return nil
}
