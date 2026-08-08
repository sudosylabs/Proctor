// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package cluster

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// DefaultDiscoveryTTL is the recommended lease window for bootstrap records
	// when operators do not override discovery settings.
	DefaultDiscoveryTTL = 30 * time.Second
	// DefaultDiscoveryHeartbeat is the recommended refresh interval and should
	// remain strictly less than DefaultDiscoveryTTL.
	DefaultDiscoveryHeartbeat = 10 * time.Second

	maxDiscoveryNodeIDBytes           = 128
	maxDiscoveryAdvertiseAddressBytes = 512
	maxDiscoveryServerVersionBytes    = 128
)

// DiscoveryNode is the transport-neutral bootstrap advertisement used before
// Memberlist membership is established. Persistence adapters map this record
// into PostgreSQL; it never carries application event payloads.
type DiscoveryNode struct {
	NodeID           string
	AdvertiseAddress string
	ServerVersion    string
	ProtocolMin      int
	ProtocolMax      int
	ExpiresAt        time.Time
	UpdatedAt        time.Time
}

// Validate checks local discovery invariants without I/O.
func (n DiscoveryNode) Validate() error {
	nodeID := strings.TrimSpace(n.NodeID)
	if nodeID == "" || len(nodeID) > maxDiscoveryNodeIDBytes || !validDiscoveryNodeID(nodeID) {
		return errors.New("discovery node_id is invalid")
	}
	address := strings.TrimSpace(n.AdvertiseAddress)
	if address == "" ||
		len(address) > maxDiscoveryAdvertiseAddressBytes ||
		!utf8.ValidString(address) {
		return errors.New("discovery advertise_address is invalid")
	}
	version := strings.TrimSpace(n.ServerVersion)
	if version == "" ||
		len(version) > maxDiscoveryServerVersionBytes ||
		!utf8.ValidString(version) {
		return errors.New("discovery server_version is invalid")
	}
	if n.ProtocolMin <= 0 || n.ProtocolMax < n.ProtocolMin {
		return errors.New("discovery protocol range is invalid")
	}
	if n.UpdatedAt.IsZero() || !n.ExpiresAt.After(n.UpdatedAt) {
		return errors.New("discovery lifetime is invalid")
	}
	return nil
}

// IsLive reports whether the advertisement is still valid at now.
func (n DiscoveryNode) IsLive(now time.Time) bool {
	return !now.IsZero() && n.ExpiresAt.After(now)
}

// DiscoveryLease computes UpdatedAt/ExpiresAt for a heartbeat at now.
func DiscoveryLease(now time.Time, ttl time.Duration) (updatedAt, expiresAt time.Time, err error) {
	if now.IsZero() {
		return time.Time{}, time.Time{}, errors.New("discovery now is required")
	}
	if ttl <= 0 {
		return time.Time{}, time.Time{}, fmt.Errorf("discovery ttl must be positive")
	}
	updatedAt = now.UTC()
	expiresAt = updatedAt.Add(ttl)
	return updatedAt, expiresAt, nil
}

func validDiscoveryNodeID(value string) bool {
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
