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
	appmail "github.com/sudosylabs/proctor/server/app/mail"
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
		want appmail.TransportOutcome
	}{
		{name: "portable temporary", err: portableMailOutcomeError{outcome: "temporary"}, want: appmail.TransportTemporary},
		{name: "portable permanent", err: portableMailOutcomeError{outcome: "permanent"}, want: appmail.TransportPermanent},
		{name: "portable uncertain", err: portableMailOutcomeError{outcome: "acceptance_uncertain"}, want: appmail.TransportAcceptanceUncertain},
		{name: "legacy temporary", err: mailpkg.ErrConnection, want: appmail.TransportTemporary},
		{name: "legacy permanent", err: mailpkg.ErrRejected, want: appmail.TransportPermanent},
		{name: "unknown", err: errors.New("unknown"), want: appmail.TransportUnknown},
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
	recorder, reader := newMailTelemetry(nil, nil)
	operational, ok := reader.(*operationalMailTelemetry)
	if !ok {
		t.Fatalf("production metrics reader type = %T", reader)
	}
	if recorder != operational {
		t.Fatalf("production recorder type = %T, want shared operational telemetry", recorder)
	}
	ctx := context.Background()
	key := mailDeliveryMetricKey{template: model.MailTemplateSystemTest, state: model.MailDeliveryQueued, code: "mail.transport.temporary"}
	attemptKey := mailDeliveryMetricKey{template: model.MailTemplateSystemTest, state: model.MailDeliverySending, code: "mail.attempt_started"}
	for range 2 {
		recorder.RecordMailDelivery(ctx, app.MailDeliveryMetric{
			TemplateKey: key.template, State: key.state, OutcomeCode: key.code,
			ProcessingLatency: time.Second,
		})
		recorder.RecordMailAttempt(ctx, app.MailAttemptMetric{TemplateKey: attemptKey.template, State: attemptKey.state})
	}
	recorder.RecordMailQueueSnapshot(ctx, []app.MailQueueMetric{{
		TemplateKey: key.template, State: key.state, OutcomeCode: key.code,
		Count: 7, OldestAge: 6 * time.Minute, HealthCode: app.MailHealthQueueDelayed,
	}})
	recorder.RecordMailHealth(ctx, app.MailHealthMetric{Code: app.MailHealthQueueDelayed})
	snapshot := reader.Snapshot()

	operational.mu.Lock()
	aggregate := operational.deliveries[key]
	attemptAggregate := operational.deliveries[attemptKey]
	if aggregate.count != 2 || aggregate.attempts != 0 || aggregate.processingLatency != 2*time.Second || aggregate.maximumLatency != time.Second ||
		attemptAggregate.count != 0 || attemptAggregate.attempts != 2 ||
		operational.queues[key].Count != 7 ||
		operational.queueBuckets[key] != "lt_15m0s" || operational.health != app.MailHealthQueueDelayed ||
		len(snapshot.Deliveries) != 2 || len(snapshot.Queues) != 1 {
		t.Fatalf("operational telemetry = deliveries %#v queues %#v buckets %#v health %q",
			operational.deliveries, operational.queues, operational.queueBuckets, operational.health)
	}
	operational.mu.Unlock()
	recorder.RecordMailQueueSnapshot(ctx, nil)
	if len(reader.Snapshot().Queues) != 0 {
		t.Fatalf("drained queue remained in snapshot: %#v", reader.Snapshot().Queues)
	}
}
