// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package memberlist

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/cluster"
)

type discoveryTicker interface {
	C() <-chan time.Time
	Stop()
}

type systemDiscoveryTicker struct {
	ticker *time.Ticker
}

func newDiscoveryTicker(interval time.Duration) discoveryTicker {
	return &systemDiscoveryTicker{ticker: time.NewTicker(interval)}
}

func (t *systemDiscoveryTicker) C() <-chan time.Time {
	return t.ticker.C
}

func (t *systemDiscoveryTicker) Stop() {
	t.ticker.Stop()
}

type discoveryMaintenanceConfig struct {
	nodeID             string
	advertiseAddress   string
	serverVersion      string
	seedAddresses      []string
	discovery          cluster.DiscoveryStore
	discoveryTTL       time.Duration
	discoveryHeartbeat time.Duration
	protocolMin        int
	protocolMax        int
	diagnostics        cluster.Logger
	now                func() time.Time
	newTicker          func(time.Duration) discoveryTicker
}

// discoveryMaintenance owns the disposable discovery lease and bootstrap seed
// preparation. Transport remains responsible for Memberlist and for invoking
// this module within its serialized lifecycle.
type discoveryMaintenance struct {
	cfg discoveryMaintenanceConfig
}

const discoveryRollbackTimeout = time.Second

func newDiscoveryMaintenance(cfg discoveryMaintenanceConfig) *discoveryMaintenance {
	cfg.seedAddresses = append([]string(nil), cfg.seedAddresses...)
	return &discoveryMaintenance{cfg: cfg}
}

func newSystemDiscoveryMaintenance(cfg Config) *discoveryMaintenance {
	return newDiscoveryMaintenance(discoveryMaintenanceConfig{
		nodeID:             cfg.NodeID,
		advertiseAddress:   cfg.AdvertiseAddress,
		serverVersion:      cfg.ServerVersion,
		seedAddresses:      cfg.SeedAddresses,
		discovery:          cfg.Discovery,
		discoveryTTL:       cfg.DiscoveryTTL,
		discoveryHeartbeat: cfg.DiscoveryHeartbeat,
		protocolMin:        cfg.ProtocolMin,
		protocolMax:        cfg.ProtocolMax,
		diagnostics:        cfg.Logger,
		now:                time.Now,
		newTicker:          newDiscoveryTicker,
	})
}

// prepare advertises this node before selecting bootstrap peers. It uses one
// instant for both the lease and the live-peer query so a startup attempt has a
// single, deterministic view of discovery time.
func (m *discoveryMaintenance) prepare(ctx context.Context) ([]string, error) {
	now := m.cfg.now().UTC()
	if err := m.advertiseAt(ctx, now); err != nil {
		return nil, err
	}

	live, err := m.cfg.discovery.ListLive(ctx, now)
	if err != nil {
		m.rollback(ctx)
		return nil, fmt.Errorf("list discovery peers: %w", err)
	}

	seeds := append([]string(nil), m.cfg.seedAddresses...)
	for _, peer := range live {
		if peer.NodeID == m.cfg.nodeID {
			continue
		}
		if !protocolsCompatible(m.cfg.protocolMin, m.cfg.protocolMax, peer.ProtocolMin, peer.ProtocolMax) {
			continue
		}
		seeds = append(seeds, peer.AdvertiseAddress)
	}
	return uniqueStrings(seeds), nil
}

// run refreshes and cleans discovery until its transport-owned context is
// canceled. Schedule construction happens here rather than during module or
// transport construction.
func (m *discoveryMaintenance) run(ctx context.Context) {
	ticker := m.cfg.newTicker(m.cfg.discoveryHeartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C():
			m.maintainAt(ctx, m.cfg.now().UTC())
		}
	}
}

func (m *discoveryMaintenance) maintainAt(ctx context.Context, now time.Time) {
	if err := m.advertiseAt(ctx, now); err != nil {
		m.cfg.diagnostics.ErrorContext(ctx, "cluster discovery heartbeat failed", err)
	}
	if _, err := m.cfg.discovery.DeleteExpired(ctx, now); err != nil {
		m.cfg.diagnostics.ErrorContext(ctx, "cluster discovery cleanup failed", err)
	}
}

func (m *discoveryMaintenance) advertiseAt(ctx context.Context, now time.Time) error {
	updatedAt, expiresAt, err := discoveryLease(now, m.cfg.discoveryTTL)
	if err != nil {
		return err
	}
	return m.cfg.discovery.Upsert(ctx, cluster.DiscoveryNode{
		NodeID:           m.cfg.nodeID,
		AdvertiseAddress: m.cfg.advertiseAddress,
		ServerVersion:    m.cfg.serverVersion,
		ProtocolMin:      m.cfg.protocolMin,
		ProtocolMax:      m.cfg.protocolMax,
		UpdatedAt:        updatedAt,
		ExpiresAt:        expiresAt,
	})
}

func discoveryLease(now time.Time, ttl time.Duration) (updatedAt, expiresAt time.Time, err error) {
	if now.IsZero() {
		return time.Time{}, time.Time{}, errors.New("discovery now is required")
	}
	if ttl <= 0 {
		return time.Time{}, time.Time{}, errors.New("discovery ttl must be positive")
	}
	updatedAt = now.UTC()
	expiresAt = updatedAt.Add(ttl)
	return updatedAt, expiresAt, nil
}

// rollback withdraws an advertisement after a later startup step fails. The
// primary startup error remains authoritative; withdrawal is best-effort and
// only reported through diagnostics.
func (m *discoveryMaintenance) rollback(ctx context.Context) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), discoveryRollbackTimeout)
	defer cancel()
	if err := m.cfg.discovery.Delete(cleanupCtx, m.cfg.nodeID); err != nil {
		m.cfg.diagnostics.ErrorContext(ctx, "cluster discovery startup rollback failed", err)
	}
}

// withdraw removes the local advertisement during graceful transport stop.
// Discovery remains disposable, so withdrawal failure is diagnostic-only.
func (m *discoveryMaintenance) withdraw(ctx context.Context) {
	if err := m.cfg.discovery.Delete(ctx, m.cfg.nodeID); err != nil {
		m.cfg.diagnostics.ErrorContext(ctx, "cluster discovery delete failed", err)
	}
}

func protocolsCompatible(localMin, localMax, peerMin, peerMax int) bool {
	return localMin <= peerMax && peerMin <= localMax
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
