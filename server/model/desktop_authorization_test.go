// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"crypto/elliptic"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDesktopAuthorizationLoopbackCallbackValidation(t *testing.T) {
	t.Parallel()

	randomPath := NewCredentialToken()
	// Decimal ports may retain leading zeroes, but the complete registered
	// callback leaves bounded room for the terminal code and state query.
	callbackPrefix := "http://127.0.0.1:"
	callbackSuffix := "55000/" + randomPath
	maximumCallback := callbackPrefix + strings.Repeat("0", DesktopAuthorizationCallbackMaximumBytes-len(callbackPrefix)-len(callbackSuffix)) + callbackSuffix
	for _, callback := range []string{
		"http://127.0.0.1:49152/" + randomPath,
		"http://[::1]:61843/" + randomPath,
		"http://127.0.0.1:65535/" + randomPath,
		"http://127.0.0.1:055000/" + randomPath,
		"http://[::1]:055000/" + randomPath,
		maximumCallback,
	} {
		assert.NoError(t, ValidateDesktopAuthorizationCallback(callback), "valid callback %q", callback)
	}

	invalid := []string{
		callbackPrefix + "0" + strings.TrimPrefix(maximumCallback, callbackPrefix),
		"http://localhost:49152/" + randomPath,
		"http://127.0.0.2:49152/" + randomPath,
		"http://192.0.2.1:49152/" + randomPath,
		"http://[::ffff:127.0.0.1]:49152/" + randomPath,
		"https://127.0.0.1:49152/" + randomPath,
		"proctor://127.0.0.1:49152/" + randomPath,
		"http://user@127.0.0.1:49152/" + randomPath,
		"http://127.0.0.1/" + randomPath,
		"http://127.0.0.1:0/" + randomPath,
		"http://127.0.0.1:49151/" + randomPath,
		"http://127.0.0.1:49152/" + randomPath + "?command=open",
		"http://127.0.0.1:49152/" + randomPath + "#fragment",
		"http://127.0.0.1:49152/short",
		"http://127.0.0.1:49152/prefix/" + randomPath,
		"http://127.0.0.1:49152/%41" + strings.TrimPrefix(randomPath, "A"),
	}
	for _, callback := range invalid {
		assert.Error(t, ValidateDesktopAuthorizationCallback(callback), "invalid callback %q was accepted", callback)
	}
}

func TestBrowserAuthenticationTransactionPinsDesktopAuthorizationRequest(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	publicJWK := testDesktopAuthorizationPublicJWK()
	thumbprint, _ := publicJWK.Thumbprint()
	transaction := &BrowserAuthenticationTransaction{
		Purpose:               BrowserAuthenticationPurposeDesktopAuthorization,
		InstitutionID:         NewInstitutionID(),
		Issuer:                "https://proctor.example.edu",
		HandleHash:            HashToken(NewCredentialToken()),
		BrowserProofHash:      HashToken(NewCredentialToken()),
		StateHash:             HashToken(NewCredentialToken()),
		CallbackURL:           "http://127.0.0.1:49152/" + NewCredentialToken(),
		CodeChallenge:         NewCredentialToken(),
		ClientType:            SessionClientDesktop,
		DeviceID:              "desktop-1",
		DeviceName:            "Exam laptop",
		ProposedPublicJWK:     publicJWK,
		ProposedKeyThumbprint: thumbprint,
		DesktopRelease:        "0.1.0", DesktopBuildID: "test-build",
		DesktopPlatform: DesktopPlatformDarwin, DesktopArchitecture: DesktopArchitectureARM64,
		DesktopRealtimeProtocol: 1,
		ExpiresAt:               at.Add(5 * time.Minute),
	}
	transaction.PrepareCreate(NewBrowserAuthenticationTransactionID(), at)
	require.NoError(t, transaction.Validate())
	require.Equal(t, BrowserAuthenticationStatePending, transaction.State)
	require.Equal(t, at, transaction.CreatedAt)
	require.Equal(t, at, transaction.UpdatedAt)
	audit := transaction.Auditable()
	for _, secret := range []string{
		"handle_hash", "browser_proof_hash", "state_hash", "code_hash",
		"callback_url", "code_challenge", "device_name",
	} {
		_, exists := audit[secret]
		assert.False(t, exists, "audit exposes %q", secret)
	}
}

func TestBrowserAuthenticationTransactionRehydratesExplicitLoopbackDevelopmentIssuer(t *testing.T) {
	t.Parallel()
	transaction := pendingDesktopAuthorizationTransaction(time.Now().UTC())
	transaction.Issuer = "http://localhost:8065"
	require.NoError(t, transaction.Validate())
}

func TestDesktopAuthorizationPKCES256(t *testing.T) {
	t.Parallel()

	// RFC 7636, Appendix B.
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	const challenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	require.True(t, IsValidPKCEVerifier(verifier), "RFC verifier was rejected")
	require.Equal(t, challenge, PKCES256Challenge(verifier))
	for _, invalid := range []string{
		"short", strings.Repeat("a", 129), strings.Repeat("a", 42) + "+",
	} {
		assert.False(t, IsValidPKCEVerifier(invalid), "invalid verifier")
	}
}

func TestBrowserAuthenticationTransactionTerminalStates(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	transaction := pendingDesktopAuthorizationTransaction(at)
	prepareBoundTransactionFixture(transaction, at)
	require.NoError(t, transaction.Validate())
	prepareAuthenticatedTransactionFixture(transaction, at, AuthenticationMultiFactor)
	require.NoError(t, transaction.Validate())
	prepareCodeIssuedTransactionFixture(transaction, at, at.Add(30*time.Second), AuthenticationMultiFactor)
	require.NoError(t, transaction.Validate())
	require.Empty(t, transaction.HandleHash)
	require.Empty(t, transaction.BrowserProofHash)
	require.Equal(t, BrowserAuthenticationStateCodeIssued, transaction.State)
	prepareExchangedTransactionFixture(transaction, at.Add(time.Second))
	require.NoError(t, transaction.Validate())
	require.Empty(t, transaction.CodeHash)
	require.True(t, transaction.ExchangedAt.Valid)

	cancelled := pendingDesktopAuthorizationTransaction(at)
	prepareCancelledTransactionFixture(cancelled, at)
	require.NoError(t, cancelled.Validate())
	require.Empty(t, cancelled.HandleHash)
	require.Empty(t, cancelled.BrowserProofHash)
	require.Empty(t, cancelled.StateHash)
	require.True(t, cancelled.CancelledAt.Valid)

	denied := pendingDesktopAuthorizationTransaction(at)
	prepareDeniedTransactionFixture(denied, at)
	require.NoError(t, denied.Validate())
	require.Empty(t, denied.HandleHash)
	require.Empty(t, denied.BrowserProofHash)
	require.Empty(t, denied.StateHash)
	require.True(t, denied.DeniedAt.Valid)
	require.Equal(t, DesktopAuthorizationDenialActiveAttempt, denied.DenialReason)
}

func TestBrowserAuthenticationTransactionExpiryDestroysProofsAtAuthoritativeDeadline(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	pending := pendingDesktopAuthorizationTransaction(at)
	prepareExpiredTransactionFixture(pending)
	require.NoError(t, pending.Validate())
	require.Equal(t, BrowserAuthenticationStateExpired, pending.State)
	require.True(t, pending.ExpiredAt.Valid)
	require.True(t, pending.ExpiredAt.Time.Equal(pending.ExpiresAt))
	require.True(t, pending.UpdatedAt.Equal(pending.ExpiresAt))
	require.Empty(t, pending.HandleHash)
	require.Empty(t, pending.BrowserProofHash)
	require.Empty(t, pending.StateHash)
	require.Empty(t, pending.CallbackURL)
	require.Empty(t, pending.CodeChallenge)

	issued := pendingDesktopAuthorizationTransaction(at)
	prepareCodeIssuedTransactionFixture(issued, at, at.Add(time.Minute), AuthenticationSingleFactor)
	prepareExpiredTransactionFixture(issued)
	require.NoError(t, issued.Validate())
	require.Empty(t, issued.CodeHash)
	require.False(t, issued.CodeExpiresAt.Valid)
	require.Empty(t, issued.StateHash)
	require.Empty(t, issued.CallbackURL)
	require.Empty(t, issued.CodeChallenge)
	require.True(t, issued.UserID.IsValid())
	require.True(t, issued.ExpiredAt.Time.Equal(at.Add(time.Minute)))
}

func TestDesktopAuthorizationPasswordProofMatchesAuthenticationState(t *testing.T) {
	t.Parallel()
	at := TimeUTC(time.Now())
	password := pendingDesktopAuthorizationTransaction(at)
	prepareAuthenticatedTransactionFixture(password, at, AuthenticationSingleFactor)
	password.AuthenticationMethod = "password"
	password.AuthenticationProviderID, password.ExternalIdentityID = "", ""
	password.PasswordCredentialID, password.PasswordCredentialRevision = NewPasswordCredentialID(), 1
	require.NoError(t, password.Validate())
	for _, state := range []BrowserAuthenticationState{BrowserAuthenticationStateAuthenticated, BrowserAuthenticationStateExpired} {
		t.Run(string(state), func(t *testing.T) {
			for _, test := range []struct {
				name   string
				mutate func(*BrowserAuthenticationTransaction)
			}{
				{name: "missing credential", mutate: func(value *BrowserAuthenticationTransaction) { value.PasswordCredentialID = "" }},
				{name: "missing revision", mutate: func(value *BrowserAuthenticationTransaction) { value.PasswordCredentialRevision = 0 }},
				{name: "negative revision", mutate: func(value *BrowserAuthenticationTransaction) { value.PasswordCredentialRevision = -1 }},
			} {
				t.Run(test.name, func(t *testing.T) {
					candidate := *password
					if state == BrowserAuthenticationStateExpired {
						prepareExpiredTransactionFixture(&candidate)
					}
					test.mutate(&candidate)
					require.Error(t, candidate.Validate(), "password authentication without complete proof was accepted")
				})
			}
		})
	}
	for _, state := range []BrowserAuthenticationState{BrowserAuthenticationStatePending, BrowserAuthenticationStateExpired} {
		candidate := pendingDesktopAuthorizationTransaction(at)
		if state == BrowserAuthenticationStateExpired {
			prepareExpiredTransactionFixture(candidate)
		}
		candidate.PasswordCredentialID, candidate.PasswordCredentialRevision = NewPasswordCredentialID(), 1
		assert.Error(t, candidate.Validate(), "%s transaction retained password proof without an authenticated User", state)
	}
	external := pendingDesktopAuthorizationTransaction(at)
	prepareAuthenticatedTransactionFixture(external, at, AuthenticationSingleFactor)
	external.PasswordCredentialID, external.PasswordCredentialRevision = NewPasswordCredentialID(), 1
	require.Error(t, external.Validate(), "external authentication accepted password proof")
}

func TestBrowserAuthenticationTransactionSupportsInvitationAcceptance(t *testing.T) {
	t.Parallel()
	at := TimeUTC(time.Now())
	transaction := &BrowserAuthenticationTransaction{
		Purpose:       BrowserAuthenticationPurposeInvitationAcceptance,
		InstitutionID: NewInstitutionID(), Issuer: "https://proctor.example.edu",
		InvitationID: NewInvitationID(), InvitationClaimHash: HashInvitationClaim(NewCredentialToken()),
		HandleHash: HashToken(NewCredentialToken()), BrowserProofHash: HashToken(NewCredentialToken()),
		ClientType: SessionClientWeb, ExpiresAt: at.Add(BrowserAuthenticationTransactionLifetime),
	}
	transaction.PrepareCreate(NewBrowserAuthenticationTransactionID(), at)
	require.NoError(t, transaction.Validate())

	completedAt := at.Add(time.Minute)
	transaction.UpdatedAt = completedAt
	transaction.State = BrowserAuthenticationStateCompleted
	transaction.HandleHash, transaction.BrowserProofHash, transaction.InvitationClaimHash = "", "", ""
	transaction.UserID = NewUserID()
	transaction.CompletedAt = OptionalTimeFrom(completedAt)
	require.NoError(t, transaction.Validate())
	require.Empty(t, transaction.InvitationClaimHash, "completed invitation transaction retained credential hashes")
	require.Empty(t, transaction.HandleHash, "completed invitation transaction retained credential hashes")
	require.Empty(t, transaction.BrowserProofHash, "completed invitation transaction retained credential hashes")
}

func pendingDesktopAuthorizationTransaction(at time.Time) *BrowserAuthenticationTransaction {
	publicJWK := testDesktopAuthorizationPublicJWK()
	thumbprint, _ := publicJWK.Thumbprint()
	transaction := &BrowserAuthenticationTransaction{
		Purpose: BrowserAuthenticationPurposeDesktopAuthorization, InstitutionID: NewInstitutionID(),
		Issuer: "https://proctor.example.edu", HandleHash: HashToken(NewCredentialToken()),
		BrowserProofHash: HashToken(NewCredentialToken()), StateHash: HashToken(NewCredentialToken()),
		CallbackURL: "http://127.0.0.1:49152/" + NewCredentialToken(), CodeChallenge: NewCredentialToken(),
		ClientType: SessionClientDesktop, ProposedPublicJWK: publicJWK, ProposedKeyThumbprint: thumbprint,
		DesktopRelease: "0.1.0", DesktopBuildID: "test-build", DesktopPlatform: DesktopPlatformDarwin,
		DesktopArchitecture: DesktopArchitectureARM64, DesktopRealtimeProtocol: 1,
		ExpiresAt: at.Add(5 * time.Minute),
	}
	transaction.PrepareCreate(NewBrowserAuthenticationTransactionID(), at)
	return transaction
}

func prepareBoundTransactionFixture(transaction *BrowserAuthenticationTransaction, at time.Time) {
	transaction.UpdatedAt = at
	transaction.State = BrowserAuthenticationStateBound
	transaction.HandleHash = ""
	transaction.BrowserProofHash = HashToken(NewCredentialToken())
}

func prepareAuthenticatedTransactionFixture(transaction *BrowserAuthenticationTransaction, at time.Time, strength AuthenticationStrength) {
	transaction.UpdatedAt = at
	transaction.State = BrowserAuthenticationStateAuthenticated
	transaction.HandleHash = ""
	transaction.UserID = NewUserID()
	transaction.AuthenticationMethod = "oidc"
	transaction.AuthenticationProviderID = "campus"
	transaction.ExternalIdentityID = NewExternalIdentityID()
	transaction.AuthenticationStrength = strength
	transaction.AuthenticatedAt = OptionalTimeFrom(at.Add(-time.Minute))
	if strength == AuthenticationMultiFactor {
		transaction.MFACompletedAt = OptionalTimeFrom(at)
	}
}

func prepareCodeIssuedTransactionFixture(transaction *BrowserAuthenticationTransaction, at, codeExpiresAt time.Time, strength AuthenticationStrength) {
	if transaction.State != BrowserAuthenticationStateAuthenticated {
		prepareBoundTransactionFixture(transaction, at)
		prepareAuthenticatedTransactionFixture(transaction, at, strength)
	}
	transaction.UpdatedAt = at
	transaction.State = BrowserAuthenticationStateCodeIssued
	transaction.HandleHash, transaction.BrowserProofHash = "", ""
	transaction.CodeHash = HashToken(NewCredentialToken())
	transaction.CodeExpiresAt = OptionalTimeFrom(codeExpiresAt)
}

func prepareExchangedTransactionFixture(transaction *BrowserAuthenticationTransaction, at time.Time) {
	transaction.UpdatedAt = at
	transaction.State = BrowserAuthenticationStateExchanged
	transaction.StateHash, transaction.CallbackURL, transaction.CodeChallenge, transaction.CodeHash = "", "", "", ""
	transaction.CodeExpiresAt = OptionalTime{}
	clearDesktopAuthorizationKeyFixture(transaction)
	transaction.ExchangedAt = OptionalTimeFrom(at)
}

func prepareCancelledTransactionFixture(transaction *BrowserAuthenticationTransaction, at time.Time) {
	transaction.UpdatedAt = at
	transaction.State = BrowserAuthenticationStateCancelled
	transaction.HandleHash, transaction.BrowserProofHash, transaction.StateHash = "", "", ""
	transaction.CallbackURL, transaction.CodeChallenge = "", ""
	clearDesktopAuthorizationKeyFixture(transaction)
	transaction.CancelledAt = OptionalTimeFrom(at)
}

func prepareDeniedTransactionFixture(transaction *BrowserAuthenticationTransaction, at time.Time) {
	transaction.UpdatedAt = at
	transaction.State = BrowserAuthenticationStateDenied
	transaction.HandleHash, transaction.BrowserProofHash, transaction.StateHash = "", "", ""
	transaction.CallbackURL, transaction.CodeChallenge = "", ""
	clearDesktopAuthorizationKeyFixture(transaction)
	transaction.DeniedAt = OptionalTimeFrom(at)
	transaction.DenialReason = DesktopAuthorizationDenialActiveAttempt
}

func testDesktopAuthorizationPublicJWK() DesktopPublicJWK {
	curve := elliptic.P256().Params()
	return DesktopPublicJWK{
		Kty: "EC", Crv: "P-256",
		X: base64.RawURLEncoding.EncodeToString(curve.Gx.FillBytes(make([]byte, 32))),
		Y: base64.RawURLEncoding.EncodeToString(curve.Gy.FillBytes(make([]byte, 32))),
	}
}

func clearDesktopAuthorizationKeyFixture(transaction *BrowserAuthenticationTransaction) {
	transaction.ProposedPublicJWK = DesktopPublicJWK{}
	transaction.ProposedKeyThumbprint = ""
	transaction.DesktopRelease = ""
	transaction.DesktopBuildID = ""
	transaction.DesktopPlatform = ""
	transaction.DesktopArchitecture = ""
	transaction.DesktopRealtimeProtocol = 0
}

func prepareExpiredTransactionFixture(transaction *BrowserAuthenticationTransaction) {
	deadline := transaction.ExpiresAt
	if transaction.CodeExpiresAt.Valid && transaction.CodeExpiresAt.Time.Before(deadline) {
		deadline = transaction.CodeExpiresAt.Time
	}
	transaction.UpdatedAt = deadline
	transaction.State = BrowserAuthenticationStateExpired
	transaction.HandleHash, transaction.BrowserProofHash, transaction.StateHash = "", "", ""
	transaction.InvitationClaimHash = ""
	transaction.CallbackURL, transaction.CodeChallenge, transaction.CodeHash = "", "", ""
	transaction.CodeExpiresAt = OptionalTime{}
	clearDesktopAuthorizationKeyFixture(transaction)
	transaction.ExpiredAt = OptionalTimeFrom(deadline)
}
