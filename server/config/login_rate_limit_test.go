// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package config

import (
	"strings"
	"testing"
)

func TestLoginIdentityAttemptLimitIsBoundedByTheOtherLoginLimits(t *testing.T) {
	t.Parallel()

	if err := Default().Validate(); err != nil {
		t.Fatalf("default configuration: %v", err)
	}
	for _, maximum := range []int{0, 9, 1_000_001} {
		cfg := Default()
		cfg.Authentication.LoginRateLimit.MaximumIdentityAttempts = maximum
		err := cfg.Validate()
		if err == nil || !strings.Contains(err.Error(), "authentication.login_rate_limit.maximum_identity_attempts:") {
			t.Fatalf("MaximumIdentityAttempts=%d: Validate() = %v", maximum, err)
		}
	}
	cfg := Default()
	cfg.Authentication.LoginRateLimit.MaximumIdentityAttempts = cfg.Authentication.LoginRateLimit.MaximumAttempts
	if err := cfg.Validate(); err != nil {
		t.Fatalf("identity limit equal to the account-and-source limit: %v", err)
	}
}
