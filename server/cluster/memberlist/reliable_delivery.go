// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package memberlist

import (
	"context"
	"errors"
	"sync"
	"time"

	hashimemberlist "github.com/hashicorp/memberlist"

	"github.com/sudosylabs/proctor/server/cluster"
)

const (
	// reliablePeerQueueEnvelopes and reliablePeerQueueBytes bound the
	// envelopes waiting for one peer's reliable stream. One wire envelope is
	// at most maxWireBytes, so the byte bound always admits the newest one.
	reliablePeerQueueEnvelopes = 64
	reliablePeerQueueBytes     = 8 * maxWireBytes
	// reliablePeerIdleTimeout ends a peer's delivery goroutine once its queue
	// has stayed empty this long; the next envelope starts a new one.
	reliablePeerIdleTimeout = time.Minute
)

// errReliableQueueFull reports an envelope dropped, oldest first, because its
// peer's bounded reliable queue could not admit a newer envelope.
var errReliableQueueFull = errors.New("cluster peer reliable queue is full")

// reliableEnvelope is one encoded envelope waiting for a peer's reliable
// stream. done receives the send outcome, errReliableQueueFull when the
// envelope is dropped, or cluster.ErrStopped when delivery stops first.
type reliableEnvelope struct {
	node    *hashimemberlist.Node
	payload []byte
	done    func(error)
}

// reliableDelivery sends envelopes too large for one UDP packet over each
// peer's reliable stream on a goroutine owned per peer. Memberlist dials a
// fresh TCP connection for every reliable send and blocks for up to its TCP
// timeout without a context, so publishers only enqueue: a slow or
// partitioned peer delays its own queue, never the caller or another peer.
type reliableDelivery struct {
	send             func(*hashimemberlist.Node, []byte) error
	maximumEnvelopes int
	maximumBytes     int
	idleTimeout      time.Duration

	mu      sync.Mutex
	peers   map[string]*reliablePeer
	stopped bool
	workers sync.WaitGroup
}

type reliablePeer struct {
	queue []reliableEnvelope
	bytes int
	wake  chan struct{}
}

func newReliableDelivery(send func(*hashimemberlist.Node, []byte) error) *reliableDelivery {
	return &reliableDelivery{
		send:             send,
		maximumEnvelopes: reliablePeerQueueEnvelopes,
		maximumBytes:     reliablePeerQueueBytes,
		idleTimeout:      reliablePeerIdleTimeout,
		peers:            make(map[string]*reliablePeer),
	}
}

// enqueue admits one envelope for node without waiting for the network. When
// the peer's queue is full, the oldest waiting envelopes are dropped until the
// new one fits; each dropped envelope's done receives errReliableQueueFull.
func (d *reliableDelivery) enqueue(node *hashimemberlist.Node, payload []byte, done func(error)) {
	d.mu.Lock()
	if d.stopped {
		d.mu.Unlock()
		done(cluster.ErrStopped)
		return
	}
	peer, exists := d.peers[node.Name]
	if !exists {
		peer = &reliablePeer{wake: make(chan struct{}, 1)}
		d.peers[node.Name] = peer
		d.workers.Add(1)
		go d.deliver(node.Name, peer)
	}
	var dropped []reliableEnvelope
	for len(peer.queue) > 0 && (len(peer.queue) >= d.maximumEnvelopes || peer.bytes+len(payload) > d.maximumBytes) {
		dropped = append(dropped, peer.queue[0])
		peer.bytes -= len(peer.queue[0].payload)
		peer.queue[0] = reliableEnvelope{}
		peer.queue = peer.queue[1:]
	}
	peer.queue = append(peer.queue, reliableEnvelope{node: node, payload: payload, done: done})
	peer.bytes += len(payload)
	d.mu.Unlock()
	select {
	case peer.wake <- struct{}{}:
	default:
	}
	for _, envelope := range dropped {
		envelope.done(errReliableQueueFull)
	}
}

func (d *reliableDelivery) deliver(name string, peer *reliablePeer) {
	defer d.workers.Done()
	idle := time.NewTimer(d.idleTimeout)
	defer idle.Stop()
	for {
		d.mu.Lock()
		if len(peer.queue) == 0 {
			if d.stopped {
				d.mu.Unlock()
				return
			}
			d.mu.Unlock()
			if !idle.Stop() {
				select {
				case <-idle.C:
				default:
				}
			}
			idle.Reset(d.idleTimeout)
			select {
			case <-peer.wake:
				continue
			case <-idle.C:
			}
			d.mu.Lock()
			if len(peer.queue) == 0 {
				if d.peers[name] == peer {
					delete(d.peers, name)
				}
				d.mu.Unlock()
				return
			}
			d.mu.Unlock()
			continue
		}
		envelope := peer.queue[0]
		peer.queue[0] = reliableEnvelope{}
		peer.queue = peer.queue[1:]
		peer.bytes -= len(envelope.payload)
		d.mu.Unlock()
		envelope.done(d.send(envelope.node, envelope.payload))
	}
}

// close refuses new envelopes and reports every waiting envelope as stopped.
// A send already inside Memberlist cannot be interrupted; Memberlist shutdown
// makes it fail, and wait observes the goroutines' exit.
func (d *reliableDelivery) close() {
	d.mu.Lock()
	if d.stopped {
		d.mu.Unlock()
		return
	}
	d.stopped = true
	var abandoned []reliableEnvelope
	for _, peer := range d.peers {
		abandoned = append(abandoned, peer.queue...)
		peer.queue, peer.bytes = nil, 0
		select {
		case peer.wake <- struct{}{}:
		default:
		}
	}
	d.mu.Unlock()
	for _, envelope := range abandoned {
		envelope.done(cluster.ErrStopped)
	}
}

// wait blocks until every peer goroutine has exited after close, or ctx ends.
func (d *reliableDelivery) wait(ctx context.Context) error {
	finished := make(chan struct{})
	go func() {
		d.workers.Wait()
		close(finished)
	}()
	select {
	case <-finished:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
