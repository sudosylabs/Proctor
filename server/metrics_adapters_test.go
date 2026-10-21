// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	mailpkg "github.com/sudosylabs/proctor/packages/mail"
	vfspkg "github.com/sudosylabs/proctor/packages/vfs"
	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/cluster"
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
		t.Run(test.want, func(t *testing.T) {
			require.Equal(t, test.want, cacheOutcome(test.err), "cacheOutcome(%v)", test.err)
		})
	}
	vfsCases := []struct {
		err  error
		want string
	}{{nil, "success"}, {vfspkg.ErrNotFound, "not_found"}, {vfspkg.ErrConflict, "conflict"}, {vfspkg.ErrUnsupported, "unsupported"}, {errors.New("private"), "error"}}
	for _, test := range vfsCases {
		t.Run(test.want, func(t *testing.T) {
			require.Equal(t, test.want, vfsOutcome(test.err), "vfsOutcome(%v)", test.err)
		})
	}
	require.Equal(t, "acceptance_uncertain", smtpOutcome(mailpkg.WithOutcome(mailpkg.OutcomeAcceptanceUncertain, errors.New("private"))))
}

func TestMeasuredClusterLabelsOnlyRegisteredEvents(t *testing.T) {
	t.Parallel()

	recorder := &measuredCluster{events: map[cluster.Event]struct{}{"registered.event": {}}}
	require.Equal(t, "registered.event", recorder.metricEvent("registered.event"))
	require.Equal(t, "unregistered", recorder.metricEvent("peer.supplied"))
	require.Equal(t, "none", recorder.metricEvent(""))
}

func TestMailQueueMetricsAggregateFailureBucketsAndExposeTruncation(t *testing.T) {
	t.Parallel()

	values := []app.MailQueueMetric{
		{TemplateKey: model.MailTemplateSystemTest, State: model.MailDeliveryQueued, OutcomeCode: "temporary", Count: 2, OldestAge: time.Minute},
		{TemplateKey: model.MailTemplateSystemTest, State: model.MailDeliveryQueued, OutcomeCode: "permanent", Count: 3, OldestAge: 2 * time.Minute, Truncated: true},
	}
	aggregates, truncated := aggregateMailQueueMetrics(values)
	aggregate := aggregates[mailQueueMetricIdentity{template: model.MailTemplateSystemTest, state: model.MailDeliveryQueued}]
	require.Equal(t, int64(5), aggregate.count, "aggregate = %#v truncated", aggregate)
	require.Equal(t, 2*time.Minute, aggregate.oldestAge, "aggregate = %#v truncated", aggregate)
	require.True(t, truncated, "aggregate = %#v truncated", aggregate)
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
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	require.Equal(t, "payload", string(data), "data")
	require.Len(t, "payload", bytesRead, "data")
	require.Len(t, outcomes, 1)
	require.Equal(t, "complete", outcomes[0])
}
