// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package server

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	mailpkg "github.com/sudosylabs/proctor/packages/mail"
	vfspkg "github.com/sudosylabs/proctor/packages/vfs"
	apppkg "github.com/sudosylabs/proctor/server/app"
	appexecution "github.com/sudosylabs/proctor/server/app/execution"
	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/cluster"
	metricspkg "github.com/sudosylabs/proctor/server/metrics"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/platform"
)

type jobMetricsRecorder struct{ metrics *metricspkg.Module }

type applicationMetricsRecorder struct{ metrics *metricspkg.Module }

func (r applicationMetricsRecorder) RecordOperationalEvent(event apppkg.OperationalEvent) {
	r.metrics.ObserveApplication(event.Subsystem(), event.Event(), event.Outcome())
}

type prometheusMailRecorder struct {
	metrics *metricspkg.Module
	mu      sync.Mutex
	queues  map[mailQueueMetricIdentity]struct{}
}

func newPrometheusMailRecorder(metrics *metricspkg.Module) *prometheusMailRecorder {
	return &prometheusMailRecorder{metrics: metrics, queues: make(map[mailQueueMetricIdentity]struct{})}
}

func (r *prometheusMailRecorder) RecordMailDelivery(_ context.Context, metric apppkg.MailDeliveryMetric) {
	r.metrics.ObserveMailDelivery(string(metric.TemplateKey), string(metric.State), metric.OutcomeCode, metric.ProcessingLatency)
}

func (r *prometheusMailRecorder) RecordMailAttempt(_ context.Context, metric apppkg.MailAttemptMetric) {
	r.metrics.ObserveMailAttempt(string(metric.TemplateKey), string(metric.State), 1)
}

func (r *prometheusMailRecorder) RecordMailQueueSnapshot(_ context.Context, values []apppkg.MailQueueMetric) {
	r.mu.Lock()
	aggregates, truncated := aggregateMailQueueMetrics(values)
	next := make(map[mailQueueMetricIdentity]struct{}, len(aggregates))
	for key, value := range aggregates {
		next[key] = struct{}{}
		r.metrics.SetMailQueue(string(key.template), string(key.state), value.count, value.oldestAge)
	}
	for key := range r.queues {
		if _, exists := next[key]; exists {
			continue
		}
		r.metrics.SetMailQueue(string(key.template), string(key.state), 0, 0)
	}
	r.metrics.SetMailQueueSnapshotTruncated(truncated)
	r.queues = next
	r.mu.Unlock()
}

type mailQueueAggregate struct {
	count     int64
	oldestAge time.Duration
}

type mailQueueMetricIdentity struct {
	template model.MailTemplateKey
	state    model.MailDeliveryState
}

func aggregateMailQueueMetrics(values []apppkg.MailQueueMetric) (map[mailQueueMetricIdentity]mailQueueAggregate, bool) {
	result := make(map[mailQueueMetricIdentity]mailQueueAggregate, len(values))
	truncated := false
	for _, value := range values {
		key := mailQueueMetricIdentity{template: value.TemplateKey, state: value.State}
		aggregate := result[key]
		aggregate.count += max(value.Count, int64(0))
		if value.OldestAge > aggregate.oldestAge {
			aggregate.oldestAge = value.OldestAge
		}
		result[key] = aggregate
		truncated = truncated || value.Truncated
	}
	return result, truncated
}

func (r *prometheusMailRecorder) RecordMailHealth(_ context.Context, metric apppkg.MailHealthMetric) {
	r.metrics.SetMailHealth(metric.Code)
}

type fanoutMailDeliveryRecorder struct {
	first  apppkg.MailDeliveryRecorder
	second apppkg.MailDeliveryRecorder
}

func (r fanoutMailDeliveryRecorder) RecordMailDelivery(ctx context.Context, metric apppkg.MailDeliveryMetric) {
	r.first.RecordMailDelivery(ctx, metric)
	r.second.RecordMailDelivery(ctx, metric)
}

func (r fanoutMailDeliveryRecorder) RecordMailAttempt(ctx context.Context, metric apppkg.MailAttemptMetric) {
	r.first.RecordMailAttempt(ctx, metric)
	r.second.RecordMailAttempt(ctx, metric)
}

func (r fanoutMailDeliveryRecorder) RecordMailQueueSnapshot(ctx context.Context, metrics []apppkg.MailQueueMetric) {
	r.first.RecordMailQueueSnapshot(ctx, metrics)
	r.second.RecordMailQueueSnapshot(ctx, metrics)
}

func (r fanoutMailDeliveryRecorder) RecordMailHealth(ctx context.Context, metric apppkg.MailHealthMetric) {
	r.first.RecordMailHealth(ctx, metric)
	r.second.RecordMailHealth(ctx, metric)
}

func (r jobMetricsRecorder) Started(jobType model.JobType) {
	r.metrics.JobStarted(string(jobType))
}

func (r jobMetricsRecorder) Finished(jobType model.JobType, result jobengine.ExecutionOutcome, duration time.Duration) {
	r.metrics.JobFinished(string(jobType), string(result), duration)
}

func (r jobMetricsRecorder) Record(activity jobengine.Activity) {
	r.metrics.ObserveJobActivity(activity.Kind, activity.Name, activity.Operation, activity.Outcome, activity.Duration, activity.QueueLatency)
}

type measuredCache struct {
	next    platform.Cache
	backend string
	metrics *metricspkg.Module
}

func (m *measuredCache) Get(ctx context.Context, key string) ([]byte, error) {
	started := time.Now()
	value, err := m.next.Get(ctx, key)
	m.metrics.ObserveCacheResult(m.backend, "get", cacheOutcome(err), time.Since(started))
	m.metrics.AddCacheBytes(m.backend, "read", len(value))
	return value, err
}
func (m *measuredCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration, condition platform.CacheCondition) error {
	started := time.Now()
	err := m.next.Set(ctx, key, value, ttl, condition)
	m.metrics.ObserveCacheResult(m.backend, "set", cacheOutcome(err), time.Since(started))
	if err == nil {
		m.metrics.AddCacheBytes(m.backend, "write", len(value))
	}
	return err
}
func (m *measuredCache) Delete(ctx context.Context, key string) error {
	started := time.Now()
	err := m.next.Delete(ctx, key)
	m.metrics.ObserveCacheResult(m.backend, "delete", cacheOutcome(err), time.Since(started))
	return err
}
func (m *measuredCache) Add(ctx context.Context, key string, delta int64, ttl time.Duration) (int64, error) {
	started := time.Now()
	value, err := m.next.Add(ctx, key, delta, ttl)
	m.metrics.ObserveCacheResult(m.backend, "add", cacheOutcome(err), time.Since(started))
	return value, err
}
func (m *measuredCache) Ping(ctx context.Context) error {
	started := time.Now()
	err := m.next.Ping(ctx)
	m.metrics.ObserveCacheResult(m.backend, "ping", cacheOutcome(err), time.Since(started))
	return err
}
func (m *measuredCache) Close() error { return m.next.Close() }

func cacheOutcome(err error) string {
	switch {
	case err == nil:
		return "success"
	case errors.Is(err, platform.ErrCacheMiss):
		return "miss"
	case errors.Is(err, platform.ErrCacheNotStored):
		return "not_stored"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	default:
		return "error"
	}
}

type measuredMailer struct {
	next    platform.Mailer
	metrics *metricspkg.Module
}

func (m *measuredMailer) Enabled() bool         { return m.next.Enabled() }
func (m *measuredMailer) From() mailpkg.Address { return m.next.From() }
func (m *measuredMailer) Send(ctx context.Context, message mailpkg.Message) (mailpkg.Receipt, error) {
	started := time.Now()
	receipt, err := m.next.Send(ctx, message)
	operation := "send"
	var operationError *mailpkg.OpError
	if errors.As(err, &operationError) {
		operation = boundedSMTPOperation(operationError.Op)
	}
	m.metrics.ObserveSMTP(operation, err, time.Since(started))
	m.metrics.ObserveSMTPMessage(smtpOutcome(err), mailRecipientCount(message), mailPayloadBytes(message))
	return receipt, err
}
func (m *measuredMailer) Test(ctx context.Context) error {
	started := time.Now()
	err := m.next.Test(ctx)
	m.metrics.ObserveSMTP("test", err, time.Since(started))
	return err
}
func (m *measuredMailer) Close() error { return m.next.Close() }

func boundedSMTPOperation(operation string) string {
	switch operation {
	case "send", "test", "connect", "greeting", "hello", "starttls", "authenticate", "mail-from", "recipient", "data", "write", "commit", "quit":
		return operation
	default:
		return "unknown"
	}
}

func smtpOutcome(err error) string {
	if err == nil {
		return "accepted"
	}
	return string(mailpkg.Classify(err))
}

func mailRecipientCount(message mailpkg.Message) int {
	return len(message.To) + len(message.CC) + len(message.BCC)
}

func mailPayloadBytes(message mailpkg.Message) int {
	total := len(message.Subject) + len(message.Text) + len(message.HTML)
	for _, attachment := range message.Attachments {
		total += len(attachment.Data)
	}
	return total
}

type measuredVFS struct {
	next    vfspkg.FileSystem
	backend string
	metrics *metricspkg.Module
}

func (m *measuredVFS) Capabilities() vfspkg.Capabilities { return m.next.Capabilities() }
func (m *measuredVFS) Open(ctx context.Context, path string, options vfspkg.OpenOptions) (*vfspkg.File, error) {
	started := time.Now()
	value, err := m.next.Open(ctx, path, options)
	m.metrics.ObserveVFSResult(m.backend, "open", vfsOutcome(err), time.Since(started))
	if err == nil && value != nil {
		m.metrics.ObserveVFSObject(m.backend, "open", value.Info.Size)
		value.Body = newMeasuredReadCloser(value.Body, func(bytes int) {
			m.metrics.AddVFSBytes(m.backend, "read", int64(bytes))
		}, func(result string) {
			m.metrics.ObserveVFSStream(m.backend, result)
		})
	}
	return value, err
}
func (m *measuredVFS) Write(ctx context.Context, path string, body io.Reader, options vfspkg.WriteOptions) (vfspkg.Info, error) {
	started := time.Now()
	if body == nil {
		value, err := m.next.Write(ctx, path, nil, options)
		m.metrics.ObserveVFSResult(m.backend, "write", vfsOutcome(err), time.Since(started))
		return value, err
	}
	counter := &countingReader{reader: body}
	value, err := m.next.Write(ctx, path, counter, options)
	m.metrics.ObserveVFSResult(m.backend, "write", vfsOutcome(err), time.Since(started))
	m.metrics.AddVFSBytes(m.backend, "write", counter.bytes)
	if err == nil {
		m.metrics.ObserveVFSObject(m.backend, "write", value.Size)
	}
	return value, err
}
func (m *measuredVFS) Stat(ctx context.Context, path string) (vfspkg.Info, error) {
	started := time.Now()
	value, err := m.next.Stat(ctx, path)
	m.metrics.ObserveVFSResult(m.backend, "stat", vfsOutcome(err), time.Since(started))
	if err == nil {
		m.metrics.ObserveVFSObject(m.backend, "stat", value.Size)
	}
	return value, err
}
func (m *measuredVFS) Remove(ctx context.Context, path string, options vfspkg.RemoveOptions) error {
	started := time.Now()
	err := m.next.Remove(ctx, path, options)
	m.metrics.ObserveVFSResult(m.backend, "remove", vfsOutcome(err), time.Since(started))
	return err
}
func (m *measuredVFS) List(ctx context.Context, options vfspkg.ListOptions) (vfspkg.Page, error) {
	started := time.Now()
	value, err := m.next.List(ctx, options)
	m.metrics.ObserveVFSResult(m.backend, "list", vfsOutcome(err), time.Since(started))
	if err == nil {
		m.metrics.ObserveVFSList(m.backend, len(value.Entries))
	}
	return value, err
}
func (m *measuredVFS) Copy(ctx context.Context, source, destination string, options vfspkg.TransferOptions) (vfspkg.Info, error) {
	started := time.Now()
	value, err := m.next.Copy(ctx, source, destination, options)
	m.metrics.ObserveVFSResult(m.backend, "copy", vfsOutcome(err), time.Since(started))
	if err == nil {
		m.metrics.ObserveVFSObject(m.backend, "copy", value.Size)
	}
	return value, err
}
func (m *measuredVFS) Move(ctx context.Context, source, destination string, options vfspkg.TransferOptions) (vfspkg.Info, error) {
	started := time.Now()
	value, err := m.next.Move(ctx, source, destination, options)
	m.metrics.ObserveVFSResult(m.backend, "move", vfsOutcome(err), time.Since(started))
	if err == nil {
		m.metrics.ObserveVFSObject(m.backend, "move", value.Size)
	}
	return value, err
}

func vfsOutcome(err error) string {
	switch {
	case err == nil:
		return "success"
	case errors.Is(err, vfspkg.ErrNotFound):
		return "not_found"
	case errors.Is(err, vfspkg.ErrAlreadyExists):
		return "already_exists"
	case errors.Is(err, vfspkg.ErrConflict):
		return "conflict"
	case errors.Is(err, vfspkg.ErrUnsupported):
		return "unsupported"
	case errors.Is(err, vfspkg.ErrInvalidPath), errors.Is(err, vfspkg.ErrInvalidRange):
		return "invalid"
	case errors.Is(err, vfspkg.ErrIsDirectory):
		return "is_directory"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	default:
		return "error"
	}
}

type countingReader struct {
	reader io.Reader
	bytes  int64
}

func (r *countingReader) Read(buffer []byte) (int, error) {
	count, err := r.reader.Read(buffer)
	r.bytes += int64(count)
	return count, err
}

type measuredReadCloser struct {
	next     io.ReadCloser
	addBytes func(int)
	finish   func(string)
	once     sync.Once
}

func newMeasuredReadCloser(next io.ReadCloser, addBytes func(int), finish func(string)) io.ReadCloser {
	if next == nil {
		return nil
	}
	return &measuredReadCloser{next: next, addBytes: addBytes, finish: finish}
}

func (r *measuredReadCloser) Read(buffer []byte) (int, error) {
	count, err := r.next.Read(buffer)
	if count > 0 && r.addBytes != nil {
		r.addBytes(count)
	}
	if err != nil {
		result := "error"
		if errors.Is(err, io.EOF) {
			result = "complete"
		}
		r.complete(result)
	}
	return count, err
}

func (r *measuredReadCloser) Close() error {
	err := r.next.Close()
	if err != nil {
		r.complete("error")
	} else {
		r.complete("closed")
	}
	return err
}

func (r *measuredReadCloser) complete(result string) {
	r.once.Do(func() {
		if r.finish != nil {
			r.finish(result)
		}
	})
}
func (m *measuredVFS) Close() error {
	if closer, ok := m.next.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}

type measuredCluster struct {
	next    cluster.Transport
	metrics *metricspkg.Module
	eventMu sync.RWMutex
	events  map[cluster.Event]struct{}
}

func (m *measuredCluster) NodeID() string { return m.next.NodeID() }
func (m *measuredCluster) Start(ctx context.Context) error {
	started := time.Now()
	err := m.next.Start(ctx)
	m.observe("start", "", err, started)
	return err
}
func (m *measuredCluster) Stop(ctx context.Context) error {
	started := time.Now()
	err := m.next.Stop(ctx)
	m.observe("stop", "", err, started)
	return err
}
func (m *measuredCluster) Ping(ctx context.Context) error {
	started := time.Now()
	err := m.next.Ping(ctx)
	m.observe("ping", "", err, started)
	return err
}
func (m *measuredCluster) RegisterHandler(event cluster.Event, handler cluster.Handler) error {
	started := time.Now()
	if handler == nil {
		err := m.next.RegisterHandler(event, nil)
		m.observe("register", event, err, started)
		return err
	}
	wrapped := func(ctx context.Context, message *cluster.Message) error {
		started := time.Now()
		err := handler(ctx, message)
		m.observe("receive", event, err, started)
		return err
	}
	err := m.next.RegisterHandler(event, wrapped)
	if err == nil {
		m.eventMu.Lock()
		if m.events == nil {
			m.events = make(map[cluster.Event]struct{})
		}
		m.events[event] = struct{}{}
		m.eventMu.Unlock()
	}
	m.observe("register", event, err, started)
	return err
}
func (m *measuredCluster) Broadcast(ctx context.Context, message *cluster.Message) error {
	started := time.Now()
	var event cluster.Event
	if message != nil {
		event = message.Event
	}
	err := m.next.Broadcast(ctx, message)
	m.observe("broadcast", event, err, started)
	return err
}
func (m *measuredCluster) SendToNode(ctx context.Context, nodeID string, message *cluster.Message) error {
	started := time.Now()
	var event cluster.Event
	if message != nil {
		event = message.Event
	}
	err := m.next.SendToNode(ctx, nodeID, message)
	m.observe("send", event, err, started)
	return err
}
func (m *measuredCluster) observe(operation string, event cluster.Event, err error, started time.Time) {
	m.metrics.ObserveCluster(operation, m.metricEvent(event), err, time.Since(started))
	if peers, ok := m.next.(interface{ PeerCount() int }); ok {
		m.metrics.SetClusterPeers(peers.PeerCount())
	}
}

func (m *measuredCluster) metricEvent(event cluster.Event) string {
	if event == "" {
		return "none"
	}
	m.eventMu.RLock()
	_, registered := m.events[event]
	m.eventMu.RUnlock()
	if !registered {
		return "unregistered"
	}
	return string(event)
}

type measuredExecutionHosts struct {
	next    executionHostDirectory
	metrics *metricspkg.Module
}

func (m *measuredExecutionHosts) Catalog(ctx context.Context) ([]appexecution.HostStatus, error) {
	started := time.Now()
	value, err := m.next.Catalog(ctx)
	m.metrics.ObserveExecutionHost("catalog", err, time.Since(started))
	if err == nil {
		m.setSnapshot(value)
	}
	return value, err
}

func (m *measuredExecutionHosts) setSnapshot(value []appexecution.HostStatus) {
	states := map[string]int{}
	slots := map[string]int{}
	for _, host := range value {
		if host.Usable {
			states["usable"]++
			slots["usable"] += max(host.Slots, 0)
		} else {
			states["unusable"]++
			slots["unusable"] += max(host.Slots, 0)
		}
		if host.Isolated {
			states["isolated"]++
		} else {
			states["unisolated"]++
		}
		if host.Freeze {
			states["freeze_capable"]++
		} else {
			states["freeze_incapable"]++
		}
	}
	m.metrics.SetExecutionHostSnapshot(states, slots)
}
func (m *measuredExecutionHosts) Ensure(ctx context.Context, hostID string, spec appexecution.Spec) (appexecution.Environment, error) {
	started := time.Now()
	value, err := m.next.Ensure(ctx, hostID, spec)
	m.metrics.ObserveExecutionHost("ensure", err, time.Since(started))
	if err != nil {
		return nil, err
	}
	if value == nil {
		return nil, nil
	}
	return &measuredEnvironment{next: value, metrics: m.metrics}, nil
}
func (m *measuredExecutionHosts) Revoke(ctx context.Context, hostID, grantID string) error {
	started := time.Now()
	err := m.next.Revoke(ctx, hostID, grantID)
	m.metrics.ObserveExecutionHost("revoke", err, time.Since(started))
	return err
}
func (m *measuredExecutionHosts) Check(ctx context.Context) error {
	started := time.Now()
	if checker, ok := m.next.(interface {
		CheckCatalog(context.Context) ([]appexecution.HostStatus, error)
	}); ok {
		value, err := checker.CheckCatalog(ctx)
		m.metrics.ObserveExecutionHost("check", err, time.Since(started))
		if value != nil {
			m.setSnapshot(value)
		}
		return err
	}
	err := m.next.Check(ctx)
	m.metrics.ObserveExecutionHost("check", err, time.Since(started))
	if err == nil {
		// Test overrides may implement only the public lifecycle contract. Keep
		// them observable at the cost of a second probe; production implements
		// CheckCatalog and always uses one catalog operation.
		_, err = m.Catalog(ctx)
	}
	return err
}
func (m *measuredExecutionHosts) Close() error { return m.next.Close() }

type measuredEnvironment struct {
	next    appexecution.Environment
	metrics *metricspkg.Module
}

func (m *measuredEnvironment) observe(operation string, started time.Time, err error) {
	m.metrics.ObserveExecutionHost(operation, err, time.Since(started))
}
func (m *measuredEnvironment) ReplaceTree(ctx context.Context, tree appexecution.Tree) error {
	started := time.Now()
	err := m.next.ReplaceTree(ctx, tree)
	m.observe("replace_tree", started, err)
	return err
}
func (m *measuredEnvironment) Apply(ctx context.Context, mutations []appexecution.Mutation) error {
	started := time.Now()
	err := m.next.Apply(ctx, mutations)
	m.observe("apply", started, err)
	return err
}
func (m *measuredEnvironment) Watch(ctx context.Context, cursor appexecution.Cursor) (appexecution.Observation, error) {
	started := time.Now()
	value, err := m.next.Watch(ctx, cursor)
	m.observe("watch", started, err)
	if err == nil && value != nil {
		value = &measuredObservation{next: value, metrics: m.metrics}
	}
	return value, err
}
func (m *measuredEnvironment) Open(ctx context.Context, path string) (io.ReadCloser, error) {
	started := time.Now()
	value, err := m.next.Open(ctx, path)
	m.observe("open", started, err)
	if err == nil && value != nil {
		value = newMeasuredReadCloser(value, func(bytes int) {
			m.metrics.ObserveExecutionStream("file", "read", "success", bytes)
		}, func(result string) {
			m.metrics.ObserveExecutionStream("file", "close", result, 0)
		})
	}
	return value, err
}
func (m *measuredEnvironment) Attach(ctx context.Context, window appexecution.Window) (appexecution.Terminal, error) {
	started := time.Now()
	value, err := m.next.Attach(ctx, window)
	m.observe("attach", started, err)
	if err == nil && value != nil {
		value = &measuredTerminal{next: value, metrics: m.metrics}
	}
	return value, err
}
func (m *measuredEnvironment) Freeze(ctx context.Context) error {
	started := time.Now()
	err := m.next.Freeze(ctx)
	m.observe("freeze", started, err)
	return err
}
func (m *measuredEnvironment) Thaw(ctx context.Context) error {
	started := time.Now()
	err := m.next.Thaw(ctx)
	m.observe("thaw", started, err)
	return err
}

type measuredObservation struct {
	next    appexecution.Observation
	metrics *metricspkg.Module
}

func (m *measuredObservation) Cursor() appexecution.Cursor { return m.next.Cursor() }
func (m *measuredObservation) Next(ctx context.Context) (appexecution.Event, error) {
	value, err := m.next.Next(ctx)
	result := "success"
	if err != nil {
		result = "error"
		if errors.Is(err, io.EOF) {
			result = "complete"
		} else if errors.Is(err, context.Canceled) {
			result = "canceled"
		} else if errors.Is(err, appexecution.ErrObservationLost) {
			result = "lost"
		}
	}
	m.metrics.ObserveExecutionStream("observation", "next", result, 0)
	return value, err
}
func (m *measuredObservation) Close() error {
	err := m.next.Close()
	m.metrics.ObserveExecutionStream("observation", "close", simpleOutcome(err), 0)
	return err
}

type measuredTerminal struct {
	next    appexecution.Terminal
	metrics *metricspkg.Module
}

func (m *measuredTerminal) Read(buffer []byte) (int, error) {
	count, err := m.next.Read(buffer)
	m.metrics.ObserveExecutionStream("terminal", "read", streamOutcome(err), count)
	return count, err
}
func (m *measuredTerminal) Write(buffer []byte) (int, error) {
	count, err := m.next.Write(buffer)
	m.metrics.ObserveExecutionStream("terminal", "write", streamOutcome(err), count)
	return count, err
}
func (m *measuredTerminal) Resize(ctx context.Context, window appexecution.Window) error {
	err := m.next.Resize(ctx, window)
	m.metrics.ObserveExecutionStream("terminal", "resize", simpleOutcome(err), 0)
	return err
}
func (m *measuredTerminal) Close() error {
	err := m.next.Close()
	m.metrics.ObserveExecutionStream("terminal", "close", simpleOutcome(err), 0)
	return err
}

func streamOutcome(err error) string {
	switch {
	case err == nil:
		return "success"
	case errors.Is(err, io.EOF):
		return "complete"
	case errors.Is(err, context.Canceled):
		return "canceled"
	default:
		return "error"
	}
}

func simpleOutcome(err error) string {
	if err == nil {
		return "success"
	}
	return "error"
}

var (
	_ platform.Cache           = (*measuredCache)(nil)
	_ platform.Mailer          = (*measuredMailer)(nil)
	_ vfspkg.FileSystem        = (*measuredVFS)(nil)
	_ cluster.Transport        = (*measuredCluster)(nil)
	_ executionHostDirectory   = (*measuredExecutionHosts)(nil)
	_ appexecution.Environment = (*measuredEnvironment)(nil)
	_ appexecution.Observation = (*measuredObservation)(nil)
	_ appexecution.Terminal    = (*measuredTerminal)(nil)
	_ jobengine.Recorder       = jobMetricsRecorder{}
)
