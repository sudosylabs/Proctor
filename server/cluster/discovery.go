// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package cluster

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

const (
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

// DiscoveryStore persists short-lived bootstrap advertisements. Concrete
// adapters live at the composition boundary; Memberlist never imports SQL.
type DiscoveryStore interface {
	Upsert(context.Context, DiscoveryNode) error
	ListLive(context.Context, time.Time) ([]DiscoveryNode, error)
	Delete(context.Context, string) error
	DeleteExpired(context.Context, time.Time) (int64, error)
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
