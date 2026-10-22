// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/app/jobhandler"
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

type sittingMailMaintenanceFake struct {
	results []*store.ExamSittingMailMaintenanceResult
	calls   int
}

func (f *sittingMailMaintenanceFake) MaintainMailExpansions(context.Context, int) (*store.ExamSittingMailMaintenanceResult, error) {
	f.calls++
	value := f.results[0]
	f.results = f.results[1:]
	return value, nil
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
func (*recordingMailMetrics) RecordMailAttempt(context.Context, MailAttemptMetric) {}
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
	require.NoError(t, monitor.Run(context.Background()))
	require.Equal(t, 1, mail.suppressed, "suppressed")
	require.Equal(t, MailHealthDisabled, health.Code(), "suppressed")
	require.False(t, health.Degraded(), "suppressed")
	require.Len(t, metrics.deliveries, 1, "disabled metrics = deliveries")
	require.Equal(t, model.MailDeliveryDisabledCode, metrics.deliveries[0].OutcomeCode, "disabled metrics = deliveries")
	require.Len(t, metrics.health, 1, "disabled metrics = deliveries")
	require.Equal(t, MailHealthDisabled, metrics.health[0].Code, "disabled metrics = deliveries")
}

func TestMailMaintenanceReportsSMTPOutageAndQueueDelayAsSubsystemDegradation(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	mail := &mailMaintenanceFake{snapshot: &store.MailQueueSnapshot{OldestQueuedAt: now.Add(-10 * time.Minute)}}
	health := newMailHealth(true)
	sender := &probingMailSender{mailSenderFake: mailSenderFake{enabled: true}, probeErr: errors.New("smtp unavailable")}
	require.NoError(t, (mailMaintenanceMonitor{mail: mail, sender: sender, health: health, now: func() time.Time { return now }}).Run(context.Background()))
	require.Equal(t, MailHealthSMTPOutage, health.Code())
	require.True(t, health.Degraded())
	sender.probeErr = nil
	require.NoError(t, (mailMaintenanceMonitor{mail: mail, sender: sender, health: health, now: func() time.Time { return now }}).Run(context.Background()))
	require.Equal(t, MailHealthQueueDelayed, health.Code())
	require.True(t, health.Degraded())
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
	require.NoError(t, monitor.Run(context.Background()))
	require.Len(t, metrics.health, 1, "initial metrics = health")
	require.Equal(t, MailHealthHealthy, metrics.health[0].Code, "initial metrics = health")
	require.Len(t, metrics.queues, 2, "initial metrics = health")
	require.True(t, metrics.queues[0].Truncated, "initial metrics = health")
	require.Equal(t, int64(500), metrics.queues[0].Count, "initial metrics = health")
	require.Equal(t, time.Minute, metrics.queues[0].OldestAge, "initial metrics = health")
	require.Equal(t, 30*time.Second, metrics.queues[1].OldestAge, "initial metrics = health")
	mail.snapshot = &store.MailQueueSnapshot{}
	require.NoError(t, monitor.Run(context.Background()))
	require.Empty(t, metrics.queues)
	sender.probeErr = errors.New("smtp unavailable")
	require.NoError(t, monitor.Run(context.Background()))
	require.NoError(t, monitor.Run(context.Background()))
	require.Len(t, metrics.health, 4)
	require.Equal(t, MailHealthSMTPOutage, metrics.health[2].Code)
	require.Equal(t, MailHealthSMTPOutage, metrics.health[3].Code)
}

func TestMailCleanupJobBoundsPagesAndReportsSafeCounts(t *testing.T) {
	mail := &mailMaintenanceFake{
		expired: []*store.MailMaintenanceResult{{Affected: 2, More: true, Deliveries: []store.MailMaintenanceDelivery{{TemplateKey: model.MailTemplateSystemTest, State: model.MailDeliverySuppressed, PublicFailureCode: model.MailDeliveryExpiredCode, AttemptCount: 1, ProcessingLatency: time.Minute}}}, {Affected: 1}},
		cleaned: []*store.MailMaintenanceResult{{Affected: 3}},
	}
	sittings := &sittingMailMaintenanceFake{results: []*store.ExamSittingMailMaintenanceResult{
		{FanoutsTerminalized: 4, DeliveriesSuppressed: 2, More: true},
		{FanoutsTerminalized: 1, DeliveriesSuppressed: 1},
	}}
	command, _ := json.Marshal(jobhandler.MailCleanupCommandV1{PageSize: 10, MaxPages: 2})
	job, err := model.NewJob(model.NewJobID(), model.JobTypeMailCleanup, 1, command, "cleanup", time.Now(), time.Now(), 5)
	require.NoError(t, err)
	metrics := &recordingMailMetrics{}
	descriptor := jobhandler.NewMailCleanupDescriptor(mail, sittings, jobMailDeliveryRecorder{recorder: metrics})
	outcome := descriptor.Handler.Run(context.Background(), jobengine.NewExecution(job, nil, nil, nil))
	require.Equal(t, jobengine.OutcomeSucceeded, outcome.Kind)
	require.Equal(t, `{"fanouts_terminalized":5,"fanout_deliveries_suppressed":3,"expired":3,"deleted":3}`, string(outcome.Result))
	require.Equal(t, 2, sittings.calls, "Sitting fan-out maintenance calls")
	require.Equal(t, []string{"cleanup_terminal", "suppress_expired", "suppress_expired"}, mail.calls, "one terminal cleanup and two bounded expiry pages")
	require.Len(t, metrics.deliveries, 1)
	require.Equal(t, model.MailDeliveryExpiredCode, metrics.deliveries[0].OutcomeCode)
}
