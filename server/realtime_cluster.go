// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"fmt"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/cluster"
)

// realtimeClusterAdapter maps the application RealtimeClusterFanout port onto
// the best-effort cluster.Transport contract.
type realtimeClusterAdapter struct {
	cluster cluster.Transport
}

func newRealtimeClusterAdapter(transport cluster.Transport) (*realtimeClusterAdapter, error) {
	if transport == nil {
		return nil, fmt.Errorf("cluster is nil")
	}
	return &realtimeClusterAdapter{cluster: transport}, nil
}

func (a *realtimeClusterAdapter) RegisterHandler(
	event string,
	handler func(context.Context, []byte) error,
) error {
	if handler == nil {
		return fmt.Errorf("realtime cluster handler for %q is nil", event)
	}
	return a.cluster.RegisterHandler(
		cluster.Event(event),
		func(ctx context.Context, message *cluster.Message) error {
			if message == nil {
				return fmt.Errorf("cluster message is nil")
			}
			if err := message.Validate(); err != nil {
				return err
			}
			return handler(ctx, message.Data)
		},
	)
}

func (a *realtimeClusterAdapter) Broadcast(
	ctx context.Context,
	event string,
	data []byte,
) error {
	return a.cluster.Broadcast(ctx, &cluster.Message{
		Event: cluster.Event(event),
		Data:  data,
	})
}

var _ app.RealtimeClusterFanout = (*realtimeClusterAdapter)(nil)
