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

func TestWebappConfigurationRequiresAPathAndRootPublicURL(t *testing.T) {
	t.Parallel()

	for _, mutate := range []func(*Config){
		func(cfg *Config) { cfg.Server.WebappDirectory = "" },
		func(cfg *Config) { cfg.Server.WebappDirectory = "bad\x00path" },
	} {
		cfg := Default()
		mutate(&cfg)
		err := cfg.Validate()
		require.Error(t, err, "Validate() error")
		require.Contains(t, err.Error(), "server.webapp_directory", "Validate() error")
	}

	cfg := Default()
	cfg.Server.PublicURL = "https://proctor.example.edu/subpath"
	err := cfg.Validate()
	require.Error(t, err, "Validate() error")
	require.Contains(t, err.Error(), "server.public_url", "Validate() error")
}
