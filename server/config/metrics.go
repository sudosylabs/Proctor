// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package config

import (
	"net"
	"strings"
	"time"
)

// Metrics configures the node-local Prometheus scrape listener. A listener
// reachable beyond loopback must use both TLS and bearer authentication.
type Metrics struct {
	Enabled                  bool       `json:"Enabled"`
	ListenAddress            string     `json:"ListenAddress"`
	BearerToken              string     `json:"BearerToken"`
	TLS                      MetricsTLS `json:"TLS"`
	ReadHeaderTimeout        Duration   `json:"ReadHeaderTimeout"`
	ReadTimeout              Duration   `json:"ReadTimeout"`
	WriteTimeout             Duration   `json:"WriteTimeout"`
	IdleTimeout              Duration   `json:"IdleTimeout"`
	ShutdownTimeout          Duration   `json:"ShutdownTimeout"`
	MaximumConcurrentScrapes int        `json:"MaximumConcurrentScrapes"`
}

type MetricsTLS struct {
	CertificateFile string `json:"CertificateFile"`
	PrivateKeyFile  string `json:"PrivateKeyFile"`
}

func validateMetrics(metrics Metrics, server Server, add func(string, string)) {
	if !validHostPort(metrics.ListenAddress) {
		add("metrics.listen_address", "must be a host:port TCP address")
	} else {
		metricsHost, _, _ := net.SplitHostPort(metrics.ListenAddress)
		if metrics.Enabled && listenAddressesConflict(metrics.ListenAddress, server.ListenAddress) {
			add("metrics.listen_address", "must differ from server.listen_address")
		}
		if metrics.Enabled && server.TLS.ForwardHTTPToHTTPS &&
			listenAddressesConflict(metrics.ListenAddress, server.TLS.HTTPListenAddress) {
			add("metrics.listen_address", "must differ from server.tls.http_listen_address")
		}
		ip := net.ParseIP(metricsHost)
		loopback := strings.EqualFold(metricsHost, "localhost") || (ip != nil && ip.IsLoopback())
		hasCertificate := strings.TrimSpace(metrics.TLS.CertificateFile) != ""
		hasKey := strings.TrimSpace(metrics.TLS.PrivateKeyFile) != ""
		if metrics.Enabled && hasCertificate != hasKey {
			add("metrics.tls", "certificate_file and private_key_file must be configured together")
		}
		if metrics.Enabled && !loopback && (!hasCertificate || !hasKey || len(metrics.BearerToken) < 32) {
			add("metrics.listen_address", "non-loopback metrics require TLS and a bearer token of at least 32 bytes")
		}
	}
	if metrics.BearerToken != "" && (len(metrics.BearerToken) < 32 || len(metrics.BearerToken) > 512) {
		add("metrics.bearer_token", "must contain between 32 and 512 bytes when configured")
	}
	for _, timeout := range []struct {
		field string
		value time.Duration
	}{
		{"metrics.read_header_timeout", metrics.ReadHeaderTimeout.Duration},
		{"metrics.read_timeout", metrics.ReadTimeout.Duration},
		{"metrics.write_timeout", metrics.WriteTimeout.Duration},
		{"metrics.idle_timeout", metrics.IdleTimeout.Duration},
		{"metrics.shutdown_timeout", metrics.ShutdownTimeout.Duration},
	} {
		if timeout.value <= 0 {
			add(timeout.field, "must be greater than zero")
		}
	}
	if metrics.MaximumConcurrentScrapes < 1 || metrics.MaximumConcurrentScrapes > 32 {
		add("metrics.maximum_concurrent_scrapes", "must be between 1 and 32")
	}
}

func listenAddressesConflict(first, second string) bool {
	firstHost, firstPort, firstErr := net.SplitHostPort(first)
	secondHost, secondPort, secondErr := net.SplitHostPort(second)
	if firstErr != nil || secondErr != nil || firstPort != secondPort {
		return false
	}
	firstHost = strings.TrimSuffix(strings.ToLower(firstHost), ".")
	secondHost = strings.TrimSuffix(strings.ToLower(secondHost), ".")
	if firstHost == secondHost {
		return true
	}
	firstIP, secondIP := net.ParseIP(firstHost), net.ParseIP(secondHost)
	if (firstIP != nil && firstIP.IsUnspecified()) || (secondIP != nil && secondIP.IsUnspecified()) ||
		firstHost == "" || secondHost == "" {
		return true
	}
	firstLoopback := firstHost == "localhost" || (firstIP != nil && firstIP.IsLoopback())
	secondLoopback := secondHost == "localhost" || (secondIP != nil && secondIP.IsLoopback())
	return firstLoopback && secondLoopback
}
