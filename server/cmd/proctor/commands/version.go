// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package commands

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	server "github.com/sudosylabs/proctor/server"
)

type buildInfoExecutor func() server.BuildInfo

func newVersionCommand(current buildInfoExecutor) *cobra.Command {
	var asJSON bool
	command := &cobra.Command{
		Use:   "version",
		Short: "Display Proctor build information",
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			info := current()
			if asJSON {
				encoder := json.NewEncoder(command.OutOrStdout())
				encoder.SetIndent("", "  ")
				return encoder.Encode(info)
			}
			_, err := fmt.Fprintf(
				command.OutOrStdout(),
				"proctor %s (commit %s, built %s, %s)\n",
				info.Version,
				info.Commit,
				info.BuildTime,
				info.GoVersion,
			)
			return err
		},
	}
	command.Flags().BoolVar(&asJSON, "json", false, "write build information as JSON")
	return command
}
