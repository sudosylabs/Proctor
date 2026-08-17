// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	netmail "net/mail"
	"strings"
	"time"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/secretseal"
	"github.com/sudosylabs/proctor/server/store"
)

type mailDeliveryLifecycleStore interface {
	GetDelivery(context.Context, model.MailDeliveryID) (*model.MailDelivery, error)
	StartDelivery(context.Context, model.MailDeliveryID, int64, time.Time) (*model.MailDelivery, error)
	CompleteDelivery(context.Context, *store.MailDeliveryCompletion) (*model.MailDelivery, error)
}

type mailDeliveryHandler struct {
	deliveries mailDeliveryLifecycleStore
	sender     MailDeliverySender
	sealer     *secretseal.Sealer
	now        func() time.Time
}

func (h mailDeliveryHandler) Run(ctx context.Context, execution jobengine.Execution) jobengine.Outcome {
	if execution.Job == nil {
		return jobengine.PermanentFailure("job.command.invalid", errors.New("mail delivery job is missing"))
	}
	command, err := model.DecodeMailDeliveryCommand(execution.Job.CommandVersion, execution.Job.Command)
	if err != nil {
		return jobengine.PermanentFailure("job.command.invalid", err)
	}
	if h.deliveries == nil || h.sender == nil || h.sealer == nil || h.now == nil || !h.sender.Enabled() {
		return jobengine.RetryableFailure("mail.delivery.unavailable", errors.New("mail delivery dependencies are unavailable"))
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
		return jobengine.PermanentFailure(nonemptyMailCode(delivery.PublicFailureCode, "mail.delivery.failed"), errors.New("mail delivery is failed"))
	}
	now := model.TimeUTC(h.now())
	if !now.Before(delivery.Deadline) {
		return h.expire(ctx, delivery, now)
	}
	sending, err := h.deliveries.StartDelivery(ctx, delivery.ID, delivery.Revision, now)
	if err != nil {
		return mailDeliveryDependencyOutcome(err)
	}
	payload, err := openFrozenMailPayload(h.sealer, sending)
	if err != nil {
		return h.fail(ctx, sending, "mail.payload.unavailable", err)
	}
	message := OutboundMail{From: MailAddress{Name: payload.FromName, Address: payload.FromAddress}, EnvelopeFrom: payload.FromAddress, To: MailAddress{Name: payload.RecipientName, Address: payload.RecipientAddress}, Subject: payload.Subject, Text: payload.Text, HTML: payload.HTML, Headers: map[string][]string{"Auto-Submitted": {payload.AutoSubmitted}, "X-Auto-Response-Suppress": {payload.AutoResponseSuppress}}, MessageID: sending.MessageID, Date: sending.MessageDate}
	if err = validateOutboundMail(message); err != nil {
		return h.fail(ctx, sending, "mail.message.invalid", err)
	}
	classification, err := h.sender.Send(ctx, message)
	if err != nil {
		completedAt := model.TimeUTC(h.now())
		if !completedAt.Before(sending.Deadline) {
			return h.expire(ctx, sending, completedAt)
		}
		code := mailTransportFailureCode(classification)
		if classification == MailTransportPermanent || sending.AttemptCount >= model.MailMaximumAttempts {
			return h.failAt(ctx, sending, code, err, completedAt)
		}
		if _, transitionErr := h.deliveries.CompleteDelivery(ctx, &store.MailDeliveryCompletion{DeliveryID: sending.ID, ExpectedRevision: sending.Revision, Kind: store.MailDeliveryCompletionRetry, PublicFailureCode: code, At: completedAt}); transitionErr != nil {
			return mailDeliveryDependencyOutcome(transitionErr)
		}
		return jobengine.RetryableFailure(code, err)
	}
	accepted, err := h.deliveries.CompleteDelivery(ctx, &store.MailDeliveryCompletion{DeliveryID: sending.ID, ExpectedRevision: sending.Revision, Kind: store.MailDeliveryCompletionAccepted, At: model.TimeUTC(h.now())})
	if err != nil {
		return mailDeliveryDependencyOutcome(err)
	}
	return mailDeliverySucceeded(accepted)
}

func (h mailDeliveryHandler) fail(ctx context.Context, delivery *model.MailDelivery, code string, cause error) jobengine.Outcome {
	at := model.TimeUTC(h.now())
	if !at.Before(delivery.Deadline) {
		return h.expire(ctx, delivery, at)
	}
	return h.failAt(ctx, delivery, code, cause, at)
}

func (h mailDeliveryHandler) failAt(ctx context.Context, delivery *model.MailDelivery, code string, cause error, at time.Time) jobengine.Outcome {
	_, transitionErr := h.deliveries.CompleteDelivery(ctx, &store.MailDeliveryCompletion{DeliveryID: delivery.ID, ExpectedRevision: delivery.Revision, Kind: store.MailDeliveryCompletionFailed, PublicFailureCode: code, At: at})
	if transitionErr != nil {
		return mailDeliveryDependencyOutcome(transitionErr)
	}
	return jobengine.PermanentFailure(code, cause)
}

func (h mailDeliveryHandler) expire(ctx context.Context, delivery *model.MailDelivery, at time.Time) jobengine.Outcome {
	expired, err := h.deliveries.CompleteDelivery(ctx, &store.MailDeliveryCompletion{DeliveryID: delivery.ID, ExpectedRevision: delivery.Revision, Kind: store.MailDeliveryCompletionExpired, At: at})
	if err != nil {
		return mailDeliveryDependencyOutcome(err)
	}
	return mailDeliverySucceeded(expired)
}

func openFrozenMailPayload(sealer *secretseal.Sealer, delivery *model.MailDelivery) (frozenMailPayloadV1, error) {
	var envelope secretseal.Envelope
	if delivery == nil || len(delivery.EncryptedPayload) == 0 || json.Unmarshal(delivery.EncryptedPayload, &envelope) != nil {
		return frozenMailPayloadV1{}, errors.New("encrypted mail payload is invalid")
	}
	plaintext, err := sealer.Open(secretseal.Binding{Purpose: mailDeliverySealingPurpose, Owner: delivery.ID.String()}, envelope)
	if err != nil {
		return frozenMailPayloadV1{}, err
	}
	var payload frozenMailPayloadV1
	decoder := json.NewDecoder(bytes.NewReader(plaintext))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&payload); err != nil || decoder.Decode(&struct{}{}) == nil || payload.Version != 1 || payload.RecipientAddress == "" || payload.FromAddress == "" || payload.Subject == "" || (payload.Text == "" && payload.HTML == "") || payload.AutoSubmitted != "auto-generated" || payload.AutoResponseSuppress != "All" {
		return frozenMailPayloadV1{}, errors.New("frozen mail payload is invalid")
	}
	return payload, nil
}

func validateOutboundMail(message OutboundMail) error {
	if _, err := netmail.ParseAddress(message.From.Address); err != nil {
		return errors.New("mail sender address is invalid")
	}
	if _, err := netmail.ParseAddress(message.To.Address); err != nil {
		return errors.New("mail recipient address is invalid")
	}
	if message.EnvelopeFrom == "" || message.Subject == "" || (message.Text == "" && message.HTML == "") ||
		message.MessageID == "" || message.Date.IsZero() || strings.ContainsAny(message.Subject, "\x00\r\n") {
		return errors.New("mail message is invalid")
	}
	return nil
}

func mailTransportFailureCode(outcome MailTransportOutcome) string {
	switch outcome {
	case MailTransportPermanent:
		return "mail.transport.permanent"
	case MailTransportAcceptanceUncertain:
		return "mail.transport.acceptance_uncertain"
	case MailTransportTemporary:
		return "mail.transport.temporary"
	default:
		return "mail.transport.unknown"
	}
}

func mailDeliveryDependencyOutcome(err error) jobengine.Outcome {
	if store.IsNotFound(err) {
		return jobengine.PermanentFailure("mail.delivery.not_found", err)
	}
	if store.IsConflict(err) {
		return jobengine.RetryableFailure("mail.delivery.conflict", err)
	}
	return jobengine.RetryableFailure("mail.delivery.unavailable", err)
}

func nonemptyMailCode(code, fallback string) string {
	if code != "" {
		return code
	}
	return fallback
}

func mailDeliverySucceeded(delivery *model.MailDelivery) jobengine.Outcome {
	if delivery == nil || !delivery.ID.IsValid() {
		return jobengine.Outcome{Kind: jobengine.OutcomeSucceeded, Err: errors.New("mail delivery result is invalid")}
	}
	document, err := json.Marshal(struct {
		DeliveryID model.MailDeliveryID    `json:"delivery_id"`
		State      model.MailDeliveryState `json:"state"`
		MessageID  string                  `json:"message_id"`
	}{delivery.ID, delivery.State, delivery.MessageID})
	return jobengine.Outcome{Kind: jobengine.OutcomeSucceeded, ResultVersion: 1, Result: document, Err: err}
}

func mailDeliveryDescriptor(handler jobengine.Handler) jobengine.Descriptor {
	return jobengine.Descriptor{Type: model.JobTypeMailDeliver, CommandVersions: []int{1}, ResultVersions: []int{1}, PublicErrorCodes: []string{"mail.delivery.conflict", "mail.delivery.failed", "mail.delivery.not_found", "mail.delivery.unavailable", "mail.message.invalid", "mail.payload.unavailable", "mail.transport.acceptance_uncertain", "mail.transport.permanent", "mail.transport.temporary", "mail.transport.unknown"}, Timeout: time.Minute, Concurrency: 8, MaximumAttempts: model.MailMaximumAttempts, LeaseDuration: time.Minute, HeartbeatInterval: 15 * time.Second, BaseRetryDelay: 30 * time.Second, MaximumRetryDelay: 30 * time.Minute, Visibility: jobengine.VisibilityOperator, SuccessRetention: 90 * 24 * time.Hour, FailureRetention: 180 * 24 * time.Hour, Handler: handler}
}
