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

func TestWebappConfigurationRequiresAPathAndRootPublicURL(t *testing.T) {
	t.Parallel()

	for _, mutate := range []func(*Config){
		func(cfg *Config) { cfg.Server.WebappDirectory = "" },
		func(cfg *Config) { cfg.Server.WebappDirectory = "bad\x00path" },
	} {
		cfg := Default()
		mutate(&cfg)
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "server.webapp_directory") {
			t.Fatalf("Validate() error = %v, want webapp directory error", err)
		}
	}

	cfg := Default()
	cfg.Server.PublicURL = "https://proctor.example.edu/subpath"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "server.public_url") {
		t.Fatalf("Validate() error = %v, want root PublicURL error", err)
	}
}
