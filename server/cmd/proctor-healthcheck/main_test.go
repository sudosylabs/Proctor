// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package main

import (
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCheckReadiness(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	require.NoError(t, checkReadiness(server.URL, time.Second, "", ""))
}

func TestCheckReadinessRejectsRedirects(t *testing.T) {
	t.Parallel()

	destination := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	}))
	defer destination.Close()
	server := httptest.NewServer(http.RedirectHandler(destination.URL, http.StatusPermanentRedirect))
	defer server.Close()
	err := checkReadiness(server.URL, time.Second, "", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "308")
}

func TestCheckReadinessSupportsPrivateTLSIdentity(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	certificate, err := x509.ParseCertificate(server.Certificate().Raw)
	require.NoError(t, err)
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw})
	caFile := filepath.Join(t.TempDir(), "issuer.pem")
	require.NoError(t, os.WriteFile(caFile, certificatePEM, 0o600))
	require.NoError(t, checkReadiness(server.URL, time.Second, "example.com", caFile))
}

func TestCertificatePoolRejectsInvalidPEM(t *testing.T) {
	t.Parallel()

	caFile := filepath.Join(t.TempDir(), "issuer.pem")
	require.NoError(t, os.WriteFile(caFile, []byte("not a certificate"), 0o600))
	_, err := certificatePool(caFile)
	require.Error(t, err, "expected invalid certificate authority to fail")
}
