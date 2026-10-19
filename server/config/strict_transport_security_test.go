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
)

func TestStrictTransportSecurityRequiresLocalTLS(t *testing.T) {
	t.Parallel()

	server := Default().Server
	if got := server.StrictTransportSecurity(); got != "" {
		t.Fatalf("disabled TLS Strict-Transport-Security = %q, want none", got)
	}
	server.TLS.Mode = ServerTLSModeStatic
	if got := server.StrictTransportSecurity(); got != "max-age=31536000" {
		t.Fatalf("default Strict-Transport-Security = %q", got)
	}
	server.TLS.Mode = ServerTLSModeLetsEncrypt
	server.TLS.StrictTransportSecurityMaxAge = Duration{Duration: time.Hour}
	if got := server.StrictTransportSecurity(); got != "max-age=3600" {
		t.Fatalf("configured Strict-Transport-Security = %q", got)
	}
	server.TLS.StrictTransportSecurityMaxAge = Duration{}
	if got := server.StrictTransportSecurity(); got != "" {
		t.Fatalf("zero max-age Strict-Transport-Security = %q, want none", got)
	}
}

func TestValidateBoundsStrictTransportSecurityMaxAge(t *testing.T) {
	t.Parallel()

	for _, maxAge := range []time.Duration{-time.Second, 2*365*24*time.Hour + time.Second, 1500 * time.Millisecond} {
		cfg := Default()
		cfg.Server.TLS.StrictTransportSecurityMaxAge = Duration{Duration: maxAge}
		if err := cfg.Validate(); err == nil {
			t.Fatalf("Validate() accepted max-age %s", maxAge)
		}
	}
	cfg := Default()
	cfg.Server.TLS.StrictTransportSecurityMaxAge = Duration{}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() rejected disabled HSTS: %v", err)
	}
}
