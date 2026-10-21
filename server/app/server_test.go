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
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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
	require.NotNil(t, helper.Server, "test helper did not expose the behavioral runtime projection")
	require.NotNil(t, helper.App, "test helper did not expose the behavioral runtime projection")
	require.NotNil(t, helper.Handler(), "test helper did not expose the behavioral runtime projection")
	require.NotNil(t, helper.Persistence, "test helper did not expose the behavioral runtime projection")
	require.NotNil(t, helper.Cache, "test helper did not expose the behavioral runtime projection")
	require.NotNil(t, helper.Cluster, "test helper did not expose the behavioral runtime projection")
	require.NotNil(t, helper.Mailer, "test helper did not expose the behavioral runtime projection")
	require.NotNil(t, helper.VFS, "test helper did not expose the behavioral runtime projection")
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
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		require.FailNow(t, "server did not stop after context cancellation")
	}
}

func TestClusterStartFailurePreventsReadinessAndClosesPlatform(t *testing.T) {
	t.Parallel()

	cluster := &failingStartCluster{}
	helper := testlib.Setup(t, testlib.WithCluster(cluster))
	err := helper.Server.Run(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "start cluster transport")
	require.False(t, helper.Server.Ready(), "server became ready after cluster transport start failed")
	require.True(t, cluster.stopped.Load(), "cluster transport was not stopped after startup failure")
}

func TestServerCannotRunTwice(t *testing.T) {
	t.Parallel()

	helper := testlib.Setup(t, testlib.WithConfig(func(cfg *config.Config) {
		cfg.Server.ListenAddress = "127.0.0.1:0"
		cfg.Server.ShutdownTimeout.Duration = time.Second
	}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.NoError(t, helper.Server.Run(ctx))
	require.Error(t, helper.Server.Run(context.Background()), "second Run() was accepted")
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
			require.FailNow(t, fmt.Sprintf("server stopped before readiness: %v", err))
		case <-deadline:
			require.FailNow(t, "server did not become ready")
		case <-time.After(time.Millisecond):
		}
	}
	require.NoError(t, helper.Server.Close())
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		require.FailNow(t, "Close returned without stopping Run")
	}
}

type pingToggleCluster struct {
	failingStartCluster
	unavailable atomic.Bool
}

func (c *pingToggleCluster) Start(context.Context) error { return nil }

func (c *pingToggleCluster) Ping(context.Context) error {
	if c.unavailable.Load() {
		return errors.New("cluster unavailable")
	}
	return nil
}

func TestReadinessProbesMandatoryDependenciesWhileServing(t *testing.T) {
	t.Parallel()

	dependency := &pingToggleCluster{}
	helper := testlib.Setup(t, testlib.WithCluster(dependency), testlib.WithConfig(func(cfg *config.Config) {
		cfg.Server.ListenAddress = "127.0.0.1:0"
		cfg.Server.ShutdownTimeout.Duration = time.Second
	}))
	done := make(chan error, 1)
	go func() { done <- helper.Server.Run(context.Background()) }()
	t.Cleanup(func() {
		_ = helper.Server.Close()
		<-done
	})
	readyStatus := func() int {
		recorder := httptest.NewRecorder()
		helper.Handler().ServeHTTP(recorder, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/health/ready", nil))
		return recorder.Code
	}
	waitFor := func(want int) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for readyStatus() != want {
			require.False(t, time.Now().After(deadline), "/health/ready status")
			time.Sleep(10 * time.Millisecond)
		}
	}

	waitFor(http.StatusOK)
	dependency.unavailable.Store(true)
	waitFor(http.StatusServiceUnavailable)
	require.False(t, helper.Server.Ready(), "server reported ready during a mandatory dependency outage")
	dependency.unavailable.Store(false)
	waitFor(http.StatusOK)
}

func TestClosedServerCannotBeRun(t *testing.T) {
	t.Parallel()

	helper := testlib.Setup(t)
	require.NoError(t, helper.Server.Close())
	require.True(t, helper.PersistenceClose.Closed(), "server close did not close the platform store")
	require.Error(t, helper.Server.Run(context.Background()), "closed server was started")
}

var _ platform.Cluster = (*failingStartCluster)(nil)
