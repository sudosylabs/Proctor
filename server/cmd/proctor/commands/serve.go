// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package commands

import (
	"context"

	"github.com/spf13/cobra"

	server "github.com/sudosylabs/proctor/server"
)

type serveExecutor func(context.Context, string) error

func newServeCommand(execute serveExecutor) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the Proctor server",
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			path, err := configPath(command)
			if err != nil {
				return err
			}
			return execute(command.Context(), path)
		},
	}
}

func serve(ctx context.Context, configPath string) error {
	var options []server.Option
	if configPath != "" {
		options = append(options, server.WithConfigPath(configPath))
	}
	node, err := server.New(ctx, options...)
	if err != nil {
		return err
	}
	return node.Start(ctx)
}
