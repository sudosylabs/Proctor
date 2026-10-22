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

	"github.com/sudosylabs/proctor/server/app/realtime"
)

// Access-key deletion retains the peer invalidation contract and clears
// snapshots left by an older process. Current authentication never reads or
// writes positive entries in this namespace.
const authenticationCachePrefix = "authentication/access/"

type authenticationSecurityEffects interface {
	AuthenticationCacheInvalidated(context.Context, string, []string)
	SessionsRevoked(context.Context, string, []string, []string)
}

type authorizationInvalidationEffects interface {
	InvalidateAuthorization(context.Context, string)
}

// authenticationCacheInvalidatorDependencies are the collaborators of authenticationCacheInvalidator;
// newAuthenticationCacheInvalidator validates them once and authenticationCacheInvalidator keeps them in deps.
type authenticationCacheInvalidatorDependencies struct {
	Cache       authenticationCache
	Diagnostics authenticationDiagnostics
}

type authenticationCacheInvalidator struct {
	deps authenticationCacheInvalidatorDependencies
}

func newAuthenticationCacheInvalidator(deps authenticationCacheInvalidatorDependencies) (*authenticationCacheInvalidator, error) {
	if deps.Cache == nil {
		return nil, errors.New("authentication cache is required")
	}
	if deps.Diagnostics == nil {
		return nil, errors.New("authentication diagnostics are required")
	}
	return &authenticationCacheInvalidator{deps: deps}, nil
}

func (i *authenticationCacheInvalidator) InvalidateAccessCredentials(
	ctx context.Context,
	hashes []string,
) {
	for _, hash := range hashes {
		if err := i.deps.Cache.Delete(ctx, authenticationCachePrefix+hash); err != nil {
			i.deps.Diagnostics.WarnContext(
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
		if err := i.deps.Cache.Delete(ctx, activityCachePrefix+sessionID); err != nil {
			i.deps.Diagnostics.WarnContext(
				ctx,
				"session activity cache delete failed",
				errors.New("cache operation failed"),
			)
		}
	}
}

var _ realtime.AuthenticationInvalidator = (*authenticationCacheInvalidator)(nil)
