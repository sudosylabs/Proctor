// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package metrics

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/config"
)

func TestMetricsListenerReloadsItsCertificateOnRequest(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	certificateFile, privateKeyFile := filepath.Join(directory, "metrics.pem"), filepath.Join(directory, "metrics-key.pem")
	writeMetricsTestCertificate(t, certificateFile, privateKeyFile, 1)
	settings := config.Default().Metrics
	settings.Enabled = true
	settings.ListenAddress = "127.0.0.1:0"
	settings.BearerToken = strings.Repeat("a", 32)
	settings.TLS.CertificateFile, settings.TLS.PrivateKeyFile = certificateFile, privateKeyFile
	module, err := New(settings, BuildInfo{Version: "test", Commit: "abc", GoVersion: "go-test"}, Sources{})
	require.NoError(t, err)
	require.NoError(t, module.ReloadCertificates(), "a module that has not started has nothing to reload")
	require.NoError(t, module.Start(t.Context()))
	t.Cleanup(func() { require.NoError(t, module.Close()) })
	address := module.listener.Addr().String()
	require.Equal(t, int64(1), metricsServedSerial(t, address), "initial serial")

	writeMetricsTestCertificate(t, certificateFile, privateKeyFile, 2)
	require.NoError(t, module.ReloadCertificates())
	require.Equal(t, int64(2), metricsServedSerial(t, address), "serial after reload")

	require.NoError(t, os.WriteFile(certificateFile, []byte("not a certificate"), 0o600))
	require.Error(t, module.ReloadCertificates(), "reload accepted an invalid certificate")
	require.Equal(t, int64(2), metricsServedSerial(t, address), "the last good pair stays in service")
}

func metricsServedSerial(t *testing.T, address string) int64 {
	t.Helper()
	connection, err := tls.Dial("tcp", address, &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}) //nolint:gosec // the test inspects the served certificate
	require.NoError(t, err)
	defer connection.Close()
	return connection.ConnectionState().PeerCertificates[0].SerialNumber.Int64()
}

func writeMetricsTestCertificate(t *testing.T, certificateFile, privateKeyFile string, serial int64) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "metrics.proctor.example.edu"},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	require.NoError(t, err)
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(certificateFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER}), 0o600))
	require.NoError(t, os.WriteFile(privateKeyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKeyDER}), 0o600))
}
