// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package commands

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

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
		effectiveUID: func() int { return 1000 },
	}
}

func TestRunWarnsButContinuesWhenRunningAsRoot(t *testing.T) {
	t.Parallel()

	execute := testExecutors()
	execute.effectiveUID = func() int { return 0 }
	code, stdout, stderr := executeForTest(context.Background(), []string{"version"}, nil, execute)
	require.Equal(t, 0, code, "code")
	require.Contains(t, stdout, "proctor dev", "stdout")
	require.Equal(t, "warning: running Proctor as root is not recommended; use a dedicated non-root user\n", stderr)
}

func executeForTest(ctx context.Context, args []string, stdin io.Reader, execute executors) (int, string, string) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(ctx, args, stdin, &stdout, &stderr, execute)
	return code, stdout.String(), stderr.String()
}

type commandTestLocalizer map[string]string

func (l commandTestLocalizer) Translate(_ string, id string, _ any) (string, error) {
	translated, ok := l[id]
	if !ok {
		return "", errors.New("translation unavailable")
	}
	return translated, nil
}

func (commandTestLocalizer) SupportedLocales() []string { return []string{"en", "fr"} }

func TestRunLocalizesCommandOwnedPresentation(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runWithText(
		context.Background(),
		[]string{"config", "validate"},
		nil,
		&stdout,
		&stderr,
		testExecutors(),
		commandText{
			localizer: commandTestLocalizer{
				"cli.config.validate.success": "configuration valide",
			},
			locale: "fr",
		},
	)
	require.Equal(t, 0, code, "code")
	require.Equal(t, "configuration valide\n", stdout.String(), "code")
	require.Empty(t, stderr.String(), "code")
}

func TestRunHelpDescribesTheExplicitCommandTree(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{"help"}, {"--help"}, {"-h"}} {
		code, stdout, stderr := executeForTest(context.Background(), args, nil, testExecutors())
		require.Equal(t, 0, code, "run(%q) code = %d, stderr = %q", args, code, stderr)
		for _, command := range []string{"administrator", "completion", "config", "migrate", "serve", "version"} {
			require.Contains(t, stdout, command, "run(%q) help does not contain %q:\n", args, command)
		}
		require.Empty(t, stderr, "run(%q) stderr", args)
	}
}

func TestRunGeneratesShellCompletion(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := executeForTest(context.Background(), []string{"completion", "bash"}, nil, testExecutors())
	require.Equal(t, 0, code, "code=%d stdout=%q stderr", code, stdout)
	require.True(t, strings.HasPrefix(stdout, "# bash completion V2 for proctor"), "code=%d stdout=%q stderr", code, stdout)
	require.Empty(t, stderr, "code=%d stdout=%q stderr", code, stdout)
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
			require.Equal(t, 2, code, "run() code = %d, want 2; stderr = %q", code, stderr)
			require.Empty(t, stdout, "stdout")
			require.Contains(t, stderr, "Usage:", "stderr = %q, want usage and %q", stderr, tt.want)
			require.Contains(t, stderr, "proctor: "+tt.want, "stderr = %q, want usage and %q", stderr, tt.want)
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
	require.Equal(t, 1, code, "code")
	require.Empty(t, stdout, "stdout")
	require.Equal(t, "proctor: open database: connection refused\n", stderr)

	code, _, stderr = executeForTest(context.Background(), []string{"serve", "extra"}, nil, testExecutors())
	require.Equal(t, 2, code, "serve usage failure: code")
	require.Contains(t, stderr, "unknown command \"extra\"", "serve usage failure: code")
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
		require.Equal(t, 0, code, "run(%q): code=%d stdout=%q stderr", args, code, stdout)
		require.Equal(t, "configuration is valid\n", stdout, "run(%q): code=%d stdout=%q stderr", args, code, stdout)
		require.Empty(t, stderr, "run(%q): code=%d stdout=%q stderr", args, code, stdout)
	}
	require.Len(t, paths, 2)
	require.Equal(t, "/etc/proctor-first.json", paths[0])
	require.Equal(t, "/etc/proctor-second.json", paths[1])
}

func TestRunBuildsAFreshCommandTreeForEveryInvocation(t *testing.T) {
	t.Parallel()

	execute := testExecutors()
	for range 2 {
		code, stdout, stderr := executeForTest(context.Background(), []string{"version", "--json"}, nil, execute)
		require.Equal(t, 0, code, "code")
		require.Contains(t, stdout, `"version": "dev"`, "code")
		require.Empty(t, stderr, "code")
	}
}
