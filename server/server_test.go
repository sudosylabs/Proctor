// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package server

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app"
)

type administratorRecoveryRuntimeFake struct {
	command    app.AdministratorRecoveryCommand
	result     *app.AdministratorRecoveryResult
	err        error
	mfaCommand app.AdministratorMFAResetCommand
	mfaResult  *app.AdministratorMFAResetResult
}

func (f *administratorRecoveryRuntimeFake) ResetAdministratorMFA(_ context.Context, command app.AdministratorMFAResetCommand) (*app.AdministratorMFAResetResult, error) {
	f.mfaCommand = command
	return f.mfaResult, f.err
}

func (f *administratorRecoveryRuntimeFake) RecoverAdministratorAccess(_ context.Context, command app.AdministratorRecoveryCommand) (*app.AdministratorRecoveryResult, error) {
	f.command = command
	return f.result, f.err
}

func TestInertServerExposesOnlyExplicitAdministratorRecoveryCapability(t *testing.T) {
	t.Parallel()
	fake := &administratorRecoveryRuntimeFake{result: &app.AdministratorRecoveryResult{PasswordRotated: true}}
	node := &Server{components: runtimeComponents{administratorRecovery: fake}}
	command := AdministratorRecoveryCommand{
		InstitutionID: "institution", UserID: "user", EnableLocalLogin: true, Password: "private",
	}
	result, err := node.RecoverAdministratorAccess(context.Background(), command)
	require.NoError(t, err)
	require.NotNil(t, result, "result/forwarded = %#v / %#v", result, fake.command)
	require.True(t, result.PasswordRotated, "result/forwarded = %#v / %#v", result, fake.command)
	require.Equal(t, "private", fake.command.Password, "result/forwarded = %#v / %#v", result, fake.command)
	require.Equal(t, command.InstitutionID, fake.command.InstitutionID, "result/forwarded = %#v / %#v", result, fake.command)
	require.Equal(t, command.UserID, fake.command.UserID, "result/forwarded = %#v / %#v", result, fake.command)
	node.state = nodeRunning
	_, err = node.RecoverAdministratorAccess(context.Background(), command)
	require.Error(t, err, "RecoverAdministratorAccess() succeeded on a running server")
}

func TestInertServerFacadeOwnsConstructedRuntime(t *testing.T) {
	t.Parallel()

	events := &lifecycleEvents{}
	readiness := &lifecycleReadiness{}
	components := runtimeComponents{
		platform:  &lifecyclePlatform{events: events},
		transport: &lifecycleTransport{events: events},
		readiness: readiness,
	}
	facade := &Server{components: components}
	require.False(t, facade.Ready(), "New().Ready() = true before Run()")
	require.NoError(t, facade.Close())
	assertLifecycleEvents(t, events, "transport-close", "platform-close")
}

func TestNewRejectsEmptyConfigurationPath(t *testing.T) {
	t.Parallel()

	_, err := New(context.Background(), WithConfigPath(""))
	require.Error(t, err, "New() error")
	require.Contains(t, err.Error(), "configuration path is empty", "New() error")
}

func TestWithReadyNotifierRejectsNil(t *testing.T) {
	t.Parallel()

	settings := options{}
	err := WithReadyNotifier(nil)(&settings)
	require.Error(t, err)
	require.Contains(t, err.Error(), "ready notifier is nil")
}
