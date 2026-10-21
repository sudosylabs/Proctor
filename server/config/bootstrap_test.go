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

func TestBootstrapProtectionAcceptsStrongExplicitSecret(t *testing.T) {
	t.Parallel()

	cfg := Default()
	cfg.Authentication.Bootstrap.DevelopmentMode = false
	cfg.Server.ListenAddress = "0.0.0.0:8065"
	cfg.Server.PublicURL = "https://proctor.example.edu"
	cfg.Authentication.Bootstrap.Secret = "operator-provided-bootstrap-secret-32-bytes"
	require.NoError(t, cfg.Validate())
}

func TestBootstrapProtectionRejectsWeakOrNonLoopbackDevelopmentConfiguration(t *testing.T) {
	t.Parallel()

	cfg := Default()
	cfg.Authentication.Bootstrap.Secret = "too-short"
	err := cfg.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "between 32 and 512 bytes")

	cfg = Default()
	cfg.Authentication.Bootstrap.Secret = "operator-provided\tbootstrap-secret-32-bytes"
	err = cfg.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "control characters")

	cfg = Default()
	cfg.Authentication.Bootstrap.DevelopmentMode = true
	cfg.Authentication.Bootstrap.Secret = "operator-provided-bootstrap-secret-32-bytes"
	cfg.Server.ListenAddress = "192.0.2.1:8065"
	cfg.Server.PublicURL = "https://proctor.example.edu"
	err = cfg.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "development_mode")
}

func TestBootstrapSecretIsRedacted(t *testing.T) {
	t.Parallel()

	cfg := Default()
	cfg.Authentication.Bootstrap.Secret = "operator-provided-bootstrap-secret-32-bytes"
	data, err := cfg.RedactedJSON()
	require.NoError(t, err)
	require.NotContains(t, string(data), cfg.Authentication.Bootstrap.Secret)
	require.Contains(t, string(data), `"Secret": "[redacted]"`)
}
