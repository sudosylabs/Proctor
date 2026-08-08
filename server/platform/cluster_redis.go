// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package platform

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/rueidis"

	"github.com/sudosylabs/proctor/server/cluster"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/mlog"
	"github.com/sudosylabs/proctor/server/model"
)

const (
	redisClusterProtocolVersion = 1
	maxRedisClusterWireBytes    = 2 << 20
)

var (
	renewRedisNodeLease = rueidis.NewLuaScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
  redis.call("PEXPIRE", KEYS[1], ARGV[2])
  redis.call("ZADD", KEYS[2], ARGV[3], ARGV[4])
  return 1
end
return 0`)
	releaseRedisNodeLease = rueidis.NewLuaScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
  redis.call("DEL", KEYS[1])
  redis.call("ZREM", KEYS[2], ARGV[2])
  return 1
end
return 0`)
	appendReliableRedisMessage = rueidis.NewLuaScript(`
if redis.call("XLEN", KEYS[1]) >= tonumber(ARGV[1]) then
  return redis.error_reply("PROCTOR_CLUSTER_RELIABLE_QUEUE_FULL")
end
return redis.call("XADD", KEYS[1], "*", "message", ARGV[2])`)
)

type redisClusterState uint8

const (
	redisClusterCreated redisClusterState = iota
	redisClusterStarted
	redisClusterStopped
)

type redisClusterEnvelope struct {
	Version   int                   `json:"version"`
	Id        string                `json:"id"`
	Source    string                `json:"source"`
	Target    string                `json:"target,omitempty"`
	CreatedAt int64                 `json:"created_at"`
	Message   *cluster.Message `json:"message"`
}

// redisCluster is Proctor's own open-source multi-node transport. Pub/Sub is
// deliberately reserved for best-effort delivery. Reliable messages are
// copied to every live peer's stream and acknowledged only after its handler
// succeeds, giving at-least-once delivery to each discovered node.
type redisCluster struct {
	nodeID   string
	settings config.ClusterRedis
	logger   *mlog.Logger
	client   rueidis.Client

	mu       sync.RWMutex
	state    redisClusterState
	handlers map[cluster.Event]cluster.Handler
	cancel   context.CancelFunc
	done     chan struct{}
	token    string
}

// NewRedisCluster constructs the Redis-backed multi-node cluster transport.
// Backend selection remains the composition root's job.
func NewRedisCluster(
	settings config.Cluster,
	logger *mlog.Logger,
) (Cluster, error) {
	if logger == nil {
		return nil, errors.New("cluster logger is required")
	}
	option := rueidis.ClientOption{
		InitAddress: append([]string(nil), settings.Redis.Addresses...),
		Username:    settings.Redis.Username,
		Password:    settings.Redis.Password,
		SelectDB:    settings.Redis.Database,
		ClientName:  "proctor-cluster-" + settings.NodeID,
		Dialer: net.Dialer{
			Timeout:   settings.Redis.ConnectTimeout.Duration,
			KeepAlive: 30 * time.Second,
		},
	}
	if settings.Redis.TLS {
		option.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	client, err := rueidis.NewClient(option)
	if err != nil {
		return nil, fmt.Errorf("create cluster Redis client: %w", err)
	}
	return &redisCluster{
		nodeID: settings.NodeID, settings: settings.Redis, client: client,
		logger: logger.With(
			mlog.String("component", "cluster"),
			mlog.String("node_id", settings.NodeID),
			mlog.String("backend", "redis"),
		),
		handlers: make(map[cluster.Event]cluster.Handler),
		token:    model.NewCredentialToken(),
	}, nil
}

func (c *redisCluster) NodeID() string {
	return c.nodeID
}

func (c *redisCluster) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	switch c.state {
	case redisClusterStarted:
		c.mu.Unlock()
		return nil
	case redisClusterStopped:
		c.mu.Unlock()
		return cluster.ErrStopped
	}
	runCtx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	c.done = make(chan struct{})
	c.mu.Unlock()

	if err := c.acquireLease(runCtx); err != nil {
		cancel()
		_ = c.releaseLease(context.Background())
		c.clearFailedStart()
		return err
	}
	if err := c.createReliableGroup(runCtx); err != nil {
		_ = c.releaseLease(context.Background())
		cancel()
		c.clearFailedStart()
		return err
	}

	c.mu.Lock()
	c.state = redisClusterStarted
	c.mu.Unlock()
	go c.run(runCtx)
	return nil
}

func (c *redisCluster) clearFailedStart() {
	c.mu.Lock()
	done := c.done
	c.cancel = nil
	c.done = nil
	c.mu.Unlock()
	if done != nil {
		close(done)
	}
}

func (c *redisCluster) Stop(ctx context.Context) error {
	c.mu.Lock()
	if c.state == redisClusterStopped {
		c.mu.Unlock()
		return nil
	}
	c.state = redisClusterStopped
	cancel := c.cancel
	done := c.done
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	var waitErr error
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			waitErr = ctx.Err()
		}
	}
	releaseErr := c.releaseLease(ctx)
	c.client.Close()
	return errors.Join(waitErr, releaseErr)
}

func (c *redisCluster) Ping(ctx context.Context) error {
	c.mu.RLock()
	state := c.state
	c.mu.RUnlock()
	if state == redisClusterStopped {
		return cluster.ErrStopped
	}
	if err := c.client.Do(ctx, c.client.B().Ping().Build()).Error(); err != nil {
		return err
	}
	if state != redisClusterStarted {
		return nil
	}
	token, err := c.client.Do(
		ctx,
		c.client.B().Get().Key(c.leaseKey(c.nodeID)).Build(),
	).ToString()
	if err != nil {
		if rueidis.IsRedisNil(err) {
			return fmt.Errorf("%w: lease for %s was lost", cluster.ErrNodeUnavailable, c.nodeID)
		}
		return err
	}
	if token != c.token {
		return fmt.Errorf("%w: %s", cluster.ErrNodeIDInUse, c.nodeID)
	}
	return nil
}

func (c *redisCluster) RegisterHandler(
	event cluster.Event,
	handler cluster.Handler,
) error {
	if handler == nil {
		return errors.New("cluster message handler is nil")
	}
	probe := &cluster.Message{Event: event}
	if err := probe.Validate(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == redisClusterStopped {
		return cluster.ErrStopped
	}
	if _, exists := c.handlers[event]; exists {
		return fmt.Errorf("%w: %s", cluster.ErrHandlerExists, event)
	}
	c.handlers[event] = handler
	return nil
}

func (c *redisCluster) Broadcast(
	ctx context.Context,
	message *cluster.Message,
) error {
	if err := c.validateSend(ctx, message); err != nil {
		return err
	}
	// Public contract is best-effort only (ADR-0026). Streams remain for
	// transitional targeted delivery helpers but are not a durable promise.
	return c.publishBestEffort(ctx, "", message)
}

func (c *redisCluster) SendToNode(
	ctx context.Context,
	nodeID string,
	message *cluster.Message,
) error {
	if err := c.validateSend(ctx, message); err != nil {
		return err
	}
	if nodeID == c.nodeID {
		return c.dispatch(ctx, c.envelope(nodeID, message))
	}
	available, err := c.nodeAvailable(ctx, nodeID)
	if err != nil {
		return err
	}
	if !available {
		return fmt.Errorf("%w: %s", cluster.ErrNodeUnavailable, nodeID)
	}
	return c.publishBestEffort(ctx, nodeID, message)
}

func (c *redisCluster) validateSend(
	ctx context.Context,
	message *cluster.Message,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := message.Validate(); err != nil {
		return err
	}
	c.mu.RLock()
	state := c.state
	c.mu.RUnlock()
	switch state {
	case redisClusterStarted:
		return nil
	case redisClusterStopped:
		return cluster.ErrStopped
	default:
		return cluster.ErrNotStarted
	}
}

func (c *redisCluster) run(ctx context.Context) {
	defer close(c.done)
	var workers sync.WaitGroup
	workers.Add(3)
	go func() {
		defer workers.Done()
		c.heartbeatLoop(ctx)
	}()
	go func() {
		defer workers.Done()
		c.bestEffortLoop(ctx)
	}()
	go func() {
		defer workers.Done()
		c.reliableLoop(ctx)
	}()
	workers.Wait()
}

func (c *redisCluster) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(c.settings.Heartbeat.Duration)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			renewed, err := renewRedisNodeLease.Exec(
				ctx,
				c.client,
				[]string{c.leaseKey(c.nodeID), c.nodesKey()},
				[]string{
					c.token,
					strconv.FormatInt(c.settings.LeaseTTL.Duration.Milliseconds(), 10),
					strconv.FormatInt(time.Now().Add(c.settings.LeaseTTL.Duration).UnixMilli(), 10),
					c.nodeID,
				},
			).ToInt64()
			if err != nil || renewed != 1 {
				c.logger.ErrorContext(ctx, "cluster node lease renewal failed", mlog.Err(err))
				c.mu.RLock()
				cancel := c.cancel
				c.mu.RUnlock()
				if cancel != nil {
					cancel()
				}
				return
			}
		}
	}
}

func (c *redisCluster) bestEffortLoop(ctx context.Context) {
	subscribe := c.client.B().Subscribe().Channel(c.pubSubKey()).Build()
	err := c.client.Receive(ctx, subscribe, func(message rueidis.PubSubMessage) {
		if len(message.Message) > maxRedisClusterWireBytes {
			c.logger.WarnContext(ctx, "discarding oversized best-effort cluster message")
			return
		}
		var envelope redisClusterEnvelope
		if err := json.Unmarshal([]byte(message.Message), &envelope); err != nil {
			c.logger.WarnContext(ctx, "discarding invalid best-effort cluster message", mlog.Err(err))
			return
		}
		if envelope.Source == c.nodeID ||
			(envelope.Target != "" && envelope.Target != c.nodeID) {
			return
		}
		if err := c.dispatch(ctx, &envelope); err != nil {
			c.logger.ErrorContext(
				ctx,
				"best-effort cluster handler failed",
				mlog.String("event", string(envelope.Message.Event)),
				mlog.Err(err),
			)
		}
	})
	if err != nil && !errors.Is(err, context.Canceled) && ctx.Err() == nil {
		c.logger.ErrorContext(ctx, "cluster Pub/Sub listener stopped", mlog.Err(err))
	}
}

func (c *redisCluster) reliableLoop(ctx context.Context) {
	stream := c.streamKey(c.nodeID)
	for ctx.Err() == nil {
		processed, err := c.readReliable(ctx, stream, "0")
		if err != nil {
			c.logReliableReadError(ctx, err)
			continue
		}
		if processed {
			continue
		}
		_, err = c.readReliable(ctx, stream, ">")
		if err != nil {
			c.logReliableReadError(ctx, err)
		}
	}
}

func (c *redisCluster) readReliable(
	ctx context.Context,
	stream string,
	id string,
) (bool, error) {
	result := c.client.Do(
		ctx,
		c.client.B().Xreadgroup().
			Group(c.consumerGroup(), c.nodeID).
			Count(32).
			Block(1000).
			Streams().
			Key(stream).
			Id(id).
			Build(),
	)
	entriesByStream, err := result.AsXRead()
	if err != nil {
		if rueidis.IsRedisNil(err) || errors.Is(err, context.Canceled) {
			return false, nil
		}
		return false, err
	}
	entries := entriesByStream[stream]
	for _, entry := range entries {
		raw, exists := entry.FieldValues["message"]
		if !exists {
			c.logger.WarnContext(ctx, "discarding malformed reliable cluster message")
			if err := c.ackReliable(ctx, stream, entry.ID); err != nil {
				return true, err
			}
			continue
		}
		if len(raw) > maxRedisClusterWireBytes {
			c.logger.WarnContext(ctx, "discarding oversized reliable cluster message")
			if err := c.ackReliable(ctx, stream, entry.ID); err != nil {
				return true, err
			}
			continue
		}
		var envelope redisClusterEnvelope
		if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
			c.logger.WarnContext(ctx, "discarding invalid reliable cluster message", mlog.Err(err))
			if err := c.ackReliable(ctx, stream, entry.ID); err != nil {
				return true, err
			}
			continue
		}
		if err := c.dispatch(ctx, &envelope); err != nil {
			c.logger.ErrorContext(
				ctx,
				"reliable cluster handler failed; delivery remains pending",
				mlog.String("event", string(envelope.Message.Event)),
				mlog.String("message_id", envelope.Id),
				mlog.Err(err),
			)
			select {
			case <-ctx.Done():
			case <-time.After(250 * time.Millisecond):
			}
			return true, nil
		}
		if err := c.ackReliable(ctx, stream, entry.ID); err != nil {
			return true, err
		}
	}
	return len(entries) != 0, nil
}

func (c *redisCluster) logReliableReadError(ctx context.Context, err error) {
	if errors.Is(err, context.Canceled) || ctx.Err() != nil {
		return
	}
	c.logger.ErrorContext(ctx, "reliable cluster reader failed", mlog.Err(err))
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
	}
}

func (c *redisCluster) dispatch(
	ctx context.Context,
	envelope *redisClusterEnvelope,
) error {
	if envelope == nil || envelope.Version != redisClusterProtocolVersion ||
		!model.IsValidId(envelope.Id) || !validRedisClusterNodeID(envelope.Source) ||
		(envelope.Target != "" && !validRedisClusterNodeID(envelope.Target)) ||
		envelope.CreatedAt <= 0 || envelope.Message == nil {
		return errors.New("invalid cluster wire envelope")
	}
	if envelope.Target != "" && envelope.Target != c.nodeID {
		return nil
	}
	if err := envelope.Message.Validate(); err != nil {
		return err
	}
	c.mu.RLock()
	handler := c.handlers[envelope.Message.Event]
	c.mu.RUnlock()
	if handler == nil {
		return nil
	}
	return callClusterHandler(ctx, handler, envelope.Message.Clone())
}

func validRedisClusterNodeID(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
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

func (c *redisCluster) envelope(
	target string,
	message *cluster.Message,
) *redisClusterEnvelope {
	return &redisClusterEnvelope{
		Version: redisClusterProtocolVersion,
		Id:      model.NewId(), Source: c.nodeID, Target: target,
		CreatedAt: time.Now().UnixMilli(), Message: message.Clone(),
	}
}

func (c *redisCluster) publishBestEffort(
	ctx context.Context,
	target string,
	message *cluster.Message,
) error {
	payload, err := json.Marshal(c.envelope(target, message))
	if err != nil {
		return err
	}
	return c.client.Do(
		ctx,
		c.client.B().Publish().Channel(c.pubSubKey()).Message(string(payload)).Build(),
	).Error()
}

func (c *redisCluster) appendReliable(
	ctx context.Context,
	target string,
	message *cluster.Message,
) error {
	payload, err := json.Marshal(c.envelope(target, message))
	if err != nil {
		return err
	}
	_, err = appendReliableRedisMessage.Exec(
		ctx,
		c.client,
		[]string{c.streamKey(target)},
		[]string{strconv.Itoa(c.settings.ReliableMaximum), string(payload)},
	).ToString()
	return err
}

func (c *redisCluster) ackReliable(
	ctx context.Context,
	stream string,
	id string,
) error {
	if err := c.client.Do(
		ctx,
		c.client.B().Xack().Key(stream).Group(c.consumerGroup()).Id(id).Build(),
	).Error(); err != nil {
		return err
	}
	return c.client.Do(ctx, c.client.B().Xdel().Key(stream).Id(id).Build()).Error()
}

func (c *redisCluster) acquireLease(ctx context.Context) error {
	result := c.client.Do(
		ctx,
		c.client.B().Set().
			Key(c.leaseKey(c.nodeID)).
			Value(c.token).
			Nx().
			PxMilliseconds(c.settings.LeaseTTL.Duration.Milliseconds()).
			Build(),
	)
	if err := result.Error(); err != nil {
		if rueidis.IsRedisNil(err) {
			return fmt.Errorf("%w: %s", cluster.ErrNodeIDInUse, c.nodeID)
		}
		return err
	}
	return c.client.Do(
		ctx,
		c.client.B().Zadd().
			Key(c.nodesKey()).
			ScoreMember().
			ScoreMember(
				float64(time.Now().Add(c.settings.LeaseTTL.Duration).UnixMilli()),
				c.nodeID,
			).
			Build(),
	).Error()
}

func (c *redisCluster) releaseLease(ctx context.Context) error {
	if c.client == nil {
		return nil
	}
	_, err := releaseRedisNodeLease.Exec(
		ctx,
		c.client,
		[]string{c.leaseKey(c.nodeID), c.nodesKey()},
		[]string{c.token, c.nodeID},
	).ToInt64()
	return err
}

func (c *redisCluster) createReliableGroup(ctx context.Context) error {
	err := c.client.Do(
		ctx,
		c.client.B().XgroupCreate().
			Key(c.streamKey(c.nodeID)).
			Group(c.consumerGroup()).
			Id("0").
			Mkstream().
			Build(),
	).Error()
	if redisErrorContains(err, "BUSYGROUP") {
		return nil
	}
	return err
}

func (c *redisCluster) liveNodes(ctx context.Context) ([]string, error) {
	now := time.Now().UnixMilli()
	if err := c.client.Do(
		ctx,
		c.client.B().Zremrangebyscore().
			Key(c.nodesKey()).
			Min("-inf").
			Max(strconv.FormatInt(now-1, 10)).
			Build(),
	).Error(); err != nil {
		return nil, err
	}
	return c.client.Do(
		ctx,
		c.client.B().Zrangebyscore().
			Key(c.nodesKey()).
			Min(strconv.FormatInt(now, 10)).
			Max("+inf").
			Build(),
	).AsStrSlice()
}

func (c *redisCluster) nodeAvailable(ctx context.Context, nodeID string) (bool, error) {
	nodes, err := c.liveNodes(ctx)
	if err != nil {
		return false, err
	}
	for _, current := range nodes {
		if current == nodeID {
			return true, nil
		}
	}
	return false, nil
}

func redisErrorContains(err error, value string) bool {
	return err != nil && strings.Contains(err.Error(), value)
}

func (c *redisCluster) prefix() string {
	// The hash tag keeps every coordination key for one logical
	// installation in the same Redis Cluster slot, including the two-key
	// lease renewal/release scripts.
	return "{" + c.settings.Namespace + ":cluster}"
}

func (c *redisCluster) pubSubKey() string {
	return c.prefix() + ":best_effort"
}

func (c *redisCluster) nodesKey() string {
	return c.prefix() + ":nodes"
}

func (c *redisCluster) leaseKey(nodeID string) string {
	return c.prefix() + ":node:" + nodeID + ":lease"
}

func (c *redisCluster) streamKey(nodeID string) string {
	return c.prefix() + ":node:" + nodeID + ":reliable"
}

func (c *redisCluster) consumerGroup() string {
	return c.prefix() + ":consumers"
}

var _ Cluster = (*redisCluster)(nil)
