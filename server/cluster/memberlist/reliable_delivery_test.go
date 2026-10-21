// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package memberlist

import (
	"bytes"
	"context"
	"maps"
	"sync"
	"testing"
	"time"

	hashimemberlist "github.com/hashicorp/memberlist"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/cluster"
)

func TestLargeBroadcastDoesNotWaitForAStalledPeer(t *testing.T) {
	t.Parallel()

	sender := newStallingSender("node-stalled")
	t.Cleanup(sender.releaseAll)
	metrics := &recordingMessageMetrics{}
	transport := &Transport{
		cfg:      Config{NodeID: "node-local", Metrics: metrics},
		handlers: map[cluster.Event]cluster.Handler{},
		reliable: newReliableDelivery(sender.SendReliable),
	}
	members := []*hashimemberlist.Node{{Name: "node-local"}, {Name: "node-stalled"}, {Name: "node-healthy"}}
	payload := bytes.Repeat([]byte{'x'}, 4096)

	returned := make(chan error, 1)
	go func() {
		returned <- transport.fanOut(context.Background(), sender, members, payload, 1272, "realtime.event")
	}()
	select {
	case err := <-returned:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		require.FailNow(t, "a large broadcast waited for a stalled peer")
	}
	select {
	case name := <-sender.delivered:
		require.Equal(t, "node-healthy", name)
	case <-time.After(5 * time.Second):
		require.FailNow(t, "a stalled peer delayed delivery to a healthy peer")
	}

	<-sender.started
	sender.releaseAll()
	require.Eventually(t, func() bool { return metrics.outcomes()["success"] == 2 }, 5*time.Second, time.Millisecond)
	transport.reliable.close()
	require.NoError(t, transport.reliable.wait(context.Background()))
	require.Equal(t, map[string]int{"success": 2}, metrics.outcomes())
}

func TestReliableDeliveryDropsOldestEnvelopeWhenAPeerQueueIsFull(t *testing.T) {
	t.Parallel()

	sender := newStallingSender("node-stalled")
	t.Cleanup(sender.releaseAll)
	delivery := newReliableDelivery(sender.SendReliable)
	delivery.maximumEnvelopes = 2
	node := &hashimemberlist.Node{Name: "node-stalled"}
	outcomes := make(chan string, 8)
	enqueue := func(name string) {
		delivery.enqueue(node, []byte(name), func(err error) {
			if err != nil {
				name += ": " + err.Error()
			}
			outcomes <- name
		})
	}

	enqueue("in flight")
	<-sender.started
	enqueue("oldest")
	enqueue("older")
	enqueue("newest")
	require.Equal(t, "oldest: "+errReliableQueueFull.Error(), <-outcomes)

	delivery.close()
	require.ElementsMatch(t, []string{
		"older: " + cluster.ErrStopped.Error(),
		"newest: " + cluster.ErrStopped.Error(),
	}, []string{<-outcomes, <-outcomes})
	sender.releaseAll()
	require.Equal(t, "in flight", <-outcomes)
	require.NoError(t, delivery.wait(context.Background()))
	enqueue("after close")
	require.Equal(t, "after close: "+cluster.ErrStopped.Error(), <-outcomes)
}

func TestReliableDeliveryRetiresAnIdlePeerAndRestartsIt(t *testing.T) {
	t.Parallel()

	sender := newStallingSender()
	delivery := newReliableDelivery(sender.SendReliable)
	delivery.idleTimeout = time.Millisecond
	node := &hashimemberlist.Node{Name: "node-a"}
	done := make(chan error, 2)

	delivery.enqueue(node, []byte("first"), func(err error) { done <- err })
	require.NoError(t, <-done)
	require.Eventually(t, func() bool {
		delivery.mu.Lock()
		defer delivery.mu.Unlock()
		return len(delivery.peers) == 0
	}, 5*time.Second, time.Millisecond)
	delivery.enqueue(node, []byte("second"), func(err error) { done <- err })
	require.NoError(t, <-done)
	delivery.close()
	require.NoError(t, delivery.wait(context.Background()))
}

// stallingSender blocks reliable sends to the named peers until released and
// reports every completed reliable send by peer name.
type stallingSender struct {
	stalled   map[string]bool
	started   chan struct{}
	release   chan struct{}
	once      sync.Once
	delivered chan string
}

func newStallingSender(stalled ...string) *stallingSender {
	sender := &stallingSender{
		stalled:   map[string]bool{},
		started:   make(chan struct{}, 8),
		release:   make(chan struct{}),
		delivered: make(chan string, 8),
	}
	for _, name := range stalled {
		sender.stalled[name] = true
	}
	return sender
}

func (s *stallingSender) SendBestEffort(*hashimemberlist.Node, []byte) error { return nil }

func (s *stallingSender) SendReliable(node *hashimemberlist.Node, _ []byte) error {
	if s.stalled[node.Name] {
		s.started <- struct{}{}
		<-s.release
		return nil
	}
	s.delivered <- node.Name
	return nil
}

func (s *stallingSender) releaseAll() { s.once.Do(func() { close(s.release) }) }

type recordingMessageMetrics struct {
	mu     sync.Mutex
	counts map[string]int
}

func (m *recordingMessageMetrics) ObserveClusterMessage(_, _, outcome string, _ int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.counts == nil {
		m.counts = map[string]int{}
	}
	m.counts[outcome]++
}

func (m *recordingMessageMetrics) outcomes() map[string]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return maps.Clone(m.counts)
}

func (*recordingMessageMetrics) ObserveClusterMembership(string)        {}
func (*recordingMessageMetrics) ObserveClusterDiscovery(string, string) {}
func (*recordingMessageMetrics) ObserveClusterAdmission(string)         {}
func (*recordingMessageMetrics) ObserveClusterFanout(int)               {}

var _ memberSender = (*stallingSender)(nil)
var _ cluster.Metrics = (*recordingMessageMetrics)(nil)
