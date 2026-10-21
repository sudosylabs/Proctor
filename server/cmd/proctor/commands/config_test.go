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
