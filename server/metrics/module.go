// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package metrics owns the node-local Prometheus registry and scrape listener.
// Consumers receive narrow recorders; no application use case depends on this
// package or on Prometheus types.
package metrics

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/store/localcachelayer"
	"github.com/sudosylabs/proctor/server/store/timerlayer"
)

type BuildInfo struct {
	Version   string
	Commit    string
	GoVersion string
}

type LogStats struct {
	Dropped        uint64
	InternalErrors uint64
}

type Sources struct {
	Database func() sql.DBStats
	Logging  func() LogStats
	ErrorLog *log.Logger
}

// Module is one deep observability module: it owns registration, exposure,
// authentication, listener lifecycle, and all bounded metric instruments.
type Module struct {
	settings config.Metrics
	registry *prometheus.Registry
	handler  http.Handler
	errorLog *log.Logger

	ready atomic.Bool

	httpRequests            *prometheus.CounterVec
	httpDuration            *prometheus.HistogramVec
	httpInFlight            prometheus.Gauge
	httpRequestBytes        *prometheus.HistogramVec
	httpResponseBytes       *prometheus.HistogramVec
	storeDuration           *prometheus.HistogramVec
	storeCache              *prometheus.CounterVec
	storeRetries            *prometheus.CounterVec
	websocketConnections    prometheus.Gauge
	websocketTransitions    *prometheus.CounterVec
	websocketBackpressure   prometheus.Counter
	websocketMessages       *prometheus.CounterVec
	websocketMessageBytes   *prometheus.CounterVec
	websocketBroadcasts     *prometheus.CounterVec
	websocketFanout         prometheus.Histogram
	websocketReplays        *prometheus.CounterVec
	websocketReplayEvents   prometheus.Histogram
	websocketSubscriptions  prometheus.Gauge
	clusterOperations       *prometheus.CounterVec
	clusterDuration         *prometheus.HistogramVec
	clusterMessages         *prometheus.CounterVec
	clusterMessageBytes     *prometheus.CounterVec
	clusterMembership       *prometheus.CounterVec
	clusterDiscovery        *prometheus.CounterVec
	clusterAdmission        *prometheus.CounterVec
	clusterFanout           prometheus.Histogram
	clusterPeerCount        atomic.Int64
	clusterPeerMu           sync.RWMutex
	clusterPeerSource       func() int
	jobExecutions           *prometheus.CounterVec
	jobDuration             *prometheus.HistogramVec
	jobsActive              *prometheus.GaugeVec
	jobActivities           *prometheus.CounterVec
	jobActivityDuration     *prometheus.HistogramVec
	jobQueueLatency         *prometheus.HistogramVec
	executionHostOperations *prometheus.CounterVec
	executionHostDuration   *prometheus.HistogramVec
	executionHostState      *prometheus.GaugeVec
	executionHostSlots      *prometheus.GaugeVec
	executionStreams        *prometheus.CounterVec
	executionStreamBytes    *prometheus.CounterVec
	vfsOperations           *prometheus.CounterVec
	vfsDuration             *prometheus.HistogramVec
	vfsBytes                *prometheus.CounterVec
	vfsObjectSize           *prometheus.HistogramVec
	vfsListEntries          *prometheus.HistogramVec
	vfsStreams              *prometheus.CounterVec
	cacheOperations         *prometheus.CounterVec
	cacheDuration           *prometheus.HistogramVec
	cacheBytes              *prometheus.CounterVec
	redisOperations         *prometheus.CounterVec
	redisDuration           *prometheus.HistogramVec
	smtpOperations          *prometheus.CounterVec
	smtpDuration            *prometheus.HistogramVec
	smtpMessages            *prometheus.CounterVec
	smtpRecipients          *prometheus.CounterVec
	smtpBytes               *prometheus.CounterVec
	mailDeliveries          *prometheus.CounterVec
	mailAttempts            *prometheus.CounterVec
	mailProcessingLatency   *prometheus.HistogramVec
	mailQueueCount          *prometheus.GaugeVec
	mailQueueOldest         *prometheus.GaugeVec
	mailQueueTruncated      prometheus.Gauge
	mailHealth              *prometheus.GaugeVec
	applicationEvents       *prometheus.CounterVec

	mu        sync.Mutex
	server    *http.Server
	listener  net.Listener
	done      chan struct{}
	started   bool
	closed    bool
	failures  chan error
	closeOnce sync.Once
	closeDone chan struct{}
	closeErr  error
}

func New(settings config.Metrics, build BuildInfo, sources Sources) (*Module, error) {
	registry := prometheus.NewRegistry()
	module := &Module{settings: settings, registry: registry, errorLog: sources.ErrorLog, failures: make(chan error, 1), closeDone: make(chan struct{})}
	module.httpRequests = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "proctor", Subsystem: "http", Name: "requests_total", Help: "Completed public HTTP requests."}, []string{"route", "method", "status_class"})
	module.httpDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: "proctor", Subsystem: "http", Name: "request_duration_seconds", Help: "Public HTTP request duration.", Buckets: prometheus.DefBuckets}, []string{"route", "method"})
	module.httpInFlight = prometheus.NewGauge(prometheus.GaugeOpts{Namespace: "proctor", Subsystem: "http", Name: "in_flight_requests", Help: "Public HTTP requests currently executing."})
	module.httpRequestBytes = prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: "proctor", Subsystem: "http", Name: "request_size_bytes", Help: "Public HTTP request body bytes consumed by handlers.", Buckets: prometheus.ExponentialBuckets(256, 4, 9)}, []string{"route", "method"})
	module.httpResponseBytes = prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: "proctor", Subsystem: "http", Name: "response_size_bytes", Help: "Public HTTP response body size.", Buckets: prometheus.ExponentialBuckets(256, 4, 9)}, []string{"route", "method"})
	module.storeDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: "proctor", Subsystem: "store", Name: "operation_duration_seconds", Help: "Authoritative store operation duration.", Buckets: prometheus.DefBuckets}, []string{"operation", "outcome"})
	module.storeCache = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "proctor", Subsystem: "store_cache", Name: "requests_total", Help: "Process-local store cache decisions."}, []string{"operation", "outcome"})
	module.storeRetries = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "proctor", Subsystem: "store", Name: "retries_total", Help: "Aggregate authoritative-store retry decisions."}, []string{"outcome"})
	module.websocketConnections = prometheus.NewGauge(prometheus.GaugeOpts{Namespace: "proctor", Subsystem: "websocket", Name: "connections", Help: "WebSocket connections attached to this node."})
	module.websocketTransitions = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "proctor", Subsystem: "websocket", Name: "transitions_total", Help: "WebSocket connection transitions."}, []string{"transition", "outcome"})
	module.websocketBackpressure = prometheus.NewCounter(prometheus.CounterOpts{Namespace: "proctor", Subsystem: "websocket", Name: "backpressure_disconnects_total", Help: "WebSocket connections closed by outbound backpressure."})
	module.websocketMessages = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "proctor", Subsystem: "websocket", Name: "messages_total", Help: "WebSocket messages processed by direction, kind, and outcome."}, []string{"direction", "kind", "outcome"})
	module.websocketMessageBytes = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "proctor", Subsystem: "websocket", Name: "message_bytes_total", Help: "Approximate WebSocket payload bytes processed."}, []string{"direction", "kind"})
	module.websocketBroadcasts = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "proctor", Subsystem: "websocket", Name: "broadcasts_total", Help: "Local WebSocket publications by bounded event class and outcome."}, []string{"event", "outcome"})
	module.websocketFanout = prometheus.NewHistogram(prometheus.HistogramOpts{Namespace: "proctor", Subsystem: "websocket", Name: "broadcast_recipients", Help: "Connections selected by a local WebSocket publication.", Buckets: prometheus.ExponentialBuckets(1, 2, 12)})
	module.websocketReplays = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "proctor", Subsystem: "websocket", Name: "replays_total", Help: "WebSocket resume attempts by outcome."}, []string{"outcome"})
	module.websocketReplayEvents = prometheus.NewHistogram(prometheus.HistogramOpts{Namespace: "proctor", Subsystem: "websocket", Name: "replay_events", Help: "Events restored by successful WebSocket resumes.", Buckets: prometheus.ExponentialBuckets(1, 2, 8)})
	module.websocketSubscriptions = prometheus.NewGauge(prometheus.GaugeOpts{Namespace: "proctor", Subsystem: "websocket", Name: "subscriptions", Help: "Active WebSocket subscriptions on this node."})
	module.clusterOperations = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "proctor", Subsystem: "cluster", Name: "operations_total", Help: "Cluster transport operations."}, []string{"operation", "event", "outcome"})
	module.clusterDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: "proctor", Subsystem: "cluster", Name: "operation_duration_seconds", Help: "Cluster transport operation duration.", Buckets: prometheus.DefBuckets}, []string{"operation", "event"})
	module.clusterMessages = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "proctor", Subsystem: "cluster", Name: "messages_total", Help: "Cluster messages by direction, registered event, and outcome."}, []string{"direction", "event", "outcome"})
	module.clusterMessageBytes = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "proctor", Subsystem: "cluster", Name: "message_bytes_total", Help: "Cluster wire bytes by direction and registered event."}, []string{"direction", "event"})
	module.clusterMembership = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "proctor", Subsystem: "cluster", Name: "membership_events_total", Help: "Memberlist membership transitions."}, []string{"event"})
	module.clusterDiscovery = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "proctor", Subsystem: "cluster", Name: "discovery_operations_total", Help: "Cluster discovery and rejoin work by operation and outcome."}, []string{"operation", "outcome"})
	module.clusterAdmission = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "proctor", Subsystem: "cluster", Name: "admission_rejections_total", Help: "Rejected cluster peers by bounded reason."}, []string{"reason"})
	module.clusterFanout = prometheus.NewHistogram(prometheus.HistogramOpts{Namespace: "proctor", Subsystem: "cluster", Name: "broadcast_recipients", Help: "Peer nodes selected by cluster broadcasts.", Buckets: prometheus.ExponentialBuckets(1, 2, 10)})
	module.jobExecutions = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "proctor", Subsystem: "jobs", Name: "executions_total", Help: "Durable Job executions completed on this node."}, []string{"type", "outcome"})
	module.jobDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: "proctor", Subsystem: "jobs", Name: "execution_duration_seconds", Help: "Durable Job handler execution duration.", Buckets: prometheus.DefBuckets}, []string{"type", "outcome"})
	module.jobsActive = prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: "proctor", Subsystem: "jobs", Name: "active", Help: "Durable Job handlers currently executing."}, []string{"type"})
	module.jobActivities = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "proctor", Subsystem: "jobs", Name: "activities_total", Help: "Durable Job runtime, recurrence, and periodic-task activity."}, []string{"kind", "name", "operation", "outcome"})
	module.jobActivityDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: "proctor", Subsystem: "jobs", Name: "activity_duration_seconds", Help: "Durable Job runtime, recurrence, and periodic-task duration.", Buckets: prometheus.DefBuckets}, []string{"kind", "name", "operation"})
	module.jobQueueLatency = prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: "proctor", Subsystem: "jobs", Name: "queue_latency_seconds", Help: "Time a durable Job was available before being claimed.", Buckets: prometheus.ExponentialBuckets(0.25, 2, 16)}, []string{"type"})
	module.executionHostOperations = operationCounter("execution_host", "Execution-host operations.", []string{"operation", "outcome"})
	module.executionHostDuration = operationDuration("execution_host", "Execution-host operation duration.", []string{"operation"})
	module.executionHostState = prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: "proctor", Subsystem: "execution_host", Name: "hosts", Help: "Configured execution hosts by bounded state."}, []string{"state"})
	module.executionHostSlots = prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: "proctor", Subsystem: "execution_host", Name: "slots", Help: "Execution-host slots by bounded state."}, []string{"state"})
	module.executionStreams = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "proctor", Subsystem: "execution_host", Name: "stream_operations_total", Help: "Execution observation, file, and terminal stream operations."}, []string{"stream", "operation", "outcome"})
	module.executionStreamBytes = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "proctor", Subsystem: "execution_host", Name: "stream_bytes_total", Help: "Bytes transferred through execution file and terminal streams."}, []string{"stream", "direction"})
	module.vfsOperations = operationCounter("vfs", "VFS backend operations.", []string{"backend", "operation", "outcome"})
	module.vfsDuration = operationDuration("vfs", "VFS backend operation duration.", []string{"backend", "operation"})
	module.vfsBytes = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "proctor", Subsystem: "vfs", Name: "bytes_total", Help: "VFS payload bytes transferred."}, []string{"backend", "direction"})
	module.vfsObjectSize = prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: "proctor", Subsystem: "vfs", Name: "object_size_bytes", Help: "VFS object sizes returned by successful operations.", Buckets: prometheus.ExponentialBuckets(256, 4, 10)}, []string{"backend", "operation"})
	module.vfsListEntries = prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: "proctor", Subsystem: "vfs", Name: "list_entries", Help: "Entries returned by VFS list pages.", Buckets: prometheus.ExponentialBuckets(1, 2, 14)}, []string{"backend"})
	module.vfsStreams = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "proctor", Subsystem: "vfs", Name: "streams_total", Help: "VFS read streams by terminal outcome."}, []string{"backend", "outcome"})
	module.cacheOperations = operationCounter("cache", "Shared cache backend operations.", []string{"backend", "operation", "outcome"})
	module.cacheDuration = operationDuration("cache", "Shared cache backend operation duration.", []string{"backend", "operation"})
	module.cacheBytes = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "proctor", Subsystem: "cache", Name: "bytes_total", Help: "Shared-cache payload bytes transferred."}, []string{"backend", "direction"})
	module.redisOperations = operationCounter("redis", "Redis cache operations.", []string{"operation", "outcome"})
	module.redisDuration = operationDuration("redis", "Redis cache operation duration.", []string{"operation"})
	module.smtpOperations = operationCounter("smtp", "SMTP transport operations.", []string{"operation", "outcome"})
	module.smtpDuration = operationDuration("smtp", "SMTP transport operation duration.", []string{"operation"})
	module.smtpMessages = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "proctor", Subsystem: "smtp", Name: "messages_total", Help: "SMTP submission attempts by portable outcome."}, []string{"outcome"})
	module.smtpRecipients = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "proctor", Subsystem: "smtp", Name: "recipients_total", Help: "Aggregate SMTP envelope recipients by portable outcome."}, []string{"outcome"})
	module.smtpBytes = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "proctor", Subsystem: "smtp", Name: "message_bytes_total", Help: "Aggregate pre-composition SMTP message payload bytes by portable outcome."}, []string{"outcome"})
	module.mailDeliveries = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "proctor", Subsystem: "mail", Name: "deliveries_total", Help: "Durable mail delivery transitions by bounded template, state, and public outcome."}, []string{"template", "state", "outcome"})
	module.mailAttempts = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "proctor", Subsystem: "mail", Name: "attempts_total", Help: "Aggregate durable mail delivery attempts."}, []string{"template", "state"})
	module.mailProcessingLatency = prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: "proctor", Subsystem: "mail", Name: "processing_latency_seconds", Help: "Durable mail processing latency.", Buckets: prometheus.ExponentialBuckets(0.25, 2, 18)}, []string{"template", "state"})
	module.mailQueueCount = prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: "proctor", Subsystem: "mail", Name: "queue", Help: "Durable mail queue entries by template and state."}, []string{"template", "state"})
	module.mailQueueOldest = prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: "proctor", Subsystem: "mail", Name: "queue_oldest_age_seconds", Help: "Age of the oldest durable mail queue entry."}, []string{"template", "state"})
	module.mailQueueTruncated = prometheus.NewGauge(prometheus.GaugeOpts{Namespace: "proctor", Subsystem: "mail", Name: "queue_snapshot_truncated", Help: "Whether the current durable mail queue snapshot reached its row bound."})
	module.mailHealth = prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: "proctor", Subsystem: "mail", Name: "health", Help: "Current bounded mail health state (one active code has value 1)."}, []string{"code"})
	module.applicationEvents = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "proctor", Subsystem: "application", Name: "events_total", Help: "Named bounded application outcomes."}, []string{"subsystem", "event", "outcome"})
	registry.MustRegister(
		collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{Namespace: "proctor", Name: "ready", Help: "Whether this node is ready to serve public traffic."}, func() float64 {
			if module.ready.Load() {
				return 1
			}
			return 0
		}),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{Namespace: "proctor", Name: "build_info", Help: "Build information.", ConstLabels: prometheus.Labels{"version": build.Version, "commit": build.Commit, "go_version": build.GoVersion}}, func() float64 { return 1 }),
		module.httpRequests, module.httpDuration, module.httpInFlight, module.httpRequestBytes, module.httpResponseBytes,
		module.storeDuration, module.storeCache, module.storeRetries,
		module.websocketConnections, module.websocketTransitions, module.websocketBackpressure,
		module.websocketMessages, module.websocketMessageBytes, module.websocketBroadcasts, module.websocketFanout,
		module.websocketReplays, module.websocketReplayEvents, module.websocketSubscriptions,
		module.clusterOperations, module.clusterDuration, module.clusterMessages, module.clusterMessageBytes,
		module.clusterMembership, module.clusterDiscovery, module.clusterAdmission, module.clusterFanout,
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{Namespace: "proctor", Subsystem: "cluster", Name: "peers", Help: "Known peer nodes, excluding this node."}, module.currentClusterPeers),
		module.jobExecutions, module.jobDuration, module.jobsActive, module.jobActivities, module.jobActivityDuration, module.jobQueueLatency,
		module.executionHostOperations, module.executionHostDuration, module.executionHostState, module.executionHostSlots,
		module.executionStreams, module.executionStreamBytes,
		module.vfsOperations, module.vfsDuration, module.vfsBytes, module.vfsObjectSize, module.vfsListEntries, module.vfsStreams,
		module.cacheOperations, module.cacheDuration, module.cacheBytes,
		module.redisOperations, module.redisDuration,
		module.smtpOperations, module.smtpDuration, module.smtpMessages, module.smtpRecipients, module.smtpBytes,
		module.mailDeliveries, module.mailAttempts, module.mailProcessingLatency, module.mailQueueCount, module.mailQueueOldest, module.mailQueueTruncated, module.mailHealth,
		module.applicationEvents,
	)
	if sources.Database != nil {
		registerDatabaseCollectors(registry, sources.Database)
	}
	if sources.Logging != nil {
		registry.MustRegister(
			prometheus.NewCounterFunc(prometheus.CounterOpts{Namespace: "proctor", Subsystem: "logging", Name: "dropped_records_total", Help: "Records dropped by bounded logging queues."}, func() float64 { return float64(sources.Logging().Dropped) }),
			prometheus.NewCounterFunc(prometheus.CounterOpts{Namespace: "proctor", Subsystem: "logging", Name: "internal_errors_total", Help: "Logging pipeline internal errors."}, func() float64 { return float64(sources.Logging().InternalErrors) }),
		)
	}
	scrapeHandler := promhttp.HandlerFor(registry, promhttp.HandlerOpts{
		ErrorLog: sources.ErrorLog, MaxRequestsInFlight: settings.MaximumConcurrentScrapes,
	})
	module.handler = module.authorize(promhttp.InstrumentMetricHandler(registry, scrapeHandler))
	return module, nil
}

func (m *Module) Enabled() bool { return m != nil && m.settings.Enabled }

func operationCounter(subsystem, help string, labels []string) *prometheus.CounterVec {
	return prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "proctor", Subsystem: subsystem, Name: "operations_total", Help: help}, labels)
}

func operationDuration(subsystem, help string, labels []string) *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: "proctor", Subsystem: subsystem, Name: "operation_duration_seconds", Help: help, Buckets: prometheus.DefBuckets}, labels)
}

func registerDatabaseCollectors(registry *prometheus.Registry, stats func() sql.DBStats) {
	values := []struct {
		name, help string
		value      func(sql.DBStats) float64
	}{
		{"open_connections", "Open database connections.", func(s sql.DBStats) float64 { return float64(s.OpenConnections) }},
		{"max_open_connections", "Configured maximum open database connections.", func(s sql.DBStats) float64 { return float64(s.MaxOpenConnections) }},
		{"in_use_connections", "Database connections currently in use.", func(s sql.DBStats) float64 { return float64(s.InUse) }},
		{"idle_connections", "Idle database connections.", func(s sql.DBStats) float64 { return float64(s.Idle) }},
	}
	registry.MustRegister(
		prometheus.NewCounterFunc(prometheus.CounterOpts{Namespace: "proctor", Subsystem: "database", Name: "waits_total", Help: "Database connection waits."}, func() float64 { return float64(stats().WaitCount) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{Namespace: "proctor", Subsystem: "database", Name: "wait_duration_seconds_total", Help: "Time spent waiting for database connections."}, func() float64 { return stats().WaitDuration.Seconds() }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{Namespace: "proctor", Subsystem: "database", Name: "max_idle_closed_total", Help: "Connections closed due to the idle pool limit."}, func() float64 { return float64(stats().MaxIdleClosed) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{Namespace: "proctor", Subsystem: "database", Name: "max_idle_time_closed_total", Help: "Connections closed due to the idle timeout."}, func() float64 { return float64(stats().MaxIdleTimeClosed) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{Namespace: "proctor", Subsystem: "database", Name: "max_lifetime_closed_total", Help: "Connections closed due to the lifetime limit."}, func() float64 { return float64(stats().MaxLifetimeClosed) }),
	)
	for _, item := range values {
		item := item
		registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Namespace: "proctor", Subsystem: "database", Name: item.name, Help: item.help}, func() float64 { return item.value(stats()) }))
	}
}

func (m *Module) authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		if request.URL.Path != "/metrics" || request.Method != http.MethodGet {
			http.NotFound(writer, request)
			return
		}
		if m.settings.BearerToken != "" {
			got := request.Header.Get("Authorization")
			want := "Bearer " + m.settings.BearerToken
			gotDigest := sha256.Sum256([]byte(got))
			wantDigest := sha256.Sum256([]byte(want))
			if subtle.ConstantTimeCompare(gotDigest[:], wantDigest[:]) != 1 {
				writer.Header().Set("WWW-Authenticate", "Bearer")
				http.Error(writer, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		next.ServeHTTP(writer, request)
	})
}

func (m *Module) Start(ctx context.Context) error {
	if m == nil || !m.settings.Enabled {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("metrics module is closed")
	}
	if m.started {
		return errors.New("metrics module is already started")
	}
	listener, err := net.Listen("tcp", m.settings.ListenAddress)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", m.settings.ListenAddress, err)
	}
	if err := ctx.Err(); err != nil {
		_ = listener.Close()
		return err
	}
	if m.settings.TLS.CertificateFile != "" {
		certificate, loadErr := tls.LoadX509KeyPair(m.settings.TLS.CertificateFile, m.settings.TLS.PrivateKeyFile)
		if loadErr != nil {
			_ = listener.Close()
			return fmt.Errorf("load metrics TLS identity: %w", loadErr)
		}
		listener = tls.NewListener(listener, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
	}
	server := &http.Server{Handler: m.handler, ErrorLog: m.errorLog, ReadHeaderTimeout: m.settings.ReadHeaderTimeout.Duration, ReadTimeout: m.settings.ReadTimeout.Duration, WriteTimeout: m.settings.WriteTimeout.Duration, IdleTimeout: m.settings.IdleTimeout.Duration, MaxHeaderBytes: 16 << 10}
	done := make(chan struct{})
	m.listener, m.server, m.done, m.started = listener, server, done, true
	go func() {
		defer close(done)
		serveErr := server.Serve(listener)
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) && !errors.Is(serveErr, net.ErrClosed) {
			select {
			case m.failures <- serveErr:
			default:
			}
		}
	}()
	return nil
}

func (m *Module) Failures() <-chan error {
	if m == nil {
		return nil
	}
	return m.failures
}

func (m *Module) SetReady(ready bool) {
	if m != nil {
		m.ready.Store(ready)
	}
}

func (m *Module) Close() error {
	if m == nil {
		return nil
	}
	m.closeOnce.Do(func() {
		m.closeErr = m.close()
		close(m.closeDone)
	})
	<-m.closeDone
	return m.closeErr
}

func (m *Module) close() error {
	m.mu.Lock()
	m.closed = true
	server, listener, done := m.server, m.listener, m.done
	m.mu.Unlock()
	m.ready.Store(false)
	if server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), m.settings.ShutdownTimeout.Duration)
	defer cancel()
	shutdownErr := server.Shutdown(ctx)
	var forceErr error
	if shutdownErr != nil {
		forceErr = server.Close()
	}
	var closeErr error
	if listener != nil {
		closeErr = listener.Close()
	}
	if errors.Is(closeErr, net.ErrClosed) {
		closeErr = nil
	}
	if done != nil {
		<-done
	}
	return errors.Join(shutdownErr, forceErr, closeErr)
}

func outcome(err error) string {
	if err != nil {
		return "error"
	}
	return "success"
}

func (m *Module) ObserveHTTPRequest(route, method string, status int, duration time.Duration) {
	if m == nil {
		return
	}
	m.httpRequests.WithLabelValues(route, method, strconv.Itoa(status/100)+"xx").Inc()
	m.httpDuration.WithLabelValues(route, method).Observe(duration.Seconds())
}
func (m *Module) ObserveHTTPPayload(route, method string, requestBytes, responseBytes int64) {
	if m == nil {
		return
	}
	m.httpRequestBytes.WithLabelValues(route, method).Observe(float64(max(requestBytes, 0)))
	m.httpResponseBytes.WithLabelValues(route, method).Observe(float64(max(responseBytes, 0)))
}
func (m *Module) HTTPStarted() func() {
	if m == nil {
		return func() {}
	}
	m.httpInFlight.Inc()
	return m.httpInFlight.Dec
}
func (m *Module) Observe(operation timerlayer.Operation, result timerlayer.Outcome, duration time.Duration) {
	m.storeDuration.WithLabelValues(operation.String(), string(result)).Observe(duration.Seconds())
}
func (m *Module) ObserveStoreCache(operation localcachelayer.Operation, result localcachelayer.Outcome) {
	m.storeCache.WithLabelValues(operation.String(), string(result)).Inc()
}
func (m *Module) ObserveStoreRetry(result string) {
	if m != nil {
		m.storeRetries.WithLabelValues(result).Inc()
	}
}
func (m *Module) WebSocketOpened(outcome string) {
	m.websocketTransitions.WithLabelValues("open", outcome).Inc()
	if outcome == "success" {
		m.websocketConnections.Inc()
	}
}
func (m *Module) WebSocketClosed(outcome string, backpressure bool) {
	m.websocketTransitions.WithLabelValues("close", outcome).Inc()
	m.websocketConnections.Dec()
	if backpressure {
		m.websocketBackpressure.Inc()
	}
}
func (m *Module) ConnectionOpened(result string) { m.WebSocketOpened(result) }
func (m *Module) ConnectionClosed(result string) { m.WebSocketClosed(result, false) }
func (m *Module) Backpressure() {
	if m != nil {
		m.websocketBackpressure.Inc()
	}
}
func (m *Module) ObserveWebSocketMessage(direction, kind, result string, bytes int) {
	if m == nil {
		return
	}
	m.websocketMessages.WithLabelValues(direction, kind, result).Inc()
	if bytes > 0 {
		m.websocketMessageBytes.WithLabelValues(direction, kind).Add(float64(bytes))
	}
}
func (m *Module) ObserveWebSocketBroadcast(event, result string, recipients int) {
	if m == nil {
		return
	}
	m.websocketBroadcasts.WithLabelValues(event, result).Inc()
	m.websocketFanout.Observe(float64(max(recipients, 0)))
}
func (m *Module) ObserveWebSocketReplay(result string, events int) {
	if m == nil {
		return
	}
	m.websocketReplays.WithLabelValues(result).Inc()
	if result == "resumed" {
		m.websocketReplayEvents.Observe(float64(max(events, 0)))
	}
}
func (m *Module) AddWebSocketSubscriptions(delta int) {
	if m != nil && delta != 0 {
		m.websocketSubscriptions.Add(float64(delta))
	}
}
func (m *Module) ObserveCluster(operation, event string, err error, duration time.Duration) {
	m.clusterOperations.WithLabelValues(operation, event, outcome(err)).Inc()
	m.clusterDuration.WithLabelValues(operation, event).Observe(duration.Seconds())
}
func (m *Module) ObserveClusterMessage(direction, event, result string, bytes int) {
	if m == nil {
		return
	}
	m.clusterMessages.WithLabelValues(direction, event, result).Inc()
	if bytes > 0 {
		m.clusterMessageBytes.WithLabelValues(direction, event).Add(float64(bytes))
	}
}
func (m *Module) ObserveClusterMembership(event string) {
	if m != nil {
		m.clusterMembership.WithLabelValues(event).Inc()
	}
}
func (m *Module) ObserveClusterDiscovery(operation, result string) {
	if m != nil {
		m.clusterDiscovery.WithLabelValues(operation, result).Inc()
	}
}
func (m *Module) ObserveClusterAdmission(reason string) {
	if m != nil {
		m.clusterAdmission.WithLabelValues(reason).Inc()
	}
}
func (m *Module) ObserveClusterFanout(recipients int) {
	if m != nil {
		m.clusterFanout.Observe(float64(max(recipients, 0)))
	}
}
func (m *Module) SetClusterPeers(peers int) { m.clusterPeerCount.Store(int64(max(peers, 0))) }
func (m *Module) SetClusterPeerSource(source func() int) {
	m.clusterPeerMu.Lock()
	m.clusterPeerSource = source
	m.clusterPeerMu.Unlock()
}
func (m *Module) currentClusterPeers() float64 {
	m.clusterPeerMu.RLock()
	source := m.clusterPeerSource
	m.clusterPeerMu.RUnlock()
	if source != nil {
		return float64(max(source(), 0))
	}
	return float64(m.clusterPeerCount.Load())
}
func (m *Module) JobStarted(jobType string) { m.jobsActive.WithLabelValues(jobType).Inc() }
func (m *Module) JobFinished(jobType, result string, duration time.Duration) {
	m.jobsActive.WithLabelValues(jobType).Dec()
	m.jobExecutions.WithLabelValues(jobType, result).Inc()
	m.jobDuration.WithLabelValues(jobType, result).Observe(duration.Seconds())
}
func (m *Module) ObserveJobActivity(kind, name, operation, result string, duration, queueLatency time.Duration) {
	if m == nil {
		return
	}
	m.jobActivities.WithLabelValues(kind, name, operation, result).Inc()
	if duration >= 0 {
		m.jobActivityDuration.WithLabelValues(kind, name, operation).Observe(duration.Seconds())
	}
	if kind == "job" && operation == "claim" && result == "success" && queueLatency >= 0 {
		m.jobQueueLatency.WithLabelValues(name).Observe(queueLatency.Seconds())
	}
}
func (m *Module) ObserveExecutionHost(operation string, err error, duration time.Duration) {
	m.executionHostOperations.WithLabelValues(operation, outcome(err)).Inc()
	m.executionHostDuration.WithLabelValues(operation).Observe(duration.Seconds())
}
func (m *Module) SetExecutionHostSnapshot(states map[string]int, slots map[string]int) {
	if m == nil {
		return
	}
	for _, state := range []string{"usable", "unusable", "isolated", "unisolated", "freeze_capable", "freeze_incapable"} {
		m.executionHostState.WithLabelValues(state).Set(float64(max(states[state], 0)))
	}
	for _, state := range []string{"usable", "unusable"} {
		m.executionHostSlots.WithLabelValues(state).Set(float64(max(slots[state], 0)))
	}
}
func (m *Module) ObserveExecutionStream(stream, operation, result string, bytes int) {
	if m == nil {
		return
	}
	m.executionStreams.WithLabelValues(stream, operation, result).Inc()
	direction := "read"
	if operation == "write" {
		direction = "write"
	}
	if bytes > 0 && (operation == "read" || operation == "write") {
		m.executionStreamBytes.WithLabelValues(stream, direction).Add(float64(bytes))
	}
}
func (m *Module) ObserveVFS(backend, operation string, err error, duration time.Duration) {
	m.ObserveVFSResult(backend, operation, outcome(err), duration)
}
func (m *Module) ObserveVFSResult(backend, operation, result string, duration time.Duration) {
	m.vfsOperations.WithLabelValues(backend, operation, result).Inc()
	m.vfsDuration.WithLabelValues(backend, operation).Observe(duration.Seconds())
}
func (m *Module) ObserveVFSObject(backend, operation string, size int64) {
	if m != nil && size >= 0 {
		m.vfsObjectSize.WithLabelValues(backend, operation).Observe(float64(size))
	}
}
func (m *Module) ObserveVFSList(backend string, entries int) {
	if m != nil {
		m.vfsListEntries.WithLabelValues(backend).Observe(float64(max(entries, 0)))
	}
}
func (m *Module) AddVFSBytes(backend, direction string, bytes int64) {
	if m != nil && bytes > 0 {
		m.vfsBytes.WithLabelValues(backend, direction).Add(float64(bytes))
	}
}
func (m *Module) ObserveVFSStream(backend, result string) {
	if m != nil {
		m.vfsStreams.WithLabelValues(backend, result).Inc()
	}
}
func (m *Module) ObserveCache(backend, operation string, err error, duration time.Duration) {
	m.ObserveCacheResult(backend, operation, outcome(err), duration)
}
func (m *Module) ObserveCacheResult(backend, operation, result string, duration time.Duration) {
	m.cacheOperations.WithLabelValues(backend, operation, result).Inc()
	m.cacheDuration.WithLabelValues(backend, operation).Observe(duration.Seconds())
	if backend == "redis" {
		m.redisOperations.WithLabelValues(operation, result).Inc()
		m.redisDuration.WithLabelValues(operation).Observe(duration.Seconds())
	}
}
func (m *Module) AddCacheBytes(backend, direction string, bytes int) {
	if m != nil && bytes > 0 {
		m.cacheBytes.WithLabelValues(backend, direction).Add(float64(bytes))
	}
}
func (m *Module) ObserveSMTP(operation string, err error, duration time.Duration) {
	m.smtpOperations.WithLabelValues(operation, outcome(err)).Inc()
	m.smtpDuration.WithLabelValues(operation).Observe(duration.Seconds())
}
func (m *Module) ObserveSMTPMessage(result string, recipients, bytes int) {
	if m == nil {
		return
	}
	m.smtpMessages.WithLabelValues(result).Inc()
	if recipients > 0 {
		m.smtpRecipients.WithLabelValues(result).Add(float64(recipients))
	}
	if bytes > 0 {
		m.smtpBytes.WithLabelValues(result).Add(float64(bytes))
	}
}
func (m *Module) ObserveMailDelivery(template, state, result string, processingLatency time.Duration) {
	if m == nil {
		return
	}
	m.mailDeliveries.WithLabelValues(template, state, result).Inc()
	if processingLatency >= 0 {
		m.mailProcessingLatency.WithLabelValues(template, state).Observe(processingLatency.Seconds())
	}
}
func (m *Module) ObserveMailAttempt(template, state string, attemptDelta int) {
	if m != nil && attemptDelta > 0 {
		m.mailAttempts.WithLabelValues(template, state).Add(float64(attemptDelta))
	}
}
func (m *Module) SetMailQueue(template, state string, count int64, oldestAge time.Duration) {
	if m == nil {
		return
	}
	m.mailQueueCount.WithLabelValues(template, state).Set(float64(max(count, int64(0))))
	m.mailQueueOldest.WithLabelValues(template, state).Set(max(oldestAge.Seconds(), 0))
}
func (m *Module) SetMailQueueSnapshotTruncated(truncated bool) {
	if m == nil {
		return
	}
	if truncated {
		m.mailQueueTruncated.Set(1)
		return
	}
	m.mailQueueTruncated.Set(0)
}
func (m *Module) SetMailHealth(code string) {
	if m == nil {
		return
	}
	m.mailHealth.Reset()
	m.mailHealth.WithLabelValues(code).Set(1)
}
func (m *Module) ObserveApplication(subsystem, event, result string) {
	if m != nil {
		m.applicationEvents.WithLabelValues(subsystem, event, result).Inc()
	}
}

var _ timerlayer.Recorder = (*Module)(nil)

// StoreCacheRecorder projects only the existing cache-layer recorder seam.
func (m *Module) StoreCacheRecorder() localcachelayer.Recorder {
	return localcachelayer.RecorderFunc(m.ObserveStoreCache)
}
