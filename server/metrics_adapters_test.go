// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	mailpkg "github.com/sudosylabs/proctor/packages/mail"
	vfspkg "github.com/sudosylabs/proctor/packages/vfs"
	apppkg "github.com/sudosylabs/proctor/server/app"
	appexecution "github.com/sudosylabs/proctor/server/app/execution"
	"github.com/sudosylabs/proctor/server/cluster"
	"github.com/sudosylabs/proctor/server/config"
	metricspkg "github.com/sudosylabs/proctor/server/metrics"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/platform"
)

func TestMetricAdapterOutcomesStayBounded(t *testing.T) {
	t.Parallel()

	cacheCases := []struct {
		err  error
		want string
	}{{nil, "success"}, {platform.ErrCacheMiss, "miss"}, {platform.ErrCacheNotStored, "not_stored"}, {context.DeadlineExceeded, "timeout"}, {errors.New("private"), "error"}}
	for _, test := range cacheCases {
		if got := cacheOutcome(test.err); got != test.want {
			t.Fatalf("cacheOutcome(%v) = %q, want %q", test.err, got, test.want)
		}
	}
	vfsCases := []struct {
		err  error
		want string
	}{{nil, "success"}, {vfspkg.ErrNotFound, "not_found"}, {vfspkg.ErrConflict, "conflict"}, {vfspkg.ErrUnsupported, "unsupported"}, {errors.New("private"), "error"}}
	for _, test := range vfsCases {
		if got := vfsOutcome(test.err); got != test.want {
			t.Fatalf("vfsOutcome(%v) = %q, want %q", test.err, got, test.want)
		}
	}
	if got := smtpOutcome(mailpkg.WithOutcome(mailpkg.OutcomeAcceptanceUncertain, errors.New("private"))); got != "acceptance_uncertain" {
		t.Fatalf("smtpOutcome() = %q", got)
	}
}

func TestMeasuredClusterLabelsOnlyRegisteredEvents(t *testing.T) {
	t.Parallel()

	recorder := &measuredCluster{events: map[cluster.Event]struct{}{"registered.event": {}}}
	if got := recorder.metricEvent("registered.event"); got != "registered.event" {
		t.Fatalf("registered event metric label = %q", got)
	}
	if got := recorder.metricEvent("peer.supplied"); got != "unregistered" {
		t.Fatalf("unregistered event metric label = %q", got)
	}
	if got := recorder.metricEvent(""); got != "none" {
		t.Fatalf("empty event metric label = %q", got)
	}
}

func TestMailQueueMetricsAggregateFailureBucketsAndExposeTruncation(t *testing.T) {
	t.Parallel()

	values := []apppkg.MailQueueMetric{
		{TemplateKey: model.MailTemplateSystemTest, State: model.MailDeliveryQueued, OutcomeCode: "temporary", Count: 2, OldestAge: time.Minute},
		{TemplateKey: model.MailTemplateSystemTest, State: model.MailDeliveryQueued, OutcomeCode: "permanent", Count: 3, OldestAge: 2 * time.Minute, Truncated: true},
	}
	aggregates, truncated := aggregateMailQueueMetrics(values)
	aggregate := aggregates[mailQueueMetricIdentity{template: model.MailTemplateSystemTest, state: model.MailDeliveryQueued}]
	if aggregate.count != 5 || aggregate.oldestAge != 2*time.Minute || !truncated {
		t.Fatalf("aggregate = %#v truncated=%v", aggregate, truncated)
	}
}

type executionHostCheckCatalogFake struct {
	executionHostDirectory
	calls   int
	catalog []appexecution.HostStatus
}

func (f *executionHostCheckCatalogFake) Check(context.Context) error {
	return errors.New("legacy Check should not be called")
}

func (f *executionHostCheckCatalogFake) CheckCatalog(context.Context) ([]appexecution.HostStatus, error) {
	f.calls++
	return append([]appexecution.HostStatus(nil), f.catalog...), nil
}

func TestExecutionHostCheckPublishesTheCheckedCatalogWithoutASecondProbe(t *testing.T) {
	t.Parallel()

	module, err := metricspkg.New(config.Default().Metrics, metricspkg.BuildInfo{}, metricspkg.Sources{})
	if err != nil {
		t.Fatal(err)
	}
	next := &executionHostCheckCatalogFake{catalog: []appexecution.HostStatus{{Usable: true, Isolated: true, Freeze: true, Slots: 2}}}
	measured := &measuredExecutionHosts{next: next, metrics: module}
	if err := measured.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if next.calls != 1 {
		t.Fatalf("CheckCatalog() calls = %d, want 1", next.calls)
	}
}

func TestMeasuredReadCloserCountsBytesAndCompletesOnce(t *testing.T) {
	t.Parallel()

	var bytesRead int
	var outcomes []string
	reader := newMeasuredReadCloser(io.NopCloser(bytes.NewBufferString("payload")), func(count int) {
		bytesRead += count
	}, func(outcome string) {
		outcomes = append(outcomes, outcome)
	})
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if string(data) != "payload" || bytesRead != len("payload") {
		t.Fatalf("data=%q bytes=%d", data, bytesRead)
	}
	if len(outcomes) != 1 || outcomes[0] != "complete" {
		t.Fatalf("outcomes = %#v", outcomes)
	}
}
