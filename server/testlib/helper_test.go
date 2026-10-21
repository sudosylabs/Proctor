// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package testlib

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/config"
)

func TestSetupUsesExplicitBootstrapModeWithANonLoopbackPublicURL(t *testing.T) {
	helper := Setup(t, WithConfig(func(cfg *config.Config) {
		cfg.Server.PublicURL = "https://proctor.example.test"
	}))

	effective := helper.ConfigStore.Get()
	require.False(t, effective.Authentication.Bootstrap.DevelopmentMode, "test graph kept loopback-only bootstrap development mode with an external public URL")
	require.Equal(t, BootstrapSecret, effective.Authentication.Bootstrap.Secret, "test graph did not retain its explicit deployment-owned bootstrap secret")
}
