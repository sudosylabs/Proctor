// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"

	"github.com/sudosylabs/proctor/server/model"
)

// Can exposes the unaudited Access Control predicate to external integration
// tests; production callers authorize through their use cases.
func (a *App) Can(ctx context.Context, principal model.Principal, action model.Action, resource model.Resource) (bool, error) {
	return a.realtimeAdmissionService.authorization.Can(ctx, principal, action, resource)
}

// Authorize exposes the audited Access Control decision to external
// integration tests.
func (a *App) Authorize(ctx context.Context, invocation Invocation, action model.Action, resource model.Resource) error {
	return a.realtimeAdmissionService.authorization.Authorize(ctx, invocation, action, resource)
}
