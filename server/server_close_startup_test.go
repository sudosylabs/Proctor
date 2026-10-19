// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"
)

func TestRefusedHandoffIsQuietOnceCloseMarksTheNodeStopping(t *testing.T) {
	t.Parallel()

	// A real listener closed underneath Accept reports "use of closed network
	// connection", which wraps net.ErrClosed.
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_ = listener.Close()
	_, closedErr := listener.Accept()
	closedErr = fmt.Errorf("serve HTTPS: %w", closedErr)
	if !errors.Is(closedErr, net.ErrClosed) {
		t.Fatalf("closed listener error = %v, want net.ErrClosed", closedErr)
	}

	live := context.Background()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, test := range []struct {
		name    string
		state   nodeState
		serving bool
		ctx     context.Context
		err     error
		want    bool
	}{
		// Close sets nodeStopping before it cancels the run context.
		{"close in progress before cancellation", nodeStopping, false, live, closedErr, true},
		{"canceled run", nodeStarting, false, canceled, closedErr, true},
		{"startup without shutdown request", nodeStarting, false, live, closedErr, false},
		{"after serving began", nodeStopping, true, live, closedErr, false},
		{"other serve failure", nodeStopping, false, live, errors.New("bind failed"), false},
	} {
		node := &Server{state: test.state}
		node.milestones.httpServing = test.serving
		if got := node.handoffRefusedForShutdown(test.ctx, test.err); got != test.want {
			t.Errorf("%s: handoffRefusedForShutdown() = %t, want %t", test.name, got, test.want)
		}
	}
}

func TestShutdownTreatsClosedListenerFromItsOwnStopAsClean(t *testing.T) {
	t.Parallel()

	events := &lifecycleEvents{}
	httpService := &lifecycleHTTP{
		events: events, started: make(chan struct{}), stopped: make(chan struct{}), stoppedErr: net.ErrClosed,
	}
	readiness := &lifecycleReadiness{events: events}
	node := newLifecycleTestServer(t, runtimeComponents{
		platform: &lifecyclePlatform{events: events},
		jobs:     &lifecycleJobs{events: events}, websocket: &lifecycleWebSocket{events: events},
		transport: &lifecycleTransport{events: events}, readiness: readiness,
		listen:  func(string, string) (net.Listener, error) { return &lifecycleListener{events: events}, nil },
		newHTTP: func(httpServerSettings) httpRuntime { return httpService },
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- node.Run(ctx) }()
	deadline := time.After(5 * time.Second)
	for !readiness.Ready() {
		select {
		case err := <-done:
			t.Fatalf("Run() stopped before readiness: %v", err)
		case <-deadline:
			t.Fatal("server did not become ready")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() = %v, want a clean shutdown", err)
	}
}
