// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package config

import (
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/idna"
)

type ServerTLSMode string

const (
	ServerTLSModeDisabled    ServerTLSMode = "disabled"
	ServerTLSModeStatic      ServerTLSMode = "static"
	ServerTLSModeLetsEncrypt ServerTLSMode = "lets_encrypt"
)

// ServerTLS configures the client-facing listener. Mode is one of disabled,
// static, or lets_encrypt. HTTP forwarding is owned by the same runtime so
// ACME HTTP-01 challenges and redirects share one lifecycle.
type ServerTLS struct {
	Mode               ServerTLSMode     `json:"Mode"`
	CertificateFile    string            `json:"CertificateFile"`
	PrivateKeyFile     string            `json:"PrivateKeyFile"`
	LetsEncrypt        ServerLetsEncrypt `json:"LetsEncrypt"`
	ForwardHTTPToHTTPS bool              `json:"ForwardHTTPToHTTPS"`
	HTTPListenAddress  string            `json:"HTTPListenAddress"`
	// StrictTransportSecurityMaxAge is the HSTS max-age sent on every
	// response while this listener terminates TLS. Zero disables HSTS.
	StrictTransportSecurityMaxAge Duration `json:"StrictTransportSecurityMaxAge"`
}

// ServerLetsEncrypt configures ACME issuance. AcceptTermsOfService records
// the operator's explicit agreement to the Let's Encrypt Subscriber Agreement;
// lets_encrypt mode is invalid until it is true.
type ServerLetsEncrypt struct {
	Email                string `json:"Email"`
	CacheDirectory       string `json:"CacheDirectory"`
	AcceptTermsOfService bool   `json:"AcceptTermsOfService"`
}

// maximumStrictTransportSecurityMaxAge bounds HSTS to two years so a
// mistaken value cannot pin browsers to HTTPS indefinitely.
const maximumStrictTransportSecurityMaxAge = 2 * 365 * 24 * time.Hour

// StrictTransportSecurity returns the Strict-Transport-Security header value
// for responses served by this listener, or empty when the listener does not
// terminate TLS or HSTS is disabled. A reverse proxy that terminates TLS owns
// HSTS for its own origin.
func (s Server) StrictTransportSecurity() string {
	maxAge := s.TLS.StrictTransportSecurityMaxAge.Duration
	if s.TLS.Mode == ServerTLSModeDisabled || maxAge <= 0 {
		return ""
	}
	return "max-age=" + strconv.FormatInt(int64(maxAge/time.Second), 10)
}

func validateServerTLS(server Server, cluster Cluster, add func(string, string)) {
	settings := server.TLS
	parsedPublicURL, _ := url.Parse(server.PublicURL)

	for _, item := range []struct {
		field string
		value string
	}{
		{"server.tls.certificate_file", settings.CertificateFile},
		{"server.tls.private_key_file", settings.PrivateKeyFile},
		{"server.tls.lets_encrypt.cache_directory", settings.LetsEncrypt.CacheDirectory},
	} {
		if strings.ContainsRune(item.value, '\x00') {
			add(item.field, "contains a null byte")
		}
	}
	if maxAge := settings.StrictTransportSecurityMaxAge.Duration; maxAge < 0 || maxAge > maximumStrictTransportSecurityMaxAge || maxAge%time.Second != 0 {
		add("server.tls.strict_transport_security_max_age", "must be whole seconds between 0 and 17520h")
	}
	if settings.LetsEncrypt.Email != "" && !validMailbox(settings.LetsEncrypt.Email) {
		add("server.tls.lets_encrypt.email", "must be a plain email address")
	}

	switch settings.Mode {
	case ServerTLSModeDisabled:
	case ServerTLSModeStatic:
		if strings.TrimSpace(settings.CertificateFile) == "" {
			add("server.tls.certificate_file", "is required when TLS mode is static")
		}
		if strings.TrimSpace(settings.PrivateKeyFile) == "" {
			add("server.tls.private_key_file", "is required when TLS mode is static")
		}
	case ServerTLSModeLetsEncrypt:
		if strings.TrimSpace(settings.LetsEncrypt.CacheDirectory) == "" {
			add("server.tls.lets_encrypt.cache_directory", "is required when TLS mode is lets_encrypt")
		}
		if server.LetsEncryptHostname() == "" {
			add("server.public_url", "must use a fully qualified DNS hostname for Let's Encrypt")
		}
		if cluster.Backend == "memberlist" {
			add("server.tls.mode", "lets_encrypt is single-node only; terminate public TLS at the load balancer in a cluster")
		}
		if !settings.ForwardHTTPToHTTPS {
			add("server.tls.forward_http_to_https", "must be enabled for Let's Encrypt HTTP-01 challenges")
		}
		if !settings.LetsEncrypt.AcceptTermsOfService {
			add("server.tls.lets_encrypt.accept_terms_of_service", "must be true to accept the Let's Encrypt Subscriber Agreement")
		}
	default:
		add("server.tls.mode", "must be disabled, static, or lets_encrypt")
	}

	if settings.Mode != ServerTLSModeDisabled && (parsedPublicURL == nil || parsedPublicURL.Scheme != "https") {
		add("server.public_url", "scheme must be https when built-in TLS is enabled")
	}
	if settings.ForwardHTTPToHTTPS {
		if settings.Mode == ServerTLSModeDisabled {
			add("server.tls.forward_http_to_https", "requires static or lets_encrypt TLS mode")
		}
		validateTCPListenAddress("server.tls.http_listen_address", settings.HTTPListenAddress, false, add)
		if settings.HTTPListenAddress == server.ListenAddress {
			add("server.tls.http_listen_address", "must differ from server.listen_address")
		}
	}
}

// LetsEncryptHostname returns the canonical exact DNS hostname eligible for
// automatic certificate issuance, or an empty string when PublicURL cannot
// identify one safely.
func (s Server) LetsEncryptHostname() string {
	parsed, err := url.Parse(s.PublicURL)
	if err != nil {
		return ""
	}
	hostname := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if !validACMEDNSHostname(hostname) {
		return ""
	}
	return hostname
}

func validACMEDNSHostname(hostname string) bool {
	if len(hostname) == 0 || len(hostname) > 253 || hostname == "localhost" ||
		net.ParseIP(hostname) != nil || !strings.Contains(hostname, ".") {
		return false
	}
	canonical, err := idna.Lookup.ToASCII(hostname)
	if err != nil || canonical != hostname {
		return false
	}
	for _, label := range strings.Split(hostname, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') &&
				(character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
}
