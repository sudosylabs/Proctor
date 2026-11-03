// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package server_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server"
	"github.com/sudosylabs/proctor/server/config"
)

func TestShowEffectiveConfigurationRedactsTheFileAndEnvironmentOverlay(t *testing.T) {
	// #nosec G101 -- Synthetic data source whose password the redaction must remove.
	const fileDataSource = "postgres://proctor:file-database-password@db.internal/proctor"
	const environmentSMTPPassword = "environment-smtp-password"
	t.Setenv("PROCTOR_MAIL_SMTP_PASSWORD", environmentSMTPPassword)

	cfg := config.Default()
	cfg.Database.DataSource = fileDataSource
	data, err := json.Marshal(cfg)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, data, 0o600))

	shown, err := server.ShowEffectiveConfiguration(context.Background(), path)
	require.NoError(t, err)

	require.NotContains(t, string(shown.Document), "file-database-password")
	require.NotContains(t, string(shown.Document), environmentSMTPPassword)
	var document config.Config
	require.NoError(t, json.Unmarshal(shown.Document, &document))
	require.Equal(t, "[redacted]", document.Database.DataSource)
	require.Equal(t, "[redacted]", document.Mail.SMTP.Password)
	require.Contains(t, shown.EnvironmentOverrides, "PROCTOR_MAIL_SMTP_PASSWORD")
	unchanged, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, data, unchanged, "showing the configuration rewrote its file")
}

func TestShowEffectiveConfigurationRejectsMissingFile(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "missing.json")
	_, err := server.ShowEffectiveConfiguration(context.Background(), missing)
	require.ErrorContains(t, err, "read configuration")
	_, statErr := os.Stat(missing)
	require.True(t, os.IsNotExist(statErr), "missing configuration was created")
}
