// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package memberlist implements Proctor's built-in multi-node cluster
// transport using HashiCorp Memberlist for gossip membership and best-effort
// direct messaging, with PostgreSQL discovery for bootstrap seeds.
package memberlist

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	hashimemberlist "github.com/hashicorp/memberlist"

	"github.com/sudosylabs/proctor/server/cluster"
)

const (
	wireProtocolVersion = 1
	maxWireBytes        = cluster.MaxMessageBytes + 4096
)

// Config configures the Memberlist transport. EncryptionKey must be 16, 24, or
// 32 bytes. BindAddress and AdvertiseAddress are host:port values.
type Config struct {
	NodeID             string
	BindAddress        string
	AdvertiseAddress   string
	EncryptionKey      []byte
	SeedAddresses      []string
	Discovery          cluster.DiscoveryStore
	DiscoveryTTL       time.Duration
	DiscoveryHeartbeat time.Duration
	ProtocolMin        int
	ProtocolMax        int
	ServerVersion      string
	AllowPublicBind    bool
	Logger             cluster.Logger
}

type state uint8

const (
	stateCreated state = iota
	stateStarted
	stateStopped
)

type nodeMeta struct {
	NodeID        string `json:"node_id"`
	ServerVersion string `json:"server_version"`
	ProtocolMin   int    `json:"protocol_min"`
	ProtocolMax   int    `json:"protocol_max"`
}

type wireEnvelope struct {
	Version int              `json:"version"`
	Source  string           `json:"source"`
	Target  string           `json:"target,omitempty"`
	Message *cluster.Message `json:"message"`
}

// Transport is the Memberlist-backed multi-node cluster adapter.
type Transport struct {
	cfg Config

	mu       sync.RWMutex
	state    state
	handlers map[cluster.Event]cluster.Handler
	list     *hashimemberlist.Memberlist
	cancel   context.CancelFunc
	done     chan struct{}
}

// New constructs an inert Memberlist transport. Start creates the gossip
// memberlist, registers discovery, and joins seeds.
func New(cfg Config) (*Transport, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	return &Transport{
		cfg:      cfg,
		state:    stateCreated,
		handlers: make(map[cluster.Event]cluster.Handler),
	}, nil
}

func validateConfig(cfg Config) error {
	if strings.TrimSpace(cfg.NodeID) == "" {
		return errors.New("cluster node ID is required")
	}
	if cfg.Logger == nil {
		return errors.New("cluster logger is required")
	}
	if cfg.Discovery == nil {
		return errors.New("cluster discovery store is required")
	}
	if len(cfg.EncryptionKey) != 16 && len(cfg.EncryptionKey) != 24 && len(cfg.EncryptionKey) != 32 {
		return errors.New("cluster encryption key must be 16, 24, or 32 bytes")
	}
	if err := validateHostPort("bind_address", cfg.BindAddress); err != nil {
		return err
	}
	if err := validateHostPort("advertise_address", cfg.AdvertiseAddress); err != nil {
		return err
	}
	if !cfg.AllowPublicBind {
		if err := rejectPublicBind(cfg.BindAddress); err != nil {
			return err
		}
	}
	if cfg.DiscoveryTTL <= 0 {
		return errors.New("discovery ttl must be positive")
	}
	if cfg.DiscoveryHeartbeat <= 0 || cfg.DiscoveryHeartbeat*2 >= cfg.DiscoveryTTL {
		return errors.New("discovery heartbeat must be greater than zero and less than half the discovery ttl")
	}
	if cfg.ProtocolMin <= 0 || cfg.ProtocolMax < cfg.ProtocolMin {
		return errors.New("protocol range is invalid")
	}
	if strings.TrimSpace(cfg.ServerVersion) == "" {
		return errors.New("server version is required")
	}
	for _, seed := range cfg.SeedAddresses {
		if err := validateHostPort("seed_address", seed); err != nil {
			return err
		}
	}
	return nil
}

func validateHostPort(field, value string) error {
	host, port, err := net.SplitHostPort(strings.TrimSpace(value))
	if err != nil || host == "" || port == "" {
		return fmt.Errorf("%s must be a host:port address", field)
	}
	return nil
}

func rejectPublicBind(bindAddress string) error {
	host, _, err := net.SplitHostPort(bindAddress)
	if err != nil {
		return err
	}
	if host == "0.0.0.0" || host == "::" || host == "[::]" {
		return errors.New("binding the cluster transport to a public interface is rejected by default")
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return nil
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return nil
	}
	return errors.New("binding the cluster transport to a public interface is rejected by default")
}

// NodeID returns this node's stable identity.
func (t *Transport) NodeID() string {
	return t.cfg.NodeID
}

// Start creates Memberlist, advertises discovery, joins seeds, and starts
// heartbeat maintenance. It is idempotent while running.
func (t *Transport) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	switch t.state {
	case stateStarted:
		return nil
	case stateStopped:
		return cluster.ErrStopped
	}

	bindHost, bindPort, err := net.SplitHostPort(t.cfg.BindAddress)
	if err != nil {
		return err
	}
	advertiseHost, advertisePort, err := net.SplitHostPort(t.cfg.AdvertiseAddress)
	if err != nil {
		return err
	}
	bindPortNumber, err := net.LookupPort("tcp", bindPort)
	if err != nil {
		return fmt.Errorf("bind port: %w", err)
	}
	advertisePortNumber, err := net.LookupPort("tcp", advertisePort)
	if err != nil {
		return fmt.Errorf("advertise port: %w", err)
	}

	config := hashimemberlist.DefaultLANConfig()
	config.Name = t.cfg.NodeID
	config.BindAddr = bindHost
	config.BindPort = bindPortNumber
	config.AdvertiseAddr = advertiseHost
	config.AdvertisePort = advertisePortNumber
	config.SecretKey = append([]byte(nil), t.cfg.EncryptionKey...)
	config.Delegate = &delegate{transport: t}
	config.Events = &eventDelegate{transport: t}
	config.Logger = newMemberlistLogger(t.cfg.Logger)

	list, err := hashimemberlist.Create(config)
	if err != nil {
		return fmt.Errorf("create memberlist: %w", err)
	}

	if err := t.advertiseDiscovery(ctx); err != nil {
		_ = list.Shutdown()
		return err
	}
	seeds, err := t.collectSeeds(ctx)
	if err != nil {
		_ = t.cfg.Discovery.Delete(context.Background(), t.cfg.NodeID)
		_ = list.Shutdown()
		return err
	}
	if len(seeds) > 0 {
		if _, err := list.Join(seeds); err != nil {
			// Join failure is not always fatal when discovery is eventual; keep
			// running so later heartbeats and gossip can still form a mesh.
			t.cfg.Logger.ErrorContext(ctx, "memberlist join incomplete", err)
		}
	}
	if err := t.rejectDuplicateNodeIDs(list); err != nil {
		_ = t.cfg.Discovery.Delete(context.Background(), t.cfg.NodeID)
		_ = list.Shutdown()
		return err
	}

	runCtx, cancel := context.WithCancel(context.Background())
	t.list = list
	t.cancel = cancel
	t.done = make(chan struct{})
	t.state = stateStarted
	go t.maintainDiscovery(runCtx)
	return nil
}

func (t *Transport) advertiseDiscovery(ctx context.Context) error {
	updatedAt, expiresAt, err := cluster.DiscoveryLease(time.Now().UTC(), t.cfg.DiscoveryTTL)
	if err != nil {
		return err
	}
	return t.cfg.Discovery.Upsert(ctx, cluster.DiscoveryNode{
		NodeID:           t.cfg.NodeID,
		AdvertiseAddress: t.cfg.AdvertiseAddress,
		ServerVersion:    t.cfg.ServerVersion,
		ProtocolMin:      t.cfg.ProtocolMin,
		ProtocolMax:      t.cfg.ProtocolMax,
		UpdatedAt:        updatedAt,
		ExpiresAt:        expiresAt,
	})
}

func (t *Transport) collectSeeds(ctx context.Context) ([]string, error) {
	seeds := append([]string(nil), t.cfg.SeedAddresses...)
	live, err := t.cfg.Discovery.ListLive(ctx, time.Now().UTC())
	if err != nil {
		return nil, fmt.Errorf("list discovery peers: %w", err)
	}
	for _, peer := range live {
		if peer.NodeID == t.cfg.NodeID {
			continue
		}
		if !protocolsCompatible(t.cfg.ProtocolMin, t.cfg.ProtocolMax, peer.ProtocolMin, peer.ProtocolMax) {
			continue
		}
		seeds = append(seeds, peer.AdvertiseAddress)
	}
	return uniqueStrings(seeds), nil
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

func (t *Transport) rejectDuplicateNodeIDs(list *hashimemberlist.Memberlist) error {
	for _, member := range list.Members() {
		if member.Name == t.cfg.NodeID {
			continue
		}
		meta, err := decodeNodeMeta(member.Meta)
		if err != nil {
			continue
		}
		if meta.NodeID == t.cfg.NodeID {
			return fmt.Errorf("%w: %s", cluster.ErrNodeIDInUse, t.cfg.NodeID)
		}
	}
	return nil
}

func (t *Transport) maintainDiscovery(ctx context.Context) {
	defer close(t.done)
	ticker := time.NewTicker(t.cfg.DiscoveryHeartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := t.advertiseDiscovery(ctx); err != nil {
				t.cfg.Logger.ErrorContext(ctx, "cluster discovery heartbeat failed", err)
			}
			if _, err := t.cfg.Discovery.DeleteExpired(ctx, time.Now().UTC()); err != nil {
				t.cfg.Logger.ErrorContext(ctx, "cluster discovery cleanup failed", err)
			}
		}
	}
}

// Stop leaves the gossip mesh, removes discovery, and is idempotent.
func (t *Transport) Stop(ctx context.Context) error {
	t.mu.Lock()
	if t.state == stateStopped {
		t.mu.Unlock()
		return nil
	}
	if t.state == stateCreated {
		t.state = stateStopped
		t.mu.Unlock()
		return nil
	}
	cancel := t.cancel
	list := t.list
	done := t.done
	t.state = stateStopped
	t.list = nil
	t.cancel = nil
	t.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	if list != nil {
		_ = list.Leave(time.Second)
		_ = list.Shutdown()
	}
	if err := t.cfg.Discovery.Delete(context.Background(), t.cfg.NodeID); err != nil {
		t.cfg.Logger.ErrorContext(ctx, "cluster discovery delete failed", err)
	}
	return nil
}

// Ping reports whether the transport is still usable. Construction-time
// dependency checks run before Start, so an unstarted transport is healthy;
// only a permanently stopped transport fails.
func (t *Transport) Ping(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.state == stateStopped {
		return cluster.ErrStopped
	}
	return nil
}

// RegisterHandler registers the sole handler for an event on this node.
func (t *Transport) RegisterHandler(event cluster.Event, handler cluster.Handler) error {
	if handler == nil {
		return errors.New("cluster message handler is nil")
	}
	probe := &cluster.Message{Event: event}
	if err := probe.Validate(); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state == stateStopped {
		return cluster.ErrStopped
	}
	if _, exists := t.handlers[event]; exists {
		return fmt.Errorf("%w: %s", cluster.ErrHandlerExists, event)
	}
	t.handlers[event] = handler
	return nil
}

// Broadcast sends a best-effort message to peer nodes only.
func (t *Transport) Broadcast(ctx context.Context, message *cluster.Message) error {
	if err := t.validateSend(ctx, message); err != nil {
		return err
	}
	payload, err := t.encodeEnvelope("", message)
	if err != nil {
		return err
	}
	t.mu.RLock()
	list := t.list
	t.mu.RUnlock()
	if list == nil {
		return cluster.ErrNotStarted
	}
	var result error
	for _, member := range list.Members() {
		if member.Name == t.cfg.NodeID {
			continue
		}
		if err := list.SendBestEffort(member, payload); err != nil {
			result = errors.Join(result, fmt.Errorf("send to %s: %w", member.Name, err))
		}
	}
	return result
}

// SendToNode delivers a best-effort message to one node. Self-target invokes
// the local handler synchronously.
func (t *Transport) SendToNode(ctx context.Context, nodeID string, message *cluster.Message) error {
	if err := t.validateSend(ctx, message); err != nil {
		return err
	}
	if nodeID == t.cfg.NodeID {
		return t.dispatchLocal(ctx, message.Clone())
	}
	payload, err := t.encodeEnvelope(nodeID, message)
	if err != nil {
		return err
	}
	t.mu.RLock()
	list := t.list
	t.mu.RUnlock()
	if list == nil {
		return cluster.ErrNotStarted
	}
	for _, member := range list.Members() {
		meta, metaErr := decodeNodeMeta(member.Meta)
		if metaErr != nil {
			if member.Name == nodeID {
				return list.SendBestEffort(member, payload)
			}
			continue
		}
		if meta.NodeID == nodeID || member.Name == nodeID {
			return list.SendBestEffort(member, payload)
		}
	}
	return fmt.Errorf("%w: %s", cluster.ErrNodeUnavailable, nodeID)
}

func (t *Transport) validateSend(ctx context.Context, message *cluster.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := message.Validate(); err != nil {
		return err
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	switch t.state {
	case stateStarted:
		return nil
	case stateStopped:
		return cluster.ErrStopped
	default:
		return cluster.ErrNotStarted
	}
}

func (t *Transport) encodeEnvelope(target string, message *cluster.Message) ([]byte, error) {
	payload, err := json.Marshal(wireEnvelope{
		Version: wireProtocolVersion,
		Source:  t.cfg.NodeID,
		Target:  target,
		Message: message.Clone(),
	})
	if err != nil {
		return nil, fmt.Errorf("encode cluster wire message: %w", err)
	}
	if len(payload) > maxWireBytes {
		return nil, fmt.Errorf("cluster wire message exceeds %d bytes", maxWireBytes)
	}
	return payload, nil
}

func (t *Transport) dispatchLocal(ctx context.Context, message *cluster.Message) error {
	t.mu.RLock()
	handler := t.handlers[message.Event]
	t.mu.RUnlock()
	if handler == nil {
		return nil
	}
	if err := callHandler(ctx, handler, message); err != nil {
		t.cfg.Logger.ErrorContext(
			ctx,
			fmt.Sprintf("cluster message handler failed event=%s data_bytes=%d", message.Event, len(message.Data)),
			err,
		)
		return fmt.Errorf("handle cluster event %s: %w", message.Event, err)
	}
	return nil
}

func callHandler(ctx context.Context, handler cluster.Handler, message *cluster.Message) (resultErr error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			resultErr = errors.New("cluster message handler panicked")
		}
	}()
	return handler(ctx, message)
}

func decodeNodeMeta(raw []byte) (nodeMeta, error) {
	var meta nodeMeta
	if len(raw) == 0 {
		return meta, errors.New("empty node metadata")
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return meta, err
	}
	if strings.TrimSpace(meta.NodeID) == "" {
		return meta, errors.New("node metadata missing node_id")
	}
	return meta, nil
}

// DecodeEncryptionKey parses a base64-encoded Memberlist secret key.
func DecodeEncryptionKey(encoded string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, fmt.Errorf("decode cluster encryption key: %w", err)
	}
	if len(key) != 16 && len(key) != 24 && len(key) != 32 {
		return nil, errors.New("cluster encryption key must decode to 16, 24, or 32 bytes")
	}
	return key, nil
}

var _ cluster.Transport = (*Transport)(nil)
