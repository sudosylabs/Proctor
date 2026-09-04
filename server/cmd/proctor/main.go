// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/sudosylabs/proctor/server/cmd/proctor/commands"
)

func main() {
	os.Exit(runMain())
}

func runMain() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return commands.Run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
}
