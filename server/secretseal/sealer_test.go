// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package secretseal_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

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
	if err != nil {
		t.Fatal(err)
	}
	binding := secretseal.Binding{Purpose: "mail.delivery", Owner: "delivery-01"}
	plaintext := []byte("recoverable application secret")

	first, err := sealer.Seal(binding, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	second, err := sealer.Seal(binding, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if first.Version != secretseal.EnvelopeVersion1 ||
		first.Algorithm != secretseal.AlgorithmAES256GCM ||
		first.KeyID == "" || first.Nonce == "" || first.Ciphertext == "" {
		t.Fatalf("Seal() envelope = %#v", first)
	}
	if first.Nonce == second.Nonce || first.Ciphertext == second.Ciphertext {
		t.Fatal("Seal() reused nonce or produced deterministic ciphertext")
	}
	opened, err := sealer.Open(binding, first)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(opened, plaintext) {
		t.Fatalf("Open() = %q, want %q", opened, plaintext)
	}

	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"version", "algorithm", "key_id", "nonce", "ciphertext"} {
		if !bytes.Contains(encoded, []byte(`"`+field+`"`)) {
			t.Fatalf("envelope JSON lacks %q: %s", field, encoded)
		}
	}
	if bytes.Contains(encoded, plaintext) {
		t.Fatalf("envelope JSON exposed plaintext: %s", encoded)
	}
	if bytes.Contains(encoded, []byte(binding.Purpose)) || bytes.Contains(encoded, []byte(binding.Owner)) {
		t.Fatalf("envelope JSON exposed its authenticated binding: %s", encoded)
	}
	var logOutput bytes.Buffer
	slog.New(slog.NewJSONHandler(&logOutput, nil)).Info("sealed", "envelope", first)
	diagnostics := fmt.Sprintf("%v\n%+v\n%#v\n%s", first, first, first, logOutput.String())
	for _, forbidden := range [][]byte{plaintext, []byte(first.Nonce), []byte(first.Ciphertext)} {
		if bytes.Contains([]byte(diagnostics), forbidden) {
			t.Fatalf("envelope diagnostics exposed sealed material: %s", diagnostics)
		}
	}
}

func TestOpenRejectsWrongBindingAndInvalidEnvelopeWithSafeErrors(t *testing.T) {
	t.Parallel()

	const plaintext = "credential-that-must-not-leak"
	sealer, err := secretseal.New(secretseal.Settings{
		EncryptionKey:    encodedKey(2),
		MaximumPlaintext: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	binding := secretseal.Binding{Purpose: "mail.delivery", Owner: "delivery-02"}
	envelope, err := sealer.Seal(binding, []byte(plaintext))
	if err != nil {
		t.Fatal(err)
	}

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
			if !errors.Is(openErr, secretseal.ErrInvalidEnvelope) {
				t.Fatalf("Open() error = %v, want ErrInvalidEnvelope", openErr)
			}
			if strings.Contains(openErr.Error(), plaintext) ||
				strings.Contains(openErr.Error(), settingsKeyMaterial(2)) ||
				strings.Contains(openErr.Error(), binding.Owner) {
				t.Fatalf("Open() exposed secret or binding data: %v", openErr)
			}
		})
	}
}

func TestFallbackKeyReadsSupportPrimaryRotation(t *testing.T) {
	t.Parallel()

	oldKey := encodedKey(3)
	newKey := encodedKey(4)
	oldSealer, err := secretseal.New(secretseal.Settings{
		EncryptionKey: oldKey, MaximumPlaintext: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := secretseal.New(secretseal.Settings{
		EncryptionKey: newKey, DecryptionKeys: []string{oldKey}, MaximumPlaintext: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	binding := secretseal.Binding{Purpose: "mail.bundle", Owner: "occurrence-01"}
	oldEnvelope, err := oldSealer.Seal(binding, []byte("before rotation"))
	if err != nil {
		t.Fatal(err)
	}
	opened, err := rotated.Open(binding, oldEnvelope)
	if err != nil || string(opened) != "before rotation" {
		t.Fatalf("Open(old envelope) = %q, %v", opened, err)
	}
	newEnvelope, err := rotated.Seal(binding, []byte("after rotation"))
	if err != nil {
		t.Fatal(err)
	}
	if newEnvelope.KeyID == oldEnvelope.KeyID {
		t.Fatal("rotated primary did not change the envelope key identity")
	}
	if _, err := oldSealer.Open(binding, newEnvelope); !errors.Is(err, secretseal.ErrInvalidEnvelope) {
		t.Fatalf("old key ring Open(new envelope) error = %v, want ErrInvalidEnvelope", err)
	}
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
			if _, err := secretseal.New(test.settings); !errors.Is(err, secretseal.ErrInvalidSettings) {
				t.Fatalf("New() error = %v, want ErrInvalidSettings", err)
			}
		})
	}
	invalidKey := settingsKeyMaterial(9)
	if _, err := secretseal.New(secretseal.Settings{
		EncryptionKey: invalidKey, MaximumPlaintext: 32,
	}); !errors.Is(err, secretseal.ErrInvalidSettings) || strings.Contains(err.Error(), invalidKey) {
		t.Fatalf("New(invalid key) error = %v", err)
	}

	sealer, err := secretseal.New(valid)
	if err != nil {
		t.Fatal(err)
	}
	validBinding := secretseal.Binding{Purpose: "mail.delivery", Owner: "delivery-04"}
	if _, err := sealer.Seal(validBinding, make([]byte, 33)); !errors.Is(err, secretseal.ErrPlaintextTooLarge) {
		t.Fatalf("Seal(oversize) error = %v, want ErrPlaintextTooLarge", err)
	}
	for _, binding := range []secretseal.Binding{
		{},
		{Purpose: "Mail Delivery", Owner: "delivery-04"},
		{Purpose: "mail.delivery", Owner: ""},
		{Purpose: "mail.delivery", Owner: "unsafe\nowner"},
	} {
		if _, err := sealer.Seal(binding, []byte("secret")); !errors.Is(err, secretseal.ErrInvalidBinding) {
			t.Fatalf("Seal(%#v) error = %v, want ErrInvalidBinding", binding, err)
		}
	}
}

func TestSettingsAndSealerExcludeKeysFromDiagnosticRepresentations(t *testing.T) {
	t.Parallel()

	settings := secretseal.Settings{
		EncryptionKey: encodedKey(7), DecryptionKeys: []string{encodedKey(8)}, MaximumPlaintext: 64,
	}
	sealer, err := secretseal.New(settings)
	if err != nil {
		t.Fatal(err)
	}
	jsonSettings, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	jsonSealer, err := json.Marshal(sealer)
	if err != nil {
		t.Fatal(err)
	}
	var logOutput bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logOutput, nil))
	logger.Info("configuration", "settings", settings, "sealer", sealer)
	diagnostics := strings.Join([]string{
		string(jsonSettings), string(jsonSealer), fmt.Sprintf("%v", settings),
		fmt.Sprintf("%+v", settings), fmt.Sprintf("%#v", settings), logOutput.String(),
	}, "\n")
	for _, forbidden := range []string{settings.EncryptionKey, settings.DecryptionKeys[0]} {
		if strings.Contains(diagnostics, forbidden) {
			t.Fatalf("diagnostics exposed key material %q: %s", forbidden, diagnostics)
		}
	}
}

func TestSettingsAndEnvelopeExposeOnlySafeAuditFields(t *testing.T) {
	t.Parallel()

	settings := secretseal.Settings{
		EncryptionKey: encodedKey(10), DecryptionKeys: []string{encodedKey(11)}, MaximumPlaintext: 128,
	}
	sealer, err := secretseal.New(settings)
	if err != nil {
		t.Fatal(err)
	}
	binding := secretseal.Binding{Purpose: "mail.delivery", Owner: "delivery-audit"}
	plaintext := []byte("audit-secret-must-not-leak")
	envelope, err := sealer.Seal(binding, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	var _ model.Auditable = settings
	var _ model.Auditable = envelope
	encoded, err := json.Marshal(map[string]any{
		"settings": settings.Auditable(),
		"envelope": envelope.Auditable(),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range [][]byte{
		[]byte(settings.EncryptionKey), []byte(settings.DecryptionKeys[0]),
		plaintext, []byte(envelope.Nonce), []byte(envelope.Ciphertext),
		[]byte(binding.Purpose), []byte(binding.Owner),
	} {
		if bytes.Contains(encoded, forbidden) {
			t.Fatalf("audit fields exposed secret material: %s", encoded)
		}
	}
	for _, safe := range []string{"version", "algorithm", "key_id", "maximum_plaintext"} {
		if !bytes.Contains(encoded, []byte(`"`+safe+`"`)) {
			t.Fatalf("audit fields lack safe metadata %q: %s", safe, encoded)
		}
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
