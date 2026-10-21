// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// ClusterMemberlist configures the built-in multi-node gossip transport.
type ClusterMemberlist struct {
	BindAddress        string   `json:"BindAddress"`
	AdvertiseAddress   string   `json:"AdvertiseAddress"`
	EncryptionKey      string   `json:"EncryptionKey"`
	DecryptionKeys     []string `json:"DecryptionKeys"`
	SeedAddresses      []string `json:"SeedAddresses"`
	DiscoveryTTL       Duration `json:"DiscoveryTTL"`
	DiscoveryHeartbeat Duration `json:"DiscoveryHeartbeat"`
	AllowPublicBind    bool     `json:"AllowPublicBind"`
}

// Cluster selects the inter-node transport and gives this process its stable
// runtime identity. "local" is the single-node degenerate transport.
// "memberlist" is the built-in multi-node backend and requires Redis so
// installation-wide disposable security counters remain coherent.
type Cluster struct {
	Backend    string            `json:"Backend"`
	NodeID     string            `json:"NodeID"`
	Memberlist ClusterMemberlist `json:"Memberlist"`
}

func validateCluster(cluster Cluster, add func(string, string)) {
	switch cluster.Backend {
	case "local":
	case "memberlist":
		if !validHostPort(cluster.Memberlist.BindAddress) {
			add("cluster.memberlist.bind_address", "must be a host:port TCP address")
		}
		if !validHostPort(cluster.Memberlist.AdvertiseAddress) {
			add("cluster.memberlist.advertise_address", "must be a host:port TCP address")
		}
		validateMemberlistKeyring(cluster.Memberlist, add)
		if cluster.Memberlist.DiscoveryTTL.Duration < 3*time.Second {
			add("cluster.memberlist.discovery_ttl", "must be at least 3s")
		}
		if cluster.Memberlist.DiscoveryHeartbeat.Duration <= 0 ||
			cluster.Memberlist.DiscoveryHeartbeat.Duration*2 >= cluster.Memberlist.DiscoveryTTL.Duration {
			add("cluster.memberlist.discovery_heartbeat", "must be greater than zero and less than half the discovery TTL")
		}
		if len(cluster.Memberlist.SeedAddresses) > 32 {
			add("cluster.memberlist.seed_addresses", "must contain at most 32 entries")
		}
		for index, address := range cluster.Memberlist.SeedAddresses {
			if !validHostPort(address) {
				add(
					fmt.Sprintf("cluster.memberlist.seed_addresses[%d]", index),
					"must be a host:port TCP address",
				)
			}
		}
		if !cluster.Memberlist.AllowPublicBind {
			if isPublicClusterBind(cluster.Memberlist.BindAddress) {
				add("cluster.memberlist.bind_address", "public-interface binding is rejected by default")
			}
		}
	default:
		add("cluster.backend", "must be local or memberlist")
	}
	if len(cluster.NodeID) == 0 || len(cluster.NodeID) > 128 {
		add("cluster.node_id", "must contain between 1 and 128 characters")
		return
	}
	for _, character := range cluster.NodeID {
		if (character < 'a' || character > 'z') &&
			(character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') &&
			character != '.' && character != '_' && character != '-' {
			add("cluster.node_id", "contains an invalid character")
			return
		}
	}
}

func validateMemberlistKeyring(settings ClusterMemberlist, add func(string, string)) {
	const maximumFallbackKeys = 8
	if len(settings.DecryptionKeys) > maximumFallbackKeys {
		add("cluster.memberlist.decryption_keys", "must contain at most 8 fallback keys")
	}
	if strings.TrimSpace(settings.EncryptionKey) == "" {
		add("cluster.memberlist.encryption_key", "is required")
	}
	keys := append([]string{settings.EncryptionKey}, settings.DecryptionKeys...)
	seen := make(map[string]struct{}, len(keys))
	for index, encoded := range keys {
		field := "cluster.memberlist.encryption_key"
		if index > 0 {
			field = fmt.Sprintf("cluster.memberlist.decryption_keys[%d]", index-1)
		}
		key, err := decodeMemberlistKey(encoded)
		if err != nil {
			add(field, err.Error())
			continue
		}
		if _, duplicate := seen[string(key)]; duplicate {
			add("cluster.memberlist.decryption_keys", "must not contain duplicate decoded keys")
			continue
		}
		seen[string(key)] = struct{}{}
	}
}

func decodeMemberlistKey(encoded string) ([]byte, error) {
	trimmed := strings.TrimSpace(encoded)
	switch len(trimmed) {
	case base64.StdEncoding.EncodedLen(16),
		base64.StdEncoding.EncodedLen(24),
		base64.StdEncoding.EncodedLen(32):
	default:
		return nil, errors.New("must decode to 16, 24, or 32 bytes")
	}
	key, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil {
		return nil, errors.New("must be base64-encoded")
	}
	if len(key) != 16 && len(key) != 24 && len(key) != 32 {
		return nil, errors.New("must decode to 16, 24, or 32 bytes")
	}
	return key, nil
}

func isPublicClusterBind(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	if host == "0.0.0.0" || host == "::" || host == "[::]" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast())
}
