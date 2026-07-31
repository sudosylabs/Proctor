// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMFACredentialValidationAndRedaction(t *testing.T) {
	pending := &MFACredential{
		UserId:           NewId(),
		State:            MFAStatePending,
		EncryptedSecret:  "ciphertext",
		EncryptionKeyId:  "0123456789abcdef",
		PendingExpiresAt: GetMillis() + 60_000,
	}
	pending.PreSave()
	if appErr := pending.IsValid(); appErr != nil {
		t.Fatal(appErr)
	}
	encoded, err := json.Marshal(pending)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), pending.EncryptedSecret) ||
		strings.Contains(string(encoded), pending.EncryptionKeyId) {
		t.Fatalf("MFA credential exposed encryption material: %s", encoded)
	}
	auditable := pending.Auditable()
	if _, exists := auditable["encrypted_secret"]; exists {
		t.Fatal("MFA auditable projection exposed encrypted secret")
	}

	active := *pending
	active.State = MFAStateActive
	active.PendingExpiresAt = 0
	active.EnabledAt = active.CreateAt
	active.LastUsedTimeStep = 1
	if appErr := active.IsValid(); appErr != nil {
		t.Fatal(appErr)
	}
	active.LastUsedTimeStep = 0
	if appErr := active.IsValid(); appErr == nil {
		t.Fatal("active MFA credential accepted missing replay state")
	}
}

func TestMFARecoveryCodeValidationAndRedaction(t *testing.T) {
	raw := NewCredentialToken()
	code := &MFARecoveryCode{
		UserId: NewId(), CodeHash: HashToken(raw),
	}
	code.PreSave()
	if appErr := code.IsValid(); appErr != nil {
		t.Fatal(appErr)
	}
	encoded, err := json.Marshal(code)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), code.CodeHash) {
		t.Fatalf("MFA recovery code exposed hash: %s", encoded)
	}
	if _, exists := code.Auditable()["code_hash"]; exists {
		t.Fatal("MFA recovery-code audit exposed hash")
	}
}
