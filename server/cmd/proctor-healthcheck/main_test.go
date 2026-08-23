// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCheckReadiness(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	if err := checkReadiness(server.URL, time.Second, "", ""); err != nil {
		t.Fatal(err)
	}
}

func TestCheckReadinessRejectsRedirects(t *testing.T) {
	t.Parallel()

	destination := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	}))
	defer destination.Close()
	server := httptest.NewServer(http.RedirectHandler(destination.URL, http.StatusPermanentRedirect))
	defer server.Close()
	if err := checkReadiness(server.URL, time.Second, "", ""); err == nil || !strings.Contains(err.Error(), "308") {
		t.Fatalf("redirect error = %v", err)
	}
}

func TestCheckReadinessSupportsPrivateTLSIdentity(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	certificate, err := x509.ParseCertificate(server.Certificate().Raw)
	if err != nil {
		t.Fatal(err)
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw})
	caFile := filepath.Join(t.TempDir(), "issuer.pem")
	if err := os.WriteFile(caFile, certificatePEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := checkReadiness(server.URL, time.Second, "example.com", caFile); err != nil {
		t.Fatal(err)
	}
}

func TestCertificatePoolRejectsInvalidPEM(t *testing.T) {
	t.Parallel()

	caFile := filepath.Join(t.TempDir(), "issuer.pem")
	if err := os.WriteFile(caFile, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := certificatePool(caFile); err == nil {
		t.Fatal("expected invalid certificate authority to fail")
	}
}
