// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"strings"
	"testing"

	"github.com/sudosylabs/proctor/server/app"
)

type administratorRecoveryRuntimeFake struct {
	command app.AdministratorRecoveryCommand
	result  *app.AdministratorRecoveryResult
	err     error
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
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || !result.PasswordRotated || fake.command.Password != "private" ||
		fake.command.InstitutionID != command.InstitutionID || fake.command.UserID != command.UserID {
		t.Fatalf("result/forwarded = %#v / %#v", result, fake.command)
	}
	node.state = nodeRunning
	if _, err := node.RecoverAdministratorAccess(context.Background(), command); err == nil {
		t.Fatal("RecoverAdministratorAccess() succeeded on a running server")
	}
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
	if facade.Ready() {
		t.Fatal("New().Ready() = true before Start()")
	}
	if err := facade.Close(); err != nil {
		t.Fatalf("New().Close() error = %v", err)
	}
	assertLifecycleEvents(t, events, "transport-close", "platform-close")
}

func TestNewRejectsEmptyConfigurationPath(t *testing.T) {
	t.Parallel()

	_, err := New(context.Background(), WithConfigPath(""))
	if err == nil || !strings.Contains(err.Error(), "configuration path is empty") {
		t.Fatalf("New() error = %v, want empty configuration path", err)
	}
}
