// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package secretseal_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/secretseal"
)

func TestSealerRoundTripUsesVersionedNondeterministicEnvelope(t *testing.T) {
	t.Parallel()

	settings := secretseal.Settings{
		EncryptionKey:    encodedKey(1),
		MaximumPlaintext: 1024,
	}
	sealer, err := secretseal.New(settings)
	require.NoError(t, err)
	binding := secretseal.Binding{Purpose: "mail.delivery", Owner: "delivery-01"}
	plaintext := []byte("recoverable application secret")

	first, err := sealer.Seal(binding, plaintext)
	require.NoError(t, err)
	second, err := sealer.Seal(binding, plaintext)
	require.NoError(t, err)
	require.Equal(t, secretseal.EnvelopeVersion1, first.Version)
	require.Equal(t, secretseal.AlgorithmAES256GCM, first.Algorithm)
	require.NotEmpty(t, first.KeyID)
	require.NotEmpty(t, first.Nonce)
	require.NotEmpty(t, first.Ciphertext)
	require.NotEqual(t, second.Nonce, first.Nonce, "Seal() reused nonce or produced deterministic ciphertext")
	require.NotEqual(t, second.Ciphertext, first.Ciphertext, "Seal() reused nonce or produced deterministic ciphertext")
	opened, err := sealer.Open(binding, first)
	require.NoError(t, err)
	require.True(t, bytes.Equal(opened, plaintext), "Open() = %q, want %q", opened, plaintext)

	encoded, err := json.Marshal(first)
	require.NoError(t, err)
	for _, field := range []string{"version", "algorithm", "key_id", "nonce", "ciphertext"} {
		require.True(t, bytes.Contains(encoded, []byte(`"`+field+`"`)), "envelope JSON lacks %q: %s", field, encoded)
	}
	require.False(t, bytes.Contains(encoded, plaintext), "envelope JSON exposed plaintext: %s", encoded)
	require.False(t, bytes.Contains(encoded, []byte(binding.Purpose)))
	require.False(t, bytes.Contains(encoded, []byte(binding.Owner)))
	var logOutput bytes.Buffer
	slog.New(slog.NewJSONHandler(&logOutput, nil)).Info("sealed", "envelope", first)
	diagnostics := fmt.Sprintf("%v\n%+v\n%#v\n%s", first, first, first, logOutput.String())
	for _, forbidden := range [][]byte{plaintext, []byte(first.Nonce), []byte(first.Ciphertext)} {
		require.False(t, bytes.Contains([]byte(diagnostics), forbidden), "envelope diagnostics exposed sealed material: %s", diagnostics)
	}
}

func TestSealerProjectsOnlyPrimaryKeyIdentity(t *testing.T) {
	sealer, err := secretseal.New(secretseal.Settings{EncryptionKey: encodedKey(9), MaximumPlaintext: 1024})
	require.NoError(t, err)
	envelope, err := sealer.Seal(secretseal.Binding{Purpose: "mail.delivery", Owner: "delivery_01"}, []byte("payload"))
	require.NoError(t, err)
	require.Equal(t, envelope.KeyID, sealer.PrimaryKeyID(), "PrimaryKeyID()")
	require.NotEmpty(t, sealer.PrimaryKeyID(), "PrimaryKeyID()")
	require.Empty(t, (*secretseal.Sealer)(nil).PrimaryKeyID(), "nil sealer exposed a primary key identity")
}

func TestOpenRejectsWrongBindingAndInvalidEnvelopeWithSafeErrors(t *testing.T) {
	t.Parallel()

	const plaintext = "credential-that-must-not-leak"
	sealer, err := secretseal.New(secretseal.Settings{
		EncryptionKey:    encodedKey(2),
		MaximumPlaintext: 1024,
	})
	require.NoError(t, err)
	binding := secretseal.Binding{Purpose: "mail.delivery", Owner: "delivery-02"}
	envelope, err := sealer.Seal(binding, []byte(plaintext))
	require.NoError(t, err)

	tests := []struct {
		name    string
		binding secretseal.Binding
		mutate  func(secretseal.Envelope) secretseal.Envelope
	}{
		{name: "wrong purpose", binding: secretseal.Binding{Purpose: "mail.bundle", Owner: binding.Owner}},
		{name: "wrong owner", binding: secretseal.Binding{Purpose: binding.Purpose, Owner: "delivery-03"}},
		{name: "unknown version", binding: binding, mutate: func(value secretseal.Envelope) secretseal.Envelope { value.Version++; return value }},
		{name: "unknown algorithm", binding: binding, mutate: func(value secretseal.Envelope) secretseal.Envelope { value.Algorithm = "unknown"; return value }},
		{name: "unknown key", binding: binding, mutate: func(value secretseal.Envelope) secretseal.Envelope {
			value.KeyID = strings.Repeat("0", len(value.KeyID))
			return value
		}},
		{name: "invalid nonce encoding", binding: binding, mutate: func(value secretseal.Envelope) secretseal.Envelope { value.Nonce = "***"; return value }},
		{name: "truncated nonce", binding: binding, mutate: func(value secretseal.Envelope) secretseal.Envelope {
			value.Nonce = base64.RawURLEncoding.EncodeToString([]byte("short"))
			return value
		}},
		{name: "truncated ciphertext", binding: binding, mutate: func(value secretseal.Envelope) secretseal.Envelope {
			value.Ciphertext = base64.RawURLEncoding.EncodeToString([]byte("short"))
			return value
		}},
		{name: "corrupted ciphertext", binding: binding, mutate: func(value secretseal.Envelope) secretseal.Envelope {
			decoded, _ := base64.RawURLEncoding.DecodeString(value.Ciphertext)
			decoded[len(decoded)-1] ^= 1
			value.Ciphertext = base64.RawURLEncoding.EncodeToString(decoded)
			return value
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			candidate := envelope
			if test.mutate != nil {
				candidate = test.mutate(candidate)
			}
			_, openErr := sealer.Open(test.binding, candidate)
			require.ErrorIs(t, openErr, secretseal.ErrInvalidEnvelope, "Open() error")
			require.NotContains(t, openErr.Error(), plaintext)
			require.NotContains(t, openErr.Error(), settingsKeyMaterial(2))
			require.NotContains(t, openErr.Error(), binding.Owner)
		})
	}
}

func TestHasKeyExposesOnlyRingMembership(t *testing.T) {
	sealer, err := secretseal.New(secretseal.Settings{EncryptionKey: encodedKey(1), MaximumPlaintext: 1024})
	require.NoError(t, err)
	envelope, err := sealer.Seal(secretseal.Binding{Purpose: "mail.payload", Owner: "owner"}, []byte("secret"))
	require.NoError(t, err)
	require.True(t, sealer.HasKey(envelope.KeyID), "HasKey did not report exact configured-key membership")
	require.False(t, sealer.HasKey(strings.Repeat("0", 32)), "HasKey did not report exact configured-key membership")
	require.False(t, (*secretseal.Sealer)(nil).HasKey(envelope.KeyID), "HasKey did not report exact configured-key membership")
}

func TestFallbackKeyReadsSupportPrimaryRotation(t *testing.T) {
	t.Parallel()

	oldKey := encodedKey(3)
	newKey := encodedKey(4)
	oldSealer, err := secretseal.New(secretseal.Settings{
		EncryptionKey: oldKey, MaximumPlaintext: 1024,
	})
	require.NoError(t, err)
	rotated, err := secretseal.New(secretseal.Settings{
		EncryptionKey: newKey, DecryptionKeys: []string{oldKey}, MaximumPlaintext: 1024,
	})
	require.NoError(t, err)
	binding := secretseal.Binding{Purpose: "mail.bundle", Owner: "occurrence-01"}
	oldEnvelope, err := oldSealer.Seal(binding, []byte("before rotation"))
	require.NoError(t, err)
	opened, err := rotated.Open(binding, oldEnvelope)
	require.NoError(t, err, "Open(old envelope)")
	require.Equal(t, "before rotation", string(opened), "Open(old envelope)")
	newEnvelope, err := rotated.Seal(binding, []byte("after rotation"))
	require.NoError(t, err)
	require.NotEqual(t, oldEnvelope.KeyID, newEnvelope.KeyID, "rotated primary did not change the envelope key identity")
	_, err = oldSealer.Open(binding, newEnvelope)
	require.ErrorIs(t, err, secretseal.ErrInvalidEnvelope, "old key ring Open(new envelope) error")
}

func TestSettingsRejectInvalidKeysBindingsAndBounds(t *testing.T) {
	t.Parallel()

	valid := secretseal.Settings{EncryptionKey: encodedKey(5), MaximumPlaintext: 32}
	tests := []struct {
		name     string
		settings secretseal.Settings
	}{
		{name: "missing primary", settings: secretseal.Settings{MaximumPlaintext: 32}},
		{name: "non canonical primary", settings: secretseal.Settings{EncryptionKey: encodedKey(5) + "\n", MaximumPlaintext: 32}},
		{name: "oversized encoded primary", settings: secretseal.Settings{EncryptionKey: strings.Repeat("A", 1<<20), MaximumPlaintext: 32}},
		{name: "short primary", settings: secretseal.Settings{EncryptionKey: base64.StdEncoding.EncodeToString(make([]byte, 31)), MaximumPlaintext: 32}},
		{name: "duplicate decoded key", settings: secretseal.Settings{EncryptionKey: encodedKey(5), DecryptionKeys: []string{encodedKey(5)}, MaximumPlaintext: 32}},
		{name: "too many fallbacks", settings: secretseal.Settings{EncryptionKey: encodedKey(5), DecryptionKeys: repeatedKeys(secretseal.MaximumFallbackKeys + 1), MaximumPlaintext: 32}},
		{name: "zero plaintext bound", settings: secretseal.Settings{EncryptionKey: encodedKey(5)}},
		{name: "excessive plaintext bound", settings: secretseal.Settings{EncryptionKey: encodedKey(5), MaximumPlaintext: secretseal.MaximumPlaintextBytes + 1}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := secretseal.New(test.settings)
			require.ErrorIs(t, err, secretseal.ErrInvalidSettings, "New() error")
		})
	}
	invalidKey := settingsKeyMaterial(9)
	_, err := secretseal.New(secretseal.Settings{
		EncryptionKey: invalidKey, MaximumPlaintext: 32,
	})
	require.ErrorIs(t, err, secretseal.ErrInvalidSettings)
	require.NotContains(t, err.Error(), invalidKey)

	sealer, err := secretseal.New(valid)
	require.NoError(t, err)
	validBinding := secretseal.Binding{Purpose: "mail.delivery", Owner: "delivery-04"}
	_, err = sealer.Seal(validBinding, make([]byte, 33))
	require.ErrorIs(t, err, secretseal.ErrPlaintextTooLarge, "Seal(oversize) error")
	for _, binding := range []secretseal.Binding{
		{},
		{Purpose: "Mail Delivery", Owner: "delivery-04"},
		{Purpose: "mail.delivery", Owner: ""},
		{Purpose: "mail.delivery", Owner: "unsafe\nowner"},
	} {
		_, err := sealer.Seal(binding, []byte("secret"))
		require.ErrorIs(t, err, secretseal.ErrInvalidBinding, "Seal(%#v) error", binding)
	}
}

func TestSettingsAndSealerExcludeKeysFromDiagnosticRepresentations(t *testing.T) {
	t.Parallel()

	settings := secretseal.Settings{
		EncryptionKey: encodedKey(7), DecryptionKeys: []string{encodedKey(8)}, MaximumPlaintext: 64,
	}
	sealer, err := secretseal.New(settings)
	require.NoError(t, err)
	jsonSettings, err := json.Marshal(settings)
	require.NoError(t, err)
	jsonSealer, err := json.Marshal(sealer)
	require.NoError(t, err)
	var logOutput bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logOutput, nil))
	logger.Info("configuration", "settings", settings, "sealer", sealer)
	diagnostics := strings.Join([]string{
		string(jsonSettings), string(jsonSealer), fmt.Sprintf("%v", settings),
		fmt.Sprintf("%+v", settings), fmt.Sprintf("%#v", settings), logOutput.String(),
	}, "\n")
	for _, forbidden := range []string{settings.EncryptionKey, settings.DecryptionKeys[0]} {
		require.NotContains(t, diagnostics, forbidden, "diagnostics exposed key material")
	}
}

func TestSettingsAndEnvelopeExposeOnlySafeAuditFields(t *testing.T) {
	t.Parallel()

	settings := secretseal.Settings{
		EncryptionKey: encodedKey(10), DecryptionKeys: []string{encodedKey(11)}, MaximumPlaintext: 128,
	}
	sealer, err := secretseal.New(settings)
	require.NoError(t, err)
	binding := secretseal.Binding{Purpose: "mail.delivery", Owner: "delivery-audit"}
	plaintext := []byte("audit-secret-must-not-leak")
	envelope, err := sealer.Seal(binding, plaintext)
	require.NoError(t, err)
	var _ model.Auditable = settings
	var _ model.Auditable = envelope
	encoded, err := json.Marshal(map[string]any{
		"settings": settings.Auditable(),
		"envelope": envelope.Auditable(),
	})
	require.NoError(t, err)
	for _, forbidden := range [][]byte{
		[]byte(settings.EncryptionKey), []byte(settings.DecryptionKeys[0]),
		plaintext, []byte(envelope.Nonce), []byte(envelope.Ciphertext),
		[]byte(binding.Purpose), []byte(binding.Owner),
	} {
		require.False(t, bytes.Contains(encoded, forbidden), "audit fields exposed secret material: %s", encoded)
	}
	for _, safe := range []string{"version", "algorithm", "key_id", "maximum_plaintext"} {
		require.True(t, bytes.Contains(encoded, []byte(`"`+safe+`"`)), "audit fields lack safe metadata %q: %s", safe, encoded)
	}
}

func encodedKey(seed byte) string {
	return base64.StdEncoding.EncodeToString([]byte(settingsKeyMaterial(seed)))
}

func settingsKeyMaterial(seed byte) string {
	return strings.Repeat(string([]byte{seed}), 32)
}

func repeatedKeys(count int) []string {
	keys := make([]string, count)
	for index := range keys {
		keys[index] = encodedKey(byte(index + 20))
	}
	return keys
}
