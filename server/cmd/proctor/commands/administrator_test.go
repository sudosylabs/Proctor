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

	server "github.com/sudosylabs/proctor/server"
)

func TestAdministratorRecoveryReadsPasswordOnlyFromPrivateInput(t *testing.T) {
	t.Parallel()

	const secret = "private-password-never-print"
	var received server.AdministratorRecoveryCommand
	execute := testExecutors()
	execute.recoverAdministrator = func(_ context.Context, path string, command server.AdministratorRecoveryCommand) (*server.AdministratorRecoveryResult, error) {
		if path != "/etc/proctor.json" {
			t.Fatalf("config path = %q", path)
		}
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
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	if received.InstitutionID != "ybndrfg8ejkmcpqxot1uwisza3" || received.UserID != "ybndrfg8ejkmcpqxot1uwisza4" ||
		received.Password != secret || !received.EnableLocalLogin {
		t.Fatalf("command = %#v", received)
	}
	if strings.Contains(stdout, secret) || strings.Contains(stderr, secret) {
		t.Fatalf("secret leaked: stdout=%q stderr=%q", stdout, stderr)
	}
	if stdout != "administrator recovery recorded; restart Proctor normally to reconcile its audit record\n" {
		t.Fatalf("stdout = %q", stdout)
	}
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
			t.Fatal("recovery executor was called")
			return nil, nil
		}
		code, stdout, stderr := executeForTest(context.Background(), args, strings.NewReader(""), execute)
		if code != 2 || stdout != "" {
			t.Fatalf("run(%q): code=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
		if strings.Contains(stdout, "leaked-password") || strings.Contains(stderr, "leaked-password") {
			t.Fatalf("argument secret leaked: stdout=%q stderr=%q", stdout, stderr)
		}
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
	if code != 1 || stdout != "" || !strings.Contains(stderr, "administrator recovery returned no result") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}
