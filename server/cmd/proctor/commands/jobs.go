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
	"io"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/sudosylabs/proctor/server"
)

type jobListExecutor func(context.Context, string, server.JobQuery) (server.JobList, error)
type jobShowExecutor func(context.Context, string, string) (server.JobDetail, error)

func newJobsCommand(list jobListExecutor, show jobShowExecutor, text commandText) *cobra.Command {
	command := &cobra.Command{
		Use:   "jobs",
		Short: text.value("cli.jobs.short", "Inspect durable background Jobs without changing them", nil),
		Args:  noArgs,
		RunE: func(*cobra.Command, []string) error {
			return newUsageError(text.value("cli.jobs.error.requires_subcommand", "jobs requires a list or show subcommand", nil))
		},
	}
	command.AddCommand(newJobsListCommand(list, text), newJobsShowCommand(show, text))
	return command
}

func newJobsListCommand(list jobListExecutor, text commandText) *cobra.Command {
	var types, states []string
	var limit int
	var asJSON bool
	command := &cobra.Command{
		Use:   "list",
		Short: text.value("cli.jobs.list.short", "List the newest Jobs with their state, attempts, and last error code", nil),
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if err := requireKnown(types, server.JobTypes(), "cli.jobs.error.unknown_type", "unknown job type {{.Value}}; known types: {{.Known}}", text); err != nil {
				return err
			}
			if err := requireKnown(states, server.JobStates(), "cli.jobs.error.unknown_state", "unknown job state {{.Value}}; known states: {{.Known}}", text); err != nil {
				return err
			}
			if limit < 1 || limit > server.JobListMaximumLimit {
				return newUsageError(text.value("cli.jobs.error.limit", "--limit must be between 1 and {{.Maximum}}",
					map[string]any{"Maximum": server.JobListMaximumLimit}))
			}
			path, err := configPath(command, text)
			if err != nil {
				return err
			}
			result, err := list(command.Context(), path, server.JobQuery{Types: types, States: states, Limit: limit})
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(command.OutOrStdout(), result)
			}
			return writeJobList(command.OutOrStdout(), result, text)
		},
	}
	flags := command.Flags()
	flags.StringSliceVar(&types, "type", nil, text.value("cli.jobs.list.flag.type", "only list Jobs of this type (repeatable or comma-separated)", nil))
	flags.StringSliceVar(&states, "state", nil, text.value("cli.jobs.list.flag.state", "only list Jobs in this state (repeatable or comma-separated)", nil))
	flags.IntVar(&limit, "limit", server.JobListDefaultLimit, text.value("cli.jobs.list.flag.limit", "list at most this many Jobs (at most 200)", nil))
	flags.BoolVar(&asJSON, "json", false, text.value("cli.jobs.flag.json", "write the result as JSON", nil))
	return command
}

func newJobsShowCommand(show jobShowExecutor, text commandText) *cobra.Command {
	var asJSON bool
	command := &cobra.Command{
		Use:   "show <job-id>",
		Short: text.value("cli.jobs.show.short", "Show one Job and its most recent attempts", nil),
		Args: func(command *cobra.Command, args []string) error {
			return wrapUsageError(cobra.ExactArgs(1)(command, args))
		},
		RunE: func(command *cobra.Command, args []string) error {
			path, err := configPath(command, text)
			if err != nil {
				return err
			}
			result, err := show(command.Context(), path, args[0])
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(command.OutOrStdout(), result)
			}
			return writeJobDetail(command.OutOrStdout(), result, text)
		},
	}
	command.Flags().BoolVar(&asJSON, "json", false, text.value("cli.jobs.flag.json", "write the result as JSON", nil))
	return command
}

func requireKnown(values, known []string, id, fallback string, text commandText) error {
	for _, value := range values {
		if !slices.Contains(known, value) {
			return newUsageError(text.value(id, fallback, map[string]any{"Value": strconv.Quote(value), "Known": strings.Join(known, ", ")}))
		}
	}
	return nil
}

func writeJobList(output io.Writer, list server.JobList, text commandText) error {
	if len(list.Jobs) == 0 {
		_, err := fmt.Fprintln(output, text.value("cli.jobs.list.none", "no Jobs match", nil))
		return err
	}
	table := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(table, text.value("cli.jobs.list.header", "ID\tTYPE\tSTATE\tATTEMPTS\tLAST ERROR\tAVAILABLE\tUPDATED", nil))
	for _, job := range list.Jobs {
		_, _ = fmt.Fprintf(table, "%s\t%s\t%s\t%d/%d\t%s\t%s\t%s\n", job.ID, job.Type, job.State,
			job.AttemptCount, job.MaximumAttempts, orDash(job.LastErrorCode),
			formatInstant(job.AvailableAt), formatInstant(job.UpdatedAt))
	}
	if err := table.Flush(); err != nil {
		return err
	}
	if list.HasMore {
		_, err := fmt.Fprintln(output, text.value("cli.jobs.list.more", "more Jobs match; narrow --type or --state, or raise --limit", nil))
		return err
	}
	return nil
}

func writeJobDetail(output io.Writer, job server.JobDetail, text commandText) error {
	table := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	rows := [][2]string{
		{text.value("cli.jobs.show.id", "ID", nil), job.ID},
		{text.value("cli.jobs.show.type", "Type", nil), job.Type},
		{text.value("cli.jobs.show.state", "State", nil), job.State},
		{text.value("cli.jobs.show.attempts", "Attempts", nil), strconv.Itoa(job.AttemptCount) + "/" + strconv.Itoa(job.MaximumAttempts)},
		{text.value("cli.jobs.show.last_error", "Last error code", nil), orDash(job.LastErrorCode)},
		{text.value("cli.jobs.show.available", "Available", nil), formatInstant(job.AvailableAt)},
		{text.value("cli.jobs.show.created", "Created", nil), formatInstant(job.CreatedAt)},
		{text.value("cli.jobs.show.updated", "Updated", nil), formatInstant(job.UpdatedAt)},
		{text.value("cli.jobs.show.started", "Started", nil), formatOptionalInstant(job.StartedAt)},
		{text.value("cli.jobs.show.completed", "Completed", nil), formatOptionalInstant(job.CompletedAt)},
	}
	if job.Progress != nil {
		rows = append(rows, [2]string{text.value("cli.jobs.show.progress", "Progress", nil),
			fmt.Sprintf("%d/%d %s", job.Progress.Current, job.Progress.Total, job.Progress.Stage)})
	}
	for _, row := range rows {
		_, _ = fmt.Fprintf(table, "%s:\t%s\n", row[0], row[1])
	}
	if err := table.Flush(); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(output); err != nil {
		return err
	}
	if len(job.Attempts) == 0 {
		_, err := fmt.Fprintln(output, text.value("cli.jobs.show.no_attempts", "no attempts yet", nil))
		return err
	}
	table = tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(table, text.value("cli.jobs.show.attempt_header", "ATTEMPT\tSTATE\tNODE\tSTARTED\tHEARTBEAT\tLEASE EXPIRES\tCOMPLETED\tERROR", nil))
	for _, attempt := range job.Attempts {
		_, _ = fmt.Fprintf(table, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", attempt.Number, attempt.State, attempt.NodeID,
			formatInstant(attempt.StartedAt), formatInstant(attempt.HeartbeatAt), formatInstant(attempt.LeaseExpiresAt),
			formatOptionalInstant(attempt.CompletedAt), orDash(attempt.ErrorCode))
	}
	if err := table.Flush(); err != nil {
		return err
	}
	if job.MoreAttempts {
		_, err := fmt.Fprintln(output, text.value("cli.jobs.show.more_attempts", "older attempts are not shown", nil))
		return err
	}
	return nil
}

func formatOptionalInstant(value *time.Time) string {
	if value == nil {
		return "-"
	}
	return formatInstant(*value)
}

func orDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}
