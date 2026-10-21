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
