// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestMFACredentialValidationAndRedaction(t *testing.T) {
	now := TimeFromMillis(1_700_000_000_000)
	pending := &MFACredential{
		UserID:           NewUserID(),
		State:            MFAStatePending,
		EncryptedSecret:  "ciphertext",
		EncryptionKeyID:  "0123456789abcdef0123456789abcdef",
		PendingExpiresAt: OptionalTimeFrom(now.Add(time.Minute)),
	}
	pending.PrepareCreate(NewMFACredentialID(), now)
	if err := pending.Validate(); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(pending)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), pending.EncryptedSecret) ||
		strings.Contains(string(encoded), pending.EncryptionKeyID) {
		t.Fatalf("MFA credential exposed encryption material: %s", encoded)
	}
	auditable := pending.Auditable()
	if _, exists := auditable["encrypted_secret"]; exists {
		t.Fatal("MFA auditable projection exposed encrypted secret")
	}
	if _, exists := auditable["encryption_key_id"]; exists {
		t.Fatal("MFA auditable projection exposed encryption key id")
	}
	if !pending.IsPendingAt(now) || pending.IsPendingAt(pending.PendingExpiresAt.Time) {
		t.Fatalf("IsPendingAt() inconsistent for %#v", pending)
	}
	invalidKeyID := *pending
	invalidKeyID.EncryptionKeyID = strings.ToUpper(invalidKeyID.EncryptionKeyID)
	if err := invalidKeyID.Validate(); err == nil {
		t.Fatal("MFA credential accepted a non-canonical key ID")
	}

	active := *pending
	active.State = MFAStateActive
	active.PendingExpiresAt = OptionalTime{}
	active.ActivatedAt = OptionalTimeFrom(active.CreatedAt)
	active.LastUsedTimeStep = 1
	if err := active.Validate(); err != nil {
		t.Fatal(err)
	}
	if !active.IsActive() {
		t.Fatal("IsActive() = false for valid active credential")
	}
	active.LastUsedTimeStep = 0
	if err := active.Validate(); err == nil {
		t.Fatal("active MFA credential accepted missing replay state")
	}
}

func TestMFARecoveryCodeValidationAndRedaction(t *testing.T) {
	raw := NewCredentialToken()
	now := TimeFromMillis(1_700_000_000_000)
	code := &MFARecoveryCode{
		UserID:   NewUserID(),
		CodeHash: HashToken(raw),
	}
	code.PrepareCreate(NewMFARecoveryCodeID(), now)
	if err := code.Validate(); err != nil {
		t.Fatal(err)
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
	code.ConsumedAt = OptionalTimeFrom(now.Add(-time.Second))
	if err := code.Validate(); err == nil {
		t.Fatal("recovery code accepted consumed_at before created_at")
	}
}
