// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package commands

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server"
)

func TestConfigValidateUsesTheServerFacade(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		contents    string
		wantCode    int
		wantOutput  string
		wantFailure string
	}{
		{name: "valid defaulted configuration", contents: "{}", wantOutput: "configuration is valid\n"},
		{name: "malformed configuration", contents: "{not json", wantCode: 1, wantFailure: "decode configuration"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "config.json")
			require.NoError(t, os.WriteFile(path, []byte(tt.contents), 0o600))
			code, stdout, stderr := executeForTest(
				context.Background(), []string{"config", "validate", "--config", path}, nil, productionExecutors(),
			)
			require.Equal(t, tt.wantCode, code, "code=%d stdout=%q stderr=%q", code, stdout, stderr)
			require.Equal(t, tt.wantOutput, stdout, "code=%d stdout=%q stderr=%q", code, stdout, stderr)
			if tt.wantFailure != "" {
				require.Contains(t, stderr, tt.wantFailure, "stderr")
			}
		})
	}
}

func TestConfigValidateRejectsMissingFileAsOperationalFailure(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "missing.json")
	code, stdout, stderr := executeForTest(
		context.Background(), []string{"config", "validate", "--config", missing}, nil, productionExecutors(),
	)
	require.Equal(t, 1, code, "code")
	require.Empty(t, stdout, "code")
	require.Contains(t, stderr, "read configuration", "code")
}

func TestConfigShowPrintsTheRedactedEffectiveConfiguration(t *testing.T) {
	t.Parallel()

	document := []byte("{\n  \"Database\": {\n    \"DataSource\": \"[redacted]\",\n    \"MaxOpenConnections\": 20\n  },\n  \"Cluster\": {\n    \"Seeds\": [],\n    \"Enabled\": false\n  },\n  \"Hosts\": [\n    \"a\",\n    \"b\"\n  ]\n}")
	execute := testExecutors()
	execute.showConfig = func(_ context.Context, path string) (server.EffectiveConfiguration, error) {
		require.Equal(t, "/etc/proctor.json", path)
		return server.EffectiveConfiguration{Document: document, EnvironmentOverrides: []string{"PROCTOR_DATABASE_DATA_SOURCE"}}, nil
	}

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "settings",
			args: []string{"config", "show", "--config", "/etc/proctor.json"},
			want: "# environment overrides: PROCTOR_DATABASE_DATA_SOURCE\n" +
				"Database.DataSource = \"[redacted]\"\n" +
				"Database.MaxOpenConnections = 20\n" +
				"Cluster.Seeds = []\n" +
				"Cluster.Enabled = false\n" +
				"Hosts[0] = \"a\"\n" +
				"Hosts[1] = \"b\"\n",
		},
		{name: "json", args: []string{"config", "show", "--json", "--config", "/etc/proctor.json"}, want: string(document) + "\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			code, stdout, stderr := executeForTest(context.Background(), tt.args, nil, execute)
			require.Equal(t, 0, code, "stderr=%q", stderr)
			require.Equal(t, tt.want, stdout)
			require.Empty(t, stderr)
		})
	}
}

func TestConfigShowNeverPrintsSecretsThroughTheServerFacade(t *testing.T) {
	t.Setenv("PROCTOR_AUTHENTICATION_BOOTSTRAP_SECRET", "bootstrap-secret-value-that-is-long-enough-1234")
	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"Database":{"DataSource":"postgres://u:database-password@db/proctor"}}`), 0o600))

	for _, args := range [][]string{{"config", "show", "--config", path}, {"config", "show", "--json", "--config", path}} {
		code, stdout, stderr := executeForTest(context.Background(), args, nil, productionExecutors())
		require.Equal(t, 0, code, "run(%q) stderr=%q", args, stderr)
		require.NotContains(t, stdout, "database-password", "run(%q)", args)
		require.NotContains(t, stdout, "bootstrap-secret-value", "run(%q)", args)
		require.Contains(t, stdout, "[redacted]", "run(%q)", args)
	}
}
