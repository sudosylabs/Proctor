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

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/secretseal"
)

func TestMailSecretSealingProjectionIsOptionalAndCopiesTheKeyRing(t *testing.T) {
	t.Parallel()

	settings, configured := mailSecretSealingSettings(config.Default().Mail)
	require.False(t, configured, "disabled default projection = %#v", settings)
	require.Empty(t, settings.EncryptionKey, "disabled default projection = %#v", settings)
	require.Empty(t, settings.DecryptionKeys, "disabled default projection = %#v", settings)
	require.Equal(t, 0, settings.MaximumPlaintext, "disabled default projection = %#v", settings)
	sealer, err := newMailSecretSealer(config.Default().Mail)
	require.NoError(t, err, "newMailSecretSealer(disabled default)")
	require.Nil(t, sealer, "newMailSecretSealer(disabled default)")

	mailConfig := config.Default().Mail
	mailConfig.SecretSealing.EncryptionKey = base64.StdEncoding.EncodeToString(
		[]byte(strings.Repeat("p", 32)),
	)
	mailConfig.SecretSealing.DecryptionKeys = []string{base64.StdEncoding.EncodeToString(
		[]byte(strings.Repeat("f", 32)),
	)}
	settings, configured = mailSecretSealingSettings(mailConfig)
	require.True(t, configured, "configured projection = %#v", settings)
	require.Equal(t, secretseal.MaximumPlaintextBytes, settings.MaximumPlaintext, "configured projection = %#v", settings)
	mailConfig.SecretSealing.DecryptionKeys[0] = "mutated"
	require.NotEqual(t, "mutated", settings.DecryptionKeys[0], "mail key-ring projection retained the configuration slice")
	mailConfig.SecretSealing.DecryptionKeys[0] = settings.DecryptionKeys[0]
	sealer, err = newMailSecretSealer(mailConfig)
	require.NoError(t, err)
	binding := secretseal.Binding{Purpose: "mail.delivery", Owner: "delivery-projection"}
	envelope, err := sealer.Seal(binding, []byte("projected secret"))
	require.NoError(t, err)
	opened, err := sealer.Open(binding, envelope)
	require.NoError(t, err, "Open(projected envelope)")
	require.Equal(t, "projected secret", string(opened), "Open(projected envelope)")
	mailConfig.SecretSealing.EncryptionKey = "credential-that-must-not-leak"
	_, err = newMailSecretSealer(mailConfig)
	require.Error(t, err)
	require.NotContains(t, err.Error(), mailConfig.SecretSealing.EncryptionKey)
}

func TestMFASecretSealingProjectionIsIndependentAndBounded(t *testing.T) {
	t.Parallel()

	settings, configured := mfaSecretSealingSettings(config.Default().Authentication.MFA)
	require.False(t, configured, "disabled default projection = %#v", settings)
	require.Empty(t, settings.EncryptionKey, "disabled default projection = %#v", settings)
	require.Empty(t, settings.DecryptionKeys, "disabled default projection = %#v", settings)
	require.Equal(t, 0, settings.MaximumPlaintext, "disabled default projection = %#v", settings)
	sealer, err := newMFASecretSealer(config.Default().Authentication.MFA)
	require.NoError(t, err, "newMFASecretSealer(disabled default)")
	require.Nil(t, sealer, "newMFASecretSealer(disabled default)")

	mfaConfig := config.Default().Authentication.MFA
	mfaConfig.EncryptionKey = base64.StdEncoding.EncodeToString([]byte(strings.Repeat("m", 32)))
	mfaConfig.DecryptionKeys = []string{base64.StdEncoding.EncodeToString([]byte(strings.Repeat("f", 32)))}
	settings, configured = mfaSecretSealingSettings(mfaConfig)
	require.True(t, configured, "configured projection = %#v", settings)
	require.Equal(t, mfaSecretMaximumPlaintextBytes, settings.MaximumPlaintext, "configured projection = %#v", settings)
	mfaConfig.DecryptionKeys[0] = "mutated"
	require.NotEqual(t, "mutated", settings.DecryptionKeys[0], "MFA key-ring projection retained the configuration slice")
	mfaConfig.DecryptionKeys[0] = settings.DecryptionKeys[0]
	sealer, err = newMFASecretSealer(mfaConfig)
	require.NoError(t, err)
	binding := secretseal.Binding{Purpose: "mfa.totp", Owner: "user-projection"}
	envelope, err := sealer.Seal(binding, []byte("TOTPSECRET"))
	require.NoError(t, err)
	opened, err := sealer.Open(binding, envelope)
	require.NoError(t, err, "Open(projected MFA envelope)")
	require.Equal(t, "TOTPSECRET", string(opened), "Open(projected MFA envelope)")
}
