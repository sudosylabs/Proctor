// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/sudosylabs/proctor/server/cluster"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store/localcachelayer"
)

type localCacheClusterAdapter struct {
	cluster cluster.Transport
}

type academicPeriodInvalidationMessage struct {
	ID string `json:"id"`
}

func newLocalCacheClusterAdapter(transport cluster.Transport) (*localCacheClusterAdapter, error) {
	if transport == nil {
		return nil, errors.New("cluster is nil")
	}
	return &localCacheClusterAdapter{cluster: transport}, nil
}

func (a *localCacheClusterAdapter) RegisterAcademicPeriod(
	handler func(context.Context, string) error,
) error {
	if handler == nil {
		return errors.New("academic-period invalidation handler is nil")
	}
	return a.cluster.RegisterHandler(
		cluster.EventAcademicPeriodInvalidated,
		func(ctx context.Context, message *cluster.Message) error {
			if message == nil {
				return errors.New("cluster message is nil")
			}
			if err := message.Validate(); err != nil {
				return err
			}
			var invalidation academicPeriodInvalidationMessage
			if err := json.Unmarshal(message.Data, &invalidation); err != nil {
				return fmt.Errorf("decode academic-period cache invalidation: %w", err)
			}
			if !model.IsValidId(invalidation.ID) {
				return errors.New("academic-period cache invalidation ID is invalid")
			}
			return handler(ctx, invalidation.ID)
		},
	)
}

func (a *localCacheClusterAdapter) BroadcastAcademicPeriod(ctx context.Context, id string) error {
	if !model.IsValidId(id) {
		return errors.New("academic-period cache invalidation ID is invalid")
	}
	data, err := json.Marshal(academicPeriodInvalidationMessage{ID: id})
	if err != nil {
		return fmt.Errorf("encode academic-period cache invalidation: %w", err)
	}
	return a.cluster.Broadcast(ctx, &cluster.Message{
		Event: cluster.EventAcademicPeriodInvalidated,
		Data:  data,
	})
}

var _ localcachelayer.InvalidationFanout = (*localCacheClusterAdapter)(nil)
