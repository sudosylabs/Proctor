// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"testing"
	"time"
)

func TestDesktopPublicJWKThumbprintAndValidation(t *testing.T) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	jwk := DesktopPublicJWK{
		Kty: "EC", Crv: "P-256",
		X: rawBase64URL.EncodeToString(privateKey.X.FillBytes(make([]byte, 32))),
		Y: rawBase64URL.EncodeToString(privateKey.Y.FillBytes(make([]byte, 32))),
	}
	thumbprint, err := jwk.Thumbprint()
	if err != nil {
		t.Fatalf("Thumbprint() error = %v", err)
	}
	if !IsValidDPoPKeyThumbprint(thumbprint) {
		t.Fatalf("Thumbprint() = %q, want canonical SHA-256 thumbprint", thumbprint)
	}
	if _, err = (DesktopPublicJWK{Kty: "EC", Crv: "P-256", X: jwk.X, Y: jwk.X}).Thumbprint(); err == nil {
		t.Fatal("Thumbprint() accepted a point that is not on P-256")
	}
}

func TestDesktopRegistrationValidation(t *testing.T) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	jwk := DesktopPublicJWK{Kty: "EC", Crv: "P-256",
		X: rawBase64URL.EncodeToString(privateKey.X.FillBytes(make([]byte, 32))),
		Y: rawBase64URL.EncodeToString(privateKey.Y.FillBytes(make([]byte, 32)))}
	at := time.Date(2026, time.August, 28, 10, 0, 0, 0, time.UTC)
	registration := DesktopRegistration{UserID: NewUserID(), InstitutionID: NewInstitutionID(), PublicJWK: jwk,
		DisplayName: "Candidate laptop", DesktopRelease: "0.1.0", DesktopBuildID: "build-1",
		Platform: DesktopPlatformDarwin, Architecture: DesktopArchitectureARM64, RealtimeProtocol: 1}
	registration.PrepareCreate(NewDesktopRegistrationID(), at)
	if err = registration.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	registration.KeyThumbprint = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	if err = registration.Validate(); err == nil {
		t.Fatal("Validate() accepted a mismatched thumbprint")
	}
}
