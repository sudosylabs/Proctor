// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// ClusterDiscoveryNodeIDMaxBytes bounds operator-configured node identities.
	ClusterDiscoveryNodeIDMaxBytes = 128
	// ClusterDiscoveryAdvertiseAddressMaxBytes bounds join addresses.
	ClusterDiscoveryAdvertiseAddressMaxBytes = 512
	// ClusterDiscoveryServerVersionMaxBytes bounds version advertisement strings.
	ClusterDiscoveryServerVersionMaxBytes = 128
)

// ClusterDiscoveryNode is a short-lived, disposable bootstrap record used to
// find peer join addresses. It is never a message queue and must not store
// application event payloads.
type ClusterDiscoveryNode struct {
	NodeID           string
	AdvertiseAddress string
	ServerVersion    string
	ProtocolMin      int
	ProtocolMax      int
	// ExpiresAt is the exclusive liveness deadline.
	ExpiresAt time.Time
	// UpdatedAt is the last successful upsert/heartbeat time.
	UpdatedAt time.Time
}

// Validate checks local discovery-record invariants without I/O.
func (n *ClusterDiscoveryNode) Validate() error {
	if n == nil {
		return errors.New("cluster discovery node is nil")
	}
	nodeID := strings.TrimSpace(n.NodeID)
	if nodeID == "" || len(nodeID) > ClusterDiscoveryNodeIDMaxBytes || !validClusterDiscoveryName(nodeID) {
		return fmt.Errorf("cluster discovery node_id is invalid")
	}
	address := strings.TrimSpace(n.AdvertiseAddress)
	if address == "" ||
		len(address) > ClusterDiscoveryAdvertiseAddressMaxBytes ||
		!utf8.ValidString(address) {
		return fmt.Errorf("cluster discovery advertise_address is invalid")
	}
	version := strings.TrimSpace(n.ServerVersion)
	if version == "" ||
		len(version) > ClusterDiscoveryServerVersionMaxBytes ||
		!utf8.ValidString(version) {
		return fmt.Errorf("cluster discovery server_version is invalid")
	}
	if n.ProtocolMin <= 0 || n.ProtocolMax < n.ProtocolMin {
		return fmt.Errorf("cluster discovery protocol range is invalid")
	}
	if n.UpdatedAt.IsZero() || !n.ExpiresAt.After(n.UpdatedAt) {
		return fmt.Errorf("cluster discovery lifetime is invalid")
	}
	return nil
}

// IsLive reports whether the record is still within its exclusive expiry window.
func (n *ClusterDiscoveryNode) IsLive(now time.Time) bool {
	return n != nil && !now.IsZero() && n.ExpiresAt.After(now)
}

// ClusterDiscoveryStore persists short-lived multi-node bootstrap records.
// Concrete adapters enforce expiry and uniqueness; callers treat rows as
// reconstructible and disposable.
type ClusterDiscoveryStore interface {
	// Upsert inserts or replaces the node record. The supplied ExpiresAt is
	// authoritative for the new lease window.
	Upsert(context.Context, *ClusterDiscoveryNode) error
	// ListLive returns non-expired peers ordered by node ID. liveAt is the
	// exclusive liveness threshold (ExpiresAt after liveAt).
	ListLive(ctx context.Context, liveAt time.Time) ([]*ClusterDiscoveryNode, error)
	// Delete removes one node record, typically on graceful shutdown.
	Delete(context.Context, string) error
	// DeleteExpired removes stale records and returns the number of rows deleted.
	// expiredAt is the inclusive expiry threshold (ExpiresAt at or before it).
	DeleteExpired(ctx context.Context, expiredAt time.Time) (int64, error)
}

func validClusterDiscoveryName(value string) bool {
	for _, character := range value {
		if (character < 'a' || character > 'z') &&
			(character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') &&
			character != '.' && character != '_' && character != '-' {
			return false
		}
	}
	return true
}
