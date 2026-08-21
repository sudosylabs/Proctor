// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package commands

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
			if err := os.WriteFile(path, []byte(tt.contents), 0o600); err != nil {
				t.Fatal(err)
			}
			code, stdout, stderr := executeForTest(
				context.Background(), []string{"config", "validate", "--config", path}, nil, productionExecutors(),
			)
			if code != tt.wantCode || stdout != tt.wantOutput {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
			if tt.wantFailure != "" && !strings.Contains(stderr, tt.wantFailure) {
				t.Fatalf("stderr = %q, want %q", stderr, tt.wantFailure)
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
	if code != 1 || stdout != "" || !strings.Contains(stderr, "read configuration") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}
