// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"strings"
	"testing"
)

func TestNewConstructsUsableFacade(t *testing.T) {
	t.Parallel()

	events := []string{}
	readiness := &lifecycleReadiness{}
	components := runtimeComponents{
		platform:  &lifecyclePlatform{events: &events},
		transport: &lifecycleTransport{events: &events},
		readiness: readiness,
	}
	option := func(settings *options) error {
		settings.runtimeFactory = func(context.Context, string) (runtimeComponents, error) {
			return components, nil
		}
		return nil
	}
	facade, err := New(context.Background(), option)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
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
