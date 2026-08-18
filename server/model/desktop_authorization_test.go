// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"strings"
	"testing"
	"time"
)

func TestDesktopAuthorizationLoopbackCallbackValidation(t *testing.T) {
	t.Parallel()

	randomPath := NewCredentialToken()
	for _, callback := range []string{
		"http://127.0.0.1:49152/" + randomPath,
		"http://[::1]:61843/" + randomPath,
		"http://127.0.0.1:65535/" + randomPath,
	} {
		if err := ValidateDesktopAuthorizationCallback(callback); err != nil {
			t.Errorf("valid callback %q: %v", callback, err)
		}
	}

	invalid := []string{
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
		if err := ValidateDesktopAuthorizationCallback(callback); err == nil {
			t.Errorf("invalid callback %q was accepted", callback)
		}
	}
}

func TestBrowserAuthenticationTransactionPinsDesktopAuthorizationRequest(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	transaction := &BrowserAuthenticationTransaction{
		Purpose:                      BrowserAuthenticationPurposeDesktopAuthorization,
		InstitutionID:                NewInstitutionID(),
		Issuer:                       "https://proctor.example.edu",
		HandleHash:                   HashToken(NewCredentialToken()),
		BrowserProofHash:             HashToken(NewCredentialToken()),
		StateHash:                    HashToken(NewCredentialToken()),
		CallbackURL:                  "http://127.0.0.1:49152/" + NewCredentialToken(),
		CodeChallenge:                NewCredentialToken(),
		ExpectedAuthenticationMethod: "password",
		ClientType:                   SessionClientDesktop,
		DeviceID:                     "desktop-1",
		DeviceName:                   "Exam laptop",
		ExpiresAt:                    at.Add(5 * time.Minute),
	}
	transaction.PrepareCreate(NewBrowserAuthenticationTransactionID(), at)
	if err := transaction.Validate(); err != nil {
		t.Fatal(err)
	}
	if transaction.State != BrowserAuthenticationStatePending ||
		transaction.CreatedAt != at || transaction.UpdatedAt != at {
		t.Fatalf("prepared transaction = %#v", transaction)
	}
	audit := transaction.Auditable()
	for _, secret := range []string{
		"handle_hash", "browser_proof_hash", "state_hash", "code_hash",
		"callback_url", "code_challenge", "device_name",
	} {
		if _, exists := audit[secret]; exists {
			t.Errorf("audit exposes %q", secret)
		}
	}
}

func TestBrowserAuthenticationTransactionRehydratesExplicitLoopbackDevelopmentIssuer(t *testing.T) {
	t.Parallel()
	transaction := pendingDesktopAuthorizationTransaction(time.Now().UTC())
	transaction.Issuer = "http://localhost:8065"
	if err := transaction.Validate(); err != nil {
		t.Fatalf("loopback development transaction: %v", err)
	}
}

func TestDesktopAuthorizationPKCES256(t *testing.T) {
	t.Parallel()

	// RFC 7636, Appendix B.
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	const challenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	if !IsValidPKCEVerifier(verifier) {
		t.Fatal("RFC verifier was rejected")
	}
	if got := PKCES256Challenge(verifier); got != challenge {
		t.Fatalf("challenge = %q", got)
	}
	for _, invalid := range []string{
		"short", strings.Repeat("a", 129), strings.Repeat("a", 42) + "+",
	} {
		if IsValidPKCEVerifier(invalid) {
			t.Errorf("invalid verifier %q was accepted", invalid)
		}
	}
}

func TestBrowserAuthenticationTransactionTerminalStates(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	transaction := pendingDesktopAuthorizationTransaction(at)
	userID := NewUserID()
	codeHash := HashToken(NewCredentialToken())
	transaction.PrepareCodeIssued(userID, "oidc", "campus", NewExternalIdentityID(), AuthenticationMultiFactor,
		at.Add(-time.Minute), OptionalTimeFrom(at), codeHash, at.Add(30*time.Second), at)
	if err := transaction.Validate(); err != nil {
		t.Fatalf("issued transaction: %v", err)
	}
	if transaction.HandleHash != "" || transaction.BrowserProofHash != "" ||
		transaction.State != BrowserAuthenticationStateCodeIssued {
		t.Fatalf("issued transaction retained browser secrets: %#v", transaction)
	}
	transaction.PrepareExchanged(at.Add(time.Second))
	if err := transaction.Validate(); err != nil {
		t.Fatalf("exchanged transaction: %v", err)
	}
	if transaction.CodeHash != "" || !transaction.ExchangedAt.Valid {
		t.Fatalf("exchanged transaction retained code: %#v", transaction)
	}

	cancelled := pendingDesktopAuthorizationTransaction(at)
	cancelled.PrepareCancelled(at)
	if err := cancelled.Validate(); err != nil {
		t.Fatalf("cancelled transaction: %v", err)
	}
	if cancelled.HandleHash != "" || cancelled.BrowserProofHash != "" ||
		cancelled.StateHash != "" || !cancelled.CancelledAt.Valid {
		t.Fatalf("cancelled transaction retained secrets: %#v", cancelled)
	}
}

func TestBrowserAuthenticationTransactionExpiryDestroysProofsAtAuthoritativeDeadline(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	pending := pendingDesktopAuthorizationTransaction(at)
	pending.PrepareExpired(at.Add(10 * time.Minute))
	if err := pending.Validate(); err != nil {
		t.Fatalf("expired pending transaction: %v", err)
	}
	if pending.State != BrowserAuthenticationStateExpired || !pending.ExpiredAt.Valid ||
		!pending.ExpiredAt.Time.Equal(pending.ExpiresAt) || !pending.UpdatedAt.Equal(pending.ExpiresAt) ||
		pending.HandleHash != "" || pending.BrowserProofHash != "" || pending.StateHash != "" ||
		pending.CallbackURL != "" || pending.CodeChallenge != "" {
		t.Fatalf("expired pending transaction retained proofs or wrong deadline: %#v", pending)
	}

	issued := pendingDesktopAuthorizationTransaction(at)
	issued.PrepareCodeIssued(NewUserID(), "oidc", "campus", NewExternalIdentityID(), AuthenticationSingleFactor,
		at, OptionalTime{}, HashToken(NewCredentialToken()), at.Add(time.Minute), at)
	issued.PrepareExpired(at.Add(2 * time.Minute))
	if err := issued.Validate(); err != nil {
		t.Fatalf("expired issued-code transaction: %v", err)
	}
	if issued.CodeHash != "" || issued.CodeExpiresAt.Valid || issued.StateHash != "" ||
		issued.CallbackURL != "" || issued.CodeChallenge != "" || !issued.UserID.IsValid() ||
		!issued.ExpiredAt.Time.Equal(at.Add(time.Minute)) {
		t.Fatalf("expired issued-code transaction retained proofs or lost safe metadata: %#v", issued)
	}
}

func pendingDesktopAuthorizationTransaction(at time.Time) *BrowserAuthenticationTransaction {
	transaction := &BrowserAuthenticationTransaction{
		Purpose: BrowserAuthenticationPurposeDesktopAuthorization, InstitutionID: NewInstitutionID(),
		Issuer: "https://proctor.example.edu", HandleHash: HashToken(NewCredentialToken()),
		BrowserProofHash: HashToken(NewCredentialToken()), StateHash: HashToken(NewCredentialToken()),
		CallbackURL: "http://127.0.0.1:49152/" + NewCredentialToken(), CodeChallenge: NewCredentialToken(),
		ExpectedAuthenticationMethod: "oidc", ExpectedProviderID: "campus",
		ClientType: SessionClientDesktop, ExpiresAt: at.Add(5 * time.Minute),
	}
	transaction.PrepareCreate(NewBrowserAuthenticationTransactionID(), at)
	return transaction
}
