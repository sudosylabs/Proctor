// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app_test

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/testlib"
)

func TestProductionGraphConstructsWithIndependentMailSecretKeyRing(t *testing.T) {
	t.Parallel()

	primary := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("p", 32)))
	fallback := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("f", 32)))
	helper := testlib.Setup(t, testlib.WithConfig(func(cfg *config.Config) {
		cfg.Mail.SecretSealing.EncryptionKey = primary
		cfg.Mail.SecretSealing.DecryptionKeys = []string{fallback}
	}))
	if helper.Server == nil || helper.App == nil {
		t.Fatal("configured mail secret key ring did not construct the production graph")
	}
	effective := helper.ConfigStore.Get()
	if effective.Mail.SecretSealing.EncryptionKey != primary ||
		len(effective.Mail.SecretSealing.DecryptionKeys) != 1 ||
		effective.Mail.SecretSealing.DecryptionKeys[0] != fallback {
		t.Fatal("production graph did not retain the configured mail key ring")
	}
}
