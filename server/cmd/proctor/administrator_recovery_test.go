// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	server "github.com/sudosylabs/proctor/server"
)

func TestRunAdministratorRecoveryReadsPasswordOnlyFromPrivateInput(t *testing.T) {
	t.Parallel()
	secret := "private-password-never-print"
	var received server.AdministratorRecoveryCommand
	executor := func(_ context.Context, path string, command server.AdministratorRecoveryCommand) (*server.AdministratorRecoveryResult, error) {
		if path != "/etc/proctor.json" {
			t.Fatalf("config path = %q", path)
		}
		received = command
		return &server.AdministratorRecoveryResult{LocalLoginEnabled: true, PasswordRotated: true}, nil
	}
	var stdout, stderr bytes.Buffer
	err := runAdministratorRecover(context.Background(), []string{
		"--config", "/etc/proctor.json",
		"--institution-id", "ybndrfg8ejkmcpqxot1uwisza3",
		"--user-id", "ybndrfg8ejkmcpqxot1uwisza4",
		"--enable-local-login", "--rotate-password",
	}, strings.NewReader(secret+"\n"), &stdout, &stderr, executor)
	if err != nil {
		t.Fatal(err)
	}
	if received.Password != secret || !received.EnableLocalLogin {
		t.Fatalf("command = %#v", received)
	}
	if strings.Contains(stdout.String(), secret) || strings.Contains(stderr.String(), secret) {
		t.Fatalf("secret leaked: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if stdout.String() != "administrator recovery recorded; restart Proctor normally to reconcile its audit record\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunAdministratorRecoveryRejectsPasswordInArguments(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"--institution-id", "ybndrfg8ejkmcpqxot1uwisza3", "--user-id", "ybndrfg8ejkmcpqxot1uwisza4", "--password", "leaked"},
		{"--institution-id", "ybndrfg8ejkmcpqxot1uwisza3", "--user-id", "ybndrfg8ejkmcpqxot1uwisza4"},
	} {
		var stdout, stderr bytes.Buffer
		err := runAdministratorRecover(context.Background(), args, strings.NewReader(""), &stdout, &stderr,
			func(context.Context, string, server.AdministratorRecoveryCommand) (*server.AdministratorRecoveryResult, error) {
				t.Fatal("executor was called")
				return nil, nil
			})
		if err == nil {
			t.Fatalf("runAdministratorRecover(%#v) error = nil", args)
		}
	}
}
