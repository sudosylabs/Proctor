// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app_test

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

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
	require.NotNil(t, helper.Server, "configured mail secret key ring did not construct the production graph")
	require.NotNil(t, helper.App, "configured mail secret key ring did not construct the production graph")
	effective := helper.ConfigStore.Get()
	require.Equal(t, primary, effective.Mail.SecretSealing.EncryptionKey, "production graph did not retain the configured mail key ring")
	require.Len(t, effective.Mail.SecretSealing.DecryptionKeys, 1, "production graph did not retain the configured mail key ring")
	require.Equal(t, fallback, effective.Mail.SecretSealing.DecryptionKeys[0], "production graph did not retain the configured mail key ring")
}
