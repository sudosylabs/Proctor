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
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/mlog"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/platform"
	"github.com/sudosylabs/proctor/server/store"
)

const (
	authenticationCachePrefix = "authentication/access/"
	activityCachePrefix       = "authentication/activity/"
)

type AuthenticationService struct {
	platform *platform.Service
	hasher   *passwordHasher
	settings config.Authentication
	now      func() time.Time
}

type cachedAuthentication struct {
	Credential *model.SessionCredential `json:"credential"`
	Session    *model.Session           `json:"session"`
	User       *model.User              `json:"user"`
}

func newAuthenticationService(applicationPlatform *platform.Service) (*AuthenticationService, error) {
	settings := applicationPlatform.Config().Authentication
	hasher, err := newPasswordHasher(settings.Password)
	if err != nil {
		return nil, err
	}
	return &AuthenticationService{
		platform: applicationPlatform,
		hasher:   hasher,
		settings: settings,
		now:      time.Now,
	}, nil
}

func (a *App) CreateLocalUser(
	ctx context.Context,
	user *model.User,
	password string,
) (*model.User, *model.AppError) {
	hash, err := a.authentication.hasher.Hash(password)
	if err != nil {
		return nil, model.NewAppError(
			"CreateLocalUser",
			"authentication.password.invalid",
			nil,
			err.Error(),
			http.StatusBadRequest,
		).WithSafeFields(map[string]string{"field": "password"})
	}
	saved, _, err := a.Store().User().SaveWithPassword(ctx, user, &model.PasswordCredential{
		PasswordHash: hash,
	})
	if err != nil {
		var conflict *store.ErrConflict
		if errors.As(err, &conflict) {
			return nil, model.NewAppError(
				"CreateLocalUser",
				"authentication.user.conflict",
				nil,
				conflict.Constraint,
				http.StatusConflict,
			).Wrap(err)
		}
		return nil, internalAuthenticationError("CreateLocalUser", err)
	}
	return saved, nil
}

func (a *App) Login(
	ctx context.Context,
	loginID string,
	password string,
	clientType model.SessionClientType,
	deviceID string,
	deviceName string,
	source string,
) (*model.User, *model.Session, *model.AuthenticationTokens, *model.AppError) {
	return a.authentication.login(ctx, loginID, password, clientType, deviceID, deviceName, source)
}

func (s *AuthenticationService) login(
	ctx context.Context,
	loginID string,
	password string,
	clientType model.SessionClientType,
	deviceID string,
	deviceName string,
	source string,
) (*model.User, *model.Session, *model.AuthenticationTokens, *model.AppError) {
	identityRateKey, appErr := s.checkLoginRateLimit(ctx, loginID, source)
	if appErr != nil {
		return nil, nil, nil, appErr
	}
	if loginID == "" ||
		len(loginID) > model.UserEmailMaxLength ||
		len(password) > s.hasher.maximumLength {
		s.hasher.VerifyDummy("invalid-password-length")
		return nil, nil, nil, invalidCredentialsError("Login")
	}
	user, err := s.findLoginUser(ctx, loginID)
	if err != nil {
		s.hasher.VerifyDummy(password)
		if !store.IsNotFound(err) {
			return nil, nil, nil, internalAuthenticationError("Login.user", err)
		}
		return nil, nil, nil, invalidCredentialsError("Login")
	}
	credential, err := s.platform.Store().PasswordCredential().GetByUser(ctx, user.Id)
	if err != nil {
		s.hasher.VerifyDummy(password)
		if !store.IsNotFound(err) {
			return nil, nil, nil, internalAuthenticationError("Login.password", err)
		}
		return nil, nil, nil, invalidCredentialsError("Login")
	}
	if verifyErr := s.hasher.Verify(credential.PasswordHash, password); verifyErr != nil || !user.IsActive() {
		return nil, nil, nil, invalidCredentialsError("Login")
	}
	if !clientType.IsValid() {
		return nil, nil, nil, model.NewAppError(
			"Login",
			"authentication.client_type.invalid",
			nil,
			"",
			http.StatusBadRequest,
		).WithSafeFields(map[string]string{"field": "client_type"})
	}

	if s.hasher.NeedsRehash(credential.PasswordHash) {
		rehashed, hashErr := s.hasher.Hash(password)
		if hashErr != nil {
			return nil, nil, nil, internalAuthenticationError("Login.rehash", hashErr)
		}
		credential.PasswordHash = rehashed
		if _, updateErr := s.platform.Store().PasswordCredential().Update(ctx, credential); updateErr != nil {
			return nil, nil, nil, internalAuthenticationError("Login.rehash", updateErr)
		}
	}

	now := s.now().UnixMilli()
	settings := s.settings.Sessions
	absoluteExpiresAt := now + settings.AbsoluteTTL.Milliseconds()
	accessExpiresAt := min(now+settings.AccessTTL.Milliseconds(), absoluteExpiresAt)
	refreshExpiresAt := min(now+settings.RefreshTTL.Milliseconds(), absoluteExpiresAt)
	session := &model.Session{
		UserId:                 user.Id,
		ClientType:             clientType,
		DeviceId:               deviceID,
		DeviceName:             deviceName,
		AuthenticationMethod:   "password",
		AuthenticationStrength: model.AuthenticationSingleFactor,
		AuthenticatedAt:        now,
		LastActivityAt:         now,
		IdleExpiresAt:          min(now+settings.IdleTTL.Milliseconds(), absoluteExpiresAt),
		ExpiresAt:              absoluteExpiresAt,
	}
	accessToken := model.NewCredentialToken()
	refreshToken := model.NewCredentialToken()
	savedSession, credentials, saveErr := s.platform.Store().Session().Save(
		ctx,
		session,
		[]*model.SessionCredential{
			{
				Kind:      model.SessionCredentialAccess,
				TokenHash: model.HashToken(accessToken),
				ExpiresAt: accessExpiresAt,
			},
			{
				Kind:      model.SessionCredentialRefresh,
				TokenHash: model.HashToken(refreshToken),
				ExpiresAt: refreshExpiresAt,
			},
		},
		settings.MaximumPerUser,
	)
	if saveErr != nil {
		var conflict *store.ErrConflict
		if errors.As(saveErr, &conflict) && conflict.Constraint == "sessions_maximum_per_user" {
			return nil, nil, nil, model.NewAppError(
				"Login",
				"authentication.sessions.maximum_reached",
				nil,
				"",
				http.StatusConflict,
			)
		}
		return nil, nil, nil, internalAuthenticationError("Login.save_session", saveErr)
	}

	var accessCredential *model.SessionCredential
	for _, savedCredential := range credentials {
		if savedCredential.Kind == model.SessionCredentialAccess {
			accessCredential = savedCredential
			break
		}
	}
	if accessCredential == nil {
		return nil, nil, nil, internalAuthenticationError(
			"Login.save_session",
			errors.New("saved session has no access credential"),
		)
	}
	s.cacheAuthentication(ctx, model.HashToken(accessToken), &cachedAuthentication{
		Credential: accessCredential,
		Session:    savedSession,
		User:       user,
	}, now)
	if err := s.platform.Cache().Delete(ctx, identityRateKey); err != nil {
		s.platform.Log().WarnContext(ctx, "login rate-limit reset failed", mlog.Err(err))
	}
	return user, savedSession, &model.AuthenticationTokens{
		AccessToken:      accessToken,
		RefreshToken:     refreshToken,
		AccessExpiresAt:  accessExpiresAt,
		RefreshExpiresAt: refreshExpiresAt,
	}, nil
}

func (s *AuthenticationService) checkLoginRateLimit(
	ctx context.Context,
	loginID string,
	source string,
) (string, *model.AppError) {
	settings := s.settings.LoginRateLimit
	normalizedLogin := strings.ToLower(strings.TrimSpace(loginID))
	normalizedSource := normalizeLoginSource(source)
	identityKey := "authentication/login/identity/" + digestCacheKey(normalizedLogin+"\x00"+normalizedSource)
	sourceKey := "authentication/login/source/" + digestCacheKey(normalizedSource)
	identityCount, err := s.platform.Cache().Add(
		ctx,
		identityKey,
		1,
		settings.Window.Duration,
	)
	if err != nil {
		return "", rateLimitUnavailableError("Login.rate_limit.identity", err)
	}
	sourceCount, err := s.platform.Cache().Add(
		ctx,
		sourceKey,
		1,
		settings.Window.Duration,
	)
	if err != nil {
		return "", rateLimitUnavailableError("Login.rate_limit.source", err)
	}
	if identityCount > int64(settings.MaximumAttempts) ||
		sourceCount > int64(settings.MaximumSourceAttempts) {
		return "", model.NewAppError(
			"Login",
			"authentication.rate_limited",
			nil,
			"",
			http.StatusTooManyRequests,
		)
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
		return s.platform.Store().User().GetByEmail(ctx, loginID)
	}
	return s.platform.Store().User().GetByUsername(ctx, loginID)
}

func (a *App) AuthenticateAccess(
	ctx context.Context,
	rawToken string,
) (*model.Principal, *model.AppError) {
	return a.authentication.authenticateAccess(ctx, rawToken)
}

func (s *AuthenticationService) authenticateAccess(
	ctx context.Context,
	rawToken string,
) (*model.Principal, *model.AppError) {
	if !validRawCredential(rawToken) {
		return nil, invalidTokenError("AuthenticateAccess")
	}
	now := s.now().UnixMilli()
	tokenHash := model.HashToken(rawToken)
	resolved := s.cachedAuthentication(ctx, tokenHash)
	if resolved == nil {
		credential, session, err := s.platform.Store().SessionCredential().GetSessionByTokenHash(
			ctx,
			tokenHash,
			model.SessionCredentialAccess,
		)
		if err != nil {
			if store.IsNotFound(err) {
				return nil, invalidTokenError("AuthenticateAccess")
			}
			return nil, internalAuthenticationError("AuthenticateAccess.resolve", err)
		}
		user, err := s.platform.Store().User().Get(ctx, session.UserId)
		if err != nil {
			if store.IsNotFound(err) {
				return nil, invalidTokenError("AuthenticateAccess")
			}
			return nil, internalAuthenticationError("AuthenticateAccess.user", err)
		}
		resolved = &cachedAuthentication{Credential: credential, Session: session, User: user}
	}
	if !resolved.User.IsActive() ||
		resolved.Credential.IsExpiredAt(now) ||
		resolved.Session.IsExpiredAt(now) {
		_ = s.platform.Cache().Delete(ctx, authenticationCachePrefix+tokenHash)
		return nil, invalidTokenError("AuthenticateAccess")
	}
	if appErr := s.updateActivity(ctx, resolved, now); appErr != nil {
		return nil, appErr
	}
	s.cacheAuthentication(ctx, tokenHash, resolved, now)
	principal := &model.Principal{
		UserId:                 resolved.User.Id,
		SessionId:              resolved.Session.Id,
		CredentialId:           resolved.Credential.Id,
		CredentialType:         model.CredentialSessionAccess,
		AuthenticationMethod:   resolved.Session.AuthenticationMethod,
		AuthenticationStrength: resolved.Session.AuthenticationStrength,
		ClientType:             resolved.Session.ClientType,
		AuthenticatedAt:        resolved.Session.AuthenticatedAt,
		MFACompletedAt:         resolved.Session.MFACompletedAt,
	}
	if !principal.IsValid() {
		return nil, internalAuthenticationError(
			"AuthenticateAccess.principal",
			errors.New("resolved principal is invalid"),
		)
	}
	return principal, nil
}

func (s *AuthenticationService) updateActivity(
	ctx context.Context,
	resolved *cachedAuthentication,
	now int64,
) *model.AppError {
	settings := s.settings.Sessions
	if now-resolved.Session.LastActivityAt < settings.ActivityUpdateInterval.Milliseconds() {
		return nil
	}
	key := activityCachePrefix + resolved.Session.Id
	err := s.platform.Cache().Set(
		ctx,
		key,
		[]byte{1},
		settings.ActivityUpdateInterval.Duration,
		platform.CacheSetIfAbsent,
	)
	if errors.Is(err, platform.ErrCacheNotStored) {
		return nil
	}
	if err != nil {
		s.platform.Log().WarnContext(ctx, "session activity debounce cache failed", mlog.Err(err))
		return nil
	}
	idleExpiresAt := min(now+settings.IdleTTL.Milliseconds(), resolved.Session.ExpiresAt)
	if err := s.platform.Store().Session().UpdateActivity(
		ctx,
		resolved.Session.Id,
		now,
		idleExpiresAt,
	); err != nil {
		if store.IsNotFound(err) {
			return invalidTokenError("AuthenticateAccess.activity")
		}
		return internalAuthenticationError("AuthenticateAccess.activity", err)
	}
	resolved.Session.LastActivityAt = now
	resolved.Session.IdleExpiresAt = idleExpiresAt
	resolved.Session.UpdateAt = max(resolved.Session.UpdateAt, now)
	return nil
}

func (a *App) RefreshSession(
	ctx context.Context,
	rawRefreshToken string,
) (*model.Session, *model.AuthenticationTokens, *model.AppError) {
	return a.authentication.refresh(ctx, rawRefreshToken)
}

func (s *AuthenticationService) refresh(
	ctx context.Context,
	rawRefreshToken string,
) (*model.Session, *model.AuthenticationTokens, *model.AppError) {
	if !validRawCredential(rawRefreshToken) {
		return nil, nil, invalidTokenError("RefreshSession")
	}
	now := s.now().UnixMilli()
	settings := s.settings.Sessions
	accessToken := model.NewCredentialToken()
	refreshToken := model.NewCredentialToken()
	rotation, err := s.platform.Store().SessionCredential().RotateRefresh(
		ctx,
		model.HashToken(rawRefreshToken),
		&model.SessionCredential{
			TokenHash: model.HashToken(accessToken),
			ExpiresAt: now + settings.AccessTTL.Milliseconds(),
		},
		&model.SessionCredential{
			TokenHash: model.HashToken(refreshToken),
			ExpiresAt: now + settings.RefreshTTL.Milliseconds(),
		},
		now,
		now+settings.IdleTTL.Milliseconds(),
	)
	if err != nil {
		var conflict *store.ErrConflict
		if store.IsNotFound(err) || errors.As(err, &conflict) {
			return nil, nil, invalidTokenError("RefreshSession")
		}
		return nil, nil, internalAuthenticationError("RefreshSession.rotate", err)
	}
	s.deleteAuthenticationCache(ctx, rotation.RevokedAccessHashes)
	if rotation.ReplayDetected {
		return nil, nil, invalidTokenError("RefreshSession.replay")
	}
	user, err := s.platform.Store().User().Get(ctx, rotation.Session.UserId)
	if err != nil || !user.IsActive() {
		if err == nil {
			var revokedAccessHashes []string
			revokedAccessHashes, err = s.platform.Store().Session().Revoke(
				ctx,
				rotation.Session.Id,
				rotation.Session.UserId,
				now,
				"inactive user",
			)
			if err == nil {
				s.deleteAuthenticationCache(ctx, revokedAccessHashes)
				s.deleteActivityCache(ctx, rotation.Session.Id)
			}
		}
		return nil, nil, invalidTokenError("RefreshSession.user")
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

func (a *App) Logout(ctx context.Context, principal model.Principal) *model.AppError {
	now := a.authentication.now().UnixMilli()
	hashes, err := a.Store().Session().Revoke(
		ctx,
		principal.SessionId,
		principal.UserId,
		now,
		"user logout",
	)
	if err != nil {
		if store.IsNotFound(err) {
			return nil
		}
		return internalAuthenticationError("Logout", err)
	}
	a.authentication.deleteAuthenticationCache(ctx, hashes)
	a.authentication.deleteActivityCache(ctx, principal.SessionId)
	return nil
}

func (a *App) GetUser(ctx context.Context, id string) (*model.User, *model.AppError) {
	user, err := a.Store().User().Get(ctx, id)
	if err != nil {
		if store.IsNotFound(err) {
			return nil, model.NewAppError(
				"GetUser",
				"user.not_found",
				nil,
				"",
				http.StatusNotFound,
			)
		}
		return nil, internalAuthenticationError("GetUser", err)
	}
	return user, nil
}

func (s *AuthenticationService) cachedAuthentication(
	ctx context.Context,
	tokenHash string,
) *cachedAuthentication {
	data, err := s.platform.Cache().Get(ctx, authenticationCachePrefix+tokenHash)
	if errors.Is(err, platform.ErrCacheMiss) {
		return nil
	}
	if err != nil {
		s.platform.Log().WarnContext(ctx, "authentication cache get failed", mlog.Err(err))
		return nil
	}
	var resolved cachedAuthentication
	if err := json.Unmarshal(data, &resolved); err != nil ||
		resolved.Credential == nil ||
		resolved.Session == nil ||
		resolved.User == nil {
		_ = s.platform.Cache().Delete(ctx, authenticationCachePrefix+tokenHash)
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
		resolved.Credential.ExpiresAt,
		resolved.Session.IdleExpiresAt,
		resolved.Session.ExpiresAt,
	)
	if expiresAt <= now {
		return
	}
	data, err := json.Marshal(resolved)
	if err != nil {
		s.platform.Log().WarnContext(ctx, "encode authentication cache value failed", mlog.Err(err))
		return
	}
	if err := s.platform.Cache().Set(
		ctx,
		authenticationCachePrefix+tokenHash,
		data,
		time.Duration(expiresAt-now)*time.Millisecond,
		platform.CacheSetAlways,
	); err != nil {
		s.platform.Log().WarnContext(ctx, "authentication cache set failed", mlog.Err(err))
	}
}

func (s *AuthenticationService) deleteAuthenticationCache(ctx context.Context, hashes []string) {
	for _, hash := range hashes {
		if err := s.platform.Cache().Delete(ctx, authenticationCachePrefix+hash); err != nil {
			s.platform.Log().WarnContext(ctx, "authentication cache delete failed", mlog.Err(err))
		}
	}
}

func (s *AuthenticationService) deleteActivityCache(ctx context.Context, sessionID string) {
	if err := s.platform.Cache().Delete(ctx, activityCachePrefix+sessionID); err != nil {
		s.platform.Log().WarnContext(ctx, "session activity cache delete failed", mlog.Err(err))
	}
}

func validRawCredential(token string) bool {
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(token)
	return err == nil && len(decoded) == 32
}

func invalidCredentialsError(where string) *model.AppError {
	return model.NewAppError(
		where,
		"authentication.invalid_credentials",
		nil,
		"",
		http.StatusUnauthorized,
	)
}

func invalidTokenError(where string) *model.AppError {
	return model.NewAppError(
		where,
		"authentication.invalid_token",
		nil,
		"",
		http.StatusUnauthorized,
	)
}

func internalAuthenticationError(where string, err error) *model.AppError {
	return model.NewAppError(
		where,
		"authentication.internal",
		nil,
		fmt.Sprintf("%T", err),
		http.StatusInternalServerError,
	).Wrap(err)
}

func rateLimitUnavailableError(where string, err error) *model.AppError {
	return model.NewAppError(
		where,
		"authentication.rate_limit_unavailable",
		nil,
		fmt.Sprintf("%T", err),
		http.StatusServiceUnavailable,
	).Wrap(err)
}
