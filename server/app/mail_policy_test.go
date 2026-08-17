// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type mailMaintenanceFake struct {
	suppressed  int
	outstanding *store.MailMaintenanceResult
	expired     []*store.MailMaintenanceResult
	cleaned     []*store.MailMaintenanceResult
	snapshot    *store.MailQueueSnapshot
	calls       []string
}

func (f *mailMaintenanceFake) SuppressOutstanding(context.Context, string, int) (*store.MailMaintenanceResult, error) {
	f.suppressed++
	if f.outstanding != nil {
		return f.outstanding, nil
	}
	return &store.MailMaintenanceResult{Affected: 1}, nil
}
func (f *mailMaintenanceFake) QueueSnapshot(context.Context) (*store.MailQueueSnapshot, error) {
	return f.snapshot, nil
}
func (f *mailMaintenanceFake) SuppressExpired(context.Context, int) (*store.MailMaintenanceResult, error) {
	f.calls = append(f.calls, "suppress_expired")
	value := f.expired[0]
	f.expired = f.expired[1:]
	return value, nil
}
func (f *mailMaintenanceFake) CleanupTerminal(context.Context, int) (*store.MailMaintenanceResult, error) {
	f.calls = append(f.calls, "cleanup_terminal")
	value := f.cleaned[0]
	f.cleaned = f.cleaned[1:]
	return value, nil
}

type probingMailSender struct {
	mailSenderFake
	probeErr error
}

func (s *probingMailSender) Probe(context.Context) error { return s.probeErr }

type recordingMailMetrics struct {
	deliveries []MailDeliveryMetric
	queues     []MailQueueMetric
	health     []MailHealthMetric
}

func (r *recordingMailMetrics) RecordMailDelivery(_ context.Context, metric MailDeliveryMetric) {
	r.deliveries = append(r.deliveries, metric)
}
func (r *recordingMailMetrics) RecordMailQueueSnapshot(_ context.Context, metrics []MailQueueMetric) {
	r.queues = append(r.queues[:0], metrics...)
}
func (r *recordingMailMetrics) RecordMailHealth(_ context.Context, metric MailHealthMetric) {
	r.health = append(r.health, metric)
}
func (r *recordingMailMetrics) Snapshot() MailMetricsSnapshot { return MailMetricsSnapshot{} }

func TestDisabledMailMaintenanceSuppressesOutstandingWithoutAffectingReadiness(t *testing.T) {
	metrics := &recordingMailMetrics{}
	mail := &mailMaintenanceFake{outstanding: &store.MailMaintenanceResult{Affected: 1, Deliveries: []store.MailMaintenanceDelivery{{
		TemplateKey: model.MailTemplateSystemTest, State: model.MailDeliverySuppressed,
		PublicFailureCode: model.MailDeliveryDisabledCode, ProcessingLatency: time.Minute,
	}}}}
	health := newMailHealth(false)
	monitor := mailMaintenanceMonitor{mail: mail, sender: &mailSenderFake{}, health: health, recorder: metrics, now: time.Now}
	if err := monitor.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if mail.suppressed != 1 || health.Code() != MailHealthDisabled || health.Degraded() {
		t.Fatalf("suppressed=%d health=%s degraded=%t", mail.suppressed, health.Code(), health.Degraded())
	}
	if len(metrics.deliveries) != 1 || metrics.deliveries[0].OutcomeCode != model.MailDeliveryDisabledCode ||
		len(metrics.health) != 1 || metrics.health[0].Code != MailHealthDisabled {
		t.Fatalf("disabled metrics = deliveries %#v health %#v", metrics.deliveries, metrics.health)
	}
}

func TestMailMaintenanceReportsSMTPOutageAndQueueDelayAsSubsystemDegradation(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	mail := &mailMaintenanceFake{snapshot: &store.MailQueueSnapshot{OldestQueuedAt: now.Add(-10 * time.Minute)}}
	health := newMailHealth(true)
	sender := &probingMailSender{mailSenderFake: mailSenderFake{enabled: true}, probeErr: errors.New("smtp unavailable")}
	if err := (mailMaintenanceMonitor{mail: mail, sender: sender, health: health, now: func() time.Time { return now }}).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if health.Code() != MailHealthSMTPOutage || !health.Degraded() {
		t.Fatalf("SMTP health = %s", health.Code())
	}
	sender.probeErr = nil
	if err := (mailMaintenanceMonitor{mail: mail, sender: sender, health: health, now: func() time.Time { return now }}).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if health.Code() != MailHealthQueueDelayed || !health.Degraded() {
		t.Fatalf("queue health = %s", health.Code())
	}
}

func TestMailMaintenanceEmitsBoundedMetricsAndLogsHealthOnlyOnTransitions(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	metrics := &recordingMailMetrics{}
	mail := &mailMaintenanceFake{snapshot: &store.MailQueueSnapshot{
		Counts: []store.MailQueueCount{
			{TemplateKey: model.MailTemplateSystemTest, State: model.MailDeliveryQueued, Count: 500, OldestObservedAt: now.Add(-time.Minute)},
			{TemplateKey: model.MailTemplateSystemTest, State: model.MailDeliverySending, Count: 1, OldestObservedAt: now.Add(-30 * time.Second)},
		},
		OldestQueuedAt: now.Add(-time.Minute), More: true,
	}}
	sender := &probingMailSender{mailSenderFake: mailSenderFake{enabled: true}}
	health := newMailHealth(true)
	monitor := mailMaintenanceMonitor{mail: mail, sender: sender, health: health, recorder: metrics, now: func() time.Time { return now }}
	if err := monitor.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(metrics.health) != 1 || metrics.health[0].Code != MailHealthHealthy || len(metrics.queues) != 2 || !metrics.queues[0].Truncated ||
		metrics.queues[0].Count != 500 || metrics.queues[0].OldestAge != time.Minute || metrics.queues[1].OldestAge != 30*time.Second {
		t.Fatalf("initial metrics = health %#v queue %#v", metrics.health, metrics.queues)
	}
	mail.snapshot = &store.MailQueueSnapshot{}
	if err := monitor.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(metrics.queues) != 0 {
		t.Fatalf("queue metrics after drain = %#v", metrics.queues)
	}
	sender.probeErr = errors.New("smtp unavailable")
	if err := monitor.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := monitor.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(metrics.health) != 4 || metrics.health[2].Code != MailHealthSMTPOutage || metrics.health[3].Code != MailHealthSMTPOutage {
		t.Fatalf("health transitions = %#v", metrics.health)
	}
}

func TestMailCleanupJobBoundsPagesAndReportsSafeCounts(t *testing.T) {
	mail := &mailMaintenanceFake{
		expired: []*store.MailMaintenanceResult{{Affected: 2, More: true, Deliveries: []store.MailMaintenanceDelivery{{TemplateKey: model.MailTemplateSystemTest, State: model.MailDeliverySuppressed, PublicFailureCode: model.MailDeliveryExpiredCode, AttemptCount: 1, ProcessingLatency: time.Minute}}}, {Affected: 1}},
		cleaned: []*store.MailMaintenanceResult{{Affected: 3}},
	}
	command, _ := modelJSON(MailCleanupCommandV1{PageSize: 10, MaxPages: 2})
	job, err := model.NewJob(model.NewJobID(), model.JobTypeMailCleanup, 1, command, "cleanup", time.Now(), time.Now(), 5)
	if err != nil {
		t.Fatal(err)
	}
	metrics := &recordingMailMetrics{}
	outcome := (mailCleanupHandler{mail: mail, recorder: metrics}).Run(context.Background(), jobengine.NewExecution(job, nil, nil, nil))
	if outcome.Kind != jobengine.OutcomeSucceeded || string(outcome.Result) != `{"expired":3,"deleted":3}` {
		t.Fatalf("cleanup outcome = %#v", outcome)
	}
	if got := strings.Join(mail.calls, ","); got != "cleanup_terminal,suppress_expired,suppress_expired" {
		t.Fatalf("cleanup order = %q", got)
	}
	if len(metrics.deliveries) != 1 || metrics.deliveries[0].OutcomeCode != model.MailDeliveryExpiredCode {
		t.Fatalf("cleanup delivery metrics = %#v", metrics.deliveries)
	}
}

func TestMailCleanupProposalRequiresDependenciesAndUsesPermanentDailyDedupe(t *testing.T) {
	occurrence := time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)
	if err := (mailCleanupProposer{}).Propose(context.Background(), occurrence); err == nil {
		t.Fatal("mail cleanup proposal accepted missing dependencies")
	}
	jobs := &deduplicatingJobEnqueuerFake{jobs: map[string]*model.Job{}}
	for index := 0; index < 2; index++ {
		proposer := mailCleanupProposer{jobs: jobs, now: func() time.Time { return occurrence.Add(time.Duration(index) * time.Second) }}
		if err := proposer.Propose(context.Background(), occurrence); err != nil {
			t.Fatal(err)
		}
	}
	if len(jobs.jobs) != 1 {
		t.Fatalf("mail cleanup logical Jobs = %d", len(jobs.jobs))
	}
	job := jobs.jobs[string(model.JobTypeMailCleanup)+":"+"mail-cleanup:2026-08-17"]
	if job == nil || job.Type != model.JobTypeMailCleanup || job.DedupePolicy != model.JobDedupePermanent {
		t.Fatalf("mail cleanup Job = %#v", job)
	}
	var command MailCleanupCommandV1
	if err := json.Unmarshal(job.Command, &command); err != nil || command.PageSize != mailCleanupPageSize || command.MaxPages != mailCleanupMaximumPages {
		t.Fatalf("mail cleanup command = %#v, %v", command, err)
	}
}

func modelJSON(value any) ([]byte, error) {
	return json.Marshal(value)
}
