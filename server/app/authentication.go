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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
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
// value so authentication does not depend on config.Config (ADR-0017).
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

// AuthenticationService owns access-credential resolution, local login, session
// issuance, refresh, and logout orchestration behind explicit ports.
type AuthenticationService struct {
	store                                    store.Store
	cache                                    authenticationCache
	hasher                                   *passwordHasher
	mfa                                      *MFAService
	sessions                                 SessionPolicy
	loginRateLimit                           LoginRateLimitPolicy
	personalAccessTokens                     PersonalAccessTokenPolicy
	diagnostics                              authenticationDiagnostics
	now                                      func() time.Time
	propagateAuthenticationCacheInvalidation func(context.Context, string, []string)
	propagateSessionRevocation               func(context.Context, string, []string, []string)
}

type cachedAuthentication struct {
	Credential *model.SessionCredential `json:"credential"`
	Session    *model.Session           `json:"session"`
	User       *model.User              `json:"user"`
}

func newAuthenticationService(
	persistence store.Store,
	cache authenticationCache,
	hasher *passwordHasher,
	mfa *MFAService,
	sessions SessionPolicy,
	loginRateLimit LoginRateLimitPolicy,
	personalAccessTokens PersonalAccessTokenPolicy,
	diagnostics authenticationDiagnostics,
	now func() time.Time,
) *AuthenticationService {
	if now == nil {
		now = time.Now
	}
	return &AuthenticationService{
		store:                persistence,
		cache:                cache,
		hasher:               hasher,
		mfa:                  mfa,
		sessions:             sessions,
		loginRateLimit:       loginRateLimit,
		personalAccessTokens: personalAccessTokens,
		diagnostics:          diagnostics,
		now:                  now,
	}
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

func (s *AuthenticationService) createLocalUser(
	ctx context.Context,
	command CreateLocalUserCommand,
) (*model.User, error) {
	hash, err := s.hasher.Hash(command.Password)
	if err != nil {
		return nil, NewError("authentication.password.invalid").
			WithField("field", "password").
			Wrap(err)
	}
	saved, _, err := s.store.User().SaveWithPassword(ctx, command.User, &model.PasswordCredential{
		PasswordHash: hash,
	})
	if err != nil {
		var conflict *store.ErrConflict
		if errors.As(err, &conflict) {
			return nil, NewError("authentication.user.conflict").Wrap(err)
		}
		return nil, authenticationUnavailable(err)
	}
	return saved, nil
}

func (a *App) Login(
	ctx context.Context,
	_ Invocation,
	command LoginCommand,
) (*LoginResult, error) {
	return a.authentication.login(ctx, command)
}

func (s *AuthenticationService) login(
	ctx context.Context,
	command LoginCommand,
) (*LoginResult, error) {
	identityRateKey, err := s.checkLoginRateLimit(ctx, command.LoginID, command.Source)
	if err != nil {
		return nil, err
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
	credential, err := s.store.PasswordCredential().GetByUser(ctx, user.ID.String())
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
	if !command.ClientType.IsValid() {
		return nil, NewError("authentication.client_type.invalid").
			WithField("field", "client_type")
	}

	authenticationStrength := model.AuthenticationSingleFactor
	mfaCompletedAt := int64(0)
	now := s.now()
	mfaPersistence := s.store.MFA()
	var (
		mfaCredential *model.MFACredential
		mfaErr        error
	)
	if mfaPersistence == nil {
		mfaErr = store.NewErrNotFound("mfa_credential", user.ID.String())
	} else {
		mfaCredential, mfaErr = mfaPersistence.GetByUser(ctx, user.ID.String())
	}
	switch {
	case mfaErr == nil && mfaCredential.IsActive():
		if s.mfa == nil || !s.mfa.settings.Enabled {
			return nil, NewError("authentication.mfa.unavailable")
		}
		if strings.TrimSpace(command.MFACode) == "" {
			return nil, NewError("authentication.mfa.required")
		}
		if appErr := s.mfa.consumeSecondFactor(
			ctx,
			s.store,
			user.ID.String(),
			command.MFACode,
			now,
		); appErr != nil {
			return nil, appErr
		}
		authenticationStrength = model.AuthenticationMultiFactor
		mfaCompletedAt = now.UnixMilli()
	case mfaErr != nil && !store.IsNotFound(mfaErr):
		return nil, authenticationUnavailable(mfaErr)
	}

	if s.hasher.NeedsRehash(credential.PasswordHash) {
		rehashed, hashErr := s.hasher.Hash(command.Password)
		if hashErr != nil {
			return nil, authenticationUnavailable(hashErr)
		}
		credential.PasswordHash = rehashed
		if _, updateErr := s.store.PasswordCredential().Update(ctx, credential); updateErr != nil {
			return nil, authenticationUnavailable(updateErr)
		}
	}

	savedSession, tokens, sessionErr := s.createSession(
		ctx,
		user,
		command.ClientType,
		command.DeviceID,
		command.DeviceName,
		"password",
		authenticationStrength,
		now.UnixMilli(),
		mfaCompletedAt,
	)
	if sessionErr != nil {
		return nil, sessionErr
	}
	if err := s.cache.Delete(ctx, identityRateKey); err != nil {
		s.warn(ctx, "login rate-limit reset failed", err)
	}
	return &LoginResult{User: user, Session: savedSession, Tokens: tokens}, nil
}

func (s *AuthenticationService) createSession(
	ctx context.Context,
	user *model.User,
	clientType model.SessionClientType,
	deviceID string,
	deviceName string,
	method string,
	strength model.AuthenticationStrength,
	authenticatedAt int64,
	mfaCompletedAt int64,
) (*model.Session, *model.AuthenticationTokens, error) {
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
	settings := s.sessions
	absoluteExpiresAt := nowMillis + settings.AbsoluteTTL.Milliseconds()
	accessExpiresAt := min(nowMillis+settings.AccessTTL.Milliseconds(), absoluteExpiresAt)
	refreshExpiresAt := min(nowMillis+settings.RefreshTTL.Milliseconds(), absoluteExpiresAt)
	session := &model.Session{
		UserID:                 user.ID,
		ClientType:             clientType,
		DeviceID:               deviceID,
		DeviceName:             deviceName,
		AuthenticationMethod:   method,
		AuthenticationStrength: strength,
		AuthenticatedAt:        model.TimeFromMillis(authenticatedAt),
		MFACompletedAt:         model.OptionalTimeFromMillis(mfaCompletedAt),
		LastActivityAt:         model.TimeFromMillis(nowMillis),
		IdleExpiresAt: model.TimeFromMillis(min(
			nowMillis+settings.IdleTTL.Milliseconds(),
			absoluteExpiresAt,
		)),
		ExpiresAt: model.TimeFromMillis(absoluteExpiresAt),
	}
	accessToken := model.NewCredentialToken()
	refreshToken := model.NewCredentialToken()
	savedSession, credentials, saveErr := s.store.Session().Save(
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
		var conflict *store.ErrConflict
		if errors.As(saveErr, &conflict) &&
			conflict.Constraint == "sessions_maximum_per_user" {
			return nil, nil, NewError("authentication.sessions.maximum_reached")
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
		AccessToken:      accessToken,
		RefreshToken:     refreshToken,
		AccessExpiresAt:  model.TimeFromMillis(accessExpiresAt),
		RefreshExpiresAt: model.TimeFromMillis(refreshExpiresAt),
	}, nil
}

func (s *AuthenticationService) checkLoginRateLimit(
	ctx context.Context,
	loginID string,
	source string,
) (string, error) {
	settings := s.loginRateLimit
	normalizedLogin := strings.ToLower(strings.TrimSpace(loginID))
	normalizedSource := normalizeLoginSource(source)
	identityKey := "authentication/login/identity/" + digestCacheKey(normalizedLogin+"\x00"+normalizedSource)
	sourceKey := "authentication/login/source/" + digestCacheKey(normalizedSource)
	identityCount, err := s.cache.Add(
		ctx,
		identityKey,
		1,
		settings.Window,
	)
	if err != nil {
		return "", rateLimitUnavailableAppError(err)
	}
	sourceCount, err := s.cache.Add(
		ctx,
		sourceKey,
		1,
		settings.Window,
	)
	if err != nil {
		return "", rateLimitUnavailableAppError(err)
	}
	if identityCount > int64(settings.MaximumAttempts) ||
		sourceCount > int64(settings.MaximumSourceAttempts) {
		return "", NewError("authentication.rate_limited")
	}
	return identityKey, nil
}

func normalizeLoginSource(source string) string {
	source = strings.TrimSpace(source)
	if host, _, err := net.SplitHostPort(source); err == nil {
		return strings.ToLower(host)
	}
	if source == "" {
		return "unknown"
	}
	return strings.ToLower(source)
}

func digestCacheKey(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func (s *AuthenticationService) findLoginUser(ctx context.Context, loginID string) (*model.User, error) {
	loginID = strings.ToLower(strings.TrimSpace(loginID))
	if strings.Contains(loginID, "@") {
		return s.store.User().GetByEmail(ctx, loginID)
	}
	return s.store.User().GetByUsername(ctx, loginID)
}

func (a *App) AuthenticateAccess(
	ctx context.Context,
	rawToken string,
) (*model.Principal, error) {
	return a.authentication.authenticateAccess(ctx, rawToken)
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
		return principal, nil
	}
	if failure, ok := As(err); !ok || failure.Code() != "authentication.invalid_token" {
		return nil, err
	}
	return a.authentication.authenticatePersonalAccessToken(ctx, rawToken)
}

func (s *AuthenticationService) authenticatePersonalAccessToken(
	ctx context.Context,
	rawToken string,
) (*model.Principal, error) {
	if !validRawCredential(rawToken) {
		return nil, invalidTokenAppError()
	}
	settings := s.personalAccessTokens
	tokenStore := s.store.PersonalAccessToken()
	if tokenStore == nil {
		return nil, authenticationUnavailable(
			errors.New("personal access token store is unavailable"),
		)
	}
	resolved, err := tokenStore.Resolve(
		ctx,
		model.HashToken(rawToken),
		s.now().UnixMilli(),
		settings.LastUsedUpdateInterval.Milliseconds(),
	)
	if err != nil {
		if store.IsNotFound(err) {
			return nil, invalidTokenAppError()
		}
		return nil, authenticationUnavailable(err)
	}
	principal := &model.Principal{
		UserID:               resolved.User.ID,
		CredentialID:         model.PrincipalCredentialID(resolved.Token.ID),
		CredentialType:       model.CredentialPersonalAccessToken,
		AuthenticationMethod: "personal_access_token",
		ClientType:           model.SessionClientCLI,
		CredentialScopes:     append([]string(nil), resolved.Token.Scopes...),
		AcademicUnitID:       resolved.Token.AcademicUnitID,
	}
	if principal.Validate() != nil {
		s.warn(ctx, "personal access token resolved to invalid principal",
			fmt.Errorf("personal_access_token_id=%s", resolved.Token.ID.String()))
		return nil, invalidTokenAppError()
	}
	return principal, nil
}

func (s *AuthenticationService) authenticateAccess(
	ctx context.Context,
	rawToken string,
) (*model.Principal, error) {
	if !validRawCredential(rawToken) {
		return nil, invalidTokenAppError()
	}
	now := s.now().UnixMilli()
	tokenHash := model.HashToken(rawToken)
	resolved := s.cachedAuthentication(ctx, tokenHash)
	if resolved == nil {
		credential, session, err := s.store.SessionCredential().GetSessionByTokenHash(
			ctx,
			tokenHash,
			model.SessionCredentialAccess,
		)
		if err != nil {
			if store.IsNotFound(err) {
				return nil, invalidTokenAppError()
			}
			return nil, authenticationUnavailable(err)
		}
		user, err := s.store.User().Get(ctx, session.UserID.String())
		if err != nil {
			if store.IsNotFound(err) {
				return nil, invalidTokenAppError()
			}
			return nil, authenticationUnavailable(err)
		}
		resolved = &cachedAuthentication{Credential: credential, Session: session, User: user}
	}
	nowTime := model.TimeFromMillis(now)
	if !resolved.User.IsActive() ||
		resolved.Credential.IsExpiredAt(nowTime) ||
		resolved.Session.IsExpiredAt(nowTime) {
		_ = s.cache.Delete(ctx, authenticationCachePrefix+tokenHash)
		return nil, invalidTokenAppError()
	}
	if err := s.updateActivity(ctx, resolved, now); err != nil {
		return nil, err
	}
	s.cacheAuthentication(ctx, tokenHash, resolved, now)
	principal := &model.Principal{
		UserID:                 resolved.User.ID,
		SessionID:              resolved.Session.ID,
		CredentialID:           model.PrincipalCredentialID(resolved.Credential.ID),
		CredentialType:         model.CredentialSessionAccess,
		AuthenticationMethod:   resolved.Session.AuthenticationMethod,
		AuthenticationStrength: resolved.Session.AuthenticationStrength,
		ClientType:             resolved.Session.ClientType,
		AuthenticatedAt:        resolved.Session.AuthenticatedAt,
		MFACompletedAt:         resolved.Session.MFACompletedAt,
	}
	if principal.Validate() != nil {
		return nil, authenticationUnavailable(
			errors.New("resolved principal is invalid"),
		)
	}
	return principal, nil
}

func (s *AuthenticationService) updateActivity(
	ctx context.Context,
	resolved *cachedAuthentication,
	now int64,
) error {
	settings := s.sessions
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
	if errors.Is(err, errAuthenticationCacheNotStored) {
		return nil
	}
	if err != nil {
		s.warn(ctx, "session activity debounce cache failed", err)
		return nil
	}
	idleExpiresAt := min(now+settings.IdleTTL.Milliseconds(), model.MillisFromTime(resolved.Session.ExpiresAt))
	if err := s.store.Session().UpdateActivity(
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
	return a.authentication.refresh(ctx, command.RefreshToken)
}

func (s *AuthenticationService) refresh(
	ctx context.Context,
	rawRefreshToken string,
) (*model.Session, *model.AuthenticationTokens, error) {
	if !validRawCredential(rawRefreshToken) {
		return nil, nil, invalidTokenAppError()
	}
	now := s.now().UnixMilli()
	settings := s.sessions
	accessToken := model.NewCredentialToken()
	refreshToken := model.NewCredentialToken()
	rotation, err := s.store.SessionCredential().RotateRefresh(
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
	s.deleteAuthenticationCache(ctx, rotation.RevokedAccessHashes)
	if rotation.ReplayDetected && s.propagateSessionRevocation != nil {
		s.propagateSessionRevocation(
			ctx,
			rotation.Session.UserID.String(),
			[]string{rotation.Session.ID.String()},
			rotation.RevokedAccessHashes,
		)
	} else if s.propagateAuthenticationCacheInvalidation != nil {
		s.propagateAuthenticationCacheInvalidation(
			ctx,
			rotation.Session.UserID.String(),
			rotation.RevokedAccessHashes,
		)
	}
	if rotation.ReplayDetected {
		return nil, nil, invalidTokenAppError()
	}
	user, err := s.store.User().Get(ctx, rotation.Session.UserID.String())
	if err != nil || !user.IsActive() {
		if err == nil {
			var revokedAccessHashes []string
			revokedAccessHashes, err = s.store.Session().Revoke(
				ctx,
				rotation.Session.ID.String(),
				rotation.Session.UserID.String(),
				now,
				"inactive user",
			)
			if err == nil {
				s.deleteAuthenticationCache(ctx, revokedAccessHashes)
				s.deleteActivityCache(ctx, rotation.Session.ID.String())
				if s.propagateSessionRevocation != nil {
					s.propagateSessionRevocation(
						ctx,
						rotation.Session.UserID.String(),
						[]string{rotation.Session.ID.String()},
						revokedAccessHashes,
					)
				}
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
		AccessToken:      accessToken,
		RefreshToken:     refreshToken,
		AccessExpiresAt:  rotation.AccessCredential.ExpiresAt,
		RefreshExpiresAt: rotation.RefreshCredential.ExpiresAt,
	}, nil
}

func (a *App) Logout(ctx context.Context, invocation Invocation, _ LogoutCommand) error {
	principal := invocation.Principal()
	if principal.Validate() != nil {
		return invalidTokenAppError()
	}
	now := a.authentication.now().UnixMilli()
	hashes, err := a.Store().Session().Revoke(
		ctx,
		principal.SessionID.String(),
		principal.UserID.String(),
		now,
		"user logout",
	)
	if err != nil {
		if store.IsNotFound(err) {
			return nil
		}
		return authenticationUnavailable(err)
	}
	a.authentication.deleteAuthenticationCache(ctx, hashes)
	a.authentication.deleteActivityCache(ctx, principal.SessionID.String())
	a.realtime.PropagateSessionRevocation(
		ctx,
		principal.UserID.String(),
		[]string{principal.SessionID.String()},
		hashes,
	)
	return nil
}

func (a *App) GetUser(ctx context.Context, id string) (*model.User, error) {
	user, err := a.Store().User().Get(ctx, id)
	if err != nil {
		if store.IsNotFound(err) {
			return nil, NewError("user.not_found")
		}
		return nil, authenticationUnavailable(err)
	}
	return user, nil
}

func (s *AuthenticationService) cachedAuthentication(
	ctx context.Context,
	tokenHash string,
) *cachedAuthentication {
	data, err := s.cache.Get(ctx, authenticationCachePrefix+tokenHash)
	if errors.Is(err, errAuthenticationCacheMiss) {
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

func (s *AuthenticationService) cacheAuthentication(
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

func (s *AuthenticationService) deleteAuthenticationCache(ctx context.Context, hashes []string) {
	for _, hash := range hashes {
		if err := s.cache.Delete(ctx, authenticationCachePrefix+hash); err != nil {
			s.warn(ctx, "authentication cache delete failed", err)
		}
	}
}

func (s *AuthenticationService) deleteActivityCache(ctx context.Context, sessionID string) {
	if err := s.cache.Delete(ctx, activityCachePrefix+sessionID); err != nil {
		s.warn(ctx, "session activity cache delete failed", err)
	}
}

func (s *AuthenticationService) warn(ctx context.Context, message string, err error) {
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
