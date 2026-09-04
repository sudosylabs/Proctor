// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package commands

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

type configValidateExecutor func(context.Context, string) error

func newConfigCommand(validate configValidateExecutor, text commandText) *cobra.Command {
	command := &cobra.Command{
		Use:   "config",
		Short: text.value("cli.config.short", "Inspect Proctor deployment configuration", nil),
		Args:  noArgs,
		RunE: func(*cobra.Command, []string) error {
			return newUsageError(text.value("cli.config.error.requires_subcommand", "config requires a subcommand", nil))
		},
	}
	command.AddCommand(&cobra.Command{
		Use:   "validate",
		Short: text.value("cli.config.validate.short", "Validate configuration without starting the server", nil),
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			path, err := configPath(command, text)
			if err != nil {
				return err
			}
			if err := validate(command.Context(), path); err != nil {
				return err
			}
			_, err = fmt.Fprintln(command.OutOrStdout(), text.value("cli.config.validate.success", "configuration is valid", nil))
			return err
		},
	})
	return command
}
