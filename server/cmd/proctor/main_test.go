// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	server "github.com/sudosylabs/proctor/server"
	"github.com/sudosylabs/proctor/server/app"
)

func TestReportErrorMapsFailuresToExitCodes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantCode   int
		wantStderr string
	}{
		{name: "success is silent", err: nil, wantCode: 0, wantStderr: ""},
		{
			name:       "usage failure exits 2 with proctor prefix",
			err:        &UsageError{Message: "a command is required"},
			wantCode:   2,
			wantStderr: "proctor: a command is required\n",
		},
		{
			name:       "wrapped usage failure keeps exit 2",
			err:        errors.Join(errors.New("serve"), &UsageError{Message: "bad flag"}),
			wantCode:   2,
			wantStderr: "proctor: serve\nbad flag\n",
		},
		{
			name:       "operational failure exits 1 with proctor prefix",
			err:        errors.New("open database: connection refused"),
			wantCode:   1,
			wantStderr: "proctor: open database: connection refused\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stderr bytes.Buffer
			if code := reportError(tt.err, &stderr); code != tt.wantCode {
				t.Fatalf("reportError() = %d, want %d", code, tt.wantCode)
			}
			if stderr.String() != tt.wantStderr {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}

func TestRunHelpWritesUsageToStdout(t *testing.T) {
	t.Parallel()

	want := "Usage:\n" +
		"  proctor serve [--config path]\n" +
		"  proctor config validate [--config path]\n" +
		"  proctor migrate <up|status> [--config path]\n" +
		"  proctor version [--json]\n" +
		"  proctor help\n"

	for _, command := range []string{"help", "-h", "--help"} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if err := run(context.Background(), []string{command}, &stdout, &stderr); err != nil {
			t.Fatalf("run(%q) error = %v", command, err)
		}
		if stdout.String() != want {
			t.Fatalf("run(%q) stdout = %q, want %q", command, stdout.String(), want)
		}
		if stderr.Len() != 0 {
			t.Fatalf("run(%q) stderr = %q, want empty", command, stderr.String())
		}
	}
}

func TestRunWithoutCommandRequiresCommand(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run(context.Background(), nil, &stdout, &stderr)
	var usageError *UsageError
	if !errors.As(err, &usageError) || usageError.Message != "a command is required" {
		t.Fatalf("run() error = %v, want command-required usage error", err)
	}
	if !strings.HasPrefix(stderr.String(), "Usage:\n") {
		t.Fatalf("stderr = %q, want usage", stderr.String())
	}
}

func TestRunVersionWritesTextBuildInformation(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := run(context.Background(), []string{"version"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	output := stdout.String()
	if !strings.HasPrefix(output, "proctor dev (commit unknown, built unknown, ") ||
		!strings.HasSuffix(output, ")\n") {
		t.Fatalf("version output = %q", output)
	}
}

func TestRunServeRejectsPositionalArguments(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run(context.Background(), []string{"serve", "extra"}, &stdout, &stderr)
	var usageError *UsageError
	if !errors.As(err, &usageError) ||
		!strings.Contains(err.Error(), "serve does not accept positional arguments") {
		t.Fatalf("run() error = %v, want serve positional-argument usage error", err)
	}
}

func TestRunServeFailsWhenConfigurationFileIsMissing(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "missing.json")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run(context.Background(), []string{"serve", "--config", missing}, &stdout, &stderr)
	if err == nil {
		t.Fatal("run() error = nil, want construction failure")
	}
	var usageError *UsageError
	if errors.As(err, &usageError) {
		t.Fatalf("run() error = %v, want operational failure rather than usage error", err)
	}
}

func TestRunConfigValidateRejectsUnreadableFile(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "missing.json")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run(context.Background(), []string{"config", "validate", "--config", missing}, &stdout, &stderr)
	if err == nil {
		t.Fatal("run() error = nil, want validation failure")
	}
	if !strings.Contains(err.Error(), "read configuration") {
		t.Fatalf("run() error = %v, want read failure", err)
	}
}

func TestRunConfigValidateRejectsMalformedFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := run(context.Background(), []string{"config", "validate", "--config", path}, &stdout, &stderr); err == nil {
		t.Fatal("run() error = nil, want validation failure")
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

func TestRunConfigValidateAcceptsDefaultedFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := run(context.Background(), []string{"config", "validate", "--config", path}, &stdout, &stderr); err != nil {
		t.Fatalf("run() error = %v, stderr = %q", err, stderr.String())
	}
	if stdout.String() != "configuration is valid\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunConfigValidate(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := run(context.Background(), []string{"config", "validate"}, &stdout, &stderr); err != nil {
		t.Fatalf("run() error = %v, stderr = %q", err, stderr.String())
	}
	if stdout.String() != "configuration is valid\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunVersionJSON(t *testing.T) {
	originalVersion := app.Version
	originalCommit := app.Commit
	originalBuildTime := app.BuildTime
	t.Cleanup(func() {
		app.Version = originalVersion
		app.Commit = originalCommit
		app.BuildTime = originalBuildTime
	})
	app.Version = "1.2.3"
	app.Commit = "abc123"
	app.BuildTime = "2026-07-26T00:00:00Z"

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := run(context.Background(), []string{"version", "--json"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	var info server.BuildInfo
	if err := json.Unmarshal(stdout.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info.Version != "1.2.3" || info.Commit != "abc123" {
		t.Fatalf("version info = %#v", info)
	}
}

func TestRunRejectsUnknownCommand(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run(context.Background(), []string{"unknown"}, &stdout, &stderr)
	var usageError *UsageError
	if !errors.As(err, &usageError) {
		t.Fatalf("run() error = %v, want *UsageError", err)
	}
	if !strings.Contains(stderr.String(), "Usage:") {
		t.Fatalf("usage was not written: %q", stderr.String())
	}
}

func TestRunMigrateRequiresAction(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run(context.Background(), []string{"migrate"}, &stdout, &stderr)
	var usageError *UsageError
	if !errors.As(err, &usageError) || !strings.Contains(err.Error(), "migrate <up|status>") {
		t.Fatalf("run() error = %v, want migrate usage error", err)
	}
}
