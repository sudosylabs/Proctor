// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package commands

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	server "github.com/sudosylabs/proctor/server"
)

type migrateUpExecutor func(context.Context, string) (int, error)
type migrateStatusExecutor func(context.Context, string) (server.MigrationStatus, error)

func newMigrateCommand(up migrateUpExecutor, status migrateStatusExecutor) *cobra.Command {
	command := &cobra.Command{
		Use:   "migrate",
		Short: "Manage the Proctor database schema",
		Args:  noArgs,
		RunE: func(*cobra.Command, []string) error {
			return newUsageError("migrate requires an up or status subcommand")
		},
	}
	command.AddCommand(
		&cobra.Command{
			Use:   "up",
			Short: "Apply pending database migrations",
			Args:  noArgs,
			RunE: func(command *cobra.Command, _ []string) error {
				path, err := configPath(command)
				if err != nil {
					return err
				}
				version, err := up(command.Context(), path)
				if err != nil {
					return err
				}
				_, err = fmt.Fprintf(command.OutOrStdout(), "database schema migrated to version %d\n", version)
				return err
			},
		},
		&cobra.Command{
			Use:   "status",
			Short: "Report applied and pending database migrations",
			Args:  noArgs,
			RunE: func(command *cobra.Command, _ []string) error {
				path, err := configPath(command)
				if err != nil {
					return err
				}
				result, err := status(command.Context(), path)
				if err != nil {
					return err
				}
				_, err = fmt.Fprintf(
					command.OutOrStdout(),
					"database schema version %d; server schema version %d; pending migrations %d\n",
					result.DatabaseVersion,
					result.ServerVersion,
					result.PendingMigrations,
				)
				return err
			},
		},
	)
	return command
}
