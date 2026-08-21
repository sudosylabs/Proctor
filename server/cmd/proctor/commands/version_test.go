// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package commands

import (
	"context"
	"encoding/json"
	"testing"

	server "github.com/sudosylabs/proctor/server"
)

func TestVersionSupportsStableTextAndJSONOutput(t *testing.T) {
	t.Parallel()

	execute := testExecutors()
	execute.currentBuildInfo = func() server.BuildInfo {
		return server.BuildInfo{Version: "1.2.3", Commit: "abc123", BuildTime: "2026-07-26T00:00:00Z", GoVersion: "go1.25.4"}
	}

	code, stdout, stderr := executeForTest(context.Background(), []string{"version"}, nil, execute)
	if code != 0 || stdout != "proctor 1.2.3 (commit abc123, built 2026-07-26T00:00:00Z, go1.25.4)\n" || stderr != "" {
		t.Fatalf("text: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	code, stdout, stderr = executeForTest(context.Background(), []string{"version", "--json"}, nil, execute)
	if code != 0 || stderr != "" {
		t.Fatalf("json: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var info server.BuildInfo
	if err := json.Unmarshal([]byte(stdout), &info); err != nil {
		t.Fatal(err)
	}
	if info.Version != "1.2.3" || info.Commit != "abc123" || info.BuildTime != "2026-07-26T00:00:00Z" || info.GoVersion != "go1.25.4" {
		t.Fatalf("info = %#v", info)
	}
}
