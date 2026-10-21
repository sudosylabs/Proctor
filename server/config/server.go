// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package config

import (
	"net/url"
	"time"
)

type Server struct {
	ListenAddress     string    `json:"ListenAddress"`
	PublicURL         string    `json:"PublicURL"`
	WebappDirectory   string    `json:"WebappDirectory"`
	TLS               ServerTLS `json:"TLS"`
	ReadHeaderTimeout Duration  `json:"ReadHeaderTimeout"`
	ReadTimeout       Duration  `json:"ReadTimeout"`
	WriteTimeout      Duration  `json:"WriteTimeout"`
	IdleTimeout       Duration  `json:"IdleTimeout"`
	ShutdownTimeout   Duration  `json:"ShutdownTimeout"`
	MaxHeaderBytes    int       `json:"MaxHeaderBytes"`
	MaxBodyBytes      int64     `json:"MaxBodyBytes"`
	// TrustedProxies lists the reverse proxies, as IP addresses or CIDR
	// prefixes, whose X-Forwarded-For and X-Request-ID headers the server
	// accepts. Empty trusts no proxy: the connection address is the client.
	TrustedProxies []string `json:"TrustedProxies"`
}

func validatePublicURL(raw string, add func(string, string)) {
	parsed, err := url.Parse(raw)
	if err != nil {
		add("server.public_url", "must be an absolute HTTP or HTTPS URL")
		return
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		add("server.public_url", "scheme must be http or https")
	}
	if parsed.Host == "" {
		add("server.public_url", "host is required")
	}
	if parsed.User != nil {
		add("server.public_url", "user information is forbidden")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		add("server.public_url", "query and fragment are forbidden")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		add("server.public_url", "path is unsupported; host Proctor at the origin root")
	}
}

// validateServerLimits checks the client listener's timeouts, header and body
// bounds, and trusted proxies.
func validateServerLimits(server Server, add func(string, string)) {
	durations := []struct {
		field string
		value time.Duration
	}{
		{"server.read_header_timeout", server.ReadHeaderTimeout.Duration},
		{"server.read_timeout", server.ReadTimeout.Duration},
		{"server.write_timeout", server.WriteTimeout.Duration},
		{"server.idle_timeout", server.IdleTimeout.Duration},
		{"server.shutdown_timeout", server.ShutdownTimeout.Duration},
	}
	for _, item := range durations {
		if item.value <= 0 {
			add(item.field, "must be greater than zero")
		}
	}
	if server.MaxHeaderBytes < 1024 || server.MaxHeaderBytes > 16<<20 {
		add("server.max_header_bytes", "must be between 1024 and 16777216")
	}
	if server.MaxBodyBytes < 1024 || server.MaxBodyBytes > 100<<20 {
		add("server.max_body_bytes", "must be between 1024 and 104857600")
	}
	if _, err := ParseTrustedProxies(server.TrustedProxies); err != nil {
		add("server.trusted_proxies", err.Error())
	}
}
