// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

// Package weborigin serializes browser origins once for every transport that
// compares a request Origin with the configured public URL.
package weborigin

import (
	"errors"
	"net/url"
	"strings"
)

// FromPublicURL returns the serialized origin of an HTTP or HTTPS public URL.
func FromPublicURL(publicURL string) (string, error) {
	parsed, err := url.Parse(publicURL)
	if err != nil || parsed.User != nil || parsed.Opaque != "" || parsed.Host == "" ||
		(parsed.Scheme != "https" && parsed.Scheme != "http") {
		return "", errors.New("public URL must have an HTTP or HTTPS origin")
	}
	return serialize(parsed), nil
}

// FromHeader returns the serialized origin carried by one Origin header value,
// or false when the value is not exactly a scheme, host, and optional port.
func FromHeader(value string) (string, bool) {
	if value == "" || strings.TrimSpace(value) != value {
		return "", false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.User != nil || parsed.Opaque != "" || parsed.Host == "" || parsed.Path != "" ||
		parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" ||
		(parsed.Scheme != "https" && parsed.Scheme != "http") {
		return "", false
	}
	return serialize(parsed), true
}

// serialize lowercases the scheme and host and omits the scheme's default
// port, following the ASCII serialization of an origin.
func serialize(parsed *url.URL) string {
	scheme := strings.ToLower(parsed.Scheme)
	host := strings.ToLower(parsed.Host)
	if scheme == "https" && parsed.Port() == "443" || scheme == "http" && parsed.Port() == "80" {
		host = strings.TrimSuffix(host, ":"+parsed.Port())
	}
	return scheme + "://" + host
}
