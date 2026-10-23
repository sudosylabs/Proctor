// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/sudosylabs/proctor/server"
)

type clusterStatusExecutor func(context.Context, string) (server.ClusterStatus, error)

func newClusterCommand(status clusterStatusExecutor, text commandText) *cobra.Command {
	command := &cobra.Command{
		Use:   "cluster",
		Short: text.value("cli.cluster.short", "Inspect the serving nodes of this installation", nil),
		Args:  noArgs,
		RunE: func(*cobra.Command, []string) error {
			return newUsageError(text.value("cli.cluster.error.requires_subcommand", "cluster requires a status subcommand", nil))
		},
	}
	var asJSON bool
	statusCommand := &cobra.Command{
		Use:   "status",
		Short: text.value("cli.cluster.status.short", "List serving node leases, discovery rows, and the schema version without writing", nil),
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			path, err := configPath(command, text)
			if err != nil {
				return err
			}
			result, err := status(command.Context(), path)
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(command.OutOrStdout(), result)
			}
			return writeClusterStatus(command.OutOrStdout(), result, text)
		},
	}
	statusCommand.Flags().BoolVar(&asJSON, "json", false, text.value("cli.cluster.status.flag.json", "write the cluster status as JSON", nil))
	command.AddCommand(statusCommand)
	return command
}

func writeClusterStatus(output io.Writer, status server.ClusterStatus, text commandText) error {
	if _, err := fmt.Fprintln(output, text.value(
		"cli.cluster.status.schema",
		"database schema version {{.DatabaseVersion}}; server schema version {{.ServerVersion}}; pending migrations {{.PendingMigrations}}",
		map[string]any{
			"DatabaseVersion":   status.Schema.DatabaseVersion,
			"ServerVersion":     status.Schema.ServerVersion,
			"PendingMigrations": status.Schema.PendingMigrations,
		},
	)); err != nil {
		return err
	}
	if !status.DatabaseTime.IsZero() {
		if _, err := fmt.Fprintln(output, text.value(
			"cli.cluster.status.database_time", "database time {{.Time}}",
			map[string]any{"Time": formatInstant(status.DatabaseTime)},
		)); err != nil {
			return err
		}
	}

	table := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(table)
	_, _ = fmt.Fprintln(table, text.value("cli.cluster.status.serving_title", "Serving nodes", nil))
	if len(status.ServingNodes) == 0 {
		_, _ = fmt.Fprintln(table, text.value("cli.cluster.status.none", "(none)", nil))
	} else {
		_, _ = fmt.Fprintln(table, text.value("cli.cluster.status.serving_header", "NODE\tSTATE\tUPDATED\tEXPIRES\tLEASE", nil))
		for _, node := range status.ServingNodes {
			_, _ = fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n", node.NodeID, liveness(node.Live, text),
				formatInstant(node.UpdatedAt), formatInstant(node.ExpiresAt), node.LeaseID)
		}
	}
	_, _ = fmt.Fprintln(table)
	_, _ = fmt.Fprintln(table, text.value("cli.cluster.status.discovery_title", "Cluster discovery", nil))
	if len(status.Discovery) == 0 {
		_, _ = fmt.Fprintln(table, text.value("cli.cluster.status.none", "(none)", nil))
	} else {
		_, _ = fmt.Fprintln(table, text.value("cli.cluster.status.discovery_header", "NODE\tSTATE\tADDRESS\tVERSION\tPROTOCOL\tUPDATED\tEXPIRES", nil))
		for _, node := range status.Discovery {
			_, _ = fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", node.NodeID, liveness(node.Live, text),
				node.AdvertiseAddress, node.ServerVersion,
				strconv.Itoa(node.ProtocolMin)+"-"+strconv.Itoa(node.ProtocolMax),
				formatInstant(node.UpdatedAt), formatInstant(node.ExpiresAt))
		}
	}
	if status.Truncated {
		_, _ = fmt.Fprintln(table)
		_, _ = fmt.Fprintln(table, text.value("cli.cluster.status.truncated", "output stopped at its row bound; more rows exist", nil))
	}
	return table.Flush()
}

func liveness(live bool, text commandText) string {
	if live {
		return text.value("cli.cluster.status.live", "live", nil)
	}
	return text.value("cli.cluster.status.expired", "expired", nil)
}

func formatInstant(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.UTC().Format(time.RFC3339)
}

func writeJSON(output io.Writer, value any) error {
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
