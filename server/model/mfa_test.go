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

	"github.com/stretchr/testify/require"
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
	require.NoError(t, pending.Validate())
	encoded, err := json.Marshal(pending)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), pending.EncryptedSecret)
	require.NotContains(t, string(encoded), pending.EncryptionKeyID)
	auditable := pending.Auditable()
	_, exists := auditable["encrypted_secret"]
	require.False(t, exists, "MFA auditable projection exposed encrypted secret")
	_, exists = auditable["encryption_key_id"]
	require.False(t, exists, "MFA auditable projection exposed encryption key id")
	require.True(t, pending.IsPendingAt(now))
	require.False(t, pending.IsPendingAt(pending.PendingExpiresAt.Time))
	invalidKeyID := *pending
	invalidKeyID.EncryptionKeyID = strings.ToUpper(invalidKeyID.EncryptionKeyID)
	require.Error(t, invalidKeyID.Validate(), "MFA credential accepted a non-canonical key ID")

	active := *pending
	active.State = MFAStateActive
	active.PendingExpiresAt = OptionalTime{}
	active.ActivatedAt = OptionalTimeFrom(active.CreatedAt)
	active.LastUsedTimeStep = 1
	require.NoError(t, active.Validate())
	require.True(t, active.IsActive(), "IsActive() = false for valid active credential")
	active.LastUsedTimeStep = 0
	require.Error(t, active.Validate(), "active MFA credential accepted missing replay state")
}

func TestMFARecoveryCodeValidationAndRedaction(t *testing.T) {
	raw := NewCredentialToken()
	now := TimeFromMillis(1_700_000_000_000)
	code := &MFARecoveryCode{
		UserID:   NewUserID(),
		CodeHash: HashToken(raw),
	}
	code.PrepareCreate(NewMFARecoveryCodeID(), now)
	require.NoError(t, code.Validate())
	encoded, err := json.Marshal(code)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), code.CodeHash, "MFA recovery code exposed hash")
	_, exists := code.Auditable()["code_hash"]
	require.False(t, exists, "MFA recovery-code audit exposed hash")
	code.ConsumedAt = OptionalTimeFrom(now.Add(-time.Second))
	require.Error(t, code.Validate(), "recovery code accepted consumed_at before created_at")
}
