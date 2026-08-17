// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

// requireStrongRecentSession is the application-layer assurance guard for
// sensitive administrative operations. Transport declarations remain an
// early rejection only; every owning use case calls this guard itself.
func requireStrongRecentSession(
	principal model.Principal,
	now time.Time,
	recentAuthenticationTTL time.Duration,
) error {
	if principal.Validate() != nil || principal.CredentialType != model.CredentialSessionAccess {
		return invalidTokenAppError()
	}
	if !principal.HasStrongAuthentication() {
		return NewError("authentication.strong_required")
	}
	if !principal.IsRecentlyAuthenticated(now, recentAuthenticationTTL) {
		return NewError("authentication.reauthentication_required")
	}
	return nil
}
