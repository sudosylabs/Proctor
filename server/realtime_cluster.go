// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"fmt"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/platform"
)

// realtimeClusterAdapter maps the application RealtimeClusterFanout port onto
// the current platform cluster wire contract. Application code never sees
// ClusterMessage, ClusterEvent, or ClusterSendType.
type realtimeClusterAdapter struct {
	cluster platform.Cluster
}

func newRealtimeClusterAdapter(cluster platform.Cluster) (*realtimeClusterAdapter, error) {
	if cluster == nil {
		return nil, fmt.Errorf("cluster is nil")
	}
	return &realtimeClusterAdapter{cluster: cluster}, nil
}

func (a *realtimeClusterAdapter) RegisterHandler(
	event string,
	handler func(context.Context, []byte) error,
) error {
	if handler == nil {
		return fmt.Errorf("realtime cluster handler for %q is nil", event)
	}
	return a.cluster.RegisterMessageHandler(
		model.ClusterEvent(event),
		func(ctx context.Context, message *model.ClusterMessage) error {
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
	reliable bool,
) error {
	sendType := model.ClusterSendBestEffort
	if reliable {
		sendType = model.ClusterSendReliable
	}
	return a.cluster.Broadcast(ctx, &model.ClusterMessage{
		Event:    model.ClusterEvent(event),
		SendType: sendType,
		Data:     data,
	})
}

var _ app.RealtimeClusterFanout = (*realtimeClusterAdapter)(nil)
