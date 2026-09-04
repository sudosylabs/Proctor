// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package memberlist

import (
	"context"
	"encoding/json"
	"log"
	"strings"

	hashimemberlist "github.com/hashicorp/memberlist"

	"github.com/sudosylabs/proctor/server/cluster"
)

type delegate struct {
	transport *Transport
}

func (d *delegate) NodeMeta(limit int) []byte {
	meta, err := json.Marshal(localNodeMeta(d.transport.cfg))
	if err != nil {
		return nil
	}
	if len(meta) > limit {
		return nil
	}
	return meta
}

func (d *delegate) NotifyMsg(message []byte) {
	if len(message) == 0 || len(message) > maxWireBytes {
		d.observeMessage("unknown", "invalid_size", len(message))
		return
	}
	var envelope wireEnvelope
	if err := json.Unmarshal(message, &envelope); err != nil {
		d.observeMessage("unknown", "invalid_envelope", len(message))
		return
	}
	if envelope.Version != wireProtocolVersion ||
		envelope.Message == nil ||
		envelope.Source == "" ||
		envelope.Source == d.transport.cfg.NodeID {
		d.observeMessage("unknown", "rejected", len(message))
		return
	}
	if envelope.Target != "" && envelope.Target != d.transport.cfg.NodeID {
		d.observeMessage("unknown", "wrong_target", len(message))
		return
	}
	if err := envelope.Message.Validate(); err != nil {
		d.observeMessage("unknown", "invalid_message", len(message))
		return
	}
	// Best-effort dispatch: handler failures are logged and not retried by the
	// transport.
	err := d.transport.dispatchLocal(context.Background(), envelope.Message.Clone())
	result := "success"
	if err != nil {
		result = "error"
	}
	d.observeMessage(d.transport.metricEvent(envelope.Message.Event), result, len(message))
}

func (d *delegate) observeMessage(event, outcome string, bytes int) {
	if d.transport.cfg.Metrics != nil {
		d.transport.cfg.Metrics.ObserveClusterMessage("receive", event, outcome, bytes)
	}
}

func (d *delegate) GetBroadcasts(overhead, limit int) [][]byte { return nil }
func (d *delegate) LocalState(join bool) []byte                { return nil }
func (d *delegate) MergeRemoteState(buf []byte, join bool)     {}

type eventDelegate struct {
	transport *Transport
}

func (e *eventDelegate) NotifyJoin(node *hashimemberlist.Node) {
	if node == nil || node.Name == e.transport.cfg.NodeID {
		return
	}
	if e.transport.cfg.Metrics != nil {
		e.transport.cfg.Metrics.ObserveClusterMembership("join")
	}
	meta, err := decodeNodeMeta(node.Meta)
	if err != nil {
		return
	}
	if meta.NodeID == e.transport.cfg.NodeID {
		e.transport.cfg.Logger.ErrorContext(
			context.Background(),
			"duplicate cluster node identity observed",
			cluster.ErrNodeIDInUse,
		)
	}
}

func (e *eventDelegate) NotifyLeave(node *hashimemberlist.Node) {
	if node != nil && node.Name != e.transport.cfg.NodeID && e.transport.cfg.Metrics != nil {
		e.transport.cfg.Metrics.ObserveClusterMembership("leave")
	}
}
func (e *eventDelegate) NotifyUpdate(node *hashimemberlist.Node) {
	if node != nil && node.Name != e.transport.cfg.NodeID && e.transport.cfg.Metrics != nil {
		e.transport.cfg.Metrics.ObserveClusterMembership("update")
	}
}

type memberlistLogWriter struct {
	logger cluster.Logger
}

func (w memberlistLogWriter) Write(payload []byte) (int, error) {
	message := strings.TrimSpace(string(payload))
	if message != "" && w.logger != nil {
		w.logger.ErrorContext(context.Background(), "memberlist: "+message, nil)
	}
	return len(payload), nil
}

func newMemberlistLogger(logger cluster.Logger) *log.Logger {
	return log.New(memberlistLogWriter{logger: logger}, "", 0)
}
