//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package commands

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/store/sqlstore"
)

func TestMigrateIntegration(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	require.NotEmpty(t, dataSource, "PROCTOR_TEST_DATABASE_URL is required for integration tests")
	t.Setenv("PROCTOR_DATABASE_DATA_SOURCE", dataSource)
	cfg, err := json.Marshal(config.Default())
	require.NoError(t, err)
	configPath := filepath.Join(t.TempDir(), "config.json")
	err = os.WriteFile(configPath, cfg, 0o600)
	require.NoError(t, err)

	code, stdout, stderr := executeForTest(context.Background(), []string{"--config", configPath, "migrate", "up"}, nil, productionExecutors())
	require.Equal(t, 0, code, "migrate up code=%d stderr=%q", code, stderr)
	version, err := sqlstore.LocalSchemaVersion()
	require.NoError(t, err)
	require.Contains(t, stdout, "version "+strconv.Itoa(version))

	code, stdout, stderr = executeForTest(context.Background(), []string{"--config", configPath, "migrate", "status"}, nil, productionExecutors())
	require.Equal(t, 0, code, "migrate status code=%d stderr=%q", code, stderr)
	require.Contains(t, stdout, "pending migrations 0")
}
