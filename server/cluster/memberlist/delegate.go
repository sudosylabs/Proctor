// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

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
	meta, err := json.Marshal(nodeMeta{
		NodeID:        d.transport.cfg.NodeID,
		ServerVersion: d.transport.cfg.ServerVersion,
		ProtocolMin:   d.transport.cfg.ProtocolMin,
		ProtocolMax:   d.transport.cfg.ProtocolMax,
	})
	if err != nil {
		return nil
	}
	if len(meta) > limit {
		return meta[:limit]
	}
	return meta
}

func (d *delegate) NotifyMsg(message []byte) {
	if len(message) == 0 || len(message) > maxWireBytes {
		return
	}
	var envelope wireEnvelope
	if err := json.Unmarshal(message, &envelope); err != nil {
		return
	}
	if envelope.Version != wireProtocolVersion ||
		envelope.Message == nil ||
		envelope.Source == "" ||
		envelope.Source == d.transport.cfg.NodeID {
		return
	}
	if envelope.Target != "" && envelope.Target != d.transport.cfg.NodeID {
		return
	}
	if err := envelope.Message.Validate(); err != nil {
		return
	}
	// Best-effort dispatch: handler failures are logged and not retried by the
	// transport.
	_ = d.transport.dispatchLocal(context.Background(), envelope.Message.Clone())
}

func (d *delegate) GetBroadcasts(overhead, limit int) [][]byte { return nil }
func (d *delegate) LocalState(join bool) []byte                 { return nil }
func (d *delegate) MergeRemoteState(buf []byte, join bool)      {}

type eventDelegate struct {
	transport *Transport
}

func (e *eventDelegate) NotifyJoin(node *hashimemberlist.Node) {
	if node == nil || node.Name == e.transport.cfg.NodeID {
		return
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

func (e *eventDelegate) NotifyLeave(*hashimemberlist.Node) {}
func (e *eventDelegate) NotifyUpdate(*hashimemberlist.Node) {}

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
