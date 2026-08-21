// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package mail

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/secretseal"
	"github.com/sudosylabs/proctor/server/store"
)

const (
	FanoutBundleSealingPurpose = "mail.fanout_bundle"
	sittingDeliveryLifetime    = 72 * time.Hour
)

type SittingScheduleDetails struct {
	ExamTitle             string
	ClassDisplayName      string
	PriorClassDisplayName string
	StartsAt              time.Time
	EndsAt                time.Time
}

type SittingRenderer interface {
	RenderSittingScheduleNotice(model.MailTemplateKey, string, SittingScheduleDetails) (FrozenContent, error)
}

type FrozenSittingBundleV1 struct {
	Version  int                                     `json:"version"`
	From     Address                                 `json:"from"`
	Messages map[model.MailTemplateKey]FrozenContent `json:"messages"`
}

type SittingComposer struct {
	renderer SittingRenderer
	sender   Sender
	sealer   *secretseal.Sealer
	now      func() time.Time
}

func NewSittingComposer(renderer SittingRenderer, sender Sender, sealer *secretseal.Sealer, now func() time.Time) (*SittingComposer, error) {
	if renderer == nil || sender == nil || now == nil || sender.Enabled() && sealer == nil {
		return nil, errors.New("sitting mail composer dependencies are invalid")
	}
	return &SittingComposer{renderer: renderer, sender: sender, sealer: sealer, now: now}, nil
}

func (p *SittingComposer) Prepare(actorID model.UserID, sitting *model.ExamSitting, change store.ExamSittingMailChangeKind,
	details SittingScheduleDetails,
) (*store.ExamSittingMailFanout, error) {
	if p == nil || !actorID.IsValid() || sitting == nil || !sitting.ID.IsValid() || !sitting.ExamID.IsValid() ||
		!sitting.ExamRevisionID.IsValid() || !sitting.ClassID.IsValid() || sitting.Revision < 1 ||
		!sitting.ScheduledStartAt.Before(sitting.ScheduledEndAt) || !validSittingDetails(details) {
		return nil, errors.New("sitting mail preparation is invalid")
	}
	if details.StartsAt.IsZero() && details.EndsAt.IsZero() {
		details.StartsAt, details.EndsAt = sitting.ScheduledStartAt, sitting.ScheduledEndAt
	}
	primaryKey := model.MailTemplateExamSittingScheduled
	switch change {
	case store.ExamSittingMailScheduled:
	case store.ExamSittingMailRescheduled:
		primaryKey = model.MailTemplateExamSittingRescheduled
	case store.ExamSittingMailCancelled:
		primaryKey = model.MailTemplateExamSittingCancelled
	case store.ExamSittingMailReconciled:
	default:
		return nil, errors.New("sitting mail change is invalid")
	}
	at, occurrenceID := model.TimeUTC(p.now()), model.NewMailOccurrenceID()
	occurrence := &model.MailOccurrence{ID: occurrenceID, Kind: model.MailOccurrenceSittingSchedule, TemplateKey: primaryKey,
		ActorUserID: actorID, CreatedAt: at}
	command, err := model.EncodeSittingMailExpansionCommand(model.SittingMailExpansionCommandV1{OccurrenceID: occurrenceID})
	if err != nil {
		return nil, err
	}
	dedupe, err := model.SittingMailExpansionDedupeKey(occurrenceID)
	if err != nil {
		return nil, err
	}
	job, err := model.NewJobWithDedupePolicy(model.NewJobID(), model.JobTypeMailExpandSitting, 1, command, dedupe,
		model.JobDedupePermanent, at, at, model.MailMaximumAttempts)
	if err != nil {
		return nil, err
	}
	prepared := &store.ExamSittingMailFanout{Occurrence: occurrence, ExpansionJob: job, ChangeKind: change, DeliveryLifetime: sittingDeliveryLifetime}
	if !p.sender.Enabled() {
		job, err = job.RequestCancellation(at)
		if err != nil {
			return nil, err
		}
		prepared.ExpansionJob = job
		return prepared, nil
	}
	from := p.sender.From()
	if err = ValidateAddress(from); err != nil {
		return nil, err
	}
	bundle := FrozenSittingBundleV1{Version: 1, From: from, Messages: make(map[model.MailTemplateKey]FrozenContent, 4)}
	for _, key := range []model.MailTemplateKey{model.MailTemplateExamSittingScheduled, model.MailTemplateExamSittingRescheduled,
		model.MailTemplateExamSittingCancelled, model.MailTemplateExamSittingAssignmentRemoved} {
		variant := details
		if key == model.MailTemplateExamSittingAssignmentRemoved && details.PriorClassDisplayName != "" {
			variant.ClassDisplayName = details.PriorClassDisplayName
		}
		rendered, renderErr := p.renderer.RenderSittingScheduleNotice(key, "", variant)
		if renderErr != nil || rendered.Subject == "" || rendered.Text == "" || rendered.HTML == "" {
			return nil, errors.New("render sitting mail bundle")
		}
		bundle.Messages[key] = rendered
	}
	plaintext, err := json.Marshal(bundle)
	if err != nil || len(plaintext) > model.MailRenderedPayloadMaximumBytes*4 {
		return nil, errors.New("sitting mail bundle is too large")
	}
	envelope, err := p.sealer.Seal(secretseal.Binding{Purpose: FanoutBundleSealingPurpose, Owner: occurrenceID.String()}, plaintext)
	if err != nil {
		return nil, err
	}
	encrypted, err := json.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	prepared.Bundle = &model.MailFanoutBundle{ID: occurrenceID, EncryptedPayload: encrypted, CreatedAt: at, Revision: 1}
	if prepared.Bundle.Validate() != nil {
		return nil, errors.New("prepared sitting mail bundle is invalid")
	}
	return prepared, nil
}

func (p *SittingComposer) OpenBundle(bundle *model.MailFanoutBundle) (FrozenSittingBundleV1, error) {
	var result FrozenSittingBundleV1
	if p == nil || p.sealer == nil || bundle == nil || bundle.Validate() != nil {
		return result, errors.New("sitting mail bundle is unavailable")
	}
	var envelope secretseal.Envelope
	if err := json.Unmarshal(bundle.EncryptedPayload, &envelope); err != nil {
		return result, err
	}
	plaintext, err := p.sealer.Open(secretseal.Binding{Purpose: FanoutBundleSealingPurpose, Owner: bundle.ID.String()}, envelope)
	if err != nil {
		return result, err
	}
	if err = json.Unmarshal(plaintext, &result); err != nil || result.Version != 1 || len(result.Messages) != 4 || ValidateAddress(result.From) != nil {
		return FrozenSittingBundleV1{}, errors.New("sitting mail bundle is invalid")
	}
	return result, nil
}

func (p *SittingComposer) PrepareRecipient(fanout *store.ExamSittingMailFanoutSnapshot, recipient *model.User,
	key model.MailTemplateKey, bundle FrozenSittingBundleV1,
) (*model.MailDelivery, *model.Job, error) {
	content, ok := bundle.Messages[key]
	if p == nil || p.sealer == nil || fanout == nil || fanout.Occurrence == nil || recipient == nil || !ok ||
		recipient.Validate() != nil || content.Subject == "" || content.Text == "" || content.HTML == "" {
		return nil, nil, errors.New("sitting mail recipient is invalid")
	}
	deliveryID, jobID := model.NewMailDeliveryID(), model.NewJobID()
	command, err := model.EncodeMailDeliveryCommand(model.MailDeliveryCommandV1{DeliveryID: deliveryID})
	if err != nil {
		return nil, nil, err
	}
	job, err := model.NewJob(jobID, model.JobTypeMailDeliver, 1, command, deliveryID.String(),
		fanout.Occurrence.CreatedAt, fanout.Occurrence.CreatedAt, model.MailMaximumAttempts)
	if err != nil {
		return nil, nil, err
	}
	payload := FrozenPayloadV1{Version: 1, RecipientName: recipient.DisplayName, RecipientAddress: recipient.Email,
		FromName: bundle.From.Name, FromAddress: bundle.From.Address, Subject: content.Subject, Text: content.Text, HTML: content.HTML,
		AutoSubmitted: "auto-generated", AutoResponseSuppress: "All"}
	plaintext, err := json.Marshal(payload)
	if err != nil || len(plaintext) > model.MailRenderedPayloadMaximumBytes {
		return nil, nil, errors.New("sitting mail recipient payload is invalid")
	}
	envelope, err := p.sealer.Seal(secretseal.Binding{Purpose: DeliverySealingPurpose, Owner: deliveryID.String()}, plaintext)
	if err != nil {
		return nil, nil, err
	}
	encrypted, err := json.Marshal(envelope)
	if err != nil {
		return nil, nil, err
	}
	delivery := &model.MailDelivery{ID: deliveryID, OccurrenceID: fanout.Occurrence.ID, JobID: jobID, TargetUserID: recipient.ID,
		TemplateKey: key, TemplateDigest: Digest(content.Subject, content.Text, content.HTML), MaskedRecipient: MaskAddress(recipient.Email),
		State: model.MailDeliveryQueued, CreatedAt: fanout.Occurrence.CreatedAt, UpdatedAt: fanout.Occurrence.CreatedAt,
		MessageDate: fanout.Occurrence.CreatedAt, Deadline: fanout.Deadline, MessageID: StableMessageID(deliveryID, bundle.From.Address),
		EncryptedPayload: encrypted, Revision: 1}
	if err = delivery.Validate(); err != nil {
		return nil, nil, err
	}
	return delivery, job, nil
}

func validSittingDetails(details SittingScheduleDetails) bool {
	for _, value := range []string{details.ExamTitle, details.ClassDisplayName} {
		if value != strings.TrimSpace(value) || value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > 255 {
			return false
		}
	}
	if details.PriorClassDisplayName != "" && (details.PriorClassDisplayName != strings.TrimSpace(details.PriorClassDisplayName) ||
		!utf8.ValidString(details.PriorClassDisplayName) || utf8.RuneCountInString(details.PriorClassDisplayName) > 255) {
		return false
	}
	return details.StartsAt.IsZero() && details.EndsAt.IsZero() || !details.StartsAt.IsZero() && details.StartsAt.Before(details.EndsAt)
}
