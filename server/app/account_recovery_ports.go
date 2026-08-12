// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type accountTokenPasswordHasher interface {
	Hash(string) (string, error)
}

type accountTokenRateLimiter interface {
	Allow(context.Context, string, string, string) error
}

type accountTokenAudit interface {
	Success(string, model.Resource, string, Invocation, *model.Principal, string) *model.AuditEvent
}

type accountTokenEffects interface {
	SessionsRevoked(context.Context, string, []string, []string)
}

// accountTokenService owns purpose-specific verification and recovery tokens.
// Terminal transitions and their success audits remain named atomic store
// operations; only post-commit session effects happen outside persistence.
type accountTokenService struct {
	users        store.UserStore
	passwords    store.PasswordCredentialStore
	tokens       store.UserTokenStore
	institutions store.InstitutionStore
	mailer       AccountMailer
	rateLimiter  accountTokenRateLimiter
	hasher       accountTokenPasswordHasher
	audit        accountTokenAudit
	effects      accountTokenEffects
	diagnostics  recoveryDiagnostics
	policy       AccountRecoveryPolicy
	publicURL    string
	newToken     func() string
	now          func() time.Time
}

func newAccountTokenService(
	users store.UserStore,
	passwords store.PasswordCredentialStore,
	tokens store.UserTokenStore,
	institutions store.InstitutionStore,
	mailer AccountMailer,
	rateLimiter accountTokenRateLimiter,
	hasher accountTokenPasswordHasher,
	audit accountTokenAudit,
	effects accountTokenEffects,
	diagnostics recoveryDiagnostics,
	policy AccountRecoveryPolicy,
	publicURL string,
	newToken func() string,
	now func() time.Time,
) (*accountTokenService, error) {
	required := []struct {
		missing bool
		name    string
	}{
		{users == nil, "user store"},
		{passwords == nil, "password credential store"},
		{tokens == nil, "user token store"},
		{institutions == nil, "institution store"},
		{mailer == nil, "account mailer"},
		{rateLimiter == nil, "account recovery rate limiter"},
		{hasher == nil, "password hasher"},
		{audit == nil, "account recovery audit"},
		{effects == nil, "account recovery effects"},
		{diagnostics == nil, "account recovery diagnostics"},
		{newToken == nil, "account token generator"},
		{now == nil, "account token clock"},
	}
	for _, dependency := range required {
		if dependency.missing {
			return nil, errors.New(dependency.name + " is required")
		}
	}
	return &accountTokenService{
		users: users, passwords: passwords, tokens: tokens,
		institutions: institutions, mailer: mailer, rateLimiter: rateLimiter,
		hasher: hasher, audit: audit, effects: effects, diagnostics: diagnostics,
		policy: policy, publicURL: publicURL, newToken: newToken, now: now,
	}, nil
}

type cacheAccountTokenRateLimiter struct {
	cache  authenticationCache
	policy LoginRateLimitPolicy
}

func newCacheAccountTokenRateLimiter(
	cache authenticationCache,
	policy LoginRateLimitPolicy,
) (*cacheAccountTokenRateLimiter, error) {
	if cache == nil {
		return nil, errors.New("account recovery cache is required")
	}
	return &cacheAccountTokenRateLimiter{cache: cache, policy: policy}, nil
}

func (l *cacheAccountTokenRateLimiter) Allow(
	ctx context.Context,
	operation string,
	identity string,
	source string,
) error {
	identityKey := "authentication/recovery/" + operation + "/identity/" +
		digestCacheKey(strings.ToLower(strings.TrimSpace(identity)))
	sourceKey := "authentication/recovery/" + operation + "/source/" +
		digestCacheKey(normalizeLoginSource(source))
	identityCount, err := l.cache.Add(ctx, identityKey, 1, l.policy.Window)
	if err != nil {
		return rateLimitUnavailableAppError(err)
	}
	sourceCount, err := l.cache.Add(ctx, sourceKey, 1, l.policy.Window)
	if err != nil {
		return rateLimitUnavailableAppError(err)
	}
	if identityCount > int64(l.policy.MaximumAttempts) ||
		sourceCount > int64(l.policy.MaximumSourceAttempts) {
		return NewError("authentication.rate_limited")
	}
	return nil
}

type accountTokenAuditRecorder struct{ nodeID string }

func (r accountTokenAuditRecorder) Success(
	action string,
	resource model.Resource,
	institutionID string,
	invocation Invocation,
	principal *model.Principal,
	authenticationMethod string,
) *model.AuditEvent {
	return recoveryAuditEvent(
		action, resource, institutionID, invocation.RequestMetadata(), r.nodeID,
		principal, authenticationMethod,
	)
}

var (
	_ accountTokenPasswordHasher = (*passwordHasher)(nil)
	_ accountTokenRateLimiter    = (*cacheAccountTokenRateLimiter)(nil)
	_ accountTokenAudit          = accountTokenAuditRecorder{}
	_ accountTokenEffects        = (*realtimeService)(nil)
)
