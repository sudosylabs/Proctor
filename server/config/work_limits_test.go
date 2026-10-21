// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package config

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkLimitsDefaultIndependentlyAndRejectUnboundedConfiguration(t *testing.T) {
	for _, test := range []struct {
		name  string
		field func(*Config) *int
	}{
		{"authentication.password.maximum_concurrent_operations", func(c *Config) *int { return &c.Authentication.Password.MaximumConcurrentOperations }},
		{"file_content.maximum_concurrent_operations", func(c *Config) *int { return &c.FileContent.MaximumConcurrentOperations }},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := Default()
			require.Equal(t, 2, *test.field(&cfg), "default work limit must admit two operations")
			for _, invalid := range []int{0, -1} {
				*test.field(&cfg) = invalid
				err := cfg.Validate()
				require.Error(t, err, "validation of limit %d", invalid)
				require.Contains(t, err.Error(), test.name, "validation of limit %d", invalid)
			}
			*test.field(&cfg) = 1
			require.NoError(t, cfg.Validate())
		})
	}
}

func TestExistingConfigurationReceivesDefaultWorkLimits(t *testing.T) {
	configuration, err := NewStore(context.Background(), NewMemoryStore([]byte(`{"Version":1}`)), StoreOptions{LookupEnv: noEnvironment})
	require.NoError(t, err)
	t.Cleanup(func() { _ = configuration.Close() })
	current := configuration.Get()
	require.Equal(t, 2, current.Authentication.Password.MaximumConcurrentOperations, "omitted limits did not receive bounded defaults")
	require.Equal(t, 2, current.FileContent.MaximumConcurrentOperations, "omitted limits did not receive bounded defaults")
}
