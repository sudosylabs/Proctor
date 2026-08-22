// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestServerTLSModesValidateCoherentDeploymentShapes(t *testing.T) {
	t.Parallel()

	static := Default()
	static.Authentication.Bootstrap.DevelopmentMode = false
	static.Server.PublicURL = "https://proctor.example.edu"
	static.Server.TLS.Mode = "static"
	static.Server.TLS.CertificateFile = "/etc/proctor/tls/certificate.pem"
	static.Server.TLS.PrivateKeyFile = "/etc/proctor/tls/private-key.pem"
	static.Server.TLS.ForwardHTTPToHTTPS = true
	static.Server.TLS.HTTPListenAddress = ":80"
	if err := static.Validate(); err != nil {
		t.Fatalf("static TLS configuration: %v", err)
	}

	letsEncrypt := Default()
	letsEncrypt.Authentication.Bootstrap.DevelopmentMode = false
	letsEncrypt.Server.PublicURL = "https://proctor.example.edu"
	letsEncrypt.Server.TLS.Mode = "lets_encrypt"
	letsEncrypt.Server.TLS.LetsEncrypt.Email = "operator@example.edu"
	letsEncrypt.Server.TLS.LetsEncrypt.CacheDirectory = "/var/lib/proctor/acme"
	letsEncrypt.Server.TLS.ForwardHTTPToHTTPS = true
	letsEncrypt.Server.TLS.HTTPListenAddress = ":80"
	if err := letsEncrypt.Validate(); err != nil {
		t.Fatalf("Let's Encrypt configuration: %v", err)
	}
}

func TestServerTLSValidationFailsClosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		configure func(*Config)
		fields    []string
	}{
		{
			name: "unknown mode",
			configure: func(cfg *Config) {
				cfg.Server.TLS.Mode = "automatic"
			},
			fields: []string{"server.tls.mode"},
		},
		{
			name: "static files are required",
			configure: func(cfg *Config) {
				cfg.Server.PublicURL = "https://proctor.example.edu"
				cfg.Server.TLS.Mode = "static"
			},
			fields: []string{"server.tls.certificate_file", "server.tls.private_key_file"},
		},
		{
			name: "built-in TLS requires HTTPS public URL",
			configure: func(cfg *Config) {
				cfg.Server.TLS.Mode = "static"
				cfg.Server.TLS.CertificateFile = "certificate.pem"
				cfg.Server.TLS.PrivateKeyFile = "private-key.pem"
			},
			fields: []string{"server.public_url"},
		},
		{
			name: "Let's Encrypt requires DNS and HTTP challenge listener",
			configure: func(cfg *Config) {
				cfg.Server.PublicURL = "https://127.0.0.1"
				cfg.Server.TLS.Mode = "lets_encrypt"
			},
			fields: []string{"server.public_url", "server.tls.forward_http_to_https"},
		},
		{
			name: "Let's Encrypt is not cluster coordinated",
			configure: func(cfg *Config) {
				cfg.Server.PublicURL = "https://proctor.example.edu"
				cfg.Server.TLS.Mode = "lets_encrypt"
				cfg.Server.TLS.ForwardHTTPToHTTPS = true
				cfg.Cluster.Backend = "memberlist"
				cfg.Cluster.Memberlist.EncryptionKey = base64.StdEncoding.EncodeToString(make([]byte, 32))
				cfg.Cache.Backend = "redis"
				cfg.VFS.Backend = "s3"
				cfg.VFS.S3.Endpoint = "127.0.0.1:9000"
				cfg.VFS.S3.Bucket = "proctor"
			},
			fields: []string{"server.tls.mode"},
		},
		{
			name: "forwarding requires built-in TLS",
			configure: func(cfg *Config) {
				cfg.Server.TLS.ForwardHTTPToHTTPS = true
			},
			fields: []string{"server.tls.forward_http_to_https"},
		},
		{
			name: "forwarding cannot reuse HTTPS listener",
			configure: func(cfg *Config) {
				cfg.Server.PublicURL = "https://localhost:8065"
				cfg.Server.TLS.Mode = "static"
				cfg.Server.TLS.CertificateFile = "certificate.pem"
				cfg.Server.TLS.PrivateKeyFile = "private-key.pem"
				cfg.Server.TLS.ForwardHTTPToHTTPS = true
				cfg.Server.TLS.HTTPListenAddress = cfg.Server.ListenAddress
			},
			fields: []string{"server.tls.http_listen_address"},
		},
		{
			name: "forwarding requires a stable port",
			configure: func(cfg *Config) {
				cfg.Server.PublicURL = "https://localhost:8065"
				cfg.Server.TLS.Mode = ServerTLSModeStatic
				cfg.Server.TLS.CertificateFile = "certificate.pem"
				cfg.Server.TLS.PrivateKeyFile = "private-key.pem"
				cfg.Server.TLS.ForwardHTTPToHTTPS = true
				cfg.Server.TLS.HTTPListenAddress = ":0"
			},
			fields: []string{"server.tls.http_listen_address"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cfg := Default()
			test.configure(&cfg)
			err := cfg.Validate()
			if err == nil {
				t.Fatal("Validate() succeeded")
			}
			for _, field := range test.fields {
				if !strings.Contains(err.Error(), field+":") {
					t.Fatalf("Validate() error = %v, want field %s", err, field)
				}
			}
		})
	}
}

func TestLetsEncryptHostnameValidationAndCanonicalization(t *testing.T) {
	t.Parallel()

	valid := Default().Server
	valid.PublicURL = "https://PROCTOR.Example.EDU.:443"
	if hostname := valid.LetsEncryptHostname(); hostname != "proctor.example.edu" {
		t.Fatalf("LetsEncryptHostname() = %q", hostname)
	}
	valid.PublicURL = "https://xn--bcher-kva.example"
	if hostname := valid.LetsEncryptHostname(); hostname != "xn--bcher-kva.example" {
		t.Fatalf("LetsEncryptHostname() for valid A-label = %q", hostname)
	}

	invalidHostnames := []string{
		"localhost",
		"127.0.0.1",
		"single-label",
		"bad..example.edu",
		"-bad.example.edu",
		"bad-.example.edu",
		"bad_name.example.edu",
		strings.Repeat("a", 64) + ".example.edu",
		strings.Repeat("a.", 126) + "example.edu",
		"éxample.edu",
		"xn--a.example.edu",
		"xn--.example.edu",
	}
	for _, hostname := range invalidHostnames {
		hostname := hostname
		t.Run(hostname, func(t *testing.T) {
			t.Parallel()
			cfg := Default()
			cfg.Server.PublicURL = "https://" + hostname
			cfg.Server.TLS.Mode = ServerTLSModeLetsEncrypt
			cfg.Server.TLS.ForwardHTTPToHTTPS = true
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "server.public_url:") {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}
