// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type mailDeliveryRelevance uint8

const (
	mailDeliveryRelevant mailDeliveryRelevance = iota + 1
	mailDeliveryObsolete
)

// evaluateMailDeliveryRelevance is the closed typed policy seam used before
// automatic and operator attempts. Each future template family must add an
// explicit authoritative check rather than defaulting to send.
func evaluateMailDeliveryRelevance(_ context.Context, delivery *model.MailDelivery) (mailDeliveryRelevance, error) {
	if delivery == nil || delivery.Validate() != nil {
		return 0, errors.New("mail delivery relevance target is invalid")
	}
	switch delivery.TemplateKey {
	case model.MailTemplateSystemTest:
		// A controlled transport test is relevant until its immutable deadline.
		return mailDeliveryRelevant, nil
	default:
		return 0, errors.New("mail delivery relevance policy is unavailable")
	}
}

type MailDeliveryMetric struct {
	TemplateKey       model.MailTemplateKey
	State             model.MailDeliveryState
	OutcomeCode       string
	AttemptCount      int
	ProcessingLatency time.Duration
}

type MailQueueMetric struct {
	TemplateKey model.MailTemplateKey
	State       model.MailDeliveryState
	OutcomeCode string
	Count       int64
	OldestAge   time.Duration
	HealthCode  string
	Truncated   bool
}

type MailHealthMetric struct{ Code string }

type MailDeliveryMetricAggregate struct {
	TemplateKey              model.MailTemplateKey
	State                    model.MailDeliveryState
	OutcomeCode              string
	Count                    uint64
	AttemptCount             uint64
	ProcessingLatency        time.Duration
	MaximumProcessingLatency time.Duration
}

type MailMetricsSnapshot struct {
	Deliveries []MailDeliveryMetricAggregate
	Queues     []MailQueueMetric
	HealthCode string
}

// MailDeliveryRecorder is a bounded telemetry event sink. The contracts omit
// delivery IDs and every recipient or content-bearing value, keeping metric
// cardinality and operational logs safe.
type MailDeliveryRecorder interface {
	RecordMailDelivery(context.Context, MailDeliveryMetric)
	RecordMailQueueSnapshot(context.Context, []MailQueueMetric)
	RecordMailHealth(context.Context, MailHealthMetric)
	Snapshot() MailMetricsSnapshot
}

// NopMailDeliveryRecorder preserves the explicit metrics seam when no
// exporter is configured. Tests and future production exporters can replace
// it without coupling application policy to a telemetry implementation.
type NopMailDeliveryRecorder struct{}

func (NopMailDeliveryRecorder) RecordMailDelivery(context.Context, MailDeliveryMetric) {}
func (NopMailDeliveryRecorder) RecordMailQueueSnapshot(context.Context, []MailQueueMetric) {
}
func (NopMailDeliveryRecorder) RecordMailHealth(context.Context, MailHealthMetric) {}
func (NopMailDeliveryRecorder) Snapshot() MailMetricsSnapshot                      { return MailMetricsSnapshot{} }

const (
	MailHealthDisabled      = "mail.disabled"
	MailHealthHealthy       = "mail.healthy"
	MailHealthSMTPOutage    = "mail.smtp_outage"
	MailHealthQueueDelayed  = "mail.queue_delayed"
	mailQueueDelayThreshold = 5 * time.Minute
)

type MailHealth struct{ code atomic.Value }

func newMailHealth(enabled bool) *MailHealth {
	health := &MailHealth{}
	if enabled {
		health.code.Store(MailHealthHealthy)
	} else {
		health.code.Store(MailHealthDisabled)
	}
	return health
}

func (h *MailHealth) Code() string {
	if h == nil {
		return MailHealthDisabled
	}
	if code, ok := h.code.Load().(string); ok {
		return code
	}
	return MailHealthDisabled
}

func (h *MailHealth) Degraded() bool {
	code := h.Code()
	return code != MailHealthHealthy && code != MailHealthDisabled
}

func (h *MailHealth) set(code string) bool {
	if h != nil {
		previous := h.code.Swap(code)
		return previous != code
	}
	return false
}

type mailMaintenanceStore interface {
	SuppressOutstanding(context.Context, string, int) (*store.MailMaintenanceResult, error)
	QueueSnapshot(context.Context) (*store.MailQueueSnapshot, error)
}

type mailDeliveryProber interface{ Probe(context.Context) error }

type mailMaintenanceMonitor struct {
	mail     mailMaintenanceStore
	sender   MailDeliverySender
	health   *MailHealth
	recorder MailDeliveryRecorder
	now      func() time.Time
}

func (m mailMaintenanceMonitor) Run(ctx context.Context) error {
	if m.mail == nil || m.sender == nil || m.health == nil || m.now == nil {
		return errors.New("mail maintenance dependencies are unavailable")
	}
	enabled := m.sender.Enabled()
	healthCode := MailHealthDisabled
	if !enabled {
		suppressed, err := m.mail.SuppressOutstanding(ctx, model.MailDeliveryDisabledCode, 500)
		if err != nil {
			return err
		}
		recordMailMaintenanceDeliveries(ctx, m.recorder, suppressed)
	} else {
		healthCode = MailHealthHealthy
	}
	if prober, ok := m.sender.(mailDeliveryProber); enabled && ok {
		if err := prober.Probe(ctx); err != nil {
			healthCode = MailHealthSMTPOutage
		}
	}
	snapshot, err := m.mail.QueueSnapshot(ctx)
	if err != nil {
		return err
	}
	now := model.TimeUTC(m.now())
	oldestAge := time.Duration(0)
	if snapshot != nil && !snapshot.OldestQueuedAt.IsZero() && now.After(snapshot.OldestQueuedAt) {
		oldestAge = now.Sub(snapshot.OldestQueuedAt)
		if oldestAge >= mailQueueDelayThreshold && healthCode == MailHealthHealthy {
			healthCode = MailHealthQueueDelayed
		}
	}
	m.health.set(healthCode)
	if m.recorder != nil {
		m.recorder.RecordMailHealth(ctx, MailHealthMetric{Code: healthCode})
	}
	if m.recorder != nil {
		metrics := make([]MailQueueMetric, 0)
		if snapshot != nil {
			metrics = make([]MailQueueMetric, 0, len(snapshot.Counts))
			for _, count := range snapshot.Counts {
				itemAge := time.Duration(0)
				if !count.OldestObservedAt.IsZero() && now.After(count.OldestObservedAt) {
					itemAge = now.Sub(count.OldestObservedAt)
				}
				metrics = append(metrics, MailQueueMetric{TemplateKey: count.TemplateKey, State: count.State, Count: count.Count, OldestAge: itemAge, HealthCode: healthCode, OutcomeCode: count.PublicFailureCode, Truncated: snapshot.More})
			}
		}
		m.recorder.RecordMailQueueSnapshot(ctx, metrics)
	}
	return nil
}

func recordMailMaintenanceDeliveries(ctx context.Context, recorder MailDeliveryRecorder, result *store.MailMaintenanceResult) {
	if recorder == nil || result == nil {
		return
	}
	for _, delivery := range result.Deliveries {
		recorder.RecordMailDelivery(ctx, MailDeliveryMetric{
			TemplateKey: delivery.TemplateKey, State: delivery.State, OutcomeCode: delivery.PublicFailureCode,
			AttemptCount: delivery.AttemptCount, ProcessingLatency: delivery.ProcessingLatency,
		})
	}
}
