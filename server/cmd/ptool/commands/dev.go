// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package commands

import (
	"github.com/spf13/cobra"
	"github.com/sudosylabs/proctor/server/cmd/ptool/devseed"
)

func newDevCommand() *cobra.Command {
	dev := &cobra.Command{Use: "dev", Short: "Local development fixtures"}
	var options devseed.Options
	seed := &cobra.Command{
		Use: "seed", Short: "Create, resume, or verify a synthetic development dataset", Args: cobra.NoArgs,
		Long: "Seed a pristine loopback installation through its public API. Repeated runs resume the private journal or verify the completed fixture without overwriting developer edits. Requires a running server and local Mailpit. Does not simulate live exams.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return devseed.Run(cmd.Context(), options, cmd.OutOrStdout())
		},
	}
	seed.Flags().StringVar(&options.ServerURL, "server", "http://localhost:8065", "Canonical loopback Proctor origin")
	seed.Flags().StringVar(&options.MailpitURL, "mailpit", "http://127.0.0.1:18025", "Loopback Mailpit origin")
	seed.Flags().StringVar(&options.StateDir, "state-dir", ".build/dev/seed", "Private dataset journal and manifest directory (relative to working directory)")
	seed.Flags().StringVar(&options.EnvironmentFile, "environment-file", ".build/dev/secrets/environment", "Generated bootstrap environment file (relative to working directory)")
	seed.Flags().StringVar(&options.Profile, "profile", "desktop", "Dataset size: small, desktop, or large")
	seed.Flags().Int64Var(&options.Seed, "seed", 42, "Reproducible synthetic-content seed; credentials remain random")
	seed.Flags().BoolVar(&options.RefreshSittings, "refresh-sittings", false, "Add another generation of future Sittings to a completed fixture")
	dev.AddCommand(seed)
	return dev
}
