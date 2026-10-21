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

func TestLoginAccountFailureDelayIsBounded(t *testing.T) {
	t.Parallel()

	require.NoError(t, Default().Validate())
	for _, failures := range []int{0, 10_001} {
		cfg := Default()
		cfg.Authentication.LoginRateLimit.AccountFailuresBeforeDelay = failures
		err := cfg.Validate()
		require.ErrorContains(t, err, "authentication.login_rate_limit.account_failures_before_delay:", "AccountFailuresBeforeDelay=%d", failures)
	}
	for _, delay := range []time.Duration{0, 249 * time.Millisecond, 31 * time.Second} {
		cfg := Default()
		cfg.Authentication.LoginRateLimit.MaximumAccountFailureDelay = Duration{Duration: delay}
		err := cfg.Validate()
		require.ErrorContains(t, err, "authentication.login_rate_limit.maximum_account_failure_delay:", "MaximumAccountFailureDelay=%s", delay)
	}
}
