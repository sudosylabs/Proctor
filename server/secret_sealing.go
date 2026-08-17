// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"fmt"

	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/secretseal"
)

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
