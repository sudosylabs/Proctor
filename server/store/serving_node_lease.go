// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

const (
	ServingNodeLeaseMinimumLifetime = 10 * time.Millisecond
	ServingNodeLeaseMaximumLifetime = 5 * time.Minute
)

// ServingNodeLease is the PostgreSQL-clocked proof that one application node
// may still serve this installation. It is disposable runtime state, not a
// membership, discovery, or application-event record.
type ServingNodeLease struct {
	NodeID    string
	LeaseID   string
	UpdatedAt time.Time
	ExpiresAt time.Time
}

func (l *ServingNodeLease) Validate() error {
	if l == nil || strings.TrimSpace(l.NodeID) == "" || len(l.NodeID) > ClusterDiscoveryNodeIDMaxBytes ||
		!model.IsValidId(l.LeaseID) || l.UpdatedAt.IsZero() || !l.ExpiresAt.After(l.UpdatedAt) {
		return errors.New("serving node lease is invalid")
	}
	return nil
}

// ServingNodeLeaseClaim is one process incarnation's exact renewable claim.
// LeaseID prevents an old process from renewing or deleting a successor that
// intentionally reuses the stable configured NodeID after expiry.
type ServingNodeLeaseClaim struct {
	NodeID   string
	LeaseID  string
	Lifetime time.Duration
}

// ServingNodeLeaseStore owns the all-backend serving fence. Upsert and Delete
// serialize with offline administrator recovery.
type ServingNodeLeaseStore interface {
	Upsert(context.Context, *ServingNodeLeaseClaim) (*ServingNodeLease, error)
	Delete(context.Context, string, string) error
}
