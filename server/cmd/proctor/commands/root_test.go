// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package commands

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	server "github.com/sudosylabs/proctor/server"
)

func testExecutors() executors {
	return executors{
		serve: func(context.Context, string) error { return nil },
		validateConfig: func(context.Context, string) error {
			return nil
		},
		migrateUp: func(context.Context, string) (int, error) {
			return 1, nil
		},
		migrateStatus: func(context.Context, string) (server.MigrationStatus, error) {
			return server.MigrationStatus{DatabaseVersion: 1, ServerVersion: 1}, nil
		},
		recoverAdministrator: func(context.Context, string, server.AdministratorRecoveryCommand) (*server.AdministratorRecoveryResult, error) {
			return &server.AdministratorRecoveryResult{}, nil
		},
		currentBuildInfo: func() server.BuildInfo {
			return server.BuildInfo{Version: "dev", Commit: "unknown", BuildTime: "unknown", GoVersion: "go-test"}
		},
	}
}

func executeForTest(ctx context.Context, args []string, stdin io.Reader, execute executors) (int, string, string) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(ctx, args, stdin, &stdout, &stderr, execute)
	return code, stdout.String(), stderr.String()
}

func TestRunHelpDescribesTheExplicitCommandTree(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{"help"}, {"--help"}, {"-h"}} {
		code, stdout, stderr := executeForTest(context.Background(), args, nil, testExecutors())
		if code != 0 {
			t.Fatalf("run(%q) code = %d, stderr = %q", args, code, stderr)
		}
		for _, command := range []string{"administrator", "completion", "config", "migrate", "serve", "version"} {
			if !strings.Contains(stdout, command) {
				t.Fatalf("run(%q) help does not contain %q:\n%s", args, command, stdout)
			}
		}
		if stderr != "" {
			t.Fatalf("run(%q) stderr = %q, want empty", args, stderr)
		}
	}
}

func TestRunGeneratesShellCompletion(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := executeForTest(context.Background(), []string{"completion", "bash"}, nil, testExecutors())
	if code != 0 || !strings.HasPrefix(stdout, "# bash completion V2 for proctor") || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestRunRequiresAKnownCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing", want: "a command is required"},
		{name: "unknown root", args: []string{"unknown"}, want: "unknown command"},
		{name: "unknown nested", args: []string{"migrate", "sideways"}, want: "unknown command"},
		{name: "missing nested", args: []string{"config"}, want: "config requires a subcommand"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			code, stdout, stderr := executeForTest(context.Background(), tt.args, nil, testExecutors())
			if code != 2 {
				t.Fatalf("run() code = %d, want 2; stderr = %q", code, stderr)
			}
			if stdout != "" {
				t.Fatalf("stdout = %q, want empty", stdout)
			}
			if !strings.Contains(stderr, "Usage:") || !strings.Contains(stderr, "proctor: "+tt.want) {
				t.Fatalf("stderr = %q, want usage and %q", stderr, tt.want)
			}
		})
	}
}

func TestRunDistinguishesUsageAndOperationalFailures(t *testing.T) {
	t.Parallel()

	execute := testExecutors()
	execute.validateConfig = func(context.Context, string) error {
		return errors.New("open database: connection refused")
	}
	code, stdout, stderr := executeForTest(context.Background(), []string{"config", "validate"}, nil, execute)
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if stderr != "proctor: open database: connection refused\n" {
		t.Fatalf("stderr = %q", stderr)
	}

	code, _, stderr = executeForTest(context.Background(), []string{"serve", "extra"}, nil, testExecutors())
	if code != 2 || !strings.Contains(stderr, "unknown command \"extra\"") {
		t.Fatalf("serve usage failure: code=%d stderr=%q", code, stderr)
	}
}

func TestPersistentConfigFlagWorksBeforeOrAfterSubcommands(t *testing.T) {
	t.Parallel()

	var paths []string
	execute := testExecutors()
	execute.validateConfig = func(_ context.Context, path string) error {
		paths = append(paths, path)
		return nil
	}

	for _, args := range [][]string{
		{"--config", "/etc/proctor-first.json", "config", "validate"},
		{"config", "validate", "-c", "/etc/proctor-second.json"},
	} {
		code, stdout, stderr := executeForTest(context.Background(), args, nil, execute)
		if code != 0 || stdout != "configuration is valid\n" || stderr != "" {
			t.Fatalf("run(%q): code=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
	}
	if len(paths) != 2 || paths[0] != "/etc/proctor-first.json" || paths[1] != "/etc/proctor-second.json" {
		t.Fatalf("paths = %#v", paths)
	}
}

func TestRunBuildsAFreshCommandTreeForEveryInvocation(t *testing.T) {
	t.Parallel()

	execute := testExecutors()
	for range 2 {
		code, stdout, stderr := executeForTest(context.Background(), []string{"version", "--json"}, nil, execute)
		if code != 0 || !strings.Contains(stdout, `"version": "dev"`) || stderr != "" {
			t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
	}
}
