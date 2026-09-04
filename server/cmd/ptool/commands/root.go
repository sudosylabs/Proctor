// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package commands

import (
	"context"
	"io"

	"github.com/spf13/cobra"
)

// Execute runs ptool with explicit process dependencies for testability.
func Execute(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	command := NewRootCommand()
	command.SetArgs(args)
	command.SetOut(stdout)
	command.SetErr(stderr)
	return command.ExecuteContext(ctx)
}

// NewRootCommand constructs a fresh ptool command tree.
func NewRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "ptool",
		Short:         "Proctor repository development tools",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newI18nCommand())
	root.AddCommand(newOpenAPICommand())
	root.AddCommand(newReleaseCommand())
	return root
}
