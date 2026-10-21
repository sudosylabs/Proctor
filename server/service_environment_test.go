// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package server

import (
	"testing"

	"github.com/stretchr/testify/require"
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
		t.Run(test.value, func(t *testing.T) {
			got, err := parseServiceEnvironment(test.value)
			require.NoError(t, err, "parse %q", test.value)
			require.Equal(t, test.want, got, "parse %q", test.value)
		})
	}
	for _, value := range []string{"development", "prod", "staging", "produciton", "dev/production"} {
		_, err := parseServiceEnvironment(value)
		require.Error(t, err, "accepted unknown environment %q", value)
	}
}

func TestNewRejectsInvalidEnvironmentBeforeOpeningConfiguration(t *testing.T) {
	t.Setenv("PROCTOR_SERVICE_ENVIRONMENT", "misspelled-production")
	_, err := New(t.Context(), WithConfigPath("nonexistent.json"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "PROCTOR_SERVICE_ENVIRONMENT")
}
