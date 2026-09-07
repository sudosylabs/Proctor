// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// requireStrongRecentSessionAt reads assurance from the current Session. The
// caller must hold the Session lock, verify its current credential, and read at
// after acquiring the mutation's potentially waiting locks.
func requireStrongRecentSessionAt(ctx context.Context, executor sqlxExecutor, sessionID model.SessionID, at time.Time, ttl time.Duration) error {
	if ttl <= 0 {
		return store.NewErrConflict("authorization", "assurance", nil)
	}
	var strong bool
	err := executor.Get(ctx, &strong, `SELECT authentication_strength='multi_factor' AND
		GREATEST(authenticated_at,reauthenticated_at,mfa_completed_at)<=? AND
		GREATEST(authenticated_at,reauthenticated_at,mfa_completed_at)>=? FROM sessions WHERE id=?`,
		at, at.Add(-ttl), sessionID.String())
	if err != nil {
		return err
	}
	if !strong {
		return store.NewErrConflict("authorization", "assurance", nil)
	}
	return nil
}
