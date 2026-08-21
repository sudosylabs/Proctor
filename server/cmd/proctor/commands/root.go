// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package commands owns the proctor host-operator command tree. Commands parse
// and present operator input while the module-root server package retains
// infrastructure selection and lifecycle ownership.
package commands

import (
	"context"
	"errors"
	"fmt"
	"io"

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
}

func productionExecutors() executors {
	return executors{
		serve:                serve,
		validateConfig:       server.ValidateConfig,
		migrateUp:            server.MigrateUp,
		migrateStatus:        server.MigrateStatus,
		recoverAdministrator: server.RecoverAdministratorAccess,
		currentBuildInfo:     server.CurrentBuildInfo,
	}
}

// Run constructs a fresh command tree, executes args, writes any terminal
// failure once, and returns 0 for success, 2 for usage failures, or 1 for
// operational failures.
func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return run(ctx, args, stdin, stdout, stderr, productionExecutors())
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, execute executors) int {
	if stdout == nil || stderr == nil {
		return 1
	}
	if ctx == nil {
		ctx = context.Background()
	}

	root := newRootCommand(stdin, stdout, stderr, execute)
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

func newRootCommand(stdin io.Reader, stdout, stderr io.Writer, execute executors) *cobra.Command {
	root := &cobra.Command{
		Use:           "proctor",
		Short:         "Run and maintain a Proctor installation",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          noArgs,
		RunE: func(*cobra.Command, []string) error {
			return newUsageError("a command is required")
		},
	}
	root.SetIn(stdin)
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return wrapUsageError(err)
	})
	root.PersistentFlags().StringP("config", "c", "", "path to a JSON configuration file")

	root.AddCommand(
		newServeCommand(execute.serve),
		newConfigCommand(execute.validateConfig),
		newMigrateCommand(execute.migrateUp, execute.migrateStatus),
		newAdministratorCommand(stdin, execute.recoverAdministrator),
		newVersionCommand(execute.currentBuildInfo),
	)
	return root
}

func configPath(command *cobra.Command) (string, error) {
	path, err := command.Flags().GetString("config")
	if err != nil {
		return "", fmt.Errorf("read config flag: %w", err)
	}
	return path, nil
}
