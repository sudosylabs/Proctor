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
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server"
)

func TestVersionSupportsStableTextAndJSONOutput(t *testing.T) {
	t.Parallel()

	execute := testExecutors()
	execute.currentBuildInfo = func() server.BuildInfo {
		return server.BuildInfo{Version: "1.2.3", Commit: "abc123", BuildTime: "2026-07-26T00:00:00Z", GoVersion: "go1.25.4"}
	}

	code, stdout, stderr := executeForTest(context.Background(), []string{"version"}, nil, execute)
	require.Equal(t, 0, code, "text: code")
	require.Equal(t, "proctor 1.2.3 (commit abc123, built 2026-07-26T00:00:00Z, go1.25.4)\n", stdout, "text: code")
	require.Empty(t, stderr, "text: code")

	code, stdout, stderr = executeForTest(context.Background(), []string{"version", "--json"}, nil, execute)
	require.Equal(t, 0, code, "json: code=%d stdout=%q stderr", code, stdout)
	require.Empty(t, stderr, "json: code=%d stdout=%q stderr", code, stdout)
	var info server.BuildInfo
	require.NoError(t, json.Unmarshal([]byte(stdout), &info))
	require.Equal(t, "1.2.3", info.Version)
	require.Equal(t, "abc123", info.Commit)
	require.Equal(t, "2026-07-26T00:00:00Z", info.BuildTime)
	require.Equal(t, "go1.25.4", info.GoVersion)
}
