// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package commands

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/sudosylabs/proctor/server"
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
	return serveNode(ctx, func(ctx context.Context) (servingNode, error) {
		node, err := server.New(ctx, options...)
		if err != nil {
			return nil, err
		}
		return node, nil
	})
}

// servingNode is the part of *server.Server that serve drives.
type servingNode interface {
	Run(context.Context) error
	Reload(context.Context) error
}

// serveNode constructs and runs one node, forwarding SIGHUP to its Reload.
// The handler is registered before construction: the default SIGHUP action
// terminates the process, and construction applies database migrations. A
// reload requested before the node exists is held and runs once it does.
func serveNode(ctx context.Context, construct func(context.Context) (servingNode, error)) error {
	reloadSignals := make(chan os.Signal, 1)
	signal.Notify(reloadSignals, syscall.SIGHUP)
	defer signal.Stop(reloadSignals)
	node, err := construct(ctx)
	if err != nil {
		return err
	}
	reloadCtx, stopReloads := context.WithCancel(ctx)
	reloadsDone := make(chan struct{})
	go func() {
		defer close(reloadsDone)
		forwardReloadSignals(reloadCtx, reloadSignals, node.Reload)
	}()
	defer func() {
		stopReloads()
		<-reloadsDone
	}()
	return node.Run(ctx)
}

// reloadSignalTimeout bounds one SIGHUP-triggered reload.
const reloadSignalTimeout = 30 * time.Second

// forwardReloadSignals runs one bounded reload per received signal until ctx
// ends. The server logs each reload outcome; a failed reload keeps the
// running configuration and certificates in force.
func forwardReloadSignals(ctx context.Context, signals <-chan os.Signal, reload func(context.Context) error) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-signals:
			reloadCtx, cancel := context.WithTimeout(ctx, reloadSignalTimeout)
			_ = reload(reloadCtx)
			cancel()
		}
	}
}
