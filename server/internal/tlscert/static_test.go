// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package tlscert

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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func writeSerialTestCertificate(t *testing.T, certificateFile, privateKeyFile string, serial int64) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "proctor.example.edu"},
		DNSNames: []string{"proctor.example.edu"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	require.NoError(t, err)
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(certificateFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER}), 0o600))
	require.NoError(t, os.WriteFile(privateKeyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKeyDER}), 0o600))
	// Distinct modification times make replacement observable on coarse
	// file-system clocks.
	stamp := now.Add(time.Duration(serial) * time.Second)
	for _, path := range []string{certificateFile, privateKeyFile} {
		require.NoError(t, os.Chtimes(path, stamp, stamp))
	}
}

func servedSerial(t *testing.T, certificate *Static) int64 {
	t.Helper()
	served, err := certificate.GetCertificate(&tls.ClientHelloInfo{ServerName: "proctor.example.edu"})
	require.NoError(t, err)
	leaf, err := x509.ParseCertificate(served.Certificate[0])
	require.NoError(t, err)
	return leaf.SerialNumber.Int64()
}

func TestStaticReplacesRotatedFilesAndKeepsLastGoodPair(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	certificateFile, privateKeyFile := filepath.Join(directory, "certificate.pem"), filepath.Join(directory, "private-key.pem")
	writeSerialTestCertificate(t, certificateFile, privateKeyFile, 1)
	var failures []error
	certificate := New(certificateFile, privateKeyFile, func(err error) { failures = append(failures, err) })
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	certificate.now = func() time.Time { return now }
	require.NoError(t, certificate.Reload())
	require.Equal(t, int64(1), servedSerial(t, certificate), "initial serial")

	writeSerialTestCertificate(t, certificateFile, privateKeyFile, 2)
	require.Equal(t, int64(1), servedSerial(t, certificate), "serial before the check interval")
	now = now.Add(CheckInterval)
	require.Equal(t, int64(2), servedSerial(t, certificate), "serial after rotation")

	require.NoError(t, os.WriteFile(certificateFile, []byte("not a certificate"), 0o600))
	now = now.Add(CheckInterval)
	require.Equal(t, int64(2), servedSerial(t, certificate), "after invalid replacement serial")
	require.Len(t, failures, 1, "after invalid replacement serial")
	require.Error(t, certificate.Reload(), "explicit reload accepted an invalid certificate")

	writeSerialTestCertificate(t, certificateFile, privateKeyFile, 3)
	require.NoError(t, certificate.Reload())
	require.Equal(t, int64(3), servedSerial(t, certificate), "serial after explicit reload")
}
