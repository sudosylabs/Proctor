// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEmailChangeUndoTTLIsBounded(t *testing.T) {
	t.Parallel()

	require.Equal(t, 72*time.Hour, Default().Authentication.AccountRecovery.EmailChangeUndoTTL.Duration)
	for _, ttl := range []time.Duration{time.Hour, 7 * 24 * time.Hour} {
		cfg := Default()
		cfg.Authentication.AccountRecovery.EmailChangeUndoTTL = Duration{Duration: ttl}
		require.NoError(t, cfg.Validate(), "EmailChangeUndoTTL=%s", ttl)
	}
	for _, ttl := range []time.Duration{time.Hour - time.Nanosecond, 7*24*time.Hour + time.Nanosecond} {
		cfg := Default()
		cfg.Authentication.AccountRecovery.EmailChangeUndoTTL = Duration{Duration: ttl}
		require.ErrorContains(t, cfg.Validate(), "authentication.account_recovery.email_change_undo_ttl: must be between 1h and 168h", "EmailChangeUndoTTL=%s", ttl)
	}
}
