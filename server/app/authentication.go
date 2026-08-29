// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/channels/app/authentication.go,
// server/channels/app/login.go, and server/channels/app/session.go. Proctor
// keeps the single application authentication flow, generic login failures,
// server-side revocable sessions, activity debouncing, and boundary-safe
// errors while using split hashed access/refresh credentials and rotation.

package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const (
	authenticationCachePrefix = "authentication/access/"
	activityCachePrefix       = "authentication/activity/"
)

// SessionPolicy is the immutable session-lifetime policy consumed by
// authentication. Composition translates deployment configuration into this
// value so authentication does not depend on config.Config.
type SessionPolicy struct {
	AccessTTL              time.Duration
	RefreshTTL             time.Duration
	IdleTTL                time.Duration
	AbsoluteTTL            time.Duration
	ActivityUpdateInterval time.Duration
	MaximumPerUser         int
}

// LoginRateLimitPolicy bounds login attempts by identity and source.
type LoginRateLimitPolicy struct {
	Window                time.Duration
	MaximumAttempts       int
	MaximumSourceAttempts int
}

// PersonalAccessTokenPolicy controls PAT lifetime bounds, per-user limits, and
// last-used write debouncing during bearer resolution.
type PersonalAccessTokenPolicy struct {
	MinimumLifetime        time.Duration
	MaximumLifetime        time.Duration
	LastUsedUpdateInterval time.Duration
	MaximumPerUser         int
}

// LoginCommand is the local-password login use-case input.
type LoginCommand struct {
	LoginID    string
	Password   string
	ClientType model.SessionClientType
	DeviceID   string
	DeviceName string
	MFACode    string
	Source     string
}

// LoginResult is the transport-neutral successful login outcome.
type LoginResult struct {
	User    *model.User
	Session *model.Session
	Tokens  *model.AuthenticationTokens
}

type localAuthenticationProof struct {
	User                   *model.User
	AuthenticationStrength model.AuthenticationStrength
	AuthenticatedAt        int64
	MFACompletedAt         int64
	receipt                authenticationAttemptReceipt
}

// CreateLocalUserCommand creates a local user with a password credential.
type CreateLocalUserCommand struct {
	User     *model.User
	Password string
}

type authenticationCache interface {
	Get(context.Context, string) ([]byte, error)
	SetAlways(context.Context, string, []byte, time.Duration) error
	SetIfAbsent(context.Context, string, []byte, time.Duration) error
	Delete(context.Context, string) error
	Add(context.Context, string, int64, time.Duration) (int64, error)
}

type authenticationDiagnostics interface {
	WarnContext(context.Context, string, error)
}

// authenticationService owns access-credential resolution, local login, session
// issuance, refresh, and logout orchestration behind explicit ports.
type authenticationService struct {
	users              store.UserStore
	passwords          store.PasswordCredentialStore
	sessions           store.SessionStore
	sessionCredentials store.SessionCredentialStore
	accessPolicy       authenticationAccessPolicy
	cache              authenticationCache
	attempts           *authenticationAttemptAccounting
	securityEffects    authenticationSecurityEffects
	hasher             *passwordHasher
	mfa                authenticationMFAVerifier
	personalTokens     authenticationPATResolver
	sessionPolicy      SessionPolicy
	loginRateLimit     LoginRateLimitPolicy
	diagnostics        authenticationDiagnostics
	registrations      authenticationDesktopRegistrationStore
	dpop               *dpopSecurity
	newCredential      func() string
	now                func() time.Time
}

type authenticationDesktopDependencies struct {
	registrations authenticationDesktopRegistrationStore
	dpop          *dpopSecurity
}

type authenticationDesktopRegistrationStore interface {
	Get(context.Context, string) (*model.DesktopRegistration, error)
}

type cachedAuthentication struct {
	Credential *model.SessionCredential `json:"credential"`
	Session    *model.Session           `json:"session"`
	User       *model.User              `json:"user"`
}

// ValidatePrincipal revalidates the authoritative session and user state for
// a previously established session principal. Long-lived transports call this
// through the App facade before continuing to trust the principal.
func (s *authenticationService) ValidatePrincipal(ctx context.Context, principal model.Principal) error {
	if principal.Validate() != nil || principal.CredentialType != model.CredentialSessionAccess {
		return invalidTokenAppError()
	}
	session, err := s.sessions.Get(ctx, principal.SessionID.String())
	if err != nil {
		if store.IsNotFound(err) {
			return invalidTokenAppError()
		}
		return authenticationUnavailable(err)
	}
	if session.UserID != principal.UserID {
		return invalidTokenAppError()
	}
	session, err = s.enforceSessionExpiry(ctx, session, s.now().UTC())
	if err != nil {
		return err
	}
	if principal.HasRegisteredDesktopKey() {
		if session.DesktopRegistrationID != principal.DesktopRegistrationID ||
			session.DPoPKeyThumbprint != principal.DPoPKeyThumbprint || s.registrations == nil {
			return invalidTokenAppError()
		}
		registration, registrationErr := s.registrations.Get(ctx, principal.DesktopRegistrationID.String())
		if registrationErr != nil || !registration.IsActive() || registration.UserID != principal.UserID ||
			registration.KeyThumbprint != principal.DPoPKeyThumbprint {
			if registrationErr == nil || store.IsNotFound(registrationErr) {
				return invalidTokenAppError()
			}
			return authenticationUnavailable(registrationErr)
		}
	}
	user, err := s.users.Get(ctx, principal.UserID.String())
	if err != nil {
		if store.IsNotFound(err) {
			return invalidTokenAppError()
		}
		return authenticationUnavailable(err)
	}
	if !user.IsActive() {
		return invalidTokenAppError()
	}
	return nil
}

func newAuthenticationService(
	users store.UserStore,
	passwords store.PasswordCredentialStore,
	sessions store.SessionStore,
	sessionCredentials store.SessionCredentialStore,
	accessPolicy authenticationAccessPolicy,
	cache authenticationCache,
	attempts *authenticationAttemptAccounting,
	securityEffects authenticationSecurityEffects,
	hasher *passwordHasher,
	mfa authenticationMFAVerifier,
	personalTokens authenticationPATResolver,
	sessionPolicy SessionPolicy,
	loginRateLimit LoginRateLimitPolicy,
	diagnostics authenticationDiagnostics,
	newCredential func() string,
	now func() time.Time,
	desktop authenticationDesktopDependencies,
) (*authenticationService, error) {
	if users == nil {
		return nil, errors.New("authentication user store is required")
	}
	if passwords == nil {
		return nil, errors.New("authentication password store is required")
	}
	if sessions == nil {
		return nil, errors.New("authentication session store is required")
	}
	if sessionCredentials == nil {
		return nil, errors.New("authentication session credential store is required")
	}
	if accessPolicy == nil {
		return nil, errors.New("authentication access policy is required")
	}
	if cache == nil {
		return nil, errors.New("authentication cache is required")
	}
	if attempts == nil {
		return nil, errors.New("authentication attempt accounting is required")
	}
	if securityEffects == nil {
		return nil, errors.New("authentication security effects are required")
	}
	if hasher == nil {
		return nil, errors.New("password hasher is required")
	}
	if mfa == nil {
		return nil, errors.New("authentication MFA verifier is required")
	}
	if personalTokens == nil {
		return nil, errors.New("authentication PAT resolver is required")
	}
	if diagnostics == nil {
		return nil, errors.New("authentication diagnostics are required")
	}
	if newCredential == nil {
		return nil, errors.New("authentication credential generator is required")
	}
	if now == nil {
		now = time.Now
	}
	if desktop.registrations == nil || desktop.dpop == nil {
		return nil, errors.New("authentication Desktop dependencies are required")
	}
	return &authenticationService{
		users: users, passwords: passwords, sessions: sessions,
		sessionCredentials: sessionCredentials, accessPolicy: accessPolicy, cache: cache, attempts: attempts,
		securityEffects: securityEffects, hasher: hasher, mfa: mfa,
		personalTokens: personalTokens, sessionPolicy: sessionPolicy,
		loginRateLimit: loginRateLimit, diagnostics: diagnostics,
		newCredential: newCredential, now: now,
		registrations: desktop.registrations, dpop: desktop.dpop,
	}, nil
}

func (a *App) CreateLocalUser(
	ctx context.Context,
	user *model.User,
	password string,
) (*model.User, error) {
	return a.authentication.createLocalUser(ctx, CreateLocalUserCommand{
		User:     user,
		Password: password,
	})
}

func (s *authenticationService) createLocalUser(
	ctx context.Context,
	command CreateLocalUserCommand,
) (*model.User, error) {
	hash, err := s.hasher.Hash(command.Password)
	if err != nil {
		return nil, NewError("authentication.password.invalid").
			WithField("field", "password").
			Wrap(err)
	}
	user, job, err := prepareUserDefaultProfilePictureJob(command.User, s.now())
	if err != nil {
		return nil, NewError("authentication.user.invalid").Wrap(err)
	}
	credential := &model.PasswordCredential{UserID: user.ID, PasswordHash: hash}
	credential.PrepareCreate(model.NewPasswordCredentialID(), user.CreatedAt)
	settings, err := prepareInitialUserSettingsDocument(user)
	if err != nil {
		return nil, NewError("authentication.user.invalid").Wrap(err)
	}
	result, err := s.users.Create(ctx, &store.UserCreation{
		User: user, Settings: settings, PasswordCredential: credential, DefaultProfilePictureJob: job,
	})
	if err != nil {
		var conflict *store.ErrConflict
		if errors.As(err, &conflict) {
			return nil, NewError("authentication.user.conflict").Wrap(err)
		}
		return nil, authenticationUnavailable(err)
	}
	return result.User, nil
}

func (a *App) Login(
	ctx context.Context,
	_ Invocation,
	command LoginCommand,
) (*LoginResult, error) {
	result, err := a.authentication.login(ctx, command)
	a.recordOperational("authentication", "login", err)
	return result, err
}

func (s *authenticationService) login(
	ctx context.Context,
	command LoginCommand,
) (*LoginResult, error) {
	if command.ClientType == model.SessionClientDesktop || !command.ClientType.IsValid() {
		return nil, NewError("authentication.client_type.invalid").WithField("field", "client_type")
	}
	proof, err := s.authenticateLocal(ctx, command)
	if err != nil {
		return nil, err
	}
	savedSession, tokens, sessionErr := s.createSession(
		ctx,
		sessionIssuance{
			User: proof.User, ClientType: command.ClientType,
			DeviceID: command.DeviceID, DeviceName: command.DeviceName,
			AuthenticationMethod: "password", AuthenticationStrength: proof.AuthenticationStrength,
			AuthenticatedAt: proof.AuthenticatedAt, MFACompletedAt: proof.MFACompletedAt,
		},
	)
	if sessionErr != nil {
		return nil, sessionErr
	}
	s.resetLocalAuthenticationAttempts(ctx, proof.receipt)
	return &LoginResult{User: proof.User, Session: savedSession, Tokens: tokens}, nil
}

// authenticateLocal proves a local identity without deciding which
// purpose-bound credential or Session will consume that proof.
func (s *authenticationService) authenticateLocal(
	ctx context.Context,
	command LoginCommand,
) (*localAuthenticationProof, error) {
	receipt, err := s.checkLoginRateLimit(ctx, command.LoginID, command.Source)
	if err != nil {
		return nil, err
	}
	localLoginAllowed, err := s.accessPolicy.AllowsLocalLogin(ctx)
	if err != nil {
		return nil, authenticationUnavailable(err)
	}
	if !localLoginAllowed {
		s.hasher.VerifyDummy(command.Password)
		return nil, invalidCredentialsAppError()
	}
	if command.LoginID == "" ||
		len(command.LoginID) > model.UserEmailMaxLength ||
		len(command.Password) > s.hasher.maximumLength {
		s.hasher.VerifyDummy("invalid-password-length")
		return nil, invalidCredentialsAppError()
	}
	user, err := s.findLoginUser(ctx, command.LoginID)
	if err != nil {
		s.hasher.VerifyDummy(command.Password)
		if !store.IsNotFound(err) {
			return nil, authenticationUnavailable(err)
		}
		return nil, invalidCredentialsAppError()
	}
	credential, err := s.passwords.GetByUser(ctx, user.ID.String())
	if err != nil {
		s.hasher.VerifyDummy(command.Password)
		if !store.IsNotFound(err) {
			return nil, authenticationUnavailable(err)
		}
		return nil, invalidCredentialsAppError()
	}
	if verifyErr := s.hasher.Verify(credential.PasswordHash, command.Password); verifyErr != nil || !user.IsActive() {
		return nil, invalidCredentialsAppError()
	}
	now := s.now()
	authenticationStrength, mfaCompletedAt, err := s.mfa.VerifyLogin(
		ctx, user.ID.String(), command.MFACode, now,
	)
	if err != nil {
		return nil, err
	}

	if s.hasher.NeedsRehash(credential.PasswordHash) {
		rehashed, hashErr := s.hasher.Hash(command.Password)
		if hashErr != nil {
			return nil, authenticationUnavailable(hashErr)
		}
		credential.PasswordHash = rehashed
		if _, updateErr := s.passwords.Update(ctx, credential); updateErr != nil {
			return nil, authenticationUnavailable(updateErr)
		}
	}

	return &localAuthenticationProof{User: user, AuthenticationStrength: authenticationStrength,
		AuthenticatedAt: now.UnixMilli(), MFACompletedAt: mfaCompletedAt, receipt: receipt}, nil
}

func (s *authenticationService) resetLocalAuthenticationAttempts(ctx context.Context, receipt authenticationAttemptReceipt) {
	if err := s.attempts.reset(ctx, receipt, authenticationAttemptDimensionIdentitySource); err != nil {
		s.warn(ctx, "login rate-limit reset failed", err)
	}
}

func (s *authenticationService) createSession(
	ctx context.Context,
	command sessionIssuance,
) (*model.Session, *model.AuthenticationTokens, error) {
	user := command.User
	clientType := command.ClientType
	strength := command.AuthenticationStrength
	authenticatedAt := command.AuthenticatedAt
	mfaCompletedAt := command.MFACompletedAt
	if user == nil || !user.IsActive() || !clientType.IsValid() ||
		!strength.IsValid() || authenticatedAt <= 0 {
		return nil, nil, NewError("authentication.session.invalid")
	}
	nowMillis := s.now().UnixMilli()
	if authenticatedAt > nowMillis {
		authenticatedAt = nowMillis
	}
	if strength == model.AuthenticationMultiFactor {
		if mfaCompletedAt < authenticatedAt || mfaCompletedAt > nowMillis {
			mfaCompletedAt = authenticatedAt
		}
	} else {
		mfaCompletedAt = 0
	}
	settings := s.sessionPolicy
	absoluteExpiresAt := nowMillis + settings.AbsoluteTTL.Milliseconds()
	accessExpiresAt := min(nowMillis+settings.AccessTTL.Milliseconds(), absoluteExpiresAt)
	refreshExpiresAt := min(nowMillis+settings.RefreshTTL.Milliseconds(), absoluteExpiresAt)
	session := &model.Session{
		UserID:                   user.ID,
		ClientType:               clientType,
		DeviceID:                 command.DeviceID,
		DeviceName:               command.DeviceName,
		AuthenticationMethod:     command.AuthenticationMethod,
		AuthenticationProviderID: command.AuthenticationProviderID,
		ExternalIdentityID:       command.ExternalIdentityID,
		AuthenticationStrength:   strength,
		AuthenticatedAt:          model.TimeFromMillis(authenticatedAt),
		MFACompletedAt:           model.OptionalTimeFromMillis(mfaCompletedAt),
		LastActivityAt:           model.TimeFromMillis(nowMillis),
		IdleExpiresAt: model.TimeFromMillis(min(
			nowMillis+settings.IdleTTL.Milliseconds(),
			absoluteExpiresAt,
		)),
		ExpiresAt: model.TimeFromMillis(absoluteExpiresAt),
	}
	accessToken := s.newCredential()
	refreshToken := s.newCredential()
	savedSession, credentials, saveErr := s.sessions.Save(
		ctx,
		session,
		[]*model.SessionCredential{
			{
				Kind:      model.SessionCredentialAccess,
				TokenHash: model.HashToken(accessToken),
				ExpiresAt: model.TimeFromMillis(accessExpiresAt),
			},
			{
				Kind:      model.SessionCredentialRefresh,
				TokenHash: model.HashToken(refreshToken),
				ExpiresAt: model.TimeFromMillis(refreshExpiresAt),
			},
		},
		settings.MaximumPerUser,
	)
	if saveErr != nil {
		if errors.Is(saveErr, store.ErrAuthenticationMethodDisabled) {
			if command.AuthenticationProviderID != "" {
				return nil, nil, invalidExternalAuthenticationError("authentication.create_session.policy")
			}
			return nil, nil, invalidCredentialsAppError()
		}
		var conflict *store.ErrConflict
		if errors.As(saveErr, &conflict) {
			switch conflict.Constraint {
			case "sessions_maximum_per_user":
				return nil, nil, NewError("authentication.sessions.maximum_reached")
			case "active_attempt_session_lock":
				return nil, nil, NewError("authentication.desktop_authorization.account_session_locked")
			}
		}
		return nil, nil, authenticationUnavailable(saveErr)
	}

	var accessCredential *model.SessionCredential
	for _, savedCredential := range credentials {
		if savedCredential.Kind == model.SessionCredentialAccess {
			accessCredential = savedCredential
			break
		}
	}
	if accessCredential == nil {
		return nil, nil, authenticationUnavailable(
			errors.New("saved session has no access credential"),
		)
	}
	s.cacheAuthentication(ctx, model.HashToken(accessToken), &cachedAuthentication{
		Credential: accessCredential,
		Session:    savedSession,
		User:       user,
	}, nowMillis)
	return savedSession, &model.AuthenticationTokens{
		TokenType:        "Bearer",
		AccessToken:      accessToken,
		RefreshToken:     refreshToken,
		AccessExpiresAt:  model.TimeFromMillis(accessExpiresAt),
		RefreshExpiresAt: model.TimeFromMillis(refreshExpiresAt),
	}, nil
}

func (s *authenticationService) checkLoginRateLimit(
	ctx context.Context,
	loginID string,
	source string,
) (authenticationAttemptReceipt, error) {
	settings := s.loginRateLimit
	receipt, limited, err := s.attempts.account(ctx, authenticationAttemptIntent{
		purpose: authenticationAttemptPurposeLocalLogin,
		window:  settings.Window,
		limits: []authenticationAttemptLimit{
			{
				dimension: authenticationAttemptDimensionIdentitySource,
				maximum:   settings.MaximumAttempts,
				identity:  loginID,
				source:    source,
			},
			{
				dimension: authenticationAttemptDimensionSource,
				maximum:   settings.MaximumSourceAttempts,
				source:    source,
			},
		},
	})
	if err != nil {
		return authenticationAttemptReceipt{}, rateLimitUnavailableAppError(err)
	}
	if limited {
		return authenticationAttemptReceipt{}, NewError("authentication.rate_limited")
	}
	return receipt, nil
}

func (s *authenticationService) findLoginUser(ctx context.Context, loginID string) (*model.User, error) {
	loginID = strings.ToLower(strings.TrimSpace(loginID))
	if strings.Contains(loginID, "@") {
		return s.users.GetByEmail(ctx, loginID)
	}
	return s.users.GetByUsername(ctx, loginID)
}

func (a *App) AuthenticateAccess(
	ctx context.Context,
	rawToken string,
) (*model.Principal, error) {
	principal, err := a.authentication.authenticateAccess(ctx, rawToken)
	a.recordOperational("authentication", "access_token", err)
	return principal, err
}

// AuthenticateBearer accepts the two Authorization-header credential classes:
// a CLI session access credential or a personal access token. Cookie
// authentication remains session-only and therefore calls AuthenticateAccess.
func (a *App) AuthenticateBearer(
	ctx context.Context,
	rawToken string,
) (*model.Principal, error) {
	principal, err := a.authentication.authenticateAccess(ctx, rawToken)
	if err == nil {
		a.recordOperational("authentication", "bearer", nil)
		return principal, nil
	}
	if failure, ok := As(err); !ok || failure.Code() != "authentication.invalid_token" {
		a.recordOperational("authentication", "bearer", err)
		return nil, err
	}
	principal, err = a.authentication.personalTokens.ResolveBearer(ctx, rawToken, a.authentication.now())
	a.recordOperational("authentication", "bearer", err)
	return principal, err
}

func (s *authenticationService) authenticateAccess(
	ctx context.Context,
	rawToken string,
) (*model.Principal, error) {
	if !validRawCredential(rawToken) {
		return nil, invalidTokenAppError()
	}
	now := s.now().UnixMilli()
	tokenHash := model.HashToken(rawToken)
	resolved := s.cachedAuthentication(ctx, tokenHash)
	var err error
	if resolved == nil {
		credential, session, resolveErr := s.sessionCredentials.GetSessionByTokenHash(
			ctx,
			tokenHash,
			model.SessionCredentialAccess,
		)
		if resolveErr != nil {
			if store.IsNotFound(resolveErr) {
				return nil, invalidTokenAppError()
			}
			return nil, authenticationUnavailable(resolveErr)
		}
		user, userErr := s.users.Get(ctx, session.UserID.String())
		if userErr != nil {
			if store.IsNotFound(userErr) {
				return nil, invalidTokenAppError()
			}
			return nil, authenticationUnavailable(userErr)
		}
		resolved = &cachedAuthentication{Credential: credential, Session: session, User: user}
	}
	nowTime := model.TimeFromMillis(now)
	if !resolved.User.IsActive() || resolved.Credential.IsExpiredAt(nowTime) {
		_ = s.cache.Delete(ctx, authenticationCachePrefix+tokenHash)
		return nil, invalidTokenAppError()
	}
	resolved.Session, err = s.enforceSessionExpiry(ctx, resolved.Session, nowTime)
	if err != nil {
		_ = s.cache.Delete(ctx, authenticationCachePrefix+tokenHash)
		return nil, err
	}
	if resolved.Session.ClientType == model.SessionClientDesktop {
		return nil, invalidTokenAppError()
	}
	if err := s.updateActivity(ctx, resolved, now); err != nil {
		return nil, err
	}
	s.cacheAuthentication(ctx, tokenHash, resolved, now)
	principal := &model.Principal{
		UserID:                   resolved.User.ID,
		SessionID:                resolved.Session.ID,
		CredentialID:             model.PrincipalCredentialID(resolved.Credential.ID),
		CredentialType:           model.CredentialSessionAccess,
		AuthenticationMethod:     resolved.Session.AuthenticationMethod,
		AuthenticationProviderID: resolved.Session.AuthenticationProviderID,
		ExternalIdentityID:       resolved.Session.ExternalIdentityID,
		AuthenticationStrength:   resolved.Session.AuthenticationStrength,
		ClientType:               resolved.Session.ClientType,
		AuthenticatedAt:          resolved.Session.AuthenticatedAt,
		MFACompletedAt:           resolved.Session.MFACompletedAt,
	}
	if principal.Validate() != nil {
		return nil, authenticationUnavailable(
			errors.New("resolved principal is invalid"),
		)
	}
	return principal, nil
}

func (s *authenticationService) updateActivity(
	ctx context.Context,
	resolved *cachedAuthentication,
	now int64,
) error {
	settings := s.sessionPolicy
	lastActivityMillis := model.MillisFromTime(resolved.Session.LastActivityAt)
	if now-lastActivityMillis < settings.ActivityUpdateInterval.Milliseconds() {
		return nil
	}
	key := activityCachePrefix + resolved.Session.ID.String()
	err := s.cache.SetIfAbsent(
		ctx,
		key,
		[]byte{1},
		settings.ActivityUpdateInterval,
	)
	if errors.Is(err, ErrAuthenticationCacheNotStored) {
		return nil
	}
	if err != nil {
		s.warn(ctx, "session activity debounce cache failed", err)
		return nil
	}
	idleExpiresAt := min(now+settings.IdleTTL.Milliseconds(), model.MillisFromTime(resolved.Session.ExpiresAt))
	if err := s.sessions.UpdateActivity(
		ctx,
		resolved.Session.ID.String(),
		now,
		idleExpiresAt,
	); err != nil {
		if store.IsNotFound(err) {
			return invalidTokenAppError()
		}
		return authenticationUnavailable(err)
	}
	nowTime := model.TimeFromMillis(now)
	resolved.Session.LastActivityAt = nowTime
	resolved.Session.IdleExpiresAt = model.TimeFromMillis(idleExpiresAt)
	if resolved.Session.UpdatedAt.Before(nowTime) {
		resolved.Session.UpdatedAt = nowTime
	}
	return nil
}

func (a *App) RefreshSession(
	ctx context.Context,
	_ Invocation,
	command RefreshSessionCommand,
) (*model.Session, *model.AuthenticationTokens, error) {
	session, tokens, err := a.authentication.refresh(ctx, command)
	a.recordOperational("authentication", "refresh", err)
	return session, tokens, err
}

func (s *authenticationService) refresh(
	ctx context.Context,
	command RefreshSessionCommand,
) (*model.Session, *model.AuthenticationTokens, error) {
	rawRefreshToken := command.RefreshToken
	if !validRawCredential(rawRefreshToken) {
		return nil, nil, invalidTokenAppError()
	}
	_, session, err := s.sessionCredentials.GetSessionByTokenHash(
		ctx,
		model.HashToken(rawRefreshToken),
		model.SessionCredentialRefresh,
	)
	if err != nil {
		if store.IsNotFound(err) {
			return nil, nil, invalidTokenAppError()
		}
		return nil, nil, authenticationUnavailable(err)
	}
	if session.ClientType == model.SessionClientDesktop {
		if command.DPoP == nil {
			return nil, nil, invalidTokenAppError()
		}
		if err = s.validateDPoPRefresh(ctx, rawRefreshToken, command.DPoP.Proof, command.DPoP.Method, command.DPoP.Path); err != nil {
			return nil, nil, err
		}
	} else if command.DPoP != nil {
		return nil, nil, invalidTokenAppError()
	}
	now := s.now().UnixMilli()
	settings := s.sessionPolicy
	accessToken := s.newCredential()
	refreshToken := s.newCredential()
	rotation, err := s.sessionCredentials.RotateRefresh(
		ctx,
		model.HashToken(rawRefreshToken),
		&model.SessionCredential{
			TokenHash: model.HashToken(accessToken),
			ExpiresAt: model.TimeFromMillis(now + settings.AccessTTL.Milliseconds()),
		},
		&model.SessionCredential{
			TokenHash: model.HashToken(refreshToken),
			ExpiresAt: model.TimeFromMillis(now + settings.RefreshTTL.Milliseconds()),
		},
		now,
		now+settings.IdleTTL.Milliseconds(),
	)
	if err != nil {
		var conflict *store.ErrConflict
		if store.IsNotFound(err) || errors.As(err, &conflict) {
			return nil, nil, invalidTokenAppError()
		}
		return nil, nil, authenticationUnavailable(err)
	}
	if rotation.ReplayDetected || rotation.Expired {
		s.sessionsRevoked(
			ctx,
			rotation.Session.UserID.String(),
			[]string{rotation.Session.ID.String()},
			rotation.RevokedAccessHashes,
		)
	} else {
		s.authenticationCacheInvalidated(
			ctx,
			rotation.Session.UserID.String(),
			rotation.RevokedAccessHashes,
		)
	}
	if rotation.ReplayDetected || rotation.Expired {
		return nil, nil, invalidTokenAppError()
	}
	user, err := s.users.Get(ctx, rotation.Session.UserID.String())
	if err != nil || !user.IsActive() {
		if err == nil {
			var revokedAccessHashes []string
			revokedAccessHashes, err = s.sessions.Revoke(
				ctx,
				rotation.Session.ID.String(),
				rotation.Session.UserID.String(),
				now,
				model.SessionRevocationInactiveUser,
			)
			if err == nil {
				s.sessionsRevoked(
					ctx,
					rotation.Session.UserID.String(),
					[]string{rotation.Session.ID.String()},
					revokedAccessHashes,
				)
			}
		}
		return nil, nil, invalidTokenAppError()
	}
	s.cacheAuthentication(ctx, rotation.AccessCredential.TokenHash, &cachedAuthentication{
		Credential: rotation.AccessCredential,
		Session:    rotation.Session,
		User:       user,
	}, now)
	return rotation.Session, &model.AuthenticationTokens{
		TokenType:        tokenTypeForSession(rotation.Session),
		AccessToken:      accessToken,
		RefreshToken:     refreshToken,
		AccessExpiresAt:  rotation.AccessCredential.ExpiresAt,
		RefreshExpiresAt: rotation.RefreshCredential.ExpiresAt,
	}, nil
}

func tokenTypeForSession(session *model.Session) string {
	if session != nil && session.ClientType == model.SessionClientDesktop {
		return "DPoP"
	}
	return "Bearer"
}

func (a *App) Logout(ctx context.Context, invocation Invocation, _ LogoutCommand) error {
	err := a.authentication.logout(ctx, invocation)
	a.recordOperational("authentication", "logout", err)
	return err
}

func (s *authenticationService) logout(ctx context.Context, invocation Invocation) error {
	principal := invocation.Principal()
	if principal.Validate() != nil {
		return invalidTokenAppError()
	}
	hashes, err := s.sessions.Revoke(
		ctx,
		principal.SessionID.String(),
		principal.UserID.String(),
		s.now().UnixMilli(),
		model.SessionRevocationUserLogout,
	)
	if err != nil {
		if store.IsNotFound(err) {
			return nil
		}
		return authenticationUnavailable(err)
	}
	s.securityEffects.SessionsRevoked(
		ctx,
		principal.UserID.String(),
		[]string{principal.SessionID.String()},
		hashes,
	)
	return nil
}

func (a *App) GetUser(ctx context.Context, id string) (*model.User, error) {
	user, err := a.authentication.users.Get(ctx, id)
	if err != nil {
		if store.IsNotFound(err) {
			return nil, NewError("user.not_found")
		}
		return nil, authenticationUnavailable(err)
	}
	return user, nil
}

func (s *authenticationService) cachedAuthentication(
	ctx context.Context,
	tokenHash string,
) *cachedAuthentication {
	data, err := s.cache.Get(ctx, authenticationCachePrefix+tokenHash)
	if errors.Is(err, ErrAuthenticationCacheMiss) {
		return nil
	}
	if err != nil {
		s.warn(ctx, "authentication cache get failed", err)
		return nil
	}
	var resolved cachedAuthentication
	if err := json.Unmarshal(data, &resolved); err != nil ||
		resolved.Credential == nil ||
		resolved.Session == nil ||
		resolved.User == nil {
		_ = s.cache.Delete(ctx, authenticationCachePrefix+tokenHash)
		return nil
	}
	return &resolved
}

func (s *authenticationService) cacheAuthentication(
	ctx context.Context,
	tokenHash string,
	resolved *cachedAuthentication,
	now int64,
) {
	expiresAt := min(
		model.MillisFromTime(resolved.Credential.ExpiresAt),
		model.MillisFromTime(resolved.Session.IdleExpiresAt),
		model.MillisFromTime(resolved.Session.ExpiresAt),
	)
	if expiresAt <= now {
		return
	}
	data, err := json.Marshal(resolved)
	if err != nil {
		s.warn(ctx, "encode authentication cache value failed", err)
		return
	}
	if err := s.cache.SetAlways(
		ctx,
		authenticationCachePrefix+tokenHash,
		data,
		time.Duration(expiresAt-now)*time.Millisecond,
	); err != nil {
		s.warn(ctx, "authentication cache set failed", err)
	}
}

func (s *authenticationService) authenticationCacheInvalidated(
	ctx context.Context,
	userID string,
	hashes []string,
) {
	s.securityEffects.AuthenticationCacheInvalidated(ctx, userID, hashes)
}

func (s *authenticationService) sessionsRevoked(
	ctx context.Context,
	userID string,
	sessionIDs []string,
	hashes []string,
) {
	s.securityEffects.SessionsRevoked(ctx, userID, sessionIDs, hashes)
}

func (s *authenticationService) enforceSessionExpiry(
	ctx context.Context,
	session *model.Session,
	now time.Time,
) (*model.Session, error) {
	if session == nil {
		return nil, invalidTokenAppError()
	}
	if !session.IsExpiredAt(now) {
		return session, nil
	}
	result, err := s.sessions.EnforceExpiry(
		ctx,
		session.ID.String(),
		session.UserID.String(),
		model.MillisFromTime(now),
	)
	if err != nil {
		if store.IsNotFound(err) {
			return nil, invalidTokenAppError()
		}
		return nil, authenticationUnavailable(err)
	}
	if result == nil || result.Session == nil {
		return nil, authenticationUnavailable(errors.New("session expiry result is incomplete"))
	}
	if !result.Expired {
		return result.Session, nil
	}
	s.sessionsRevoked(
		ctx,
		result.Session.UserID.String(),
		[]string{result.Session.ID.String()},
		result.TokenHashes,
	)
	return nil, invalidTokenAppError()
}

func (s *authenticationService) warn(ctx context.Context, message string, err error) {
	if s.diagnostics == nil {
		return
	}
	s.diagnostics.WarnContext(ctx, message, err)
}

func validRawCredential(token string) bool {
	return model.IsValidCredentialToken(token)
}

func invalidCredentialsAppError() error {
	return NewError("authentication.invalid_credentials")
}

func invalidTokenAppError() error {
	return NewError("authentication.invalid_token")
}

func authenticationUnavailable(err error) error {
	return NewError("authentication.internal").Wrap(err)
}

func rateLimitUnavailableAppError(err error) error {
	return NewError("authentication.rate_limit_unavailable").Wrap(err)
}
