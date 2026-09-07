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
	"strings"
	"testing"

	server "github.com/sudosylabs/proctor/server"
)

type unusedMFAResetInput struct{ t *testing.T }

func (r unusedMFAResetInput) Read([]byte) (int, error) {
	r.t.Fatal("MFA reset read a credential from stdin")
	return 0, nil
}

func TestAdministratorMFAResetIsASeparateHostCommandWithNoCredentialInput(t *testing.T) {
	t.Parallel()
	execute := testExecutors()
	called := false
	execute.resetAdministratorMFA = func(ctx context.Context, path string, command server.AdministratorMFAResetCommand) (*server.AdministratorMFAResetResult, error) {
		called = true
		if ctx == nil || path != "/etc/proctor.json" || command.InstitutionID != "ybndrfg8ejkmcpqxot1uwisza3" || command.UserID != "ybndrfg8ejkmcpqxot1uwisza4" {
			t.Fatalf("forwarded path=%q command=%#v", path, command)
		}
		return &server.AdministratorMFAResetResult{RecoveryGeneration: 1, ReenrollmentRequired: true}, nil
	}
	code, stdout, stderr := executeForTest(context.Background(), []string{"administrator", "reset-mfa", "--config", "/etc/proctor.json",
		"--institution-id", " ybndrfg8ejkmcpqxot1uwisza3 ", "--user-id", " ybndrfg8ejkmcpqxot1uwisza4 "}, unusedMFAResetInput{t}, execute)
	if code != 0 || !called || stderr != "" || !strings.Contains(stdout, "enroll a new authenticator") {
		t.Fatalf("code=%d called=%v stdout=%q stderr=%q", code, called, stdout, stderr)
	}
}

func TestAdministratorMFAResetRejectsSecretArgumentsWithoutEcho(t *testing.T) {
	t.Parallel()
	for _, extra := range [][]string{{"private-proof"}, {"--password=private-proof"}, {"--enable-local-login"}, {"--rotate-password"}} {
		execute := testExecutors()
		execute.resetAdministratorMFA = func(context.Context, string, server.AdministratorMFAResetCommand) (*server.AdministratorMFAResetResult, error) {
			t.Fatal("invalid MFA reset reached execution")
			return nil, nil
		}
		args := append([]string{"administrator", "reset-mfa", "--institution-id", "ybndrfg8ejkmcpqxot1uwisza3", "--user-id", "ybndrfg8ejkmcpqxot1uwisza4"}, extra...)
		code, stdout, stderr := executeForTest(context.Background(), args, unusedMFAResetInput{t}, execute)
		if code != 2 || stdout != "" || strings.Contains(stderr, "private-proof") {
			t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
	}
}

func TestAdministratorMFAResetDoesNotReportSuccessWithoutCommittedRestriction(t *testing.T) {
	t.Parallel()
	for _, failure := range []error{nil, errors.New("serving node is active")} {
		execute := testExecutors()
		execute.resetAdministratorMFA = func(context.Context, string, server.AdministratorMFAResetCommand) (*server.AdministratorMFAResetResult, error) {
			return nil, failure
		}
		code, stdout, stderr := executeForTest(context.Background(), []string{"administrator", "reset-mfa", "--institution-id", "ybndrfg8ejkmcpqxot1uwisza3", "--user-id", "ybndrfg8ejkmcpqxot1uwisza4"}, unusedMFAResetInput{t}, execute)
		if code != 1 || stdout != "" || stderr == "" {
			t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
	}
}
