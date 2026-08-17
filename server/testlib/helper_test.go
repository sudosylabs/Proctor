// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package testlib

import (
	"testing"

	"github.com/sudosylabs/proctor/server/config"
)

func TestSetupUsesExplicitBootstrapModeWithANonLoopbackPublicURL(t *testing.T) {
	helper := Setup(t, WithConfig(func(cfg *config.Config) {
		cfg.Server.PublicURL = "https://proctor.example.test"
	}))

	effective := helper.ConfigStore.Get()
	if effective.Authentication.Bootstrap.DevelopmentMode {
		t.Fatal("test graph kept loopback-only bootstrap development mode with an external public URL")
	}
	if effective.Authentication.Bootstrap.Secret != BootstrapSecret {
		t.Fatal("test graph did not retain its explicit deployment-owned bootstrap secret")
	}
}
