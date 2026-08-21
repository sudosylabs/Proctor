// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package commands

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

type configValidateExecutor func(context.Context, string) error

func newConfigCommand(validate configValidateExecutor) *cobra.Command {
	command := &cobra.Command{
		Use:   "config",
		Short: "Inspect Proctor deployment configuration",
		Args:  noArgs,
		RunE: func(*cobra.Command, []string) error {
			return newUsageError("config requires a subcommand")
		},
	}
	command.AddCommand(&cobra.Command{
		Use:   "validate",
		Short: "Validate configuration without starting the server",
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			path, err := configPath(command)
			if err != nil {
				return err
			}
			if err := validate(command.Context(), path); err != nil {
				return err
			}
			_, err = fmt.Fprintln(command.OutOrStdout(), "configuration is valid")
			return err
		},
	})
	return command
}
