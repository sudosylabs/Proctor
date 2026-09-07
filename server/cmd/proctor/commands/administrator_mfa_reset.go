// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package commands

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	server "github.com/sudosylabs/proctor/server"
)

type administratorMFAResetExecutor func(context.Context, string, server.AdministratorMFAResetCommand) (*server.AdministratorMFAResetResult, error)

func newAdministratorMFAResetCommand(reset administratorMFAResetExecutor, text commandText) *cobra.Command {
	var institutionID, userID string
	command := &cobra.Command{
		Use:   "reset-mfa",
		Short: text.value("cli.administrator.reset_mfa.short", "Reset an administrator's MFA while all nodes are stopped", nil),
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 0 {
				return newUsageError(text.value("cli.administrator.reset_mfa.arguments", "administrator reset-mfa accepts no positional arguments or authentication proofs", nil))
			}
			return nil
		},
		RunE: func(command *cobra.Command, _ []string) error {
			institutionID, userID = strings.TrimSpace(institutionID), strings.TrimSpace(userID)
			if institutionID == "" || userID == "" {
				return newUsageError(text.value("cli.administrator.reset_mfa.input", "administrator reset-mfa requires --institution-id and --user-id", nil))
			}
			path, err := configPath(command, text)
			if err != nil {
				return err
			}
			if reset == nil {
				return errors.New(text.value("cli.administrator.reset_mfa.unavailable", "administrator MFA reset is unavailable", nil))
			}
			result, err := reset(command.Context(), path, server.AdministratorMFAResetCommand{InstitutionID: institutionID, UserID: userID})
			if err != nil {
				return err
			}
			if result == nil || !result.ReenrollmentRequired || result.RecoveryGeneration < 1 {
				return errors.New(text.value("cli.administrator.reset_mfa.no_result", "administrator MFA reset returned no recovery restriction", nil))
			}
			_, err = fmt.Fprintln(command.OutOrStdout(), text.value("cli.administrator.reset_mfa.success", "administrator MFA reset recorded; restart Proctor, sign in with the existing password or permitted provider, and enroll a new authenticator", nil))
			return err
		},
	}
	command.Flags().StringVar(&institutionID, "institution-id", "", text.value("cli.administrator.flag.institution_id", "exact Institution identifier to confirm", nil))
	command.Flags().StringVar(&userID, "user-id", "", text.value("cli.administrator.flag.user_id", "active system-administrator User identifier", nil))
	command.SetFlagErrorFunc(func(*cobra.Command, error) error {
		return newUsageError(text.value("cli.administrator.reset_mfa.invalid_flags", "administrator reset-mfa contains an invalid flag or flag value", nil))
	})
	return command
}
