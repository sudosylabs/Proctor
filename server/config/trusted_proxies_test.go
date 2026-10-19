// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package config

import (
	"net/netip"
	"reflect"
	"testing"
)

func TestParseTrustedProxies(t *testing.T) {
	t.Parallel()

	got, err := ParseTrustedProxies([]string{"10.0.0.0/8", "192.0.2.7", "2001:db8::/32", "::ffff:198.51.100.0/120", "10.1.2.3/8"})
	if err != nil {
		t.Fatal(err)
	}
	want := []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("192.0.2.7/32"),
		netip.MustParsePrefix("2001:db8::/32"),
		netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("10.0.0.0/8"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseTrustedProxies() = %v, want %v", got, want)
	}

	for _, invalid := range []string{"", " 10.0.0.1", "proxy.example.edu", "10.0.0.0/33", "fe80::1%eth0", "10.0.0.1:443"} {
		if _, err := ParseTrustedProxies([]string{invalid}); err == nil {
			t.Fatalf("ParseTrustedProxies(%q) succeeded", invalid)
		}
	}
}

func TestValidateRejectsInvalidTrustedProxy(t *testing.T) {
	t.Parallel()

	cfg := Default()
	cfg.Server.TrustedProxies = []string{"not-an-address"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() accepted an invalid trusted proxy")
	}
}
