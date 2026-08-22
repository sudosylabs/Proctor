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

func newVersionCommand(current buildInfoExecutor, text commandText) *cobra.Command {
	var asJSON bool
	command := &cobra.Command{
		Use:   "version",
		Short: text.value("cli.version.short", "Display Proctor build information", nil),
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			info := current()
			if asJSON {
				encoder := json.NewEncoder(command.OutOrStdout())
				encoder.SetIndent("", "  ")
				return encoder.Encode(info)
			}
			_, err := fmt.Fprintln(command.OutOrStdout(), text.value(
				"cli.version.output",
				"proctor {{.Version}} (commit {{.Commit}}, built {{.BuildTime}}, {{.GoVersion}})",
				map[string]any{
					"Version":   info.Version,
					"Commit":    info.Commit,
					"BuildTime": info.BuildTime,
					"GoVersion": info.GoVersion,
				},
			))
			return err
		},
	}
	command.Flags().BoolVar(&asJSON, "json", false, text.value("cli.version.flag.json", "write build information as JSON", nil))
	return command
}
