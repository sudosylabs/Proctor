// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

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
	wireProtocolVersion       = 1
	supportedProtocolMin      = wireProtocolVersion
	supportedProtocolMax      = wireProtocolVersion
	maximumDecryptionKeys     = 8
	maximumConfiguredSeeds    = 32
	maximumJoinCandidates     = 64
	maximumJoinAttemptsPerRun = 3
	maxWireBytes              = cluster.MaxMessageBytes + 4096
)

// Config configures the Memberlist transport. EncryptionKey is the primary
// encryption key; DecryptionKeys are bounded fallback keys used during a
// rolling rotation. Every key must be 16, 24, or 32 bytes. BindAddress and
// AdvertiseAddress are host:port values.
type Config struct {
	NodeID             string
	BindAddress        string
	AdvertiseAddress   string
	EncryptionKey      []byte
	DecryptionKeys     [][]byte
	SeedAddresses      []string
	Discovery          cluster.DiscoveryStore
	DiscoveryTTL       time.Duration
	DiscoveryHeartbeat time.Duration
	ServerVersion      string
	AllowPublicBind    bool
	Logger             cluster.Logger
	Metrics            cluster.Metrics
}

type state uint8

const (
	stateCreated state = iota
	stateStarted
	stateStopping
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
	cfg       Config
	discovery *discoveryMaintenance
	lookupIP  func(context.Context, string) ([]net.IPAddr, error)

	mu       sync.RWMutex
	stopMu   sync.Mutex
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
	cfg.EncryptionKey = append([]byte(nil), cfg.EncryptionKey...)
	cfg.DecryptionKeys = cloneKeys(cfg.DecryptionKeys)
	cfg.SeedAddresses = append([]string(nil), cfg.SeedAddresses...)
	return &Transport{
		cfg:       cfg,
		discovery: newSystemDiscoveryMaintenance(cfg),
		lookupIP:  net.DefaultResolver.LookupIPAddr,
		state:     stateCreated,
		handlers:  make(map[cluster.Event]cluster.Handler),
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
	if err := validateEncryptionKeys(cfg.EncryptionKey, cfg.DecryptionKeys); err != nil {
		return err
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
	if strings.TrimSpace(cfg.ServerVersion) == "" {
		return errors.New("server version is required")
	}
	probeAt := time.Unix(1, 0).UTC()
	if err := (cluster.DiscoveryNode{
		NodeID:           cfg.NodeID,
		AdvertiseAddress: cfg.AdvertiseAddress,
		ServerVersion:    cfg.ServerVersion,
		ProtocolMin:      supportedProtocolMin,
		ProtocolMax:      supportedProtocolMax,
		UpdatedAt:        probeAt,
		ExpiresAt:        probeAt.Add(time.Second),
	}).Validate(); err != nil {
		return fmt.Errorf("cluster node metadata: %w", err)
	}
	if len(cfg.SeedAddresses) > maximumConfiguredSeeds {
		return fmt.Errorf("seed addresses must contain at most %d entries", maximumConfiguredSeeds)
	}
	for _, seed := range cfg.SeedAddresses {
		if err := validateHostPort("seed_address", seed); err != nil {
			return err
		}
	}
	meta, err := json.Marshal(localNodeMeta(cfg))
	if err != nil {
		return fmt.Errorf("encode local node metadata: %w", err)
	}
	if len(meta) > hashimemberlist.MetaMaxSize {
		return fmt.Errorf("local node metadata exceeds %d bytes", hashimemberlist.MetaMaxSize)
	}
	return nil
}

func validateEncryptionKeys(primary []byte, fallbacks [][]byte) error {
	if err := validateEncryptionKey(primary); err != nil {
		return err
	}
	if len(fallbacks) > maximumDecryptionKeys {
		return fmt.Errorf("cluster decryption keys must contain at most %d entries", maximumDecryptionKeys)
	}
	seen := make(map[string]struct{}, 1+len(fallbacks))
	seen[string(primary)] = struct{}{}
	for index, key := range fallbacks {
		if err := validateEncryptionKey(key); err != nil {
			return fmt.Errorf("cluster decryption key %d: %w", index, err)
		}
		if _, exists := seen[string(key)]; exists {
			return errors.New("cluster encryption keyring contains duplicate keys")
		}
		seen[string(key)] = struct{}{}
	}
	return nil
}

func validateEncryptionKey(key []byte) error {
	if len(key) != 16 && len(key) != 24 && len(key) != 32 {
		return errors.New("cluster encryption key must be 16, 24, or 32 bytes")
	}
	return nil
}

func cloneKeys(keys [][]byte) [][]byte {
	cloned := make([][]byte, len(keys))
	for index := range keys {
		cloned[index] = append([]byte(nil), keys[index]...)
	}
	return cloned
}

func localNodeMeta(cfg Config) nodeMeta {
	return nodeMeta{
		NodeID:        cfg.NodeID,
		ServerVersion: cfg.ServerVersion,
		ProtocolMin:   supportedProtocolMin,
		ProtocolMax:   supportedProtocolMax,
	}
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

// PeerCount reports the current node-local mesh view without exposing member
// identity or metadata.
func (t *Transport) PeerCount() int {
	t.mu.RLock()
	list := t.list
	t.mu.RUnlock()
	if list == nil {
		return 0
	}
	return max(list.NumMembers()-1, 0)
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
	case stateStopping, stateStopped:
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
	advertiseHost, err = t.resolveAdvertiseHost(ctx, advertiseHost)
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
	// A crashed process cannot announce StateLeft. Once peers have declared it
	// dead, wait one full discovery lease before allowing the stable name to
	// move to another address; periodic rediscovery keeps retrying during that
	// safety window.
	config.DeadNodeReclaimTime = t.cfg.DiscoveryTTL
	config.Name = t.cfg.NodeID
	config.BindAddr = bindHost
	config.BindPort = bindPortNumber
	config.AdvertiseAddr = advertiseHost
	config.AdvertisePort = advertisePortNumber
	keyring, err := hashimemberlist.NewKeyring(cloneKeys(t.cfg.DecryptionKeys), append([]byte(nil), t.cfg.EncryptionKey...))
	if err != nil {
		return fmt.Errorf("create memberlist keyring: %w", err)
	}
	admission := newAdmissionDelegate(t)
	config.Keyring = keyring
	config.Delegate = &delegate{transport: t}
	config.Events = &eventDelegate{transport: t}
	config.Alive = admission
	config.Merge = admission
	config.Conflict = admission
	config.DelegateProtocolVersion = uint8(wireProtocolVersion)
	config.DelegateProtocolMin = uint8(supportedProtocolMin)
	config.DelegateProtocolMax = uint8(supportedProtocolMax)
	config.Logger = newMemberlistLogger(t.cfg.Logger)

	list, err := hashimemberlist.Create(config)
	if err != nil {
		return fmt.Errorf("create memberlist: %w", err)
	}

	seeds, err := t.discovery.prepare(ctx)
	if err != nil {
		_ = list.Shutdown()
		return err
	}
	if len(seeds) > 0 {
		if _, err := list.Join(seeds); err != nil {
			if t.cfg.Metrics != nil {
				t.cfg.Metrics.ObserveClusterDiscovery("initial_join", "error")
			}
			t.cfg.Logger.ErrorContext(ctx, "memberlist join incomplete", err)
		} else if t.cfg.Metrics != nil {
			t.cfg.Metrics.ObserveClusterDiscovery("initial_join", "success")
		}
	} else if t.cfg.Metrics != nil {
		t.cfg.Metrics.ObserveClusterDiscovery("initial_join", "empty")
	}
	if err := admission.finishStartup(); err != nil {
		t.discovery.rollback(ctx)
		_ = list.Shutdown()
		return err
	}
	if err := t.admitJoinedPeers(list.LocalNode(), list.Members()); err != nil {
		if t.cfg.Metrics != nil {
			t.cfg.Metrics.ObserveClusterAdmission(admissionReason(err))
		}
		t.discovery.rollback(ctx)
		_ = list.Shutdown()
		return err
	}

	runCtx, cancel := context.WithCancel(context.Background())
	t.list = list
	t.cancel = cancel
	t.done = make(chan struct{})
	t.state = stateStarted
	done := t.done
	reconciler := newSeedReconciler(list)
	go func() {
		defer close(done)
		t.discovery.run(runCtx, func(ctx context.Context, seeds []string) error {
			return reconciler.rejoin(ctx, seeds)
		})
	}()
	return nil
}

// resolveAdvertiseHost translates a stable service name into the concrete IP
// required by Memberlist while preserving the configured hostname in durable
// discovery. This lets orchestrators assign container IPs without making those
// disposable addresses part of Proctor's configuration contract.
func (t *Transport) resolveAdvertiseHost(ctx context.Context, host string) (string, error) {
	if ip := net.ParseIP(host); ip != nil {
		return ip.String(), nil
	}
	addresses, err := t.lookupIP(ctx, host)
	if err != nil {
		return "", fmt.Errorf("resolve cluster advertise host %q: %w", host, err)
	}
	for _, address := range addresses {
		if ipv4 := address.IP.To4(); ipv4 != nil {
			return ipv4.String(), nil
		}
	}
	for _, address := range addresses {
		if ipv6 := address.IP.To16(); ipv6 != nil {
			return ipv6.String(), nil
		}
	}
	return "", fmt.Errorf("resolve cluster advertise host %q: no IP addresses returned", host)
}

// Stop leaves the gossip mesh, removes discovery, and is idempotent.
func (t *Transport) Stop(ctx context.Context) error {
	t.stopMu.Lock()
	defer t.stopMu.Unlock()

	t.mu.Lock()
	switch t.state {
	case stateStopped:
		t.mu.Unlock()
		return nil
	case stateCreated:
		t.state = stateStopped
		t.mu.Unlock()
		return nil
	case stateStarted:
		t.state = stateStopping
	}
	cancel := t.cancel
	done := t.done
	if cancel != nil {
		t.cancel = nil
	}
	t.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		// DiscoveryStore operations receive the canceled maintenance context and
		// must release it. Wait for the owned goroutine even when the caller's
		// deadline expires so Platform can safely close persistence afterwards.
		<-done
		t.mu.Lock()
		if t.done == done {
			t.done = nil
		}
		t.mu.Unlock()
	}

	t.mu.Lock()
	list := t.list
	t.mu.Unlock()
	if list != nil {
		if timeout, ok := memberlistLeaveTimeout(ctx); ok {
			_ = list.Leave(timeout)
		}
		_ = list.Shutdown()
		t.mu.Lock()
		if t.list == list {
			t.list = nil
		}
		t.mu.Unlock()
	}
	if ctx.Err() == nil {
		t.discovery.withdraw(ctx)
	}
	t.mu.Lock()
	t.state = stateStopped
	t.done = nil
	t.cancel = nil
	t.list = nil
	t.mu.Unlock()
	return ctx.Err()
}

func memberlistLeaveTimeout(ctx context.Context) (time.Duration, bool) {
	if ctx.Err() != nil {
		return 0, false
	}
	timeout := time.Second
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return 0, false
		}
		if remaining < timeout {
			timeout = remaining
		}
	}
	return timeout, true
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
	if t.state == stateStopping || t.state == stateStopped {
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
	if t.state == stateStopping || t.state == stateStopped {
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
	members := list.Members()
	recipients := 0
	for _, member := range members {
		if member != nil && member.Name != t.cfg.NodeID {
			recipients++
		}
	}
	if t.cfg.Metrics != nil {
		t.cfg.Metrics.ObserveClusterFanout(recipients)
	}
	return broadcastMembers(
		ctx,
		t.cfg.NodeID,
		members,
		payload,
		list.SendBestEffort,
		func(err error) { t.observeSentMessage(message.Event, payload, err) },
	)
}

func broadcastMembers(
	ctx context.Context,
	localNodeID string,
	members []*hashimemberlist.Node,
	payload []byte,
	send func(*hashimemberlist.Node, []byte) error,
	observers ...func(error),
) error {
	var result error
	for _, member := range members {
		if err := ctx.Err(); err != nil {
			return errors.Join(result, err)
		}
		if member.Name == localNodeID {
			continue
		}
		err := send(member, payload)
		for _, observe := range observers {
			if observe != nil {
				observe(err)
			}
		}
		if err != nil {
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
		err := t.dispatchLocal(ctx, message.Clone())
		if t.cfg.Metrics != nil {
			result := "success"
			if err != nil {
				result = "error"
			}
			t.cfg.Metrics.ObserveClusterMessage("local", t.metricEvent(message.Event), result, len(message.Data))
		}
		return err
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
				err = list.SendBestEffort(member, payload)
				t.observeSentMessage(message.Event, payload, err)
				return err
			}
			continue
		}
		if meta.NodeID == nodeID || member.Name == nodeID {
			err = list.SendBestEffort(member, payload)
			t.observeSentMessage(message.Event, payload, err)
			return err
		}
	}
	return fmt.Errorf("%w: %s", cluster.ErrNodeUnavailable, nodeID)
}

func (t *Transport) observeSentMessage(event cluster.Event, payload []byte, err error) {
	if t.cfg.Metrics == nil {
		return
	}
	result := "success"
	if err != nil {
		result = "error"
	}
	t.cfg.Metrics.ObserveClusterMessage("send", t.metricEvent(event), result, len(payload))
}

// metricEvent exposes only startup-registered event names. Message validation
// bounds syntax and size, but registration is what closes metric cardinality.
func (t *Transport) metricEvent(event cluster.Event) string {
	t.mu.RLock()
	_, registered := t.handlers[event]
	t.mu.RUnlock()
	if !registered {
		return "unregistered"
	}
	return string(event)
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
	case stateStopping, stateStopped:
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

// DecodeEncryptionKeyring parses the primary key and bounded fallback keys.
func DecodeEncryptionKeyring(primary string, fallbacks []string) ([]byte, [][]byte, error) {
	primaryKey, err := DecodeEncryptionKey(primary)
	if err != nil {
		return nil, nil, err
	}
	decodedFallbacks := make([][]byte, 0, len(fallbacks))
	for index, encoded := range fallbacks {
		key, decodeErr := DecodeEncryptionKey(encoded)
		if decodeErr != nil {
			return nil, nil, fmt.Errorf("decode cluster decryption key %d: %w", index, decodeErr)
		}
		decodedFallbacks = append(decodedFallbacks, key)
	}
	if err := validateEncryptionKeys(primaryKey, decodedFallbacks); err != nil {
		return nil, nil, err
	}
	return primaryKey, decodedFallbacks, nil
}

var _ cluster.Transport = (*Transport)(nil)
