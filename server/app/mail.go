// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/mail"
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/secretseal"
	"github.com/sudosylabs/proctor/server/store"
)

const (
	mailDeliverySealingPurpose = "mail.delivery"
	mailTestRateLimitWindow    = time.Hour
	mailTestRateLimitMaximum   = 3
)

type FrozenMailContent struct{ Subject, Text, HTML string }

type MailAddress struct {
	Name    string
	Address string
}

type OutboundMail struct {
	From         MailAddress
	EnvelopeFrom string
	To           MailAddress
	Subject      string
	Text         string
	HTML         string
	Headers      map[string][]string
	MessageID    string
	Date         time.Time
}

type MailTransportOutcome string

const (
	MailTransportUnknown             MailTransportOutcome = "unknown"
	MailTransportTemporary           MailTransportOutcome = "temporary"
	MailTransportPermanent           MailTransportOutcome = "permanent"
	MailTransportAcceptanceUncertain MailTransportOutcome = "acceptance_uncertain"
)

type MailTemplateRenderer interface {
	RenderSystemMailTest(recipientLocale, installationLocale string) (FrozenMailContent, error)
}

type MailDeliverySender interface {
	Enabled() bool
	From() MailAddress
	Send(context.Context, OutboundMail) (MailTransportOutcome, error)
}

type frozenMailPayloadV1 struct {
	Version              int    `json:"version"`
	RecipientName        string `json:"recipient_name"`
	RecipientAddress     string `json:"recipient_address"`
	FromName             string `json:"from_name"`
	FromAddress          string `json:"from_address"`
	Subject              string `json:"subject"`
	Text                 string `json:"text"`
	HTML                 string `json:"html"`
	AutoSubmitted        string `json:"auto_submitted"`
	AutoResponseSuppress string `json:"auto_response_suppress"`
}

type MailDeliveryView struct {
	ID                                          model.MailDeliveryID
	OccurrenceID                                model.MailOccurrenceID
	TargetUserID                                model.UserID
	TemplateKey                                 model.MailTemplateKey
	TemplateDigest                              string
	MaskedRecipient                             string
	State                                       model.MailDeliveryState
	CreatedAt, UpdatedAt, MessageDate, Deadline time.Time
	MessageID                                   string
	AttemptCount                                int
	AcceptedAt, FailedAt                        model.OptionalTime
	PublicFailureCode                           string
}

type mailStore interface {
	EnqueueTest(context.Context, *store.MailTestEnqueue) (*model.MailDelivery, error)
	GetDelivery(context.Context, model.MailDeliveryID) (*model.MailDelivery, error)
}

type mailUserStore interface {
	Get(context.Context, string) (*model.User, error)
}

type mailAuthorizer interface {
	Authorize(context.Context, Invocation, model.Action) (model.Resource, error)
}

type mailInstitutionStore interface {
	GetSingleton(context.Context) (*model.Institution, error)
}

type mailAuthorizationAdapter struct {
	authorization *accessControlService
	institutions  mailInstitutionStore
}

func (a mailAuthorizationAdapter) Authorize(ctx context.Context, invocation Invocation, action model.Action) (model.Resource, error) {
	if a.authorization == nil || a.institutions == nil {
		return model.Resource{}, NewError("mail.unavailable")
	}
	institution, err := a.institutions.GetSingleton(ctx)
	if err != nil || institution == nil || !institution.ID.IsValid() {
		return model.Resource{}, NewError("mail.unavailable").Wrap(err)
	}
	resource := model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}
	if err = a.authorization.authorizeCurrentState(ctx, invocation.Principal(), action, resource, invocation.RequestMetadata()); err != nil {
		return model.Resource{}, err
	}
	return resource, nil
}

type mailAuditPreparer interface {
	PrepareTest(context.Context, Invocation, model.Resource, model.MailDeliveryID) (*model.AuditEvent, error)
}

type mailService struct {
	mail                    mailStore
	users                   mailUserStore
	authorization           mailAuthorizer
	audit                   mailAuditPreparer
	attempts                *authenticationAttemptAccounting
	renderer                MailTemplateRenderer
	sender                  MailDeliverySender
	sealer                  *secretseal.Sealer
	recentAuthenticationTTL time.Duration
	now                     func() time.Time
	wake                    func()
}

func newMailService(mailStore mailStore, users mailUserStore, authorization mailAuthorizer, audit mailAuditPreparer, attempts *authenticationAttemptAccounting, renderer MailTemplateRenderer, sender MailDeliverySender, sealer *secretseal.Sealer, recentTTL time.Duration, now func() time.Time) (*mailService, error) {
	if mailStore == nil || users == nil || authorization == nil || audit == nil || attempts == nil || renderer == nil || sender == nil || now == nil || recentTTL <= 0 {
		return nil, errors.New("mail service dependencies are invalid")
	}
	if sender.Enabled() && sealer == nil {
		return nil, errors.New("enabled mail requires secret sealing")
	}
	return &mailService{mail: mailStore, users: users, authorization: authorization, audit: audit, attempts: attempts, renderer: renderer, sender: sender, sealer: sealer, recentAuthenticationTTL: recentTTL, now: now}, nil
}

func (a *App) SendTestMail(ctx context.Context, invocation Invocation) (MailDeliveryView, error) {
	if a == nil || a.mail == nil {
		return MailDeliveryView{}, NewError("mail.unavailable")
	}
	return a.mail.SendTest(ctx, invocation)
}

func (a *App) GetMailDelivery(ctx context.Context, invocation Invocation, id model.MailDeliveryID) (MailDeliveryView, error) {
	if a == nil || a.mail == nil {
		return MailDeliveryView{}, NewError("mail.unavailable")
	}
	return a.mail.Get(ctx, invocation, id)
}

func (s *mailService) SendTest(ctx context.Context, invocation Invocation) (MailDeliveryView, error) {
	principal := invocation.Principal()
	if principal.Validate() != nil || principal.CredentialType != model.CredentialSessionAccess {
		return MailDeliveryView{}, invalidTokenAppError()
	}
	if !principal.IsRecentlyAuthenticated(s.now(), s.recentAuthenticationTTL) {
		return MailDeliveryView{}, NewError("authentication.reauthentication_required")
	}
	institutionResource, err := s.authorization.Authorize(ctx, invocation, model.ActionMailManage)
	if err != nil {
		return MailDeliveryView{}, err
	}
	if err = s.checkTestRateLimit(ctx, principal.UserID); err != nil {
		return MailDeliveryView{}, err
	}
	if !s.sender.Enabled() || s.sealer == nil {
		return MailDeliveryView{}, NewError("mail.unavailable")
	}
	user, err := s.users.Get(ctx, principal.UserID.String())
	if err != nil {
		return MailDeliveryView{}, mailAppError(err)
	}
	if user == nil || user.Validate() != nil || user.ID != principal.UserID {
		return MailDeliveryView{}, NewError("mail.unavailable")
	}
	if !user.EmailVerified {
		return MailDeliveryView{}, NewError("mail.recipient_unverified")
	}
	if !user.IsActive() {
		return MailDeliveryView{}, NewError("mail.recipient_ineligible")
	}
	rendered, err := s.renderer.RenderSystemMailTest(user.Locale, model.DefaultLocale)
	if err != nil {
		return MailDeliveryView{}, NewError("mail.unavailable").Wrap(err)
	}
	templateDigest := digestRenderedMail(rendered.Subject, rendered.Text, rendered.HTML)
	from := s.sender.From()
	if err = validateMailAddress(from); err != nil {
		return MailDeliveryView{}, NewError("mail.unavailable").Wrap(err)
	}
	now := model.TimeUTC(s.now())
	occurrenceID, deliveryID, jobID := model.NewMailOccurrenceID(), model.NewMailDeliveryID(), model.NewJobID()
	payload := frozenMailPayloadV1{Version: 1, RecipientName: user.DisplayName, RecipientAddress: user.Email, FromName: from.Name, FromAddress: from.Address, Subject: rendered.Subject, Text: rendered.Text, HTML: rendered.HTML, AutoSubmitted: "auto-generated", AutoResponseSuppress: "All"}
	plaintext, err := json.Marshal(payload)
	if err != nil || len(plaintext) > model.MailRenderedPayloadMaximumBytes {
		return MailDeliveryView{}, NewError("mail.unavailable").Wrap(err)
	}
	envelope, err := s.sealer.Seal(secretseal.Binding{Purpose: mailDeliverySealingPurpose, Owner: deliveryID.String()}, plaintext)
	if err != nil {
		return MailDeliveryView{}, NewError("mail.unavailable").Wrap(err)
	}
	encrypted, err := json.Marshal(envelope)
	if err != nil {
		return MailDeliveryView{}, NewError("mail.unavailable").Wrap(err)
	}
	command, err := model.EncodeMailDeliveryCommand(model.MailDeliveryCommandV1{DeliveryID: deliveryID})
	if err != nil {
		return MailDeliveryView{}, NewError("mail.unavailable").Wrap(err)
	}
	job, err := model.NewJob(jobID, model.JobTypeMailDeliver, 1, command, deliveryID.String(), now, now, model.MailMaximumAttempts)
	if err != nil {
		return MailDeliveryView{}, NewError("mail.unavailable").Wrap(err)
	}
	delivery := &model.MailDelivery{ID: deliveryID, OccurrenceID: occurrenceID, JobID: jobID, TargetUserID: user.ID, TemplateKey: model.MailTemplateSystemTest, TemplateDigest: templateDigest, MaskedRecipient: maskMailAddress(user.Email), State: model.MailDeliveryQueued, CreatedAt: now, UpdatedAt: now, MessageDate: now, Deadline: now.Add(24 * time.Hour), MessageID: stableMailMessageID(deliveryID, from.Address), EncryptedPayload: encrypted, Revision: 1}
	if err = delivery.Validate(); err != nil {
		return MailDeliveryView{}, NewError("mail.unavailable").Wrap(err)
	}
	audit, err := s.audit.PrepareTest(ctx, invocation, institutionResource, deliveryID)
	if err != nil {
		return MailDeliveryView{}, err
	}
	created, err := s.mail.EnqueueTest(ctx, &store.MailTestEnqueue{Occurrence: &model.MailOccurrence{ID: occurrenceID, Kind: model.MailOccurrenceOperatorTest, TemplateKey: model.MailTemplateSystemTest, ActorUserID: user.ID, CreatedAt: now}, Delivery: delivery, Job: job, AuditEvent: audit})
	if err != nil {
		return MailDeliveryView{}, mailAppError(err)
	}
	if s.wake != nil {
		s.wake()
	}
	return mailDeliveryView(created), nil
}

func validateMailAddress(address MailAddress) error {
	if address.Address == "" || strings.ContainsAny(address.Name+address.Address, "\x00\r\n") {
		return errors.New("mail address is invalid")
	}
	parsed, err := mail.ParseAddress(address.Address)
	if err != nil || parsed.Address != address.Address {
		return errors.New("mail address is invalid")
	}
	return nil
}

func (s *mailService) Get(ctx context.Context, invocation Invocation, id model.MailDeliveryID) (MailDeliveryView, error) {
	principal := invocation.Principal()
	if principal.Validate() != nil {
		return MailDeliveryView{}, invalidTokenAppError()
	}
	if _, err := s.authorization.Authorize(ctx, invocation, model.ActionMailView); err != nil {
		return MailDeliveryView{}, err
	}
	if !id.IsValid() {
		return MailDeliveryView{}, NewError("request.invalid")
	}
	delivery, err := s.mail.GetDelivery(ctx, id)
	if err != nil {
		return MailDeliveryView{}, mailAppError(err)
	}
	return mailDeliveryView(delivery), nil
}

func (s *mailService) checkTestRateLimit(ctx context.Context, userID model.UserID) error {
	_, limited, err := s.attempts.account(ctx, authenticationAttemptIntent{purpose: authenticationAttemptPurposeMailTest, window: mailTestRateLimitWindow, limits: []authenticationAttemptLimit{{dimension: authenticationAttemptDimensionIdentity, maximum: mailTestRateLimitMaximum, identity: userID.String()}}})
	if err != nil {
		return NewError("mail.unavailable").Wrap(err)
	}
	if limited {
		return NewError("mail.test.rate_limited")
	}
	return nil
}

func stableMailMessageID(id model.MailDeliveryID, from string) string {
	domain := "localhost"
	if parsed, err := mail.ParseAddress(from); err == nil {
		if at := strings.LastIndexByte(parsed.Address, '@'); at >= 0 {
			domain = parsed.Address[at+1:]
		}
	}
	return "<mail." + id.String() + "@" + strings.ToLower(domain) + ">"
}

func maskMailAddress(address string) string {
	at := strings.LastIndexByte(address, '@')
	if at < 1 {
		return "***"
	}
	local := []rune(address[:at])
	prefix := "***"
	if len(local) > 1 {
		prefix = string(local[0]) + strings.Repeat("*", min(3, len(local)-1))
	}
	return prefix + address[at:]
}

func mailDeliveryView(delivery *model.MailDelivery) MailDeliveryView {
	if delivery == nil {
		return MailDeliveryView{}
	}
	return MailDeliveryView{ID: delivery.ID, OccurrenceID: delivery.OccurrenceID, TargetUserID: delivery.TargetUserID, TemplateKey: delivery.TemplateKey, TemplateDigest: delivery.TemplateDigest, MaskedRecipient: delivery.MaskedRecipient, State: delivery.State, CreatedAt: delivery.CreatedAt, UpdatedAt: delivery.UpdatedAt, MessageDate: delivery.MessageDate, Deadline: delivery.Deadline, MessageID: delivery.MessageID, AttemptCount: delivery.AttemptCount, AcceptedAt: delivery.AcceptedAt, FailedAt: delivery.FailedAt, PublicFailureCode: delivery.PublicFailureCode}
}

func digestRenderedMail(subject, text, html string) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(subject))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(text))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(html))
	return hex.EncodeToString(hash.Sum(nil))
}

func mailAppError(err error) error {
	switch {
	case store.IsNotFound(err):
		return NewError("resource.not_found").Wrap(err)
	case store.IsConflict(err):
		return NewError("mail.conflict").Wrap(err)
	default:
		return NewError("mail.unavailable").Wrap(err)
	}
}
