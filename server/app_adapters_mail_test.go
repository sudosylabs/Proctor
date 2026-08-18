// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	mailpkg "github.com/sudosylabs/proctor/packages/mail"
	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type portableMailOutcomeError struct{ outcome string }

func (e portableMailOutcomeError) Error() string       { return "transport failed" }
func (e portableMailOutcomeError) MailOutcome() string { return e.outcome }

func TestAccountMailerAdapterClassifiesPortableAndLegacyFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want app.MailTransportOutcome
	}{
		{name: "portable temporary", err: portableMailOutcomeError{outcome: "temporary"}, want: app.MailTransportTemporary},
		{name: "portable permanent", err: portableMailOutcomeError{outcome: "permanent"}, want: app.MailTransportPermanent},
		{name: "portable uncertain", err: portableMailOutcomeError{outcome: "acceptance_uncertain"}, want: app.MailTransportAcceptanceUncertain},
		{name: "legacy temporary", err: mailpkg.ErrConnection, want: app.MailTransportTemporary},
		{name: "legacy permanent", err: mailpkg.ErrRejected, want: app.MailTransportPermanent},
		{name: "unknown", err: errors.New("unknown"), want: app.MailTransportUnknown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := classifyMailTransportError(fmt.Errorf("wrapped: %w", test.err)); got != test.want {
				t.Fatalf("classifyMailTransportError() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestProductionMailTelemetryRetainsBoundedSafeMetrics(t *testing.T) {
	t.Parallel()
	recorder, ok := newMailDeliveryRecorder(nil, nil).(*operationalMailTelemetry)
	if !ok {
		t.Fatalf("production recorder type = %T", recorder)
	}
	ctx := context.Background()
	key := mailDeliveryMetricKey{template: model.MailTemplateSystemTest, state: model.MailDeliveryQueued, code: "mail.transport.temporary"}
	recorder.RecordMailDelivery(ctx, app.MailDeliveryMetric{
		TemplateKey: key.template, State: key.state, OutcomeCode: key.code,
		AttemptCount: 2, ProcessingLatency: time.Second,
	})
	recorder.RecordMailQueueSnapshot(ctx, []app.MailQueueMetric{{
		TemplateKey: key.template, State: key.state, OutcomeCode: key.code,
		Count: 7, OldestAge: 6 * time.Minute, HealthCode: app.MailHealthQueueDelayed,
	}})
	recorder.RecordMailHealth(ctx, app.MailHealthMetric{Code: app.MailHealthQueueDelayed})
	snapshot := recorder.Snapshot()

	recorder.mu.Lock()
	aggregate := recorder.deliveries[key]
	if aggregate.count != 1 || aggregate.attempts != 2 || aggregate.processingLatency != time.Second || aggregate.maximumLatency != time.Second ||
		recorder.queues[key].Count != 7 ||
		recorder.queueBuckets[key] != "lt_15m0s" || recorder.health != app.MailHealthQueueDelayed ||
		len(snapshot.Deliveries) != 1 || snapshot.Deliveries[0].AttemptCount != 2 || len(snapshot.Queues) != 1 {
		t.Fatalf("operational telemetry = deliveries %#v queues %#v buckets %#v health %q",
			recorder.deliveries, recorder.queues, recorder.queueBuckets, recorder.health)
	}
	recorder.mu.Unlock()
	recorder.RecordMailQueueSnapshot(ctx, nil)
	if len(recorder.Snapshot().Queues) != 0 {
		t.Fatalf("drained queue remained in snapshot: %#v", recorder.Snapshot().Queues)
	}
}
