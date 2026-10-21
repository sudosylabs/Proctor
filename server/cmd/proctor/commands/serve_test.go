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
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestReloadSignalsRunOneBoundedReloadEachUntilCanceled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	signals := make(chan os.Signal)
	reloads := make(chan bool)
	done := make(chan struct{})
	go func() {
		defer close(done)
		forwardReloadSignals(ctx, signals, func(reloadCtx context.Context) error {
			_, bounded := reloadCtx.Deadline()
			reloads <- bounded
			return errors.New("invalid configuration")
		})
	}()
	for range 2 {
		signals <- syscall.SIGHUP
		require.True(t, (<-reloads), "reload context has no deadline")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		require.FailNow(t, "reload forwarding did not stop after cancellation")
	}
}

func TestServeReceivesContextAndConfigPath(t *testing.T) {
	t.Parallel()

	type contextKey string
	ctx := context.WithValue(context.Background(), contextKey("request"), "present")
	var gotPath string
	execute := testExecutors()
	execute.serve = func(gotContext context.Context, path string) error {
		require.Equal(t, "present", gotContext.Value(contextKey("request")), "serve context did not originate at the CLI root")
		gotPath = path
		return nil
	}
	code, stdout, stderr := executeForTest(ctx, []string{"serve", "--config", "/etc/proctor.json"}, nil, execute)
	require.Equal(t, 0, code, "code")
	require.Empty(t, stdout, "code")
	require.Empty(t, stderr, "code")
	require.Equal(t, "/etc/proctor.json", gotPath, "code")
}

func TestServeHoldsSIGHUPReceivedDuringStartupUntilTheNodeExists(t *testing.T) {
	// SIGHUP is process-wide; this test must not share its delivery window.
	node := &reloadRecordingNode{reloaded: make(chan struct{}, 1)}
	err := serveNode(context.Background(), func(context.Context) (servingNode, error) {
		// A reload request arriving while the node is still constructing, for
		// example during migration, must not terminate the process.
		require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGHUP))
		time.Sleep(100 * time.Millisecond)
		return node, nil
	})
	require.NoError(t, err)
}

type reloadRecordingNode struct{ reloaded chan struct{} }

func (n *reloadRecordingNode) Reload(context.Context) error {
	n.reloaded <- struct{}{}
	return nil
}

func (n *reloadRecordingNode) Run(context.Context) error {
	select {
	case <-n.reloaded:
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("startup SIGHUP was not forwarded to the constructed node")
	}
}
