// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package metrics

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/config"
)

func TestModuleServesAuthenticatedNodeMetricsWithoutDebugEndpoints(t *testing.T) {
	t.Parallel()

	settings := config.Default().Metrics
	settings.Enabled = true
	settings.ListenAddress = "127.0.0.1:0"
	settings.BearerToken = strings.Repeat("a", 32)
	module, err := New(settings, BuildInfo{Version: "test", Commit: "abc", GoVersion: "go-test"}, Sources{})
	if err != nil {
		t.Fatal(err)
	}
	module.ObserveCache("redis", "get", nil, time.Millisecond)
	module.ObserveVFS("s3", "open", context.DeadlineExceeded, time.Millisecond)
	module.ObserveSMTP("send", nil, time.Millisecond)
	module.ObserveHTTPRequest("/api/v1/discovery", http.MethodGet, http.StatusOK, time.Millisecond)
	module.ObserveHTTPPayload("/api/v1/discovery", http.MethodGet, 12, 24)
	module.ObserveCluster("broadcast", "realtime.event", nil, time.Millisecond)
	module.ObserveClusterMessage("send", "realtime.event", "success", 32)
	module.ObserveClusterMembership("join")
	module.ObserveClusterDiscovery("rejoin", "success")
	module.ObserveClusterAdmission("protocol_incompatible")
	module.ObserveClusterFanout(2)
	module.JobStarted("example_job")
	module.JobFinished("example_job", "succeeded", time.Millisecond)
	module.ObserveJobActivity("job", "example_job", "claim", "success", time.Millisecond, time.Second)
	module.ObserveExecutionHost("ensure", nil, time.Millisecond)
	module.SetExecutionHostSnapshot(map[string]int{"usable": 1, "isolated": 1, "thawed": 1}, map[string]int{"usable": 2})
	module.ObserveExecutionStream("terminal", "read", "success", 16)
	module.ObserveVFSObject("s3", "open", 64)
	module.ObserveVFSList("s3", 3)
	module.AddVFSBytes("s3", "read", 64)
	module.ObserveVFSStream("s3", "complete")
	module.AddCacheBytes("redis", "read", 12)
	module.ObserveSMTPMessage("accepted", 2, 128)
	module.ObserveMailDelivery("account_recovery", "sent", "accepted", time.Second)
	module.ObserveMailAttempt("account_recovery", "sending", 1)
	module.SetMailQueue("account_recovery", "queued", 2, time.Minute)
	module.SetMailQueueSnapshotTruncated(true)
	module.SetMailHealth("mail.healthy")
	module.ObserveApplication("authentication", "login", "success")
	module.ObserveStoreRetry("retry")
	module.ConnectionOpened("success")
	module.Backpressure()
	module.ObserveWebSocketMessage("outbound", "event", "success", 32)
	module.ObserveWebSocketBroadcast("exam.updated", "published", 2)
	module.ObserveWebSocketReplay("resumed", 3)
	module.AddWebSocketSubscriptions(1)
	module.ConnectionClosed("closed")
	module.SetReady(true)
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	response := httptest.NewRecorder()
	module.handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	request.Header.Set("Authorization", "Bearer "+settings.BearerToken)
	response = httptest.NewRecorder()
	module.handler.ServeHTTP(response, request)
	body, err := io.ReadAll(response.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK {
		t.Fatalf("authenticated status = %d: %s", response.Code, body)
	}
	for _, name := range []string{
		"proctor_build_info", "proctor_ready", "proctor_cache_operations_total",
		"proctor_redis_operations_total",
		"proctor_vfs_operations_total", "proctor_smtp_operations_total",
		"proctor_websocket_backpressure_disconnects_total",
		"proctor_http_requests_total", "proctor_cluster_operations_total",
		"proctor_jobs_executions_total", "proctor_execution_host_operations_total",
		"proctor_http_request_size_bytes", "proctor_http_response_size_bytes",
		"proctor_websocket_messages_total", "proctor_websocket_broadcasts_total",
		"proctor_websocket_replays_total", "proctor_websocket_subscriptions",
		"proctor_cluster_messages_total", "proctor_cluster_membership_events_total",
		"proctor_cluster_discovery_operations_total", "proctor_cluster_admission_rejections_total",
		"proctor_jobs_activities_total", "proctor_jobs_queue_latency_seconds",
		"proctor_execution_host_hosts", "proctor_execution_host_stream_operations_total",
		"proctor_vfs_bytes_total", "proctor_vfs_object_size_bytes", "proctor_vfs_streams_total",
		"proctor_cache_bytes_total", "proctor_smtp_messages_total", "proctor_mail_deliveries_total",
		"proctor_mail_queue", "proctor_mail_queue_snapshot_truncated", "proctor_mail_health", "proctor_application_events_total", "proctor_store_retries_total",
	} {
		if !strings.Contains(string(body), name) {
			t.Fatalf("scrape is missing %q", name)
		}
	}

	debugRequest := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
	debugRequest.Header.Set("Authorization", "Bearer "+settings.BearerToken)
	debugResponse := httptest.NewRecorder()
	module.handler.ServeHTTP(debugResponse, debugRequest)
	if debugResponse.Code != http.StatusNotFound {
		t.Fatalf("debug endpoint status = %d", debugResponse.Code)
	}
}

func TestDisabledModuleOwnsNoListener(t *testing.T) {
	t.Parallel()

	settings := config.Default().Metrics
	module, err := New(settings, BuildInfo{}, Sources{})
	if err != nil {
		t.Fatal(err)
	}
	if err := module.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if module.listener != nil {
		t.Fatal("disabled metrics module opened a listener")
	}
	if err := module.Close(); err != nil {
		t.Fatal(err)
	}
	if err := module.Close(); err != nil {
		t.Fatalf("repeated Close: %v", err)
	}
}
