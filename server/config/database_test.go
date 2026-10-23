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

func TestDatabaseSlowOperationThresholdIsDisabledByDefaultAndNeverNegative(t *testing.T) {
	t.Parallel()

	cfg := Default()
	require.Zero(t, cfg.Database.SlowOperationThreshold.Duration, "default slow-operation threshold")

	cfg.Database.SlowOperationThreshold = Duration{Duration: 250 * time.Millisecond}
	require.NoError(t, cfg.Validate())

	cfg.Database.SlowOperationThreshold = Duration{Duration: -time.Millisecond}
	err := cfg.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "database.slow_operation_threshold")
}
