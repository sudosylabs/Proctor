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
		if bounded := <-reloads; !bounded {
			t.Fatal("reload context has no deadline")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reload forwarding did not stop after cancellation")
	}
}

func TestServeReceivesContextAndConfigPath(t *testing.T) {
	t.Parallel()

	type contextKey string
	ctx := context.WithValue(context.Background(), contextKey("request"), "present")
	var gotPath string
	execute := testExecutors()
	execute.serve = func(gotContext context.Context, path string) error {
		if gotContext.Value(contextKey("request")) != "present" {
			t.Fatal("serve context did not originate at the CLI root")
		}
		gotPath = path
		return nil
	}
	code, stdout, stderr := executeForTest(ctx, []string{"serve", "--config", "/etc/proctor.json"}, nil, execute)
	if code != 0 || stdout != "" || stderr != "" || gotPath != "/etc/proctor.json" {
		t.Fatalf("code=%d stdout=%q stderr=%q path=%q", code, stdout, stderr, gotPath)
	}
}
