// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/cluster"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/platform"
	"github.com/sudosylabs/proctor/server/testlib"
)

type failingStartCluster struct {
	stopped atomic.Bool
}

func (c *failingStartCluster) NodeID() string {
	return "failing-node"
}

func (c *failingStartCluster) Start(context.Context) error {
	return errors.New("start failure")
}

func (c *failingStartCluster) Stop(context.Context) error {
	c.stopped.Store(true)
	return nil
}

func (c *failingStartCluster) Ping(context.Context) error {
	return nil
}

func (c *failingStartCluster) RegisterHandler(cluster.Event, cluster.Handler) error {
	return nil
}

func (c *failingStartCluster) Broadcast(context.Context, *cluster.Message) error {
	return nil
}

func (c *failingStartCluster) SendToNode(context.Context, string, *cluster.Message) error {
	return nil
}

func TestTestlibConstructsOneSharedApplicationGraph(t *testing.T) {
	t.Parallel()

	helper := testlib.Setup(t)
	if helper.Server == nil || helper.App == nil || helper.Handler() == nil ||
		helper.Persistence == nil || helper.Cache == nil || helper.Cluster == nil ||
		helper.Mailer == nil || helper.VFS == nil {
		t.Fatal("test helper did not expose the behavioral runtime projection")
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
		done <- helper.Server.Run(ctx)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not stop after context cancellation")
	}
}

func TestClusterStartFailurePreventsReadinessAndClosesPlatform(t *testing.T) {
	t.Parallel()

	cluster := &failingStartCluster{}
	helper := testlib.Setup(t, testlib.WithCluster(cluster))
	err := helper.Server.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "start cluster transport") {
		t.Fatalf("Run() error = %v", err)
	}
	if helper.Server.Ready() {
		t.Fatal("server became ready after cluster transport start failed")
	}
	if !cluster.stopped.Load() {
		t.Fatal("cluster transport was not stopped after startup failure")
	}
}

func TestServerCannotRunTwice(t *testing.T) {
	t.Parallel()

	helper := testlib.Setup(t, testlib.WithConfig(func(cfg *config.Config) {
		cfg.Server.ListenAddress = "127.0.0.1:0"
		cfg.Server.ShutdownTimeout.Duration = time.Second
	}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := helper.Server.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if err := helper.Server.Run(context.Background()); err == nil {
		t.Fatal("second Run() was accepted")
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
		done <- helper.Server.Run(context.Background())
	}()

	deadline := time.After(2 * time.Second)
	for !helper.Server.Ready() {
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
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close returned without stopping Run")
	}
}

func TestClosedServerCannotBeRun(t *testing.T) {
	t.Parallel()

	helper := testlib.Setup(t)
	if err := helper.Server.Close(); err != nil {
		t.Fatal(err)
	}
	if !helper.PersistenceClose.Closed() {
		t.Fatal("server close did not close the platform store")
	}
	if err := helper.Server.Run(context.Background()); err == nil {
		t.Fatal("closed server was started")
	}
}

var _ platform.Cluster = (*failingStartCluster)(nil)
