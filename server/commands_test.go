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
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server"
	"github.com/sudosylabs/proctor/server/config"
)

func TestValidateConfigUsesEnvironmentConfigurationPath(t *testing.T) {
	path := writeValidConfig(t)
	t.Setenv(server.ConfigPathEnv, path)

	require.NoError(t, server.ValidateConfig(context.Background(), ""), "ValidateConfig() error")
}

func TestExplicitConfigurationPathTakesPrecedenceOverEnvironment(t *testing.T) {
	t.Setenv(server.ConfigPathEnv, filepath.Join(t.TempDir(), "missing.json"))

	require.NoError(t, server.ValidateConfig(context.Background(), writeValidConfig(t)), "ValidateConfig() error")
}

func TestValidateConfigRejectsMissingFile(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "missing.json")
	err := server.ValidateConfig(context.Background(), missing)
	require.Error(t, err, "ValidateConfig() error")
	require.Contains(t, err.Error(), "copy config/config.example.json", "ValidateConfig() error")
	require.Contains(t, err.Error(), "read configuration", "ValidateConfig() error")
	_, statErr := os.Stat(missing)
	require.True(t, os.IsNotExist(statErr), "missing configuration was created")
}

func TestValidateConfigRejectsMalformedFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o600))
	require.Error(t, server.ValidateConfig(context.Background(), path), "ValidateConfig() error = nil, want validation failure")
}

func TestValidateConfigRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"unknown_field": true}`), 0o600))
	require.Error(t, server.ValidateConfig(context.Background(), path), "ValidateConfig() error = nil, want strict field failure")
}

func TestMigrateCommandsRejectMissingConfigurationFile(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "missing.json")
	_, err := server.MigrateUp(context.Background(), missing)
	require.Error(t, err, "MigrateUp() error = nil, want configuration failure")
	_, err = server.MigrateStatus(context.Background(), missing)
	require.Error(t, err, "MigrateStatus() error = nil, want configuration failure")
}

func writeValidConfig(t *testing.T) string {
	t.Helper()

	data, err := json.Marshal(config.Default())
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return path
}

func TestCurrentBuildInfoReportsBuildValues(t *testing.T) {
	t.Parallel()

	info := server.CurrentBuildInfo()
	require.Equal(t, "dev", info.Version, "CurrentBuildInfo() = %#v, want default build values", info)
	require.Equal(t, "unknown", info.Commit, "CurrentBuildInfo() = %#v, want default build values", info)
	require.Equal(t, "unknown", info.BuildTime, "CurrentBuildInfo() = %#v, want default build values", info)
	require.Equal(t, runtime.Version(), info.GoVersion, "CurrentBuildInfo().GoVersion")
}

func TestBuildInfoUsesWireFieldNames(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(server.BuildInfo{
		Version:   "1.2.3",
		Commit:    "abc123",
		BuildTime: "2026-07-26T00:00:00Z",
		GoVersion: "go1.25.4",
	})
	require.NoError(t, err)
	var fields map[string]string
	require.NoError(t, json.Unmarshal(encoded, &fields))
	want := map[string]string{
		"version":    "1.2.3",
		"commit":     "abc123",
		"build_time": "2026-07-26T00:00:00Z",
		"go_version": "go1.25.4",
	}
	for key, value := range want {
		require.Equal(t, value, fields[key], "BuildInfo JSON field %q = %q, want %q (encoded %s)", key, fields[key], value, encoded)
	}
}
