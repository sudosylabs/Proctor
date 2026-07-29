// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/testlib"
)

func TestTestlibConstructsOneSharedApplicationGraph(t *testing.T) {
	t.Parallel()

	helper := testlib.Setup(t)
	if helper.Server.App() != helper.App {
		t.Fatal("server and test helper do not share the same application")
	}
	if helper.App.Platform() != helper.Platform || helper.Server.Platform() != helper.Platform {
		t.Fatal("application graph contains more than one platform service")
	}
	if helper.Platform.ConfigStore() != helper.ConfigStore {
		t.Fatal("platform and test helper do not share the same configuration store")
	}
	if helper.App.Log() != helper.Platform.Log() {
		t.Fatal("application and platform do not share the same logger")
	}
	if helper.Platform.Store() != helper.Store {
		t.Fatal("platform and test helper do not share the same persistence store")
	}
	if helper.App.Store() != helper.Store {
		t.Fatal("application and platform do not share the same persistence store")
	}
}

func TestServerStopsWhenContextIsCanceled(t *testing.T) {
	t.Parallel()

	helper := testlib.Setup(t, testlib.WithConfig(func(cfg *config.Config) {
		cfg.Server.ListenAddress = "127.0.0.1:0"
		cfg.Server.ShutdownTimeout.Duration = time.Second
	}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() {
		done <- helper.Server.Start(ctx)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Start() error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not stop after context cancellation")
	}
}

func TestServerCannotStartTwice(t *testing.T) {
	t.Parallel()

	helper := testlib.Setup(t, testlib.WithConfig(func(cfg *config.Config) {
		cfg.Server.ListenAddress = "127.0.0.1:0"
		cfg.Server.ShutdownTimeout.Duration = time.Second
	}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := helper.Server.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := helper.Server.Start(context.Background()); err == nil {
		t.Fatal("second Start() was accepted")
	}
}

func TestCloseStopsARunningServerAndWaitsForItsLifecycle(t *testing.T) {
	t.Parallel()

	helper := testlib.Setup(t, testlib.WithConfig(func(cfg *config.Config) {
		cfg.Server.ListenAddress = "127.0.0.1:0"
		cfg.Server.ShutdownTimeout.Duration = time.Second
	}))
	done := make(chan error, 1)
	go func() {
		done <- helper.Server.Start(context.Background())
	}()

	deadline := time.After(2 * time.Second)
	for !helper.Server.Health().Ready() {
		select {
		case err := <-done:
			t.Fatalf("server stopped before readiness: %v", err)
		case <-deadline:
			t.Fatal("server did not become ready")
		case <-time.After(time.Millisecond):
		}
	}
	if err := helper.Server.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Start() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close returned without stopping Start")
	}
}

func TestClosedServerCannotBeStarted(t *testing.T) {
	t.Parallel()

	helper := testlib.Setup(t)
	if err := helper.Server.Close(); err != nil {
		t.Fatal(err)
	}
	if !helper.Store.Closed() {
		t.Fatal("server close did not close the platform store")
	}
	if err := helper.Server.Start(context.Background()); err == nil {
		t.Fatal("closed server was started")
	}
}
