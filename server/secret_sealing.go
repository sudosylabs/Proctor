// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package server

import (
	"fmt"

	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/secretseal"
)

const mfaSecretMaximumPlaintextBytes = 128

// mailSecretSealingSettings is the sole projection of deployment-owned mail
// keys into cryptographic mechanics. An absent ring deliberately projects to
// nil; any configured ring is constructed and validated during composition.
func mailSecretSealingSettings(mailConfig config.Mail) (secretseal.Settings, bool) {
	keys := mailConfig.SecretSealing
	if keys.EncryptionKey == "" && len(keys.DecryptionKeys) == 0 {
		return secretseal.Settings{}, false
	}
	return secretseal.Settings{
		EncryptionKey:    keys.EncryptionKey,
		DecryptionKeys:   append([]string(nil), keys.DecryptionKeys...),
		MaximumPlaintext: secretseal.MaximumPlaintextBytes,
	}, true
}

func newMailSecretSealer(mailConfig config.Mail) (*secretseal.Sealer, error) {
	settings, configured := mailSecretSealingSettings(mailConfig)
	if !configured {
		return nil, nil
	}
	sealer, err := secretseal.New(settings)
	if err != nil {
		return nil, fmt.Errorf("configure mail secret sealing: %w", err)
	}
	return sealer, nil
}

// mfaSecretSealingSettings projects the independent MFA key ring into the
// shared cryptographic module without exposing deployment configuration to the
// application package.
func mfaSecretSealingSettings(mfaConfig config.MFA) (secretseal.Settings, bool) {
	if mfaConfig.EncryptionKey == "" && len(mfaConfig.DecryptionKeys) == 0 {
		return secretseal.Settings{}, false
	}
	return secretseal.Settings{
		EncryptionKey:    mfaConfig.EncryptionKey,
		DecryptionKeys:   append([]string(nil), mfaConfig.DecryptionKeys...),
		MaximumPlaintext: mfaSecretMaximumPlaintextBytes,
	}, true
}

func newMFASecretSealer(mfaConfig config.MFA) (*secretseal.Sealer, error) {
	settings, configured := mfaSecretSealingSettings(mfaConfig)
	if !configured {
		return nil, nil
	}
	sealer, err := secretseal.New(settings)
	if err != nil {
		return nil, fmt.Errorf("configure MFA secret sealing: %w", err)
	}
	return sealer, nil
}
