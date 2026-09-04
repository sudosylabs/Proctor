// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package server

import (
	"context"
	"fmt"

	apprealtime "github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/cluster"
)

// realtimeClusterAdapter maps the Realtime child module's ClusterFanout port onto
// the best-effort cluster.Transport contract.
type realtimeClusterAdapter struct {
	cluster borrowedCluster
}

func newRealtimeClusterAdapter(transport borrowedCluster) (*realtimeClusterAdapter, error) {
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

var _ apprealtime.ClusterFanout = (*realtimeClusterAdapter)(nil)
