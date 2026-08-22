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

func newMigrateCommand(up migrateUpExecutor, status migrateStatusExecutor, text commandText) *cobra.Command {
	command := &cobra.Command{
		Use:   "migrate",
		Short: text.value("cli.migrate.short", "Manage the Proctor database schema", nil),
		Args:  noArgs,
		RunE: func(*cobra.Command, []string) error {
			return newUsageError(text.value("cli.migrate.error.requires_subcommand", "migrate requires an up or status subcommand", nil))
		},
	}
	command.AddCommand(
		&cobra.Command{
			Use:   "up",
			Short: text.value("cli.migrate.up.short", "Apply pending database migrations", nil),
			Args:  noArgs,
			RunE: func(command *cobra.Command, _ []string) error {
				path, err := configPath(command, text)
				if err != nil {
					return err
				}
				version, err := up(command.Context(), path)
				if err != nil {
					return err
				}
				_, err = fmt.Fprintln(command.OutOrStdout(), text.value(
					"cli.migrate.up.success",
					"database schema migrated to version {{.Version}}",
					map[string]any{"Version": version},
				))
				return err
			},
		},
		&cobra.Command{
			Use:   "status",
			Short: text.value("cli.migrate.status.short", "Report applied and pending database migrations", nil),
			Args:  noArgs,
			RunE: func(command *cobra.Command, _ []string) error {
				path, err := configPath(command, text)
				if err != nil {
					return err
				}
				result, err := status(command.Context(), path)
				if err != nil {
					return err
				}
				_, err = fmt.Fprintln(command.OutOrStdout(), text.value(
					"cli.migrate.status.success",
					"database schema version {{.DatabaseVersion}}; server schema version {{.ServerVersion}}; pending migrations {{.PendingMigrations}}",
					map[string]any{
						"DatabaseVersion":   result.DatabaseVersion,
						"ServerVersion":     result.ServerVersion,
						"PendingMigrations": result.PendingMigrations,
					},
				))
				return err
			},
		},
	)
	return command
}
