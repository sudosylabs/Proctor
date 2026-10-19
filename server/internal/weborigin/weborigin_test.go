// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package weborigin

import "testing"

func TestFromPublicURLSerializesOrigin(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"https://proctor.example":         "https://proctor.example",
		"https://Proctor.Example:443/":    "https://proctor.example",
		"HTTPS://PROCTOR.EXAMPLE":         "https://proctor.example",
		"http://localhost:80":             "http://localhost",
		"http://localhost:8065":           "http://localhost:8065",
		"https://proctor.example:8443":    "https://proctor.example:8443",
		"http://proctor.example:443":      "http://proctor.example:443",
		"https://[2001:DB8::1]:443/":      "https://[2001:db8::1]",
		"https://[2001:db8::1]:8443/path": "https://[2001:db8::1]:8443",
	}
	for publicURL, want := range tests {
		got, err := FromPublicURL(publicURL)
		if err != nil || got != want {
			t.Fatalf("FromPublicURL(%q) = %q, %v; want %q", publicURL, got, err, want)
		}
	}
	for _, invalid := range []string{"", "ftp://proctor.example", "https://user@proctor.example", "proctor.example", "mailto:a@b"} {
		if _, err := FromPublicURL(invalid); err == nil {
			t.Fatalf("FromPublicURL(%q) succeeded", invalid)
		}
	}
}

func TestFromHeaderAcceptsOnlyExactOrigins(t *testing.T) {
	t.Parallel()

	for value, want := range map[string]string{
		"https://proctor.example":     "https://proctor.example",
		"https://PROCTOR.example:443": "https://proctor.example",
		"http://localhost:8065":       "http://localhost:8065",
	} {
		if got, ok := FromHeader(value); !ok || got != want {
			t.Fatalf("FromHeader(%q) = %q, %t; want %q", value, got, ok, want)
		}
	}
	for _, invalid := range []string{"", "null", " https://proctor.example", "https://proctor.example/", "https://proctor.example?x", "https://proctor.example#f", "https://u@proctor.example", "chrome-extension://abc"} {
		if got, ok := FromHeader(invalid); ok {
			t.Fatalf("FromHeader(%q) = %q, want rejection", invalid, got)
		}
	}
}
