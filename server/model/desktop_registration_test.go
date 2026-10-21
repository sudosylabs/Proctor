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

	"github.com/stretchr/testify/require"
)

func TestDesktopPublicJWKThumbprintAndValidation(t *testing.T) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	jwk := DesktopPublicJWK{
		Kty: "EC", Crv: "P-256",
		X: rawBase64URL.EncodeToString(privateKey.X.FillBytes(make([]byte, 32))),
		Y: rawBase64URL.EncodeToString(privateKey.Y.FillBytes(make([]byte, 32))),
	}
	thumbprint, err := jwk.Thumbprint()
	require.NoError(t, err)
	require.True(t, IsValidDPoPKeyThumbprint(thumbprint), "Thumbprint()")
	_, err = (DesktopPublicJWK{Kty: "EC", Crv: "P-256", X: jwk.X, Y: jwk.X}).Thumbprint()
	require.Error(t, err, "Thumbprint() accepted a point that is not on P-256")
}

func TestDesktopRegistrationValidation(t *testing.T) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	jwk := DesktopPublicJWK{Kty: "EC", Crv: "P-256",
		X: rawBase64URL.EncodeToString(privateKey.X.FillBytes(make([]byte, 32))),
		Y: rawBase64URL.EncodeToString(privateKey.Y.FillBytes(make([]byte, 32)))}
	at := time.Date(2026, time.August, 28, 10, 0, 0, 0, time.UTC)
	registration := DesktopRegistration{UserID: NewUserID(), InstitutionID: NewInstitutionID(), PublicJWK: jwk,
		DisplayName: "Candidate laptop", DesktopRelease: "0.1.0", DesktopBuildID: "build-1",
		Platform: DesktopPlatformDarwin, Architecture: DesktopArchitectureARM64, RealtimeProtocol: 1}
	registration.PrepareCreate(NewDesktopRegistrationID(), at)
	err = registration.Validate()
	require.NoError(t, err)
	registration.KeyThumbprint = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	err = registration.Validate()
	require.Error(t, err, "Validate() accepted a mismatched thumbprint")
}
