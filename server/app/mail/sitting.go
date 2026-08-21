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
)

type SittingScheduleDetails struct {
	ExamTitle             string
	ClassDisplayName      string
	PriorClassDisplayName string
	StartsAt              time.Time
	EndsAt                time.Time
}

func (SittingScheduleDetails) mailPresentation() {}

type SittingRenderer interface {
	Renderer
	SittingLocales() (string, []string)
}

type FrozenSittingBundleV1 struct {
	Version  int                                     `json:"version"`
	From     Address                                 `json:"from"`
	Messages map[model.MailTemplateKey]FrozenContent `json:"messages"`
}

// FrozenSittingBundleV2 freezes every supported locale for one template
// release. Expansion selects a recipient locale without consulting mutable
// runtime assets.
type FrozenSittingBundleV2 struct {
	Version       int                                                `json:"version"`
	From          Address                                            `json:"from"`
	DefaultLocale string                                             `json:"default_locale"`
	Messages      map[string]map[model.MailTemplateKey]FrozenContent `json:"messages"`
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
	deliveryLifetime, ok := defaultLifetimeFor(primaryKey)
	if !ok {
		return nil, errors.New("sitting mail definition is invalid")
	}
	prepared := &store.ExamSittingMailFanout{Occurrence: occurrence, ExpansionJob: job, ChangeKind: change, DeliveryLifetime: deliveryLifetime}
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
	defaultLocale, locales := p.renderer.SittingLocales()
	locales = normalizedSittingLocales(defaultLocale, locales)
	if len(locales) == 0 || len(locales) > 64 {
		return nil, errors.New("sitting mail locales are invalid")
	}
	bundle := FrozenSittingBundleV2{Version: 2, From: from, DefaultLocale: normalizeSittingLocale(defaultLocale),
		Messages: make(map[string]map[model.MailTemplateKey]FrozenContent, len(locales))}
	for _, locale := range locales {
		messages := make(map[model.MailTemplateKey]FrozenContent, 4)
		for _, key := range sittingTemplateKeys() {
			variant := details
			if key == model.MailTemplateExamSittingAssignmentRemoved && details.PriorClassDisplayName != "" {
				variant.ClassDisplayName = details.PriorClassDisplayName
			}
			rendered, renderErr := p.renderer.Render(RenderRequest{Key: key, Locale: locale, Presentation: variant})
			if renderErr != nil || rendered.Subject == "" || rendered.Text == "" || rendered.HTML == "" {
				return nil, errors.New("render sitting mail bundle")
			}
			messages[key] = rendered
		}
		bundle.Messages[locale] = messages
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

func (p *SittingComposer) OpenBundle(bundle *model.MailFanoutBundle) (FrozenSittingBundleV2, error) {
	var result FrozenSittingBundleV2
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
	var header struct {
		Version int `json:"version"`
	}
	if err = json.Unmarshal(plaintext, &header); err != nil {
		return FrozenSittingBundleV2{}, errors.New("sitting mail bundle is invalid")
	}
	if header.Version == 1 {
		var legacy FrozenSittingBundleV1
		if err = json.Unmarshal(plaintext, &legacy); err != nil || len(legacy.Messages) != 4 || ValidateAddress(legacy.From) != nil {
			return FrozenSittingBundleV2{}, errors.New("sitting mail bundle is invalid")
		}
		return FrozenSittingBundleV2{Version: 2, From: legacy.From, DefaultLocale: model.DefaultLocale,
			Messages: map[string]map[model.MailTemplateKey]FrozenContent{model.DefaultLocale: legacy.Messages}}, nil
	}
	if err = json.Unmarshal(plaintext, &result); err != nil || !validFrozenSittingBundle(result) {
		return FrozenSittingBundleV2{}, errors.New("sitting mail bundle is invalid")
	}
	return result, nil
}

func (p *SittingComposer) PrepareRecipient(fanout *store.ExamSittingMailFanoutSnapshot, recipient *model.User,
	key model.MailTemplateKey, bundle FrozenSittingBundleV2,
) (*model.MailDelivery, *model.Job, error) {
	definition, defined := definitionFor(key)
	content, ok := bundle.contentFor(recipient.Locale, key)
	if p == nil || p.sealer == nil || fanout == nil || fanout.Occurrence == nil || recipient == nil || !defined ||
		definition.presentation != presentationSittingSchedule || !ok ||
		recipient.Validate() != nil || content.Subject == "" || content.Text == "" || content.HTML == "" {
		return nil, nil, errors.New("sitting mail recipient is invalid")
	}
	deliveryID, jobID := model.NewMailDeliveryID(), model.NewJobID()
	command, err := model.EncodeMailDeliveryCommand(model.MailDeliveryCommandV1{DeliveryID: deliveryID})
	if err != nil {
		return nil, nil, err
	}
	job, err := model.NewJob(jobID, definition.jobType, 1, command, deliveryID.String(),
		fanout.Occurrence.CreatedAt, fanout.Occurrence.CreatedAt, model.MailMaximumAttempts)
	if err != nil {
		return nil, nil, err
	}
	frozen, err := freezeDeliveryPayload(p.sealer, deliveryID, bundle.From,
		Address{Name: recipient.DisplayName, Address: recipient.Email}, content)
	if err != nil {
		return nil, nil, err
	}
	delivery := &model.MailDelivery{ID: deliveryID, OccurrenceID: fanout.Occurrence.ID, JobID: jobID, TargetUserID: recipient.ID,
		TemplateKey: key, TemplateDigest: frozen.templateDigest, MaskedRecipient: MaskAddress(recipient.Email),
		State: model.MailDeliveryQueued, CreatedAt: fanout.Occurrence.CreatedAt, UpdatedAt: fanout.Occurrence.CreatedAt,
		MessageDate: fanout.Occurrence.CreatedAt, Deadline: fanout.Deadline, MessageID: frozen.messageID,
		EncryptedPayload: frozen.encrypted, Revision: 1}
	if err = delivery.Validate(); err != nil {
		return nil, nil, err
	}
	return delivery, job, nil
}

func sittingTemplateKeys() []model.MailTemplateKey {
	return []model.MailTemplateKey{model.MailTemplateExamSittingScheduled, model.MailTemplateExamSittingRescheduled,
		model.MailTemplateExamSittingCancelled, model.MailTemplateExamSittingAssignmentRemoved}
}

func normalizeSittingLocale(locale string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(locale), "_", "-"))
}

func normalizedSittingLocales(defaultLocale string, locales []string) []string {
	seen := make(map[string]struct{}, len(locales)+2)
	result := make([]string, 0, len(locales)+2)
	values := append([]string(nil), locales...)
	values = append(values, defaultLocale, model.DefaultLocale)
	for _, raw := range values {
		locale := normalizeSittingLocale(raw)
		if locale == "" {
			continue
		}
		if _, exists := seen[locale]; exists {
			continue
		}
		seen[locale] = struct{}{}
		result = append(result, locale)
	}
	return result
}

func validFrozenSittingBundle(bundle FrozenSittingBundleV2) bool {
	if bundle.Version != 2 || ValidateAddress(bundle.From) != nil || bundle.DefaultLocale == "" ||
		len(bundle.Messages) == 0 || len(bundle.Messages) > 64 {
		return false
	}
	if _, ok := bundle.Messages[bundle.DefaultLocale]; !ok {
		return false
	}
	for locale, messages := range bundle.Messages {
		if locale != normalizeSittingLocale(locale) || len(messages) != len(sittingTemplateKeys()) {
			return false
		}
		for _, key := range sittingTemplateKeys() {
			content, ok := messages[key]
			if !ok || content.Subject == "" || content.Text == "" || content.HTML == "" {
				return false
			}
		}
	}
	return true
}

func (bundle FrozenSittingBundleV2) contentFor(requestedLocale string, key model.MailTemplateKey) (FrozenContent, bool) {
	requested := normalizeSittingLocale(requestedLocale)
	locales := make([]string, 0, 4)
	if requested != "" {
		locales = append(locales, requested)
		if base, _, found := strings.Cut(requested, "-"); found {
			locales = append(locales, base)
		}
	}
	for _, fallback := range []string{bundle.DefaultLocale, model.DefaultLocale} {
		fallback = normalizeSittingLocale(fallback)
		if fallback != "" {
			locales = append(locales, fallback)
		}
	}
	seen := make(map[string]struct{}, len(locales))
	for _, locale := range locales {
		if _, exists := seen[locale]; exists {
			continue
		}
		seen[locale] = struct{}{}
		if messages, ok := bundle.Messages[locale]; ok {
			content, exists := messages[key]
			return content, exists
		}
	}
	return FrozenContent{}, false
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
