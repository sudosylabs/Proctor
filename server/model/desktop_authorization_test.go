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
	prepareCodeIssuedTransactionFixture(transaction, at, at.Add(30*time.Second), AuthenticationMultiFactor)
	if err := transaction.Validate(); err != nil {
		t.Fatalf("issued transaction: %v", err)
	}
	if transaction.HandleHash != "" || transaction.BrowserProofHash != "" ||
		transaction.State != BrowserAuthenticationStateCodeIssued {
		t.Fatalf("issued transaction retained browser secrets: %#v", transaction)
	}
	prepareExchangedTransactionFixture(transaction, at.Add(time.Second))
	if err := transaction.Validate(); err != nil {
		t.Fatalf("exchanged transaction: %v", err)
	}
	if transaction.CodeHash != "" || !transaction.ExchangedAt.Valid {
		t.Fatalf("exchanged transaction retained code: %#v", transaction)
	}

	cancelled := pendingDesktopAuthorizationTransaction(at)
	prepareCancelledTransactionFixture(cancelled, at)
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
	prepareExpiredTransactionFixture(pending)
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
	prepareCodeIssuedTransactionFixture(issued, at, at.Add(time.Minute), AuthenticationSingleFactor)
	prepareExpiredTransactionFixture(issued)
	if err := issued.Validate(); err != nil {
		t.Fatalf("expired issued-code transaction: %v", err)
	}
	if issued.CodeHash != "" || issued.CodeExpiresAt.Valid || issued.StateHash != "" ||
		issued.CallbackURL != "" || issued.CodeChallenge != "" || !issued.UserID.IsValid() ||
		!issued.ExpiredAt.Time.Equal(at.Add(time.Minute)) {
		t.Fatalf("expired issued-code transaction retained proofs or lost safe metadata: %#v", issued)
	}
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
	if err := transaction.Validate(); err != nil {
		t.Fatalf("pending invitation transaction: %v", err)
	}

	completedAt := at.Add(time.Minute)
	transaction.UpdatedAt = completedAt
	transaction.State = BrowserAuthenticationStateCompleted
	transaction.HandleHash, transaction.BrowserProofHash, transaction.InvitationClaimHash = "", "", ""
	transaction.UserID = NewUserID()
	transaction.CompletedAt = OptionalTimeFrom(completedAt)
	if err := transaction.Validate(); err != nil {
		t.Fatalf("completed invitation transaction: %v", err)
	}
	if transaction.InvitationClaimHash != "" || transaction.HandleHash != "" || transaction.BrowserProofHash != "" {
		t.Fatal("completed invitation transaction retained credential hashes")
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

func prepareCodeIssuedTransactionFixture(transaction *BrowserAuthenticationTransaction, at, codeExpiresAt time.Time, strength AuthenticationStrength) {
	transaction.UpdatedAt = at
	transaction.State = BrowserAuthenticationStateCodeIssued
	transaction.HandleHash, transaction.BrowserProofHash = "", ""
	transaction.UserID = NewUserID()
	transaction.AuthenticationMethod = transaction.ExpectedAuthenticationMethod
	transaction.AuthenticationProviderID = transaction.ExpectedProviderID
	transaction.ExternalIdentityID = NewExternalIdentityID()
	transaction.AuthenticationStrength = strength
	transaction.AuthenticatedAt = OptionalTimeFrom(at.Add(-time.Minute))
	if strength == AuthenticationMultiFactor {
		transaction.MFACompletedAt = OptionalTimeFrom(at)
	}
	transaction.CodeHash = HashToken(NewCredentialToken())
	transaction.CodeExpiresAt = OptionalTimeFrom(codeExpiresAt)
}

func prepareExchangedTransactionFixture(transaction *BrowserAuthenticationTransaction, at time.Time) {
	transaction.UpdatedAt = at
	transaction.State = BrowserAuthenticationStateExchanged
	transaction.StateHash, transaction.CallbackURL, transaction.CodeChallenge, transaction.CodeHash = "", "", "", ""
	transaction.CodeExpiresAt = OptionalTime{}
	transaction.ExchangedAt = OptionalTimeFrom(at)
}

func prepareCancelledTransactionFixture(transaction *BrowserAuthenticationTransaction, at time.Time) {
	transaction.UpdatedAt = at
	transaction.State = BrowserAuthenticationStateCancelled
	transaction.HandleHash, transaction.BrowserProofHash, transaction.StateHash = "", "", ""
	transaction.CallbackURL, transaction.CodeChallenge = "", ""
	transaction.CancelledAt = OptionalTimeFrom(at)
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
	transaction.ExpiredAt = OptionalTimeFrom(deadline)
}
