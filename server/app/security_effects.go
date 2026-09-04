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
)

type authenticationInvalidator interface {
	InvalidateAccessCredentials(context.Context, []string)
	InvalidateSessionActivity(context.Context, []string)
}

type authenticationSecurityEffects interface {
	AuthenticationCacheInvalidated(context.Context, string, []string)
	SessionsRevoked(context.Context, string, []string, []string)
}

type authorizationInvalidationEffects interface {
	InvalidateAuthorization(context.Context, string)
}

type authenticationCacheInvalidator struct {
	cache       authenticationCache
	diagnostics authenticationDiagnostics
}

func newAuthenticationCacheInvalidator(
	cache authenticationCache,
	diagnostics authenticationDiagnostics,
) (*authenticationCacheInvalidator, error) {
	if cache == nil {
		return nil, errors.New("authentication cache is required")
	}
	if diagnostics == nil {
		return nil, errors.New("authentication diagnostics are required")
	}
	return &authenticationCacheInvalidator{cache: cache, diagnostics: diagnostics}, nil
}

func (i *authenticationCacheInvalidator) InvalidateAccessCredentials(
	ctx context.Context,
	hashes []string,
) {
	for _, hash := range hashes {
		if err := i.cache.Delete(ctx, authenticationCachePrefix+hash); err != nil {
			i.diagnostics.WarnContext(
				ctx,
				"authentication cache delete failed",
				errors.New("cache operation failed"),
			)
		}
	}
}

func (i *authenticationCacheInvalidator) InvalidateSessionActivity(
	ctx context.Context,
	sessionIDs []string,
) {
	for _, sessionID := range sessionIDs {
		if err := i.cache.Delete(ctx, activityCachePrefix+sessionID); err != nil {
			i.diagnostics.WarnContext(
				ctx,
				"session activity cache delete failed",
				errors.New("cache operation failed"),
			)
		}
	}
}

var _ authenticationInvalidator = (*authenticationCacheInvalidator)(nil)
