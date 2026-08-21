// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"time"

	appmail "github.com/sudosylabs/proctor/server/app/mail"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/secretseal"
	"github.com/sudosylabs/proctor/server/store"
)

const (
	mailDeliverySealingPurpose = appmail.DeliverySealingPurpose
	mailTestRateLimitWindow    = time.Hour
	mailTestRateLimitMaximum   = 3
)

type FrozenMailContent = appmail.FrozenContent
type DirectMailPreparation = appmail.DirectPreparation
type MailAddress = appmail.Address
type OutboundMail = appmail.Outbound
type MailTransportOutcome = appmail.TransportOutcome
type PersonalAccessTokenMailDetails = appmail.PersonalAccessTokenDetails
type ExamManagerMailDetails = appmail.ExamManagerDetails
type ClassTransitionMailDetails = appmail.ClassTransitionDetails
type SubmissionReceiptMailDetails = appmail.SubmissionReceiptDetails
type ResultReleaseMailDetails = appmail.ResultReleaseDetails
type DirectMailTemplateRenderer = appmail.Renderer
type MailDeliverySender = appmail.Sender
type frozenMailPayloadV1 = appmail.FrozenPayloadV1
type directMailPreparer = appmail.Composer

const (
	MailTransportUnknown             = appmail.TransportUnknown
	MailTransportTemporary           = appmail.TransportTemporary
	MailTransportPermanent           = appmail.TransportPermanent
	MailTransportAcceptanceUncertain = appmail.TransportAcceptanceUncertain
)

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

type MailDeliveryPage struct{ Items []MailDeliveryView }

type ListMailDeliveriesQuery struct {
	States          []model.MailDeliveryState
	TemplateKeys    []model.MailTemplateKey
	CreatedAfter    time.Time
	CreatedBefore   time.Time
	BeforeCreatedAt time.Time
	BeforeID        model.MailDeliveryID
	Limit           int
}

type mailStore interface {
	EnqueueTest(context.Context, *store.MailTestEnqueue) (*model.MailDelivery, error)
	GetDelivery(context.Context, model.MailDeliveryID) (*model.MailDelivery, error)
	ListDeliveries(context.Context, store.MailDeliveryListOptions) ([]*model.MailDelivery, error)
	CancelDelivery(context.Context, *store.MailDeliveryMutation) (*model.MailDelivery, error)
	RetryDelivery(context.Context, *store.MailDeliveryMutation) (*model.MailDelivery, error)
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
	PrepareControl(context.Context, Invocation, model.Resource, *model.MailDelivery, string) (string, error)
	Fail(context.Context, string, string) error
}

type mailService struct {
	mail                    mailStore
	rekey                   mailRekeyStarter
	keyState                mailKeyInspector
	rekeyJobs               mailRekeyJobReader
	rekeyAudit              mutationAuditor
	users                   mailUserStore
	authorization           mailAuthorizer
	audit                   mailAuditPreparer
	attempts                *authenticationAttemptAccounting
	composer                *directMailPreparer
	sender                  MailDeliverySender
	metrics                 MailDeliveryRecorder
	sealer                  *secretseal.Sealer
	recentAuthenticationTTL time.Duration
	now                     func() time.Time
	wake                    func()
}

func newDirectMailPreparer(renderer DirectMailTemplateRenderer, sender MailDeliverySender, sealer *secretseal.Sealer) (*directMailPreparer, error) {
	return appmail.NewComposer(renderer, sender, sealer)
}

func newMailService(mailStore mailStore, users mailUserStore, authorization mailAuthorizer, audit mailAuditPreparer, attempts *authenticationAttemptAccounting, renderer DirectMailTemplateRenderer, sender MailDeliverySender, metrics MailDeliveryRecorder, sealer *secretseal.Sealer, recentTTL time.Duration, now func() time.Time) (*mailService, error) {
	if mailStore == nil || users == nil || authorization == nil || audit == nil || attempts == nil || renderer == nil || sender == nil || metrics == nil || now == nil || recentTTL <= 0 {
		return nil, errors.New("mail service dependencies are invalid")
	}
	if sender.Enabled() && sealer == nil {
		return nil, errors.New("enabled mail requires secret sealing")
	}
	composer, err := newDirectMailPreparer(renderer, sender, sealer)
	if err != nil {
		return nil, err
	}
	rekey, _ := mailStore.(mailRekeyStarter)
	keyState, _ := mailStore.(mailKeyInspector)
	rekeyAudit, _ := audit.(mutationAuditor)
	return &mailService{mail: mailStore, rekey: rekey, keyState: keyState, rekeyAudit: rekeyAudit, users: users,
		authorization: authorization, audit: audit, attempts: attempts, composer: composer,
		sender: sender, metrics: metrics, sealer: sealer, recentAuthenticationTTL: recentTTL, now: now}, nil
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

func (a *App) ListMailDeliveries(ctx context.Context, invocation Invocation, query ListMailDeliveriesQuery) (MailDeliveryPage, error) {
	if a == nil || a.mail == nil {
		return MailDeliveryPage{}, NewError("mail.unavailable")
	}
	return a.mail.List(ctx, invocation, query)
}

func (a *App) GetMailMetrics(ctx context.Context, invocation Invocation) (MailMetricsSnapshot, error) {
	if a == nil || a.mail == nil {
		return MailMetricsSnapshot{}, NewError("mail.unavailable")
	}
	return a.mail.Metrics(ctx, invocation)
}

func (s *mailService) Metrics(ctx context.Context, invocation Invocation) (MailMetricsSnapshot, error) {
	if _, err := s.authorization.Authorize(ctx, invocation, model.ActionMailView); err != nil {
		return MailMetricsSnapshot{}, err
	}
	return s.metrics.Snapshot(), nil
}

func (a *App) CancelMailDelivery(ctx context.Context, invocation Invocation, id model.MailDeliveryID) (MailDeliveryView, error) {
	if a == nil || a.mail == nil {
		return MailDeliveryView{}, NewError("mail.unavailable")
	}
	return a.mail.Cancel(ctx, invocation, id)
}

func (a *App) RetryMailDelivery(ctx context.Context, invocation Invocation, id model.MailDeliveryID) (MailDeliveryView, error) {
	if a == nil || a.mail == nil {
		return MailDeliveryView{}, NewError("mail.unavailable")
	}
	return a.mail.Retry(ctx, invocation, id)
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
	now := model.TimeUTC(s.now())
	prepared, err := s.composer.PrepareDirect(DirectMailPreparation{Recipient: user, OccurrenceID: model.NewMailOccurrenceID(),
		Kind: model.MailOccurrenceOperatorTest, TemplateKey: model.MailTemplateSystemTest, At: now,
		Deadline: now.Add(24 * time.Hour), JobType: model.JobTypeMailDeliver})
	if err != nil {
		return MailDeliveryView{}, NewError("mail.unavailable").Wrap(err)
	}
	audit, err := s.audit.PrepareTest(ctx, invocation, institutionResource, prepared.Delivery.ID)
	if err != nil {
		return MailDeliveryView{}, err
	}
	created, err := s.mail.EnqueueTest(ctx, &store.MailTestEnqueue{Occurrence: prepared.Occurrence, Delivery: prepared.Delivery, Job: prepared.Job, AuditEvent: audit})
	if err != nil {
		return MailDeliveryView{}, mailAppError(err)
	}
	if s.wake != nil {
		s.wake()
	}
	return mailDeliveryView(created), nil
}

func validateMailAddress(address MailAddress) error {
	return appmail.ValidateAddress(address)
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

func (s *mailService) List(ctx context.Context, invocation Invocation, query ListMailDeliveriesQuery) (MailDeliveryPage, error) {
	if invocation.Principal().Validate() != nil {
		return MailDeliveryPage{}, invalidTokenAppError()
	}
	if _, err := s.authorization.Authorize(ctx, invocation, model.ActionMailView); err != nil {
		return MailDeliveryPage{}, err
	}
	deliveries, err := s.mail.ListDeliveries(ctx, store.MailDeliveryListOptions{
		States: query.States, TemplateKeys: query.TemplateKeys,
		CreatedAfter: query.CreatedAfter, CreatedBefore: query.CreatedBefore,
		BeforeCreatedAt: query.BeforeCreatedAt, BeforeID: query.BeforeID, Limit: query.Limit,
	})
	if err != nil {
		var invalid *store.ErrInvalidInput
		if errors.As(err, &invalid) {
			return MailDeliveryPage{}, NewError("mail.query.invalid").Wrap(err)
		}
		return MailDeliveryPage{}, mailAppError(err)
	}
	page := MailDeliveryPage{Items: make([]MailDeliveryView, 0, len(deliveries))}
	for _, delivery := range deliveries {
		page.Items = append(page.Items, mailDeliveryView(delivery))
	}
	return page, nil
}

func (s *mailService) Cancel(ctx context.Context, invocation Invocation, id model.MailDeliveryID) (MailDeliveryView, error) {
	return s.mutate(ctx, invocation, "cancel", id, false, s.mail.CancelDelivery)
}

func (s *mailService) Retry(ctx context.Context, invocation Invocation, id model.MailDeliveryID) (MailDeliveryView, error) {
	return s.mutate(ctx, invocation, "retry", id, true, s.mail.RetryDelivery)
}

func (s *mailService) mutate(ctx context.Context, invocation Invocation, operation string, id model.MailDeliveryID, retry bool, apply func(context.Context, *store.MailDeliveryMutation) (*model.MailDelivery, error)) (MailDeliveryView, error) {
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
	if !id.IsValid() {
		return MailDeliveryView{}, NewError("request.invalid")
	}
	delivery, err := s.mail.GetDelivery(ctx, id)
	if err != nil {
		return MailDeliveryView{}, mailAppError(err)
	}
	now := model.TimeUTC(s.now())
	if retry {
		relevance, relevanceErr := evaluateMailDeliveryRelevance(ctx, delivery)
		if relevanceErr != nil || relevance != mailDeliveryRelevant {
			return MailDeliveryView{}, NewError("mail.conflict").Wrap(relevanceErr)
		}
		if _, transitionErr := delivery.OperatorRetry(now); transitionErr != nil {
			return MailDeliveryView{}, NewError("mail.conflict").Wrap(transitionErr)
		}
	}
	auditID, err := s.audit.PrepareControl(ctx, invocation, institutionResource, delivery, operation)
	if err != nil {
		return MailDeliveryView{}, err
	}
	updated, err := apply(ctx, &store.MailDeliveryMutation{ID: delivery.ID, ExpectedRevision: delivery.Revision, AuditEventID: auditID, AuditAt: now.UnixMilli()})
	if err != nil {
		_ = s.audit.Fail(ctx, auditID, "mail.conflict")
		return MailDeliveryView{}, mailAppError(err)
	}
	if retry && s.wake != nil {
		s.wake()
	}
	return mailDeliveryView(updated), nil
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
	return appmail.StableMessageID(id, from)
}

func maskMailAddress(address string) string {
	return appmail.MaskAddress(address)
}

func mailDeliveryView(delivery *model.MailDelivery) MailDeliveryView {
	if delivery == nil {
		return MailDeliveryView{}
	}
	return MailDeliveryView{ID: delivery.ID, OccurrenceID: delivery.OccurrenceID, TargetUserID: delivery.TargetUserID, TemplateKey: delivery.TemplateKey, TemplateDigest: delivery.TemplateDigest, MaskedRecipient: delivery.MaskedRecipient, State: delivery.State, CreatedAt: delivery.CreatedAt, UpdatedAt: delivery.UpdatedAt, MessageDate: delivery.MessageDate, Deadline: delivery.Deadline, MessageID: delivery.MessageID, AttemptCount: delivery.AttemptCount, AcceptedAt: delivery.AcceptedAt, FailedAt: delivery.FailedAt, PublicFailureCode: delivery.PublicFailureCode}
}

func digestRenderedMail(subject, text, html string) string {
	return appmail.Digest(subject, text, html)
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
