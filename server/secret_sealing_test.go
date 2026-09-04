// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package server

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/secretseal"
)

func TestMailSecretSealingProjectionIsOptionalAndCopiesTheKeyRing(t *testing.T) {
	t.Parallel()

	settings, configured := mailSecretSealingSettings(config.Default().Mail)
	if configured || settings.EncryptionKey != "" || len(settings.DecryptionKeys) != 0 ||
		settings.MaximumPlaintext != 0 {
		t.Fatalf("disabled default projection = %#v, %t", settings, configured)
	}
	sealer, err := newMailSecretSealer(config.Default().Mail)
	if err != nil || sealer != nil {
		t.Fatalf("newMailSecretSealer(disabled default) = %v, %v", sealer, err)
	}

	mailConfig := config.Default().Mail
	mailConfig.SecretSealing.EncryptionKey = base64.StdEncoding.EncodeToString(
		[]byte(strings.Repeat("p", 32)),
	)
	mailConfig.SecretSealing.DecryptionKeys = []string{base64.StdEncoding.EncodeToString(
		[]byte(strings.Repeat("f", 32)),
	)}
	settings, configured = mailSecretSealingSettings(mailConfig)
	if !configured || settings.MaximumPlaintext != secretseal.MaximumPlaintextBytes {
		t.Fatalf("configured projection = %#v, %t", settings, configured)
	}
	mailConfig.SecretSealing.DecryptionKeys[0] = "mutated"
	if settings.DecryptionKeys[0] == "mutated" {
		t.Fatal("mail key-ring projection retained the configuration slice")
	}
	mailConfig.SecretSealing.DecryptionKeys[0] = settings.DecryptionKeys[0]
	sealer, err = newMailSecretSealer(mailConfig)
	if err != nil {
		t.Fatalf("newMailSecretSealer(configured) = %v", err)
	}
	binding := secretseal.Binding{Purpose: "mail.delivery", Owner: "delivery-projection"}
	envelope, err := sealer.Seal(binding, []byte("projected secret"))
	if err != nil {
		t.Fatal(err)
	}
	opened, err := sealer.Open(binding, envelope)
	if err != nil || string(opened) != "projected secret" {
		t.Fatalf("Open(projected envelope) = %q, %v", opened, err)
	}
	mailConfig.SecretSealing.EncryptionKey = "credential-that-must-not-leak"
	if _, err := newMailSecretSealer(mailConfig); err == nil ||
		strings.Contains(err.Error(), mailConfig.SecretSealing.EncryptionKey) {
		t.Fatalf("newMailSecretSealer(invalid) error = %v", err)
	}
}

func TestMFASecretSealingProjectionIsIndependentAndBounded(t *testing.T) {
	t.Parallel()

	settings, configured := mfaSecretSealingSettings(config.Default().Authentication.MFA)
	if configured || settings.EncryptionKey != "" || len(settings.DecryptionKeys) != 0 ||
		settings.MaximumPlaintext != 0 {
		t.Fatalf("disabled default projection = %#v, %t", settings, configured)
	}
	sealer, err := newMFASecretSealer(config.Default().Authentication.MFA)
	if err != nil || sealer != nil {
		t.Fatalf("newMFASecretSealer(disabled default) = %v, %v", sealer, err)
	}

	mfaConfig := config.Default().Authentication.MFA
	mfaConfig.EncryptionKey = base64.StdEncoding.EncodeToString([]byte(strings.Repeat("m", 32)))
	mfaConfig.DecryptionKeys = []string{base64.StdEncoding.EncodeToString([]byte(strings.Repeat("f", 32)))}
	settings, configured = mfaSecretSealingSettings(mfaConfig)
	if !configured || settings.MaximumPlaintext != mfaSecretMaximumPlaintextBytes {
		t.Fatalf("configured projection = %#v, %t", settings, configured)
	}
	mfaConfig.DecryptionKeys[0] = "mutated"
	if settings.DecryptionKeys[0] == "mutated" {
		t.Fatal("MFA key-ring projection retained the configuration slice")
	}
	mfaConfig.DecryptionKeys[0] = settings.DecryptionKeys[0]
	sealer, err = newMFASecretSealer(mfaConfig)
	if err != nil {
		t.Fatal(err)
	}
	binding := secretseal.Binding{Purpose: "mfa.totp", Owner: "user-projection"}
	envelope, err := sealer.Seal(binding, []byte("TOTPSECRET"))
	if err != nil {
		t.Fatal(err)
	}
	opened, err := sealer.Open(binding, envelope)
	if err != nil || string(opened) != "TOTPSECRET" {
		t.Fatalf("Open(projected MFA envelope) = %q, %v", opened, err)
	}
}
