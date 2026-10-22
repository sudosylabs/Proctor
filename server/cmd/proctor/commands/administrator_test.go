// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package commands

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server"
)

func TestAdministratorRecoveryReadsPasswordOnlyFromPrivateInput(t *testing.T) {
	t.Parallel()

	const secret = "private-password-never-print"
	var received server.AdministratorRecoveryCommand
	execute := testExecutors()
	execute.recoverAdministrator = func(_ context.Context, path string, command server.AdministratorRecoveryCommand) (*server.AdministratorRecoveryResult, error) {
		require.Equal(t, "/etc/proctor.json", path)
		received = command
		return &server.AdministratorRecoveryResult{LocalLoginEnabled: true, PasswordRotated: true}, nil
	}
	code, stdout, stderr := executeForTest(context.Background(), []string{
		"administrator", "recover",
		"--config", "/etc/proctor.json",
		"--institution-id", " ybndrfg8ejkmcpqxot1uwisza3 ",
		"--user-id", " ybndrfg8ejkmcpqxot1uwisza4 ",
		"--enable-local-login", "--rotate-password",
	}, strings.NewReader(secret+"\n"), execute)
	require.Equal(t, 0, code, "code=%d stderr=%q", code, stderr)
	require.Equal(t, "ybndrfg8ejkmcpqxot1uwisza3", received.InstitutionID)
	require.Equal(t, "ybndrfg8ejkmcpqxot1uwisza4", received.UserID)
	require.Equal(t, secret, received.Password)
	require.True(t, received.EnableLocalLogin)
	require.NotContains(t, stdout, secret, "secret leaked: stdout")
	require.NotContains(t, stderr, secret, "secret leaked: stdout")
	require.Equal(t, "administrator recovery recorded; restart Proctor normally to reconcile its audit record\n", stdout)
}

func TestAdministratorRecoveryRejectsPasswordArgumentsAndMissingActions(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"administrator", "leaked-password"},
		{
			"administrator", "recover",
			"--institution-id", "ybndrfg8ejkmcpqxot1uwisza3",
			"--user-id", "ybndrfg8ejkmcpqxot1uwisza4",
			"--password", "leaked-password",
		},
		{
			"administrator", "recover",
			"--institution-id", "ybndrfg8ejkmcpqxot1uwisza3",
			"--user-id", "ybndrfg8ejkmcpqxot1uwisza4",
			"--password=leaked-password",
		},
		{
			"administrator", "recover",
			"--institution-id", "ybndrfg8ejkmcpqxot1uwisza3",
			"--user-id", "ybndrfg8ejkmcpqxot1uwisza4",
			"--rotate-password=leaked-password",
		},
		{
			"administrator", "recover",
			"--institution-id", "ybndrfg8ejkmcpqxot1uwisza3",
			"--user-id", "ybndrfg8ejkmcpqxot1uwisza4",
			"--enable-local-login=leaked-password",
		},
		{
			"administrator", "recover",
			"--institution-id", "ybndrfg8ejkmcpqxot1uwisza3",
			"--user-id", "ybndrfg8ejkmcpqxot1uwisza4",
		},
		{
			"administrator", "recover",
			"--institution-id", "ybndrfg8ejkmcpqxot1uwisza3",
			"--user-id", "ybndrfg8ejkmcpqxot1uwisza4",
			"--enable-local-login",
			"leaked-password",
		},
	} {
		execute := testExecutors()
		execute.recoverAdministrator = func(context.Context, string, server.AdministratorRecoveryCommand) (*server.AdministratorRecoveryResult, error) {
			require.FailNow(t, "recovery executor was called")
			return nil, nil
		}
		code, stdout, stderr := executeForTest(context.Background(), args, strings.NewReader(""), execute)
		require.Equal(t, 2, code, "run(%q): code=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		require.Empty(t, stdout, "run(%q): code=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		require.NotContains(t, stdout, "leaked-password", "argument secret leaked: stdout")
		require.NotContains(t, stderr, "leaked-password", "argument secret leaked: stdout")
	}
}

func TestAdministratorRecoveryRejectsNilResult(t *testing.T) {
	t.Parallel()

	execute := testExecutors()
	execute.recoverAdministrator = func(context.Context, string, server.AdministratorRecoveryCommand) (*server.AdministratorRecoveryResult, error) {
		return nil, nil
	}
	code, stdout, stderr := executeForTest(context.Background(), []string{
		"administrator", "recover",
		"--institution-id", "ybndrfg8ejkmcpqxot1uwisza3",
		"--user-id", "ybndrfg8ejkmcpqxot1uwisza4",
		"--enable-local-login",
	}, nil, execute)
	require.Equal(t, 1, code, "code")
	require.Empty(t, stdout, "code")
	require.Contains(t, stderr, "administrator recovery returned no result", "code")
}
