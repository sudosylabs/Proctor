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

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/app/mail"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// mfaAudit preserves the exact critical-audit ordering of MFA mutations while
// keeping the focused application service independent of the audit service.
type mfaAudit interface {
	Begin(context.Context, invocation.Invocation, model.Action, model.Resource, any) (model.AuditEventID, error)
	Complete(context.Context, model.AuditEventID, model.AuditStatus, string, any) error
}

type mfaSecurityDependencies struct {
	authorization resourceAuthorizer
	capabilities  accessPolicyCapabilitySource
	attempts      *authenticationAttemptAccounting
	rateLimit     LoginRateLimitPolicy
}

type mfaEffects interface {
	SessionsRevoked(context.Context, string, []string, []string)
}

// mfaApplicationServiceDependencies are the collaborators of mfaApplicationService;
// newMFAApplicationService validates them once and mfaApplicationService keeps them in deps.
type mfaApplicationServiceDependencies struct {
	Users                   store.UserStore
	Credentials             store.MFAStore
	Sessions                store.SessionStore
	Institutions            store.InstitutionStore
	Audit                   mfaAudit
	Effects                 mfaEffects
	Mail                    mfaNoticeMailPreparer
	Mechanics               *mfaMechanics
	Security                mfaSecurityDependencies
	RecentAuthenticationTTL time.Duration
	Now                     func() time.Time
}

// mfaApplicationService owns enrollment, verification, recovery-code, and
// assurance-transition policy. mfaMechanics remains the cryptographic owner.
type mfaApplicationService struct {
	deps mfaApplicationServiceDependencies
}

func newMFAApplicationService(deps mfaApplicationServiceDependencies) (*mfaApplicationService, error) {
	switch {
	case deps.Users == nil:
		return nil, errors.New("MFA user store is required")
	case deps.Credentials == nil:
		return nil, errors.New("MFA store is required")
	case deps.Sessions == nil:
		return nil, errors.New("MFA session store is required")
	case deps.Institutions == nil:
		return nil, errors.New("MFA institution store is required")
	case deps.Audit == nil:
		return nil, errors.New("MFA audit is required")
	case deps.Effects == nil:
		return nil, errors.New("MFA effects are required")
	case deps.Mail == nil:
		return nil, errors.New("MFA mail preparer is required")
	case deps.Mechanics == nil:
		return nil, errors.New("MFA mechanics are required")
	case deps.Security.authorization == nil || deps.Security.capabilities == nil || deps.Security.attempts == nil:
		return nil, errors.New("MFA security dependencies are required")
	case deps.Security.rateLimit.MaximumAttempts <= 0 || deps.Security.rateLimit.MaximumSourceAttempts <= 0 || deps.Security.rateLimit.Window <= 0:
		return nil, errors.New("MFA attempt policy is required")
	case deps.RecentAuthenticationTTL <= 0:
		return nil, errors.New("MFA recent authentication TTL must be positive")
	case deps.Now == nil:
		return nil, errors.New("MFA clock is required")
	}
	return &mfaApplicationService{deps: deps}, nil
}

func (s *mfaApplicationService) GetMFAStatus(
	ctx context.Context,
	invocation invocation.Invocation,
	_ GetMFAStatusQuery,
) (*MFAStatus, error) {
	principal := invocation.Principal()
	if err := s.requireRecoverySession(principal, false); err != nil {
		return nil, err
	}
	status := &MFAStatus{ServiceEnabled: s.deps.Mechanics.deps.Settings.Enabled, MFARecoveryRequired: principal.MFARecoveryRequired,
		AuthenticationMethod: principal.AuthenticationMethod, AuthenticationProviderID: principal.AuthenticationProviderID,
		AuthenticationStrength: principal.AuthenticationStrength, RecentlyAuthenticated: principal.IsRecentlyAuthenticated(s.deps.Now(), s.deps.RecentAuthenticationTTL)}
	credential, err := s.deps.Credentials.GetByUser(ctx, principal.UserID)
	if store.IsNotFound(err) {
		return status, nil
	}
	if err != nil {
		return nil, mfaStoreErrors.Translate(err)
	}
	status.Enabled = credential.IsActive()
	status.Pending = credential.IsPendingAt(model.TimeUTC(s.deps.Now()))
	status.PendingExpiresAt = credential.PendingExpiresAt
	if status.Enabled {
		status.RecoveryCodesRemaining, err = s.deps.Credentials.CountRecoveryCodes(ctx, principal.UserID)
		if err != nil {
			return nil, mfaStoreErrors.Translate(err)
		}
	}
	return status, nil
}

func (s *mfaApplicationService) SetupMFA(
	ctx context.Context,
	invocation invocation.Invocation,
	command SetupMFACommand,
) (*MFASetup, error) {
	principal := invocation.Principal()
	if err := s.requireRecoverySession(principal, true); err != nil {
		return nil, err
	}
	if err := s.requireEnabled(); err != nil {
		return nil, err
	}
	user, err := s.deps.Users.Get(ctx, principal.UserID)
	if err != nil {
		return nil, mfaStoreErrors.Translate(err)
	}
	accountName := command.AccountName
	if strings.TrimSpace(accountName) == "" {
		accountName = user.Email
	}
	secret, err := s.deps.Mechanics.newSecret()
	if err != nil {
		return nil, authenticationUnavailable(err)
	}
	sealed, err := s.deps.Mechanics.sealTOTPSecret(principal.UserID.String(), secret)
	if err != nil {
		return nil, authenticationUnavailable(err)
	}
	now := model.TimeUTC(s.deps.Now())
	candidate := &model.MFACredential{
		UserID: principal.UserID, State: model.MFAStatePending,
		EncryptedSecret: sealed.encoded, EncryptionKeyID: sealed.keyID,
		PendingExpiresAt: model.OptionalTimeFrom(now.Add(s.deps.Mechanics.deps.Settings.SetupTTL)),
		CreatedAt:        now,
	}
	resource, appErr := s.auditResource(ctx)
	if appErr != nil {
		return nil, appErr
	}
	auditID, appErr := s.deps.Audit.Begin(
		ctx, invocation, model.ActionMFASetup, resource, nil,
	)
	if appErr != nil {
		return nil, appErr
	}
	saved, err := s.deps.Credentials.SavePending(ctx, &store.MFAPendingEnrollment{Principal: principal, Credential: candidate, Lifetime: s.deps.Mechanics.deps.Settings.SetupTTL, RecentAuthenticationTTL: s.deps.RecentAuthenticationTTL, AuditEventID: auditID})
	if err != nil {
		return nil, s.failMutation(ctx, auditID, err)
	}
	return &MFASetup{
		Secret: secret,
		ProvisioningURI: mfaProvisioningURI(
			s.deps.Mechanics.deps.Settings.Issuer, accountName, secret,
		),
		ExpiresAt: saved.PendingExpiresAt.Time,
	}, nil
}

func (s *mfaApplicationService) ActivateMFA(
	ctx context.Context,
	invocation invocation.Invocation,
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
	credential, err := s.deps.Credentials.GetByUser(ctx, principal.UserID)
	if err != nil {
		return nil, mfaStoreErrors.Translate(err)
	}
	now := model.TimeUTC(s.deps.Now())
	if !credential.IsPendingAt(now) {
		return nil, mfaInvalidCodeError()
	}
	secret, err := s.deps.Mechanics.decrypt(principal.UserID.String(), credential)
	if err != nil {
		return nil, authenticationUnavailable(err)
	}
	timeStep, valid := verifyTOTP(secret, command.Code, 0, now)
	if !valid {
		return nil, mfaInvalidCodeError()
	}
	rawCodes, recoveryCodes, err := s.deps.Mechanics.newRecoveryCodes(
		principal.UserID.String(), s.deps.Mechanics.deps.Settings.RecoveryCodeCount,
	)
	if err != nil {
		return nil, authenticationUnavailable(err)
	}
	// The frozen notice retains its existing millisecond timestamp contract;
	// the credential and Session expiry decision uses the native instant.
	prepared, appErr := s.prepareSecurityNotice(
		ctx, principal.UserID, mail.MFANoticeEnabled, model.TimeFromMillis(now.UnixMilli()),
	)
	if appErr != nil {
		return nil, appErr
	}
	resource, appErr := s.auditResource(ctx)
	if appErr != nil {
		return nil, appErr
	}
	auditID, appErr := s.deps.Audit.Begin(
		ctx, invocation, model.ActionMFAActivate, resource, credential.Auditable(),
	)
	if appErr != nil {
		return nil, appErr
	}
	activated, err := s.deps.Credentials.Activate(ctx, &store.MFAActivationMutation{
		Principal: principal, RecentAuthenticationTTL: s.deps.RecentAuthenticationTTL,
		CredentialID: credential.ID, UserID: principal.UserID, TimeStep: timeStep,
		RecoveryCodes: recoveryCodes, SessionID: principal.SessionID, At: now,
		AuditEventID: auditID, AuditAt: now.UnixMilli(), Notice: mfaSecurityNotice(prepared),
	})
	if err != nil {
		return nil, s.failMutation(ctx, auditID, err)
	}
	s.deps.Effects.SessionsRevoked(
		ctx, principal.UserID.String(), []string{principal.SessionID.String()},
		activated.AccessTokenHashes,
	)
	return &MFAActivation{RecoveryCodes: rawCodes}, nil
}

func (s *mfaApplicationService) ChallengeMFA(
	ctx context.Context,
	invocation invocation.Invocation,
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
	auditID, appErr := s.deps.Audit.Begin(ctx, invocation, model.ActionMFAChallenge, resource, nil)
	if appErr != nil {
		return nil, appErr
	}
	now := model.TimeUTC(s.deps.Now())
	proof, err := s.deps.Mechanics.verifySecondFactor(ctx, s.deps.Credentials, principal.UserID.String(), command.Code, now)
	if err != nil {
		code := "authentication.mfa.code_invalid"
		if failure, ok := apperror.As(err); ok {
			code = failure.Code()
		}
		if auditErr := s.deps.Audit.Complete(ctx, auditID, model.AuditStatusFail, code, nil); auditErr != nil {
			return nil, errors.Join(err, auditErr)
		}
		return nil, err
	}
	result, err := s.deps.Credentials.Challenge(ctx, &store.MFAChallenge{Principal: principal, CredentialID: proof.credentialID, TimeStep: proof.step, RecoveryCodeHash: proof.recoveryHash, VerifiedAt: now, AuditEventID: auditID})
	if err != nil {
		if store.IsNotFound(err) {
			if auditErr := s.deps.Audit.Complete(ctx, auditID, model.AuditStatusFail, "authentication.mfa.code_invalid", nil); auditErr != nil {
				return nil, errors.Join(mfaInvalidCodeError(), auditErr)
			}
			return nil, mfaInvalidCodeError()
		}
		return nil, s.failMutation(ctx, auditID, err)
	}
	if result == nil || result.Session == nil {
		return nil, authenticationUnavailable(errors.New("MFA challenge returned no Session"))
	}
	s.deps.Effects.SessionsRevoked(ctx, principal.UserID.String(), []string{principal.SessionID.String()}, result.AccessTokenHashes)
	return result.Session, nil
}

func (s *mfaApplicationService) RegenerateMFARecoveryCodes(
	ctx context.Context,
	invocation invocation.Invocation,
	_ RegenerateMFARecoveryCodesCommand,
) ([]string, error) {
	principal := invocation.Principal()
	if err := s.requireStrongRecentSession(principal); err != nil {
		return nil, err
	}
	if err := s.requireEnabled(); err != nil {
		return nil, err
	}
	rawCodes, codes, err := s.deps.Mechanics.newRecoveryCodes(
		principal.UserID.String(), s.deps.Mechanics.deps.Settings.RecoveryCodeCount,
	)
	if err != nil {
		return nil, authenticationUnavailable(err)
	}
	now := model.TimeFromMillis(s.deps.Now().UnixMilli())
	prepared, appErr := s.prepareSecurityNotice(
		ctx, principal.UserID, mail.MFANoticeRecoveryCodesRegenerated, now,
	)
	if appErr != nil {
		return nil, appErr
	}
	resource, appErr := s.auditResource(ctx)
	if appErr != nil {
		return nil, appErr
	}
	auditID, appErr := s.deps.Audit.Begin(
		ctx, invocation, model.ActionMFARecoveryCodesRegenerate, resource, nil,
	)
	if appErr != nil {
		return nil, appErr
	}
	if err := s.deps.Credentials.ReplaceRecoveryCodes(ctx, &store.MFARecoveryCodesRegeneration{
		Principal: principal, RecentAuthenticationTTL: s.deps.RecentAuthenticationTTL,
		UserID: principal.UserID, RecoveryCodes: codes, At: now.UnixMilli(),
		AuditEventID: auditID, AuditAt: now.UnixMilli(), Notice: mfaSecurityNotice(prepared),
	}); err != nil {
		return nil, s.failMutation(
			ctx, auditID, err)

	}
	return rawCodes, nil
}

func (s *mfaApplicationService) DisableMFA(
	ctx context.Context,
	invocation invocation.Invocation,
	_ DisableMFACommand,
) error {
	principal := invocation.Principal()
	if err := s.requireStrongRecentSession(principal); err != nil {
		return err
	}
	if err := s.requireEnabled(); err != nil {
		return err
	}
	now := model.TimeFromMillis(s.deps.Now().UnixMilli())
	prepared, appErr := s.prepareSecurityNotice(
		ctx, principal.UserID, mail.MFANoticeDisabled, now,
	)
	if appErr != nil {
		return appErr
	}
	resource, appErr := s.auditResource(ctx)
	if appErr != nil {
		return appErr
	}
	auditID, appErr := s.deps.Audit.Begin(ctx, invocation, model.ActionMFADisable, resource, nil)
	if appErr != nil {
		return appErr
	}
	result, err := s.deps.Credentials.Disable(ctx, &store.MFADisablement{
		Principal: principal, RecentAuthenticationTTL: s.deps.RecentAuthenticationTTL,
		UserID: principal.UserID, At: now.UnixMilli(), AuditEventID: auditID,
		AuditAt: now.UnixMilli(), Notice: mfaSecurityNotice(prepared),
	})
	if err != nil {
		return s.failMutation(ctx, auditID, err)
	}
	s.deps.Effects.SessionsRevoked(
		ctx, principal.UserID.String(), nil, result.AccessTokenHashes,
	)
	return nil
}

func (s *mfaApplicationService) prepareSecurityNotice(
	ctx context.Context,
	userID model.UserID,
	kind mail.MFANoticeKind,
	at time.Time,
) (*store.PreparedMail, error) {
	user, err := s.deps.Users.Get(ctx, userID)
	if err != nil {
		return nil, mfaStoreErrors.Translate(err)
	}
	prepared, err := s.deps.Mail.PrepareMFANotice(mail.NoticePreparation{Recipient: user, At: at}, kind)
	if err != nil {
		return nil, authenticationUnavailable(err)
	}
	return prepared, nil
}

func mfaSecurityNotice(prepared *store.PreparedMail) store.MFASecurityNotice {
	if prepared == nil {
		return store.MFASecurityNotice{}
	}
	return store.MFASecurityNotice{
		Occurrence: prepared.Occurrence, Delivery: prepared.Delivery, Job: prepared.Job,
	}
}

// verifyLogin is the narrow behavior Authentication consumes. Authentication
// does not retain or query the MFA application implementation.
func (s *mfaApplicationService) verifyLogin(
	ctx context.Context,
	userID string,
	code string,
	at time.Time,
) (model.AuthenticationStrength, int64, error) {
	credential, err := s.deps.Credentials.GetByUser(ctx, model.UserID(userID))
	if store.IsNotFound(err) {
		return model.AuthenticationSingleFactor, 0, nil
	}
	if err != nil {
		return "", 0, authenticationUnavailable(err)
	}
	if !credential.IsActive() {
		return model.AuthenticationSingleFactor, 0, nil
	}
	if !s.deps.Mechanics.deps.Settings.Enabled {
		return "", 0, apperror.New("authentication.mfa.unavailable")
	}
	if strings.TrimSpace(code) == "" {
		return "", 0, apperror.New("authentication.mfa.required")
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
	return s.deps.Mechanics.consumeSecondFactor(ctx, s.deps.Credentials, userID, code, now)
}

func (s *mfaApplicationService) requireEnabled() error {
	if s.deps.Mechanics.deps.Settings.Enabled {
		return nil
	}
	return apperror.New("authentication.mfa.disabled")
}

func (s *mfaApplicationService) requireInteractiveSession(
	principal model.Principal,
	recent bool,
) error {
	if principal.Validate() != nil || principal.CredentialType != model.CredentialSessionAccess {
		return apperror.New("authentication.session_required")
	}
	if recent && !principal.IsRecentlyAuthenticated(s.deps.Now(), s.deps.RecentAuthenticationTTL) {
		return apperror.New("authentication.reauthentication_required")
	}
	return nil
}

func (s *mfaApplicationService) requireStrongRecentSession(principal model.Principal) error {
	if err := s.requireInteractiveSession(principal, true); err != nil {
		return err
	}
	if !principal.HasStrongAuthentication() {
		return apperror.New("authentication.strong_required")
	}
	return nil
}

func (s *mfaApplicationService) auditResource(ctx context.Context) (model.Resource, error) {
	institution, err := s.deps.Institutions.GetSingleton(ctx)
	if err != nil {
		return model.Resource{}, mfaStoreErrors.Translate(err)
	}
	return model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}, nil
}

func (s *mfaApplicationService) failMutation(
	ctx context.Context,
	auditID model.AuditEventID,
	err error,
) error {
	mapped := mfaStoreErrors.Translate(err)
	code := "authentication.mfa.unavailable"
	if failure, ok := apperror.As(mapped); ok {
		code = failure.Code()
	}
	if auditErr := s.deps.Audit.Complete(
		ctx, auditID, model.AuditStatusFail, code, nil,
	); auditErr != nil {
		return errors.Join(mapped, auditErr)
	}
	return mapped
}

type mfaAuditAdapter struct{ audit *auditService }

func (a mfaAuditAdapter) Begin(ctx context.Context, invocation invocation.Invocation, action model.Action, resource model.Resource, prior any) (model.AuditEventID, error) {
	event, err := a.audit.BeginCriticalAction(
		ctx, invocation.Principal(), action, resource,
		invocation.RequestMetadata(), nil, prior,
	)
	if err != nil {
		return "", err
	}
	return event.ID, nil
}

func (a mfaAuditAdapter) Complete(
	ctx context.Context,
	auditID model.AuditEventID,
	status model.AuditStatus,
	errorCode string,
	result any,
) error {
	_, err := a.audit.CompleteCriticalAction(ctx, auditID, status, errorCode, result)
	return err
}

var _ authenticationMFAVerifier = (*mfaApplicationService)(nil)

func (s *mfaApplicationService) recoveryState(ctx context.Context, userID model.UserID) (*model.UserMFARecovery, error) {
	state, err := s.deps.Credentials.GetRecoveryState(ctx, userID)
	if err != nil {
		return nil, authenticationUnavailable(err)
	}
	if state.ReenrollmentRequired && !s.deps.Mechanics.deps.Settings.Enabled {
		return nil, apperror.New("authentication.mfa.unavailable")
	}
	return state, nil
}

func (s *mfaApplicationService) requireRecoverySession(principal model.Principal, recent bool) error {
	if principal.ValidateMFARecovery() != nil || principal.CredentialType != model.CredentialSessionAccess {
		return apperror.New("authentication.session_required")
	}
	if recent && !principal.IsRecentlyAuthenticated(s.deps.Now(), s.deps.RecentAuthenticationTTL) {
		return apperror.New("authentication.reauthentication_required")
	}
	return nil
}

func (s *mfaApplicationService) limitFactorAttempt(ctx context.Context, invocation invocation.Invocation) error {
	_, limited, err := s.deps.Security.attempts.account(ctx, authenticationAttemptIntent{
		purpose: authenticationAttemptPurposeMFA, window: s.deps.Security.rateLimit.Window,
		limits: []authenticationAttemptLimit{
			{dimension: authenticationAttemptDimensionIdentity, maximum: s.deps.Security.rateLimit.MaximumAttempts, identity: invocation.Principal().UserID.String()},
			{dimension: authenticationAttemptDimensionSource, maximum: s.deps.Security.rateLimit.MaximumSourceAttempts, source: invocation.RequestMetadata().IPAddress},
		},
	})
	if err != nil {
		return rateLimitUnavailableAppError(err)
	}
	if limited {
		return apperror.New("authentication.rate_limited")
	}
	return nil
}
