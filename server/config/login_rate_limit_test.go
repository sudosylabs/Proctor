// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoginIdentityAttemptLimitIsBoundedByTheOtherLoginLimits(t *testing.T) {
	t.Parallel()

	require.NoError(t, Default().Validate())
	for _, maximum := range []int{0, 9, 1_000_001} {
		cfg := Default()
		cfg.Authentication.LoginRateLimit.MaximumIdentityAttempts = maximum
		err := cfg.Validate()
		require.Error(t, err, "MaximumIdentityAttempts=%d: Validate()", maximum)
		require.Contains(t, err.Error(), "authentication.login_rate_limit.maximum_identity_attempts:", "MaximumIdentityAttempts=%d: Validate()", maximum)
	}
	cfg := Default()
	cfg.Authentication.LoginRateLimit.MaximumIdentityAttempts = cfg.Authentication.LoginRateLimit.MaximumAttempts
	require.NoError(t, cfg.Validate())
}
