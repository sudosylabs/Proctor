// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package commands

import (
	"context"
	"os"

	"github.com/spf13/cobra"

	server "github.com/sudosylabs/proctor/server"
)

type serveExecutor func(context.Context, string) error

func newServeCommand(execute serveExecutor, text commandText) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: text.value("cli.serve.short", "Run the Proctor server", nil),
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			path, err := configPath(command, text)
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
	if socketPath := os.Getenv("NOTIFY_SOCKET"); socketPath != "" {
		options = append(options, server.WithReadyNotifier(systemdReadyNotifier(socketPath)))
	}
	node, err := server.New(ctx, options...)
	if err != nil {
		return err
	}
	return node.Run(ctx)
}
