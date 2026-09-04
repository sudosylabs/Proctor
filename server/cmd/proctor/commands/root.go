// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

// Package commands owns the proctor host-operator command tree. Commands parse
// and present operator input while the module-root server package retains
// infrastructure selection and lifecycle ownership.
package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	server "github.com/sudosylabs/proctor/server"
)

type usageError struct {
	message string
}

func (e *usageError) Error() string {
	return e.message
}

func newUsageError(message string) error {
	return &usageError{message: message}
}

func wrapUsageError(err error) error {
	if err == nil {
		return nil
	}
	return newUsageError(err.Error())
}

func noArgs(command *cobra.Command, args []string) error {
	return wrapUsageError(cobra.NoArgs(command, args))
}

type executors struct {
	serve                serveExecutor
	validateConfig       configValidateExecutor
	migrateUp            migrateUpExecutor
	migrateStatus        migrateStatusExecutor
	recoverAdministrator administratorRecoveryExecutor
	currentBuildInfo     buildInfoExecutor
	effectiveUID         func() int
}

func productionExecutors() executors {
	return executors{
		serve:                serve,
		validateConfig:       server.ValidateConfig,
		migrateUp:            server.MigrateUp,
		migrateStatus:        server.MigrateStatus,
		recoverAdministrator: server.RecoverAdministratorAccess,
		currentBuildInfo:     server.CurrentBuildInfo,
		effectiveUID:         os.Geteuid,
	}
}

// Run constructs a fresh command tree, executes args, writes any terminal
// failure once, and returns 0 for success, 2 for usage failures, or 1 for
// operational failures.
func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return runWithText(ctx, args, stdin, stdout, stderr, productionExecutors(), productionCommandText())
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, execute executors) int {
	return runWithText(ctx, args, stdin, stdout, stderr, execute, englishCommandText())
}

func runWithText(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, execute executors, text commandText) int {
	if stdout == nil || stderr == nil {
		return 1
	}
	if ctx == nil {
		ctx = context.Background()
	}

	root := newRootCommand(stdin, stdout, stderr, execute, text)
	root.SetArgs(args)
	command, err := root.ExecuteContextC(ctx)
	if err == nil {
		return 0
	}
	if command == nil {
		command = root
	}

	var usage *usageError
	if errors.As(err, &usage) {
		_, _ = fmt.Fprint(stderr, command.UsageString())
		_, _ = fmt.Fprintln(stderr, "proctor:", err)
		return 2
	}
	_, _ = fmt.Fprintln(stderr, "proctor:", err)
	return 1
}

func newRootCommand(stdin io.Reader, stdout, stderr io.Writer, execute executors, text commandText) *cobra.Command {
	root := &cobra.Command{
		Use:           "proctor",
		Short:         text.value("cli.root.short", "Run and maintain a Proctor installation", nil),
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          noArgs,
		RunE: func(*cobra.Command, []string) error {
			return newUsageError(text.value("cli.root.error.command_required", "a command is required", nil))
		},
		PersistentPreRun: func(command *cobra.Command, _ []string) {
			checkForRootUser(command.ErrOrStderr(), execute.effectiveUID, text)
		},
	}
	root.SetIn(stdin)
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return wrapUsageError(err)
	})
	root.PersistentFlags().StringP(
		"config",
		"c",
		"",
		text.value(
			"cli.config.flag.path",
			"configuration file path (default config/config.json; env PROCTOR_CONFIG)",
			nil,
		),
	)

	root.AddCommand(
		newServeCommand(execute.serve, text),
		newConfigCommand(execute.validateConfig, text),
		newMigrateCommand(execute.migrateUp, execute.migrateStatus, text),
		newAdministratorCommand(stdin, execute.recoverAdministrator, text),
		newVersionCommand(execute.currentBuildInfo, text),
	)
	return root
}

func checkForRootUser(stderr io.Writer, effectiveUID func() int, text commandText) {
	if stderr == nil || effectiveUID == nil || effectiveUID() != 0 {
		return
	}
	_, _ = fmt.Fprintln(
		stderr,
		text.value(
			"cli.root.warning.root_user",
			"warning: running Proctor as root is not recommended; use a dedicated non-root user",
			nil,
		),
	)
}

func configPath(command *cobra.Command, text commandText) (string, error) {
	path, err := command.Flags().GetString("config")
	if err != nil {
		return "", fmt.Errorf("%s: %w", text.value("cli.config.error.read_flag", "read config flag", nil), err)
	}
	return path, nil
}
