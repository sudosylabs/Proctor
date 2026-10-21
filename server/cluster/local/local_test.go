// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package local_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/cluster"
	"github.com/sudosylabs/proctor/server/cluster/local"
)

type testLogger struct {
	mu      sync.Mutex
	entries []string
}

func (l *testLogger) ErrorContext(_ context.Context, message string, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry := message
	if err != nil {
		entry += ": " + err.Error()
	}
	l.entries = append(l.entries, entry)
}

func (l *testLogger) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.entries, "\n")
}

func TestLocalTransportLifecycleAndLoopSafeDelivery(t *testing.T) {
	t.Parallel()

	logger := &testLogger{}
	transport, err := local.New("node-a", logger)
	require.NoError(t, err)
	message := &cluster.Message{
		Event: "test.event", Data: []byte("original"),
	}
	var handled atomic.Int32
	require.NoError(t, transport.RegisterHandler(message.Event, func(
		_ context.Context,
		received *cluster.Message,
	) error {
		handled.Add(1)
		received.Data[0] = 'X'
		return errors.New("handler failure")
	}))
	require.ErrorIs(t, transport.RegisterHandler(message.Event, func(context.Context, *cluster.Message) error {
		return nil
	}), cluster.ErrHandlerExists)
	require.ErrorIs(t, transport.Broadcast(context.Background(), message), cluster.ErrNotStarted)
	require.NoError(t, transport.Start(context.Background()))
	require.NoError(t, transport.Broadcast(context.Background(), message))
	require.Equal(t, int32(0), handled.Load(), "peer broadcast was delivered locally and can cause a rebroadcast loop")
	require.ErrorIs(t, transport.SendToNode(context.Background(), "missing", message), cluster.ErrNodeUnavailable)
	require.Error(t, transport.SendToNode(context.Background(), transport.NodeID(), message), "targeted delivery hid the handler failure")
	require.Equal(t, int32(1), handled.Load(), "local targeted delivery count")
	require.Equal(t, "original", string(message.Data), "handler mutated the caller's message")
	require.Contains(t, logger.String(), "cluster message handler failed")
	require.NotContains(t, logger.String(), "original")
	require.NoError(t, transport.Stop(context.Background()))
	require.NoError(t, transport.Stop(context.Background()))
	require.ErrorIs(t, transport.Ping(context.Background()), cluster.ErrStopped)
	require.ErrorIs(t, transport.SendToNode(context.Background(), transport.NodeID(), message), cluster.ErrStopped)
	require.ErrorIs(t, transport.Start(context.Background()), cluster.ErrStopped)
}

func TestLocalTransportRecoversHandlerPanicAndHonorsContext(t *testing.T) {
	t.Parallel()

	logger := &testLogger{}
	transport, err := local.New("node-a", logger)
	require.NoError(t, err)
	require.NoError(t, transport.RegisterHandler("panic", func(context.Context, *cluster.Message) error {
		panic("sensitive payload")
	}))
	require.NoError(t, transport.Start(context.Background()))
	message := &cluster.Message{Event: "panic"}
	err = transport.SendToNode(context.Background(), transport.NodeID(), message)
	require.Error(t, err, "targeted delivery hid the recovered handler panic")
	require.NotContains(t, err.Error(), "sensitive payload", "panic value leaked into returned error")
	require.Contains(t, logger.String(), "cluster message handler failed")
	require.NotContains(t, logger.String(), "sensitive payload")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, transport.Broadcast(ctx, message), context.Canceled)
}
