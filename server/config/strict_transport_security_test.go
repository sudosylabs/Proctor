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

func TestStrictTransportSecurityRequiresLocalTLS(t *testing.T) {
	t.Parallel()

	server := Default().Server
	require.Empty(t, server.StrictTransportSecurity(), "disabled TLS Strict-Transport-Security")
	server.TLS.Mode = ServerTLSModeStatic
	require.Equal(t, "max-age=31536000", server.StrictTransportSecurity())
	server.TLS.Mode = ServerTLSModeLetsEncrypt
	server.TLS.StrictTransportSecurityMaxAge = Duration{Duration: time.Hour}
	require.Equal(t, "max-age=3600", server.StrictTransportSecurity())
	server.TLS.StrictTransportSecurityMaxAge = Duration{}
	require.Empty(t, server.StrictTransportSecurity(), "zero max-age Strict-Transport-Security")
}

func TestValidateBoundsStrictTransportSecurityMaxAge(t *testing.T) {
	t.Parallel()

	for _, maxAge := range []time.Duration{-time.Second, 2*365*24*time.Hour + time.Second, 1500 * time.Millisecond} {
		cfg := Default()
		cfg.Server.TLS.StrictTransportSecurityMaxAge = Duration{Duration: maxAge}
		require.Error(t, cfg.Validate(), "Validate() accepted max-age %s", maxAge)
	}
	cfg := Default()
	cfg.Server.TLS.StrictTransportSecurityMaxAge = Duration{}
	require.NoError(t, cfg.Validate())
}
