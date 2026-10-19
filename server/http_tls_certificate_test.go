// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package server

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
)

func writeSerialTestCertificate(t *testing.T, certificateFile, privateKeyFile string, serial int64) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "proctor.example.edu"},
		DNSNames: []string{"proctor.example.edu"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certificateFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(privateKeyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKeyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	// Distinct modification times make replacement observable on coarse
	// file-system clocks.
	stamp := now.Add(time.Duration(serial) * time.Second)
	for _, path := range []string{certificateFile, privateKeyFile} {
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
}

func servedSerial(t *testing.T, certificate *staticCertificate) int64 {
	t.Helper()
	served, err := certificate.GetCertificate(&tls.ClientHelloInfo{ServerName: "proctor.example.edu"})
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(served.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	return leaf.SerialNumber.Int64()
}

func TestStaticCertificateReplacesRotatedFilesAndKeepsLastGoodPair(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	certificateFile, privateKeyFile := filepath.Join(directory, "certificate.pem"), filepath.Join(directory, "private-key.pem")
	writeSerialTestCertificate(t, certificateFile, privateKeyFile, 1)
	var failures []error
	certificate := newStaticCertificate(certificateFile, privateKeyFile, func(err error) { failures = append(failures, err) })
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	certificate.now = func() time.Time { return now }
	if err := certificate.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := servedSerial(t, certificate); got != 1 {
		t.Fatalf("initial serial = %d, want 1", got)
	}

	writeSerialTestCertificate(t, certificateFile, privateKeyFile, 2)
	if got := servedSerial(t, certificate); got != 1 {
		t.Fatalf("serial before the check interval = %d, want 1", got)
	}
	now = now.Add(staticCertificateCheckInterval)
	if got := servedSerial(t, certificate); got != 2 {
		t.Fatalf("serial after rotation = %d, want 2", got)
	}

	if err := os.WriteFile(certificateFile, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	now = now.Add(staticCertificateCheckInterval)
	if got := servedSerial(t, certificate); got != 2 || len(failures) != 1 {
		t.Fatalf("after invalid replacement serial = %d failures = %d, want last good serial 2 and one failure", got, len(failures))
	}
	if err := certificate.Reload(); err == nil {
		t.Fatal("explicit reload accepted an invalid certificate")
	}

	writeSerialTestCertificate(t, certificateFile, privateKeyFile, 3)
	if err := certificate.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := servedSerial(t, certificate); got != 3 {
		t.Fatalf("serial after explicit reload = %d, want 3", got)
	}
}
