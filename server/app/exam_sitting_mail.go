// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	examsitting "github.com/sudosylabs/proctor/server/app/exam/sitting"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/secretseal"
	"github.com/sudosylabs/proctor/server/store"
)

const sittingMailDeliveryLifetime = 72 * time.Hour

type SittingScheduleMailDetails struct {
	ExamTitle             string
	ClassDisplayName      string
	PriorClassDisplayName string
	StartsAt              time.Time
	EndsAt                time.Time
}

type SittingMailTemplateRenderer interface {
	RenderSittingScheduleNotice(model.MailTemplateKey, string, string, SittingScheduleMailDetails) (FrozenMailContent, error)
}

type frozenSittingMailBundleV1 struct {
	Version  int                                         `json:"version"`
	From     MailAddress                                 `json:"from"`
	Messages map[model.MailTemplateKey]FrozenMailContent `json:"messages"`
}

type sittingMailPreparer struct {
	renderer SittingMailTemplateRenderer
	sender   MailDeliverySender
	sealer   *secretseal.Sealer
	now      func() time.Time
}

type sittingScheduleMailPreparationAdapter struct {
	preparer  *sittingMailPreparer
	revisions store.ExamRevisionStore
	classes   store.ClassStore
}

func (adapter sittingScheduleMailPreparationAdapter) Prepare(ctx context.Context,
	request examsitting.ScheduleMailRequest,
) (*store.ExamSittingMailFanout, error) {
	if adapter.preparer == nil || adapter.revisions == nil || adapter.classes == nil {
		return nil, errors.New("Sitting schedule mail preparation is unavailable")
	}
	revision, err := adapter.revisions.GetSummary(ctx, request.ExamID, request.ExamRevisionID)
	if err != nil {
		return nil, err
	}
	class, err := adapter.classes.Get(ctx, request.ClassID.String())
	if err != nil {
		return nil, err
	}
	priorClassName := class.DisplayName
	if request.PriorClassID.IsValid() && request.PriorClassID != request.ClassID {
		priorClass, priorErr := adapter.classes.Get(ctx, request.PriorClassID.String())
		if priorErr != nil {
			return nil, priorErr
		}
		priorClassName = priorClass.DisplayName
	}
	sitting := &model.ExamSitting{ID: request.SittingID, ExamID: request.ExamID, ExamRevisionID: request.ExamRevisionID,
		ClassID: request.ClassID, ScheduledStartAt: request.StartsAt, ScheduledEndAt: request.EndsAt,
		State: model.ExamSittingScheduled, CreatedAt: adapter.preparer.now(), UpdatedAt: adapter.preparer.now(), Revision: request.SittingRevision}
	if request.ChangeKind == store.ExamSittingMailCancelled {
		sitting.State = model.ExamSittingCanceled
		sitting.ReasonCode = model.ExamSittingReasonManagerCanceled
	}
	return adapter.preparer.Prepare(request.ActorUserID, sitting, request.ChangeKind, SittingScheduleMailDetails{
		ExamTitle: revision.Title, ClassDisplayName: class.DisplayName, PriorClassDisplayName: priorClassName,
		StartsAt: request.StartsAt, EndsAt: request.EndsAt,
	})
}

func newSittingMailPreparer(renderer SittingMailTemplateRenderer, sender MailDeliverySender, sealer *secretseal.Sealer,
	now func() time.Time,
) (*sittingMailPreparer, error) {
	if renderer == nil || sender == nil || now == nil || sender.Enabled() && sealer == nil {
		return nil, errors.New("Sitting mail preparer dependencies are invalid")
	}
	return &sittingMailPreparer{renderer: renderer, sender: sender, sealer: sealer, now: now}, nil
}

func (p *sittingMailPreparer) Prepare(actorID model.UserID, sitting *model.ExamSitting,
	change store.ExamSittingMailChangeKind, details SittingScheduleMailDetails,
) (*store.ExamSittingMailFanout, error) {
	if p == nil || !actorID.IsValid() || sitting == nil || !sitting.ID.IsValid() || !sitting.ExamID.IsValid() ||
		!sitting.ExamRevisionID.IsValid() || !sitting.ClassID.IsValid() || sitting.Revision < 1 ||
		!sitting.ScheduledStartAt.Before(sitting.ScheduledEndAt) || !validSittingScheduleMailDetails(details) {
		return nil, errors.New("Sitting mail preparation is invalid")
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
		return nil, errors.New("Sitting mail change is invalid")
	}
	at := model.TimeUTC(p.now())
	occurrenceID := model.NewMailOccurrenceID()
	occurrence := &model.MailOccurrence{ID: occurrenceID, Kind: model.MailOccurrenceSittingSchedule,
		TemplateKey: primaryKey, ActorUserID: actorID, CreatedAt: at}
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
	prepared := &store.ExamSittingMailFanout{Occurrence: occurrence, ExpansionJob: job, ChangeKind: change,
		DeliveryLifetime: sittingMailDeliveryLifetime}
	if !p.sender.Enabled() {
		job, err = job.RequestCancellation(at)
		if err != nil {
			return nil, err
		}
		prepared.ExpansionJob = job
		return prepared, nil
	}
	from := p.sender.From()
	if err = validateMailAddress(from); err != nil {
		return nil, err
	}
	bundle := frozenSittingMailBundleV1{Version: 1, From: from, Messages: make(map[model.MailTemplateKey]FrozenMailContent, 4)}
	for _, key := range []model.MailTemplateKey{model.MailTemplateExamSittingScheduled, model.MailTemplateExamSittingRescheduled,
		model.MailTemplateExamSittingCancelled, model.MailTemplateExamSittingAssignmentRemoved} {
		variant := details
		if key == model.MailTemplateExamSittingAssignmentRemoved && details.PriorClassDisplayName != "" {
			variant.ClassDisplayName = details.PriorClassDisplayName
		}
		rendered, renderErr := p.renderer.RenderSittingScheduleNotice(key, model.DefaultLocale, model.DefaultLocale, variant)
		if renderErr != nil || rendered.Subject == "" || rendered.Text == "" || rendered.HTML == "" {
			return nil, errors.New("render Sitting mail bundle")
		}
		bundle.Messages[key] = rendered
	}
	plaintext, err := json.Marshal(bundle)
	if err != nil || len(plaintext) > model.MailRenderedPayloadMaximumBytes*4 {
		return nil, errors.New("Sitting mail bundle is too large")
	}
	envelope, err := p.sealer.Seal(secretseal.Binding{Purpose: mailFanoutBundleSealPurpose, Owner: occurrenceID.String()}, plaintext)
	if err != nil {
		return nil, err
	}
	encrypted, err := json.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	prepared.Bundle = &model.MailFanoutBundle{ID: occurrenceID, EncryptedPayload: encrypted, CreatedAt: at, Revision: 1}
	if prepared.Bundle.Validate() != nil {
		return nil, errors.New("prepared Sitting mail bundle is invalid")
	}
	return prepared, nil
}

func (p *sittingMailPreparer) open(bundle *model.MailFanoutBundle) (frozenSittingMailBundleV1, error) {
	var result frozenSittingMailBundleV1
	if p == nil || p.sealer == nil || bundle == nil || bundle.Validate() != nil {
		return result, errors.New("Sitting mail bundle is unavailable")
	}
	var envelope secretseal.Envelope
	if err := json.Unmarshal(bundle.EncryptedPayload, &envelope); err != nil {
		return result, err
	}
	plaintext, err := p.sealer.Open(secretseal.Binding{Purpose: mailFanoutBundleSealPurpose, Owner: bundle.ID.String()}, envelope)
	if err != nil {
		return result, err
	}
	if err = json.Unmarshal(plaintext, &result); err != nil || result.Version != 1 || len(result.Messages) != 4 || validateMailAddress(result.From) != nil {
		return frozenSittingMailBundleV1{}, errors.New("Sitting mail bundle is invalid")
	}
	return result, nil
}

func (p *sittingMailPreparer) prepareRecipient(fanout *store.ExamSittingMailFanoutSnapshot, recipient *model.User,
	key model.MailTemplateKey, bundle frozenSittingMailBundleV1,
) (*model.MailDelivery, *model.Job, error) {
	content, ok := bundle.Messages[key]
	if p == nil || p.sealer == nil || fanout == nil || fanout.Occurrence == nil || recipient == nil || !ok ||
		recipient.Validate() != nil || content.Subject == "" || content.Text == "" || content.HTML == "" {
		return nil, nil, errors.New("Sitting mail recipient is invalid")
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
	payload := frozenMailPayloadV1{Version: 1, RecipientName: recipient.DisplayName, RecipientAddress: recipient.Email,
		FromName: bundle.From.Name, FromAddress: bundle.From.Address, Subject: content.Subject, Text: content.Text, HTML: content.HTML,
		AutoSubmitted: "auto-generated", AutoResponseSuppress: "All"}
	plaintext, err := json.Marshal(payload)
	if err != nil || len(plaintext) > model.MailRenderedPayloadMaximumBytes {
		return nil, nil, errors.New("Sitting mail recipient payload is invalid")
	}
	envelope, err := p.sealer.Seal(secretseal.Binding{Purpose: mailDeliverySealingPurpose, Owner: deliveryID.String()}, plaintext)
	if err != nil {
		return nil, nil, err
	}
	encrypted, err := json.Marshal(envelope)
	if err != nil {
		return nil, nil, err
	}
	delivery := &model.MailDelivery{ID: deliveryID, OccurrenceID: fanout.Occurrence.ID, JobID: jobID,
		TargetUserID: recipient.ID, TemplateKey: key, TemplateDigest: digestRenderedMail(content.Subject, content.Text, content.HTML),
		MaskedRecipient: maskMailAddress(recipient.Email), State: model.MailDeliveryQueued,
		CreatedAt: fanout.Occurrence.CreatedAt, UpdatedAt: fanout.Occurrence.CreatedAt, MessageDate: fanout.Occurrence.CreatedAt,
		Deadline: fanout.Deadline, MessageID: stableMailMessageID(deliveryID, bundle.From.Address),
		EncryptedPayload: encrypted, Revision: 1}
	if err = delivery.Validate(); err != nil {
		return nil, nil, err
	}
	return delivery, job, nil
}

func validSittingScheduleMailDetails(details SittingScheduleMailDetails) bool {
	for _, value := range []string{details.ExamTitle, details.ClassDisplayName} {
		if value != strings.TrimSpace(value) || value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > 255 {
			return false
		}
	}
	if details.PriorClassDisplayName != "" && (details.PriorClassDisplayName != strings.TrimSpace(details.PriorClassDisplayName) ||
		!utf8.ValidString(details.PriorClassDisplayName) || utf8.RuneCountInString(details.PriorClassDisplayName) > 255) {
		return false
	}
	return (details.StartsAt.IsZero() && details.EndsAt.IsZero()) ||
		(!details.StartsAt.IsZero() && details.StartsAt.Before(details.EndsAt))
}
