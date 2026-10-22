// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package jobhandler

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/app/mail"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/secretseal"
	"github.com/sudosylabs/proctor/server/store"
)

type MailDeliveryLifecycleStore interface {
	GetDelivery(context.Context, model.MailDeliveryID) (*model.MailDelivery, error)
	StartDelivery(context.Context, *store.MailDeliveryStart) (*model.MailDelivery, error)
	CompleteDelivery(context.Context, *store.MailDeliveryCompletion) (*model.MailDelivery, error)
	AcquireSendPermit(context.Context, store.MailSendClass) (*store.MailSendPermit, error)
}

type MailDeliveryMetric struct {
	TemplateKey       model.MailTemplateKey
	State             model.MailDeliveryState
	OutcomeCode       string
	ProcessingLatency time.Duration
}

type MailAttemptMetric struct {
	TemplateKey model.MailTemplateKey
	State       model.MailDeliveryState
}

type MailDeliveryRecorder interface {
	RecordJobMailDelivery(context.Context, MailDeliveryMetric)
	RecordJobMailAttempt(context.Context, MailAttemptMetric)
}

type MailHealth interface {
	SetJobMailHealth(string)
}

type MailDeliveryRelevance func(context.Context, *model.MailDelivery) (bool, error)

type mailDeliveryHandler struct {
	deliveries MailDeliveryLifecycleStore
	sender     mail.Sender
	sealer     *secretseal.Sealer
	recorder   MailDeliveryRecorder
	health     MailHealth
	relevance  MailDeliveryRelevance
	class      store.MailSendClass
	now        func() time.Time
}

func (h mailDeliveryHandler) Run(ctx context.Context, execution job.Execution) job.Outcome {
	if execution.Job == nil {
		return job.PermanentFailure("job.command.invalid", errors.New("mail delivery job is missing"))
	}
	command, err := model.DecodeMailDeliveryCommand(execution.Job.Command)
	if err != nil {
		return job.PermanentFailure("job.command.invalid", err)
	}
	if h.deliveries == nil || h.sender == nil || h.now == nil {
		return job.RetryableFailure("mail.delivery.unavailable", errors.New("mail delivery dependencies are unavailable"))
	}
	delivery, err := h.deliveries.GetDelivery(ctx, command.DeliveryID)
	if err != nil {
		return mailDeliveryDependencyOutcome(err)
	}
	switch delivery.State {
	case model.MailDeliveryAccepted:
		return mailDeliverySucceeded(delivery)
	case model.MailDeliverySuppressed, model.MailDeliveryCanceled:
		return mailDeliverySucceeded(delivery)
	case model.MailDeliveryFailed:
		return job.PermanentFailure(nonemptyMailCode(delivery.PublicFailureCode, "mail.delivery.failed"), errors.New("mail delivery is failed"))
	}
	now := model.TimeUTC(h.now())
	if !now.Before(delivery.Deadline) {
		return h.expire(ctx, delivery, now)
	}
	if !h.sender.Enabled() {
		return h.suppress(ctx, delivery, model.MailDeliveryDisabledCode, now)
	}
	relevant, relevanceErr := h.isRelevant(ctx, delivery)
	if relevanceErr != nil {
		return job.RetryableFailure("mail.delivery.unavailable", relevanceErr)
	}
	if !relevant {
		return h.suppress(ctx, delivery, model.MailDeliveryObsoleteCode, now)
	}
	if h.sealer == nil {
		return job.RetryableFailure("mail.delivery.unavailable", errors.New("mail payload sealing is unavailable"))
	}
	if err = h.waitForPermit(ctx); err != nil {
		return job.RetryableFailure("mail.delivery.unavailable", err)
	}
	// Rate waiting may span a concurrent operator or maintenance transition.
	// Re-read and repeat deadline/relevance fencing immediately before Start.
	delivery, err = h.deliveries.GetDelivery(ctx, command.DeliveryID)
	if err != nil {
		return mailDeliveryDependencyOutcome(err)
	}
	if delivery.State == model.MailDeliveryAccepted || delivery.State == model.MailDeliverySuppressed || delivery.State == model.MailDeliveryCanceled {
		return mailDeliverySucceeded(delivery)
	}
	if delivery.State == model.MailDeliveryFailed {
		return job.PermanentFailure(nonemptyMailCode(delivery.PublicFailureCode, "mail.delivery.failed"), errors.New("mail delivery is failed"))
	}
	now = model.TimeUTC(h.now())
	if !now.Before(delivery.Deadline) {
		return h.expire(ctx, delivery, now)
	}
	if !h.sender.Enabled() {
		return h.suppress(ctx, delivery, model.MailDeliveryDisabledCode, now)
	}
	relevant, relevanceErr = h.isRelevant(ctx, delivery)
	if relevanceErr != nil {
		return job.RetryableFailure("mail.delivery.unavailable", relevanceErr)
	}
	if !relevant {
		return h.suppress(ctx, delivery, model.MailDeliveryObsoleteCode, now)
	}
	if delivery.State == model.MailDeliverySending && delivery.AttemptCount >= model.MailMaximumAttempts {
		// The last send attempt lost its worker mid-send. Its outcome is
		// unknown and no attempt remains, so the delivery fails visibly for an
		// operator instead of waiting Sending for its deadline.
		return h.failAt(ctx, delivery, "mail.transport.acceptance_uncertain", errors.New("mail delivery attempts are exhausted"), now)
	}
	sending, err := h.deliveries.StartDelivery(ctx, &store.MailDeliveryStart{ID: delivery.ID, ExpectedRevision: delivery.Revision, At: now})
	if err != nil {
		return mailDeliveryDependencyOutcome(err)
	}
	if sending.State == model.MailDeliverySuppressed || sending.State == model.MailDeliveryCanceled || sending.State == model.MailDeliveryAccepted {
		return mailDeliverySucceeded(sending)
	}
	h.recordAttempt(ctx, sending)
	message, err := mail.OpenDelivery(h.sealer, sending)
	if err != nil {
		if errors.Is(err, mail.ErrInvalidMessage) {
			return h.fail(ctx, sending, "mail.message.invalid", err)
		}
		return h.fail(ctx, sending, "mail.payload.unavailable", err)
	}
	classification, err := h.sender.Send(ctx, message)
	if err != nil {
		completedAt := model.TimeUTC(h.now())
		if !completedAt.Before(sending.Deadline) {
			return h.expire(ctx, sending, completedAt)
		}
		code := mailTransportFailureCode(classification)
		if h.health != nil && classification != mail.TransportPermanent {
			h.health.SetJobMailHealth("mail.smtp_outage")
		}
		// The delivery and its Job count attempts separately. On the Job's last
		// attempt a requeued delivery would be left Queued with no Job, so it
		// fails now and stays available to an operator retry.
		lastJobAttempt := execution.Job.AttemptCount >= execution.Job.MaximumAttempts
		if classification == mail.TransportPermanent || sending.AttemptCount >= model.MailMaximumAttempts || lastJobAttempt {
			return h.failAt(ctx, sending, code, err, completedAt)
		}
		if _, transitionErr := h.deliveries.CompleteDelivery(ctx, &store.MailDeliveryCompletion{DeliveryID: sending.ID, ExpectedRevision: sending.Revision, Kind: store.MailDeliveryCompletionRetry, PublicFailureCode: code, At: completedAt}); transitionErr != nil {
			return mailDeliveryDependencyOutcome(transitionErr)
		}
		h.record(ctx, sending, model.MailDeliveryQueued, code, completedAt)
		return job.RetryableFailure(code, err)
	}
	accepted, err := h.deliveries.CompleteDelivery(ctx, &store.MailDeliveryCompletion{DeliveryID: sending.ID, ExpectedRevision: sending.Revision, Kind: store.MailDeliveryCompletionAccepted, At: model.TimeUTC(h.now())})
	if err != nil {
		return mailDeliveryDependencyOutcome(err)
	}
	if h.health != nil {
		h.health.SetJobMailHealth("mail.healthy")
	}
	h.record(ctx, accepted, accepted.State, "mail.accepted", accepted.UpdatedAt)
	return mailDeliverySucceeded(accepted)
}

func (h mailDeliveryHandler) isRelevant(ctx context.Context, delivery *model.MailDelivery) (bool, error) {
	if h.relevance == nil {
		return false, errors.New("mail delivery relevance policy is unavailable")
	}
	return h.relevance(ctx, delivery)
}

func (h mailDeliveryHandler) waitForPermit(ctx context.Context) error {
	class := h.class
	if class == "" {
		class = store.MailSendOrdinary
	}
	for {
		permit, err := h.deliveries.AcquireSendPermit(ctx, class)
		if err != nil {
			return err
		}
		if permit == nil {
			return errors.New("mail send limiter returned no decision")
		}
		if permit.Allowed {
			return nil
		}
		if permit.RetryAfter <= 0 || permit.RetryAfter > time.Second {
			return errors.New("mail send limiter returned an invalid delay")
		}
		timer := time.NewTimer(permit.RetryAfter)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (h mailDeliveryHandler) fail(ctx context.Context, delivery *model.MailDelivery, code string, cause error) job.Outcome {
	at := model.TimeUTC(h.now())
	if !at.Before(delivery.Deadline) {
		return h.expire(ctx, delivery, at)
	}
	return h.failAt(ctx, delivery, code, cause, at)
}

func (h mailDeliveryHandler) failAt(ctx context.Context, delivery *model.MailDelivery, code string, cause error, at time.Time) job.Outcome {
	failed, transitionErr := h.deliveries.CompleteDelivery(ctx, &store.MailDeliveryCompletion{DeliveryID: delivery.ID, ExpectedRevision: delivery.Revision, Kind: store.MailDeliveryCompletionFailed, PublicFailureCode: code, At: at})
	if transitionErr != nil {
		return mailDeliveryDependencyOutcome(transitionErr)
	}
	h.record(ctx, failed, failed.State, code, at)
	return job.PermanentFailure(code, cause)
}

func (h mailDeliveryHandler) expire(ctx context.Context, delivery *model.MailDelivery, at time.Time) job.Outcome {
	expired, err := h.deliveries.CompleteDelivery(ctx, &store.MailDeliveryCompletion{DeliveryID: delivery.ID, ExpectedRevision: delivery.Revision, Kind: store.MailDeliveryCompletionExpired, At: at})
	if err != nil {
		return mailDeliveryDependencyOutcome(err)
	}
	h.record(ctx, expired, expired.State, model.MailDeliveryExpiredCode, at)
	return mailDeliverySucceeded(expired)
}

func (h mailDeliveryHandler) suppress(ctx context.Context, delivery *model.MailDelivery, code string, at time.Time) job.Outcome {
	suppressed, err := h.deliveries.CompleteDelivery(ctx, &store.MailDeliveryCompletion{DeliveryID: delivery.ID, ExpectedRevision: delivery.Revision, Kind: store.MailDeliveryCompletionSuppress, PublicFailureCode: code, At: at})
	if err != nil {
		return mailDeliveryDependencyOutcome(err)
	}
	h.record(ctx, suppressed, suppressed.State, code, at)
	return mailDeliverySucceeded(suppressed)
}

func (h mailDeliveryHandler) record(ctx context.Context, delivery *model.MailDelivery, state model.MailDeliveryState, code string, at time.Time) {
	if h.recorder == nil || delivery == nil {
		return
	}
	latency := model.TimeUTC(at).Sub(delivery.CreatedAt)
	if latency < 0 {
		latency = 0
	}
	h.recorder.RecordJobMailDelivery(ctx, MailDeliveryMetric{TemplateKey: delivery.TemplateKey, State: state, OutcomeCode: code, ProcessingLatency: latency})
}

func (h mailDeliveryHandler) recordAttempt(ctx context.Context, delivery *model.MailDelivery) {
	if h.recorder == nil || delivery == nil {
		return
	}
	h.recorder.RecordJobMailAttempt(ctx, MailAttemptMetric{TemplateKey: delivery.TemplateKey, State: delivery.State})
}

func mailTransportFailureCode(outcome mail.TransportOutcome) string {
	switch outcome {
	case mail.TransportPermanent:
		return "mail.transport.permanent"
	case mail.TransportAcceptanceUncertain:
		return "mail.transport.acceptance_uncertain"
	case mail.TransportTemporary:
		return "mail.transport.temporary"
	default:
		return "mail.transport.unknown"
	}
}

func mailDeliveryDependencyOutcome(err error) job.Outcome {
	if store.IsNotFound(err) {
		return job.PermanentFailure("mail.delivery.not_found", err)
	}
	if store.IsConflict(err) {
		return job.RetryableFailure("mail.delivery.conflict", err)
	}
	return job.RetryableFailure("mail.delivery.unavailable", err)
}

func nonemptyMailCode(code, fallback string) string {
	if code != "" {
		return code
	}
	return fallback
}

func mailDeliverySucceeded(delivery *model.MailDelivery) job.Outcome {
	if delivery == nil || !delivery.ID.IsValid() {
		return job.Outcome{Kind: job.OutcomeSucceeded, Err: errors.New("mail delivery result is invalid")}
	}
	document, err := json.Marshal(struct {
		DeliveryID model.MailDeliveryID    `json:"delivery_id"`
		State      model.MailDeliveryState `json:"state"`
		MessageID  string                  `json:"message_id"`
	}{delivery.ID, delivery.State, delivery.MessageID})
	return job.Outcome{Kind: job.OutcomeSucceeded, Result: document, Err: err}
}

func mailDeliveryDescriptor(handler mailDeliveryHandler) job.Descriptor {
	handler.class = store.MailSendOrdinary
	return newMailDeliveryDescriptor(model.JobTypeMailDeliver, 8, handler)
}

func mailCredentialDeliveryDescriptor(handler mailDeliveryHandler) job.Descriptor {
	handler.class = store.MailSendCredential
	return newMailDeliveryDescriptor(model.JobTypeMailDeliverCredential, 4, handler)
}

func newMailDeliveryDescriptor(jobType model.JobType, concurrency int, handler job.Handler) job.Descriptor {
	return job.Descriptor{Type: jobType, PublicErrorCodes: []string{"mail.delivery.conflict", "mail.delivery.failed", "mail.delivery.not_found", "mail.delivery.unavailable", "mail.message.invalid", "mail.payload.unavailable", "mail.transport.acceptance_uncertain", "mail.transport.permanent", "mail.transport.temporary", "mail.transport.unknown"}, Timeout: time.Minute, Concurrency: concurrency, MaximumAttempts: model.MailMaximumAttempts, LeaseDuration: time.Minute, HeartbeatInterval: 15 * time.Second, BaseRetryDelay: 30 * time.Second, MaximumRetryDelay: 30 * time.Minute, Visibility: job.VisibilityOperator, SuccessRetention: 90 * 24 * time.Hour, FailureRetention: 180 * 24 * time.Hour, Handler: handler}
}

// NewMailDeliveryDescriptor registers durable delivery of one frozen mail
// message; credential selects the dedicated credential-mail pool.
func NewMailDeliveryDescriptor(deliveries MailDeliveryLifecycleStore, sender mail.Sender, sealer *secretseal.Sealer,
	recorder MailDeliveryRecorder, health MailHealth, relevance MailDeliveryRelevance, now func() time.Time, credential bool,
) job.Descriptor {
	handler := mailDeliveryHandler{deliveries: deliveries, sender: sender, sealer: sealer, recorder: recorder,
		health: health, relevance: relevance, now: now}
	if credential {
		return mailCredentialDeliveryDescriptor(handler)
	}
	return mailDeliveryDescriptor(handler)
}
