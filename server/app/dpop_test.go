// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

func TestDPoPProofNonceBindingAndReplay(t *testing.T) {
	at := time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)
	cache := newDPoPCacheFake()
	security, err := newDPoPSecurity(cache, dpopPolicy{
		Origin: "https://proctor.example", NonceLifetime: 5 * time.Minute,
		ProofLifetime: 5 * time.Minute, ClockSkew: time.Minute,
		NewNonce: model.NewCredentialToken,
		Now:      func() time.Time { return at },
	})
	if err != nil {
		t.Fatal(err)
	}
	privateKey, jwk := testDPoPKey(t)
	thumbprint, _ := jwk.Thumbprint()
	binding := dpopBinding{
		Kind: dpopBindingAuthorization, AuthorizationID: model.NewBrowserAuthenticationTransactionID(),
		KeyThumbprint: thumbprint, Origin: "https://proctor.example",
	}
	nonce, err := security.IssueNonce(context.Background(), binding)
	if err != nil {
		t.Fatal(err)
	}
	proof := signDPoPProof(t, privateKey, jwk, map[string]any{
		"jti": "1234567890123456", "htm": "POST",
		"htu": "https://proctor.example/api/v1/auth/desktop/token",
		"iat": at.Unix(), "nonce": nonce,
	})
	verified, err := security.Verify(
		context.Background(), proof, "POST",
		"https://proctor.example/api/v1/auth/desktop/token", "", &jwk, binding,
	)
	if err != nil || verified.KeyThumbprint != thumbprint {
		t.Fatalf("Verify() = %#v, %v", verified, err)
	}
	if _, err = security.Verify(
		context.Background(), proof, "POST",
		"https://proctor.example/api/v1/auth/desktop/token", "", &jwk, binding,
	); !Is(err, "authentication.dpop.replayed") {
		t.Fatalf("replayed Verify() error = %v", err)
	}
}

func TestDPoPProofRequiresAccessTokenHashAndFreshNonce(t *testing.T) {
	at := time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)
	cache := newDPoPCacheFake()
	security, err := newDPoPSecurity(cache, dpopPolicy{
		Origin: "https://proctor.example", NonceLifetime: 5 * time.Minute,
		ProofLifetime: 5 * time.Minute, ClockSkew: time.Minute,
		Now:      func() time.Time { return at },
		NewNonce: model.NewCredentialToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	privateKey, jwk := testDPoPKey(t)
	thumbprint, _ := jwk.Thumbprint()
	binding := dpopBinding{
		Kind: dpopBindingSession, SessionID: model.NewSessionID(),
		DesktopRegistrationID: model.NewDesktopRegistrationID(),
		KeyThumbprint:         thumbprint, Origin: "https://proctor.example",
	}
	proof := signDPoPProof(t, privateKey, jwk, map[string]any{
		"jti": "1234567890123456", "htm": "GET",
		"htu": "https://proctor.example/api/v1/users/me",
		"iat": at.Unix(), "ath": dpopAccessTokenHash("access"),
	})
	_, err = security.Verify(
		context.Background(), proof, "GET",
		"https://proctor.example/api/v1/users/me", "access", nil, binding,
	)
	var challenge *dpopChallengeError
	if !errors.As(err, &challenge) || challenge.Nonce() == "" {
		t.Fatalf("Verify() error = %v, want nonce challenge", err)
	}
}

func testDPoPKey(t *testing.T) (*ecdsa.PrivateKey, model.DesktopPublicJWK) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return privateKey, model.DesktopPublicJWK{
		Kty: "EC", Crv: "P-256",
		X: base64.RawURLEncoding.EncodeToString(privateKey.X.FillBytes(make([]byte, 32))),
		Y: base64.RawURLEncoding.EncodeToString(privateKey.Y.FillBytes(make([]byte, 32))),
	}
}

func signDPoPProof(
	t *testing.T,
	key *ecdsa.PrivateKey,
	jwk model.DesktopPublicJWK,
	claims map[string]any,
) string {
	t.Helper()
	header, err := json.Marshal(map[string]any{"typ": "dpop+jwt", "alg": "ES256", "jwk": jwk})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	encodedHeader := base64.RawURLEncoding.EncodeToString(header)
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(encodedHeader + "." + encodedPayload))
	r, signatureS, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	signature := append(
		r.FillBytes(make([]byte, 32)),
		signatureS.FillBytes(make([]byte, 32))...,
	)
	return encodedHeader + "." + encodedPayload + "." +
		base64.RawURLEncoding.EncodeToString(signature)
}

type dpopCacheEntry struct {
	value []byte
}

type dpopCacheFake struct {
	values       map[string]dpopCacheEntry
	setAlwaysErr error
}

func newDPoPCacheFake() *dpopCacheFake {
	return &dpopCacheFake{values: map[string]dpopCacheEntry{}}
}

func (c *dpopCacheFake) Get(_ context.Context, key string) ([]byte, error) {
	entry, ok := c.values[key]
	if !ok {
		return nil, ErrAuthenticationCacheMiss
	}
	return append([]byte(nil), entry.value...), nil
}

func (c *dpopCacheFake) SetAlways(_ context.Context, key string, value []byte, _ time.Duration) error {
	if c.setAlwaysErr != nil {
		return c.setAlwaysErr
	}
	c.values[key] = dpopCacheEntry{value: append([]byte(nil), value...)}
	return nil
}

func (c *dpopCacheFake) SetIfAbsent(_ context.Context, key string, value []byte, _ time.Duration) error {
	if _, exists := c.values[key]; exists {
		return ErrAuthenticationCacheNotStored
	}
	c.values[key] = dpopCacheEntry{value: append([]byte(nil), value...)}
	return nil
}

func (c *dpopCacheFake) Delete(_ context.Context, key string) error {
	delete(c.values, key)
	return nil
}

func (c *dpopCacheFake) Add(context.Context, string, int64, time.Duration) (int64, error) {
	return 0, nil
}
