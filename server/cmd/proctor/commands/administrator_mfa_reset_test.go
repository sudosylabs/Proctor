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
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server"
)

type unusedMFAResetInput struct{ t *testing.T }

func (r unusedMFAResetInput) Read([]byte) (int, error) {
	require.FailNow(r.t, "MFA reset read a credential from stdin")
	return 0, nil
}

func TestAdministratorMFAResetIsASeparateHostCommandWithNoCredentialInput(t *testing.T) {
	t.Parallel()
	execute := testExecutors()
	called := false
	execute.resetAdministratorMFA = func(ctx context.Context, path string, command server.AdministratorMFAResetCommand) (*server.AdministratorMFAResetResult, error) {
		called = true
		require.NotNil(t, ctx, "forwarded path=%q command=%#v", path, command)
		require.Equal(t, "/etc/proctor.json", path, "forwarded path=%q command=%#v", path, command)
		require.Equal(t, "ybndrfg8ejkmcpqxot1uwisza3", command.InstitutionID, "forwarded path=%q command=%#v", path, command)
		require.Equal(t, "ybndrfg8ejkmcpqxot1uwisza4", command.UserID, "forwarded path=%q command=%#v", path, command)
		return &server.AdministratorMFAResetResult{RecoveryGeneration: 1, ReenrollmentRequired: true}, nil
	}
	code, stdout, stderr := executeForTest(context.Background(), []string{"administrator", "reset-mfa", "--config", "/etc/proctor.json",
		"--institution-id", " ybndrfg8ejkmcpqxot1uwisza3 ", "--user-id", " ybndrfg8ejkmcpqxot1uwisza4 "}, unusedMFAResetInput{t}, execute)
	require.Equal(t, 0, code, "code")
	require.True(t, called, "code")
	require.Empty(t, stderr, "code")
	require.Contains(t, stdout, "enroll a new authenticator", "code")
}

func TestAdministratorMFAResetRejectsSecretArgumentsWithoutEcho(t *testing.T) {
	t.Parallel()
	for _, extra := range [][]string{{"private-proof"}, {"--password=private-proof"}, {"--enable-local-login"}, {"--rotate-password"}} {
		execute := testExecutors()
		execute.resetAdministratorMFA = func(context.Context, string, server.AdministratorMFAResetCommand) (*server.AdministratorMFAResetResult, error) {
			require.FailNow(t, "invalid MFA reset reached execution")
			return nil, nil
		}
		args := append([]string{"administrator", "reset-mfa", "--institution-id", "ybndrfg8ejkmcpqxot1uwisza3", "--user-id", "ybndrfg8ejkmcpqxot1uwisza4"}, extra...)
		code, stdout, stderr := executeForTest(context.Background(), args, unusedMFAResetInput{t}, execute)
		require.Equal(t, 2, code, "code")
		require.Empty(t, stdout, "code")
		require.NotContains(t, stderr, "private-proof", "code")
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
		require.Equal(t, 1, code, "code")
		require.Empty(t, stdout, "code")
		require.NotEmpty(t, stderr, "code")
	}
}
