// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sudosylabs/proctor/server"
)

type migrateDownPlanExecutor func(context.Context, string, int) (server.MigrationRollbackPlan, error)
type migrateDownExecutor func(context.Context, string, int) (int, error)

func newMigrateDownCommand(plan migrateDownPlanExecutor, down migrateDownExecutor, text commandText) *cobra.Command {
	var steps int
	var confirm bool
	command := &cobra.Command{
		Use:   "down",
		Short: text.value("cli.migrate.down.short", "Revert the newest database migrations while every node is stopped", nil),
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if steps < 1 {
				return newUsageError(text.value("cli.migrate.down.error.steps", "migrate down requires --steps with a count of at least 1", nil))
			}
			path, err := configPath(command, text)
			if err != nil {
				return err
			}
			planned, err := plan(command.Context(), path, steps)
			if err != nil {
				return err
			}
			output := command.OutOrStdout()
			if err = writeRollbackPlan(output, planned, text); err != nil {
				return err
			}
			if len(planned.LiveServingNodes) > 0 {
				return errors.New(text.value(
					"cli.migrate.down.error.live_nodes",
					"refusing to revert: serving nodes {{.Nodes}} hold a live lease; stop every node and wait for its lease to expire",
					map[string]any{"Nodes": strings.Join(planned.LiveServingNodes, ", ")},
				))
			}
			if !confirm {
				return errors.New(text.value("cli.migrate.down.error.confirm", "nothing was reverted; re-run with --confirm to revert these migrations", nil))
			}
			version, err := down(command.Context(), path, steps)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(output, text.value(
				"cli.migrate.down.success", "database schema reverted to version {{.Version}}",
				map[string]any{"Version": version},
			))
			return err
		},
	}
	command.Flags().IntVar(&steps, "steps", 0, text.value("cli.migrate.down.flag.steps", "number of newest migrations to revert (required)", nil))
	command.Flags().BoolVar(&confirm, "confirm", false, text.value("cli.migrate.down.flag.confirm", "revert the listed migrations; without it the command only prints them", nil))
	return command
}

func writeRollbackPlan(output io.Writer, plan server.MigrationRollbackPlan, text commandText) error {
	if _, err := fmt.Fprintln(output, text.value(
		"cli.migrate.down.plan",
		"database schema version {{.DatabaseVersion}}; reverting {{.Count}} migration(s) to version {{.TargetVersion}}:",
		map[string]any{"DatabaseVersion": plan.DatabaseVersion, "Count": len(plan.Migrations), "TargetVersion": plan.TargetVersion},
	)); err != nil {
		return err
	}
	for _, migration := range plan.Migrations {
		if _, err := fmt.Fprintf(output, "  %06d %s\n", migration.Version, migration.Name); err != nil {
			return err
		}
	}
	if plan.TargetVersion == 0 {
		_, err := fmt.Fprintln(output, text.value(
			"cli.migrate.down.empty_schema",
			"warning: this reverts the single pre-release baseline and leaves an empty schema; every Proctor table and all of its data is dropped, and only a database backup restores it",
			nil,
		))
		return err
	}
	return nil
}
