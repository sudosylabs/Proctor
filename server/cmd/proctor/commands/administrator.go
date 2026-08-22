// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	server "github.com/sudosylabs/proctor/server"
)

type administratorRecoveryExecutor func(context.Context, string, server.AdministratorRecoveryCommand) (*server.AdministratorRecoveryResult, error)

func newAdministratorCommand(stdin io.Reader, recover administratorRecoveryExecutor, text commandText) *cobra.Command {
	command := &cobra.Command{
		Use:   "administrator",
		Short: text.value("cli.administrator.short", "Perform host-only administrator operations", nil),
		Args: func(_ *cobra.Command, args []string) error {
			return administratorArgs(text, args)
		},
		RunE: func(*cobra.Command, []string) error {
			return newUsageError(text.value("cli.administrator.error.requires_subcommand", "administrator requires a subcommand", nil))
		},
	}

	var institutionID string
	var userID string
	var enableLocalLogin bool
	var rotatePassword bool
	recoverCommand := &cobra.Command{
		Use:   "recover",
		Short: text.value("cli.administrator.recover.short", "Recover an administrator's local authentication path while all nodes are stopped", nil),
		Args: func(_ *cobra.Command, args []string) error {
			return administratorRecoveryArgs(text, args)
		},
		RunE: func(command *cobra.Command, _ []string) error {
			institutionID = strings.TrimSpace(institutionID)
			userID = strings.TrimSpace(userID)
			if institutionID == "" || userID == "" || (!enableLocalLogin && !rotatePassword) {
				return newUsageError(text.value("cli.administrator.error.requires_recovery_input", "administrator recover requires --institution-id, --user-id, and at least one recovery action", nil))
			}

			password := ""
			if rotatePassword {
				var err error
				password, err = readPrivatePassword(stdin, text)
				if err != nil {
					return err
				}
			}
			path, err := configPath(command, text)
			if err != nil {
				return err
			}
			result, err := recover(command.Context(), path, server.AdministratorRecoveryCommand{
				InstitutionID:    institutionID,
				UserID:           userID,
				EnableLocalLogin: enableLocalLogin,
				Password:         password,
			})
			password = ""
			if err != nil {
				return err
			}
			if result == nil {
				return errors.New(text.value("cli.administrator.error.no_result", "administrator recovery returned no result", nil))
			}
			_, err = fmt.Fprintln(command.OutOrStdout(), text.value("cli.administrator.recover.success", "administrator recovery recorded; restart Proctor normally to reconcile its audit record", nil))
			return err
		},
	}
	recoverCommand.Flags().StringVar(&institutionID, "institution-id", "", text.value("cli.administrator.flag.institution_id", "exact Institution identifier to confirm", nil))
	recoverCommand.Flags().StringVar(&userID, "user-id", "", text.value("cli.administrator.flag.user_id", "active system-administrator User identifier", nil))
	recoverCommand.Flags().BoolVar(&enableLocalLogin, "enable-local-login", false, text.value("cli.administrator.flag.enable_local_login", "re-enable local login", nil))
	recoverCommand.Flags().BoolVar(&rotatePassword, "rotate-password", false, text.value("cli.administrator.flag.rotate_password", "read and rotate the password from private input", nil))
	recoverCommand.SetFlagErrorFunc(func(*cobra.Command, error) error {
		return newUsageError(text.value("cli.administrator.error.invalid_flags", "administrator recover contains an invalid flag or flag value", nil))
	})
	command.AddCommand(recoverCommand)
	return command
}

func administratorArgs(text commandText, args []string) error {
	if len(args) != 0 {
		return newUsageError(text.value("cli.administrator.error.requires_recover", "administrator requires the recover subcommand", nil))
	}
	return nil
}

func administratorRecoveryArgs(text commandText, args []string) error {
	if len(args) != 0 {
		return newUsageError(text.value("cli.administrator.error.unexpected_arguments", "administrator recover does not accept positional arguments or passwords", nil))
	}
	return nil
}
