// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package config

import (
	"context"
	"strings"
	"testing"
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
			if *test.field(&cfg) != 2 {
				t.Fatal("default work limit must admit two operations")
			}
			for _, invalid := range []int{0, -1} {
				*test.field(&cfg) = invalid
				if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), test.name) {
					t.Fatalf("validation of limit %d = %v", invalid, err)
				}
			}
			*test.field(&cfg) = 1
			if err := cfg.Validate(); err != nil {
				t.Fatalf("one-operation policy rejected: %v", err)
			}
		})
	}
}

func TestExistingConfigurationReceivesDefaultWorkLimits(t *testing.T) {
	configuration, err := NewStore(context.Background(), NewMemoryStore([]byte(`{"Version":1}`)), StoreOptions{LookupEnv: noEnvironment})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = configuration.Close() })
	current := configuration.Get()
	if current.Authentication.Password.MaximumConcurrentOperations != 2 || current.FileContent.MaximumConcurrentOperations != 2 {
		t.Fatal("omitted limits did not receive bounded defaults")
	}
}
