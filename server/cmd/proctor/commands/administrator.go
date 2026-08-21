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

func newAdministratorCommand(stdin io.Reader, recover administratorRecoveryExecutor) *cobra.Command {
	command := &cobra.Command{
		Use:   "administrator",
		Short: "Perform host-only administrator operations",
		Args:  administratorArgs,
		RunE: func(*cobra.Command, []string) error {
			return newUsageError("administrator requires a subcommand")
		},
	}

	var institutionID string
	var userID string
	var enableLocalLogin bool
	var rotatePassword bool
	recoverCommand := &cobra.Command{
		Use:   "recover",
		Short: "Recover an administrator's local authentication path while all nodes are stopped",
		Args:  administratorRecoveryArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			institutionID = strings.TrimSpace(institutionID)
			userID = strings.TrimSpace(userID)
			if institutionID == "" || userID == "" || (!enableLocalLogin && !rotatePassword) {
				return newUsageError("administrator recover requires --institution-id, --user-id, and at least one recovery action")
			}

			password := ""
			if rotatePassword {
				var err error
				password, err = readPrivatePassword(stdin)
				if err != nil {
					return err
				}
			}
			path, err := configPath(command)
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
				return errors.New("administrator recovery returned no result")
			}
			_, err = fmt.Fprintln(command.OutOrStdout(), "administrator recovery recorded; restart Proctor normally to reconcile its audit record")
			return err
		},
	}
	recoverCommand.Flags().StringVar(&institutionID, "institution-id", "", "exact Institution identifier to confirm")
	recoverCommand.Flags().StringVar(&userID, "user-id", "", "active system-administrator User identifier")
	recoverCommand.Flags().BoolVar(&enableLocalLogin, "enable-local-login", false, "re-enable local login")
	recoverCommand.Flags().BoolVar(&rotatePassword, "rotate-password", false, "read and rotate the password from private input")
	recoverCommand.SetFlagErrorFunc(func(*cobra.Command, error) error {
		return newUsageError("administrator recover contains an invalid flag or flag value")
	})
	command.AddCommand(recoverCommand)
	return command
}

func administratorArgs(_ *cobra.Command, args []string) error {
	if len(args) != 0 {
		return newUsageError("administrator requires the recover subcommand")
	}
	return nil
}

func administratorRecoveryArgs(_ *cobra.Command, args []string) error {
	if len(args) != 0 {
		return newUsageError("administrator recover does not accept positional arguments or passwords")
	}
	return nil
}
