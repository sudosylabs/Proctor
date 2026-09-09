// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package server

import (
	"strings"
	"testing"
)

func TestParseServiceEnvironment(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		value string
		want  ServiceEnvironment
	}{
		{"", defaultServiceEnvironment}, {" ", defaultServiceEnvironment},
		{"production", ServiceEnvironmentProduction}, {"dev", ServiceEnvironmentDev},
		{"test", ServiceEnvironmentTest}, {"  PrOdUcTiOn ", ServiceEnvironmentProduction},
		{" DEV ", ServiceEnvironmentDev},
	} {
		got, err := parseServiceEnvironment(test.value)
		if err != nil || got != test.want {
			t.Fatalf("parse %q = %q, %v", test.value, got, err)
		}
	}
	for _, value := range []string{"development", "prod", "staging", "produciton", "dev/production"} {
		if _, err := parseServiceEnvironment(value); err == nil {
			t.Fatalf("accepted unknown environment %q", value)
		}
	}
}

func TestNewRejectsInvalidEnvironmentBeforeOpeningConfiguration(t *testing.T) {
	t.Setenv("PROCTOR_SERVICE_ENVIRONMENT", "misspelled-production")
	if _, err := New(t.Context(), WithConfigPath("nonexistent.json")); err == nil || !strings.Contains(err.Error(), "PROCTOR_SERVICE_ENVIRONMENT") {
		t.Fatalf("invalid environment did not stop construction first: %v", err)
	}
}
